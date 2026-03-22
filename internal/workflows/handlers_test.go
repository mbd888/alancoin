package workflows

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

func newTestHandler() (*Handler, *Service, *mockLedger) {
	ml := newMockLedger()
	store := NewMemoryStore()
	svc := NewService(store, ml).WithLogger(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	h := NewHandler(svc)
	return h, svc, ml
}

func setupWfRouter(h *Handler) *gin.Engine {
	r := gin.New()
	group := r.Group("/v1")
	h.RegisterRoutes(group)
	h.RegisterProtectedRoutes(group)
	return r
}

func doWfReq(r *gin.Engine, method, path string, body interface{}) *httptest.ResponseRecorder {
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

func parseWfBody(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("bad json: %v\nbody: %s", err, w.Body.String())
	}
	return body
}

// ---------------------------------------------------------------------------
// GET /v1/workflows/:id
// ---------------------------------------------------------------------------

func TestHandler_GetWorkflow_Found(t *testing.T) {
	h, svc, _ := newTestHandler()
	r := setupWfRouter(h)
	ctx := context.Background()

	wf, _ := svc.Create(ctx, "0xowner", defaultReq())

	w := doWfReq(r, "GET", "/v1/workflows/"+wf.ID, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_GetWorkflow_NotFound(t *testing.T) {
	h, _, _ := newTestHandler()
	r := setupWfRouter(h)

	w := doWfReq(r, "GET", "/v1/workflows/nonexistent", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// GET /v1/workflows/:id/costs
// ---------------------------------------------------------------------------

func TestHandler_GetCostReport_Found(t *testing.T) {
	h, svc, _ := newTestHandler()
	r := setupWfRouter(h)
	ctx := context.Background()

	wf, _ := svc.Create(ctx, "0xowner", defaultReq())

	w := doWfReq(r, "GET", "/v1/workflows/"+wf.ID+"/costs", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := parseWfBody(t, w)
	if body["costReport"] == nil {
		t.Error("expected costReport in response")
	}
}

func TestHandler_GetCostReport_NotFound(t *testing.T) {
	h, _, _ := newTestHandler()
	r := setupWfRouter(h)

	w := doWfReq(r, "GET", "/v1/workflows/nonexistent/costs", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// GET /v1/workflows/:id/audit
// ---------------------------------------------------------------------------

func TestHandler_GetAuditTrail_Found(t *testing.T) {
	h, svc, _ := newTestHandler()
	r := setupWfRouter(h)
	ctx := context.Background()

	wf, _ := svc.Create(ctx, "0xowner", defaultReq())

	w := doWfReq(r, "GET", "/v1/workflows/"+wf.ID+"/audit", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := parseWfBody(t, w)
	if body["auditTrail"] == nil {
		t.Error("expected auditTrail in response")
	}
}

func TestHandler_GetAuditTrail_NotFound(t *testing.T) {
	h, _, _ := newTestHandler()
	r := setupWfRouter(h)

	w := doWfReq(r, "GET", "/v1/workflows/nonexistent/audit", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// GET /v1/agents/:address/workflows
// ---------------------------------------------------------------------------

func TestHandler_ListWorkflows(t *testing.T) {
	h, svc, _ := newTestHandler()
	r := setupWfRouter(h)
	ctx := context.Background()

	svc.Create(ctx, "0xowner", defaultReq())
	svc.Create(ctx, "0xowner", defaultReq())

	w := doWfReq(r, "GET", "/v1/agents/0xowner/workflows", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := parseWfBody(t, w)
	if body["count"].(float64) != 2 {
		t.Errorf("expected 2, got %v", body["count"])
	}
}

func TestHandler_ListWorkflows_WithLimit(t *testing.T) {
	h, svc, _ := newTestHandler()
	r := setupWfRouter(h)
	ctx := context.Background()

	svc.Create(ctx, "0xowner", defaultReq())
	svc.Create(ctx, "0xowner", defaultReq())
	svc.Create(ctx, "0xowner", defaultReq())

	w := doWfReq(r, "GET", "/v1/agents/0xowner/workflows?limit=2", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := parseWfBody(t, w)
	if body["count"].(float64) != 2 {
		t.Errorf("expected 2 with limit, got %v", body["count"])
	}
}

func TestHandler_ListWorkflows_MaxLimit(t *testing.T) {
	h, _, _ := newTestHandler()
	r := setupWfRouter(h)

	w := doWfReq(r, "GET", "/v1/agents/0xowner/workflows?limit=999", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHandler_ListWorkflows_Empty(t *testing.T) {
	h, _, _ := newTestHandler()
	r := setupWfRouter(h)

	w := doWfReq(r, "GET", "/v1/agents/0xnobody/workflows", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// POST /v1/workflows (CreateWorkflow)
// ---------------------------------------------------------------------------

func TestHandler_CreateWorkflow_InvalidBody(t *testing.T) {
	h, _, _ := newTestHandler()
	r := setupWfRouter(h)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/workflows", bytes.NewReader([]byte(`{invalid`)))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandler_CreateWorkflow_NoSteps(t *testing.T) {
	h, _, _ := newTestHandler()
	r := setupWfRouter(h)

	body := CreateWorkflowRequest{
		Name:        "test",
		BudgetTotal: "10.000000",
		Steps:       nil,
	}
	w := doWfReq(r, "POST", "/v1/workflows", body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty steps, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_CreateWorkflow_InvalidBudget(t *testing.T) {
	h, _, _ := newTestHandler()
	r := setupWfRouter(h)

	body := CreateWorkflowRequest{
		Name:        "test",
		BudgetTotal: "not-a-number",
		Steps:       []StepDefinition{{Name: "s1", AgentAddr: "0xa"}},
	}
	w := doWfReq(r, "POST", "/v1/workflows", body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid budget, got %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// POST /v1/workflows/:id/steps/:step/start (StartStep)
// ---------------------------------------------------------------------------

func authRouter(h *Handler, addr string) *gin.Engine {
	rr := gin.New()
	rr.Use(func(c *gin.Context) {
		c.Set("authAgentAddr", addr)
		c.Next()
	})
	group := rr.Group("/v1")
	h.RegisterProtectedRoutes(group)
	h.RegisterRoutes(group)
	return rr
}

func TestHandler_StartStep_Success(t *testing.T) {
	h, svc, _ := newTestHandler()
	ctx := context.Background()

	wf, _ := svc.Create(ctx, "0xowner", defaultReq())
	rr := authRouter(h, "0xowner")

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/workflows/"+wf.ID+"/steps/intake/start", nil)
	rr.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_StartStep_NotFound(t *testing.T) {
	h, _, _ := newTestHandler()
	rr := authRouter(h, "0xowner")

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/workflows/nonexistent/steps/intake/start", nil)
	rr.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// POST /v1/workflows/:id/steps/:step/complete (CompleteStep)
// ---------------------------------------------------------------------------

func TestHandler_CompleteStep_InvalidBody(t *testing.T) {
	h, svc, _ := newTestHandler()
	ctx := context.Background()
	wf, _ := svc.Create(ctx, "0xowner", defaultReq())
	svc.StartStep(ctx, wf.ID, "intake", "0xowner")

	rr := authRouter(h, "0xowner")

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/workflows/"+wf.ID+"/steps/intake/complete", bytes.NewReader([]byte(`{invalid`)))
	req.Header.Set("Content-Type", "application/json")
	rr.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandler_CompleteStep_Success(t *testing.T) {
	h, svc, _ := newTestHandler()
	ctx := context.Background()
	wf, _ := svc.Create(ctx, "0xowner", defaultReq())
	svc.StartStep(ctx, wf.ID, "intake", "0xowner")

	rr := authRouter(h, "0xowner")

	body, _ := json.Marshal(CompleteStepRequest{ActualCost: "0.500000"})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/workflows/"+wf.ID+"/steps/intake/complete", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// POST /v1/workflows/:id/steps/:step/fail (FailStep)
// ---------------------------------------------------------------------------

func TestHandler_FailStep_Success(t *testing.T) {
	h, svc, _ := newTestHandler()
	ctx := context.Background()
	wf, _ := svc.Create(ctx, "0xowner", defaultReq())
	svc.StartStep(ctx, wf.ID, "intake", "0xowner")

	rr := authRouter(h, "0xowner")

	body, _ := json.Marshal(FailStepRequest{Reason: "test failure"})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/workflows/"+wf.ID+"/steps/intake/fail", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// POST /v1/workflows/:id/abort (AbortWorkflow)
// ---------------------------------------------------------------------------

func TestHandler_AbortWorkflow_Success(t *testing.T) {
	h, svc, _ := newTestHandler()
	ctx := context.Background()
	wf, _ := svc.Create(ctx, "0xowner", defaultReq())

	rr := authRouter(h, "0xowner")

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/workflows/"+wf.ID+"/abort", nil)
	rr.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_AbortWorkflow_Unauthorized(t *testing.T) {
	h, svc, _ := newTestHandler()
	ctx := context.Background()
	wf, _ := svc.Create(ctx, "0xowner", defaultReq())

	rr := authRouter(h, "0xstranger")

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/workflows/"+wf.ID+"/abort", nil)
	rr.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// errStatus / errCode coverage
// ---------------------------------------------------------------------------

func TestHandler_ErrStatus(t *testing.T) {
	h, _, _ := newTestHandler()
	tests := []struct {
		err    error
		status int
	}{
		{ErrWorkflowNotFound, http.StatusNotFound},
		{ErrStepNotFound, http.StatusNotFound},
		{ErrUnauthorized, http.StatusForbidden},
		{ErrWorkflowCompleted, http.StatusConflict},
		{ErrStepAlreadyStarted, http.StatusConflict},
		{ErrStepAlreadyDone, http.StatusConflict},
		{ErrWorkflowAborted, http.StatusConflict},
		{ErrBudgetExceeded, http.StatusBadRequest},
		{ErrStepBudgetExceeded, http.StatusBadRequest},
		{ErrVelocityBreaker, http.StatusBadRequest},
		{ErrInvalidAmount, http.StatusBadRequest},
		{ErrStepNotStarted, http.StatusBadRequest},
	}
	for _, tt := range tests {
		got := h.errStatus(tt.err)
		if got != tt.status {
			t.Errorf("errStatus(%v) = %d, want %d", tt.err, got, tt.status)
		}
	}
}

func TestHandler_ErrCode(t *testing.T) {
	h, _, _ := newTestHandler()
	tests := []struct {
		err  error
		code string
	}{
		{ErrWorkflowNotFound, "not_found"},
		{ErrUnauthorized, "unauthorized"},
		{ErrBudgetExceeded, "budget_exceeded"},
		{ErrStepBudgetExceeded, "step_budget_exceeded"},
		{ErrVelocityBreaker, "circuit_broken"},
		{ErrWorkflowCompleted, "already_closed"},
		{ErrStepAlreadyStarted, "invalid_state"},
	}
	for _, tt := range tests {
		got := h.errCode(tt.err)
		if got != tt.code {
			t.Errorf("errCode(%v) = %q, want %q", tt.err, got, tt.code)
		}
	}
}
