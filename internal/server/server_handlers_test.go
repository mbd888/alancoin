package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mbd888/alancoin/internal/eventbus"
	"github.com/mbd888/alancoin/internal/forensics"
	"github.com/mbd888/alancoin/internal/realtime"
	"github.com/mbd888/alancoin/internal/reputation"
	"github.com/mbd888/alancoin/internal/sessionkeys"
	"github.com/mbd888/alancoin/internal/webhooks"
)

// ---------------------------------------------------------------------------
// Helper: register an agent and return the API key for authenticated requests
// ---------------------------------------------------------------------------

func registerAgent(t *testing.T, s *Server, address, name string) string {
	t.Helper()
	body := `{"address":"` + address + `","name":"` + name + `"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/agents", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("register agent: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("register agent: failed to parse response: %v", err)
	}

	apiKey, ok := resp["apiKey"].(string)
	if !ok || apiKey == "" {
		t.Fatal("register agent: missing apiKey in response")
	}
	return apiKey
}

// authedRequest creates an HTTP request with the API key in the Authorization header.
func authedRequest(method, path, apiKey string, body string) *http.Request {
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	req.Header.Set("Authorization", apiKey)
	return req
}

// ---------------------------------------------------------------------------
// Handler tests: infoHandler
// ---------------------------------------------------------------------------

func TestInfoHandler(t *testing.T) {
	s := newTestServer(t)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api", nil)
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if resp["name"] != "Alancoin" {
		t.Errorf("Expected name 'Alancoin', got %v", resp["name"])
	}
	if resp["version"] != "0.1.0" {
		t.Errorf("Expected version '0.1.0', got %v", resp["version"])
	}
	if resp["currency"] != "USDC" {
		t.Errorf("Expected currency 'USDC', got %v", resp["currency"])
	}
}

// ---------------------------------------------------------------------------
// Handler tests: platformHandler
// ---------------------------------------------------------------------------

func TestPlatformHandler(t *testing.T) {
	s := newTestServer(t)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/platform", nil)
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	platform, ok := resp["platform"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected 'platform' object in response")
	}
	if platform["name"] != "Alancoin" {
		t.Errorf("Expected platform name 'Alancoin', got %v", platform["name"])
	}

	instructions, ok := resp["instructions"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected 'instructions' object in response")
	}
	if instructions["deposit"] == nil {
		t.Error("Expected 'deposit' instruction")
	}
}

// ---------------------------------------------------------------------------
// Handler tests: enhancedStatsHandler
// ---------------------------------------------------------------------------

func TestEnhancedStatsHandler(t *testing.T) {
	s := newTestServer(t)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/network/stats/enhanced", nil)
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	// Should include base stats fields
	if _, ok := resp["totalAgents"]; !ok {
		t.Error("Expected 'totalAgents' in enhanced stats")
	}
	if _, ok := resp["totalServices"]; !ok {
		t.Error("Expected 'totalServices' in enhanced stats")
	}
	if _, ok := resp["totalTransactions"]; !ok {
		t.Error("Expected 'totalTransactions' in enhanced stats")
	}

	// Should include session key count (in-memory mode has session manager)
	if _, ok := resp["activeSessionKeys"]; !ok {
		t.Error("Expected 'activeSessionKeys' in enhanced stats")
	}
}

// ---------------------------------------------------------------------------
// Handler tests: getTimeline
// ---------------------------------------------------------------------------

func TestGetTimeline_Empty(t *testing.T) {
	s := newTestServer(t)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/timeline", nil)
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	count, ok := resp["count"].(float64)
	if !ok {
		t.Fatal("Expected 'count' field in response")
	}
	if count != 0 {
		t.Errorf("Expected 0 timeline items, got %v", count)
	}
}

// ---------------------------------------------------------------------------
// Handler tests: registerAgentWithAPIKey
// ---------------------------------------------------------------------------

func TestRegisterAgent_InvalidBody(t *testing.T) {
	s := newTestServer(t)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/agents", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestRegisterAgent_InvalidAddress(t *testing.T) {
	s := newTestServer(t)

	body := `{"address":"not-a-valid-address","name":"TestBot"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/agents", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected 400, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}
	if resp["error"] != "invalid_address" {
		t.Errorf("Expected error 'invalid_address', got %v", resp["error"])
	}
}

func TestRegisterAgent_DuplicateAddress(t *testing.T) {
	s := newTestServer(t)
	addr := "0xbbbb000000000000000000000000000000000001"

	// First registration
	registerAgent(t, s, addr, "First")

	// Second registration with same address
	body := `{"address":"` + addr + `","name":"Second"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/agents", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("Expected 409, got %d: %s", w.Code, w.Body.String())
	}
}

func TestRegisterAgent_WithMetadata(t *testing.T) {
	s := newTestServer(t)

	body := `{"address":"0xcccc000000000000000000000000000000000001","name":"MetaBot","description":"A bot with metadata","metadata":{"type":"ai"}}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/agents", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("Expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if resp["apiKey"] == nil || resp["apiKey"] == "" {
		t.Error("Expected apiKey in registration response")
	}
	if resp["keyId"] == nil {
		t.Error("Expected keyId in registration response")
	}
	if resp["warning"] == nil {
		t.Error("Expected security warning in response")
	}
}

// ---------------------------------------------------------------------------
// Handler tests: healthHandler (in-memory, no db)
// ---------------------------------------------------------------------------

func TestHealthHandler_InMemory(t *testing.T) {
	s := newTestServer(t)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/health", nil)
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d", w.Code)
	}

	var resp HealthResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if resp.Status != "healthy" {
		t.Errorf("Expected status 'healthy', got %q", resp.Status)
	}
	if resp.Version != "0.1.0" {
		t.Errorf("Expected version '0.1.0', got %q", resp.Version)
	}
	if resp.Timestamp == "" {
		t.Error("Expected non-empty timestamp")
	}
}

// ---------------------------------------------------------------------------
// Handler tests: livenessHandler
// ---------------------------------------------------------------------------

func TestLivenessHandler_Unhealthy(t *testing.T) {
	s := newTestServer(t)
	s.healthy.Store(false)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/health/live", nil)
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("Expected 503 for unhealthy, got %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// Handler tests: readinessHandler
// ---------------------------------------------------------------------------

func TestReadinessHandler_Draining(t *testing.T) {
	s := newTestServer(t)
	s.ready.Store(true)
	s.isDraining.Store(true)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/health/ready", nil)
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("Expected 503 for draining, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to parse: %v", err)
	}
	if resp["status"] != "draining" {
		t.Errorf("Expected status 'draining', got %v", resp["status"])
	}
}

func TestReadinessHandler_NotReady(t *testing.T) {
	s := newTestServer(t)
	// Server hasn't called Run(), so ready is false
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/health/ready", nil)
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("Expected 503 (not ready), got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to parse: %v", err)
	}
	if resp["status"] != "not_ready" {
		t.Errorf("Expected status 'not_ready', got %v", resp["status"])
	}
}

// ---------------------------------------------------------------------------
// Draining middleware
// ---------------------------------------------------------------------------

func TestDrainingMiddleware_RejectsNonHealthRequests(t *testing.T) {
	s := newTestServer(t)
	s.isDraining.Store(true)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/platform", nil)
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("Expected 503 for draining, got %d", w.Code)
	}

	if w.Header().Get("Retry-After") != "5" {
		t.Errorf("Expected Retry-After header, got %q", w.Header().Get("Retry-After"))
	}
}

func TestDrainingMiddleware_AllowsHealthEndpoints(t *testing.T) {
	s := newTestServer(t)
	s.isDraining.Store(true)

	for _, path := range []string{"/health", "/health/live", "/health/ready"} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", path, nil)
		s.router.ServeHTTP(w, req)

		// Health endpoints should not get 503 from the drain middleware
		// (they may still return 503 for other reasons like not ready)
		if w.Code == http.StatusServiceUnavailable {
			var resp map[string]interface{}
			json.Unmarshal(w.Body.Bytes(), &resp)
			if resp["error"] == "draining" {
				t.Errorf("%s should not get drain 503", path)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Authenticated endpoints
// ---------------------------------------------------------------------------

func TestGetAgentBalance_RequiresAuth(t *testing.T) {
	s := newTestServer(t)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/agents/0xaaaa000000000000000000000000000000000001/balance", nil)
	s.router.ServeHTTP(w, req)

	// Should fail with 401 (no API key)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected 401 for unauthenticated balance request, got %d", w.Code)
	}
}

func TestGetAgentBalance_WithAuth(t *testing.T) {
	s := newTestServer(t)
	addr := "0xdddd000000000000000000000000000000000001"
	apiKey := registerAgent(t, s, addr, "BalanceBot")

	w := httptest.NewRecorder()
	req := authedRequest("GET", "/v1/agents/"+addr+"/balance", apiKey, "")
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if _, ok := resp["balance"]; !ok {
		t.Error("Expected 'balance' in response")
	}
}

func TestDeleteAgent_RequiresOwnership(t *testing.T) {
	s := newTestServer(t)
	addr1 := "0xeeee000000000000000000000000000000000001"
	addr2 := "0xeeee000000000000000000000000000000000002"
	apiKey1 := registerAgent(t, s, addr1, "Agent1")
	_ = registerAgent(t, s, addr2, "Agent2")

	// Try to delete agent2 using agent1's key
	w := httptest.NewRecorder()
	req := authedRequest("DELETE", "/v1/agents/"+addr2, apiKey1, "")
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("Expected 403 for non-owner delete, got %d: %s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// WebSocket auth
// ---------------------------------------------------------------------------

func TestWebSocket_RequiresAuth(t *testing.T) {
	s := newTestServer(t)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/ws", nil)
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected 401 for unauthenticated websocket, got %d", w.Code)
	}
}

func TestWebSocket_InvalidKey(t *testing.T) {
	s := newTestServer(t)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/ws?token=invalid-key", nil)
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected 401 for invalid websocket token, got %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// Public read endpoints
// ---------------------------------------------------------------------------

func TestListAgents(t *testing.T) {
	s := newTestServer(t)
	registerAgent(t, s, "0xaaaa000000000000000000000000000000000010", "ListBot")

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/agents", nil)
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestGetAgent(t *testing.T) {
	s := newTestServer(t)
	addr := "0xaaaa000000000000000000000000000000000020"
	registerAgent(t, s, addr, "GetBot")

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/agents/"+addr, nil)
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestDiscoverServices(t *testing.T) {
	s := newTestServer(t)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/services", nil)
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestNetworkStats(t *testing.T) {
	s := newTestServer(t)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/network/stats", nil)
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPublicFeed(t *testing.T) {
	s := newTestServer(t)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/feed", nil)
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAuthInfo(t *testing.T) {
	s := newTestServer(t)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/auth/info", nil)
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Reputation routes (public)
// ---------------------------------------------------------------------------

func TestReputationRoutes(t *testing.T) {
	s := newTestServer(t)
	addr := "0xaaaa000000000000000000000000000000000030"
	registerAgent(t, s, addr, "RepBot")

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/reputation/"+addr, nil)
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Flywheel routes (public)
// ---------------------------------------------------------------------------

func TestFlywheelHealth(t *testing.T) {
	s := newTestServer(t)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/flywheel/health", nil)
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Intelligence routes (public)
// ---------------------------------------------------------------------------

func TestIntelligenceRoutes(t *testing.T) {
	s := newTestServer(t)
	addr := "0xaaaa000000000000000000000000000000000040"
	registerAgent(t, s, addr, "IntelBot")

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/agents/"+addr+"/intelligence", nil)
	s.router.ServeHTTP(w, req)

	// May return 200 with empty profile or 404 — both acceptable
	if w.Code != http.StatusOK && w.Code != http.StatusNotFound {
		t.Errorf("Expected 200 or 404, got %d: %s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Helper function tests
// ---------------------------------------------------------------------------

func TestMaskDSN(t *testing.T) {
	t.Run("with password", func(t *testing.T) {
		got := maskDSN("postgres://user:secret@localhost:5432/db")
		if strings.Contains(got, "secret") {
			t.Errorf("maskDSN should hide password, got %q", got)
		}
		if !strings.Contains(got, "user") {
			t.Errorf("maskDSN should preserve username, got %q", got)
		}
		if !strings.Contains(got, "localhost") {
			t.Errorf("maskDSN should preserve host, got %q", got)
		}
	})

	t.Run("without password", func(t *testing.T) {
		got := maskDSN("postgres://localhost:5432/db")
		if !strings.Contains(got, "localhost") {
			t.Errorf("maskDSN should preserve host, got %q", got)
		}
	})

	t.Run("empty string", func(t *testing.T) {
		got := maskDSN("")
		// Should not panic
		_ = got
	})
}

func TestAppendDSNParams(t *testing.T) {
	tests := []struct {
		name    string
		dsn     string
		ct, st  int
		wantSub string
	}{
		{
			"postgres URL without params",
			"postgres://localhost/db",
			5, 30000,
			"?connect_timeout=5&statement_timeout=30000",
		},
		{
			"postgres URL with existing params",
			"postgres://localhost/db?sslmode=disable",
			5, 30000,
			"&connect_timeout=5&statement_timeout=30000",
		},
		{
			"key-value format",
			"host=localhost dbname=db",
			5, 30000,
			" connect_timeout=5 statement_timeout=30000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := appendDSNParams(tt.dsn, tt.ct, tt.st)
			if !strings.Contains(got, tt.wantSub) {
				t.Errorf("appendDSNParams(%q, %d, %d) = %q, expected to contain %q",
					tt.dsn, tt.ct, tt.st, got, tt.wantSub)
			}
		})
	}
}

func TestTimerStatus(t *testing.T) {
	if got := timerStatus(nil); got != "not_configured" {
		t.Errorf("timerStatus(nil) = %q, want %q", got, "not_configured")
	}

	// Non-runnable type
	if got := timerStatus("not-a-timer"); got != "unknown" {
		t.Errorf("timerStatus(string) = %q, want %q", got, "unknown")
	}

}

func TestGenerateRequestID(t *testing.T) {
	id1 := generateRequestID()
	id2 := generateRequestID()

	if id1 == "" {
		t.Error("Expected non-empty request ID")
	}
	if id1 == id2 {
		t.Error("Expected unique request IDs")
	}
	if len(id1) != 32 {
		t.Errorf("Expected 32-char hex ID, got %d chars: %s", len(id1), id1)
	}
}

func TestCacheControlMiddleware(t *testing.T) {
	s := newTestServer(t)

	// /v1/services uses cacheControl(30)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/services", nil)
	s.router.ServeHTTP(w, req)

	cc := w.Header().Get("Cache-Control")
	if !strings.Contains(cc, "max-age=30") {
		t.Errorf("Expected Cache-Control with max-age=30, got %q", cc)
	}
}

func TestSortTimelineItems(t *testing.T) {
	now := time.Now()
	items := []TimelineItem{
		{Type: "a", Timestamp: now.Add(-2 * time.Hour)},
		{Type: "b", Timestamp: now},
		{Type: "c", Timestamp: now.Add(-1 * time.Hour)},
	}

	sortTimelineItems(items)

	// Should be sorted descending (newest first)
	if items[0].Type != "b" {
		t.Errorf("Expected 'b' first (newest), got %q", items[0].Type)
	}
	if items[1].Type != "c" {
		t.Errorf("Expected 'c' second, got %q", items[1].Type)
	}
	if items[2].Type != "a" {
		t.Errorf("Expected 'a' last (oldest), got %q", items[2].Type)
	}
}

func TestSortTimelineItems_Empty(t *testing.T) {
	// Should not panic on empty or single-item slices
	sortTimelineItems(nil)
	sortTimelineItems([]TimelineItem{})
	sortTimelineItems([]TimelineItem{{Type: "solo"}})
}

func TestFormatBigInt(t *testing.T) {
	tests := []struct {
		name string
		in   *big.Int
		want string
	}{
		{"nil", nil, "0.000000"},
		{"zero", big.NewInt(0), "0.000000"},
		{"one USDC", big.NewInt(1_000_000), "1.000000"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatBigInt(tt.in)
			if got != tt.want {
				t.Errorf("formatBigInt(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Adapter tests: realtimeEventEmitter
// ---------------------------------------------------------------------------

func TestRealtimeEventEmitter_NilHub(t *testing.T) {
	e := &realtimeEventEmitter{hub: nil}
	// Should not panic
	e.EmitTransaction(map[string]interface{}{"test": true})
	e.EmitSessionKeyUsed("key1", "0xabc", "1.00")
}

func TestRealtimeEventEmitter_WithHub(t *testing.T) {
	hub := realtime.NewHub(nil)
	e := &realtimeEventEmitter{hub: hub}
	// Should not panic (hub exists but no clients)
	e.EmitTransaction(map[string]interface{}{"test": true})
	e.EmitSessionKeyUsed("key1", "0xabc", "1.00")
}

// ---------------------------------------------------------------------------
// Adapter tests: gatewayRecorderAdapter
// ---------------------------------------------------------------------------

func TestGatewayRecorderAdapter(t *testing.T) {
	s := newTestServer(t)
	adapter := &gatewayRecorderAdapter{r: s.registry}

	ctx := context.Background()
	err := adapter.RecordTransaction(ctx, "tx_001", "0xaaa", "0xbbb", "1.00", "svc_1", "completed")
	if err != nil {
		t.Fatalf("RecordTransaction failed: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Adapter tests: gatewayLedgerAdapter
// ---------------------------------------------------------------------------

func TestGatewayLedgerAdapter(t *testing.T) {
	s := newTestServer(t)
	adapter := &gatewayLedgerAdapter{l: s.ledgerService}

	ctx := context.Background()

	// Fund the agent first
	_ = s.ledger.StoreRef().Credit(ctx, "0xdddd000000000000000000000000000000000099", "100.000000", "deposit_1", "test deposit")

	// Hold
	err := adapter.Hold(ctx, "0xdddd000000000000000000000000000000000099", "5.000000", "ref_1")
	if err != nil {
		t.Fatalf("Hold failed: %v", err)
	}

	// Release hold
	err = adapter.ReleaseHold(ctx, "0xdddd000000000000000000000000000000000099", "5.000000", "ref_1")
	if err != nil {
		t.Fatalf("ReleaseHold failed: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Adapter tests: escrowLedgerAdapter
// ---------------------------------------------------------------------------

func TestEscrowLedgerAdapter(t *testing.T) {
	s := newTestServer(t)
	adapter := &escrowLedgerAdapter{l: s.ledgerService}

	ctx := context.Background()
	addr := "0xdddd000000000000000000000000000000000088"

	// Fund the agent
	_ = s.ledger.StoreRef().Credit(ctx, addr, "100.000000", "deposit_2", "test deposit")

	// EscrowLock (use small amount within supervisor per-tx limits)
	err := adapter.EscrowLock(ctx, addr, "1.000000", "esc_ref_1")
	if err != nil {
		t.Fatalf("EscrowLock failed: %v", err)
	}

	// RefundEscrow
	err = adapter.RefundEscrow(ctx, addr, "1.000000", "esc_ref_1")
	if err != nil {
		t.Fatalf("RefundEscrow failed: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Adapter tests: streamLedgerAdapter
// ---------------------------------------------------------------------------

func TestStreamLedgerAdapter(t *testing.T) {
	s := newTestServer(t)
	adapter := &streamLedgerAdapter{l: s.ledgerService}

	ctx := context.Background()
	addr := "0xdddd000000000000000000000000000000000077"

	// Fund
	_ = s.ledger.StoreRef().Credit(ctx, addr, "100.000000", "deposit_3", "test deposit")

	// Hold
	err := adapter.Hold(ctx, addr, "5.000000", "stream_ref_1")
	if err != nil {
		t.Fatalf("Hold failed: %v", err)
	}

	// Release
	err = adapter.ReleaseHold(ctx, addr, "5.000000", "stream_ref_1")
	if err != nil {
		t.Fatalf("ReleaseHold failed: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Adapter tests: webhookAlertNotifier
// ---------------------------------------------------------------------------

func TestWebhookAlertNotifier(t *testing.T) {
	s := newTestServer(t)
	notifier := &webhookAlertNotifier{d: s.webhooks}

	ctx := context.Background()
	// Should not error even with no subscribers
	err := notifier.NotifyAlert(ctx, sessionkeys.AlertEvent{
		Type:        "budget_warning",
		KeyID:       "key_1",
		OwnerAddr:   "0xaaa",
		TriggeredAt: time.Now(),
	})
	if err != nil {
		// Expected: no webhook subscribers for this agent, error is acceptable
		t.Logf("NotifyAlert returned (expected): %v", err)
	}

	err = notifier.NotifyAlert(ctx, sessionkeys.AlertEvent{
		Type:        "expiring",
		KeyID:       "key_2",
		OwnerAddr:   "0xbbb",
		TriggeredAt: time.Now(),
	})
	if err != nil {
		t.Logf("NotifyAlert returned (expected): %v", err)
	}
}

// ---------------------------------------------------------------------------
// Adapter tests: receiptIssuerAdapter
// ---------------------------------------------------------------------------

func TestReceiptIssuerAdapter(t *testing.T) {
	s := newTestServer(t)
	adapter := &receiptIssuerAdapter{svc: s.receiptService}

	ctx := context.Background()
	err := adapter.IssueReceipt(ctx, "gateway", "ref_1", "0xaaa", "0xbbb", "1.000000", "svc_1", "settled", "")
	if err != nil {
		t.Fatalf("IssueReceipt failed: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Adapter tests: kyaReputationAdapter
// ---------------------------------------------------------------------------

func TestKyaReputationAdapter_NilProvider(t *testing.T) {
	adapter := &kyaReputationAdapter{rep: nil}
	ctx := context.Background()

	score, err := adapter.GetScore(ctx, "0xaaa")
	if err != nil {
		t.Fatalf("GetScore failed: %v", err)
	}
	if score != 50.0 {
		t.Errorf("Expected default score 50.0, got %v", score)
	}

	rate, err := adapter.GetSuccessRate(ctx, "0xaaa")
	if err != nil {
		t.Fatalf("GetSuccessRate failed: %v", err)
	}
	if rate != 0.95 {
		t.Errorf("Expected default success rate 0.95, got %v", rate)
	}

	dispute, err := adapter.GetDisputeRate(ctx, "0xaaa")
	if err != nil {
		t.Fatalf("GetDisputeRate failed: %v", err)
	}
	if dispute != 0.02 {
		t.Errorf("Expected default dispute rate 0.02, got %v", dispute)
	}

	txCount, err := adapter.GetTxCount(ctx, "0xaaa")
	if err != nil {
		t.Fatalf("GetTxCount failed: %v", err)
	}
	if txCount != 50 {
		t.Errorf("Expected default tx count 50, got %v", txCount)
	}
}

// ---------------------------------------------------------------------------
// Adapter tests: kyaTrustGateAdapter
// ---------------------------------------------------------------------------

func TestKyaTrustGateAdapter_NilService(t *testing.T) {
	adapter := &kyaTrustGateAdapter{kyaSvc: nil}
	ctx := context.Background()

	err := adapter.CheckCounterpartyTrust(ctx, "0xaaa")
	if err != nil {
		t.Errorf("Expected nil error for nil KYA service, got: %v", err)
	}
}

func TestKyaTrustGateAdapter_NoCertificate(t *testing.T) {
	s := newTestServer(t)
	adapter := &kyaTrustGateAdapter{kyaSvc: s.kyaService}
	ctx := context.Background()

	// Agent without KYA certificate — should pass (KYA is optional)
	err := adapter.CheckCounterpartyTrust(ctx, "0xaaaa000000000000000000000000000000000099")
	if err != nil {
		t.Errorf("Expected nil error for agent without KYA cert, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Adapter tests: budgetPreFlightAdapter
// ---------------------------------------------------------------------------

func TestBudgetPreFlightAdapter_NoCostCenters(t *testing.T) {
	s := newTestServer(t)
	adapter := &budgetPreFlightAdapter{svc: s.chargebackService}
	ctx := context.Background()

	// No cost centers configured — should pass silently
	err := adapter.CheckBudget(ctx, "tenant_1", "0xaaa", "1.00")
	if err != nil {
		t.Errorf("Expected nil error with no cost centers, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Adapter tests: chargebackGatewayAdapter
// ---------------------------------------------------------------------------

func TestChargebackGatewayAdapter_NoCostCenters(t *testing.T) {
	s := newTestServer(t)
	adapter := &chargebackGatewayAdapter{svc: s.chargebackService}
	ctx := context.Background()

	// No cost centers — should skip silently
	err := adapter.RecordGatewaySpend(ctx, "tenant_1", "0xaaa", "1.00", "compute", "sess_1")
	if err != nil {
		t.Errorf("Expected nil error with no cost centers, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Adapter tests: forensicsGatewayAdapter
// ---------------------------------------------------------------------------

func TestForensicsGatewayAdapter(t *testing.T) {
	s := newTestServer(t)
	adapter := &forensicsGatewayAdapter{svc: s.forensicsService}
	ctx := context.Background()

	err := adapter.IngestSpend(ctx, "0xaaa", "0xbbb", 1.5, "compute")
	if err != nil {
		t.Fatalf("IngestSpend failed: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Adapter tests: intelligenceTraceRankAdapter
// ---------------------------------------------------------------------------

func TestIntelligenceTraceRankAdapter_NilStore(t *testing.T) {
	adapter := &intelligenceTraceRankAdapter{store: nil}
	ctx := context.Background()

	score, inDeg, outDeg, inVol, outVol, err := adapter.GetScore(ctx, "0xaaa")
	if err != nil {
		t.Fatalf("GetScore failed: %v", err)
	}
	if score != 0 || inDeg != 0 || outDeg != 0 || inVol != 0 || outVol != 0 {
		t.Error("Expected all zeros for nil store")
	}

	all, err := adapter.GetAllScores(ctx)
	if err != nil {
		t.Fatalf("GetAllScores failed: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("Expected empty map for nil store, got %d entries", len(all))
	}
}

func TestIntelligenceTraceRankAdapter_WithStore(t *testing.T) {
	s := newTestServer(t)
	adapter := &intelligenceTraceRankAdapter{store: s.traceRankStore}
	ctx := context.Background()

	// No scores stored yet
	_, _, _, _, _, err := adapter.GetScore(ctx, "0xaaa")
	if err != nil {
		t.Logf("GetScore returned (expected for empty store): %v", err)
	}

	all, err := adapter.GetAllScores(ctx)
	if err != nil {
		t.Fatalf("GetAllScores failed: %v", err)
	}
	if all == nil {
		t.Error("Expected non-nil map from GetAllScores")
	}
}

// ---------------------------------------------------------------------------
// Adapter tests: intelligenceForensicsAdapter
// ---------------------------------------------------------------------------

func TestIntelligenceForensicsAdapter_NilService(t *testing.T) {
	adapter := &intelligenceForensicsAdapter{svc: nil}
	ctx := context.Background()

	txCount, mean, stddev, err := adapter.GetBaseline(ctx, "0xaaa")
	if err != nil {
		t.Fatalf("GetBaseline failed: %v", err)
	}
	if txCount != 0 || mean != 0 || stddev != 0 {
		t.Error("Expected all zeros for nil service")
	}

	total, critical, err := adapter.CountAlerts30d(ctx, "0xaaa")
	if err != nil {
		t.Fatalf("CountAlerts30d failed: %v", err)
	}
	if total != 0 || critical != 0 {
		t.Error("Expected zero alerts for nil service")
	}
}

func TestIntelligenceForensicsAdapter_WithService(t *testing.T) {
	s := newTestServer(t)
	adapter := &intelligenceForensicsAdapter{svc: s.forensicsService}
	ctx := context.Background()

	// No baseline data
	txCount, _, _, err := adapter.GetBaseline(ctx, "0xaaa")
	if err != nil {
		t.Logf("GetBaseline returned: %v", err)
	}
	if txCount != 0 {
		t.Logf("txCount = %d (expected 0 for new agent)", txCount)
	}

	total, critical, err := adapter.CountAlerts30d(ctx, "0xaaa")
	if err != nil {
		t.Fatalf("CountAlerts30d failed: %v", err)
	}
	if total != 0 || critical != 0 {
		t.Error("Expected zero alerts for agent with no history")
	}
}

// ---------------------------------------------------------------------------
// Adapter tests: intelligenceReputationAdapter
// ---------------------------------------------------------------------------

func TestIntelligenceReputationAdapter(t *testing.T) {
	s := newTestServer(t)

	// Create a reputation provider the same way setupRoutes does
	repProvider := reputation.NewRegistryProvider(s.registry)
	adapter := &intelligenceReputationAdapter{provider: repProvider}
	ctx := context.Background()

	// Register an agent so metrics can be looked up
	addr := "0xaaaa000000000000000000000000000000000050"
	registerAgent(t, s, addr, "RepTestBot")

	data, err := adapter.GetMetrics(ctx, addr)
	if err != nil {
		t.Fatalf("GetMetrics failed: %v", err)
	}
	if data == nil {
		t.Fatal("Expected non-nil reputation data")
	}

	all, err := adapter.GetAllMetrics(ctx)
	if err != nil {
		t.Fatalf("GetAllMetrics failed: %v", err)
	}
	if all == nil {
		t.Error("Expected non-nil map from GetAllMetrics")
	}
}

// ---------------------------------------------------------------------------
// Adapter tests: intelligenceAgentSourceAdapter
// ---------------------------------------------------------------------------

func TestIntelligenceAgentSourceAdapter(t *testing.T) {
	s := newTestServer(t)
	adapter := &intelligenceAgentSourceAdapter{store: s.registry}
	ctx := context.Background()

	// Initially empty
	addrs, err := adapter.ListAllAddresses(ctx)
	if err != nil {
		t.Fatalf("ListAllAddresses failed: %v", err)
	}
	if len(addrs) != 0 {
		t.Errorf("Expected 0 addresses, got %d", len(addrs))
	}

	// Register an agent
	registerAgent(t, s, "0xaaaa000000000000000000000000000000000060", "SourceBot")

	addrs, err = adapter.ListAllAddresses(ctx)
	if err != nil {
		t.Fatalf("ListAllAddresses failed: %v", err)
	}
	if len(addrs) != 1 {
		t.Errorf("Expected 1 address, got %d", len(addrs))
	}
}

// ---------------------------------------------------------------------------
// Adapter tests: kyaRegistryAdapter
// ---------------------------------------------------------------------------

func TestKyaRegistryAdapter(t *testing.T) {
	s := newTestServer(t)
	adapter := &kyaRegistryAdapter{reg: s.registry}
	ctx := context.Background()

	addr := "0xaaaa000000000000000000000000000000000070"
	registerAgent(t, s, addr, "KYABot")

	name, err := adapter.GetAgentName(ctx, addr)
	if err != nil {
		t.Fatalf("GetAgentName failed: %v", err)
	}
	if name != "KYABot" {
		t.Errorf("Expected name 'KYABot', got %q", name)
	}

	createdAt, err := adapter.GetAgentCreatedAt(ctx, addr)
	if err != nil {
		t.Fatalf("GetAgentCreatedAt failed: %v", err)
	}
	if createdAt.IsZero() {
		t.Error("Expected non-zero createdAt")
	}
}

// ---------------------------------------------------------------------------
// Adapter tests: gatewayRegistryAdapter
// ---------------------------------------------------------------------------

func TestGatewayRegistryAdapter_ListServices(t *testing.T) {
	s := newTestServer(t)
	adapter := &gatewayRegistryAdapter{store: s.registry, traceRankStore: s.traceRankStore}
	ctx := context.Background()

	// No services registered yet
	candidates, err := adapter.ListServices(ctx, "compute", "10.00")
	if err != nil {
		t.Fatalf("ListServices failed: %v", err)
	}
	if len(candidates) != 0 {
		t.Errorf("Expected 0 candidates, got %d", len(candidates))
	}
}

// ---------------------------------------------------------------------------
// Adapter tests: gatewayTenantSettingsAdapter
// ---------------------------------------------------------------------------

func TestGatewayTenantSettingsAdapter_NotFound(t *testing.T) {
	s := newTestServer(t)
	adapter := &gatewayTenantSettingsAdapter{store: s.tenantStore}
	ctx := context.Background()

	_, err := adapter.GetTakeRateBPS(ctx, "nonexistent")
	if err == nil {
		t.Error("Expected error for nonexistent tenant")
	}

	_, err = adapter.GetTenantStatus(ctx, "nonexistent")
	if err == nil {
		t.Error("Expected error for nonexistent tenant")
	}

	_, err = adapter.GetStripeCustomerID(ctx, "nonexistent")
	if err == nil {
		t.Error("Expected error for nonexistent tenant")
	}
}

// ---------------------------------------------------------------------------
// Adapter tests: eventBusGatewayAdapter
// ---------------------------------------------------------------------------

func TestEventBusGatewayAdapter(t *testing.T) {
	s := newTestServer(t)
	adapter := &eventBusGatewayAdapter{bus: s.eventBus}
	ctx := context.Background()

	err := adapter.PublishSettlement(ctx, "sess_1", "tenant_1", "0xaaa", "0xbbb",
		"1.500000", "0.015000", "compute", "svc_1", "ref_1", 150)
	if err != nil {
		t.Fatalf("PublishSettlement failed: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Adapter tests: adminGatewayAdapter
// ---------------------------------------------------------------------------

func TestAdminGatewayAdapter_GetSession_NotFound(t *testing.T) {
	s := newTestServer(t)
	adapter := &adminGatewayAdapter{svc: s.gatewayService}
	ctx := context.Background()

	_, err := adapter.GetSession(ctx, "nonexistent")
	if err == nil {
		t.Error("Expected error for nonexistent session")
	}
}

// ---------------------------------------------------------------------------
// Adapter tests: adminEscrowAdapter
// ---------------------------------------------------------------------------

func TestAdminEscrowAdapter_ForceCloseExpired(t *testing.T) {
	s := newTestServer(t)
	adapter := &adminEscrowAdapter{svc: s.escrowService}
	ctx := context.Background()

	closed, err := adapter.ForceCloseExpired(ctx)
	if err != nil {
		t.Fatalf("ForceCloseExpired failed: %v", err)
	}
	if closed != 0 {
		t.Errorf("Expected 0 closed, got %d", closed)
	}
}

// ---------------------------------------------------------------------------
// Adapter tests: adminCoalitionAdapter
// ---------------------------------------------------------------------------

func TestAdminCoalitionAdapter_ForceCloseExpired(t *testing.T) {
	s := newTestServer(t)
	adapter := &adminCoalitionAdapter{svc: s.coalitionService}
	ctx := context.Background()

	closed, err := adapter.ForceCloseExpired(ctx)
	if err != nil {
		t.Fatalf("ForceCloseExpired failed: %v", err)
	}
	if closed != 0 {
		t.Errorf("Expected 0 closed, got %d", closed)
	}
}

// ---------------------------------------------------------------------------
// Adapter tests: adminStreamAdapter
// ---------------------------------------------------------------------------

func TestAdminStreamAdapter_ForceCloseStale(t *testing.T) {
	s := newTestServer(t)
	adapter := &adminStreamAdapter{svc: s.streamService}
	ctx := context.Background()

	closed, err := adapter.ForceCloseStale(ctx)
	if err != nil {
		t.Fatalf("ForceCloseStale failed: %v", err)
	}
	if closed != 0 {
		t.Errorf("Expected 0 closed, got %d", closed)
	}
}

// ---------------------------------------------------------------------------
// Adapter tests: coalitionRealtimeAdapter
// ---------------------------------------------------------------------------

func TestCoalitionRealtimeAdapter(t *testing.T) {
	hub := realtime.NewHub(nil)
	adapter := &coalitionRealtimeAdapter{hub: hub}

	// Should not panic
	adapter.BroadcastCoalitionEvent("settle", "coal_1", "0xaaa", "settled")
}

// ---------------------------------------------------------------------------
// Adapter tests: coalitionContractAdapter
// ---------------------------------------------------------------------------

func TestCoalitionContractAdapter_GetContract_NotFound(t *testing.T) {
	s := newTestServer(t)
	adapter := &coalitionContractAdapter{svc: s.contractService}
	ctx := context.Background()

	_, err := adapter.GetContractByEscrow(ctx, "nonexistent")
	if err == nil {
		t.Error("Expected error for nonexistent contract")
	}
}

// ---------------------------------------------------------------------------
// Adapter tests: adminWSStateProvider
// ---------------------------------------------------------------------------

func TestAdminWSStateProvider(t *testing.T) {
	hub := realtime.NewHub(nil)
	provider := &adminWSStateProvider{hub: hub}

	state := provider.AdminState(context.Background())
	if state == nil {
		t.Fatal("Expected non-nil state map")
	}
}

// ---------------------------------------------------------------------------
// Event bus consumer tests: makeForensicsConsumer
// ---------------------------------------------------------------------------

func TestMakeForensicsConsumer(t *testing.T) {
	s := newTestServer(t)
	consumer := s.makeForensicsConsumer()

	ctx := context.Background()

	// Create a valid settlement event
	event, err := eventbus.NewEvent(eventbus.TopicSettlement, "0xbuyer", eventbus.SettlementPayload{
		SessionID:   "sess_1",
		BuyerAddr:   "0xbuyer",
		SellerAddr:  "0xseller",
		Amount:      "1.500000",
		ServiceType: "compute",
		AmountFloat: 1.5,
	})
	if err != nil {
		t.Fatalf("Failed to create event: %v", err)
	}

	// Process batch
	err = consumer(ctx, []eventbus.Event{event})
	if err != nil {
		t.Fatalf("Forensics consumer failed: %v", err)
	}
}

func TestMakeForensicsConsumer_InvalidPayload(t *testing.T) {
	s := newTestServer(t)
	consumer := s.makeForensicsConsumer()

	ctx := context.Background()
	// Invalid JSON payload — should be skipped without error
	badEvent := eventbus.Event{
		ID:      "evt_bad",
		Topic:   eventbus.TopicSettlement,
		Payload: []byte(`{invalid json`),
	}

	err := consumer(ctx, []eventbus.Event{badEvent})
	if err != nil {
		t.Fatalf("Expected nil error for invalid payload, got: %v", err)
	}
}

func TestMakeForensicsConsumer_NilService(t *testing.T) {
	s := newTestServer(t)
	s.forensicsService = nil
	consumer := s.makeForensicsConsumer()

	ctx := context.Background()
	event, _ := eventbus.NewEvent(eventbus.TopicSettlement, "0xbuyer", eventbus.SettlementPayload{
		BuyerAddr:   "0xbuyer",
		AmountFloat: 1.0,
	})

	err := consumer(ctx, []eventbus.Event{event})
	if err != nil {
		t.Fatalf("Expected nil error for nil service, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Event bus consumer tests: makeChargebackConsumer
// ---------------------------------------------------------------------------

func TestMakeChargebackConsumer(t *testing.T) {
	s := newTestServer(t)
	consumer := s.makeChargebackConsumer()

	ctx := context.Background()

	// Event with no tenant ID — should be skipped
	event, err := eventbus.NewEvent(eventbus.TopicSettlement, "0xbuyer", eventbus.SettlementPayload{
		SessionID:   "sess_1",
		TenantID:    "", // empty tenant
		BuyerAddr:   "0xbuyer",
		SellerAddr:  "0xseller",
		Amount:      "1.500000",
		ServiceType: "compute",
	})
	if err != nil {
		t.Fatalf("Failed to create event: %v", err)
	}

	err = consumer(ctx, []eventbus.Event{event})
	if err != nil {
		t.Fatalf("Chargeback consumer failed: %v", err)
	}
}

func TestMakeChargebackConsumer_WithTenant(t *testing.T) {
	s := newTestServer(t)
	consumer := s.makeChargebackConsumer()

	ctx := context.Background()

	event, _ := eventbus.NewEvent(eventbus.TopicSettlement, "0xbuyer", eventbus.SettlementPayload{
		SessionID:   "sess_2",
		TenantID:    "tenant_1",
		BuyerAddr:   "0xbuyer",
		SellerAddr:  "0xseller",
		Amount:      "1.500000",
		ServiceType: "compute",
	})

	// No cost centers configured — should succeed (skip silently)
	err := consumer(ctx, []eventbus.Event{event})
	if err != nil {
		t.Fatalf("Chargeback consumer failed: %v", err)
	}
}

func TestMakeChargebackConsumer_NilService(t *testing.T) {
	s := newTestServer(t)
	s.chargebackService = nil
	consumer := s.makeChargebackConsumer()

	ctx := context.Background()
	event, _ := eventbus.NewEvent(eventbus.TopicSettlement, "0xbuyer", eventbus.SettlementPayload{
		TenantID:  "tenant_1",
		BuyerAddr: "0xbuyer",
	})

	err := consumer(ctx, []eventbus.Event{event})
	if err != nil {
		t.Fatalf("Expected nil error for nil service, got: %v", err)
	}
}

func TestMakeChargebackConsumer_InvalidPayload(t *testing.T) {
	s := newTestServer(t)
	consumer := s.makeChargebackConsumer()

	ctx := context.Background()
	badEvent := eventbus.Event{
		ID:      "evt_bad",
		Topic:   eventbus.TopicSettlement,
		Payload: []byte(`not-json`),
	}

	err := consumer(ctx, []eventbus.Event{badEvent})
	if err != nil {
		t.Fatalf("Expected nil error for invalid payload, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Event bus consumer tests: makeWebhookConsumer
// ---------------------------------------------------------------------------

func TestMakeWebhookConsumer(t *testing.T) {
	s := newTestServer(t)
	consumer := s.makeWebhookConsumer()

	ctx := context.Background()

	event, err := eventbus.NewEvent(eventbus.TopicSettlement, "0xbuyer", eventbus.SettlementPayload{
		SessionID:  "sess_1",
		BuyerAddr:  "0xbuyer",
		SellerAddr: "0xseller",
		Amount:     "1.500000",
	})
	if err != nil {
		t.Fatalf("Failed to create event: %v", err)
	}

	err = consumer(ctx, []eventbus.Event{event})
	if err != nil {
		t.Fatalf("Webhook consumer failed: %v", err)
	}
}

func TestMakeWebhookConsumer_NilDispatcher(t *testing.T) {
	s := newTestServer(t)
	s.webhooks = nil
	consumer := s.makeWebhookConsumer()

	ctx := context.Background()
	event, _ := eventbus.NewEvent(eventbus.TopicSettlement, "0xbuyer", eventbus.SettlementPayload{
		BuyerAddr: "0xbuyer",
	})

	err := consumer(ctx, []eventbus.Event{event})
	if err != nil {
		t.Fatalf("Expected nil error for nil dispatcher, got: %v", err)
	}
}

func TestMakeWebhookConsumer_InvalidPayload(t *testing.T) {
	s := newTestServer(t)
	consumer := s.makeWebhookConsumer()

	ctx := context.Background()
	badEvent := eventbus.Event{
		ID:      "evt_bad",
		Topic:   eventbus.TopicSettlement,
		Payload: []byte(`{broken`),
	}

	err := consumer(ctx, []eventbus.Event{badEvent})
	if err != nil {
		t.Fatalf("Expected nil error for invalid payload, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Chargeback with cost center
// ---------------------------------------------------------------------------

func TestChargebackGatewayAdapter_WithCostCenter(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()

	// Create a cost center so the adapter has something to attribute to
	_, err := s.chargebackService.CreateCostCenter(ctx, "tenant_cc", "Default", "Engineering", "PROJ-1", "1000.000000", 80)
	if err != nil {
		t.Fatalf("Failed to create cost center: %v", err)
	}

	adapter := &chargebackGatewayAdapter{svc: s.chargebackService}
	err = adapter.RecordGatewaySpend(ctx, "tenant_cc", "0xbuyer", "1.500000", "compute", "sess_1")
	if err != nil {
		t.Fatalf("RecordGatewaySpend failed: %v", err)
	}
}

// ---------------------------------------------------------------------------
// BudgetPreFlight with cost center that has budget
// ---------------------------------------------------------------------------

func TestBudgetPreFlightAdapter_WithBudget(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()

	_, err := s.chargebackService.CreateCostCenter(ctx, "tenant_budget", "BudgetCenter", "Finance", "PROJ-B", "100.000000", 80)
	if err != nil {
		t.Fatalf("Failed to create cost center: %v", err)
	}

	adapter := &budgetPreFlightAdapter{svc: s.chargebackService}
	err = adapter.CheckBudget(ctx, "tenant_budget", "0xbuyer", "1.00")
	if err != nil {
		t.Errorf("Expected no error with budget remaining, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Route registration: additional subsystem routes
// ---------------------------------------------------------------------------

func TestSubsystemRoutesRegistered(t *testing.T) {
	s := newTestServer(t)

	routes := s.router.Routes()
	routeSet := make(map[string]bool)
	for _, route := range routes {
		routeSet[route.Method+":"+route.Path] = true
	}

	expected := []string{
		// Platform/info
		"GET:/api",
		"GET:/v1/platform",
		// Network stats
		"GET:/v1/network/stats",
		"GET:/v1/network/stats/enhanced",
		// Timeline
		"GET:/v1/timeline",
		// Services
		"GET:/v1/services",
		// Feed
		"GET:/v1/feed",
		// Reputation
		"GET:/v1/reputation/:address",
		// Flywheel
		"GET:/v1/flywheel/health",
		// Auth
		"GET:/v1/auth/info",
		// WebSocket
		"GET:/ws",
		// Metrics
		"GET:/metrics",
	}

	for _, e := range expected {
		if !routeSet[e] {
			t.Errorf("Route %s not registered", e)
		}
	}
}

// ---------------------------------------------------------------------------
// Request ID middleware
// ---------------------------------------------------------------------------

func TestRequestIDMiddleware(t *testing.T) {
	s := newTestServer(t)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/health", nil)
	s.router.ServeHTTP(w, req)

	rid := w.Header().Get("X-Request-ID")
	if rid == "" {
		t.Error("Expected X-Request-ID header in response")
	}
}

func TestRequestIDMiddleware_PreservesExisting(t *testing.T) {
	s := newTestServer(t)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/health", nil)
	req.Header.Set("X-Request-ID", "custom-req-id-123")
	s.router.ServeHTTP(w, req)

	rid := w.Header().Get("X-Request-ID")
	if rid != "custom-req-id-123" {
		t.Errorf("Expected preserved request ID 'custom-req-id-123', got %q", rid)
	}
}

// ---------------------------------------------------------------------------
// Security headers
// ---------------------------------------------------------------------------

func TestSecurityHeaders(t *testing.T) {
	s := newTestServer(t)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/health", nil)
	s.router.ServeHTTP(w, req)

	// The security middleware should set at least some of these headers
	headers := []string{
		"X-Content-Type-Options",
		"X-Frame-Options",
	}

	for _, h := range headers {
		if w.Header().Get(h) == "" {
			t.Errorf("Expected security header %s to be set", h)
		}
	}
}

// ---------------------------------------------------------------------------
// Billing helpers
// ---------------------------------------------------------------------------

func TestBillingProviderName(t *testing.T) {
	cfg := testConfig()
	if billingProviderName(cfg) != "noop" {
		t.Errorf("Expected 'noop', got %q", billingProviderName(cfg))
	}

	cfg.StripeSecretKey = "sk_test_123"
	if billingProviderName(cfg) != "stripe" {
		t.Errorf("Expected 'stripe', got %q", billingProviderName(cfg))
	}
}

func TestInitBillingProvider_Noop(t *testing.T) {
	cfg := testConfig()
	provider := initBillingProvider(cfg, nil)
	if provider == nil {
		t.Fatal("Expected non-nil billing provider")
	}
}

// ---------------------------------------------------------------------------
// Gateway billing adapter
// ---------------------------------------------------------------------------

func TestGatewayBillingAdapter(t *testing.T) {
	s := newTestServer(t)
	adapter := &gatewayBillingAdapter{store: s.gatewayStore}
	ctx := context.Background()

	// Non-existent tenant — should return error or empty summary
	summary, err := adapter.GetBillingSummary(ctx, "nonexistent_tenant")
	if err != nil {
		// Some stores may return error for non-existent tenants
		t.Logf("GetBillingSummary returned error (expected): %v", err)
	} else if summary == nil {
		t.Error("Expected non-nil summary when no error")
	}
}

// ---------------------------------------------------------------------------
// WatcherCreditorAdapter / WatcherAgentResolverAdapter (in-memory)
// ---------------------------------------------------------------------------

func TestWatcherCreditorAdapter(t *testing.T) {
	s := newTestServer(t)
	adapter := &watcherCreditorAdapter{store: s.ledger.StoreRef()}
	ctx := context.Background()

	err := adapter.Credit(ctx, "0xaaaa000000000000000000000000000000000099", "10.000000", "deposit_tx_1", "watcher deposit")
	if err != nil {
		t.Fatalf("Credit failed: %v", err)
	}

	has, err := adapter.HasDeposit(ctx, "deposit_tx_1")
	if err != nil {
		t.Fatalf("HasDeposit failed: %v", err)
	}
	if !has {
		t.Error("Expected HasDeposit to return true after credit")
	}

	has, err = adapter.HasDeposit(ctx, "nonexistent_tx")
	if err != nil {
		t.Fatalf("HasDeposit failed: %v", err)
	}
	if has {
		t.Error("Expected HasDeposit to return false for nonexistent tx")
	}
}

func TestWatcherAgentResolverAdapter(t *testing.T) {
	s := newTestServer(t)
	adapter := &watcherAgentResolverAdapter{reg: s.registry}
	ctx := context.Background()

	// Not registered
	registered, err := adapter.IsRegisteredAgent(ctx, "0xaaaa000000000000000000000000000000000111")
	if err != nil {
		t.Fatalf("IsRegisteredAgent failed: %v", err)
	}
	if registered {
		t.Error("Expected false for unregistered agent")
	}

	// Register and check again
	addr := "0xaaaa000000000000000000000000000000000111"
	registerAgent(t, s, addr, "WatcherBot")

	registered, err = adapter.IsRegisteredAgent(ctx, addr)
	if err != nil {
		t.Fatalf("IsRegisteredAgent failed: %v", err)
	}
	if !registered {
		t.Error("Expected true for registered agent")
	}
}

// ---------------------------------------------------------------------------
// AdminReconcileStateProvider
// ---------------------------------------------------------------------------

func TestAdminReconcileStateProvider_NoRun(t *testing.T) {
	// Since reconcileRunner is nil in in-memory mode, test the provider with a nil report scenario
	// We test the provider directly by checking the nil report case
	// The reconcileRunner is only created in Postgres mode, so we test the state provider pattern
	_ = newTestServer(t)

	// Test that adminReconcileStateProvider handles nil report correctly
	// by verifying the code path exists (can't easily construct a runner without DB)
}

// ---------------------------------------------------------------------------
// Server creation and option tests
// ---------------------------------------------------------------------------

func TestServerWithLogger(t *testing.T) {
	// Verify the WithLogger option works with a valid logger
	cfg := testConfig()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s, err := New(cfg, WithLogger(logger))
	if err != nil {
		t.Fatalf("New with custom logger failed: %v", err)
	}
	if s == nil {
		t.Fatal("Expected non-nil server")
	}
	if s.logger != logger {
		t.Error("Expected server to use custom logger")
	}
}

func TestRouter(t *testing.T) {
	s := newTestServer(t)
	if s.Router() == nil {
		t.Fatal("Expected non-nil router")
	}
}

// ---------------------------------------------------------------------------
// Gzip middleware
// ---------------------------------------------------------------------------

func TestGzipMiddleware_NoAcceptEncoding(t *testing.T) {
	s := newTestServer(t)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/health", nil)
	// No Accept-Encoding header
	s.router.ServeHTTP(w, req)

	if w.Header().Get("Content-Encoding") == "gzip" {
		t.Error("Expected no gzip encoding without Accept-Encoding header")
	}
}

func TestGzipMiddleware_WithAcceptEncoding(t *testing.T) {
	s := newTestServer(t)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/health", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	s.router.ServeHTTP(w, req)

	if w.Header().Get("Content-Encoding") != "gzip" {
		t.Error("Expected gzip Content-Encoding with Accept-Encoding header")
	}
}

// ---------------------------------------------------------------------------
// Timeout middleware (websocket exemption)
// ---------------------------------------------------------------------------

func TestTimeoutMiddleware_SkipsWebSocket(t *testing.T) {
	s := newTestServer(t)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/ws", nil)
	req.Header.Set("Upgrade", "websocket")
	// Will fail auth but should not be timeout-killed
	s.router.ServeHTTP(w, req)

	// The request should reach the handler (and get 401 for missing auth)
	if w.Code != http.StatusUnauthorized {
		t.Logf("WebSocket upgrade request got status %d (expected 401)", w.Code)
	}
}

// ---------------------------------------------------------------------------
// Multiple routes test (additional subsystem routes)
// ---------------------------------------------------------------------------

func TestProtectedRoutesRequireAuth(t *testing.T) {
	s := newTestServer(t)

	protectedRoutes := []struct {
		method string
		path   string
	}{
		{"GET", "/v1/agents/0xaaaa000000000000000000000000000000000001/balance"},
		{"GET", "/v1/agents/0xaaaa000000000000000000000000000000000001/ledger"},
		{"POST", "/v1/transactions"},
		{"POST", "/v1/agents/0xaaaa000000000000000000000000000000000001/services"},
	}

	for _, r := range protectedRoutes {
		t.Run(r.method+" "+r.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(r.method, r.path, nil)
			s.router.ServeHTTP(w, req)

			// Accept 401 (unauthorized), 403 (forbidden), or 429 (rate limited before auth)
			if w.Code != http.StatusUnauthorized && w.Code != http.StatusForbidden && w.Code != http.StatusTooManyRequests {
				t.Errorf("%s %s: expected 401, 403, or 429, got %d", r.method, r.path, w.Code)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Forensics consumer with cost center integration test
// ---------------------------------------------------------------------------

func TestForensicsConsumerEndToEnd(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()

	// Ingest some spend events
	_, err := s.forensicsService.Ingest(ctx, forensics.SpendEvent{
		AgentAddr:    "0xbuyer",
		Counterparty: "0xseller",
		Amount:       2.5,
		ServiceType:  "compute",
		Timestamp:    time.Now(),
	})
	if err != nil {
		t.Fatalf("Forensics ingest failed: %v", err)
	}

	// Now run the consumer
	consumer := s.makeForensicsConsumer()
	event, _ := eventbus.NewEvent(eventbus.TopicSettlement, "0xbuyer", eventbus.SettlementPayload{
		BuyerAddr:   "0xbuyer",
		SellerAddr:  "0xseller",
		Amount:      "3.000000",
		ServiceType: "compute",
		AmountFloat: 3.0,
	})

	err = consumer(ctx, []eventbus.Event{event})
	if err != nil {
		t.Fatalf("Forensics consumer failed: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Webhook consumer with emitter
// ---------------------------------------------------------------------------

func TestWebhookConsumerWithEmitter(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()

	// Set up a webhook for the buyer agent
	store := webhooks.NewMemoryStore()
	s.webhooks = webhooks.NewDispatcher(store)

	consumer := s.makeWebhookConsumer()
	event, _ := eventbus.NewEvent(eventbus.TopicSettlement, "0xbuyer", eventbus.SettlementPayload{
		SessionID:  "sess_1",
		BuyerAddr:  "0xbuyer",
		SellerAddr: "0xseller",
		Amount:     "1.500000",
	})

	err := consumer(ctx, []eventbus.Event{event})
	if err != nil {
		t.Fatalf("Webhook consumer failed: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Chargeback consumer with cost center end-to-end
// ---------------------------------------------------------------------------

func TestChargebackConsumerWithCostCenter(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()

	// Create a cost center
	_, err := s.chargebackService.CreateCostCenter(ctx, "tenant_cb", "Test", "QA", "PROJ-T", "500.000000", 80)
	if err != nil {
		t.Fatalf("Failed to create cost center: %v", err)
	}

	consumer := s.makeChargebackConsumer()
	event, _ := eventbus.NewEvent(eventbus.TopicSettlement, "0xbuyer", eventbus.SettlementPayload{
		SessionID:   "sess_cb",
		TenantID:    "tenant_cb",
		BuyerAddr:   "0xbuyer",
		SellerAddr:  "0xseller",
		Amount:      "2.000000",
		ServiceType: "compute",
	})

	err = consumer(ctx, []eventbus.Event{event})
	if err != nil {
		t.Fatalf("Chargeback consumer failed: %v", err)
	}
}
