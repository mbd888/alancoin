package alancoin

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPostOffer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/offers" {
			t.Errorf("path = %q, want /v1/offers", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}

		body, _ := io.ReadAll(r.Body)
		var req PostOfferRequest
		json.Unmarshal(body, &req)
		if req.ServiceType != "inference" {
			t.Errorf("serviceType = %q, want inference", req.ServiceType)
		}
		if req.Price != "2.50" {
			t.Errorf("price = %q, want 2.50", req.Price)
		}
		if req.Capacity != 10 {
			t.Errorf("capacity = %d, want 10", req.Capacity)
		}

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"offer": map[string]any{
				"id": "off_new", "serviceType": "inference", "price": "2.500000",
				"capacity": 10, "remainingCap": 10, "status": "active",
			},
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	offer, err := c.PostOffer(context.Background(), PostOfferRequest{
		ServiceType: "inference",
		Price:       "2.50",
		Capacity:    10,
		Description: "LLM inference",
	})
	if err != nil {
		t.Fatalf("PostOffer: %v", err)
	}
	if offer.ID != "off_new" {
		t.Errorf("ID = %q, want off_new", offer.ID)
	}
}

func TestGetOffer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/offers/off_123" {
			t.Errorf("path = %q, want /v1/offers/off_123", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"id": "off_123", "serviceType": "translation", "price": "1.000000",
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	offer, err := c.GetOffer(context.Background(), "off_123")
	if err != nil {
		t.Fatalf("GetOffer: %v", err)
	}
	if offer.ID != "off_123" {
		t.Errorf("ID = %q, want off_123", offer.ID)
	}
}

func TestListOffers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/offers" {
			t.Errorf("path = %q, want /v1/offers", r.URL.Path)
		}
		if r.URL.Query().Get("type") != "llm" {
			t.Errorf("type = %q, want llm", r.URL.Query().Get("type"))
		}
		json.NewEncoder(w).Encode(map[string]any{
			"offers": []map[string]any{
				{"id": "off_1", "serviceType": "llm"},
				{"id": "off_2", "serviceType": "llm"},
			},
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	offers, err := c.ListOffers(context.Background(), "llm", 10)
	if err != nil {
		t.Fatalf("ListOffers: %v", err)
	}
	if len(offers) != 2 {
		t.Errorf("len = %d, want 2", len(offers))
	}
}

func TestCancelOffer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/offers/off_cancel/cancel" {
			t.Errorf("path = %q, want /v1/offers/off_cancel/cancel", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	err := c.CancelOffer(context.Background(), "off_cancel")
	if err != nil {
		t.Fatalf("CancelOffer: %v", err)
	}
}

func TestClaimOffer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/offers/off_claim/claim" {
			t.Errorf("path = %q, want /v1/offers/off_claim/claim", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"claim": map[string]any{
				"id": "clm_new", "offerId": "off_claim", "status": "pending",
			},
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	claim, err := c.ClaimOffer(context.Background(), "off_claim")
	if err != nil {
		t.Fatalf("ClaimOffer: %v", err)
	}
	if claim.ID != "clm_new" {
		t.Errorf("ID = %q, want clm_new", claim.ID)
	}
}

func TestGetClaim(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/claims/clm_123" {
			t.Errorf("path = %q", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"id": "clm_123", "status": "pending",
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	claim, err := c.GetClaim(context.Background(), "clm_123")
	if err != nil {
		t.Fatalf("GetClaim: %v", err)
	}
	if claim.ID != "clm_123" {
		t.Errorf("ID = %q, want clm_123", claim.ID)
	}
}

func TestListClaims(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/offers/off_list/claims" {
			t.Errorf("path = %q", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"claims": []map[string]any{
				{"id": "clm_1"}, {"id": "clm_2"},
			},
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	claims, err := c.ListClaims(context.Background(), "off_list", 10)
	if err != nil {
		t.Fatalf("ListClaims: %v", err)
	}
	if len(claims) != 2 {
		t.Errorf("len = %d, want 2", len(claims))
	}
}

func TestDeliverClaim(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/claims/clm_del/deliver" {
			t.Errorf("path = %q", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{"id": "clm_del", "status": "delivered"})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	claim, err := c.DeliverClaim(context.Background(), "clm_del")
	if err != nil {
		t.Fatalf("DeliverClaim: %v", err)
	}
	if claim.Status != "delivered" {
		t.Errorf("status = %q, want delivered", claim.Status)
	}
}

func TestCompleteClaim(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/claims/clm_done/complete" {
			t.Errorf("path = %q", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{"id": "clm_done", "status": "completed"})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	claim, err := c.CompleteClaim(context.Background(), "clm_done")
	if err != nil {
		t.Fatalf("CompleteClaim: %v", err)
	}
	if claim.Status != "completed" {
		t.Errorf("status = %q, want completed", claim.Status)
	}
}

func TestRefundClaim(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/claims/clm_ref/refund" {
			t.Errorf("path = %q", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{"id": "clm_ref", "status": "refunded"})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	claim, err := c.RefundClaim(context.Background(), "clm_ref")
	if err != nil {
		t.Fatalf("RefundClaim: %v", err)
	}
	if claim.Status != "refunded" {
		t.Errorf("status = %q, want refunded", claim.Status)
	}
}
