package sessionkeys

import (
	"context"
	"sync"
	"testing"
	"time"
)

type mockNotifier struct {
	mu     sync.Mutex
	events []AlertEvent
}

func (m *mockNotifier) NotifyAlert(_ context.Context, event AlertEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, event)
	return nil
}

func (m *mockNotifier) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.events)
}

func (m *mockNotifier) eventTypes() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	types := make([]string, len(m.events))
	for i, e := range m.events {
		types[i] = e.Type
	}
	return types
}

func TestAlertChecker_CheckBudget_NoNotifier(t *testing.T) {
	ac := &AlertChecker{thresholds: DefaultBudgetThresholds}
	key := &SessionKey{
		ID:         "key1",
		Permission: Permission{MaxTotal: "100.000000"},
		Usage:      SessionKeyUsage{TotalSpent: "90.000000"},
	}
	ac.CheckBudget(context.Background(), key)
}

func TestAlertChecker_CheckBudget_NoMaxTotal(t *testing.T) {
	n := &mockNotifier{}
	ac := NewAlertChecker(n)
	key := &SessionKey{
		ID:    "key1",
		Usage: SessionKeyUsage{TotalSpent: "50.000000"},
	}
	ac.CheckBudget(context.Background(), key)
	if n.count() != 0 {
		t.Fatalf("expected 0 alerts when MaxTotal is empty, got %d", n.count())
	}
}

func TestAlertChecker_CheckBudget_ZeroMaxTotal(t *testing.T) {
	n := &mockNotifier{}
	ac := NewAlertChecker(n)
	key := &SessionKey{
		ID:         "key1",
		Permission: Permission{MaxTotal: "0.000000"},
		Usage:      SessionKeyUsage{TotalSpent: "0.000000"},
	}
	ac.CheckBudget(context.Background(), key)
	if n.count() != 0 {
		t.Fatalf("expected 0 alerts for zero MaxTotal, got %d", n.count())
	}
}

func TestAlertChecker_CheckBudget_BelowAllThresholds(t *testing.T) {
	n := &mockNotifier{}
	ac := NewAlertChecker(n)
	key := &SessionKey{
		ID:         "key1",
		Permission: Permission{MaxTotal: "100.000000"},
		Usage:      SessionKeyUsage{TotalSpent: "10.000000"},
	}
	ac.CheckBudget(context.Background(), key)
	if n.count() != 0 {
		t.Fatalf("expected 0 alerts at 10%% usage, got %d", n.count())
	}
}

func TestAlertChecker_CheckBudget_CrossesThresholds(t *testing.T) {
	n := &mockNotifier{}
	ac := NewAlertChecker(n)

	key := &SessionKey{
		ID:         "key1",
		OwnerAddr:  "0xowner",
		Permission: Permission{MaxTotal: "100.000000"},
		Usage:      SessionKeyUsage{TotalSpent: "95.000000"},
	}
	ac.CheckBudget(context.Background(), key)

	if n.count() == 0 {
		t.Fatal("expected alerts at 95% usage")
	}
	for _, e := range n.events {
		if e.Type != "budget_warning" {
			t.Fatalf("expected type budget_warning, got %s", e.Type)
		}
		if e.KeyID != "key1" {
			t.Fatalf("expected keyId key1, got %s", e.KeyID)
		}
	}
}

func TestAlertChecker_CheckBudget_Idempotent(t *testing.T) {
	n := &mockNotifier{}
	ac := NewAlertChecker(n)

	key := &SessionKey{
		ID:         "key1",
		OwnerAddr:  "0xowner",
		Permission: Permission{MaxTotal: "100.000000"},
		Usage:      SessionKeyUsage{TotalSpent: "95.000000"},
	}

	ac.CheckBudget(context.Background(), key)
	first := n.count()

	ac.CheckBudget(context.Background(), key)
	second := n.count()

	if first != second {
		t.Fatalf("expected idempotent: first=%d, second=%d", first, second)
	}
}

func TestAlertChecker_CheckExpiration_NoNotifier(t *testing.T) {
	ac := &AlertChecker{thresholds: DefaultBudgetThresholds}
	key := &SessionKey{
		ID:         "key1",
		Permission: Permission{ExpiresAt: time.Now().Add(30 * time.Minute)},
	}
	ac.CheckExpiration(context.Background(), key)
}

func TestAlertChecker_CheckExpiration_AlreadyRevoked(t *testing.T) {
	n := &mockNotifier{}
	ac := NewAlertChecker(n)
	now := time.Now()
	key := &SessionKey{
		ID:         "key1",
		RevokedAt:  &now,
		Permission: Permission{ExpiresAt: time.Now().Add(30 * time.Minute)},
	}
	ac.CheckExpiration(context.Background(), key)
	if n.count() != 0 {
		t.Fatalf("expected 0 alerts for revoked key, got %d", n.count())
	}
}

func TestAlertChecker_CheckExpiration_AlreadyExpired(t *testing.T) {
	n := &mockNotifier{}
	ac := NewAlertChecker(n)
	key := &SessionKey{
		ID:         "key1",
		Permission: Permission{ExpiresAt: time.Now().Add(-1 * time.Hour)},
	}
	ac.CheckExpiration(context.Background(), key)
	if n.count() != 0 {
		t.Fatalf("expected 0 alerts for already-expired key, got %d", n.count())
	}
}

func TestAlertChecker_CheckExpiration_FarFuture(t *testing.T) {
	n := &mockNotifier{}
	ac := NewAlertChecker(n)
	key := &SessionKey{
		ID:         "key1",
		Permission: Permission{ExpiresAt: time.Now().Add(48 * time.Hour)},
	}
	ac.CheckExpiration(context.Background(), key)
	if n.count() != 0 {
		t.Fatalf("expected 0 alerts for key expiring in 48h, got %d", n.count())
	}
}

func TestAlertChecker_CheckExpiration_Within24h(t *testing.T) {
	n := &mockNotifier{}
	ac := NewAlertChecker(n)
	key := &SessionKey{
		ID:        "key1",
		OwnerAddr: "0xowner",
		Permission: Permission{
			ExpiresAt: time.Now().Add(12 * time.Hour),
		},
	}
	ac.CheckExpiration(context.Background(), key)

	if n.count() == 0 {
		t.Fatal("expected expiration alert within 24h window")
	}
	found := false
	for _, e := range n.events {
		if e.Type == "expiring" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected type 'expiring'")
	}
}

func TestAlertChecker_CheckExpiration_Within1h(t *testing.T) {
	n := &mockNotifier{}
	ac := NewAlertChecker(n)
	key := &SessionKey{
		ID:        "key2",
		OwnerAddr: "0xowner",
		Permission: Permission{
			ExpiresAt: time.Now().Add(30 * time.Minute),
		},
	}
	ac.CheckExpiration(context.Background(), key)

	if n.count() < 2 {
		t.Fatalf("expected 2 alerts (1h + 24h window), got %d", n.count())
	}
}

func TestAlertChecker_CheckExpiration_Idempotent(t *testing.T) {
	n := &mockNotifier{}
	ac := NewAlertChecker(n)
	key := &SessionKey{
		ID:        "key3",
		OwnerAddr: "0xowner",
		Permission: Permission{
			ExpiresAt: time.Now().Add(30 * time.Minute),
		},
	}

	ac.CheckExpiration(context.Background(), key)
	first := n.count()

	ac.CheckExpiration(context.Background(), key)
	second := n.count()

	if first != second {
		t.Fatalf("expected idempotent: first=%d, second=%d", first, second)
	}
}
