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
