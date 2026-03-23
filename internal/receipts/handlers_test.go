package receipts

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func setupReceiptRouter() (*gin.Engine, *Service) {
	svc := newTestService()

	router := gin.New()
	h := NewHandler(svc)

	g := router.Group("/v1")
	h.RegisterRoutes(g)

	return router, svc
}

// issueAndGetID issues a test receipt and returns its ID.
func issueAndGetID(t *testing.T, svc *Service) string {
	t.Helper()
	issueTestReceipt(t, svc, PathGateway, "ref_test", "confirmed")
	receipts, err := svc.ListByAgent(context.Background(), testBuyer, 10)
	if err != nil {
		t.Fatalf("ListByAgent: %v", err)
	}
	if len(receipts) == 0 {
		t.Fatal("no receipts found after issue")
	}
	return receipts[len(receipts)-1].ID
}

func TestHandlerGetReceipt_Success(t *testing.T) {
	r, svc := setupReceiptRouter()
	id := issueAndGetID(t, svc)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/receipts/"+id, nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200. body: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Receipt *Receipt `json:"receipt"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Receipt == nil {
		t.Fatal("expected receipt in response")
	}
	if resp.Receipt.ID != id {
		t.Errorf("receipt ID = %q, want %q", resp.Receipt.ID, id)
	}
}

func TestHandlerGetReceipt_NotFound(t *testing.T) {
	r, _ := setupReceiptRouter()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/receipts/nonexistent", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404. body: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error"] != "not_found" {
		t.Errorf("error = %q, want not_found", resp["error"])
	}
}

func TestHandlerListByAgent_Success(t *testing.T) {
	r, svc := setupReceiptRouter()

	// Issue multiple receipts for testBuyer
	issueTestReceipt(t, svc, PathGateway, "ref1", "confirmed")
	issueTestReceipt(t, svc, PathStream, "ref2", "confirmed")

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/agents/"+testBuyer+"/receipts", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200. body: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Receipts []*Receipt `json:"receipts"`
		Count    int        `json:"count"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Count != 2 {
		t.Errorf("count = %d, want 2", resp.Count)
	}
	if len(resp.Receipts) != 2 {
		t.Errorf("len(receipts) = %d, want 2", len(resp.Receipts))
	}
}

func TestHandlerListByAgent_Empty(t *testing.T) {
	r, _ := setupReceiptRouter()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/agents/0xNobody/receipts", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200. body: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Count int `json:"count"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Count != 0 {
		t.Errorf("count = %d, want 0", resp.Count)
	}
}

func TestHandlerListByAgent_WithLimit(t *testing.T) {
	r, svc := setupReceiptRouter()

	for i := 0; i < 5; i++ {
		issueTestReceipt(t, svc, PathGateway, "ref", "confirmed")
	}

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/agents/"+testBuyer+"/receipts?limit=2", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp struct {
		Count int `json:"count"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Count != 2 {
		t.Errorf("count = %d, want 2 (limited)", resp.Count)
	}
}

func TestHandlerVerifyReceipt_Valid(t *testing.T) {
	r, svc := setupReceiptRouter()
	id := issueAndGetID(t, svc)

	body, _ := json.Marshal(VerifyRequest{ReceiptID: id})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/receipts/verify", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200. body: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Verification *VerifyResponse `json:"verification"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Verification == nil {
		t.Fatal("expected verification in response")
	}
	if !resp.Verification.Valid {
		t.Errorf("expected valid=true, got false. error: %s", resp.Verification.Error)
	}
}

func TestHandlerVerifyReceipt_Tampered(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store, NewSigner(testSecret))

	router := gin.New()
	h := NewHandler(svc)
	g := router.Group("/v1")
	h.RegisterRoutes(g)

	// Issue a receipt
	issueTestReceipt(t, svc, PathEscrow, "esc_123", "confirmed")
	receipts, _ := svc.ListByAgent(context.Background(), testBuyer, 10)
	if len(receipts) == 0 {
		t.Fatal("no receipts found")
	}

	// Tamper with signature
	rcpt := receipts[0]
	rcpt.Signature = "deadbeef"
	store.mu.Lock()
	store.receipts[rcpt.ID] = rcpt
	store.mu.Unlock()

	body, _ := json.Marshal(VerifyRequest{ReceiptID: rcpt.ID})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/receipts/verify", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200. body: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Verification *VerifyResponse `json:"verification"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Verification.Valid {
		t.Error("expected valid=false for tampered receipt")
	}
	if resp.Verification.Error == "" {
		t.Error("expected error message for tampered receipt")
	}
}

func TestHandlerVerifyReceipt_NotFound(t *testing.T) {
	r, _ := setupReceiptRouter()

	body, _ := json.Marshal(VerifyRequest{ReceiptID: "nonexistent_id"})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/receipts/verify", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200. body: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Verification *VerifyResponse `json:"verification"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Verification.Valid {
		t.Error("expected valid=false for not-found receipt")
	}
	if resp.Verification.Error != ErrReceiptNotFound.Error() {
		t.Errorf("error = %q, want %q", resp.Verification.Error, ErrReceiptNotFound.Error())
	}
}

func TestHandlerVerifyReceipt_InvalidBody(t *testing.T) {
	r, _ := setupReceiptRouter()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/receipts/verify", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400. body: %s", w.Code, w.Body.String())
	}
}

func TestNewHandler(t *testing.T) {
	svc := newTestService()
	h := NewHandler(svc)
	if h == nil {
		t.Fatal("expected non-nil handler")
	}
}

func TestRegisterRoutes(t *testing.T) {
	svc := newTestService()
	h := NewHandler(svc)
	router := gin.New()
	g := router.Group("/v1")
	h.RegisterRoutes(g)

	routes := router.Routes()
	expected := map[string]string{
		"GET /v1/receipts/:id":             "",
		"GET /v1/agents/:address/receipts": "",
		"POST /v1/receipts/verify":         "",
	}
	for _, route := range routes {
		key := route.Method + " " + route.Path
		delete(expected, key)
	}
	if len(expected) > 0 {
		for k := range expected {
			t.Errorf("missing route: %s", k)
		}
	}
}
