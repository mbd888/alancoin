// Package flywheel computes network-health metrics over the registry: tx
// velocity and growth, graph density, retention, and a per-tier incentive
// schedule (fee discounts + discovery boosts).
package flywheel

import (
	"context"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mbd888/alancoin/internal/registry"
)

// State is a point-in-time snapshot of network health metrics.
type State struct {
	// Velocity
	TransactionsPerHour float64 `json:"transactionsPerHour"`
	VolumePerHourUSD    float64 `json:"volumePerHourUsd"`

	// Growth (current 7d window vs the prior 7d)
	TxVelocity7dPrior   float64 `json:"txVelocity7dPrior"`
	TxGrowthRatePct     float64 `json:"txGrowthRatePct"`
	VolumeGrowthRatePct float64 `json:"volumeGrowthRatePct"`
	NewAgents7d         int     `json:"newAgents7d"`
	NewAgents7dPrior    int     `json:"newAgents7dPrior"`
	AgentGrowthRatePct  float64 `json:"agentGrowthRatePct"`

	// Graph density
	TotalAgents    int     `json:"totalAgents"`
	ActiveAgents7d int     `json:"activeAgents7d"`
	TotalEdges     int     `json:"totalEdges"`   // unique (from, to) pairs
	GraphDensity   float64 `json:"graphDensity"` // edges / (n*(n-1)/2)
	AvgDegree      float64 `json:"avgDegree"`
	Reciprocity    float64 `json:"reciprocity"` // fraction of edges that are bidirectional

	// Reputation effectiveness
	TierDistribution      map[string]int `json:"tierDistribution"`
	TopTierTrafficShare   float64        `json:"topTierTrafficShare"`
	ReputationCorrelation float64        `json:"reputationCorrelation"` // rank correlation: rep score vs tx count

	// Retention
	RetentionRate7d float64 `json:"retentionRate7d"` // of agents active 8-14d ago, fraction still active in last 7d
	ChurnRate7d     float64 `json:"churnRate7d"`

	// Composite health score (0-100) and its sub-scores
	HealthScore        float64 `json:"healthScore"`
	HealthTier         string  `json:"healthTier"`
	VelocityScore      float64 `json:"velocityScore"`
	GrowthScore        float64 `json:"growthScore"`
	DensityScore       float64 `json:"densityScore"`
	EffectivenessScore float64 `json:"effectivenessScore"`
	RetentionScore     float64 `json:"retentionScore"`

	ComputedAt time.Time `json:"computedAt"`
}

// HealthTier labels for HealthScore bands of 20.
const (
	TierCold         = "cold"         // 0-20
	TierWarming      = "warming"      // 20-40
	TierSpinning     = "spinning"     // 40-60
	TierAccelerating = "accelerating" // 60-80
	TierFlywheel     = "flywheel"     // 80-100
)

func healthTier(score float64) string {
	switch {
	case score >= 80:
		return TierFlywheel
	case score >= 60:
		return TierAccelerating
	case score >= 40:
		return TierSpinning
	case score >= 20:
		return TierWarming
	default:
		return TierCold
	}
}

// Health score weights.
const (
	weightVelocity      = 0.25
	weightGrowth        = 0.20
	weightDensity       = 0.20
	weightEffectiveness = 0.20
	weightRetention     = 0.15
)

// Engine computes and caches flywheel state.
type Engine struct {
	registryStore registry.Store

	mu      sync.RWMutex
	latest  *State
	history []*State // rolling window for trends (max historySize)
}

const historySize = 288 // 24 hours at 5-minute intervals

// NewEngine creates a flywheel computation engine.
func NewEngine(registryStore registry.Store) *Engine {
	return &Engine{
		registryStore: registryStore,
		history:       make([]*State, 0, historySize),
	}
}

// Latest returns the most recent flywheel state.
func (e *Engine) Latest() *State {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.latest == nil {
		return nil
	}
	cp := *e.latest
	return &cp
}

// History returns the recent flywheel state history.
func (e *Engine) History() []*State {
	e.mu.RLock()
	defer e.mu.RUnlock()
	cp := make([]*State, len(e.history))
	for i, s := range e.history {
		v := *s
		cp[i] = &v
	}
	return cp
}

// Compute runs a full flywheel state computation.
func (e *Engine) Compute(ctx context.Context) (*State, error) {
	now := time.Now()

	// Gather all agents
	agents, err := e.registryStore.ListAgents(ctx, registry.AgentQuery{Limit: 10000})
	if err != nil {
		return nil, err
	}

	// Gather recent transactions (large window for trend computation)
	recentTxns, err := e.registryStore.GetRecentTransactions(ctx, 50000)
	if err != nil {
		return nil, err
	}

	state := &State{
		TotalAgents:      len(agents),
		TierDistribution: make(map[string]int),
		ComputedAt:       now,
	}

	// --- Velocity & Growth ---
	e.computeVelocity(state, recentTxns, now)
	e.computeGrowth(state, agents, recentTxns, now)

	// --- Network Density ---
	e.computeDensity(state, recentTxns, agents, now)

	// --- Reputation Effectiveness ---
	e.computeEffectiveness(state, agents, recentTxns)

	// --- Retention ---
	e.computeRetention(state, agents, now)

	// --- Health Score ---
	e.computeHealth(state)

	// Cache the result
	e.mu.Lock()
	e.latest = state
	e.history = append(e.history, state)
	if len(e.history) > historySize {
		e.history = e.history[len(e.history)-historySize:]
	}
	e.mu.Unlock()

	return state, nil
}

// computeVelocity calculates transaction throughput.
func (e *Engine) computeVelocity(state *State, txns []*registry.Transaction, now time.Time) {
	oneHourAgo := now.Add(-1 * time.Hour)
	var txnsInHour int
	var volumeInHour float64

	for _, tx := range txns {
		if tx.CreatedAt.After(oneHourAgo) && isConfirmed(tx) {
			txnsInHour++
			volumeInHour += parseFloat(tx.Amount)
		}
	}

	state.TransactionsPerHour = float64(txnsInHour)
	state.VolumePerHourUSD = volumeInHour

	// Velocity sub-score: log-scaled, 100 txns/hr → 100
	if txnsInHour > 0 {
		state.VelocityScore = math.Min(100, 50*math.Log10(float64(txnsInHour)+1))
	}
}

// computeGrowth calculates week-over-week trends.
func (e *Engine) computeGrowth(state *State, agents []*registry.Agent, txns []*registry.Transaction, now time.Time) {
	sevenDaysAgo := now.Add(-7 * 24 * time.Hour)
	fourteenDaysAgo := now.Add(-14 * 24 * time.Hour)

	// Transaction growth: compare last 7d vs prior 7d
	var txns7d, txns7dPrior int
	var vol7d, vol7dPrior float64
	for _, tx := range txns {
		if !isConfirmed(tx) {
			continue
		}
		if tx.CreatedAt.After(sevenDaysAgo) {
			txns7d++
			vol7d += parseFloat(tx.Amount)
		} else if tx.CreatedAt.After(fourteenDaysAgo) {
			txns7dPrior++
			vol7dPrior += parseFloat(tx.Amount)
		}
	}

	// Normalize to per-hour for comparability
	hours7d := 7.0 * 24
	state.TxVelocity7dPrior = float64(txns7dPrior) / hours7d

	if txns7dPrior > 0 {
		state.TxGrowthRatePct = (float64(txns7d) - float64(txns7dPrior)) / float64(txns7dPrior) * 100
	}
	if vol7dPrior > 0 {
		state.VolumeGrowthRatePct = (vol7d - vol7dPrior) / vol7dPrior * 100
	}

	// Agent growth
	var new7d, new7dPrior int
	for _, a := range agents {
		if a.CreatedAt.After(sevenDaysAgo) {
			new7d++
		} else if a.CreatedAt.After(fourteenDaysAgo) {
			new7dPrior++
		}
	}
	state.NewAgents7d = new7d
	state.NewAgents7dPrior = new7dPrior
	if new7dPrior > 0 {
		state.AgentGrowthRatePct = (float64(new7d) - float64(new7dPrior)) / float64(new7dPrior) * 100
	}

	// Growth sub-score: based on positive growth rate, capped at 100% growth → 100
	growthSignal := 0.0
	if state.TxGrowthRatePct > 0 {
		growthSignal += math.Min(50, state.TxGrowthRatePct/2)
	}
	if state.AgentGrowthRatePct > 0 {
		growthSignal += math.Min(50, state.AgentGrowthRatePct/2)
	}
	state.GrowthScore = math.Min(100, growthSignal)
}

// computeDensity measures graph interconnectedness.
func (e *Engine) computeDensity(state *State, txns []*registry.Transaction, agents []*registry.Agent, now time.Time) {
	sevenDaysAgo := now.Add(-7 * 24 * time.Hour)

	// Build edge set from recent transactions
	type edge struct{ from, to string }
	edgeSet := make(map[edge]bool)
	degreeMap := make(map[string]int) // agent → unique connections
	activeSet := make(map[string]bool)

	for _, tx := range txns {
		if !isConfirmed(tx) || tx.CreatedAt.Before(sevenDaysAgo) {
			continue
		}
		from := strings.ToLower(tx.From)
		to := strings.ToLower(tx.To)
		if from == to {
			continue
		}

		e := edge{from, to}
		if !edgeSet[e] {
			edgeSet[e] = true
			degreeMap[from]++
			degreeMap[to]++
		}
		activeSet[from] = true
		activeSet[to] = true
	}

	state.TotalEdges = len(edgeSet)
	state.ActiveAgents7d = len(activeSet)

	n := len(agents)
	if n > 1 {
		maxEdges := float64(n) * float64(n-1) / 2
		state.GraphDensity = float64(state.TotalEdges) / maxEdges
	}

	if len(degreeMap) > 0 {
		totalDegree := 0
		for _, d := range degreeMap {
			totalDegree += d
		}
		state.AvgDegree = float64(totalDegree) / float64(len(degreeMap))
	}

	// Reciprocity: fraction of edges where both directions exist
	bidirectional := 0
	for e := range edgeSet {
		reverse := edge{e.to, e.from}
		if edgeSet[reverse] {
			bidirectional++
		}
	}
	if len(edgeSet) > 0 {
		state.Reciprocity = float64(bidirectional) / float64(len(edgeSet))
	}

	// Density sub-score: combination of graph density and avg degree
	densitySignal := 0.0
	if state.GraphDensity > 0 {
		// Density: 0.01 → 20, 0.05 → 40, 0.1 → 50, 0.5 → 80
		densitySignal += math.Min(50, state.GraphDensity*500)
	}
	if state.AvgDegree > 0 {
		// Avg degree: 1 → 10, 3 → 30, 10 → 50
		densitySignal += math.Min(50, state.AvgDegree*5)
	}
	state.DensityScore = math.Min(100, densitySignal)
}

// computeEffectiveness measures whether reputation actually drives traffic.
func (e *Engine) computeEffectiveness(state *State, agents []*registry.Agent, txns []*registry.Transaction) {
	// Tier distribution: how many agents per reputation tier
	// (approximation from success rate + tx count since we don't import reputation package)
	for _, a := range agents {
		tier := approximateTier(a)
		state.TierDistribution[tier]++
	}

	// Top-tier traffic share: what fraction of recent txns involve high-rep agents?
	topTierAgents := make(map[string]bool)
	for _, a := range agents {
		tier := approximateTier(a)
		if tier == "trusted" || tier == "elite" {
			topTierAgents[strings.ToLower(a.Address)] = true
		}
	}

	var recentCount, topTierCount int
	for _, tx := range txns {
		if !isConfirmed(tx) {
			continue
		}
		recentCount++
		from := strings.ToLower(tx.From)
		to := strings.ToLower(tx.To)
		if topTierAgents[from] || topTierAgents[to] {
			topTierCount++
		}
	}
	if recentCount > 0 {
		state.TopTierTrafficShare = float64(topTierCount) / float64(recentCount)
	}

	// Reputation correlation: Spearman rank correlation between tx count and tier
	state.ReputationCorrelation = e.computeRankCorrelation(agents)

	// Effectiveness sub-score
	effectSignal := 0.0
	// Top-tier traffic share: 50% → 50 points, 80%+ → 80 points
	effectSignal += math.Min(50, state.TopTierTrafficShare*100)
	// Positive correlation → good
	if state.ReputationCorrelation > 0 {
		effectSignal += math.Min(50, state.ReputationCorrelation*50)
	}
	state.EffectivenessScore = math.Min(100, effectSignal)
}

// computeRetention calculates agent stickiness.
func (e *Engine) computeRetention(state *State, agents []*registry.Agent, now time.Time) {
	sevenDaysAgo := now.Add(-7 * 24 * time.Hour)
	fourteenDaysAgo := now.Add(-14 * 24 * time.Hour)

	// Agents active in the prior 7d window (8-14 days ago)
	var priorActive, stillActive int
	for _, a := range agents {
		lastActive := a.Stats.LastActive
		if lastActive.IsZero() {
			if a.Stats.LastTransactionAt != nil {
				lastActive = *a.Stats.LastTransactionAt
			}
		}

		wasActivePrior := lastActive.After(fourteenDaysAgo) && lastActive.Before(sevenDaysAgo)
		isActiveNow := lastActive.After(sevenDaysAgo)

		// Agent was active 8-14d ago: is it still active?
		if wasActivePrior || (a.CreatedAt.After(fourteenDaysAgo) && a.CreatedAt.Before(sevenDaysAgo)) {
			priorActive++
			if isActiveNow {
				stillActive++
			}
		}
	}

	if priorActive > 0 {
		state.RetentionRate7d = float64(stillActive) / float64(priorActive)
		state.ChurnRate7d = 1.0 - state.RetentionRate7d
	}

	// Retention sub-score: 50% retention → 50, 80% → 80, 100% → 100
	state.RetentionScore = math.Min(100, state.RetentionRate7d*100)
}

// computeHealth calculates the composite health score.
func (e *Engine) computeHealth(state *State) {
	state.HealthScore = weightVelocity*state.VelocityScore +
		weightGrowth*state.GrowthScore +
		weightDensity*state.DensityScore +
		weightEffectiveness*state.EffectivenessScore +
		weightRetention*state.RetentionScore

	// Clamp and round
	state.HealthScore = math.Max(0, math.Min(100, state.HealthScore))
	state.HealthScore = math.Round(state.HealthScore*10) / 10

	state.HealthTier = healthTier(state.HealthScore)
}

// computeRankCorrelation computes Spearman rank correlation between
// agent transaction count and their approximate reputation tier.
// A positive correlation means reputation is aligned with activity.
func (e *Engine) computeRankCorrelation(agents []*registry.Agent) float64 {
	if len(agents) < 3 {
		return 0
	}

	type pair struct {
		txRank  float64
		repRank float64
	}

	// Sort by tx count for rank
	sorted := make([]*registry.Agent, len(agents))
	copy(sorted, agents)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Stats.TransactionCount < sorted[j].Stats.TransactionCount
	})

	pairs := make([]pair, len(sorted))
	for i, a := range sorted {
		pairs[i].txRank = float64(i + 1)
		pairs[i].repRank = tierToRank(approximateTier(a))
	}

	// Spearman correlation: 1 - (6 * Σd² / (n³ - n))
	n := float64(len(pairs))
	sumD2 := 0.0
	for _, p := range pairs {
		d := p.txRank - p.repRank
		sumD2 += d * d
	}

	rho := 1 - (6*sumD2)/(n*n*n-n)
	if math.IsNaN(rho) || math.IsInf(rho, 0) {
		return 0
	}
	return rho
}

// approximateTier estimates an agent's reputation tier from its stats.
// This avoids importing the reputation package while providing a useful signal.
func approximateTier(a *registry.Agent) string {
	txCount := a.Stats.TransactionCount
	rate := a.Stats.SuccessRate

	switch {
	case txCount >= 100 && rate >= 0.95:
		return "elite"
	case txCount >= 50 && rate >= 0.90:
		return "trusted"
	case txCount >= 20 && rate >= 0.80:
		return "established"
	case txCount >= 5:
		return "emerging"
	default:
		return "new"
	}
}

func tierToRank(tier string) float64 {
	switch tier {
	case "elite":
		return 5
	case "trusted":
		return 4
	case "established":
		return 3
	case "emerging":
		return 2
	default:
		return 1
	}
}

func isConfirmed(tx *registry.Transaction) bool {
	return tx.Status == "confirmed" || tx.Status == "completed"
}

func parseFloat(s string) float64 {
	if s == "" {
		return 0
	}
	var f float64
	var decimal bool
	var pow float64 = 10
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '.' {
			decimal = true
			continue
		}
		if c < '0' || c > '9' {
			continue
		}
		if decimal {
			f += float64(c-'0') / pow
			pow *= 10
		} else {
			f = f*10 + float64(c-'0')
		}
	}
	return f
}
