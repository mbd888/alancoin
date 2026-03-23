// Package supervisor provides a ledger decorator that evaluates agent
// spending patterns before allowing money-moving operations.
package supervisor

import (
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

// FlowEdge tracks bilateral volume between two agents.
type FlowEdge struct {
	From      string
	To        string
	Volume    *big.Int
	LastEvent time.Time
	Events    []SpendEvent
}

// evictOld removes events older than edgeEventRetention to prevent unbounded growth.
func (e *FlowEdge) evictOld(now time.Time) {
	cutoff := now.Add(-edgeEventRetention)
	i := 0
	for i < len(e.Events) && e.Events[i].At.Before(cutoff) {
		i++
	}
	if i > 0 {
		e.Events = append(e.Events[:0], e.Events[i:]...)
	}
}

// SpendGraph is the in-memory behavioral graph. All access is serialized
// by a sync.RWMutex.
type SpendGraph struct {
	mu    sync.RWMutex
	nodes map[string]*AgentNode
	edges map[string]*FlowEdge // key: "from:to"
}

// NewSpendGraph creates an empty graph.
func NewSpendGraph() *SpendGraph {
	return &SpendGraph{
		nodes: make(map[string]*AgentNode),
		edges: make(map[string]*FlowEdge),
	}
}

func edgeKey(from, to string) string {
	return strings.ToLower(from) + ":" + strings.ToLower(to)
}

// RecordEvent logs a spending event for an agent. Updates EWMA cascade
// (O(1) per event) and total spent counter.
func (g *SpendGraph) RecordEvent(agent, counterparty string, amount *big.Int, now time.Time) {
	agent = strings.ToLower(agent)

	g.mu.Lock()
	defer g.mu.Unlock()

	node := g.getOrCreate(agent)

	// Update EWMA cascade — O(1), no eviction needed
	node.Cascade.Update(float64(amount.Int64()), now)
	node.TotalSpent.Add(node.TotalSpent, amount)

	// Update edge
	if counterparty != "" {
		counterparty = strings.ToLower(counterparty)
		key := edgeKey(agent, counterparty)
		edge, ok := g.edges[key]
		if !ok {
			edge = &FlowEdge{
				From:   agent,
				To:     counterparty,
				Volume: new(big.Int),
			}
			g.edges[key] = edge
		}
		edge.Volume.Add(edge.Volume, amount)
		edge.LastEvent = now
		edge.evictOld(now)
		edge.Events = append(edge.Events, SpendEvent{Amount: new(big.Int).Set(amount), At: now})
	}
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

	g.mu.Lock()
	defer g.mu.Unlock()

	key := edgeKey(agent, counterparty)
	edge, ok := g.edges[key]
	if !ok {
		edge = &FlowEdge{
			From:   agent,
			To:     counterparty,
			Volume: new(big.Int),
		}
		g.edges[key] = edge
	}
	edge.Volume.Add(edge.Volume, amount)
	edge.LastEvent = now
	edge.evictOld(now)
	edge.Events = append(edge.Events, SpendEvent{Amount: new(big.Int).Set(amount), At: now})
}

// GetNode returns a snapshot of an agent's behavioral state.
// Returns nil if the agent has no recorded events.
func (g *SpendGraph) GetNode(agent string) *AgentSnapshot {
	agent = strings.ToLower(agent)
	now := time.Now()

	g.mu.RLock()
	defer g.mu.RUnlock()

	node, ok := g.nodes[agent]
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
	g.mu.RLock()
	defer g.mu.RUnlock()

	edge, ok := g.edges[edgeKey(from, to)]
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

	g.mu.Lock()
	defer g.mu.Unlock()

	node := g.getOrCreate(agent)
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

	g.mu.Lock()
	defer g.mu.Unlock()

	node, ok := g.nodes[agent]
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

	g.mu.Lock()
	defer g.mu.Unlock()

	node := g.getOrCreate(agent)
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

	g.mu.Lock()
	defer g.mu.Unlock()

	node, ok := g.nodes[agent]
	if !ok || node.ActiveEscrows <= 0 {
		return false
	}
	node.ActiveEscrows--
	return true
}

// HasCyclicFlow checks for A->B->...->A cycles reachable from start
// using edges with events within the given time window.
// Returns the cycle path (e.g. ["A","B","C","A"]) or nil.
func (g *SpendGraph) HasCyclicFlow(start string, window time.Duration) []string {
	start = strings.ToLower(start)
	cutoff := time.Now().Add(-window)

	g.mu.RLock()
	defer g.mu.RUnlock()

	// Build adjacency list from recent edges
	adj := make(map[string][]string)
	for _, edge := range g.edges {
		for _, ev := range edge.Events {
			if !ev.At.Before(cutoff) {
				adj[edge.From] = append(adj[edge.From], edge.To)
				break // one recent event is enough to include the edge
			}
		}
	}

	// DFS from start looking for a path back to start
	visited := make(map[string]bool)
	path := []string{start}

	var dfs func(current string) []string
	dfs = func(current string) []string {
		for _, next := range adj[current] {
			if next == start && len(path) > 1 {
				return append(path, start)
			}
			if visited[next] {
				continue
			}
			visited[next] = true
			path = append(path, next)
			if result := dfs(next); result != nil {
				return result
			}
			path = path[:len(path)-1]
			visited[next] = false
		}
		return nil
	}

	visited[start] = true
	return dfs(start)
}

// getOrCreate returns the node for agent, creating it if needed.
// Caller must hold write lock.
func (g *SpendGraph) getOrCreate(agent string) *AgentNode {
	node, ok := g.nodes[agent]
	if !ok {
		node = newAgentNode()
		g.nodes[agent] = node
	}
	return node
}
