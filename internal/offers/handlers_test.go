package offers

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func setupTestRouter() (*gin.Engine, *Service) {
	ml := newMockLedger()
	store := NewMemoryStore()
	svc := NewService(store, ml).WithLogger(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	handler := NewHandler(svc)

	r := gin.New()
	v1 := r.Group("/v1")
	handler.RegisterRoutes(v1)

	authGroup := v1.Group("")
	authGroup.Use(func(c *gin.Context) {
		if addr := c.GetHeader("X-Agent-Address"); addr != "" {
			c.Set("authAgentAddr", addr)
		}
		c.Next()
	})
	handler.RegisterProtectedRoutes(authGroup)

	return r, svc
}

// jsonReq creates a JSON request with optional auth header.
func jsonReq(t *testing.T, method, url string, body interface{}, agentAddr string) *http.Request {
	t.Helper()
	var buf *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		buf = bytes.NewReader(b)
	} else {
		buf = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, url, buf)
	req.Header.Set("Content-Type", "application/json")
	if agentAddr != "" {
		req.Header.Set("X-Agent-Address", agentAddr)
	}
	return req
}

// createOffer is a helper that posts an offer via HTTP and returns the ID.
func createOffer(t *testing.T, router *gin.Engine, seller string, req CreateOfferRequest) string {
	t.Helper()
	w := httptest.NewRecorder()
	router.ServeHTTP(w, jsonReq(t, "POST", "/v1/offers", req, seller))
	if w.Code != http.StatusCreated {
		t.Fatalf("createOffer: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Offer struct {
			ID string `json:"id"`
		} `json:"offer"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	return resp.Offer.ID
}

// claimOffer is a helper that claims an offer via HTTP and returns the claim ID.
func claimOffer(t *testing.T, router *gin.Engine, offerID, buyer string) string {
	t.Helper()
	w := httptest.NewRecorder()
	router.ServeHTTP(w, jsonReq(t, "POST", "/v1/offers/"+offerID+"/claim", nil, buyer))
	if w.Code != http.StatusCreated {
		t.Fatalf("claimOffer: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Claim struct {
			ID string `json:"id"`
		} `json:"claim"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	return resp.Claim.ID
}

// --- PostOffer handler tests ---

func TestHandler_PostOffer_Success(t *testing.T) {
	router, _ := setupTestRouter()

	body := CreateOfferRequest{
		ServiceType: "inference",
		Price:       "0.010000",
		Capacity:    100,
		Description: "GPT-4 inference",
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, jsonReq(t, "POST", "/v1/offers", body, "0xSeller"))

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Offer struct {
			ID          string `json:"id"`
			Status      string `json:"status"`
			ServiceType string `json:"serviceType"`
			Price       string `json:"price"`
		} `json:"offer"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp.Offer.Status != "active" {
		t.Errorf("expected active, got %s", resp.Offer.Status)
	}
	if resp.Offer.ServiceType != "inference" {
		t.Errorf("expected inference, got %s", resp.Offer.ServiceType)
	}
	if resp.Offer.Price != "0.010000" {
		t.Errorf("expected 0.010000, got %s", resp.Offer.Price)
	}
}

func TestHandler_PostOffer_MissingFields(t *testing.T) {
	router, _ := setupTestRouter()

	// Missing required fields
	w := httptest.NewRecorder()
	router.ServeHTTP(w, jsonReq(t, "POST", "/v1/offers", map[string]string{}, "0xSeller"))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_PostOffer_InvalidPrice(t *testing.T) {
	router, _ := setupTestRouter()

	body := CreateOfferRequest{
		ServiceType: "inference",
		Price:       "notanumber",
		Capacity:    10,
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, jsonReq(t, "POST", "/v1/offers", body, "0xSeller"))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_PostOffer_InvalidCapacity(t *testing.T) {
	router, _ := setupTestRouter()

	tests := []struct {
		name     string
		capacity int
	}{
		{"zero", 0},
		{"negative", -5},
		{"over_max", MaxCapacity + 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := CreateOfferRequest{
				ServiceType: "inference",
				Price:       "1.000000",
				Capacity:    tt.capacity,
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, jsonReq(t, "POST", "/v1/offers", body, "0xSeller"))

			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
			}
		})
	}
}

// --- GetOffer handler tests ---

func TestHandler_GetOffer_Success(t *testing.T) {
	router, _ := setupTestRouter()

	offerID := createOffer(t, router, "0xSeller", CreateOfferRequest{
		ServiceType: "inference",
		Price:       "0.010000",
		Capacity:    10,
	})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, jsonReq(t, "GET", "/v1/offers/"+offerID, nil, ""))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Offer struct {
			ID string `json:"id"`
		} `json:"offer"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Offer.ID != offerID {
		t.Errorf("expected ID %s, got %s", offerID, resp.Offer.ID)
	}
}

func TestHandler_GetOffer_NotFound(t *testing.T) {
	router, _ := setupTestRouter()

	w := httptest.NewRecorder()
	router.ServeHTTP(w, jsonReq(t, "GET", "/v1/offers/nonexistent", nil, ""))

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

// --- ListOffers handler tests ---

func TestHandler_ListOffers(t *testing.T) {
	router, _ := setupTestRouter()

	createOffer(t, router, "0xSeller1", CreateOfferRequest{
		ServiceType: "inference",
		Price:       "0.010000",
		Capacity:    10,
	})
	createOffer(t, router, "0xSeller2", CreateOfferRequest{
		ServiceType: "translation",
		Price:       "0.050000",
		Capacity:    5,
	})

	tests := []struct {
		name          string
		query         string
		expectedCount int
	}{
		{"all", "/v1/offers", 2},
		{"by_type", "/v1/offers?service_type=inference", 1},
		{"with_limit", "/v1/offers?limit=1", 1},
		{"invalid_limit", "/v1/offers?limit=abc", 2},
		{"huge_limit_capped", "/v1/offers?limit=999", 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			router.ServeHTTP(w, jsonReq(t, "GET", tt.query, nil, ""))

			if w.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
			}

			var resp struct {
				Offers []json.RawMessage `json:"offers"`
				Count  int               `json:"count"`
			}
			json.Unmarshal(w.Body.Bytes(), &resp)
			if resp.Count != tt.expectedCount {
				t.Errorf("expected %d offers, got %d", tt.expectedCount, resp.Count)
			}
		})
	}
}

// --- ListSellerOffers handler tests ---

func TestHandler_ListSellerOffers(t *testing.T) {
	router, _ := setupTestRouter()

	createOffer(t, router, "0xSeller1", CreateOfferRequest{
		ServiceType: "inference",
		Price:       "0.010000",
		Capacity:    10,
	})
	createOffer(t, router, "0xSeller1", CreateOfferRequest{
		ServiceType: "translation",
		Price:       "0.050000",
		Capacity:    5,
	})
	createOffer(t, router, "0xSeller2", CreateOfferRequest{
		ServiceType: "inference",
		Price:       "0.020000",
		Capacity:    20,
	})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, jsonReq(t, "GET", "/v1/agents/0xSeller1/offers", nil, ""))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Offers []json.RawMessage `json:"offers"`
		Count  int               `json:"count"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Count != 2 {
		t.Errorf("expected 2, got %d", resp.Count)
	}
}

func TestHandler_ListSellerOffers_WithLimit(t *testing.T) {
	router, _ := setupTestRouter()

	createOffer(t, router, "0xSeller1", CreateOfferRequest{
		ServiceType: "inference",
		Price:       "0.010000",
		Capacity:    10,
	})
	createOffer(t, router, "0xSeller1", CreateOfferRequest{
		ServiceType: "translation",
		Price:       "0.050000",
		Capacity:    5,
	})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, jsonReq(t, "GET", "/v1/agents/0xSeller1/offers?limit=1", nil, ""))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Count int `json:"count"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Count != 1 {
		t.Errorf("expected 1, got %d", resp.Count)
	}
}

// --- ClaimOffer handler tests ---

func TestHandler_ClaimOffer_Success(t *testing.T) {
	router, _ := setupTestRouter()

	offerID := createOffer(t, router, "0xSeller", CreateOfferRequest{
		ServiceType: "inference",
		Price:       "0.010000",
		Capacity:    10,
	})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, jsonReq(t, "POST", "/v1/offers/"+offerID+"/claim", nil, "0xBuyer"))

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Claim struct {
			ID     string `json:"id"`
			Status string `json:"status"`
			Amount string `json:"amount"`
		} `json:"claim"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Claim.Status != "pending" {
		t.Errorf("expected pending, got %s", resp.Claim.Status)
	}
}

func TestHandler_ClaimOffer_NotFound(t *testing.T) {
	router, _ := setupTestRouter()

	w := httptest.NewRecorder()
	router.ServeHTTP(w, jsonReq(t, "POST", "/v1/offers/nonexistent/claim", nil, "0xBuyer"))

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_ClaimOffer_SelfClaim(t *testing.T) {
	router, _ := setupTestRouter()

	offerID := createOffer(t, router, "0xSeller", CreateOfferRequest{
		ServiceType: "inference",
		Price:       "0.010000",
		Capacity:    10,
	})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, jsonReq(t, "POST", "/v1/offers/"+offerID+"/claim", nil, "0xSeller"))

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_ClaimOffer_Exhausted(t *testing.T) {
	router, _ := setupTestRouter()

	offerID := createOffer(t, router, "0xSeller", CreateOfferRequest{
		ServiceType: "inference",
		Price:       "0.010000",
		Capacity:    1,
	})

	// First claim succeeds
	claimOffer(t, router, offerID, "0xBuyer1")

	// Second claim should fail
	w := httptest.NewRecorder()
	router.ServeHTTP(w, jsonReq(t, "POST", "/v1/offers/"+offerID+"/claim", nil, "0xBuyer2"))

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_ClaimOffer_Cancelled(t *testing.T) {
	router, _ := setupTestRouter()

	offerID := createOffer(t, router, "0xSeller", CreateOfferRequest{
		ServiceType: "inference",
		Price:       "0.010000",
		Capacity:    10,
	})

	// Cancel the offer
	w := httptest.NewRecorder()
	router.ServeHTTP(w, jsonReq(t, "POST", "/v1/offers/"+offerID+"/cancel", nil, "0xSeller"))
	if w.Code != http.StatusOK {
		t.Fatalf("cancel: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Claim should fail with 410
	w = httptest.NewRecorder()
	router.ServeHTTP(w, jsonReq(t, "POST", "/v1/offers/"+offerID+"/claim", nil, "0xBuyer"))

	if w.Code != http.StatusGone {
		t.Fatalf("expected 410, got %d: %s", w.Code, w.Body.String())
	}
}

// --- CancelOffer handler tests ---

func TestHandler_CancelOffer_Success(t *testing.T) {
	router, _ := setupTestRouter()

	offerID := createOffer(t, router, "0xSeller", CreateOfferRequest{
		ServiceType: "inference",
		Price:       "0.010000",
		Capacity:    10,
	})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, jsonReq(t, "POST", "/v1/offers/"+offerID+"/cancel", nil, "0xSeller"))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Offer struct {
			Status string `json:"status"`
		} `json:"offer"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Offer.Status != "cancelled" {
		t.Errorf("expected cancelled, got %s", resp.Offer.Status)
	}
}

func TestHandler_CancelOffer_NotFound(t *testing.T) {
	router, _ := setupTestRouter()

	w := httptest.NewRecorder()
	router.ServeHTTP(w, jsonReq(t, "POST", "/v1/offers/nonexistent/cancel", nil, "0xSeller"))

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_CancelOffer_Unauthorized(t *testing.T) {
	router, _ := setupTestRouter()

	offerID := createOffer(t, router, "0xSeller", CreateOfferRequest{
		ServiceType: "inference",
		Price:       "0.010000",
		Capacity:    10,
	})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, jsonReq(t, "POST", "/v1/offers/"+offerID+"/cancel", nil, "0xStranger"))

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

// --- ListClaims handler tests ---

func TestHandler_ListClaims(t *testing.T) {
	router, _ := setupTestRouter()

	offerID := createOffer(t, router, "0xSeller", CreateOfferRequest{
		ServiceType: "inference",
		Price:       "0.010000",
		Capacity:    10,
	})

	claimOffer(t, router, offerID, "0xBuyer1")
	claimOffer(t, router, offerID, "0xBuyer2")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, jsonReq(t, "GET", "/v1/offers/"+offerID+"/claims", nil, ""))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Claims []json.RawMessage `json:"claims"`
		Count  int               `json:"count"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Count != 2 {
		t.Errorf("expected 2 claims, got %d", resp.Count)
	}
}

func TestHandler_ListClaims_WithLimit(t *testing.T) {
	router, _ := setupTestRouter()

	offerID := createOffer(t, router, "0xSeller", CreateOfferRequest{
		ServiceType: "inference",
		Price:       "0.010000",
		Capacity:    10,
	})

	claimOffer(t, router, offerID, "0xBuyer1")
	claimOffer(t, router, offerID, "0xBuyer2")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, jsonReq(t, "GET", "/v1/offers/"+offerID+"/claims?limit=1", nil, ""))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Count int `json:"count"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Count != 1 {
		t.Errorf("expected 1 claim, got %d", resp.Count)
	}
}

// --- GetClaim handler tests ---

func TestHandler_GetClaim_Success(t *testing.T) {
	router, _ := setupTestRouter()

	offerID := createOffer(t, router, "0xSeller", CreateOfferRequest{
		ServiceType: "inference",
		Price:       "0.010000",
		Capacity:    10,
	})
	claimID := claimOffer(t, router, offerID, "0xBuyer")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, jsonReq(t, "GET", "/v1/claims/"+claimID, nil, ""))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Claim struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"claim"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Claim.ID != claimID {
		t.Errorf("expected ID %s, got %s", claimID, resp.Claim.ID)
	}
}

func TestHandler_GetClaim_NotFound(t *testing.T) {
	router, _ := setupTestRouter()

	w := httptest.NewRecorder()
	router.ServeHTTP(w, jsonReq(t, "GET", "/v1/claims/nonexistent", nil, ""))

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

// --- DeliverClaim handler tests ---

func TestHandler_DeliverClaim_Success(t *testing.T) {
	router, _ := setupTestRouter()

	offerID := createOffer(t, router, "0xSeller", CreateOfferRequest{
		ServiceType: "inference",
		Price:       "0.010000",
		Capacity:    10,
	})
	claimID := claimOffer(t, router, offerID, "0xBuyer")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, jsonReq(t, "POST", "/v1/claims/"+claimID+"/deliver", nil, "0xSeller"))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Claim struct {
			Status string `json:"status"`
		} `json:"claim"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Claim.Status != "delivered" {
		t.Errorf("expected delivered, got %s", resp.Claim.Status)
	}
}

func TestHandler_DeliverClaim_NotFound(t *testing.T) {
	router, _ := setupTestRouter()

	w := httptest.NewRecorder()
	router.ServeHTTP(w, jsonReq(t, "POST", "/v1/claims/nonexistent/deliver", nil, "0xSeller"))

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_DeliverClaim_Unauthorized(t *testing.T) {
	router, _ := setupTestRouter()

	offerID := createOffer(t, router, "0xSeller", CreateOfferRequest{
		ServiceType: "inference",
		Price:       "0.010000",
		Capacity:    10,
	})
	claimID := claimOffer(t, router, offerID, "0xBuyer")

	// Buyer can't deliver
	w := httptest.NewRecorder()
	router.ServeHTTP(w, jsonReq(t, "POST", "/v1/claims/"+claimID+"/deliver", nil, "0xBuyer"))

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_DeliverClaim_NotPending(t *testing.T) {
	router, _ := setupTestRouter()

	offerID := createOffer(t, router, "0xSeller", CreateOfferRequest{
		ServiceType: "inference",
		Price:       "0.010000",
		Capacity:    10,
	})
	claimID := claimOffer(t, router, offerID, "0xBuyer")

	// Deliver first
	w := httptest.NewRecorder()
	router.ServeHTTP(w, jsonReq(t, "POST", "/v1/claims/"+claimID+"/deliver", nil, "0xSeller"))
	if w.Code != http.StatusOK {
		t.Fatalf("first deliver: expected 200, got %d", w.Code)
	}

	// Second deliver should fail
	w = httptest.NewRecorder()
	router.ServeHTTP(w, jsonReq(t, "POST", "/v1/claims/"+claimID+"/deliver", nil, "0xSeller"))

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
}

// --- CompleteClaim handler tests ---

func TestHandler_CompleteClaim_Success(t *testing.T) {
	router, _ := setupTestRouter()

	offerID := createOffer(t, router, "0xSeller", CreateOfferRequest{
		ServiceType: "inference",
		Price:       "0.010000",
		Capacity:    10,
	})
	claimID := claimOffer(t, router, offerID, "0xBuyer")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, jsonReq(t, "POST", "/v1/claims/"+claimID+"/complete", nil, "0xBuyer"))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Claim struct {
			Status string `json:"status"`
		} `json:"claim"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Claim.Status != "completed" {
		t.Errorf("expected completed, got %s", resp.Claim.Status)
	}
}

func TestHandler_CompleteClaim_NotFound(t *testing.T) {
	router, _ := setupTestRouter()

	w := httptest.NewRecorder()
	router.ServeHTTP(w, jsonReq(t, "POST", "/v1/claims/nonexistent/complete", nil, "0xBuyer"))

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_CompleteClaim_Unauthorized(t *testing.T) {
	router, _ := setupTestRouter()

	offerID := createOffer(t, router, "0xSeller", CreateOfferRequest{
		ServiceType: "inference",
		Price:       "0.010000",
		Capacity:    10,
	})
	claimID := claimOffer(t, router, offerID, "0xBuyer")

	// Seller can't complete
	w := httptest.NewRecorder()
	router.ServeHTTP(w, jsonReq(t, "POST", "/v1/claims/"+claimID+"/complete", nil, "0xSeller"))

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_CompleteClaim_AlreadyCompleted(t *testing.T) {
	router, _ := setupTestRouter()

	offerID := createOffer(t, router, "0xSeller", CreateOfferRequest{
		ServiceType: "inference",
		Price:       "0.010000",
		Capacity:    10,
	})
	claimID := claimOffer(t, router, offerID, "0xBuyer")

	// Complete first
	w := httptest.NewRecorder()
	router.ServeHTTP(w, jsonReq(t, "POST", "/v1/claims/"+claimID+"/complete", nil, "0xBuyer"))
	if w.Code != http.StatusOK {
		t.Fatalf("first complete: expected 200, got %d", w.Code)
	}

	// Second complete should fail
	w = httptest.NewRecorder()
	router.ServeHTTP(w, jsonReq(t, "POST", "/v1/claims/"+claimID+"/complete", nil, "0xBuyer"))

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
}

// --- RefundClaim handler tests ---

func TestHandler_RefundClaim_Success(t *testing.T) {
	router, _ := setupTestRouter()

	offerID := createOffer(t, router, "0xSeller", CreateOfferRequest{
		ServiceType: "inference",
		Price:       "0.010000",
		Capacity:    10,
	})
	claimID := claimOffer(t, router, offerID, "0xBuyer")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, jsonReq(t, "POST", "/v1/claims/"+claimID+"/refund", nil, "0xBuyer"))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Claim struct {
			Status string `json:"status"`
		} `json:"claim"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Claim.Status != "refunded" {
		t.Errorf("expected refunded, got %s", resp.Claim.Status)
	}
}

func TestHandler_RefundClaim_NotFound(t *testing.T) {
	router, _ := setupTestRouter()

	w := httptest.NewRecorder()
	router.ServeHTTP(w, jsonReq(t, "POST", "/v1/claims/nonexistent/refund", nil, "0xBuyer"))

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_RefundClaim_Unauthorized(t *testing.T) {
	router, _ := setupTestRouter()

	offerID := createOffer(t, router, "0xSeller", CreateOfferRequest{
		ServiceType: "inference",
		Price:       "0.010000",
		Capacity:    10,
	})
	claimID := claimOffer(t, router, offerID, "0xBuyer")

	// Stranger can't refund
	w := httptest.NewRecorder()
	router.ServeHTTP(w, jsonReq(t, "POST", "/v1/claims/"+claimID+"/refund", nil, "0xStranger"))

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_RefundClaim_AlreadyRefunded(t *testing.T) {
	router, _ := setupTestRouter()

	offerID := createOffer(t, router, "0xSeller", CreateOfferRequest{
		ServiceType: "inference",
		Price:       "0.010000",
		Capacity:    10,
	})
	claimID := claimOffer(t, router, offerID, "0xBuyer")

	// Refund first
	w := httptest.NewRecorder()
	router.ServeHTTP(w, jsonReq(t, "POST", "/v1/claims/"+claimID+"/refund", nil, "0xBuyer"))
	if w.Code != http.StatusOK {
		t.Fatalf("first refund: expected 200, got %d", w.Code)
	}

	// Second refund should fail
	w = httptest.NewRecorder()
	router.ServeHTTP(w, jsonReq(t, "POST", "/v1/claims/"+claimID+"/refund", nil, "0xBuyer"))

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
}

// --- Full lifecycle through handlers ---

func TestHandler_FullLifecycle_DeliverComplete(t *testing.T) {
	router, _ := setupTestRouter()

	offerID := createOffer(t, router, "0xSeller", CreateOfferRequest{
		ServiceType: "inference",
		Price:       "0.010000",
		Capacity:    10,
	})
	claimID := claimOffer(t, router, offerID, "0xBuyer")

	// Deliver
	w := httptest.NewRecorder()
	router.ServeHTTP(w, jsonReq(t, "POST", "/v1/claims/"+claimID+"/deliver", nil, "0xSeller"))
	if w.Code != http.StatusOK {
		t.Fatalf("deliver: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Complete
	w = httptest.NewRecorder()
	router.ServeHTTP(w, jsonReq(t, "POST", "/v1/claims/"+claimID+"/complete", nil, "0xBuyer"))
	if w.Code != http.StatusOK {
		t.Fatalf("complete: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Verify final state
	w = httptest.NewRecorder()
	router.ServeHTTP(w, jsonReq(t, "GET", "/v1/claims/"+claimID, nil, ""))

	var resp struct {
		Claim struct {
			Status string `json:"status"`
		} `json:"claim"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Claim.Status != "completed" {
		t.Errorf("expected completed, got %s", resp.Claim.Status)
	}
}

// --- Claim offer error mapping for expired offers ---

func TestHandler_ClaimOffer_Expired(t *testing.T) {
	router, svc := setupTestRouter()
	ctx := context.Background()

	// Create offer with very short expiry via service directly so we can control timing
	offer, err := svc.PostOffer(ctx, "0xSeller", CreateOfferRequest{
		ServiceType: "inference",
		Price:       "0.010000",
		Capacity:    10,
		ExpiresIn:   "1ms",
	})
	if err != nil {
		t.Fatalf("PostOffer: %v", err)
	}

	// Wait for it to expire
	time.Sleep(5 * time.Millisecond)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, jsonReq(t, "POST", "/v1/offers/"+offer.ID+"/claim", nil, "0xBuyer"))

	if w.Code != http.StatusGone {
		t.Fatalf("expected 410, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_ClaimOffer_ConditionNotMet(t *testing.T) {
	router, svc := setupTestRouter()
	ctx := context.Background()

	offer, err := svc.PostOffer(ctx, "0xSeller", CreateOfferRequest{
		ServiceType: "inference",
		Price:       "0.010000",
		Capacity:    10,
		Conditions:  []Condition{{Type: "allowed_buyers", Value: "0xallowed"}},
	})
	if err != nil {
		t.Fatalf("PostOffer: %v", err)
	}

	w := httptest.NewRecorder()
	router.ServeHTTP(w, jsonReq(t, "POST", "/v1/offers/"+offer.ID+"/claim", nil, "0xNotAllowed"))

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
}
