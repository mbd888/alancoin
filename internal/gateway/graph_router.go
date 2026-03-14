package gateway

import (
	"container/heap"
	"context"
	"fmt"
	"log/slog"
	"math"
	"math/big"
	"sync"
	"time"

	"github.com/mbd888/alancoin/internal/usdc"
)

// RequestOutcome tracks the binary outcome of a routed request.
type RequestOutcome int

const (
	OutcomeSucceeded RequestOutcome = iota // First provider succeeded
	OutcomeRerouted                        // Rerouted to another provider after failure
	OutcomeEscalated                       // All providers failed, request escalated
)

// String returns the outcome name for metrics labels.
func (o RequestOutcome) String() string {
	switch o {
	case OutcomeSucceeded:
		return "succeeded"
	case OutcomeRerouted:
		return "rerouted"
	case OutcomeEscalated:
		return "escalated"
	default:
		return "unknown"
	}
}

// graphEdge represents a connection to a provider with a computed weight.
type graphEdge struct {
	providerID string
	candidate  ServiceCandidate
	weight     float64 // lower is better: price * (1/quality) * (1/health)
}

// ProviderGraph models providers as a weighted graph for shortest-path routing.
// Edge weights combine price, quality (reputation), and health into a single
// cost metric. When a provider fails, its weight is set to infinity so
// Dijkstra's algorithm naturally routes around it.
type ProviderGraph struct {
	mu        sync.RWMutex
	edges     map[string]*graphEdge          // providerID → edge
	byService map[string]map[string]struct{} // serviceType → set of providerIDs
	failed    map[string]time.Time           // providerID → failure timestamp
}

// NewProviderGraph creates an empty provider graph.
func NewProviderGraph() *ProviderGraph {
	return &ProviderGraph{
		edges:     make(map[string]*graphEdge),
		byService: make(map[string]map[string]struct{}),
		failed:    make(map[string]time.Time),
	}
}

// UpdateProviders rebuilds the graph from a set of candidates and health data.
func (g *ProviderGraph) UpdateProviders(serviceType string, candidates []ServiceCandidate, healthFn func(providerID string) HealthStatus) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if _, ok := g.byService[serviceType]; !ok {
		g.byService[serviceType] = make(map[string]struct{})
	}

	for _, c := range candidates {
		health := HealthStatus{State: HealthHealthy, SuccessRate: 1.0}
		if healthFn != nil {
			health = healthFn(c.AgentAddress)
		}

		weight := computeEdgeWeight(c, health)

		// If provider was marked as failed, set weight to infinity.
		if _, failed := g.failed[c.AgentAddress]; failed {
			weight = math.MaxFloat64
		}

		g.edges[c.AgentAddress] = &graphEdge{
			providerID: c.AgentAddress,
			candidate:  c,
			weight:     weight,
		}
		g.byService[serviceType][c.AgentAddress] = struct{}{}
	}
}

// MarkFailed marks a provider as failed, setting its edge weight to infinity.
func (g *ProviderGraph) MarkFailed(providerID string) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.failed[providerID] = time.Now()
	if e, ok := g.edges[providerID]; ok {
		e.weight = math.MaxFloat64
	}
}

// ClearFailed removes the failure mark for a provider, restoring normal routing.
func (g *ProviderGraph) ClearFailed(providerID string) {
	g.mu.Lock()
	defer g.mu.Unlock()

	delete(g.failed, providerID)
}

// ShortestPath returns providers for a service type, sorted by edge weight
// using Dijkstra's algorithm (simplified for a flat graph where all edges
// originate from a virtual "source" node).
func (g *ProviderGraph) ShortestPath(serviceType string) []ServiceCandidate {
	g.mu.RLock()
	defer g.mu.RUnlock()

	providers, ok := g.byService[serviceType]
	if !ok {
		return nil
	}

	// Use a min-heap to sort by weight (Dijkstra over a flat graph).
	h := &edgeHeap{}
	heap.Init(h)

	for providerID := range providers {
		if e, ok := g.edges[providerID]; ok {
			heap.Push(h, *e)
		}
	}

	var result []ServiceCandidate
	for h.Len() > 0 {
		e := heap.Pop(h).(graphEdge)
		if e.weight >= math.MaxFloat64 {
			break // All remaining are failed — stop.
		}
		result = append(result, e.candidate)
	}

	return result
}

// computeEdgeWeight computes cost = price * (1/quality) * (1/health).
// Lower weight is better.
func computeEdgeWeight(c ServiceCandidate, health HealthStatus) float64 {
	price, _ := usdc.Parse(c.Price)
	if price == nil || price.Sign() == 0 {
		return math.MaxFloat64
	}

	// Normalize price to float (USDC has 6 decimal places).
	priceF, _ := new(big.Float).SetInt(price).Float64()
	if priceF <= 0 {
		return math.MaxFloat64
	}
	normalizedPrice := priceF / 1e6

	// Quality from reputation score (0-100, normalize to 0-1, floor at 0.01).
	quality := c.ReputationScore / 100.0
	if quality < 0.01 {
		quality = 0.01
	}

	// Health factor from success rate (floor at 0.01).
	healthFactor := health.SuccessRate
	if healthFactor < 0.01 {
		healthFactor = 0.01
	}

	// Apply state multiplier: degraded providers are penalized,
	// unhealthy providers get very high weight.
	switch health.State {
	case HealthDegraded:
		healthFactor *= 0.5
	case HealthUnhealthy:
		healthFactor *= 0.01
	case HealthUnknown:
		// Slightly penalize unknown providers to prefer known-good ones.
		healthFactor *= 0.8
	}

	return normalizedPrice * (1.0 / quality) * (1.0 / healthFactor)
}

// edgeHeap implements heap.Interface for min-heap by weight.
type edgeHeap []graphEdge

func (h edgeHeap) Len() int            { return len(h) }
func (h edgeHeap) Less(i, j int) bool  { return h[i].weight < h[j].weight }
func (h edgeHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *edgeHeap) Push(x interface{}) { *h = append(*h, x.(graphEdge)) }
func (h *edgeHeap) Pop() interface{} {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}

// HealthAwareRouter wraps the existing resolver with health-based failover.
// It uses the provider graph to route requests and automatically reroutes
// on failure without requiring the client to retry.
type HealthAwareRouter struct {
	mu            sync.RWMutex
	graph         *ProviderGraph
	healthMonitor *HealthMonitor
	resolver      *Resolver
	logger        *slog.Logger
	onOutcome     func(outcome RequestOutcome, from, to string) // callback for metrics
}

// NewHealthAwareRouter creates a router with health-aware failover.
func NewHealthAwareRouter(resolver *Resolver, healthMonitor *HealthMonitor, logger *slog.Logger) *HealthAwareRouter {
	return &HealthAwareRouter{
		graph:         NewProviderGraph(),
		healthMonitor: healthMonitor,
		resolver:      resolver,
		logger:        logger,
	}
}

// OnOutcome sets a callback invoked after each routing decision.
func (r *HealthAwareRouter) OnOutcome(fn func(outcome RequestOutcome, from, to string)) {
	r.mu.Lock()
	r.onOutcome = fn
	r.mu.Unlock()
}

// Graph returns the underlying provider graph for direct access.
func (r *HealthAwareRouter) Graph() *ProviderGraph {
	return r.graph
}

// Route resolves candidates using both the base resolver and health data.
// It returns candidates ordered by the graph's shortest path (health-aware).
func (r *HealthAwareRouter) Route(ctx context.Context, req ProxyRequest, strategy, maxPerRequest string) ([]ServiceCandidate, error) {
	// Get base candidates from the resolver.
	candidates, err := r.resolver.Resolve(ctx, req, strategy, maxPerRequest)
	if err != nil {
		return nil, err
	}

	// Update the graph with current candidates and health data.
	r.graph.UpdateProviders(req.ServiceType, candidates, func(providerID string) HealthStatus {
		return r.healthMonitor.GetHealth(providerID)
	})

	// Get graph-ordered candidates (Dijkstra shortest path).
	graphOrdered := r.graph.ShortestPath(req.ServiceType)
	if len(graphOrdered) == 0 {
		return candidates, nil // Fall back to base ordering.
	}

	return graphOrdered, nil
}

// RecordSuccess records a successful forward and updates health data.
func (r *HealthAwareRouter) RecordSuccess(providerID string, latency time.Duration) {
	r.healthMonitor.RecordSuccess(providerID, latency)
	r.graph.ClearFailed(providerID)

	r.mu.RLock()
	fn := r.onOutcome
	r.mu.RUnlock()
	if fn != nil {
		fn(OutcomeSucceeded, providerID, "")
	}
}

// RecordFailure records a forward failure and marks the provider as failed.
func (r *HealthAwareRouter) RecordFailure(providerID string, err error) {
	r.healthMonitor.RecordFailure(providerID, err)
	r.graph.MarkFailed(providerID)
}

// RecordReroute records that a request was rerouted from one provider to another.
func (r *HealthAwareRouter) RecordReroute(fromProvider, toProvider string) {
	r.mu.RLock()
	fn := r.onOutcome
	r.mu.RUnlock()
	if fn != nil {
		fn(OutcomeRerouted, fromProvider, toProvider)
	}

	if r.logger != nil {
		r.logger.Info("request rerouted due to provider failure",
			"from_provider", fromProvider,
			"to_provider", toProvider)
	}
}

// RecordEscalation records that all providers failed and the request was escalated.
func (r *HealthAwareRouter) RecordEscalation(lastProvider string) {
	r.mu.RLock()
	fn := r.onOutcome
	r.mu.RUnlock()
	if fn != nil {
		fn(OutcomeEscalated, lastProvider, "")
	}

	if r.logger != nil {
		r.logger.Warn("all providers failed, request escalated",
			"last_provider", lastProvider)
	}
}

// RouteWithFailover resolves candidates and attempts to forward the request,
// automatically rerouting on failure. Returns the successful candidate index
// and the outcome.
func (r *HealthAwareRouter) RouteWithFailover(ctx context.Context, req ProxyRequest, strategy, maxPerRequest string, tryFn func(candidate ServiceCandidate) error) (ServiceCandidate, RequestOutcome, error) {
	candidates, err := r.Route(ctx, req, strategy, maxPerRequest)
	if err != nil {
		return ServiceCandidate{}, OutcomeEscalated, fmt.Errorf("route resolution failed: %w", err)
	}

	var firstProvider string
	for i, candidate := range candidates {
		if i == 0 {
			firstProvider = candidate.AgentAddress
		}

		err := tryFn(candidate)
		if err == nil {
			// Success.
			if i == 0 {
				r.RecordSuccess(candidate.AgentAddress, 0) // Latency tracked by caller.
				return candidate, OutcomeSucceeded, nil
			}
			r.RecordReroute(firstProvider, candidate.AgentAddress)
			return candidate, OutcomeRerouted, nil
		}

		// Failure: mark provider and try next.
		r.RecordFailure(candidate.AgentAddress, err)
		if r.logger != nil {
			r.logger.Warn("provider failed, trying next",
				"provider", candidate.AgentAddress,
				"attempt", i+1,
				"error", err)
		}
	}

	// All candidates exhausted.
	r.RecordEscalation(firstProvider)
	return ServiceCandidate{}, OutcomeEscalated, fmt.Errorf("all %d providers failed", len(candidates))
}
