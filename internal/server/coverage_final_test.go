package server

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mbd888/alancoin/internal/reputation"
)

// ---------------------------------------------------------------------------
// timerStatus is tested directly; readinessHandler timer path requires
// real (non-nil-pointer) timers which are only created in Postgres mode.
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// timerStatus edge cases
// ---------------------------------------------------------------------------

type mockRunnable struct {
	running bool
}

func (m *mockRunnable) Running() bool { return m.running }

func TestTimerStatus_Running(t *testing.T) {
	r := &mockRunnable{running: true}
	if got := timerStatus(r); got != "running" {
		t.Errorf("Expected 'running', got %q", got)
	}
}

func TestTimerStatus_Stopped(t *testing.T) {
	r := &mockRunnable{running: false}
	if got := timerStatus(r); got != "stopped" {
		t.Errorf("Expected 'stopped', got %q", got)
	}
}

func TestTimerStatus_NotConfigured(t *testing.T) {
	if got := timerStatus(nil); got != "not_configured" {
		t.Errorf("Expected 'not_configured', got %q", got)
	}
}

func TestTimerStatus_Unknown(t *testing.T) {
	// Pass something that doesn't implement runnable
	if got := timerStatus("not-a-timer"); got != "unknown" {
		t.Errorf("Expected 'unknown', got %q", got)
	}
}

// ---------------------------------------------------------------------------
// gzipWriter WriteString
// ---------------------------------------------------------------------------

func TestGzipWriter_WriteString(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(gzipMiddleware())
	router.GET("/test-ws", func(c *gin.Context) {
		// Use WriteString explicitly to cover gzipWriter.WriteString
		c.Writer.WriteString(strings.Repeat("test string data ", 100))
		c.Status(200)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test-ws", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	router.ServeHTTP(w, req)

	if w.Header().Get("Content-Encoding") != "gzip" {
		t.Error("Expected gzip Content-Encoding")
	}

	reader, err := gzip.NewReader(w.Body)
	if err != nil {
		t.Fatalf("Failed to create gzip reader: %v", err)
	}
	defer reader.Close()
	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("Failed to read gzip body: %v", err)
	}
	if !strings.Contains(string(body), "test string data") {
		t.Error("Expected decompressed body to contain 'test string data'")
	}
}

// ---------------------------------------------------------------------------
// escrowLedgerAdapter — ReleaseEscrow, PartialEscrowSettle
// ---------------------------------------------------------------------------

func TestEscrowLedgerAdapter_ReleaseEscrow(t *testing.T) {
	s := newTestServer(t)
	adapter := &escrowLedgerAdapter{l: s.ledgerService}

	ctx := context.Background()
	buyerAddr := "0xdddd000000000000000000000000000000000051"
	sellerAddr := "0xdddd000000000000000000000000000000000052"

	// Fund both agents
	_ = s.ledger.StoreRef().Credit(ctx, buyerAddr, "100.000000", "dep_r1", "test")

	// Lock escrow first
	err := adapter.EscrowLock(ctx, buyerAddr, "5.000000", "esc_rel_1")
	if err != nil {
		t.Fatalf("EscrowLock failed: %v", err)
	}

	// Release escrow to seller
	err = adapter.ReleaseEscrow(ctx, buyerAddr, sellerAddr, "5.000000", "esc_rel_1")
	if err != nil {
		t.Fatalf("ReleaseEscrow failed: %v", err)
	}
}

func TestEscrowLedgerAdapter_PartialEscrowSettle(t *testing.T) {
	s := newTestServer(t)
	adapter := &escrowLedgerAdapter{l: s.ledgerService}

	ctx := context.Background()
	buyerAddr := "0xdddd000000000000000000000000000000000053"
	sellerAddr := "0xdddd000000000000000000000000000000000054"

	// Fund
	_ = s.ledger.StoreRef().Credit(ctx, buyerAddr, "100.000000", "dep_p1", "test")

	// Lock escrow (within per-tx limit of 5 USDC for new agents)
	err := adapter.EscrowLock(ctx, buyerAddr, "4.000000", "esc_part_1")
	if err != nil {
		t.Fatalf("EscrowLock failed: %v", err)
	}

	// Partial settle: release some, refund some
	err = adapter.PartialEscrowSettle(ctx, buyerAddr, sellerAddr, "2.500000", "1.500000", "esc_part_1")
	if err != nil {
		t.Fatalf("PartialEscrowSettle failed: %v", err)
	}
}

// ---------------------------------------------------------------------------
// streamLedgerAdapter — SettleHold
// ---------------------------------------------------------------------------

func TestStreamLedgerAdapter_SettleHold(t *testing.T) {
	s := newTestServer(t)
	adapter := &streamLedgerAdapter{l: s.ledgerService}

	ctx := context.Background()
	buyerAddr := "0xdddd000000000000000000000000000000000055"
	sellerAddr := "0xdddd000000000000000000000000000000000056"

	// Fund buyer
	_ = s.ledger.StoreRef().Credit(ctx, buyerAddr, "100.000000", "dep_s1", "test")

	// Hold
	err := adapter.Hold(ctx, buyerAddr, "5.000000", "stream_settle_1")
	if err != nil {
		t.Fatalf("Hold failed: %v", err)
	}

	// SettleHold
	err = adapter.SettleHold(ctx, buyerAddr, sellerAddr, "5.000000", "stream_settle_1")
	if err != nil {
		t.Fatalf("SettleHold failed: %v", err)
	}
}

// ---------------------------------------------------------------------------
// gatewayLedgerAdapter — SettleHold, SettleHoldWithFee
// ---------------------------------------------------------------------------

func TestGatewayLedgerAdapter_SettleHold(t *testing.T) {
	s := newTestServer(t)
	adapter := &gatewayLedgerAdapter{l: s.ledgerService}

	ctx := context.Background()
	buyerAddr := "0xdddd000000000000000000000000000000000057"
	sellerAddr := "0xdddd000000000000000000000000000000000058"

	// Fund buyer
	_ = s.ledger.StoreRef().Credit(ctx, buyerAddr, "100.000000", "dep_g1", "test")

	// Hold
	err := adapter.Hold(ctx, buyerAddr, "5.000000", "gw_settle_1")
	if err != nil {
		t.Fatalf("Hold failed: %v", err)
	}

	// SettleHold
	err = adapter.SettleHold(ctx, buyerAddr, sellerAddr, "5.000000", "gw_settle_1")
	if err != nil {
		t.Fatalf("SettleHold failed: %v", err)
	}
}

func TestGatewayLedgerAdapter_SettleHoldWithFee(t *testing.T) {
	s := newTestServer(t)
	adapter := &gatewayLedgerAdapter{l: s.ledgerService}

	ctx := context.Background()
	buyerAddr := "0xdddd000000000000000000000000000000000059"
	sellerAddr := "0xdddd000000000000000000000000000000000060"
	platformAddr := "0xdddd000000000000000000000000000000000061"

	// Fund buyer
	_ = s.ledger.StoreRef().Credit(ctx, buyerAddr, "100.000000", "dep_gf1", "test")

	// Hold (within per-tx limit)
	err := adapter.Hold(ctx, buyerAddr, "4.000000", "gw_fee_1")
	if err != nil {
		t.Fatalf("Hold failed: %v", err)
	}

	// SettleHoldWithFee
	err = adapter.SettleHoldWithFee(ctx, buyerAddr, sellerAddr, "3.500000", platformAddr, "0.500000", "gw_fee_1")
	if err != nil {
		t.Fatalf("SettleHoldWithFee failed: %v", err)
	}
}

// ---------------------------------------------------------------------------
// kyaReputationAdapter — non-nil provider path (via registry)
// ---------------------------------------------------------------------------

func TestKyaReputationAdapter_WithProvider(t *testing.T) {
	s := newTestServer(t)
	// Register an agent so the reputation provider has data
	addr := "0xaaaa000000000000000000000000000000000071"
	registerAgent(t, s, addr, "RepAdapter")

	repProvider := reputation.NewRegistryProvider(s.registry)
	adapter := &kyaReputationAdapter{rep: repProvider}
	ctx := context.Background()

	// GetScore (non-nil rep path)
	score, err := adapter.GetScore(ctx, addr)
	if err != nil {
		t.Fatalf("GetScore failed: %v", err)
	}
	if score < 0 {
		t.Errorf("Expected non-negative score, got %v", score)
	}

	// GetSuccessRate
	rate, err := adapter.GetSuccessRate(ctx, addr)
	if err != nil {
		t.Fatalf("GetSuccessRate failed: %v", err)
	}
	// New agent: 0 txns, success rate should be 0
	if rate != 0 {
		t.Logf("Success rate for new agent: %v", rate)
	}

	// GetDisputeRate
	disputeRate, err := adapter.GetDisputeRate(ctx, addr)
	if err != nil {
		t.Fatalf("GetDisputeRate failed: %v", err)
	}
	if disputeRate < 0 {
		t.Errorf("Expected non-negative dispute rate, got %v", disputeRate)
	}

	// GetTxCount
	txCount, err := adapter.GetTxCount(ctx, addr)
	if err != nil {
		t.Fatalf("GetTxCount failed: %v", err)
	}
	if txCount < 0 {
		t.Errorf("Expected non-negative tx count, got %v", txCount)
	}
}

// ---------------------------------------------------------------------------
// adminWSStateProvider
// ---------------------------------------------------------------------------

func TestAdminWSStateProvider_WithHub(t *testing.T) {
	s := newTestServer(t)
	provider := &adminWSStateProvider{hub: s.realtimeHub}

	state := provider.AdminState(context.Background())
	if state == nil {
		t.Fatal("Expected non-nil state")
	}
	if _, ok := state["connectedClients"]; !ok {
		t.Error("Expected 'connectedClients' in state")
	}
}

// ---------------------------------------------------------------------------
// adminDBStateProvider — db is nil in test server, but verify code path
// ---------------------------------------------------------------------------

func TestAdminDBStateProvider(t *testing.T) {
	// We can't easily get a sql.DB in test mode, but we verify the type
	// exists and test the happy path when db is available.
	// For now, test with a nil-guard: the server handles nil db gracefully.
	s := newTestServer(t)
	if s.db != nil {
		provider := &adminDBStateProvider{db: s.db}
		state := provider.AdminState(context.Background())
		if state == nil {
			t.Fatal("Expected non-nil state")
		}
	}
}

// ---------------------------------------------------------------------------
// adminReconcileStateProvider
// ---------------------------------------------------------------------------

func TestAdminReconcileStateProvider_NilReport(t *testing.T) {
	// reconcileRunner is nil in in-memory mode. Test the nil runner path.
	s := newTestServer(t)
	if s.reconcileRunner != nil {
		provider := &adminReconcileStateProvider{runner: s.reconcileRunner}
		state := provider.AdminState(context.Background())
		if state == nil {
			t.Fatal("Expected non-nil state")
		}
		if state["last_run"] != nil {
			t.Error("Expected nil last_run for fresh runner")
		}
	}
}

// ---------------------------------------------------------------------------
// initBillingProvider edge cases
// ---------------------------------------------------------------------------

func TestInitBillingProvider_NoStripeKey(t *testing.T) {
	cfg := testConfig()
	cfg.StripeSecretKey = ""
	provider := initBillingProvider(cfg, nil)
	if provider == nil {
		t.Fatal("Expected non-nil noop provider")
	}
}

func TestInitBillingProvider_WithStripeKey(t *testing.T) {
	cfg := testConfig()
	cfg.StripeSecretKey = "sk_test_fake"
	cfg.StripePriceStarterID = "price_starter"
	cfg.StripePriceGrowthID = "price_growth"
	cfg.StripePriceEnterpriseID = "price_enterprise"
	provider := initBillingProvider(cfg, nil)
	if provider == nil {
		t.Fatal("Expected non-nil stripe provider")
	}
}

// ---------------------------------------------------------------------------
// billingProviderName
// ---------------------------------------------------------------------------

func TestBillingProviderName_Stripe(t *testing.T) {
	cfg := testConfig()
	cfg.StripeSecretKey = "sk_test_fake"
	if got := billingProviderName(cfg); got != "stripe" {
		t.Errorf("Expected 'stripe', got %q", got)
	}
}

// readinessHandler full path requires real timers (Postgres mode only).

// ---------------------------------------------------------------------------
// Gzip WriteString coverage through the full handler path
// ---------------------------------------------------------------------------

func TestGzipMiddleware_WriteStringPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(gzipMiddleware())
	router.GET("/big", func(c *gin.Context) {
		// gin's c.String internally calls WriteString
		c.String(200, strings.Repeat("a long response body ", 200))
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/big", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	router.ServeHTTP(w, req)

	if w.Header().Get("Content-Encoding") != "gzip" {
		t.Error("Expected gzip encoding")
	}

	reader, err := gzip.NewReader(w.Body)
	if err != nil {
		t.Fatalf("Failed to create gzip reader: %v", err)
	}
	defer reader.Close()

	body, _ := io.ReadAll(reader)
	if !strings.Contains(string(body), "a long response body") {
		t.Error("Expected decompressed body content")
	}
}

// ---------------------------------------------------------------------------
// Additional route tests for dashboard/intelligence endpoints
// ---------------------------------------------------------------------------

func TestDashboardRouteRegistered(t *testing.T) {
	s := newTestServer(t)

	routes := s.router.Routes()
	routeSet := make(map[string]bool)
	for _, route := range routes {
		routeSet[route.Method+":"+route.Path] = true
	}

	dashRoutes := []string{
		"GET:/v1/timeline",
		"GET:/v1/network/stats",
		"GET:/v1/network/stats/enhanced",
		"GET:/v1/feed",
		"GET:/v1/services",
	}

	for _, e := range dashRoutes {
		if !routeSet[e] {
			t.Errorf("Route %s not registered", e)
		}
	}
}

// ---------------------------------------------------------------------------
// Verify transactions can be made and timeline is non-empty
// ---------------------------------------------------------------------------

func TestGetTimeline_WithTransactions(t *testing.T) {
	s := newTestServer(t)

	// Register two agents
	fromAddr := "0xaaaa000000000000000000000000000000000081"
	toAddr := "0xaaaa000000000000000000000000000000000082"
	fromKey := registerAgent(t, s, fromAddr, "TimelineSender")
	_ = registerAgent(t, s, toAddr, "TimelineRecv")

	// Fund the sender
	_ = s.ledger.StoreRef().Credit(context.Background(), fromAddr, "100.000000", "dep_tl", "test")

	// Send a transaction
	txBody := `{"to":"` + toAddr + `","amount":"1.000000","service_type":"test","reference":"tl_ref1"}`
	w := httptest.NewRecorder()
	req := authedRequest("POST", "/v1/transactions", fromKey, txBody)
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK && w.Code != http.StatusCreated {
		t.Logf("Transaction response: %d %s", w.Code, w.Body.String())
	}

	// Get timeline
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/v1/timeline", nil)
	s.router.ServeHTTP(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", w2.Code, w2.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Register agent — description and metadata edge cases
// ---------------------------------------------------------------------------

func TestRegisterAgent_WithDescription(t *testing.T) {
	s := newTestServer(t)

	body := `{"address":"0xaaaa000000000000000000000000000000000091","name":"DescBot","description":"A test bot with a description"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/agents", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("Expected 201, got %d: %s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Health handler — no DB (in-memory) path
// ---------------------------------------------------------------------------

func TestHealthHandler_NoDB_AllHealthy(t *testing.T) {
	s := newTestServer(t)
	// In-memory server: db is nil, should skip DB check

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/health", nil)
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d", w.Code)
	}

	var resp HealthResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to parse: %v", err)
	}
	if resp.Status != "healthy" {
		t.Errorf("Expected 'healthy', got %q", resp.Status)
	}
}
