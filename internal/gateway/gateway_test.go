package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// --- Mock Ledger ---

type mockLedger struct {
	holds      map[string]string // ref → amount
	deposits   map[string]string // ref → amount
	holdErr    error
	confirmErr error
	releaseErr error
	depositErr error
}

func newMockLedger() *mockLedger {
	return &mockLedger{
		holds:    make(map[string]string),
		deposits: make(map[string]string),
	}
}

func (m *mockLedger) Hold(_ context.Context, agentAddr, amount, reference string) error {
	if m.holdErr != nil {
		return m.holdErr
	}
	m.holds[reference] = amount
	return nil
}

func (m *mockLedger) ConfirmHold(_ context.Context, agentAddr, amount, reference string) error {
	if m.confirmErr != nil {
		return m.confirmErr
	}
	return nil
}

func (m *mockLedger) ReleaseHold(_ context.Context, agentAddr, amount, reference string) error {
	if m.releaseErr != nil {
		return m.releaseErr
	}
	delete(m.holds, reference)
	return nil
}

func (m *mockLedger) Deposit(_ context.Context, agentAddr, amount, reference string) error {
	if m.depositErr != nil {
		return m.depositErr
	}
	m.deposits[reference] = amount
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

	session, err := svc.CreateSession(context.Background(), "0xbuyer", CreateSessionRequest{
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

	_, err := svc.CreateSession(context.Background(), "0xbuyer", CreateSessionRequest{
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

	_, err := svc.CreateSession(context.Background(), "0xbuyer", CreateSessionRequest{
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

	session, err := svc.CreateSession(context.Background(), "0xbuyer", CreateSessionRequest{
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
	if result.AmountPaid != "0.50" {
		t.Errorf("expected 0.50 paid, got %s", result.AmountPaid)
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

	session, _ := svc.CreateSession(context.Background(), "0xbuyer", CreateSessionRequest{
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

	session, _ := svc.CreateSession(context.Background(), "0xbuyer", CreateSessionRequest{
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

	session, _ := svc.CreateSession(context.Background(), "0xbuyer", CreateSessionRequest{
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

	// Payment was made to both the failed and successful service
	if len(ml.deposits) != 2 {
		t.Errorf("expected 2 deposits (failed + successful), got %d", len(ml.deposits))
	}

	// Session spend reflects both payments (0.10 failed + 0.10 success)
	updated, _ := svc.GetSession(context.Background(), session.ID)
	if updated.TotalSpent != "0.200000" {
		t.Errorf("expected 0.200000 total spent (two payments), got %s", updated.TotalSpent)
	}
	if updated.RequestCount != 2 {
		t.Errorf("expected 2 request count (failed + success), got %d", updated.RequestCount)
	}
}

func TestProxy_AllowedTypes(t *testing.T) {
	ml := newMockLedger()
	reg := &mockRegistry{}
	svc := newTestServiceWithLogger(ml, reg)

	session, _ := svc.CreateSession(context.Background(), "0xbuyer", CreateSessionRequest{
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

	session, _ := svc.CreateSession(context.Background(), "0xbuyer", CreateSessionRequest{
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

	session, _ := svc.CreateSession(context.Background(), "0xbuyer", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "1.00",
	})

	_, err := svc.CloseSession(context.Background(), session.ID, "0xstranger")
	if err == nil {
		t.Fatal("expected error for wrong owner")
	}
}

func TestCloseSession_AlreadyClosed(t *testing.T) {
	ml := newMockLedger()
	svc := newTestService(ml, &mockRegistry{})

	session, _ := svc.CreateSession(context.Background(), "0xbuyer", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "1.00",
	})

	svc.CloseSession(context.Background(), session.ID, "0xbuyer")

	_, err := svc.CloseSession(context.Background(), session.ID, "0xbuyer")
	if err == nil {
		t.Fatal("expected error for already closed session")
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
	session, _ := svc.CreateSession(context.Background(), "0xbuyer", CreateSessionRequest{
		MaxTotal:      "10.00",
		MaxPerRequest: "1.00",
	})

	handler := NewHandler(svc)

	// Valid token
	t.Run("valid_token", func(t *testing.T) {
		called := false
		testHandler := handler.gatewayTokenAuth()

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("POST", "/", nil)
		c.Request.Header.Set("X-Gateway-Token", session.ID)

		// Chain with a next handler
		gin.SetMode(gin.TestMode)
		r := gin.New()
		r.POST("/test", testHandler, func(c *gin.Context) {
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

	session, _ := svc.CreateSession(context.Background(), "0xbuyer", CreateSessionRequest{
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
