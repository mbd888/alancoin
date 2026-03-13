package tracerank

import "strings"

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
	weight float64 // row-normalized weight (0-1)
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
				// Already at cap, skip additional edges from this pair
				continue
			}
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
			// First time seeing this edge
			g.nodeInfo[from].outDegree++
			g.nodeInfo[to].inDegree++
			g.edgeCount++
		}

		g.rawEdges[from][to] += e.Volume
		g.nodeInfo[from].outVolume += e.Volume
		g.nodeInfo[to].inVolume += e.Volume
	}

	// Sort nodes for deterministic iteration
	sortStrings(g.nodes)

	// Precompute normalized incoming edges for PageRank
	g.precomputeIncoming()

	return g
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

// precomputeIncoming builds the incoming edge list with row-normalized weights.
// This is the critical data structure for PageRank iteration:
// for each node, we need to know which nodes point to it and with what weight.
func (g *graph) precomputeIncoming() {
	// First compute total outgoing volume per node (for normalization)
	outTotal := make(map[string]float64, len(g.nodes))
	for from, targets := range g.rawEdges {
		for _, vol := range targets {
			outTotal[from] += vol
		}
	}

	// Build incoming edges with normalized weights
	for from, targets := range g.rawEdges {
		total := outTotal[from]
		if total == 0 {
			continue
		}
		for to, vol := range targets {
			g.incomingEdges[to] = append(g.incomingEdges[to], weightedEdge{
				from:   from,
				weight: vol / total,
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
