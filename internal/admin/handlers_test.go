package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// --- Mock implementations ---

type mockGatewayService struct {
	sessions     map[string]GatewaySession
	stuckList    []StuckSession
	closeErr     error
	listStuckErr error
}

func (m *mockGatewayService) GetSession(_ context.Context, id string) (GatewaySession, error) {
	s, ok := m.sessions[id]
	if !ok {
		return GatewaySession{}, errors.New("not found")
	}
	return s, nil
}

func (m *mockGatewayService) CloseSession(_ context.Context, _, _ string) error {
	return m.closeErr
}

func (m *mockGatewayService) ListStuckSessions(_ context.Context, limit int) ([]StuckSession, error) {
	if m.listStuckErr != nil {
		return nil, m.listStuckErr
	}
	if limit > 0 && len(m.stuckList) > limit {
		return m.stuckList[:limit], nil
	}
	return m.stuckList, nil
}

type mockEscrowService struct {
	closed int
	err    error
}

func (m *mockEscrowService) ForceCloseExpired(_ context.Context) (int, error) {
	return m.closed, m.err
}

type mockStreamService struct {
	closed int
	err    error
}

func (m *mockStreamService) ForceCloseStale(_ context.Context) (int, error) {
	return m.closed, m.err
}

type mockReconciler struct {
	report *ReconciliationReport
	err    error
}

func (m *mockReconciler) RunAll(_ context.Context) (*ReconciliationReport, error) {
	return m.report, m.err
}

type mockDenialExporter struct {
	records []DenialExportRecord
	err     error
}

func (m *mockDenialExporter) ListDenials(_ context.Context, _ time.Time, _ int) ([]DenialExportRecord, error) {
	return m.records, m.err
}

type mockStateProvider struct {
	state map[string]interface{}
}

func (m *mockStateProvider) AdminState(_ context.Context) map[string]interface{} {
	return m.state
}

// --- Helpers ---

func setupRouter(h *Handler) *gin.Engine {
	r := gin.New()
	group := r.Group("")
	h.RegisterRoutes(group)
	return r
}

func doRequest(r *gin.Engine, method, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(method, path, nil)
	r.ServeHTTP(w, req)
	return w
}

func parseBody(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to parse JSON body: %v", err)
	}
	return body
}

// --- Tests ---

func TestListStuck(t *testing.T) {
	gw := &mockGatewayService{
		stuckList: []StuckSession{
			{ID: "sess_1", AgentAddr: "0xaaa", Status: "settlement_failed"},
			{ID: "sess_2", AgentAddr: "0xbbb", Status: "settlement_failed"},
		},
	}
	h := NewHandler().WithGatewayService(gw)
	r := setupRouter(h)

	w := doRequest(r, "GET", "/admin/gateway/stuck")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := parseBody(t, w)
	if count := body["count"].(float64); count != 2 {
		t.Errorf("expected 2 stuck sessions, got %v", count)
	}
}

func TestListStuck_ServiceUnavailable(t *testing.T) {
	h := NewHandler() // no gateway service
	r := setupRouter(h)

	w := doRequest(r, "GET", "/admin/gateway/stuck")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestListStuck_WithLimit(t *testing.T) {
	stuckSessions := make([]StuckSession, 5)
	for i := range stuckSessions {
		stuckSessions[i] = StuckSession{ID: "sess", Status: "settlement_failed"}
	}
	gw := &mockGatewayService{stuckList: stuckSessions}
	h := NewHandler().WithGatewayService(gw)
	r := setupRouter(h)

	w := doRequest(r, "GET", "/admin/gateway/stuck?limit=3")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := parseBody(t, w)
	if count := body["count"].(float64); count != 3 {
		t.Errorf("expected 3, got %v", count)
	}
}

func TestResolveSession(t *testing.T) {
	gw := &mockGatewayService{
		sessions: map[string]GatewaySession{
			"sess_1": {ID: "sess_1", AgentAddr: "0xaaa", Status: "settlement_failed"},
		},
	}
	h := NewHandler().WithGatewayService(gw)
	r := setupRouter(h)

	w := doRequest(r, "POST", "/admin/gateway/sessions/sess_1/resolve")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := parseBody(t, w)
	if body["resolved"] != true {
		t.Errorf("expected resolved=true")
	}
}

func TestResolveSession_WrongStatus(t *testing.T) {
	gw := &mockGatewayService{
		sessions: map[string]GatewaySession{
			"sess_1": {ID: "sess_1", AgentAddr: "0xaaa", Status: "active"},
		},
	}
	h := NewHandler().WithGatewayService(gw)
	r := setupRouter(h)

	w := doRequest(r, "POST", "/admin/gateway/sessions/sess_1/resolve")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestResolveSession_NotFound(t *testing.T) {
	gw := &mockGatewayService{sessions: map[string]GatewaySession{}}
	h := NewHandler().WithGatewayService(gw)
	r := setupRouter(h)

	w := doRequest(r, "POST", "/admin/gateway/sessions/nonexistent/resolve")
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestRetrySettlement(t *testing.T) {
	gw := &mockGatewayService{
		sessions: map[string]GatewaySession{
			"sess_1": {ID: "sess_1", AgentAddr: "0xaaa", Status: "settlement_failed"},
		},
	}
	h := NewHandler().WithGatewayService(gw)
	r := setupRouter(h)

	w := doRequest(r, "POST", "/admin/gateway/sessions/sess_1/retry-settlement")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := parseBody(t, w)
	if body["retried"] != true {
		t.Errorf("expected retried=true")
	}
}

func TestForceCloseExpiredEscrows(t *testing.T) {
	esc := &mockEscrowService{closed: 5}
	h := NewHandler().WithEscrowService(esc)
	r := setupRouter(h)

	w := doRequest(r, "POST", "/admin/escrow/force-close-expired")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := parseBody(t, w)
	if body["closedCount"].(float64) != 5 {
		t.Errorf("expected closedCount=5, got %v", body["closedCount"])
	}
}

func TestForceCloseExpiredEscrows_ServiceUnavailable(t *testing.T) {
	h := NewHandler()
	r := setupRouter(h)

	w := doRequest(r, "POST", "/admin/escrow/force-close-expired")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestForceCloseStaleStreams(t *testing.T) {
	streams := &mockStreamService{closed: 3}
	h := NewHandler().WithStreamService(streams)
	r := setupRouter(h)

	w := doRequest(r, "POST", "/admin/streams/force-close-stale")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := parseBody(t, w)
	if body["closedCount"].(float64) != 3 {
		t.Errorf("expected closedCount=3, got %v", body["closedCount"])
	}
}

func TestTriggerReconciliation(t *testing.T) {
	rec := &mockReconciler{
		report: &ReconciliationReport{
			Healthy:   true,
			Timestamp: time.Now(),
		},
	}
	h := NewHandler().WithReconciler(rec)
	r := setupRouter(h)

	w := doRequest(r, "POST", "/admin/reconcile")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestTriggerReconciliation_Error(t *testing.T) {
	rec := &mockReconciler{err: errors.New("db down")}
	h := NewHandler().WithReconciler(rec)
	r := setupRouter(h)

	w := doRequest(r, "POST", "/admin/reconcile")
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

func TestExportDenials(t *testing.T) {
	exp := &mockDenialExporter{
		records: []DenialExportRecord{
			{ID: 1, AgentAddr: "0xaaa", RuleName: "rate_limit", Reason: "too fast"},
			{ID: 2, AgentAddr: "0xbbb", RuleName: "max_amount", Reason: "over limit"},
		},
	}
	h := NewHandler().WithDenialExporter(exp)
	r := setupRouter(h)

	w := doRequest(r, "GET", "/admin/denials/export")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := parseBody(t, w)
	if body["count"].(float64) != 2 {
		t.Errorf("expected count=2, got %v", body["count"])
	}
}

func TestExportDenials_ServiceUnavailable(t *testing.T) {
	h := NewHandler()
	r := setupRouter(h)

	w := doRequest(r, "GET", "/admin/denials/export")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestInspectState(t *testing.T) {
	h := NewHandler().
		WithStateProvider("ledger", &mockStateProvider{
			state: map[string]interface{}{"totalHolds": 42},
		}).
		WithStateProvider("escrow", &mockStateProvider{
			state: map[string]interface{}{"pending": 10},
		})
	r := setupRouter(h)

	w := doRequest(r, "GET", "/admin/state")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := parseBody(t, w)
	state, ok := body["state"].(map[string]interface{})
	if !ok {
		t.Fatal("expected state map in response")
	}
	if _, ok := state["ledger"]; !ok {
		t.Error("expected ledger state")
	}
	if _, ok := state["escrow"]; !ok {
		t.Error("expected escrow state")
	}
}

func TestInspectState_Empty(t *testing.T) {
	h := NewHandler()
	r := setupRouter(h)

	w := doRequest(r, "GET", "/admin/state")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}
