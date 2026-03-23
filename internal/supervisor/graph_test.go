package supervisor

import (
	"math/big"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// FlowEdge circular buffer unit tests
// ---------------------------------------------------------------------------

func TestFlowEdge_CircularBuffer(t *testing.T) {
	e := &FlowEdge{
		From:   "a",
		To:     "b",
		Volume: new(big.Int),
	}

	base := time.Now()
	// Add events spread over 2 hours
	e.addEvent(SpendEvent{Amount: big.NewInt(100), At: base.Add(-2 * time.Hour)})
	e.addEvent(SpendEvent{Amount: big.NewInt(200), At: base.Add(-30 * time.Minute)})
	e.addEvent(SpendEvent{Amount: big.NewInt(300), At: base})

	if e.count != 3 {
		t.Fatalf("expected 3 events, got %d", e.count)
	}

	// recentEvents with 1hr window should exclude the 2hr-old event
	cutoff := base.Add(-edgeEventRetention)
	recent := e.recentEvents(cutoff)
	if len(recent) != 2 {
		t.Fatalf("expected 2 recent events, got %d", len(recent))
	}
	if recent[0].Amount.Int64() != 200 {
		t.Fatalf("expected first recent event to be 200, got %d", recent[0].Amount.Int64())
	}
}

func TestFlowEdge_CircularBufferOverflow(t *testing.T) {
	e := &FlowEdge{
		From:   "a",
		To:     "b",
		Volume: new(big.Int),
	}

	now := time.Now()
	// Fill beyond capacity — oldest entries should be overwritten
	for i := 0; i < maxEdgeEvents+10; i++ {
		e.addEvent(SpendEvent{Amount: big.NewInt(int64(i)), At: now})
	}

	if e.count != maxEdgeEvents {
		t.Fatalf("expected count capped at %d, got %d", maxEdgeEvents, e.count)
	}

	// All events are recent
	recent := e.recentEvents(now.Add(-1 * time.Hour))
	if len(recent) != maxEdgeEvents {
		t.Fatalf("expected %d recent events, got %d", maxEdgeEvents, len(recent))
	}

	// The oldest surviving event should be #10 (the first 10 were overwritten)
	if recent[0].Amount.Int64() != 10 {
		t.Fatalf("expected oldest surviving event amount 10, got %d", recent[0].Amount.Int64())
	}
}

func TestFlowEdge_HasRecentEvent(t *testing.T) {
	e := &FlowEdge{
		From:   "a",
		To:     "b",
		Volume: new(big.Int),
	}

	now := time.Now()
	e.addEvent(SpendEvent{Amount: big.NewInt(100), At: now.Add(-2 * time.Hour)})

	// Only old events — no recent within 1hr
	if e.hasRecentEvent(now.Add(-1 * time.Hour)) {
		t.Fatal("expected no recent event")
	}

	// Add a recent one
	e.addEvent(SpendEvent{Amount: big.NewInt(200), At: now})
	if !e.hasRecentEvent(now.Add(-1 * time.Hour)) {
		t.Fatal("expected recent event")
	}
}

// ---------------------------------------------------------------------------
// SpendGraph: RecordEvent, GetNode, GetEdge
// ---------------------------------------------------------------------------

func TestSpendGraph_RecordEventAndGetNode(t *testing.T) {
	g := NewSpendGraph()
	now := time.Now()

	g.RecordEvent("0xAlice", "0xBob", big.NewInt(1000000), now)
	g.RecordEvent("0xAlice", "0xBob", big.NewInt(2000000), now)

	snap := g.GetNode("0xalice")
	if snap == nil {
		t.Fatal("expected node for alice")
	}
	if snap.TotalSpent.Int64() != 3000000 {
		t.Fatalf("expected TotalSpent 3000000, got %d", snap.TotalSpent.Int64())
	}
	// EWMA windows should approximate the total (float64 rounding tolerance)
	for i, total := range snap.WindowTotals {
		diff := total.Int64() - 3000000
		if diff < 0 {
			diff = -diff
		}
		// Allow 0.1% tolerance for float64 rounding in EWMA
		if diff > 3000 {
			t.Errorf("window %d: expected ~3000000, got %d (diff %d)", i, total.Int64(), diff)
		}
	}
}

func TestSpendGraph_GetNodeNil(t *testing.T) {
	g := NewSpendGraph()
	if g.GetNode("0xunknown") != nil {
		t.Fatal("expected nil for unknown agent")
	}
}

func TestSpendGraph_GetEdge(t *testing.T) {
	g := NewSpendGraph()
	now := time.Now()

	g.RecordEvent("0xAlice", "0xBob", big.NewInt(1000000), now)

	edge := g.GetEdge("0xAlice", "0xBob")
	if edge == nil {
		t.Fatal("expected edge alice->bob")
	}
	if edge.Volume.Int64() != 1000000 {
		t.Fatalf("expected volume 1000000, got %d", edge.Volume.Int64())
	}
	if edge.From != "0xalice" || edge.To != "0xbob" {
		t.Fatalf("expected normalized addresses, got from=%s to=%s", edge.From, edge.To)
	}
}

func TestSpendGraph_GetEdgeNil(t *testing.T) {
	g := NewSpendGraph()
	if g.GetEdge("0xAlice", "0xBob") != nil {
		t.Fatal("expected nil for non-existent edge")
	}
}

func TestSpendGraph_RecordEventNoCounterparty(t *testing.T) {
	g := NewSpendGraph()
	now := time.Now()

	// Record without counterparty — no edge should be created
	g.RecordEvent("0xAlice", "", big.NewInt(1000000), now)

	snap := g.GetNode("0xalice")
	if snap == nil {
		t.Fatal("expected node")
	}
	if snap.TotalSpent.Int64() != 1000000 {
		t.Fatalf("expected TotalSpent 1000000, got %d", snap.TotalSpent.Int64())
	}

	// No edges
	if g.GetEdge("0xAlice", "") != nil {
		t.Fatal("expected no edge for empty counterparty")
	}
}

// ---------------------------------------------------------------------------
// SpendGraph: RecordEdgeOnly
// ---------------------------------------------------------------------------

func TestSpendGraph_RecordEdgeOnly(t *testing.T) {
	g := NewSpendGraph()
	now := time.Now()

	// Pre-populate a node with a velocity event
	g.RecordEvent("0xAlice", "0xBob", big.NewInt(1000000), now)

	// RecordEdgeOnly should update edge volume but NOT velocity windows or TotalSpent
	g.RecordEdgeOnly("0xAlice", "0xBob", big.NewInt(500000), now)

	snap := g.GetNode("0xalice")
	if snap.TotalSpent.Int64() != 1000000 {
		t.Fatalf("RecordEdgeOnly should not change TotalSpent, got %d", snap.TotalSpent.Int64())
	}

	edge := g.GetEdge("0xAlice", "0xBob")
	if edge.Volume.Int64() != 1500000 {
		t.Fatalf("expected edge volume 1500000, got %d", edge.Volume.Int64())
	}
}

func TestSpendGraph_RecordEdgeOnlyEmptyCounterparty(t *testing.T) {
	g := NewSpendGraph()
	// Should be a no-op
	g.RecordEdgeOnly("0xAlice", "", big.NewInt(1000000), time.Now())

	if g.GetNode("0xalice") != nil {
		t.Fatal("RecordEdgeOnly with empty counterparty should not create node")
	}
}

func TestSpendGraph_RecordEdgeOnlyCreatesEdge(t *testing.T) {
	g := NewSpendGraph()
	now := time.Now()

	// Call RecordEdgeOnly without any prior RecordEvent
	g.RecordEdgeOnly("0xNew", "0xTarget", big.NewInt(1000000), now)

	edge := g.GetEdge("0xNew", "0xTarget")
	if edge == nil {
		t.Fatal("expected edge to be created")
	}
	if edge.Volume.Int64() != 1000000 {
		t.Fatalf("expected volume 1000000, got %d", edge.Volume.Int64())
	}
}

// ---------------------------------------------------------------------------
// SpendGraph: TryAcquire / Release Hold and Escrow
// ---------------------------------------------------------------------------

func TestSpendGraph_TryAcquireHoldBasic(t *testing.T) {
	g := NewSpendGraph()

	// Acquire 3 with limit 3
	for i := 0; i < 3; i++ {
		if !g.TryAcquireHold("0xAlice", 3) {
			t.Fatalf("acquire %d should succeed", i)
		}
	}

	// 4th should fail
	if g.TryAcquireHold("0xAlice", 3) {
		t.Fatal("4th acquire should fail")
	}

	// Release one
	if !g.ReleaseActiveHold("0xAlice") {
		t.Fatal("release should succeed")
	}

	// Now acquire should work again
	if !g.TryAcquireHold("0xAlice", 3) {
		t.Fatal("acquire after release should succeed")
	}
}

func TestSpendGraph_ReleaseActiveHoldUnderflow(t *testing.T) {
	g := NewSpendGraph()

	// Release on unknown agent → false
	if g.ReleaseActiveHold("0xUnknown") {
		t.Fatal("release on unknown agent should return false")
	}

	// Acquire and release
	g.TryAcquireHold("0xAlice", 10)
	g.ReleaseActiveHold("0xAlice")

	// Second release → underflow → false
	if g.ReleaseActiveHold("0xAlice") {
		t.Fatal("double release should return false")
	}
}

func TestSpendGraph_TryAcquireEscrowBasic(t *testing.T) {
	g := NewSpendGraph()

	if !g.TryAcquireEscrow("0xAlice", 2) {
		t.Fatal("first acquire should succeed")
	}
	if !g.TryAcquireEscrow("0xAlice", 2) {
		t.Fatal("second acquire should succeed")
	}
	if g.TryAcquireEscrow("0xAlice", 2) {
		t.Fatal("third acquire should fail (limit 2)")
	}
}

func TestSpendGraph_ReleaseActiveEscrowUnderflow(t *testing.T) {
	g := NewSpendGraph()

	if g.ReleaseActiveEscrow("0xUnknown") {
		t.Fatal("release on unknown agent should return false")
	}

	g.TryAcquireEscrow("0xAlice", 10)
	g.ReleaseActiveEscrow("0xAlice")

	if g.ReleaseActiveEscrow("0xAlice") {
		t.Fatal("double release should return false")
	}
}

func TestSpendGraph_MixedHoldsAndEscrowsShareLimit(t *testing.T) {
	g := NewSpendGraph()

	// Acquire 2 holds and 1 escrow with limit 3
	g.TryAcquireHold("0xAlice", 3)
	g.TryAcquireHold("0xAlice", 3)
	g.TryAcquireEscrow("0xAlice", 3)

	// Next hold should fail (2+1=3 = limit)
	if g.TryAcquireHold("0xAlice", 3) {
		t.Fatal("should fail: holds+escrows at limit")
	}
	// Next escrow should also fail
	if g.TryAcquireEscrow("0xAlice", 3) {
		t.Fatal("should fail: holds+escrows at limit")
	}
}

// ---------------------------------------------------------------------------
// SpendGraph: HasCyclicFlow
// ---------------------------------------------------------------------------

func TestSpendGraph_HasCyclicFlowNoCycle(t *testing.T) {
	g := NewSpendGraph()
	now := time.Now()

	// Linear: A -> B -> C (no cycle)
	g.RecordEvent("0xa", "0xb", big.NewInt(100), now)
	g.RecordEvent("0xb", "0xc", big.NewInt(100), now)

	cycle := g.HasCyclicFlow("0xa", 1*time.Hour)
	if cycle != nil {
		t.Fatalf("expected no cycle, got %v", cycle)
	}
}

func TestSpendGraph_HasCyclicFlowDirectCycle(t *testing.T) {
	g := NewSpendGraph()
	now := time.Now()

	// A -> B -> A
	g.RecordEvent("0xa", "0xb", big.NewInt(100), now)
	g.RecordEvent("0xb", "0xa", big.NewInt(100), now)

	cycle := g.HasCyclicFlow("0xa", 1*time.Hour)
	if cycle == nil {
		t.Fatal("expected cycle")
	}
	if len(cycle) != 3 { // [a, b, a]
		t.Fatalf("expected cycle length 3, got %d: %v", len(cycle), cycle)
	}
}

func TestSpendGraph_HasCyclicFlowExpiredEdges(t *testing.T) {
	g := NewSpendGraph()
	old := time.Now().Add(-2 * time.Hour) // older than 1hr window

	// Create cycle with old events
	g.RecordEvent("0xa", "0xb", big.NewInt(100), old)
	g.RecordEvent("0xb", "0xa", big.NewInt(100), old)

	// Cycle detection with 1hr window should not find it
	cycle := g.HasCyclicFlow("0xa", 1*time.Hour)
	if cycle != nil {
		t.Fatalf("expected no cycle (edges expired), got %v", cycle)
	}
}

func TestSpendGraph_HasCyclicFlowThreeNodeCycle(t *testing.T) {
	g := NewSpendGraph()
	now := time.Now()

	g.RecordEvent("0xa", "0xb", big.NewInt(100), now)
	g.RecordEvent("0xb", "0xc", big.NewInt(100), now)
	g.RecordEvent("0xc", "0xa", big.NewInt(100), now)

	cycle := g.HasCyclicFlow("0xa", 1*time.Hour)
	if cycle == nil {
		t.Fatal("expected 3-node cycle")
	}
	// Should be [a, b, c, a]
	if len(cycle) != 4 {
		t.Fatalf("expected cycle length 4, got %d: %v", len(cycle), cycle)
	}
}

func TestSpendGraph_HasCyclicFlowDepthBounded(t *testing.T) {
	g := NewSpendGraph()
	now := time.Now()

	// Create a chain longer than maxDFSDepth
	agents := make([]string, maxDFSDepth+3)
	for i := range agents {
		agents[i] = "0x" + string(rune('a'+i))
	}
	for i := 0; i < len(agents)-1; i++ {
		g.RecordEvent(agents[i], agents[i+1], big.NewInt(100), now)
	}
	// Close the cycle
	g.RecordEvent(agents[len(agents)-1], agents[0], big.NewInt(100), now)

	// Should NOT detect cycle because chain exceeds maxDFSDepth
	cycle := g.HasCyclicFlow(agents[0], 1*time.Hour)
	if cycle != nil {
		t.Fatalf("expected no cycle detection beyond maxDFSDepth, got %v", cycle)
	}
}

// ---------------------------------------------------------------------------
// SpendGraph: Active hold/escrow tracking in snapshot
// ---------------------------------------------------------------------------

func TestSpendGraph_SnapshotTracksHoldsAndEscrows(t *testing.T) {
	g := NewSpendGraph()

	g.TryAcquireHold("0xAlice", 10)
	g.TryAcquireHold("0xAlice", 10)
	g.TryAcquireEscrow("0xAlice", 10)

	snap := g.GetNode("0xalice")
	if snap == nil {
		t.Fatal("expected node after acquiring holds")
	}
	if snap.ActiveHolds != 2 {
		t.Errorf("expected 2 active holds, got %d", snap.ActiveHolds)
	}
	if snap.ActiveEscrows != 1 {
		t.Errorf("expected 1 active escrow, got %d", snap.ActiveEscrows)
	}
}

// ---------------------------------------------------------------------------
// edgeKey: case insensitivity
// ---------------------------------------------------------------------------

func TestEdgeKey_CaseInsensitive(t *testing.T) {
	k1 := edgeKey("0xAlice", "0xBob")
	k2 := edgeKey("0xALICE", "0xBOB")
	if k1 != k2 {
		t.Fatalf("expected same key, got %q vs %q", k1, k2)
	}
}
