package risk

import (
	"context"
	"math"
	"strconv"
	"sync"
	"time"

	"github.com/mbd888/alancoin/internal/idgen"
)

// windowEntry records a single transaction for sliding-window analysis.
type windowEntry struct {
	To        string
	AmountUSD float64
	Timestamp time.Time
}

const (
	maxWindowSize  = 1000
	windowDuration = 24 * time.Hour

	weightVelocity  = 0.35
	weightNovelty   = 0.25
	weightTimeOfDay = 0.20
	weightBurnRate  = 0.20
)

// Engine scores transactions using in-memory sliding windows per key.
type Engine struct {
	windows        sync.Map // map[string]*keyWindow
	store          Store
	blockThreshold float64
	warnThreshold  float64
}

type keyWindow struct {
	mu      sync.Mutex
	entries []windowEntry
}

// NewEngine creates a risk scoring engine backed by the given audit store.
func NewEngine(store Store) *Engine {
	return &Engine{
		store:          store,
		blockThreshold: DefaultBlockThreshold,
		warnThreshold:  DefaultWarnThreshold,
	}
}

// WithBlockThreshold overrides the default block threshold.
func (e *Engine) WithBlockThreshold(t float64) *Engine {
	e.blockThreshold = t
	return e
}

// WithWarnThreshold overrides the default warn threshold.
func (e *Engine) WithWarnThreshold(t float64) *Engine {
	e.warnThreshold = t
	return e
}

// Score evaluates a transaction and returns a risk assessment.
// Pure in-memory computation — designed to run in <10ms.
func (e *Engine) Score(ctx context.Context, tx *TransactionContext) *RiskAssessment {
	w := e.getWindow(tx.KeyID)
	w.mu.Lock()
	entries := e.snapshotEntries(w)
	w.mu.Unlock()

	factors := map[string]float64{
		"velocity":    e.velocityFactor(entries, tx.AmountUSDC),
		"novelty":     e.noveltyFactor(entries, tx.To),
		"time_of_day": e.timeOfDayFactor(entries),
		"burn_rate":   e.burnRateFactor(entries, tx),
	}

	score := factors["velocity"]*weightVelocity +
		factors["novelty"]*weightNovelty +
		factors["time_of_day"]*weightTimeOfDay +
		factors["burn_rate"]*weightBurnRate

	// Clamp to [0, 1]
	if score > 1.0 {
		score = 1.0
	}
	if score < 0.0 {
		score = 0.0
	}

	decision := DecisionAllow
	if score >= e.blockThreshold {
		decision = DecisionBlock
	} else if score >= e.warnThreshold {
		decision = DecisionWarn
	}

	assessment := &RiskAssessment{
		ID:          idgen.WithPrefix("risk_"),
		KeyID:       tx.KeyID,
		Score:       math.Round(score*1000) / 1000, // 3 decimal places
		Factors:     factors,
		Decision:    decision,
		EvaluatedAt: time.Now(),
	}

	// Persist asynchronously (best-effort audit trail)
	if e.store != nil {
		go func() {
			_ = e.store.Record(context.Background(), assessment)
		}()
	}

	return assessment
}

// RecordTransaction appends a completed transaction to the sliding window.
func (e *Engine) RecordTransaction(keyID, to, amount string) {
	amountF, _ := strconv.ParseFloat(amount, 64)
	w := e.getWindow(keyID)
	w.mu.Lock()
	defer w.mu.Unlock()

	w.entries = append(w.entries, windowEntry{
		To:        to,
		AmountUSD: amountF,
		Timestamp: time.Now(),
	})

	// Prune old entries and cap size
	e.pruneWindow(w)
}

// getWindow returns or creates the sliding window for a key.
func (e *Engine) getWindow(keyID string) *keyWindow {
	v, _ := e.windows.LoadOrStore(keyID, &keyWindow{})
	return v.(*keyWindow)
}

// snapshotEntries returns a copy of non-expired entries (caller holds lock).
func (e *Engine) snapshotEntries(w *keyWindow) []windowEntry {
	cutoff := time.Now().Add(-windowDuration)
	result := make([]windowEntry, 0, len(w.entries))
	for _, entry := range w.entries {
		if entry.Timestamp.After(cutoff) {
			result = append(result, entry)
		}
	}
	return result
}

// pruneWindow removes entries older than 24h and caps at maxWindowSize.
func (e *Engine) pruneWindow(w *keyWindow) {
	cutoff := time.Now().Add(-windowDuration)
	start := 0
	for start < len(w.entries) && w.entries[start].Timestamp.Before(cutoff) {
		start++
	}
	if start > 0 {
		w.entries = w.entries[start:]
	}
	if len(w.entries) > maxWindowSize {
		w.entries = w.entries[len(w.entries)-maxWindowSize:]
	}
}

// velocityFactor: 5-min spend rate vs 24h average.
// 10x spike = 0.5, 100x spike = 1.0, uses log10 scaling.
func (e *Engine) velocityFactor(entries []windowEntry, currentAmount float64) float64 {
	if len(entries) < 2 {
		return 0.0 // not enough history
	}

	now := time.Now()
	fiveMinAgo := now.Add(-5 * time.Minute)

	var totalSpent24h, spent5min float64
	for _, entry := range entries {
		totalSpent24h += entry.AmountUSD
		if entry.Timestamp.After(fiveMinAgo) {
			spent5min += entry.AmountUSD
		}
	}
	spent5min += currentAmount // include the current transaction

	// Compute 24h average rate per 5-min window (24h = 288 five-minute windows)
	avg5minRate := totalSpent24h / 288.0
	if avg5minRate <= 0 {
		return 0.0
	}

	ratio := spent5min / avg5minRate
	if ratio <= 1.0 {
		return 0.0
	}

	// log10(ratio) / 2: 10x→0.5, 100x→1.0
	score := math.Log10(ratio) / 2.0
	if score > 1.0 {
		score = 1.0
	}
	return math.Round(score*1000) / 1000
}

// noveltyFactor: score based on how many times we've seen this recipient.
// Never seen = 0.6, seen 1-2x = 0.3, seen 3+ = 0.0
func (e *Engine) noveltyFactor(entries []windowEntry, to string) float64 {
	count := 0
	for _, entry := range entries {
		if entry.To == to {
			count++
		}
	}
	switch {
	case count >= 3:
		return 0.0
	case count >= 1:
		return 0.3
	default:
		if len(entries) == 0 {
			// No history at all — cold start, treat as safe
			return 0.0
		}
		return 0.6
	}
}

// timeOfDayFactor: measures how unusual the current hour is relative to history.
// Unusual hour (< 2% of transactions) = 0.8. Insufficient data (<10 txs) = 0.0.
func (e *Engine) timeOfDayFactor(entries []windowEntry) float64 {
	if len(entries) < 10 {
		return 0.0
	}

	// Build hourly histogram
	var histogram [24]int
	for _, entry := range entries {
		histogram[entry.Timestamp.Hour()]++
	}

	currentHour := time.Now().Hour()
	fraction := float64(histogram[currentHour]) / float64(len(entries))

	if fraction < 0.02 {
		return 0.8
	}
	return 0.0
}

// burnRateFactor: at current velocity, how quickly will MaxTotal be exhausted?
// < 1 hour remaining = proportional score (0.0-1.0). Unlimited budget = 0.0.
func (e *Engine) burnRateFactor(entries []windowEntry, tx *TransactionContext) float64 {
	if tx.MaxTotal == "" {
		return 0.0
	}

	maxTotal, err := strconv.ParseFloat(tx.MaxTotal, 64)
	if err != nil || maxTotal <= 0 {
		return 0.0
	}

	totalSpent, _ := strconv.ParseFloat(tx.TotalSpent, 64)
	remaining := maxTotal - totalSpent - tx.AmountUSDC
	if remaining <= 0 {
		return 1.0
	}

	// Calculate recent spending rate (last 1h)
	oneHourAgo := time.Now().Add(-time.Hour)
	var spentLastHour float64
	for _, entry := range entries {
		if entry.Timestamp.After(oneHourAgo) {
			spentLastHour += entry.AmountUSD
		}
	}
	spentLastHour += tx.AmountUSDC

	if spentLastHour <= 0 {
		return 0.0
	}

	// Hours until exhaustion at current rate
	hoursRemaining := remaining / spentLastHour
	if hoursRemaining >= 1.0 {
		return 0.0
	}

	// Proportional: 0h remaining = 1.0, 1h remaining = 0.0
	return math.Round((1.0-hoursRemaining)*1000) / 1000
}
