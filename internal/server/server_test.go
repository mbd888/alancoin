package server

import (
	"context"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/gin-gonic/gin"
	"github.com/mbd888/alancoin/internal/config"
	"github.com/mbd888/alancoin/internal/wallet"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// mockWallet implements wallet.WalletService for testing
type mockWallet struct{}

func (m *mockWallet) Transfer(ctx context.Context, to common.Address, amount *big.Int) (*wallet.TransferResult, error) {
	return &wallet.TransferResult{TxHash: "0xmock", From: "0xplatform", To: to.Hex(), Amount: "1.00"}, nil
}

func (m *mockWallet) WaitForConfirmation(ctx context.Context, txHash string, timeout time.Duration) (*wallet.TransferResult, error) {
	return &wallet.TransferResult{TxHash: txHash}, nil
}

func (m *mockWallet) BalanceOf(ctx context.Context, addr common.Address) (*big.Int, error) {
	return big.NewInt(1000000), nil
}

func (m *mockWallet) VerifyPayment(ctx context.Context, from string, minAmount string, txHash string) (bool, error) {
	return true, nil
}

func (m *mockWallet) Address() string {
	return "0x0000000000000000000000000000000000000001"
}

func (m *mockWallet) Balance(ctx context.Context) (string, error) {
	return "1.000000", nil
}

func (m *mockWallet) WaitForConfirmationAny(ctx context.Context, txHash string, timeout time.Duration) (interface{}, error) {
	return nil, nil
}

func (m *mockWallet) Close() error {
	return nil
}

// testConfig returns a minimal config for testing
func testConfig() *config.Config {
	return &config.Config{
		Port:         "0",
		Env:          "development",
		LogLevel:     "error",
		RPCURL:       "https://sepolia.base.org",
		ChainID:      84532,
		PrivateKey:   "0000000000000000000000000000000000000000000000000000000000000001",
		USDCContract: "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
		DefaultPrice: "0.001",
	}
}

// newTestServer creates a server with mock dependencies
func newTestServer(t *testing.T) *Server {
	t.Helper()
	s, err := New(testConfig(), WithWallet(&mockWallet{}))
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	return s
}

// ---------------------------------------------------------------------------
// Health endpoint tests
// ---------------------------------------------------------------------------

func TestHealthEndpoint(t *testing.T) {
	s := newTestServer(t)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/health", nil)
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if resp["status"] != "healthy" {
		t.Errorf("Expected status 'healthy', got %v", resp["status"])
	}
}

func TestLivenessEndpoint(t *testing.T) {
	s := newTestServer(t)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/health/live", nil)
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected 200, got %d", w.Code)
	}
}

func TestReadinessEndpoint(t *testing.T) {
	s := newTestServer(t)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/health/ready", nil)
	s.router.ServeHTTP(w, req)

	// Server hasn't called Run() so ready is false
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("Expected 503 (not ready), got %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// Route registration tests
// ---------------------------------------------------------------------------

func TestEscrowRoutesRegistered(t *testing.T) {
	s := newTestServer(t)

	routes := s.router.Routes()
	escrowRoutes := map[string]bool{
		"GET:/v1/escrow/:id":              false,
		"POST:/v1/escrow":                 false,
		"POST:/v1/escrow/:id/deliver":     false,
		"POST:/v1/escrow/:id/confirm":     false,
		"POST:/v1/escrow/:id/dispute":     false,
		"GET:/v1/agents/:address/escrows": false,
	}

	for _, route := range routes {
		key := route.Method + ":" + route.Path
		if _, ok := escrowRoutes[key]; ok {
			escrowRoutes[key] = true
		}
	}

	for route, found := range escrowRoutes {
		if !found {
			t.Errorf("Escrow route %s not registered", route)
		}
	}
}

func TestCoreRoutesRegistered(t *testing.T) {
	s := newTestServer(t)

	routes := s.router.Routes()
	expected := []string{
		"GET:/health",
		"GET:/health/live",
		"GET:/health/ready",
		"POST:/v1/agents",
		"GET:/v1/agents/:address",
	}

	routeSet := make(map[string]bool)
	for _, route := range routes {
		routeSet[route.Method+":"+route.Path] = true
	}

	for _, e := range expected {
		if !routeSet[e] {
			t.Errorf("Core route %s not registered", e)
		}
	}
}

// ---------------------------------------------------------------------------
// Dashboard page test
// ---------------------------------------------------------------------------

func TestDashboardEndpoint(t *testing.T) {
	s := newTestServer(t)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected 200 for dashboard, got %d", w.Code)
	}

	if w.Header().Get("Content-Type") == "" {
		t.Error("Expected Content-Type header")
	}
}

// ---------------------------------------------------------------------------
// Agent registration test
// ---------------------------------------------------------------------------

func TestAgentRegistration(t *testing.T) {
	s := newTestServer(t)

	body := `{"address":"0xaaaa000000000000000000000000000000000001","name":"TestBot"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/agents", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated && w.Code != http.StatusOK {
		t.Errorf("Expected 201 or 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if resp["apiKey"] == nil || resp["apiKey"] == "" {
		t.Error("Expected apiKey in registration response")
	}
}

// ---------------------------------------------------------------------------
// 404 test
// ---------------------------------------------------------------------------

func TestNotFoundRoute(t *testing.T) {
	s := newTestServer(t)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/nonexistent", nil)
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected 404, got %d", w.Code)
	}
}
