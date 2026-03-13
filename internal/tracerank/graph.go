package tracerank

import (
	"math"
	"strings"
	"time"
)

// nodeInfo holds aggregated statistics for a node in the payment graph.
type nodeInfo struct {
	inDegree  int
	outDegree int
	inVolume  float64
	outVolume float64
}

// weightedEdge is a normalized incoming edge for PageRank computation.
type weightedEdge struct {
	from   string
	weight float64 // row-normalized weight (0-1), possibly penalized
}

// graph is the internal representation of the payment graph.
type graph struct {
	nodes         []string                      // all unique addresses (deterministic order)
	nodeSet       map[string]bool               // O(1) membership check
	nodeInfo      map[string]*nodeInfo          // per-node statistics
	rawEdges      map[string]map[string]float64 // from -> to -> volume
	incomingEdges map[string][]weightedEdge     // precomputed for PageRank
	edgeCount     int
}

// buildGraph constructs the payment graph from edges, applying config filters.
func buildGraph(edges []PaymentEdge, cfg Config) *graph {
	g := &graph{
		nodeSet:       make(map[string]bool),
		nodeInfo:      make(map[string]*nodeInfo),
		rawEdges:      make(map[string]map[string]float64),
		incomingEdges: make(map[string][]weightedEdge),
	}

	now := time.Now()

	// Track per-pair transaction counts for anti-wash-trading cap
	pairTxCounts := make(map[string]int)

	for _, e := range edges {
		from := strings.ToLower(e.From)
		to := strings.ToLower(e.To)

		// Skip self-loops
		if from == to {
			continue
		}

		// Apply minimum volume filter
		if e.Volume < cfg.MinEdgeVolume {
			continue
		}

		// Apply minimum tx count filter
		if e.TxCount < cfg.MinEdgeTxCount {
			continue
		}

		// Anti-wash-trading: cap per-counterparty pair contribution
		pairKey := from + ":" + to
		if cfg.MaxPerCounterparty > 0 {
			pairTxCounts[pairKey] += e.TxCount
			if pairTxCounts[pairKey] > cfg.MaxPerCounterparty {
				continue
			}
		}

		// Apply temporal decay: recent edges matter more.
		volume := e.Volume
		if cfg.TemporalDecayRate > 0 && !e.LastTxAt.IsZero() {
			daysSince := now.Sub(e.LastTxAt).Hours() / 24
			if daysSince > 0 {
				volume *= math.Exp(-cfg.TemporalDecayRate * daysSince)
			}
		}

		// After decay, re-check minimum volume
		if volume < cfg.MinEdgeVolume {
			continue
		}

		// Register nodes
		if !g.nodeSet[from] {
			g.nodeSet[from] = true
			g.nodes = append(g.nodes, from)
			g.nodeInfo[from] = &nodeInfo{}
		}
		if !g.nodeSet[to] {
			g.nodeSet[to] = true
			g.nodes = append(g.nodes, to)
			g.nodeInfo[to] = &nodeInfo{}
		}

		// Accumulate edge volume
		if g.rawEdges[from] == nil {
			g.rawEdges[from] = make(map[string]float64)
		}

		if g.rawEdges[from][to] == 0 {
			g.nodeInfo[from].outDegree++
			g.nodeInfo[to].inDegree++
			g.edgeCount++
		}

		g.rawEdges[from][to] += volume
		g.nodeInfo[from].outVolume += volume
		g.nodeInfo[to].inVolume += volume
	}

	// Sort nodes for deterministic iteration
	sortStrings(g.nodes)

	// Apply max source influence cap at the raw volume level (before normalization).
	// This caps how much raw volume any single payer can contribute to a node's
	// incoming total, preventing reputation concentration attacks.
	if cfg.MaxSourceInfluence > 0 {
		g.applySourceInfluenceCap(cfg.MaxSourceInfluence)
	}

	// Detect cycle edges for penalty during PageRank
	cycleEdges := g.detectCycleEdges(cfg.CyclePenalty)

	// Precompute normalized incoming edges for PageRank,
	// applying cycle penalty to normalized weights.
	g.precomputeIncoming(cycleEdges, cfg.CyclePenalty)

	return g
}

// detectCycleEdges finds edges that participate in short cycles (length 2 or 3).
// Returns a set of "from:to" keys for edges that should be penalized.
func (g *graph) detectCycleEdges(penalty float64) map[string]bool {
	if penalty <= 0 {
		return nil
	}

	cycleEdges := make(map[string]bool)

	// Detect 2-cycles: A->B and B->A
	for from, targets := range g.rawEdges {
		for to := range targets {
			if _, ok := g.rawEdges[to][from]; ok {
				cycleEdges[from+":"+to] = true
				cycleEdges[to+":"+from] = true
			}
		}
	}

	// Detect 3-cycles: A->B->C->A
	for a, aTargets := range g.rawEdges {
		for b := range aTargets {
			bTargets := g.rawEdges[b]
			for c := range bTargets {
				if c == a || c == b {
					continue
				}
				if _, ok := g.rawEdges[c][a]; ok {
					cycleEdges[a+":"+b] = true
					cycleEdges[b+":"+c] = true
					cycleEdges[c+":"+a] = true
				}
			}
		}
	}

	return cycleEdges
}

// normalizedAdj returns the row-normalized adjacency for outgoing edges.
// Used to identify dangling nodes (nodes with no outgoing edges).
func (g *graph) normalizedAdj() map[string][]weightedEdge {
	adj := make(map[string][]weightedEdge, len(g.nodes))
	for from, targets := range g.rawEdges {
		totalOut := 0.0
		for _, vol := range targets {
			totalOut += vol
		}
		if totalOut == 0 {
			continue
		}
		for to, vol := range targets {
			adj[from] = append(adj[from], weightedEdge{
				from:   to,
				weight: vol / totalOut,
			})
		}
	}
	return adj
}

// applySourceInfluenceCap caps how much raw volume any single source can
// contribute to a node's total incoming volume. This operates on raw edge
// weights BEFORE normalization, which changes the normalized distribution
// across a source's outgoing edges.
func (g *graph) applySourceInfluenceCap(maxFraction float64) {
	for _, to := range g.nodes {
		// Collect all incoming raw volumes for this target
		type sourceEdge struct {
			from string
			vol  float64
		}
		var sources []sourceEdge
		totalIncoming := 0.0
		for from, targets := range g.rawEdges {
			if vol, ok := targets[to]; ok && vol > 0 {
				sources = append(sources, sourceEdge{from, vol})
				totalIncoming += vol
			}
		}
		if totalIncoming == 0 || len(sources) <= 1 {
			continue
		}

		maxAllowed := totalIncoming * maxFraction
		for _, src := range sources {
			if src.vol > maxAllowed {
				diff := src.vol - maxAllowed
				g.rawEdges[src.from][to] = maxAllowed
				g.nodeInfo[to].inVolume -= diff
				g.nodeInfo[src.from].outVolume -= diff
			}
		}
	}
}

// precomputeIncoming builds the incoming edge list with row-normalized weights.
// Cycle edges have their normalized weight reduced by the penalty factor.
func (g *graph) precomputeIncoming(cycleEdges map[string]bool, cyclePenalty float64) {
	// First compute total outgoing volume per node (for normalization)
	outTotal := make(map[string]float64, len(g.nodes))
	for from, targets := range g.rawEdges {
		for _, vol := range targets {
			outTotal[from] += vol
		}
	}

	// Build incoming edges with normalized weights, applying cycle penalty
	cycleMultiplier := 1.0 - cyclePenalty
	for from, targets := range g.rawEdges {
		total := outTotal[from]
		if total == 0 {
			continue
		}
		for to, vol := range targets {
			w := vol / total

			// Apply cycle penalty: reduce flow through cycle edges
			if cycleEdges[from+":"+to] {
				w *= cycleMultiplier
			}

			g.incomingEdges[to] = append(g.incomingEdges[to], weightedEdge{
				from:   from,
				weight: w,
			})
		}
	}
}

// sortStrings sorts a string slice in place (avoids importing sort for this).
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
