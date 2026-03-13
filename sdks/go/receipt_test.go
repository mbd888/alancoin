package alancoin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newReceiptServer(t *testing.T) *httptest.Server {
	t.Helper()
	now := time.Now()
	mux := http.NewServeMux()

	mux.HandleFunc("GET /v1/receipts/rcpt_1", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"receipt": Receipt{
				ID:          "rcpt_1",
				PaymentPath: "direct",
				Reference:   "tx_abc",
				From:        "0xBUYER",
				To:          "0xSELLER",
				Amount:      "1.00",
				Status:      "valid",
				PayloadHash: "sha256:abc",
				Signature:   "hmac:xyz",
				IssuedAt:    now,
				ExpiresAt:   now.Add(24 * time.Hour),
			},
		})
	})

	mux.HandleFunc("POST /v1/receipts/verify", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"result": ReceiptVerifyResponse{
				Valid:     true,
				ReceiptID: "rcpt_1",
			},
		})
	})

	mux.HandleFunc("GET /v1/agents/0xBUYER/receipts", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(listReceiptsResponse{
			Receipts: []Receipt{
				{ID: "rcpt_1", Amount: "1.00", Status: "valid"},
				{ID: "rcpt_2", Amount: "2.00", Status: "expired"},
			},
		})
	})

	return httptest.NewServer(mux)
}

func TestGetReceipt(t *testing.T) {
	srv := newReceiptServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	rcpt, err := c.GetReceipt(context.Background(), "rcpt_1")
	if err != nil {
		t.Fatal(err)
	}
	if rcpt.ID != "rcpt_1" || rcpt.Amount != "1.00" {
		t.Errorf("receipt = %+v", rcpt)
	}
	if rcpt.PayloadHash != "sha256:abc" {
		t.Errorf("PayloadHash = %q", rcpt.PayloadHash)
	}
}

func TestVerifyReceipt(t *testing.T) {
	srv := newReceiptServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	result, err := c.VerifyReceipt(context.Background(), "rcpt_1")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Valid {
		t.Errorf("Valid = %v", result.Valid)
	}
	if result.ReceiptID != "rcpt_1" {
		t.Errorf("ReceiptID = %q", result.ReceiptID)
	}
}

func TestListReceipts(t *testing.T) {
	srv := newReceiptServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	receipts, err := c.ListReceipts(context.Background(), "0xBUYER", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(receipts) != 2 {
		t.Errorf("len = %d", len(receipts))
	}
}
