package stakes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func setupAnalyticsRouter() (*gin.Engine, *Service, *StakeAnalyticsService) {
	gin.SetMode(gin.TestMode)

	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	analytics := NewStakeAnalyticsService(store)
	handler := NewHandler(svc).WithAnalytics(analytics)

	r := gin.New()
	v1 := r.Group("/v1")
	handler.RegisterRoutes(v1)

	// Mock auth middleware
	authGroup := v1.Group("")
	authGroup.Use(func(c *gin.Context) {
		if addr := c.GetHeader("X-Agent-Address"); addr != "" {
			c.Set("authAgentAddr", addr)
		}
		c.Next()
	})
	handler.RegisterProtectedRoutes(authGroup)

	return r, svc, analytics
}

func TestHandler_GetPortfolioAnalytics(t *testing.T) {
	router, svc, _ := setupAnalyticsRouter()

	// Create stake and invest
	stake, _ := svc.CreateStake(context.Background(), CreateStakeRequest{
		AgentAddr:     "0xagent1",
		RevenueShare:  0.10,
		TotalShares:   100,
		PricePerShare: "5.000000",
		VestingPeriod: "1d",
	})
	svc.Invest(context.Background(), stake.ID, InvestRequest{
		InvestorAddr: "0xinvestor",
		Shares:       10,
	})

	req := httptest.NewRequest("GET", "/v1/agents/0xinvestor/portfolio/analytics", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Analytics struct {
			HoldingCount  int    `json:"holdingCount"`
			TotalInvested string `json:"totalInvested"`
		} `json:"analytics"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp.Analytics.HoldingCount != 1 {
		t.Errorf("expected 1 holding, got %d", resp.Analytics.HoldingCount)
	}
	if resp.Analytics.TotalInvested != "50.000000" {
		t.Errorf("expected 50.000000, got %s", resp.Analytics.TotalInvested)
	}
}

func TestHandler_GetPortfolioAnalytics_NoAnalytics(t *testing.T) {
	gin.SetMode(gin.TestMode)

	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	handler := NewHandler(svc) // No WithAnalytics()

	r := gin.New()
	v1 := r.Group("/v1")
	handler.RegisterRoutes(v1)

	req := httptest.NewRequest("GET", "/v1/agents/0xinvestor/portfolio/analytics", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotImplemented {
		t.Errorf("Expected 501 when analytics not configured, got %d", w.Code)
	}

	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error"] != "not_implemented" {
		t.Errorf("expected error 'not_implemented', got %s", resp["error"])
	}
}

func TestHandler_GetStakeNAV(t *testing.T) {
	router, svc, _ := setupAnalyticsRouter()

	stake, _ := svc.CreateStake(context.Background(), CreateStakeRequest{
		AgentAddr:     "0xagent1",
		RevenueShare:  0.10,
		TotalShares:   100,
		PricePerShare: "1.000000",
		VestingPeriod: "1d",
	})
	svc.Invest(context.Background(), stake.ID, InvestRequest{
		InvestorAddr: "0xinvestor",
		Shares:       50,
	})

	req := httptest.NewRequest("GET", "/v1/stakes/"+stake.ID+"/nav", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		NAV struct {
			IssuedShares int    `json:"issuedShares"`
			NAVPerShare  string `json:"navPerShare"`
		} `json:"nav"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp.NAV.IssuedShares != 50 {
		t.Errorf("expected 50 issued, got %d", resp.NAV.IssuedShares)
	}
	if resp.NAV.NAVPerShare != "1.000000" {
		t.Errorf("expected NAV 1.000000, got %s", resp.NAV.NAVPerShare)
	}
}

func TestHandler_GetStakeNAV_NotFound(t *testing.T) {
	router, _, _ := setupAnalyticsRouter()

	req := httptest.NewRequest("GET", "/v1/stakes/stk_nonexistent/nav", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected 404, got %d", w.Code)
	}
}

func TestHandler_GetStakePerformance(t *testing.T) {
	router, svc, _ := setupAnalyticsRouter()

	stake, _ := svc.CreateStake(context.Background(), CreateStakeRequest{
		AgentAddr:     "0xagent1",
		RevenueShare:  0.10,
		TotalShares:   100,
		PricePerShare: "1.000000",
		VestingPeriod: "1d",
	})
	svc.Invest(context.Background(), stake.ID, InvestRequest{
		InvestorAddr: "0xinvestor",
		Shares:       30,
	})

	req := httptest.NewRequest("GET", "/v1/stakes/"+stake.ID+"/performance", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Performance struct {
			IssuedShares int `json:"issuedShares"`
			HolderCount  int `json:"holderCount"`
		} `json:"performance"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp.Performance.IssuedShares != 30 {
		t.Errorf("expected 30 issued, got %d", resp.Performance.IssuedShares)
	}
	if resp.Performance.HolderCount != 1 {
		t.Errorf("expected 1 holder, got %d", resp.Performance.HolderCount)
	}
}

func TestPortfolioAnalytics_WithLiquidatedHoldings(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store, newMockLedger())
	analytics := NewStakeAnalyticsService(store)

	stake, _ := svc.CreateStake(context.Background(), CreateStakeRequest{
		AgentAddr:     "0xagent1",
		RevenueShare:  0.10,
		TotalShares:   100,
		PricePerShare: "10.000000",
		VestingPeriod: "1d",
	})
	svc.Invest(context.Background(), stake.ID, InvestRequest{
		InvestorAddr: "0xinvestor",
		Shares:       10,
	})

	// Liquidate the holding
	holdings, _ := store.ListHoldingsByInvestor(context.Background(), "0xinvestor")
	h := holdings[0]
	h.Status = string(HoldingStatusLiquidated)
	h.TotalEarned = "5.000000"
	store.UpdateHolding(context.Background(), h)

	pa, err := analytics.GetPortfolioAnalytics(context.Background(), "0xinvestor")
	if err != nil {
		t.Fatalf("analytics: %v", err)
	}

	// Liquidated should count in total but not active
	if pa.HoldingCount != 1 {
		t.Errorf("expected 1 total holding, got %d", pa.HoldingCount)
	}
	if pa.ActiveHoldings != 0 {
		t.Errorf("expected 0 active holdings, got %d", pa.ActiveHoldings)
	}
	if pa.TotalInvested != "100.000000" {
		t.Errorf("expected totalInvested 100.000000, got %s", pa.TotalInvested)
	}
	if pa.TotalEarned != "5.000000" {
		t.Errorf("expected totalEarned 5.000000, got %s", pa.TotalEarned)
	}
}

func TestStakePerformance_AllLiquidatedHolders(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store, newMockLedger())
	analytics := NewStakeAnalyticsService(store)

	stake, _ := svc.CreateStake(context.Background(), CreateStakeRequest{
		AgentAddr:     "0xagent1",
		RevenueShare:  0.10,
		TotalShares:   100,
		PricePerShare: "1.000000",
		VestingPeriod: "1d",
	})
	svc.Invest(context.Background(), stake.ID, InvestRequest{
		InvestorAddr: "0xinvestor1",
		Shares:       30,
	})
	svc.Invest(context.Background(), stake.ID, InvestRequest{
		InvestorAddr: "0xinvestor2",
		Shares:       20,
	})

	// Liquidate both holdings
	holdings, _ := store.ListHoldingsByStake(context.Background(), stake.ID)
	for _, h := range holdings {
		h.Status = string(HoldingStatusLiquidated)
		store.UpdateHolding(context.Background(), h)
	}

	perf, err := analytics.GetStakePerformance(context.Background(), stake.ID)
	if err != nil {
		t.Fatalf("performance: %v", err)
	}

	if perf.HolderCount != 0 {
		t.Errorf("expected 0 active holders, got %d", perf.HolderCount)
	}
	if perf.IssuedShares != 50 {
		t.Errorf("expected 50 issued shares, got %d", perf.IssuedShares)
	}
}

func TestPortfolioAnalytics_ZeroInvestedEdgeCase(t *testing.T) {
	store := NewMemoryStore()
	analytics := NewStakeAnalyticsService(store)

	// Manually create a zero-cost holding (edge case from secondary market gift?)
	now := time.Now()
	holding := &Holding{
		ID:           "hld_zero",
		StakeID:      "stk_test",
		InvestorAddr: "0xinvestor",
		Shares:       10,
		CostBasis:    "0.000000", // Zero cost
		TotalEarned:  "5.000000", // But has earned
		Status:       string(HoldingStatusActive),
		VestedAt:     now,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	store.CreateHolding(context.Background(), holding)

	// Also need a stake for the lookup
	stake := &Stake{
		ID:              "stk_test",
		AgentAddr:       "0xagent1",
		TotalShares:     100,
		AvailableShares: 90,
		PricePerShare:   "1.000000",
		Status:          string(StakeStatusOpen),
		TotalRaised:     "0.000000",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	store.CreateStake(context.Background(), stake)

	pa, err := analytics.GetPortfolioAnalytics(context.Background(), "0xinvestor")
	if err != nil {
		t.Fatalf("analytics: %v", err)
	}

	// Should not panic with division by zero
	if pa.ROI != 0 {
		t.Errorf("expected 0 ROI when invested is 0, got %.2f", pa.ROI)
	}
	if pa.AnnualizedReturn != 0 {
		t.Errorf("expected 0 annualized when invested is 0, got %.2f", pa.AnnualizedReturn)
	}
}
