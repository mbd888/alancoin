package reputation

import (
	"context"
	"io"
	"log/slog"
	"testing"
)

func noopTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestRealtimeUpdater_OnTransactionConfirmed(t *testing.T) {
	store := NewMemorySnapshotStore()
	provider := &rtMockProvider{
		metrics: map[string]*Metrics{
			"0xseller": {
				TotalTransactions:    10,
				TotalVolumeUSD:       100.0,
				SuccessfulTxns:       9,
				FailedTxns:           1,
				UniqueCounterparties: 3,
				DaysOnNetwork:        30,
			},
		},
	}

	updater := NewRealtimeUpdater(provider, store, nil)
	// Set a noop logger to avoid nil panics
	updater.logger = noopTestLogger()

	ctx := context.Background()

	// Trigger a transaction confirmation
	updater.OnTransactionConfirmed(ctx, "0xbuyer", "0xseller", "5.000000")

	// Check delta was recorded
	updater.mu.RLock()
	sellerDelta := updater.deltaCache["0xseller"]
	buyerDelta := updater.deltaCache["0xbuyer"]
	updater.mu.RUnlock()

	if sellerDelta == nil {
		t.Fatal("expected seller delta")
	}
	if sellerDelta.txnsDelta != 1 {
		t.Fatalf("expected 1 txn delta, got %d", sellerDelta.txnsDelta)
	}
	if sellerDelta.successDelta != 1 {
		t.Fatalf("expected 1 success delta, got %d", sellerDelta.successDelta)
	}
	if sellerDelta.volumeDelta != 5.0 {
		t.Fatalf("expected 5.0 volume delta, got %f", sellerDelta.volumeDelta)
	}
	if buyerDelta == nil {
		t.Fatal("expected buyer delta")
	}
}

func TestRealtimeUpdater_OnTransactionFailed(t *testing.T) {
	store := NewMemorySnapshotStore()
	provider := &rtMockProvider{metrics: map[string]*Metrics{}}
	updater := NewRealtimeUpdater(provider, store, noopTestLogger())

	ctx := context.Background()
	updater.OnTransactionFailed(ctx, "0xseller", "1.000000")

	updater.mu.RLock()
	delta := updater.deltaCache["0xseller"]
	updater.mu.RUnlock()

	if delta == nil {
		t.Fatal("expected delta")
	}
	if delta.failedDelta != 1 {
		t.Fatalf("expected 1 failed delta, got %d", delta.failedDelta)
	}
}

func TestRealtimeUpdater_OnDisputeResolved(t *testing.T) {
	store := NewMemorySnapshotStore()
	provider := &rtMockProvider{metrics: map[string]*Metrics{}}
	updater := NewRealtimeUpdater(provider, store, noopTestLogger())

	ctx := context.Background()

	// Buyer wins dispute
	updater.OnDisputeResolved(ctx, "0xseller", "0xbuyer", true)

	updater.mu.RLock()
	sellerDelta := updater.deltaCache["0xseller"]
	updater.mu.RUnlock()

	if sellerDelta == nil {
		t.Fatal("expected seller delta")
	}
	if sellerDelta.disputedDelta != 1 {
		t.Fatalf("expected 1 disputed delta, got %d", sellerDelta.disputedDelta)
	}
}

func TestRealtimeUpdater_ClearDeltas(t *testing.T) {
	store := NewMemorySnapshotStore()
	provider := &rtMockProvider{metrics: map[string]*Metrics{}}
	updater := NewRealtimeUpdater(provider, store, noopTestLogger())

	ctx := context.Background()
	updater.OnTransactionConfirmed(ctx, "0xbuyer", "0xseller", "1.000000")

	updater.mu.RLock()
	before := len(updater.deltaCache)
	updater.mu.RUnlock()

	if before == 0 {
		t.Fatal("expected deltas before clear")
	}

	updater.ClearDeltas()

	updater.mu.RLock()
	after := len(updater.deltaCache)
	updater.mu.RUnlock()

	if after != 0 {
		t.Fatalf("expected 0 deltas after clear, got %d", after)
	}
}

func TestParseFloat(t *testing.T) {
	tests := []struct {
		input    string
		expected float64
	}{
		{"5.000000", 5.0},
		{"0.001000", 0.001},
		{"100.500000", 100.5},
		{"0.000000", 0.0},
	}
	for _, tt := range tests {
		got := parseFloat(tt.input)
		diff := got - tt.expected
		if diff < 0 {
			diff = -diff
		}
		if diff > 0.0001 {
			t.Errorf("parseFloat(%q) = %f, want %f", tt.input, got, tt.expected)
		}
	}
}

// rtMockProvider implements MetricsProvider for testing.
type rtMockProvider struct {
	metrics map[string]*Metrics
}

func (m *rtMockProvider) GetAgentMetrics(_ context.Context, addr string) (*Metrics, error) {
	if met, ok := m.metrics[addr]; ok {
		return met, nil
	}
	return &Metrics{}, nil
}

func (m *rtMockProvider) GetAllAgentMetrics(_ context.Context) (map[string]*Metrics, error) {
	return m.metrics, nil
}
