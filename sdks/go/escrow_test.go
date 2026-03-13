package alancoin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newEscrowServer(t *testing.T) *httptest.Server {
	t.Helper()
	now := time.Now()
	mux := http.NewServeMux()

	mux.HandleFunc("POST /v1/escrow", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(escrowResponse{
			Escrow: Escrow{
				ID:            "esc_1",
				BuyerAddr:     "0xBUYER",
				SellerAddr:    "0xSELLER",
				Amount:        "5.00",
				Status:        "locked",
				AutoReleaseAt: now.Add(5 * time.Minute),
				CreatedAt:     now,
			},
		})
	})

	mux.HandleFunc("GET /v1/escrow/esc_1", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(escrowResponse{
			Escrow: Escrow{ID: "esc_1", Status: "locked", Amount: "5.00"},
		})
	})

	mux.HandleFunc("POST /v1/escrow/esc_1/confirm", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(escrowResponse{
			Escrow: Escrow{ID: "esc_1", Status: "released", Amount: "5.00"},
		})
	})

	mux.HandleFunc("POST /v1/escrow/esc_1/dispute", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(escrowResponse{
			Escrow: Escrow{ID: "esc_1", Status: "disputed", Amount: "5.00", DisputeReason: "service not delivered"},
		})
	})

	mux.HandleFunc("POST /v1/escrow/esc_1/deliver", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(escrowResponse{
			Escrow: Escrow{ID: "esc_1", Status: "delivered", Amount: "5.00"},
		})
	})

	mux.HandleFunc("POST /v1/escrow/esc_1/evidence", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(escrowResponse{
			Escrow: Escrow{
				ID:     "esc_1",
				Status: "disputed",
				Amount: "5.00",
				DisputeEvidence: []EvidenceEntry{
					{SubmittedBy: "0xBUYER", Content: "screenshot of failure", SubmittedAt: now},
				},
			},
		})
	})

	mux.HandleFunc("POST /v1/escrow/esc_1/arbitrate", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(escrowResponse{
			Escrow: Escrow{ID: "esc_1", Status: "disputed", Amount: "5.00", ArbitratorAddr: "0xARB"},
		})
	})

	mux.HandleFunc("POST /v1/escrow/esc_1/resolve", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(escrowResponse{
			Escrow: Escrow{ID: "esc_1", Status: "resolved", Amount: "5.00", Resolution: "partial_refund", PartialReleaseAmount: "2.50"},
		})
	})

	mux.HandleFunc("GET /v1/agents/0xBUYER/escrows", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(listEscrowsResponse{
			Escrows: []Escrow{
				{ID: "esc_1", Status: "locked", Amount: "5.00"},
				{ID: "esc_2", Status: "released", Amount: "2.00"},
			},
		})
	})

	return httptest.NewServer(mux)
}

func TestCreateEscrow(t *testing.T) {
	srv := newEscrowServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	esc, err := c.CreateEscrow(context.Background(), CreateEscrowRequest{
		BuyerAddr:  "0xBUYER",
		SellerAddr: "0xSELLER",
		Amount:     "5.00",
	})
	if err != nil {
		t.Fatal(err)
	}
	if esc.ID != "esc_1" || esc.Status != "locked" {
		t.Errorf("escrow = %+v", esc)
	}
}

func TestGetEscrow(t *testing.T) {
	srv := newEscrowServer(t)
	defer srv.Close()

	c := NewClient(srv.URL)
	esc, err := c.GetEscrow(context.Background(), "esc_1")
	if err != nil {
		t.Fatal(err)
	}
	if esc.Amount != "5.00" {
		t.Errorf("Amount = %q", esc.Amount)
	}
}

func TestConfirmEscrow(t *testing.T) {
	srv := newEscrowServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	esc, err := c.ConfirmEscrow(context.Background(), "esc_1")
	if err != nil {
		t.Fatal(err)
	}
	if esc.Status != "released" {
		t.Errorf("Status = %q", esc.Status)
	}
}

func TestDisputeEscrow(t *testing.T) {
	srv := newEscrowServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	esc, err := c.DisputeEscrow(context.Background(), "esc_1", "service not delivered")
	if err != nil {
		t.Fatal(err)
	}
	if esc.Status != "disputed" {
		t.Errorf("Status = %q", esc.Status)
	}
}

func TestDeliverEscrow(t *testing.T) {
	srv := newEscrowServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	esc, err := c.DeliverEscrow(context.Background(), "esc_1")
	if err != nil {
		t.Fatal(err)
	}
	if esc.Status != "delivered" {
		t.Errorf("Status = %q", esc.Status)
	}
}

func TestListEscrows(t *testing.T) {
	srv := newEscrowServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	escrows, err := c.ListEscrows(context.Background(), "0xBUYER", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(escrows) != 2 {
		t.Errorf("len = %d", len(escrows))
	}
}

func TestSubmitEvidence(t *testing.T) {
	srv := newEscrowServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	esc, err := c.SubmitEvidence(context.Background(), "esc_1", "screenshot of failure")
	if err != nil {
		t.Fatal(err)
	}
	if len(esc.DisputeEvidence) != 1 {
		t.Errorf("evidence count = %d", len(esc.DisputeEvidence))
	}
	if esc.DisputeEvidence[0].Content != "screenshot of failure" {
		t.Errorf("Content = %q", esc.DisputeEvidence[0].Content)
	}
}

func TestAssignArbitrator(t *testing.T) {
	srv := newEscrowServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	esc, err := c.AssignArbitrator(context.Background(), "esc_1", "0xARB")
	if err != nil {
		t.Fatal(err)
	}
	if esc.ArbitratorAddr != "0xARB" {
		t.Errorf("ArbitratorAddr = %q", esc.ArbitratorAddr)
	}
}

func TestResolveArbitration(t *testing.T) {
	srv := newEscrowServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	esc, err := c.ResolveArbitration(context.Background(), "esc_1", "partial_refund", "2.50")
	if err != nil {
		t.Fatal(err)
	}
	if esc.Status != "resolved" {
		t.Errorf("Status = %q", esc.Status)
	}
	if esc.PartialReleaseAmount != "2.50" {
		t.Errorf("PartialReleaseAmount = %q", esc.PartialReleaseAmount)
	}
}
