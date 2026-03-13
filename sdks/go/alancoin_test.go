package alancoin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewClient_Defaults(t *testing.T) {
	c := NewClient("https://example.com")
	if c.baseURL != "https://example.com" {
		t.Errorf("baseURL = %q, want %q", c.baseURL, "https://example.com")
	}
	if c.apiKey != "" {
		t.Errorf("apiKey = %q, want empty", c.apiKey)
	}
	if c.httpClient.Timeout != 30*time.Second {
		t.Errorf("timeout = %v, want 30s", c.httpClient.Timeout)
	}
}

func TestNewClient_WithOptions(t *testing.T) {
	c := NewClient("https://example.com",
		WithAPIKey("ak_test"),
		WithTimeout(10*time.Second),
	)
	if c.apiKey != "ak_test" {
		t.Errorf("apiKey = %q, want %q", c.apiKey, "ak_test")
	}
	if c.httpClient.Timeout != 10*time.Second {
		t.Errorf("timeout = %v, want 10s", c.httpClient.Timeout)
	}
}

func TestNewClient_WithHTTPClient(t *testing.T) {
	custom := &http.Client{Timeout: 5 * time.Second}
	c := NewClient("https://example.com", WithHTTPClient(custom))
	if c.httpClient != custom {
		t.Error("expected custom http client")
	}
}

func TestHealth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Errorf("path = %q, want /health", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("method = %q, want GET", r.Method)
		}
		json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	out, err := c.Health(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if out["status"] != "ok" {
		t.Errorf("status = %v, want ok", out["status"])
	}
}

func TestDoJSON_SetsHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-API-Key"); got != "ak_test" {
			t.Errorf("X-API-Key = %q, want ak_test", got)
		}
		if got := r.Header.Get("User-Agent"); got != userAgent {
			t.Errorf("User-Agent = %q, want %q", got, userAgent)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", got)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	err := c.doJSON(context.Background(), http.MethodPost, "/test", map[string]string{"a": "b"}, nil)
	if err != nil {
		t.Fatal(err)
	}
}

func TestDoJSON_NoContentType_WhenNoBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Content-Type"); got != "" {
			t.Errorf("Content-Type = %q, want empty", got)
		}
		json.NewEncoder(w).Encode(map[string]string{})
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	err := c.doJSON(context.Background(), http.MethodGet, "/test", nil, &map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
}

func TestDoJSON_ErrorParsing(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		body     string
		sentinel error
	}{
		{
			name:     "bad request",
			status:   400,
			body:     `{"error":"invalid field","code":"validation_error"}`,
			sentinel: ErrValidation,
		},
		{
			name:     "unauthorized",
			status:   401,
			body:     `{"error":"bad api key"}`,
			sentinel: ErrUnauthorized,
		},
		{
			name:     "payment required",
			status:   402,
			body:     `{"error":"insufficient funds"}`,
			sentinel: ErrPaymentRequired,
		},
		{
			name:     "forbidden/policy",
			status:   403,
			body:     `{"error":"spending policy denied"}`,
			sentinel: ErrPolicyDenied,
		},
		{
			name:     "not found agent",
			status:   404,
			body:     `{"error":"agent not found","code":"agent_not_found"}`,
			sentinel: ErrAgentNotFound,
		},
		{
			name:     "not found service",
			status:   404,
			body:     `{"error":"service not found","code":"service_not_found"}`,
			sentinel: ErrServiceNotFound,
		},
		{
			name:     "conflict",
			status:   409,
			body:     `{"error":"agent exists"}`,
			sentinel: ErrAgentExists,
		},
		{
			name:     "rate limited",
			status:   429,
			body:     `{"error":"too many requests"}`,
			sentinel: ErrRateLimited,
		},
		{
			name:     "server error",
			status:   500,
			body:     `{"error":"internal error"}`,
			sentinel: ErrServer,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			c := NewClient(srv.URL)
			err := c.doJSON(context.Background(), http.MethodGet, "/test", nil, nil)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !errors.Is(err, tt.sentinel) {
				t.Errorf("errors.Is(err, %v) = false; err = %v", tt.sentinel, err)
			}
			var apiErr *Error
			if !errors.As(err, &apiErr) {
				t.Fatal("expected *Error type")
			}
			if apiErr.StatusCode != tt.status {
				t.Errorf("StatusCode = %d, want %d", apiErr.StatusCode, tt.status)
			}
		})
	}
}

func TestDoJSON_NetworkError(t *testing.T) {
	c := NewClient("http://127.0.0.1:1") // closed port
	err := c.doJSON(context.Background(), http.MethodGet, "/test", nil, nil)
	if err == nil {
		t.Fatal("expected network error")
	}
	if !errors.Is(err, ErrNetwork) {
		t.Errorf("expected ErrNetwork, got %v", err)
	}
}

func TestBuildQuery(t *testing.T) {
	tests := []struct {
		name  string
		pairs []string
		want  string
	}{
		{"empty", nil, ""},
		{"skip empty vals", []string{"a", "", "b", "2"}, "?b=2"},
		{"all present", []string{"limit", "10", "offset", "5"}, "?limit=10&offset=5"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildQuery(tt.pairs...)
			if got != tt.want {
				t.Errorf("buildQuery() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRegister(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/agents" || r.Method != http.MethodPost {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		var req RegisterAgentRequest
		json.NewDecoder(r.Body).Decode(&req)
		if req.Address != "0xABC" {
			t.Errorf("address = %q", req.Address)
		}
		json.NewEncoder(w).Encode(RegisterAgentResponse{
			Agent:  Agent{Address: "0xABC", Name: "test"},
			APIKey: "ak_new",
			KeyID:  "key_1",
			Usage:  "Set X-API-Key header",
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	resp, err := c.Register(context.Background(), RegisterAgentRequest{
		Address: "0xABC",
		Name:    "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.APIKey != "ak_new" {
		t.Errorf("APIKey = %q", resp.APIKey)
	}
	if resp.Agent.Address != "0xABC" {
		t.Errorf("Agent.Address = %q", resp.Agent.Address)
	}
}

func TestGetAgent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/agents/0xABC" {
			t.Errorf("path = %q", r.URL.Path)
		}
		json.NewEncoder(w).Encode(Agent{Address: "0xABC", Name: "test"})
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	agent, err := c.GetAgent(context.Background(), "0xABC")
	if err != nil {
		t.Fatal(err)
	}
	if agent.Name != "test" {
		t.Errorf("Name = %q", agent.Name)
	}
}

func TestDeleteAgent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/v1/agents/0xABC" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	err := c.DeleteAgent(context.Background(), "0xABC")
	if err != nil {
		t.Fatal(err)
	}
}

func TestDiscover(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/services" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.URL.Query().Get("type") != "inference" {
			t.Errorf("type = %q", r.URL.Query().Get("type"))
		}
		json.NewEncoder(w).Encode(DiscoverResponse{
			Services: []ServiceListing{{ID: "svc_1", Type: "inference", Name: "GPT", Price: "0.50"}},
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	services, err := c.Discover(context.Background(), DiscoverOptions{Type: "inference"})
	if err != nil {
		t.Fatal(err)
	}
	if len(services) != 1 || services[0].ID != "svc_1" {
		t.Errorf("unexpected services: %+v", services)
	}
}

func TestGetReputation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(ReputationResponse{
			Reputation: Reputation{Score: 85.5, Tier: "gold"},
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	rep, err := c.GetReputation(context.Background(), "0xABC")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Score != 85.5 || rep.Tier != "gold" {
		t.Errorf("reputation = %+v", rep)
	}
}

func TestGetBalance(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(BalanceResponse{
			Balance: Balance{Available: "100.00", Pending: "5.00", TotalIn: "200.00", TotalOut: "95.00"},
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	bal, err := c.GetBalance(context.Background(), "0xABC")
	if err != nil {
		t.Fatal(err)
	}
	if bal.Available != "100.00" {
		t.Errorf("Available = %q", bal.Available)
	}
}

func TestSingleCall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/gateway/call" || r.Method != http.MethodPost {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		var req SingleCallRequest
		json.NewDecoder(r.Body).Decode(&req)
		if req.ServiceType != "inference" {
			t.Errorf("serviceType = %q", req.ServiceType)
		}
		json.NewEncoder(w).Encode(SingleCallResult{
			Response:    map[string]any{"text": "hello"},
			ServiceUsed: "0xSELLER",
			AmountPaid:  "0.50",
			LatencyMs:   120,
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	result, err := c.SingleCall(context.Background(), "inference", "1.00", map[string]any{"prompt": "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if result.AmountPaid != "0.50" {
		t.Errorf("AmountPaid = %q", result.AmountPaid)
	}
}

func TestSpend_Convenience(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(SingleCallResult{
			Response:   map[string]any{"text": "world"},
			AmountPaid: "0.25",
		})
	}))
	defer srv.Close()

	result, err := Spend(context.Background(), srv.URL, "ak_test", "inference", "1.00", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.AmountPaid != "0.25" {
		t.Errorf("AmountPaid = %q", result.AmountPaid)
	}
}

func TestWebhookCRUD(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/agents/0xABC/webhooks", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(createWebhookResponse{
			ID: "wh_1", Secret: "secret_123", URL: "https://example.com/hook", Events: []string{"payment.received"},
		})
	})
	mux.HandleFunc("GET /v1/agents/0xABC/webhooks", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(listWebhooksResponse{
			Webhooks: []Webhook{{ID: "wh_1", URL: "https://example.com/hook"}},
		})
	})
	mux.HandleFunc("DELETE /v1/agents/0xABC/webhooks/wh_1", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	ctx := context.Background()

	// Create
	wh, err := c.CreateWebhook(ctx, "0xABC", CreateWebhookRequest{
		URL: "https://example.com/hook", Events: []string{"payment.received"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if wh.ID != "wh_1" || wh.Secret != "secret_123" {
		t.Errorf("webhook = %+v", wh)
	}

	// List
	webhooks, err := c.ListWebhooks(ctx, "0xABC")
	if err != nil {
		t.Fatal(err)
	}
	if len(webhooks) != 1 {
		t.Errorf("len = %d", len(webhooks))
	}

	// Delete
	if err := c.DeleteWebhook(ctx, "0xABC", "wh_1"); err != nil {
		t.Fatal(err)
	}
}
