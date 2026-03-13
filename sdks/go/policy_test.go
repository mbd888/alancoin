package alancoin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newPolicyServer(t *testing.T) *httptest.Server {
	t.Helper()
	now := time.Now()
	mux := http.NewServeMux()

	policy := SpendPolicy{
		ID:              "pol_1",
		TenantID:        "tenant_1",
		Name:            "daily-limit",
		Rules:           []PolicyRule{{Type: "max_per_day", Params: map[string]any{"amount": "100.00"}}},
		Priority:        10,
		Enabled:         true,
		EnforcementMode: "enforce",
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	mux.HandleFunc("POST /v1/tenants/tenant_1/policies", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"policy": policy})
	})

	mux.HandleFunc("GET /v1/tenants/tenant_1/policies/pol_1", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"policy": policy})
	})

	mux.HandleFunc("GET /v1/tenants/tenant_1/policies", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(listPoliciesResponse{
			Policies: []SpendPolicy{policy, {ID: "pol_2", Name: "shadow-test"}},
		})
	})

	mux.HandleFunc("PUT /v1/tenants/tenant_1/policies/pol_1", func(w http.ResponseWriter, r *http.Request) {
		updated := policy
		updated.Name = "updated-limit"
		json.NewEncoder(w).Encode(map[string]any{"policy": updated})
	})

	mux.HandleFunc("DELETE /v1/tenants/tenant_1/policies/pol_1", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	return httptest.NewServer(mux)
}

func TestCreatePolicy(t *testing.T) {
	srv := newPolicyServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	pol, err := c.CreatePolicy(context.Background(), "tenant_1", CreatePolicyRequest{
		Name:            "daily-limit",
		Rules:           []PolicyRule{{Type: "max_per_day", Params: map[string]any{"amount": "100.00"}}},
		EnforcementMode: "enforce",
	})
	if err != nil {
		t.Fatal(err)
	}
	if pol.ID != "pol_1" || pol.Name != "daily-limit" {
		t.Errorf("policy = %+v", pol)
	}
}

func TestGetPolicy(t *testing.T) {
	srv := newPolicyServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	pol, err := c.GetPolicy(context.Background(), "tenant_1", "pol_1")
	if err != nil {
		t.Fatal(err)
	}
	if pol.ID != "pol_1" {
		t.Errorf("ID = %q", pol.ID)
	}
	if !pol.Enabled {
		t.Error("Enabled should be true")
	}
}

func TestListPolicies(t *testing.T) {
	srv := newPolicyServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	pols, err := c.ListPolicies(context.Background(), "tenant_1")
	if err != nil {
		t.Fatal(err)
	}
	if len(pols) != 2 {
		t.Errorf("len = %d", len(pols))
	}
}

func TestUpdatePolicy(t *testing.T) {
	srv := newPolicyServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	pol, err := c.UpdatePolicy(context.Background(), "tenant_1", "pol_1", UpdatePolicyRequest{
		Name: "updated-limit",
	})
	if err != nil {
		t.Fatal(err)
	}
	if pol.Name != "updated-limit" {
		t.Errorf("Name = %q", pol.Name)
	}
}

func TestDeletePolicy(t *testing.T) {
	srv := newPolicyServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	err := c.DeletePolicy(context.Background(), "tenant_1", "pol_1")
	if err != nil {
		t.Fatal(err)
	}
}
