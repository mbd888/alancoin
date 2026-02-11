package streams

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
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
