package escrow

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func setupTestRouter() (*gin.Engine, *Service) {
	gin.SetMode(gin.TestMode)

	store := NewMemoryStore()
	ledger := &mockLedger{
		locked:   make(map[string]string),
		released: make(map[string]string),
		refunded: make(map[string]string),
	}
	svc := NewService(store, ledger)
	handler := NewHandler(svc)

	r := gin.New()
	v1 := r.Group("/v1")
	handler.RegisterRoutes(v1)

	// Simulate auth middleware by setting agent_address
	authGroup := v1.Group("")
	authGroup.Use(func(c *gin.Context) {
		// Use X-Agent-Address header as a test stand-in for auth middleware
		if addr := c.GetHeader("X-Agent-Address"); addr != "" {
			c.Set("authAgentAddr", addr)
		}
		c.Next()
	})
	handler.RegisterProtectedRoutes(authGroup)

	return r, svc
}

func TestHandler_CreateAndGetEscrow(t *testing.T) {
	router, _ := setupTestRouter()

	// Create escrow
	body, _ := json.Marshal(CreateRequest{
		BuyerAddr:  "0xaaaa000000000000000000000000000000000001",
		SellerAddr: "0xbbbb000000000000000000000000000000000002",
		Amount:     "1.50",
	})
	req := httptest.NewRequest("POST", "/v1/escrow", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Address", "0xaaaa000000000000000000000000000000000001")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("Expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var createResp struct {
		Escrow struct {
			ID     string `json:"id"`
			Status string `json:"status"`
			Amount string `json:"amount"`
		} `json:"escrow"`
	}
	json.Unmarshal(w.Body.Bytes(), &createResp)

	if createResp.Escrow.Status != "pending" {
		t.Errorf("Expected status pending, got %s", createResp.Escrow.Status)
	}
	if createResp.Escrow.Amount != "1.50" {
		t.Errorf("Expected amount 1.50, got %s", createResp.Escrow.Amount)
	}

	// Get escrow by ID
	w = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/v1/escrow/"+createResp.Escrow.ID, nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var getResp struct {
		Escrow struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"escrow"`
	}
	json.Unmarshal(w.Body.Bytes(), &getResp)

	if getResp.Escrow.ID != createResp.Escrow.ID {
		t.Errorf("Expected ID %s, got %s", createResp.Escrow.ID, getResp.Escrow.ID)
	}
}

func TestHandler_GetEscrowNotFound(t *testing.T) {
	router, _ := setupTestRouter()

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/escrow/esc_nonexistent", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected 404, got %d", w.Code)
	}
}

func TestHandler_CreateInvalidBody(t *testing.T) {
	router, _ := setupTestRouter()

	// Missing required fields
	body, _ := json.Marshal(map[string]string{"amount": "1.00"})
	req := httptest.NewRequest("POST", "/v1/escrow", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Address", "0xaaaa000000000000000000000000000000000001")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected 400 for missing fields, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_ConfirmEscrow(t *testing.T) {
	router, svc := setupTestRouter()

	// Create via service directly
	esc, _ := svc.Create(context.TODO(), CreateRequest{
		BuyerAddr:  "0xaaaa000000000000000000000000000000000001",
		SellerAddr: "0xbbbb000000000000000000000000000000000002",
		Amount:     "1.00",
	})

	// Confirm via HTTP
	req := httptest.NewRequest("POST", "/v1/escrow/"+esc.ID+"/confirm", nil)
	req.Header.Set("X-Agent-Address", "0xaaaa000000000000000000000000000000000001")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Escrow struct {
			Status string `json:"status"`
		} `json:"escrow"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp.Escrow.Status != "released" {
		t.Errorf("Expected status released, got %s", resp.Escrow.Status)
	}
}

func TestHandler_ConfirmUnauthorized(t *testing.T) {
	router, svc := setupTestRouter()

	esc, _ := svc.Create(context.TODO(), CreateRequest{
		BuyerAddr:  "0xaaaa000000000000000000000000000000000001",
		SellerAddr: "0xbbbb000000000000000000000000000000000002",
		Amount:     "1.00",
	})

	// Try to confirm as seller (should fail)
	req := httptest.NewRequest("POST", "/v1/escrow/"+esc.ID+"/confirm", nil)
	req.Header.Set("X-Agent-Address", "0xbbbb000000000000000000000000000000000002")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("Expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_DisputeEscrow(t *testing.T) {
	router, svc := setupTestRouter()

	esc, _ := svc.Create(context.TODO(), CreateRequest{
		BuyerAddr:  "0xaaaa000000000000000000000000000000000001",
		SellerAddr: "0xbbbb000000000000000000000000000000000002",
		Amount:     "1.00",
	})

	body, _ := json.Marshal(DisputeRequest{Reason: "service failed"})
	req := httptest.NewRequest("POST", "/v1/escrow/"+esc.ID+"/dispute", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Address", "0xaaaa000000000000000000000000000000000001")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Escrow struct {
			Status        string `json:"status"`
			DisputeReason string `json:"disputeReason"`
		} `json:"escrow"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp.Escrow.Status != "refunded" {
		t.Errorf("Expected status refunded, got %s", resp.Escrow.Status)
	}
	if resp.Escrow.DisputeReason != "service failed" {
		t.Errorf("Expected reason 'service failed', got %s", resp.Escrow.DisputeReason)
	}
}

func TestHandler_DisputeNoReason(t *testing.T) {
	router, svc := setupTestRouter()

	esc, _ := svc.Create(context.TODO(), CreateRequest{
		BuyerAddr:  "0xaaaa000000000000000000000000000000000001",
		SellerAddr: "0xbbbb000000000000000000000000000000000002",
		Amount:     "1.00",
	})

	// Empty body (no reason)
	req := httptest.NewRequest("POST", "/v1/escrow/"+esc.ID+"/dispute", bytes.NewReader([]byte("{}")))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Address", "0xaaaa000000000000000000000000000000000001")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected 400 for missing reason, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_MarkDelivered(t *testing.T) {
	router, svc := setupTestRouter()

	esc, _ := svc.Create(context.TODO(), CreateRequest{
		BuyerAddr:  "0xaaaa000000000000000000000000000000000001",
		SellerAddr: "0xbbbb000000000000000000000000000000000002",
		Amount:     "1.00",
	})

	req := httptest.NewRequest("POST", "/v1/escrow/"+esc.ID+"/deliver", nil)
	req.Header.Set("X-Agent-Address", "0xbbbb000000000000000000000000000000000002")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Escrow struct {
			Status string `json:"status"`
		} `json:"escrow"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp.Escrow.Status != "delivered" {
		t.Errorf("Expected status delivered, got %s", resp.Escrow.Status)
	}
}

func TestHandler_ListEscrows(t *testing.T) {
	router, svc := setupTestRouter()

	svc.Create(context.TODO(), CreateRequest{BuyerAddr: "0xaaaa000000000000000000000000000000000001", SellerAddr: "0xcccc000000000000000000000000000000000003", Amount: "1.00"})
	svc.Create(context.TODO(), CreateRequest{BuyerAddr: "0xaaaa000000000000000000000000000000000001", SellerAddr: "0xcccc000000000000000000000000000000000004", Amount: "2.00"})
	svc.Create(context.TODO(), CreateRequest{BuyerAddr: "0xdddd000000000000000000000000000000000008", SellerAddr: "0xs3", Amount: "3.00"})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/agents/0xaaaa000000000000000000000000000000000001/escrows", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Escrows []json.RawMessage `json:"escrows"`
		Count   int               `json:"count"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp.Count != 2 {
		t.Errorf("Expected 2 escrows for 0xaaaa000000000000000000000000000000000001, got %d", resp.Count)
	}
}

func TestHandler_ListEscrowsWithLimit(t *testing.T) {
	router, svc := setupTestRouter()

	for i := 0; i < 5; i++ {
		svc.Create(context.TODO(), CreateRequest{BuyerAddr: "0xaaaa000000000000000000000000000000000001", SellerAddr: "0xbbbb000000000000000000000000000000000002", Amount: "1.00"})
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/agents/0xaaaa000000000000000000000000000000000001/escrows?limit=2", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d", w.Code)
	}

	var resp struct {
		Count int `json:"count"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp.Count != 2 {
		t.Errorf("Expected limit of 2, got %d", resp.Count)
	}
}

func TestHandler_DoubleConfirmReturnsConflict(t *testing.T) {
	router, svc := setupTestRouter()

	esc, _ := svc.Create(context.TODO(), CreateRequest{
		BuyerAddr:  "0xaaaa000000000000000000000000000000000001",
		SellerAddr: "0xbbbb000000000000000000000000000000000002",
		Amount:     "1.00",
	})

	// First confirm
	req := httptest.NewRequest("POST", "/v1/escrow/"+esc.ID+"/confirm", nil)
	req.Header.Set("X-Agent-Address", "0xaaaa000000000000000000000000000000000001")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("First confirm: expected 200, got %d", w.Code)
	}

	// Second confirm → 409 Conflict
	req = httptest.NewRequest("POST", "/v1/escrow/"+esc.ID+"/confirm", nil)
	req.Header.Set("X-Agent-Address", "0xaaaa000000000000000000000000000000000001")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Errorf("Double confirm: expected 409, got %d: %s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Handler edge cases: authorization
// ---------------------------------------------------------------------------

func TestHandler_DeliverUnauthorized(t *testing.T) {
	router, svc := setupTestRouter()

	esc, _ := svc.Create(context.TODO(), CreateRequest{
		BuyerAddr:  "0xaaaa000000000000000000000000000000000001",
		SellerAddr: "0xbbbb000000000000000000000000000000000002",
		Amount:     "1.00",
	})

	// Buyer tries to deliver (only seller should be able to)
	req := httptest.NewRequest("POST", "/v1/escrow/"+esc.ID+"/deliver", nil)
	req.Header.Set("X-Agent-Address", "0xaaaa000000000000000000000000000000000001")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("Expected 403 when buyer tries to deliver, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_DisputeUnauthorized(t *testing.T) {
	router, svc := setupTestRouter()

	esc, _ := svc.Create(context.TODO(), CreateRequest{
		BuyerAddr:  "0xaaaa000000000000000000000000000000000001",
		SellerAddr: "0xbbbb000000000000000000000000000000000002",
		Amount:     "1.00",
	})

	body, _ := json.Marshal(DisputeRequest{Reason: "whatever"})
	req := httptest.NewRequest("POST", "/v1/escrow/"+esc.ID+"/dispute", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Address", "0xbbbb000000000000000000000000000000000002") // seller can't dispute
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("Expected 403 when seller tries to dispute, got %d: %s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Handler edge cases: 404 on nonexistent escrows
// ---------------------------------------------------------------------------

func TestHandler_ConfirmNotFound(t *testing.T) {
	router, _ := setupTestRouter()

	req := httptest.NewRequest("POST", "/v1/escrow/esc_ghost/confirm", nil)
	req.Header.Set("X-Agent-Address", "0xaaaa000000000000000000000000000000000001")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_DisputeNotFound(t *testing.T) {
	router, _ := setupTestRouter()

	body, _ := json.Marshal(DisputeRequest{Reason: "reason"})
	req := httptest.NewRequest("POST", "/v1/escrow/esc_ghost/dispute", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Address", "0xaaaa000000000000000000000000000000000001")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_DeliverNotFound(t *testing.T) {
	router, _ := setupTestRouter()

	req := httptest.NewRequest("POST", "/v1/escrow/esc_ghost/deliver", nil)
	req.Header.Set("X-Agent-Address", "0xbbbb000000000000000000000000000000000002")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Handler edge cases: conflict states
// ---------------------------------------------------------------------------

func TestHandler_DeliverAlreadyReleased(t *testing.T) {
	router, svc := setupTestRouter()

	esc, _ := svc.Create(context.TODO(), CreateRequest{
		BuyerAddr:  "0xaaaa000000000000000000000000000000000001",
		SellerAddr: "0xbbbb000000000000000000000000000000000002",
		Amount:     "1.00",
	})

	// Confirm (release) the escrow
	svc.Confirm(context.TODO(), esc.ID, "0xaaaa000000000000000000000000000000000001")

	// Try to deliver after release
	req := httptest.NewRequest("POST", "/v1/escrow/"+esc.ID+"/deliver", nil)
	req.Header.Set("X-Agent-Address", "0xbbbb000000000000000000000000000000000002")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("Expected 409 when delivering released escrow, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_DisputeAlreadyReleased(t *testing.T) {
	router, svc := setupTestRouter()

	esc, _ := svc.Create(context.TODO(), CreateRequest{
		BuyerAddr:  "0xaaaa000000000000000000000000000000000001",
		SellerAddr: "0xbbbb000000000000000000000000000000000002",
		Amount:     "1.00",
	})

	// Confirm (release) the escrow
	svc.Confirm(context.TODO(), esc.ID, "0xaaaa000000000000000000000000000000000001")

	body, _ := json.Marshal(DisputeRequest{Reason: "too late"})
	req := httptest.NewRequest("POST", "/v1/escrow/"+esc.ID+"/dispute", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Address", "0xaaaa000000000000000000000000000000000001")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("Expected 409 when disputing released escrow, got %d: %s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Handler edge cases: full lifecycle through HTTP
// ---------------------------------------------------------------------------

func TestHandler_FullLifecycle_CreateDeliverConfirm(t *testing.T) {
	router, _ := setupTestRouter()

	// 1. Create
	body, _ := json.Marshal(CreateRequest{
		BuyerAddr:  "0xaaaa000000000000000000000000000000000001",
		SellerAddr: "0xbbbb000000000000000000000000000000000002",
		Amount:     "2.50",
		ServiceID:  "svc_test",
	})
	req := httptest.NewRequest("POST", "/v1/escrow", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Address", "0xaaaa000000000000000000000000000000000001")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("Create: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var createResp struct {
		Escrow struct {
			ID        string `json:"id"`
			Status    string `json:"status"`
			BuyerAddr string `json:"buyerAddr"`
			Amount    string `json:"amount"`
		} `json:"escrow"`
	}
	json.Unmarshal(w.Body.Bytes(), &createResp)

	if createResp.Escrow.Status != "pending" {
		t.Fatalf("Create: expected pending, got %s", createResp.Escrow.Status)
	}
	escrowID := createResp.Escrow.ID

	// 2. Deliver (seller)
	req = httptest.NewRequest("POST", "/v1/escrow/"+escrowID+"/deliver", nil)
	req.Header.Set("X-Agent-Address", "0xbbbb000000000000000000000000000000000002")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Deliver: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var deliverResp struct {
		Escrow struct {
			Status string `json:"status"`
		} `json:"escrow"`
	}
	json.Unmarshal(w.Body.Bytes(), &deliverResp)
	if deliverResp.Escrow.Status != "delivered" {
		t.Fatalf("Deliver: expected delivered, got %s", deliverResp.Escrow.Status)
	}

	// 3. Confirm (buyer)
	req = httptest.NewRequest("POST", "/v1/escrow/"+escrowID+"/confirm", nil)
	req.Header.Set("X-Agent-Address", "0xaaaa000000000000000000000000000000000001")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Confirm: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var confirmResp struct {
		Escrow struct {
			Status string `json:"status"`
		} `json:"escrow"`
	}
	json.Unmarshal(w.Body.Bytes(), &confirmResp)
	if confirmResp.Escrow.Status != "released" {
		t.Fatalf("Confirm: expected released, got %s", confirmResp.Escrow.Status)
	}

	// 4. Verify via GET
	req = httptest.NewRequest("GET", "/v1/escrow/"+escrowID, nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Get: expected 200, got %d", w.Code)
	}

	var getResp struct {
		Escrow struct {
			Status string `json:"status"`
		} `json:"escrow"`
	}
	json.Unmarshal(w.Body.Bytes(), &getResp)
	if getResp.Escrow.Status != "released" {
		t.Errorf("Get: expected released, got %s", getResp.Escrow.Status)
	}
}

func TestHandler_FullLifecycle_CreateDeliverDispute(t *testing.T) {
	router, _ := setupTestRouter()

	// 1. Create
	body, _ := json.Marshal(CreateRequest{
		BuyerAddr:  "0xaaaa000000000000000000000000000000000001",
		SellerAddr: "0xbbbb000000000000000000000000000000000002",
		Amount:     "3.00",
	})
	req := httptest.NewRequest("POST", "/v1/escrow", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Address", "0xaaaa000000000000000000000000000000000001")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	var createResp struct {
		Escrow struct {
			ID string `json:"id"`
		} `json:"escrow"`
	}
	json.Unmarshal(w.Body.Bytes(), &createResp)
	escrowID := createResp.Escrow.ID

	// 2. Deliver (seller)
	req = httptest.NewRequest("POST", "/v1/escrow/"+escrowID+"/deliver", nil)
	req.Header.Set("X-Agent-Address", "0xbbbb000000000000000000000000000000000002")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Deliver: expected 200, got %d", w.Code)
	}

	// 3. Dispute after delivery (buyer protection)
	body, _ = json.Marshal(DisputeRequest{Reason: "output was gibberish"})
	req = httptest.NewRequest("POST", "/v1/escrow/"+escrowID+"/dispute", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Address", "0xaaaa000000000000000000000000000000000001")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Dispute: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var disputeResp struct {
		Escrow struct {
			Status        string `json:"status"`
			DisputeReason string `json:"disputeReason"`
		} `json:"escrow"`
	}
	json.Unmarshal(w.Body.Bytes(), &disputeResp)
	if disputeResp.Escrow.Status != "refunded" {
		t.Errorf("Expected refunded, got %s", disputeResp.Escrow.Status)
	}
	if disputeResp.Escrow.DisputeReason != "output was gibberish" {
		t.Errorf("Expected dispute reason, got %s", disputeResp.Escrow.DisputeReason)
	}
}

// ---------------------------------------------------------------------------
// Handler edge cases: malformed input
// ---------------------------------------------------------------------------

func TestHandler_CreateMalformedJSON(t *testing.T) {
	router, _ := setupTestRouter()

	req := httptest.NewRequest("POST", "/v1/escrow", bytes.NewReader([]byte("not json")))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Address", "0xaaaa000000000000000000000000000000000001")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected 400 for malformed JSON, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_DisputeMalformedJSON(t *testing.T) {
	router, svc := setupTestRouter()

	esc, _ := svc.Create(context.TODO(), CreateRequest{
		BuyerAddr:  "0xaaaa000000000000000000000000000000000001",
		SellerAddr: "0xbbbb000000000000000000000000000000000002",
		Amount:     "1.00",
	})

	req := httptest.NewRequest("POST", "/v1/escrow/"+esc.ID+"/dispute", bytes.NewReader([]byte("bad")))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Address", "0xaaaa000000000000000000000000000000000001")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected 400 for malformed dispute body, got %d: %s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Handler edge cases: list empty results
// ---------------------------------------------------------------------------

func TestHandler_ListEscrowsEmpty(t *testing.T) {
	router, _ := setupTestRouter()

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/agents/0xnobody/escrows", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200 for empty list, got %d", w.Code)
	}

	var resp struct {
		Escrows []json.RawMessage `json:"escrows"`
		Count   int               `json:"count"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp.Count != 0 {
		t.Errorf("Expected 0 escrows, got %d", resp.Count)
	}
}

func TestHandler_ListEscrowsInvalidLimit(t *testing.T) {
	router, svc := setupTestRouter()

	svc.Create(context.TODO(), CreateRequest{BuyerAddr: "0xaaaa000000000000000000000000000000000001", SellerAddr: "0xbbbb000000000000000000000000000000000002", Amount: "1.00"})

	// Invalid limit should be ignored (uses default 50)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/agents/0xaaaa000000000000000000000000000000000001/escrows?limit=abc", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d", w.Code)
	}

	var resp struct {
		Count int `json:"count"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp.Count != 1 {
		t.Errorf("Expected 1 escrow with default limit, got %d", resp.Count)
	}
}

func TestHandler_ListEscrowsNegativeLimit(t *testing.T) {
	router, svc := setupTestRouter()

	svc.Create(context.TODO(), CreateRequest{BuyerAddr: "0xaaaa000000000000000000000000000000000001", SellerAddr: "0xbbbb000000000000000000000000000000000002", Amount: "1.00"})

	// Negative limit should be ignored (uses default)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/agents/0xaaaa000000000000000000000000000000000001/escrows?limit=-5", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d", w.Code)
	}

	var resp struct {
		Count int `json:"count"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp.Count != 1 {
		t.Errorf("Expected 1 escrow with default limit, got %d", resp.Count)
	}
}

// ---------------------------------------------------------------------------
// Handler: response JSON structure verification
// ---------------------------------------------------------------------------

func TestHandler_CreateResponseStructure(t *testing.T) {
	router, _ := setupTestRouter()

	body, _ := json.Marshal(CreateRequest{
		BuyerAddr:  "0xaaaa000000000000000000000000000000000001",
		SellerAddr: "0xbbbb000000000000000000000000000000000002",
		Amount:     "5.00",
		ServiceID:  "svc_abc",
	})
	req := httptest.NewRequest("POST", "/v1/escrow", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Address", "0xaaaa000000000000000000000000000000000001")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("Expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Escrow struct {
			ID            string `json:"id"`
			BuyerAddr     string `json:"buyerAddr"`
			SellerAddr    string `json:"sellerAddr"`
			Amount        string `json:"amount"`
			ServiceID     string `json:"serviceId"`
			Status        string `json:"status"`
			AutoReleaseAt string `json:"autoReleaseAt"`
			CreatedAt     string `json:"createdAt"`
			UpdatedAt     string `json:"updatedAt"`
		} `json:"escrow"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to parse response JSON: %v", err)
	}

	if resp.Escrow.ID == "" {
		t.Error("Expected non-empty escrow ID")
	}
	if resp.Escrow.BuyerAddr != "0xaaaa000000000000000000000000000000000001" {
		t.Errorf("Expected buyerAddr 0xaaaa000000000000000000000000000000000001, got %s", resp.Escrow.BuyerAddr)
	}
	if resp.Escrow.SellerAddr != "0xbbbb000000000000000000000000000000000002" {
		t.Errorf("Expected sellerAddr 0xbbbb000000000000000000000000000000000002, got %s", resp.Escrow.SellerAddr)
	}
	if resp.Escrow.Amount != "5.00" {
		t.Errorf("Expected amount 5.00, got %s", resp.Escrow.Amount)
	}
	if resp.Escrow.ServiceID != "svc_abc" {
		t.Errorf("Expected serviceId svc_abc, got %s", resp.Escrow.ServiceID)
	}
	if resp.Escrow.Status != "pending" {
		t.Errorf("Expected status pending, got %s", resp.Escrow.Status)
	}
	if resp.Escrow.AutoReleaseAt == "" {
		t.Error("Expected non-empty autoReleaseAt")
	}
	if resp.Escrow.CreatedAt == "" {
		t.Error("Expected non-empty createdAt")
	}
}

func TestHandler_ErrorResponseStructure(t *testing.T) {
	router, _ := setupTestRouter()

	// 404 response structure
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/escrow/esc_fake", nil)
	router.ServeHTTP(w, req)

	var errResp struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("Failed to parse error JSON: %v", err)
	}
	if errResp.Error == "" {
		t.Error("Expected non-empty error code")
	}
	if errResp.Message == "" {
		t.Error("Expected non-empty message")
	}
}

// ---------------------------------------------------------------------------
// Handler: unicode and special characters
// ---------------------------------------------------------------------------

func TestHandler_DisputeUnicodeReason(t *testing.T) {
	router, svc := setupTestRouter()

	esc, _ := svc.Create(context.TODO(), CreateRequest{
		BuyerAddr:  "0xaaaa000000000000000000000000000000000001",
		SellerAddr: "0xbbbb000000000000000000000000000000000002",
		Amount:     "1.00",
	})

	reason := "Service returned \u2603 garbage \u00e9\u00e8\u00ea with \u4e2d\u6587 characters"
	body, _ := json.Marshal(DisputeRequest{Reason: reason})
	req := httptest.NewRequest("POST", "/v1/escrow/"+esc.ID+"/dispute", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Address", "0xaaaa000000000000000000000000000000000001")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Escrow struct {
			DisputeReason string `json:"disputeReason"`
		} `json:"escrow"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Escrow.DisputeReason != reason {
		t.Errorf("Expected unicode reason preserved, got %s", resp.Escrow.DisputeReason)
	}
}

// ---------------------------------------------------------------------------
// Handler: create with all optional fields
// ---------------------------------------------------------------------------

func TestHandler_CreateWithAllFields(t *testing.T) {
	router, _ := setupTestRouter()

	body, _ := json.Marshal(map[string]string{
		"buyerAddr":    "0xaaaa000000000000000000000000000000000001",
		"sellerAddr":   "0xbbbb000000000000000000000000000000000002",
		"amount":       "7.50",
		"serviceId":    "svc_full",
		"sessionKeyId": "sk_123",
		"autoRelease":  "1h",
	})
	req := httptest.NewRequest("POST", "/v1/escrow", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Address", "0xaaaa000000000000000000000000000000000001")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("Expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Escrow struct {
			ServiceID    string `json:"serviceId"`
			SessionKeyID string `json:"sessionKeyId"`
		} `json:"escrow"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp.Escrow.ServiceID != "svc_full" {
		t.Errorf("Expected serviceId svc_full, got %s", resp.Escrow.ServiceID)
	}
	if resp.Escrow.SessionKeyID != "sk_123" {
		t.Errorf("Expected sessionKeyId sk_123, got %s", resp.Escrow.SessionKeyID)
	}
}

// ---------------------------------------------------------------------------
// Handler: addresses are lowercased in response
// ---------------------------------------------------------------------------

func TestHandler_CreateLowercasesAddresses(t *testing.T) {
	router, _ := setupTestRouter()

	body, _ := json.Marshal(CreateRequest{
		BuyerAddr:  "0xAAAA00000000000000000000000000000000000A",
		SellerAddr: "0xBBBB00000000000000000000000000000000000B",
		Amount:     "1.00",
	})
	req := httptest.NewRequest("POST", "/v1/escrow", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Address", "0xAAAA00000000000000000000000000000000000A")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("Expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Escrow struct {
			BuyerAddr  string `json:"buyerAddr"`
			SellerAddr string `json:"sellerAddr"`
		} `json:"escrow"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp.Escrow.BuyerAddr != "0xaaaa00000000000000000000000000000000000a" {
		t.Errorf("Expected lowercased buyer, got %s", resp.Escrow.BuyerAddr)
	}
	if resp.Escrow.SellerAddr != "0xbbbb00000000000000000000000000000000000b" {
		t.Errorf("Expected lowercased seller, got %s", resp.Escrow.SellerAddr)
	}
}

// ---------------------------------------------------------------------------
// Handler: list escrows returns seller's escrows too
// ---------------------------------------------------------------------------

func TestHandler_ListEscrowsAsSeller(t *testing.T) {
	router, svc := setupTestRouter()

	svc.Create(context.TODO(), CreateRequest{BuyerAddr: "0xeeee000000000000000000000000000000000005", SellerAddr: "0xbbbb000000000000000000000000000000000002", Amount: "1.00"})
	svc.Create(context.TODO(), CreateRequest{BuyerAddr: "0xeeee000000000000000000000000000000000006", SellerAddr: "0xbbbb000000000000000000000000000000000002", Amount: "2.00"})
	svc.Create(context.TODO(), CreateRequest{BuyerAddr: "0xeeee000000000000000000000000000000000007", SellerAddr: "0xdddd000000000000000000000000000000000008", Amount: "3.00"})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/agents/0xbbbb000000000000000000000000000000000002/escrows", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d", w.Code)
	}

	var resp struct {
		Count int `json:"count"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp.Count != 2 {
		t.Errorf("Expected 2 escrows for seller, got %d", resp.Count)
	}
}

// ---------------------------------------------------------------------------
// Handler: empty body on create (different from invalid body)
// ---------------------------------------------------------------------------

func TestHandler_CreateEmptyBody(t *testing.T) {
	router, _ := setupTestRouter()

	req := httptest.NewRequest("POST", "/v1/escrow", nil)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Address", "0xaaaa000000000000000000000000000000000001")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected 400 for empty body, got %d: %s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Handler: double dispute returns 409
// ---------------------------------------------------------------------------

func TestHandler_DoubleDisputeReturnsConflict(t *testing.T) {
	router, svc := setupTestRouter()

	esc, _ := svc.Create(context.TODO(), CreateRequest{
		BuyerAddr:  "0xaaaa000000000000000000000000000000000001",
		SellerAddr: "0xbbbb000000000000000000000000000000000002",
		Amount:     "1.00",
	})

	body, _ := json.Marshal(DisputeRequest{Reason: "bad"})

	// First dispute
	req := httptest.NewRequest("POST", "/v1/escrow/"+esc.ID+"/dispute", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Address", "0xaaaa000000000000000000000000000000000001")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("First dispute: expected 200, got %d", w.Code)
	}

	// Second dispute → 409
	body, _ = json.Marshal(DisputeRequest{Reason: "bad again"})
	req = httptest.NewRequest("POST", "/v1/escrow/"+esc.ID+"/dispute", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Address", "0xaaaa000000000000000000000000000000000001")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("Double dispute: expected 409, got %d: %s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Handler: double deliver returns 409
// ---------------------------------------------------------------------------

func TestHandler_DoubleDeliverReturnsConflict(t *testing.T) {
	router, svc := setupTestRouter()

	esc, _ := svc.Create(context.TODO(), CreateRequest{
		BuyerAddr:  "0xaaaa000000000000000000000000000000000001",
		SellerAddr: "0xbbbb000000000000000000000000000000000002",
		Amount:     "1.00",
	})

	// First deliver
	req := httptest.NewRequest("POST", "/v1/escrow/"+esc.ID+"/deliver", nil)
	req.Header.Set("X-Agent-Address", "0xbbbb000000000000000000000000000000000002")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("First deliver: expected 200, got %d", w.Code)
	}

	// Second deliver → 409
	req = httptest.NewRequest("POST", "/v1/escrow/"+esc.ID+"/deliver", nil)
	req.Header.Set("X-Agent-Address", "0xbbbb000000000000000000000000000000000002")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("Double deliver: expected 409, got %d: %s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Handler: create escrow requires authenticated agent to be the buyer
// ---------------------------------------------------------------------------

func TestHandler_CreateAsWrongAgent(t *testing.T) {
	router, _ := setupTestRouter()

	body, _ := json.Marshal(CreateRequest{
		BuyerAddr:  "0xaaaa000000000000000000000000000000000001",
		SellerAddr: "0xbbbb000000000000000000000000000000000002",
		Amount:     "1.00",
	})
	req := httptest.NewRequest("POST", "/v1/escrow", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Address", "0xcccc000000000000000000000000000000000009") // Not the buyer
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("Expected 403 when creating escrow as wrong agent, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_CreateAsWrongAgentCaseInsensitive(t *testing.T) {
	router, _ := setupTestRouter()

	body, _ := json.Marshal(CreateRequest{
		BuyerAddr:  "0xAAAA000000000000000000000000000000000001",
		SellerAddr: "0xbbbb000000000000000000000000000000000002",
		Amount:     "1.00",
	})
	req := httptest.NewRequest("POST", "/v1/escrow", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Address", "0xaaaa000000000000000000000000000000000001") // Same address, different case
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("Expected 201 with case-insensitive match, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_CreateWithNoAuth(t *testing.T) {
	router, _ := setupTestRouter()

	body, _ := json.Marshal(CreateRequest{
		BuyerAddr:  "0xaaaa000000000000000000000000000000000001",
		SellerAddr: "0xbbbb000000000000000000000000000000000002",
		Amount:     "1.00",
	})
	req := httptest.NewRequest("POST", "/v1/escrow", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	// No X-Agent-Address header -> empty callerAddr
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("Expected 403 when creating escrow without auth, got %d: %s", w.Code, w.Body.String())
	}
}

// Ensure the mock ledger is used (compile-time interface check)
var _ LedgerService = (*mockLedger)(nil)
var _ LedgerService = (*failingLedger)(nil)
