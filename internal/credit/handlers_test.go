package credit

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// ---------------------------------------------------------------------------
// Test router setup (follows escrow/handlers_test.go pattern)
// ---------------------------------------------------------------------------

func setupHandlerTestRouter() (*gin.Engine, *Service, *mockReputationProvider, *mockMetricsProvider) {
	gin.SetMode(gin.TestMode)

	store := NewMemoryStore()
	rep := newMockReputation()
	met := newMockMetrics()
	ledger := newMockLedger()
	scorer := NewScorer()
	svc := NewService(store, scorer, rep, met, ledger)
	handler := NewHandler(svc)

	r := gin.New()
	v1 := r.Group("/v1")
	handler.RegisterRoutes(v1)

	// Simulate auth middleware
	authGroup := v1.Group("")
	authGroup.Use(func(c *gin.Context) {
		if addr := c.GetHeader("X-Agent-Address"); addr != "" {
			c.Set("authAgentAddr", addr)
		}
		c.Next()
	})
	handler.RegisterProtectedRoutes(authGroup)

	// Admin routes
	adminGroup := v1.Group("/admin")
	handler.RegisterAdminRoutes(adminGroup)

	return r, svc, rep, met
}

// ---------------------------------------------------------------------------
// GET /v1/agents/:address/credit
// ---------------------------------------------------------------------------

func TestHandler_GetCreditLine_200(t *testing.T) {
	router, svc, rep, met := setupHandlerTestRouter()

	addr := "0xaaaa000000000000000000000000000000000001"
	setupEligibleAgent(rep, met, addr, 65.0, "trusted")
	svc.Apply(context.Background(), addr)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/agents/"+addr+"/credit", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		CreditLine struct {
			ID           string  `json:"id"`
			AgentAddr    string  `json:"agentAddr"`
			CreditLimit  string  `json:"creditLimit"`
			CreditUsed   string  `json:"creditUsed"`
			Status       string  `json:"status"`
			InterestRate float64 `json:"interestRate"`
		} `json:"credit_line"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if resp.CreditLine.ID == "" {
		t.Error("Expected non-empty credit line ID")
	}
	if resp.CreditLine.AgentAddr != addr {
		t.Errorf("Expected agent addr %s, got %s", addr, resp.CreditLine.AgentAddr)
	}
	if resp.CreditLine.Status != "active" {
		t.Errorf("Expected status active, got %s", resp.CreditLine.Status)
	}
	if resp.CreditLine.InterestRate <= 0 {
		t.Error("Expected positive interest rate")
	}
}

func TestHandler_GetCreditLine_404(t *testing.T) {
	router, _, _, _ := setupHandlerTestRouter()

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/agents/0xnonexistent/credit", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected 404, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp.Error != "not_found" {
		t.Errorf("Expected error code not_found, got %s", resp.Error)
	}
}

// ---------------------------------------------------------------------------
// POST /v1/agents/:address/credit/apply
// ---------------------------------------------------------------------------

func TestHandler_ApplyForCredit_Approved(t *testing.T) {
	router, _, rep, met := setupHandlerTestRouter()

	addr := "0xaaaa000000000000000000000000000000000001"
	setupEligibleAgent(rep, met, addr, 65.0, "trusted")

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/agents/"+addr+"/credit/apply", nil)
	req.Header.Set("X-Agent-Address", addr)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("Expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		CreditLine struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"credit_line"`
		Evaluation struct {
			Eligible    bool    `json:"eligible"`
			CreditLimit float64 `json:"creditLimit"`
		} `json:"evaluation"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if resp.CreditLine.ID == "" {
		t.Error("Expected non-empty credit line ID")
	}
	if resp.CreditLine.Status != "active" {
		t.Errorf("Expected active, got %s", resp.CreditLine.Status)
	}
	if !resp.Evaluation.Eligible {
		t.Error("Expected eligible=true in evaluation")
	}
	if resp.Evaluation.CreditLimit <= 0 {
		t.Error("Expected positive credit limit")
	}
}

func TestHandler_ApplyForCredit_NewAgentNotEligible(t *testing.T) {
	router, _, rep, met := setupHandlerTestRouter()

	addr := "0xbbbb000000000000000000000000000000000002"
	rep.setScore(addr, 5.0, "new")
	met.setMetrics(addr, 2, 1.0, 3, 10)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/agents/"+addr+"/credit/apply", nil)
	req.Header.Set("X-Agent-Address", addr)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("Expected 422, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Error      string `json:"error"`
		Evaluation struct {
			Eligible bool   `json:"eligible"`
			Reason   string `json:"reason"`
		} `json:"evaluation"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp.Error != "not_eligible" {
		t.Errorf("Expected error not_eligible, got %s", resp.Error)
	}
	if resp.Evaluation.Eligible {
		t.Error("Expected eligible=false")
	}
	if resp.Evaluation.Reason == "" {
		t.Error("Expected rejection reason in evaluation")
	}
}

func TestHandler_ApplyForCredit_Unauthorized(t *testing.T) {
	router, _, rep, met := setupHandlerTestRouter()

	addr := "0xaaaa000000000000000000000000000000000001"
	setupEligibleAgent(rep, met, addr, 65.0, "trusted")

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/agents/"+addr+"/credit/apply", nil)
	req.Header.Set("X-Agent-Address", "0xdifferent_agent")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("Expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_ApplyForCredit_AlreadyExists(t *testing.T) {
	router, _, rep, met := setupHandlerTestRouter()

	addr := "0xaaaa000000000000000000000000000000000001"
	setupEligibleAgent(rep, met, addr, 65.0, "trusted")

	// First apply
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/agents/"+addr+"/credit/apply", nil)
	req.Header.Set("X-Agent-Address", addr)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("First apply: expected 201, got %d", w.Code)
	}

	// Second apply
	w = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/v1/agents/"+addr+"/credit/apply", nil)
	req.Header.Set("X-Agent-Address", addr)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Errorf("Second apply: expected 409, got %d: %s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// POST /v1/agents/:address/credit/repay
// ---------------------------------------------------------------------------

func TestHandler_RepayCredit_ValidAmount(t *testing.T) {
	router, _, rep, met := setupHandlerTestRouter()

	addr := "0xaaaa000000000000000000000000000000000001"
	setupEligibleAgent(rep, met, addr, 65.0, "trusted")

	// Apply first
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/agents/"+addr+"/credit/apply", nil)
	req.Header.Set("X-Agent-Address", addr)
	router.ServeHTTP(w, req)

	// Repay
	body, _ := json.Marshal(RepaymentRequest{Amount: "5.00"})
	w = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/v1/agents/"+addr+"/credit/repay", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Address", addr)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_RepayCredit_MissingAmount(t *testing.T) {
	router, _, rep, met := setupHandlerTestRouter()

	addr := "0xaaaa000000000000000000000000000000000001"
	setupEligibleAgent(rep, met, addr, 65.0, "trusted")

	// Apply first
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/agents/"+addr+"/credit/apply", nil)
	req.Header.Set("X-Agent-Address", addr)
	router.ServeHTTP(w, req)

	// Repay with empty body
	w = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/v1/agents/"+addr+"/credit/repay", bytes.NewReader([]byte("{}")))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Address", addr)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected 400 for missing amount, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_RepayCredit_NotFound(t *testing.T) {
	router, _, _, _ := setupHandlerTestRouter()

	addr := "0xaaaa000000000000000000000000000000000001"

	body, _ := json.Marshal(RepaymentRequest{Amount: "5.00"})
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/agents/"+addr+"/credit/repay", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Address", addr)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected 404 for nonexistent credit line, got %d: %s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// GET /v1/credit/active
// ---------------------------------------------------------------------------

func TestHandler_ListActiveCredits(t *testing.T) {
	router, _, rep, met := setupHandlerTestRouter()

	// Create two credit lines
	agents := []string{
		"0xaaaa000000000000000000000000000000000001",
		"0xbbbb000000000000000000000000000000000002",
	}
	for _, addr := range agents {
		setupEligibleAgent(rep, met, addr, 65.0, "trusted")
		w := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/v1/agents/"+addr+"/credit/apply", nil)
		req.Header.Set("X-Agent-Address", addr)
		router.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("Setup: expected 201, got %d for %s", w.Code, addr)
		}
	}

	// List active
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/credit/active", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		CreditLines []json.RawMessage `json:"credit_lines"`
		Count       int               `json:"count"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp.Count != 2 {
		t.Errorf("Expected 2 active credit lines, got %d", resp.Count)
	}
}

func TestHandler_ListActiveCredits_WithLimit(t *testing.T) {
	router, _, rep, met := setupHandlerTestRouter()

	// Create 3 credit lines
	for i, addr := range []string{
		"0xaaaa000000000000000000000000000000000001",
		"0xbbbb000000000000000000000000000000000002",
		"0xcccc000000000000000000000000000000000003",
	} {
		setupEligibleAgent(rep, met, addr, 65.0+float64(i), "trusted")
		w := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/v1/agents/"+addr+"/credit/apply", nil)
		req.Header.Set("X-Agent-Address", addr)
		router.ServeHTTP(w, req)
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/credit/active?limit=2", nil)
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

func TestHandler_ListActiveCredits_Empty(t *testing.T) {
	router, _, _, _ := setupHandlerTestRouter()

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/credit/active", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200 for empty list, got %d", w.Code)
	}

	var resp struct {
		Count int `json:"count"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp.Count != 0 {
		t.Errorf("Expected 0 credit lines, got %d", resp.Count)
	}
}

// ---------------------------------------------------------------------------
// POST /v1/agents/:address/credit/review
// ---------------------------------------------------------------------------

func TestHandler_ReviewCredit(t *testing.T) {
	router, _, rep, met := setupHandlerTestRouter()

	addr := "0xaaaa000000000000000000000000000000000001"
	setupEligibleAgent(rep, met, addr, 65.0, "trusted")

	// Apply
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/agents/"+addr+"/credit/apply", nil)
	req.Header.Set("X-Agent-Address", addr)
	router.ServeHTTP(w, req)

	// Upgrade reputation
	rep.setScore(addr, 85.0, "elite")
	met.setMetrics(addr, 200, 0.99, 120, 50000)

	// Review
	w = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/v1/agents/"+addr+"/credit/review", nil)
	req.Header.Set("X-Agent-Address", addr)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		CreditLine struct {
			ReputationTier string `json:"reputationTier"`
		} `json:"credit_line"`
		Evaluation struct {
			Eligible bool `json:"eligible"`
		} `json:"evaluation"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp.CreditLine.ReputationTier != "elite" {
		t.Errorf("Expected tier elite after review, got %s", resp.CreditLine.ReputationTier)
	}
	if !resp.Evaluation.Eligible {
		t.Error("Expected eligible=true after review")
	}
}

// ---------------------------------------------------------------------------
// POST /v1/admin/credit/:address/revoke
// ---------------------------------------------------------------------------

func TestHandler_RevokeCredit(t *testing.T) {
	router, _, rep, met := setupHandlerTestRouter()

	addr := "0xaaaa000000000000000000000000000000000001"
	setupEligibleAgent(rep, met, addr, 65.0, "trusted")

	// Apply first
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/agents/"+addr+"/credit/apply", nil)
	req.Header.Set("X-Agent-Address", addr)
	router.ServeHTTP(w, req)

	// Revoke
	body, _ := json.Marshal(map[string]string{"reason": "policy violation"})
	w = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/v1/admin/credit/"+addr+"/revoke", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		CreditLine struct {
			Status string `json:"status"`
		} `json:"credit_line"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp.CreditLine.Status != "revoked" {
		t.Errorf("Expected status revoked, got %s", resp.CreditLine.Status)
	}
}

func TestHandler_RevokeCredit_NotFound(t *testing.T) {
	router, _, _, _ := setupHandlerTestRouter()

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/admin/credit/0xnonexistent/revoke", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// POST /v1/admin/credit/check-defaults
// ---------------------------------------------------------------------------

func TestHandler_CheckDefaults(t *testing.T) {
	router, _, _, _ := setupHandlerTestRouter()

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/admin/credit/check-defaults", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		DefaultsFound int    `json:"defaults_found"`
		Message       string `json:"message"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp.DefaultsFound != 0 {
		t.Errorf("Expected 0 defaults on fresh system, got %d", resp.DefaultsFound)
	}
	if resp.Message == "" {
		t.Error("Expected non-empty message")
	}
}
