// Package supervisor provides a ledger decorator that evaluates agent
// spending patterns before allowing money-moving operations.
package supervisor

import (
	"hash/fnv"
	"math/big"
	"strings"
	"sync"
	"time"
)

// SpendEvent records a single spending observation.
// Retained for edge event tracking (cycle detection needs discrete events).
type SpendEvent struct {
	Amount *big.Int
	At     time.Time
}

// AgentNode tracks behavioral state for a single agent.
// Velocity is tracked via EWMA cascade (O(1) per event, O(1) memory)
// instead of fixed windows that stored every event.
type AgentNode struct {
	Cascade       *EWMACascade // EWMA at 1min, 5min, 1hr time constants
	ActiveHolds   int
	ActiveEscrows int
	TotalSpent    *big.Int
}

func newAgentNode() *AgentNode {
	return &AgentNode{
		Cascade:    NewEWMACascade(),
		TotalSpent: new(big.Int),
	}
}

// AgentSnapshot is a read-only copy of an AgentNode at a point in time.
type AgentSnapshot struct {
	WindowTotals  [3]*big.Int // filtered totals for 1min, 5min, 1hr
	ActiveHolds   int
	ActiveEscrows int
	TotalSpent    *big.Int
}

// edgeEventRetention is the maximum age of events kept in FlowEdge.Events.
// Matches the cycle detection window used by CircularFlowRule.
const edgeEventRetention = 1 * time.Hour

// maxEdgeEvents is the fixed capacity of the per-edge circular buffer.
// Prevents unbounded growth for high-frequency agent pairs.
const maxEdgeEvents = 128

// maxDFSDepth limits cycle detection traversal depth to prevent O(N)
// graph walks on every transaction.
const maxDFSDepth = 8

// graphShards is the number of shards. Must be a power of 2.
const graphShards = 128

// FlowEdge tracks bilateral volume between two agents.
// Events are stored in a fixed-capacity circular buffer.
type FlowEdge struct {
	From      string
	To        string
	Volume    *big.Int
	LastEvent time.Time
	events    [maxEdgeEvents]SpendEvent // circular buffer (fixed size, no alloc)
	head      int                       // next write position
	count     int                       // number of valid entries (≤ maxEdgeEvents)
}

// addEvent appends an event to the circular buffer, overwriting the oldest
// entry when full. O(1) per call, no slice allocation.
func (e *FlowEdge) addEvent(ev SpendEvent) {
	e.events[e.head] = ev
	e.head = (e.head + 1) % maxEdgeEvents
	if e.count < maxEdgeEvents {
		e.count++
	}
}

// recentEvents returns events within the given window. The returned slice
// is a copy safe for use outside the lock.
func (e *FlowEdge) recentEvents(cutoff time.Time) []SpendEvent {
	var out []SpendEvent
	for i := 0; i < e.count; i++ {
		idx := (e.head - e.count + i + maxEdgeEvents) % maxEdgeEvents
		ev := e.events[idx]
		if !ev.At.Before(cutoff) {
			out = append(out, ev)
		}
	}
	return out
}

// hasRecentEvent reports whether any event in the buffer is at or after cutoff.
func (e *FlowEdge) hasRecentEvent(cutoff time.Time) bool {
	for i := 0; i < e.count; i++ {
		idx := (e.head - e.count + i + maxEdgeEvents) % maxEdgeEvents
		if !e.events[idx].At.Before(cutoff) {
			return true
		}
	}
	return false
}

// graphShard holds a subset of agents and their outgoing edges.
type graphShard struct {
	mu    sync.RWMutex
	nodes map[string]*AgentNode
	edges map[string]*FlowEdge // key: "from:to"
}

// SpendGraph is the in-memory behavioral graph. Access is sharded by agent
// address (128 shards) to eliminate global lock contention on the hot path.
type SpendGraph struct {
	shards [graphShards]graphShard
}

// NewSpendGraph creates an empty graph.
func NewSpendGraph() *SpendGraph {
	g := &SpendGraph{}
	for i := range g.shards {
		g.shards[i].nodes = make(map[string]*AgentNode)
		g.shards[i].edges = make(map[string]*FlowEdge)
	}
	return g
}

func edgeKey(from, to string) string {
	return strings.ToLower(from) + ":" + strings.ToLower(to)
}

// shardFor returns the shard index for a given agent address.
func shardFor(agent string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(agent))
	return h.Sum32() & (graphShards - 1)
}

// RecordEvent logs a spending event for an agent. Updates EWMA cascade
// (O(1) per event) and total spent counter.
func (g *SpendGraph) RecordEvent(agent, counterparty string, amount *big.Int, now time.Time) {
	agent = strings.ToLower(agent)

	shard := &g.shards[shardFor(agent)]
	shard.mu.Lock()

	node := shard.getOrCreate(agent)

	// Update EWMA cascade — O(1), no eviction needed
	node.Cascade.Update(float64(amount.Int64()), now)
	node.TotalSpent.Add(node.TotalSpent, amount)

	// Update edge (edges are stored in the "from" agent's shard)
	if counterparty != "" {
		counterparty = strings.ToLower(counterparty)
		key := edgeKey(agent, counterparty)
		edge, ok := shard.edges[key]
		if !ok {
			edge = &FlowEdge{
				From:   agent,
				To:     counterparty,
				Volume: new(big.Int),
			}
			shard.edges[key] = edge
		}
		edge.Volume.Add(edge.Volume, amount)
		edge.LastEvent = now
		edge.addEvent(SpendEvent{Amount: new(big.Int).Set(amount), At: now})
	}

	shard.mu.Unlock()
}

// RecordEdgeOnly updates only the bilateral flow edge between agent and
// counterparty without touching velocity windows or TotalSpent. Used for
// settlement operations (SettleHold, ReleaseEscrow, PartialEscrowSettle)
// where the spend was already counted at hold/escrow acquisition time.
func (g *SpendGraph) RecordEdgeOnly(agent, counterparty string, amount *big.Int, now time.Time) {
	if counterparty == "" {
		return
	}
	agent = strings.ToLower(agent)
	counterparty = strings.ToLower(counterparty)

	shard := &g.shards[shardFor(agent)]
	shard.mu.Lock()

	key := edgeKey(agent, counterparty)
	edge, ok := shard.edges[key]
	if !ok {
		edge = &FlowEdge{
			From:   agent,
			To:     counterparty,
			Volume: new(big.Int),
		}
		shard.edges[key] = edge
	}
	edge.Volume.Add(edge.Volume, amount)
	edge.LastEvent = now
	edge.addEvent(SpendEvent{Amount: new(big.Int).Set(amount), At: now})

	shard.mu.Unlock()
}

// GetNode returns a snapshot of an agent's behavioral state.
// Returns nil if the agent has no recorded events.
func (g *SpendGraph) GetNode(agent string) *AgentSnapshot {
	agent = strings.ToLower(agent)
	now := time.Now()

	shard := &g.shards[shardFor(agent)]
	shard.mu.RLock()
	defer shard.mu.RUnlock()

	node, ok := shard.nodes[agent]
	if !ok {
		return nil
	}

	snap := &AgentSnapshot{
		ActiveHolds:   node.ActiveHolds,
		ActiveEscrows: node.ActiveEscrows,
		TotalSpent:    new(big.Int).Set(node.TotalSpent),
		WindowTotals:  node.Cascade.Estimates(now),
	}
	return snap
}

// GetEdge returns the flow edge between two agents. Returns nil if none.
func (g *SpendGraph) GetEdge(from, to string) *FlowEdge {
	from = strings.ToLower(from)
	to = strings.ToLower(to)

	shard := &g.shards[shardFor(from)]
	shard.mu.RLock()
	defer shard.mu.RUnlock()

	edge, ok := shard.edges[edgeKey(from, to)]
	if !ok {
		return nil
	}
	// Return a copy
	cp := &FlowEdge{
		From:      edge.From,
		To:        edge.To,
		Volume:    new(big.Int).Set(edge.Volume),
		LastEvent: edge.LastEvent,
	}
	return cp
}

// TryAcquireHold atomically checks the combined holds+escrows count against
// limit and increments ActiveHolds if under the limit. Returns true if the
// slot was acquired. This eliminates the TOCTOU race between reading the
// counter in evaluate() and incrementing it after inner.Hold() succeeds.
func (g *SpendGraph) TryAcquireHold(agent string, limit int) bool {
	agent = strings.ToLower(agent)

	shard := &g.shards[shardFor(agent)]
	shard.mu.Lock()
	defer shard.mu.Unlock()

	node := shard.getOrCreate(agent)
	if node.ActiveHolds+node.ActiveEscrows >= limit {
		return false
	}
	node.ActiveHolds++
	return true
}

// ReleaseActiveHold decrements ActiveHolds by 1. Returns false if the counter
// was already zero, indicating a mismatched acquire/release (bug).
// Unlike the old IncrementActiveHolds(-1), this does NOT silently clamp to
// zero — the caller is responsible for logging the mismatch.
func (g *SpendGraph) ReleaseActiveHold(agent string) bool {
	agent = strings.ToLower(agent)

	shard := &g.shards[shardFor(agent)]
	shard.mu.Lock()
	defer shard.mu.Unlock()

	node, ok := shard.nodes[agent]
	if !ok || node.ActiveHolds <= 0 {
		return false
	}
	node.ActiveHolds--
	return true
}

// TryAcquireEscrow atomically checks the combined holds+escrows count against
// limit and increments ActiveEscrows if under the limit. Returns true if the
// slot was acquired.
func (g *SpendGraph) TryAcquireEscrow(agent string, limit int) bool {
	agent = strings.ToLower(agent)

	shard := &g.shards[shardFor(agent)]
	shard.mu.Lock()
	defer shard.mu.Unlock()

	node := shard.getOrCreate(agent)
	if node.ActiveHolds+node.ActiveEscrows >= limit {
		return false
	}
	node.ActiveEscrows++
	return true
}

// ReleaseActiveEscrow decrements ActiveEscrows by 1. Returns false if the
// counter was already zero, indicating a mismatched acquire/release (bug).
func (g *SpendGraph) ReleaseActiveEscrow(agent string) bool {
	agent = strings.ToLower(agent)

	shard := &g.shards[shardFor(agent)]
	shard.mu.Lock()
	defer shard.mu.Unlock()

	node, ok := shard.nodes[agent]
	if !ok || node.ActiveEscrows <= 0 {
		return false
	}
	node.ActiveEscrows--
	return true
}

// HasCyclicFlow checks for A->B->...->A cycles reachable from start
// using edges with events within the given time window.
// Returns the cycle path (e.g. ["A","B","C","A"]) or nil.
//
// Traversal is bounded by maxDFSDepth to prevent O(N) walks on large graphs.
func (g *SpendGraph) HasCyclicFlow(start string, window time.Duration) []string {
	start = strings.ToLower(start)
	cutoff := time.Now().Add(-window)

	// Build adjacency list from recent edges across all shards.
	// Take read locks one shard at a time to avoid holding all locks.
	adj := make(map[string][]string)
	for i := range g.shards {
		shard := &g.shards[i]
		shard.mu.RLock()
		for _, edge := range shard.edges {
			if edge.hasRecentEvent(cutoff) {
				adj[edge.From] = append(adj[edge.From], edge.To)
			}
		}
		shard.mu.RUnlock()
	}

	// DFS from start looking for a path back to start, bounded by depth.
	visited := make(map[string]bool)
	path := []string{start}

	var dfs func(current string, depth int) []string
	dfs = func(current string, depth int) []string {
		if depth >= maxDFSDepth {
			return nil
		}
		for _, next := range adj[current] {
			if next == start && len(path) > 1 {
				return append(path, start)
			}
			if visited[next] {
				continue
			}
			visited[next] = true
			path = append(path, next)
			if result := dfs(next, depth+1); result != nil {
				return result
			}
			path = path[:len(path)-1]
			visited[next] = false
		}
		return nil
	}

	visited[start] = true
	return dfs(start, 0)
}

// getOrCreate returns the node for agent, creating it if needed.
// Caller must hold shard write lock.
func (s *graphShard) getOrCreate(agent string) *AgentNode {
	node, ok := s.nodes[agent]
	if !ok {
		node = newAgentNode()
		s.nodes[agent] = node
	}
	return node
}
