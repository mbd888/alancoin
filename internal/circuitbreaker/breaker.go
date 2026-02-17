// Package circuitbreaker provides a per-key circuit breaker with
// closed → open → half-open state transitions.
package circuitbreaker

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// State represents the circuit breaker state.
type State int

const (
	StateClosed   State = iota // Normal: requests flow through
	StateOpen                  // Tripped: requests are rejected
	StateHalfOpen              // Probing: one request allowed to test recovery
)

// String returns the state name.
func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half_open"
	default:
		return "unknown"
	}
}

var cbStateTransitions = prometheus.NewCounterVec(prometheus.CounterOpts{
	Namespace: "alancoin",
	Subsystem: "circuitbreaker",
	Name:      "state_transitions_total",
	Help:      "Circuit breaker state transitions by key, from-state, and to-state.",
}, []string{"key", "from_state", "to_state"})

func init() {
	prometheus.MustRegister(cbStateTransitions)
}

// entry tracks per-key circuit state.
type entry struct {
	state       State
	failures    int
	lastFailure time.Time
}

// Breaker is a per-key circuit breaker. It tracks failure counts per key
// and trips open when failures exceed the threshold. After openDuration,
// the circuit moves to half-open and allows one probe request.
type Breaker struct {
	mu           sync.Mutex
	entries      map[string]*entry
	threshold    int
	openDuration time.Duration
	onTransition func(key string, from, to State) // optional callback for metrics
}

// New creates a circuit breaker that opens after threshold consecutive
// failures and stays open for openDuration before probing.
func New(threshold int, openDuration time.Duration) *Breaker {
	if threshold <= 0 {
		threshold = 5
	}
	if openDuration <= 0 {
		openDuration = 30 * time.Second
	}
	return &Breaker{
		entries:      make(map[string]*entry),
		threshold:    threshold,
		openDuration: openDuration,
	}
}

// OnTransition sets a callback invoked on state changes (for metrics).
func (b *Breaker) OnTransition(fn func(key string, from, to State)) {
	b.mu.Lock()
	b.onTransition = fn
	b.mu.Unlock()
}

// Allow returns true if a request to key should be allowed.
// If the circuit is open and openDuration has elapsed, it transitions to half-open.
func (b *Breaker) Allow(key string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	e, ok := b.entries[key]
	if !ok {
		return true // No entry = closed
	}

	switch e.state {
	case StateClosed:
		return true
	case StateOpen:
		if time.Since(e.lastFailure) >= b.openDuration {
			b.transition(e, key, StateHalfOpen)
			return true // Allow one probe
		}
		return false
	case StateHalfOpen:
		return false // Already probing — reject until probe completes
	default:
		return true
	}
}

// RecordSuccess records a successful request. Resets failure count and
// closes the circuit if it was half-open.
func (b *Breaker) RecordSuccess(key string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	e, ok := b.entries[key]
	if !ok {
		return
	}

	if e.state == StateHalfOpen {
		b.transition(e, key, StateClosed)
	}
	e.failures = 0
}

// RecordFailure records a failed request. If consecutive failures exceed
// the threshold, trips the circuit open.
func (b *Breaker) RecordFailure(key string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	e, ok := b.entries[key]
	if !ok {
		e = &entry{state: StateClosed}
		b.entries[key] = e
	}

	e.failures++
	e.lastFailure = time.Now()

	if e.state == StateHalfOpen {
		// Probe failed — back to open.
		b.transition(e, key, StateOpen)
		return
	}

	if e.state == StateClosed && e.failures >= b.threshold {
		b.transition(e, key, StateOpen)
	}
}

// State returns the current state for a key. Returns StateClosed for unknown keys.
func (b *Breaker) State(key string) State {
	b.mu.Lock()
	defer b.mu.Unlock()

	e, ok := b.entries[key]
	if !ok {
		return StateClosed
	}
	return e.state
}

// transition changes state and fires the callback if set.
// Caller must hold b.mu.
func (b *Breaker) transition(e *entry, key string, to State) {
	from := e.state
	if from == to {
		return
	}
	e.state = to
	cbStateTransitions.WithLabelValues(key, from.String(), to.String()).Inc()
	if b.onTransition != nil {
		fn := b.onTransition
		go fn(key, from, to)
	}
}
