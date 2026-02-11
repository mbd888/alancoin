package negotiation

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// mockReputation returns a fixed score for any address.
type mockReputation struct {
	scores map[string]float64
}

func (m *mockReputation) GetScore(_ context.Context, address string) (float64, string, error) {
	addr := strings.ToLower(address)
	if score, ok := m.scores[addr]; ok {
		return score, "established", nil
	}
	return 0, "new", nil
}

// mockContractFormer records contract formation calls.
type mockContractFormer struct {
	calls     []formContractCall
	returnErr error
}

type formContractCall struct {
	RFPID      string
	BidID      string
	BuyerAddr  string
	SellerAddr string
}

func (m *mockContractFormer) FormContract(_ context.Context, rfp *RFP, bid *Bid) (string, error) {
	m.calls = append(m.calls, formContractCall{
		RFPID:      rfp.ID,
		BidID:      bid.ID,
		BuyerAddr:  rfp.BuyerAddr,
		SellerAddr: bid.SellerAddr,
	})
	if m.returnErr != nil {
		return "", m.returnErr
	}
	return "contract_test123", nil
}

// mockLedger records hold/confirm/release calls for testing bid bonds.
type mockLedger struct {
	holds   map[string]string // ref -> amount
	holdErr error
	mu      sync.Mutex
}

func newMockLedger() *mockLedger {
	return &mockLedger{holds: make(map[string]string)}
}

func (m *mockLedger) Hold(_ context.Context, _, amount, reference string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.holdErr != nil {
		return m.holdErr
	}
	m.holds[reference] = amount
	return nil
}

func (m *mockLedger) ConfirmHold(_ context.Context, _, _, reference string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.holds, reference)
	return nil
}

func (m *mockLedger) ReleaseHold(_ context.Context, _, _, reference string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.holds, reference)
	return nil
}

func (m *mockLedger) Deposit(_ context.Context, _, _, _ string) error {
	return nil
}

func (m *mockLedger) heldCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.holds)
}

func newTestService() (*Service, *MemoryStore, *mockReputation) {
	store := NewMemoryStore()
	rep := &mockReputation{scores: map[string]float64{
		"0xseller1": 80,
		"0xseller2": 60,
		"0xseller3": 40,
	}}
	svc := NewService(store, rep)
	return svc, store, rep
}

func publishTestRFP(t *testing.T, svc *Service) *RFP {
	t.Helper()
	rfp, err := svc.PublishRFP(context.Background(), PublishRFPRequest{
		BuyerAddr:   "0xBuyer",
		ServiceType: "translation",
		Description: "Need translation service",
		MinBudget:   "0.50",
		MaxBudget:   "1.00",
		Duration:    "7d",
		BidDeadline: "24h",
	})
	if err != nil {
		t.Fatalf("failed to publish RFP: %v", err)
	}
	return rfp
}

// --- Publish RFP tests ---

func TestPublishRFP(t *testing.T) {
	svc, _, _ := newTestService()
	rfp := publishTestRFP(t, svc)

	if !strings.HasPrefix(rfp.ID, "rfp_") {
		t.Errorf("expected ID prefix rfp_, got %s", rfp.ID)
	}
	if rfp.BuyerAddr != "0xbuyer" {
		t.Errorf("expected lowercase buyer addr, got %s", rfp.BuyerAddr)
	}
	if rfp.Status != RFPStatusOpen {
		t.Errorf("expected status open, got %s", rfp.Status)
	}
	if rfp.ServiceType != "translation" {
		t.Errorf("expected service type translation, got %s", rfp.ServiceType)
	}
	if rfp.MaxCounterRounds != 3 {
		t.Errorf("expected default max counter rounds 3, got %d", rfp.MaxCounterRounds)
	}
	if rfp.MinSuccessRate != 95.0 {
		t.Errorf("expected default min success rate 95, got %f", rfp.MinSuccessRate)
	}
	if rfp.BidDeadline.Before(time.Now()) {
		t.Error("expected deadline in the future")
	}
}

func TestPublishRFP_InvalidBudget(t *testing.T) {
	svc, _, _ := newTestService()

	_, err := svc.PublishRFP(context.Background(), PublishRFPRequest{
		BuyerAddr:   "0xBuyer",
		ServiceType: "translation",
		MinBudget:   "2.00",
		MaxBudget:   "1.00", // max < min
		Duration:    "7d",
		BidDeadline: "24h",
	})
	if err == nil {
		t.Error("expected error for invalid budget range")
	}
}

func TestPublishRFP_CustomScoringWeights(t *testing.T) {
	svc, _, _ := newTestService()

	weights := ScoringWeights{Price: 0.5, Reputation: 0.3, SLA: 0.2}
	rfp, err := svc.PublishRFP(context.Background(), PublishRFPRequest{
		BuyerAddr:      "0xBuyer",
		ServiceType:    "translation",
		MinBudget:      "0.50",
		MaxBudget:      "1.00",
		Duration:       "7d",
		BidDeadline:    "24h",
		ScoringWeights: &weights,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rfp.ScoringWeights.Price != 0.5 {
		t.Errorf("expected price weight 0.5, got %f", rfp.ScoringWeights.Price)
	}
}

func TestPublishRFP_InvalidDeadline(t *testing.T) {
	svc, _, _ := newTestService()

	_, err := svc.PublishRFP(context.Background(), PublishRFPRequest{
		BuyerAddr:   "0xBuyer",
		ServiceType: "translation",
		MinBudget:   "0.50",
		MaxBudget:   "1.00",
		Duration:    "7d",
		BidDeadline: "not-a-deadline",
	})
	if err == nil {
		t.Error("expected error for invalid deadline")
	}
}

// --- Place Bid tests ---

func TestPlaceBid(t *testing.T) {
	svc, _, _ := newTestService()
	rfp := publishTestRFP(t, svc)

	bid, err := svc.PlaceBid(context.Background(), rfp.ID, PlaceBidRequest{
		SellerAddr:   "0xSeller1",
		PricePerCall: "0.005",
		TotalBudget:  "0.75",
		Duration:     "7d",
		SuccessRate:  98,
		Message:      "I can do it",
	})
	if err != nil {
		t.Fatalf("failed to place bid: %v", err)
	}

	if !strings.HasPrefix(bid.ID, "bid_") {
		t.Errorf("expected ID prefix bid_, got %s", bid.ID)
	}
	if bid.SellerAddr != "0xseller1" {
		t.Errorf("expected lowercase seller addr, got %s", bid.SellerAddr)
	}
	if bid.Status != BidStatusPending {
		t.Errorf("expected status pending, got %s", bid.Status)
	}
	if bid.Score <= 0 {
		t.Errorf("expected positive score, got %f", bid.Score)
	}
	if bid.Message != "I can do it" {
		t.Errorf("expected message, got %s", bid.Message)
	}

	// Check RFP bid count was updated
	updated, _ := svc.Get(context.Background(), rfp.ID)
	if updated.BidCount != 1 {
		t.Errorf("expected bid count 1, got %d", updated.BidCount)
	}
}

func TestPlaceBid_SelfBid(t *testing.T) {
	svc, _, _ := newTestService()
	rfp := publishTestRFP(t, svc)

	_, err := svc.PlaceBid(context.Background(), rfp.ID, PlaceBidRequest{
		SellerAddr:   "0xBuyer", // Same as buyer
		PricePerCall: "0.005",
		TotalBudget:  "0.75",
		Duration:     "7d",
	})
	if !errors.Is(err, ErrSelfBid) {
		t.Errorf("expected ErrSelfBid, got %v", err)
	}
}

func TestPlaceBid_DuplicateBid(t *testing.T) {
	svc, _, _ := newTestService()
	rfp := publishTestRFP(t, svc)

	_, err := svc.PlaceBid(context.Background(), rfp.ID, PlaceBidRequest{
		SellerAddr:   "0xSeller1",
		PricePerCall: "0.005",
		TotalBudget:  "0.75",
		Duration:     "7d",
	})
	if err != nil {
		t.Fatalf("first bid failed: %v", err)
	}

	_, err = svc.PlaceBid(context.Background(), rfp.ID, PlaceBidRequest{
		SellerAddr:   "0xSeller1", // Same seller
		PricePerCall: "0.004",
		TotalBudget:  "0.70",
		Duration:     "7d",
	})
	if !errors.Is(err, ErrDuplicateBid) {
		t.Errorf("expected ErrDuplicateBid, got %v", err)
	}
}

func TestPlaceBid_BudgetOutOfRange(t *testing.T) {
	svc, _, _ := newTestService()
	rfp := publishTestRFP(t, svc) // min=0.50, max=1.00

	// Below minimum
	_, err := svc.PlaceBid(context.Background(), rfp.ID, PlaceBidRequest{
		SellerAddr:   "0xSeller1",
		PricePerCall: "0.005",
		TotalBudget:  "0.10", // Below 0.50
		Duration:     "7d",
	})
	if !errors.Is(err, ErrBudgetOutOfRange) {
		t.Errorf("expected ErrBudgetOutOfRange, got %v", err)
	}

	// Above maximum
	_, err = svc.PlaceBid(context.Background(), rfp.ID, PlaceBidRequest{
		SellerAddr:   "0xSeller2",
		PricePerCall: "0.005",
		TotalBudget:  "5.00", // Above 1.00
		Duration:     "7d",
	})
	if !errors.Is(err, ErrBudgetOutOfRange) {
		t.Errorf("expected ErrBudgetOutOfRange, got %v", err)
	}
}

func TestPlaceBid_LowReputation(t *testing.T) {
	svc, _, _ := newTestService()

	// RFP with minimum reputation requirement
	rfp, err := svc.PublishRFP(context.Background(), PublishRFPRequest{
		BuyerAddr:     "0xBuyer",
		ServiceType:   "translation",
		MinBudget:     "0.50",
		MaxBudget:     "1.00",
		Duration:      "7d",
		BidDeadline:   "24h",
		MinReputation: 50,
	})
	if err != nil {
		t.Fatalf("failed to publish RFP: %v", err)
	}

	// seller3 has rep=40, below min=50
	_, err = svc.PlaceBid(context.Background(), rfp.ID, PlaceBidRequest{
		SellerAddr:   "0xSeller3",
		PricePerCall: "0.005",
		TotalBudget:  "0.75",
		Duration:     "7d",
	})
	if !errors.Is(err, ErrLowReputation) {
		t.Errorf("expected ErrLowReputation, got %v", err)
	}
}

func TestPlaceBid_ExpiredDeadline(t *testing.T) {
	svc, store, _ := newTestService()
	rfp := publishTestRFP(t, svc)

	// Manually set deadline to the past
	store.mu.Lock()
	stored := store.rfps[rfp.ID]
	stored.BidDeadline = time.Now().Add(-1 * time.Hour)
	store.mu.Unlock()

	_, err := svc.PlaceBid(context.Background(), rfp.ID, PlaceBidRequest{
		SellerAddr:   "0xSeller1",
		PricePerCall: "0.005",
		TotalBudget:  "0.75",
		Duration:     "7d",
	})
	if !errors.Is(err, ErrBidDeadlinePast) {
		t.Errorf("expected ErrBidDeadlinePast, got %v", err)
	}
}

func TestPlaceBid_CancelledRFP(t *testing.T) {
	svc, _, _ := newTestService()
	rfp := publishTestRFP(t, svc)

	// Cancel the RFP first
	svc.CancelRFP(context.Background(), rfp.ID, "0xBuyer", "no longer needed")

	_, err := svc.PlaceBid(context.Background(), rfp.ID, PlaceBidRequest{
		SellerAddr:   "0xSeller1",
		PricePerCall: "0.005",
		TotalBudget:  "0.75",
		Duration:     "7d",
	})
	if !errors.Is(err, ErrInvalidStatus) {
		t.Errorf("expected ErrInvalidStatus for cancelled RFP, got %v", err)
	}
}

func TestPlaceBid_RFPNotFound(t *testing.T) {
	svc, _, _ := newTestService()

	_, err := svc.PlaceBid(context.Background(), "rfp_nonexistent", PlaceBidRequest{
		SellerAddr:   "0xSeller1",
		PricePerCall: "0.005",
		TotalBudget:  "0.75",
		Duration:     "7d",
	})
	if !errors.Is(err, ErrRFPNotFound) {
		t.Errorf("expected ErrRFPNotFound, got %v", err)
	}
}

// --- Counter tests ---

func TestCounter(t *testing.T) {
	svc, _, _ := newTestService()
	rfp := publishTestRFP(t, svc)

	bid, _ := svc.PlaceBid(context.Background(), rfp.ID, PlaceBidRequest{
		SellerAddr:   "0xSeller1",
		PricePerCall: "0.005",
		TotalBudget:  "0.75",
		Duration:     "7d",
		SuccessRate:  95,
	})

	// Buyer counters
	counter, err := svc.Counter(context.Background(), rfp.ID, bid.ID, "0xBuyer", CounterRequest{
		PricePerCall: "0.004",
		Message:      "Can you lower?",
	})
	if err != nil {
		t.Fatalf("failed to counter: %v", err)
	}

	if counter.PricePerCall != "0.004" {
		t.Errorf("expected counter price 0.004, got %s", counter.PricePerCall)
	}
	if counter.TotalBudget != "0.75" {
		t.Errorf("expected total budget to carry over, got %s", counter.TotalBudget)
	}
	if counter.CounterRound != 1 {
		t.Errorf("expected counter round 1, got %d", counter.CounterRound)
	}
	if counter.ParentBidID != bid.ID {
		t.Errorf("expected parent bid ID %s, got %s", bid.ID, counter.ParentBidID)
	}

	// Original bid should be countered
	original, _ := svc.GetBid(context.Background(), bid.ID)
	if original.Status != BidStatusCountered {
		t.Errorf("expected original bid status countered, got %s", original.Status)
	}
	if original.CounteredByID != counter.ID {
		t.Errorf("expected countered by ID %s, got %s", counter.ID, original.CounteredByID)
	}
}

func TestCounter_MaxRounds(t *testing.T) {
	svc, _, _ := newTestService()

	rfp, _ := svc.PublishRFP(context.Background(), PublishRFPRequest{
		BuyerAddr:        "0xBuyer",
		ServiceType:      "translation",
		MinBudget:        "0.50",
		MaxBudget:        "1.00",
		Duration:         "7d",
		BidDeadline:      "24h",
		MaxCounterRounds: 2,
	})

	bid, _ := svc.PlaceBid(context.Background(), rfp.ID, PlaceBidRequest{
		SellerAddr:   "0xSeller1",
		PricePerCall: "0.005",
		TotalBudget:  "0.75",
		Duration:     "7d",
	})

	// Counter 1 (buyer)
	c1, err := svc.Counter(context.Background(), rfp.ID, bid.ID, "0xBuyer", CounterRequest{PricePerCall: "0.004"})
	if err != nil {
		t.Fatalf("counter 1 failed: %v", err)
	}

	// Counter 2 (seller)
	c2, err := svc.Counter(context.Background(), rfp.ID, c1.ID, "0xSeller1", CounterRequest{PricePerCall: "0.0045"})
	if err != nil {
		t.Fatalf("counter 2 failed: %v", err)
	}

	// Counter 3 should fail (max=2)
	_, err = svc.Counter(context.Background(), rfp.ID, c2.ID, "0xBuyer", CounterRequest{PricePerCall: "0.004"})
	if !errors.Is(err, ErrMaxCounterRounds) {
		t.Errorf("expected ErrMaxCounterRounds, got %v", err)
	}
}

func TestCounter_BySeller(t *testing.T) {
	svc, _, _ := newTestService()
	rfp := publishTestRFP(t, svc)

	bid, _ := svc.PlaceBid(context.Background(), rfp.ID, PlaceBidRequest{
		SellerAddr:   "0xSeller1",
		PricePerCall: "0.005",
		TotalBudget:  "0.75",
		Duration:     "7d",
		SuccessRate:  95,
	})

	// Seller counters their own bid
	counter, err := svc.Counter(context.Background(), rfp.ID, bid.ID, "0xSeller1", CounterRequest{
		PricePerCall: "0.004",
		SuccessRate:  99,
		Message:      "I can do better",
	})
	if err != nil {
		t.Fatalf("seller counter failed: %v", err)
	}

	if counter.PricePerCall != "0.004" {
		t.Errorf("expected counter price 0.004, got %s", counter.PricePerCall)
	}
	if counter.SuccessRate != 99 {
		t.Errorf("expected counter success rate 99, got %f", counter.SuccessRate)
	}
	if counter.SellerAddr != "0xseller1" {
		t.Errorf("expected seller addr preserved, got %s", counter.SellerAddr)
	}
}

func TestCounter_Unauthorized(t *testing.T) {
	svc, _, _ := newTestService()
	rfp := publishTestRFP(t, svc)

	bid, _ := svc.PlaceBid(context.Background(), rfp.ID, PlaceBidRequest{
		SellerAddr:   "0xSeller1",
		PricePerCall: "0.005",
		TotalBudget:  "0.75",
		Duration:     "7d",
	})

	// Random address tries to counter
	_, err := svc.Counter(context.Background(), rfp.ID, bid.ID, "0xRandom", CounterRequest{
		PricePerCall: "0.004",
	})
	if !errors.Is(err, ErrUnauthorized) {
		t.Errorf("expected ErrUnauthorized, got %v", err)
	}
}

// --- Select Winner tests ---

func TestSelectWinner(t *testing.T) {
	svc, _, _ := newTestService()
	cf := &mockContractFormer{}
	svc.WithContractFormer(cf)

	rfp := publishTestRFP(t, svc)

	bid1, _ := svc.PlaceBid(context.Background(), rfp.ID, PlaceBidRequest{
		SellerAddr:   "0xSeller1",
		PricePerCall: "0.005",
		TotalBudget:  "0.75",
		Duration:     "7d",
	})
	bid2, _ := svc.PlaceBid(context.Background(), rfp.ID, PlaceBidRequest{
		SellerAddr:   "0xSeller2",
		PricePerCall: "0.006",
		TotalBudget:  "0.80",
		Duration:     "7d",
	})

	// Select bid1 as winner
	updatedRFP, winningBid, err := svc.SelectWinner(context.Background(), rfp.ID, bid1.ID, "0xBuyer")
	if err != nil {
		t.Fatalf("failed to select winner: %v", err)
	}

	if updatedRFP.Status != RFPStatusAwarded {
		t.Errorf("expected status awarded, got %s", updatedRFP.Status)
	}
	if updatedRFP.WinningBidID != bid1.ID {
		t.Errorf("expected winning bid ID %s, got %s", bid1.ID, updatedRFP.WinningBidID)
	}
	if updatedRFP.ContractID != "contract_test123" {
		t.Errorf("expected contract ID contract_test123, got %s", updatedRFP.ContractID)
	}
	if updatedRFP.AwardedAt == nil {
		t.Error("expected awarded_at to be set")
	}
	if winningBid.Status != BidStatusAccepted {
		t.Errorf("expected winning bid status accepted, got %s", winningBid.Status)
	}

	// Contract should have been formed
	if len(cf.calls) != 1 {
		t.Fatalf("expected 1 contract formation, got %d", len(cf.calls))
	}

	// Losing bid should be rejected
	loser, _ := svc.GetBid(context.Background(), bid2.ID)
	if loser.Status != BidStatusRejected {
		t.Errorf("expected losing bid status rejected, got %s", loser.Status)
	}
}

func TestSelectWinner_Unauthorized(t *testing.T) {
	svc, _, _ := newTestService()
	rfp := publishTestRFP(t, svc)

	bid, _ := svc.PlaceBid(context.Background(), rfp.ID, PlaceBidRequest{
		SellerAddr:   "0xSeller1",
		PricePerCall: "0.005",
		TotalBudget:  "0.75",
		Duration:     "7d",
	})

	// Seller tries to select (only buyer can)
	_, _, err := svc.SelectWinner(context.Background(), rfp.ID, bid.ID, "0xSeller1")
	if !errors.Is(err, ErrUnauthorized) {
		t.Errorf("expected ErrUnauthorized, got %v", err)
	}
}

func TestSelectWinner_CounteredBid(t *testing.T) {
	svc, _, _ := newTestService()
	rfp := publishTestRFP(t, svc)

	bid, _ := svc.PlaceBid(context.Background(), rfp.ID, PlaceBidRequest{
		SellerAddr:   "0xSeller1",
		PricePerCall: "0.005",
		TotalBudget:  "0.75",
		Duration:     "7d",
	})

	// Counter the bid (making it status=countered)
	svc.Counter(context.Background(), rfp.ID, bid.ID, "0xBuyer", CounterRequest{
		PricePerCall: "0.004",
	})

	// Try to select the countered bid
	_, _, err := svc.SelectWinner(context.Background(), rfp.ID, bid.ID, "0xBuyer")
	if !errors.Is(err, ErrInvalidStatus) {
		t.Errorf("expected ErrInvalidStatus for countered bid, got %v", err)
	}
}

func TestSelectWinner_AlreadyAwarded(t *testing.T) {
	svc, _, _ := newTestService()
	rfp := publishTestRFP(t, svc)

	bid1, _ := svc.PlaceBid(context.Background(), rfp.ID, PlaceBidRequest{
		SellerAddr:   "0xSeller1",
		PricePerCall: "0.005",
		TotalBudget:  "0.75",
		Duration:     "7d",
	})

	_, _, err := svc.SelectWinner(context.Background(), rfp.ID, bid1.ID, "0xBuyer")
	if err != nil {
		t.Fatalf("first select failed: %v", err)
	}

	// Try to select again
	_, _, err = svc.SelectWinner(context.Background(), rfp.ID, bid1.ID, "0xBuyer")
	if !errors.Is(err, ErrAlreadyAwarded) {
		t.Errorf("expected ErrAlreadyAwarded, got %v", err)
	}
}

func TestSelectWinner_ContractFormerError(t *testing.T) {
	svc, _, _ := newTestService()
	cf := &mockContractFormer{returnErr: errors.New("contract service down")}
	svc.WithContractFormer(cf)

	rfp := publishTestRFP(t, svc)

	bid, _ := svc.PlaceBid(context.Background(), rfp.ID, PlaceBidRequest{
		SellerAddr:   "0xSeller1",
		PricePerCall: "0.005",
		TotalBudget:  "0.75",
		Duration:     "7d",
	})

	// Select should succeed even if contract formation fails (non-fatal)
	updatedRFP, _, err := svc.SelectWinner(context.Background(), rfp.ID, bid.ID, "0xBuyer")
	if err != nil {
		t.Fatalf("expected select to succeed despite contract error, got %v", err)
	}

	if updatedRFP.Status != RFPStatusAwarded {
		t.Errorf("expected status awarded, got %s", updatedRFP.Status)
	}
	// Contract ID should be empty since formation failed
	if updatedRFP.ContractID != "" {
		t.Errorf("expected empty contract ID, got %s", updatedRFP.ContractID)
	}
}

// --- Auto-select tests ---

func TestAutoSelect(t *testing.T) {
	svc, _, _ := newTestService()
	cf := &mockContractFormer{}
	svc.WithContractFormer(cf)

	rfp, _ := svc.PublishRFP(context.Background(), PublishRFPRequest{
		BuyerAddr:   "0xBuyer",
		ServiceType: "translation",
		MinBudget:   "0.50",
		MaxBudget:   "1.00",
		Duration:    "7d",
		BidDeadline: "24h",
		AutoSelect:  true,
	})

	// seller1 has rep=80, seller2 has rep=60
	svc.PlaceBid(context.Background(), rfp.ID, PlaceBidRequest{
		SellerAddr:   "0xSeller1",
		PricePerCall: "0.005",
		TotalBudget:  "0.75",
		Duration:     "7d",
		SuccessRate:  98,
	})
	svc.PlaceBid(context.Background(), rfp.ID, PlaceBidRequest{
		SellerAddr:   "0xSeller2",
		PricePerCall: "0.004",
		TotalBudget:  "0.70",
		Duration:     "7d",
		SuccessRate:  95,
	})

	updatedRFP, winningBid, err := svc.AutoSelect(context.Background(), rfp.ID)
	if err != nil {
		t.Fatalf("auto-select failed: %v", err)
	}

	if updatedRFP.Status != RFPStatusAwarded {
		t.Errorf("expected status awarded, got %s", updatedRFP.Status)
	}

	// seller1 should win: higher reputation (80 vs 60) and good SLA
	if winningBid.SellerAddr != "0xseller1" {
		t.Errorf("expected seller1 to win (higher reputation), got %s", winningBid.SellerAddr)
	}
}

func TestAutoSelect_NoBids(t *testing.T) {
	svc, _, _ := newTestService()

	rfp, _ := svc.PublishRFP(context.Background(), PublishRFPRequest{
		BuyerAddr:   "0xBuyer",
		ServiceType: "translation",
		MinBudget:   "0.50",
		MaxBudget:   "1.00",
		Duration:    "7d",
		BidDeadline: "24h",
		AutoSelect:  true,
	})

	_, _, err := svc.AutoSelect(context.Background(), rfp.ID)
	if !errors.Is(err, ErrNoBids) {
		t.Errorf("expected ErrNoBids, got %v", err)
	}
}

// --- Cancel RFP tests ---

func TestCancelRFP(t *testing.T) {
	svc, _, _ := newTestService()
	rfp := publishTestRFP(t, svc)

	bid, _ := svc.PlaceBid(context.Background(), rfp.ID, PlaceBidRequest{
		SellerAddr:   "0xSeller1",
		PricePerCall: "0.005",
		TotalBudget:  "0.75",
		Duration:     "7d",
	})

	cancelled, err := svc.CancelRFP(context.Background(), rfp.ID, "0xBuyer", "Changed plans")
	if err != nil {
		t.Fatalf("failed to cancel: %v", err)
	}

	if cancelled.Status != RFPStatusCancelled {
		t.Errorf("expected status cancelled, got %s", cancelled.Status)
	}
	if cancelled.CancelReason != "Changed plans" {
		t.Errorf("expected cancel reason, got %s", cancelled.CancelReason)
	}

	// Bid should be rejected
	rejectedBid, _ := svc.GetBid(context.Background(), bid.ID)
	if rejectedBid.Status != BidStatusRejected {
		t.Errorf("expected bid status rejected, got %s", rejectedBid.Status)
	}
}

func TestCancelRFP_NoBids(t *testing.T) {
	svc, _, _ := newTestService()
	rfp := publishTestRFP(t, svc)

	cancelled, err := svc.CancelRFP(context.Background(), rfp.ID, "0xBuyer", "No interest")
	if err != nil {
		t.Fatalf("failed to cancel: %v", err)
	}

	if cancelled.Status != RFPStatusCancelled {
		t.Errorf("expected status cancelled, got %s", cancelled.Status)
	}
}

func TestCancelRFP_Unauthorized(t *testing.T) {
	svc, _, _ := newTestService()
	rfp := publishTestRFP(t, svc)

	_, err := svc.CancelRFP(context.Background(), rfp.ID, "0xNotBuyer", "")
	if !errors.Is(err, ErrUnauthorized) {
		t.Errorf("expected ErrUnauthorized, got %v", err)
	}
}

func TestCancelRFP_AlreadyAwarded(t *testing.T) {
	svc, _, _ := newTestService()
	rfp := publishTestRFP(t, svc)

	bid, _ := svc.PlaceBid(context.Background(), rfp.ID, PlaceBidRequest{
		SellerAddr:   "0xSeller1",
		PricePerCall: "0.005",
		TotalBudget:  "0.75",
		Duration:     "7d",
	})

	svc.SelectWinner(context.Background(), rfp.ID, bid.ID, "0xBuyer")

	_, err := svc.CancelRFP(context.Background(), rfp.ID, "0xBuyer", "Too late")
	if !errors.Is(err, ErrAlreadyAwarded) {
		t.Errorf("expected ErrAlreadyAwarded, got %v", err)
	}
}

// --- Scoring tests ---

func TestScoreBid(t *testing.T) {
	rfp := &RFP{
		MaxBudget: "1.00",
		ScoringWeights: ScoringWeights{
			Price:      0.30,
			Reputation: 0.40,
			SLA:        0.30,
		},
	}

	bid := &Bid{
		TotalBudget: "0.50",
		SuccessRate: 95.0,
	}

	// price_score = 1 - (0.50/1.00) = 0.50
	// rep_score = 80/100 = 0.80
	// sla_score = 95/100 = 0.95
	// total = 0.30*0.50 + 0.40*0.80 + 0.30*0.95 = 0.15 + 0.32 + 0.285 = 0.755
	score := ScoreBid(bid, rfp, 80.0)
	expected := 0.755
	if score < expected-0.01 || score > expected+0.01 {
		t.Errorf("expected score ~%.3f, got %.3f", expected, score)
	}
}

func TestScoreBid_ZeroMaxBudget(t *testing.T) {
	rfp := &RFP{
		MaxBudget:      "0",
		ScoringWeights: DefaultScoringWeights(),
	}
	bid := &Bid{TotalBudget: "0.50", SuccessRate: 90}

	score := ScoreBid(bid, rfp, 50)
	if score < 0 {
		t.Errorf("expected non-negative score, got %f", score)
	}
}

// --- List/Get tests ---

func TestListOpenRFPs(t *testing.T) {
	svc, _, _ := newTestService()
	publishTestRFP(t, svc)

	svc.PublishRFP(context.Background(), PublishRFPRequest{
		BuyerAddr:   "0xBuyer2",
		ServiceType: "inference",
		MinBudget:   "1.00",
		MaxBudget:   "2.00",
		Duration:    "7d",
		BidDeadline: "24h",
	})

	// All open
	all, err := svc.ListOpenRFPs(context.Background(), "", 50)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("expected 2 open RFPs, got %d", len(all))
	}

	// Filter by type
	translations, err := svc.ListOpenRFPs(context.Background(), "translation", 50)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(translations) != 1 {
		t.Errorf("expected 1 translation RFP, got %d", len(translations))
	}
}

func TestListByBuyer(t *testing.T) {
	svc, _, _ := newTestService()
	publishTestRFP(t, svc) // buyer = 0xBuyer

	svc.PublishRFP(context.Background(), PublishRFPRequest{
		BuyerAddr:   "0xOther",
		ServiceType: "inference",
		MinBudget:   "1.00",
		MaxBudget:   "2.00",
		Duration:    "7d",
		BidDeadline: "24h",
	})

	rfps, err := svc.ListByBuyer(context.Background(), "0xBuyer", 50)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(rfps) != 1 {
		t.Errorf("expected 1 RFP for buyer, got %d", len(rfps))
	}
}

func TestListBidsBySeller(t *testing.T) {
	svc, _, _ := newTestService()
	rfp := publishTestRFP(t, svc)

	svc.PlaceBid(context.Background(), rfp.ID, PlaceBidRequest{
		SellerAddr:   "0xSeller1",
		PricePerCall: "0.005",
		TotalBudget:  "0.75",
		Duration:     "7d",
	})

	bids, err := svc.ListBidsBySeller(context.Background(), "0xSeller1", 50)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(bids) != 1 {
		t.Errorf("expected 1 bid for seller, got %d", len(bids))
	}
}

func TestListBidsByRFP(t *testing.T) {
	svc, _, _ := newTestService()
	rfp := publishTestRFP(t, svc)

	svc.PlaceBid(context.Background(), rfp.ID, PlaceBidRequest{
		SellerAddr:   "0xSeller1",
		PricePerCall: "0.005",
		TotalBudget:  "0.75",
		Duration:     "7d",
	})
	svc.PlaceBid(context.Background(), rfp.ID, PlaceBidRequest{
		SellerAddr:   "0xSeller2",
		PricePerCall: "0.006",
		TotalBudget:  "0.80",
		Duration:     "7d",
	})

	bids, err := svc.ListBidsByRFP(context.Background(), rfp.ID, 50)
	if err != nil {
		t.Fatalf("list bids failed: %v", err)
	}
	if len(bids) != 2 {
		t.Errorf("expected 2 bids, got %d", len(bids))
	}

	// Verify bid count on RFP
	updated, _ := svc.Get(context.Background(), rfp.ID)
	if updated.BidCount != 2 {
		t.Errorf("expected bid count 2, got %d", updated.BidCount)
	}
}

func TestSelectWinner_InSelectingStatus(t *testing.T) {
	svc, store, _ := newTestService()
	rfp := publishTestRFP(t, svc)

	bid, _ := svc.PlaceBid(context.Background(), rfp.ID, PlaceBidRequest{
		SellerAddr:   "0xSeller1",
		PricePerCall: "0.005",
		TotalBudget:  "0.75",
		Duration:     "7d",
	})

	// Manually move to "selecting" status (as CheckExpired would)
	store.mu.Lock()
	store.rfps[rfp.ID].Status = RFPStatusSelecting
	store.mu.Unlock()

	// Should still be able to select in "selecting" status
	updatedRFP, _, err := svc.SelectWinner(context.Background(), rfp.ID, bid.ID, "0xBuyer")
	if err != nil {
		t.Fatalf("select in selecting status failed: %v", err)
	}
	if updatedRFP.Status != RFPStatusAwarded {
		t.Errorf("expected awarded, got %s", updatedRFP.Status)
	}
}

// --- CheckExpired tests ---

func TestCheckExpired_AutoSelect(t *testing.T) {
	svc, store, _ := newTestService()
	cf := &mockContractFormer{}
	svc.WithContractFormer(cf)

	rfp, _ := svc.PublishRFP(context.Background(), PublishRFPRequest{
		BuyerAddr:   "0xBuyer",
		ServiceType: "translation",
		MinBudget:   "0.50",
		MaxBudget:   "1.00",
		Duration:    "7d",
		BidDeadline: "24h",
		AutoSelect:  true,
	})

	svc.PlaceBid(context.Background(), rfp.ID, PlaceBidRequest{
		SellerAddr:   "0xSeller1",
		PricePerCall: "0.005",
		TotalBudget:  "0.75",
		Duration:     "7d",
	})

	// Set deadline to the past
	store.mu.Lock()
	store.rfps[rfp.ID].BidDeadline = time.Now().Add(-1 * time.Hour)
	store.mu.Unlock()

	svc.CheckExpired(context.Background())

	updated, _ := svc.Get(context.Background(), rfp.ID)
	if updated.Status != RFPStatusAwarded {
		t.Errorf("expected auto-selected to awarded, got %s", updated.Status)
	}
}

func TestCheckExpired_NoBids(t *testing.T) {
	svc, store, _ := newTestService()

	rfp, _ := svc.PublishRFP(context.Background(), PublishRFPRequest{
		BuyerAddr:   "0xBuyer",
		ServiceType: "translation",
		MinBudget:   "0.50",
		MaxBudget:   "1.00",
		Duration:    "7d",
		BidDeadline: "24h",
	})

	// Set deadline to past (non-auto, no bids)
	store.mu.Lock()
	store.rfps[rfp.ID].BidDeadline = time.Now().Add(-1 * time.Hour)
	store.mu.Unlock()

	svc.CheckExpired(context.Background())

	updated, _ := svc.Get(context.Background(), rfp.ID)
	if updated.Status != RFPStatusExpired {
		t.Errorf("expected expired (no bids), got %s", updated.Status)
	}
}

func TestCheckExpired_NonAutoWithBids(t *testing.T) {
	svc, store, _ := newTestService()

	rfp, _ := svc.PublishRFP(context.Background(), PublishRFPRequest{
		BuyerAddr:   "0xBuyer",
		ServiceType: "translation",
		MinBudget:   "0.50",
		MaxBudget:   "1.00",
		Duration:    "7d",
		BidDeadline: "24h",
	})

	svc.PlaceBid(context.Background(), rfp.ID, PlaceBidRequest{
		SellerAddr:   "0xSeller1",
		PricePerCall: "0.005",
		TotalBudget:  "0.75",
		Duration:     "7d",
	})

	// Set deadline to past (non-auto, has bids → selecting)
	store.mu.Lock()
	store.rfps[rfp.ID].BidDeadline = time.Now().Add(-1 * time.Hour)
	store.mu.Unlock()

	svc.CheckExpired(context.Background())

	updated, _ := svc.Get(context.Background(), rfp.ID)
	if updated.Status != RFPStatusSelecting {
		t.Errorf("expected selecting (has bids), got %s", updated.Status)
	}
}

func TestCheckExpired_StaleSelecting(t *testing.T) {
	svc, store, _ := newTestService()

	rfp, _ := svc.PublishRFP(context.Background(), PublishRFPRequest{
		BuyerAddr:   "0xBuyer",
		ServiceType: "translation",
		MinBudget:   "0.50",
		MaxBudget:   "1.00",
		Duration:    "7d",
		BidDeadline: "24h",
	})

	// Place a bid so it transitions to selecting
	svc.PlaceBid(context.Background(), rfp.ID, PlaceBidRequest{
		SellerAddr:   "0xSeller",
		PricePerCall: "0.005",
		TotalBudget:  "0.75",
		Duration:     "7d",
	})

	// Set deadline to 25 hours ago (past the 24h grace period)
	store.mu.Lock()
	store.rfps[rfp.ID].BidDeadline = time.Now().Add(-25 * time.Hour)
	store.rfps[rfp.ID].Status = RFPStatusSelecting
	store.mu.Unlock()

	svc.CheckExpired(context.Background())

	updated, _ := svc.Get(context.Background(), rfp.ID)
	if updated.Status != RFPStatusExpired {
		t.Errorf("expected stale selecting to expire, got %s", updated.Status)
	}

	// Verify bids were rejected
	bids, _ := svc.ListBidsByRFP(context.Background(), rfp.ID, 50)
	for _, b := range bids {
		if b.Status != BidStatusRejected {
			t.Errorf("expected bid %s rejected, got %s", b.ID, b.Status)
		}
	}
}

// --- IsTerminal tests ---

func TestRFP_IsTerminal(t *testing.T) {
	tests := []struct {
		status   RFPStatus
		terminal bool
	}{
		{RFPStatusOpen, false},
		{RFPStatusSelecting, false},
		{RFPStatusAwarded, true},
		{RFPStatusExpired, true},
		{RFPStatusCancelled, true},
	}

	for _, tt := range tests {
		rfp := &RFP{Status: tt.status}
		if rfp.IsTerminal() != tt.terminal {
			t.Errorf("status %s: expected IsTerminal()=%v", tt.status, tt.terminal)
		}
	}
}

// --- Parse helpers tests ---

func TestParseDuration(t *testing.T) {
	tests := []struct {
		input    string
		expected time.Duration
		wantErr  bool
	}{
		{"24h", 24 * time.Hour, false},
		{"7d", 7 * 24 * time.Hour, false},
		{"30m", 30 * time.Minute, false},
		{"", 0, true},
		{"-1d", 0, true},
		{"abc", 0, true},
	}

	for _, tt := range tests {
		d, err := parseDuration(tt.input)
		if tt.wantErr {
			if err == nil {
				t.Errorf("parseDuration(%q): expected error", tt.input)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseDuration(%q): unexpected error: %v", tt.input, err)
			continue
		}
		if d != tt.expected {
			t.Errorf("parseDuration(%q): expected %v, got %v", tt.input, tt.expected, d)
		}
	}
}

func TestMergeHelpers(t *testing.T) {
	if mergeString("new", "old") != "new" {
		t.Error("mergeString: expected new value")
	}
	if mergeString("", "old") != "old" {
		t.Error("mergeString: expected old value")
	}
	if mergeInt(5, 3) != 5 {
		t.Error("mergeInt: expected new value")
	}
	if mergeInt(0, 3) != 3 {
		t.Error("mergeInt: expected old value")
	}
	if mergeFloat(1.5, 2.0) != 1.5 {
		t.Error("mergeFloat: expected new value")
	}
	if mergeFloat(0, 2.0) != 2.0 {
		t.Error("mergeFloat: expected old value")
	}
}

// --- Bid Bond tests ---

func publishBondRFP(t *testing.T, svc *Service, bondPct float64, noWithdrawWindow string) *RFP {
	t.Helper()
	rfp, err := svc.PublishRFP(context.Background(), PublishRFPRequest{
		BuyerAddr:        "0xBuyer",
		ServiceType:      "translation",
		Description:      "Need translation service",
		MinBudget:        "0.50",
		MaxBudget:        "1.00",
		Duration:         "7d",
		BidDeadline:      "24h",
		RequiredBondPct:  bondPct,
		NoWithdrawWindow: noWithdrawWindow,
	})
	if err != nil {
		t.Fatalf("failed to publish RFP with bonds: %v", err)
	}
	return rfp
}

func TestPlaceBid_WithBond(t *testing.T) {
	svc, _, _ := newTestService()
	ml := newMockLedger()
	svc.WithLedger(ml)

	rfp := publishBondRFP(t, svc, 10, "") // 10% bond

	bid, err := svc.PlaceBid(context.Background(), rfp.ID, PlaceBidRequest{
		SellerAddr:   "0xSeller1",
		PricePerCall: "0.005",
		TotalBudget:  "0.75",
		Duration:     "7d",
	})
	if err != nil {
		t.Fatalf("failed to place bid with bond: %v", err)
	}

	if bid.BondAmount != "0.075000" {
		t.Errorf("expected bond amount 0.075000, got %s", bid.BondAmount)
	}
	if bid.BondStatus != BondStatusHeld {
		t.Errorf("expected bond status held, got %s", bid.BondStatus)
	}
	if ml.heldCount() != 1 {
		t.Errorf("expected 1 held bond, got %d", ml.heldCount())
	}
}

func TestPlaceBid_BondNoLedger(t *testing.T) {
	svc, _, _ := newTestService()
	// No ledger configured
	rfp := publishBondRFP(t, svc, 10, "")

	_, err := svc.PlaceBid(context.Background(), rfp.ID, PlaceBidRequest{
		SellerAddr:   "0xSeller1",
		PricePerCall: "0.005",
		TotalBudget:  "0.75",
		Duration:     "7d",
	})
	if !errors.Is(err, ErrBondRequired) {
		t.Errorf("expected ErrBondRequired, got %v", err)
	}
}

func TestPlaceBid_BondHoldFails(t *testing.T) {
	svc, _, _ := newTestService()
	ml := newMockLedger()
	ml.holdErr = errors.New("insufficient balance")
	svc.WithLedger(ml)

	rfp := publishBondRFP(t, svc, 10, "")

	_, err := svc.PlaceBid(context.Background(), rfp.ID, PlaceBidRequest{
		SellerAddr:   "0xSeller1",
		PricePerCall: "0.005",
		TotalBudget:  "0.75",
		Duration:     "7d",
	})
	if !errors.Is(err, ErrInsufficientBond) {
		t.Errorf("expected ErrInsufficientBond, got %v", err)
	}
}

func TestSelectWinner_BondsReleased(t *testing.T) {
	svc, _, _ := newTestService()
	ml := newMockLedger()
	svc.WithLedger(ml)

	rfp := publishBondRFP(t, svc, 10, "")

	bid1, _ := svc.PlaceBid(context.Background(), rfp.ID, PlaceBidRequest{
		SellerAddr:   "0xSeller1",
		PricePerCall: "0.005",
		TotalBudget:  "0.75",
		Duration:     "7d",
	})
	bid2, _ := svc.PlaceBid(context.Background(), rfp.ID, PlaceBidRequest{
		SellerAddr:   "0xSeller2",
		PricePerCall: "0.006",
		TotalBudget:  "0.80",
		Duration:     "7d",
	})

	if ml.heldCount() != 2 {
		t.Fatalf("expected 2 held bonds before award, got %d", ml.heldCount())
	}

	_, _, err := svc.SelectWinner(context.Background(), rfp.ID, bid1.ID, "0xBuyer")
	if err != nil {
		t.Fatalf("select winner failed: %v", err)
	}

	// All bonds should be released (winner + loser)
	if ml.heldCount() != 0 {
		t.Errorf("expected 0 held bonds after award, got %d", ml.heldCount())
	}

	// Winner's bond released
	winner, _ := svc.GetBid(context.Background(), bid1.ID)
	if winner.BondStatus != BondStatusReleased {
		t.Errorf("expected winner bond released, got %s", winner.BondStatus)
	}

	// Loser's bond released
	loser, _ := svc.GetBid(context.Background(), bid2.ID)
	if loser.BondStatus != BondStatusReleased {
		t.Errorf("expected loser bond released, got %s", loser.BondStatus)
	}
}

func TestCancelRFP_BondsReleased(t *testing.T) {
	svc, _, _ := newTestService()
	ml := newMockLedger()
	svc.WithLedger(ml)

	rfp := publishBondRFP(t, svc, 10, "")

	svc.PlaceBid(context.Background(), rfp.ID, PlaceBidRequest{
		SellerAddr:   "0xSeller1",
		PricePerCall: "0.005",
		TotalBudget:  "0.75",
		Duration:     "7d",
	})

	if ml.heldCount() != 1 {
		t.Fatalf("expected 1 held bond before cancel, got %d", ml.heldCount())
	}

	_, err := svc.CancelRFP(context.Background(), rfp.ID, "0xBuyer", "changed plans")
	if err != nil {
		t.Fatalf("cancel failed: %v", err)
	}

	if ml.heldCount() != 0 {
		t.Errorf("expected 0 held bonds after cancel, got %d", ml.heldCount())
	}
}

func TestCounter_BondTransfer(t *testing.T) {
	svc, _, _ := newTestService()
	ml := newMockLedger()
	svc.WithLedger(ml)

	rfp := publishBondRFP(t, svc, 10, "")

	bid, _ := svc.PlaceBid(context.Background(), rfp.ID, PlaceBidRequest{
		SellerAddr:   "0xSeller1",
		PricePerCall: "0.005",
		TotalBudget:  "0.75",
		Duration:     "7d",
	})

	counter, err := svc.Counter(context.Background(), rfp.ID, bid.ID, "0xBuyer", CounterRequest{
		PricePerCall: "0.004",
	})
	if err != nil {
		t.Fatalf("counter failed: %v", err)
	}

	// Counter bid should inherit bond
	if counter.BondAmount != "0.075000" {
		t.Errorf("expected counter bond amount 0.075000, got %s", counter.BondAmount)
	}
	if counter.BondStatus != BondStatusHeld {
		t.Errorf("expected counter bond status held, got %s", counter.BondStatus)
	}

	// Old bid should have zeroed bond
	old, _ := svc.GetBid(context.Background(), bid.ID)
	if old.BondAmount != "0" {
		t.Errorf("expected old bid bond amount 0, got %s", old.BondAmount)
	}
	if old.BondStatus != BondStatusNone {
		t.Errorf("expected old bid bond status none, got %s", old.BondStatus)
	}
}

// --- Withdrawal tests ---

func TestWithdrawBid_Simple(t *testing.T) {
	svc, _, _ := newTestService()
	rfp := publishTestRFP(t, svc)

	bid, _ := svc.PlaceBid(context.Background(), rfp.ID, PlaceBidRequest{
		SellerAddr:   "0xSeller1",
		PricePerCall: "0.005",
		TotalBudget:  "0.75",
		Duration:     "7d",
	})

	withdrawn, err := svc.WithdrawBid(context.Background(), rfp.ID, bid.ID, "0xSeller1")
	if err != nil {
		t.Fatalf("withdraw failed: %v", err)
	}

	if withdrawn.Status != BidStatusWithdrawn {
		t.Errorf("expected status withdrawn, got %s", withdrawn.Status)
	}
}

func TestWithdrawBid_Unauthorized(t *testing.T) {
	svc, _, _ := newTestService()
	rfp := publishTestRFP(t, svc)

	bid, _ := svc.PlaceBid(context.Background(), rfp.ID, PlaceBidRequest{
		SellerAddr:   "0xSeller1",
		PricePerCall: "0.005",
		TotalBudget:  "0.75",
		Duration:     "7d",
	})

	_, err := svc.WithdrawBid(context.Background(), rfp.ID, bid.ID, "0xSeller2")
	if !errors.Is(err, ErrUnauthorized) {
		t.Errorf("expected ErrUnauthorized, got %v", err)
	}
}

func TestWithdrawBid_AlreadyWithdrawn(t *testing.T) {
	svc, _, _ := newTestService()
	rfp := publishTestRFP(t, svc)

	bid, _ := svc.PlaceBid(context.Background(), rfp.ID, PlaceBidRequest{
		SellerAddr:   "0xSeller1",
		PricePerCall: "0.005",
		TotalBudget:  "0.75",
		Duration:     "7d",
	})

	svc.WithdrawBid(context.Background(), rfp.ID, bid.ID, "0xSeller1")

	_, err := svc.WithdrawBid(context.Background(), rfp.ID, bid.ID, "0xSeller1")
	if !errors.Is(err, ErrBidAlreadyWithdrawn) {
		t.Errorf("expected ErrBidAlreadyWithdrawn, got %v", err)
	}
}

func TestWithdrawBid_NoWithdrawWindow(t *testing.T) {
	svc, store, _ := newTestService()
	ml := newMockLedger()
	svc.WithLedger(ml)

	// RFP with 2h no-withdrawal window before deadline
	rfp := publishBondRFP(t, svc, 10, "2h")

	bid, _ := svc.PlaceBid(context.Background(), rfp.ID, PlaceBidRequest{
		SellerAddr:   "0xSeller1",
		PricePerCall: "0.005",
		TotalBudget:  "0.75",
		Duration:     "7d",
	})

	// Move deadline to 1h from now (within the 2h no-withdrawal window)
	store.mu.Lock()
	store.rfps[rfp.ID].BidDeadline = time.Now().Add(1 * time.Hour)
	store.mu.Unlock()

	_, err := svc.WithdrawBid(context.Background(), rfp.ID, bid.ID, "0xSeller1")
	if !errors.Is(err, ErrWithdrawalBlocked) {
		t.Errorf("expected ErrWithdrawalBlocked, got %v", err)
	}
}

func TestWithdrawBid_EarlyRelease(t *testing.T) {
	svc, _, _ := newTestService()
	ml := newMockLedger()
	svc.WithLedger(ml)

	rfp := publishBondRFP(t, svc, 10, "")

	bid, _ := svc.PlaceBid(context.Background(), rfp.ID, PlaceBidRequest{
		SellerAddr:   "0xSeller1",
		PricePerCall: "0.005",
		TotalBudget:  "0.75",
		Duration:     "7d",
	})

	if ml.heldCount() != 1 {
		t.Fatalf("expected 1 held bond, got %d", ml.heldCount())
	}

	// Withdraw early (well before deadline) → bond released fully
	withdrawn, err := svc.WithdrawBid(context.Background(), rfp.ID, bid.ID, "0xSeller1")
	if err != nil {
		t.Fatalf("withdraw failed: %v", err)
	}

	if withdrawn.BondStatus != BondStatusReleased {
		t.Errorf("expected bond released, got %s", withdrawn.BondStatus)
	}
	if ml.heldCount() != 0 {
		t.Errorf("expected 0 held bonds after early withdraw, got %d", ml.heldCount())
	}
}

func TestWithdrawBid_LateWithdrawalPenalty(t *testing.T) {
	svc, store, _ := newTestService()
	ml := newMockLedger()
	svc.WithLedger(ml)

	rfp := publishBondRFP(t, svc, 10, "")

	bid, _ := svc.PlaceBid(context.Background(), rfp.ID, PlaceBidRequest{
		SellerAddr:   "0xSeller1",
		PricePerCall: "0.005",
		TotalBudget:  "0.75",
		Duration:     "7d",
	})

	// Move RFP creation time back so we're in the last 25% of the window.
	// deadline is 24h from original creation. Set createdAt to 23h ago
	// so totalWindow=24h, timeSinceCreation=23h > 18h (75% of 24h).
	store.mu.Lock()
	stored := store.rfps[rfp.ID]
	stored.CreatedAt = time.Now().Add(-23 * time.Hour)
	stored.BidDeadline = stored.CreatedAt.Add(24 * time.Hour)
	store.mu.Unlock()

	withdrawn, err := svc.WithdrawBid(context.Background(), rfp.ID, bid.ID, "0xSeller1")
	if err != nil {
		t.Fatalf("withdraw failed: %v", err)
	}

	if withdrawn.BondStatus != BondStatusForfeited {
		t.Errorf("expected bond forfeited, got %s", withdrawn.BondStatus)
	}
}

func TestWithdrawBid_TerminalRFP(t *testing.T) {
	svc, _, _ := newTestService()
	rfp := publishTestRFP(t, svc)

	bid, _ := svc.PlaceBid(context.Background(), rfp.ID, PlaceBidRequest{
		SellerAddr:   "0xSeller1",
		PricePerCall: "0.005",
		TotalBudget:  "0.75",
		Duration:     "7d",
	})

	// Cancel the RFP first
	svc.CancelRFP(context.Background(), rfp.ID, "0xBuyer", "done")

	_, err := svc.WithdrawBid(context.Background(), rfp.ID, bid.ID, "0xSeller1")
	if !errors.Is(err, ErrInvalidStatus) {
		t.Errorf("expected ErrInvalidStatus for terminal RFP, got %v", err)
	}
}

func TestPublishRFP_InvalidBondPct(t *testing.T) {
	svc, _, _ := newTestService()

	_, err := svc.PublishRFP(context.Background(), PublishRFPRequest{
		BuyerAddr:       "0xBuyer",
		ServiceType:     "translation",
		MinBudget:       "0.50",
		MaxBudget:       "1.00",
		Duration:        "7d",
		BidDeadline:     "24h",
		RequiredBondPct: 150, // > 100
	})
	if err == nil {
		t.Error("expected error for invalid bond percentage")
	}
}

func TestPublishRFP_InvalidNoWithdrawWindow(t *testing.T) {
	svc, _, _ := newTestService()

	_, err := svc.PublishRFP(context.Background(), PublishRFPRequest{
		BuyerAddr:        "0xBuyer",
		ServiceType:      "translation",
		MinBudget:        "0.50",
		MaxBudget:        "1.00",
		Duration:         "7d",
		BidDeadline:      "24h",
		NoWithdrawWindow: "not-a-duration",
	})
	if err == nil {
		t.Error("expected error for invalid no-withdraw window")
	}
}
