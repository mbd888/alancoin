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
