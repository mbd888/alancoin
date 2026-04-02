//go:build integration

package workflows

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

func makeWorkflow(id, owner string) *Workflow {
	now := time.Now().Truncate(time.Microsecond)
	return &Workflow{
		ID:           id,
		OwnerAddr:    owner,
		Name:         "test-workflow",
		Description:  "integration test",
		BudgetTotal:  "100.000000",
		BudgetSpent:  "0.000000",
		BudgetRemain: "100.000000",
		Steps: []WorkflowStep{
			{ID: "step_1", ServiceType: "translation", Status: StepPending, BudgetCap: "50.000000"},
		},
		Status:    WSActive,
		EscrowRef: "esc_wf_1",
		AuditTrail: []AuditEntry{
			{Action: "created", Actor: owner, Hash: "abc123", Timestamp: now},
		},
		StepsTotal:     1,
		StepsDone:      0,
		MaxCostPerStep: "50.000000",
		MaxVelocity:    2.5,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
}

func TestPostgres_CreateAndGet(t *testing.T) {
	store, cleanup := setupPgStore(t)
	defer cleanup()
	ctx := context.Background()

	w := makeWorkflow("wf_pg_001", "0xowner1")
	if err := store.Create(ctx, w); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := store.Get(ctx, "wf_pg_001")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.ID != w.ID {
		t.Errorf("ID: want %s, got %s", w.ID, got.ID)
	}
	if got.OwnerAddr != "0xowner1" {
		t.Errorf("OwnerAddr: want 0xowner1, got %s", got.OwnerAddr)
	}
	if got.BudgetTotal != "100.000000" {
		t.Errorf("BudgetTotal: want 100.000000, got %s", got.BudgetTotal)
	}
	if got.Status != WSActive {
		t.Errorf("Status: want active, got %s", got.Status)
	}
	if len(got.Steps) != 1 {
		t.Errorf("Steps: want 1, got %d", len(got.Steps))
	}
	if len(got.AuditTrail) != 1 {
		t.Errorf("AuditTrail: want 1, got %d", len(got.AuditTrail))
	}
	if got.MaxCostPerStep != "50.000000" {
		t.Errorf("MaxCostPerStep: want 50.000000, got %s", got.MaxCostPerStep)
	}
	if got.MaxVelocity != 2.5 {
		t.Errorf("MaxVelocity: want 2.5, got %f", got.MaxVelocity)
	}
	if got.ClosedAt != nil {
		t.Errorf("ClosedAt: want nil, got %v", got.ClosedAt)
	}
}

func TestPostgres_Get_NotFound(t *testing.T) {
	store, cleanup := setupPgStore(t)
	defer cleanup()

	_, err := store.Get(context.Background(), "nonexistent")
	if err != ErrWorkflowNotFound {
		t.Errorf("want ErrWorkflowNotFound, got %v", err)
	}
}

func TestPostgres_Update(t *testing.T) {
	store, cleanup := setupPgStore(t)
	defer cleanup()
	ctx := context.Background()

	w := makeWorkflow("wf_pg_002", "0xowner2")
	store.Create(ctx, w)

	// Update progress
	w.BudgetSpent = "30.500000"
	w.BudgetRemain = "69.500000"
	w.StepsDone = 1
	w.Status = WSCompleted
	closedAt := time.Now().Truncate(time.Microsecond)
	w.ClosedAt = &closedAt
	w.AuditTrail = append(w.AuditTrail, AuditEntry{Action: "step_completed", Actor: "0xworker"})
	w.UpdatedAt = time.Now().Truncate(time.Microsecond)

	if err := store.Update(ctx, w); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, _ := store.Get(ctx, w.ID)
	if got.BudgetSpent != "30.500000" {
		t.Errorf("BudgetSpent: want 30.500000, got %s", got.BudgetSpent)
	}
	if got.StepsDone != 1 {
		t.Errorf("StepsDone: want 1, got %d", got.StepsDone)
	}
	if got.Status != WSCompleted {
		t.Errorf("Status: want completed, got %s", got.Status)
	}
	if got.ClosedAt == nil {
		t.Error("ClosedAt: want non-nil")
	}
	if len(got.AuditTrail) != 2 {
		t.Errorf("AuditTrail: want 2, got %d", len(got.AuditTrail))
	}
}

func TestPostgres_Update_NotFound(t *testing.T) {
	store, cleanup := setupPgStore(t)
	defer cleanup()

	w := makeWorkflow("nonexistent", "0x1")
	err := store.Update(context.Background(), w)
	if err != ErrWorkflowNotFound {
		t.Errorf("want ErrWorkflowNotFound, got %v", err)
	}
}

func TestPostgres_ListByOwner_Ordering(t *testing.T) {
	store, cleanup := setupPgStore(t)
	defer cleanup()
	ctx := context.Background()

	owner := "0xowner_list"
	for i, offset := range []time.Duration{-3 * time.Minute, -2 * time.Minute, -1 * time.Minute} {
		w := makeWorkflow("wf_list_"+string(rune('a'+i)), owner)
		w.CreatedAt = time.Now().Add(offset).Truncate(time.Microsecond)
		w.UpdatedAt = w.CreatedAt
		store.Create(ctx, w)
	}

	got, err := store.ListByOwner(ctx, owner, 10)
	if err != nil {
		t.Fatalf("ListByOwner: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3, got %d", len(got))
	}
	// Should be newest first
	if got[0].ID != "wf_list_c" {
		t.Errorf("first: want wf_list_c (newest), got %s", got[0].ID)
	}
	if got[2].ID != "wf_list_a" {
		t.Errorf("last: want wf_list_a (oldest), got %s", got[2].ID)
	}
}

func TestPostgres_ListByOwner_Limit(t *testing.T) {
	store, cleanup := setupPgStore(t)
	defer cleanup()
	ctx := context.Background()

	owner := "0xowner_limit"
	for i := 0; i < 5; i++ {
		w := makeWorkflow("wf_lim_"+string(rune('0'+i)), owner)
		w.CreatedAt = time.Now().Add(time.Duration(-i) * time.Minute).Truncate(time.Microsecond)
		w.UpdatedAt = w.CreatedAt
		store.Create(ctx, w)
	}

	got, err := store.ListByOwner(ctx, owner, 2)
	if err != nil {
		t.Fatalf("ListByOwner: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("want 2, got %d", len(got))
	}
}

func TestPostgres_ListByOwner_Isolation(t *testing.T) {
	store, cleanup := setupPgStore(t)
	defer cleanup()
	ctx := context.Background()

	w1 := makeWorkflow("wf_iso_a", "0xownerA")
	w2 := makeWorkflow("wf_iso_b", "0xownerB")
	store.Create(ctx, w1)
	store.Create(ctx, w2)

	got, _ := store.ListByOwner(ctx, "0xownerA", 10)
	if len(got) != 1 || got[0].ID != "wf_iso_a" {
		t.Errorf("want only wf_iso_a, got %v", got)
	}
}

func TestPostgres_ListByOwner_Empty(t *testing.T) {
	store, cleanup := setupPgStore(t)
	defer cleanup()

	got, err := store.ListByOwner(context.Background(), "0xnobody", 10)
	if err != nil {
		t.Fatalf("ListByOwner: %v", err)
	}
	if got != nil && len(got) != 0 {
		t.Errorf("want empty slice, got %d items", len(got))
	}
}

func TestPostgres_NumericPrecision(t *testing.T) {
	store, cleanup := setupPgStore(t)
	defer cleanup()
	ctx := context.Background()

	w := makeWorkflow("wf_prec", "0xprec")
	w.BudgetTotal = "99999999999999.999999"
	w.BudgetSpent = "0.000001"
	w.BudgetRemain = "99999999999999.999998"
	store.Create(ctx, w)

	got, err := store.Get(ctx, w.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.BudgetTotal != "99999999999999.999999" {
		t.Errorf("BudgetTotal precision lost: want 99999999999999.999999, got %s", got.BudgetTotal)
	}
	if got.BudgetSpent != "0.000001" {
		t.Errorf("BudgetSpent precision lost: want 0.000001, got %s", got.BudgetSpent)
	}
}

func TestPostgres_Create_NilOptionalFields(t *testing.T) {
	store, cleanup := setupPgStore(t)
	defer cleanup()
	ctx := context.Background()

	w := makeWorkflow("wf_nil", "0xnil")
	w.MaxCostPerStep = "" // maps to NULL
	w.MaxVelocity = 0     // maps to NULL
	w.ClosedAt = nil      // maps to NULL
	store.Create(ctx, w)

	got, err := store.Get(ctx, w.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.MaxCostPerStep != "" {
		t.Errorf("MaxCostPerStep: want empty, got %q", got.MaxCostPerStep)
	}
	if got.MaxVelocity != 0 {
		t.Errorf("MaxVelocity: want 0, got %f", got.MaxVelocity)
	}
	if got.ClosedAt != nil {
		t.Errorf("ClosedAt: want nil, got %v", got.ClosedAt)
	}
}
