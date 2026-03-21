package contracts

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gin-gonic/gin"
)

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
