package escrow

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"
)

func TestTimer_ReleaseExpired_DeliveredWithDisputeWindowPassed(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	ctx := context.Background()

	// Create and deliver an escrow
	esc, err := svc.Create(ctx, CreateRequest{
		BuyerAddr:  "0xbuyer",
		SellerAddr: "0xseller",
		Amount:     "10.000000",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Mark delivered
	esc, err = svc.MarkDelivered(ctx, esc.ID, "0xseller")
	if err != nil {
		t.Fatal(err)
	}

	// Set dispute window to the past so auto-release fires
	past := time.Now().Add(-1 * time.Hour)
	esc.DisputeWindowUntil = &past
	esc.AutoReleaseAt = time.Now().Add(-1 * time.Hour)
	store.Update(ctx, esc)

	timer := NewTimer(svc, store, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	timer.releaseExpired(ctx)

	// AutoRelease sets status to "expired" (auto-released to seller)
	updated, _ := store.Get(ctx, esc.ID)
	if updated.Status != StatusExpired {
		t.Errorf("expected expired (auto-released), got %s", updated.Status)
	}
}

func TestTimer_ReleaseExpired_DeliveredDisputeWindowStillOpen(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	ctx := context.Background()

	esc, _ := svc.Create(ctx, CreateRequest{
		BuyerAddr:  "0xbuyer",
		SellerAddr: "0xseller",
		Amount:     "10.000000",
	})
	esc, _ = svc.MarkDelivered(ctx, esc.ID, "0xseller")

	// Dispute window is in the future
	future := time.Now().Add(1 * time.Hour)
	esc.DisputeWindowUntil = &future
	esc.AutoReleaseAt = time.Now().Add(-1 * time.Hour)
	store.Update(ctx, esc)

	timer := NewTimer(svc, store, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	timer.releaseExpired(ctx)

	// Should NOT be released (dispute window still open)
	updated, _ := store.Get(ctx, esc.ID)
	if updated.Status != StatusDelivered {
		t.Errorf("expected delivered (dispute window open), got %s", updated.Status)
	}
}

func TestTimer_ResolveExpiredArbitrations(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	ctx := context.Background()

	esc, _ := svc.Create(ctx, CreateRequest{
		BuyerAddr:  "0xbuyer",
		SellerAddr: "0xseller",
		Amount:     "5.000000",
	})

	// Move to disputed then arbitrating
	esc, _ = svc.Dispute(ctx, esc.ID, "0xbuyer", "bad work")
	esc.Status = StatusArbitrating
	esc.ArbitratorAddr = "0xarbiter"
	deadline := time.Now().Add(-1 * time.Hour)
	esc.ArbitrationDeadline = &deadline
	store.Update(ctx, esc)

	timer := NewTimer(svc, store, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	timer.resolveExpiredArbitrations(ctx, time.Now())

	updated, _ := store.Get(ctx, esc.ID)
	if updated.Status != StatusReleased {
		t.Errorf("expected released after arbitration deadline, got %s", updated.Status)
	}
}

func TestTimer_ResolveExpiredArbitrations_DeadlineNotPassed(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	ctx := context.Background()

	esc, _ := svc.Create(ctx, CreateRequest{
		BuyerAddr:  "0xbuyer",
		SellerAddr: "0xseller",
		Amount:     "5.000000",
	})

	esc, _ = svc.Dispute(ctx, esc.ID, "0xbuyer", "bad work")
	esc.Status = StatusArbitrating
	esc.ArbitratorAddr = "0xarbiter"
	future := time.Now().Add(1 * time.Hour)
	esc.ArbitrationDeadline = &future
	store.Update(ctx, esc)

	timer := NewTimer(svc, store, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	timer.resolveExpiredArbitrations(ctx, time.Now())

	updated, _ := store.Get(ctx, esc.ID)
	if updated.Status != StatusArbitrating {
		t.Errorf("expected still arbitrating, got %s", updated.Status)
	}
}
