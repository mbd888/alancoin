package verified

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Mock implementations
// ---------------------------------------------------------------------------

// mockReputationProvider returns fixed reputation data.
type mockReputationProvider struct {
	mu    sync.Mutex
	score float64
	tier  string
	err   error
}

func newMockReputation(score float64, tier string) *mockReputationProvider {
	return &mockReputationProvider{score: score, tier: tier}
}

func (m *mockReputationProvider) GetScore(_ context.Context, address string) (float64, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return 0, "", m.err
	}
	return m.score, m.tier, nil
}

func (m *mockReputationProvider) setScore(score float64, tier string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.score = score
	m.tier = tier
}

// mockMetricsProvider returns fixed metrics data.
type mockMetricsProvider struct {
	mu             sync.Mutex
	totalTxns      int
	successRate    float64
	daysOnNetwork  int
	totalVolumeUSD float64
	err            error
}

func newMockMetrics(totalTxns int, successRate float64, daysOnNetwork int, totalVolumeUSD float64) *mockMetricsProvider {
	return &mockMetricsProvider{
		totalTxns:      totalTxns,
		successRate:    successRate,
		daysOnNetwork:  daysOnNetwork,
		totalVolumeUSD: totalVolumeUSD,
	}
}

func (m *mockMetricsProvider) GetAgentMetrics(_ context.Context, address string) (int, float64, int, float64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return 0, 0, 0, 0, m.err
	}
	return m.totalTxns, m.successRate, m.daysOnNetwork, m.totalVolumeUSD, nil
}

func (m *mockMetricsProvider) setMetrics(totalTxns int, successRate float64, daysOnNetwork int, totalVolumeUSD float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.totalTxns = totalTxns
	m.successRate = successRate
	m.daysOnNetwork = daysOnNetwork
	m.totalVolumeUSD = totalVolumeUSD
}

// mockLedger records ledger operations.
type mockLedger struct {
	mu       sync.Mutex
	holds    map[string]string // reference → amount
	confirms map[string]string
	releases map[string]string
	deposits map[string]string // agentAddr → amount
	holdErr  error
}

func newMockLedger() *mockLedger {
	return &mockLedger{
		holds:    make(map[string]string),
		confirms: make(map[string]string),
		releases: make(map[string]string),
		deposits: make(map[string]string),
	}
}

func (m *mockLedger) Hold(_ context.Context, agentAddr, amount, reference string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.holdErr != nil {
		return m.holdErr
	}
	m.holds[reference] = amount
	return nil
}

func (m *mockLedger) ConfirmHold(_ context.Context, agentAddr, amount, reference string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.confirms[reference] = amount
	return nil
}

func (m *mockLedger) ReleaseHold(_ context.Context, agentAddr, amount, reference string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.releases[reference] = amount
	return nil
}

func (m *mockLedger) Deposit(_ context.Context, agentAddr, amount, reference string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deposits[agentAddr] = amount
	return nil
}

// mockContractCallProvider returns fixed call data for enforcement.
type mockContractCallProvider struct {
	mu           sync.Mutex
	successCount int
	totalCount   int
	err          error
}

func newMockCallProvider(successCount, totalCount int) *mockContractCallProvider {
	return &mockContractCallProvider{
		successCount: successCount,
		totalCount:   totalCount,
	}
}

func (m *mockContractCallProvider) GetRecentCallsByAgent(_ context.Context, agentAddr string, windowSize int) (int, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return 0, 0, m.err
	}
	return m.successCount, m.totalCount, nil
}

// ---------------------------------------------------------------------------
// Scorer tests
// ---------------------------------------------------------------------------

func TestScorer_EligibleElite(t *testing.T) {
	scorer := NewScorer()

	result := scorer.Evaluate(
		85.0, // score (elite minimum is 80)
		"elite",
		100,    // transactions (minimum is 50)
		0.97,   // success rate (minimum is 0.95)
		20,     // days on network (minimum is 14)
		1000.0, // volume USD
	)

	if !result.Eligible {
		t.Fatalf("expected eligible, got reason: %s", result.Reason)
	}
	if result.GuaranteedSuccessRate != 97.0 {
		t.Errorf("expected guaranteed rate 97.0, got %.1f", result.GuaranteedSuccessRate)
	}
	if result.MinBondAmount != 5.0 {
		t.Errorf("expected min bond 5.0, got %.2f", result.MinBondAmount)
	}
	if result.GuaranteePremiumRate != 0.05 {
		t.Errorf("expected premium 0.05, got %.2f", result.GuaranteePremiumRate)
	}
	if result.SLAWindowSize != 20 {
		t.Errorf("expected window size 20, got %d", result.SLAWindowSize)
	}
}

func TestScorer_EligibleTrusted(t *testing.T) {
	scorer := NewScorer()

	result := scorer.Evaluate(
		65.0, // score (trusted minimum is 60)
		"trusted",
		60,    // transactions (minimum is 50)
		0.96,  // success rate (minimum is 0.95)
		15,    // days on network (minimum is 14)
		500.0, // volume USD
	)

	if !result.Eligible {
		t.Fatalf("expected eligible, got reason: %s", result.Reason)
	}
	if result.GuaranteedSuccessRate != 95.0 {
		t.Errorf("expected guaranteed rate 95.0, got %.1f", result.GuaranteedSuccessRate)
	}
	if result.MinBondAmount != 1.0 {
		t.Errorf("expected min bond 1.0, got %.2f", result.MinBondAmount)
	}
	if result.GuaranteePremiumRate != 0.07 {
		t.Errorf("expected premium 0.07, got %.2f", result.GuaranteePremiumRate)
	}
}

func TestScorer_IneligibleNewTier(t *testing.T) {
	scorer := NewScorer()

	result := scorer.Evaluate(
		50.0,
		"new", // new tier has no policy
		100,
		0.99,
		30,
		1000.0,
	)

	if result.Eligible {
		t.Fatal("expected ineligible for new tier")
	}
	if !strings.Contains(result.Reason, "not eligible for verification") {
		t.Errorf("expected tier ineligibility reason, got: %s", result.Reason)
	}
}

func TestScorer_IneligibleLowSuccessRate(t *testing.T) {
	scorer := NewScorer()

	result := scorer.Evaluate(
		85.0,
		"elite",
		100,
		0.90, // below 0.95 minimum
		20,
		1000.0,
	)

	if result.Eligible {
		t.Fatal("expected ineligible due to low success rate")
	}
	if !strings.Contains(result.Reason, "success rate") {
		t.Errorf("expected success rate reason, got: %s", result.Reason)
	}
}

func TestScorer_IneligibleInsufficientTransactions(t *testing.T) {
	scorer := NewScorer()

	result := scorer.Evaluate(
		85.0,
		"elite",
		30, // below 50 minimum
		0.97,
		20,
		1000.0,
	)

	if result.Eligible {
		t.Fatal("expected ineligible due to insufficient transactions")
	}
	if !strings.Contains(result.Reason, "transactions below minimum") {
		t.Errorf("expected transaction count reason, got: %s", result.Reason)
	}
}

func TestScorer_IneligibleInsufficientDays(t *testing.T) {
	scorer := NewScorer()

	result := scorer.Evaluate(
		85.0,
		"elite",
		100,
		0.97,
		10, // below 14 minimum
		1000.0,
	)

	if result.Eligible {
		t.Fatal("expected ineligible due to insufficient days")
	}
	if !strings.Contains(result.Reason, "days on network") {
		t.Errorf("expected days on network reason, got: %s", result.Reason)
	}
}

func TestScorer_IneligibleLowReputationScore(t *testing.T) {
	scorer := NewScorer()

	result := scorer.Evaluate(
		55.0, // below 60 minimum for trusted
		"trusted",
		100,
		0.97,
		20,
		1000.0,
	)

	if result.Eligible {
		t.Fatal("expected ineligible due to low reputation score")
	}
	if !strings.Contains(result.Reason, "reputation score") {
		t.Errorf("expected reputation score reason, got: %s", result.Reason)
	}
}

func TestScorer_BondAmountScaling(t *testing.T) {
	scorer := NewScorer()

	testCases := []struct {
		name        string
		volumeUSD   float64
		wantMinBond float64
		wantMinMax  float64 // minimum acceptable max bond
	}{
		{
			name:        "low volume",
			volumeUSD:   10.0,
			wantMinBond: 5.0,
			wantMinMax:  5.0,
		},
		{
			name:        "medium volume",
			volumeUSD:   1000.0,
			wantMinBond: 5.0,
			wantMinMax:  100.0,
		},
		{
			name:        "high volume",
			volumeUSD:   100000.0,
			wantMinBond: 5.0,
			wantMinMax:  400.0,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := scorer.Evaluate(85.0, "elite", 100, 0.97, 20, tc.volumeUSD)
			if !result.Eligible {
				t.Fatalf("expected eligible, got: %s", result.Reason)
			}
			if result.MinBondAmount != tc.wantMinBond {
				t.Errorf("expected min bond %.2f, got %.2f", tc.wantMinBond, result.MinBondAmount)
			}
			if result.MaxBondAmount < tc.wantMinMax {
				t.Errorf("expected max bond >= %.2f, got %.2f", tc.wantMinMax, result.MaxBondAmount)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Service.Apply tests
// ---------------------------------------------------------------------------

func TestService_ApplySuccessful(t *testing.T) {
	store := NewMemoryStore()
	scorer := NewScorer()
	reputation := newMockReputation(85.0, "elite")
	metrics := newMockMetrics(100, 0.97, 20, 1000.0)
	ledger := newMockLedger()
	svc := NewService(store, scorer, reputation, metrics, ledger)

	ctx := context.Background()
	agentAddr := "0x1111111111111111111111111111111111111111"

	v, result, err := svc.Apply(ctx, agentAddr, "10.000000")
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}

	if !result.Eligible {
		t.Fatalf("expected eligible, got: %s", result.Reason)
	}
	if v.Status != StatusActive {
		t.Errorf("expected status active, got %s", v.Status)
	}
	if v.AgentAddr != strings.ToLower(agentAddr) {
		t.Errorf("expected agent addr %s, got %s", strings.ToLower(agentAddr), v.AgentAddr)
	}
	if v.BondAmount != "10.000000" {
		t.Errorf("expected bond 10.000000, got %s", v.BondAmount)
	}
	if v.GuaranteedSuccessRate != 97.0 {
		t.Errorf("expected guaranteed rate 97.0, got %.1f", v.GuaranteedSuccessRate)
	}

	// Verify hold was placed
	if _, ok := ledger.holds[v.BondReference]; !ok {
		t.Error("expected bond hold to be placed on ledger")
	}

	// Verify stored
	stored, err := store.GetByAgent(ctx, agentAddr)
	if err != nil {
		t.Fatalf("GetByAgent failed: %v", err)
	}
	if stored.ID != v.ID {
		t.Error("verification not stored correctly")
	}
}

func TestService_ApplyRejectedNotEligible(t *testing.T) {
	store := NewMemoryStore()
	scorer := NewScorer()
	reputation := newMockReputation(50.0, "emerging")
	metrics := newMockMetrics(10, 0.80, 5, 10.0)
	ledger := newMockLedger()
	svc := NewService(store, scorer, reputation, metrics, ledger)

	ctx := context.Background()
	agentAddr := "0x2222222222222222222222222222222222222222"

	_, result, err := svc.Apply(ctx, agentAddr, "1.000000")
	if err != ErrNotEligible {
		t.Fatalf("expected ErrNotEligible, got: %v", err)
	}
	if result.Eligible {
		t.Fatal("expected result.Eligible = false")
	}

	// Verify no hold placed
	if len(ledger.holds) > 0 {
		t.Error("expected no hold to be placed for ineligible agent")
	}
}

func TestService_ApplyRejectedAlreadyVerified(t *testing.T) {
	store := NewMemoryStore()
	scorer := NewScorer()
	reputation := newMockReputation(85.0, "elite")
	metrics := newMockMetrics(100, 0.97, 20, 1000.0)
	ledger := newMockLedger()
	svc := NewService(store, scorer, reputation, metrics, ledger)

	ctx := context.Background()
	agentAddr := "0x3333333333333333333333333333333333333333"

	// First application succeeds
	_, _, err := svc.Apply(ctx, agentAddr, "10.000000")
	if err != nil {
		t.Fatalf("first Apply failed: %v", err)
	}

	// Second application should fail
	_, _, err = svc.Apply(ctx, agentAddr, "15.000000")
	if err != ErrAlreadyVerified {
		t.Fatalf("expected ErrAlreadyVerified, got: %v", err)
	}
}

func TestService_ApplyBondTooLow(t *testing.T) {
	store := NewMemoryStore()
	scorer := NewScorer()
	reputation := newMockReputation(85.0, "elite")
	metrics := newMockMetrics(100, 0.97, 20, 1000.0)
	ledger := newMockLedger()
	svc := NewService(store, scorer, reputation, metrics, ledger)

	ctx := context.Background()
	agentAddr := "0x4444444444444444444444444444444444444444"

	// Elite tier requires min bond of 5.0 USDC
	_, result, err := svc.Apply(ctx, agentAddr, "2.000000")
	if !errors.Is(err, ErrBondTooLow) {
		t.Fatalf("expected ErrBondTooLow, got: %v", err)
	}
	if result.MinBondAmount != 5.0 {
		t.Errorf("expected result to show min bond 5.0, got %.2f", result.MinBondAmount)
	}
}

func TestService_ApplyLedgerHoldFails(t *testing.T) {
	store := NewMemoryStore()
	scorer := NewScorer()
	reputation := newMockReputation(85.0, "elite")
	metrics := newMockMetrics(100, 0.97, 20, 1000.0)
	ledger := newMockLedger()
	ledger.holdErr = errors.New("insufficient balance")
	svc := NewService(store, scorer, reputation, metrics, ledger)

	ctx := context.Background()
	agentAddr := "0x5555555555555555555555555555555555555555"

	_, _, err := svc.Apply(ctx, agentAddr, "10.000000")
	if err == nil {
		t.Fatal("expected error when ledger hold fails")
	}
	if !strings.Contains(err.Error(), "failed to hold bond") {
		t.Errorf("expected hold error, got: %v", err)
	}

	// Verify nothing was stored
	_, err = store.GetByAgent(ctx, agentAddr)
	if err == nil {
		t.Error("expected verification not to be stored when hold fails")
	}
}

// ---------------------------------------------------------------------------
// Service.Revoke tests
// ---------------------------------------------------------------------------

func TestService_RevokeSuccessful(t *testing.T) {
	store := NewMemoryStore()
	scorer := NewScorer()
	reputation := newMockReputation(85.0, "elite")
	metrics := newMockMetrics(100, 0.97, 20, 1000.0)
	ledger := newMockLedger()
	svc := NewService(store, scorer, reputation, metrics, ledger)

	ctx := context.Background()
	agentAddr := "0x6666666666666666666666666666666666666666"

	// Apply first
	v, _, err := svc.Apply(ctx, agentAddr, "10.000000")
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	bondRef := v.BondReference

	// Revoke
	revoked, err := svc.Revoke(ctx, agentAddr)
	if err != nil {
		t.Fatalf("Revoke failed: %v", err)
	}

	if revoked.Status != StatusRevoked {
		t.Errorf("expected status revoked, got %s", revoked.Status)
	}
	if revoked.RevokedAt == nil {
		t.Error("expected RevokedAt to be set")
	}

	// Verify bond was released
	if _, ok := ledger.releases[bondRef]; !ok {
		t.Error("expected bond to be released")
	}
	if ledger.releases[bondRef] != "10.000000" {
		t.Errorf("expected release amount 10.000000, got %s", ledger.releases[bondRef])
	}
}

func TestService_RevokeNotVerified(t *testing.T) {
	store := NewMemoryStore()
	scorer := NewScorer()
	reputation := newMockReputation(85.0, "elite")
	metrics := newMockMetrics(100, 0.97, 20, 1000.0)
	ledger := newMockLedger()
	svc := NewService(store, scorer, reputation, metrics, ledger)

	ctx := context.Background()
	agentAddr := "0x7777777777777777777777777777777777777777"

	_, err := svc.Revoke(ctx, agentAddr)
	if err != ErrNotVerified {
		t.Fatalf("expected ErrNotVerified, got: %v", err)
	}
}

func TestService_RevokeAlreadyTerminal(t *testing.T) {
	store := NewMemoryStore()
	scorer := NewScorer()
	reputation := newMockReputation(85.0, "elite")
	metrics := newMockMetrics(100, 0.97, 20, 1000.0)
	ledger := newMockLedger()
	svc := NewService(store, scorer, reputation, metrics, ledger)

	ctx := context.Background()
	agentAddr := "0x8888888888888888888888888888888888888888"

	// Apply and revoke
	_, _, err := svc.Apply(ctx, agentAddr, "10.000000")
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	_, err = svc.Revoke(ctx, agentAddr)
	if err != nil {
		t.Fatalf("first Revoke failed: %v", err)
	}

	// Try to revoke again
	_, err = svc.Revoke(ctx, agentAddr)
	if err != ErrInvalidStatus {
		t.Fatalf("expected ErrInvalidStatus, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Service.RecordViolation tests
// ---------------------------------------------------------------------------

func TestService_RecordViolationPartialForfeiture(t *testing.T) {
	store := NewMemoryStore()
	scorer := NewScorer()
	reputation := newMockReputation(85.0, "elite")
	metrics := newMockMetrics(100, 0.97, 20, 1000.0)
	ledger := newMockLedger()
	svc := NewService(store, scorer, reputation, metrics, ledger)

	ctx := context.Background()
	agentAddr := "0x9999999999999999999999999999999999999999"
	fundAddr := "0xfund0000000000000000000000000000000000"

	// Apply (elite tier guarantees 97% success rate)
	v, _, err := svc.Apply(ctx, agentAddr, "100.000000")
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	bondRef := v.BondReference

	// Record violation: actual rate 90% vs guaranteed 97%
	// Shortfall = (97-90)/97 ≈ 7.2%, so forfeit ≈ 7.2 USDC
	violated, err := svc.RecordViolation(ctx, agentAddr, 90.0, fundAddr)
	if err != nil {
		t.Fatalf("RecordViolation failed: %v", err)
	}

	if violated.Status != StatusSuspended {
		t.Errorf("expected status suspended, got %s", violated.Status)
	}
	if violated.ViolationCount != 1 {
		t.Errorf("expected violation count 1, got %d", violated.ViolationCount)
	}
	if violated.LastViolationAt.IsZero() {
		t.Error("expected LastViolationAt to be set")
	}

	// Verify partial forfeiture
	if _, ok := ledger.confirms[bondRef]; !ok {
		t.Error("expected bond confirmation (forfeiture)")
	}

	// Verify deposit to guarantee fund
	if _, ok := ledger.deposits[fundAddr]; !ok {
		t.Error("expected deposit to guarantee fund")
	}

	// Remaining bond should be reduced but not zero
	if violated.BondAmount == "0.000000" {
		t.Error("expected bond to remain after partial forfeiture")
	}
	if violated.BondAmount == "100.000000" {
		t.Error("expected bond to be reduced after forfeiture")
	}
}

func TestService_RecordViolationFullForfeiture(t *testing.T) {
	store := NewMemoryStore()
	scorer := NewScorer()
	reputation := newMockReputation(85.0, "elite")
	metrics := newMockMetrics(100, 0.97, 20, 1000.0)
	ledger := newMockLedger()
	svc := NewService(store, scorer, reputation, metrics, ledger)

	ctx := context.Background()
	agentAddr := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	fundAddr := "0xfund0000000000000000000000000000000000"

	// Apply with small bond
	_, _, err := svc.Apply(ctx, agentAddr, "5.000000")
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}

	// Record severe violation: actual rate 0% vs guaranteed 97%
	// Shortfall = (97-0)/97 = 100%, so full forfeiture
	violated, err := svc.RecordViolation(ctx, agentAddr, 0.0, fundAddr)
	if err != nil {
		t.Fatalf("RecordViolation failed: %v", err)
	}

	if violated.Status != StatusForfeited {
		t.Errorf("expected status forfeited, got %s", violated.Status)
	}
	if violated.BondAmount != "0.000000" {
		t.Errorf("expected bond 0 after full forfeiture, got %s", violated.BondAmount)
	}
}

func TestService_RecordViolationCorrectForfeiture(t *testing.T) {
	store := NewMemoryStore()
	scorer := NewScorer()
	reputation := newMockReputation(65.0, "trusted")
	metrics := newMockMetrics(60, 0.96, 15, 500.0)
	ledger := newMockLedger()
	svc := NewService(store, scorer, reputation, metrics, ledger)

	ctx := context.Background()
	agentAddr := "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	fundAddr := "0xfund0000000000000000000000000000000000"

	// Apply (trusted tier guarantees 95% success rate)
	v, _, err := svc.Apply(ctx, agentAddr, "10.000000")
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	bondRef := v.BondReference

	// Record violation: actual rate 85% vs guaranteed 95%
	// Shortfall = (95-85)/95 ≈ 10.5%, forfeit ≈ 1.05 USDC
	violated, err := svc.RecordViolation(ctx, agentAddr, 85.0, fundAddr)
	if err != nil {
		t.Fatalf("RecordViolation failed: %v", err)
	}

	// Check forfeited amount is approximately 10.5% of 10 USDC
	forfeitedStr := ledger.confirms[bondRef]
	if forfeitedStr == "" {
		t.Fatal("expected forfeiture confirmation")
	}

	// Remaining bond should be approximately 8.95 USDC
	if violated.BondAmount > "9.500000" || violated.BondAmount < "8.500000" {
		t.Errorf("expected remaining bond ~8.95, got %s", violated.BondAmount)
	}
}

func TestService_RecordViolationNotActive(t *testing.T) {
	store := NewMemoryStore()
	scorer := NewScorer()
	reputation := newMockReputation(85.0, "elite")
	metrics := newMockMetrics(100, 0.97, 20, 1000.0)
	ledger := newMockLedger()
	svc := NewService(store, scorer, reputation, metrics, ledger)

	ctx := context.Background()
	agentAddr := "0xcccccccccccccccccccccccccccccccccccccccc"
	fundAddr := "0xfund0000000000000000000000000000000000"

	// Apply and revoke
	_, _, err := svc.Apply(ctx, agentAddr, "10.000000")
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	_, err = svc.Revoke(ctx, agentAddr)
	if err != nil {
		t.Fatalf("Revoke failed: %v", err)
	}

	// Try to record violation on revoked verification
	_, err = svc.RecordViolation(ctx, agentAddr, 80.0, fundAddr)
	if err != ErrInvalidStatus {
		t.Fatalf("expected ErrInvalidStatus, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Service.Reinstate tests
// ---------------------------------------------------------------------------

func TestService_ReinstateSuccessful(t *testing.T) {
	store := NewMemoryStore()
	scorer := NewScorer()
	reputation := newMockReputation(85.0, "elite")
	metrics := newMockMetrics(100, 0.97, 20, 1000.0)
	ledger := newMockLedger()
	svc := NewService(store, scorer, reputation, metrics, ledger)

	ctx := context.Background()
	agentAddr := "0xdddddddddddddddddddddddddddddddddddddddd"
	fundAddr := "0xfund0000000000000000000000000000000000"

	// Apply
	_, _, err := svc.Apply(ctx, agentAddr, "10.000000")
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}

	// Record violation to suspend
	_, err = svc.RecordViolation(ctx, agentAddr, 90.0, fundAddr)
	if err != nil {
		t.Fatalf("RecordViolation failed: %v", err)
	}

	// Verify suspended
	v, _ := svc.GetByAgent(ctx, agentAddr)
	if v.Status != StatusSuspended {
		t.Fatalf("expected suspended status, got %s", v.Status)
	}

	// Reinstate (reputation still meets requirements)
	reinstated, err := svc.Reinstate(ctx, agentAddr)
	if err != nil {
		t.Fatalf("Reinstate failed: %v", err)
	}

	if reinstated.Status != StatusActive {
		t.Errorf("expected status active, got %s", reinstated.Status)
	}
}

func TestService_ReinstateNotSuspended(t *testing.T) {
	store := NewMemoryStore()
	scorer := NewScorer()
	reputation := newMockReputation(85.0, "elite")
	metrics := newMockMetrics(100, 0.97, 20, 1000.0)
	ledger := newMockLedger()
	svc := NewService(store, scorer, reputation, metrics, ledger)

	ctx := context.Background()
	agentAddr := "0xeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"

	// Apply (active, not suspended)
	_, _, err := svc.Apply(ctx, agentAddr, "10.000000")
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}

	// Try to reinstate active verification
	_, err = svc.Reinstate(ctx, agentAddr)
	if err != ErrInvalidStatus {
		t.Fatalf("expected ErrInvalidStatus, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Service.Review tests
// ---------------------------------------------------------------------------

func TestService_ReviewSuspendDueToReputationDrop(t *testing.T) {
	store := NewMemoryStore()
	scorer := NewScorer()
	reputation := newMockReputation(85.0, "elite")
	metrics := newMockMetrics(100, 0.97, 20, 1000.0)
	ledger := newMockLedger()
	svc := NewService(store, scorer, reputation, metrics, ledger)

	ctx := context.Background()
	agentAddr := "0xffffffffffffffffffffffffffffffffffffffff"

	// Apply
	_, _, err := svc.Apply(ctx, agentAddr, "10.000000")
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}

	// Drop reputation below elite minimum
	reputation.setScore(70.0, "trusted")
	metrics.setMetrics(100, 0.90, 20, 1000.0) // also drop success rate

	// Review
	reviewed, result, err := svc.Review(ctx, agentAddr)
	if err != nil {
		t.Fatalf("Review failed: %v", err)
	}

	if reviewed.Status != StatusSuspended {
		t.Errorf("expected status suspended after reputation drop, got %s", reviewed.Status)
	}
	if result.Eligible {
		t.Error("expected result to show ineligible")
	}
}

func TestService_ReviewReactivateFromSuspended(t *testing.T) {
	store := NewMemoryStore()
	scorer := NewScorer()
	reputation := newMockReputation(65.0, "trusted")
	metrics := newMockMetrics(60, 0.96, 15, 500.0)
	ledger := newMockLedger()
	svc := NewService(store, scorer, reputation, metrics, ledger)

	ctx := context.Background()
	agentAddr := "0x0000000000000000000000000000000000000001"
	fundAddr := "0xfund0000000000000000000000000000000000"

	// Apply as trusted
	_, _, err := svc.Apply(ctx, agentAddr, "5.000000")
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}

	// Drop metrics to trigger suspension via violation
	metrics.setMetrics(60, 0.85, 15, 500.0)
	_, err = svc.RecordViolation(ctx, agentAddr, 80.0, fundAddr)
	if err != nil {
		t.Fatalf("RecordViolation failed: %v", err)
	}

	// Verify suspended
	v, _ := svc.GetByAgent(ctx, agentAddr)
	if v.Status != StatusSuspended {
		t.Fatalf("expected suspended, got %s", v.Status)
	}

	// Restore metrics
	metrics.setMetrics(80, 0.97, 20, 800.0)

	// Review should reactivate
	reviewed, result, err := svc.Review(ctx, agentAddr)
	if err != nil {
		t.Fatalf("Review failed: %v", err)
	}

	if reviewed.Status != StatusActive {
		t.Errorf("expected status active after reputation recovery, got %s", reviewed.Status)
	}
	if !result.Eligible {
		t.Error("expected result to show eligible")
	}
}

// ---------------------------------------------------------------------------
// Service.GetGuarantee tests
// ---------------------------------------------------------------------------

func TestService_GetGuaranteeActive(t *testing.T) {
	store := NewMemoryStore()
	scorer := NewScorer()
	reputation := newMockReputation(85.0, "elite")
	metrics := newMockMetrics(100, 0.97, 20, 1000.0)
	ledger := newMockLedger()
	svc := NewService(store, scorer, reputation, metrics, ledger)

	ctx := context.Background()
	agentAddr := "0x0000000000000000000000000000000000000002"

	// Apply
	v, _, err := svc.Apply(ctx, agentAddr, "10.000000")
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}

	// Get guarantee
	guaranteedRate, premiumRate, err := svc.GetGuarantee(ctx, agentAddr)
	if err != nil {
		t.Fatalf("GetGuarantee failed: %v", err)
	}

	if guaranteedRate != v.GuaranteedSuccessRate {
		t.Errorf("expected guaranteed rate %.1f, got %.1f", v.GuaranteedSuccessRate, guaranteedRate)
	}
	if premiumRate != v.GuaranteePremiumRate {
		t.Errorf("expected premium rate %.2f, got %.2f", v.GuaranteePremiumRate, premiumRate)
	}
}

func TestService_GetGuaranteeInactive(t *testing.T) {
	store := NewMemoryStore()
	scorer := NewScorer()
	reputation := newMockReputation(85.0, "elite")
	metrics := newMockMetrics(100, 0.97, 20, 1000.0)
	ledger := newMockLedger()
	svc := NewService(store, scorer, reputation, metrics, ledger)

	ctx := context.Background()
	agentAddr := "0x0000000000000000000000000000000000000003"

	// Apply and revoke
	_, _, err := svc.Apply(ctx, agentAddr, "10.000000")
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	_, err = svc.Revoke(ctx, agentAddr)
	if err != nil {
		t.Fatalf("Revoke failed: %v", err)
	}

	// Get guarantee for revoked agent
	guaranteedRate, premiumRate, err := svc.GetGuarantee(ctx, agentAddr)
	if err != nil {
		t.Fatalf("GetGuarantee failed: %v", err)
	}

	if guaranteedRate != 0 {
		t.Errorf("expected guaranteed rate 0 for inactive, got %.1f", guaranteedRate)
	}
	if premiumRate != 0 {
		t.Errorf("expected premium rate 0 for inactive, got %.2f", premiumRate)
	}
}

// ---------------------------------------------------------------------------
// MemoryStore tests
// ---------------------------------------------------------------------------

func TestMemoryStore_CreateAndGet(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	now := time.Now()
	v := &Verification{
		ID:                    "v_test123",
		AgentAddr:             "0xtest",
		Status:                StatusActive,
		BondAmount:            "10.000000",
		BondReference:         "ref_123",
		GuaranteedSuccessRate: 95.0,
		SLAWindowSize:         20,
		GuaranteePremiumRate:  0.07,
		ReputationScore:       65.0,
		ReputationTier:        "trusted",
		VerifiedAt:            now,
		CreatedAt:             now,
		UpdatedAt:             now,
	}

	err := store.Create(ctx, v)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Get by ID
	fetched, err := store.Get(ctx, "v_test123")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if fetched.AgentAddr != "0xtest" {
		t.Errorf("expected agent 0xtest, got %s", fetched.AgentAddr)
	}

	// Get by agent
	byAgent, err := store.GetByAgent(ctx, "0xTEST") // case insensitive
	if err != nil {
		t.Fatalf("GetByAgent failed: %v", err)
	}
	if byAgent.ID != "v_test123" {
		t.Errorf("expected ID v_test123, got %s", byAgent.ID)
	}
}

func TestMemoryStore_GetNotFound(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	_, err := store.Get(ctx, "nonexistent")
	if err != ErrVerificationFound {
		t.Fatalf("expected ErrVerificationFound, got: %v", err)
	}

	_, err = store.GetByAgent(ctx, "0xnonexistent")
	if err != ErrVerificationFound {
		t.Fatalf("expected ErrVerificationFound, got: %v", err)
	}
}

func TestMemoryStore_Update(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	v := &Verification{
		ID:        "v_update",
		AgentAddr: "0xupdate",
		Status:    StatusActive,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	err := store.Create(ctx, v)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Update status
	v.Status = StatusSuspended
	v.ViolationCount = 1
	err = store.Update(ctx, v)
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	// Fetch and verify
	fetched, err := store.Get(ctx, "v_update")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if fetched.Status != StatusSuspended {
		t.Errorf("expected status suspended, got %s", fetched.Status)
	}
	if fetched.ViolationCount != 1 {
		t.Errorf("expected violation count 1, got %d", fetched.ViolationCount)
	}
}

func TestMemoryStore_ListActive(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	// Create 3 verifications: 2 active, 1 revoked
	for i := 0; i < 3; i++ {
		status := StatusActive
		if i == 2 {
			status = StatusRevoked
		}
		v := &Verification{
			ID:        "v_" + string(rune('a'+i)),
			AgentAddr: "0x000" + string(rune('1'+i)),
			Status:    status,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		err := store.Create(ctx, v)
		if err != nil {
			t.Fatalf("Create %d failed: %v", i, err)
		}
	}

	active, err := store.ListActive(ctx, 10)
	if err != nil {
		t.Fatalf("ListActive failed: %v", err)
	}

	if len(active) != 2 {
		t.Errorf("expected 2 active verifications, got %d", len(active))
	}
	for _, v := range active {
		if v.Status != StatusActive {
			t.Errorf("expected only active verifications, got %s", v.Status)
		}
	}
}

func TestMemoryStore_IsVerified(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	// Create active verification
	v := &Verification{
		ID:        "v_verified",
		AgentAddr: "0xverified",
		Status:    StatusActive,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	err := store.Create(ctx, v)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Check verified
	isVerified, err := store.IsVerified(ctx, "0xverified")
	if err != nil {
		t.Fatalf("IsVerified failed: %v", err)
	}
	if !isVerified {
		t.Error("expected agent to be verified")
	}

	// Revoke
	v.Status = StatusRevoked
	err = store.Update(ctx, v)
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	// Check not verified after revocation
	isVerified, err = store.IsVerified(ctx, "0xverified")
	if err != nil {
		t.Fatalf("IsVerified failed: %v", err)
	}
	if isVerified {
		t.Error("expected agent not to be verified after revocation")
	}

	// Check non-existent agent
	isVerified, err = store.IsVerified(ctx, "0xnonexistent")
	if err != nil {
		t.Fatalf("IsVerified failed: %v", err)
	}
	if isVerified {
		t.Error("expected non-existent agent not to be verified")
	}
}

// ---------------------------------------------------------------------------
// Enforcer tests
// ---------------------------------------------------------------------------

func TestEnforcer_SkipIfWindowNotFull(t *testing.T) {
	store := NewMemoryStore()
	scorer := NewScorer()
	reputation := newMockReputation(85.0, "elite")
	metrics := newMockMetrics(100, 0.97, 20, 1000.0)
	ledger := newMockLedger()
	svc := NewService(store, scorer, reputation, metrics, ledger)

	ctx := context.Background()
	agentAddr := "0x0000000000000000000000000000000000000004"

	// Apply (SLA window size is 20)
	_, _, err := svc.Apply(ctx, agentAddr, "10.000000")
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}

	// Only 10 calls in window (less than 20)
	callProvider := newMockCallProvider(8, 10)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	enforcer := NewEnforcer(svc, callProvider, "0xfund", logger)

	// Check agent
	v, _ := store.GetByAgent(ctx, agentAddr)
	enforcer.checkAgent(ctx, v)

	// Verify no violation recorded (window not full)
	updated, _ := store.GetByAgent(ctx, agentAddr)
	if updated.ViolationCount != 0 {
		t.Errorf("expected no violation when window not full, got %d violations", updated.ViolationCount)
	}
	if updated.Status != StatusActive {
		t.Errorf("expected status to remain active, got %s", updated.Status)
	}
}

func TestEnforcer_TriggerViolationWhenSuccessRateDrops(t *testing.T) {
	store := NewMemoryStore()
	scorer := NewScorer()
	reputation := newMockReputation(85.0, "elite")
	metrics := newMockMetrics(100, 0.97, 20, 1000.0)
	ledger := newMockLedger()
	svc := NewService(store, scorer, reputation, metrics, ledger)

	ctx := context.Background()
	agentAddr := "0x0000000000000000000000000000000000000005"
	fundAddr := "0xfund0000000000000000000000000000000000"

	// Apply (elite guarantees 97% success rate, window size 20)
	_, _, err := svc.Apply(ctx, agentAddr, "50.000000")
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}

	// Window is full with 20 calls, but only 18 succeeded (90% success rate)
	callProvider := newMockCallProvider(18, 20)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	enforcer := NewEnforcer(svc, callProvider, fundAddr, logger)

	// Check agent
	v, _ := store.GetByAgent(ctx, agentAddr)
	enforcer.checkAgent(ctx, v)

	// Verify violation was recorded
	updated, _ := store.GetByAgent(ctx, agentAddr)
	if updated.ViolationCount != 1 {
		t.Errorf("expected 1 violation, got %d", updated.ViolationCount)
	}
	if updated.Status != StatusSuspended {
		t.Errorf("expected status suspended, got %s", updated.Status)
	}

	// Verify bond was forfeited
	if len(ledger.confirms) == 0 {
		t.Error("expected bond forfeiture confirmation")
	}
}

func TestEnforcer_NoViolationWhenRateMeetsGuarantee(t *testing.T) {
	store := NewMemoryStore()
	scorer := NewScorer()
	reputation := newMockReputation(85.0, "elite")
	metrics := newMockMetrics(100, 0.97, 20, 1000.0)
	ledger := newMockLedger()
	svc := NewService(store, scorer, reputation, metrics, ledger)

	ctx := context.Background()
	agentAddr := "0x0000000000000000000000000000000000000006"
	fundAddr := "0xfund0000000000000000000000000000000000"

	// Apply (elite guarantees 97% success rate)
	_, _, err := svc.Apply(ctx, agentAddr, "50.000000")
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}

	// Window full with 20 calls, 20 succeeded (100% success rate)
	callProvider := newMockCallProvider(20, 20)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	enforcer := NewEnforcer(svc, callProvider, fundAddr, logger)

	// Check agent
	v, _ := store.GetByAgent(ctx, agentAddr)
	enforcer.checkAgent(ctx, v)

	// Verify no violation
	updated, _ := store.GetByAgent(ctx, agentAddr)
	if updated.ViolationCount != 0 {
		t.Errorf("expected no violation when rate meets guarantee, got %d violations", updated.ViolationCount)
	}
	if updated.Status != StatusActive {
		t.Errorf("expected status to remain active, got %s", updated.Status)
	}

	// Verify no forfeiture
	if len(ledger.confirms) > 0 {
		t.Error("expected no bond forfeiture when rate meets guarantee")
	}
}
