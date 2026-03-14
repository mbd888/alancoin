package alancoin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestListStuckSessions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/admin/gateway/stuck" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("method = %q, want GET", r.Method)
		}
		json.NewEncoder(w).Encode(listStuckSessionsResponse{
			Sessions: []StuckSession{
				{ID: "sess_1", AgentAddr: "0xABC", Status: "settlement_failed", MaxTotal: "10.00", TotalSpent: "3.00"},
				{ID: "sess_2", AgentAddr: "0xDEF", Status: "settlement_failed", MaxTotal: "20.00", TotalSpent: "15.00"},
			},
			Count: 2,
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_admin"))
	sessions, err := c.ListStuckSessions(context.Background(), 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Fatalf("len = %d, want 2", len(sessions))
	}
	if sessions[0].ID != "sess_1" || sessions[0].Status != "settlement_failed" {
		t.Errorf("sessions[0] = %+v", sessions[0])
	}
}

func TestResolveStuckSession(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/admin/gateway/sessions/sess_stuck/resolve" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		json.NewEncoder(w).Encode(ResolveResult{Resolved: true, SessionID: "sess_stuck"})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_admin"))
	result, err := c.ResolveStuckSession(context.Background(), "sess_stuck")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Resolved {
		t.Error("expected resolved = true")
	}
	if result.SessionID != "sess_stuck" {
		t.Errorf("sessionId = %q", result.SessionID)
	}
}

func TestRetrySettlement(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/admin/gateway/sessions/sess_fail/retry-settlement" {
			t.Errorf("path = %q", r.URL.Path)
		}
		json.NewEncoder(w).Encode(RetryResult{Retried: true, SessionID: "sess_fail"})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_admin"))
	result, err := c.RetrySettlement(context.Background(), "sess_fail")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Retried {
		t.Error("expected retried = true")
	}
}

func TestForceCloseExpiredEscrows(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/admin/escrow/force-close-expired" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		json.NewEncoder(w).Encode(ForceCloseResult{ClosedCount: 5})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_admin"))
	result, err := c.ForceCloseExpiredEscrows(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.ClosedCount != 5 {
		t.Errorf("closedCount = %d, want 5", result.ClosedCount)
	}
}

func TestForceCloseStaleStreams(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/admin/streams/force-close-stale" {
			t.Errorf("path = %q", r.URL.Path)
		}
		json.NewEncoder(w).Encode(ForceCloseResult{ClosedCount: 3})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_admin"))
	result, err := c.ForceCloseStaleStreams(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.ClosedCount != 3 {
		t.Errorf("closedCount = %d, want 3", result.ClosedCount)
	}
}

func TestReconcile(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/admin/reconcile" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		json.NewEncoder(w).Encode(reconcileResponse{
			Report: ReconciliationReport{
				LedgerMismatches: 0,
				StuckEscrows:     2,
				StaleStreams:     1,
				OrphanedHolds:    0,
				Healthy:          false,
				DurationMs:       125,
				Timestamp:        now,
			},
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_admin"))
	report, err := c.Reconcile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.Healthy {
		t.Error("expected healthy = false")
	}
	if report.StuckEscrows != 2 {
		t.Errorf("stuckEscrows = %d, want 2", report.StuckEscrows)
	}
	if report.StaleStreams != 1 {
		t.Errorf("staleStreams = %d, want 1", report.StaleStreams)
	}
	if report.DurationMs != 125 {
		t.Errorf("durationMs = %d, want 125", report.DurationMs)
	}
}

func TestExportDenials(t *testing.T) {
	since := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/admin/denials/export" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got := r.URL.Query().Get("since"); got != since.Format(time.RFC3339) {
			t.Errorf("since = %q, want %q", got, since.Format(time.RFC3339))
		}
		if got := r.URL.Query().Get("limit"); got != "500" {
			t.Errorf("limit = %q, want 500", got)
		}
		json.NewEncoder(w).Encode(DenialExport{
			Denials: []DenialExportRecord{
				{ID: 1, AgentAddr: "0xABC", RuleName: "velocity_limit", Reason: "hourly spend exceeded", Amount: "50.00"},
				{ID: 2, AgentAddr: "0xDEF", RuleName: "new_agent_cap", Reason: "new agent spending cap", Amount: "10.00"},
			},
			Count: 2,
			Since: since,
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_admin"))
	export, err := c.ExportDenials(context.Background(), since, 500)
	if err != nil {
		t.Fatal(err)
	}
	if export.Count != 2 {
		t.Errorf("count = %d, want 2", export.Count)
	}
	if len(export.Denials) != 2 {
		t.Fatalf("len = %d, want 2", len(export.Denials))
	}
	if export.Denials[0].RuleName != "velocity_limit" {
		t.Errorf("denials[0].ruleName = %q", export.Denials[0].RuleName)
	}
}

func TestExportDenials_NoSince(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("since"); got != "" {
			t.Errorf("since should be empty, got %q", got)
		}
		json.NewEncoder(w).Encode(DenialExport{Denials: nil, Count: 0})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_admin"))
	export, err := c.ExportDenials(context.Background(), time.Time{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if export.Count != 0 {
		t.Errorf("count = %d", export.Count)
	}
}

func TestInspectState(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/admin/state" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("method = %q, want GET", r.Method)
		}
		json.NewEncoder(w).Encode(StateInspection{
			State: map[string]any{
				"db":             map[string]any{"pool_active": 5, "pool_idle": 10},
				"websocket":      map[string]any{"connected_clients": 42},
				"reconciliation": map[string]any{"last_run": "2026-03-13T10:00:00Z"},
			},
			Timestamp: time.Now().UTC(),
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_admin"))
	state, err := c.InspectState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.State == nil {
		t.Fatal("state is nil")
	}
	if state.State["db"] == nil {
		t.Error("expected db state")
	}
	if state.State["websocket"] == nil {
		t.Error("expected websocket state")
	}
	if state.Timestamp.IsZero() {
		t.Error("expected non-zero timestamp")
	}
}
