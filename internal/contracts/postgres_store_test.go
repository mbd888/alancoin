//go:build integration

package contracts

import (
	"context"
	"testing"
	"time"

	"github.com/mbd888/alancoin/internal/testutil"
)

func setupPgStore(t *testing.T) (*PostgresStore, func()) {
	t.Helper()
	db, cleanup := testutil.PGTest(t)
	return NewPostgresStore(db), cleanup
}

func makeContract(id string) *Contract {
	now := time.Now().Truncate(time.Microsecond)
	return &Contract{
		ID:          id,
		Name:        "test-contract",
		Description: "integration test",
		Preconditions: []Condition{
			{Type: CondMaxTotalCost, Severity: SeveritySoft, Params: map[string]interface{}{"limit": 100.0}},
		},
		Invariants: []Condition{
			{Type: CondMaxLatency, Severity: SeverityHard, Params: map[string]interface{}{"ms": 5000.0}},
		},
		Recovery:       RecoveryAbort,
		Status:         StatusDraft,
		Violations:     nil,
		SoftViolations: 0,
		HardViolations: 0,
		QualityPenalty: 0,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
}

func TestPostgres_CreateAndGet(t *testing.T) {
	store, cleanup := setupPgStore(t)
	defer cleanup()
	ctx := context.Background()

	c := makeContract("ctr_pg_001")
	if err := store.Create(ctx, c); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := store.Get(ctx, "ctr_pg_001")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.ID != c.ID {
		t.Errorf("ID: want %s, got %s", c.ID, got.ID)
	}
	if got.Name != c.Name {
		t.Errorf("Name: want %s, got %s", c.Name, got.Name)
	}
	if got.Status != StatusDraft {
		t.Errorf("Status: want draft, got %s", got.Status)
	}
	if got.Recovery != RecoveryAbort {
		t.Errorf("Recovery: want abort, got %s", got.Recovery)
	}
	if len(got.Preconditions) != 1 {
		t.Errorf("Preconditions: want 1, got %d", len(got.Preconditions))
	}
	if len(got.Invariants) != 1 {
		t.Errorf("Invariants: want 1, got %d", len(got.Invariants))
	}
	if got.BoundEscrowID != "" {
		t.Errorf("BoundEscrowID: want empty, got %q", got.BoundEscrowID)
	}
}

func TestPostgres_Get_NotFound(t *testing.T) {
	store, cleanup := setupPgStore(t)
	defer cleanup()

	_, err := store.Get(context.Background(), "nonexistent")
	if err != ErrContractNotFound {
		t.Errorf("want ErrContractNotFound, got %v", err)
	}
}

func TestPostgres_Update_FullLifecycle(t *testing.T) {
	store, cleanup := setupPgStore(t)
	defer cleanup()
	ctx := context.Background()

	c := makeContract("ctr_pg_002")
	store.Create(ctx, c)

	// Activate: bind to escrow
	c.Status = StatusActive
	c.BoundEscrowID = "esc_integration_1"
	c.UpdatedAt = time.Now().Truncate(time.Microsecond)
	if err := store.Update(ctx, c); err != nil {
		t.Fatalf("Update (activate): %v", err)
	}

	got, _ := store.Get(ctx, c.ID)
	if got.Status != StatusActive {
		t.Errorf("Status: want active, got %s", got.Status)
	}
	if got.BoundEscrowID != "esc_integration_1" {
		t.Errorf("BoundEscrowID: want esc_integration_1, got %s", got.BoundEscrowID)
	}

	// Add violations
	c.Violations = []Violation{
		{ConditionType: CondMaxLatency, Severity: SeveritySoft, Message: "latency exceeded", OccurredAt: time.Now()},
	}
	c.SoftViolations = 1
	c.QualityPenalty = 0.15
	c.UpdatedAt = time.Now().Truncate(time.Microsecond)
	if err := store.Update(ctx, c); err != nil {
		t.Fatalf("Update (violations): %v", err)
	}

	got, _ = store.Get(ctx, c.ID)
	if got.SoftViolations != 1 {
		t.Errorf("SoftViolations: want 1, got %d", got.SoftViolations)
	}
	if got.QualityPenalty != 0.15 {
		t.Errorf("QualityPenalty: want 0.15, got %f", got.QualityPenalty)
	}
	if len(got.Violations) != 1 {
		t.Errorf("Violations: want 1, got %d", len(got.Violations))
	}
}

func TestPostgres_Update_NotFound(t *testing.T) {
	store, cleanup := setupPgStore(t)
	defer cleanup()

	c := makeContract("nonexistent")
	err := store.Update(context.Background(), c)
	if err != ErrContractNotFound {
		t.Errorf("want ErrContractNotFound, got %v", err)
	}
}

func TestPostgres_GetByEscrow(t *testing.T) {
	store, cleanup := setupPgStore(t)
	defer cleanup()
	ctx := context.Background()

	c1 := makeContract("ctr_pg_003")
	c1.BoundEscrowID = "esc_target"
	c1.Status = StatusActive
	store.Create(ctx, c1)

	c2 := makeContract("ctr_pg_004")
	store.Create(ctx, c2)

	got, err := store.GetByEscrow(ctx, "esc_target")
	if err != nil {
		t.Fatalf("GetByEscrow: %v", err)
	}
	if got.ID != "ctr_pg_003" {
		t.Errorf("ID: want ctr_pg_003, got %s", got.ID)
	}
}

func TestPostgres_GetByEscrow_NotFound(t *testing.T) {
	store, cleanup := setupPgStore(t)
	defer cleanup()

	_, err := store.GetByEscrow(context.Background(), "nonexistent_escrow")
	if err != ErrContractNotFound {
		t.Errorf("want ErrContractNotFound, got %v", err)
	}
}

func TestPostgres_Create_DuplicateID(t *testing.T) {
	store, cleanup := setupPgStore(t)
	defer cleanup()
	ctx := context.Background()

	c := makeContract("ctr_pg_dup")
	if err := store.Create(ctx, c); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	if err := store.Create(ctx, c); err == nil {
		t.Error("second Create with same ID should fail")
	}
}

func TestPostgres_Create_NilSlices(t *testing.T) {
	store, cleanup := setupPgStore(t)
	defer cleanup()
	ctx := context.Background()

	c := makeContract("ctr_pg_nil")
	c.Preconditions = nil
	c.Invariants = nil
	c.Violations = nil
	store.Create(ctx, c)

	got, err := store.Get(ctx, c.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	// nil JSONB should unmarshal as empty slices (not nil)
	if got.Preconditions == nil {
		t.Error("Preconditions should be non-nil empty slice")
	}
	if got.Invariants == nil {
		t.Error("Invariants should be non-nil empty slice")
	}
}
