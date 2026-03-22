package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

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

	_, _, found := c.getOrReserve(ctx, "s1", "k1")
	if found {
		t.Fatal("expected not found on first call")
	}

	result := &ProxyResult{AmountPaid: "1.00"}
	c.complete("s1", "k1", result)

	got, err, found := c.getOrReserve(ctx, "s1", "k1")
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

	_, _, found := c.getOrReserve(ctx, "s1", "k1")
	if found {
		t.Fatal("expected not found on first call")
	}

	c.cancel("s1", "k1")

	// Should be able to reserve again after cancel
	_, _, found = c.getOrReserve(ctx, "s1", "k1")
	if found {
		t.Fatal("expected not found after cancel")
	}
}

func TestIdempotencyCache_SweepSkipsInFlight(t *testing.T) {
	c := newIdempotencyCache(1 * time.Millisecond)
	ctx := context.Background()

	// Reserve but don't complete
	_, _, _ = c.getOrReserve(ctx, "s1", "k1")

	time.Sleep(5 * time.Millisecond)
	removed := c.sweep()
	if removed != 0 {
		t.Errorf("expected 0 removed (in-flight), got %d", removed)
	}
	if c.size() != 1 {
		t.Errorf("expected 1 entry (in-flight), got %d", c.size())
	}
}

func TestIdempotencyCache_MaxSize(t *testing.T) {
	c := newIdempotencyCache(time.Minute)
	c.maxSize = 2
	ctx := context.Background()

	// Fill up
	_, _, _ = c.getOrReserve(ctx, "s1", "k1")
	c.complete("s1", "k1", &ProxyResult{})
	_, _, _ = c.getOrReserve(ctx, "s1", "k2")
	c.complete("s1", "k2", &ProxyResult{})

	// Third should not be found (cache full) but also not cached
	_, _, found := c.getOrReserve(ctx, "s1", "k3")
	if found {
		t.Fatal("expected not found when cache at capacity")
	}
}

func TestIdempotencyCache_ExpiredEntry(t *testing.T) {
	c := newIdempotencyCache(1 * time.Millisecond)
	ctx := context.Background()

	_, _, _ = c.getOrReserve(ctx, "s1", "k1")
	c.complete("s1", "k1", &ProxyResult{AmountPaid: "old"})

	time.Sleep(5 * time.Millisecond)

	// Expired entry should be deleted on access, allowing re-reserve
	_, _, found := c.getOrReserve(ctx, "s1", "k1")
	if found {
		t.Fatal("expected not found for expired entry")
	}
}

func TestIdempotencyCache_ContextCancelled(t *testing.T) {
	c := newIdempotencyCache(time.Minute)

	// Reserve a key (in-flight)
	_, _, _ = c.getOrReserve(context.Background(), "s1", "k1")

	// Try to get the same key with a cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err, found := c.getOrReserve(ctx, "s1", "k1")
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

func TestRateLimiter_SweepKeepsCustomLimit(t *testing.T) {
	rl := newRateLimiter()
	rl.window = 1 * time.Millisecond

	rl.setLimit("s1", 200) // non-default limit
	rl.allow("s1")

	time.Sleep(5 * time.Millisecond)

	removed := rl.sweep()
	if removed != 0 {
		t.Errorf("expected 0 removed (custom limit), got %d", removed)
	}
	if rl.size() != 1 {
		t.Errorf("expected entry preserved with custom limit, got %d", rl.size())
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
	svc.idemCache.getOrReserve(ctx, "s1", "k1")
	svc.idemCache.complete("s1", "k1", &ProxyResult{})

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
	// best_value weights reputation 70% and inverse-price 30%.
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
