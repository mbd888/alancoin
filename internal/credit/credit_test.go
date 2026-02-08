package credit

import (
	"context"
	"fmt"
	"math"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Mock implementations
// ---------------------------------------------------------------------------

type mockReputationProvider struct {
	mu     sync.Mutex
	scores map[string]struct {
		score float64
		tier  string
	}
}

func newMockReputation() *mockReputationProvider {
	return &mockReputationProvider{
		scores: make(map[string]struct {
			score float64
			tier  string
		}),
	}
}

func (m *mockReputationProvider) setScore(addr string, score float64, tier string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.scores[addr] = struct {
		score float64
		tier  string
	}{score, tier}
}

func (m *mockReputationProvider) GetScore(_ context.Context, address string) (float64, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.scores[address]
	if !ok {
		return 0, "new", fmt.Errorf("agent not found: %s", address)
	}
	return s.score, s.tier, nil
}

type mockMetricsProvider struct {
	mu      sync.Mutex
	metrics map[string]struct {
		totalTxns      int
		successRate    float64
		daysOnNetwork  int
		totalVolumeUSD float64
	}
}

func newMockMetrics() *mockMetricsProvider {
	return &mockMetricsProvider{
		metrics: make(map[string]struct {
			totalTxns      int
			successRate    float64
			daysOnNetwork  int
			totalVolumeUSD float64
		}),
	}
}

func (m *mockMetricsProvider) setMetrics(addr string, txns int, rate float64, days int, volume float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.metrics[addr] = struct {
		totalTxns      int
		successRate    float64
		daysOnNetwork  int
		totalVolumeUSD float64
	}{txns, rate, days, volume}
}

func (m *mockMetricsProvider) GetAgentMetrics(_ context.Context, address string) (int, float64, int, float64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	met, ok := m.metrics[address]
	if !ok {
		return 0, 0, 0, 0, fmt.Errorf("metrics not found: %s", address)
	}
	return met.totalTxns, met.successRate, met.daysOnNetwork, met.totalVolumeUSD, nil
}

type mockLedgerService struct {
	mu               sync.Mutex
	creditLimits     map[string]string
	repayments       map[string]string
	setCreditLimitCt int
	repayCreditCt    int
	setLimitErr      error
	repayErr         error
}

func newMockLedger() *mockLedgerService {
	return &mockLedgerService{
		creditLimits: make(map[string]string),
		repayments:   make(map[string]string),
	}
}

func (m *mockLedgerService) GetCreditInfo(_ context.Context, agentAddr string) (string, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	limit := m.creditLimits[agentAddr]
	if limit == "" {
		limit = "0"
	}
	return limit, "0", nil
}

func (m *mockLedgerService) SetCreditLimit(_ context.Context, agentAddr, limit string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.setCreditLimitCt++
	if m.setLimitErr != nil {
		return m.setLimitErr
	}
	m.creditLimits[agentAddr] = limit
	return nil
}

func (m *mockLedgerService) RepayCredit(_ context.Context, agentAddr, amount string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.repayCreditCt++
	if m.repayErr != nil {
		return m.repayErr
	}
	m.repayments[agentAddr] = amount
	return nil
}

// Compile-time interface checks
var _ ReputationProvider = (*mockReputationProvider)(nil)
var _ MetricsProvider = (*mockMetricsProvider)(nil)
var _ LedgerService = (*mockLedgerService)(nil)

// ---------------------------------------------------------------------------
// Helper: create a fully-wired test service
// ---------------------------------------------------------------------------

func newTestService() (*Service, *MemoryStore, *mockReputationProvider, *mockMetricsProvider, *mockLedgerService) {
	store := NewMemoryStore()
	rep := newMockReputation()
	met := newMockMetrics()
	ledger := newMockLedger()
	scorer := NewScorer()
	svc := NewService(store, scorer, rep, met, ledger)
	return svc, store, rep, met, ledger
}

func setupEligibleAgent(rep *mockReputationProvider, met *mockMetricsProvider, addr string, score float64, tier string) {
	rep.setScore(addr, score, tier)
	met.setMetrics(addr, 50, 0.98, 60, 1000)
}

// ===========================================================================
// Scorer tests
// ===========================================================================

func TestScorer_NewAgentRejected(t *testing.T) {
	scorer := NewScorer()
	result := scorer.Evaluate(5.0, "new", 2, 1.0, 3, 10)
	if result.Eligible {
		t.Error("New agents should not be eligible")
	}
	if result.Reason == "" {
		t.Error("Expected rejection reason")
	}
}

func TestScorer_EmergingAgentRejected(t *testing.T) {
	scorer := NewScorer()
	result := scorer.Evaluate(25.0, "emerging", 20, 0.97, 15, 200)
	if result.Eligible {
		t.Error("Emerging agents should not be eligible")
	}
}

func TestScorer_EstablishedAgentApproved(t *testing.T) {
	scorer := NewScorer()
	result := scorer.Evaluate(45.0, "established", 50, 0.98, 60, 1000)
	if !result.Eligible {
		t.Fatalf("Established agent should be eligible, reason: %s", result.Reason)
	}
	if result.InterestRate != 0.10 {
		t.Errorf("Expected 10%% interest, got %f", result.InterestRate)
	}
	if result.CreditLimit <= 0 || result.CreditLimit > 5.0 {
		t.Errorf("Established limit should be (0, 5.0], got %f", result.CreditLimit)
	}
}

func TestScorer_TrustedAgentApproved(t *testing.T) {
	scorer := NewScorer()
	result := scorer.Evaluate(65.0, "trusted", 50, 0.98, 60, 1000)
	if !result.Eligible {
		t.Fatalf("Trusted agent should be eligible, reason: %s", result.Reason)
	}
	if result.InterestRate != 0.07 {
		t.Errorf("Expected 7%% interest, got %f", result.InterestRate)
	}
	if result.CreditLimit <= 0 || result.CreditLimit > 50.0 {
		t.Errorf("Trusted limit should be (0, 50.0], got %f", result.CreditLimit)
	}
}

func TestScorer_EliteAgentApproved(t *testing.T) {
	scorer := NewScorer()
	result := scorer.Evaluate(90.0, "elite", 200, 0.99, 120, 50000)
	if !result.Eligible {
		t.Fatalf("Elite agent should be eligible, reason: %s", result.Reason)
	}
	if result.InterestRate != 0.05 {
		t.Errorf("Expected 5%% interest, got %f", result.InterestRate)
	}
	if result.CreditLimit <= 0 || result.CreditLimit > 500.0 {
		t.Errorf("Elite limit should be (0, 500.0], got %f", result.CreditLimit)
	}
}

func TestScorer_LowSuccessRateRejected(t *testing.T) {
	scorer := NewScorer()
	result := scorer.Evaluate(50.0, "established", 50, 0.90, 60, 1000)
	if result.Eligible {
		t.Error("Should reject agent with < 95% success rate")
	}
	if result.Reason == "" {
		t.Error("Expected rejection reason about success rate")
	}
}

func TestScorer_InsufficientDaysRejected(t *testing.T) {
	scorer := NewScorer()
	result := scorer.Evaluate(50.0, "established", 50, 0.98, 10, 1000)
	if result.Eligible {
		t.Error("Should reject agent with insufficient days on network")
	}
}

func TestScorer_InsufficientTransactionsRejected(t *testing.T) {
	scorer := NewScorer()
	result := scorer.Evaluate(50.0, "established", 5, 0.98, 60, 1000)
	if result.Eligible {
		t.Error("Should reject agent with insufficient transactions")
	}
}

func TestScorer_LowReputationScoreRejected(t *testing.T) {
	scorer := NewScorer()
	// Trusted tier requires 60+ score
	result := scorer.Evaluate(55.0, "trusted", 50, 0.98, 60, 1000)
	if result.Eligible {
		t.Error("Should reject agent with score below tier minimum")
	}
}

func TestScorer_VolumeScaling(t *testing.T) {
	scorer := NewScorer()

	// Zero volume → factor is 0.5 → limit = maxCredit * 0.5
	result := scorer.Evaluate(50.0, "established", 50, 0.98, 60, 0)
	if !result.Eligible {
		t.Fatalf("Should be eligible, reason: %s", result.Reason)
	}
	expectedZero := 5.0 * 0.5 // 2.50
	if math.Abs(result.CreditLimit-expectedZero) > 0.01 {
		t.Errorf("Zero volume: expected ~%.2f, got %.2f", expectedZero, result.CreditLimit)
	}

	// High volume (10000) → factor approaches 1.0 → full limit
	result = scorer.Evaluate(50.0, "established", 50, 0.98, 60, 10000)
	if !result.Eligible {
		t.Fatalf("Should be eligible, reason: %s", result.Reason)
	}
	// log10(10001) ≈ 4.0, factor = min(1.0, 0.5 + 0.5*4/4) = 1.0
	if result.CreditLimit < 4.99 {
		t.Errorf("High volume: expected ~5.00, got %.2f", result.CreditLimit)
	}

	// Medium volume (100) → factor between 0.5 and 1.0
	result = scorer.Evaluate(50.0, "established", 50, 0.98, 60, 100)
	if !result.Eligible {
		t.Fatalf("Should be eligible, reason: %s", result.Reason)
	}
	if result.CreditLimit <= expectedZero || result.CreditLimit >= 5.0 {
		t.Errorf("Medium volume: expected limit between %.2f and 5.00, got %.2f", expectedZero, result.CreditLimit)
	}
}

func TestScorer_UnknownTierRejected(t *testing.T) {
	scorer := NewScorer()
	result := scorer.Evaluate(50.0, "unknown_tier", 50, 0.98, 60, 1000)
	if result.Eligible {
		t.Error("Unknown tier should not be eligible")
	}
}

// ===========================================================================
// Service tests
// ===========================================================================

func TestService_ApplyHappyPath(t *testing.T) {
	svc, _, rep, met, ledger := newTestService()
	ctx := context.Background()

	addr := "0xagent1"
	setupEligibleAgent(rep, met, addr, 65.0, "trusted")

	line, result, err := svc.Apply(ctx, addr)
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	if line == nil {
		t.Fatal("Expected credit line")
	}
	if result == nil || !result.Eligible {
		t.Fatal("Expected eligible evaluation")
	}
	if line.Status != StatusActive {
		t.Errorf("Expected status active, got %s", line.Status)
	}
	if line.AgentAddr != addr {
		t.Errorf("Expected agent addr %s, got %s", addr, line.AgentAddr)
	}
	if line.CreditUsed != "0.000000" {
		t.Errorf("Expected credit used 0, got %s", line.CreditUsed)
	}
	if line.ID == "" {
		t.Error("Expected non-empty ID")
	}

	// Verify ledger was updated
	ledger.mu.Lock()
	if ledger.setCreditLimitCt != 1 {
		t.Errorf("Expected 1 SetCreditLimit call, got %d", ledger.setCreditLimitCt)
	}
	if ledger.creditLimits[addr] == "" {
		t.Error("Expected credit limit to be set in ledger")
	}
	ledger.mu.Unlock()
}

func TestService_ApplyAlreadyExists(t *testing.T) {
	svc, _, rep, met, _ := newTestService()
	ctx := context.Background()

	addr := "0xagent2"
	setupEligibleAgent(rep, met, addr, 65.0, "trusted")

	// First application
	_, _, err := svc.Apply(ctx, addr)
	if err != nil {
		t.Fatalf("First apply failed: %v", err)
	}

	// Second application should fail
	_, _, err = svc.Apply(ctx, addr)
	if err != ErrCreditLineExists {
		t.Errorf("Expected ErrCreditLineExists, got %v", err)
	}
}

func TestService_ApplyNotEligible(t *testing.T) {
	svc, _, rep, met, _ := newTestService()
	ctx := context.Background()

	addr := "0xnewagent"
	rep.setScore(addr, 10.0, "new")
	met.setMetrics(addr, 3, 1.0, 5, 10)

	line, result, err := svc.Apply(ctx, addr)
	if err != ErrNotEligible {
		t.Errorf("Expected ErrNotEligible, got %v", err)
	}
	if line != nil {
		t.Error("Expected nil credit line for ineligible agent")
	}
	if result == nil {
		t.Fatal("Expected evaluation result even when not eligible")
	}
	if result.Eligible {
		t.Error("Expected eligible=false in evaluation")
	}
	if result.Reason == "" {
		t.Error("Expected rejection reason")
	}
}

func TestService_ApplyCaseInsensitive(t *testing.T) {
	svc, _, rep, met, _ := newTestService()
	ctx := context.Background()

	addr := "0xagent_case"
	setupEligibleAgent(rep, met, addr, 65.0, "trusted")

	_, _, err := svc.Apply(ctx, "0xAGENT_CASE")
	if err != nil {
		t.Fatalf("Apply with uppercase should work: %v", err)
	}

	// Lookup with lowercase should find it
	line, err := svc.GetByAgent(ctx, addr)
	if err != nil {
		t.Fatalf("GetByAgent failed: %v", err)
	}
	if line.AgentAddr != addr {
		t.Errorf("Expected lowercased addr %s, got %s", addr, line.AgentAddr)
	}
}

func TestService_ReviewUpgrade(t *testing.T) {
	svc, _, rep, met, _ := newTestService()
	ctx := context.Background()

	addr := "0xagent_upgrade"
	setupEligibleAgent(rep, met, addr, 45.0, "established")

	// Apply as established
	line, _, err := svc.Apply(ctx, addr)
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	originalLimit := line.CreditLimit

	// Upgrade to trusted
	rep.setScore(addr, 70.0, "trusted")
	met.setMetrics(addr, 100, 0.99, 90, 5000)

	line, result, err := svc.Review(ctx, addr)
	if err != nil {
		t.Fatalf("Review failed: %v", err)
	}
	if !result.Eligible {
		t.Fatal("Should still be eligible after upgrade")
	}
	if line.CreditLimit == originalLimit {
		t.Error("Expected credit limit to change after upgrade")
	}
	if line.ReputationTier != "trusted" {
		t.Errorf("Expected tier trusted, got %s", line.ReputationTier)
	}
}

func TestService_ReviewDowngrade(t *testing.T) {
	svc, _, rep, met, _ := newTestService()
	ctx := context.Background()

	addr := "0xagent_downgrade"
	setupEligibleAgent(rep, met, addr, 65.0, "trusted")

	_, _, err := svc.Apply(ctx, addr)
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}

	// Downgrade: reputation drops below minimum
	rep.setScore(addr, 15.0, "new")
	met.setMetrics(addr, 5, 0.80, 10, 50)

	line, _, err := svc.Review(ctx, addr)
	if err != nil {
		t.Fatalf("Review failed: %v", err)
	}
	if line.Status != StatusSuspended {
		t.Errorf("Expected status suspended after downgrade, got %s", line.Status)
	}
}

func TestService_Revoke(t *testing.T) {
	svc, _, rep, met, ledger := newTestService()
	ctx := context.Background()

	addr := "0xagent_revoke"
	setupEligibleAgent(rep, met, addr, 65.0, "trusted")

	_, _, err := svc.Apply(ctx, addr)
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}

	line, err := svc.Revoke(ctx, addr, "policy violation")
	if err != nil {
		t.Fatalf("Revoke failed: %v", err)
	}
	if line.Status != StatusRevoked {
		t.Errorf("Expected status revoked, got %s", line.Status)
	}
	if line.RevokedAt.IsZero() {
		t.Error("Expected RevokedAt to be set")
	}

	// Ledger should have limit zeroed out
	ledger.mu.Lock()
	if ledger.creditLimits[addr] != "0" {
		t.Errorf("Expected credit limit zeroed, got %s", ledger.creditLimits[addr])
	}
	ledger.mu.Unlock()
}

func TestService_CheckDefaults(t *testing.T) {
	svc, store, _, _, ledger := newTestService()
	ctx := context.Background()

	// Create an overdue credit line directly in the store (>90 days old, with usage)
	overdue := &CreditLine{
		ID:          "cl_overdue",
		AgentAddr:   "0xoverdue",
		CreditLimit: "50.000000",
		CreditUsed:  "25.000000",
		Status:      StatusActive,
		ApprovedAt:  time.Now().AddDate(0, 0, -100), // 100 days ago
		CreatedAt:   time.Now().AddDate(0, 0, -100),
		UpdatedAt:   time.Now(),
	}
	store.Create(ctx, overdue)

	// Create a recent credit line (should not be defaulted)
	recent := &CreditLine{
		ID:          "cl_recent",
		AgentAddr:   "0xrecent",
		CreditLimit: "5.000000",
		CreditUsed:  "1.000000",
		Status:      StatusActive,
		ApprovedAt:  time.Now().AddDate(0, 0, -10), // 10 days ago
		CreatedAt:   time.Now().AddDate(0, 0, -10),
		UpdatedAt:   time.Now(),
	}
	store.Create(ctx, recent)

	count, err := svc.CheckDefaults(ctx)
	if err != nil {
		t.Fatalf("CheckDefaults failed: %v", err)
	}
	if count != 1 {
		t.Errorf("Expected 1 default, got %d", count)
	}

	// Verify the overdue line was defaulted
	got, _ := store.Get(ctx, "cl_overdue")
	if got.Status != StatusDefaulted {
		t.Errorf("Expected overdue line to be defaulted, got %s", got.Status)
	}
	if got.DefaultedAt.IsZero() {
		t.Error("Expected DefaultedAt to be set")
	}

	// Verify the recent line is still active
	got, _ = store.Get(ctx, "cl_recent")
	if got.Status != StatusActive {
		t.Errorf("Expected recent line to still be active, got %s", got.Status)
	}

	// Verify ledger was zeroed for the defaulted line
	ledger.mu.Lock()
	if ledger.creditLimits["0xoverdue"] != "0" {
		t.Errorf("Expected credit limit zeroed for defaulted, got %s", ledger.creditLimits["0xoverdue"])
	}
	ledger.mu.Unlock()
}

func TestService_RepayCallsLedger(t *testing.T) {
	svc, _, rep, met, ledger := newTestService()
	ctx := context.Background()

	addr := "0xagent_repay"
	setupEligibleAgent(rep, met, addr, 65.0, "trusted")

	_, _, err := svc.Apply(ctx, addr)
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}

	err = svc.Repay(ctx, addr, "5.00")
	if err != nil {
		t.Fatalf("Repay failed: %v", err)
	}

	ledger.mu.Lock()
	if ledger.repayCreditCt != 1 {
		t.Errorf("Expected 1 RepayCredit call, got %d", ledger.repayCreditCt)
	}
	if ledger.repayments[addr] != "5.00" {
		t.Errorf("Expected repayment of 5.00, got %s", ledger.repayments[addr])
	}
	ledger.mu.Unlock()
}

func TestService_RepayNoCreditLine(t *testing.T) {
	svc, _, _, _, _ := newTestService()
	ctx := context.Background()

	err := svc.Repay(ctx, "0xnonexistent", "5.00")
	if err != ErrCreditLineNotFound {
		t.Errorf("Expected ErrCreditLineNotFound, got %v", err)
	}
}

func TestService_RepayRevokedLine(t *testing.T) {
	svc, _, rep, met, _ := newTestService()
	ctx := context.Background()

	addr := "0xagent_repay_revoked"
	setupEligibleAgent(rep, met, addr, 65.0, "trusted")

	_, _, _ = svc.Apply(ctx, addr)
	_, _ = svc.Revoke(ctx, addr, "test")

	err := svc.Repay(ctx, addr, "5.00")
	if err == nil {
		t.Error("Expected error when repaying a revoked credit line")
	}
}

func TestService_ApplyAfterRevoke(t *testing.T) {
	svc, _, rep, met, _ := newTestService()
	ctx := context.Background()

	addr := "0xagent_reapply"
	setupEligibleAgent(rep, met, addr, 65.0, "trusted")

	_, _, _ = svc.Apply(ctx, addr)
	_, _ = svc.Revoke(ctx, addr, "test")

	// Trying to reapply after revocation should be rejected
	_, _, err := svc.Apply(ctx, addr)
	if err != ErrCreditLineRevoked {
		t.Errorf("Expected ErrCreditLineRevoked, got %v", err)
	}
}

func TestService_ReviewNonexistent(t *testing.T) {
	svc, _, _, _, _ := newTestService()
	ctx := context.Background()

	_, _, err := svc.Review(ctx, "0xnonexistent")
	if err != ErrCreditLineNotFound {
		t.Errorf("Expected ErrCreditLineNotFound, got %v", err)
	}
}

// ===========================================================================
// MemoryStore tests
// ===========================================================================

func TestMemoryStore_CRUDLifecycle(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	now := time.Now()
	line := &CreditLine{
		ID:          "cl_test_1",
		AgentAddr:   "0xagent_store",
		CreditLimit: "50.000000",
		CreditUsed:  "0.000000",
		Status:      StatusActive,
		ApprovedAt:  now,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	// Create
	err := store.Create(ctx, line)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Read by ID
	got, err := store.Get(ctx, "cl_test_1")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got.AgentAddr != "0xagent_store" {
		t.Errorf("Expected addr 0xagent_store, got %s", got.AgentAddr)
	}

	// Update
	got.CreditUsed = "10.000000"
	err = store.Update(ctx, got)
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	updated, _ := store.Get(ctx, "cl_test_1")
	if updated.CreditUsed != "10.000000" {
		t.Errorf("Expected credit used 10.000000, got %s", updated.CreditUsed)
	}

	// Get non-existent
	_, err = store.Get(ctx, "cl_nonexistent")
	if err != ErrCreditLineNotFound {
		t.Errorf("Expected ErrCreditLineNotFound, got %v", err)
	}
}

func TestMemoryStore_GetByAgent(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	now := time.Now()
	store.Create(ctx, &CreditLine{
		ID: "cl_1", AgentAddr: "0xfoo", Status: StatusActive,
		CreditLimit: "5.000000", CreditUsed: "0.000000",
		ApprovedAt: now, CreatedAt: now, UpdatedAt: now,
	})

	// Lookup
	line, err := store.GetByAgent(ctx, "0xfoo")
	if err != nil {
		t.Fatalf("GetByAgent failed: %v", err)
	}
	if line.ID != "cl_1" {
		t.Errorf("Expected cl_1, got %s", line.ID)
	}

	// Case insensitive
	line, err = store.GetByAgent(ctx, "0xFOO")
	if err != nil {
		t.Fatalf("GetByAgent case insensitive failed: %v", err)
	}
	if line.ID != "cl_1" {
		t.Errorf("Expected cl_1 for uppercase lookup, got %s", line.ID)
	}

	// Non-existent
	_, err = store.GetByAgent(ctx, "0xbar")
	if err != ErrCreditLineNotFound {
		t.Errorf("Expected ErrCreditLineNotFound, got %v", err)
	}
}

func TestMemoryStore_ListActive(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	now := time.Now()
	store.Create(ctx, &CreditLine{
		ID: "cl_a", AgentAddr: "0xa", Status: StatusActive,
		CreditLimit: "5.000000", CreditUsed: "0.000000",
		ApprovedAt: now, CreatedAt: now, UpdatedAt: now,
	})
	store.Create(ctx, &CreditLine{
		ID: "cl_b", AgentAddr: "0xb", Status: StatusActive,
		CreditLimit: "50.000000", CreditUsed: "0.000000",
		ApprovedAt: now, CreatedAt: now, UpdatedAt: now,
	})
	store.Create(ctx, &CreditLine{
		ID: "cl_c", AgentAddr: "0xc", Status: StatusRevoked,
		CreditLimit: "5.000000", CreditUsed: "0.000000",
		ApprovedAt: now, CreatedAt: now, UpdatedAt: now,
	})
	store.Create(ctx, &CreditLine{
		ID: "cl_d", AgentAddr: "0xd", Status: StatusDefaulted,
		CreditLimit: "500.000000", CreditUsed: "100.000000",
		ApprovedAt: now, CreatedAt: now, UpdatedAt: now,
	})

	// Should only return active lines
	active, err := store.ListActive(ctx, 100)
	if err != nil {
		t.Fatalf("ListActive failed: %v", err)
	}
	if len(active) != 2 {
		t.Errorf("Expected 2 active lines, got %d", len(active))
	}

	// Limit
	active, err = store.ListActive(ctx, 1)
	if err != nil {
		t.Fatalf("ListActive with limit failed: %v", err)
	}
	if len(active) != 1 {
		t.Errorf("Expected 1 with limit, got %d", len(active))
	}
}

func TestMemoryStore_ListOverdue(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	now := time.Now()

	// Overdue: active, has credit used, approved > 90 days ago
	store.Create(ctx, &CreditLine{
		ID: "cl_overdue", AgentAddr: "0xoverdue", Status: StatusActive,
		CreditLimit: "50.000000", CreditUsed: "25.000000",
		ApprovedAt: now.AddDate(0, 0, -100), CreatedAt: now.AddDate(0, 0, -100), UpdatedAt: now,
	})

	// Not overdue: active but recent
	store.Create(ctx, &CreditLine{
		ID: "cl_recent", AgentAddr: "0xrecent", Status: StatusActive,
		CreditLimit: "5.000000", CreditUsed: "2.000000",
		ApprovedAt: now.AddDate(0, 0, -10), CreatedAt: now.AddDate(0, 0, -10), UpdatedAt: now,
	})

	// Not overdue: old but no credit used
	store.Create(ctx, &CreditLine{
		ID: "cl_unused", AgentAddr: "0xunused", Status: StatusActive,
		CreditLimit: "50.000000", CreditUsed: "0.000000",
		ApprovedAt: now.AddDate(0, 0, -100), CreatedAt: now.AddDate(0, 0, -100), UpdatedAt: now,
	})

	// Not overdue: revoked
	store.Create(ctx, &CreditLine{
		ID: "cl_revoked", AgentAddr: "0xrevoked", Status: StatusRevoked,
		CreditLimit: "50.000000", CreditUsed: "10.000000",
		ApprovedAt: now.AddDate(0, 0, -100), CreatedAt: now.AddDate(0, 0, -100), UpdatedAt: now,
	})

	overdue, err := store.ListOverdue(ctx, 90, 100)
	if err != nil {
		t.Fatalf("ListOverdue failed: %v", err)
	}
	if len(overdue) != 1 {
		t.Errorf("Expected 1 overdue line, got %d", len(overdue))
	}
	if len(overdue) > 0 && overdue[0].ID != "cl_overdue" {
		t.Errorf("Expected cl_overdue, got %s", overdue[0].ID)
	}
}

func TestMemoryStore_UpdateNonexistent(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	err := store.Update(ctx, &CreditLine{ID: "cl_ghost"})
	if err != ErrCreditLineNotFound {
		t.Errorf("Expected ErrCreditLineNotFound, got %v", err)
	}
}

func TestMemoryStore_CreateDuplicate(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	now := time.Now()
	store.Create(ctx, &CreditLine{
		ID: "cl_first", AgentAddr: "0xdup", Status: StatusActive,
		CreditLimit: "5.000000", CreditUsed: "0.000000",
		ApprovedAt: now, CreatedAt: now, UpdatedAt: now,
	})

	// Same agent, second active credit line should fail
	err := store.Create(ctx, &CreditLine{
		ID: "cl_second", AgentAddr: "0xdup", Status: StatusActive,
		CreditLimit: "10.000000", CreditUsed: "0.000000",
		ApprovedAt: now, CreatedAt: now, UpdatedAt: now,
	})
	if err != ErrCreditLineExists {
		t.Errorf("Expected ErrCreditLineExists for duplicate, got %v", err)
	}
}

func TestMemoryStore_ReturnsCopies(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	now := time.Now()
	store.Create(ctx, &CreditLine{
		ID: "cl_copy", AgentAddr: "0xcopy", Status: StatusActive,
		CreditLimit: "5.000000", CreditUsed: "0.000000",
		ApprovedAt: now, CreatedAt: now, UpdatedAt: now,
	})

	// Mutating the returned value should not affect the store
	got, _ := store.Get(ctx, "cl_copy")
	got.CreditUsed = "999.000000"

	fresh, _ := store.Get(ctx, "cl_copy")
	if fresh.CreditUsed != "0.000000" {
		t.Errorf("Store should return copies; mutation leaked: got %s", fresh.CreditUsed)
	}
}
