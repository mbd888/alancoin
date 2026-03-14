package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// RACERConfig configures the RACER (Risk-Aware Calibrated Expansion Router).
type RACERConfig struct {
	// ConfidenceThreshold is the minimum score gap between the top two candidates
	// required to avoid expanding to a candidate set. When the gap is below this
	// threshold, the router has low confidence in the top pick and races multiple
	// candidates. Range: 0.0–1.0, where 0 always races and 1 never races.
	ConfidenceThreshold float64

	// MaxCandidates is the maximum number of providers to race concurrently
	// when confidence is low. Default: 3.
	MaxCandidates int

	// RaceTimeout is the deadline for racing candidates. The first successful
	// response wins; others are cancelled. Default: 10s.
	RaceTimeout time.Duration
}

// DefaultRACERConfig returns production-quality defaults.
func DefaultRACERConfig() RACERConfig {
	return RACERConfig{
		ConfidenceThreshold: 0.3,
		MaxCandidates:       3,
		RaceTimeout:         10 * time.Second,
	}
}

// RACERResult wraps a race outcome with metadata for calibration tracking.
type RACERResult struct {
	Candidate ServiceCandidate // The winning candidate
	Expanded  bool             // True if the candidate set was expanded (low confidence)
	SetSize   int              // Number of candidates raced
	Latency   time.Duration    // Time to get the winning response
}

// RACER implements risk-aware calibrated expansion routing.
// When the router has low confidence in the top candidate (similar scores),
// it expands to a candidate set and races them, using the fastest successful
// response. This reduces tail latency and improves reliability.
type RACER struct {
	config         RACERConfig
	logger         *slog.Logger
	expansionCount atomic.Int64 // total expansions for metrics
	totalRequests  atomic.Int64 // total requests for calibration
	onExpansion    func(setSize int)
	onExpansionMu  sync.RWMutex
}

// NewRACER creates a new RACER with the given configuration.
func NewRACER(config RACERConfig, logger *slog.Logger) *RACER {
	if config.MaxCandidates <= 0 {
		config.MaxCandidates = 3
	}
	if config.RaceTimeout <= 0 {
		config.RaceTimeout = 10 * time.Second
	}
	return &RACER{
		config: config,
		logger: logger,
	}
}

// OnExpansion sets a callback invoked when the candidate set is expanded.
func (r *RACER) OnExpansion(fn func(setSize int)) {
	r.onExpansionMu.Lock()
	r.onExpansion = fn
	r.onExpansionMu.Unlock()
}

// ExpansionCount returns the total number of requests that triggered expansion.
func (r *RACER) ExpansionCount() int64 {
	return r.expansionCount.Load()
}

// TotalRequests returns the total number of requests processed.
func (r *RACER) TotalRequests() int64 {
	return r.totalRequests.Load()
}

// ExpansionRate returns the fraction of requests that needed expansion (for calibration).
func (r *RACER) ExpansionRate() float64 {
	total := r.totalRequests.Load()
	if total == 0 {
		return 0
	}
	return float64(r.expansionCount.Load()) / float64(total)
}

// ShouldExpand determines whether the candidate set should be expanded
// based on the confidence threshold. Returns true if scores are too close.
func (r *RACER) ShouldExpand(candidates []ServiceCandidate) bool {
	if len(candidates) < 2 {
		return false
	}

	// Compute confidence as the normalized gap between the top two scores.
	// A larger gap means higher confidence in the top pick.
	topScore := candidateScore(candidates[0])
	secondScore := candidateScore(candidates[1])

	if topScore == 0 {
		return true // No score data — expand.
	}

	gap := (topScore - secondScore) / topScore
	return gap < r.config.ConfidenceThreshold
}

// candidateScore computes a composite score for comparison.
// Uses reputation + inverse price for a balanced ranking.
func candidateScore(c ServiceCandidate) float64 {
	return c.ReputationScore + c.TraceRankScore
}

// Race runs multiple candidates concurrently and returns the first success.
// The tryFn is called for each candidate; the first to return nil error wins.
// Other in-flight candidates are cancelled via context.
func (r *RACER) Race(ctx context.Context, candidates []ServiceCandidate, tryFn func(ctx context.Context, c ServiceCandidate) error) (*RACERResult, error) {
	r.totalRequests.Add(1)

	if len(candidates) == 0 {
		return nil, fmt.Errorf("racer: no candidates to race")
	}

	// If confidence is high, just try the top candidate.
	if !r.ShouldExpand(candidates) {
		start := time.Now()
		if err := tryFn(ctx, candidates[0]); err != nil {
			return nil, fmt.Errorf("racer: top candidate failed: %w", err)
		}
		return &RACERResult{
			Candidate: candidates[0],
			Expanded:  false,
			SetSize:   1,
			Latency:   time.Since(start),
		}, nil
	}

	// Low confidence — expand and race.
	setSize := len(candidates)
	if setSize > r.config.MaxCandidates {
		setSize = r.config.MaxCandidates
	}
	raceSet := candidates[:setSize]

	r.expansionCount.Add(1)
	r.onExpansionMu.RLock()
	fn := r.onExpansion
	r.onExpansionMu.RUnlock()
	if fn != nil {
		fn(setSize)
	}

	if r.logger != nil {
		r.logger.Info("RACER expanding candidate set",
			"set_size", setSize,
			"total_candidates", len(candidates))
	}

	// Race with a timeout.
	raceCtx, cancel := context.WithTimeout(ctx, r.config.RaceTimeout)
	defer cancel()

	type raceResult struct {
		idx int
		err error
	}

	results := make(chan raceResult, setSize)
	start := time.Now()

	for i := range raceSet {
		idx := i
		go func() {
			err := tryFn(raceCtx, raceSet[idx])
			results <- raceResult{idx: idx, err: err}
		}()
	}

	// Wait for the first success or all failures.
	var errors []error
	for range raceSet {
		select {
		case res := <-results:
			if res.err == nil {
				cancel() // Cancel remaining races.
				return &RACERResult{
					Candidate: raceSet[res.idx],
					Expanded:  true,
					SetSize:   setSize,
					Latency:   time.Since(start),
				}, nil
			}
			errors = append(errors, res.err)
		case <-raceCtx.Done():
			return nil, fmt.Errorf("racer: race timed out after %v with %d/%d failures", r.config.RaceTimeout, len(errors), setSize)
		}
	}

	// All candidates failed.
	return nil, fmt.Errorf("racer: all %d candidates failed: %v", setSize, errors)
}

// SelectCandidateSet returns the candidates that should be raced.
// If confidence is high, returns only the top candidate.
// If confidence is low, returns up to MaxCandidates.
func (r *RACER) SelectCandidateSet(candidates []ServiceCandidate) []ServiceCandidate {
	if len(candidates) == 0 {
		return nil
	}

	if !r.ShouldExpand(candidates) {
		return candidates[:1]
	}

	setSize := len(candidates)
	if setSize > r.config.MaxCandidates {
		setSize = r.config.MaxCandidates
	}
	return candidates[:setSize]
}
