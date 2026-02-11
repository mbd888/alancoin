package risk

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestNormalTransaction(t *testing.T) {
	engine := NewEngine(NewMemoryStore())

	// Seed history spread over 24h to various known recipients
	w := engine.getWindow("key1")
	w.mu.Lock()
	for i := 0; i < 20; i++ {
		w.entries = append(w.entries, windowEntry{
			To:        fmt.Sprintf("0x%040d", i%5),
			AmountUSD: 0.50,
			Timestamp: time.Now().Add(-time.Duration(i) * time.Hour),
		})
	}
	w.mu.Unlock()

	// Transaction to a known recipient at a normal amount
	tx := &TransactionContext{
		KeyID:      "key1",
		To:         fmt.Sprintf("0x%040d", 1),
		Amount:     "0.50",
		AmountUSDC: 0.50,
		MaxTotal:   "100.00",
		TotalSpent: "10.00",
	}

	result := engine.Score(context.Background(), tx)
	if result.Score >= 0.3 {
		t.Errorf("normal transaction score too high: %f (factors: %v)", result.Score, result.Factors)
	}
	if result.Decision != DecisionAllow {
		t.Errorf("expected allow, got %s", result.Decision)
	}
}

func TestVelocitySpike(t *testing.T) {
	engine := NewEngine(NewMemoryStore())

	// Seed low-rate history: small amounts spread over 24h
	w := engine.getWindow("key1")
	w.mu.Lock()
	for i := 0; i < 50; i++ {
		w.entries = append(w.entries, windowEntry{
			To:        "0xseller",
			AmountUSD: 0.01,
			Timestamp: time.Now().Add(-time.Duration(i) * 30 * time.Minute),
		})
	}
	w.mu.Unlock()

	// Now score a massive transaction (100x normal rate)
	tx := &TransactionContext{
		KeyID:      "key1",
		To:         "0xseller",
		Amount:     "50.00",
		AmountUSDC: 50.00,
		MaxTotal:   "1000.00",
		TotalSpent: "0.50",
	}

	result := engine.Score(context.Background(), tx)
	if result.Factors["velocity"] < 0.7 {
		t.Errorf("velocity factor too low for spike: %f", result.Factors["velocity"])
	}
}

func TestNovelRecipient(t *testing.T) {
	engine := NewEngine(NewMemoryStore())

	// Seed history with known recipients
	for i := 0; i < 10; i++ {
		engine.RecordTransaction("key1", "0xknown", "1.00")
	}

	tx := &TransactionContext{
		KeyID:      "key1",
		To:         "0xnever_seen_before",
		Amount:     "1.00",
		AmountUSDC: 1.00,
		MaxTotal:   "100.00",
		TotalSpent: "10.00",
	}

	result := engine.Score(context.Background(), tx)
	if result.Factors["novelty"] != 0.6 {
		t.Errorf("novel recipient factor should be 0.6, got %f", result.Factors["novelty"])
	}
}

func TestBurnRateNearExhaustion(t *testing.T) {
	engine := NewEngine(NewMemoryStore())

	// Seed recent spending (all within last hour)
	w := engine.getWindow("key1")
	w.mu.Lock()
	for i := 0; i < 10; i++ {
		w.entries = append(w.entries, windowEntry{
			To:        "0xseller",
			AmountUSD: 5.00,
			Timestamp: time.Now().Add(-time.Duration(i) * time.Minute),
		})
	}
	w.mu.Unlock()

	tx := &TransactionContext{
		KeyID:      "key1",
		To:         "0xseller",
		Amount:     "5.00",
		AmountUSDC: 5.00,
		MaxTotal:   "60.00",
		TotalSpent: "50.00",
	}

	result := engine.Score(context.Background(), tx)
	if result.Factors["burn_rate"] < 0.5 {
		t.Errorf("burn rate factor too low near exhaustion: %f", result.Factors["burn_rate"])
	}
}

func TestCombinedHighFactorsBlock(t *testing.T) {
	engine := NewEngine(NewMemoryStore())

	// Seed low-rate history spread over many hours and only in specific hours
	// to make the current hour unusual (time_of_day factor)
	w := engine.getWindow("key1")
	w.mu.Lock()
	// All historical txs at hour+6 (different from current hour)
	baseTime := time.Now().Add(-12 * time.Hour)
	for i := 0; i < 50; i++ {
		w.entries = append(w.entries, windowEntry{
			To:        "0xknown",
			AmountUSD: 0.01,
			Timestamp: baseTime.Add(-time.Duration(i) * 10 * time.Minute),
		})
	}
	w.mu.Unlock()

	// Novel recipient + velocity spike + near exhaustion + unusual hour
	tx := &TransactionContext{
		KeyID:      "key1",
		To:         "0xattacker",
		Amount:     "45.00",
		AmountUSDC: 45.00,
		MaxTotal:   "50.00",
		TotalSpent: "0.50",
	}

	result := engine.Score(context.Background(), tx)
	if result.Decision != DecisionBlock {
		t.Errorf("expected block for combined high-risk, got %s (score: %f, factors: %v)",
			result.Decision, result.Score, result.Factors)
	}
}

func TestNewKeyNoHistory(t *testing.T) {
	engine := NewEngine(NewMemoryStore())

	tx := &TransactionContext{
		KeyID:      "brand_new_key",
		To:         "0xseller",
		Amount:     "1.00",
		AmountUSDC: 1.00,
		MaxTotal:   "100.00",
		TotalSpent: "0.00",
	}

	result := engine.Score(context.Background(), tx)
	if result.Score > 0.01 {
		t.Errorf("new key should have near-zero score, got %f (factors: %v)", result.Score, result.Factors)
	}
	if result.Decision != DecisionAllow {
		t.Errorf("new key should be allowed, got %s", result.Decision)
	}
}

func TestRecordTransactionUpdatesWindow(t *testing.T) {
	engine := NewEngine(NewMemoryStore())

	engine.RecordTransaction("key1", "0xA", "1.00")
	engine.RecordTransaction("key1", "0xB", "2.00")
	engine.RecordTransaction("key1", "0xA", "3.00")

	w := engine.getWindow("key1")
	w.mu.Lock()
	count := len(w.entries)
	w.mu.Unlock()

	if count != 3 {
		t.Errorf("expected 3 entries, got %d", count)
	}
}

func TestWindowPrunesOldEntries(t *testing.T) {
	engine := NewEngine(NewMemoryStore())

	w := engine.getWindow("key1")
	w.mu.Lock()
	// Add old entries (25h ago)
	for i := 0; i < 5; i++ {
		w.entries = append(w.entries, windowEntry{
			To:        "0xold",
			AmountUSD: 1.00,
			Timestamp: time.Now().Add(-25 * time.Hour),
		})
	}
	w.mu.Unlock()

	// Add a fresh entry via RecordTransaction which triggers pruning
	engine.RecordTransaction("key1", "0xnew", "1.00")

	w.mu.Lock()
	count := len(w.entries)
	w.mu.Unlock()

	if count != 1 {
		t.Errorf("expected 1 entry after pruning, got %d", count)
	}
}

// --- New tests for warn threshold, custom thresholds, edge cases ---

func TestWarnDecision(t *testing.T) {
	engine := NewEngine(NewMemoryStore())

	// Seed 20 entries all 12h ago in a single hour (makes current hour unusual).
	// All to a known recipient, small amounts.
	w := engine.getWindow("key1")
	w.mu.Lock()
	baseTime := time.Now().Add(-12 * time.Hour)
	for i := 0; i < 20; i++ {
		w.entries = append(w.entries, windowEntry{
			To:        "0xknown",
			AmountUSD: 0.10,
			Timestamp: baseTime.Add(-time.Duration(i) * time.Minute),
		})
	}
	w.mu.Unlock()

	// Novel recipient + unusual hour + velocity spike + low burn concern
	// Expected: novelty=0.6(0.15), time_of_day=0.8(0.16), velocity~1.0(0.35), burn=0(0) → ~0.66
	tx := &TransactionContext{
		KeyID:      "key1",
		To:         "0xnever_seen",
		Amount:     "1.00",
		AmountUSDC: 1.00,
		MaxTotal:   "100.00",
		TotalSpent: "10.00",
	}

	result := engine.Score(context.Background(), tx)
	if result.Decision != DecisionWarn {
		t.Errorf("expected warn, got %s (score: %f, factors: %v)",
			result.Decision, result.Score, result.Factors)
	}
	if result.Score < 0.5 || result.Score >= 0.8 {
		t.Errorf("expected score in [0.5, 0.8), got %f", result.Score)
	}
}

func TestCustomBlockThreshold(t *testing.T) {
	engine := NewEngine(NewMemoryStore()).WithBlockThreshold(0.5)

	// Same scenario as TestWarnDecision — score ~0.66 — but block threshold is now 0.5
	w := engine.getWindow("key1")
	w.mu.Lock()
	baseTime := time.Now().Add(-12 * time.Hour)
	for i := 0; i < 20; i++ {
		w.entries = append(w.entries, windowEntry{
			To:        "0xknown",
			AmountUSD: 0.10,
			Timestamp: baseTime.Add(-time.Duration(i) * time.Minute),
		})
	}
	w.mu.Unlock()

	tx := &TransactionContext{
		KeyID:      "key1",
		To:         "0xnever_seen",
		Amount:     "1.00",
		AmountUSDC: 1.00,
		MaxTotal:   "100.00",
		TotalSpent: "10.00",
	}

	result := engine.Score(context.Background(), tx)
	if result.Decision != DecisionBlock {
		t.Errorf("expected block with lowered threshold 0.5, got %s (score: %f)",
			result.Decision, result.Score)
	}
}

func TestCustomWarnThreshold(t *testing.T) {
	// Raise warn threshold above the score so it becomes allow instead of warn
	engine := NewEngine(NewMemoryStore()).WithWarnThreshold(0.9)

	// Same scenario — score ~0.66 — but warn threshold is now 0.9
	w := engine.getWindow("key1")
	w.mu.Lock()
	baseTime := time.Now().Add(-12 * time.Hour)
	for i := 0; i < 20; i++ {
		w.entries = append(w.entries, windowEntry{
			To:        "0xknown",
			AmountUSD: 0.10,
			Timestamp: baseTime.Add(-time.Duration(i) * time.Minute),
		})
	}
	w.mu.Unlock()

	tx := &TransactionContext{
		KeyID:      "key1",
		To:         "0xnever_seen",
		Amount:     "1.00",
		AmountUSDC: 1.00,
		MaxTotal:   "100.00",
		TotalSpent: "10.00",
	}

	result := engine.Score(context.Background(), tx)
	if result.Decision != DecisionAllow {
		t.Errorf("expected allow with raised warn threshold 0.9, got %s (score: %f)",
			result.Decision, result.Score)
	}
}

func TestScoreBounds(t *testing.T) {
	engine := NewEngine(NewMemoryStore())

	// All factors at maximum: velocity=1.0, novelty=0.6, time_of_day=0.8, burn_rate=1.0
	// Max possible score = 0.35 + 0.15 + 0.16 + 0.20 = 0.86 (within [0,1])
	w := engine.getWindow("key1")
	w.mu.Lock()
	baseTime := time.Now().Add(-12 * time.Hour)
	for i := 0; i < 50; i++ {
		w.entries = append(w.entries, windowEntry{
			To:        "0xknown",
			AmountUSD: 0.01,
			Timestamp: baseTime.Add(-time.Duration(i) * 10 * time.Minute),
		})
	}
	w.mu.Unlock()

	tx := &TransactionContext{
		KeyID:      "key1",
		To:         "0xnever_seen",
		Amount:     "45.00",
		AmountUSDC: 45.00,
		MaxTotal:   "50.00",
		TotalSpent: "4.50",
	}

	result := engine.Score(context.Background(), tx)
	if result.Score < 0.0 || result.Score > 1.0 {
		t.Errorf("score out of bounds: %f", result.Score)
	}

	// Also test a brand-new key (all factors zero) stays at 0
	tx2 := &TransactionContext{
		KeyID:      "brand_new",
		To:         "0xseller",
		Amount:     "1.00",
		AmountUSDC: 1.00,
		MaxTotal:   "1000.00",
		TotalSpent: "0.00",
	}
	result2 := engine.Score(context.Background(), tx2)
	if result2.Score < 0.0 || result2.Score > 1.0 {
		t.Errorf("score out of bounds for new key: %f", result2.Score)
	}
}

func TestNilStore(t *testing.T) {
	// Engine with nil store should not panic
	engine := NewEngine(nil)

	tx := &TransactionContext{
		KeyID:      "key1",
		To:         "0xseller",
		Amount:     "1.00",
		AmountUSDC: 1.00,
		MaxTotal:   "100.00",
		TotalSpent: "0.00",
	}

	result := engine.Score(context.Background(), tx)
	if result == nil {
		t.Fatal("Score returned nil with nil store")
	}
	if result.Decision != DecisionAllow {
		t.Errorf("expected allow for nil store, got %s", result.Decision)
	}

	// RecordTransaction should also not panic
	engine.RecordTransaction("key1", "0xseller", "1.00")
}

func TestWindowCapAt1000(t *testing.T) {
	engine := NewEngine(NewMemoryStore())

	w := engine.getWindow("key1")
	w.mu.Lock()
	// Add 1100 recent entries (all within 24h)
	for i := 0; i < 1100; i++ {
		w.entries = append(w.entries, windowEntry{
			To:        "0xseller",
			AmountUSD: 0.01,
			Timestamp: time.Now().Add(-time.Duration(i) * time.Second),
		})
	}
	w.mu.Unlock()

	// RecordTransaction triggers pruneWindow
	engine.RecordTransaction("key1", "0xseller", "0.01")

	w.mu.Lock()
	count := len(w.entries)
	w.mu.Unlock()

	if count > maxWindowSize {
		t.Errorf("window exceeds max size: got %d, want <= %d", count, maxWindowSize)
	}
}

func TestRecordTransactionInvalidAmount(t *testing.T) {
	engine := NewEngine(NewMemoryStore())

	// Should not panic; ParseFloat returns 0 on error
	engine.RecordTransaction("key1", "0xseller", "not_a_number")

	w := engine.getWindow("key1")
	w.mu.Lock()
	defer w.mu.Unlock()

	if len(w.entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(w.entries))
	}
	if w.entries[0].AmountUSD != 0 {
		t.Errorf("expected amount 0 for invalid string, got %f", w.entries[0].AmountUSD)
	}
}

func TestVelocityFactorEdgeCases(t *testing.T) {
	engine := NewEngine(NewMemoryStore())

	// 0 entries → velocity should be 0 (< 2 entries)
	result0 := engine.velocityFactor(nil, 1.0)
	if result0 != 0.0 {
		t.Errorf("velocity with 0 entries = %f, want 0.0", result0)
	}

	// 1 entry → still < 2, should be 0
	oneEntry := []windowEntry{
		{To: "0x1", AmountUSD: 1.0, Timestamp: time.Now().Add(-time.Hour)},
	}
	result1 := engine.velocityFactor(oneEntry, 1.0)
	if result1 != 0.0 {
		t.Errorf("velocity with 1 entry = %f, want 0.0", result1)
	}

	// 2 entries: sufficient history, ratio <= 1 → 0.0
	twoEntries := []windowEntry{
		{To: "0x1", AmountUSD: 100.0, Timestamp: time.Now().Add(-time.Hour)},
		{To: "0x1", AmountUSD: 100.0, Timestamp: time.Now().Add(-2 * time.Hour)},
	}
	// avg5minRate = 200/288 = 0.694. spent5min = 0 + 0.01 = 0.01. ratio = 0.014 < 1 → 0.0
	result2 := engine.velocityFactor(twoEntries, 0.01)
	if result2 != 0.0 {
		t.Errorf("velocity with low current amount = %f, want 0.0", result2)
	}
}

func TestBurnRateFactorEdgeCases(t *testing.T) {
	engine := NewEngine(NewMemoryStore())

	// Empty MaxTotal → 0.0
	tx1 := &TransactionContext{
		KeyID: "key1", MaxTotal: "", TotalSpent: "0", AmountUSDC: 1.0,
	}
	result1 := engine.burnRateFactor(nil, tx1)
	if result1 != 0.0 {
		t.Errorf("burn rate with empty MaxTotal = %f, want 0.0", result1)
	}

	// Remaining <= 0 → 1.0
	tx2 := &TransactionContext{
		KeyID: "key1", MaxTotal: "10.00", TotalSpent: "9.00", AmountUSDC: 2.0,
	}
	result2 := engine.burnRateFactor(nil, tx2)
	if result2 != 1.0 {
		t.Errorf("burn rate with remaining <= 0 = %f, want 1.0", result2)
	}

	// No recent spending (entries all old) and current amount small → 0.0
	oldEntries := []windowEntry{
		{To: "0x1", AmountUSD: 1.0, Timestamp: time.Now().Add(-2 * time.Hour)},
	}
	tx3 := &TransactionContext{
		KeyID: "key1", MaxTotal: "100.00", TotalSpent: "10.00", AmountUSDC: 0.0,
	}
	// spentLastHour = 0 (old entries) + 0.0 (current) = 0, so spentLastHour <= 0 → 0.0
	result3 := engine.burnRateFactor(oldEntries, tx3)
	if result3 != 0.0 {
		t.Errorf("burn rate with zero recent spend = %f, want 0.0", result3)
	}

	// Invalid MaxTotal string → 0.0
	tx4 := &TransactionContext{
		KeyID: "key1", MaxTotal: "invalid", TotalSpent: "0", AmountUSDC: 1.0,
	}
	result4 := engine.burnRateFactor(nil, tx4)
	if result4 != 0.0 {
		t.Errorf("burn rate with invalid MaxTotal = %f, want 0.0", result4)
	}
}
