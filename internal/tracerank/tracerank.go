// Package tracerank implements a PageRank-inspired reputation algorithm
// that uses payment flows as implicit endorsements.
//
// Core insight: when agent A pays agent B for a service, that payment is
// an implicit endorsement of B's quality. The more payments B receives
// from highly-reputable agents, the higher B's TraceRank score.
//
// Sybil resistance: agents without external seed signals (verification,
// age, ERC-8004) start with zero trust. Self-dealing between zero-seed
// agents propagates zero trust regardless of volume. Only payments from
// seeded nodes contribute meaningful reputation.
//
// This creates a data flywheel: every transaction through Alancoin
// strengthens the reputation graph. Competitors cannot replicate without
// matching transaction volume.
package tracerank

import (
	"context"
	"math"
	"sort"
	"time"
)

// DefaultDamping controls how much direct payment matters vs transitive trust.
// Standard PageRank uses 0.85. Higher values give more weight to the graph
// structure; lower values anchor more to seed signals.
const DefaultDamping = 0.85

// DefaultMaxIterations caps the convergence loop.
// Must be high enough for the L1 norm to converge at 1e-6 threshold.
// With damping 0.85, convergence needs ~85 iterations (0.85^k < 1e-6).
const DefaultMaxIterations = 100

// DefaultConvergenceThreshold stops iteration when the L1 norm of the
// change vector drops below this value.
const DefaultConvergenceThreshold = 1e-6

// AgentScore holds a computed TraceRank score for a single agent.
type AgentScore struct {
	Address      string    `json:"address"`
	GraphScore   float64   `json:"graphScore"` // 0-100, the TraceRank score
	RawRank      float64   `json:"rawRank"`    // raw PageRank value before normalization
	SeedSignal   float64   `json:"seedSignal"` // 0-1, the external trust seed
	InDegree     int       `json:"inDegree"`   // number of unique payers
	OutDegree    int       `json:"outDegree"`  // number of unique payees
	InVolume     float64   `json:"inVolume"`   // total USDC received
	OutVolume    float64   `json:"outVolume"`  // total USDC sent
	Iterations   int       `json:"iterations"` // convergence iteration count
	ComputedAt   time.Time `json:"computedAt"`
	ComputeRunID string    `json:"computeRunId"` // links to the batch computation
}

// SeedProvider supplies external trust signals for agents.
// Implementations can pull from ERC-8004, admin verification,
// time-on-network, or any external trust source.
type SeedProvider interface {
	// GetSeed returns a trust signal in [0, 1] for the given agent.
	// Returns 0 for unknown or untrusted agents.
	GetSeed(ctx context.Context, address string) (float64, error)

	// GetAllSeeds returns trust signals for all known agents.
	GetAllSeeds(ctx context.Context) (map[string]float64, error)
}

// TransactionSource provides payment graph data for TraceRank computation.
type TransactionSource interface {
	// GetPaymentEdges returns all confirmed payment edges (from, to, volume)
	// within the given time window. If since is zero, returns all edges.
	GetPaymentEdges(ctx context.Context, since time.Time) ([]PaymentEdge, error)
}

// PaymentEdge represents a directional payment between two agents.
type PaymentEdge struct {
	From     string  // payer address
	To       string  // payee address
	Volume   float64 // total USDC volume for this edge
	TxCount  int     // number of transactions
	LastTxAt time.Time
}

// ScoreProvider returns TraceRank scores.
type ScoreProvider interface {
	// GetScore returns the latest TraceRank score for an agent.
	// Returns nil, nil if no score exists.
	GetScore(ctx context.Context, address string) (*AgentScore, error)

	// GetScores returns scores for multiple agents.
	GetScores(ctx context.Context, addresses []string) (map[string]*AgentScore, error)

	// GetTopScores returns the top N agents by TraceRank score.
	GetTopScores(ctx context.Context, limit int) ([]*AgentScore, error)
}

// Config holds parameters for the TraceRank computation engine.
type Config struct {
	Damping              float64       // PageRank damping factor (default 0.85)
	MaxIterations        int           // max convergence iterations (default 50)
	ConvergenceThreshold float64       // L1 norm convergence threshold (default 1e-6)
	EdgeWindow           time.Duration // only consider edges within this window (0 = all time)
	MinEdgeVolume        float64       // minimum USDC volume for an edge to count
	MinEdgeTxCount       int           // minimum tx count for an edge to count
	MaxPerCounterparty   int           // cap transactions per counterparty pair (anti-wash)
}

// DefaultConfig returns production-safe defaults.
func DefaultConfig() Config {
	return Config{
		Damping:              DefaultDamping,
		MaxIterations:        DefaultMaxIterations,
		ConvergenceThreshold: DefaultConvergenceThreshold,
		EdgeWindow:           0, // all time
		MinEdgeVolume:        0.001,
		MinEdgeTxCount:       1,
		MaxPerCounterparty:   50, // cap per-pair contribution
	}
}

// Engine computes TraceRank scores from the payment graph.
type Engine struct {
	cfg    Config
	seeds  SeedProvider
	source TransactionSource
}

// NewEngine creates a TraceRank computation engine.
func NewEngine(source TransactionSource, seeds SeedProvider, cfg Config) *Engine {
	if cfg.Damping <= 0 || cfg.Damping >= 1 {
		cfg.Damping = DefaultDamping
	}
	if cfg.MaxIterations <= 0 {
		cfg.MaxIterations = DefaultMaxIterations
	}
	if cfg.ConvergenceThreshold <= 0 {
		cfg.ConvergenceThreshold = DefaultConvergenceThreshold
	}
	return &Engine{
		cfg:    cfg,
		seeds:  seeds,
		source: source,
	}
}

// ComputeResult holds the output of a full TraceRank computation.
type ComputeResult struct {
	Scores     map[string]*AgentScore
	RunID      string
	Iterations int  // actual iterations to converge
	Converged  bool // true if converged within MaxIterations
	Duration   time.Duration
	NodeCount  int
	EdgeCount  int
	ComputedAt time.Time
}

// Compute runs the full TraceRank algorithm and returns scores for all agents.
func (e *Engine) Compute(ctx context.Context, runID string) (*ComputeResult, error) {
	start := time.Now()

	// 1. Build the payment graph
	var since time.Time
	if e.cfg.EdgeWindow > 0 {
		since = time.Now().Add(-e.cfg.EdgeWindow)
	}

	edges, err := e.source.GetPaymentEdges(ctx, since)
	if err != nil {
		return nil, err
	}

	graph := buildGraph(edges, e.cfg)

	// 2. Get seed signals
	seeds, err := e.seeds.GetAllSeeds(ctx)
	if err != nil {
		return nil, err
	}

	// 3. Run PageRank with seed-weighted personalization
	ranks, iterations, converged := e.pagerank(graph, seeds)

	// 4. Normalize to [0, 100] and build result
	scores := e.buildScores(graph, ranks, seeds, iterations, runID)

	return &ComputeResult{
		Scores:     scores,
		RunID:      runID,
		Iterations: iterations,
		Converged:  converged,
		Duration:   time.Since(start),
		NodeCount:  len(graph.nodes),
		EdgeCount:  graph.edgeCount,
		ComputedAt: start,
	}, nil
}

// pagerank runs the personalized PageRank algorithm.
//
// The key Sybil-resistance property: the personalization vector is the seed
// signal. Agents with seed=0 get zero base trust. Self-dealing between
// zero-seed agents propagates 0 * weight = 0 trust, regardless of volume.
func (e *Engine) pagerank(g *graph, seeds map[string]float64) (map[string]float64, int, bool) {
	n := len(g.nodes)
	if n == 0 {
		return nil, 0, true
	}

	d := e.cfg.Damping

	// Build the personalization vector from seeds.
	// Normalize so it sums to 1 (required for PageRank convergence).
	personalization := make(map[string]float64, n)
	seedSum := 0.0
	for _, addr := range g.nodes {
		s := seeds[addr]
		if s < 0 {
			s = 0
		}
		if s > 1 {
			s = 1
		}
		personalization[addr] = s
		seedSum += s
	}

	// If no seeds at all, give uniform distribution (fallback).
	if seedSum == 0 {
		uniform := 1.0 / float64(n)
		for _, addr := range g.nodes {
			personalization[addr] = uniform
		}
		seedSum = 1.0
	} else {
		// Normalize
		for addr := range personalization {
			personalization[addr] /= seedSum
		}
	}

	// Initialize rank vector: start from personalization.
	rank := make(map[string]float64, n)
	for addr, p := range personalization {
		rank[addr] = p
	}

	// Build row-normalized adjacency (outgoing edge weights).
	// adj[from] = [(to, weight), ...] where weights sum to 1.
	adj := g.normalizedAdj()

	// Iterative computation
	var iterations int
	converged := false

	for iter := 0; iter < e.cfg.MaxIterations; iter++ {
		iterations = iter + 1
		newRank := make(map[string]float64, n)

		// Compute dangling node mass (nodes with no outgoing edges).
		// Their rank mass is redistributed via the personalization vector.
		danglingMass := 0.0
		for _, addr := range g.nodes {
			if len(adj[addr]) == 0 {
				danglingMass += rank[addr]
			}
		}

		// PageRank update: r_new[i] = (1-d) * p[i] + d * (Σ_j r[j] * w[j→i] + dangling * p[i])
		for _, addr := range g.nodes {
			incoming := g.incomingEdges[addr]
			sum := 0.0
			for _, edge := range incoming {
				// Weight of edge from source to addr, normalized by source's total outgoing
				sum += rank[edge.from] * edge.weight
			}
			newRank[addr] = (1-d)*personalization[addr] + d*(sum+danglingMass*personalization[addr])
		}

		// Check convergence (L1 norm of difference)
		diff := 0.0
		for _, addr := range g.nodes {
			diff += math.Abs(newRank[addr] - rank[addr])
		}

		rank = newRank

		if diff < e.cfg.ConvergenceThreshold {
			converged = true
			break
		}
	}

	return rank, iterations, converged
}

// buildScores converts raw PageRank values to normalized AgentScore structs.
func (e *Engine) buildScores(g *graph, ranks, seeds map[string]float64, iterations int, runID string) map[string]*AgentScore {
	now := time.Now()
	scores := make(map[string]*AgentScore, len(g.nodes))

	// Find max rank for normalization
	maxRank := 0.0
	for _, r := range ranks {
		if r > maxRank {
			maxRank = r
		}
	}

	if maxRank == 0 {
		maxRank = 1 // prevent division by zero
	}

	for _, addr := range g.nodes {
		nodeInfo := g.nodeInfo[addr]
		graphScore := (ranks[addr] / maxRank) * 100
		if graphScore > 100 {
			graphScore = 100
		}
		// Round to 1 decimal place
		graphScore = math.Round(graphScore*10) / 10

		scores[addr] = &AgentScore{
			Address:      addr,
			GraphScore:   graphScore,
			RawRank:      ranks[addr],
			SeedSignal:   seeds[addr],
			InDegree:     nodeInfo.inDegree,
			OutDegree:    nodeInfo.outDegree,
			InVolume:     nodeInfo.inVolume,
			OutVolume:    nodeInfo.outVolume,
			Iterations:   iterations,
			ComputedAt:   now,
			ComputeRunID: runID,
		}
	}

	return scores
}

// Leaderboard returns agents sorted by TraceRank score descending.
func Leaderboard(scores map[string]*AgentScore, limit int) []*AgentScore {
	list := make([]*AgentScore, 0, len(scores))
	for _, s := range scores {
		list = append(list, s)
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].GraphScore > list[j].GraphScore
	})
	if limit > 0 && len(list) > limit {
		list = list[:limit]
	}
	return list
}
