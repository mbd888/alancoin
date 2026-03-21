package intelligence

import (
	"context"
	"log/slog"
	"math"
	"sort"
	"time"
)

// Credit score weights (must sum to 1.0).
const (
	weightTraceRank  = 0.35
	weightReputation = 0.25
	weightSuccess    = 0.20
	weightVolume     = 0.10
	weightAge        = 0.10
)

// Risk score weights (must sum to 1.0).
const (
	weightAnomalyFreq      = 0.30
	weightDisputeRate      = 0.25
	weightVolatility       = 0.20
	weightCriticalAlerts   = 0.15
	weightNetworkIsolation = 0.10
)

// Composite score blending.
const (
	compositeCredit = 0.60
	compositeRisk   = 0.40
)

// TraceRankProvider reads TraceRank scores without importing the tracerank package.
type TraceRankProvider interface {
	GetScore(ctx context.Context, address string) (graphScore float64, inDegree, outDegree int, inVolume, outVolume float64, err error)
	GetAllScores(ctx context.Context) (map[string]TraceRankData, error)
}

// TraceRankData is a flat copy of the data we need from tracerank.AgentScore.
type TraceRankData struct {
	GraphScore float64
	InDegree   int
	OutDegree  int
	InVolume   float64
	OutVolume  float64
}

// ForensicsProvider reads forensics baselines and alerts.
type ForensicsProvider interface {
	GetBaseline(ctx context.Context, agentAddr string) (txCount int, meanAmount, stdDevAmount float64, err error)
	CountAlerts30d(ctx context.Context, agentAddr string) (total, critical int, err error)
}

// ReputationData is the data we need from the reputation system.
type ReputationData struct {
	Score                float64
	TotalTransactions    int
	SuccessfulTxns       int
	FailedTxns           int
	TotalVolumeUSD       float64
	UniqueCounterparties int
	DaysOnNetwork        int
}

// ReputationProvider reads reputation metrics.
type ReputationProvider interface {
	GetMetrics(ctx context.Context, address string) (*ReputationData, error)
	GetAllMetrics(ctx context.Context) (map[string]*ReputationData, error)
}

// AgentSource lists all known agent addresses.
type AgentSource interface {
	ListAllAddresses(ctx context.Context) ([]string, error)
}

// Engine computes intelligence profiles by pulling from existing subsystems.
type Engine struct {
	traceRank  TraceRankProvider
	forensics  ForensicsProvider
	reputation ReputationProvider
	agents     AgentSource
	store      Store
	logger     *slog.Logger
}

// NewEngine creates an intelligence computation engine.
func NewEngine(
	tr TraceRankProvider,
	fr ForensicsProvider,
	rep ReputationProvider,
	agents AgentSource,
	store Store,
	logger *slog.Logger,
) *Engine {
	return &Engine{
		traceRank:  tr,
		forensics:  fr,
		reputation: rep,
		agents:     agents,
		store:      store,
		logger:     logger,
	}
}

// ComputeResult holds the output of a full computation run.
type ComputeResult struct {
	Profiles   []*AgentProfile
	Benchmarks *NetworkBenchmarks
	RunID      string
	Duration   time.Duration
}

// ComputeAll recomputes intelligence profiles for all agents.
func (e *Engine) ComputeAll(ctx context.Context, runID string) (*ComputeResult, error) {
	start := time.Now()

	addresses, err := e.agents.ListAllAddresses(ctx)
	if err != nil {
		return nil, err
	}

	if len(addresses) == 0 {
		return &ComputeResult{RunID: runID, Duration: time.Since(start)}, nil
	}

	// Batch-load all TraceRank scores
	trScores, err := e.traceRank.GetAllScores(ctx)
	if err != nil {
		e.logger.Warn("intelligence: failed to load tracerank scores, continuing without", "error", err)
		trScores = map[string]TraceRankData{}
	}

	// Batch-load all reputation metrics
	repMetrics, err := e.reputation.GetAllMetrics(ctx)
	if err != nil {
		e.logger.Warn("intelligence: failed to load reputation metrics, continuing without", "error", err)
		repMetrics = map[string]*ReputationData{}
	}

	now := time.Now().UTC()
	profiles := make([]*AgentProfile, 0, len(addresses))

	for _, addr := range addresses {
		tr := trScores[addr]
		rep := repMetrics[addr]
		if rep == nil {
			rep = &ReputationData{}
		}

		// Forensics data
		fTxCount, fMean, fStdDev, fErr := e.forensics.GetBaseline(ctx, addr)
		if fErr != nil {
			fTxCount, fMean, fStdDev = 0, 0, 0
		}

		alertTotal, alertCritical, aErr := e.forensics.CountAlerts30d(ctx, addr)
		if aErr != nil {
			alertTotal, alertCritical = 0, 0
		}

		profile := e.computeProfile(addr, tr, rep, fTxCount, fMean, fStdDev, alertTotal, alertCritical, runID, now)

		// Compute trend deltas from historical data
		e.computeTrends(ctx, profile)

		profiles = append(profiles, profile)
	}

	benchmarks := e.computeBenchmarks(profiles, runID, now)

	return &ComputeResult{
		Profiles:   profiles,
		Benchmarks: benchmarks,
		RunID:      runID,
		Duration:   time.Since(start),
	}, nil
}

// ComputeOne recomputes the intelligence profile for a single agent.
func (e *Engine) ComputeOne(ctx context.Context, address string, runID string) (*AgentProfile, error) {
	graphScore, inDeg, outDeg, _, _, trErr := e.traceRank.GetScore(ctx, address)
	tr := TraceRankData{GraphScore: graphScore, InDegree: inDeg, OutDegree: outDeg}
	if trErr != nil {
		tr = TraceRankData{}
	}

	rep, repErr := e.reputation.GetMetrics(ctx, address)
	if repErr != nil || rep == nil {
		rep = &ReputationData{}
	}

	fTxCount, fMean, fStdDev, fErr := e.forensics.GetBaseline(ctx, address)
	if fErr != nil {
		fTxCount, fMean, fStdDev = 0, 0, 0
	}

	alertTotal, alertCritical, aErr := e.forensics.CountAlerts30d(ctx, address)
	if aErr != nil {
		alertTotal, alertCritical = 0, 0
	}

	now := time.Now().UTC()
	profile := e.computeProfile(address, tr, rep, fTxCount, fMean, fStdDev, alertTotal, alertCritical, runID, now)
	e.computeTrends(ctx, profile)

	return profile, nil
}

func (e *Engine) computeProfile(
	address string,
	tr TraceRankData,
	rep *ReputationData,
	forensicsTxCount int,
	forensicsMeanAmount, forensicsStdDevAmount float64,
	alertTotal, alertCritical int,
	runID string,
	now time.Time,
) *AgentProfile {
	// --- Credit Score ---
	// 35% TraceRank GraphScore (already 0-100)
	trComponent := tr.GraphScore

	// 25% Reputation composite score (already 0-100)
	repComponent := rep.Score

	// 20% Transaction success rate
	var successRate float64
	totalTx := rep.TotalTransactions
	if totalTx > 0 {
		successRate = float64(rep.SuccessfulTxns) / float64(totalTx)
	}
	successComponent := successRate * 100

	// 10% Volume maturity: log-scaled, $10K = 100
	volumeComponent := 0.0
	if rep.TotalVolumeUSD > 0 {
		volumeComponent = clamp(math.Log10(rep.TotalVolumeUSD+1)/4.0*100, 0, 100) // log10(10001)~4 -> 100
	}

	// 10% Network age: log-scaled, 365 days = 100
	ageComponent := 0.0
	if rep.DaysOnNetwork > 0 {
		ageComponent = clamp(math.Log10(float64(rep.DaysOnNetwork)+1)/math.Log10(366)*100, 0, 100)
	}

	creditScore := clamp(
		weightTraceRank*trComponent+
			weightReputation*repComponent+
			weightSuccess*successComponent+
			weightVolume*volumeComponent+
			weightAge*ageComponent,
		0, 100)

	// --- Risk Score ---
	// 30% Anomaly frequency: 10+ alerts in 30d = max risk
	anomalyComponent := clamp(float64(alertTotal)/10.0*100, 0, 100)

	// 25% Dispute rate
	var disputeRate float64
	if totalTx > 0 {
		disputeRate = float64(rep.FailedTxns) / float64(totalTx)
	}
	disputeComponent := clamp(disputeRate*500, 0, 100) // 20% dispute rate = max risk

	// 20% Behavioral volatility: coefficient of variation (stddev/mean)
	volatilityComponent := 0.0
	var behavioralVolatility float64
	if forensicsMeanAmount > 0 {
		behavioralVolatility = forensicsStdDevAmount / forensicsMeanAmount
		volatilityComponent = clamp(behavioralVolatility/2.0*100, 0, 100) // CV of 2.0 = max risk
	}

	// 15% Critical alert count: 3+ = max risk
	criticalComponent := clamp(float64(alertCritical)/3.0*100, 0, 100)

	// 10% Network isolation: agents with no incoming connections are isolated
	isolationComponent := 100.0 // Default: fully isolated
	if tr.InDegree > 0 {
		isolationComponent = clamp(100.0-math.Log10(float64(tr.InDegree)+1)*50, 0, 100) // 100 payers -> ~0 isolation
	}

	riskScore := clamp(
		weightAnomalyFreq*anomalyComponent+
			weightDisputeRate*disputeComponent+
			weightVolatility*volatilityComponent+
			weightCriticalAlerts*criticalComponent+
			weightNetworkIsolation*isolationComponent,
		0, 100)

	// Forensic score: inverted risk (100 = safe, for display purposes)
	forensicScore := clamp(100-riskScore, 0, 100)

	// --- Composite Score ---
	compositeScore := clamp(
		compositeCredit*creditScore+compositeRisk*(100-riskScore),
		0, 100)

	tier := TierFromScore(compositeScore)

	return &AgentProfile{
		Address:        address,
		CreditScore:    round1(creditScore),
		RiskScore:      round1(riskScore),
		CompositeScore: round1(compositeScore),
		Tier:           tier,
		ComputeRunID:   runID,
		ComputedAt:     now,
		Credit: CreditFactors{
			TraceRankInput:  round1(tr.GraphScore),
			ReputationInput: round1(rep.Score),
			DisputeRate:     roundN(disputeRate, 4),
			TxSuccessRate:   roundN(successRate, 4),
			TotalVolume:     rep.TotalVolumeUSD,
		},
		Risk: RiskFactors{
			AnomalyCount30d:      alertTotal,
			CriticalAlerts:       alertCritical,
			MeanAmount:           forensicsMeanAmount,
			StdDevAmount:         forensicsStdDevAmount,
			ForensicScore:        round1(forensicScore),
			BehavioralVolatility: roundN(behavioralVolatility, 4),
		},
		Network: NetworkPosition{
			InDegree:              tr.InDegree,
			OutDegree:             tr.OutDegree,
			ClusteringCoefficient: 0, // Computed from full graph in batch mode, not per-agent
			BridgeScore:           0, // Computed from full graph in batch mode, not per-agent
		},
		Ops: OperationalMetrics{
			TotalTxns:     totalTx,
			DaysOnNetwork: rep.DaysOnNetwork,
		},
	}
}

func (e *Engine) computeTrends(ctx context.Context, profile *AgentProfile) {
	now := time.Now().UTC()

	// 7-day lookback
	history7d, err := e.store.GetScoreHistory(ctx, profile.Address, now.Add(-7*24*time.Hour), now, 1)
	if err == nil && len(history7d) > 0 {
		profile.Trends.CreditDelta7d = round1(profile.CreditScore - history7d[len(history7d)-1].CreditScore)
		profile.Trends.RiskDelta7d = round1(profile.RiskScore - history7d[len(history7d)-1].RiskScore)
	}

	// 30-day lookback
	history30d, err := e.store.GetScoreHistory(ctx, profile.Address, now.Add(-30*24*time.Hour), now.Add(-29*24*time.Hour), 1)
	if err == nil && len(history30d) > 0 {
		profile.Trends.CreditDelta30d = round1(profile.CreditScore - history30d[0].CreditScore)
		profile.Trends.RiskDelta30d = round1(profile.RiskScore - history30d[0].RiskScore)
	}
}

func (e *Engine) computeBenchmarks(profiles []*AgentProfile, runID string, now time.Time) *NetworkBenchmarks {
	n := len(profiles)
	if n == 0 {
		return &NetworkBenchmarks{ComputeRunID: runID, ComputedAt: now}
	}

	var sumCredit, sumRisk, sumComposite float64
	creditScores := make([]float64, n)

	for i, p := range profiles {
		sumCredit += p.CreditScore
		sumRisk += p.RiskScore
		sumComposite += p.CompositeScore
		creditScores[i] = p.CreditScore
	}

	sort.Float64s(creditScores)

	return &NetworkBenchmarks{
		TotalAgents:       n,
		AvgCreditScore:    round1(sumCredit / float64(n)),
		MedianCreditScore: round1(percentile(creditScores, 0.50)),
		AvgRiskScore:      round1(sumRisk / float64(n)),
		P90CreditScore:    round1(percentile(creditScores, 0.90)),
		P10CreditScore:    round1(percentile(creditScores, 0.10)),
		AvgCompositeScore: round1(sumComposite / float64(n)),
		ComputeRunID:      runID,
		ComputedAt:        now,
	}
}

// percentile returns the p-th percentile from a sorted slice.
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := p * float64(len(sorted)-1)
	lower := int(math.Floor(idx))
	upper := int(math.Ceil(idx))
	if lower == upper || upper >= len(sorted) {
		return sorted[lower]
	}
	frac := idx - float64(lower)
	return sorted[lower]*(1-frac) + sorted[upper]*frac
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func round1(v float64) float64 {
	return math.Round(v*10) / 10
}

func roundN(v float64, decimals int) float64 {
	pow := math.Pow(10, float64(decimals))
	return math.Round(v*pow) / pow
}
