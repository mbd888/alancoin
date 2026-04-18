package compliance

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// stubChainHead satisfies ChainHeadProvider for readiness tests without
// pulling in the receipts package.
type stubChainHead struct {
	head *ChainHeadSnapshot
	err  error
}

func (s *stubChainHead) GetChainHead(_ context.Context, _ string) (*ChainHeadSnapshot, error) {
	return s.head, s.err
}

func newTestService(chain ChainHeadProvider) (*Service, *MemoryStore) {
	store := NewMemoryStore()
	return NewService(store, chain), store
}

func TestRecordAndListIncident(t *testing.T) {
	svc, _ := newTestService(nil)
	ctx := context.Background()

	inc, err := svc.RecordFromAlert(ctx, IncidentInput{
		Scope: "tenant_a", Source: "forensics", Kind: "velocity_spike",
		Severity: SeverityWarning, AgentAddr: "0xABC", Title: "Unusual velocity",
		Detail: "3σ above mean", ReceiptRef: "rcpt_1",
	})
	if err != nil {
		t.Fatalf("RecordFromAlert: %v", err)
	}
	if inc.ID == "" {
		t.Fatal("incident ID should be assigned by the store")
	}
	if inc.Scope != "tenant_a" {
		t.Errorf("scope=%q, want tenant_a", inc.Scope)
	}
	if inc.AgentAddr != "0xabc" {
		t.Errorf("agent should be lowercased, got %q", inc.AgentAddr)
	}
	if inc.OccurredAt.IsZero() {
		t.Error("OccurredAt should default to server time")
	}

	list, err := svc.ListIncidents(ctx, IncidentFilter{Scope: "tenant_a"})
	if err != nil {
		t.Fatalf("ListIncidents: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("expected 1 incident, got %d", len(list))
	}
}

func TestListIncidents_FilterByMinSeverity(t *testing.T) {
	svc, _ := newTestService(nil)
	ctx := context.Background()

	_, _ = svc.RecordFromAlert(ctx, IncidentInput{Scope: "tenant_a", Source: "forensics", Severity: SeverityInfo, Title: "noise"})
	_, _ = svc.RecordFromAlert(ctx, IncidentInput{Scope: "tenant_a", Source: "forensics", Severity: SeverityWarning, Title: "warn"})
	_, _ = svc.RecordFromAlert(ctx, IncidentInput{Scope: "tenant_a", Source: "forensics", Severity: SeverityCritical, Title: "crit"})

	only, err := svc.ListIncidents(ctx, IncidentFilter{Scope: "tenant_a", MinSeverity: SeverityWarning})
	if err != nil {
		t.Fatalf("ListIncidents: %v", err)
	}
	if len(only) != 2 {
		t.Errorf("expected 2 at >= warning, got %d", len(only))
	}
	for _, inc := range only {
		if !inc.Severity.IsAtLeast(SeverityWarning) {
			t.Errorf("got severity %q below floor", inc.Severity)
		}
	}
}

func TestListIncidents_ScopeIsolated(t *testing.T) {
	svc, _ := newTestService(nil)
	ctx := context.Background()

	_, _ = svc.RecordFromAlert(ctx, IncidentInput{Scope: "tenant_a", Source: "forensics", Severity: SeverityWarning, Title: "a"})
	_, _ = svc.RecordFromAlert(ctx, IncidentInput{Scope: "tenant_b", Source: "forensics", Severity: SeverityWarning, Title: "b"})

	listA, _ := svc.ListIncidents(ctx, IncidentFilter{Scope: "tenant_a"})
	listB, _ := svc.ListIncidents(ctx, IncidentFilter{Scope: "tenant_b"})
	if len(listA) != 1 || len(listB) != 1 {
		t.Errorf("scope isolation broken: a=%d b=%d", len(listA), len(listB))
	}
	if listA[0].Title != "a" || listB[0].Title != "b" {
		t.Errorf("wrong incident per scope")
	}
}

func TestAcknowledgeIncident(t *testing.T) {
	svc, _ := newTestService(nil)
	ctx := context.Background()
	inc, _ := svc.RecordFromAlert(ctx, IncidentInput{Scope: "tenant_a", Source: "forensics", Severity: SeverityCritical, Title: "boom"})

	if err := svc.AcknowledgeIncident(ctx, inc.ID, "analyst@example.com", "false positive"); err != nil {
		t.Fatalf("Acknowledge: %v", err)
	}
	got, _ := svc.GetIncident(ctx, inc.ID)
	if !got.Acknowledged {
		t.Error("incident should be acknowledged")
	}
	if got.AckBy != "analyst@example.com" || got.AckNote != "false positive" {
		t.Errorf("ack metadata not saved: by=%q note=%q", got.AckBy, got.AckNote)
	}

	// Filter OnlyUnacked should exclude it.
	open, _ := svc.ListIncidents(ctx, IncidentFilter{Scope: "tenant_a", OnlyUnacked: true})
	if len(open) != 0 {
		t.Errorf("expected 0 open after ack, got %d", len(open))
	}
}

func TestAcknowledgeIncident_NotFound(t *testing.T) {
	svc, _ := newTestService(nil)
	err := svc.AcknowledgeIncident(context.Background(), "missing", "actor", "")
	if err == nil {
		t.Fatal("expected error for missing incident")
	}
}

func TestReadiness_CombinesControlsIncidentsChain(t *testing.T) {
	chain := &stubChainHead{head: &ChainHeadSnapshot{Scope: "tenant_a", HeadHash: "abc123", HeadIndex: 9}}
	svc, _ := newTestService(chain)
	ctx := context.Background()

	_ = svc.RegisterControl(ctx, Control{ID: "ctl_1", Title: "Receipt chain", Status: StatusEnabled, Group: "audit"})
	_ = svc.RegisterControl(ctx, Control{ID: "ctl_2", Title: "Policy engine", Status: StatusDegraded, Group: "oversight"})
	_ = svc.RegisterControl(ctx, Control{ID: "ctl_3", Title: "Deposit watcher", Status: StatusDisabled, Group: "audit"})

	_, _ = svc.RecordFromAlert(ctx, IncidentInput{Scope: "tenant_a", Source: "forensics", Severity: SeverityWarning, Title: "w1"})
	_, _ = svc.RecordFromAlert(ctx, IncidentInput{Scope: "tenant_a", Source: "forensics", Severity: SeverityCritical, Title: "c1"})

	r, err := svc.Readiness(ctx, "tenant_a")
	if err != nil {
		t.Fatalf("Readiness: %v", err)
	}
	if r.Scope != "tenant_a" {
		t.Errorf("scope=%q", r.Scope)
	}
	if r.EnabledCount != 1 || r.DegradedCount != 1 || r.DisabledCount != 1 {
		t.Errorf("control counts wrong: e=%d d=%d x=%d", r.EnabledCount, r.DegradedCount, r.DisabledCount)
	}
	if r.Incidents.Warning != 1 || r.Incidents.Critical != 1 {
		t.Errorf("incident counts wrong: w=%d c=%d", r.Incidents.Warning, r.Incidents.Critical)
	}
	if r.Incidents.Open != 2 {
		t.Errorf("expected 2 open, got %d", r.Incidents.Open)
	}
	if r.ChainHeadHash != "abc123" || r.ChainHeadIndex != 9 || r.ChainReceipts != 10 {
		t.Errorf("chain fields wrong: hash=%q idx=%d count=%d", r.ChainHeadHash, r.ChainHeadIndex, r.ChainReceipts)
	}
	if r.OldestOpen == nil {
		t.Error("OldestOpen should be set while incidents are open")
	}
}

func TestReadiness_NilChainProvider(t *testing.T) {
	svc, _ := newTestService(nil)
	r, err := svc.Readiness(context.Background(), "tenant_a")
	if err != nil {
		t.Fatalf("Readiness: %v", err)
	}
	if r.ChainHeadHash != "" {
		t.Errorf("expected empty ChainHeadHash, got %q", r.ChainHeadHash)
	}
	if r.ChainHeadIndex != -1 {
		t.Errorf("expected ChainHeadIndex -1, got %d", r.ChainHeadIndex)
	}
}

// --- handler tests ---

func setupRouter(t *testing.T) (*gin.Engine, *Service) {
	t.Helper()
	chain := &stubChainHead{head: &ChainHeadSnapshot{Scope: "tenant_a", HeadHash: "hf", HeadIndex: 1}}
	svc, _ := newTestService(chain)
	r := gin.New()
	NewHandler(svc).RegisterRoutes(r.Group("/v1"))
	return r, svc
}

func TestHandler_RecordAndListIncidents(t *testing.T) {
	r, _ := setupRouter(t)

	body, _ := json.Marshal(map[string]any{
		"source":   "forensics",
		"severity": "warning",
		"kind":     "velocity_spike",
		"title":    "unusual velocity",
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/compliance/tenant_a/incidents", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", w.Code, w.Body.String())
	}

	// List should return it.
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("GET", "/v1/compliance/tenant_a/incidents", nil)
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("list status=%d", w2.Code)
	}
	var resp struct {
		Count int `json:"count"`
	}
	_ = json.Unmarshal(w2.Body.Bytes(), &resp)
	if resp.Count != 1 {
		t.Errorf("expected 1 incident, got %d", resp.Count)
	}
}

func TestHandler_InvalidSeverity(t *testing.T) {
	r, _ := setupRouter(t)
	body, _ := json.Marshal(map[string]any{
		"source":   "forensics",
		"severity": "nonsense",
		"title":    "bad",
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/compliance/tenant_a/incidents", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandler_AckFlow(t *testing.T) {
	r, svc := setupRouter(t)
	inc, _ := svc.RecordFromAlert(context.Background(), IncidentInput{Scope: "tenant_a", Source: "forensics", Severity: SeverityCritical, Title: "boom"})

	body, _ := json.Marshal(map[string]any{"actor": "oncall", "note": "investigated"})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/compliance/incidents/"+inc.ID+"/ack", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ack status=%d body=%s", w.Code, w.Body.String())
	}
	got, _ := svc.GetIncident(context.Background(), inc.ID)
	if !got.Acknowledged {
		t.Error("should be acknowledged after handler call")
	}
}

func TestHandler_AckUnknownIncident(t *testing.T) {
	r, _ := setupRouter(t)
	body, _ := json.Marshal(map[string]any{"actor": "oncall"})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/compliance/incidents/bogus/ack", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestHandler_UpsertAndListControls(t *testing.T) {
	r, _ := setupRouter(t)

	body, _ := json.Marshal(map[string]any{
		"title":  "Receipt chain integrity",
		"group":  "audit",
		"status": "enabled",
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/v1/compliance/controls/chain_intact", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("upsert status=%d body=%s", w.Code, w.Body.String())
	}

	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("GET", "/v1/compliance/controls", nil)
	r.ServeHTTP(w2, req2)
	var resp struct {
		Count int `json:"count"`
	}
	_ = json.Unmarshal(w2.Body.Bytes(), &resp)
	if resp.Count != 1 {
		t.Errorf("expected 1 control, got %d", resp.Count)
	}
}

func TestHandler_ReadinessRoute(t *testing.T) {
	r, svc := setupRouter(t)
	_ = svc.RegisterControl(context.Background(), Control{ID: "c1", Title: "X", Status: StatusEnabled})
	_, _ = svc.RecordFromAlert(context.Background(), IncidentInput{Scope: "tenant_a", Source: "forensics", Severity: SeverityWarning, Title: "w"})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/compliance/tenant_a/readiness", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Report *ReadinessReport `json:"report"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Report == nil || resp.Report.Scope != "tenant_a" {
		t.Error("readiness response malformed")
	}
	if resp.Report.ChainHeadHash != "hf" {
		t.Errorf("expected stub chain head, got %q", resp.Report.ChainHeadHash)
	}
}

func TestHandler_InvalidTimeFormat(t *testing.T) {
	r, _ := setupRouter(t)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/compliance/tenant_a/incidents?since=not-a-time", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}
