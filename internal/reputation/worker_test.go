package reputation

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"
)

// mockProvider implements MetricsProvider for testing.
type mockProvider struct {
	agents map[string]*Metrics
}

func (m *mockProvider) GetAgentMetrics(_ context.Context, address string) (*Metrics, error) {
	if metrics, ok := m.agents[address]; ok {
		return metrics, nil
	}
	return nil, nil
}

func (m *mockProvider) GetAllAgentMetrics(_ context.Context) (map[string]*Metrics, error) {
	return m.agents, nil
}

func TestWorkerSnapshot(t *testing.T) {
	provider := &mockProvider{
		agents: map[string]*Metrics{
			"0xaaa": {
				TotalTransactions:    50,
				TotalVolumeUSD:       1000,
				SuccessfulTxns:       48,
				FailedTxns:           2,
				UniqueCounterparties: 10,
				DaysOnNetwork:        30,
				FirstSeen:            time.Now().AddDate(0, -1, 0),
				LastActive:           time.Now(),
			},
			"0xbbb": {
				TotalTransactions:    5,
				TotalVolumeUSD:       50,
				SuccessfulTxns:       5,
				UniqueCounterparties: 2,
				DaysOnNetwork:        7,
				FirstSeen:            time.Now().AddDate(0, 0, -7),
				LastActive:           time.Now(),
			},
		},
	}

	store := NewMemorySnapshotStore()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	worker := NewWorker(provider, store, 100*time.Millisecond, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 350*time.Millisecond)
	defer cancel()

	go worker.Start(ctx)

	// Wait for at least one snapshot cycle
	time.Sleep(200 * time.Millisecond)

	// Check snapshots were created
	snapsA, err := store.Query(context.Background(), HistoryQuery{Address: "0xaaa", Limit: 10})
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if len(snapsA) == 0 {
		t.Fatal("expected snapshots for 0xaaa")
	}
	if snapsA[0].Score <= 0 {
		t.Errorf("expected positive score, got %f", snapsA[0].Score)
	}
	if snapsA[0].TotalTxns != 50 {
		t.Errorf("expected 50 txns, got %d", snapsA[0].TotalTxns)
	}

	snapsB, err := store.Query(context.Background(), HistoryQuery{Address: "0xbbb", Limit: 10})
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if len(snapsB) == 0 {
		t.Fatal("expected snapshots for 0xbbb")
	}

	// Higher activity should yield higher score
	if snapsA[0].Score <= snapsB[0].Score {
		t.Errorf("expected 0xaaa score (%f) > 0xbbb score (%f)", snapsA[0].Score, snapsB[0].Score)
	}

	cancel()
	worker.Stop()
}

func TestWorkerEmptyNetwork(t *testing.T) {
	provider := &mockProvider{agents: map[string]*Metrics{}}
	store := NewMemorySnapshotStore()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	worker := NewWorker(provider, store, 100*time.Millisecond, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	go worker.Start(ctx)
	time.Sleep(150 * time.Millisecond)

	// No agents means no snapshots, but no crash either
	snaps, err := store.Query(context.Background(), HistoryQuery{Address: "0xnone", Limit: 10})
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if len(snaps) != 0 {
		t.Errorf("expected no snapshots, got %d", len(snaps))
	}

	cancel()
	worker.Stop()
}

func TestMemorySnapshotStoreLatest(t *testing.T) {
	store := NewMemorySnapshotStore()
	ctx := context.Background()

	// Save two snapshots for the same address
	snap1 := &Snapshot{Address: "0xaaa", Score: 50.0, Tier: TierEstablished, CreatedAt: time.Now().Add(-time.Hour)}
	snap2 := &Snapshot{Address: "0xaaa", Score: 60.0, Tier: TierTrusted, CreatedAt: time.Now()}

	_ = store.Save(ctx, snap1)
	_ = store.Save(ctx, snap2)

	latest, err := store.Latest(ctx, "0xaaa")
	if err != nil {
		t.Fatalf("latest failed: %v", err)
	}
	if latest == nil {
		t.Fatal("expected non-nil latest")
	}
	if latest.Score != 60.0 {
		t.Errorf("expected score 60.0, got %f", latest.Score)
	}
}

func TestSignerRoundTrip(t *testing.T) {
	signer := NewSigner("test-secret")
	if signer == nil {
		t.Fatal("signer should not be nil")
	}

	payload := map[string]interface{}{"score": 72.5, "address": "0xaaa"}
	sig, issuedAt, expiresAt, err := signer.Sign(payload)
	if err != nil {
		t.Fatalf("sign failed: %v", err)
	}
	if sig == "" || issuedAt == "" || expiresAt == "" {
		t.Fatal("expected non-empty signature fields")
	}

	if !signer.Verify(payload, sig) {
		t.Error("expected signature to verify")
	}

	// Tampered payload should fail
	payload["score"] = 99.9
	if signer.Verify(payload, sig) {
		t.Error("expected tampered payload to fail verification")
	}
}

func TestNilSigner(t *testing.T) {
	signer := NewSigner("")
	if signer != nil {
		t.Fatal("expected nil signer for empty secret")
	}
}
