package sessionkeys

import (
	"context"
	"testing"
)

func TestKeyAnalytics_Basic(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	svc := NewAnalyticsService(store)

	key, err := mgr.Create(context.Background(), "0x1234567890abcdef1234567890abcdef12345678", &SessionKeyRequest{
		PublicKey: "0xabcdefabcdefabcdefabcdefabcdefabcdefabcd",
		MaxTotal:  "10.000000",
		ExpiresIn: "1h",
		AllowAny:  true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Record some usage
	_ = mgr.RecordUsage(context.Background(), key.ID, "2.500000", 1)
	_ = mgr.RecordUsage(context.Background(), key.ID, "1.500000", 2)

	ka, err := svc.GetKeyAnalytics(context.Background(), key.ID)
	if err != nil {
		t.Fatalf("analytics: %v", err)
	}

	if ka.TransactionCount != 2 {
		t.Errorf("expected 2 txs, got %d", ka.TransactionCount)
	}
	if ka.TotalSpent != "4.000000" {
		t.Errorf("expected totalSpent 4.000000, got %s", ka.TotalSpent)
	}
	if ka.AvgTransaction != "2.000000" {
		t.Errorf("expected avg 2.000000, got %s", ka.AvgTransaction)
	}
	if ka.BudgetUtilization < 39 || ka.BudgetUtilization > 41 {
		t.Errorf("expected ~40%% utilization, got %.2f%%", ka.BudgetUtilization)
	}
	if !ka.Active {
		t.Error("expected key to be active")
	}
}

func TestKeyAnalytics_ZeroTransactions(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	svc := NewAnalyticsService(store)

	key, err := mgr.Create(context.Background(), "0x1234567890abcdef1234567890abcdef12345678", &SessionKeyRequest{
		PublicKey: "0xabcdefabcdefabcdefabcdefabcdefabcdefabcd",
		MaxTotal:  "10.000000",
		ExpiresIn: "1h",
		AllowAny:  true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	ka, err := svc.GetKeyAnalytics(context.Background(), key.ID)
	if err != nil {
		t.Fatalf("analytics: %v", err)
	}

	if ka.TransactionCount != 0 {
		t.Errorf("expected 0 txs, got %d", ka.TransactionCount)
	}
	if ka.AvgTransaction != "0" {
		t.Errorf("expected avg 0, got %s", ka.AvgTransaction)
	}
	if ka.BudgetUtilization != 0 {
		t.Errorf("expected 0%% utilization, got %.2f%%", ka.BudgetUtilization)
	}
}

func TestKeyAnalytics_NoBudgetSet(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	svc := NewAnalyticsService(store)

	key, err := mgr.Create(context.Background(), "0x1234567890abcdef1234567890abcdef12345678", &SessionKeyRequest{
		PublicKey: "0xabcdefabcdefabcdefabcdefabcdefabcdefabcd",
		ExpiresIn: "1h",
		AllowAny:  true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	_ = mgr.RecordUsage(context.Background(), key.ID, "5.000000", 1)

	ka, err := svc.GetKeyAnalytics(context.Background(), key.ID)
	if err != nil {
		t.Fatalf("analytics: %v", err)
	}

	// No maxTotal → 0% utilization
	if ka.BudgetUtilization != 0 {
		t.Errorf("expected 0%% utilization when no budget set, got %.2f%%", ka.BudgetUtilization)
	}
}

func TestOwnerAnalytics(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	svc := NewAnalyticsService(store)

	owner := "0x1234567890abcdef1234567890abcdef12345678"

	key1, err := mgr.Create(context.Background(), owner, &SessionKeyRequest{
		PublicKey: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		MaxTotal:  "20.000000",
		ExpiresIn: "1h",
		AllowAny:  true,
	})
	if err != nil {
		t.Fatalf("create key1: %v", err)
	}

	key2, err := mgr.Create(context.Background(), owner, &SessionKeyRequest{
		PublicKey: "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		MaxTotal:  "30.000000",
		ExpiresIn: "1h",
		AllowAny:  true,
	})
	if err != nil {
		t.Fatalf("create key2: %v", err)
	}

	// Record usage
	_ = mgr.RecordUsage(context.Background(), key1.ID, "5.000000", 1)
	_ = mgr.RecordUsage(context.Background(), key1.ID, "3.000000", 2)
	_ = mgr.RecordUsage(context.Background(), key2.ID, "7.000000", 1)

	oa, err := svc.GetOwnerAnalytics(context.Background(), owner)
	if err != nil {
		t.Fatalf("analytics: %v", err)
	}

	if oa.TotalKeys != 2 {
		t.Errorf("expected 2 total keys, got %d", oa.TotalKeys)
	}
	if oa.ActiveKeys != 2 {
		t.Errorf("expected 2 active keys, got %d", oa.ActiveKeys)
	}
	if oa.TotalTransactions != 3 {
		t.Errorf("expected 3 total txs, got %d", oa.TotalTransactions)
	}
	if oa.TotalSpentAll != "15.000000" {
		t.Errorf("expected totalSpentAll 15.000000, got %s", oa.TotalSpentAll)
	}
	if len(oa.PerKey) != 2 {
		t.Errorf("expected 2 per-key entries, got %d", len(oa.PerKey))
	}
}

func TestOwnerAnalytics_Empty(t *testing.T) {
	store := NewMemoryStore()
	svc := NewAnalyticsService(store)

	oa, err := svc.GetOwnerAnalytics(context.Background(), "0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	if err != nil {
		t.Fatalf("analytics: %v", err)
	}

	if oa.TotalKeys != 0 {
		t.Errorf("expected 0 keys, got %d", oa.TotalKeys)
	}
	if oa.TotalSpentAll != "0.000000" {
		t.Errorf("expected totalSpentAll 0.000000, got %s", oa.TotalSpentAll)
	}
}

func TestKeyAnalytics_NotFound(t *testing.T) {
	store := NewMemoryStore()
	svc := NewAnalyticsService(store)

	_, err := svc.GetKeyAnalytics(context.Background(), "sk_nonexistent")
	if err != ErrKeyNotFound {
		t.Errorf("expected ErrKeyNotFound, got %v", err)
	}
}
