package policy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mbd888/alancoin/internal/gateway"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// ============================================================================
// Validation tests
// ============================================================================

func TestValidateRules_TimeWindow(t *testing.T) {
	tests := []struct {
		name    string
		params  TimeWindowParams
		wantErr bool
	}{
		{"valid", TimeWindowParams{StartHour: 9, EndHour: 17}, false},
		{"overnight", TimeWindowParams{StartHour: 22, EndHour: 6}, false},
		{"with days", TimeWindowParams{StartHour: 9, EndHour: 17, Days: []string{"monday", "friday"}}, false},
		{"with tz", TimeWindowParams{StartHour: 9, EndHour: 17, Timezone: "America/New_York"}, false},
		{"bad start hour", TimeWindowParams{StartHour: 25, EndHour: 17}, true},
		{"bad end hour", TimeWindowParams{StartHour: 9, EndHour: -1}, true},
		{"bad day", TimeWindowParams{StartHour: 9, EndHour: 17, Days: []string{"funday"}}, true},
		{"bad timezone", TimeWindowParams{StartHour: 9, EndHour: 17, Timezone: "Mars/Olympus"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, _ := json.Marshal(tt.params)
			rules := []Rule{{Type: "time_window", Params: raw}}
			err := ValidateRules(rules)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateRules() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateRules_RateLimit(t *testing.T) {
	tests := []struct {
		name    string
		params  RateLimitParams
		wantErr bool
	}{
		{"valid", RateLimitParams{MaxRequests: 10, WindowSeconds: 60}, false},
		{"zero max", RateLimitParams{MaxRequests: 0, WindowSeconds: 60}, true},
		{"zero window", RateLimitParams{MaxRequests: 10, WindowSeconds: 0}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, _ := json.Marshal(tt.params)
			rules := []Rule{{Type: "rate_limit", Params: raw}}
			err := ValidateRules(rules)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateRules() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateRules_ServiceLists(t *testing.T) {
	valid, _ := json.Marshal(ServiceListParams{Services: []string{"translation"}})
	empty, _ := json.Marshal(ServiceListParams{Services: []string{}})

	for _, ruleType := range []string{"service_allowlist", "service_blocklist"} {
		t.Run(ruleType+"_valid", func(t *testing.T) {
			if err := ValidateRules([]Rule{{Type: ruleType, Params: valid}}); err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
		t.Run(ruleType+"_empty", func(t *testing.T) {
			if err := ValidateRules([]Rule{{Type: ruleType, Params: empty}}); err == nil {
				t.Error("expected error for empty services list")
			}
		})
	}
}

func TestValidateRules_MaxRequests(t *testing.T) {
	valid, _ := json.Marshal(MaxRequestsParams{MaxCount: 100})
	invalid, _ := json.Marshal(MaxRequestsParams{MaxCount: 0})

	if err := ValidateRules([]Rule{{Type: "max_requests", Params: valid}}); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if err := ValidateRules([]Rule{{Type: "max_requests", Params: invalid}}); err == nil {
		t.Error("expected error for zero maxCount")
	}
}

func TestValidateRules_SpendVelocity(t *testing.T) {
	valid, _ := json.Marshal(SpendVelocityParams{MaxPerHour: "100.00"})
	empty, _ := json.Marshal(SpendVelocityParams{MaxPerHour: ""})

	if err := ValidateRules([]Rule{{Type: "spend_velocity", Params: valid}}); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if err := ValidateRules([]Rule{{Type: "spend_velocity", Params: empty}}); err == nil {
		t.Error("expected error for empty maxPerHour")
	}
}

func TestValidateRules_UnknownType(t *testing.T) {
	rules := []Rule{{Type: "future_rule", Params: json.RawMessage(`{}`)}}
	if err := ValidateRules(rules); err == nil {
		t.Error("unknown rule types should be rejected at validation time")
	}
}

// ============================================================================
// Evaluator tests
// ============================================================================

func makeSession(tenantID string, requestCount int, totalSpent string, createdAt time.Time) *gateway.Session {
	return &gateway.Session{
		ID:           "gw_test",
		AgentAddr:    "0xagent",
		TenantID:     tenantID,
		MaxTotal:     "1000.000000",
		TotalSpent:   totalSpent,
		RequestCount: requestCount,
		Status:       gateway.StatusActive,
		CreatedAt:    createdAt,
		UpdatedAt:    time.Now(),
	}
}

func TestEvaluator_NoTenant(t *testing.T) {
	store := NewMemoryStore()
	eval := NewEvaluator(store)

	session := makeSession("", 0, "0.000000", time.Now())
	decision, err := eval.EvaluateProxy(context.Background(), session, "translation")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !decision.Allowed {
		t.Error("no-tenant session should be allowed")
	}
}

func TestEvaluator_NoPolicies(t *testing.T) {
	store := NewMemoryStore()
	eval := NewEvaluator(store)

	session := makeSession("ten_abc", 0, "0.000000", time.Now())
	decision, err := eval.EvaluateProxy(context.Background(), session, "translation")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !decision.Allowed {
		t.Error("no policies should mean allow")
	}
}

func TestEvaluator_DisabledPolicy(t *testing.T) {
	store := NewMemoryStore()
	maxReqParams, _ := json.Marshal(MaxRequestsParams{MaxCount: 1})
	pol := &SpendPolicy{
		ID:        "sp_1",
		TenantID:  "ten_abc",
		Name:      "disabled",
		Rules:     []Rule{{Type: "max_requests", Params: maxReqParams}},
		Enabled:   false,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	_ = store.Create(context.Background(), pol)

	eval := NewEvaluator(store)
	session := makeSession("ten_abc", 5, "10.000000", time.Now())
	decision, err := eval.EvaluateProxy(context.Background(), session, "translation")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !decision.Allowed {
		t.Error("disabled policy should not deny")
	}
}

func TestEvaluator_ServiceBlocklist(t *testing.T) {
	store := NewMemoryStore()
	blockParams, _ := json.Marshal(ServiceListParams{Services: []string{"compute", "dangerous"}})
	pol := &SpendPolicy{
		ID:        "sp_1",
		TenantID:  "ten_abc",
		Name:      "block compute",
		Rules:     []Rule{{Type: "service_blocklist", Params: blockParams}},
		Enabled:   true,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	_ = store.Create(context.Background(), pol)

	eval := NewEvaluator(store)

	// Blocked service
	session := makeSession("ten_abc", 0, "0.000000", time.Now())
	_, err := eval.EvaluateProxy(context.Background(), session, "compute")
	if err == nil {
		t.Error("expected denial for blocked service type")
	}

	// Allowed service
	decision, err := eval.EvaluateProxy(context.Background(), session, "translation")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !decision.Allowed {
		t.Error("translation should be allowed")
	}

	// Empty service type (session creation) should skip
	decision, err = eval.EvaluateProxy(context.Background(), session, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !decision.Allowed {
		t.Error("empty service type should be allowed (session creation)")
	}
}

func TestEvaluator_ServiceAllowlist(t *testing.T) {
	store := NewMemoryStore()
	allowParams, _ := json.Marshal(ServiceListParams{Services: []string{"translation", "inference"}})
	pol := &SpendPolicy{
		ID:        "sp_1",
		TenantID:  "ten_abc",
		Name:      "allow list",
		Rules:     []Rule{{Type: "service_allowlist", Params: allowParams}},
		Enabled:   true,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	_ = store.Create(context.Background(), pol)

	eval := NewEvaluator(store)
	session := makeSession("ten_abc", 0, "0.000000", time.Now())

	// Allowed
	decision, err := eval.EvaluateProxy(context.Background(), session, "translation")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !decision.Allowed {
		t.Error("translation should be allowed")
	}

	// Not in list
	_, err = eval.EvaluateProxy(context.Background(), session, "compute")
	if err == nil {
		t.Error("expected denial for service not in allowlist")
	}
}

func TestEvaluator_MaxRequests(t *testing.T) {
	store := NewMemoryStore()
	params, _ := json.Marshal(MaxRequestsParams{MaxCount: 5})
	pol := &SpendPolicy{
		ID:        "sp_1",
		TenantID:  "ten_abc",
		Name:      "max req",
		Rules:     []Rule{{Type: "max_requests", Params: params}},
		Enabled:   true,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	_ = store.Create(context.Background(), pol)

	eval := NewEvaluator(store)

	// Under limit
	session := makeSession("ten_abc", 4, "10.000000", time.Now())
	decision, err := eval.EvaluateProxy(context.Background(), session, "translation")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !decision.Allowed {
		t.Error("4/5 requests should be allowed")
	}

	// At limit
	session = makeSession("ten_abc", 5, "10.000000", time.Now())
	_, err = eval.EvaluateProxy(context.Background(), session, "translation")
	if err == nil {
		t.Error("5/5 requests should be denied")
	}
}

func TestEvaluator_RateLimit(t *testing.T) {
	store := NewMemoryStore()
	params, _ := json.Marshal(RateLimitParams{MaxRequests: 10, WindowSeconds: 60})
	pol := &SpendPolicy{
		ID:        "sp_1",
		TenantID:  "ten_abc",
		Name:      "rate limit",
		Rules:     []Rule{{Type: "rate_limit", Params: params}},
		Enabled:   true,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	_ = store.Create(context.Background(), pol)

	eval := NewEvaluator(store)

	// Within window, under limit
	session := makeSession("ten_abc", 9, "0.000000", time.Now().Add(-30*time.Second))
	decision, err := eval.EvaluateProxy(context.Background(), session, "translation")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !decision.Allowed {
		t.Error("9/10 in 30s window should be allowed")
	}

	// Within window, at limit
	session = makeSession("ten_abc", 10, "0.000000", time.Now().Add(-30*time.Second))
	_, err = eval.EvaluateProxy(context.Background(), session, "translation")
	if err == nil {
		t.Error("10/10 in 30s window should be denied")
	}
}

func TestEvaluator_SpendVelocity_GracePeriod(t *testing.T) {
	store := NewMemoryStore()
	params, _ := json.Marshal(SpendVelocityParams{MaxPerHour: "10.000000"})
	pol := &SpendPolicy{
		ID:        "sp_1",
		TenantID:  "ten_abc",
		Name:      "velocity",
		Rules:     []Rule{{Type: "spend_velocity", Params: params}},
		Enabled:   true,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	_ = store.Create(context.Background(), pol)

	eval := NewEvaluator(store)

	// Within 60-second grace period — should be allowed even if over-spending
	session := makeSession("ten_abc", 100, "500.000000", time.Now().Add(-30*time.Second))
	decision, err := eval.EvaluateProxy(context.Background(), session, "translation")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !decision.Allowed {
		t.Error("within 60-second grace period should be allowed")
	}
}

func TestEvaluator_SpendVelocity_Deny(t *testing.T) {
	store := NewMemoryStore()
	params, _ := json.Marshal(SpendVelocityParams{MaxPerHour: "10.000000"})
	pol := &SpendPolicy{
		ID:        "sp_1",
		TenantID:  "ten_abc",
		Name:      "velocity",
		Rules:     []Rule{{Type: "spend_velocity", Params: params}},
		Enabled:   true,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	_ = store.Create(context.Background(), pol)

	eval := NewEvaluator(store)

	// Past grace period, spending too fast
	session := makeSession("ten_abc", 50, "500.000000", time.Now().Add(-30*time.Minute))
	_, err := eval.EvaluateProxy(context.Background(), session, "translation")
	if err == nil {
		t.Error("500 USDC in 30 min (1000/hr) exceeds 10/hr limit")
	}
}

func TestEvaluator_SpendVelocity_Allow(t *testing.T) {
	store := NewMemoryStore()
	params, _ := json.Marshal(SpendVelocityParams{MaxPerHour: "100.000000"})
	pol := &SpendPolicy{
		ID:        "sp_1",
		TenantID:  "ten_abc",
		Name:      "velocity",
		Rules:     []Rule{{Type: "spend_velocity", Params: params}},
		Enabled:   true,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	_ = store.Create(context.Background(), pol)

	eval := NewEvaluator(store)

	// Past grace period, within rate
	session := makeSession("ten_abc", 5, "10.000000", time.Now().Add(-1*time.Hour))
	decision, err := eval.EvaluateProxy(context.Background(), session, "translation")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !decision.Allowed {
		t.Error("10 USDC in 1 hour is under 100/hr limit")
	}
}

func TestEvaluator_Priority(t *testing.T) {
	store := NewMemoryStore()

	// Policy 1: priority 10, allows all
	allowParams, _ := json.Marshal(MaxRequestsParams{MaxCount: 100})
	pol1 := &SpendPolicy{
		ID:        "sp_1",
		TenantID:  "ten_abc",
		Name:      "lenient",
		Rules:     []Rule{{Type: "max_requests", Params: allowParams}},
		Priority:  10,
		Enabled:   true,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	// Policy 2: priority 1, strict
	denyParams, _ := json.Marshal(MaxRequestsParams{MaxCount: 1})
	pol2 := &SpendPolicy{
		ID:        "sp_2",
		TenantID:  "ten_abc",
		Name:      "strict",
		Rules:     []Rule{{Type: "max_requests", Params: denyParams}},
		Priority:  1,
		Enabled:   true,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	_ = store.Create(context.Background(), pol1)
	_ = store.Create(context.Background(), pol2)

	eval := NewEvaluator(store)
	session := makeSession("ten_abc", 5, "0.000000", time.Now())
	decision, err := eval.EvaluateProxy(context.Background(), session, "translation")
	if err == nil {
		t.Fatal("expected denial from strict (priority 1) policy")
	}
	_ = decision
}

func TestEvaluator_UnknownRuleSkipped(t *testing.T) {
	store := NewMemoryStore()
	pol := &SpendPolicy{
		ID:        "sp_1",
		TenantID:  "ten_abc",
		Name:      "future",
		Rules:     []Rule{{Type: "quantum_limit", Params: json.RawMessage(`{}`)}},
		Enabled:   true,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	_ = store.Create(context.Background(), pol)

	eval := NewEvaluator(store)
	session := makeSession("ten_abc", 0, "0.000000", time.Now())
	decision, err := eval.EvaluateProxy(context.Background(), session, "translation")
	if err != nil {
		t.Fatalf("unknown rule types should be skipped: %v", err)
	}
	if !decision.Allowed {
		t.Error("unknown rule types should not deny")
	}
}

func TestEvaluator_DecisionFields(t *testing.T) {
	store := NewMemoryStore()
	params, _ := json.Marshal(ServiceListParams{Services: []string{"compute"}})
	pol := &SpendPolicy{
		ID:        "sp_1",
		TenantID:  "ten_abc",
		Name:      "block compute",
		Rules:     []Rule{{Type: "service_blocklist", Params: params}},
		Enabled:   true,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	_ = store.Create(context.Background(), pol)

	eval := NewEvaluator(store)
	session := makeSession("ten_abc", 0, "0.000000", time.Now())
	decision, _ := eval.EvaluateProxy(context.Background(), session, "compute")

	if decision.Allowed {
		t.Fatal("should be denied")
	}
	if decision.DeniedBy != "block compute" {
		t.Errorf("DeniedBy = %q, want %q", decision.DeniedBy, "block compute")
	}
	if decision.DeniedRule != "service_blocklist" {
		t.Errorf("DeniedRule = %q, want %q", decision.DeniedRule, "service_blocklist")
	}
	if decision.Reason == "" {
		t.Error("Reason should not be empty")
	}
	if decision.LatencyUs <= 0 {
		t.Error("LatencyUs should be positive")
	}
}

// ============================================================================
// Memory store tests
// ============================================================================

func TestMemoryStore_CRUD(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	now := time.Now()
	p := &SpendPolicy{
		ID:        "sp_1",
		TenantID:  "ten_abc",
		Name:      "test policy",
		Rules:     []Rule{{Type: "max_requests", Params: json.RawMessage(`{"maxCount":10}`)}},
		Priority:  0,
		Enabled:   true,
		CreatedAt: now,
		UpdatedAt: now,
	}

	// Create
	if err := store.Create(ctx, p); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Get
	got, err := store.Get(ctx, "sp_1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "test policy" {
		t.Errorf("Name = %q, want %q", got.Name, "test policy")
	}

	// List
	list, err := store.List(ctx, "ten_abc")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("List: got %d, want 1", len(list))
	}

	// Update
	got.Name = "updated"
	got.UpdatedAt = time.Now()
	if err := store.Update(ctx, got); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got2, _ := store.Get(ctx, "sp_1")
	if got2.Name != "updated" {
		t.Errorf("Name after update = %q, want %q", got2.Name, "updated")
	}

	// Delete
	if err := store.Delete(ctx, "sp_1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, err = store.Get(ctx, "sp_1")
	if err != ErrPolicyNotFound {
		t.Errorf("Get after Delete: got %v, want ErrPolicyNotFound", err)
	}
}

func TestMemoryStore_NameUniqueness(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	now := time.Now()
	p1 := &SpendPolicy{ID: "sp_1", TenantID: "ten_abc", Name: "dup", CreatedAt: now, UpdatedAt: now}
	p2 := &SpendPolicy{ID: "sp_2", TenantID: "ten_abc", Name: "dup", CreatedAt: now, UpdatedAt: now}
	p3 := &SpendPolicy{ID: "sp_3", TenantID: "ten_other", Name: "dup", CreatedAt: now, UpdatedAt: now}

	_ = store.Create(ctx, p1)

	// Same tenant, same name → error
	if err := store.Create(ctx, p2); err != ErrNameTaken {
		t.Errorf("expected ErrNameTaken, got %v", err)
	}

	// Different tenant, same name → OK
	if err := store.Create(ctx, p3); err != nil {
		t.Errorf("different tenant should allow same name: %v", err)
	}
}

func TestMemoryStore_NotFound(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	_, err := store.Get(ctx, "nonexistent")
	if err != ErrPolicyNotFound {
		t.Errorf("Get nonexistent: got %v, want ErrPolicyNotFound", err)
	}
	if err := store.Update(ctx, &SpendPolicy{ID: "nonexistent"}); err != ErrPolicyNotFound {
		t.Errorf("Update nonexistent: got %v, want ErrPolicyNotFound", err)
	}
	if err := store.Delete(ctx, "nonexistent"); err != ErrPolicyNotFound {
		t.Errorf("Delete nonexistent: got %v, want ErrPolicyNotFound", err)
	}
}

// ============================================================================
// Handler tests
// ============================================================================

func setupRouter(store Store, tenantID string) *gin.Engine {
	r := gin.New()
	h := NewHandler(store)
	// Simulate auth middleware by setting tenant ID in context
	group := r.Group("/v1", func(c *gin.Context) {
		c.Set("authTenantID", tenantID)
		c.Next()
	})
	h.RegisterRoutes(group)
	return r
}

func TestHandler_CreateAndList(t *testing.T) {
	store := NewMemoryStore()
	router := setupRouter(store, "ten_abc")

	body := `{
		"name": "business hours",
		"rules": [{"type":"time_window","params":{"startHour":9,"endHour":17}}],
		"priority": 1,
		"enabled": true
	}`

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/tenants/ten_abc/policies", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("Create: status %d, body: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Policy SpendPolicy `json:"policy"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Policy.Name != "business hours" {
		t.Errorf("Name = %q, want %q", resp.Policy.Name, "business hours")
	}
	if resp.Policy.TenantID != "ten_abc" {
		t.Errorf("TenantID = %q, want %q", resp.Policy.TenantID, "ten_abc")
	}

	// List
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/v1/tenants/ten_abc/policies", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("List: status %d, body: %s", w.Code, w.Body.String())
	}

	var listResp struct {
		Policies []SpendPolicy `json:"policies"`
		Count    int           `json:"count"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &listResp)
	if listResp.Count != 1 {
		t.Errorf("Count = %d, want 1", listResp.Count)
	}
}

func TestHandler_GetUpdateDelete(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	now := time.Now()
	pol := &SpendPolicy{
		ID:        "sp_test",
		TenantID:  "ten_abc",
		Name:      "test",
		Rules:     []Rule{{Type: "max_requests", Params: json.RawMessage(`{"maxCount":10}`)}},
		Enabled:   true,
		CreatedAt: now,
		UpdatedAt: now,
	}
	_ = store.Create(ctx, pol)

	router := setupRouter(store, "ten_abc")

	// Get
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/tenants/ten_abc/policies/sp_test", nil)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("Get: status %d", w.Code)
	}

	// Update
	updateBody := `{"name":"updated name","enabled":false}`
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("PUT", "/v1/tenants/ten_abc/policies/sp_test", bytes.NewBufferString(updateBody))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("Update: status %d, body: %s", w.Code, w.Body.String())
	}

	var updateResp struct {
		Policy SpendPolicy `json:"policy"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &updateResp)
	if updateResp.Policy.Name != "updated name" {
		t.Errorf("Name = %q, want %q", updateResp.Policy.Name, "updated name")
	}
	if updateResp.Policy.Enabled {
		t.Error("Enabled should be false after update")
	}

	// Delete
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("DELETE", "/v1/tenants/ten_abc/policies/sp_test", nil)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("Delete: status %d", w.Code)
	}

	// Verify deleted
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/v1/tenants/ten_abc/policies/sp_test", nil)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("Get after delete: status %d, want 404", w.Code)
	}
}

func TestHandler_InvalidRules(t *testing.T) {
	store := NewMemoryStore()
	router := setupRouter(store, "ten_abc")

	body := `{
		"name": "bad",
		"rules": [{"type":"rate_limit","params":{"maxRequests":0,"windowSeconds":60}}]
	}`

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/tenants/ten_abc/policies", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid rules, got %d", w.Code)
	}
}

func TestHandler_DuplicateName(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	now := time.Now()
	_ = store.Create(ctx, &SpendPolicy{
		ID: "sp_1", TenantID: "ten_abc", Name: "dup", Enabled: true,
		CreatedAt: now, UpdatedAt: now,
	})

	router := setupRouter(store, "ten_abc")
	body := `{"name":"dup","rules":[{"type":"max_requests","params":{"maxCount":5}}]}`

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/tenants/ten_abc/policies", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("expected 409 for duplicate name, got %d", w.Code)
	}
}

func TestHandler_WrongTenant(t *testing.T) {
	store := NewMemoryStore()
	router := setupRouter(store, "ten_other") // authenticated as different tenant

	body := `{"name":"test","rules":[{"type":"max_requests","params":{"maxCount":5}}]}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/tenants/ten_abc/policies", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 for wrong tenant, got %d", w.Code)
	}
}

func TestHandler_AdminBypass(t *testing.T) {
	// Set ADMIN_SECRET so auth.IsAdminRequest validates correctly.
	os.Setenv("ADMIN_SECRET", "test-secret")
	defer os.Unsetenv("ADMIN_SECRET")

	store := NewMemoryStore()
	r := gin.New()
	h := NewHandler(store)
	group := r.Group("/v1")
	h.RegisterRoutes(group)

	body := `{"name":"admin created","rules":[{"type":"max_requests","params":{"maxCount":5}}]}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/tenants/ten_abc/policies", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Admin-Secret", "test-secret")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("admin should bypass tenant check, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_AdminBypass_WrongSecret(t *testing.T) {
	os.Setenv("ADMIN_SECRET", "real-secret")
	defer os.Unsetenv("ADMIN_SECRET")

	store := NewMemoryStore()
	r := gin.New()
	h := NewHandler(store)
	group := r.Group("/v1")
	h.RegisterRoutes(group)

	body := `{"name":"test","rules":[{"type":"max_requests","params":{"maxCount":5}}]}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/tenants/ten_abc/policies", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Admin-Secret", "wrong-secret")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("wrong admin secret should be forbidden, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_CrossTenantGet(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	now := time.Now()
	_ = store.Create(ctx, &SpendPolicy{
		ID: "sp_secret", TenantID: "ten_other", Name: "secret", Enabled: true,
		CreatedAt: now, UpdatedAt: now,
	})

	router := setupRouter(store, "ten_abc")

	// Try to read another tenant's policy using our tenant's route
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/tenants/ten_abc/policies/sp_secret", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("cross-tenant get should return 404, got %d", w.Code)
	}
}

func TestHandler_UpdateWithNewRules(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	now := time.Now()
	_ = store.Create(ctx, &SpendPolicy{
		ID:        "sp_1",
		TenantID:  "ten_abc",
		Name:      "test",
		Rules:     []Rule{{Type: "max_requests", Params: json.RawMessage(`{"maxCount":10}`)}},
		Enabled:   true,
		CreatedAt: now,
		UpdatedAt: now,
	})

	router := setupRouter(store, "ten_abc")

	// Update with new rules
	body := fmt.Sprintf(`{"rules":[{"type":"service_blocklist","params":{"services":["compute"]}}]}`)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/v1/tenants/ten_abc/policies/sp_1", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Update: status %d, body: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Policy SpendPolicy `json:"policy"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Policy.Rules) != 1 || resp.Policy.Rules[0].Type != "service_blocklist" {
		t.Errorf("rules not updated correctly: %+v", resp.Policy.Rules)
	}
}

func TestHandler_UpdateInvalidRules(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	now := time.Now()
	_ = store.Create(ctx, &SpendPolicy{
		ID: "sp_1", TenantID: "ten_abc", Name: "test", Enabled: true,
		CreatedAt: now, UpdatedAt: now,
	})

	router := setupRouter(store, "ten_abc")

	body := `{"rules":[{"type":"rate_limit","params":{"maxRequests":-1,"windowSeconds":60}}]}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/v1/tenants/ten_abc/policies/sp_1", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid rule update, got %d", w.Code)
	}
}

// ============================================================================
// evalTimeWindow tests
// ============================================================================

func TestEvalTimeWindow_WithinHours(t *testing.T) {
	// Monday at 10:00 UTC
	now := time.Date(2026, 2, 16, 10, 0, 0, 0, time.UTC) // Monday
	params, _ := json.Marshal(TimeWindowParams{StartHour: 9, EndHour: 17})
	rule := Rule{Type: "time_window", Params: params}

	if err := evalTimeWindow(rule, now); err != nil {
		t.Errorf("10:00 should be within 9-17: %v", err)
	}
}

func TestEvalTimeWindow_OutsideHours(t *testing.T) {
	// Monday at 8:00 UTC (before 9am)
	now := time.Date(2026, 2, 16, 8, 0, 0, 0, time.UTC)
	params, _ := json.Marshal(TimeWindowParams{StartHour: 9, EndHour: 17})
	rule := Rule{Type: "time_window", Params: params}

	if err := evalTimeWindow(rule, now); err == nil {
		t.Error("8:00 should be outside 9-17")
	}
}

func TestEvalTimeWindow_AtEndHourBoundary(t *testing.T) {
	// Exactly at endHour should be OUTSIDE (endHour is exclusive)
	now := time.Date(2026, 2, 16, 17, 0, 0, 0, time.UTC)
	params, _ := json.Marshal(TimeWindowParams{StartHour: 9, EndHour: 17})
	rule := Rule{Type: "time_window", Params: params}

	if err := evalTimeWindow(rule, now); err == nil {
		t.Error("17:00 exactly should be outside 9-17 (endHour exclusive)")
	}
}

func TestEvalTimeWindow_AtStartHourBoundary(t *testing.T) {
	// Exactly at startHour should be INSIDE
	now := time.Date(2026, 2, 16, 9, 0, 0, 0, time.UTC)
	params, _ := json.Marshal(TimeWindowParams{StartHour: 9, EndHour: 17})
	rule := Rule{Type: "time_window", Params: params}

	if err := evalTimeWindow(rule, now); err != nil {
		t.Errorf("9:00 should be within 9-17: %v", err)
	}
}

func TestEvalTimeWindow_OvernightWindow(t *testing.T) {
	// Overnight window: 22-6 (e.g., night shift)
	params, _ := json.Marshal(TimeWindowParams{StartHour: 22, EndHour: 6})
	rule := Rule{Type: "time_window", Params: params}

	// 23:00 should be allowed
	now := time.Date(2026, 2, 16, 23, 0, 0, 0, time.UTC)
	if err := evalTimeWindow(rule, now); err != nil {
		t.Errorf("23:00 should be within overnight 22-6: %v", err)
	}

	// 3:00 should be allowed
	now = time.Date(2026, 2, 16, 3, 0, 0, 0, time.UTC)
	if err := evalTimeWindow(rule, now); err != nil {
		t.Errorf("3:00 should be within overnight 22-6: %v", err)
	}

	// 12:00 should be denied
	now = time.Date(2026, 2, 16, 12, 0, 0, 0, time.UTC)
	if err := evalTimeWindow(rule, now); err == nil {
		t.Error("12:00 should be outside overnight 22-6")
	}
}

func TestEvalTimeWindow_DayFilter(t *testing.T) {
	params, _ := json.Marshal(TimeWindowParams{
		StartHour: 0,
		EndHour:   23,
		Days:      []string{"monday", "wednesday", "friday"},
	})
	rule := Rule{Type: "time_window", Params: params}

	// Monday at 10:00
	monday := time.Date(2026, 2, 16, 10, 0, 0, 0, time.UTC)
	if err := evalTimeWindow(rule, monday); err != nil {
		t.Errorf("Monday should be allowed: %v", err)
	}

	// Tuesday at 10:00
	tuesday := time.Date(2026, 2, 17, 10, 0, 0, 0, time.UTC)
	if err := evalTimeWindow(rule, tuesday); err == nil {
		t.Error("Tuesday should be denied")
	}
}

func TestEvalTimeWindow_Timezone(t *testing.T) {
	// Policy: 9-17 EST. Now is 14:00 UTC = 9:00 EST (should be allowed)
	params, _ := json.Marshal(TimeWindowParams{
		StartHour: 9,
		EndHour:   17,
		Timezone:  "America/New_York",
	})
	rule := Rule{Type: "time_window", Params: params}

	// 14:00 UTC = 9:00 EST (allowed)
	now := time.Date(2026, 2, 16, 14, 0, 0, 0, time.UTC)
	if err := evalTimeWindow(rule, now); err != nil {
		t.Errorf("14:00 UTC = 9:00 EST should be allowed: %v", err)
	}

	// 13:00 UTC = 8:00 EST (denied)
	now = time.Date(2026, 2, 16, 13, 0, 0, 0, time.UTC)
	if err := evalTimeWindow(rule, now); err == nil {
		t.Error("13:00 UTC = 8:00 EST should be denied")
	}
}

// ============================================================================
// Rate limit multi-window tests
// ============================================================================

func TestEvaluator_RateLimit_MultiWindow(t *testing.T) {
	store := NewMemoryStore()
	params, _ := json.Marshal(RateLimitParams{MaxRequests: 10, WindowSeconds: 60})
	pol := &SpendPolicy{
		ID:        "sp_1",
		TenantID:  "ten_abc",
		Name:      "rate limit",
		Rules:     []Rule{{Type: "rate_limit", Params: params}},
		Enabled:   true,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	_ = store.Create(context.Background(), pol)
	eval := NewEvaluator(store)

	// Session is 150 seconds old = 2 full windows → allowed = 10 * 2 = 20
	session := makeSession("ten_abc", 19, "0.000000", time.Now().Add(-150*time.Second))
	decision, err := eval.EvaluateProxy(context.Background(), session, "translation")
	if err != nil {
		t.Fatalf("19 requests in 2.5 windows (allowed=20): %v", err)
	}
	if !decision.Allowed {
		t.Error("19/20 should be allowed")
	}

	// 20 requests should be denied (>= allowed)
	session = makeSession("ten_abc", 20, "0.000000", time.Now().Add(-150*time.Second))
	_, err = eval.EvaluateProxy(context.Background(), session, "translation")
	if err == nil {
		t.Error("20/20 should be denied (>= limit)")
	}
}

// ============================================================================
// Multi-rule policy tests
// ============================================================================

func TestEvaluator_MultiRule_AllPass(t *testing.T) {
	store := NewMemoryStore()
	maxReq, _ := json.Marshal(MaxRequestsParams{MaxCount: 100})
	allowList, _ := json.Marshal(ServiceListParams{Services: []string{"translation", "inference"}})
	pol := &SpendPolicy{
		ID:       "sp_1",
		TenantID: "ten_abc",
		Name:     "combined",
		Rules: []Rule{
			{Type: "max_requests", Params: maxReq},
			{Type: "service_allowlist", Params: allowList},
		},
		Enabled:   true,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	_ = store.Create(context.Background(), pol)
	eval := NewEvaluator(store)

	session := makeSession("ten_abc", 5, "0.000000", time.Now())
	decision, err := eval.EvaluateProxy(context.Background(), session, "translation")
	if err != nil {
		t.Fatalf("both rules should pass: %v", err)
	}
	if !decision.Allowed {
		t.Error("both rules passed, should be allowed")
	}
}

func TestEvaluator_MultiRule_SecondDenies(t *testing.T) {
	store := NewMemoryStore()
	maxReq, _ := json.Marshal(MaxRequestsParams{MaxCount: 100})
	allowList, _ := json.Marshal(ServiceListParams{Services: []string{"translation"}})
	pol := &SpendPolicy{
		ID:       "sp_1",
		TenantID: "ten_abc",
		Name:     "combined",
		Rules: []Rule{
			{Type: "max_requests", Params: maxReq},
			{Type: "service_allowlist", Params: allowList},
		},
		Enabled:   true,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	_ = store.Create(context.Background(), pol)
	eval := NewEvaluator(store)

	session := makeSession("ten_abc", 5, "0.000000", time.Now())
	decision, _ := eval.EvaluateProxy(context.Background(), session, "compute")
	if decision.Allowed {
		t.Error("compute not in allowlist, should be denied")
	}
	if decision.DeniedRule != "service_allowlist" {
		t.Errorf("DeniedRule = %q, want service_allowlist", decision.DeniedRule)
	}
}
