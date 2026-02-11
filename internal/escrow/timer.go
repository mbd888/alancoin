package escrow

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"
)

// Timer periodically checks for expired escrows and auto-releases them.
type Timer struct {
	service  *Service
	store    Store
	interval time.Duration
	logger   *slog.Logger
	stop     chan struct{}
	running  atomic.Bool
}

// NewTimer creates a new escrow auto-release timer.
func NewTimer(service *Service, store Store, logger *slog.Logger) *Timer {
	return &Timer{
		service:  service,
		store:    store,
		interval: 30 * time.Second,
		logger:   logger,
		stop:     make(chan struct{}),
	}
}

// Running reports whether the timer loop is actively running.
func (t *Timer) Running() bool {
	return t.running.Load()
}

// Start begins the auto-release loop. Call in a goroutine.
func (t *Timer) Start(ctx context.Context) {
	t.running.Store(true)
	defer t.running.Store(false)

	ticker := time.NewTicker(t.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.stop:
			return
		case <-ticker.C:
			t.safeReleaseExpired(ctx)
		}
	}
}

// Stop signals the timer to stop.
func (t *Timer) Stop() {
	select {
	case t.stop <- struct{}{}:
	default:
	}
}

func (t *Timer) safeReleaseExpired(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			t.logger.Error("panic in escrow timer", "panic", fmt.Sprint(r))
		}
	}()
	t.releaseExpired(ctx)
}

func (t *Timer) releaseExpired(ctx context.Context) {
	now := time.Now()

	// 1. Auto-release escrows past their auto-release deadline
	expired, err := t.store.ListExpired(ctx, now, 100)
	if err != nil {
		t.logger.Warn("failed to list expired escrows", "error", err)
		return
	}

	for _, escrow := range expired {
		// Delivered escrows must wait for the dispute window to pass before auto-release.
		if escrow.Status == StatusDelivered {
			if escrow.DisputeWindowUntil != nil && now.After(*escrow.DisputeWindowUntil) {
				if err := t.service.AutoRelease(ctx, escrow); err != nil {
					t.logger.Warn("failed to auto-release escrow after dispute window",
						"escrowId", escrow.ID, "error", err)
				} else {
					t.logger.Info("auto-released escrow (dispute window expired)",
						"escrowId", escrow.ID, "seller", escrow.SellerAddr, "amount", escrow.Amount)
				}
			} else {
				t.logger.Debug("skipping delivered escrow, dispute window still open",
					"escrowId", escrow.ID, "disputeWindowUntil", escrow.DisputeWindowUntil)
			}
			continue
		}

		if err := t.service.AutoRelease(ctx, escrow); err != nil {
			t.logger.Warn("failed to auto-release escrow",
				"escrowId", escrow.ID,
				"error", err,
			)
			continue
		}
		t.logger.Info("auto-released escrow",
			"escrowId", escrow.ID,
			"buyer", escrow.BuyerAddr,
			"seller", escrow.SellerAddr,
			"amount", escrow.Amount,
		)
	}

	// 2. Auto-resolve arbitrations past their deadline (default: release to seller)
	t.resolveExpiredArbitrations(ctx, now)
}

func (t *Timer) resolveExpiredArbitrations(ctx context.Context, now time.Time) {
	arbitrating, err := t.store.ListByStatus(ctx, StatusArbitrating, 100)
	if err != nil {
		return // ListByStatus may not be implemented in all stores
	}

	for _, escrow := range arbitrating {
		if escrow.ArbitrationDeadline == nil || !now.After(*escrow.ArbitrationDeadline) {
			continue
		}

		// Default: release to seller after arbitration deadline
		_, err := t.service.ResolveArbitration(ctx, escrow.ID, escrow.ArbitratorAddr, ResolveRequest{
			Resolution: "release",
			Reason:     "arbitration deadline expired, auto-released to seller",
		})
		if err != nil {
			t.logger.Warn("failed to auto-resolve expired arbitration",
				"escrowId", escrow.ID, "error", err)
			continue
		}
		t.logger.Info("auto-resolved expired arbitration",
			"escrowId", escrow.ID, "seller", escrow.SellerAddr, "amount", escrow.Amount)
	}
}
