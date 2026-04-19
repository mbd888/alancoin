package flywheel

import (
	"context"
	"log/slog"
	"strconv"
	"sync"
)

// RevenueAccumulator tracks seller revenue from all payment paths
// (gateway, escrow, streams, session keys). This data feeds velocity
// metrics and will serve as the foundation for a future staking system.
//
// The accumulator is fire-and-forget — errors are logged but never block
// the payment path that triggered accumulation.
type RevenueAccumulator struct {
	mu      sync.Mutex
	totals  map[string]float64 // agentAddr → total revenue accumulated
	pending []RevenueEntry     // buffered entries for batch processing
	logger  *slog.Logger
}

// RevenueEntry records a single revenue event.
type RevenueEntry struct {
	AgentAddr string
	Amount    string
	TxRef     string
}

// NewRevenueAccumulator creates a new revenue accumulator.
func NewRevenueAccumulator(logger *slog.Logger) *RevenueAccumulator {
	return &RevenueAccumulator{
		totals: make(map[string]float64),
		logger: logger,
	}
}

// AccumulateRevenue records revenue earned by a seller. This is called from
// gateway proxy, escrow confirm/release, stream settle, and session key transact.
//
// The method satisfies gateway.RevenueAccumulator, escrow.RevenueAccumulator,
// streams.RevenueAccumulator, and sessionkeys.RevenueAccumulator — all four
// interfaces share the same signature.
func (r *RevenueAccumulator) AccumulateRevenue(_ context.Context, agentAddr, amount, txRef string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.pending = append(r.pending, RevenueEntry{
		AgentAddr: agentAddr,
		Amount:    amount,
		TxRef:     txRef,
	})

	if f, err := strconv.ParseFloat(amount, 64); err == nil {
		r.totals[agentAddr] += f
	}

	r.logger.Debug("revenue accumulated",
		"agent", agentAddr,
		"amount", amount,
		"ref", txRef,
	)

	return nil
}

// DrainPending returns and clears the buffered revenue entries.
// This is used by periodic workers that batch-process revenue.
func (r *RevenueAccumulator) DrainPending() []RevenueEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	entries := r.pending
	r.pending = nil
	return entries
}

// TotalRevenue returns the accumulated revenue for an agent.
func (r *RevenueAccumulator) TotalRevenue(agentAddr string) float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.totals[agentAddr]
}
