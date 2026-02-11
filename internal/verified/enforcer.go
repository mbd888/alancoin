package verified

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"
)

// ContractCallProvider fetches recent call results for SLA enforcement.
type ContractCallProvider interface {
	// GetRecentCallsByAgent returns the most recent call results for contracts
	// where the given address is the seller, within the specified window size.
	GetRecentCallsByAgent(ctx context.Context, agentAddr string, windowSize int) (successCount, totalCount int, err error)
}

// Enforcer periodically checks verified agents' rolling success rates
// and triggers violations when they drop below the guaranteed threshold.
type Enforcer struct {
	service           *Service
	callProvider      ContractCallProvider
	guaranteeFundAddr string // Platform address that receives forfeited bonds
	interval          time.Duration
	logger            *slog.Logger
	stop              chan struct{}
	running           atomic.Bool
}

// NewEnforcer creates a new guarantee enforcement timer.
func NewEnforcer(service *Service, callProvider ContractCallProvider, guaranteeFundAddr string, logger *slog.Logger) *Enforcer {
	return &Enforcer{
		service:           service,
		callProvider:      callProvider,
		guaranteeFundAddr: guaranteeFundAddr,
		interval:          30 * time.Second,
		logger:            logger,
		stop:              make(chan struct{}),
	}
}

// Running reports whether the enforcer loop is actively running.
func (e *Enforcer) Running() bool {
	return e.running.Load()
}

// Start begins the enforcement check loop.
func (e *Enforcer) Start(ctx context.Context) {
	e.running.Store(true)
	defer e.running.Store(false)

	ticker := time.NewTicker(e.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-e.stop:
			return
		case <-ticker.C:
			e.safeCheck(ctx)
		}
	}
}

// Stop signals the enforcer to stop.
func (e *Enforcer) Stop() {
	select {
	case e.stop <- struct{}{}:
	default:
	}
}

func (e *Enforcer) safeCheck(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			e.logger.Error("panic in verified enforcer", "panic", fmt.Sprint(r))
		}
	}()
	e.checkAll(ctx)
}

func (e *Enforcer) checkAll(ctx context.Context) {
	verifications, err := e.service.ListActive(ctx, 1000)
	if err != nil {
		e.logger.Warn("failed to list active verifications", "error", err)
		return
	}

	for _, v := range verifications {
		e.checkAgent(ctx, v)
	}
}

func (e *Enforcer) checkAgent(ctx context.Context, v *Verification) {
	successCount, totalCount, err := e.callProvider.GetRecentCallsByAgent(ctx, v.AgentAddr, v.SLAWindowSize)
	if err != nil {
		e.logger.Warn("failed to get recent calls for verified agent",
			"agent", v.AgentAddr, "error", err)
		return
	}

	// Need at least a full window to evaluate
	if totalCount < v.SLAWindowSize {
		return
	}

	windowRate := float64(successCount) / float64(totalCount) * 100.0

	if windowRate < v.GuaranteedSuccessRate {
		e.logger.Warn("verified agent SLA violation detected",
			"agent", v.AgentAddr,
			"windowRate", windowRate,
			"guaranteed", v.GuaranteedSuccessRate,
			"window", v.SLAWindowSize,
		)

		if _, err := e.service.RecordViolation(ctx, v.AgentAddr, windowRate, e.guaranteeFundAddr); err != nil {
			e.logger.Error("failed to record violation",
				"agent", v.AgentAddr, "error", err)
		}
	}

	// Update monitoring count
	v.TotalCallsMonitored += totalCount
	v.UpdatedAt = time.Now()
	_ = e.service.store.Update(ctx, v)
}
