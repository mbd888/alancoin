package workflows

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"testing"
)

type mockLedger struct {
	mu        sync.Mutex
	locked    map[string]string
	released  map[string]string
	refunded  map[string]string
	refundErr error // inject error for RefundEscrow
}

func newMockLedger() *mockLedger {
	return &mockLedger{
		locked:   make(map[string]string),
		released: make(map[string]string),
		refunded: make(map[string]string),
	}
}

func (m *mockLedger) EscrowLock(_ context.Context, _, amount, reference string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.locked[reference] = amount
	return nil
}

func (m *mockLedger) ReleaseEscrow(_ context.Context, _, _, amount, reference string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.released[reference] = amount
	return nil
}

func (m *mockLedger) RefundEscrow(_ context.Context, _, amount, reference string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.refundErr != nil {
		return m.refundErr
	}
	m.refunded[reference] = amount
	return nil
}

func newTestService() (*Service, *mockLedger) {
	ml := newMockLedger()
	store := NewMemoryStore()
	svc := NewService(store, ml).WithLogger(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	return svc, ml
}

func defaultReq() CreateWorkflowRequest {
	return CreateWorkflowRequest{
		Name:        "claims-pipeline",
		BudgetTotal: "10.000000",
		Steps: []StepDefinition{
			{Name: "intake", AgentAddr: "0xIntake", MaxCost: "1.000000"},
			{Name: "assess", AgentAddr: "0xAssess", MaxCost: "3.000000"},
			{Name: "fraud", AgentAddr: "0xFraud", MaxCost: "5.000000"},
			{Name: "settle", AgentAddr: "0xSettle", MaxCost: "1.000000"},
		},
	}
}

func TestWorkflow_CreateAndGet(t *testing.T) {
	svc, ml := newTestService()
	ctx := context.Background()

	wf, err := svc.Create(ctx, "0xOwner", defaultReq())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if wf.Status != WSActive {
		t.Fatalf("expected active, got %s", wf.Status)
	}
	if wf.StepsTotal != 4 {
		t.Fatalf("expected 4 steps, got %d", wf.StepsTotal)
	}
	if wf.BudgetRemain != "10.000000" {
		t.Fatalf("expected 10.000000 remaining, got %s", wf.BudgetRemain)
	}
	if ml.locked[wf.EscrowRef] != "10.000000" {
		t.Fatalf("expected 10.000000 locked, got %s", ml.locked[wf.EscrowRef])
	}
	// Audit trail should have genesis entry
	if len(wf.AuditTrail) != 1 {
		t.Fatalf("expected 1 audit entry, got %d", len(wf.AuditTrail))
	}
	if wf.AuditTrail[0].Action != "workflow_created" {
		t.Fatalf("expected workflow_created, got %s", wf.AuditTrail[0].Action)
	}
	if wf.AuditTrail[0].Hash == "" {
		t.Fatal("audit entry should have hash")
	}

	got, _ := svc.Get(ctx, wf.ID)
	if got.ID != wf.ID {
		t.Fatal("Get returned wrong workflow")
	}
}

func TestWorkflow_FullLifecycle(t *testing.T) {
	svc, ml := newTestService()
	ctx := context.Background()

	wf, _ := svc.Create(ctx, "0xOwner", defaultReq())

	// Start and complete each step
	steps := []struct {
		name string
		cost string
	}{
		{"intake", "0.500000"},
		{"assess", "2.000000"},
		{"fraud", "1.500000"},
		{"settle", "0.800000"},
	}

	for _, s := range steps {
		wf, _ = svc.StartStep(ctx, wf.ID, s.name, "0xOwner")
		wf, _ = svc.CompleteStep(ctx, wf.ID, s.name, "0xOwner", s.cost)
	}

	if wf.Status != WSCompleted {
		t.Fatalf("expected completed, got %s", wf.Status)
	}
	if wf.BudgetSpent != "4.800000" {
		t.Fatalf("expected 4.800000 spent, got %s", wf.BudgetSpent)
	}
	if wf.BudgetRemain != "5.200000" {
		t.Fatalf("expected 5.200000 remaining, got %s", wf.BudgetRemain)
	}

	// Verify refund of remaining
	refRef := wf.EscrowRef + ":refund"
	if ml.refunded[refRef] != "5.200000" {
		t.Fatalf("expected refund 5.200000, got %s", ml.refunded[refRef])
	}

	// Verify per-step releases
	for _, s := range steps {
		ref := wf.EscrowRef + ":step:" + s.name
		if ml.released[ref] != s.cost {
			t.Fatalf("step %s: expected release %s, got %s", s.name, s.cost, ml.released[ref])
		}
	}

	// Audit trail should have all entries
	// 1 create + 4 starts + 4 completes + 1 workflow_completed = 10
	if len(wf.AuditTrail) != 10 {
		t.Fatalf("expected 10 audit entries, got %d", len(wf.AuditTrail))
	}
}

func TestWorkflow_CostReport(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	wf, _ := svc.Create(ctx, "0xOwner", defaultReq())
	svc.StartStep(ctx, wf.ID, "intake", "0xOwner")
	svc.CompleteStep(ctx, wf.ID, "intake", "0xOwner", "0.750000")

	report, err := svc.GetCostReport(ctx, wf.ID)
	if err != nil {
		t.Fatalf("GetCostReport: %v", err)
	}
	if report.BudgetSpent != "0.750000" {
		t.Fatalf("expected 0.750000 spent, got %s", report.BudgetSpent)
	}
	if len(report.StepCosts) != 4 {
		t.Fatalf("expected 4 step costs, got %d", len(report.StepCosts))
	}
	if report.StepCosts[0].ActualCost != "0.750000" {
		t.Fatalf("expected intake cost 0.750000, got %s", report.StepCosts[0].ActualCost)
	}
}

func TestWorkflow_StepBudgetExceeded(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	wf, _ := svc.Create(ctx, "0xOwner", defaultReq())
	svc.StartStep(ctx, wf.ID, "intake", "0xOwner")

	// Intake step has maxCost 1.000000, try to complete with 2.000000
	_, err := svc.CompleteStep(ctx, wf.ID, "intake", "0xOwner", "2.000000")
	if !errors.Is(err, ErrStepBudgetExceeded) {
		t.Fatalf("expected ErrStepBudgetExceeded, got %v", err)
	}
}

func TestWorkflow_TotalBudgetExceeded(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	req := CreateWorkflowRequest{
		Name:        "small-budget",
		BudgetTotal: "1.000000",
		Steps: []StepDefinition{
			{Name: "step1", AgentAddr: "0xA", MaxCost: "1.000000"},
			{Name: "step2", AgentAddr: "0xB", MaxCost: "1.000000"},
		},
	}
	wf, _ := svc.Create(ctx, "0xOwner", req)

	svc.StartStep(ctx, wf.ID, "step1", "0xOwner")
	svc.CompleteStep(ctx, wf.ID, "step1", "0xOwner", "0.800000")

	svc.StartStep(ctx, wf.ID, "step2", "0xOwner")
	_, err := svc.CompleteStep(ctx, wf.ID, "step2", "0xOwner", "0.500000")
	if !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("expected ErrBudgetExceeded, got %v", err)
	}
}

func TestWorkflow_VelocityCircuitBreaker(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	req := defaultReq()
	req.MaxVelocity = 1.0 // Max 1 USDC per minute

	wf, _ := svc.Create(ctx, "0xOwner", req)
	svc.StartStep(ctx, wf.ID, "intake", "0xOwner")
	// First step: 0.5 USDC — under velocity
	svc.CompleteStep(ctx, wf.ID, "intake", "0xOwner", "0.500000")

	svc.StartStep(ctx, wf.ID, "assess", "0xOwner")
	// Second step: 0.6 USDC — total 1.1 in <1 min → circuit break
	wf, err := svc.CompleteStep(ctx, wf.ID, "assess", "0xOwner", "0.600000")
	if !errors.Is(err, ErrVelocityBreaker) {
		t.Fatalf("expected ErrVelocityBreaker, got %v", err)
	}
	if wf.Status != WSBreaker {
		t.Fatalf("expected circuit_broken, got %s", wf.Status)
	}
}

func TestWorkflow_Abort(t *testing.T) {
	svc, ml := newTestService()
	ctx := context.Background()

	wf, _ := svc.Create(ctx, "0xOwner", defaultReq())

	// Complete one step, then abort
	svc.StartStep(ctx, wf.ID, "intake", "0xOwner")
	svc.CompleteStep(ctx, wf.ID, "intake", "0xOwner", "0.500000")

	wf, err := svc.Abort(ctx, wf.ID, "0xOwner")
	if err != nil {
		t.Fatalf("Abort: %v", err)
	}
	if wf.Status != WSAborted {
		t.Fatalf("expected aborted, got %s", wf.Status)
	}

	// Remaining 9.5 should be refunded
	ref := wf.EscrowRef + ":abort"
	if ml.refunded[ref] != "9.500000" {
		t.Fatalf("expected refund 9.500000, got %s", ml.refunded[ref])
	}
}

func TestWorkflow_AbortUnauthorized(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	wf, _ := svc.Create(ctx, "0xOwner", defaultReq())
	_, err := svc.Abort(ctx, wf.ID, "0xStranger")
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got %v", err)
	}
}

func TestWorkflow_DoubleStart(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	wf, _ := svc.Create(ctx, "0xOwner", defaultReq())
	svc.StartStep(ctx, wf.ID, "intake", "0xOwner")

	_, err := svc.StartStep(ctx, wf.ID, "intake", "0xOwner")
	if !errors.Is(err, ErrStepAlreadyStarted) {
		t.Fatalf("expected ErrStepAlreadyStarted, got %v", err)
	}
}

func TestWorkflow_CompleteBeforeStart(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	wf, _ := svc.Create(ctx, "0xOwner", defaultReq())
	_, err := svc.CompleteStep(ctx, wf.ID, "intake", "0xOwner", "0.500000")
	if !errors.Is(err, ErrStepNotStarted) {
		t.Fatalf("expected ErrStepNotStarted, got %v", err)
	}
}

func TestWorkflow_StepNotFound(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	wf, _ := svc.Create(ctx, "0xOwner", defaultReq())
	_, err := svc.StartStep(ctx, wf.ID, "nonexistent", "0xOwner")
	if !errors.Is(err, ErrStepNotFound) {
		t.Fatalf("expected ErrStepNotFound, got %v", err)
	}
}

func TestWorkflow_FailStep(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	req := CreateWorkflowRequest{
		Name:        "fail-test",
		BudgetTotal: "5.000000",
		Steps: []StepDefinition{
			{Name: "step1", AgentAddr: "0xA", MaxCost: "5.000000"},
		},
	}
	wf, _ := svc.Create(ctx, "0xOwner", req)
	svc.StartStep(ctx, wf.ID, "step1", "0xOwner")

	wf, err := svc.FailStep(ctx, wf.ID, "step1", "0xOwner", "agent crashed")
	if err != nil {
		t.Fatalf("FailStep: %v", err)
	}
	if wf.Steps[0].Status != SSFailed {
		t.Fatalf("expected failed, got %s", wf.Steps[0].Status)
	}
	if wf.Steps[0].Error != "agent crashed" {
		t.Fatalf("expected error message, got %s", wf.Steps[0].Error)
	}
	// Single step workflow should auto-complete on fail
	if wf.Status != WSCompleted {
		t.Fatalf("expected completed, got %s", wf.Status)
	}
}

func TestWorkflow_AuditTrailHashChain(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	wf, _ := svc.Create(ctx, "0xOwner", defaultReq())
	svc.StartStep(ctx, wf.ID, "intake", "0xOwner")
	wf, _ = svc.CompleteStep(ctx, wf.ID, "intake", "0xOwner", "0.500000")

	// Verify hash chain integrity
	for i := 1; i < len(wf.AuditTrail); i++ {
		if wf.AuditTrail[i].Hash == "" {
			t.Fatalf("audit entry %d has empty hash", i)
		}
		if wf.AuditTrail[i].Hash == wf.AuditTrail[i-1].Hash {
			t.Fatalf("audit entries %d and %d have same hash (chain broken)", i-1, i)
		}
	}

	// Verify sequential numbering
	for i, entry := range wf.AuditTrail {
		if entry.Seq != i {
			t.Fatalf("audit entry %d has seq %d", i, entry.Seq)
		}
	}
}

func TestWorkflow_NotFound(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	_, err := svc.Get(ctx, "nonexistent")
	if !errors.Is(err, ErrWorkflowNotFound) {
		t.Fatalf("expected ErrWorkflowNotFound, got %v", err)
	}
}

func TestWorkflow_InvalidBudget(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	_, err := svc.Create(ctx, "0xOwner", CreateWorkflowRequest{
		Name:        "bad",
		BudgetTotal: "-1.000000",
		Steps:       []StepDefinition{{Name: "x", AgentAddr: "0xA"}},
	})
	if !errors.Is(err, ErrInvalidAmount) {
		t.Fatalf("expected ErrInvalidAmount, got %v", err)
	}
}

func TestWorkflow_ListByOwner(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	svc.Create(ctx, "0xOwner", defaultReq())
	svc.Create(ctx, "0xOwner", defaultReq())
	svc.Create(ctx, "0xOther", defaultReq())

	list, _ := svc.ListByOwner(ctx, "0xOwner", 50)
	if len(list) != 2 {
		t.Fatalf("expected 2 workflows, got %d", len(list))
	}
}

func TestWorkflow_DeepCopy(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	wf := &Workflow{
		ID:        "wfl_test",
		OwnerAddr: "owner",
		Status:    WSActive,
		Steps: []WorkflowStep{
			{Name: "step1", Status: SSPending},
		},
		AuditTrail: []AuditEntry{
			{Seq: 0, Action: "test"},
		},
	}
	store.Create(ctx, wf)

	// Mutate original
	wf.Steps = append(wf.Steps, WorkflowStep{Name: "step2"})

	got, _ := store.Get(ctx, "wfl_test")
	if len(got.Steps) != 1 {
		t.Fatalf("deep copy broken: got %d steps", len(got.Steps))
	}
}

func TestWorkflow_GlobalMaxCostPerStep(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	req := defaultReq()
	req.MaxCostPerStep = "0.500000" // Global cap: no step can cost more than 0.50

	wf, _ := svc.Create(ctx, "0xOwner", req)
	svc.StartStep(ctx, wf.ID, "intake", "0xOwner")

	// Step has maxCost 1.000000 but global cap is 0.500000
	_, err := svc.CompleteStep(ctx, wf.ID, "intake", "0xOwner", "0.600000")
	if !errors.Is(err, ErrStepBudgetExceeded) {
		t.Fatalf("expected ErrStepBudgetExceeded from global cap, got %v", err)
	}

	// Under global cap should work
	_, err = svc.CompleteStep(ctx, wf.ID, "intake", "0xOwner", "0.400000")
	if err != nil {
		t.Fatalf("should accept under global cap: %v", err)
	}
}

func TestWorkflow_OperationsAfterCompleted(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	req := CreateWorkflowRequest{
		Name:        "single",
		BudgetTotal: "1.000000",
		Steps:       []StepDefinition{{Name: "only", AgentAddr: "0xA", MaxCost: "1.000000"}},
	}
	wf, _ := svc.Create(ctx, "0xOwner", req)
	svc.StartStep(ctx, wf.ID, "only", "0xOwner")
	svc.CompleteStep(ctx, wf.ID, "only", "0xOwner", "0.500000")

	_, err := svc.Abort(ctx, wf.ID, "0xOwner")
	if !errors.Is(err, ErrWorkflowCompleted) {
		t.Fatalf("expected ErrWorkflowCompleted, got %v", err)
	}
}

func TestWorkflow_CreateStoreFailRefundError(t *testing.T) {
	// Verify that a store.Create failure followed by a refund failure
	// does not panic and still returns the store error to the caller.
	ml := newMockLedger()
	ml.refundErr = errors.New("ledger unavailable")

	// Use a store that fails on Create
	store := &failingStore{MemoryStore: NewMemoryStore(), createErr: errors.New("db down")}
	svc := NewService(store, ml).WithLogger(slog.New(slog.NewTextHandler(os.Stderr, nil)))

	ctx := context.Background()
	req := defaultReq()
	_, err := svc.Create(ctx, "0xOwner", req)
	if err == nil {
		t.Fatal("expected error from store.Create")
	}
	if err.Error() != "db down" {
		t.Fatalf("expected store error, got: %v", err)
	}
}

// failingStore wraps MemoryStore and fails on Create.
type failingStore struct {
	*MemoryStore
	createErr error
}

func (f *failingStore) Create(_ context.Context, _ *Workflow) error {
	return f.createErr
}
