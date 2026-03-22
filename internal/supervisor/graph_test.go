package supervisor

import (
	"math/big"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// VelocityWindow unit tests
// ---------------------------------------------------------------------------

func TestVelocityWindow_AddAndEvict(t *testing.T) {
	w := newVelocityWindow(1 * time.Minute)

	base := time.Now()

	// Add two events
	w.add(big.NewInt(100), base)
	w.add(big.NewInt(200), base.Add(10*time.Second))

	if w.Total.Int64() != 300 {
		t.Fatalf("expected total 300, got %d", w.Total.Int64())
	}
	if len(w.Events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(w.Events))
	}

	// Evict at 70s later — first event at base is now >1min old
	w.evict(base.Add(70 * time.Second))
	if w.Total.Int64() != 200 {
		t.Fatalf("expected total 200 after evict, got %d", w.Total.Int64())
	}
	if len(w.Events) != 1 {
		t.Fatalf("expected 1 event after evict, got %d", len(w.Events))
	}
}

func TestVelocityWindow_Snapshot(t *testing.T) {
	w := newVelocityWindow(1 * time.Minute)

	base := time.Now()
	w.add(big.NewInt(100), base.Add(-90*time.Second)) // already expired
	w.add(big.NewInt(200), base.Add(-30*time.Second)) // still active
	w.add(big.NewInt(300), base)                      // still active

	snap := w.snapshot(base)
	if snap.Int64() != 500 {
		t.Fatalf("expected snapshot 500, got %d", snap.Int64())
	}

	// snapshot should NOT mutate the original Total
	if w.Total.Int64() != 600 {
		t.Fatalf("snapshot should not mutate Total, got %d", w.Total.Int64())
	}
}

func TestVelocityWindow_EvictNone(t *testing.T) {
	w := newVelocityWindow(1 * time.Hour)

	now := time.Now()
	w.add(big.NewInt(100), now)
	w.add(big.NewInt(200), now)

	// Evict at same time — nothing expired
	w.evict(now)
	if w.Total.Int64() != 300 {
		t.Fatalf("expected 300 (no eviction), got %d", w.Total.Int64())
	}
	if len(w.Events) != 2 {
		t.Fatalf("expected 2 events (no eviction), got %d", len(w.Events))
	}
}

func TestVelocityWindow_EvictAll(t *testing.T) {
	w := newVelocityWindow(1 * time.Minute)

	base := time.Now()
	w.add(big.NewInt(100), base)
	w.add(big.NewInt(200), base)

	// Evict 2 minutes later — everything is expired
	w.evict(base.Add(2 * time.Minute))
	if w.Total.Int64() != 0 {
		t.Fatalf("expected 0 after full evict, got %d", w.Total.Int64())
	}
	if len(w.Events) != 0 {
		t.Fatalf("expected 0 events after full evict, got %d", len(w.Events))
	}
}

// ---------------------------------------------------------------------------
// FlowEdge unit tests
// ---------------------------------------------------------------------------

func TestFlowEdge_EvictOld(t *testing.T) {
	e := &FlowEdge{
		From:   "a",
		To:     "b",
		Volume: new(big.Int),
	}

	base := time.Now()
	// Add events spread over 2 hours
	e.Events = []SpendEvent{
		{Amount: big.NewInt(100), At: base.Add(-2 * time.Hour)},
		{Amount: big.NewInt(200), At: base.Add(-30 * time.Minute)},
		{Amount: big.NewInt(300), At: base},
	}

	e.evictOld(base)

	// First event (2hr ago) should be evicted (edgeEventRetention = 1hr)
	if len(e.Events) != 2 {
		t.Fatalf("expected 2 events after evict, got %d", len(e.Events))
	}
	if e.Events[0].Amount.Int64() != 200 {
		t.Fatalf("expected first remaining event to be 200, got %d", e.Events[0].Amount.Int64())
	}
}

func TestFlowEdge_EvictOldNone(t *testing.T) {
	e := &FlowEdge{
		From:   "a",
		To:     "b",
		Volume: new(big.Int),
	}

	now := time.Now()
	e.Events = []SpendEvent{
		{Amount: big.NewInt(100), At: now},
		{Amount: big.NewInt(200), At: now},
	}

	e.evictOld(now)
	if len(e.Events) != 2 {
		t.Fatalf("expected 2 events (none evicted), got %d", len(e.Events))
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
	// All three windows should have the total
	for i, total := range snap.WindowTotals {
		if total.Int64() != 3000000 {
			t.Errorf("window %d: expected 3000000, got %d", i, total.Int64())
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
