package gateway

import (
	"math"
	"sort"
	"sync"
	"time"
)

// HealthState represents the health status of a provider.
type HealthState int

const (
	HealthUnknown   HealthState = iota // No data collected yet
	HealthHealthy                      // Operating normally
	HealthDegraded                     // Elevated errors or latency
	HealthUnhealthy                    // Critical failure rate
)

// String returns the health state name.
func (s HealthState) String() string {
	switch s {
	case HealthHealthy:
		return "healthy"
	case HealthDegraded:
		return "degraded"
	case HealthUnhealthy:
		return "unhealthy"
	default:
		return "unknown"
	}
}

// HealthStatus is the computed health for a single provider.
type HealthStatus struct {
	State       HealthState
	ErrorRate   float64       // 0.0–1.0
	SuccessRate float64       // 0.0–1.0
	P50Latency  time.Duration // Median latency
	P95Latency  time.Duration // 95th percentile latency
	P99Latency  time.Duration // 99th percentile latency
	TotalCalls  int
	LastUpdated time.Time
}

// HealthMonitorConfig configures thresholds for health state transitions.
type HealthMonitorConfig struct {
	Window                time.Duration // Sliding window duration (default 5m)
	DegradedErrorRate     float64       // Error rate threshold for Degraded (default 0.1)
	UnhealthyErrorRate    float64       // Error rate threshold for Unhealthy (default 0.5)
	DegradedP95Latency    time.Duration // P95 latency threshold for Degraded (default 5s)
	MinSamplesForDecision int           // Minimum observations before leaving Unknown (default 5)
}

// DefaultHealthMonitorConfig returns production-quality defaults.
func DefaultHealthMonitorConfig() HealthMonitorConfig {
	return HealthMonitorConfig{
		Window:                5 * time.Minute,
		DegradedErrorRate:     0.1,
		UnhealthyErrorRate:    0.5,
		DegradedP95Latency:    5 * time.Second,
		MinSamplesForDecision: 5,
	}
}

// observation records a single request outcome.
type observation struct {
	timestamp time.Time
	latency   time.Duration
	success   bool
}

// providerStats holds raw observations for one provider.
type providerStats struct {
	observations []observation
}

// HealthMonitor tracks per-provider health metrics using a sliding window.
// Thread-safe for concurrent updates from multiple goroutines.
type HealthMonitor struct {
	mu       sync.RWMutex
	config   HealthMonitorConfig
	stats    map[string]*providerStats
	nowFunc  func() time.Time // overridable clock for testing
	onChange func(providerID string, from, to HealthState)
}

// NewHealthMonitor creates a health monitor with the given configuration.
func NewHealthMonitor(config HealthMonitorConfig) *HealthMonitor {
	return &HealthMonitor{
		config:  config,
		stats:   make(map[string]*providerStats),
		nowFunc: time.Now,
	}
}

// OnStateChange sets a callback invoked when a provider's health state changes.
func (hm *HealthMonitor) OnStateChange(fn func(providerID string, from, to HealthState)) {
	hm.mu.Lock()
	hm.onChange = fn
	hm.mu.Unlock()
}

// RecordSuccess records a successful request with the given latency.
func (hm *HealthMonitor) RecordSuccess(providerID string, latency time.Duration) {
	hm.record(providerID, latency, true)
}

// RecordFailure records a failed request.
func (hm *HealthMonitor) RecordFailure(providerID string, err error) {
	hm.record(providerID, 0, false)
}

func (hm *HealthMonitor) record(providerID string, latency time.Duration, success bool) {
	now := hm.nowFunc()

	hm.mu.Lock()
	ps, ok := hm.stats[providerID]
	if !ok {
		ps = &providerStats{}
		hm.stats[providerID] = ps
	}

	// Compute state before the new observation.
	oldState := hm.computeStateLocked(ps, now)

	ps.observations = append(ps.observations, observation{
		timestamp: now,
		latency:   latency,
		success:   success,
	})

	// Prune expired observations while we hold the lock.
	hm.pruneLocked(ps, now)

	// Compute new state.
	newState := hm.computeStateLocked(ps, now)
	onChange := hm.onChange
	hm.mu.Unlock()

	// Fire callback outside the lock to avoid deadlock.
	if onChange != nil && oldState != newState {
		onChange(providerID, oldState, newState)
	}
}

// GetHealth returns the computed health status for a provider.
func (hm *HealthMonitor) GetHealth(providerID string) HealthStatus {
	now := hm.nowFunc()

	hm.mu.RLock()
	ps, ok := hm.stats[providerID]
	if !ok {
		hm.mu.RUnlock()
		return HealthStatus{State: HealthUnknown}
	}

	status := hm.computeStatusLocked(ps, now)
	hm.mu.RUnlock()
	return status
}

// GetAllHealth returns health status for all tracked providers.
func (hm *HealthMonitor) GetAllHealth() map[string]HealthStatus {
	now := hm.nowFunc()
	result := make(map[string]HealthStatus)

	hm.mu.RLock()
	for id, ps := range hm.stats {
		result[id] = hm.computeStatusLocked(ps, now)
	}
	hm.mu.RUnlock()

	return result
}

// pruneLocked removes observations outside the sliding window.
// Caller must hold hm.mu (write lock).
func (hm *HealthMonitor) pruneLocked(ps *providerStats, now time.Time) {
	cutoff := now.Add(-hm.config.Window)
	i := 0
	for i < len(ps.observations) && ps.observations[i].timestamp.Before(cutoff) {
		i++
	}
	if i > 0 {
		ps.observations = ps.observations[i:]
	}
}

// computeStateLocked returns the health state from current observations.
// Caller must hold hm.mu (at least read lock).
func (hm *HealthMonitor) computeStateLocked(ps *providerStats, now time.Time) HealthState {
	cutoff := now.Add(-hm.config.Window)
	var successes, failures int

	for _, obs := range ps.observations {
		if obs.timestamp.Before(cutoff) {
			continue
		}
		if obs.success {
			successes++
		} else {
			failures++
		}
	}

	total := successes + failures
	if total < hm.config.MinSamplesForDecision {
		return HealthUnknown
	}

	errorRate := float64(failures) / float64(total)

	if errorRate >= hm.config.UnhealthyErrorRate {
		return HealthUnhealthy
	}

	if errorRate >= hm.config.DegradedErrorRate {
		return HealthDegraded
	}

	// Check P95 latency threshold.
	p95 := hm.computePercentileLocked(ps, now, 0.95)
	if p95 > hm.config.DegradedP95Latency {
		return HealthDegraded
	}

	return HealthHealthy
}

// computeStatusLocked builds a full HealthStatus from provider stats.
// Caller must hold hm.mu (at least read lock).
func (hm *HealthMonitor) computeStatusLocked(ps *providerStats, now time.Time) HealthStatus {
	cutoff := now.Add(-hm.config.Window)
	var successes, failures int
	var lastUpdated time.Time

	for _, obs := range ps.observations {
		if obs.timestamp.Before(cutoff) {
			continue
		}
		if obs.success {
			successes++
		} else {
			failures++
		}
		if obs.timestamp.After(lastUpdated) {
			lastUpdated = obs.timestamp
		}
	}

	total := successes + failures
	if total == 0 {
		return HealthStatus{State: HealthUnknown}
	}

	errorRate := float64(failures) / float64(total)
	successRate := float64(successes) / float64(total)

	return HealthStatus{
		State:       hm.computeStateLocked(ps, now),
		ErrorRate:   errorRate,
		SuccessRate: successRate,
		P50Latency:  hm.computePercentileLocked(ps, now, 0.50),
		P95Latency:  hm.computePercentileLocked(ps, now, 0.95),
		P99Latency:  hm.computePercentileLocked(ps, now, 0.99),
		TotalCalls:  total,
		LastUpdated: lastUpdated,
	}
}

// computePercentileLocked computes a latency percentile from successful observations.
// Caller must hold hm.mu (at least read lock).
func (hm *HealthMonitor) computePercentileLocked(ps *providerStats, now time.Time, percentile float64) time.Duration {
	cutoff := now.Add(-hm.config.Window)

	var latencies []time.Duration
	for _, obs := range ps.observations {
		if obs.timestamp.Before(cutoff) || !obs.success {
			continue
		}
		latencies = append(latencies, obs.latency)
	}

	if len(latencies) == 0 {
		return 0
	}

	sort.Slice(latencies, func(i, j int) bool {
		return latencies[i] < latencies[j]
	})

	idx := int(math.Ceil(percentile*float64(len(latencies)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(latencies) {
		idx = len(latencies) - 1
	}

	return latencies[idx]
}
