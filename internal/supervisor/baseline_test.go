package supervisor

import (
	"context"
	"errors"
	"log/slog"
	"math/big"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// 1. BaselineRule: no baseline → returns nil (VelocityRule still applies)
// ---------------------------------------------------------------------------

func TestBaselineRuleNoBaseline(t *testing.T) {
	mock := &mockLedgerService{}
	store := NewMemoryBaselineStore()
	sv := New(mock, WithBaselineStore(store))
	ctx := context.Background()

	// No baseline exists for this agent. The baseline rule should return nil
	// and the default velocity rule should still apply.
	err := sv.Hold(ctx, "0xNoBaseline", "1.00", "ref1")
	if err != nil {
		t.Fatalf("expected allow (no baseline, within velocity): %v", err)
	}
}

// ---------------------------------------------------------------------------
// 2. BaselineRule: insufficient samples → returns nil
// ---------------------------------------------------------------------------

func TestBaselineRuleInsufficientSamples(t *testing.T) {
	mock := &mockLedgerService{}
	store := NewMemoryBaselineStore()
	sv := New(mock, WithBaselineStore(store))

	// Manually seed a baseline with fewer than 24 sample hours
	sv.RefreshBaselines(map[string]*AgentBaseline{
		"0xfewsamples": {
			AgentAddr:    "0xfewsamples",
			HourlyMean:   big.NewInt(1000000), // $1
			HourlyStddev: big.NewInt(100000),  // $0.10
			SampleHours:  10,                  // < 24
		},
	})

	ctx := context.Background()
	// Should not be blocked by baseline (insufficient data)
	err := sv.Hold(ctx, "0xFewSamples", "1.00", "ref1")
	if err != nil {
		t.Fatalf("expected allow (insufficient samples): %v", err)
	}
}

// ---------------------------------------------------------------------------
// 3. BaselineRule: spend exceeds mean+3*stddev → Deny
// ---------------------------------------------------------------------------

func TestBaselineRuleDenyAnomaly(t *testing.T) {
	mock := &mockLedgerService{}
	store := NewMemoryBaselineStore()
	sv := New(mock,
		WithReputation(&mockReputation{tier: "new"}),
		WithBaselineStore(store),
	)

	// "new" tier: velocity=$50/hr, floor = 50% of $50 = $25
	// mean=$20, raw stddev=$1 → min stddev = max(20%*$20, $1) = max($4, $1) = $4
	// effective stddev = max($1, $4) = $4
	// threshold = $20 + 3*$4 = $32
	// effective = max($32, $25) = $32
	sv.RefreshBaselines(map[string]*AgentBaseline{
		"0xanomaly": {
			AgentAddr:    "0xanomaly",
			HourlyMean:   mustParse("20"), // $20/hr
			HourlyStddev: mustParse("1"),  // $1/hr (floored to $4 at eval time)
			SampleHours:  48,
		},
	})

	ctx := context.Background()
	// $4/tx stays within NewAgentRule ($5 limit).
	// 8 holds of $4 = $32 (at threshold). 9th projects to $36 > $32.
	for i := 0; i < 8; i++ {
		if err := sv.Hold(ctx, "0xAnomaly", "4.00", "ref"); err != nil {
			t.Fatalf("hold %d should succeed: %v", i, err)
		}
		_ = sv.ReleaseHold(ctx, "0xAnomaly", "4.00", "ref")
	}

	// 9th hold: projected = $32 + $4 = $36 > $32 → denied
	err := sv.Hold(ctx, "0xAnomaly", "4.00", "ref_deny")
	if err == nil {
		t.Fatal("should be denied by baseline anomaly rule")
	}
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("expected ErrDenied, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// 4. BaselineRule: spend within range → nil (allow)
// ---------------------------------------------------------------------------

func TestBaselineRuleAllowNormal(t *testing.T) {
	mock := &mockLedgerService{}
	store := NewMemoryBaselineStore()
	sv := New(mock,
		WithReputation(&mockReputation{tier: "new"}),
		WithBaselineStore(store),
	)

	// mean=$30, stddev=$5 → minStddev = max($6, $1) = $6 → effective stddev = $6
	// threshold = $30 + 3*$6 = $48, floor = $25
	// effective = max($48, $25) = $48
	sv.RefreshBaselines(map[string]*AgentBaseline{
		"0xnormal": {
			AgentAddr:    "0xnormal",
			HourlyMean:   mustParse("30"),
			HourlyStddev: mustParse("5"),
			SampleHours:  48,
		},
	})

	ctx := context.Background()
	// $5 is well within $45 threshold
	err := sv.Hold(ctx, "0xNormal", "5.00", "ref1")
	if err != nil {
		t.Fatalf("should be allowed (within baseline): %v", err)
	}
}

// ---------------------------------------------------------------------------
// 5. BaselineRule: floor protection (low baseline doesn't over-restrict)
// ---------------------------------------------------------------------------

func TestBaselineRuleFloorProtection(t *testing.T) {
	mock := &mockLedgerService{}
	store := NewMemoryBaselineStore()
	sv := New(mock,
		WithReputation(&mockReputation{tier: "established"}),
		WithBaselineStore(store),
	)

	// Very low baseline: mean=$1, stddev=$0.50 → threshold = $2.50
	// Floor = 50% of $5000 (established) = $2500
	// effective = max($2.50, $2500) = $2500
	sv.RefreshBaselines(map[string]*AgentBaseline{
		"0xlow": {
			AgentAddr:    "0xlow",
			HourlyMean:   mustParse("1"),
			HourlyStddev: big.NewInt(500000), // $0.50
			SampleHours:  48,
		},
	})

	ctx := context.Background()
	// $100 should be allowed because floor protects agent
	err := sv.Hold(ctx, "0xLow", "100.00", "ref1")
	if err != nil {
		t.Fatalf("floor should protect: $100 < $2500 floor: %v", err)
	}
}

// ---------------------------------------------------------------------------
// 6. Unit test for mean/stddev computation
// ---------------------------------------------------------------------------

func TestComputeMeanStddev(t *testing.T) {
	totals := map[time.Time]*big.Int{
		time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC): big.NewInt(10000000), // $10
		time.Date(2024, 1, 1, 1, 0, 0, 0, time.UTC): big.NewInt(20000000), // $20
		time.Date(2024, 1, 1, 2, 0, 0, 0, time.UTC): big.NewInt(30000000), // $30
	}

	mean, stddev := computeMeanStddev(totals)

	// Mean = ($10+$20+$30)/3 = $20 = 20000000
	if mean.Int64() != 20000000 {
		t.Errorf("expected mean 20000000, got %d", mean.Int64())
	}

	// Population stddev of [10,20,30] million:
	// var = ((10-20)^2 + (20-20)^2 + (30-20)^2) / 3 = (100+0+100)*10^12 / 3
	// Actually in USDC units: values are 10M, 20M, 30M
	// var = ((10M-20M)^2 + (20M-20M)^2 + (30M-20M)^2) / 3
	//     = (100T + 0 + 100T) / 3 = 66.67T
	// stddev = sqrt(66.67T) ≈ 8164965 ≈ $8.16
	if stddev.Int64() < 8000000 || stddev.Int64() > 8500000 {
		t.Errorf("expected stddev ~8164965, got %d", stddev.Int64())
	}

	// Empty map
	emptyMean, emptyStddev := computeMeanStddev(map[time.Time]*big.Int{})
	if emptyMean.Int64() != 0 || emptyStddev.Int64() != 0 {
		t.Errorf("expected zeros for empty, got mean=%d stddev=%d", emptyMean.Int64(), emptyStddev.Int64())
	}
}

// ---------------------------------------------------------------------------
// 7. EventWriter: drops on full channel
// ---------------------------------------------------------------------------

func TestEventWriterDropsOnFull(t *testing.T) {
	store := NewMemoryBaselineStore()
	w := NewEventWriter(store, testLogger())

	// Fill the channel without starting the writer
	for i := 0; i < eventWriterChanSize+10; i++ {
		w.Send("0xAgent", "", big.NewInt(1000000), time.Now())
	}

	if w.Dropped() != 10 {
		t.Errorf("expected 10 drops, got %d", w.Dropped())
	}
}

// ---------------------------------------------------------------------------
// 8. EventWriter: batch persistence
// ---------------------------------------------------------------------------

func TestEventWriterBatch(t *testing.T) {
	store := NewMemoryBaselineStore()
	w := NewEventWriter(store, testLogger())

	ctx, cancel := context.WithCancel(context.Background())
	go w.Start(ctx)

	// Send some events
	for i := 0; i < 5; i++ {
		w.Send("0xagent", "0xcounterparty", big.NewInt(1000000), time.Now())
	}

	// Give time for flush
	time.Sleep(700 * time.Millisecond)
	cancel()
	// Wait for stop
	time.Sleep(100 * time.Millisecond)

	events := store.GetEvents()
	if len(events) != 5 {
		t.Fatalf("expected 5 persisted events, got %d", len(events))
	}
}

// ---------------------------------------------------------------------------
// 9. BaselineWorker: refreshes cache
// ---------------------------------------------------------------------------

func TestBaselineWorkerRefreshesCache(t *testing.T) {
	store := NewMemoryBaselineStore()
	mock := &mockLedgerService{}
	sv := New(mock, WithBaselineStore(store))

	// Pre-seed baselines in the store
	_ = store.SaveBaselineBatch(context.Background(), []*AgentBaseline{
		{
			AgentAddr:    "0xworker",
			HourlyMean:   big.NewInt(5000000),
			HourlyStddev: big.NewInt(1000000),
			SampleHours:  48,
		},
	})

	timer := NewBaselineTimer(store, sv, testLogger())

	// Call loadBaselines directly
	timer.loadBaselines(context.Background())

	// Verify it's in the supervisor cache
	b := sv.GetCachedBaseline("0xworker")
	if b == nil {
		t.Fatal("expected baseline in cache after load")
	}
	if b.HourlyMean.Int64() != 5000000 {
		t.Errorf("expected mean 5000000, got %d", b.HourlyMean.Int64())
	}
}

// ---------------------------------------------------------------------------
// 10. Denial log feature vector
// ---------------------------------------------------------------------------

func TestDenialLogFeatureVector(t *testing.T) {
	mock := &mockLedgerService{}
	store := NewMemoryBaselineStore()
	sv := New(mock,
		WithReputation(&mockReputation{tier: "new"}),
		WithBaselineStore(store),
	)

	ctx := context.Background()
	// Trigger velocity denial: spend $51 as "new" agent ($50/hr limit)
	// Use $5 tx (at per-tx limit)
	for i := 0; i < 10; i++ {
		_ = sv.Hold(ctx, "0xDenied", "5.00", "ref")
		_ = sv.ReleaseHold(ctx, "0xDenied", "5.00", "ref")
	}
	// This should be denied by velocity rule
	_ = sv.Hold(ctx, "0xDenied", "5.00", "ref_final")

	// Give async goroutine time to persist
	time.Sleep(200 * time.Millisecond)

	denials := store.GetDenials()
	if len(denials) < 1 {
		t.Fatal("expected at least 1 denial logged")
	}
	d := denials[0]
	if d.AgentAddr != "0xDenied" {
		t.Errorf("expected agent 0xDenied, got %s", d.AgentAddr)
	}
	if d.RuleName != "velocity" {
		t.Errorf("expected rule 'velocity', got %s", d.RuleName)
	}
	if d.OpType != "hold" {
		t.Errorf("expected op_type 'hold', got %s", d.OpType)
	}
	if d.Tier != "new" {
		t.Errorf("expected tier 'new', got %s", d.Tier)
	}
}

// ---------------------------------------------------------------------------
// 11. Existing rules unchanged with baseline enabled but empty
// ---------------------------------------------------------------------------

func TestExistingRulesUnchanged(t *testing.T) {
	mock := &mockLedgerService{}
	store := NewMemoryBaselineStore()
	rep := &mockReputation{tier: "new"}
	sv := New(mock, WithReputation(rep), WithBaselineStore(store))
	ctx := context.Background()

	// NewAgentRule still applies: $6 > $5 per-tx limit
	err := sv.Hold(ctx, "0xTest", "6.00", "ref1")
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("expected new agent per-tx deny, got: %v", err)
	}

	// Velocity still applies: 10 x $5 = $50, 11th denied
	for i := 0; i < 10; i++ {
		_ = sv.Hold(ctx, "0xTest2", "5.00", "ref")
		_ = sv.ReleaseHold(ctx, "0xTest2", "5.00", "ref")
	}
	err = sv.Hold(ctx, "0xTest2", "5.00", "ref_final")
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("expected velocity deny, got: %v", err)
	}

	// Passthrough still works
	_, _ = sv.GetBalance(ctx, "0xTest")
	err = sv.Deposit(ctx, "0xTest", "100.00", "tx1")
	if err != nil {
		t.Fatalf("deposit should pass through: %v", err)
	}
}

// ---------------------------------------------------------------------------
// 12. Graph rebuild from persisted events
// ---------------------------------------------------------------------------

func TestGraphRebuildFromEvents(t *testing.T) {
	store := NewMemoryBaselineStore()
	mock := &mockLedgerService{}
	sv := New(mock, WithBaselineStore(store))

	now := time.Now()

	// Persist events directly to store (simulating previous server run)
	events := []*SpendEventRecord{
		{AgentAddr: "0xrebuild", Counterparty: "0xseller", Amount: big.NewInt(5000000), CreatedAt: now.Add(-30 * time.Minute)},
		{AgentAddr: "0xrebuild", Counterparty: "0xseller", Amount: big.NewInt(3000000), CreatedAt: now.Add(-15 * time.Minute)},
	}
	_ = store.AppendSpendEventBatch(context.Background(), events)

	// Create timer and rebuild graph
	timer := NewBaselineTimer(store, sv, testLogger())
	timer.rebuildGraph(context.Background())

	// Verify the graph has the events
	snap := sv.graph.GetNode("0xrebuild")
	if snap == nil {
		t.Fatal("expected node in graph after rebuild")
	}

	// 1hr window should have both events: $5 + $3 = $8
	if snap.WindowTotals[2].Int64() != 8000000 {
		t.Errorf("expected 1hr total 8000000, got %d", snap.WindowTotals[2].Int64())
	}

	// Edge should exist
	edge := sv.graph.GetEdge("0xrebuild", "0xseller")
	if edge == nil {
		t.Fatal("expected edge after rebuild")
	}
	if edge.Volume.Int64() != 8000000 {
		t.Errorf("expected edge volume 8000000, got %d", edge.Volume.Int64())
	}
}

// ---------------------------------------------------------------------------
// 13. Minimum stddev prevents cold-start lock-in
// ---------------------------------------------------------------------------

func TestMinStddevFloor(t *testing.T) {
	mock := &mockLedgerService{}
	store := NewMemoryBaselineStore()
	sv := New(mock,
		WithReputation(&mockReputation{tier: "new"}),
		WithBaselineStore(store),
	)

	// Agent consistently spends $20/hr → stddev=0.
	// Without min stddev: threshold = $20 + 0 = $20. Agent locked at $20.
	// With min stddev: effectiveStddev = max(20%*$20, $1) = $4
	// threshold = $20 + 3*$4 = $32. Agent gets 60% headroom.
	sv.RefreshBaselines(map[string]*AgentBaseline{
		"0xconsistent": {
			AgentAddr:    "0xconsistent",
			HourlyMean:   mustParse("20"),
			HourlyStddev: big.NewInt(0), // zero stddev
			SampleHours:  48,
		},
	})

	ctx := context.Background()
	// $5 should be allowed ($5 < $32 threshold)
	err := sv.Hold(ctx, "0xConsistent", "5.00", "ref1")
	if err != nil {
		t.Fatalf("should allow $5 with min stddev headroom: %v", err)
	}
	_ = sv.ReleaseHold(ctx, "0xConsistent", "5.00", "ref1")

	// Accumulate $28, then $5 more = $33 > $32 → should deny
	for i := 0; i < 7; i++ {
		_ = sv.Hold(ctx, "0xConsistent", "4.00", "ref")
		_ = sv.ReleaseHold(ctx, "0xConsistent", "4.00", "ref")
	}
	err = sv.Hold(ctx, "0xConsistent", "5.00", "ref_deny")
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("expected deny at $33 > $32 threshold, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// 14. Human-readable denial message format
// ---------------------------------------------------------------------------

func TestDenialMessageHumanReadable(t *testing.T) {
	mock := &mockLedgerService{}
	store := NewMemoryBaselineStore()
	sv := New(mock,
		WithReputation(&mockReputation{tier: "new"}),
		WithBaselineStore(store),
	)

	sv.RefreshBaselines(map[string]*AgentBaseline{
		"0xmsg": {
			AgentAddr:    "0xmsg",
			HourlyMean:   mustParse("10"),
			HourlyStddev: mustParse("1"),
			SampleHours:  48,
		},
	})

	ctx := context.Background()
	// Exceed threshold: mean=$10, minStddev=max($2,$1)=$2, threshold=$10+3*$2=$16
	// floor=$25. effective=max($16,$25)=$25.
	// Need to exceed $25. 6*$5=$30, 7th projects to $35 > $25.
	for i := 0; i < 6; i++ {
		_ = sv.Hold(ctx, "0xMsg", "5.00", "ref")
		_ = sv.ReleaseHold(ctx, "0xMsg", "5.00", "ref")
	}
	err := sv.Hold(ctx, "0xMsg", "5.00", "ref_deny")
	if err == nil {
		t.Fatal("expected denial")
	}

	// Verify message uses dollar format, not raw integers
	msg := err.Error()
	if !containsAll(msg, "$", "/hr", "operator") {
		t.Errorf("expected human-readable message with $, /hr, operator; got: %s", msg)
	}
}

// ---------------------------------------------------------------------------
// 15. Event pruning
// ---------------------------------------------------------------------------

func TestEventPruning(t *testing.T) {
	store := NewMemoryBaselineStore()

	now := time.Now()
	old := now.Add(-10 * 24 * time.Hour) // 10 days ago
	recent := now.Add(-1 * time.Hour)

	_ = store.AppendSpendEventBatch(context.Background(), []*SpendEventRecord{
		{AgentAddr: "0xa", Amount: big.NewInt(1000000), CreatedAt: old},
		{AgentAddr: "0xa", Amount: big.NewInt(2000000), CreatedAt: old.Add(time.Hour)},
		{AgentAddr: "0xa", Amount: big.NewInt(3000000), CreatedAt: recent},
	})

	// Prune events older than 8 days
	pruned, err := store.PruneOldEvents(context.Background(), now.Add(-8*24*time.Hour))
	if err != nil {
		t.Fatalf("prune failed: %v", err)
	}
	if pruned != 2 {
		t.Errorf("expected 2 pruned, got %d", pruned)
	}

	events := store.GetEvents()
	if len(events) != 1 {
		t.Errorf("expected 1 remaining event, got %d", len(events))
	}
}

// ---------------------------------------------------------------------------
// 16. Only settled spends are persisted (not holds)
// ---------------------------------------------------------------------------

func TestOnlySettledSpendsArePersisted(t *testing.T) {
	mock := &mockLedgerService{}
	store := NewMemoryBaselineStore()
	sv := New(mock, WithBaselineStore(store))
	w := NewEventWriter(store, testLogger())
	sv.SetEventWriter(w)

	ctx, cancel := context.WithCancel(context.Background())
	go w.Start(ctx)

	// Hold should NOT persist
	_ = sv.Hold(ctx, "0xAgent", "10.00", "ref1")
	_ = sv.ReleaseHold(ctx, "0xAgent", "10.00", "ref1")

	// Transfer SHOULD persist
	_ = sv.Transfer(ctx, "0xAgent", "0xSeller", "5.00", "ref2")

	// SettleHold SHOULD persist
	_ = sv.Hold(ctx, "0xAgent", "3.00", "ref3")
	_ = sv.SettleHold(ctx, "0xAgent", "0xSeller", "3.00", "ref3")

	// Flush
	time.Sleep(700 * time.Millisecond)
	cancel()
	time.Sleep(100 * time.Millisecond)

	events := store.GetEvents()
	// Should have exactly 2 events (Transfer + SettleHold), NOT the Hold
	if len(events) != 2 {
		t.Fatalf("expected 2 persisted events (transfer + settle), got %d", len(events))
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func testLogger() *slog.Logger {
	return slog.Default()
}

func containsAll(s string, substrs ...string) bool {
	for _, sub := range substrs {
		found := false
		for i := 0; i <= len(s)-len(sub); i++ {
			if s[i:i+len(sub)] == sub {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
