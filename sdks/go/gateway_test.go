package alancoin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newTestGatewayServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("POST /v1/gateway/sessions", func(w http.ResponseWriter, r *http.Request) {
		var cfg GatewayConfig
		json.NewDecoder(r.Body).Decode(&cfg)
		json.NewEncoder(w).Encode(createSessionResponse{
			Session: GatewaySessionInfo{
				ID:            "sess_test",
				MaxTotal:      cfg.MaxTotal,
				MaxPerRequest: cfg.MaxPerRequest,
				TotalSpent:    "0.00",
				Status:        "active",
				ExpiresAt:     time.Now().Add(time.Hour),
			},
		})
	})

	mux.HandleFunc("POST /v1/gateway/proxy", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Gateway-Token") != "sess_test" {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid token"})
			return
		}
		var req ProxyRequest
		json.NewDecoder(r.Body).Decode(&req)
		json.NewEncoder(w).Encode(ProxyResult{
			Response:    map[string]any{"text": "response for " + req.ServiceType},
			ServiceUsed: "0xSELLER",
			ServiceName: "TestService",
			AmountPaid:  "0.50",
			TotalSpent:  "0.50",
			Remaining:   "4.50",
			LatencyMs:   42,
		})
	})

	mux.HandleFunc("POST /v1/gateway/pipeline", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Gateway-Token") != "sess_test" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var req struct {
			Steps []PipelineStep `json:"steps"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		steps := make([]PipelineStepResult, len(req.Steps))
		for i, s := range req.Steps {
			steps[i] = PipelineStepResult{
				StepIndex:   i,
				ServiceType: s.ServiceType,
				Response:    map[string]any{"result": "step " + s.ServiceType},
				AmountPaid:  "0.25",
				LatencyMs:   10,
			}
		}
		json.NewEncoder(w).Encode(PipelineResult{
			Steps:      steps,
			TotalPaid:  "0.50",
			TotalSpent: "1.00",
			Remaining:  "4.00",
		})
	})

	mux.HandleFunc("DELETE /v1/gateway/sessions/sess_test", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(closeSessionResponse{
			Session: GatewaySessionInfo{
				ID:         "sess_test",
				TotalSpent: "0.50",
				Status:     "closed",
			},
		})
	})

	mux.HandleFunc("GET /v1/gateway/sessions/sess_test", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(createSessionResponse{
			Session: GatewaySessionInfo{
				ID:         "sess_test",
				TotalSpent: "0.50",
				MaxTotal:   "5.00",
				Status:     "active",
			},
		})
	})

	mux.HandleFunc("POST /v1/gateway/sessions/sess_test/dry-run", func(w http.ResponseWriter, r *http.Request) {
		var req DryRunRequest
		json.NewDecoder(r.Body).Decode(&req)
		json.NewEncoder(w).Encode(dryRunResponse{
			Result: DryRunResult{
				Allowed:      true,
				BudgetOk:     true,
				Remaining:    "4.50",
				ServiceFound: true,
				BestPrice:    "0.25",
				BestService:  "0xSELLER",
				PolicyResult: &PolicyDecision{
					Evaluated: 2,
					Allowed:   true,
					LatencyUs: 150,
				},
			},
		})
	})

	mux.HandleFunc("GET /v1/gateway/sessions/sess_test/logs", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(listLogsResponse{
			Logs: []RequestLog{
				{ID: "log_1", SessionID: "sess_test", ServiceType: "inference", Amount: "0.50", Status: "success"},
			},
		})
	})

	return httptest.NewServer(mux)
}

func TestGateway_CreateAndCall(t *testing.T) {
	srv := newTestGatewayServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	ctx := context.Background()

	gw, err := c.Gateway(ctx, GatewayConfig{
		MaxTotal:      "5.00",
		MaxPerRequest: "1.00",
	})
	if err != nil {
		t.Fatal(err)
	}
	if gw.ID() != "sess_test" {
		t.Errorf("ID = %q", gw.ID())
	}
	if !gw.IsActive() {
		t.Error("expected active session")
	}

	result, err := gw.Call(ctx, "inference", nil, map[string]any{"prompt": "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if result.AmountPaid != "0.50" {
		t.Errorf("AmountPaid = %q", result.AmountPaid)
	}
	if result.Response["text"] != "response for inference" {
		t.Errorf("Response = %v", result.Response)
	}
	if gw.TotalSpent() != "0.50" {
		t.Errorf("TotalSpent = %q", gw.TotalSpent())
	}
	if gw.RequestCount() != 1 {
		t.Errorf("RequestCount = %d", gw.RequestCount())
	}
}

func TestGateway_CallWithOptions(t *testing.T) {
	srv := newTestGatewayServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	ctx := context.Background()

	gw, err := c.Gateway(ctx, GatewayConfig{MaxTotal: "5.00"})
	if err != nil {
		t.Fatal(err)
	}

	result, err := gw.Call(ctx, "inference", &ProxyRequest{
		MaxPrice:       "0.75",
		PreferAgent:    "0xPREF",
		IdempotencyKey: "idem_1",
	}, map[string]any{"prompt": "test"})
	if err != nil {
		t.Fatal(err)
	}
	if result.AmountPaid != "0.50" {
		t.Errorf("AmountPaid = %q", result.AmountPaid)
	}
}

func TestGateway_Pipeline(t *testing.T) {
	srv := newTestGatewayServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	ctx := context.Background()

	gw, err := c.Gateway(ctx, GatewayConfig{MaxTotal: "5.00"})
	if err != nil {
		t.Fatal(err)
	}

	result, err := gw.Pipeline(ctx, []PipelineStep{
		{ServiceType: "embedding", Params: map[string]any{"text": "hello"}},
		{ServiceType: "search", Params: map[string]any{"vector": "$prev"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Steps) != 2 {
		t.Fatalf("steps = %d", len(result.Steps))
	}
	if result.TotalPaid != "0.50" {
		t.Errorf("TotalPaid = %q", result.TotalPaid)
	}
	if result.TotalSpent != "1.00" {
		t.Errorf("TotalSpent = %q", result.TotalSpent)
	}
	if gw.TotalSpent() != "1.00" {
		t.Errorf("session TotalSpent = %q", gw.TotalSpent())
	}
}

func TestGateway_Close(t *testing.T) {
	srv := newTestGatewayServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	ctx := context.Background()

	gw, err := c.Gateway(ctx, GatewayConfig{MaxTotal: "5.00"})
	if err != nil {
		t.Fatal(err)
	}

	info, err := gw.Close(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.Status != "closed" {
		t.Errorf("Status = %q", info.Status)
	}
	if gw.IsActive() {
		t.Error("expected inactive after close")
	}
}

func TestGateway_Logs(t *testing.T) {
	srv := newTestGatewayServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	ctx := context.Background()

	gw, err := c.Gateway(ctx, GatewayConfig{MaxTotal: "5.00"})
	if err != nil {
		t.Fatal(err)
	}

	logs, err := gw.Logs(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 || logs[0].ID != "log_1" {
		t.Errorf("logs = %+v", logs)
	}
}

func TestGateway_Refresh(t *testing.T) {
	srv := newTestGatewayServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	ctx := context.Background()

	gw, err := c.Gateway(ctx, GatewayConfig{MaxTotal: "5.00"})
	if err != nil {
		t.Fatal(err)
	}

	info, err := gw.Refresh(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.TotalSpent != "0.50" { // test server returns "0.50" on GET
		t.Errorf("TotalSpent = %q", info.TotalSpent)
	}
	if gw.TotalSpent() != "0.50" {
		t.Errorf("session TotalSpent = %q", gw.TotalSpent())
	}
}

func TestGateway_DryRun(t *testing.T) {
	srv := newTestGatewayServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	ctx := context.Background()

	gw, err := c.Gateway(ctx, GatewayConfig{MaxTotal: "5.00"})
	if err != nil {
		t.Fatal(err)
	}

	result, err := gw.DryRun(ctx, DryRunRequest{
		ServiceType: "inference",
		Params:      map[string]any{"prompt": "test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Allowed {
		t.Error("expected allowed")
	}
	if !result.BudgetOk {
		t.Error("expected budgetOk")
	}
	if !result.ServiceFound {
		t.Error("expected serviceFound")
	}
	if result.BestPrice != "0.25" {
		t.Errorf("BestPrice = %q", result.BestPrice)
	}
	if result.PolicyResult == nil {
		t.Fatal("expected PolicyResult")
	}
	if result.PolicyResult.Evaluated != 2 {
		t.Errorf("Evaluated = %d", result.PolicyResult.Evaluated)
	}
}

func TestConnect_Convenience(t *testing.T) {
	srv := newTestGatewayServer(t)
	defer srv.Close()

	gw, err := Connect(context.Background(), srv.URL, "ak_test", "10.00")
	if err != nil {
		t.Fatal(err)
	}
	if gw.ID() != "sess_test" {
		t.Errorf("ID = %q", gw.ID())
	}
}
