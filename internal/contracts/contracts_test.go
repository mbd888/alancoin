package contracts

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"
)

func newTestService() *Service {
	store := NewMemoryStore()
	return NewService(store).WithLogger(slog.New(slog.NewTextHandler(os.Stderr, nil)))
}

func defaultContractReq() CreateContractRequest {
	return CreateContractRequest{
		Name: "test-contract",
		Invariants: []Condition{
			{
				Type:     CondMaxLatency,
				Severity: SeverityHard,
				Params:   map[string]interface{}{"maxMs": float64(5000)},
			},
			{
				Type:     CondNoPII,
				Severity: SeveritySoft,
				Params:   map[string]interface{}{},
			},
		},
		Recovery: RecoveryAbort,
	}
}

func TestContract_CreateAndGet(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	c, err := svc.Create(ctx, defaultContractReq())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if c.Status != StatusDraft {
		t.Fatalf("expected draft, got %s", c.Status)
	}
	if c.Name != "test-contract" {
		t.Fatalf("expected test-contract, got %s", c.Name)
	}
	if len(c.Invariants) != 2 {
		t.Fatalf("expected 2 invariants, got %d", len(c.Invariants))
	}

	got, err := svc.Get(ctx, c.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != c.ID {
		t.Fatalf("Get returned wrong ID")
	}
}

func TestContract_BindToEscrow(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	c, _ := svc.Create(ctx, defaultContractReq())

	bound, err := svc.BindToEscrow(ctx, c.ID, "coa_abc123")
	if err != nil {
		t.Fatalf("BindToEscrow: %v", err)
	}
	if bound.Status != StatusActive {
		t.Fatalf("expected active, got %s", bound.Status)
	}
	if bound.BoundEscrowID != "coa_abc123" {
		t.Fatalf("expected coa_abc123, got %s", bound.BoundEscrowID)
	}

	// Double bind should fail
	_, err = svc.BindToEscrow(ctx, c.ID, "coa_def456")
	if err == nil {
		t.Fatal("expected error on double bind")
	}
}

func TestContract_CheckInvariant_NoViolation(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	c, _ := svc.Create(ctx, defaultContractReq())
	svc.BindToEscrow(ctx, c.ID, "coa_test")

	// Normal latency, no PII
	result, err := svc.CheckInvariant(ctx, c.ID, CheckInvariantRequest{
		MemberAddr:    "0xAgent1",
		StepIndex:     0,
		LatencyMs:     1500,
		OutputPayload: "This is a normal output with no sensitive data.",
	})
	if err != nil {
		t.Fatalf("CheckInvariant: %v", err)
	}
	if result.HardViolations != 0 {
		t.Fatalf("expected 0 hard violations, got %d", result.HardViolations)
	}
	if result.SoftViolations != 0 {
		t.Fatalf("expected 0 soft violations, got %d", result.SoftViolations)
	}
}

func TestContract_CheckInvariant_HardViolation_Latency(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	c, _ := svc.Create(ctx, defaultContractReq())
	svc.BindToEscrow(ctx, c.ID, "coa_test")

	// Latency exceeds 5000ms → hard violation
	result, err := svc.CheckInvariant(ctx, c.ID, CheckInvariantRequest{
		MemberAddr: "0xAgent1",
		StepIndex:  0,
		LatencyMs:  7500,
	})
	if !errors.Is(err, ErrContractViolation) {
		t.Fatalf("expected ErrContractViolation, got %v", err)
	}
	if result.Status != StatusViolated {
		t.Fatalf("expected violated, got %s", result.Status)
	}
	if result.HardViolations != 1 {
		t.Fatalf("expected 1 hard violation, got %d", result.HardViolations)
	}
	if len(result.Violations) != 1 {
		t.Fatalf("expected 1 violation record, got %d", len(result.Violations))
	}
	if result.Violations[0].ConditionType != CondMaxLatency {
		t.Fatalf("expected max_latency violation, got %s", result.Violations[0].ConditionType)
	}
}

func TestContract_CheckInvariant_SoftViolation_PII(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	c, _ := svc.Create(ctx, defaultContractReq())
	svc.BindToEscrow(ctx, c.ID, "coa_test")

	// Output contains PII → soft violation
	result, err := svc.CheckInvariant(ctx, c.ID, CheckInvariantRequest{
		MemberAddr:    "0xAgent1",
		StepIndex:     0,
		LatencyMs:     100,
		OutputPayload: "The user's SSN is 123-45-6789",
	})
	if err != nil {
		t.Fatalf("CheckInvariant: %v (soft violation should not error)", err)
	}
	if result.SoftViolations != 1 {
		t.Fatalf("expected 1 soft violation, got %d", result.SoftViolations)
	}
	if result.QualityPenalty != 0.05 {
		t.Fatalf("expected penalty 0.05, got %f", result.QualityPenalty)
	}
}

func TestContract_QualityPenaltyCumulative(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	c, _ := svc.Create(ctx, defaultContractReq())
	svc.BindToEscrow(ctx, c.ID, "coa_test")

	// 3 soft violations → 0.15 penalty
	for i := 0; i < 3; i++ {
		_, err := svc.CheckInvariant(ctx, c.ID, CheckInvariantRequest{
			MemberAddr:    "0xAgent1",
			StepIndex:     i,
			LatencyMs:     100,
			OutputPayload: "Contains credit card number",
		})
		if err != nil {
			t.Fatalf("CheckInvariant %d: %v", i, err)
		}
	}

	result, _ := svc.Get(ctx, c.ID)
	if result.SoftViolations != 3 {
		t.Fatalf("expected 3 soft violations, got %d", result.SoftViolations)
	}
	if result.QualityPenalty < 0.14 || result.QualityPenalty > 0.16 {
		t.Fatalf("expected penalty ~0.15, got %f", result.QualityPenalty)
	}
}

func TestContract_EffectiveQualityScore(t *testing.T) {
	c := &Contract{QualityPenalty: 0.15}

	tests := []struct {
		raw  float64
		want float64
	}{
		{1.0, 0.85},
		{0.5, 0.35},
		{0.1, 0.0}, // clamped at 0
		{0.0, 0.0},
	}
	for _, tt := range tests {
		got := c.EffectiveQualityScore(tt.raw)
		if got < tt.want-0.001 || got > tt.want+0.001 {
			t.Errorf("raw=%.2f: got %.4f, want %.4f", tt.raw, got, tt.want)
		}
	}
}

func TestContract_MarkPassed(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	c, _ := svc.Create(ctx, defaultContractReq())
	svc.BindToEscrow(ctx, c.ID, "coa_test")

	result, err := svc.MarkPassed(ctx, c.ID)
	if err != nil {
		t.Fatalf("MarkPassed: %v", err)
	}
	if result.Status != StatusPassed {
		t.Fatalf("expected passed, got %s", result.Status)
	}
}

func TestContract_MarkPassed_NotActive(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	c, _ := svc.Create(ctx, defaultContractReq())

	// Contract is still draft → should fail
	_, err := svc.MarkPassed(ctx, c.ID)
	if err == nil {
		t.Fatal("expected error when marking draft as passed")
	}
}

func TestContract_CheckAfterViolated(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	c, _ := svc.Create(ctx, defaultContractReq())
	svc.BindToEscrow(ctx, c.ID, "coa_test")

	// Cause hard violation
	svc.CheckInvariant(ctx, c.ID, CheckInvariantRequest{
		MemberAddr: "0xAgent1",
		LatencyMs:  99999,
	})

	// Further checks should fail (contract is violated)
	_, err := svc.CheckInvariant(ctx, c.ID, CheckInvariantRequest{
		MemberAddr: "0xAgent1",
		LatencyMs:  100,
	})
	if err == nil {
		t.Fatal("expected error when checking violated contract")
	}
}

func TestContract_GetByEscrow(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	c, _ := svc.Create(ctx, defaultContractReq())
	svc.BindToEscrow(ctx, c.ID, "coa_target")

	found, err := svc.GetByEscrow(ctx, "coa_target")
	if err != nil {
		t.Fatalf("GetByEscrow: %v", err)
	}
	if found.ID != c.ID {
		t.Fatalf("wrong contract returned")
	}

	// Not found
	_, err = svc.GetByEscrow(ctx, "coa_nonexistent")
	if !errors.Is(err, ErrContractNotFound) {
		t.Fatalf("expected ErrContractNotFound, got %v", err)
	}
}

func TestContract_NotFound(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	_, err := svc.Get(ctx, "nonexistent")
	if !errors.Is(err, ErrContractNotFound) {
		t.Fatalf("expected ErrContractNotFound, got %v", err)
	}
}

func TestContract_AuditTrail(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	c, _ := svc.Create(ctx, defaultContractReq())
	svc.BindToEscrow(ctx, c.ID, "coa_audit_test")

	// Generate a soft violation
	svc.CheckInvariant(ctx, c.ID, CheckInvariantRequest{
		MemberAddr:    "0xAgent1",
		LatencyMs:     100,
		OutputPayload: "Contains passport number",
	})

	trail, err := svc.GetAuditTrail(ctx, c.ID)
	if err != nil {
		t.Fatalf("GetAuditTrail: %v", err)
	}
	if trail.ContractID != c.ID {
		t.Fatalf("wrong contract ID in audit trail")
	}
	if trail.EscrowID != "coa_audit_test" {
		t.Fatalf("wrong escrow ID in audit trail")
	}
	if trail.SoftViolations != 1 {
		t.Fatalf("expected 1 soft violation in trail, got %d", trail.SoftViolations)
	}
	if len(trail.Violations) != 1 {
		t.Fatalf("expected 1 violation record in trail, got %d", len(trail.Violations))
	}
	if len(trail.Invariants) != 2 {
		t.Fatalf("expected 2 invariants in trail, got %d", len(trail.Invariants))
	}
}

func TestContract_ValidationErrors(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	// No conditions
	_, err := svc.Create(ctx, CreateContractRequest{
		Name:     "empty",
		Recovery: RecoveryAbort,
	})
	if !errors.Is(err, ErrNoConditions) {
		t.Fatalf("expected ErrNoConditions, got %v", err)
	}

	// Invalid recovery
	_, err = svc.Create(ctx, CreateContractRequest{
		Name:       "bad-recovery",
		Invariants: []Condition{{Type: CondNoPII, Severity: SeveritySoft}},
		Recovery:   "invalid",
	})
	if err == nil {
		t.Fatal("expected error for invalid recovery")
	}

	// Invalid condition type
	_, err = svc.Create(ctx, CreateContractRequest{
		Name:       "bad-type",
		Invariants: []Condition{{Type: "unknown", Severity: SeveritySoft}},
		Recovery:   RecoveryAbort,
	})
	if err == nil {
		t.Fatal("expected error for invalid condition type")
	}

	// Invalid severity
	_, err = svc.Create(ctx, CreateContractRequest{
		Name:       "bad-severity",
		Invariants: []Condition{{Type: CondNoPII, Severity: "unknown"}},
		Recovery:   RecoveryAbort,
	})
	if err == nil {
		t.Fatal("expected error for invalid severity")
	}
}

func TestContract_MaxLatency_BelowThreshold(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	c, _ := svc.Create(ctx, CreateContractRequest{
		Name: "latency-test",
		Invariants: []Condition{
			{Type: CondMaxLatency, Severity: SeverityHard, Params: map[string]interface{}{"maxMs": float64(1000)}},
		},
		Recovery: RecoveryAbort,
	})
	svc.BindToEscrow(ctx, c.ID, "coa_lat")

	// Exactly at threshold: 1000ms <= 1000ms → no violation
	result, err := svc.CheckInvariant(ctx, c.ID, CheckInvariantRequest{
		MemberAddr: "0xAgent1",
		LatencyMs:  1000,
	})
	if err != nil {
		t.Fatalf("should not violate at threshold: %v", err)
	}
	if result.HardViolations != 0 {
		t.Fatalf("should have 0 violations at threshold")
	}
}

func TestContract_NoPII_CreditCard(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	c, _ := svc.Create(ctx, CreateContractRequest{
		Name: "pii-test",
		Invariants: []Condition{
			{Type: CondNoPII, Severity: SeverityHard, Params: map[string]interface{}{}},
		},
		Recovery: RecoveryAbort,
	})
	svc.BindToEscrow(ctx, c.ID, "coa_pii")

	_, err := svc.CheckInvariant(ctx, c.ID, CheckInvariantRequest{
		MemberAddr:    "0xAgent1",
		OutputPayload: "Please charge this credit card: 4111-1111-1111-1111",
	})
	if !errors.Is(err, ErrContractViolation) {
		t.Fatalf("expected hard violation for credit card PII, got %v", err)
	}
}

func TestContract_QualityPenaltyCap(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	c, _ := svc.Create(ctx, CreateContractRequest{
		Name: "cap-test",
		Invariants: []Condition{
			{Type: CondNoPII, Severity: SeveritySoft, Params: map[string]interface{}{}},
		},
		Recovery: RecoveryAlert,
	})
	svc.BindToEscrow(ctx, c.ID, "coa_cap")

	// 25 soft violations → penalty should be capped at 1.0
	for i := 0; i < 25; i++ {
		svc.CheckInvariant(ctx, c.ID, CheckInvariantRequest{
			MemberAddr:    "0xAgent1",
			StepIndex:     i,
			OutputPayload: "Contains SSN data",
		})
	}

	result, _ := svc.Get(ctx, c.ID)
	if result.QualityPenalty != 1.0 {
		t.Fatalf("expected penalty capped at 1.0, got %f", result.QualityPenalty)
	}
	if result.SoftViolations != 25 {
		t.Fatalf("expected 25 soft violations, got %d", result.SoftViolations)
	}
}

func TestContract_CostComparison_Numeric(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	c, _ := svc.Create(ctx, CreateContractRequest{
		Name: "cost-test",
		Invariants: []Condition{
			{
				Type:     CondMaxStepCost,
				Severity: SeverityHard,
				Params:   map[string]interface{}{"maxCost": "1.000000"},
			},
		},
		Recovery: RecoveryAbort,
	})
	svc.BindToEscrow(ctx, c.ID, "coa_cost")

	// 0.999999 < 1.000000 — should NOT violate
	result, err := svc.CheckInvariant(ctx, c.ID, CheckInvariantRequest{
		MemberAddr: "0xAgent1",
		StepCost:   "0.999999",
	})
	if err != nil {
		t.Fatalf("0.999999 should not exceed 1.000000: %v", err)
	}
	if result.HardViolations != 0 {
		t.Fatal("should not violate at 0.999999")
	}

	// 1.500000 > 1.000000 — should violate
	_, err = svc.CheckInvariant(ctx, c.ID, CheckInvariantRequest{
		MemberAddr: "0xAgent1",
		StepCost:   "1.500000",
	})
	if !errors.Is(err, ErrContractViolation) {
		t.Fatalf("1.500000 should exceed 1.000000: %v", err)
	}
}

func TestContract_TotalCostComparison(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	c, _ := svc.Create(ctx, CreateContractRequest{
		Name: "total-cost-test",
		Invariants: []Condition{
			{
				Type:     CondMaxTotalCost,
				Severity: SeveritySoft,
				Params:   map[string]interface{}{"maxCost": "10.000000"},
			},
		},
		Recovery: RecoveryDegrade,
	})
	svc.BindToEscrow(ctx, c.ID, "coa_tc")

	// Below threshold
	result, err := svc.CheckInvariant(ctx, c.ID, CheckInvariantRequest{
		MemberAddr: "0xAgent1",
		TotalCost:  "9.500000",
	})
	if err != nil {
		t.Fatalf("should not violate: %v", err)
	}
	if result.SoftViolations != 0 {
		t.Fatal("should have 0 violations")
	}

	// Above threshold
	result, err = svc.CheckInvariant(ctx, c.ID, CheckInvariantRequest{
		MemberAddr: "0xAgent1",
		TotalCost:  "10.500000",
	})
	if err != nil {
		t.Fatalf("soft violation should not error: %v", err)
	}
	if result.SoftViolations != 1 {
		t.Fatalf("expected 1 soft violation, got %d", result.SoftViolations)
	}
}

func TestContract_ViolationsCapped(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	c, _ := svc.Create(ctx, CreateContractRequest{
		Name: "cap-test",
		Invariants: []Condition{
			{Type: CondNoPII, Severity: SeveritySoft, Params: map[string]interface{}{}},
		},
		Recovery: RecoveryAlert,
	})
	svc.BindToEscrow(ctx, c.ID, "coa_viol_cap")

	// Generate 1050 violations — should be capped at 1000
	for i := 0; i < 1050; i++ {
		svc.CheckInvariant(ctx, c.ID, CheckInvariantRequest{
			MemberAddr:    "0xAgent1",
			StepIndex:     i,
			OutputPayload: "Contains SSN info",
		})
	}

	result, _ := svc.Get(ctx, c.ID)
	if len(result.Violations) > 1000 {
		t.Fatalf("expected violations capped at 1000, got %d", len(result.Violations))
	}
	// Counter still tracks all 1050
	if result.SoftViolations != 1050 {
		t.Fatalf("expected 1050 soft violations counted, got %d", result.SoftViolations)
	}
}

func TestContract_DeepCopy_MutationSafety(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	c := &Contract{
		ID:   "abc_test",
		Name: "test",
		Invariants: []Condition{
			{Type: CondNoPII, Severity: SeveritySoft, Params: map[string]interface{}{"key": "value"}},
		},
		Status: StatusDraft,
	}
	store.Create(ctx, c)

	// Mutate the original
	c.Invariants = append(c.Invariants, Condition{Type: CondMaxLatency, Severity: SeverityHard})

	got, _ := store.Get(ctx, "abc_test")
	if len(got.Invariants) != 1 {
		t.Fatalf("deep copy broken: stored invariants mutated, got %d", len(got.Invariants))
	}
}
