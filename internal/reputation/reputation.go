// Package reputation implements agent reputation scoring for Alancoin.
//
// Reputation is calculated from on-chain behavior:
// - Transaction volume and count
// - Success rate (completed vs failed/disputed)
// - Time on network (age)
// - Unique counterparties (network breadth)
//
// Agents build reputation over time through consistent, verified behavior.
package reputation

import (
	"context"
	"math"
	"time"
)

// Score represents an agent's reputation
type Score struct {
	Address    string     `json:"address"`
	Score      float64    `json:"score"`      // 0-100
	Tier       Tier       `json:"tier"`       // Human-readable tier
	Components Components `json:"components"` // Score breakdown

	// Raw metrics
	Metrics Metrics `json:"metrics"`

	// Metadata
	CalculatedAt time.Time `json:"calculatedAt"`
}

// Tier represents reputation levels
type Tier string

const (
	TierNew         Tier = "new"         // 0-19: Just joined, no history
	TierEmerging    Tier = "emerging"    // 20-39: Some activity
	TierEstablished Tier = "established" // 40-59: Regular participant
	TierTrusted     Tier = "trusted"     // 60-79: Proven track record
	TierElite       Tier = "elite"       // 80-100: Top tier, high volume
)

// Components breaks down the score
type Components struct {
	VolumeScore    float64 `json:"volumeScore"`    // Based on total volume
	ActivityScore  float64 `json:"activityScore"`  // Based on tx count
	SuccessScore   float64 `json:"successScore"`   // Based on success rate
	AgeScore       float64 `json:"ageScore"`       // Based on time on network
	DiversityScore float64 `json:"diversityScore"` // Based on unique counterparties
	TraceRankScore float64 `json:"traceRankScore"` // Graph-based reputation from TraceRank (0-100)
}

// Metrics are the raw inputs to the score
type Metrics struct {
	TotalTransactions    int       `json:"totalTransactions"`
	TotalVolumeUSD       float64   `json:"totalVolumeUsd"`
	SuccessfulTxns       int       `json:"successfulTxns"`
	FailedTxns           int       `json:"failedTxns"`
	UniqueCounterparties int       `json:"uniqueCounterparties"`
	FirstSeen            time.Time `json:"firstSeen"`
	LastActive           time.Time `json:"lastActive"`
	DaysOnNetwork        int       `json:"daysOnNetwork"`
	TraceRankInput       float64   `json:"traceRankInput"` // 0-100, GraphScore from TraceRank
}

// Weights for score components (must sum to 1.0)
type Weights struct {
	TraceRank float64
	Volume    float64
	Activity  float64
	Success   float64
	Age       float64
	Diversity float64
}

// DefaultWeights balances all factors (TraceRank zero by default until data is available)
var DefaultWeights = Weights{
	TraceRank: 0.00, // zero by default (no TraceRank data)
	Volume:    0.25, // Transaction volume matters
	Activity:  0.20, // But so does regular activity
	Success:   0.25, // Success rate is critical
	Age:       0.15, // Time builds trust
	Diversity: 0.15, // Broad network is good
}

// TraceRankWeights rebalances when TraceRank graph data is available.
// TraceRank subsumes volume, age, and diversity signals, so those weights
// are reduced in favor of the graph-based score.
var TraceRankWeights = Weights{
	TraceRank: 0.35, // Graph-based reputation (primary signal)
	Volume:    0.10, // Reduced - TraceRank subsumes volume signal
	Activity:  0.15, // Activity still matters
	Success:   0.25, // Success rate remains critical
	Age:       0.05, // Reduced - TraceRank subsumes age via seed
	Diversity: 0.10, // Reduced - TraceRank subsumes diversity
}

// Calculator computes reputation scores
type Calculator struct {
	weights Weights
}

// NewCalculator creates a reputation calculator
func NewCalculator() *Calculator {
	return &Calculator{weights: DefaultWeights}
}

// NewCalculatorWithWeights creates a calculator with custom weights
func NewCalculatorWithWeights(w Weights) *Calculator {
	return &Calculator{weights: w}
}

// Calculate computes reputation from metrics
func (c *Calculator) Calculate(address string, m Metrics) *Score {
	comp := Components{}

	// Volume score: logarithmic scale, caps at $100k
	// $0 = 0, $100 = 25, $1k = 50, $10k = 75, $100k+ = 100
	if m.TotalVolumeUSD > 0 {
		comp.VolumeScore = math.Min(100, 25*math.Log10(m.TotalVolumeUSD+1))
	}

	// Activity score: logarithmic scale, caps at 1000 txns
	// 0 = 0, 10 = 33, 100 = 66, 1000+ = 100
	if m.TotalTransactions > 0 {
		comp.ActivityScore = math.Min(100, 33.3*math.Log10(float64(m.TotalTransactions)+1))
	}

	// Success score: percentage based, with minimum txn threshold
	// < 5 txns = neutral (50), otherwise based on success rate
	if m.TotalTransactions < 5 {
		comp.SuccessScore = 50 // Neutral until enough data
	} else {
		successRate := float64(m.SuccessfulTxns) / float64(m.TotalTransactions)
		comp.SuccessScore = successRate * 100
	}

	// Age score: logarithmic based on days, caps at 1 year
	// 0 days = 0, 7 days = 28, 30 days = 49, 90 days = 65, 365 days = 85
	if m.DaysOnNetwork > 0 {
		comp.AgeScore = math.Min(100, 33.3*math.Log10(float64(m.DaysOnNetwork)+1))
	}

	// Diversity score: unique counterparties, logarithmic
	// 1 = 0, 5 = 46, 10 = 66, 50 = 100
	if m.UniqueCounterparties > 1 {
		comp.DiversityScore = math.Min(100, 50*math.Log10(float64(m.UniqueCounterparties)))
	}

	// TraceRank score: direct pass-through from graph-based computation (0-100)
	comp.TraceRankScore = m.TraceRankInput

	// Weighted average
	score := c.weights.TraceRank*comp.TraceRankScore +
		c.weights.Volume*comp.VolumeScore +
		c.weights.Activity*comp.ActivityScore +
		c.weights.Success*comp.SuccessScore +
		c.weights.Age*comp.AgeScore +
		c.weights.Diversity*comp.DiversityScore

	// Guard against NaN/Inf from extreme inputs
	if math.IsNaN(score) || math.IsInf(score, 0) {
		score = 0
	}

	// Clamp to 0-100
	score = math.Max(0, math.Min(100, score))

	return &Score{
		Address:      address,
		Score:        math.Round(score*10) / 10, // 1 decimal place
		Tier:         getTier(score),
		Components:   comp,
		Metrics:      m,
		CalculatedAt: time.Now(),
	}
}

func getTier(score float64) Tier {
	switch {
	case score >= 80:
		return TierElite
	case score >= 60:
		return TierTrusted
	case score >= 40:
		return TierEstablished
	case score >= 20:
		return TierEmerging
	default:
		return TierNew
	}
}

// MetricsProvider fetches metrics for reputation calculation
type MetricsProvider interface {
	GetAgentMetrics(ctx context.Context, address string) (*Metrics, error)
	GetAllAgentMetrics(ctx context.Context) (map[string]*Metrics, error)
}
