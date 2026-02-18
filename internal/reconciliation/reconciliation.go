// Package reconciliation provides periodic cross-subsystem financial consistency checks.
package reconciliation

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// Report summarizes the results of a reconciliation run.
type Report struct {
	LedgerMismatches int           `json:"ledgerMismatches"`
	StuckEscrows     int           `json:"stuckEscrows"`
	StaleStreams     int           `json:"staleStreams"`
	OrphanedHolds    int           `json:"orphanedHolds"`
	Healthy          bool          `json:"healthy"`
	Duration         time.Duration `json:"durationMs"`
	Timestamp        time.Time     `json:"timestamp"`
}

// LedgerChecker reconciles ledger event replay against actual balances.
type LedgerChecker interface {
	CheckAll(ctx context.Context) (mismatches int, err error)
}

// EscrowChecker checks for stuck escrows past their auto-release deadline.
type EscrowChecker interface {
	CountExpired(ctx context.Context) (int, error)
}

// StreamChecker checks for stale open streams.
type StreamChecker interface {
	CountStale(ctx context.Context) (int, error)
}

// HoldChecker checks for orphaned ledger holds with no matching session/stream/escrow.
type HoldChecker interface {
	CountOrphaned(ctx context.Context) (int, error)
}

// Runner runs all reconciliation checks and aggregates results.
type Runner struct {
	ledger LedgerChecker
	escrow EscrowChecker
	stream StreamChecker
	hold   HoldChecker
	logger *slog.Logger

	lastMu     sync.RWMutex
	lastReport *Report
}

// NewRunner creates a new reconciliation runner.
func NewRunner(logger *slog.Logger) *Runner {
	return &Runner{logger: logger}
}

// WithLedger sets the ledger checker.
func (r *Runner) WithLedger(c LedgerChecker) *Runner {
	r.ledger = c
	return r
}

// WithEscrow sets the escrow checker.
func (r *Runner) WithEscrow(c EscrowChecker) *Runner {
	r.escrow = c
	return r
}

// WithStream sets the stream checker.
func (r *Runner) WithStream(c StreamChecker) *Runner {
	r.stream = c
	return r
}

// WithHold sets the hold checker.
func (r *Runner) WithHold(c HoldChecker) *Runner {
	r.hold = c
	return r
}

// RunAll executes all reconciliation checks and returns a consolidated report.
func (r *Runner) RunAll(ctx context.Context) (*Report, error) {
	start := time.Now()
	report := &Report{Timestamp: start}

	if r.ledger != nil {
		mismatches, err := r.ledger.CheckAll(ctx)
		if err != nil {
			r.logger.Error("reconciliation: ledger check failed", "error", err)
			reconcileErrors.Inc()
		} else {
			report.LedgerMismatches = mismatches
			reconcileLedgerMismatches.Set(float64(mismatches))
		}
	}

	if r.escrow != nil {
		stuck, err := r.escrow.CountExpired(ctx)
		if err != nil {
			r.logger.Error("reconciliation: escrow check failed", "error", err)
			reconcileErrors.Inc()
		} else {
			report.StuckEscrows = stuck
			reconcileStuckEscrows.Set(float64(stuck))
		}
	}

	if r.stream != nil {
		stale, err := r.stream.CountStale(ctx)
		if err != nil {
			r.logger.Error("reconciliation: stream check failed", "error", err)
			reconcileErrors.Inc()
		} else {
			report.StaleStreams = stale
			reconcileStaleStreams.Set(float64(stale))
		}
	}

	if r.hold != nil {
		orphaned, err := r.hold.CountOrphaned(ctx)
		if err != nil {
			r.logger.Error("reconciliation: hold check failed", "error", err)
			reconcileErrors.Inc()
		} else {
			report.OrphanedHolds = orphaned
			reconcileOrphanedHolds.Set(float64(orphaned))
		}
	}

	report.Duration = time.Since(start)
	report.Healthy = report.LedgerMismatches == 0 &&
		report.StuckEscrows == 0 &&
		report.StaleStreams == 0 &&
		report.OrphanedHolds == 0

	reconcileDuration.Observe(report.Duration.Seconds())

	r.logger.Info("reconciliation complete",
		"ledgerMismatches", report.LedgerMismatches,
		"stuckEscrows", report.StuckEscrows,
		"staleStreams", report.StaleStreams,
		"orphanedHolds", report.OrphanedHolds,
		"healthy", report.Healthy,
		"duration", report.Duration,
	)

	r.lastMu.Lock()
	r.lastReport = report
	r.lastMu.Unlock()

	return report, nil
}

// LastReport returns the most recent reconciliation result, or nil if no run has completed yet.
func (r *Runner) LastReport() *Report {
	r.lastMu.RLock()
	defer r.lastMu.RUnlock()
	return r.lastReport
}
