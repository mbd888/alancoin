package arbitration

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func setupArbRouter() (*gin.Engine, *Service) {
	e := &mockEscrow{}
	r := &mockReputation{}
	svc := NewService(NewMemoryStore(), e, r, slog.Default())

	router := gin.New()
	h := NewHandler(svc)

	authed := router.Group("/v1", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xBuyer")
		c.Set("authTenantID", "ten_1")
		c.Next()
	})
	h.RegisterRoutes(authed)
	h.RegisterProtectedRoutes(authed)

	return router, svc
}

func TestHandlerFileCase(t *testing.T) {
	r, _ := setupArbRouter()

	body, _ := json.Marshal(map[string]interface{}{
		"escrowId":   "esc_1",
		"buyerAddr":  "0xBuyer",
		"sellerAddr": "0xSeller",
		"amount":     "100.00",
		"reason":     "Bad output",
		"contractId": "contract_1",
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/arbitration/cases", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201. body: %s", w.Code, w.Body.String())
	}
}

func TestHandlerFileCaseForbiddenForNonParty(t *testing.T) {
	r, _ := setupArbRouter()

	// Caller is 0xBuyer but filing case between other parties
	body, _ := json.Marshal(map[string]interface{}{
		"escrowId":   "esc_1",
		"buyerAddr":  "0xOtherBuyer",
		"sellerAddr": "0xOtherSeller",
		"amount":     "100.00",
		"reason":     "Attempting to file for others",
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/arbitration/cases", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (only buyer/seller can file). body: %s", w.Code, w.Body.String())
	}
}

func TestHandlerAutoResolve(t *testing.T) {
	r, svc := setupArbRouter()

	c, _ := svc.FileCase(context.Background(), "esc_1", "0xBuyer", "0xSeller", "200.00", "Dispute", "contract_1")

	body, _ := json.Marshal(map[string]interface{}{
		"contractPassed": false,
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/arbitration/cases/"+c.ID+"/auto-resolve", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200. body: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Resolved bool `json:"resolved"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if !resp.Resolved {
		t.Error("expected resolved=true")
	}
}

func TestHandlerGetCase(t *testing.T) {
	r, svc := setupArbRouter()

	c, _ := svc.FileCase(context.Background(), "esc_1", "0xBuyer", "0xSeller", "50.00", "Test", "")

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/arbitration/cases/"+c.ID, nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}

func TestHandlerGetCaseNotFound(t *testing.T) {
	r, _ := setupArbRouter()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/arbitration/cases/nonexistent", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestHandlerSubmitEvidence(t *testing.T) {
	r, svc := setupArbRouter()

	c, _ := svc.FileCase(context.Background(), "esc_1", "0xBuyer", "0xSeller", "100.00", "Test", "")

	body, _ := json.Marshal(map[string]interface{}{
		"submittedBy": "0xBuyer",
		"role":        "buyer",
		"type":        "text",
		"content":     "The output was incorrect",
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/arbitration/cases/"+c.ID+"/evidence", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201. body: %s", w.Code, w.Body.String())
	}
}

func TestHandlerListOpenCases(t *testing.T) {
	r, svc := setupArbRouter()

	svc.FileCase(context.Background(), "esc_1", "0xBuyer", "0xSeller", "100.00", "R1", "")
	svc.FileCase(context.Background(), "esc_2", "0xBuyer", "0xSeller", "200.00", "R2", "")

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/arbitration/cases?limit=10", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp struct {
		Cases []*Case `json:"cases"`
		Count int     `json:"count"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Count != 2 {
		t.Errorf("count = %d, want 2", resp.Count)
	}
}

// --- Handler: AssignArbiter ---

func TestHandlerAssignArbiter_Success(t *testing.T) {
	r, svc := setupArbRouter()

	c, _ := svc.FileCase(context.Background(), "esc_1", "0xBuyer", "0xSeller", "100.00", "Test", "")

	body, _ := json.Marshal(map[string]interface{}{
		"arbiterAddr": "0xArbiter",
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/arbitration/cases/"+c.ID+"/assign", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200. body: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["status"] != "assigned" {
		t.Errorf("status = %q, want assigned", resp["status"])
	}
}

func TestHandlerAssignArbiter_WrongState(t *testing.T) {
	r, svc := setupArbRouter()

	c, _ := svc.FileCase(context.Background(), "esc_1", "0xBuyer", "0xSeller", "100.00", "Test", "")
	// Assign once to move out of "open" state
	svc.AssignArbiter(context.Background(), c.ID, "0xArbiter1")

	body, _ := json.Marshal(map[string]interface{}{
		"arbiterAddr": "0xArbiter2",
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/arbitration/cases/"+c.ID+"/assign", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409. body: %s", w.Code, w.Body.String())
	}
}

func TestHandlerAssignArbiter_NotFound(t *testing.T) {
	r, _ := setupArbRouter()

	body, _ := json.Marshal(map[string]interface{}{
		"arbiterAddr": "0xArbiter",
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/arbitration/cases/nonexistent/assign", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404. body: %s", w.Code, w.Body.String())
	}
}

// --- Handler: Resolve ---

func TestHandlerResolve_BuyerWins(t *testing.T) {
	r, svc := setupArbRouter()

	c, _ := svc.FileCase(context.Background(), "esc_1", "0xBuyer", "0xSeller", "100.00", "Test", "")
	svc.AssignArbiter(context.Background(), c.ID, "0xArbiter")

	body, _ := json.Marshal(map[string]interface{}{
		"arbiterAddr": "0xArbiter",
		"outcome":     "buyer_wins",
		"decision":    "Buyer is right",
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/arbitration/cases/"+c.ID+"/resolve", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200. body: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["status"] != "resolved" {
		t.Errorf("status = %q, want resolved", resp["status"])
	}
}

func TestHandlerResolve_SellerWins(t *testing.T) {
	r, svc := setupArbRouter()

	c, _ := svc.FileCase(context.Background(), "esc_1", "0xBuyer", "0xSeller", "100.00", "Test", "")
	svc.AssignArbiter(context.Background(), c.ID, "0xArbiter")

	body, _ := json.Marshal(map[string]interface{}{
		"arbiterAddr": "0xArbiter",
		"outcome":     "seller_wins",
		"decision":    "Seller delivered correctly",
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/arbitration/cases/"+c.ID+"/resolve", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200. body: %s", w.Code, w.Body.String())
	}
}

func TestHandlerResolve_Split(t *testing.T) {
	r, svc := setupArbRouter()

	c, _ := svc.FileCase(context.Background(), "esc_1", "0xBuyer", "0xSeller", "100.00", "Test", "")
	svc.AssignArbiter(context.Background(), c.ID, "0xArbiter")

	body, _ := json.Marshal(map[string]interface{}{
		"arbiterAddr": "0xArbiter",
		"outcome":     "split",
		"splitPct":    40,
		"decision":    "Partial delivery",
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/arbitration/cases/"+c.ID+"/resolve", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200. body: %s", w.Code, w.Body.String())
	}
}

func TestHandlerResolve_WrongState(t *testing.T) {
	r, svc := setupArbRouter()

	c, _ := svc.FileCase(context.Background(), "esc_1", "0xBuyer", "0xSeller", "100.00", "Test", "")
	svc.AssignArbiter(context.Background(), c.ID, "0xArbiter")

	// Resolve first time
	svc.Resolve(context.Background(), c.ID, "0xArbiter", OutcomeBuyerWins, 0, "Done")

	// Try to resolve again
	body, _ := json.Marshal(map[string]interface{}{
		"arbiterAddr": "0xArbiter",
		"outcome":     "seller_wins",
		"decision":    "Double resolve",
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/arbitration/cases/"+c.ID+"/resolve", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409. body: %s", w.Code, w.Body.String())
	}
}

func TestHandlerResolve_Unauthorized(t *testing.T) {
	r, svc := setupArbRouter()

	c, _ := svc.FileCase(context.Background(), "esc_1", "0xBuyer", "0xSeller", "100.00", "Test", "")
	svc.AssignArbiter(context.Background(), c.ID, "0xArbiter")

	body, _ := json.Marshal(map[string]interface{}{
		"arbiterAddr": "0xWrongPerson",
		"outcome":     "buyer_wins",
		"decision":    "Unauthorized attempt",
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/arbitration/cases/"+c.ID+"/resolve", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403. body: %s", w.Code, w.Body.String())
	}
}

func TestHandlerResolve_InvalidOutcome(t *testing.T) {
	r, svc := setupArbRouter()

	c, _ := svc.FileCase(context.Background(), "esc_1", "0xBuyer", "0xSeller", "100.00", "Test", "")
	svc.AssignArbiter(context.Background(), c.ID, "0xArbiter")

	body, _ := json.Marshal(map[string]interface{}{
		"arbiterAddr": "0xArbiter",
		"outcome":     "invalid_outcome",
		"decision":    "Bad outcome type",
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/arbitration/cases/"+c.ID+"/resolve", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400. body: %s", w.Code, w.Body.String())
	}
}

// --- Handler: ListByEscrow ---

func TestHandlerListByEscrow_Success(t *testing.T) {
	r, svc := setupArbRouter()

	svc.FileCase(context.Background(), "esc_1", "0xBuyer", "0xSeller", "100.00", "R1", "")
	svc.FileCase(context.Background(), "esc_1", "0xBuyer", "0xSeller", "200.00", "R2", "")
	svc.FileCase(context.Background(), "esc_2", "0xBuyer", "0xSeller", "300.00", "R3", "")

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/arbitration/escrows/esc_1/cases", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200. body: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Cases []*Case `json:"cases"`
		Count int     `json:"count"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Count != 2 {
		t.Errorf("count = %d, want 2", resp.Count)
	}
}

func TestHandlerListByEscrow_Empty(t *testing.T) {
	r, _ := setupArbRouter()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/arbitration/escrows/esc_nonexistent/cases", nil)
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
