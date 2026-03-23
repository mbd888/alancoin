package gateway

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"
)

func TestProviderGraph_ShortestPath(t *testing.T) {
	graph := NewProviderGraph()

	candidates := []ServiceCandidate{
		{AgentAddress: "expensive", Price: "10.000000", Endpoint: "http://a", ReputationScore: 80},
		{AgentAddress: "cheap", Price: "1.000000", Endpoint: "http://b", ReputationScore: 80},
		{AgentAddress: "medium", Price: "5.000000", Endpoint: "http://c", ReputationScore: 80},
	}

	graph.UpdateProviders("inference", candidates, nil)

	result := graph.ShortestPath("inference")
	if len(result) != 3 {
		t.Fatalf("expected 3 candidates, got %d", len(result))
	}

	// Cheapest should come first (lowest weight).
	if result[0].AgentAddress != "cheap" {
		t.Errorf("expected 'cheap' first, got %q", result[0].AgentAddress)
	}
}

func TestProviderGraph_MarkFailed(t *testing.T) {
	graph := NewProviderGraph()

	candidates := []ServiceCandidate{
		{AgentAddress: "good", Price: "1.000000", Endpoint: "http://a", ReputationScore: 90},
		{AgentAddress: "bad", Price: "2.000000", Endpoint: "http://b", ReputationScore: 90},
	}

	graph.UpdateProviders("inference", candidates, nil)

	// Mark the cheapest as failed.
	graph.MarkFailed("good")

	result := graph.ShortestPath("inference")
	if len(result) != 1 {
		t.Fatalf("expected 1 candidate (failed provider excluded), got %d", len(result))
	}
	if result[0].AgentAddress != "bad" {
		t.Errorf("expected 'bad' as only candidate, got %q", result[0].AgentAddress)
	}
}

func TestProviderGraph_ClearFailed(t *testing.T) {
	graph := NewProviderGraph()

	candidates := []ServiceCandidate{
		{AgentAddress: "provider-1", Price: "1.000000", Endpoint: "http://a", ReputationScore: 80},
	}

	graph.UpdateProviders("inference", candidates, nil)
	graph.MarkFailed("provider-1")

	result := graph.ShortestPath("inference")
	if len(result) != 0 {
		t.Fatalf("expected 0 candidates when failed, got %d", len(result))
	}

	graph.ClearFailed("provider-1")

	// Need to re-update to recalculate weights.
	graph.UpdateProviders("inference", candidates, nil)
	result = graph.ShortestPath("inference")
	if len(result) != 1 {
		t.Fatalf("expected 1 candidate after clear, got %d", len(result))
	}
}

func TestProviderGraph_HealthAffectsWeight(t *testing.T) {
	graph := NewProviderGraph()

	candidates := []ServiceCandidate{
		{AgentAddress: "healthy-expensive", Price: "5.000000", Endpoint: "http://a", ReputationScore: 80},
		{AgentAddress: "unhealthy-cheap", Price: "1.000000", Endpoint: "http://b", ReputationScore: 80},
	}

	// Make the cheap one unhealthy.
	healthFn := func(providerID string) HealthStatus {
		if providerID == "unhealthy-cheap" {
			return HealthStatus{State: HealthUnhealthy, SuccessRate: 0.1}
		}
		return HealthStatus{State: HealthHealthy, SuccessRate: 0.99}
	}

	graph.UpdateProviders("inference", candidates, healthFn)

	result := graph.ShortestPath("inference")
	if len(result) < 2 {
		t.Fatalf("expected 2 candidates, got %d", len(result))
	}

	// Healthy expensive should come before unhealthy cheap due to health penalty.
	if result[0].AgentAddress != "healthy-expensive" {
		t.Errorf("expected healthy-expensive first, got %q", result[0].AgentAddress)
	}
}

func TestProviderGraph_EmptyServiceType(t *testing.T) {
	graph := NewProviderGraph()
	result := graph.ShortestPath("nonexistent")
	if len(result) != 0 {
		t.Errorf("expected 0 candidates for unknown service, got %d", len(result))
	}
}

func TestHealthAwareRouter_Route(t *testing.T) {
	registry := &mockRegistry{
		services: []ServiceCandidate{
			{AgentAddress: "p1", Price: "1.000000", Endpoint: "http://p1", ServiceName: "svc1", ReputationScore: 80},
			{AgentAddress: "p2", Price: "2.000000", Endpoint: "http://p2", ServiceName: "svc2", ReputationScore: 90},
		},
	}
	resolver := NewResolver(registry)
	hm := NewHealthMonitor(DefaultHealthMonitorConfig())
	router := NewHealthAwareRouter(resolver, hm, testLogger())

	candidates, err := router.Route(context.Background(), ProxyRequest{ServiceType: "inference"}, "cheapest", "10.000000")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(candidates) == 0 {
		t.Fatal("expected candidates, got none")
	}
}

func TestHealthAwareRouter_RouteWithFailover(t *testing.T) {
	registry := &mockRegistry{
		services: []ServiceCandidate{
			{AgentAddress: "p1", Price: "1.000000", Endpoint: "http://p1", ServiceName: "svc1", ReputationScore: 80},
			{AgentAddress: "p2", Price: "2.000000", Endpoint: "http://p2", ServiceName: "svc2", ReputationScore: 70},
			{AgentAddress: "p3", Price: "3.000000", Endpoint: "http://p3", ServiceName: "svc3", ReputationScore: 60},
		},
	}
	resolver := NewResolver(registry)
	hm := NewHealthMonitor(DefaultHealthMonitorConfig())
	router := NewHealthAwareRouter(resolver, hm, testLogger())

	var mu sync.Mutex
	var outcomes []RequestOutcome
	router.OnOutcome(func(outcome RequestOutcome, from, to string) {
		mu.Lock()
		outcomes = append(outcomes, outcome)
		mu.Unlock()
	})

	// First provider fails, second succeeds.
	callCount := 0
	candidate, outcome, err := router.RouteWithFailover(
		context.Background(),
		ProxyRequest{ServiceType: "inference"},
		"cheapest", "10.000000",
		func(c ServiceCandidate) error {
			callCount++
			if callCount == 1 {
				return errors.New("connection refused")
			}
			return nil
		},
	)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome != OutcomeRerouted {
		t.Errorf("expected OutcomeRerouted, got %s", outcome.String())
	}
	if candidate.AgentAddress == "" {
		t.Error("expected a valid candidate")
	}
	if callCount != 2 {
		t.Errorf("expected 2 calls (1 failure + 1 success), got %d", callCount)
	}
}

func TestHealthAwareRouter_AllFail(t *testing.T) {
	registry := &mockRegistry{
		services: []ServiceCandidate{
			{AgentAddress: "p1", Price: "1.000000", Endpoint: "http://p1", ServiceName: "svc1", ReputationScore: 80},
			{AgentAddress: "p2", Price: "2.000000", Endpoint: "http://p2", ServiceName: "svc2", ReputationScore: 70},
		},
	}
	resolver := NewResolver(registry)
	hm := NewHealthMonitor(DefaultHealthMonitorConfig())
	router := NewHealthAwareRouter(resolver, hm, testLogger())

	_, outcome, err := router.RouteWithFailover(
		context.Background(),
		ProxyRequest{ServiceType: "inference"},
		"cheapest", "10.000000",
		func(c ServiceCandidate) error {
			return errors.New("all broken")
		},
	)

	if err == nil {
		t.Fatal("expected error when all providers fail")
	}
	if outcome != OutcomeEscalated {
		t.Errorf("expected OutcomeEscalated, got %s", outcome.String())
	}
}

func TestHealthAwareRouter_RecordSuccessUpdatesHealth(t *testing.T) {
	registry := &mockRegistry{
		services: []ServiceCandidate{
			{AgentAddress: "p1", Price: "1.000000", Endpoint: "http://p1", ServiceName: "svc1", ReputationScore: 80},
		},
	}
	resolver := NewResolver(registry)
	config := DefaultHealthMonitorConfig()
	config.MinSamplesForDecision = 1
	hm := NewHealthMonitor(config)
	router := NewHealthAwareRouter(resolver, hm, testLogger())

	router.RecordSuccess("p1", 50*time.Millisecond)

	status := hm.GetHealth("p1")
	if status.State != HealthHealthy {
		t.Errorf("expected HealthHealthy after success, got %s", status.State.String())
	}
}

func TestHealthAwareRouter_RecordFailureMarksProvider(t *testing.T) {
	registry := &mockRegistry{}
	resolver := NewResolver(registry)
	hm := NewHealthMonitor(DefaultHealthMonitorConfig())
	router := NewHealthAwareRouter(resolver, hm, testLogger())

	router.RecordFailure("p1", errors.New("timeout"))

	// Check the graph has the provider marked as failed.
	graph := router.Graph()
	graph.mu.RLock()
	_, isFailed := graph.failed["p1"]
	graph.mu.RUnlock()

	if !isFailed {
		t.Error("expected provider p1 to be marked as failed in graph")
	}
}

func TestRequestOutcome_String(t *testing.T) {
	tests := []struct {
		outcome RequestOutcome
		want    string
	}{
		{OutcomeSucceeded, "succeeded"},
		{OutcomeRerouted, "rerouted"},
		{OutcomeEscalated, "escalated"},
		{RequestOutcome(99), "unknown"},
	}

	for _, tt := range tests {
		if got := tt.outcome.String(); got != tt.want {
			t.Errorf("RequestOutcome(%d).String() = %q, want %q", tt.outcome, got, tt.want)
		}
	}
}

func TestComputeEdgeWeight_ZeroPrice(t *testing.T) {
	c := ServiceCandidate{Price: "0.000000", ReputationScore: 80}
	health := HealthStatus{State: HealthHealthy, SuccessRate: 1.0}

	weight := computeEdgeWeight(c, health)
	if weight < 1e300 {
		t.Errorf("expected very large weight for zero price, got %f", weight)
	}
}

func TestComputeEdgeWeight_HealthPenalty(t *testing.T) {
	c := ServiceCandidate{Price: "1.000000", ReputationScore: 80}

	healthyWeight := computeEdgeWeight(c, HealthStatus{State: HealthHealthy, SuccessRate: 0.99})
	degradedWeight := computeEdgeWeight(c, HealthStatus{State: HealthDegraded, SuccessRate: 0.85})
	unhealthyWeight := computeEdgeWeight(c, HealthStatus{State: HealthUnhealthy, SuccessRate: 0.30})

	if healthyWeight >= degradedWeight {
		t.Errorf("expected healthy weight (%f) < degraded weight (%f)", healthyWeight, degradedWeight)
	}
	if degradedWeight >= unhealthyWeight {
		t.Errorf("expected degraded weight (%f) < unhealthy weight (%f)", degradedWeight, unhealthyWeight)
	}
}

func TestComputeEdgeWeight_InvalidPrice(t *testing.T) {
	c := ServiceCandidate{Price: "invalid", ReputationScore: 80}
	health := HealthStatus{State: HealthHealthy, SuccessRate: 1.0}
	weight := computeEdgeWeight(c, health)
	if weight < 1e300 {
		t.Errorf("expected max weight for invalid price, got %f", weight)
	}
}

func TestComputeEdgeWeight_VeryLowReputation(t *testing.T) {
	c := ServiceCandidate{Price: "1.000000", ReputationScore: 0}
	health := HealthStatus{State: HealthHealthy, SuccessRate: 1.0}
	weight := computeEdgeWeight(c, health)
	// Low rep = high weight
	normalC := ServiceCandidate{Price: "1.000000", ReputationScore: 80}
	normalWeight := computeEdgeWeight(normalC, health)
	if weight <= normalWeight {
		t.Error("expected higher weight for low reputation")
	}
}

func TestComputeEdgeWeight_UnknownHealth(t *testing.T) {
	c := ServiceCandidate{Price: "1.000000", ReputationScore: 80}
	unknown := computeEdgeWeight(c, HealthStatus{State: HealthUnknown, SuccessRate: 0.5})
	healthy := computeEdgeWeight(c, HealthStatus{State: HealthHealthy, SuccessRate: 0.99})
	if unknown <= healthy {
		t.Error("expected unknown health to have higher weight than healthy")
	}
}

func TestProviderGraph_WeightRecalculationOnUpdate(t *testing.T) {
	graph := NewProviderGraph()

	candidates := []ServiceCandidate{
		{AgentAddress: "p1", Price: "1.000000", Endpoint: "http://a", ReputationScore: 80},
		{AgentAddress: "p2", Price: "2.000000", Endpoint: "http://b", ReputationScore: 80},
	}

	// Initial update with both healthy.
	graph.UpdateProviders("inference", candidates, func(providerID string) HealthStatus {
		return HealthStatus{State: HealthHealthy, SuccessRate: 0.99}
	})

	firstResult := graph.ShortestPath("inference")
	if firstResult[0].AgentAddress != "p1" {
		t.Errorf("expected p1 first initially, got %q", firstResult[0].AgentAddress)
	}

	// Re-update with p1 unhealthy.
	graph.UpdateProviders("inference", candidates, func(providerID string) HealthStatus {
		if providerID == "p1" {
			return HealthStatus{State: HealthUnhealthy, SuccessRate: 0.05}
		}
		return HealthStatus{State: HealthHealthy, SuccessRate: 0.99}
	})

	secondResult := graph.ShortestPath("inference")
	if secondResult[0].AgentAddress != "p2" {
		t.Errorf("expected p2 first after p1 became unhealthy, got %q", secondResult[0].AgentAddress)
	}
}

func TestHealthAwareRouter_NoLogger(t *testing.T) {
	// Ensure the router works without a logger (nil logger).
	registry := &mockRegistry{
		services: []ServiceCandidate{
			{AgentAddress: "p1", Price: "1.000000", Endpoint: "http://p1", ServiceName: "svc1", ReputationScore: 80},
		},
	}
	resolver := NewResolver(registry)
	hm := NewHealthMonitor(DefaultHealthMonitorConfig())
	router := NewHealthAwareRouter(resolver, hm, nil)

	// Should not panic.
	router.RecordReroute("p1", "p2")
	router.RecordEscalation("p1")
}

// Integration test: simulate provider failure mid-stream and verify auto-reroute.
func TestIntegration_ProviderFailureAutoReroute(t *testing.T) {
	registry := &mockRegistry{
		services: []ServiceCandidate{
			{AgentAddress: "primary", Price: "1.000000", Endpoint: "http://primary", ServiceName: "primary-svc", ReputationScore: 90},
			{AgentAddress: "secondary", Price: "2.000000", Endpoint: "http://secondary", ServiceName: "secondary-svc", ReputationScore: 80},
			{AgentAddress: "tertiary", Price: "3.000000", Endpoint: "http://tertiary", ServiceName: "tertiary-svc", ReputationScore: 70},
		},
	}

	resolver := NewResolver(registry)
	config := DefaultHealthMonitorConfig()
	config.MinSamplesForDecision = 1
	hm := NewHealthMonitor(config)
	logger := slog.Default()
	router := NewHealthAwareRouter(resolver, hm, logger)

	// Simulate: first 5 requests succeed on primary.
	for i := 0; i < 5; i++ {
		candidate, outcome, err := router.RouteWithFailover(
			context.Background(),
			ProxyRequest{ServiceType: "inference"},
			"cheapest", "10.000000",
			func(c ServiceCandidate) error {
				if c.AgentAddress == "primary" {
					return nil // Primary works.
				}
				return errors.New("should not be called")
			},
		)
		if err != nil {
			t.Fatalf("request %d: unexpected error: %v", i, err)
		}
		if outcome != OutcomeSucceeded {
			t.Errorf("request %d: expected OutcomeSucceeded, got %s", i, outcome.String())
		}
		if candidate.AgentAddress != "primary" {
			t.Errorf("request %d: expected primary, got %q", i, candidate.AgentAddress)
		}
	}

	// Now primary starts failing — should auto-reroute to secondary.
	candidate, outcome, err := router.RouteWithFailover(
		context.Background(),
		ProxyRequest{ServiceType: "inference"},
		"cheapest", "10.000000",
		func(c ServiceCandidate) error {
			if c.AgentAddress == "primary" {
				return errors.New("primary down")
			}
			return nil // Secondary or tertiary works.
		},
	)

	if err != nil {
		t.Fatalf("reroute request: unexpected error: %v", err)
	}
	if outcome != OutcomeRerouted {
		t.Errorf("expected OutcomeRerouted, got %s", outcome.String())
	}
	if candidate.AgentAddress == "primary" {
		t.Error("expected request to be served by secondary or tertiary, not primary")
	}

	// Verify primary is marked as failed in the graph.
	graph := router.Graph()
	graph.mu.RLock()
	_, primaryFailed := graph.failed["primary"]
	graph.mu.RUnlock()
	if !primaryFailed {
		t.Error("expected primary to be marked as failed after reroute")
	}

	// Subsequent request should skip primary entirely.
	candidate, outcome, err = router.RouteWithFailover(
		context.Background(),
		ProxyRequest{ServiceType: "inference"},
		"cheapest", "10.000000",
		func(c ServiceCandidate) error {
			if c.AgentAddress == "primary" {
				t.Error("primary should not be tried — it was marked failed")
				return errors.New("still down")
			}
			return nil
		},
	)

	if err != nil {
		t.Fatalf("post-reroute request: unexpected error: %v", err)
	}

	_ = candidate
	_ = outcome
}
