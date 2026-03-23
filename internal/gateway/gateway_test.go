package gateway

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
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

func (m *mockLedger) SettleHoldWithCallback(ctx context.Context, buyerAddr, sellerAddr, amount, reference string, preCommit func(tx *sql.Tx) error) error {
	return m.SettleHold(ctx, buyerAddr, sellerAddr, amount, reference)
}

func (m *mockLedger) SettleHoldWithFeeAndCallback(ctx context.Context, buyerAddr, sellerAddr, sellerAmount, platformAddr, feeAmount, reference string, preCommit func(tx *sql.Tx) error) error {
	return m.SettleHoldWithFee(ctx, buyerAddr, sellerAddr, sellerAmount, platformAddr, feeAmount, reference)
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
	forwarder := NewForwarder(5 * time.Second).WithAllowLocalEndpoints()
	return NewService(store, resolver, forwarder, ledger, nil)
}

func newTestServiceWithLogger(ledger *mockLedger, registry *mockRegistry) *Service {
	store := NewMemoryStore()
	resolver := NewResolver(registry)
	forwarder := NewForwarder(5 * time.Second).WithAllowLocalEndpoints()
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
	forwarder := NewForwarder(5 * time.Second).WithAllowLocalEndpoints()
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
	forwarder := NewForwarder(5 * time.Second).WithAllowLocalEndpoints()
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

func (r *retryMockLedger) SettleHoldWithCallback(ctx context.Context, buyerAddr, sellerAddr, amount, reference string, _ func(tx *sql.Tx) error) error {
	return r.SettleHold(ctx, buyerAddr, sellerAddr, amount, reference)
}

func (r *retryMockLedger) SettleHoldWithFeeAndCallback(ctx context.Context, buyerAddr, sellerAddr, sellerAmount, platformAddr, feeAmount, reference string, _ func(tx *sql.Tx) error) error {
	return r.SettleHoldWithFee(ctx, buyerAddr, sellerAddr, sellerAmount, platformAddr, feeAmount, reference)
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
	forwarder := NewForwarder(5 * time.Second).WithAllowLocalEndpoints()
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

// ---------------------------------------------------------------------------
// Rate limiter tests
// ---------------------------------------------------------------------------

func TestProxy_RateLimited(t *testing.T) {
	server := fakeServiceEndpoint(200, map[string]interface{}{"ok": true})
	defer server.Close()

	ml := newMockLedger()
	reg := &mockRegistry{
		services: []ServiceCandidate{
			{AgentAddress: "0xseller", ServiceID: "svc1", ServiceName: "test", Price: "0.01", Endpoint: server.URL},
		},
	}
	svc := newTestServiceWithLogger(ml, reg)

	ctx := context.Background()
	session, err := svc.CreateSession(ctx, "0xbuyer", "", CreateSessionRequest{
		MaxTotal:             "100.00",
		MaxPerRequest:        "1.00",
		MaxRequestsPerMinute: 3, // Very low limit for testing
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	// First 3 requests should succeed
	for i := 0; i < 3; i++ {
		_, err := svc.Proxy(ctx, session.ID, ProxyRequest{ServiceType: "test"})
		if err != nil {
			t.Fatalf("proxy %d should succeed: %v", i, err)
		}
	}

	// 4th request should be rate limited
	_, err = svc.Proxy(ctx, session.ID, ProxyRequest{ServiceType: "test"})
	if err == nil {
		t.Fatal("expected rate limit error on 4th request")
	}
	if !strings.Contains(err.Error(), "rate limit") {
		t.Errorf("expected rate limit error, got: %v", err)
	}

	// Only 3 settlements should exist
	if len(ml.settlements) != 3 {
		t.Errorf("expected 3 settlements, got %d", len(ml.settlements))
	}
}

func TestProxy_RateLimitDefaultApplied(t *testing.T) {
	ml := newMockLedger()
	svc := newTestServiceWithLogger(ml, &mockRegistry{
		services: []ServiceCandidate{
			{AgentAddress: "0xseller", ServiceID: "svc1", ServiceName: "test", Price: "0.01",
				Endpoint: "http://localhost"},
		},
	})

	ctx := context.Background()
	session, _ := svc.CreateSession(ctx, "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "100.00",
		MaxPerRequest: "1.00",
		// No MaxRequestsPerMinute → default 100
	})

	if session.MaxRequestsPerMinute != defaultMaxRequestsPerMinute {
		t.Errorf("expected default %d, got %d", defaultMaxRequestsPerMinute, session.MaxRequestsPerMinute)
	}
}

func TestProxy_RateLimitCappedAt1000(t *testing.T) {
	ml := newMockLedger()
	svc := newTestServiceWithLogger(ml, &mockRegistry{})

	ctx := context.Background()
	session, _ := svc.CreateSession(ctx, "0xbuyer", "", CreateSessionRequest{
		MaxTotal:             "100.00",
		MaxPerRequest:        "1.00",
		MaxRequestsPerMinute: 5000, // Above max
	})

	if session.MaxRequestsPerMinute != 1000 {
		t.Errorf("expected capped at 1000, got %d", session.MaxRequestsPerMinute)
	}
}

func TestProxy_RateLimitCleanedUpOnClose(t *testing.T) {
	ml := newMockLedger()
	svc := newTestServiceWithLogger(ml, &mockRegistry{})

	ctx := context.Background()
	session, _ := svc.CreateSession(ctx, "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "1.00",
	})

	if svc.rateLimit.size() != 1 {
		t.Errorf("expected 1 rate limit entry after create, got %d", svc.rateLimit.size())
	}

	svc.CloseSession(ctx, session.ID, "0xbuyer")

	if svc.rateLimit.size() != 0 {
		t.Errorf("expected 0 rate limit entries after close, got %d", svc.rateLimit.size())
	}
}

func TestRateLimiter_WindowReset(t *testing.T) {
	rl := newRateLimiter()
	rl.window = 50 * time.Millisecond // Short window for testing

	rl.setLimit("s1", 2)

	if !rl.allow("s1") {
		t.Fatal("first request should be allowed")
	}
	if !rl.allow("s1") {
		t.Fatal("second request should be allowed")
	}
	if rl.allow("s1") {
		t.Fatal("third request should be denied (limit=2)")
	}

	// Wait for window to reset
	time.Sleep(60 * time.Millisecond)

	if !rl.allow("s1") {
		t.Fatal("request after window reset should be allowed")
	}
}

func TestRateLimiter_Sweep(t *testing.T) {
	rl := newRateLimiter()
	rl.window = 25 * time.Millisecond // Short window for testing

	rl.allow("s1")
	rl.allow("s2")

	if rl.size() != 2 {
		t.Errorf("expected 2 entries, got %d", rl.size())
	}

	// Wait for entries to become stale (2 * window = 50ms)
	time.Sleep(60 * time.Millisecond)

	removed := rl.sweep()
	if removed != 2 {
		t.Errorf("expected 2 removed, got %d", removed)
	}
	if rl.size() != 0 {
		t.Errorf("expected 0 entries after sweep, got %d", rl.size())
	}
}

// ---------------------------------------------------------------------------
// DryRun tests
// ---------------------------------------------------------------------------

func TestDryRun_AllGreen(t *testing.T) {
	server := fakeServiceEndpoint(200, map[string]interface{}{"ok": true})
	defer server.Close()

	ml := newMockLedger()
	reg := &mockRegistry{
		services: []ServiceCandidate{
			{AgentAddress: "0xseller", ServiceID: "svc1", ServiceName: "translate", Price: "0.50", Endpoint: server.URL},
		},
	}
	svc := newTestServiceWithLogger(ml, reg)

	ctx := context.Background()
	session, _ := svc.CreateSession(ctx, "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "1.00",
	})

	result, err := svc.DryRun(ctx, session.ID, ProxyRequest{ServiceType: "translation"})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if !result.Allowed {
		t.Errorf("expected allowed, got denied: %s", result.DenyReason)
	}
	if !result.BudgetOK {
		t.Error("expected budgetOk=true")
	}
	if !result.ServiceFound {
		t.Error("expected serviceFound=true")
	}
	if result.BestPrice != "0.50" {
		t.Errorf("expected bestPrice 0.50, got %s", result.BestPrice)
	}
	if result.BestService != "translate" {
		t.Errorf("expected bestService translate, got %s", result.BestService)
	}
	// DryRun must NOT move money
	if len(ml.settlements) != 0 {
		t.Errorf("expected 0 settlements (dry run), got %d", len(ml.settlements))
	}
	// Session spend unchanged
	updated, _ := svc.GetSession(ctx, session.ID)
	if updated.RequestCount != 0 {
		t.Errorf("expected 0 requests (dry run), got %d", updated.RequestCount)
	}
}

func TestDryRun_NoService(t *testing.T) {
	ml := newMockLedger()
	reg := &mockRegistry{services: nil}
	svc := newTestServiceWithLogger(ml, reg)

	ctx := context.Background()
	session, _ := svc.CreateSession(ctx, "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "1.00",
	})

	result, err := svc.DryRun(ctx, session.ID, ProxyRequest{ServiceType: "translation"})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if result.Allowed {
		t.Error("expected denied when no services available")
	}
	if result.ServiceFound {
		t.Error("expected serviceFound=false")
	}
}

func TestDryRun_PolicyDenied(t *testing.T) {
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
			Reason:     "limit exceeded",
		},
		err:       fmt.Errorf("limit exceeded"),
		denyAfter: 1, // allow CreateSession, deny DryRun
	}
	svc.WithPolicyEvaluator(policy)

	ctx := context.Background()
	session, _ := svc.CreateSession(ctx, "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "1.00",
	})

	result, err := svc.DryRun(ctx, session.ID, ProxyRequest{ServiceType: "test"})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if result.Allowed {
		t.Error("expected denied when policy blocks")
	}
	if result.PolicyResult == nil {
		t.Fatal("expected PolicyResult in dry run result")
	}
	if result.PolicyResult.DeniedRule != "tx_count" {
		t.Errorf("expected deniedRule 'tx_count', got %q", result.PolicyResult.DeniedRule)
	}
}

func TestDryRun_ClosedSession(t *testing.T) {
	ml := newMockLedger()
	svc := newTestServiceWithLogger(ml, &mockRegistry{})

	ctx := context.Background()
	session, _ := svc.CreateSession(ctx, "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "1.00",
	})
	svc.CloseSession(ctx, session.ID, "0xbuyer")

	result, err := svc.DryRun(ctx, session.ID, ProxyRequest{ServiceType: "test"})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if result.Allowed {
		t.Error("expected denied for closed session")
	}
	if result.DenyReason != "session is not active" {
		t.Errorf("expected 'session is not active', got %q", result.DenyReason)
	}
}

func TestDryRun_ShadowPolicyStillAllows(t *testing.T) {
	server := fakeServiceEndpoint(200, map[string]interface{}{"ok": true})
	defer server.Close()

	ml := newMockLedger()
	reg := &mockRegistry{
		services: []ServiceCandidate{
			{AgentAddress: "0xseller", ServiceID: "svc1", ServiceName: "test", Price: "0.10", Endpoint: server.URL},
		},
	}
	svc := newTestServiceWithLogger(ml, reg)

	// Shadow policy: would deny but Shadow=true means it doesn't block.
	policy := &mockPolicyEvaluator{
		decision: &PolicyDecision{
			Evaluated:  1,
			Allowed:    false,
			Shadow:     true,
			DeniedBy:   "shadow_policy",
			DeniedRule: "tx_count",
			Reason:     "shadow denial",
		},
		// Shadow mode: decision has Allowed=false + Shadow=true, but err is nil.
		err:       nil,
		denyAfter: 1, // allow CreateSession (1st call), return shadow on DryRun (2nd call)
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

	result, err := svc.DryRun(ctx, session.ID, ProxyRequest{ServiceType: "test"})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if !result.Allowed {
		t.Error("expected allowed (shadow policy should not block)")
	}
	if result.PolicyResult == nil {
		t.Fatal("expected PolicyResult to be present")
	}
	if !result.PolicyResult.Shadow {
		t.Error("expected shadow=true in policy result")
	}
}

// ---------------------------------------------------------------------------
// valueScore big.Float fix tests
// ---------------------------------------------------------------------------

func TestValueScore_SmallPrice(t *testing.T) {
	score := valueScore(ServiceCandidate{
		Price:           "0.50",
		ReputationScore: 80,
	})
	if score <= 0 {
		t.Errorf("expected positive score, got %f", score)
	}
	// Cobb-Douglas: 80^0.65 × (1/0.50)^0.35 ≈ 22.0
	expected := 22.0
	if score < expected*0.95 || score > expected*1.05 {
		t.Errorf("expected ~%.1f, got %f", expected, score)
	}
}

func TestValueScore_ZeroPrice(t *testing.T) {
	score := valueScore(ServiceCandidate{
		Price:           "0.00",
		ReputationScore: 80,
	})
	if score != 0 {
		t.Errorf("expected 0 for zero price, got %f", score)
	}
}

func TestValueScore_InvalidPrice(t *testing.T) {
	score := valueScore(ServiceCandidate{
		Price:           "invalid",
		ReputationScore: 80,
	})
	if score != 0 {
		t.Errorf("expected 0 for invalid price, got %f", score)
	}
}

// ---------------------------------------------------------------------------
// Policy evaluation error (nil decision) path test
// ---------------------------------------------------------------------------

func TestProxy_PolicyEvalFailure_NilDecision(t *testing.T) {
	server := fakeServiceEndpoint(200, map[string]interface{}{"ok": true})
	defer server.Close()

	ml := newMockLedger()
	reg := &mockRegistry{
		services: []ServiceCandidate{
			{AgentAddress: "0xseller", ServiceID: "svc1", ServiceName: "test", Price: "0.10", Endpoint: server.URL},
		},
	}
	svc := newTestServiceWithLogger(ml, reg)

	// Policy evaluator returns nil decision + error (store failure).
	policy := &mockPolicyEvaluator{
		decision:  nil,
		err:       fmt.Errorf("database connection refused"),
		denyAfter: 1, // allow CreateSession, fail on Proxy
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

	_, err = svc.Proxy(ctx, session.ID, ProxyRequest{ServiceType: "test"})
	if err == nil {
		t.Fatal("expected error when policy evaluator returns nil decision + error")
	}

	// Should not have forwarded (fail closed)
	if len(ml.settlements) != 0 {
		t.Errorf("expected 0 settlements (fail closed), got %d", len(ml.settlements))
	}

	// Check request log for "policy_error" status
	logs, _ := svc.ListLogs(ctx, session.ID, 10)
	var found bool
	for _, log := range logs {
		if log.Status == "policy_error" {
			found = true
		}
	}
	if !found {
		t.Error("expected a 'policy_error' log entry for nil-decision store failure")
	}
}

func TestProxy_ShadowPolicyDoesNotBlock(t *testing.T) {
	server := fakeServiceEndpoint(200, map[string]interface{}{"ok": true})
	defer server.Close()

	ml := newMockLedger()
	reg := &mockRegistry{
		services: []ServiceCandidate{
			{AgentAddress: "0xseller", ServiceID: "svc1", ServiceName: "test", Price: "0.10", Endpoint: server.URL},
		},
	}
	svc := newTestServiceWithLogger(ml, reg)

	// Shadow policy: decision.Allowed=false, Shadow=true, err=nil
	policy := &mockPolicyEvaluator{
		decision: &PolicyDecision{
			Evaluated:  1,
			Allowed:    false,
			Shadow:     true,
			DeniedBy:   "shadow_test",
			DeniedRule: "tx_count",
			Reason:     "shadow denial",
		},
		err:       nil, // nil error + shadow = allow
		denyAfter: 1,   // allow CreateSession (1st call), shadow deny on Proxy (2nd call)
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

	result, err := svc.Proxy(ctx, session.ID, ProxyRequest{ServiceType: "test"})
	if err != nil {
		t.Fatalf("shadow policy should not block proxy: %v", err)
	}
	if result.AmountPaid != "0.100000" {
		t.Errorf("expected 0.100000 paid, got %s", result.AmountPaid)
	}

	// Should have settled (request went through)
	if len(ml.settlements) != 1 {
		t.Errorf("expected 1 settlement (shadow allows), got %d", len(ml.settlements))
	}

	// Check request logs for shadow_denied entry
	logs, _ := svc.ListLogs(ctx, session.ID, 10)
	var shadowFound bool
	for _, log := range logs {
		if log.Status == "shadow_denied" {
			shadowFound = true
		}
	}
	if !shadowFound {
		t.Error("expected a 'shadow_denied' log entry")
	}
}

func TestIdempotencyCacheSweep(t *testing.T) {
	cache := newIdempotencyCache(50 * time.Millisecond) // 50ms TTL

	// Reserve and complete an entry
	_, _, found := cache.GetOrReserve(context.Background(), "s1", "key1")
	if found {
		t.Fatal("should not find entry on first call")
	}
	cache.Complete("s1", "key1", &ProxyResult{AmountPaid: "0.10"})

	// Should be in cache
	if cache.Size() != 1 {
		t.Errorf("expected 1 entry, got %d", cache.Size())
	}

	// Wait for TTL to expire
	time.Sleep(60 * time.Millisecond)

	// Sweep should remove expired entry
	removed := cache.Sweep()
	if removed != 1 {
		t.Errorf("expected 1 removed, got %d", removed)
	}
	if cache.Size() != 0 {
		t.Errorf("expected 0 entries after sweep, got %d", cache.Size())
	}
}

// --- merged from coverage_extra_test.go ---

// ============================================================================
// MemoryStore coverage: billing, analytics, time-series, policy denials
// ============================================================================

func TestMemoryStore_GetBillingSummary(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	// Empty summary
	row, err := store.GetBillingSummary(ctx, "tenant1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if row.TotalRequests != 0 {
		t.Errorf("expected 0 total requests, got %d", row.TotalRequests)
	}
	if row.SettledVolume != "0" {
		t.Errorf("expected 0 settled volume, got %s", row.SettledVolume)
	}

	// Add some logs
	store.CreateLog(ctx, &RequestLog{
		ID:          "log1",
		SessionID:   "s1",
		TenantID:    "tenant1",
		ServiceType: "inference",
		Amount:      "1.000000",
		FeeAmount:   "0.010000",
		Status:      "success",
		CreatedAt:   time.Now(),
	})
	store.CreateLog(ctx, &RequestLog{
		ID:          "log2",
		SessionID:   "s1",
		TenantID:    "tenant1",
		ServiceType: "inference",
		Amount:      "2.000000",
		FeeAmount:   "0.020000",
		Status:      "success",
		CreatedAt:   time.Now(),
	})
	store.CreateLog(ctx, &RequestLog{
		ID:          "log3",
		SessionID:   "s1",
		TenantID:    "tenant1",
		ServiceType: "inference",
		Amount:      "0",
		Status:      "forward_failed",
		CreatedAt:   time.Now(),
	})
	// Different tenant
	store.CreateLog(ctx, &RequestLog{
		ID:          "log4",
		SessionID:   "s2",
		TenantID:    "tenant2",
		ServiceType: "inference",
		Amount:      "5.000000",
		FeeAmount:   "0.050000",
		Status:      "success",
		CreatedAt:   time.Now(),
	})

	row, err = store.GetBillingSummary(ctx, "tenant1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if row.TotalRequests != 3 {
		t.Errorf("expected 3 total requests, got %d", row.TotalRequests)
	}
	if row.SettledRequests != 2 {
		t.Errorf("expected 2 settled requests, got %d", row.SettledRequests)
	}
	if row.SettledVolume != "3.000000" {
		t.Errorf("expected 3.000000 settled volume, got %s", row.SettledVolume)
	}
	if row.FeesCollected != "0.030000" {
		t.Errorf("expected 0.030000 fees, got %s", row.FeesCollected)
	}
}

func TestMemoryStore_GetBillingTimeSeries_Day(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	store.CreateLog(ctx, &RequestLog{
		ID: "log1", SessionID: "s1", TenantID: "t1",
		Amount: "1.000000", FeeAmount: "0.010000", Status: "success",
		CreatedAt: today.Add(2 * time.Hour),
	})
	store.CreateLog(ctx, &RequestLog{
		ID: "log2", SessionID: "s1", TenantID: "t1",
		Amount: "2.000000", FeeAmount: "0.020000", Status: "success",
		CreatedAt: today.Add(5 * time.Hour),
	})

	from := today
	to := today.Add(24 * time.Hour)
	points, err := store.GetBillingTimeSeries(ctx, "t1", "day", from, to)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(points) != 1 {
		t.Fatalf("expected 1 daily bucket, got %d", len(points))
	}
	if points[0].Requests != 2 {
		t.Errorf("expected 2 requests in bucket, got %d", points[0].Requests)
	}
	if points[0].SettledRequests != 2 {
		t.Errorf("expected 2 settled requests, got %d", points[0].SettledRequests)
	}
}

func TestMemoryStore_GetBillingTimeSeries_Hour(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Hour)

	store.CreateLog(ctx, &RequestLog{
		ID: "log1", SessionID: "s1", TenantID: "t1",
		Amount: "1.000000", Status: "success",
		CreatedAt: now.Add(10 * time.Minute),
	})
	store.CreateLog(ctx, &RequestLog{
		ID: "log2", SessionID: "s1", TenantID: "t1",
		Amount: "2.000000", Status: "success",
		CreatedAt: now.Add(70 * time.Minute), // next hour
	})

	from := now
	to := now.Add(3 * time.Hour)
	points, err := store.GetBillingTimeSeries(ctx, "t1", "hour", from, to)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(points) != 2 {
		t.Errorf("expected 2 hourly buckets, got %d", len(points))
	}
}

func TestMemoryStore_GetBillingTimeSeries_Week(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	// Use a known date in week 1 of 2026
	refDate := time.Date(2026, 1, 5, 12, 0, 0, 0, time.UTC) // Monday Jan 5
	store.CreateLog(ctx, &RequestLog{
		ID: "log1", SessionID: "s1", TenantID: "t1",
		Amount: "1.000000", Status: "success",
		CreatedAt: refDate,
	})

	from := refDate.AddDate(0, 0, -7)
	to := refDate.AddDate(0, 0, 7)
	points, err := store.GetBillingTimeSeries(ctx, "t1", "week", from, to)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(points) == 0 {
		t.Error("expected at least 1 weekly bucket")
	}
}

func TestMemoryStore_GetBillingTimeSeries_OutOfRange(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	now := time.Now().UTC()
	store.CreateLog(ctx, &RequestLog{
		ID: "log1", SessionID: "s1", TenantID: "t1",
		Amount: "1.000000", Status: "success",
		CreatedAt: now,
	})

	// Query a range that doesn't include the log
	from := now.Add(1 * time.Hour)
	to := now.Add(2 * time.Hour)
	points, err := store.GetBillingTimeSeries(ctx, "t1", "day", from, to)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(points) != 0 {
		t.Errorf("expected 0 buckets for out-of-range, got %d", len(points))
	}
}

func TestMemoryStore_GetTopServiceTypes(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	store.CreateLog(ctx, &RequestLog{
		ID: "log1", SessionID: "s1", TenantID: "t1",
		ServiceType: "inference", Amount: "1.000000", Status: "success",
		CreatedAt: time.Now(),
	})
	store.CreateLog(ctx, &RequestLog{
		ID: "log2", SessionID: "s1", TenantID: "t1",
		ServiceType: "inference", Amount: "2.000000", Status: "success",
		CreatedAt: time.Now(),
	})
	store.CreateLog(ctx, &RequestLog{
		ID: "log3", SessionID: "s1", TenantID: "t1",
		ServiceType: "translation", Amount: "0.500000", Status: "success",
		CreatedAt: time.Now(),
	})
	// Failed request should be excluded
	store.CreateLog(ctx, &RequestLog{
		ID: "log4", SessionID: "s1", TenantID: "t1",
		ServiceType: "inference", Amount: "0", Status: "forward_failed",
		CreatedAt: time.Now(),
	})

	types, err := store.GetTopServiceTypes(ctx, "t1", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(types) != 2 {
		t.Fatalf("expected 2 service types, got %d", len(types))
	}
	// inference should be first (more requests)
	if types[0].ServiceType != "inference" {
		t.Errorf("expected inference first, got %s", types[0].ServiceType)
	}
	if types[0].Requests != 2 {
		t.Errorf("expected 2 inference requests, got %d", types[0].Requests)
	}
}

func TestMemoryStore_GetTopServiceTypes_WithLimit(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		store.CreateLog(ctx, &RequestLog{
			ID: fmt.Sprintf("log%d", i), SessionID: "s1", TenantID: "t1",
			ServiceType: fmt.Sprintf("type%d", i), Amount: "1.000000", Status: "success",
			CreatedAt: time.Now(),
		})
	}

	types, err := store.GetTopServiceTypes(ctx, "t1", 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(types) != 2 {
		t.Errorf("expected 2 types with limit, got %d", len(types))
	}
}

func TestMemoryStore_GetPolicyDenials(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	store.CreateLog(ctx, &RequestLog{
		ID: "log1", SessionID: "s1", TenantID: "t1",
		Status: "policy_denied", CreatedAt: time.Now().Add(-2 * time.Hour),
	})
	store.CreateLog(ctx, &RequestLog{
		ID: "log2", SessionID: "s1", TenantID: "t1",
		Status: "policy_denied", CreatedAt: time.Now().Add(-1 * time.Hour),
	})
	store.CreateLog(ctx, &RequestLog{
		ID: "log3", SessionID: "s1", TenantID: "t1",
		Status: "success", CreatedAt: time.Now(),
	})

	denials, err := store.GetPolicyDenials(ctx, "t1", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(denials) != 2 {
		t.Fatalf("expected 2 denials, got %d", len(denials))
	}
	// Most recent first
	if denials[0].ID != "log2" {
		t.Errorf("expected log2 first (most recent), got %s", denials[0].ID)
	}
}

func TestMemoryStore_GetPolicyDenials_WithLimit(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		store.CreateLog(ctx, &RequestLog{
			ID: fmt.Sprintf("log%d", i), SessionID: "s1", TenantID: "t1",
			Status: "policy_denied", CreatedAt: time.Now().Add(time.Duration(-i) * time.Hour),
		})
	}

	denials, err := store.GetPolicyDenials(ctx, "t1", 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(denials) != 2 {
		t.Errorf("expected 2 denials with limit, got %d", len(denials))
	}
}

func TestMemoryStore_ListByStatus(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	now := time.Now()
	store.CreateSession(ctx, &Session{
		ID: "s1", AgentAddr: "0xa", Status: StatusActive,
		UpdatedAt: now.Add(-2 * time.Hour),
	})
	store.CreateSession(ctx, &Session{
		ID: "s2", AgentAddr: "0xb", Status: StatusClosed,
		UpdatedAt: now.Add(-1 * time.Hour),
	})
	store.CreateSession(ctx, &Session{
		ID: "s3", AgentAddr: "0xc", Status: StatusActive,
		UpdatedAt: now,
	})

	active, err := store.ListByStatus(ctx, StatusActive, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(active) != 2 {
		t.Errorf("expected 2 active sessions, got %d", len(active))
	}
	// Most recently updated first
	if active[0].ID != "s3" {
		t.Errorf("expected s3 first (most recent), got %s", active[0].ID)
	}
}

func TestMemoryStore_ListByStatus_WithLimit(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		store.CreateSession(ctx, &Session{
			ID: fmt.Sprintf("s%d", i), AgentAddr: "0xa", Status: StatusActive,
			UpdatedAt: time.Now().Add(time.Duration(i) * time.Minute),
		})
	}

	active, err := store.ListByStatus(ctx, StatusActive, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(active) != 2 {
		t.Errorf("expected 2 active with limit, got %d", len(active))
	}
}

func TestMemoryStore_ListSessionsByTenant(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	now := time.Now()
	store.CreateSession(ctx, &Session{
		ID: "s1", AgentAddr: "0xa", TenantID: "tenant1",
		CreatedAt: now.Add(-2 * time.Hour),
	})
	store.CreateSession(ctx, &Session{
		ID: "s2", AgentAddr: "0xb", TenantID: "tenant2",
		CreatedAt: now.Add(-1 * time.Hour),
	})
	store.CreateSession(ctx, &Session{
		ID: "s3", AgentAddr: "0xc", TenantID: "tenant1",
		CreatedAt: now,
	})

	sessions, err := store.ListSessionsByTenant(ctx, "tenant1", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions for tenant1, got %d", len(sessions))
	}
	// Most recent first
	if sessions[0].ID != "s3" {
		t.Errorf("expected s3 first, got %s", sessions[0].ID)
	}
}

func TestMemoryStore_ListSessionsByTenant_WithLimit(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		store.CreateSession(ctx, &Session{
			ID: fmt.Sprintf("s%d", i), AgentAddr: "0xa", TenantID: "t1",
			CreatedAt: time.Now().Add(time.Duration(i) * time.Minute),
		})
	}

	sessions, err := store.ListSessionsByTenant(ctx, "t1", 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sessions) != 2 {
		t.Errorf("expected 2 sessions with limit, got %d", len(sessions))
	}
}

func TestMemoryStore_ListExpired(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	now := time.Now()
	store.CreateSession(ctx, &Session{
		ID: "s1", AgentAddr: "0xa", Status: StatusActive,
		ExpiresAt:    now.Add(-1 * time.Hour),
		AllowedTypes: []string{"test"},
	})
	store.CreateSession(ctx, &Session{
		ID: "s2", AgentAddr: "0xb", Status: StatusActive,
		ExpiresAt: now.Add(1 * time.Hour), // not expired
	})
	store.CreateSession(ctx, &Session{
		ID: "s3", AgentAddr: "0xc", Status: StatusClosed, // not active
		ExpiresAt: now.Add(-2 * time.Hour),
	})

	expired, err := store.ListExpired(ctx, now, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(expired) != 1 {
		t.Fatalf("expected 1 expired session, got %d", len(expired))
	}
	if expired[0].ID != "s1" {
		t.Errorf("expected s1, got %s", expired[0].ID)
	}
	// Verify deep copy of AllowedTypes
	if len(expired[0].AllowedTypes) != 1 || expired[0].AllowedTypes[0] != "test" {
		t.Error("expected AllowedTypes to be deep copied")
	}
}

func TestMemoryStore_ListExpired_WithLimit(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	now := time.Now()
	for i := 0; i < 5; i++ {
		store.CreateSession(ctx, &Session{
			ID: fmt.Sprintf("s%d", i), AgentAddr: "0xa", Status: StatusActive,
			ExpiresAt: now.Add(-1 * time.Hour),
		})
	}

	expired, err := store.ListExpired(ctx, now, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(expired) != 2 {
		t.Errorf("expected 2 expired with limit, got %d", len(expired))
	}
}

func TestMemoryStore_UpdateSession_NotFound(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	err := store.UpdateSession(ctx, &Session{ID: "nonexistent"})
	if err != ErrSessionNotFound {
		t.Errorf("expected ErrSessionNotFound, got %v", err)
	}
}

func TestMemoryStore_UpdateSession_DeepCopy(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	store.CreateSession(ctx, &Session{
		ID: "s1", AgentAddr: "0xa", Status: StatusActive,
		AllowedTypes: []string{"test"},
	})

	// Update with new allowed types
	updated := &Session{
		ID: "s1", AgentAddr: "0xa", Status: StatusActive,
		AllowedTypes: []string{"test", "inference"},
	}
	err := store.UpdateSession(ctx, updated)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Mutate the caller's copy
	updated.AllowedTypes[0] = "mutated"

	// Stored copy should not be affected
	stored, _ := store.GetSession(ctx, "s1")
	if stored.AllowedTypes[0] != "test" {
		t.Errorf("expected deep copy to prevent mutation, got %s", stored.AllowedTypes[0])
	}
}

func TestMemoryStore_ListLogs_Limit(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	for i := 0; i < 10; i++ {
		store.CreateLog(ctx, &RequestLog{
			ID: fmt.Sprintf("log%d", i), SessionID: "s1",
			CreatedAt: time.Now(),
		})
	}

	logs, err := store.ListLogs(ctx, "s1", 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(logs) != 3 {
		t.Errorf("expected 3 logs with limit, got %d", len(logs))
	}
}

// ============================================================================
// addDecimalStrings coverage
// ============================================================================

func TestAddDecimalStrings(t *testing.T) {
	tests := []struct {
		a, b, want string
	}{
		{"1.000000", "2.000000", "3.000000"},
		{"0", "0", "0.000000"},
		{"invalid", "1.000000", "1.000000"},
		{"1.000000", "invalid", "1.000000"},
		{"invalid", "invalid", "0.000000"},
	}

	for _, tt := range tests {
		got := addDecimalStrings(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("addDecimalStrings(%q, %q) = %q, want %q", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestResolver_WithDiscoveryBooster(t *testing.T) {
	reg := &mockRegistry{
		services: []ServiceCandidate{
			{AgentAddress: "0xa", Price: "1.00", Endpoint: "http://a", ReputationScore: 50},
			{AgentAddress: "0xb", Price: "1.00", Endpoint: "http://b", ReputationScore: 80},
		},
	}

	booster := &testBooster{boost: 10.0}
	resolver := NewResolver(reg).WithDiscoveryBooster(booster)

	candidates, err := resolver.Resolve(context.Background(), ProxyRequest{ServiceType: "test"}, "reputation", "5.00")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	// Both should have boosted scores
	if len(candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(candidates))
	}
}

type testBooster struct {
	boost float64
}

func (b *testBooster) BoostScore(_ context.Context, tier string, baseScore float64) float64 {
	return baseScore + b.boost
}

func TestResolver_WithMaxPriceOverride(t *testing.T) {
	reg := &mockRegistry{
		services: []ServiceCandidate{
			{AgentAddress: "0xa", Price: "1.00", Endpoint: "http://a"},
		},
	}
	resolver := NewResolver(reg)

	// MaxPrice from request should override maxPerRequest
	candidates, err := resolver.Resolve(context.Background(), ProxyRequest{
		ServiceType: "test",
		MaxPrice:    "5.00",
	}, "cheapest", "1.00")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(candidates) == 0 {
		t.Error("expected candidates with maxPrice override")
	}
}

func TestResolver_RegistryError(t *testing.T) {
	reg := &mockRegistry{err: fmt.Errorf("db error")}
	resolver := NewResolver(reg)

	_, err := resolver.Resolve(context.Background(), ProxyRequest{ServiceType: "test"}, "cheapest", "5.00")
	if err == nil {
		t.Fatal("expected error from registry")
	}
}

// ============================================================================
// ForwardError coverage
// ============================================================================

func TestForwardError_IsTransient(t *testing.T) {
	tests := []struct {
		code int
		want bool
	}{
		{500, true},
		{502, true},
		{503, true},
		{429, true},
		{400, false},
		{403, false},
		{404, false},
	}
	for _, tt := range tests {
		e := &ForwardError{StatusCode: tt.code, Message: "test"}
		if got := e.IsTransient(); got != tt.want {
			t.Errorf("ForwardError{%d}.IsTransient() = %v, want %v", tt.code, got, tt.want)
		}
	}
}

func TestForwardError_Error(t *testing.T) {
	e := &ForwardError{StatusCode: 500, Message: "service returned HTTP 500"}
	if e.Error() != "service returned HTTP 500" {
		t.Errorf("unexpected error message: %s", e.Error())
	}
}

// ============================================================================
// PolicyDecision nil-safe accessors
// ============================================================================

// ============================================================================
// Session model: IsExpired, Remaining edge cases
// ============================================================================

func TestSession_IsExpired_ZeroTime(t *testing.T) {
	s := &Session{}
	if s.IsExpired() {
		t.Error("zero ExpiresAt should not be expired")
	}
}

func TestSession_IsExpired_Future(t *testing.T) {
	s := &Session{ExpiresAt: time.Now().Add(1 * time.Hour)}
	if s.IsExpired() {
		t.Error("future ExpiresAt should not be expired")
	}
}

func TestSession_IsExpired_Past(t *testing.T) {
	s := &Session{ExpiresAt: time.Now().Add(-1 * time.Hour)}
	if !s.IsExpired() {
		t.Error("past ExpiresAt should be expired")
	}
}

func TestSession_Remaining_NegativeSpent(t *testing.T) {
	// If spent exceeds total (edge case), remaining should be 0
	s := &Session{MaxTotal: "1.000000", TotalSpent: "2.000000"}
	r := s.Remaining()
	if r != "0.000000" {
		t.Errorf("expected 0.000000 for over-spent, got %s", r)
	}
}

func TestSession_BuildAllowedTypesSet_Empty(t *testing.T) {
	s := &Session{}
	s.BuildAllowedTypesSet()
	if s.allowedTypesSet != nil {
		t.Error("expected nil set for empty AllowedTypes")
	}
}

// ============================================================================
// MoneyError
// ============================================================================

func TestMoneyError_WithEmptyFields(t *testing.T) {
	me := &MoneyError{
		Err:         fmt.Errorf("test"),
		FundsStatus: "no_change",
		Recovery:    "retry",
	}
	fields := moneyFields(me)
	if fields == nil {
		t.Fatal("expected fields")
	}
	// Amount and Reference are empty, should not be in fields
	if _, ok := fields["amount"]; ok {
		t.Error("empty amount should not be in fields")
	}
	if _, ok := fields["reference"]; ok {
		t.Error("empty reference should not be in fields")
	}
}

// ============================================================================
// Handler: gatewayTokenAuth edge cases
// ============================================================================

func TestGatewayTokenAuth_ExpiredSession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ml := newMockLedger()
	svc := newTestServiceWithLogger(ml, &mockRegistry{})

	session, _ := svc.CreateSession(context.Background(), "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "1.00",
	})

	// Expire the session
	s, _ := svc.store.GetSession(context.Background(), session.ID)
	s.ExpiresAt = time.Now().Add(-time.Hour)
	svc.store.UpdateSession(context.Background(), s)

	handler := NewHandler(svc)
	r := gin.New()
	r.POST("/test", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xbuyer")
		c.Next()
	}, handler.gatewayTokenAuth(), func(c *gin.Context) {
		c.Status(200)
	})

	req := httptest.NewRequest("POST", "/test", nil)
	req.Header.Set("X-Gateway-Token", session.ID)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for expired session, got %d", w.Code)
	}
}

// ============================================================================
// Handler: CreateSession error paths
// ============================================================================

func TestHandler_CreateSession_ValidationError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newTestServiceWithLogger(newMockLedger(), &mockRegistry{})
	handler := NewHandler(svc)

	r := gin.New()
	r.POST("/sessions", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xbuyer")
		c.Next()
	}, handler.CreateSession)

	body := `{"maxTotal":"bad","maxPerRequest":"1.00"}`
	req := httptest.NewRequest("POST", "/sessions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for validation error, got %d", w.Code)
	}
}

// ============================================================================
// Handler: Proxy error mapping
// ============================================================================

func TestHandler_Proxy_MissingServiceType(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newTestServiceWithLogger(newMockLedger(), &mockRegistry{})

	session, _ := svc.CreateSession(context.Background(), "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "1.00",
	})

	handler := NewHandler(svc)
	r := gin.New()
	r.POST("/proxy", func(c *gin.Context) {
		c.Set("gatewaySessionID", session.ID)
		c.Next()
	}, handler.Proxy)

	// Empty body (missing serviceType)
	req := httptest.NewRequest("POST", "/proxy", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid body, got %d", w.Code)
	}
}

// ============================================================================
// Handler: Pipeline error
// ============================================================================

func TestHandler_Pipeline_InvalidBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newTestServiceWithLogger(newMockLedger(), &mockRegistry{})

	session, _ := svc.CreateSession(context.Background(), "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "1.00",
	})

	handler := NewHandler(svc)
	r := gin.New()
	r.POST("/pipeline", func(c *gin.Context) {
		c.Set("gatewaySessionID", session.ID)
		c.Next()
	}, handler.Pipeline)

	req := httptest.NewRequest("POST", "/pipeline", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid pipeline body, got %d", w.Code)
	}
}

// ============================================================================
// Handler: DryRun error paths
// ============================================================================

func TestHandler_DryRun_WrongOwner(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ml := newMockLedger()
	svc := newTestServiceWithLogger(ml, &mockRegistry{})

	session, _ := svc.CreateSession(context.Background(), "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "1.00",
	})

	handler := NewHandler(svc)
	r := gin.New()
	r.POST("/dry-run/:id", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xstranger")
		c.Next()
	}, handler.DryRun)

	body := `{"serviceType":"test"}`
	req := httptest.NewRequest("POST", "/dry-run/"+session.ID, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 for wrong owner, got %d", w.Code)
	}
}

func TestHandler_DryRun_InvalidBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ml := newMockLedger()
	svc := newTestServiceWithLogger(ml, &mockRegistry{})

	session, _ := svc.CreateSession(context.Background(), "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "1.00",
	})

	handler := NewHandler(svc)
	r := gin.New()
	r.POST("/dry-run/:id", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xbuyer")
		c.Next()
	}, handler.DryRun)

	req := httptest.NewRequest("POST", "/dry-run/"+session.ID, strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid body, got %d", w.Code)
	}
}

// ============================================================================
// Handler: ListLogs error paths
// ============================================================================

func TestHandler_ListLogs_WrongOwner(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ml := newMockLedger()
	svc := newTestServiceWithLogger(ml, &mockRegistry{})

	session, _ := svc.CreateSession(context.Background(), "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "1.00",
	})

	handler := NewHandler(svc)
	r := gin.New()
	r.GET("/logs/:id", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xstranger")
		c.Next()
	}, handler.ListLogs)

	req := httptest.NewRequest("GET", "/logs/"+session.ID, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 for wrong owner, got %d", w.Code)
	}
}

func TestHandler_ListLogs_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newTestServiceWithLogger(newMockLedger(), &mockRegistry{})

	handler := NewHandler(svc)
	r := gin.New()
	r.GET("/logs/:id", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xbuyer")
		c.Next()
	}, handler.ListLogs)

	req := httptest.NewRequest("GET", "/logs/nonexistent", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for not found, got %d", w.Code)
	}
}

func TestHandler_ListLogs_WithLimitParam(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ml := newMockLedger()
	svc := newTestServiceWithLogger(ml, &mockRegistry{})

	session, _ := svc.CreateSession(context.Background(), "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "1.00",
	})

	handler := NewHandler(svc)
	r := gin.New()
	r.GET("/logs/:id", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xbuyer")
		c.Next()
	}, handler.ListLogs)

	req := httptest.NewRequest("GET", "/logs/"+session.ID+"?limit=5", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

// ============================================================================
// Handler: SingleCall
// ============================================================================

func TestHandler_SingleCall_BadBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newTestServiceWithLogger(newMockLedger(), &mockRegistry{})

	handler := NewHandler(svc)
	r := gin.New()
	r.POST("/call", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xbuyer")
		c.Next()
	}, handler.SingleCall)

	req := httptest.NewRequest("POST", "/call", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for bad body, got %d", w.Code)
	}
}

// ============================================================================
// Handler: ListSessions with limit and cursor
// ============================================================================

func TestHandler_ListSessions_WithLimitParam(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ml := newMockLedger()
	svc := newTestServiceWithLogger(ml, &mockRegistry{})

	svc.CreateSession(context.Background(), "0xbuyer", "", CreateSessionRequest{
		MaxTotal: "10.00", MaxPerRequest: "1.00",
	})

	handler := NewHandler(svc)
	r := gin.New()
	r.GET("/sessions", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xbuyer")
		c.Next()
	}, handler.ListSessions)

	req := httptest.NewRequest("GET", "/sessions?limit=5", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["has_more"] != false {
		t.Error("expected has_more=false")
	}
}

func TestHandler_ListSessions_CapAt200(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newTestServiceWithLogger(newMockLedger(), &mockRegistry{})

	handler := NewHandler(svc)
	r := gin.New()
	r.GET("/sessions", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xbuyer")
		c.Next()
	}, handler.ListSessions)

	req := httptest.NewRequest("GET", "/sessions?limit=500", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

// ============================================================================
// Handler: CloseSession error paths
// ============================================================================

func TestHandler_CloseSession_WrongOwner(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ml := newMockLedger()
	svc := newTestServiceWithLogger(ml, &mockRegistry{})

	session, _ := svc.CreateSession(context.Background(), "0xbuyer", "", CreateSessionRequest{
		MaxTotal: "10.00", MaxPerRequest: "1.00",
	})

	handler := NewHandler(svc)
	r := gin.New()
	r.DELETE("/sessions/:id", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xstranger")
		c.Next()
	}, handler.CloseSession)

	req := httptest.NewRequest("DELETE", "/sessions/"+session.ID, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}
}

// ============================================================================
// Service: SetupRouting, HealthMonitor, HealthAwareRouter accessors
// ============================================================================

func TestService_SetupRouting(t *testing.T) {
	svc := newTestServiceWithLogger(newMockLedger(), &mockRegistry{})
	got := svc.SetupRouting(testLogger())
	if got != svc {
		t.Error("SetupRouting should return same service")
	}
	if svc.HealthMonitor() == nil {
		t.Error("expected non-nil health monitor after SetupRouting")
	}
	if svc.HealthAwareRouter() == nil {
		t.Error("expected non-nil health-aware router after SetupRouting")
	}
}

// ============================================================================
// Routing metrics helpers (exercise but no assertion needed)
// ============================================================================

func TestRoutingMetricHelpers(t *testing.T) {
	// These just exercise the metric recording functions without panicking
	updateProviderHealthMetric("test-provider", HealthHealthy)
	updateProviderHealthMetric("test-provider", HealthDegraded)
	updateProviderHealthMetric("test-provider", HealthUnhealthy)
	updateProviderHealthMetric("test-provider", HealthUnknown)
	recordRerouteMetric("from", "to")
	recordRacerExpansionMetric()
	recordRequestOutcomeMetric(OutcomeSucceeded)
	recordRequestOutcomeMetric(OutcomeRerouted)
	recordRequestOutcomeMetric(OutcomeEscalated)
}

// ============================================================================
// Forwarder coverage
// ============================================================================

func TestForwarder_NonJSONResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(200)
		w.Write([]byte("not json"))
	}))
	defer server.Close()

	fwd := NewForwarder(5 * time.Second).WithAllowLocalEndpoints()
	resp, err := fwd.Forward(context.Background(), ForwardRequest{
		Endpoint: server.URL,
		Params:   map[string]interface{}{"key": "val"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Body["raw"] != "not json" {
		t.Errorf("expected raw body, got %v", resp.Body)
	}
}

func TestForwarder_4xxError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		w.Write([]byte(`{"error":"bad request"}`))
	}))
	defer server.Close()

	fwd := NewForwarder(5 * time.Second).WithAllowLocalEndpoints()
	_, err := fwd.Forward(context.Background(), ForwardRequest{
		Endpoint: server.URL,
		Params:   map[string]interface{}{},
	})
	if err == nil {
		t.Fatal("expected error for 400 response")
	}
	fe, ok := err.(*ForwardError)
	if !ok {
		t.Fatalf("expected ForwardError, got %T", err)
	}
	if fe.StatusCode != 400 {
		t.Errorf("expected 400, got %d", fe.StatusCode)
	}
	if fe.IsTransient() {
		t.Error("400 should not be transient")
	}
}

func TestForwarder_PaymentHeaders(t *testing.T) {
	var gotHeaders http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(map[string]string{"ok": "true"})
	}))
	defer server.Close()

	fwd := NewForwarder(5 * time.Second).WithAllowLocalEndpoints()
	_, err := fwd.Forward(context.Background(), ForwardRequest{
		Endpoint:  server.URL,
		Params:    map[string]interface{}{},
		FromAddr:  "0xbuyer",
		Amount:    "1.000000",
		Reference: "ref123",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotHeaders.Get("X-Payment-Amount") != "1.000000" {
		t.Errorf("missing X-Payment-Amount header")
	}
	if gotHeaders.Get("X-Payment-From") != "0xbuyer" {
		t.Errorf("missing X-Payment-From header")
	}
	if gotHeaders.Get("X-Payment-Ref") != "ref123" {
		t.Errorf("missing X-Payment-Ref header")
	}
}

func TestForwarder_EmptyResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer server.Close()

	fwd := NewForwarder(5 * time.Second).WithAllowLocalEndpoints()
	resp, err := fwd.Forward(context.Background(), ForwardRequest{
		Endpoint: server.URL,
		Params:   map[string]interface{}{},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Body) > 0 {
		t.Errorf("expected nil/empty body for empty response, got %v", resp.Body)
	}
}

// ============================================================================
// Handler: RegisterRoutes coverage (no panic)
// ============================================================================

func TestHandler_RegisterRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newTestService(newMockLedger(), &mockRegistry{})
	handler := NewHandler(svc)

	r := gin.New()
	g := r.Group("/v1")
	handler.RegisterProtectedRoutes(g)
	handler.RegisterProxyRoute(g)

	// Just verify routes are registered without panic
	routes := r.Routes()
	if len(routes) == 0 {
		t.Error("expected routes to be registered")
	}
}

// ============================================================================
// ListOption / WithCursor coverage
// ============================================================================

func TestListOption_WithCursor(t *testing.T) {
	// Invalid cursor should not panic
	opt := WithCursor("invalid-base64")
	var o listOpts
	opt(&o)
	if o.cursor != nil {
		t.Error("expected nil cursor for invalid input")
	}
}

func TestApplyListOpts_Empty(t *testing.T) {
	o := applyListOpts(nil)
	if o.cursor != nil {
		t.Error("expected nil cursor for empty opts")
	}
}

// ============================================================================
// Proxy handler: SingleCall with MoneyError
// ============================================================================

func TestHandler_SingleCall_NoService(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newTestServiceWithLogger(newMockLedger(), &mockRegistry{services: nil})

	handler := NewHandler(svc)
	r := gin.New()
	r.POST("/call", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xbuyer")
		c.Next()
	}, handler.SingleCall)

	body := `{"maxPrice":"10.00","serviceType":"test"}`
	req := httptest.NewRequest("POST", "/call", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for no service, got %d", w.Code)
	}
}

// ============================================================================
// Handler: GetSession internal error path
// ============================================================================

func TestHandler_GetSession_InternalError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newTestServiceWithLogger(newMockLedger(), &mockRegistry{})

	handler := NewHandler(svc)
	r := gin.New()
	r.GET("/sessions/:id", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xbuyer")
		c.Next()
	}, handler.GetSession)

	// Session doesn't exist -> returns not found
	req := httptest.NewRequest("GET", "/sessions/nonexistent", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

// ============================================================================
// Handler: Proxy various error codes
// ============================================================================

func TestHandler_Pipeline_StepFails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ml := newMockLedger()
	svc := newTestServiceWithLogger(ml, &mockRegistry{services: nil})

	session, _ := svc.CreateSession(context.Background(), "0xbuyer", "", CreateSessionRequest{
		MaxTotal: "10.00", MaxPerRequest: "1.00",
	})

	handler := NewHandler(svc)
	r := gin.New()
	r.POST("/pipeline", func(c *gin.Context) {
		c.Set("gatewaySessionID", session.ID)
		c.Next()
	}, handler.Pipeline)

	body, _ := json.Marshal(PipelineRequest{
		Steps: []PipelineStep{{ServiceType: "test"}},
	})
	req := httptest.NewRequest("POST", "/pipeline", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Should fail because no services available
	if w.Code == http.StatusOK {
		t.Error("expected error status for failed pipeline")
	}
}

// --- merged from service_coverage_test.go ---

// --- Additional mock types for coverage (named to avoid conflicts) ---

type mockTenantSettings struct {
	takeRateBPS      int
	takeRateErr      error
	status           string
	statusErr        error
	stripeCustomerID string
	stripeCustErr    error
}

func (m *mockTenantSettings) GetTakeRateBPS(_ context.Context, tenantID string) (int, error) {
	return m.takeRateBPS, m.takeRateErr
}

func (m *mockTenantSettings) GetTenantStatus(_ context.Context, tenantID string) (string, error) {
	return m.status, m.statusErr
}

func (m *mockTenantSettings) GetStripeCustomerID(_ context.Context, tenantID string) (string, error) {
	return m.stripeCustomerID, m.stripeCustErr
}

type mockWebhookEmitter struct {
	sessionCreated int
	sessionClosed  int
	proxySuccess   int
	settleFailed   int
}

func (m *mockWebhookEmitter) EmitSessionCreated(agentAddr, sessionID, maxTotal string) {
	m.sessionCreated++
}
func (m *mockWebhookEmitter) EmitSessionClosed(agentAddr, sessionID, totalSpent, status string) {
	m.sessionClosed++
}
func (m *mockWebhookEmitter) EmitProxySuccess(agentAddr, sessionID, serviceUsed, amountPaid string) {
	m.proxySuccess++
}
func (m *mockWebhookEmitter) EmitSettlementFailed(agentAddr, sessionID, sellerAddr, amount string) {
	m.settleFailed++
}

type mockUsageMeter struct {
	requests int
	volumes  int
}

func (m *mockUsageMeter) RecordRequest(tenantID, customerID string) { m.requests++ }
func (m *mockUsageMeter) RecordVolume(tenantID, customerID string, microUSDC int64) {
	m.volumes++
}

type mockRevenue struct {
	accumulated int
	err         error
}

func (m *mockRevenue) AccumulateRevenue(_ context.Context, agentAddr, amount, txRef string) error {
	m.accumulated++
	return m.err
}

type mockForensics struct {
	ingested int
}

func (m *mockForensics) IngestSpend(_ context.Context, agentAddr, counterparty string, amountFloat float64, serviceType string) error {
	m.ingested++
	return nil
}

type mockChargeback struct {
	recorded int
}

func (m *mockChargeback) RecordGatewaySpend(_ context.Context, tenantID, agentAddr, amount, serviceType, sessionID string) error {
	m.recorded++
	return nil
}

type mockBudgetPreFlight struct {
	err error
}

func (m *mockBudgetPreFlight) CheckBudget(_ context.Context, tenantID, serviceType, estimatedAmount string) error {
	return m.err
}

type mockEventPublisher struct {
	published int
	err       error
}

func (m *mockEventPublisher) PublishSettlement(_ context.Context, sessionID, tenantID, buyerAddr, sellerAddr, amount, fee, serviceType, serviceID, reference string, latencyMs int64) error {
	m.published++
	return m.err
}

type mockIncentiveProvider struct {
	adjustedBPS int
	err         error
}

func (m *mockIncentiveProvider) AdjustFeeBPS(_ context.Context, tier string, baseBPS int) (int, error) {
	if m.err != nil {
		return 0, m.err
	}
	return m.adjustedBPS, nil
}

type mockReceiptIssuer struct {
	issued int
}

func (m *mockReceiptIssuer) IssueReceipt(_ context.Context, path, reference, from, to, amount, serviceID, status, metadata string) error {
	m.issued++
	return nil
}

// --- Idempotency Cache tests ---

func TestIdempotencyCache_Key(t *testing.T) {
	c := newIdempotencyCache(time.Minute)
	k := c.key("session1", "req1")
	if k != "session1:req1" {
		t.Errorf("expected session1:req1, got %s", k)
	}
}

func TestIdempotencyCache_Complete(t *testing.T) {
	c := newIdempotencyCache(time.Minute)
	ctx := context.Background()

	_, _, found := c.GetOrReserve(ctx, "s1", "k1")
	if found {
		t.Fatal("expected not found on first call")
	}

	result := &ProxyResult{AmountPaid: "1.00"}
	c.Complete("s1", "k1", result)

	got, err, found := c.GetOrReserve(ctx, "s1", "k1")
	if !found {
		t.Fatal("expected found after complete")
	}
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if got.AmountPaid != "1.00" {
		t.Errorf("expected 1.00, got %s", got.AmountPaid)
	}
}

func TestIdempotencyCache_Cancel(t *testing.T) {
	c := newIdempotencyCache(time.Minute)
	ctx := context.Background()

	_, _, found := c.GetOrReserve(ctx, "s1", "k1")
	if found {
		t.Fatal("expected not found on first call")
	}

	c.Cancel("s1", "k1")

	// Should be able to reserve again after cancel
	_, _, found = c.GetOrReserve(ctx, "s1", "k1")
	if found {
		t.Fatal("expected not found after cancel")
	}
}

func TestIdempotencyCache_SweepSkipsInFlight(t *testing.T) {
	c := newIdempotencyCache(1 * time.Millisecond)
	ctx := context.Background()

	// Reserve but don't complete
	_, _, _ = c.GetOrReserve(ctx, "s1", "k1")

	time.Sleep(5 * time.Millisecond)
	removed := c.Sweep()
	if removed != 0 {
		t.Errorf("expected 0 removed (in-flight), got %d", removed)
	}
	if c.Size() != 1 {
		t.Errorf("expected 1 entry (in-flight), got %d", c.Size())
	}
}

func TestIdempotencyCache_MaxSize(t *testing.T) {
	c := newIdempotencyCache(time.Minute)
	c.maxSize = 2
	ctx := context.Background()

	// Fill up
	_, _, _ = c.GetOrReserve(ctx, "s1", "k1")
	c.Complete("s1", "k1", &ProxyResult{})
	_, _, _ = c.GetOrReserve(ctx, "s1", "k2")
	c.Complete("s1", "k2", &ProxyResult{})

	// Third should not be found (cache full) but also not cached
	_, _, found := c.GetOrReserve(ctx, "s1", "k3")
	if found {
		t.Fatal("expected not found when cache at capacity")
	}
}

func TestIdempotencyCache_ExpiredEntry(t *testing.T) {
	c := newIdempotencyCache(1 * time.Millisecond)
	ctx := context.Background()

	_, _, _ = c.GetOrReserve(ctx, "s1", "k1")
	c.Complete("s1", "k1", &ProxyResult{AmountPaid: "old"})

	time.Sleep(5 * time.Millisecond)

	// Expired entry should be deleted on access, allowing re-reserve
	_, _, found := c.GetOrReserve(ctx, "s1", "k1")
	if found {
		t.Fatal("expected not found for expired entry")
	}
}

func TestIdempotencyCache_ContextCancelled(t *testing.T) {
	c := newIdempotencyCache(time.Minute)

	// Reserve a key (in-flight)
	_, _, _ = c.GetOrReserve(context.Background(), "s1", "k1")

	// Try to get the same key with a cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err, found := c.GetOrReserve(ctx, "s1", "k1")
	if !found {
		t.Fatal("expected found (blocked then cancelled)")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

// --- Rate Limiter tests ---

func TestRateLimiter_AllowBasic(t *testing.T) {
	rl := newRateLimiter()

	if !rl.allow("s1") {
		t.Error("expected first request allowed")
	}
	if rl.size() != 1 {
		t.Errorf("expected 1 entry, got %d", rl.size())
	}
}

func TestRateLimiter_Exceeds(t *testing.T) {
	rl := newRateLimiter()
	rl.setLimit("s1", 3)

	for i := 0; i < 3; i++ {
		if !rl.allow("s1") {
			t.Errorf("request %d should be allowed", i+1)
		}
	}
	if rl.allow("s1") {
		t.Error("4th request should be rate limited")
	}
}

func TestRateLimiter_Remove(t *testing.T) {
	rl := newRateLimiter()
	rl.allow("s1")
	rl.remove("s1")
	if rl.size() != 0 {
		t.Errorf("expected 0 entries after remove, got %d", rl.size())
	}
}

func TestRateLimiter_SweepCleansCustomLimit(t *testing.T) {
	rl := newRateLimiter()
	rl.window = 1 * time.Millisecond

	rl.setLimit("s1", 200) // non-default limit
	rl.allow("s1")

	time.Sleep(5 * time.Millisecond)

	// Custom-limit entries are now swept like any other entry to prevent
	// memory leaks. The custom limit is restored by setLimit when reused.
	removed := rl.sweep()
	if removed != 1 {
		t.Errorf("expected 1 removed (stale entry), got %d", removed)
	}
	if rl.size() != 0 {
		t.Errorf("expected 0 entries after sweep, got %d", rl.size())
	}
}

// --- Service With* method tests ---

func TestService_WithMethods(t *testing.T) {
	svc := newTestService(newMockLedger(), &mockRegistry{})

	if got := svc.WithRecorder(&mockGatewayRecorder{}); got != svc {
		t.Error("WithRecorder should return same service")
	}
	if got := svc.WithReceiptIssuer(&mockReceiptIssuer{}); got != svc {
		t.Error("WithReceiptIssuer should return same service")
	}
	if got := svc.WithTenantSettings(&mockTenantSettings{}); got != svc {
		t.Error("WithTenantSettings should return same service")
	}
	if got := svc.WithPlatformAddress("0xPLATFORM"); got != svc {
		t.Error("WithPlatformAddress should return same service")
	}
	if got := svc.WithWebhookEmitter(&mockWebhookEmitter{}); got != svc {
		t.Error("WithWebhookEmitter should return same service")
	}
	if got := svc.WithUsageMeter(&mockUsageMeter{}); got != svc {
		t.Error("WithUsageMeter should return same service")
	}
	if got := svc.WithRevenueAccumulator(&mockRevenue{}); got != svc {
		t.Error("WithRevenueAccumulator should return same service")
	}
	if got := svc.WithForensics(&mockForensics{}); got != svc {
		t.Error("WithForensics should return same service")
	}
	if got := svc.WithChargeback(&mockChargeback{}); got != svc {
		t.Error("WithChargeback should return same service")
	}
	if got := svc.WithBudgetPreFlight(&mockBudgetPreFlight{}); got != svc {
		t.Error("WithBudgetPreFlight should return same service")
	}
	if got := svc.WithEventBus(&mockEventPublisher{}); got != svc {
		t.Error("WithEventBus should return same service")
	}
	if got := svc.WithIncentives(&mockIncentiveProvider{}); got != svc {
		t.Error("WithIncentives should return same service")
	}
	if got := svc.WithIntelligence(newMockIntelligence()); got != svc {
		t.Error("WithIntelligence should return same service")
	}
	if svc.CircuitBreaker() != nil {
		t.Error("expected nil circuit breaker")
	}
}

// --- PendingSpend tests ---

func TestPendingSpend(t *testing.T) {
	svc := newTestService(newMockLedger(), &mockRegistry{})

	got := svc.getPendingSpend("session1")
	if got.Sign() != 0 {
		t.Errorf("expected 0, got %s", got.String())
	}

	svc.addPendingSpend("session1", big.NewInt(500000))
	got = svc.getPendingSpend("session1")
	if got.Cmp(big.NewInt(500000)) != 0 {
		t.Errorf("expected 500000, got %s", got.String())
	}

	svc.addPendingSpend("session1", big.NewInt(300000))
	got = svc.getPendingSpend("session1")
	if got.Cmp(big.NewInt(800000)) != 0 {
		t.Errorf("expected 800000, got %s", got.String())
	}

	svc.removePendingSpend("session1", big.NewInt(300000))
	got = svc.getPendingSpend("session1")
	if got.Cmp(big.NewInt(500000)) != 0 {
		t.Errorf("expected 500000, got %s", got.String())
	}

	svc.removePendingSpend("session1", big.NewInt(500000))
	got = svc.getPendingSpend("session1")
	if got.Sign() != 0 {
		t.Errorf("expected 0 after removing all, got %s", got.String())
	}
}

func TestPendingSpend_RemoveMoreThanExists(t *testing.T) {
	svc := newTestService(newMockLedger(), &mockRegistry{})
	svc.addPendingSpend("s1", big.NewInt(100))
	svc.removePendingSpend("s1", big.NewInt(200))

	got := svc.getPendingSpend("s1")
	if got.Sign() != 0 {
		t.Errorf("expected 0 when removing more than exists, got %s", got.String())
	}
}

// --- SweepIdempotencyCache / SweepRateLimiter ---

func TestService_SweepIdempotencyCache(t *testing.T) {
	svc := newTestService(newMockLedger(), &mockRegistry{})
	svc.idemCache = newIdempotencyCache(1 * time.Millisecond)

	ctx := context.Background()
	svc.idemCache.GetOrReserve(ctx, "s1", "k1")
	svc.idemCache.Complete("s1", "k1", &ProxyResult{})

	time.Sleep(5 * time.Millisecond)
	removed := svc.SweepIdempotencyCache()
	if removed != 1 {
		t.Errorf("expected 1 swept, got %d", removed)
	}
}

func TestService_SweepRateLimiter(t *testing.T) {
	svc := newTestService(newMockLedger(), &mockRegistry{})
	svc.rateLimit.window = 1 * time.Millisecond
	svc.rateLimit.allow("s1")

	time.Sleep(5 * time.Millisecond)
	removed := svc.SweepRateLimiter()
	if removed != 1 {
		t.Errorf("expected 1 swept, got %d", removed)
	}
}

// --- CreateSession edge cases ---

func TestCreateSession_WarnAtPercent(t *testing.T) {
	ml := newMockLedger()
	svc := newTestService(ml, &mockRegistry{})

	session, err := svc.CreateSession(context.Background(), "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "1.00",
		WarnAtPercent: 20,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if session.WarnAtPercent != 20 {
		t.Errorf("expected warnAtPercent 20, got %d", session.WarnAtPercent)
	}

	session, err = svc.CreateSession(context.Background(), "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "1.00",
		WarnAtPercent: -5,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if session.WarnAtPercent != 0 {
		t.Errorf("expected warnAtPercent 0 for negative, got %d", session.WarnAtPercent)
	}
}

func TestCreateSession_TenantSuspended(t *testing.T) {
	ml := newMockLedger()
	svc := newTestService(ml, &mockRegistry{})
	svc.WithTenantSettings(&mockTenantSettings{status: "suspended"})

	_, err := svc.CreateSession(context.Background(), "0xbuyer", "tenant1", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "1.00",
	})
	if !errors.Is(err, ErrTenantSuspended) {
		t.Errorf("expected ErrTenantSuspended, got %v", err)
	}
}

func TestCreateSession_AllowedTypes_TooMany(t *testing.T) {
	ml := newMockLedger()
	svc := newTestService(ml, &mockRegistry{})

	types := make([]string, 101)
	for i := range types {
		types[i] = fmt.Sprintf("type%d", i)
	}

	_, err := svc.CreateSession(context.Background(), "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "1.00",
		AllowedTypes:  types,
	})
	if err == nil {
		t.Fatal("expected error for >100 allowed types")
	}
}

func TestCreateSession_AllowedTypes_InvalidFormat(t *testing.T) {
	ml := newMockLedger()
	svc := newTestService(ml, &mockRegistry{})

	_, err := svc.CreateSession(context.Background(), "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "1.00",
		AllowedTypes:  []string{"valid-type", "invalid type with spaces!"},
	})
	if err == nil {
		t.Fatal("expected error for invalid service type format")
	}
}

func TestCreateSession_PolicyUnavailable(t *testing.T) {
	ml := newMockLedger()
	svc := newTestServiceWithLogger(ml, &mockRegistry{})
	svc.policyEvaluator = &mockPolicyEvaluator{
		decision: nil,
		err:      fmt.Errorf("db down"),
	}

	_, err := svc.CreateSession(context.Background(), "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "1.00",
	})
	if !errors.Is(err, ErrPolicyUnavailable) {
		t.Errorf("expected ErrPolicyUnavailable, got %v", err)
	}
}

// --- DryRun additional tests ---

func TestDryRun_ExpiredSession(t *testing.T) {
	ml := newMockLedger()
	svc := newTestServiceWithLogger(ml, &mockRegistry{})

	session, _ := svc.CreateSession(context.Background(), "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "1.00",
	})

	// Expire it
	s, _ := svc.store.GetSession(context.Background(), session.ID)
	s.ExpiresAt = time.Now().Add(-time.Hour)
	svc.store.UpdateSession(context.Background(), s)

	result, err := svc.DryRun(context.Background(), session.ID, ProxyRequest{ServiceType: "test"})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if result.DenyReason == "" {
		t.Error("expected deny reason for expired session")
	}
}

func TestDryRun_InvalidServiceType(t *testing.T) {
	ml := newMockLedger()
	svc := newTestService(ml, &mockRegistry{})

	session, _ := svc.CreateSession(context.Background(), "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "1.00",
	})

	_, err := svc.DryRun(context.Background(), session.ID, ProxyRequest{ServiceType: "invalid type!"})
	if err == nil {
		t.Fatal("expected error for invalid service type")
	}
}

// --- SingleCall additional tests ---

func TestSingleCall_InvalidAmount(t *testing.T) {
	ml := newMockLedger()
	svc := newTestServiceWithLogger(ml, &mockRegistry{})

	_, err := svc.SingleCall(context.Background(), "0xbuyer", "", SingleCallRequest{
		MaxPrice:    "invalid",
		ServiceType: "test",
	})
	if err == nil {
		t.Fatal("expected error for invalid amount")
	}
}

// --- ListSessions / ListByStatus / ListLogs ---

func TestListSessions_DefaultLimit(t *testing.T) {
	ml := newMockLedger()
	svc := newTestService(ml, &mockRegistry{})

	sessions, err := svc.ListSessions(context.Background(), "0xbuyer", 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	_ = sessions
}

func TestListByStatus(t *testing.T) {
	ml := newMockLedger()
	svc := newTestService(ml, &mockRegistry{})

	svc.CreateSession(context.Background(), "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "1.00",
	})

	sessions, err := svc.ListByStatus(context.Background(), StatusActive, 0)
	if err != nil {
		t.Fatalf("list by status: %v", err)
	}
	if len(sessions) != 1 {
		t.Errorf("expected 1 active session, got %d", len(sessions))
	}
}

func TestListLogs_DefaultLimit(t *testing.T) {
	ml := newMockLedger()
	svc := newTestService(ml, &mockRegistry{})

	logs, err := svc.ListLogs(context.Background(), "some-session", 0)
	if err != nil {
		t.Fatalf("list logs: %v", err)
	}
	_ = logs
}

// --- ComputeFee tests ---

func TestComputeFee_NoTenant(t *testing.T) {
	svc := newTestService(newMockLedger(), &mockRegistry{})
	price := big.NewInt(1000000)

	seller, fee := svc.computeFee(context.Background(), "", price)
	if fee != "0.000000" {
		t.Errorf("expected 0 fee without tenant, got %s", fee)
	}
	if seller != "1.000000" {
		t.Errorf("expected 1.000000 seller, got %s", seller)
	}
}

func TestComputeFee_WithTenantAndFee(t *testing.T) {
	svc := newTestServiceWithLogger(newMockLedger(), &mockRegistry{})
	svc.WithTenantSettings(&mockTenantSettings{takeRateBPS: 100})
	svc.WithPlatformAddress("0xplatform")

	price := big.NewInt(10000000) // 10 USDC
	seller, fee := svc.computeFee(context.Background(), "tenant1", price)
	if fee == "0.000000" {
		t.Error("expected non-zero fee")
	}
	_ = seller
}

func TestComputeFee_WithIncentives(t *testing.T) {
	svc := newTestServiceWithLogger(newMockLedger(), &mockRegistry{})
	svc.WithTenantSettings(&mockTenantSettings{takeRateBPS: 100})
	svc.WithPlatformAddress("0xplatform")
	svc.WithIncentives(&mockIncentiveProvider{adjustedBPS: 50})

	price := big.NewInt(10000000) // 10 USDC
	_, fee1 := svc.computeFee(context.Background(), "tenant1", price, "elite")
	_, fee2 := svc.computeFee(context.Background(), "tenant1", price)

	_ = fee1
	_ = fee2
}

// --- MoneyError tests ---

func TestMoneyError(t *testing.T) {
	inner := fmt.Errorf("ledger failure")
	me := &MoneyError{
		Err:         inner,
		FundsStatus: "held_pending",
		Recovery:    "Contact support",
		Amount:      "10.00",
		Reference:   "ref123",
	}

	if me.Error() != "ledger failure" {
		t.Errorf("expected 'ledger failure', got %s", me.Error())
	}
	if me.Unwrap() != inner {
		t.Error("Unwrap should return inner error")
	}
}

func TestMoneyFields(t *testing.T) {
	fields := moneyFields(fmt.Errorf("plain error"))
	if fields != nil {
		t.Error("expected nil for non-MoneyError")
	}

	me := &MoneyError{
		Err:         fmt.Errorf("test"),
		FundsStatus: "no_change",
		Recovery:    "Retry",
		Amount:      "5.00",
		Reference:   "ref1",
	}
	fields = moneyFields(me)
	if fields == nil {
		t.Fatal("expected non-nil for MoneyError")
	}
	if fields["funds_status"] != "no_change" {
		t.Errorf("expected no_change, got %v", fields["funds_status"])
	}
}

// --- Session model tests ---

func TestSession_Remaining(t *testing.T) {
	s := &Session{MaxTotal: "10.00", TotalSpent: "3.50"}
	if s.Remaining() != "6.500000" {
		t.Errorf("expected 6.500000, got %s", s.Remaining())
	}
}

func TestSession_Remaining_NilValues(t *testing.T) {
	s := &Session{MaxTotal: "", TotalSpent: ""}
	r := s.Remaining()
	if r != "0.000000" {
		t.Errorf("expected 0.000000, got %s", r)
	}
}

func TestSession_IsTypeAllowed(t *testing.T) {
	s := &Session{AllowedTypes: []string{"translation", "inference"}}
	s.BuildAllowedTypesSet()

	if !s.IsTypeAllowed("translation") {
		t.Error("expected translation allowed")
	}
	if s.IsTypeAllowed("image") {
		t.Error("expected image not allowed")
	}
}

func TestSession_IsTypeAllowed_Empty(t *testing.T) {
	s := &Session{}
	s.BuildAllowedTypesSet()

	if !s.IsTypeAllowed("anything") {
		t.Error("expected all types allowed when AllowedTypes is empty")
	}
}

// --- Resolver tests ---

func TestResolver_BestValue(t *testing.T) {
	reg := &mockRegistry{
		services: []ServiceCandidate{
			{AgentAddress: "0xcheap", Price: "0.10", Endpoint: "http://a", ReputationScore: 10},
			{AgentAddress: "0xgoodval", Price: "1.00", Endpoint: "http://b", ReputationScore: 90},
		},
	}
	resolver := NewResolver(reg)

	candidates, err := resolver.Resolve(context.Background(), ProxyRequest{ServiceType: "test"}, "best_value", "2.00")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	// best_value uses Cobb-Douglas: rep^0.65 × (1/price)^0.35.
	// Just verify the strategy runs without error and returns both candidates.
	if len(candidates) != 2 {
		t.Errorf("expected 2 candidates, got %d", len(candidates))
	}
}

func TestResolver_TraceRank(t *testing.T) {
	reg := &mockRegistry{
		services: []ServiceCandidate{
			{AgentAddress: "0xlow", Price: "0.10", Endpoint: "http://a", TraceRankScore: 10},
			{AgentAddress: "0xhigh", Price: "0.50", Endpoint: "http://b", TraceRankScore: 90},
		},
	}
	resolver := NewResolver(reg)

	candidates, err := resolver.Resolve(context.Background(), ProxyRequest{ServiceType: "test"}, "tracerank", "2.00")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if candidates[0].AgentAddress != "0xhigh" {
		t.Errorf("expected 0xhigh first for tracerank, got %s", candidates[0].AgentAddress)
	}
}

func TestScoreTier(t *testing.T) {
	tests := []struct {
		score float64
		want  string
	}{
		{90, "elite"},
		{80, "elite"},
		{70, "trusted"},
		{60, "trusted"},
		{50, "established"},
		{40, "established"},
		{30, "emerging"},
		{20, "emerging"},
		{10, "new"},
		{0, "new"},
	}
	for _, tt := range tests {
		got := scoreTier(tt.score)
		if got != tt.want {
			t.Errorf("scoreTier(%.0f) = %s, want %s", tt.score, got, tt.want)
		}
	}
}

// --- Handler tests ---

func TestHandler_CreateSession(t *testing.T) {
	gin.SetMode(gin.TestMode)

	ml := newMockLedger()
	svc := newTestServiceWithLogger(ml, &mockRegistry{})
	handler := NewHandler(svc)

	body := `{"maxTotal":"10.00","maxPerRequest":"1.00"}`
	req := httptest.NewRequest("POST", "/v1/gateway/sessions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r := gin.New()
	r.POST("/v1/gateway/sessions", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xbuyer")
		c.Next()
	}, handler.CreateSession)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_CreateSession_InvalidBody(t *testing.T) {
	gin.SetMode(gin.TestMode)

	svc := newTestServiceWithLogger(newMockLedger(), &mockRegistry{})
	handler := NewHandler(svc)

	req := httptest.NewRequest("POST", "/v1/gateway/sessions", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r := gin.New()
	r.POST("/v1/gateway/sessions", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xbuyer")
		c.Next()
	}, handler.CreateSession)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandler_GetSession(t *testing.T) {
	gin.SetMode(gin.TestMode)

	svc := newTestServiceWithLogger(newMockLedger(), &mockRegistry{})
	handler := NewHandler(svc)

	session, _ := svc.CreateSession(context.Background(), "0xbuyer", "", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "1.00",
	})

	req := httptest.NewRequest("GET", "/v1/gateway/sessions/"+session.ID, nil)
	w := httptest.NewRecorder()

	r := gin.New()
	r.GET("/v1/gateway/sessions/:id", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xbuyer")
		c.Next()
	}, handler.GetSession)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHandler_GetSession_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)

	svc := newTestServiceWithLogger(newMockLedger(), &mockRegistry{})
	handler := NewHandler(svc)

	req := httptest.NewRequest("GET", "/v1/gateway/sessions/nonexistent", nil)
	w := httptest.NewRecorder()

	r := gin.New()
	r.GET("/v1/gateway/sessions/:id", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xbuyer")
		c.Next()
	}, handler.GetSession)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestHandler_GetSession_WrongOwner(t *testing.T) {
	gin.SetMode(gin.TestMode)

	svc := newTestServiceWithLogger(newMockLedger(), &mockRegistry{})
	handler := NewHandler(svc)

	session, _ := svc.CreateSession(context.Background(), "0xbuyer", "", CreateSessionRequest{
		MaxTotal: "10.00", MaxPerRequest: "1.00",
	})

	req := httptest.NewRequest("GET", "/v1/gateway/sessions/"+session.ID, nil)
	w := httptest.NewRecorder()

	r := gin.New()
	r.GET("/v1/gateway/sessions/:id", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xstranger")
		c.Next()
	}, handler.GetSession)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}
}

func TestHandler_ListSessions(t *testing.T) {
	gin.SetMode(gin.TestMode)

	svc := newTestServiceWithLogger(newMockLedger(), &mockRegistry{})
	handler := NewHandler(svc)

	svc.CreateSession(context.Background(), "0xbuyer", "", CreateSessionRequest{
		MaxTotal: "10.00", MaxPerRequest: "1.00",
	})

	req := httptest.NewRequest("GET", "/v1/gateway/sessions?limit=10", nil)
	w := httptest.NewRecorder()

	r := gin.New()
	r.GET("/v1/gateway/sessions", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xbuyer")
		c.Next()
	}, handler.ListSessions)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if int(resp["count"].(float64)) != 1 {
		t.Errorf("expected count 1, got %v", resp["count"])
	}
}

func TestHandler_CloseSession(t *testing.T) {
	gin.SetMode(gin.TestMode)

	svc := newTestServiceWithLogger(newMockLedger(), &mockRegistry{})
	handler := NewHandler(svc)

	session, _ := svc.CreateSession(context.Background(), "0xbuyer", "", CreateSessionRequest{
		MaxTotal: "10.00", MaxPerRequest: "1.00",
	})

	req := httptest.NewRequest("DELETE", "/v1/gateway/sessions/"+session.ID, nil)
	w := httptest.NewRecorder()

	r := gin.New()
	r.DELETE("/v1/gateway/sessions/:id", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xbuyer")
		c.Next()
	}, handler.CloseSession)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_CloseSession_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)

	svc := newTestServiceWithLogger(newMockLedger(), &mockRegistry{})
	handler := NewHandler(svc)

	req := httptest.NewRequest("DELETE", "/v1/gateway/sessions/nonexistent", nil)
	w := httptest.NewRecorder()

	r := gin.New()
	r.DELETE("/v1/gateway/sessions/:id", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xbuyer")
		c.Next()
	}, handler.CloseSession)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestHandler_DryRun(t *testing.T) {
	gin.SetMode(gin.TestMode)

	server := fakeServiceEndpoint(200, map[string]interface{}{"ok": true})
	defer server.Close()

	svc := newTestServiceWithLogger(newMockLedger(), &mockRegistry{
		services: []ServiceCandidate{
			{AgentAddress: "0xseller", Price: "0.50", Endpoint: server.URL, ServiceName: "test", ReputationScore: 80},
		},
	})
	handler := NewHandler(svc)

	session, _ := svc.CreateSession(context.Background(), "0xbuyer", "", CreateSessionRequest{
		MaxTotal: "10.00", MaxPerRequest: "5.00",
	})

	body := `{"serviceType":"test"}`
	req := httptest.NewRequest("POST", "/v1/gateway/sessions/"+session.ID+"/dry-run", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r := gin.New()
	r.POST("/v1/gateway/sessions/:id/dry-run", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xbuyer")
		c.Next()
	}, handler.DryRun)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_DryRun_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)

	svc := newTestServiceWithLogger(newMockLedger(), &mockRegistry{})
	handler := NewHandler(svc)

	body := `{"serviceType":"test"}`
	req := httptest.NewRequest("POST", "/v1/gateway/sessions/nonexistent/dry-run", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r := gin.New()
	r.POST("/v1/gateway/sessions/:id/dry-run", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xbuyer")
		c.Next()
	}, handler.DryRun)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestHandler_ListLogs(t *testing.T) {
	gin.SetMode(gin.TestMode)

	svc := newTestServiceWithLogger(newMockLedger(), &mockRegistry{})
	handler := NewHandler(svc)

	session, _ := svc.CreateSession(context.Background(), "0xbuyer", "", CreateSessionRequest{
		MaxTotal: "10.00", MaxPerRequest: "1.00",
	})

	req := httptest.NewRequest("GET", "/v1/gateway/sessions/"+session.ID+"/logs", nil)
	w := httptest.NewRecorder()

	r := gin.New()
	r.GET("/v1/gateway/sessions/:id/logs", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xbuyer")
		c.Next()
	}, handler.ListLogs)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHandler_SingleCall(t *testing.T) {
	gin.SetMode(gin.TestMode)

	server := fakeServiceEndpoint(200, map[string]interface{}{"ok": true})
	defer server.Close()

	svc := newTestServiceWithLogger(newMockLedger(), &mockRegistry{
		services: []ServiceCandidate{
			{AgentAddress: "0xseller", Price: "0.50", Endpoint: server.URL, ServiceName: "test", ReputationScore: 80},
		},
	})
	handler := NewHandler(svc)

	body := `{"maxPrice":"1.00","serviceType":"test"}`
	req := httptest.NewRequest("POST", "/v1/gateway/call", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r := gin.New()
	r.POST("/v1/gateway/call", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xbuyer")
		c.Next()
	}, handler.SingleCall)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_SingleCall_InvalidBody(t *testing.T) {
	gin.SetMode(gin.TestMode)

	svc := newTestServiceWithLogger(newMockLedger(), &mockRegistry{})
	handler := NewHandler(svc)

	req := httptest.NewRequest("POST", "/v1/gateway/call", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r := gin.New()
	r.POST("/v1/gateway/call", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xbuyer")
		c.Next()
	}, handler.SingleCall)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandler_Proxy_InvalidBody(t *testing.T) {
	gin.SetMode(gin.TestMode)

	svc := newTestServiceWithLogger(newMockLedger(), &mockRegistry{})
	handler := NewHandler(svc)

	session, _ := svc.CreateSession(context.Background(), "0xbuyer", "", CreateSessionRequest{
		MaxTotal: "10.00", MaxPerRequest: "1.00",
	})

	req := httptest.NewRequest("POST", "/v1/gateway/proxy", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r := gin.New()
	r.POST("/v1/gateway/proxy", func(c *gin.Context) {
		c.Set("gatewaySessionID", session.ID)
		c.Next()
	}, handler.Proxy)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

// --- WithHealthMonitor / WithHealthAwareRouter ---

func TestService_WithHealthMonitor(t *testing.T) {
	svc := newTestService(newMockLedger(), &mockRegistry{})
	hm := NewHealthMonitor(DefaultHealthMonitorConfig())
	svc.WithHealthMonitor(hm)
	if svc.HealthMonitor() != hm {
		t.Error("expected health monitor set")
	}
}
