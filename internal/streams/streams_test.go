package streams

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// mockLedger records calls for verification.
type mockLedger struct {
	mu          sync.Mutex
	holds       map[string]string // reference → amount
	settlements map[string]string // reference → amount (SettleHold calls)
	releases    map[string]string
	holdErr     error
}

func newMockLedger() *mockLedger {
	return &mockLedger{
		holds:       make(map[string]string),
		settlements: make(map[string]string),
		releases:    make(map[string]string),
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

func (m *mockLedger) SettleHold(_ context.Context, buyerAddr, sellerAddr, amount, reference string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.settlements[reference] = amount
	return nil
}

func (m *mockLedger) ReleaseHold(_ context.Context, agentAddr, amount, reference string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.releases[reference] = amount
	return nil
}

func TestOpenStream(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)

	ctx := context.Background()
	stream, err := svc.Open(ctx, OpenRequest{
		BuyerAddr:    "0x1111111111111111111111111111111111111111",
		SellerAddr:   "0x2222222222222222222222222222222222222222",
		HoldAmount:   "1.000000",
		PricePerTick: "0.001000",
	})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	if stream.Status != StatusOpen {
		t.Errorf("expected status open, got %s", stream.Status)
	}
	if stream.SpentAmount != "0.000000" {
		t.Errorf("expected spent 0, got %s", stream.SpentAmount)
	}
	if stream.TickCount != 0 {
		t.Errorf("expected 0 ticks, got %d", stream.TickCount)
	}

	// Verify hold was placed
	if _, ok := ledger.holds[stream.ID]; !ok {
		t.Error("expected hold to be placed on ledger")
	}
}

func TestOpenStreamSameParty(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)

	_, err := svc.Open(context.Background(), OpenRequest{
		BuyerAddr:    "0x1111111111111111111111111111111111111111",
		SellerAddr:   "0x1111111111111111111111111111111111111111",
		HoldAmount:   "1.000000",
		PricePerTick: "0.001000",
	})
	if err == nil {
		t.Fatal("expected error for same buyer/seller")
	}
}

func TestTickStream(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)

	ctx := context.Background()
	stream, _ := svc.Open(ctx, OpenRequest{
		BuyerAddr:    "0x1111111111111111111111111111111111111111",
		SellerAddr:   "0x2222222222222222222222222222222222222222",
		HoldAmount:   "0.010000",
		PricePerTick: "0.001000",
	})

	// Record 5 ticks using default price
	for i := 0; i < 5; i++ {
		tick, updated, err := svc.RecordTick(ctx, stream.ID, TickRequest{})
		if err != nil {
			t.Fatalf("tick %d failed: %v", i+1, err)
		}
		if tick.Seq != i+1 {
			t.Errorf("tick %d: expected seq %d, got %d", i+1, i+1, tick.Seq)
		}
		if updated.TickCount != i+1 {
			t.Errorf("tick %d: expected count %d, got %d", i+1, i+1, updated.TickCount)
		}
	}

	// Verify spent amount after 5 ticks at 0.001 each = 0.005
	updated, _ := svc.Get(ctx, stream.ID)
	if updated.SpentAmount != "0.005000" {
		t.Errorf("expected spent 0.005000, got %s", updated.SpentAmount)
	}
}

func TestTickStreamExceedsHold(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)

	ctx := context.Background()
	stream, _ := svc.Open(ctx, OpenRequest{
		BuyerAddr:    "0x1111111111111111111111111111111111111111",
		SellerAddr:   "0x2222222222222222222222222222222222222222",
		HoldAmount:   "0.002000",
		PricePerTick: "0.001000",
	})

	// First two ticks should succeed (0.002 total = exactly hold)
	for i := 0; i < 2; i++ {
		_, _, err := svc.RecordTick(ctx, stream.ID, TickRequest{})
		if err != nil {
			t.Fatalf("tick %d failed: %v", i+1, err)
		}
	}

	// Third tick should fail (would exceed hold)
	_, _, err := svc.RecordTick(ctx, stream.ID, TickRequest{})
	if err != ErrHoldExhausted {
		t.Errorf("expected ErrHoldExhausted, got %v", err)
	}
}

func TestTickStreamCustomAmount(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)

	ctx := context.Background()
	stream, _ := svc.Open(ctx, OpenRequest{
		BuyerAddr:    "0x1111111111111111111111111111111111111111",
		SellerAddr:   "0x2222222222222222222222222222222222222222",
		HoldAmount:   "1.000000",
		PricePerTick: "0.001000",
	})

	// Tick with custom amount
	tick, _, err := svc.RecordTick(ctx, stream.ID, TickRequest{
		Amount:   "0.005000",
		Metadata: "5 tokens",
	})
	if err != nil {
		t.Fatalf("tick failed: %v", err)
	}
	if tick.Amount != "0.005000" {
		t.Errorf("expected tick amount 0.005000, got %s", tick.Amount)
	}
	if tick.Metadata != "5 tokens" {
		t.Errorf("expected metadata '5 tokens', got %s", tick.Metadata)
	}
}

func TestCloseStream(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)

	ctx := context.Background()
	stream, _ := svc.Open(ctx, OpenRequest{
		BuyerAddr:    "0x1111111111111111111111111111111111111111",
		SellerAddr:   "0x2222222222222222222222222222222222222222",
		HoldAmount:   "1.000000",
		PricePerTick: "0.001000",
	})

	// Record 3 ticks (spent = 0.003)
	for i := 0; i < 3; i++ {
		svc.RecordTick(ctx, stream.ID, TickRequest{})
	}

	// Close (buyer closes)
	closed, err := svc.Close(ctx, stream.ID, "0x1111111111111111111111111111111111111111", "done")
	if err != nil {
		t.Fatalf("close failed: %v", err)
	}

	if closed.Status != StatusClosed {
		t.Errorf("expected status closed, got %s", closed.Status)
	}
	if closed.CloseReason != "done" {
		t.Errorf("expected reason 'done', got %s", closed.CloseReason)
	}

	// Verify settlement: SettleHold for spent, ReleaseHold for unused
	if _, ok := ledger.settlements[stream.ID]; !ok {
		t.Error("expected SettleHold for spent amount")
	}
	if _, ok := ledger.releases[stream.ID]; !ok {
		t.Error("expected ReleaseHold for unused amount")
	}
}

func TestCloseStreamUnauthorized(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)

	ctx := context.Background()
	stream, _ := svc.Open(ctx, OpenRequest{
		BuyerAddr:    "0x1111111111111111111111111111111111111111",
		SellerAddr:   "0x2222222222222222222222222222222222222222",
		HoldAmount:   "1.000000",
		PricePerTick: "0.001000",
	})

	// Third party cannot close
	_, err := svc.Close(ctx, stream.ID, "0x3333333333333333333333333333333333333333", "")
	if err != ErrUnauthorized {
		t.Errorf("expected ErrUnauthorized, got %v", err)
	}
}

func TestCloseStreamAlreadyClosed(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)

	ctx := context.Background()
	stream, _ := svc.Open(ctx, OpenRequest{
		BuyerAddr:    "0x1111111111111111111111111111111111111111",
		SellerAddr:   "0x2222222222222222222222222222222222222222",
		HoldAmount:   "1.000000",
		PricePerTick: "0.001000",
	})

	svc.Close(ctx, stream.ID, "0x1111111111111111111111111111111111111111", "")

	// Second close should fail
	_, err := svc.Close(ctx, stream.ID, "0x1111111111111111111111111111111111111111", "")
	if err != ErrAlreadyClosed {
		t.Errorf("expected ErrAlreadyClosed, got %v", err)
	}
}

func TestTickClosedStream(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)

	ctx := context.Background()
	stream, _ := svc.Open(ctx, OpenRequest{
		BuyerAddr:    "0x1111111111111111111111111111111111111111",
		SellerAddr:   "0x2222222222222222222222222222222222222222",
		HoldAmount:   "1.000000",
		PricePerTick: "0.001000",
	})

	svc.Close(ctx, stream.ID, "0x1111111111111111111111111111111111111111", "")

	_, _, err := svc.RecordTick(ctx, stream.ID, TickRequest{})
	if err != ErrAlreadyClosed {
		t.Errorf("expected ErrAlreadyClosed, got %v", err)
	}
}

func TestAutoCloseStaleStream(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)

	ctx := context.Background()
	stream, _ := svc.Open(ctx, OpenRequest{
		BuyerAddr:       "0x1111111111111111111111111111111111111111",
		SellerAddr:      "0x2222222222222222222222222222222222222222",
		HoldAmount:      "1.000000",
		PricePerTick:    "0.001000",
		StaleTimeoutSec: 1, // 1 second for testing
	})

	// Record a tick
	svc.RecordTick(ctx, stream.ID, TickRequest{})

	// Auto-close
	err := svc.AutoClose(ctx, stream)
	if err != nil {
		t.Fatalf("auto-close failed: %v", err)
	}

	result, _ := svc.Get(ctx, stream.ID)
	if result.Status != StatusStaleClosed {
		t.Errorf("expected status stale_closed, got %s", result.Status)
	}
}

func TestCloseZeroSpentStream(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)

	ctx := context.Background()
	stream, _ := svc.Open(ctx, OpenRequest{
		BuyerAddr:    "0x1111111111111111111111111111111111111111",
		SellerAddr:   "0x2222222222222222222222222222222222222222",
		HoldAmount:   "1.000000",
		PricePerTick: "0.001000",
	})

	// Close without any ticks
	closed, err := svc.Close(ctx, stream.ID, "0x1111111111111111111111111111111111111111", "cancelled")
	if err != nil {
		t.Fatalf("close failed: %v", err)
	}

	if closed.Status != StatusClosed {
		t.Errorf("expected status closed, got %s", closed.Status)
	}

	// No settlement (nothing spent), but release should exist (full refund)
	if _, ok := ledger.settlements[stream.ID]; ok {
		t.Error("expected no SettleHold for zero-spent stream")
	}
	if _, ok := ledger.releases[stream.ID]; !ok {
		t.Error("expected release hold for full refund")
	}
}

func TestListByAgent(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)

	ctx := context.Background()
	buyer := "0x1111111111111111111111111111111111111111"
	seller := "0x2222222222222222222222222222222222222222"

	// Open 3 streams
	for i := 0; i < 3; i++ {
		svc.Open(ctx, OpenRequest{
			BuyerAddr:    buyer,
			SellerAddr:   seller,
			HoldAmount:   "1.000000",
			PricePerTick: "0.001000",
		})
	}

	// List as buyer
	streams, err := svc.ListByAgent(ctx, buyer, 50)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(streams) != 3 {
		t.Errorf("expected 3 streams, got %d", len(streams))
	}

	// List as seller
	streams, err = svc.ListByAgent(ctx, seller, 50)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(streams) != 3 {
		t.Errorf("expected 3 streams, got %d", len(streams))
	}
}

func TestListTicks(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)

	ctx := context.Background()
	stream, _ := svc.Open(ctx, OpenRequest{
		BuyerAddr:    "0x1111111111111111111111111111111111111111",
		SellerAddr:   "0x2222222222222222222222222222222222222222",
		HoldAmount:   "1.000000",
		PricePerTick: "0.001000",
	})

	for i := 0; i < 5; i++ {
		svc.RecordTick(ctx, stream.ID, TickRequest{Metadata: "test"})
	}

	ticks, err := svc.ListTicks(ctx, stream.ID, 100)
	if err != nil {
		t.Fatalf("list ticks failed: %v", err)
	}
	if len(ticks) != 5 {
		t.Errorf("expected 5 ticks, got %d", len(ticks))
	}

	// Verify sequential numbering
	for i, tick := range ticks {
		if tick.Seq != i+1 {
			t.Errorf("tick %d: expected seq %d, got %d", i, i+1, tick.Seq)
		}
	}
}

func TestSellerCanCloseStream(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)

	ctx := context.Background()
	stream, _ := svc.Open(ctx, OpenRequest{
		BuyerAddr:    "0x1111111111111111111111111111111111111111",
		SellerAddr:   "0x2222222222222222222222222222222222222222",
		HoldAmount:   "1.000000",
		PricePerTick: "0.001000",
	})

	// Record a tick so there's something to settle
	svc.RecordTick(ctx, stream.ID, TickRequest{})

	// Seller closes
	closed, err := svc.Close(ctx, stream.ID, "0x2222222222222222222222222222222222222222", "service_complete")
	if err != nil {
		t.Fatalf("seller close failed: %v", err)
	}
	if closed.Status != StatusClosed {
		t.Errorf("expected status closed, got %s", closed.Status)
	}
	if closed.CloseReason != "service_complete" {
		t.Errorf("expected reason 'service_complete', got %s", closed.CloseReason)
	}
}

func TestCloseFullySpentStream(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)

	ctx := context.Background()
	stream, _ := svc.Open(ctx, OpenRequest{
		BuyerAddr:    "0x1111111111111111111111111111111111111111",
		SellerAddr:   "0x2222222222222222222222222222222222222222",
		HoldAmount:   "0.003000",
		PricePerTick: "0.001000",
	})

	// Spend all 3 ticks (exactly exhausts hold)
	for i := 0; i < 3; i++ {
		_, _, err := svc.RecordTick(ctx, stream.ID, TickRequest{})
		if err != nil {
			t.Fatalf("tick %d failed: %v", i+1, err)
		}
	}

	closed, err := svc.Close(ctx, stream.ID, "0x1111111111111111111111111111111111111111", "done")
	if err != nil {
		t.Fatalf("close failed: %v", err)
	}

	// SettleHold for full amount (spent == hold)
	if _, ok := ledger.settlements[stream.ID]; !ok {
		t.Error("expected SettleHold for spent amount")
	}

	// No release needed (nothing unused)
	if _, ok := ledger.releases[stream.ID]; ok {
		t.Error("expected no release hold when fully spent")
	}

	if closed.SpentAmount != "0.003000" {
		t.Errorf("expected spent 0.003000, got %s", closed.SpentAmount)
	}
}

func TestOpenStreamHoldFailure(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	ledger.holdErr = errors.New("insufficient balance")
	svc := NewService(store, ledger)

	_, err := svc.Open(context.Background(), OpenRequest{
		BuyerAddr:    "0x1111111111111111111111111111111111111111",
		SellerAddr:   "0x2222222222222222222222222222222222222222",
		HoldAmount:   "1.000000",
		PricePerTick: "0.001000",
	})
	if err == nil {
		t.Fatal("expected error when hold fails")
	}
	if !strings.Contains(err.Error(), "insufficient balance") {
		t.Errorf("expected insufficient balance error, got: %v", err)
	}
}

func TestOpenStreamInvalidAmounts(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)

	tests := []struct {
		name  string
		hold  string
		price string
	}{
		{"zero hold", "0.000000", "0.001000"},
		{"negative hold", "-1.000000", "0.001000"},
		{"zero price", "1.000000", "0.000000"},
		{"negative price", "1.000000", "-0.001000"},
		{"invalid hold format", "abc", "0.001000"},
		{"invalid price format", "1.000000", "xyz"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := svc.Open(context.Background(), OpenRequest{
				BuyerAddr:    "0x1111111111111111111111111111111111111111",
				SellerAddr:   "0x2222222222222222222222222222222222222222",
				HoldAmount:   tt.hold,
				PricePerTick: tt.price,
			})
			if err == nil {
				t.Error("expected error for invalid amount")
			}
		})
	}
}

func TestGetNonExistentStream(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)

	_, err := svc.Get(context.Background(), "str_nonexistent")
	if err != ErrStreamNotFound {
		t.Errorf("expected ErrStreamNotFound, got %v", err)
	}
}

func TestTickNonExistentStream(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)

	_, _, err := svc.RecordTick(context.Background(), "str_nonexistent", TickRequest{})
	if err != ErrStreamNotFound {
		t.Errorf("expected ErrStreamNotFound, got %v", err)
	}
}

func TestCumulativeAmountsWithMixedTicks(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)

	ctx := context.Background()
	stream, _ := svc.Open(ctx, OpenRequest{
		BuyerAddr:    "0x1111111111111111111111111111111111111111",
		SellerAddr:   "0x2222222222222222222222222222222222222222",
		HoldAmount:   "1.000000",
		PricePerTick: "0.001000",
	})

	// Mix of default and custom amounts
	amounts := []string{"", "0.005000", "", "0.010000"} // 0.001 + 0.005 + 0.001 + 0.010 = 0.017
	expectedCumulative := []string{"0.001000", "0.006000", "0.007000", "0.017000"}

	for i, amt := range amounts {
		tick, _, err := svc.RecordTick(ctx, stream.ID, TickRequest{Amount: amt})
		if err != nil {
			t.Fatalf("tick %d failed: %v", i+1, err)
		}
		if tick.Cumulative != expectedCumulative[i] {
			t.Errorf("tick %d: expected cumulative %s, got %s", i+1, expectedCumulative[i], tick.Cumulative)
		}
	}

	// Verify final stream state
	updated, _ := svc.Get(ctx, stream.ID)
	if updated.SpentAmount != "0.017000" {
		t.Errorf("expected final spent 0.017000, got %s", updated.SpentAmount)
	}
	if updated.TickCount != 4 {
		t.Errorf("expected 4 ticks, got %d", updated.TickCount)
	}
}

// mockRecorder records transactions for verification.
type mockRecorder struct {
	mu           sync.Mutex
	transactions []recordedTx
}

type recordedTx struct {
	from, to, amount, serviceID, status string
}

func (r *mockRecorder) RecordTransaction(_ context.Context, _, from, to, amount, serviceID, status string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.transactions = append(r.transactions, recordedTx{from, to, amount, serviceID, status})
	return nil
}

func TestRecorderCalledOnClose(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	recorder := &mockRecorder{}
	svc := NewService(store, ledger).WithRecorder(recorder)

	ctx := context.Background()
	stream, _ := svc.Open(ctx, OpenRequest{
		BuyerAddr:    "0x1111111111111111111111111111111111111111",
		SellerAddr:   "0x2222222222222222222222222222222222222222",
		HoldAmount:   "1.000000",
		PricePerTick: "0.001000",
		ServiceID:    "svc_translate",
	})

	svc.RecordTick(ctx, stream.ID, TickRequest{})
	svc.RecordTick(ctx, stream.ID, TickRequest{})

	svc.Close(ctx, stream.ID, "0x1111111111111111111111111111111111111111", "done")

	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if len(recorder.transactions) != 1 {
		t.Fatalf("expected 1 recorded transaction, got %d", len(recorder.transactions))
	}
	tx := recorder.transactions[0]
	if tx.status != "confirmed" {
		t.Errorf("expected status 'confirmed', got %s", tx.status)
	}
	if tx.serviceID != "svc_translate" {
		t.Errorf("expected serviceID 'svc_translate', got %s", tx.serviceID)
	}
	if tx.amount != "0.002000" {
		t.Errorf("expected amount '0.002000', got %s", tx.amount)
	}
}

func TestRecorderNotCalledOnZeroSpent(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	recorder := &mockRecorder{}
	svc := NewService(store, ledger).WithRecorder(recorder)

	ctx := context.Background()
	stream, _ := svc.Open(ctx, OpenRequest{
		BuyerAddr:    "0x1111111111111111111111111111111111111111",
		SellerAddr:   "0x2222222222222222222222222222222222222222",
		HoldAmount:   "1.000000",
		PricePerTick: "0.001000",
	})

	svc.Close(ctx, stream.ID, "0x1111111111111111111111111111111111111111", "cancelled")

	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if len(recorder.transactions) != 0 {
		t.Errorf("expected no recorded transactions for zero-spent close, got %d", len(recorder.transactions))
	}
}

func TestConcurrentTicks(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)

	ctx := context.Background()
	stream, _ := svc.Open(ctx, OpenRequest{
		BuyerAddr:    "0x1111111111111111111111111111111111111111",
		SellerAddr:   "0x2222222222222222222222222222222222222222",
		HoldAmount:   "1.000000",
		PricePerTick: "0.001000",
	})

	// Fire 20 concurrent ticks
	var wg sync.WaitGroup
	errs := make([]error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, _, errs[idx] = svc.RecordTick(ctx, stream.ID, TickRequest{})
		}(i)
	}
	wg.Wait()

	// All should succeed (20 * 0.001 = 0.020 < 1.000)
	for i, err := range errs {
		if err != nil {
			t.Errorf("concurrent tick %d failed: %v", i, err)
		}
	}

	// Verify final state is consistent
	updated, _ := svc.Get(ctx, stream.ID)
	if updated.TickCount != 20 {
		t.Errorf("expected 20 ticks, got %d", updated.TickCount)
	}
	if updated.SpentAmount != "0.020000" {
		t.Errorf("expected spent 0.020000, got %s", updated.SpentAmount)
	}
}

// --- Tick sequence validation tests ---

func TestTickDuplicateSeq(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)

	ctx := context.Background()
	stream, _ := svc.Open(ctx, OpenRequest{
		BuyerAddr:    "0x1111111111111111111111111111111111111111",
		SellerAddr:   "0x2222222222222222222222222222222222222222",
		HoldAmount:   "1.000000",
		PricePerTick: "0.001000",
	})

	// First tick with seq=1
	_, _, err := svc.RecordTick(ctx, stream.ID, TickRequest{Seq: 1})
	if err != nil {
		t.Fatalf("first tick failed: %v", err)
	}

	// Duplicate seq=1 should fail
	_, _, err = svc.RecordTick(ctx, stream.ID, TickRequest{Seq: 1})
	if !errors.Is(err, ErrDuplicateTickSeq) {
		t.Errorf("expected ErrDuplicateTickSeq, got %v", err)
	}
}

func TestTickOutOfOrderSeq(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)

	ctx := context.Background()
	stream, _ := svc.Open(ctx, OpenRequest{
		BuyerAddr:    "0x1111111111111111111111111111111111111111",
		SellerAddr:   "0x2222222222222222222222222222222222222222",
		HoldAmount:   "1.000000",
		PricePerTick: "0.001000",
	})

	// First tick with seq=1
	_, _, err := svc.RecordTick(ctx, stream.ID, TickRequest{Seq: 1})
	if err != nil {
		t.Fatalf("first tick failed: %v", err)
	}

	// Seq=3 (skip 2) should fail
	_, _, err = svc.RecordTick(ctx, stream.ID, TickRequest{Seq: 3})
	if !errors.Is(err, ErrInvalidTickSeq) {
		t.Errorf("expected ErrInvalidTickSeq, got %v", err)
	}
}

func TestTickInvalidAmount(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)

	ctx := context.Background()
	stream, _ := svc.Open(ctx, OpenRequest{
		BuyerAddr:    "0x1111111111111111111111111111111111111111",
		SellerAddr:   "0x2222222222222222222222222222222222222222",
		HoldAmount:   "1.000000",
		PricePerTick: "0.001000",
	})

	tests := []struct {
		name   string
		amount string
	}{
		{"zero amount", "0.000000"},
		{"invalid format", "abc"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := svc.RecordTick(ctx, stream.ID, TickRequest{Amount: tt.amount})
			if !errors.Is(err, ErrInvalidAmount) {
				t.Errorf("expected ErrInvalidAmount, got %v", err)
			}
		})
	}
}

// --- Service options (WithRevenueAccumulator, WithReceiptIssuer, WithWebhookEmitter) ---

type mockRevenue struct {
	mu    sync.Mutex
	calls []struct{ addr, amount, ref string }
}

func (m *mockRevenue) AccumulateRevenue(_ context.Context, agentAddr, amount, txRef string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, struct{ addr, amount, ref string }{agentAddr, amount, txRef})
	return nil
}

type mockReceipt struct {
	mu    sync.Mutex
	calls []struct{ path, reference, from, to, amount string }
}

func (m *mockReceipt) IssueReceipt(_ context.Context, path, reference, from, to, amount, serviceID, status, metadata string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, struct{ path, reference, from, to, amount string }{path, reference, from, to, amount})
	return nil
}

type mockWebhook struct {
	mu          sync.Mutex
	openEvents  int
	closeEvents int
}

func (m *mockWebhook) EmitStreamOpened(_, _, _, _ string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.openEvents++
}

func (m *mockWebhook) EmitStreamClosed(_, _, _, _, _ string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closeEvents++
}

func TestRevenueAccumulatorCalledOnClose(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	rev := &mockRevenue{}
	svc := NewService(store, ledger).WithRevenueAccumulator(rev)

	ctx := context.Background()
	stream, _ := svc.Open(ctx, OpenRequest{
		BuyerAddr:    "0x1111111111111111111111111111111111111111",
		SellerAddr:   "0x2222222222222222222222222222222222222222",
		HoldAmount:   "1.000000",
		PricePerTick: "0.001000",
	})

	svc.RecordTick(ctx, stream.ID, TickRequest{})
	svc.Close(ctx, stream.ID, "0x1111111111111111111111111111111111111111", "done")

	rev.mu.Lock()
	defer rev.mu.Unlock()
	if len(rev.calls) != 1 {
		t.Fatalf("expected 1 revenue call, got %d", len(rev.calls))
	}
	if rev.calls[0].addr != "0x2222222222222222222222222222222222222222" {
		t.Errorf("expected seller addr, got %s", rev.calls[0].addr)
	}
}

func TestReceiptIssuerCalledOnClose(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	rcpt := &mockReceipt{}
	svc := NewService(store, ledger).WithReceiptIssuer(rcpt)

	ctx := context.Background()
	stream, _ := svc.Open(ctx, OpenRequest{
		BuyerAddr:    "0x1111111111111111111111111111111111111111",
		SellerAddr:   "0x2222222222222222222222222222222222222222",
		HoldAmount:   "1.000000",
		PricePerTick: "0.001000",
	})

	svc.RecordTick(ctx, stream.ID, TickRequest{})
	svc.Close(ctx, stream.ID, "0x1111111111111111111111111111111111111111", "done")

	rcpt.mu.Lock()
	defer rcpt.mu.Unlock()
	if len(rcpt.calls) != 1 {
		t.Fatalf("expected 1 receipt call, got %d", len(rcpt.calls))
	}
	if rcpt.calls[0].path != "stream" {
		t.Errorf("expected path 'stream', got %s", rcpt.calls[0].path)
	}
}

func TestWebhookEmitterCalledOnOpenAndClose(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	wh := &mockWebhook{}
	svc := NewService(store, ledger).WithWebhookEmitter(wh)

	ctx := context.Background()
	stream, _ := svc.Open(ctx, OpenRequest{
		BuyerAddr:    "0x1111111111111111111111111111111111111111",
		SellerAddr:   "0x2222222222222222222222222222222222222222",
		HoldAmount:   "1.000000",
		PricePerTick: "0.001000",
	})

	// Webhook emitters fire in goroutines; give them a moment
	time.Sleep(50 * time.Millisecond)

	wh.mu.Lock()
	if wh.openEvents != 1 {
		t.Errorf("expected 1 open webhook, got %d", wh.openEvents)
	}
	wh.mu.Unlock()

	svc.RecordTick(ctx, stream.ID, TickRequest{})
	svc.Close(ctx, stream.ID, "0x1111111111111111111111111111111111111111", "done")

	time.Sleep(50 * time.Millisecond)

	wh.mu.Lock()
	if wh.closeEvents != 1 {
		t.Errorf("expected 1 close webhook, got %d", wh.closeEvents)
	}
	wh.mu.Unlock()
}

// --- ForceCloseStale ---

func TestForceCloseStale(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)

	ctx := context.Background()

	// Open a stream with very short stale timeout
	stream, _ := svc.Open(ctx, OpenRequest{
		BuyerAddr:       "0x1111111111111111111111111111111111111111",
		SellerAddr:      "0x2222222222222222222222222222222222222222",
		HoldAmount:      "1.000000",
		PricePerTick:    "0.001000",
		StaleTimeoutSec: 1,
	})

	// Manually backdate the stream so it appears stale
	stored, _ := store.Get(ctx, stream.ID)
	stored.CreatedAt = time.Now().Add(-2 * time.Minute)
	stored.UpdatedAt = stored.CreatedAt
	store.Update(ctx, stored)

	closed, err := svc.ForceCloseStale(ctx)
	if err != nil {
		t.Fatalf("ForceCloseStale failed: %v", err)
	}
	if closed != 1 {
		t.Errorf("expected 1 closed, got %d", closed)
	}

	result, _ := svc.Get(ctx, stream.ID)
	if result.Status != StatusStaleClosed {
		t.Errorf("expected stale_closed, got %s", result.Status)
	}
}

func TestForceCloseStaleSkipsTerminal(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)

	ctx := context.Background()
	stream, _ := svc.Open(ctx, OpenRequest{
		BuyerAddr:       "0x1111111111111111111111111111111111111111",
		SellerAddr:      "0x2222222222222222222222222222222222222222",
		HoldAmount:      "1.000000",
		PricePerTick:    "0.001000",
		StaleTimeoutSec: 1,
	})

	// Close it normally first
	svc.Close(ctx, stream.ID, "0x1111111111111111111111111111111111111111", "done")

	closed, err := svc.ForceCloseStale(ctx)
	if err != nil {
		t.Fatalf("ForceCloseStale failed: %v", err)
	}
	if closed != 0 {
		t.Errorf("expected 0 closed (already terminal), got %d", closed)
	}
}

// --- ListByAgent with default limit ---

func TestListByAgentDefaultLimit(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)

	ctx := context.Background()
	svc.Open(ctx, OpenRequest{
		BuyerAddr:    "0x1111111111111111111111111111111111111111",
		SellerAddr:   "0x2222222222222222222222222222222222222222",
		HoldAmount:   "1.000000",
		PricePerTick: "0.001000",
	})

	// Pass zero/negative limit to trigger default
	streams, err := svc.ListByAgent(ctx, "0x1111111111111111111111111111111111111111", 0)
	if err != nil {
		t.Fatalf("ListByAgent failed: %v", err)
	}
	if len(streams) != 1 {
		t.Errorf("expected 1 stream, got %d", len(streams))
	}
}

// --- ListTicks with default limit ---

func TestListTicksDefaultLimit(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)

	ctx := context.Background()
	stream, _ := svc.Open(ctx, OpenRequest{
		BuyerAddr:    "0x1111111111111111111111111111111111111111",
		SellerAddr:   "0x2222222222222222222222222222222222222222",
		HoldAmount:   "1.000000",
		PricePerTick: "0.001000",
	})

	svc.RecordTick(ctx, stream.ID, TickRequest{})

	// Pass zero limit to trigger default
	ticks, err := svc.ListTicks(ctx, stream.ID, 0)
	if err != nil {
		t.Fatalf("ListTicks failed: %v", err)
	}
	if len(ticks) != 1 {
		t.Errorf("expected 1 tick, got %d", len(ticks))
	}
}

// --- MemoryStore edge cases ---

func TestMemoryStore_ListByStatus(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	now := time.Now()
	store.Create(ctx, &Stream{ID: "s1", BuyerAddr: "a", SellerAddr: "b", Status: StatusOpen, CreatedAt: now, UpdatedAt: now})
	store.Create(ctx, &Stream{ID: "s2", BuyerAddr: "a", SellerAddr: "b", Status: StatusClosed, CreatedAt: now, UpdatedAt: now})
	store.Create(ctx, &Stream{ID: "s3", BuyerAddr: "a", SellerAddr: "b", Status: StatusOpen, CreatedAt: now, UpdatedAt: now})

	result, err := store.ListByStatus(ctx, StatusOpen, 10)
	if err != nil {
		t.Fatalf("ListByStatus failed: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("expected 2 open streams, got %d", len(result))
	}

	// Test limit
	result, err = store.ListByStatus(ctx, StatusOpen, 1)
	if err != nil {
		t.Fatalf("ListByStatus failed: %v", err)
	}
	if len(result) != 1 {
		t.Errorf("expected 1 stream with limit=1, got %d", len(result))
	}
}

func TestMemoryStore_ListStale(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	past := time.Now().Add(-5 * time.Minute)
	store.Create(ctx, &Stream{
		ID: "stale1", BuyerAddr: "a", SellerAddr: "b",
		Status: StatusOpen, StaleTimeoutSec: 60,
		CreatedAt: past, UpdatedAt: past,
	})

	// Non-stale stream (created recently)
	now := time.Now()
	store.Create(ctx, &Stream{
		ID: "fresh1", BuyerAddr: "a", SellerAddr: "b",
		Status: StatusOpen, StaleTimeoutSec: 60,
		CreatedAt: now, UpdatedAt: now,
	})

	// Closed stream should not appear
	store.Create(ctx, &Stream{
		ID: "closed1", BuyerAddr: "a", SellerAddr: "b",
		Status: StatusClosed, StaleTimeoutSec: 60,
		CreatedAt: past, UpdatedAt: past,
	})

	result, err := store.ListStale(ctx, time.Now(), 100)
	if err != nil {
		t.Fatalf("ListStale failed: %v", err)
	}
	if len(result) != 1 {
		t.Errorf("expected 1 stale stream, got %d", len(result))
	}
	if result[0].ID != "stale1" {
		t.Errorf("expected stale1, got %s", result[0].ID)
	}
}

func TestMemoryStore_ListStaleWithLastTick(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	pastTick := time.Now().Add(-5 * time.Minute)
	store.Create(ctx, &Stream{
		ID: "stale_ticked", BuyerAddr: "a", SellerAddr: "b",
		Status: StatusOpen, StaleTimeoutSec: 60,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
		LastTickAt: &pastTick,
	})

	result, err := store.ListStale(ctx, time.Now(), 100)
	if err != nil {
		t.Fatalf("ListStale failed: %v", err)
	}
	if len(result) != 1 {
		t.Errorf("expected 1 stale stream (based on LastTickAt), got %d", len(result))
	}
}

func TestMemoryStore_GetLastTick(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	// No ticks
	tick, err := store.GetLastTick(ctx, "str_123")
	if err != nil {
		t.Fatalf("GetLastTick failed: %v", err)
	}
	if tick != nil {
		t.Error("expected nil tick for empty stream")
	}

	// Add ticks
	store.CreateTick(ctx, &Tick{ID: "t1", StreamID: "str_123", Seq: 1, Amount: "0.001000", Cumulative: "0.001000", CreatedAt: time.Now()})
	store.CreateTick(ctx, &Tick{ID: "t2", StreamID: "str_123", Seq: 2, Amount: "0.001000", Cumulative: "0.002000", CreatedAt: time.Now()})

	tick, err = store.GetLastTick(ctx, "str_123")
	if err != nil {
		t.Fatalf("GetLastTick failed: %v", err)
	}
	if tick == nil {
		t.Fatal("expected a tick, got nil")
	}
	if tick.Seq != 2 {
		t.Errorf("expected last tick seq 2, got %d", tick.Seq)
	}
}

func TestMemoryStore_UpdateNonExistent(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	err := store.Update(ctx, &Stream{ID: "str_nonexistent"})
	if err != ErrStreamNotFound {
		t.Errorf("expected ErrStreamNotFound, got %v", err)
	}
}

func TestMemoryStore_CreateTickDuplicate(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	tick := &Tick{ID: "t1", StreamID: "str_1", Seq: 1, Amount: "0.001000", Cumulative: "0.001000", CreatedAt: time.Now()}
	if err := store.CreateTick(ctx, tick); err != nil {
		t.Fatalf("first tick failed: %v", err)
	}

	dup := &Tick{ID: "t2", StreamID: "str_1", Seq: 1, Amount: "0.001000", Cumulative: "0.001000", CreatedAt: time.Now()}
	err := store.CreateTick(ctx, dup)
	if err != ErrDuplicateTickSeq {
		t.Errorf("expected ErrDuplicateTickSeq, got %v", err)
	}
}

// --- Timer tests ---

func TestTimerStartStop(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	logger := slog.Default()

	timer := NewTimer(svc, store, logger)

	if timer.Running() {
		t.Error("timer should not be running before Start")
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		timer.Start(ctx)
		close(done)
	}()

	// Wait for the timer to mark itself as running
	time.Sleep(50 * time.Millisecond)
	if !timer.Running() {
		t.Error("timer should be running after Start")
	}

	cancel()
	<-done

	if timer.Running() {
		t.Error("timer should not be running after context cancel")
	}
}

func TestTimerStop(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	logger := slog.Default()

	timer := NewTimer(svc, store, logger)

	ctx := context.Background()
	done := make(chan struct{})
	go func() {
		timer.Start(ctx)
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	timer.Stop()

	select {
	case <-done:
		// Success
	case <-time.After(2 * time.Second):
		t.Fatal("timer did not stop within timeout")
	}
}

func TestTimerClosesStaleStreams(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	logger := slog.Default()

	ctx := context.Background()

	// Create a stream that's already stale
	past := time.Now().Add(-5 * time.Minute)
	store.Create(ctx, &Stream{
		ID: "stale_timer", BuyerAddr: "0x1111111111111111111111111111111111111111",
		SellerAddr:      "0x2222222222222222222222222222222222222222",
		Status:          StatusOpen,
		HoldAmount:      "1.000000",
		SpentAmount:     "0.000000",
		PricePerTick:    "0.001000",
		StaleTimeoutSec: 1,
		CreatedAt:       past,
		UpdatedAt:       past,
	})

	timer := NewTimer(svc, store, logger)
	// Directly call closeStale instead of running the full timer loop
	timer.closeStale(ctx)

	result, _ := store.Get(ctx, "stale_timer")
	if result.Status != StatusStaleClosed {
		t.Errorf("expected stale_closed, got %s", result.Status)
	}
}

func TestTimerReconcileStuck(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	logger := slog.Default()

	ctx := context.Background()

	now := time.Now()
	store.Create(ctx, &Stream{
		ID: "stuck1", BuyerAddr: "a", SellerAddr: "b",
		Status: StatusSettlementFailed, HoldAmount: "1.000000",
		SpentAmount: "0.001000", PricePerTick: "0.001000",
		CreatedAt: now, UpdatedAt: now,
	})

	timer := NewTimer(svc, store, logger)
	timer.reconcileStuck(ctx)

	result, _ := store.Get(ctx, "stuck1")
	if result.Status != StatusClosed {
		t.Errorf("expected reconciled to closed, got %s", result.Status)
	}
	if result.CloseReason != "reconciled" {
		t.Errorf("expected reason 'reconciled', got %s", result.CloseReason)
	}
}

// --- IsTerminal tests ---

func TestIsTerminal(t *testing.T) {
	tests := []struct {
		status   Status
		terminal bool
	}{
		{StatusOpen, false},
		{StatusClosed, true},
		{StatusStaleClosed, true},
		{StatusDisputed, true},
		{StatusSettlementFailed, true},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			s := &Stream{Status: tt.status}
			if got := s.IsTerminal(); got != tt.terminal {
				t.Errorf("IsTerminal() for %s = %v, want %v", tt.status, got, tt.terminal)
			}
		})
	}
}

// --- AutoClose already-closed stream ---

func TestAutoCloseAlreadyClosed(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)

	ctx := context.Background()
	stream, _ := svc.Open(ctx, OpenRequest{
		BuyerAddr:    "0x1111111111111111111111111111111111111111",
		SellerAddr:   "0x2222222222222222222222222222222222222222",
		HoldAmount:   "1.000000",
		PricePerTick: "0.001000",
	})

	svc.Close(ctx, stream.ID, "0x1111111111111111111111111111111111111111", "done")

	err := svc.AutoClose(ctx, stream)
	if !errors.Is(err, ErrAlreadyClosed) {
		t.Errorf("expected ErrAlreadyClosed, got %v", err)
	}
}

// --- Open with custom stale timeout ---

func TestOpenCustomStaleTimeout(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)

	stream, err := svc.Open(context.Background(), OpenRequest{
		BuyerAddr:       "0x1111111111111111111111111111111111111111",
		SellerAddr:      "0x2222222222222222222222222222222222222222",
		HoldAmount:      "1.000000",
		PricePerTick:    "0.001000",
		StaleTimeoutSec: 120,
	})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	if stream.StaleTimeoutSec != 120 {
		t.Errorf("expected stale timeout 120, got %d", stream.StaleTimeoutSec)
	}
}

func TestOpenDefaultStaleTimeout(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)

	stream, err := svc.Open(context.Background(), OpenRequest{
		BuyerAddr:    "0x1111111111111111111111111111111111111111",
		SellerAddr:   "0x2222222222222222222222222222222222222222",
		HoldAmount:   "1.000000",
		PricePerTick: "0.001000",
	})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	if stream.StaleTimeoutSec != int(DefaultStaleTimeout.Seconds()) {
		t.Errorf("expected default stale timeout %d, got %d", int(DefaultStaleTimeout.Seconds()), stream.StaleTimeoutSec)
	}
}

// --- Close with settle failure mock ---

type failSettleLedger struct {
	mockLedger
	settleErr error
}

func (f *failSettleLedger) SettleHold(_ context.Context, _, _, _, _ string) error {
	if f.settleErr != nil {
		return f.settleErr
	}
	return nil
}

func TestCloseSettleHoldFailure(t *testing.T) {
	store := NewMemoryStore()
	ledger := &failSettleLedger{
		mockLedger: *newMockLedger(),
		settleErr:  errors.New("db error"),
	}
	svc := NewService(store, ledger)

	ctx := context.Background()
	stream, _ := svc.Open(ctx, OpenRequest{
		BuyerAddr:    "0x1111111111111111111111111111111111111111",
		SellerAddr:   "0x2222222222222222222222222222222222222222",
		HoldAmount:   "1.000000",
		PricePerTick: "0.001000",
	})
	svc.RecordTick(ctx, stream.ID, TickRequest{})

	_, err := svc.Close(ctx, stream.ID, "0x1111111111111111111111111111111111111111", "done")
	if err == nil {
		t.Fatal("expected error when settle fails")
	}

	// Stream should be marked as settlement_failed
	result, _ := svc.Get(ctx, stream.ID)
	if result.Status != StatusSettlementFailed {
		t.Errorf("expected settlement_failed, got %s", result.Status)
	}
}

// --- Close with release failure mock ---

type failReleaseLedger struct {
	mockLedger
	releaseErr error
}

func (f *failReleaseLedger) ReleaseHold(_ context.Context, _, _, _ string) error {
	if f.releaseErr != nil {
		return f.releaseErr
	}
	return nil
}

func TestCloseReleaseHoldFailure(t *testing.T) {
	store := NewMemoryStore()
	ledger := &failReleaseLedger{
		mockLedger: *newMockLedger(),
		releaseErr: errors.New("release failed"),
	}
	svc := NewService(store, ledger)

	ctx := context.Background()
	stream, _ := svc.Open(ctx, OpenRequest{
		BuyerAddr:    "0x1111111111111111111111111111111111111111",
		SellerAddr:   "0x2222222222222222222222222222222222222222",
		HoldAmount:   "1.000000",
		PricePerTick: "0.001000",
	})

	// Record a tick so there's both spent + unused
	svc.RecordTick(ctx, stream.ID, TickRequest{})

	_, err := svc.Close(ctx, stream.ID, "0x1111111111111111111111111111111111111111", "done")
	if err == nil {
		t.Fatal("expected error when release fails")
	}
	if !strings.Contains(err.Error(), "release") {
		t.Errorf("expected release error, got: %v", err)
	}
}

// --- Store create failure after hold ---

type failCreateStore struct {
	MemoryStore
	createErr error
}

func (f *failCreateStore) Create(_ context.Context, _ *Stream) error {
	return f.createErr
}

func TestOpenStoreCreateFailureReleasesHold(t *testing.T) {
	store := &failCreateStore{
		MemoryStore: *NewMemoryStore(),
		createErr:   errors.New("db down"),
	}
	ledger := newMockLedger()
	svc := NewService(store, ledger)

	_, err := svc.Open(context.Background(), OpenRequest{
		BuyerAddr:    "0x1111111111111111111111111111111111111111",
		SellerAddr:   "0x2222222222222222222222222222222222222222",
		HoldAmount:   "1.000000",
		PricePerTick: "0.001000",
	})
	if err == nil {
		t.Fatal("expected error when store create fails")
	}

	// Verify that the hold was released as best-effort cleanup
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	if len(ledger.releases) == 0 {
		t.Error("expected hold to be released after store failure")
	}
}
