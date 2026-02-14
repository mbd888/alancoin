package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// --- Mock Ledger ---

type mockLedger struct {
	holds       map[string]string // ref → amount
	settlements map[string]string // ref → amount (SettleHold calls)
	holdErr     error
	settleErr   error
	releaseErr  error
}

func newMockLedger() *mockLedger {
	return &mockLedger{
		holds:       make(map[string]string),
		settlements: make(map[string]string),
	}
}

func (m *mockLedger) Hold(_ context.Context, agentAddr, amount, reference string) error {
	if m.holdErr != nil {
		return m.holdErr
	}
	m.holds[reference] = amount
	return nil
}

func (m *mockLedger) SettleHold(_ context.Context, buyerAddr, sellerAddr, amount, reference string) error {
	if m.settleErr != nil {
		return m.settleErr
	}
	m.settlements[reference] = amount
	return nil
}

func (m *mockLedger) SettleHoldWithFee(_ context.Context, buyerAddr, sellerAddr, sellerAmount, platformAddr, feeAmount, reference string) error {
	if m.settleErr != nil {
		return m.settleErr
	}
	m.settlements[reference] = sellerAmount
	return nil
}

func (m *mockLedger) ReleaseHold(_ context.Context, agentAddr, amount, reference string) error {
	if m.releaseErr != nil {
		return m.releaseErr
	}
	delete(m.holds, reference)
	return nil
}

// --- Mock Registry ---

type mockRegistry struct {
	services []ServiceCandidate
	err      error
}

func (m *mockRegistry) ListServices(_ context.Context, serviceType, maxPrice string) ([]ServiceCandidate, error) {
	if m.err != nil {
		return nil, m.err
	}
	var filtered []ServiceCandidate
	for _, s := range m.services {
		if s.Endpoint != "" {
			filtered = append(filtered, s)
		}
	}
	return filtered, nil
}

// --- Fake Service Endpoint ---

func fakeServiceEndpoint(statusCode int, response map[string]interface{}) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		json.NewEncoder(w).Encode(response)
	}))
}

// --- Helper ---

func newTestService(ledger *mockLedger, registry *mockRegistry) *Service {
	store := NewMemoryStore()
	resolver := NewResolver(registry)
	forwarder := NewForwarder(5 * time.Second)
	return NewService(store, resolver, forwarder, ledger, nil)
}

func newTestServiceWithLogger(ledger *mockLedger, registry *mockRegistry) *Service {
	store := NewMemoryStore()
	resolver := NewResolver(registry)
	forwarder := NewForwarder(5 * time.Second)
	// Use a no-op logger for tests that need one (proxy logs warnings)
	return NewService(store, resolver, forwarder, ledger, testLogger())
}

func testLogger() *slog.Logger {
	return slog.Default()
}

// --- Tests ---

func TestCreateSession_Success(t *testing.T) {
	ml := newMockLedger()
	svc := newTestService(ml, &mockRegistry{})

	session, err := svc.CreateSession(context.Background(), "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "1.00",
		Strategy:      "cheapest",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if session.Status != StatusActive {
		t.Errorf("expected active, got %s", session.Status)
	}
	if session.TotalSpent != "0.000000" {
		t.Errorf("expected 0 spent, got %s", session.TotalSpent)
	}
	if len(ml.holds) != 1 {
		t.Errorf("expected 1 hold, got %d", len(ml.holds))
	}
}

func TestCreateSession_InvalidAmount(t *testing.T) {
	ml := newMockLedger()
	svc := newTestService(ml, &mockRegistry{})

	_, err := svc.CreateSession(context.Background(), "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "invalid",
		MaxPerRequest: "1.00",
	})
	if err == nil {
		t.Fatal("expected error for invalid amount")
	}
}

func TestCreateSession_HoldFails(t *testing.T) {
	ml := newMockLedger()
	ml.holdErr = fmt.Errorf("insufficient balance")
	svc := newTestService(ml, &mockRegistry{})

	_, err := svc.CreateSession(context.Background(), "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "1.00",
	})
	if err == nil {
		t.Fatal("expected error when hold fails")
	}
}

func TestProxy_Success(t *testing.T) {
	server := fakeServiceEndpoint(200, map[string]interface{}{"result": "translated"})
	defer server.Close()

	ml := newMockLedger()
	reg := &mockRegistry{
		services: []ServiceCandidate{
			{
				AgentAddress:    "0xseller",
				AgentName:       "TranslatorBot",
				ServiceID:       "svc1",
				ServiceName:     "translate",
				Price:           "0.50",
				Endpoint:        server.URL,
				ReputationScore: 80,
			},
		},
	}

	svc := newTestServiceWithLogger(ml, reg)

	session, err := svc.CreateSession(context.Background(), "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "1.00",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	result, err := svc.Proxy(context.Background(), session.ID, ProxyRequest{
		ServiceType: "translation",
		Params:      map[string]interface{}{"text": "hello"},
	})
	if err != nil {
		t.Fatalf("proxy: %v", err)
	}

	if result.ServiceUsed != "0xseller" {
		t.Errorf("expected seller 0xseller, got %s", result.ServiceUsed)
	}
	if result.AmountPaid != "0.500000" {
		t.Errorf("expected 0.500000 paid, got %s", result.AmountPaid)
	}

	// Verify session updated
	updated, _ := svc.GetSession(context.Background(), session.ID)
	if updated.TotalSpent != "0.500000" {
		t.Errorf("expected 0.500000 spent, got %s", updated.TotalSpent)
	}
	if updated.RequestCount != 1 {
		t.Errorf("expected 1 request, got %d", updated.RequestCount)
	}
}

func TestProxy_NoService(t *testing.T) {
	ml := newMockLedger()
	reg := &mockRegistry{services: nil}

	svc := newTestServiceWithLogger(ml, reg)

	session, _ := svc.CreateSession(context.Background(), "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "1.00",
	})

	_, err := svc.Proxy(context.Background(), session.ID, ProxyRequest{
		ServiceType: "translation",
	})
	if err == nil {
		t.Fatal("expected error for no services")
	}
}

func TestProxy_SessionClosed(t *testing.T) {
	ml := newMockLedger()
	reg := &mockRegistry{}
	svc := newTestServiceWithLogger(ml, reg)

	session, _ := svc.CreateSession(context.Background(), "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "1.00",
	})

	// Close the session
	svc.CloseSession(context.Background(), session.ID, "0xbuyer")

	_, err := svc.Proxy(context.Background(), session.ID, ProxyRequest{
		ServiceType: "translation",
	})
	if err == nil {
		t.Fatal("expected error for closed session")
	}
}

func TestProxy_RetryOnForwardFailure(t *testing.T) {
	// First server returns 500, second succeeds
	failServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer failServer.Close()

	goodServer := fakeServiceEndpoint(200, map[string]interface{}{"ok": true})
	defer goodServer.Close()

	ml := newMockLedger()
	reg := &mockRegistry{
		services: []ServiceCandidate{
			{AgentAddress: "0xfail", ServiceID: "svc1", ServiceName: "bad", Price: "0.10", Endpoint: failServer.URL},
			{AgentAddress: "0xgood", ServiceID: "svc2", ServiceName: "good", Price: "0.10", Endpoint: goodServer.URL},
		},
	}

	svc := newTestServiceWithLogger(ml, reg)

	session, _ := svc.CreateSession(context.Background(), "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "1.00",
	})

	// Forwarder returns error for 5xx, so the gateway retries with the next candidate
	result, err := svc.Proxy(context.Background(), session.ID, ProxyRequest{
		ServiceType: "translation",
	})
	if err != nil {
		t.Fatalf("proxy: %v", err)
	}
	// Second candidate should be used after first returned 500
	if result.ServiceUsed != "0xgood" {
		t.Errorf("expected 0xgood (retry candidate), got %s", result.ServiceUsed)
	}
	if result.Retries != 1 {
		t.Errorf("expected 1 retry, got %d", result.Retries)
	}

	// Only the successful service should be settled — failed forward = no payment
	if len(ml.settlements) != 1 {
		t.Errorf("expected 1 settlement (successful only), got %d", len(ml.settlements))
	}

	// Session spend reflects only the successful payment
	updated, _ := svc.GetSession(context.Background(), session.ID)
	if updated.TotalSpent != "0.100000" {
		t.Errorf("expected 0.100000 total spent (one payment), got %s", updated.TotalSpent)
	}
	if updated.RequestCount != 1 {
		t.Errorf("expected 1 request count (success only), got %d", updated.RequestCount)
	}
}

func TestProxy_AllowedTypes(t *testing.T) {
	ml := newMockLedger()
	reg := &mockRegistry{}
	svc := newTestServiceWithLogger(ml, reg)

	session, _ := svc.CreateSession(context.Background(), "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "1.00",
		AllowedTypes:  []string{"translation"},
	})

	_, err := svc.Proxy(context.Background(), session.ID, ProxyRequest{
		ServiceType: "inference",
	})
	if err == nil {
		t.Fatal("expected error for disallowed service type")
	}
}

func TestCloseSession_Success(t *testing.T) {
	ml := newMockLedger()
	svc := newTestService(ml, &mockRegistry{})

	session, _ := svc.CreateSession(context.Background(), "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "1.00",
	})

	closed, err := svc.CloseSession(context.Background(), session.ID, "0xbuyer")
	if err != nil {
		t.Fatalf("close: %v", err)
	}
	if closed.Status != StatusClosed {
		t.Errorf("expected closed, got %s", closed.Status)
	}
}

func TestCloseSession_WrongOwner(t *testing.T) {
	ml := newMockLedger()
	svc := newTestService(ml, &mockRegistry{})

	session, _ := svc.CreateSession(context.Background(), "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "1.00",
	})

	_, err := svc.CloseSession(context.Background(), session.ID, "0xstranger")
	if err == nil {
		t.Fatal("expected error for wrong owner")
	}
}

func TestCloseSession_Idempotent(t *testing.T) {
	ml := newMockLedger()
	svc := newTestService(ml, &mockRegistry{})

	session, _ := svc.CreateSession(context.Background(), "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "1.00",
	})

	// First close
	closed1, err := svc.CloseSession(context.Background(), session.ID, "0xbuyer")
	if err != nil {
		t.Fatalf("first close: %v", err)
	}
	if closed1.Status != StatusClosed {
		t.Errorf("expected closed, got %s", closed1.Status)
	}

	// Second close — should be idempotent (return current state, no error)
	closed2, err := svc.CloseSession(context.Background(), session.ID, "0xbuyer")
	if err != nil {
		t.Fatalf("second close should not error (idempotent), got: %v", err)
	}
	if closed2.Status != StatusClosed {
		t.Errorf("expected closed, got %s", closed2.Status)
	}
}

func TestResolver_Cheapest(t *testing.T) {
	reg := &mockRegistry{
		services: []ServiceCandidate{
			{AgentAddress: "0xexpensive", Price: "1.00", Endpoint: "http://a"},
			{AgentAddress: "0xcheap", Price: "0.10", Endpoint: "http://b"},
			{AgentAddress: "0xmid", Price: "0.50", Endpoint: "http://c"},
		},
	}
	resolver := NewResolver(reg)

	candidates, err := resolver.Resolve(context.Background(), ProxyRequest{ServiceType: "test"}, "cheapest", "2.00")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if candidates[0].AgentAddress != "0xcheap" {
		t.Errorf("expected cheapest first, got %s", candidates[0].AgentAddress)
	}
}

func TestResolver_Reputation(t *testing.T) {
	reg := &mockRegistry{
		services: []ServiceCandidate{
			{AgentAddress: "0xlow", Price: "0.10", Endpoint: "http://a", ReputationScore: 20},
			{AgentAddress: "0xhigh", Price: "0.50", Endpoint: "http://b", ReputationScore: 90},
		},
	}
	resolver := NewResolver(reg)

	candidates, err := resolver.Resolve(context.Background(), ProxyRequest{ServiceType: "test"}, "reputation", "2.00")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if candidates[0].AgentAddress != "0xhigh" {
		t.Errorf("expected highest reputation first, got %s", candidates[0].AgentAddress)
	}
}

func TestResolver_PreferAgent(t *testing.T) {
	reg := &mockRegistry{
		services: []ServiceCandidate{
			{AgentAddress: "0xa", Price: "0.10", Endpoint: "http://a"},
			{AgentAddress: "0xpreferred", Price: "0.50", Endpoint: "http://b"},
		},
	}
	resolver := NewResolver(reg)

	candidates, err := resolver.Resolve(context.Background(), ProxyRequest{
		ServiceType: "test",
		PreferAgent: "0xpreferred",
	}, "cheapest", "2.00")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if candidates[0].AgentAddress != "0xpreferred" {
		t.Errorf("expected preferred first, got %s", candidates[0].AgentAddress)
	}
}

func TestResolver_NoEndpoint(t *testing.T) {
	reg := &mockRegistry{
		services: []ServiceCandidate{
			{AgentAddress: "0xa", Price: "0.10", Endpoint: ""}, // no endpoint
		},
	}
	resolver := NewResolver(reg)

	_, err := resolver.Resolve(context.Background(), ProxyRequest{ServiceType: "test"}, "cheapest", "2.00")
	if err == nil {
		t.Fatal("expected error for no services with endpoints")
	}
}

func TestGatewayTokenAuth(t *testing.T) {
	ml := newMockLedger()
	svc := newTestServiceWithLogger(ml, &mockRegistry{})

	// Create a session to use as token
	session, _ := svc.CreateSession(context.Background(), "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "1.00",
	})

	handler := NewHandler(svc)

	// Valid token with matching owner
	t.Run("valid_token", func(t *testing.T) {
		called := false
		testHandler := handler.gatewayTokenAuth()

		gin.SetMode(gin.TestMode)
		r := gin.New()
		r.POST("/test", func(c *gin.Context) {
			// Simulate auth middleware setting agent address
			c.Set("authAgentAddr", "0xbuyer")
			c.Next()
		}, testHandler, func(c *gin.Context) {
			called = true
			sid := c.GetString("gatewaySessionID")
			if sid != session.ID {
				t.Errorf("expected session ID %s, got %s", session.ID, sid)
			}
			c.Status(200)
		})

		req := httptest.NewRequest("POST", "/test", nil)
		req.Header.Set("X-Gateway-Token", session.ID)
		w2 := httptest.NewRecorder()
		r.ServeHTTP(w2, req)

		if !called {
			t.Error("handler should have been called")
		}
	})

	// Wrong owner — should be forbidden
	t.Run("wrong_owner", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		r := gin.New()
		testHandler := handler.gatewayTokenAuth()
		r.POST("/test", func(c *gin.Context) {
			c.Set("authAgentAddr", "0xstranger")
			c.Next()
		}, testHandler, func(c *gin.Context) {
			c.Status(200)
		})

		req := httptest.NewRequest("POST", "/test", nil)
		req.Header.Set("X-Gateway-Token", session.ID)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusForbidden {
			t.Errorf("expected 403, got %d", w.Code)
		}
	})

	// No auth (no authAgentAddr) — should be unauthorized
	t.Run("no_auth", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		r := gin.New()
		testHandler := handler.gatewayTokenAuth()
		r.POST("/test", testHandler, func(c *gin.Context) {
			c.Status(200)
		})

		req := httptest.NewRequest("POST", "/test", nil)
		req.Header.Set("X-Gateway-Token", session.ID)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", w.Code)
		}
	})

	// Missing token
	t.Run("missing_token", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		r := gin.New()
		testHandler := handler.gatewayTokenAuth()
		r.POST("/test", testHandler, func(c *gin.Context) {
			c.Status(200)
		})

		req := httptest.NewRequest("POST", "/test", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", w.Code)
		}
	})

	// Invalid token
	t.Run("invalid_token", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		r := gin.New()
		testHandler := handler.gatewayTokenAuth()
		r.POST("/test", testHandler, func(c *gin.Context) {
			c.Status(200)
		})

		req := httptest.NewRequest("POST", "/test", nil)
		req.Header.Set("X-Gateway-Token", "gw_nonexistent")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", w.Code)
		}
	})
}

func TestMemoryStore_ListSessions(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	now := time.Now()
	for i := 0; i < 5; i++ {
		store.CreateSession(ctx, &Session{
			ID:        fmt.Sprintf("gw_%d", i),
			AgentAddr: "0xbuyer",
			Status:    StatusActive,
			CreatedAt: now.Add(time.Duration(i) * time.Second),
		})
	}

	sessions, err := store.ListSessions(ctx, "0xbuyer", 3)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(sessions) != 3 {
		t.Errorf("expected 3, got %d", len(sessions))
	}
	// Should be newest first
	if sessions[0].ID != "gw_4" {
		t.Errorf("expected gw_4 first, got %s", sessions[0].ID)
	}
}

func TestMemoryStore_RequestLogs(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		store.CreateLog(ctx, &RequestLog{
			ID:        fmt.Sprintf("log_%d", i),
			SessionID: "gw_1",
			Status:    "success",
		})
	}

	logs, err := store.ListLogs(ctx, "gw_1", 3)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(logs) != 3 {
		t.Errorf("expected 3, got %d", len(logs))
	}
}

func TestSessionExpiry(t *testing.T) {
	ml := newMockLedger()
	svc := newTestServiceWithLogger(ml, &mockRegistry{
		services: []ServiceCandidate{
			{AgentAddress: "0xseller", Price: "0.10", Endpoint: "http://localhost"},
		},
	})

	session, _ := svc.CreateSession(context.Background(), "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "1.00",
		ExpiresInSec:  -1, // Already expired (in the past)
	})

	// Manually set expired time
	s, _ := svc.store.GetSession(context.Background(), session.ID)
	s.ExpiresAt = time.Now().Add(-time.Hour)
	svc.store.UpdateSession(context.Background(), s)

	_, err := svc.Proxy(context.Background(), session.ID, ProxyRequest{
		ServiceType: "test",
	})
	if err == nil {
		t.Fatal("expected error for expired session")
	}
}

// ---------------------------------------------------------------------------
// #12: Verify payment headers sent to services
// ---------------------------------------------------------------------------

func TestProxy_PaymentHeadersSentToService(t *testing.T) {
	var capturedHeaders http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
	}))
	defer server.Close()

	ml := newMockLedger()
	reg := &mockRegistry{
		services: []ServiceCandidate{
			{
				AgentAddress: "0xseller",
				ServiceID:    "svc1",
				ServiceName:  "translate",
				Price:        "0.75",
				Endpoint:     server.URL,
			},
		},
	}

	svc := newTestServiceWithLogger(ml, reg)

	session, err := svc.CreateSession(context.Background(), "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "1.00",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	_, err = svc.Proxy(context.Background(), session.ID, ProxyRequest{
		ServiceType: "translation",
		Params:      map[string]interface{}{"text": "hello"},
	})
	if err != nil {
		t.Fatalf("proxy: %v", err)
	}

	// Verify payment headers
	if capturedHeaders.Get("X-Payment-Amount") != "0.75" {
		t.Errorf("expected X-Payment-Amount '0.75', got %q", capturedHeaders.Get("X-Payment-Amount"))
	}
	if capturedHeaders.Get("X-Payment-From") != "0xbuyer" {
		t.Errorf("expected X-Payment-From '0xbuyer', got %q", capturedHeaders.Get("X-Payment-From"))
	}
	ref := capturedHeaders.Get("X-Payment-Ref")
	if ref == "" {
		t.Error("expected X-Payment-Ref to be set")
	}
	// Reference should contain the session ID
	if !strings.Contains(ref, session.ID) {
		t.Errorf("expected X-Payment-Ref to contain session ID %s, got %q", session.ID, ref)
	}
	if capturedHeaders.Get("Content-Type") != "application/json" {
		t.Errorf("expected Content-Type 'application/json', got %q", capturedHeaders.Get("Content-Type"))
	}
}

// --- Mock Recorder ---

type mockGatewayRecorder struct {
	transactions []recordedGwTx
}

type recordedGwTx struct {
	txHash, from, to, amount, serviceID, status string
}

func (r *mockGatewayRecorder) RecordTransaction(_ context.Context, txHash, from, to, amount, serviceID, status string) error {
	r.transactions = append(r.transactions, recordedGwTx{txHash, from, to, amount, serviceID, status})
	return nil
}

// --- Timer / AutoClose / Recorder Tests ---

func TestAutoCloseExpired(t *testing.T) {
	ml := newMockLedger()
	svc := newTestServiceWithLogger(ml, &mockRegistry{})

	ctx := context.Background()
	session, err := svc.CreateSession(ctx, "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "1.00",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Manually expire it
	s, _ := svc.store.GetSession(ctx, session.ID)
	s.ExpiresAt = time.Now().Add(-time.Hour)
	svc.store.UpdateSession(ctx, s)

	// Auto-close
	err = svc.AutoCloseExpired(ctx, s)
	if err != nil {
		t.Fatalf("auto close: %v", err)
	}

	updated, _ := svc.GetSession(ctx, session.ID)
	if updated.Status != StatusExpired {
		t.Errorf("expected expired, got %s", updated.Status)
	}
}

func TestAutoCloseExpired_AlreadyClosed(t *testing.T) {
	ml := newMockLedger()
	svc := newTestServiceWithLogger(ml, &mockRegistry{})

	ctx := context.Background()
	session, _ := svc.CreateSession(ctx, "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "1.00",
	})

	// Close normally first
	svc.CloseSession(ctx, session.ID, "0xbuyer")

	s, _ := svc.store.GetSession(ctx, session.ID)
	err := svc.AutoCloseExpired(ctx, s)
	if err == nil {
		t.Fatal("expected error for already closed session")
	}
}

func TestAutoCloseExpired_ReleasesUnspent(t *testing.T) {
	ml := newMockLedger()
	server := fakeServiceEndpoint(200, map[string]interface{}{"ok": true})
	defer server.Close()

	reg := &mockRegistry{
		services: []ServiceCandidate{
			{AgentAddress: "0xseller", ServiceID: "svc1", Price: "2.00", Endpoint: server.URL},
		},
	}
	svc := newTestServiceWithLogger(ml, reg)

	ctx := context.Background()
	session, _ := svc.CreateSession(ctx, "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "5.00",
	})

	// Spend some
	_, err := svc.Proxy(ctx, session.ID, ProxyRequest{ServiceType: "test"})
	if err != nil {
		t.Fatalf("proxy: %v", err)
	}

	// Expire and auto-close
	s, _ := svc.store.GetSession(ctx, session.ID)
	s.ExpiresAt = time.Now().Add(-time.Hour)
	svc.store.UpdateSession(ctx, s)

	err = svc.AutoCloseExpired(ctx, s)
	if err != nil {
		t.Fatalf("auto close: %v", err)
	}

	updated, _ := svc.GetSession(ctx, session.ID)
	if updated.Status != StatusExpired {
		t.Errorf("expected expired, got %s", updated.Status)
	}
	// Hold for remaining 8.00 should have been released
	if len(ml.holds) != 0 {
		t.Errorf("expected all holds released, got %d", len(ml.holds))
	}
}

func TestListExpired_MemoryStore(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	now := time.Now()
	// Active and expired
	store.CreateSession(ctx, &Session{
		ID: "gw_expired", AgentAddr: "0xa", Status: StatusActive,
		ExpiresAt: now.Add(-time.Hour), CreatedAt: now,
	})
	// Active and not expired
	store.CreateSession(ctx, &Session{
		ID: "gw_active", AgentAddr: "0xa", Status: StatusActive,
		ExpiresAt: now.Add(time.Hour), CreatedAt: now,
	})
	// Closed (should not appear)
	store.CreateSession(ctx, &Session{
		ID: "gw_closed", AgentAddr: "0xa", Status: StatusClosed,
		ExpiresAt: now.Add(-time.Hour), CreatedAt: now,
	})

	expired, err := store.ListExpired(ctx, now, 10)
	if err != nil {
		t.Fatalf("list expired: %v", err)
	}
	if len(expired) != 1 {
		t.Fatalf("expected 1 expired, got %d", len(expired))
	}
	if expired[0].ID != "gw_expired" {
		t.Errorf("expected gw_expired, got %s", expired[0].ID)
	}
}

func TestProxy_RecorderCalled(t *testing.T) {
	server := fakeServiceEndpoint(200, map[string]interface{}{"ok": true})
	defer server.Close()

	ml := newMockLedger()
	rec := &mockGatewayRecorder{}
	reg := &mockRegistry{
		services: []ServiceCandidate{
			{AgentAddress: "0xseller", ServiceID: "svc1", Price: "0.50", Endpoint: server.URL},
		},
	}

	svc := newTestServiceWithLogger(ml, reg)
	svc.WithRecorder(rec)

	ctx := context.Background()
	session, _ := svc.CreateSession(ctx, "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "1.00",
	})

	_, err := svc.Proxy(ctx, session.ID, ProxyRequest{ServiceType: "translation"})
	if err != nil {
		t.Fatalf("proxy: %v", err)
	}

	if len(rec.transactions) != 1 {
		t.Fatalf("expected 1 recorded transaction, got %d", len(rec.transactions))
	}
	tx := rec.transactions[0]
	if tx.from != "0xbuyer" {
		t.Errorf("expected from 0xbuyer, got %s", tx.from)
	}
	if tx.to != "0xseller" {
		t.Errorf("expected to 0xseller, got %s", tx.to)
	}
	if tx.status != "confirmed" {
		t.Errorf("expected status confirmed, got %s", tx.status)
	}
}

func TestProxy_RecorderCalledOnForwardFailure(t *testing.T) {
	failServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer failServer.Close()

	ml := newMockLedger()
	rec := &mockGatewayRecorder{}
	reg := &mockRegistry{
		services: []ServiceCandidate{
			{AgentAddress: "0xfail", ServiceID: "svc1", Price: "0.10", Endpoint: failServer.URL},
		},
	}

	svc := newTestServiceWithLogger(ml, reg)
	svc.WithRecorder(rec)

	ctx := context.Background()
	session, _ := svc.CreateSession(ctx, "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "1.00",
	})

	_, _ = svc.Proxy(ctx, session.ID, ProxyRequest{ServiceType: "test"})

	if len(rec.transactions) != 1 {
		t.Fatalf("expected 1 recorded transaction (failed), got %d", len(rec.transactions))
	}
	if rec.transactions[0].status != "failed" {
		t.Errorf("expected status failed, got %s", rec.transactions[0].status)
	}
	if rec.transactions[0].amount != "0" {
		t.Errorf("expected amount 0 (no payment on forward failure), got %s", rec.transactions[0].amount)
	}
}

func TestTimerSweepsExpiredSessions(t *testing.T) {
	ml := newMockLedger()
	store := NewMemoryStore()
	resolver := NewResolver(&mockRegistry{})
	forwarder := NewForwarder(5 * time.Second)
	svc := NewService(store, resolver, forwarder, ml, testLogger())

	ctx := context.Background()

	// Create an expired session directly in the store
	now := time.Now()
	store.CreateSession(ctx, &Session{
		ID:         "gw_sweep",
		AgentAddr:  "0xbuyer",
		MaxTotal:   "5.00",
		TotalSpent: "0.000000",
		Status:     StatusActive,
		ExpiresAt:  now.Add(-time.Minute),
		CreatedAt:  now,
		UpdatedAt:  now,
	})
	// Hold the funds (simulate what CreateSession does)
	ml.holds["gw_sweep"] = "5.00"

	timer := NewTimer(svc, store, testLogger())

	// Manually call sweep
	timer.sweepExpired(ctx)

	updated, _ := store.GetSession(ctx, "gw_sweep")
	if updated.Status != StatusExpired {
		t.Errorf("expected expired after sweep, got %s", updated.Status)
	}
	if len(ml.holds) != 0 {
		t.Errorf("expected holds released, got %d", len(ml.holds))
	}
}

// ---------------------------------------------------------------------------
// Idempotency tests
// ---------------------------------------------------------------------------

func TestProxy_IdempotencyKey_ReturnsCachedResult(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"call": callCount})
	}))
	defer server.Close()

	ml := newMockLedger()
	reg := &mockRegistry{
		services: []ServiceCandidate{
			{AgentAddress: "0xseller", ServiceID: "svc1", ServiceName: "test", Price: "0.10", Endpoint: server.URL},
		},
	}
	svc := newTestServiceWithLogger(ml, reg)

	ctx := context.Background()
	session, _ := svc.CreateSession(ctx, "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "1.00",
	})

	// First call with idempotency key
	r1, err := svc.Proxy(ctx, session.ID, ProxyRequest{
		ServiceType:    "test",
		IdempotencyKey: "key-1",
	})
	if err != nil {
		t.Fatalf("first proxy: %v", err)
	}

	// Second call with SAME idempotency key — should return cached result
	r2, err := svc.Proxy(ctx, session.ID, ProxyRequest{
		ServiceType:    "test",
		IdempotencyKey: "key-1",
	})
	if err != nil {
		t.Fatalf("second proxy: %v", err)
	}

	// Should have the same result (same AmountPaid, etc.)
	if r1.AmountPaid != r2.AmountPaid {
		t.Errorf("expected same amount, got %s vs %s", r1.AmountPaid, r2.AmountPaid)
	}

	// Service should only have been called ONCE
	if callCount != 1 {
		t.Errorf("expected service called once, got %d", callCount)
	}

	// Only one settlement should exist
	if len(ml.settlements) != 1 {
		t.Errorf("expected 1 settlement (not double-charged), got %d", len(ml.settlements))
	}

	// Session spend should only reflect one charge
	updated, _ := svc.GetSession(ctx, session.ID)
	if updated.TotalSpent != "0.100000" {
		t.Errorf("expected 0.100000 total spent (single charge), got %s", updated.TotalSpent)
	}
}

func TestProxy_DifferentIdempotencyKey_ProcessesBoth(t *testing.T) {
	server := fakeServiceEndpoint(200, map[string]interface{}{"ok": true})
	defer server.Close()

	ml := newMockLedger()
	reg := &mockRegistry{
		services: []ServiceCandidate{
			{AgentAddress: "0xseller", ServiceID: "svc1", ServiceName: "test", Price: "0.10", Endpoint: server.URL},
		},
	}
	svc := newTestServiceWithLogger(ml, reg)

	ctx := context.Background()
	session, _ := svc.CreateSession(ctx, "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "1.00",
	})

	// Two calls with DIFFERENT keys — both should process
	_, err := svc.Proxy(ctx, session.ID, ProxyRequest{
		ServiceType:    "test",
		IdempotencyKey: "key-A",
	})
	if err != nil {
		t.Fatalf("first proxy: %v", err)
	}

	_, err = svc.Proxy(ctx, session.ID, ProxyRequest{
		ServiceType:    "test",
		IdempotencyKey: "key-B",
	})
	if err != nil {
		t.Fatalf("second proxy: %v", err)
	}

	// Both should have settled
	if len(ml.settlements) != 2 {
		t.Errorf("expected 2 settlements, got %d", len(ml.settlements))
	}

	updated, _ := svc.GetSession(ctx, session.ID)
	if updated.TotalSpent != "0.200000" {
		t.Errorf("expected 0.200000, got %s", updated.TotalSpent)
	}
}

func TestProxy_NoIdempotencyKey_ProcessesEveryTime(t *testing.T) {
	server := fakeServiceEndpoint(200, map[string]interface{}{"ok": true})
	defer server.Close()

	ml := newMockLedger()
	reg := &mockRegistry{
		services: []ServiceCandidate{
			{AgentAddress: "0xseller", ServiceID: "svc1", ServiceName: "test", Price: "0.10", Endpoint: server.URL},
		},
	}
	svc := newTestServiceWithLogger(ml, reg)

	ctx := context.Background()
	session, _ := svc.CreateSession(ctx, "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "1.00",
	})

	// Two calls without idempotency key — both should process
	for i := 0; i < 3; i++ {
		_, err := svc.Proxy(ctx, session.ID, ProxyRequest{
			ServiceType: "test",
		})
		if err != nil {
			t.Fatalf("proxy %d: %v", i, err)
		}
	}

	if len(ml.settlements) != 3 {
		t.Errorf("expected 3 settlements, got %d", len(ml.settlements))
	}
}

// ---------------------------------------------------------------------------
// Bug fix verification tests
// ---------------------------------------------------------------------------

// TestProxy_ConcurrentBudgetRace verifies that concurrent proxy requests
// cannot over-allocate the session budget (Bug #1 fix).
// Two goroutines race to spend the last $0.08 of a $1.00 budget.
// Without pendingSpend reservation, both would forward and one seller
// would deliver service without payment.
func TestProxy_ConcurrentBudgetRace(t *testing.T) {
	// Slow server to widen the race window
	slowServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
	}))
	defer slowServer.Close()

	ml := newMockLedger()
	reg := &mockRegistry{
		services: []ServiceCandidate{
			{AgentAddress: "0xseller", ServiceID: "svc1", ServiceName: "test", Price: "0.08", Endpoint: slowServer.URL},
		},
	}
	svc := newTestServiceWithLogger(ml, reg)

	ctx := context.Background()
	session, err := svc.CreateSession(ctx, "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "1.00",
		MaxPerRequest: "0.50",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Spend $0.92 sequentially so only $0.08 remains
	fastServer := fakeServiceEndpoint(200, map[string]interface{}{"ok": true})
	defer fastServer.Close()

	// Temporarily update registry to use a $0.092 service for 10 calls
	reg.services = []ServiceCandidate{
		{AgentAddress: "0xsetup", ServiceID: "svc0", ServiceName: "setup", Price: "0.092", Endpoint: fastServer.URL},
	}
	for i := 0; i < 10; i++ {
		_, err := svc.Proxy(ctx, session.ID, ProxyRequest{ServiceType: "test"})
		if err != nil {
			t.Fatalf("setup proxy %d: %v", i, err)
		}
	}

	updated, _ := svc.GetSession(ctx, session.ID)
	if updated.TotalSpent != "0.920000" {
		t.Fatalf("expected 0.920000 spent after setup, got %s", updated.TotalSpent)
	}

	// Now switch to $0.08 service and race two concurrent requests
	reg.services = []ServiceCandidate{
		{AgentAddress: "0xseller", ServiceID: "svc1", ServiceName: "test", Price: "0.08", Endpoint: slowServer.URL},
	}

	var wg sync.WaitGroup
	results := make(chan error, 2)

	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.Proxy(ctx, session.ID, ProxyRequest{ServiceType: "test"})
			results <- err
		}()
	}

	wg.Wait()
	close(results)

	successCount := 0
	budgetExceededCount := 0
	for err := range results {
		if err == nil {
			successCount++
		} else if strings.Contains(err.Error(), "budget") {
			budgetExceededCount++
		} else {
			t.Errorf("unexpected error: %v", err)
		}
	}

	// Exactly one should succeed and one should fail with budget exceeded.
	// Without the pendingSpend fix, both would forward and one seller would
	// be unpaid (the old bug).
	if successCount != 1 {
		t.Errorf("expected exactly 1 success, got %d", successCount)
	}
	if budgetExceededCount != 1 {
		t.Errorf("expected exactly 1 budget exceeded, got %d", budgetExceededCount)
	}

	// Settlements: 10 setup + 1 race winner = 11
	if len(ml.settlements) != 11 {
		t.Errorf("expected 11 settlements, got %d", len(ml.settlements))
	}
}

// TestProxy_SettlementRetry verifies that transient SettleHold failures
// are retried before giving up (Bug #2 fix).
func TestProxy_SettlementRetry(t *testing.T) {
	server := fakeServiceEndpoint(200, map[string]interface{}{"ok": true})
	defer server.Close()

	ml := newMockLedger()
	reg := &mockRegistry{
		services: []ServiceCandidate{
			{AgentAddress: "0xseller", ServiceID: "svc1", ServiceName: "test", Price: "0.10", Endpoint: server.URL},
		},
	}
	svc := newTestServiceWithLogger(ml, reg)

	ctx := context.Background()
	session, _ := svc.CreateSession(ctx, "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "1.00",
	})

	// Make SettleHold fail twice, then succeed on 3rd attempt
	settleAttempts := 0

	customLedger := &retryMockLedger{
		mockLedger:   ml,
		failCount:    2,
		attemptCount: &settleAttempts,
	}

	// Rebuild service with custom ledger
	store := NewMemoryStore()
	resolver := NewResolver(reg)
	forwarder := NewForwarder(5 * time.Second)
	svc2 := NewService(store, resolver, forwarder, customLedger, testLogger())

	session2, _ := svc2.CreateSession(ctx, "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "1.00",
	})

	result, err := svc2.Proxy(ctx, session2.ID, ProxyRequest{
		ServiceType: "test",
	})
	if err != nil {
		t.Fatalf("proxy should succeed after retries: %v", err)
	}
	if result.AmountPaid != "0.100000" {
		t.Errorf("expected 0.100000 paid, got %s", result.AmountPaid)
	}
	if settleAttempts != 3 {
		t.Errorf("expected 3 settle attempts, got %d", settleAttempts)
	}

	// Session should reflect the successful settlement
	updated, _ := svc2.GetSession(ctx, session2.ID)
	if updated.TotalSpent != "0.100000" {
		t.Errorf("expected 0.100000 spent, got %s", updated.TotalSpent)
	}
	_ = session // suppress unused
}

// retryMockLedger wraps mockLedger to fail SettleHold a configurable number of times.
type retryMockLedger struct {
	*mockLedger
	failCount    int
	attemptCount *int
}

func (r *retryMockLedger) SettleHold(ctx context.Context, buyerAddr, sellerAddr, amount, reference string) error {
	*r.attemptCount++
	if *r.attemptCount <= r.failCount {
		return fmt.Errorf("transient DB error (attempt %d)", *r.attemptCount)
	}
	return r.mockLedger.SettleHold(ctx, buyerAddr, sellerAddr, amount, reference)
}

func (r *retryMockLedger) SettleHoldWithFee(ctx context.Context, buyerAddr, sellerAddr, sellerAmount, platformAddr, feeAmount, reference string) error {
	*r.attemptCount++
	if *r.attemptCount <= r.failCount {
		return fmt.Errorf("transient DB error (attempt %d)", *r.attemptCount)
	}
	return r.mockLedger.SettleHoldWithFee(ctx, buyerAddr, sellerAddr, sellerAmount, platformAddr, feeAmount, reference)
}

// TestProxy_SettlementFailure_ReturnsResponse verifies that when settlement
// fails on ALL retries, the buyer still gets the service response (not a retry
// to the next candidate). The buyer got value — don't throw it away.
func TestProxy_SettlementFailure_ReturnsResponse(t *testing.T) {
	server := fakeServiceEndpoint(200, map[string]interface{}{"result": "translated"})
	defer server.Close()

	ml := newMockLedger()
	settleAttempts := 0
	customLedger := &retryMockLedger{
		mockLedger:   ml,
		failCount:    999, // Always fail settlement
		attemptCount: &settleAttempts,
	}

	reg := &mockRegistry{
		services: []ServiceCandidate{
			{AgentAddress: "0xseller1", ServiceID: "svc1", ServiceName: "good", Price: "0.10", Endpoint: server.URL},
			{AgentAddress: "0xseller2", ServiceID: "svc2", ServiceName: "backup", Price: "0.10", Endpoint: server.URL},
		},
	}

	store := NewMemoryStore()
	resolver := NewResolver(reg)
	forwarder := NewForwarder(5 * time.Second)
	svc := NewService(store, resolver, forwarder, customLedger, testLogger())

	ctx := context.Background()
	session, _ := svc.CreateSession(ctx, "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "1.00",
	})

	// Proxy should return the FIRST candidate's response (not try the second)
	result, err := svc.Proxy(ctx, session.ID, ProxyRequest{
		ServiceType: "test",
	})
	if err != nil {
		t.Fatalf("should return success even with settlement failure: %v", err)
	}

	// Response came from the first candidate
	if result.ServiceUsed != "0xseller1" {
		t.Errorf("expected first candidate 0xseller1 (no retry to second), got %s", result.ServiceUsed)
	}

	// Amount paid is 0 because settlement failed
	if result.AmountPaid != "0.000000" {
		t.Errorf("expected 0.000000 paid (settlement failed), got %s", result.AmountPaid)
	}

	// Session spend should be unchanged (settlement didn't happen)
	updated, _ := svc.GetSession(ctx, session.ID)
	if updated.TotalSpent != "0.000000" {
		t.Errorf("expected 0.000000 spent (settlement failed, buyer not charged), got %s", updated.TotalSpent)
	}

	// Settlement was attempted 3 times (retry loop)
	if settleAttempts != 3 {
		t.Errorf("expected 3 settle attempts, got %d", settleAttempts)
	}
}

// TestProxy_ConcurrentIdempotencyDedup verifies that concurrent requests
// with the same idempotency key result in exactly one forward and one
// settlement (Bug #3 fix).
func TestProxy_ConcurrentIdempotencyDedup(t *testing.T) {
	callCount := int32(0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		time.Sleep(50 * time.Millisecond) // Slow enough for both goroutines to arrive
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"call": atomic.LoadInt32(&callCount)})
	}))
	defer server.Close()

	ml := newMockLedger()
	reg := &mockRegistry{
		services: []ServiceCandidate{
			{AgentAddress: "0xseller", ServiceID: "svc1", ServiceName: "test", Price: "0.10", Endpoint: server.URL},
		},
	}
	svc := newTestServiceWithLogger(ml, reg)

	ctx := context.Background()
	session, _ := svc.CreateSession(ctx, "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "1.00",
	})

	// Race two requests with the same idempotency key
	var wg sync.WaitGroup
	type proxyResult struct {
		result *ProxyResult
		err    error
	}
	results := make(chan proxyResult, 2)

	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := svc.Proxy(ctx, session.ID, ProxyRequest{
				ServiceType:    "test",
				IdempotencyKey: "same-key",
			})
			results <- proxyResult{r, err}
		}()
	}

	wg.Wait()
	close(results)

	var successes int
	for pr := range results {
		if pr.err != nil {
			t.Errorf("unexpected error: %v", pr.err)
		} else {
			successes++
		}
	}

	if successes != 2 {
		t.Errorf("expected 2 successes (one real, one cached), got %d", successes)
	}

	// Service should have been called exactly ONCE
	if atomic.LoadInt32(&callCount) != 1 {
		t.Errorf("expected service called once (deduped), got %d", atomic.LoadInt32(&callCount))
	}

	// Only one settlement
	if len(ml.settlements) != 1 {
		t.Errorf("expected 1 settlement, got %d", len(ml.settlements))
	}

	// Session spend should reflect single charge
	updated, _ := svc.GetSession(ctx, session.ID)
	if updated.TotalSpent != "0.100000" {
		t.Errorf("expected 0.100000, got %s", updated.TotalSpent)
	}
}

// ---------------------------------------------------------------------------
// Single-shot call tests
// ---------------------------------------------------------------------------

func TestSingleCall_Success(t *testing.T) {
	server := fakeServiceEndpoint(200, map[string]interface{}{"result": "done"})
	defer server.Close()

	ml := newMockLedger()
	reg := &mockRegistry{
		services: []ServiceCandidate{
			{AgentAddress: "0xseller", ServiceID: "svc1", ServiceName: "translate", Price: "0.50", Endpoint: server.URL},
		},
	}
	svc := newTestServiceWithLogger(ml, reg)

	result, err := svc.SingleCall(context.Background(), "0xbuyer", "", SingleCallRequest{
		MaxPrice:    "1.00",
		ServiceType: "translation",
		Params:      map[string]interface{}{"text": "hello"},
	})
	if err != nil {
		t.Fatalf("single call: %v", err)
	}
	if result.AmountPaid != "0.500000" {
		t.Errorf("expected 0.500000 paid, got %s", result.AmountPaid)
	}
	if result.ServiceUsed != "0xseller" {
		t.Errorf("expected 0xseller, got %s", result.ServiceUsed)
	}

	// Session should have been closed (ephemeral)
	sessions, _ := svc.ListSessions(context.Background(), "0xbuyer", 10)
	for _, s := range sessions {
		if s.Status == StatusActive {
			t.Error("expected no active sessions after single call")
		}
	}
}

func TestSingleCall_NoService(t *testing.T) {
	ml := newMockLedger()
	reg := &mockRegistry{services: nil}
	svc := newTestServiceWithLogger(ml, reg)

	_, err := svc.SingleCall(context.Background(), "0xbuyer", "", SingleCallRequest{
		MaxPrice:    "1.00",
		ServiceType: "translation",
	})
	if err == nil {
		t.Fatal("expected error for no services")
	}
}

// ---------------------------------------------------------------------------
// Input validation tests
// ---------------------------------------------------------------------------

func TestCreateSession_InvalidServiceType(t *testing.T) {
	ml := newMockLedger()
	svc := newTestService(ml, &mockRegistry{})

	_, err := svc.CreateSession(context.Background(), "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "1.00",
		AllowedTypes:  []string{"valid-type", "invalid type with spaces"},
	})
	if err == nil {
		t.Fatal("expected error for invalid service type")
	}
}

func TestCreateSession_ExpiryTooShort(t *testing.T) {
	ml := newMockLedger()
	svc := newTestService(ml, &mockRegistry{})

	_, err := svc.CreateSession(context.Background(), "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "1.00",
		ExpiresInSec:  10, // Too short (min 60)
	})
	if err == nil {
		t.Fatal("expected error for short expiry")
	}
}

func TestCreateSession_ExpiryTooLong(t *testing.T) {
	ml := newMockLedger()
	svc := newTestService(ml, &mockRegistry{})

	_, err := svc.CreateSession(context.Background(), "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "1.00",
		ExpiresInSec:  100000, // Too long (max 86400)
	})
	if err == nil {
		t.Fatal("expected error for long expiry")
	}
}

func TestProxy_InvalidServiceType(t *testing.T) {
	ml := newMockLedger()
	reg := &mockRegistry{}
	svc := newTestServiceWithLogger(ml, reg)

	session, _ := svc.CreateSession(context.Background(), "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "1.00",
	})

	_, err := svc.Proxy(context.Background(), session.ID, ProxyRequest{
		ServiceType: "invalid type!",
	})
	if err == nil {
		t.Fatal("expected error for invalid service type in proxy")
	}
}

// ---------------------------------------------------------------------------
// Budget warning tests
// ---------------------------------------------------------------------------

func TestProxy_BudgetLow(t *testing.T) {
	server := fakeServiceEndpoint(200, map[string]interface{}{"ok": true})
	defer server.Close()

	ml := newMockLedger()
	reg := &mockRegistry{
		services: []ServiceCandidate{
			{AgentAddress: "0xseller", ServiceID: "svc1", ServiceName: "test", Price: "0.90", Endpoint: server.URL},
		},
	}
	svc := newTestServiceWithLogger(ml, reg)

	session, _ := svc.CreateSession(context.Background(), "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "1.00",
		MaxPerRequest: "1.00",
		WarnAtPercent: 20, // Warn when < 20% remaining
	})

	// Spend $0.90 of $1.00 → remaining $0.10 = 10% → below 20% threshold
	result, err := svc.Proxy(context.Background(), session.ID, ProxyRequest{
		ServiceType: "test",
	})
	if err != nil {
		t.Fatalf("proxy: %v", err)
	}
	if !result.BudgetLow {
		t.Error("expected budgetLow=true when remaining is 10% (below 20% threshold)")
	}
	if result.TotalSpent != "0.900000" {
		t.Errorf("expected totalSpent 0.900000, got %s", result.TotalSpent)
	}
	if result.Remaining != "0.100000" {
		t.Errorf("expected remaining 0.100000, got %s", result.Remaining)
	}
}

// ---------------------------------------------------------------------------
// Idempotency cache sweep test
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Policy evaluator tests
// ---------------------------------------------------------------------------

// mockPolicyEvaluator implements PolicyEvaluator for testing.
type mockPolicyEvaluator struct {
	decision *PolicyDecision
	err      error
	calls    int // number of times EvaluateProxy was called
	// denyAfter: if > 0, allow the first N calls then deny
	denyAfter int
}

func (m *mockPolicyEvaluator) EvaluateProxy(_ context.Context, _ *Session, _ string) (*PolicyDecision, error) {
	m.calls++
	if m.denyAfter > 0 && m.calls <= m.denyAfter {
		return &PolicyDecision{Evaluated: 0, Allowed: true}, nil
	}
	return m.decision, m.err
}

func TestProxy_PolicyDenied(t *testing.T) {
	server := fakeServiceEndpoint(200, map[string]interface{}{"ok": true})
	defer server.Close()

	ml := newMockLedger()
	reg := &mockRegistry{
		services: []ServiceCandidate{
			{AgentAddress: "0xseller", ServiceID: "svc1", ServiceName: "test", Price: "0.10", Endpoint: server.URL},
		},
	}
	svc := newTestServiceWithLogger(ml, reg)

	policy := &mockPolicyEvaluator{
		decision: &PolicyDecision{
			Evaluated:  1,
			Allowed:    false,
			DeniedBy:   "rate_policy",
			DeniedRule: "tx_count",
			Reason:     "maximum transaction count exceeded",
		},
		err:       fmt.Errorf("maximum transaction count exceeded"),
		denyAfter: 1, // allow CreateSession (1st call), deny Proxy (2nd call)
	}
	svc.WithPolicyEvaluator(policy)

	ctx := context.Background()
	session, err := svc.CreateSession(ctx, "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "1.00",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	_, err = svc.Proxy(ctx, session.ID, ProxyRequest{ServiceType: "translation"})
	if err == nil {
		t.Fatal("expected error when policy denies")
	}
	if !strings.Contains(err.Error(), "policy denied") {
		t.Errorf("expected 'policy denied' in error, got: %v", err)
	}

	// No settlement should have occurred
	if len(ml.settlements) != 0 {
		t.Errorf("expected 0 settlements (policy denied before forwarding), got %d", len(ml.settlements))
	}

	// Session spend should be unchanged
	updated, _ := svc.GetSession(ctx, session.ID)
	if updated.TotalSpent != "0.000000" {
		t.Errorf("expected 0 spent, got %s", updated.TotalSpent)
	}
}

func TestProxy_PolicyAllowed(t *testing.T) {
	server := fakeServiceEndpoint(200, map[string]interface{}{"ok": true})
	defer server.Close()

	ml := newMockLedger()
	reg := &mockRegistry{
		services: []ServiceCandidate{
			{AgentAddress: "0xseller", ServiceID: "svc1", ServiceName: "test", Price: "0.10", Endpoint: server.URL},
		},
	}
	svc := newTestServiceWithLogger(ml, reg)

	policy := &mockPolicyEvaluator{
		decision: &PolicyDecision{Evaluated: 2, Allowed: true, LatencyUs: 50},
	}
	svc.WithPolicyEvaluator(policy)

	ctx := context.Background()
	session, _ := svc.CreateSession(ctx, "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "1.00",
	})

	result, err := svc.Proxy(ctx, session.ID, ProxyRequest{ServiceType: "translation"})
	if err != nil {
		t.Fatalf("proxy should succeed when policy allows: %v", err)
	}
	if result.AmountPaid != "0.100000" {
		t.Errorf("expected 0.100000 paid, got %s", result.AmountPaid)
	}

	// Policy should have been evaluated (once in CreateSession + once in Proxy phase 2a)
	if policy.calls < 2 {
		t.Errorf("expected at least 2 policy evaluations (create + proxy), got %d", policy.calls)
	}
}

func TestCreateSession_PolicyDenied(t *testing.T) {
	ml := newMockLedger()
	svc := newTestServiceWithLogger(ml, &mockRegistry{})

	policy := &mockPolicyEvaluator{
		decision: &PolicyDecision{
			Evaluated:  1,
			Allowed:    false,
			DeniedBy:   "time_policy",
			DeniedRule: "time_window",
			Reason:     "outside allowed hours",
		},
		err: fmt.Errorf("outside allowed hours"),
	}
	svc.WithPolicyEvaluator(policy)

	_, err := svc.CreateSession(context.Background(), "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "1.00",
	})
	if err == nil {
		t.Fatal("expected error when policy denies session creation")
	}
	if !strings.Contains(err.Error(), "policy denied") {
		t.Errorf("expected 'policy denied' in error, got: %v", err)
	}

	// No hold should have been created (policy denied before hold)
	if len(ml.holds) != 0 {
		t.Errorf("expected 0 holds (policy denied before hold), got %d", len(ml.holds))
	}
}

func TestProxy_PolicyDeniedLogsDecision(t *testing.T) {
	server := fakeServiceEndpoint(200, map[string]interface{}{"ok": true})
	defer server.Close()

	ml := newMockLedger()
	reg := &mockRegistry{
		services: []ServiceCandidate{
			{AgentAddress: "0xseller", ServiceID: "svc1", ServiceName: "test", Price: "0.10", Endpoint: server.URL},
		},
	}
	svc := newTestServiceWithLogger(ml, reg)

	policy := &mockPolicyEvaluator{
		decision: &PolicyDecision{
			Evaluated:  1,
			Allowed:    false,
			DeniedBy:   "spending_limit",
			DeniedRule: "tx_count",
			Reason:     "limit exceeded",
			LatencyUs:  42,
		},
		err:       fmt.Errorf("limit exceeded"),
		denyAfter: 1, // allow CreateSession (1st call), deny Proxy (2nd call)
	}
	svc.WithPolicyEvaluator(policy)

	ctx := context.Background()
	session, err := svc.CreateSession(ctx, "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "1.00",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	_, _ = svc.Proxy(ctx, session.ID, ProxyRequest{ServiceType: "translation"})

	// Check request logs for the policy_denied entry
	logs, err := svc.ListLogs(ctx, session.ID, 10)
	if err != nil {
		t.Fatalf("list logs: %v", err)
	}

	var found bool
	for _, log := range logs {
		if log.Status == "policy_denied" {
			found = true
			if log.PolicyResult == nil {
				t.Error("expected PolicyResult in log entry")
			} else {
				if log.PolicyResult.DeniedBy != "spending_limit" {
					t.Errorf("expected DeniedBy 'spending_limit', got %q", log.PolicyResult.DeniedBy)
				}
				if log.PolicyResult.DeniedRule != "tx_count" {
					t.Errorf("expected DeniedRule 'tx_count', got %q", log.PolicyResult.DeniedRule)
				}
			}
		}
	}
	if !found {
		t.Error("expected a 'policy_denied' log entry")
	}
}

func TestProxy_NilPolicyEvaluator(t *testing.T) {
	server := fakeServiceEndpoint(200, map[string]interface{}{"ok": true})
	defer server.Close()

	ml := newMockLedger()
	reg := &mockRegistry{
		services: []ServiceCandidate{
			{AgentAddress: "0xseller", ServiceID: "svc1", ServiceName: "test", Price: "0.10", Endpoint: server.URL},
		},
	}
	// No policy evaluator set — should work fine
	svc := newTestServiceWithLogger(ml, reg)

	ctx := context.Background()
	session, _ := svc.CreateSession(ctx, "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "1.00",
	})

	result, err := svc.Proxy(ctx, session.ID, ProxyRequest{ServiceType: "test"})
	if err != nil {
		t.Fatalf("proxy should succeed without policy evaluator: %v", err)
	}
	if result.AmountPaid != "0.100000" {
		t.Errorf("expected 0.100000, got %s", result.AmountPaid)
	}
}

func TestPolicyDecision_NilSafeAccessors(t *testing.T) {
	var d *PolicyDecision

	if d.GetDeniedBy() != "" {
		t.Error("GetDeniedBy on nil should return empty")
	}
	if d.GetDeniedRule() != "" {
		t.Error("GetDeniedRule on nil should return empty")
	}
	if d.GetReason() != "" {
		t.Error("GetReason on nil should return empty")
	}

	d = &PolicyDecision{DeniedBy: "p", DeniedRule: "r", Reason: "x"}
	if d.GetDeniedBy() != "p" {
		t.Errorf("expected 'p', got %q", d.GetDeniedBy())
	}
}

func TestIdempotencyCacheSweep(t *testing.T) {
	cache := newIdempotencyCache(50 * time.Millisecond) // 50ms TTL

	// Reserve and complete an entry
	_, _, found := cache.getOrReserve(context.Background(), "s1", "key1")
	if found {
		t.Fatal("should not find entry on first call")
	}
	cache.complete("s1", "key1", &ProxyResult{AmountPaid: "0.10"})

	// Should be in cache
	if cache.size() != 1 {
		t.Errorf("expected 1 entry, got %d", cache.size())
	}

	// Wait for TTL to expire
	time.Sleep(60 * time.Millisecond)

	// Sweep should remove expired entry
	removed := cache.sweep()
	if removed != 1 {
		t.Errorf("expected 1 removed, got %d", removed)
	}
	if cache.size() != 0 {
		t.Errorf("expected 0 entries after sweep, got %d", cache.size())
	}
}
