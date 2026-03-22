package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// ============================================================================
// MemoryStore coverage: billing, analytics, time-series, policy denials
// ============================================================================

func TestMemoryStore_GetBillingSummary(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	// Empty summary
	row, err := store.GetBillingSummary(ctx, "tenant1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if row.TotalRequests != 0 {
		t.Errorf("expected 0 total requests, got %d", row.TotalRequests)
	}
	if row.SettledVolume != "0" {
		t.Errorf("expected 0 settled volume, got %s", row.SettledVolume)
	}

	// Add some logs
	store.CreateLog(ctx, &RequestLog{
		ID:          "log1",
		SessionID:   "s1",
		TenantID:    "tenant1",
		ServiceType: "inference",
		Amount:      "1.000000",
		FeeAmount:   "0.010000",
		Status:      "success",
		CreatedAt:   time.Now(),
	})
	store.CreateLog(ctx, &RequestLog{
		ID:          "log2",
		SessionID:   "s1",
		TenantID:    "tenant1",
		ServiceType: "inference",
		Amount:      "2.000000",
		FeeAmount:   "0.020000",
		Status:      "success",
		CreatedAt:   time.Now(),
	})
	store.CreateLog(ctx, &RequestLog{
		ID:          "log3",
		SessionID:   "s1",
		TenantID:    "tenant1",
		ServiceType: "inference",
		Amount:      "0",
		Status:      "forward_failed",
		CreatedAt:   time.Now(),
	})
	// Different tenant
	store.CreateLog(ctx, &RequestLog{
		ID:          "log4",
		SessionID:   "s2",
		TenantID:    "tenant2",
		ServiceType: "inference",
		Amount:      "5.000000",
		FeeAmount:   "0.050000",
		Status:      "success",
		CreatedAt:   time.Now(),
	})

	row, err = store.GetBillingSummary(ctx, "tenant1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if row.TotalRequests != 3 {
		t.Errorf("expected 3 total requests, got %d", row.TotalRequests)
	}
	if row.SettledRequests != 2 {
		t.Errorf("expected 2 settled requests, got %d", row.SettledRequests)
	}
	if row.SettledVolume != "3.000000" {
		t.Errorf("expected 3.000000 settled volume, got %s", row.SettledVolume)
	}
	if row.FeesCollected != "0.030000" {
		t.Errorf("expected 0.030000 fees, got %s", row.FeesCollected)
	}
}

func TestMemoryStore_GetBillingTimeSeries_Day(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	store.CreateLog(ctx, &RequestLog{
		ID: "log1", SessionID: "s1", TenantID: "t1",
		Amount: "1.000000", FeeAmount: "0.010000", Status: "success",
		CreatedAt: today.Add(2 * time.Hour),
	})
	store.CreateLog(ctx, &RequestLog{
		ID: "log2", SessionID: "s1", TenantID: "t1",
		Amount: "2.000000", FeeAmount: "0.020000", Status: "success",
		CreatedAt: today.Add(5 * time.Hour),
	})

	from := today
	to := today.Add(24 * time.Hour)
	points, err := store.GetBillingTimeSeries(ctx, "t1", "day", from, to)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(points) != 1 {
		t.Fatalf("expected 1 daily bucket, got %d", len(points))
	}
	if points[0].Requests != 2 {
		t.Errorf("expected 2 requests in bucket, got %d", points[0].Requests)
	}
	if points[0].SettledRequests != 2 {
		t.Errorf("expected 2 settled requests, got %d", points[0].SettledRequests)
	}
}

func TestMemoryStore_GetBillingTimeSeries_Hour(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Hour)

	store.CreateLog(ctx, &RequestLog{
		ID: "log1", SessionID: "s1", TenantID: "t1",
		Amount: "1.000000", Status: "success",
		CreatedAt: now.Add(10 * time.Minute),
	})
	store.CreateLog(ctx, &RequestLog{
		ID: "log2", SessionID: "s1", TenantID: "t1",
		Amount: "2.000000", Status: "success",
		CreatedAt: now.Add(70 * time.Minute), // next hour
	})

	from := now
	to := now.Add(3 * time.Hour)
	points, err := store.GetBillingTimeSeries(ctx, "t1", "hour", from, to)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(points) != 2 {
		t.Errorf("expected 2 hourly buckets, got %d", len(points))
	}
}

func TestMemoryStore_GetBillingTimeSeries_Week(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	// Use a known date in week 1 of 2026
	refDate := time.Date(2026, 1, 5, 12, 0, 0, 0, time.UTC) // Monday Jan 5
	store.CreateLog(ctx, &RequestLog{
		ID: "log1", SessionID: "s1", TenantID: "t1",
		Amount: "1.000000", Status: "success",
		CreatedAt: refDate,
	})

	from := refDate.AddDate(0, 0, -7)
	to := refDate.AddDate(0, 0, 7)
	points, err := store.GetBillingTimeSeries(ctx, "t1", "week", from, to)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(points) == 0 {
		t.Error("expected at least 1 weekly bucket")
	}
}

func TestMemoryStore_GetBillingTimeSeries_OutOfRange(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	now := time.Now().UTC()
	store.CreateLog(ctx, &RequestLog{
		ID: "log1", SessionID: "s1", TenantID: "t1",
		Amount: "1.000000", Status: "success",
		CreatedAt: now,
	})

	// Query a range that doesn't include the log
	from := now.Add(1 * time.Hour)
	to := now.Add(2 * time.Hour)
	points, err := store.GetBillingTimeSeries(ctx, "t1", "day", from, to)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(points) != 0 {
		t.Errorf("expected 0 buckets for out-of-range, got %d", len(points))
	}
}

func TestMemoryStore_GetTopServiceTypes(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	store.CreateLog(ctx, &RequestLog{
		ID: "log1", SessionID: "s1", TenantID: "t1",
		ServiceType: "inference", Amount: "1.000000", Status: "success",
		CreatedAt: time.Now(),
	})
	store.CreateLog(ctx, &RequestLog{
		ID: "log2", SessionID: "s1", TenantID: "t1",
		ServiceType: "inference", Amount: "2.000000", Status: "success",
		CreatedAt: time.Now(),
	})
	store.CreateLog(ctx, &RequestLog{
		ID: "log3", SessionID: "s1", TenantID: "t1",
		ServiceType: "translation", Amount: "0.500000", Status: "success",
		CreatedAt: time.Now(),
	})
	// Failed request should be excluded
	store.CreateLog(ctx, &RequestLog{
		ID: "log4", SessionID: "s1", TenantID: "t1",
		ServiceType: "inference", Amount: "0", Status: "forward_failed",
		CreatedAt: time.Now(),
	})

	types, err := store.GetTopServiceTypes(ctx, "t1", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(types) != 2 {
		t.Fatalf("expected 2 service types, got %d", len(types))
	}
	// inference should be first (more requests)
	if types[0].ServiceType != "inference" {
		t.Errorf("expected inference first, got %s", types[0].ServiceType)
	}
	if types[0].Requests != 2 {
		t.Errorf("expected 2 inference requests, got %d", types[0].Requests)
	}
}

func TestMemoryStore_GetTopServiceTypes_WithLimit(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		store.CreateLog(ctx, &RequestLog{
			ID: fmt.Sprintf("log%d", i), SessionID: "s1", TenantID: "t1",
			ServiceType: fmt.Sprintf("type%d", i), Amount: "1.000000", Status: "success",
			CreatedAt: time.Now(),
		})
	}

	types, err := store.GetTopServiceTypes(ctx, "t1", 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(types) != 2 {
		t.Errorf("expected 2 types with limit, got %d", len(types))
	}
}

func TestMemoryStore_GetPolicyDenials(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	store.CreateLog(ctx, &RequestLog{
		ID: "log1", SessionID: "s1", TenantID: "t1",
		Status: "policy_denied", CreatedAt: time.Now().Add(-2 * time.Hour),
	})
	store.CreateLog(ctx, &RequestLog{
		ID: "log2", SessionID: "s1", TenantID: "t1",
		Status: "policy_denied", CreatedAt: time.Now().Add(-1 * time.Hour),
	})
	store.CreateLog(ctx, &RequestLog{
		ID: "log3", SessionID: "s1", TenantID: "t1",
		Status: "success", CreatedAt: time.Now(),
	})

	denials, err := store.GetPolicyDenials(ctx, "t1", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(denials) != 2 {
		t.Fatalf("expected 2 denials, got %d", len(denials))
	}
	// Most recent first
	if denials[0].ID != "log2" {
		t.Errorf("expected log2 first (most recent), got %s", denials[0].ID)
	}
}

func TestMemoryStore_GetPolicyDenials_WithLimit(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		store.CreateLog(ctx, &RequestLog{
			ID: fmt.Sprintf("log%d", i), SessionID: "s1", TenantID: "t1",
			Status: "policy_denied", CreatedAt: time.Now().Add(time.Duration(-i) * time.Hour),
		})
	}

	denials, err := store.GetPolicyDenials(ctx, "t1", 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(denials) != 2 {
		t.Errorf("expected 2 denials with limit, got %d", len(denials))
	}
}

func TestMemoryStore_ListByStatus(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	now := time.Now()
	store.CreateSession(ctx, &Session{
		ID: "s1", AgentAddr: "0xa", Status: StatusActive,
		UpdatedAt: now.Add(-2 * time.Hour),
	})
	store.CreateSession(ctx, &Session{
		ID: "s2", AgentAddr: "0xb", Status: StatusClosed,
		UpdatedAt: now.Add(-1 * time.Hour),
	})
	store.CreateSession(ctx, &Session{
		ID: "s3", AgentAddr: "0xc", Status: StatusActive,
		UpdatedAt: now,
	})

	active, err := store.ListByStatus(ctx, StatusActive, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(active) != 2 {
		t.Errorf("expected 2 active sessions, got %d", len(active))
	}
	// Most recently updated first
	if active[0].ID != "s3" {
		t.Errorf("expected s3 first (most recent), got %s", active[0].ID)
	}
}

func TestMemoryStore_ListByStatus_WithLimit(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		store.CreateSession(ctx, &Session{
			ID: fmt.Sprintf("s%d", i), AgentAddr: "0xa", Status: StatusActive,
			UpdatedAt: time.Now().Add(time.Duration(i) * time.Minute),
		})
	}

	active, err := store.ListByStatus(ctx, StatusActive, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(active) != 2 {
		t.Errorf("expected 2 active with limit, got %d", len(active))
	}
}

func TestMemoryStore_ListSessionsByTenant(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	now := time.Now()
	store.CreateSession(ctx, &Session{
		ID: "s1", AgentAddr: "0xa", TenantID: "tenant1",
		CreatedAt: now.Add(-2 * time.Hour),
	})
	store.CreateSession(ctx, &Session{
		ID: "s2", AgentAddr: "0xb", TenantID: "tenant2",
		CreatedAt: now.Add(-1 * time.Hour),
	})
	store.CreateSession(ctx, &Session{
		ID: "s3", AgentAddr: "0xc", TenantID: "tenant1",
		CreatedAt: now,
	})

	sessions, err := store.ListSessionsByTenant(ctx, "tenant1", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions for tenant1, got %d", len(sessions))
	}
	// Most recent first
	if sessions[0].ID != "s3" {
		t.Errorf("expected s3 first, got %s", sessions[0].ID)
	}
}

func TestMemoryStore_ListSessionsByTenant_WithLimit(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		store.CreateSession(ctx, &Session{
			ID: fmt.Sprintf("s%d", i), AgentAddr: "0xa", TenantID: "t1",
			CreatedAt: time.Now().Add(time.Duration(i) * time.Minute),
		})
	}

	sessions, err := store.ListSessionsByTenant(ctx, "t1", 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sessions) != 2 {
		t.Errorf("expected 2 sessions with limit, got %d", len(sessions))
	}
}

func TestMemoryStore_ListExpired(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	now := time.Now()
	store.CreateSession(ctx, &Session{
		ID: "s1", AgentAddr: "0xa", Status: StatusActive,
		ExpiresAt:    now.Add(-1 * time.Hour),
		AllowedTypes: []string{"test"},
	})
	store.CreateSession(ctx, &Session{
		ID: "s2", AgentAddr: "0xb", Status: StatusActive,
		ExpiresAt: now.Add(1 * time.Hour), // not expired
	})
	store.CreateSession(ctx, &Session{
		ID: "s3", AgentAddr: "0xc", Status: StatusClosed, // not active
		ExpiresAt: now.Add(-2 * time.Hour),
	})

	expired, err := store.ListExpired(ctx, now, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(expired) != 1 {
		t.Fatalf("expected 1 expired session, got %d", len(expired))
	}
	if expired[0].ID != "s1" {
		t.Errorf("expected s1, got %s", expired[0].ID)
	}
	// Verify deep copy of AllowedTypes
	if len(expired[0].AllowedTypes) != 1 || expired[0].AllowedTypes[0] != "test" {
		t.Error("expected AllowedTypes to be deep copied")
	}
}

func TestMemoryStore_ListExpired_WithLimit(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	now := time.Now()
	for i := 0; i < 5; i++ {
		store.CreateSession(ctx, &Session{
			ID: fmt.Sprintf("s%d", i), AgentAddr: "0xa", Status: StatusActive,
			ExpiresAt: now.Add(-1 * time.Hour),
		})
	}

	expired, err := store.ListExpired(ctx, now, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(expired) != 2 {
		t.Errorf("expected 2 expired with limit, got %d", len(expired))
	}
}

func TestMemoryStore_UpdateSession_NotFound(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	err := store.UpdateSession(ctx, &Session{ID: "nonexistent"})
	if err != ErrSessionNotFound {
		t.Errorf("expected ErrSessionNotFound, got %v", err)
	}
}

func TestMemoryStore_UpdateSession_DeepCopy(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	store.CreateSession(ctx, &Session{
		ID: "s1", AgentAddr: "0xa", Status: StatusActive,
		AllowedTypes: []string{"test"},
	})

	// Update with new allowed types
	updated := &Session{
		ID: "s1", AgentAddr: "0xa", Status: StatusActive,
		AllowedTypes: []string{"test", "inference"},
	}
	err := store.UpdateSession(ctx, updated)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Mutate the caller's copy
	updated.AllowedTypes[0] = "mutated"

	// Stored copy should not be affected
	stored, _ := store.GetSession(ctx, "s1")
	if stored.AllowedTypes[0] != "test" {
		t.Errorf("expected deep copy to prevent mutation, got %s", stored.AllowedTypes[0])
	}
}

func TestMemoryStore_ListLogs_Limit(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	for i := 0; i < 10; i++ {
		store.CreateLog(ctx, &RequestLog{
			ID: fmt.Sprintf("log%d", i), SessionID: "s1",
			CreatedAt: time.Now(),
		})
	}

	logs, err := store.ListLogs(ctx, "s1", 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(logs) != 3 {
		t.Errorf("expected 3 logs with limit, got %d", len(logs))
	}
}

// ============================================================================
// addDecimalStrings coverage
// ============================================================================

func TestAddDecimalStrings(t *testing.T) {
	tests := []struct {
		a, b, want string
	}{
		{"1.000000", "2.000000", "3.000000"},
		{"0", "0", "0.000000"},
		{"invalid", "1.000000", "1.000000"},
		{"1.000000", "invalid", "1.000000"},
		{"invalid", "invalid", "0.000000"},
	}

	for _, tt := range tests {
		got := addDecimalStrings(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("addDecimalStrings(%q, %q) = %q, want %q", tt.a, tt.b, got, tt.want)
		}
	}
}

// ============================================================================
// Pipeline substitution coverage
// ============================================================================

func TestSubstitutePrev_NilParams_Extra(t *testing.T) {
	result := substitutePrev(nil, map[string]interface{}{"key": "val"})
	if result != nil {
		t.Error("expected nil for nil params")
	}
}

func TestSubstitutePrev_NilPrev_Extra(t *testing.T) {
	params := map[string]interface{}{"key": "val"}
	result := substitutePrev(params, nil)
	if result["key"] != "val" {
		t.Error("expected original params when prev is nil")
	}
}

func TestSubstitutePrev_EntireResponse_Extra(t *testing.T) {
	prev := map[string]interface{}{"data": "hello"}
	params := map[string]interface{}{"input": "$prev"}
	result := substitutePrev(params, prev)
	inner, ok := result["input"].(map[string]interface{})
	if !ok {
		t.Fatal("expected map for $prev substitution")
	}
	if inner["data"] != "hello" {
		t.Errorf("expected hello, got %v", inner["data"])
	}
}

func TestSubstitutePrev_NestedKey_Extra(t *testing.T) {
	prev := map[string]interface{}{
		"result": map[string]interface{}{
			"text": "translated",
		},
	}
	params := map[string]interface{}{"input": "$prev.result.text"}
	result := substitutePrev(params, prev)
	if result["input"] != "translated" {
		t.Errorf("expected translated, got %v", result["input"])
	}
}

func TestSubstitutePrev_NonexistentKey(t *testing.T) {
	prev := map[string]interface{}{"key": "val"}
	params := map[string]interface{}{"input": "$prev.nonexistent"}
	result := substitutePrev(params, prev)
	if result["input"] != "$prev.nonexistent" {
		t.Errorf("expected original string for missing key, got %v", result["input"])
	}
}

func TestSubstitutePrev_PathNotMap(t *testing.T) {
	prev := map[string]interface{}{"key": "val"}
	params := map[string]interface{}{"input": "$prev.key.sub"}
	result := substitutePrev(params, prev)
	if result["input"] != "$prev.key.sub" {
		t.Errorf("expected original when traversing non-map, got %v", result["input"])
	}
}

func TestSubstitutePrev_ComplexValue(t *testing.T) {
	prev := map[string]interface{}{
		"data": map[string]interface{}{
			"items": []interface{}{1, 2, 3},
		},
	}
	params := map[string]interface{}{"input": "$prev.data.items"}
	result := substitutePrev(params, prev)
	// Complex value should be JSON-encoded
	got, ok := result["input"].(string)
	if !ok {
		t.Fatal("expected string for complex value")
	}
	if !strings.Contains(got, "1") || !strings.Contains(got, "2") {
		t.Errorf("expected JSON array, got %s", got)
	}
}

func TestSubstitutePrev_InNestedMap(t *testing.T) {
	prev := map[string]interface{}{"key": "val"}
	params := map[string]interface{}{
		"outer": map[string]interface{}{
			"inner": "$prev.key",
		},
	}
	result := substitutePrev(params, prev)
	outer := result["outer"].(map[string]interface{})
	if outer["inner"] != "val" {
		t.Errorf("expected val, got %v", outer["inner"])
	}
}

func TestSubstitutePrev_InArray(t *testing.T) {
	prev := map[string]interface{}{"key": "val"}
	params := map[string]interface{}{
		"items": []interface{}{"$prev.key", "static"},
	}
	result := substitutePrev(params, prev)
	items := result["items"].([]interface{})
	if items[0] != "val" {
		t.Errorf("expected val in array, got %v", items[0])
	}
	if items[1] != "static" {
		t.Errorf("expected static, got %v", items[1])
	}
}

func TestSubstitutePrev_NonStringValue(t *testing.T) {
	prev := map[string]interface{}{"key": "val"}
	params := map[string]interface{}{"num": 42}
	result := substitutePrev(params, prev)
	if result["num"] != 42 {
		t.Errorf("expected 42, got %v", result["num"])
	}
}

func TestSumStepAmounts_Coverage(t *testing.T) {
	results := []PipelineStepResult{
		{AmountPaid: "1.000000"},
		{AmountPaid: "2.500000"},
		{AmountPaid: "0.500000"},
	}
	total := sumStepAmounts(results)
	if total != "4.000000" {
		t.Errorf("expected 4.000000, got %s", total)
	}
}

func TestSumStepAmounts_Empty_Coverage(t *testing.T) {
	total := sumStepAmounts(nil)
	if total != "0.000000" {
		t.Errorf("expected 0.000000, got %s", total)
	}
}

func TestSumStepAmounts_InvalidAmount(t *testing.T) {
	results := []PipelineStepResult{
		{AmountPaid: "1.000000"},
		{AmountPaid: "invalid"},
		{AmountPaid: "2.000000"},
	}
	total := sumStepAmounts(results)
	if total != "3.000000" {
		t.Errorf("expected 3.000000 (skip invalid), got %s", total)
	}
}

// ============================================================================
// Resolver coverage: strategies and intelligence
// ============================================================================

func TestResolver_BestValue_Coverage(t *testing.T) {
	reg := &mockRegistry{
		services: []ServiceCandidate{
			{AgentAddress: "0xexpensive", Price: "10.00", Endpoint: "http://a", ReputationScore: 50},
			{AgentAddress: "0xcheap", Price: "0.50", Endpoint: "http://b", ReputationScore: 80},
		},
	}
	resolver := NewResolver(reg)

	candidates, err := resolver.Resolve(context.Background(), ProxyRequest{ServiceType: "test"}, "best_value", "20.00")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	// Best value should rank cheap+high-rep first
	if candidates[0].AgentAddress != "0xcheap" {
		t.Errorf("expected 0xcheap first, got %s", candidates[0].AgentAddress)
	}
}

func TestResolver_WithDiscoveryBooster(t *testing.T) {
	reg := &mockRegistry{
		services: []ServiceCandidate{
			{AgentAddress: "0xa", Price: "1.00", Endpoint: "http://a", ReputationScore: 50},
			{AgentAddress: "0xb", Price: "1.00", Endpoint: "http://b", ReputationScore: 80},
		},
	}

	booster := &testBooster{boost: 10.0}
	resolver := NewResolver(reg).WithDiscoveryBooster(booster)

	candidates, err := resolver.Resolve(context.Background(), ProxyRequest{ServiceType: "test"}, "reputation", "5.00")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	// Both should have boosted scores
	if len(candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(candidates))
	}
}

type testBooster struct {
	boost float64
}

func (b *testBooster) BoostScore(_ context.Context, tier string, baseScore float64) float64 {
	return baseScore + b.boost
}

func TestResolver_WithMaxPriceOverride(t *testing.T) {
	reg := &mockRegistry{
		services: []ServiceCandidate{
			{AgentAddress: "0xa", Price: "1.00", Endpoint: "http://a"},
		},
	}
	resolver := NewResolver(reg)

	// MaxPrice from request should override maxPerRequest
	candidates, err := resolver.Resolve(context.Background(), ProxyRequest{
		ServiceType: "test",
		MaxPrice:    "5.00",
	}, "cheapest", "1.00")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(candidates) == 0 {
		t.Error("expected candidates with maxPrice override")
	}
}

func TestResolver_RegistryError(t *testing.T) {
	reg := &mockRegistry{err: fmt.Errorf("db error")}
	resolver := NewResolver(reg)

	_, err := resolver.Resolve(context.Background(), ProxyRequest{ServiceType: "test"}, "cheapest", "5.00")
	if err == nil {
		t.Fatal("expected error from registry")
	}
}

// ============================================================================
// Intelligence discovery boost / RPM adjustment
// ============================================================================

func TestIntelligenceDiscoveryBoost_AllTiers(t *testing.T) {
	tests := []struct {
		tier string
		want float64
	}{
		{"diamond", 15.0},
		{"platinum", 10.0},
		{"gold", 5.0},
		{"silver", 2.0},
		{"bronze", 0},
		{"unknown", 0},
		{"", 0},
	}
	for _, tt := range tests {
		got := intelligenceDiscoveryBoost(tt.tier)
		if got != tt.want {
			t.Errorf("intelligenceDiscoveryBoost(%q) = %f, want %f", tt.tier, got, tt.want)
		}
	}
}

func TestAdjustRPMByRisk_AllTiers(t *testing.T) {
	tests := []struct {
		tier    string
		base    int
		wantMin int
		wantMax int
	}{
		{"diamond", 100, 140, 160},
		{"platinum", 100, 120, 130},
		{"gold", 100, 100, 100},
		{"silver", 100, 70, 80},
		{"bronze", 100, 45, 55},
		{"unknown", 100, 100, 100},
	}
	for _, tt := range tests {
		got := adjustRPMByRisk(tt.tier, tt.base)
		if got < tt.wantMin || got > tt.wantMax {
			t.Errorf("adjustRPMByRisk(%q, %d) = %d, want [%d, %d]", tt.tier, tt.base, got, tt.wantMin, tt.wantMax)
		}
	}
}

func TestScoreTier_AllRanges(t *testing.T) {
	tests := []struct {
		score float64
		want  string
	}{
		{100, "elite"},
		{80, "elite"},
		{79, "trusted"},
		{60, "trusted"},
		{59, "established"},
		{40, "established"},
		{39, "emerging"},
		{20, "emerging"},
		{19, "new"},
		{0, "new"},
	}
	for _, tt := range tests {
		got := scoreTier(tt.score)
		if got != tt.want {
			t.Errorf("scoreTier(%f) = %q, want %q", tt.score, got, tt.want)
		}
	}
}

// ============================================================================
// computeEdgeWeight coverage: edge cases
// ============================================================================

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

// ============================================================================
// ForwardError coverage
// ============================================================================

func TestForwardError_IsTransient(t *testing.T) {
	tests := []struct {
		code int
		want bool
	}{
		{500, true},
		{502, true},
		{503, true},
		{429, true},
		{400, false},
		{403, false},
		{404, false},
	}
	for _, tt := range tests {
		e := &ForwardError{StatusCode: tt.code, Message: "test"}
		if got := e.IsTransient(); got != tt.want {
			t.Errorf("ForwardError{%d}.IsTransient() = %v, want %v", tt.code, got, tt.want)
		}
	}
}

func TestForwardError_Error(t *testing.T) {
	e := &ForwardError{StatusCode: 500, Message: "service returned HTTP 500"}
	if e.Error() != "service returned HTTP 500" {
		t.Errorf("unexpected error message: %s", e.Error())
	}
}

// ============================================================================
// PolicyDecision nil-safe accessors
// ============================================================================

func TestPolicyDecision_NilAccessors(t *testing.T) {
	var d *PolicyDecision
	if d.GetDeniedBy() != "" {
		t.Error("expected empty string for nil")
	}
	if d.GetDeniedRule() != "" {
		t.Error("expected empty string for nil")
	}
	if d.GetReason() != "" {
		t.Error("expected empty string for nil")
	}
}

func TestPolicyDecision_WithValues(t *testing.T) {
	d := &PolicyDecision{
		DeniedBy:   "policy1",
		DeniedRule: "max_spend",
		Reason:     "exceeded limit",
	}
	if d.GetDeniedBy() != "policy1" {
		t.Errorf("expected policy1, got %s", d.GetDeniedBy())
	}
	if d.GetDeniedRule() != "max_spend" {
		t.Errorf("expected max_spend, got %s", d.GetDeniedRule())
	}
	if d.GetReason() != "exceeded limit" {
		t.Errorf("expected exceeded limit, got %s", d.GetReason())
	}
}

// ============================================================================
// Session model: IsExpired, Remaining edge cases
// ============================================================================

func TestSession_IsExpired_ZeroTime(t *testing.T) {
	s := &Session{}
	if s.IsExpired() {
		t.Error("zero ExpiresAt should not be expired")
	}
}

func TestSession_IsExpired_Future(t *testing.T) {
	s := &Session{ExpiresAt: time.Now().Add(1 * time.Hour)}
	if s.IsExpired() {
		t.Error("future ExpiresAt should not be expired")
	}
}

func TestSession_IsExpired_Past(t *testing.T) {
	s := &Session{ExpiresAt: time.Now().Add(-1 * time.Hour)}
	if !s.IsExpired() {
		t.Error("past ExpiresAt should be expired")
	}
}

func TestSession_Remaining_NegativeSpent(t *testing.T) {
	// If spent exceeds total (edge case), remaining should be 0
	s := &Session{MaxTotal: "1.000000", TotalSpent: "2.000000"}
	r := s.Remaining()
	if r != "0.000000" {
		t.Errorf("expected 0.000000 for over-spent, got %s", r)
	}
}

func TestSession_BuildAllowedTypesSet_Empty(t *testing.T) {
	s := &Session{}
	s.BuildAllowedTypesSet()
	if s.allowedTypesSet != nil {
		t.Error("expected nil set for empty AllowedTypes")
	}
}

// ============================================================================
// MoneyError
// ============================================================================

func TestMoneyError_WithEmptyFields(t *testing.T) {
	me := &MoneyError{
		Err:         fmt.Errorf("test"),
		FundsStatus: "no_change",
		Recovery:    "retry",
	}
	fields := moneyFields(me)
	if fields == nil {
		t.Fatal("expected fields")
	}
	// Amount and Reference are empty, should not be in fields
	if _, ok := fields["amount"]; ok {
		t.Error("empty amount should not be in fields")
	}
	if _, ok := fields["reference"]; ok {
		t.Error("empty reference should not be in fields")
	}
}

// ============================================================================
// Handler: gatewayTokenAuth edge cases
// ============================================================================

func TestGatewayTokenAuth_NoToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newTestServiceWithLogger(newMockLedger(), &mockRegistry{})
	handler := NewHandler(svc)

	r := gin.New()
	r.POST("/test", handler.gatewayTokenAuth(), func(c *gin.Context) {
		c.Status(200)
	})

	req := httptest.NewRequest("POST", "/test", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for missing token, got %d", w.Code)
	}
}

func TestGatewayTokenAuth_InvalidToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newTestServiceWithLogger(newMockLedger(), &mockRegistry{})
	handler := NewHandler(svc)

	r := gin.New()
	r.POST("/test", handler.gatewayTokenAuth(), func(c *gin.Context) {
		c.Status(200)
	})

	req := httptest.NewRequest("POST", "/test", nil)
	req.Header.Set("X-Gateway-Token", "invalid-token")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for invalid token, got %d", w.Code)
	}
}

func TestGatewayTokenAuth_ClosedSession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ml := newMockLedger()
	svc := newTestServiceWithLogger(ml, &mockRegistry{})

	session, _ := svc.CreateSession(context.Background(), "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "1.00",
	})
	svc.CloseSession(context.Background(), session.ID, "0xbuyer")

	handler := NewHandler(svc)
	r := gin.New()
	r.POST("/test", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xbuyer")
		c.Next()
	}, handler.gatewayTokenAuth(), func(c *gin.Context) {
		c.Status(200)
	})

	req := httptest.NewRequest("POST", "/test", nil)
	req.Header.Set("X-Gateway-Token", session.ID)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for closed session, got %d", w.Code)
	}
}

func TestGatewayTokenAuth_ExpiredSession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ml := newMockLedger()
	svc := newTestServiceWithLogger(ml, &mockRegistry{})

	session, _ := svc.CreateSession(context.Background(), "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "1.00",
	})

	// Expire the session
	s, _ := svc.store.GetSession(context.Background(), session.ID)
	s.ExpiresAt = time.Now().Add(-time.Hour)
	svc.store.UpdateSession(context.Background(), s)

	handler := NewHandler(svc)
	r := gin.New()
	r.POST("/test", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xbuyer")
		c.Next()
	}, handler.gatewayTokenAuth(), func(c *gin.Context) {
		c.Status(200)
	})

	req := httptest.NewRequest("POST", "/test", nil)
	req.Header.Set("X-Gateway-Token", session.ID)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for expired session, got %d", w.Code)
	}
}

// ============================================================================
// Handler: CreateSession error paths
// ============================================================================

func TestHandler_CreateSession_ValidationError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newTestServiceWithLogger(newMockLedger(), &mockRegistry{})
	handler := NewHandler(svc)

	r := gin.New()
	r.POST("/sessions", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xbuyer")
		c.Next()
	}, handler.CreateSession)

	body := `{"maxTotal":"bad","maxPerRequest":"1.00"}`
	req := httptest.NewRequest("POST", "/sessions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for validation error, got %d", w.Code)
	}
}

// ============================================================================
// Handler: Proxy error mapping
// ============================================================================

func TestHandler_Proxy_MissingServiceType(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newTestServiceWithLogger(newMockLedger(), &mockRegistry{})

	session, _ := svc.CreateSession(context.Background(), "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "1.00",
	})

	handler := NewHandler(svc)
	r := gin.New()
	r.POST("/proxy", func(c *gin.Context) {
		c.Set("gatewaySessionID", session.ID)
		c.Next()
	}, handler.Proxy)

	// Empty body (missing serviceType)
	req := httptest.NewRequest("POST", "/proxy", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid body, got %d", w.Code)
	}
}

// ============================================================================
// Handler: Pipeline error
// ============================================================================

func TestHandler_Pipeline_InvalidBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newTestServiceWithLogger(newMockLedger(), &mockRegistry{})

	session, _ := svc.CreateSession(context.Background(), "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "1.00",
	})

	handler := NewHandler(svc)
	r := gin.New()
	r.POST("/pipeline", func(c *gin.Context) {
		c.Set("gatewaySessionID", session.ID)
		c.Next()
	}, handler.Pipeline)

	req := httptest.NewRequest("POST", "/pipeline", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid pipeline body, got %d", w.Code)
	}
}

// ============================================================================
// Handler: DryRun error paths
// ============================================================================

func TestHandler_DryRun_WrongOwner(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ml := newMockLedger()
	svc := newTestServiceWithLogger(ml, &mockRegistry{})

	session, _ := svc.CreateSession(context.Background(), "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "1.00",
	})

	handler := NewHandler(svc)
	r := gin.New()
	r.POST("/dry-run/:id", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xstranger")
		c.Next()
	}, handler.DryRun)

	body := `{"serviceType":"test"}`
	req := httptest.NewRequest("POST", "/dry-run/"+session.ID, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 for wrong owner, got %d", w.Code)
	}
}

func TestHandler_DryRun_InvalidBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ml := newMockLedger()
	svc := newTestServiceWithLogger(ml, &mockRegistry{})

	session, _ := svc.CreateSession(context.Background(), "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "1.00",
	})

	handler := NewHandler(svc)
	r := gin.New()
	r.POST("/dry-run/:id", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xbuyer")
		c.Next()
	}, handler.DryRun)

	req := httptest.NewRequest("POST", "/dry-run/"+session.ID, strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid body, got %d", w.Code)
	}
}

// ============================================================================
// Handler: ListLogs error paths
// ============================================================================

func TestHandler_ListLogs_WrongOwner(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ml := newMockLedger()
	svc := newTestServiceWithLogger(ml, &mockRegistry{})

	session, _ := svc.CreateSession(context.Background(), "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "1.00",
	})

	handler := NewHandler(svc)
	r := gin.New()
	r.GET("/logs/:id", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xstranger")
		c.Next()
	}, handler.ListLogs)

	req := httptest.NewRequest("GET", "/logs/"+session.ID, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 for wrong owner, got %d", w.Code)
	}
}

func TestHandler_ListLogs_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newTestServiceWithLogger(newMockLedger(), &mockRegistry{})

	handler := NewHandler(svc)
	r := gin.New()
	r.GET("/logs/:id", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xbuyer")
		c.Next()
	}, handler.ListLogs)

	req := httptest.NewRequest("GET", "/logs/nonexistent", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for not found, got %d", w.Code)
	}
}

func TestHandler_ListLogs_WithLimitParam(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ml := newMockLedger()
	svc := newTestServiceWithLogger(ml, &mockRegistry{})

	session, _ := svc.CreateSession(context.Background(), "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "1.00",
	})

	handler := NewHandler(svc)
	r := gin.New()
	r.GET("/logs/:id", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xbuyer")
		c.Next()
	}, handler.ListLogs)

	req := httptest.NewRequest("GET", "/logs/"+session.ID+"?limit=5", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

// ============================================================================
// Handler: SingleCall
// ============================================================================

func TestHandler_SingleCall_BadBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newTestServiceWithLogger(newMockLedger(), &mockRegistry{})

	handler := NewHandler(svc)
	r := gin.New()
	r.POST("/call", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xbuyer")
		c.Next()
	}, handler.SingleCall)

	req := httptest.NewRequest("POST", "/call", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for bad body, got %d", w.Code)
	}
}

// ============================================================================
// Handler: ListSessions with limit and cursor
// ============================================================================

func TestHandler_ListSessions_WithLimitParam(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ml := newMockLedger()
	svc := newTestServiceWithLogger(ml, &mockRegistry{})

	svc.CreateSession(context.Background(), "0xbuyer", "", CreateSessionRequest{
		MaxTotal: "10.00", MaxPerRequest: "1.00",
	})

	handler := NewHandler(svc)
	r := gin.New()
	r.GET("/sessions", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xbuyer")
		c.Next()
	}, handler.ListSessions)

	req := httptest.NewRequest("GET", "/sessions?limit=5", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["has_more"] != false {
		t.Error("expected has_more=false")
	}
}

func TestHandler_ListSessions_CapAt200(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newTestServiceWithLogger(newMockLedger(), &mockRegistry{})

	handler := NewHandler(svc)
	r := gin.New()
	r.GET("/sessions", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xbuyer")
		c.Next()
	}, handler.ListSessions)

	req := httptest.NewRequest("GET", "/sessions?limit=500", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

// ============================================================================
// Handler: CloseSession error paths
// ============================================================================

func TestHandler_CloseSession_WrongOwner(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ml := newMockLedger()
	svc := newTestServiceWithLogger(ml, &mockRegistry{})

	session, _ := svc.CreateSession(context.Background(), "0xbuyer", "", CreateSessionRequest{
		MaxTotal: "10.00", MaxPerRequest: "1.00",
	})

	handler := NewHandler(svc)
	r := gin.New()
	r.DELETE("/sessions/:id", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xstranger")
		c.Next()
	}, handler.CloseSession)

	req := httptest.NewRequest("DELETE", "/sessions/"+session.ID, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}
}

// ============================================================================
// Service: SetupRouting, HealthMonitor, HealthAwareRouter accessors
// ============================================================================

func TestService_SetupRouting(t *testing.T) {
	svc := newTestServiceWithLogger(newMockLedger(), &mockRegistry{})
	got := svc.SetupRouting(testLogger())
	if got != svc {
		t.Error("SetupRouting should return same service")
	}
	if svc.HealthMonitor() == nil {
		t.Error("expected non-nil health monitor after SetupRouting")
	}
	if svc.HealthAwareRouter() == nil {
		t.Error("expected non-nil health-aware router after SetupRouting")
	}
}

func TestService_WithHealthMonitor_Extra(t *testing.T) {
	svc := newTestService(newMockLedger(), &mockRegistry{})
	hm := NewHealthMonitor(DefaultHealthMonitorConfig())
	svc.WithHealthMonitor(hm)
	if svc.HealthMonitor() != hm {
		t.Error("expected health monitor to be set")
	}
}

// ============================================================================
// Routing metrics helpers (exercise but no assertion needed)
// ============================================================================

func TestRoutingMetricHelpers(t *testing.T) {
	// These just exercise the metric recording functions without panicking
	updateProviderHealthMetric("test-provider", HealthHealthy)
	updateProviderHealthMetric("test-provider", HealthDegraded)
	updateProviderHealthMetric("test-provider", HealthUnhealthy)
	updateProviderHealthMetric("test-provider", HealthUnknown)
	recordRerouteMetric("from", "to")
	recordRacerExpansionMetric()
	recordRequestOutcomeMetric(OutcomeSucceeded)
	recordRequestOutcomeMetric(OutcomeRerouted)
	recordRequestOutcomeMetric(OutcomeEscalated)
}

// ============================================================================
// Forwarder coverage
// ============================================================================

func TestForwarder_NonJSONResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(200)
		w.Write([]byte("not json"))
	}))
	defer server.Close()

	fwd := NewForwarder(5 * time.Second).WithAllowLocalEndpoints()
	resp, err := fwd.Forward(context.Background(), ForwardRequest{
		Endpoint: server.URL,
		Params:   map[string]interface{}{"key": "val"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Body["raw"] != "not json" {
		t.Errorf("expected raw body, got %v", resp.Body)
	}
}

func TestForwarder_4xxError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		w.Write([]byte(`{"error":"bad request"}`))
	}))
	defer server.Close()

	fwd := NewForwarder(5 * time.Second).WithAllowLocalEndpoints()
	_, err := fwd.Forward(context.Background(), ForwardRequest{
		Endpoint: server.URL,
		Params:   map[string]interface{}{},
	})
	if err == nil {
		t.Fatal("expected error for 400 response")
	}
	fe, ok := err.(*ForwardError)
	if !ok {
		t.Fatalf("expected ForwardError, got %T", err)
	}
	if fe.StatusCode != 400 {
		t.Errorf("expected 400, got %d", fe.StatusCode)
	}
	if fe.IsTransient() {
		t.Error("400 should not be transient")
	}
}

func TestForwarder_PaymentHeaders(t *testing.T) {
	var gotHeaders http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(map[string]string{"ok": "true"})
	}))
	defer server.Close()

	fwd := NewForwarder(5 * time.Second).WithAllowLocalEndpoints()
	_, err := fwd.Forward(context.Background(), ForwardRequest{
		Endpoint:  server.URL,
		Params:    map[string]interface{}{},
		FromAddr:  "0xbuyer",
		Amount:    "1.000000",
		Reference: "ref123",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotHeaders.Get("X-Payment-Amount") != "1.000000" {
		t.Errorf("missing X-Payment-Amount header")
	}
	if gotHeaders.Get("X-Payment-From") != "0xbuyer" {
		t.Errorf("missing X-Payment-From header")
	}
	if gotHeaders.Get("X-Payment-Ref") != "ref123" {
		t.Errorf("missing X-Payment-Ref header")
	}
}

func TestForwarder_EmptyResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer server.Close()

	fwd := NewForwarder(5 * time.Second).WithAllowLocalEndpoints()
	resp, err := fwd.Forward(context.Background(), ForwardRequest{
		Endpoint: server.URL,
		Params:   map[string]interface{}{},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Body) > 0 {
		t.Errorf("expected nil/empty body for empty response, got %v", resp.Body)
	}
}

// ============================================================================
// Handler: RegisterRoutes coverage (no panic)
// ============================================================================

func TestHandler_RegisterRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newTestService(newMockLedger(), &mockRegistry{})
	handler := NewHandler(svc)

	r := gin.New()
	g := r.Group("/v1")
	handler.RegisterProtectedRoutes(g)
	handler.RegisterProxyRoute(g)

	// Just verify routes are registered without panic
	routes := r.Routes()
	if len(routes) == 0 {
		t.Error("expected routes to be registered")
	}
}

// ============================================================================
// ListOption / WithCursor coverage
// ============================================================================

func TestListOption_WithCursor(t *testing.T) {
	// Invalid cursor should not panic
	opt := WithCursor("invalid-base64")
	var o listOpts
	opt(&o)
	if o.cursor != nil {
		t.Error("expected nil cursor for invalid input")
	}
}

func TestApplyListOpts_Empty(t *testing.T) {
	o := applyListOpts(nil)
	if o.cursor != nil {
		t.Error("expected nil cursor for empty opts")
	}
}

// ============================================================================
// Proxy handler: SingleCall with MoneyError
// ============================================================================

func TestHandler_SingleCall_NoService(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newTestServiceWithLogger(newMockLedger(), &mockRegistry{services: nil})

	handler := NewHandler(svc)
	r := gin.New()
	r.POST("/call", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xbuyer")
		c.Next()
	}, handler.SingleCall)

	body := `{"maxPrice":"10.00","serviceType":"test"}`
	req := httptest.NewRequest("POST", "/call", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for no service, got %d", w.Code)
	}
}

// ============================================================================
// Handler: GetSession internal error path
// ============================================================================

func TestHandler_GetSession_InternalError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newTestServiceWithLogger(newMockLedger(), &mockRegistry{})

	handler := NewHandler(svc)
	r := gin.New()
	r.GET("/sessions/:id", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xbuyer")
		c.Next()
	}, handler.GetSession)

	// Session doesn't exist -> returns not found
	req := httptest.NewRequest("GET", "/sessions/nonexistent", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

// ============================================================================
// Handler: Proxy various error codes
// ============================================================================

func TestHandler_Pipeline_StepFails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ml := newMockLedger()
	svc := newTestServiceWithLogger(ml, &mockRegistry{services: nil})

	session, _ := svc.CreateSession(context.Background(), "0xbuyer", "", CreateSessionRequest{
		MaxTotal: "10.00", MaxPerRequest: "1.00",
	})

	handler := NewHandler(svc)
	r := gin.New()
	r.POST("/pipeline", func(c *gin.Context) {
		c.Set("gatewaySessionID", session.ID)
		c.Next()
	}, handler.Pipeline)

	body, _ := json.Marshal(PipelineRequest{
		Steps: []PipelineStep{{ServiceType: "test"}},
	})
	req := httptest.NewRequest("POST", "/pipeline", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Should fail because no services available
	if w.Code == http.StatusOK {
		t.Error("expected error status for failed pipeline")
	}
}
