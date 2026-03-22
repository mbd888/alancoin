package contracts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gin-gonic/gin"
)

func newTestService() *Service {
	store := NewMemoryStore()
	return NewService(store).WithLogger(slog.New(slog.NewTextHandler(os.Stderr, nil)))
}

func defaultContractReq() CreateContractRequest {
	return CreateContractRequest{
		Name: "test-contract",
		Invariants: []Condition{
			{
				Type:     CondMaxLatency,
				Severity: SeverityHard,
				Params:   map[string]interface{}{"maxMs": float64(5000)},
			},
			{
				Type:     CondNoPII,
				Severity: SeveritySoft,
				Params:   map[string]interface{}{},
			},
		},
		Recovery: RecoveryAbort,
	}
}

func TestContract_CreateAndGet(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	c, err := svc.Create(ctx, defaultContractReq())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if c.Status != StatusDraft {
		t.Fatalf("expected draft, got %s", c.Status)
	}
	if c.Name != "test-contract" {
		t.Fatalf("expected test-contract, got %s", c.Name)
	}
	if len(c.Invariants) != 2 {
		t.Fatalf("expected 2 invariants, got %d", len(c.Invariants))
	}

	got, err := svc.Get(ctx, c.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != c.ID {
		t.Fatalf("Get returned wrong ID")
	}
}

func TestContract_BindToEscrow(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	c, _ := svc.Create(ctx, defaultContractReq())

	bound, err := svc.BindToEscrow(ctx, c.ID, "coa_abc123")
	if err != nil {
		t.Fatalf("BindToEscrow: %v", err)
	}
	if bound.Status != StatusActive {
		t.Fatalf("expected active, got %s", bound.Status)
	}
	if bound.BoundEscrowID != "coa_abc123" {
		t.Fatalf("expected coa_abc123, got %s", bound.BoundEscrowID)
	}

	// Double bind should fail
	_, err = svc.BindToEscrow(ctx, c.ID, "coa_def456")
	if err == nil {
		t.Fatal("expected error on double bind")
	}
}

func TestContract_CheckInvariant_NoViolation(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	c, _ := svc.Create(ctx, defaultContractReq())
	svc.BindToEscrow(ctx, c.ID, "coa_test")

	// Normal latency, no PII
	result, err := svc.CheckInvariant(ctx, c.ID, CheckInvariantRequest{
		MemberAddr:    "0xAgent1",
		StepIndex:     0,
		LatencyMs:     1500,
		OutputPayload: "This is a normal output with no sensitive data.",
	})
	if err != nil {
		t.Fatalf("CheckInvariant: %v", err)
	}
	if result.HardViolations != 0 {
		t.Fatalf("expected 0 hard violations, got %d", result.HardViolations)
	}
	if result.SoftViolations != 0 {
		t.Fatalf("expected 0 soft violations, got %d", result.SoftViolations)
	}
}

func TestContract_CheckInvariant_HardViolation_Latency(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	c, _ := svc.Create(ctx, defaultContractReq())
	svc.BindToEscrow(ctx, c.ID, "coa_test")

	// Latency exceeds 5000ms → hard violation
	result, err := svc.CheckInvariant(ctx, c.ID, CheckInvariantRequest{
		MemberAddr: "0xAgent1",
		StepIndex:  0,
		LatencyMs:  7500,
	})
	if !errors.Is(err, ErrContractViolation) {
		t.Fatalf("expected ErrContractViolation, got %v", err)
	}
	if result.Status != StatusViolated {
		t.Fatalf("expected violated, got %s", result.Status)
	}
	if result.HardViolations != 1 {
		t.Fatalf("expected 1 hard violation, got %d", result.HardViolations)
	}
	if len(result.Violations) != 1 {
		t.Fatalf("expected 1 violation record, got %d", len(result.Violations))
	}
	if result.Violations[0].ConditionType != CondMaxLatency {
		t.Fatalf("expected max_latency violation, got %s", result.Violations[0].ConditionType)
	}
}

func TestContract_CheckInvariant_SoftViolation_PII(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	c, _ := svc.Create(ctx, defaultContractReq())
	svc.BindToEscrow(ctx, c.ID, "coa_test")

	// Output contains PII → soft violation
	result, err := svc.CheckInvariant(ctx, c.ID, CheckInvariantRequest{
		MemberAddr:    "0xAgent1",
		StepIndex:     0,
		LatencyMs:     100,
		OutputPayload: "The user's SSN is 123-45-6789",
	})
	if err != nil {
		t.Fatalf("CheckInvariant: %v (soft violation should not error)", err)
	}
	if result.SoftViolations != 1 {
		t.Fatalf("expected 1 soft violation, got %d", result.SoftViolations)
	}
	if result.QualityPenalty != 0.05 {
		t.Fatalf("expected penalty 0.05, got %f", result.QualityPenalty)
	}
}

func TestContract_QualityPenaltyCumulative(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	c, _ := svc.Create(ctx, defaultContractReq())
	svc.BindToEscrow(ctx, c.ID, "coa_test")

	// 3 soft violations → 0.15 penalty
	for i := 0; i < 3; i++ {
		_, err := svc.CheckInvariant(ctx, c.ID, CheckInvariantRequest{
			MemberAddr:    "0xAgent1",
			StepIndex:     i,
			LatencyMs:     100,
			OutputPayload: "Contains credit card number",
		})
		if err != nil {
			t.Fatalf("CheckInvariant %d: %v", i, err)
		}
	}

	result, _ := svc.Get(ctx, c.ID)
	if result.SoftViolations != 3 {
		t.Fatalf("expected 3 soft violations, got %d", result.SoftViolations)
	}
	if result.QualityPenalty < 0.14 || result.QualityPenalty > 0.16 {
		t.Fatalf("expected penalty ~0.15, got %f", result.QualityPenalty)
	}
}

func TestContract_EffectiveQualityScore(t *testing.T) {
	c := &Contract{QualityPenalty: 0.15}

	tests := []struct {
		raw  float64
		want float64
	}{
		{1.0, 0.85},
		{0.5, 0.35},
		{0.1, 0.0}, // clamped at 0
		{0.0, 0.0},
	}
	for _, tt := range tests {
		got := c.EffectiveQualityScore(tt.raw)
		if got < tt.want-0.001 || got > tt.want+0.001 {
			t.Errorf("raw=%.2f: got %.4f, want %.4f", tt.raw, got, tt.want)
		}
	}
}

func TestContract_MarkPassed(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	c, _ := svc.Create(ctx, defaultContractReq())
	svc.BindToEscrow(ctx, c.ID, "coa_test")

	result, err := svc.MarkPassed(ctx, c.ID)
	if err != nil {
		t.Fatalf("MarkPassed: %v", err)
	}
	if result.Status != StatusPassed {
		t.Fatalf("expected passed, got %s", result.Status)
	}
}

func TestContract_MarkPassed_NotActive(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	c, _ := svc.Create(ctx, defaultContractReq())

	// Contract is still draft → should fail
	_, err := svc.MarkPassed(ctx, c.ID)
	if err == nil {
		t.Fatal("expected error when marking draft as passed")
	}
}

func TestContract_CheckAfterViolated(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	c, _ := svc.Create(ctx, defaultContractReq())
	svc.BindToEscrow(ctx, c.ID, "coa_test")

	// Cause hard violation
	svc.CheckInvariant(ctx, c.ID, CheckInvariantRequest{
		MemberAddr: "0xAgent1",
		LatencyMs:  99999,
	})

	// Further checks should fail (contract is violated)
	_, err := svc.CheckInvariant(ctx, c.ID, CheckInvariantRequest{
		MemberAddr: "0xAgent1",
		LatencyMs:  100,
	})
	if err == nil {
		t.Fatal("expected error when checking violated contract")
	}
}

func TestContract_GetByEscrow(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	c, _ := svc.Create(ctx, defaultContractReq())
	svc.BindToEscrow(ctx, c.ID, "coa_target")

	found, err := svc.GetByEscrow(ctx, "coa_target")
	if err != nil {
		t.Fatalf("GetByEscrow: %v", err)
	}
	if found.ID != c.ID {
		t.Fatalf("wrong contract returned")
	}

	// Not found
	_, err = svc.GetByEscrow(ctx, "coa_nonexistent")
	if !errors.Is(err, ErrContractNotFound) {
		t.Fatalf("expected ErrContractNotFound, got %v", err)
	}
}

func TestContract_NotFound(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	_, err := svc.Get(ctx, "nonexistent")
	if !errors.Is(err, ErrContractNotFound) {
		t.Fatalf("expected ErrContractNotFound, got %v", err)
	}
}

func TestContract_AuditTrail(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	c, _ := svc.Create(ctx, defaultContractReq())
	svc.BindToEscrow(ctx, c.ID, "coa_audit_test")

	// Generate a soft violation
	svc.CheckInvariant(ctx, c.ID, CheckInvariantRequest{
		MemberAddr:    "0xAgent1",
		LatencyMs:     100,
		OutputPayload: "Contains passport number",
	})

	trail, err := svc.GetAuditTrail(ctx, c.ID)
	if err != nil {
		t.Fatalf("GetAuditTrail: %v", err)
	}
	if trail.ContractID != c.ID {
		t.Fatalf("wrong contract ID in audit trail")
	}
	if trail.EscrowID != "coa_audit_test" {
		t.Fatalf("wrong escrow ID in audit trail")
	}
	if trail.SoftViolations != 1 {
		t.Fatalf("expected 1 soft violation in trail, got %d", trail.SoftViolations)
	}
	if len(trail.Violations) != 1 {
		t.Fatalf("expected 1 violation record in trail, got %d", len(trail.Violations))
	}
	if len(trail.Invariants) != 2 {
		t.Fatalf("expected 2 invariants in trail, got %d", len(trail.Invariants))
	}
}

func TestContract_ValidationErrors(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	// No conditions
	_, err := svc.Create(ctx, CreateContractRequest{
		Name:     "empty",
		Recovery: RecoveryAbort,
	})
	if !errors.Is(err, ErrNoConditions) {
		t.Fatalf("expected ErrNoConditions, got %v", err)
	}

	// Invalid recovery
	_, err = svc.Create(ctx, CreateContractRequest{
		Name:       "bad-recovery",
		Invariants: []Condition{{Type: CondNoPII, Severity: SeveritySoft}},
		Recovery:   "invalid",
	})
	if err == nil {
		t.Fatal("expected error for invalid recovery")
	}

	// Invalid condition type
	_, err = svc.Create(ctx, CreateContractRequest{
		Name:       "bad-type",
		Invariants: []Condition{{Type: "unknown", Severity: SeveritySoft}},
		Recovery:   RecoveryAbort,
	})
	if err == nil {
		t.Fatal("expected error for invalid condition type")
	}

	// Invalid severity
	_, err = svc.Create(ctx, CreateContractRequest{
		Name:       "bad-severity",
		Invariants: []Condition{{Type: CondNoPII, Severity: "unknown"}},
		Recovery:   RecoveryAbort,
	})
	if err == nil {
		t.Fatal("expected error for invalid severity")
	}
}

func TestContract_MaxLatency_BelowThreshold(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	c, _ := svc.Create(ctx, CreateContractRequest{
		Name: "latency-test",
		Invariants: []Condition{
			{Type: CondMaxLatency, Severity: SeverityHard, Params: map[string]interface{}{"maxMs": float64(1000)}},
		},
		Recovery: RecoveryAbort,
	})
	svc.BindToEscrow(ctx, c.ID, "coa_lat")

	// Exactly at threshold: 1000ms <= 1000ms → no violation
	result, err := svc.CheckInvariant(ctx, c.ID, CheckInvariantRequest{
		MemberAddr: "0xAgent1",
		LatencyMs:  1000,
	})
	if err != nil {
		t.Fatalf("should not violate at threshold: %v", err)
	}
	if result.HardViolations != 0 {
		t.Fatalf("should have 0 violations at threshold")
	}
}

func TestContract_NoPII_CreditCard(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	c, _ := svc.Create(ctx, CreateContractRequest{
		Name: "pii-test",
		Invariants: []Condition{
			{Type: CondNoPII, Severity: SeverityHard, Params: map[string]interface{}{}},
		},
		Recovery: RecoveryAbort,
	})
	svc.BindToEscrow(ctx, c.ID, "coa_pii")

	_, err := svc.CheckInvariant(ctx, c.ID, CheckInvariantRequest{
		MemberAddr:    "0xAgent1",
		OutputPayload: "Please charge this credit card: 4111-1111-1111-1111",
	})
	if !errors.Is(err, ErrContractViolation) {
		t.Fatalf("expected hard violation for credit card PII, got %v", err)
	}
}

func TestContract_QualityPenaltyCap(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	c, _ := svc.Create(ctx, CreateContractRequest{
		Name: "cap-test",
		Invariants: []Condition{
			{Type: CondNoPII, Severity: SeveritySoft, Params: map[string]interface{}{}},
		},
		Recovery: RecoveryAlert,
	})
	svc.BindToEscrow(ctx, c.ID, "coa_cap")

	// 25 soft violations → penalty should be capped at 1.0
	for i := 0; i < 25; i++ {
		svc.CheckInvariant(ctx, c.ID, CheckInvariantRequest{
			MemberAddr:    "0xAgent1",
			StepIndex:     i,
			OutputPayload: "Contains SSN data",
		})
	}

	result, _ := svc.Get(ctx, c.ID)
	if result.QualityPenalty != 1.0 {
		t.Fatalf("expected penalty capped at 1.0, got %f", result.QualityPenalty)
	}
	if result.SoftViolations != 25 {
		t.Fatalf("expected 25 soft violations, got %d", result.SoftViolations)
	}
}

func TestContract_CostComparison_Numeric(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	c, _ := svc.Create(ctx, CreateContractRequest{
		Name: "cost-test",
		Invariants: []Condition{
			{
				Type:     CondMaxStepCost,
				Severity: SeverityHard,
				Params:   map[string]interface{}{"maxCost": "1.000000"},
			},
		},
		Recovery: RecoveryAbort,
	})
	svc.BindToEscrow(ctx, c.ID, "coa_cost")

	// 0.999999 < 1.000000 — should NOT violate
	result, err := svc.CheckInvariant(ctx, c.ID, CheckInvariantRequest{
		MemberAddr: "0xAgent1",
		StepCost:   "0.999999",
	})
	if err != nil {
		t.Fatalf("0.999999 should not exceed 1.000000: %v", err)
	}
	if result.HardViolations != 0 {
		t.Fatal("should not violate at 0.999999")
	}

	// 1.500000 > 1.000000 — should violate
	_, err = svc.CheckInvariant(ctx, c.ID, CheckInvariantRequest{
		MemberAddr: "0xAgent1",
		StepCost:   "1.500000",
	})
	if !errors.Is(err, ErrContractViolation) {
		t.Fatalf("1.500000 should exceed 1.000000: %v", err)
	}
}

func TestContract_TotalCostComparison(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	c, _ := svc.Create(ctx, CreateContractRequest{
		Name: "total-cost-test",
		Invariants: []Condition{
			{
				Type:     CondMaxTotalCost,
				Severity: SeveritySoft,
				Params:   map[string]interface{}{"maxCost": "10.000000"},
			},
		},
		Recovery: RecoveryDegrade,
	})
	svc.BindToEscrow(ctx, c.ID, "coa_tc")

	// Below threshold
	result, err := svc.CheckInvariant(ctx, c.ID, CheckInvariantRequest{
		MemberAddr: "0xAgent1",
		TotalCost:  "9.500000",
	})
	if err != nil {
		t.Fatalf("should not violate: %v", err)
	}
	if result.SoftViolations != 0 {
		t.Fatal("should have 0 violations")
	}

	// Above threshold
	result, err = svc.CheckInvariant(ctx, c.ID, CheckInvariantRequest{
		MemberAddr: "0xAgent1",
		TotalCost:  "10.500000",
	})
	if err != nil {
		t.Fatalf("soft violation should not error: %v", err)
	}
	if result.SoftViolations != 1 {
		t.Fatalf("expected 1 soft violation, got %d", result.SoftViolations)
	}
}

func TestContract_ViolationsCapped(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	c, _ := svc.Create(ctx, CreateContractRequest{
		Name: "cap-test",
		Invariants: []Condition{
			{Type: CondNoPII, Severity: SeveritySoft, Params: map[string]interface{}{}},
		},
		Recovery: RecoveryAlert,
	})
	svc.BindToEscrow(ctx, c.ID, "coa_viol_cap")

	// Generate 1050 violations — should be capped at 1000
	for i := 0; i < 1050; i++ {
		svc.CheckInvariant(ctx, c.ID, CheckInvariantRequest{
			MemberAddr:    "0xAgent1",
			StepIndex:     i,
			OutputPayload: "Contains SSN info",
		})
	}

	result, _ := svc.Get(ctx, c.ID)
	if len(result.Violations) > 1000 {
		t.Fatalf("expected violations capped at 1000, got %d", len(result.Violations))
	}
	// Counter still tracks all 1050
	if result.SoftViolations != 1050 {
		t.Fatalf("expected 1050 soft violations counted, got %d", result.SoftViolations)
	}
}

func TestContract_DeepCopy_MutationSafety(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	c := &Contract{
		ID:   "abc_test",
		Name: "test",
		Invariants: []Condition{
			{Type: CondNoPII, Severity: SeveritySoft, Params: map[string]interface{}{"key": "value"}},
		},
		Status: StatusDraft,
	}
	store.Create(ctx, c)

	// Mutate the original
	c.Invariants = append(c.Invariants, Condition{Type: CondMaxLatency, Severity: SeverityHard})

	got, _ := store.Get(ctx, "abc_test")
	if len(got.Invariants) != 1 {
		t.Fatalf("deep copy broken: stored invariants mutated, got %d", len(got.Invariants))
	}
}

// --- merged from coverage_extra_test.go ---

func init() {
	gin.SetMode(gin.TestMode)
}

// ---------------------------------------------------------------------------
// Handler coverage
// ---------------------------------------------------------------------------

func newTestHandler() (*Handler, *Service) {
	store := NewMemoryStore()
	svc := NewService(store).WithLogger(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	h := NewHandler(svc)
	return h, svc
}

func setupContractRouter(h *Handler) *gin.Engine {
	r := gin.New()
	group := r.Group("/v1")
	h.RegisterRoutes(group)
	h.RegisterProtectedRoutes(group)
	return r
}

func doContractReq(r *gin.Engine, method, path string, body interface{}) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w
}

// ---------------------------------------------------------------------------
// POST /v1/contracts (CreateContract)
// ---------------------------------------------------------------------------

func TestHandler_CreateContract_Success(t *testing.T) {
	h, _ := newTestHandler()
	r := setupContractRouter(h)

	w := doContractReq(r, "POST", "/v1/contracts", defaultContractReq())
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_CreateContract_InvalidBody(t *testing.T) {
	h, _ := newTestHandler()
	r := setupContractRouter(h)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/contracts", bytes.NewReader([]byte(`{invalid`)))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandler_CreateContract_NoConditions(t *testing.T) {
	h, _ := newTestHandler()
	r := setupContractRouter(h)

	body := CreateContractRequest{
		Name:     "empty",
		Recovery: RecoveryAbort,
	}
	w := doContractReq(r, "POST", "/v1/contracts", body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for no conditions, got %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// GET /v1/contracts/:id (GetContract)
// ---------------------------------------------------------------------------

func TestHandler_GetContract_Found(t *testing.T) {
	h, svc := newTestHandler()
	r := setupContractRouter(h)

	c, _ := svc.Create(context.Background(), defaultContractReq())

	w := doContractReq(r, "GET", "/v1/contracts/"+c.ID, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHandler_GetContract_NotFound(t *testing.T) {
	h, _ := newTestHandler()
	r := setupContractRouter(h)

	w := doContractReq(r, "GET", "/v1/contracts/nonexistent", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// POST /v1/contracts/:id/bind (BindToEscrow)
// ---------------------------------------------------------------------------

func TestHandler_BindToEscrow_Success(t *testing.T) {
	h, svc := newTestHandler()
	r := setupContractRouter(h)

	c, _ := svc.Create(context.Background(), defaultContractReq())

	w := doContractReq(r, "POST", "/v1/contracts/"+c.ID+"/bind", BindRequest{EscrowID: "coa_test"})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_BindToEscrow_InvalidBody(t *testing.T) {
	h, svc := newTestHandler()
	r := setupContractRouter(h)

	c, _ := svc.Create(context.Background(), defaultContractReq())

	ww := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/contracts/"+c.ID+"/bind", bytes.NewReader([]byte(`{invalid`)))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(ww, req)

	if ww.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", ww.Code)
	}
}

func TestHandler_BindToEscrow_NotFound(t *testing.T) {
	h, _ := newTestHandler()
	r := setupContractRouter(h)

	w := doContractReq(r, "POST", "/v1/contracts/nonexistent/bind", BindRequest{EscrowID: "coa_test"})
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHandler_BindToEscrow_AlreadyBound(t *testing.T) {
	h, svc := newTestHandler()
	r := setupContractRouter(h)

	c, _ := svc.Create(context.Background(), defaultContractReq())
	svc.BindToEscrow(context.Background(), c.ID, "coa_first")

	w := doContractReq(r, "POST", "/v1/contracts/"+c.ID+"/bind", BindRequest{EscrowID: "coa_second"})
	// Should fail since it is already bound (status is active, not draft)
	if w.Code == http.StatusOK {
		t.Fatal("expected error for double bind")
	}
}

// ---------------------------------------------------------------------------
// POST /v1/contracts/:id/check (CheckInvariant)
// ---------------------------------------------------------------------------

func TestHandler_CheckInvariant_NoViolation(t *testing.T) {
	h, svc := newTestHandler()
	r := setupContractRouter(h)

	c, _ := svc.Create(context.Background(), defaultContractReq())
	svc.BindToEscrow(context.Background(), c.ID, "coa_check")

	body := CheckInvariantRequest{
		MemberAddr: "0xAgent1",
		LatencyMs:  100,
	}
	w := doContractReq(r, "POST", "/v1/contracts/"+c.ID+"/check", body)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["violated"].(bool) {
		t.Error("expected violated=false")
	}
}

func TestHandler_CheckInvariant_HardViolation(t *testing.T) {
	h, svc := newTestHandler()
	r := setupContractRouter(h)

	c, _ := svc.Create(context.Background(), defaultContractReq())
	svc.BindToEscrow(context.Background(), c.ID, "coa_check")

	body := CheckInvariantRequest{
		MemberAddr: "0xAgent1",
		LatencyMs:  99999, // exceeds max 5000ms
	}
	w := doContractReq(r, "POST", "/v1/contracts/"+c.ID+"/check", body)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 (violation is not HTTP error), got %d", w.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if !resp["violated"].(bool) {
		t.Error("expected violated=true")
	}
	if resp["action"].(string) != string(RecoveryAbort) {
		t.Errorf("action = %v, want abort", resp["action"])
	}
}

func TestHandler_CheckInvariant_InvalidBody(t *testing.T) {
	h, svc := newTestHandler()
	r := setupContractRouter(h)

	c, _ := svc.Create(context.Background(), defaultContractReq())
	svc.BindToEscrow(context.Background(), c.ID, "coa_check")

	ww := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/contracts/"+c.ID+"/check", bytes.NewReader([]byte(`{invalid`)))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(ww, req)

	if ww.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", ww.Code)
	}
}

func TestHandler_CheckInvariant_NotFound(t *testing.T) {
	h, _ := newTestHandler()
	r := setupContractRouter(h)

	body := CheckInvariantRequest{MemberAddr: "0xAgent1", LatencyMs: 100}
	w := doContractReq(r, "POST", "/v1/contracts/nonexistent/check", body)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHandler_CheckInvariant_NotActive(t *testing.T) {
	h, svc := newTestHandler()
	r := setupContractRouter(h)

	c, _ := svc.Create(context.Background(), defaultContractReq())
	// Contract is draft (not bound/active)

	body := CheckInvariantRequest{MemberAddr: "0xAgent1", LatencyMs: 100}
	w := doContractReq(r, "POST", "/v1/contracts/"+c.ID+"/check", body)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 (contract not active), got %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// POST /v1/contracts/:id/pass (MarkPassed)
// ---------------------------------------------------------------------------

func TestHandler_MarkPassed_Success(t *testing.T) {
	h, svc := newTestHandler()
	r := setupContractRouter(h)

	c, _ := svc.Create(context.Background(), defaultContractReq())
	svc.BindToEscrow(context.Background(), c.ID, "coa_pass")

	w := doContractReq(r, "POST", "/v1/contracts/"+c.ID+"/pass", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_MarkPassed_NotFound(t *testing.T) {
	h, _ := newTestHandler()
	r := setupContractRouter(h)

	w := doContractReq(r, "POST", "/v1/contracts/nonexistent/pass", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHandler_MarkPassed_WrongState(t *testing.T) {
	h, svc := newTestHandler()
	r := setupContractRouter(h)

	c, _ := svc.Create(context.Background(), defaultContractReq())
	// Draft state, not active

	w := doContractReq(r, "POST", "/v1/contracts/"+c.ID+"/pass", nil)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// GET /v1/contracts/:id/audit-trail (GetAuditTrail)
// ---------------------------------------------------------------------------

func TestHandler_GetAuditTrail_Found(t *testing.T) {
	h, svc := newTestHandler()
	r := setupContractRouter(h)

	c, _ := svc.Create(context.Background(), defaultContractReq())

	w := doContractReq(r, "GET", "/v1/contracts/"+c.ID+"/audit-trail", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHandler_GetAuditTrail_NotFound(t *testing.T) {
	h, _ := newTestHandler()
	r := setupContractRouter(h)

	w := doContractReq(r, "GET", "/v1/contracts/nonexistent/audit-trail", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// MemoryStore coverage
// ---------------------------------------------------------------------------

func TestMemoryStore_Update_NotFound(t *testing.T) {
	store := NewMemoryStore()
	err := store.Update(context.Background(), &Contract{ID: "nonexistent"})
	if err != ErrContractNotFound {
		t.Fatalf("expected ErrContractNotFound, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Contract methods
// ---------------------------------------------------------------------------

func TestContract_IsTerminal(t *testing.T) {
	tests := []struct {
		status   ContractStatus
		terminal bool
	}{
		{StatusDraft, false},
		{StatusActive, false},
		{StatusPassed, true},
		{StatusViolated, true},
		{StatusExpired, true},
	}
	for _, tt := range tests {
		c := &Contract{Status: tt.status}
		if c.IsTerminal() != tt.terminal {
			t.Errorf("IsTerminal(%s) = %v, want %v", tt.status, c.IsTerminal(), tt.terminal)
		}
	}
}

func TestContract_EffectiveQualityScore_NoNegative(t *testing.T) {
	c := &Contract{QualityPenalty: 2.0} // penalty > 1
	score := c.EffectiveQualityScore(0.5)
	if score != 0 {
		t.Errorf("expected 0 (clamped), got %f", score)
	}
}

// ---------------------------------------------------------------------------
// condParamFloat / condParamString coverage
// ---------------------------------------------------------------------------

func TestCondParamFloat_Types(t *testing.T) {
	c := Condition{Params: map[string]interface{}{
		"f64":     float64(42.5),
		"int":     int(10),
		"int64":   int64(20),
		"str":     "notanumber",
		"missing": nil,
	}}

	if v, ok := condParamFloat(c, "f64"); !ok || v != 42.5 {
		t.Errorf("f64: got %f, ok=%v", v, ok)
	}
	if v, ok := condParamFloat(c, "int"); !ok || v != 10 {
		t.Errorf("int: got %f, ok=%v", v, ok)
	}
	if v, ok := condParamFloat(c, "int64"); !ok || v != 20 {
		t.Errorf("int64: got %f, ok=%v", v, ok)
	}
	if _, ok := condParamFloat(c, "str"); ok {
		t.Error("str should not be parseable as float")
	}
	if _, ok := condParamFloat(c, "nonexistent"); ok {
		t.Error("nonexistent key should return false")
	}
}

func TestCondParamString_Types(t *testing.T) {
	c := Condition{Params: map[string]interface{}{
		"s":   "hello",
		"num": 42,
	}}

	if v, ok := condParamString(c, "s"); !ok || v != "hello" {
		t.Errorf("s: got %q, ok=%v", v, ok)
	}
	if _, ok := condParamString(c, "num"); ok {
		t.Error("num should not be parseable as string")
	}
}

// ---------------------------------------------------------------------------
// compareCost coverage
// ---------------------------------------------------------------------------

func TestCompareCost(t *testing.T) {
	if compareCost("1.5", "2.0") >= 0 {
		t.Error("1.5 < 2.0")
	}
	if compareCost("2.0", "1.5") <= 0 {
		t.Error("2.0 > 1.5")
	}
	if compareCost("1.0", "1.0") != 0 {
		t.Error("1.0 == 1.0")
	}
	// Non-parseable strings fall back to string comparison
	if compareCost("abc", "def") >= 0 {
		t.Error("abc < def in string comparison")
	}
}

// ---------------------------------------------------------------------------
// evaluateCondition: CondOutputSchema
// ---------------------------------------------------------------------------

func TestEvaluateCondition_OutputSchema(t *testing.T) {
	cond := Condition{
		Type:     CondOutputSchema,
		Severity: SeveritySoft,
		Params:   map[string]interface{}{"required": float64(1)},
	}
	violated, _ := evaluateCondition(cond, CheckInvariantRequest{OutputPayload: ""})
	if !violated {
		t.Error("expected violation when required output is empty")
	}

	violated, _ = evaluateCondition(cond, CheckInvariantRequest{OutputPayload: "some output"})
	if violated {
		t.Error("should not violate when output is present")
	}
}

// ---------------------------------------------------------------------------
// evaluateCondition: CondRateLimit, CondAllowedEndpoints, CondCustom
// ---------------------------------------------------------------------------

func TestEvaluateCondition_DeclarativeOnly(t *testing.T) {
	// These condition types are declarative only (always pass)
	for _, ct := range []ConditionType{CondRateLimit, CondAllowedEndpoints, CondCustom} {
		cond := Condition{Type: ct, Severity: SeveritySoft}
		violated, _ := evaluateCondition(cond, CheckInvariantRequest{})
		if violated {
			t.Errorf("%s should not violate (declarative only)", ct)
		}
	}
}

// ---------------------------------------------------------------------------
// evaluateCondition: missing params
// ---------------------------------------------------------------------------

func TestEvaluateCondition_MissingParams(t *testing.T) {
	// Missing maxMs param
	cond := Condition{Type: CondMaxLatency, Severity: SeverityHard, Params: map[string]interface{}{}}
	violated, _ := evaluateCondition(cond, CheckInvariantRequest{LatencyMs: 99999})
	if violated {
		t.Error("should not violate when maxMs param is missing")
	}

	// Missing maxCost param for step cost
	cond = Condition{Type: CondMaxStepCost, Severity: SeverityHard, Params: map[string]interface{}{}}
	violated, _ = evaluateCondition(cond, CheckInvariantRequest{StepCost: "100.0"})
	if violated {
		t.Error("should not violate when maxCost param is missing")
	}

	// Missing maxCost param for total cost
	cond = Condition{Type: CondMaxTotalCost, Severity: SeverityHard, Params: map[string]interface{}{}}
	violated, _ = evaluateCondition(cond, CheckInvariantRequest{TotalCost: "100.0"})
	if violated {
		t.Error("should not violate when maxCost param is missing")
	}
}

// ---------------------------------------------------------------------------
// Service.WithLogger
// ---------------------------------------------------------------------------

func TestService_WithLogger(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store).WithLogger(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if svc.logger == nil {
		t.Error("expected non-nil logger")
	}
}

// ---------------------------------------------------------------------------
// Preconditions in Create
// ---------------------------------------------------------------------------

func TestContract_CreateWithPreconditions(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	c, err := svc.Create(ctx, CreateContractRequest{
		Name: "pre-test",
		Preconditions: []Condition{
			{Type: CondMaxLatency, Severity: SeverityHard, Params: map[string]interface{}{"maxMs": float64(1000)}},
		},
		Invariants: []Condition{
			{Type: CondNoPII, Severity: SeveritySoft, Params: map[string]interface{}{}},
		},
		Recovery: RecoveryAlert,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(c.Preconditions) != 1 {
		t.Errorf("expected 1 precondition, got %d", len(c.Preconditions))
	}
}

// ---------------------------------------------------------------------------
// validateCondition coverage
// ---------------------------------------------------------------------------

func TestValidateCondition_AllTypes(t *testing.T) {
	validTypes := []ConditionType{
		CondMaxLatency, CondMaxTotalCost, CondMaxStepCost, CondRateLimit,
		CondNoPII, CondOutputSchema, CondAllowedEndpoints, CondCustom,
	}
	for _, ct := range validTypes {
		err := validateCondition(Condition{Type: ct, Severity: SeverityHard})
		if err != nil {
			t.Errorf("validateCondition(%s) unexpected error: %v", ct, err)
		}
	}

	// Invalid severity
	err := validateCondition(Condition{Type: CondNoPII, Severity: "unknown"})
	if err == nil {
		t.Error("expected error for unknown severity")
	}
}
