package billing

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// tenantCounters holds atomic usage counters for a single tenant.
type tenantCounters struct {
	requests   atomic.Int64
	volumeUsdc atomic.Int64 // stored as micro-USDC (1 USDC = 1_000_000) for atomic ops
	customerID string
}

// Meter aggregates per-tenant usage in memory and periodically flushes to the
// billing provider. This keeps metering off the proxy hot path — the only cost
// per request is an atomic int increment.
type Meter struct {
	provider Provider
	counters map[string]*tenantCounters // tenantID → counters
	mu       sync.RWMutex
	interval time.Duration
	logger   *slog.Logger
}

// NewMeter creates an async usage meter that flushes to the given provider.
func NewMeter(provider Provider, logger *slog.Logger) *Meter {
	return &Meter{
		provider: provider,
		counters: make(map[string]*tenantCounters),
		interval: 60 * time.Second,
		logger:   logger,
	}
}

// getOrCreate returns (or lazily creates) counters for a tenant.
func (m *Meter) getOrCreate(tenantID, customerID string) *tenantCounters {
	m.mu.RLock()
	tc, ok := m.counters[tenantID]
	m.mu.RUnlock()
	if ok {
		return tc
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	// Double-check after acquiring write lock.
	if tc, ok = m.counters[tenantID]; ok {
		return tc
	}
	tc = &tenantCounters{customerID: customerID}
	m.counters[tenantID] = tc
	return tc
}

// RecordRequest increments the request counter for a tenant.
// Called from the gateway proxy hot path — lock-free atomic increment.
func (m *Meter) RecordRequest(tenantID, customerID string) {
	if tenantID == "" || customerID == "" {
		return
	}
	m.getOrCreate(tenantID, customerID).requests.Add(1)
}

// RecordVolume adds settled volume (in micro-USDC) for a tenant.
// Called from settlement — lock-free atomic add.
func (m *Meter) RecordVolume(tenantID, customerID string, microUSDC int64) {
	if tenantID == "" || customerID == "" || microUSDC <= 0 {
		return
	}
	m.getOrCreate(tenantID, customerID).volumeUsdc.Add(microUSDC)
}

// Flush pushes accumulated counters to the billing provider and resets them.
// Called by the gateway timer every sweep cycle.
func (m *Meter) Flush(ctx context.Context) {
	m.mu.RLock()
	tenantIDs := make([]string, 0, len(m.counters))
	for id := range m.counters {
		tenantIDs = append(tenantIDs, id)
	}
	m.mu.RUnlock()

	for _, tenantID := range tenantIDs {
		m.mu.RLock()
		tc, ok := m.counters[tenantID]
		m.mu.RUnlock()
		if !ok {
			continue
		}

		// Swap counters to zero atomically — if flush fails, we lose this
		// batch's counts but avoid unbounded accumulation.
		requests := tc.requests.Swap(0)
		volume := tc.volumeUsdc.Swap(0)

		if requests == 0 && volume == 0 {
			continue
		}

		if err := m.provider.ReportUsage(ctx, tc.customerID, requests, volume); err != nil {
			// Put the counts back so they'll be retried next flush.
			tc.requests.Add(requests)
			tc.volumeUsdc.Add(volume)
			m.logger.Warn("billing meter flush failed, will retry",
				"tenant", tenantID, "requests", requests, "volume", volume, "error", err)
		}
	}
}
