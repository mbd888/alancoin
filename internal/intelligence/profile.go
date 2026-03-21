// Package intelligence provides a unified Agent Intelligence Profile that
// consolidates TraceRank, Forensics, Reputation, and operational data into
// a single queryable profile per agent.
//
// Every agent gets a composite credit score, risk score, network position,
// and trend indicators — the "FICO score for AI agents." This is the
// foundation for compliance (EU AI Act), insurance pricing, marketplace
// trust, and monetizable intelligence APIs.
package intelligence

import "time"

// Tier classifies agents by composite score.
type Tier string

const (
	TierUnknown  Tier = "unknown"  // No data yet
	TierBronze   Tier = "bronze"   // 0-25
	TierSilver   Tier = "silver"   // 25-50
	TierGold     Tier = "gold"     // 50-75
	TierPlatinum Tier = "platinum" // 75-90
	TierDiamond  Tier = "diamond"  // 90-100
)

// TierFromScore maps a composite score to a tier.
func TierFromScore(score float64) Tier {
	switch {
	case score >= 90:
		return TierDiamond
	case score >= 75:
		return TierPlatinum
	case score >= 50:
		return TierGold
	case score >= 25:
		return TierSilver
	case score > 0:
		return TierBronze
	default:
		return TierUnknown
	}
}

// AgentProfile is the unified intelligence profile for a single agent.
type AgentProfile struct {
	Address        string    `json:"address"`
	CreditScore    float64   `json:"creditScore"`    // 0-100
	RiskScore      float64   `json:"riskScore"`      // 0-100 (higher = riskier)
	CompositeScore float64   `json:"compositeScore"` // 0-100
	Tier           Tier      `json:"tier"`
	ComputeRunID   string    `json:"computeRunId"`
	ComputedAt     time.Time `json:"computedAt"`

	Credit  CreditFactors      `json:"credit"`
	Risk    RiskFactors        `json:"risk"`
	Network NetworkPosition    `json:"network"`
	Ops     OperationalMetrics `json:"operational"`
	Trends  TrendIndicators    `json:"trends"`
}

// CreditFactors are the inputs to the credit score.
type CreditFactors struct {
	TraceRankInput  float64 `json:"traceRankInput"`  // 0-100, from TraceRank GraphScore
	ReputationInput float64 `json:"reputationInput"` // 0-100, from reputation composite
	DisputeRate     float64 `json:"disputeRate"`     // 0-1, fraction of disputed txns
	TxSuccessRate   float64 `json:"txSuccessRate"`   // 0-1, fraction of successful txns
	TotalVolume     float64 `json:"totalVolume"`     // Lifetime USDC volume
}

// RiskFactors are the inputs to the risk score.
type RiskFactors struct {
	AnomalyCount30d      int     `json:"anomalyCount30d"`      // Alerts in last 30 days
	CriticalAlerts       int     `json:"criticalAlerts"`       // Critical-severity alerts
	MeanAmount           float64 `json:"meanAmount"`           // Baseline mean tx amount
	StdDevAmount         float64 `json:"stdDevAmount"`         // Baseline amount stddev
	ForensicScore        float64 `json:"forensicScore"`        // 0-100 (100 = safe)
	BehavioralVolatility float64 `json:"behavioralVolatility"` // stddev/mean ratio
}

// NetworkPosition captures the agent's position in the payment graph.
type NetworkPosition struct {
	InDegree              int     `json:"inDegree"`              // Unique payers
	OutDegree             int     `json:"outDegree"`             // Unique payees
	ClusteringCoefficient float64 `json:"clusteringCoefficient"` // Local clustering
	BridgeScore           float64 `json:"bridgeScore"`           // Betweenness-like centrality
}

// OperationalMetrics capture the agent's operational profile.
type OperationalMetrics struct {
	TotalTxns     int `json:"totalTxns"`
	DaysOnNetwork int `json:"daysOnNetwork"`
}

// TrendIndicators show score movement over time.
type TrendIndicators struct {
	CreditDelta7d  float64 `json:"creditDelta7d"`
	CreditDelta30d float64 `json:"creditDelta30d"`
	RiskDelta7d    float64 `json:"riskDelta7d"`
	RiskDelta30d   float64 `json:"riskDelta30d"`
}

// NetworkBenchmarks holds network-wide aggregate statistics.
type NetworkBenchmarks struct {
	TotalAgents       int       `json:"totalAgents"`
	AvgCreditScore    float64   `json:"avgCreditScore"`
	MedianCreditScore float64   `json:"medianCreditScore"`
	AvgRiskScore      float64   `json:"avgRiskScore"`
	P90CreditScore    float64   `json:"p90CreditScore"`
	P10CreditScore    float64   `json:"p10CreditScore"`
	AvgCompositeScore float64   `json:"avgCompositeScore"`
	ComputeRunID      string    `json:"computeRunId"`
	ComputedAt        time.Time `json:"computedAt"`
}

// ScoreHistoryPoint is a single point in the time-series of an agent's scores.
type ScoreHistoryPoint struct {
	Address        string    `json:"address"`
	CreditScore    float64   `json:"creditScore"`
	RiskScore      float64   `json:"riskScore"`
	CompositeScore float64   `json:"compositeScore"`
	Tier           Tier      `json:"tier"`
	ComputeRunID   string    `json:"computeRunId"`
	CreatedAt      time.Time `json:"createdAt"`
}
