package alancoin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newMultiStepServer(t *testing.T) *httptest.Server {
	t.Helper()
	now := time.Now()
	mux := http.NewServeMux()

	mux.HandleFunc("POST /v1/escrow/multistep", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(multiStepEscrowResponse{
			Escrow: MultiStepEscrow{
				ID:          "mse_1",
				BuyerAddr:   "0xBUYER",
				TotalAmount: "3.00",
				SpentAmount: "0.00",
				TotalSteps:  3,
				PlannedSteps: []PlannedStep{
					{SellerAddr: "0xA", Amount: "1.00"},
					{SellerAddr: "0xB", Amount: "1.00"},
					{SellerAddr: "0xC", Amount: "1.00"},
				},
				Status:    "locked",
				CreatedAt: now,
				UpdatedAt: now,
			},
		})
	})

	mux.HandleFunc("GET /v1/escrow/multistep/mse_1", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(multiStepEscrowResponse{
			Escrow: MultiStepEscrow{
				ID:             "mse_1",
				TotalAmount:    "3.00",
				SpentAmount:    "1.00",
				TotalSteps:     3,
				ConfirmedSteps: 1,
				Status:         "in_progress",
			},
		})
	})

	mux.HandleFunc("POST /v1/escrow/multistep/mse_1/confirm-step", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(multiStepEscrowResponse{
			Escrow: MultiStepEscrow{
				ID:             "mse_1",
				TotalAmount:    "3.00",
				SpentAmount:    "2.00",
				TotalSteps:     3,
				ConfirmedSteps: 2,
				Status:         "in_progress",
			},
		})
	})

	mux.HandleFunc("POST /v1/escrow/multistep/mse_1/refund", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(multiStepEscrowResponse{
			Escrow: MultiStepEscrow{
				ID:          "mse_1",
				TotalAmount: "3.00",
				SpentAmount: "2.00",
				Status:      "refunded",
			},
		})
	})

	return httptest.NewServer(mux)
}

func TestCreateMultiStepEscrow(t *testing.T) {
	srv := newMultiStepServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	esc, err := c.CreateMultiStepEscrow(context.Background(), CreateMultiStepEscrowRequest{
		TotalAmount: "3.00",
		TotalSteps:  3,
		PlannedSteps: []PlannedStep{
			{SellerAddr: "0xA", Amount: "1.00"},
			{SellerAddr: "0xB", Amount: "1.00"},
			{SellerAddr: "0xC", Amount: "1.00"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if esc.ID != "mse_1" || esc.Status != "locked" {
		t.Errorf("escrow = %+v", esc)
	}
	if esc.TotalSteps != 3 {
		t.Errorf("TotalSteps = %d", esc.TotalSteps)
	}
}

func TestGetMultiStepEscrow(t *testing.T) {
	srv := newMultiStepServer(t)
	defer srv.Close()

	c := NewClient(srv.URL)
	esc, err := c.GetMultiStepEscrow(context.Background(), "mse_1")
	if err != nil {
		t.Fatal(err)
	}
	if esc.ConfirmedSteps != 1 {
		t.Errorf("ConfirmedSteps = %d", esc.ConfirmedSteps)
	}
}

func TestConfirmStep(t *testing.T) {
	srv := newMultiStepServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	esc, err := c.ConfirmStep(context.Background(), "mse_1", ConfirmStepRequest{
		StepIndex:  1,
		SellerAddr: "0xB",
		Amount:     "1.00",
	})
	if err != nil {
		t.Fatal(err)
	}
	if esc.ConfirmedSteps != 2 {
		t.Errorf("ConfirmedSteps = %d", esc.ConfirmedSteps)
	}
	if esc.SpentAmount != "2.00" {
		t.Errorf("SpentAmount = %q", esc.SpentAmount)
	}
}

func TestRefundMultiStep(t *testing.T) {
	srv := newMultiStepServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	esc, err := c.RefundMultiStep(context.Background(), "mse_1")
	if err != nil {
		t.Fatal(err)
	}
	if esc.Status != "refunded" {
		t.Errorf("Status = %q", esc.Status)
	}
}
