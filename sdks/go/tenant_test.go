package alancoin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newTenantServer(t *testing.T) *httptest.Server {
	t.Helper()
	now := time.Now()
	mux := http.NewServeMux()

	tenant := Tenant{
		ID:     "ten_1",
		Name:   "Test Corp",
		Slug:   "test-corp",
		Plan:   "starter",
		Status: "active",
		Settings: TenantSettings{
			RateLimitRpm:     1000,
			MaxAgents:        5,
			MaxSessionBudget: "1000.00",
			TakeRateBps:      250,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}

	mux.HandleFunc("POST /v1/tenants", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(CreateTenantResponse{
			Tenant:  tenant,
			APIKey:  "sk_tenant_key",
			KeyID:   "key_t1",
			Warning: "Store this API key securely.",
		})
	})

	mux.HandleFunc("GET /v1/tenants/ten_1", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(tenantResponse{Tenant: tenant})
	})

	mux.HandleFunc("PATCH /v1/tenants/ten_1", func(w http.ResponseWriter, r *http.Request) {
		updated := tenant
		updated.Name = "Updated Corp"
		json.NewEncoder(w).Encode(tenantResponse{Tenant: updated})
	})

	mux.HandleFunc("GET /v1/tenants/ten_1/agents", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(listTenantAgentsResponse{
			Agents: []string{"0xABC", "0xDEF"},
			Count:  2,
		})
	})

	mux.HandleFunc("POST /v1/tenants/ten_1/agents", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(TenantAgentResponse{
			Agent:   map[string]any{"address": "0xNEW", "name": "New Agent"},
			APIKey:  "sk_agent_key",
			KeyID:   "key_a1",
			Warning: "Store this key securely.",
		})
	})

	mux.HandleFunc("GET /v1/tenants/ten_1/keys", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(listTenantKeysResponse{
			Keys: []TenantKeyInfo{
				{ID: "key_t1", AgentAddr: "0xABC", Name: "default", CreatedAt: now},
			},
			Count: 1,
		})
	})

	mux.HandleFunc("POST /v1/tenants/ten_1/keys", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(CreateAPIKeyResponse{
			APIKey: "sk_new_tenant_key",
			KeyID:  "key_t2",
			Name:   "extra-key",
		})
	})

	mux.HandleFunc("DELETE /v1/tenants/ten_1/keys/key_t1", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("GET /v1/tenants/ten_1/billing", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(tenantBillingResponse{
			Billing: TenantBilling{
				TotalRequests:   1500,
				SettledRequests: 1450,
				SettledVolume:   "450.25",
				FeesCollected:   "112.56",
				TakeRateBps:     250,
				Plan:            "starter",
			},
		})
	})

	return httptest.NewServer(mux)
}

func TestCreateTenant(t *testing.T) {
	srv := newTenantServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("admin_key"))
	resp, err := c.CreateTenant(context.Background(), CreateTenantRequest{
		Name: "Test Corp",
		Slug: "test-corp",
		Plan: "starter",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Tenant.ID != "ten_1" {
		t.Errorf("ID = %q", resp.Tenant.ID)
	}
	if resp.APIKey == "" {
		t.Error("APIKey is empty")
	}
}

func TestGetTenant(t *testing.T) {
	srv := newTenantServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	tenant, err := c.GetTenant(context.Background(), "ten_1")
	if err != nil {
		t.Fatal(err)
	}
	if tenant.Name != "Test Corp" {
		t.Errorf("Name = %q", tenant.Name)
	}
	if tenant.Settings.TakeRateBps != 250 {
		t.Errorf("TakeRateBps = %d", tenant.Settings.TakeRateBps)
	}
}

func TestUpdateTenant(t *testing.T) {
	srv := newTenantServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	tenant, err := c.UpdateTenant(context.Background(), "ten_1", UpdateTenantRequest{
		Name: "Updated Corp",
	})
	if err != nil {
		t.Fatal(err)
	}
	if tenant.Name != "Updated Corp" {
		t.Errorf("Name = %q", tenant.Name)
	}
}

func TestListTenantAgents(t *testing.T) {
	srv := newTenantServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	agents, err := c.ListTenantAgents(context.Background(), "ten_1")
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 2 {
		t.Errorf("len = %d", len(agents))
	}
}

func TestRegisterTenantAgent(t *testing.T) {
	srv := newTenantServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	resp, err := c.RegisterTenantAgent(context.Background(), "ten_1", TenantAgentRequest{
		Address: "0xNEW",
		Name:    "New Agent",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.APIKey != "sk_agent_key" {
		t.Errorf("APIKey = %q", resp.APIKey)
	}
}

func TestListTenantKeys(t *testing.T) {
	srv := newTenantServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	keys, err := c.ListTenantKeys(context.Background(), "ten_1")
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 {
		t.Errorf("len = %d", len(keys))
	}
}

func TestCreateTenantKey(t *testing.T) {
	srv := newTenantServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	resp, err := c.CreateTenantKey(context.Background(), "ten_1", TenantKeyRequest{
		AgentAddr: "0xABC",
		Name:      "extra-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.KeyID != "key_t2" {
		t.Errorf("KeyID = %q", resp.KeyID)
	}
}

func TestRevokeTenantKey(t *testing.T) {
	srv := newTenantServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	err := c.RevokeTenantKey(context.Background(), "ten_1", "key_t1")
	if err != nil {
		t.Fatal(err)
	}
}

func TestGetTenantBilling(t *testing.T) {
	srv := newTenantServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	billing, err := c.GetTenantBilling(context.Background(), "ten_1")
	if err != nil {
		t.Fatal(err)
	}
	if billing.TotalRequests != 1500 {
		t.Errorf("TotalRequests = %d", billing.TotalRequests)
	}
	if billing.FeesCollected != "112.56" {
		t.Errorf("FeesCollected = %q", billing.FeesCollected)
	}
}
