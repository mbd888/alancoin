package alancoin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newBillingServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	sub := SubscriptionInfo{
		ID:                "sub_1",
		Plan:              "starter",
		Status:            "active",
		CurrentPeriodEnd:  "2024-04-01T00:00:00Z",
		CancelAtPeriodEnd: false,
	}

	mux.HandleFunc("POST /v1/tenants/ten_1/billing/subscribe", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"subscription": sub})
	})

	mux.HandleFunc("POST /v1/tenants/ten_1/billing/upgrade", func(w http.ResponseWriter, r *http.Request) {
		upgraded := sub
		upgraded.Plan = "growth"
		json.NewEncoder(w).Encode(map[string]any{"subscription": upgraded})
	})

	mux.HandleFunc("POST /v1/tenants/ten_1/billing/cancel", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("GET /v1/tenants/ten_1/billing/subscription", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"subscription": sub})
	})

	return httptest.NewServer(mux)
}

func TestSubscribe(t *testing.T) {
	srv := newBillingServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	sub, err := c.Subscribe(context.Background(), "ten_1", SubscribeRequest{Plan: "starter"})
	if err != nil {
		t.Fatal(err)
	}
	if sub.ID != "sub_1" {
		t.Errorf("ID = %q", sub.ID)
	}
	if sub.Plan != "starter" {
		t.Errorf("Plan = %q", sub.Plan)
	}
}

func TestUpgradePlan(t *testing.T) {
	srv := newBillingServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	sub, err := c.UpgradePlan(context.Background(), "ten_1", "growth")
	if err != nil {
		t.Fatal(err)
	}
	if sub.Plan != "growth" {
		t.Errorf("Plan = %q", sub.Plan)
	}
}

func TestCancelSubscription(t *testing.T) {
	srv := newBillingServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	err := c.CancelSubscription(context.Background(), "ten_1")
	if err != nil {
		t.Fatal(err)
	}
}

func TestGetSubscription(t *testing.T) {
	srv := newBillingServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	sub, err := c.GetSubscription(context.Background(), "ten_1")
	if err != nil {
		t.Fatal(err)
	}
	if sub.Status != "active" {
		t.Errorf("Status = %q", sub.Status)
	}
}
