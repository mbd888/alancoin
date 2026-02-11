package escrow

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"
)

func newMultiStepTestService() (*MultiStepService, *mockLedger) {
	ml := newMockLedger()
	store := NewMultiStepMemoryStore()
	svc := NewMultiStepService(store, ml).WithLogger(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	return svc, ml
}

func TestMultiStep_LockAndConfirmAll(t *testing.T) {
	svc, ml := newMultiStepTestService()
	ctx := context.Background()

	planned := []PlannedStep{
		{SellerAddr: "0xSeller1", Amount: "0.010000"},
		{SellerAddr: "0xSeller2", Amount: "0.010000"},
		{SellerAddr: "0xSeller3", Amount: "0.010000"},
	}
	mse, err := svc.LockSteps(ctx, "0xBuyer", "0.030000", 3, planned)
	if err != nil {
		t.Fatalf("LockSteps: %v", err)
	}
	if mse.Status != MSOpen {
		t.Fatalf("expected open, got %s", mse.Status)
	}
	if mse.TotalSteps != 3 {
		t.Fatalf("expected 3 steps, got %d", mse.TotalSteps)
	}
	if len(mse.PlannedSteps) != 3 {
		t.Fatalf("expected 3 planned steps, got %d", len(mse.PlannedSteps))
	}

	// Verify funds were locked
	lockRef := "mse:" + mse.ID
	if ml.locked[lockRef] != "0.030000" {
		t.Fatalf("expected locked 0.030000, got %s", ml.locked[lockRef])
	}

	// Confirm step 0
	mse, err = svc.ConfirmStep(ctx, mse.ID, 0, "0xSeller1", "0.010000")
	if err != nil {
		t.Fatalf("ConfirmStep 0: %v", err)
	}
	if mse.ConfirmedSteps != 1 {
		t.Fatalf("expected 1 confirmed, got %d", mse.ConfirmedSteps)
	}
	if mse.Status != MSOpen {
		t.Fatalf("expected open after step 0, got %s", mse.Status)
	}

	// Confirm step 1
	mse, err = svc.ConfirmStep(ctx, mse.ID, 1, "0xSeller2", "0.010000")
	if err != nil {
		t.Fatalf("ConfirmStep 1: %v", err)
	}
	if mse.ConfirmedSteps != 2 {
		t.Fatalf("expected 2 confirmed, got %d", mse.ConfirmedSteps)
	}

	// Confirm step 2 → should auto-complete
	mse, err = svc.ConfirmStep(ctx, mse.ID, 2, "0xSeller3", "0.010000")
	if err != nil {
		t.Fatalf("ConfirmStep 2: %v", err)
	}
	if mse.Status != MSCompleted {
		t.Fatalf("expected completed, got %s", mse.Status)
	}
	if mse.ConfirmedSteps != 3 {
		t.Fatalf("expected 3 confirmed, got %d", mse.ConfirmedSteps)
	}

	// Verify all 3 releases happened
	for i := 0; i < 3; i++ {
		ref := "mse:" + mse.ID + ":step:" + string(rune('0'+i))
		if _, ok := ml.released[ref]; !ok {
			t.Errorf("step %d release not found in ledger", i)
		}
	}
}

func TestMultiStep_PartialConfirmThenRefund(t *testing.T) {
	svc, ml := newMultiStepTestService()
	ctx := context.Background()

	planned := []PlannedStep{
		{SellerAddr: "0xSeller1", Amount: "0.010000"},
		{SellerAddr: "0xSeller2", Amount: "0.010000"},
		{SellerAddr: "0xSeller3", Amount: "0.010000"},
	}
	mse, err := svc.LockSteps(ctx, "0xBuyer", "0.030000", 3, planned)
	if err != nil {
		t.Fatalf("LockSteps: %v", err)
	}

	// Confirm step 0 only
	mse, err = svc.ConfirmStep(ctx, mse.ID, 0, "0xSeller1", "0.010000")
	if err != nil {
		t.Fatalf("ConfirmStep 0: %v", err)
	}

	// Refund remaining
	mse, err = svc.RefundRemaining(ctx, mse.ID, "0xBuyer")
	if err != nil {
		t.Fatalf("RefundRemaining: %v", err)
	}
	if mse.Status != MSAborted {
		t.Fatalf("expected aborted, got %s", mse.Status)
	}

	// Verify refund of 0.020000 (0.030000 - 0.010000)
	refRef := "mse:" + mse.ID + ":refund"
	if ml.refunded[refRef] != "0.020000" {
		t.Fatalf("expected refund of 0.020000, got %s", ml.refunded[refRef])
	}
}

func TestMultiStep_ConfirmExceedsRemaining(t *testing.T) {
	svc, _ := newMultiStepTestService()
	ctx := context.Background()

	planned := []PlannedStep{
		{SellerAddr: "0xSeller1", Amount: "0.015000"},
		{SellerAddr: "0xSeller2", Amount: "0.010000"},
	}
	mse, err := svc.LockSteps(ctx, "0xBuyer", "0.020000", 2, planned)
	if err != nil {
		t.Fatalf("LockSteps: %v", err)
	}

	// Confirm step 0 with 0.015
	_, err = svc.ConfirmStep(ctx, mse.ID, 0, "0xSeller1", "0.015000")
	if err != nil {
		t.Fatalf("ConfirmStep 0: %v", err)
	}

	// Step 1 tries to use 0.010 → total would be 0.025 > 0.020
	_, err = svc.ConfirmStep(ctx, mse.ID, 1, "0xSeller2", "0.010000")
	if !errors.Is(err, ErrAmountExceedsTotal) {
		t.Fatalf("expected ErrAmountExceedsTotal, got %v", err)
	}
}

func TestMultiStep_DuplicateStep(t *testing.T) {
	svc, _ := newMultiStepTestService()
	ctx := context.Background()

	planned := []PlannedStep{
		{SellerAddr: "0xSeller1", Amount: "0.010000"},
		{SellerAddr: "0xSeller2", Amount: "0.010000"},
	}
	mse, err := svc.LockSteps(ctx, "0xBuyer", "0.020000", 2, planned)
	if err != nil {
		t.Fatalf("LockSteps: %v", err)
	}

	_, err = svc.ConfirmStep(ctx, mse.ID, 0, "0xSeller1", "0.010000")
	if err != nil {
		t.Fatalf("ConfirmStep 0: %v", err)
	}

	// Try to confirm step 0 again
	_, err = svc.ConfirmStep(ctx, mse.ID, 0, "0xSeller1", "0.010000")
	if !errors.Is(err, ErrDuplicateStep) {
		t.Fatalf("expected ErrDuplicateStep, got %v", err)
	}
}

func TestMultiStep_AbortAlreadyCompleted(t *testing.T) {
	svc, _ := newMultiStepTestService()
	ctx := context.Background()

	planned := []PlannedStep{
		{SellerAddr: "0xSeller1", Amount: "0.010000"},
	}
	mse, err := svc.LockSteps(ctx, "0xBuyer", "0.010000", 1, planned)
	if err != nil {
		t.Fatalf("LockSteps: %v", err)
	}

	// Confirm the only step → auto-complete
	_, err = svc.ConfirmStep(ctx, mse.ID, 0, "0xSeller1", "0.010000")
	if err != nil {
		t.Fatalf("ConfirmStep: %v", err)
	}

	// Try to refund completed escrow
	_, err = svc.RefundRemaining(ctx, mse.ID, "0xBuyer")
	if err == nil {
		t.Fatal("expected error when refunding completed escrow")
	}
	if !errors.Is(err, ErrInvalidStatus) {
		t.Fatalf("expected ErrInvalidStatus, got %v", err)
	}
}

func TestMultiStep_ConfirmOnAborted(t *testing.T) {
	svc, _ := newMultiStepTestService()
	ctx := context.Background()

	planned := []PlannedStep{
		{SellerAddr: "0xSeller1", Amount: "0.010000"},
		{SellerAddr: "0xSeller2", Amount: "0.010000"},
	}
	mse, err := svc.LockSteps(ctx, "0xBuyer", "0.020000", 2, planned)
	if err != nil {
		t.Fatalf("LockSteps: %v", err)
	}

	// Abort immediately
	_, err = svc.RefundRemaining(ctx, mse.ID, "0xBuyer")
	if err != nil {
		t.Fatalf("RefundRemaining: %v", err)
	}

	// Try to confirm step on aborted
	_, err = svc.ConfirmStep(ctx, mse.ID, 0, "0xSeller1", "0.010000")
	if err == nil {
		t.Fatal("expected error when confirming on aborted escrow")
	}
	if !errors.Is(err, ErrInvalidStatus) {
		t.Fatalf("expected ErrInvalidStatus, got %v", err)
	}
}

func TestMultiStep_UnauthorizedRefund(t *testing.T) {
	svc, _ := newMultiStepTestService()
	ctx := context.Background()

	planned := []PlannedStep{
		{SellerAddr: "0xSeller1", Amount: "0.010000"},
		{SellerAddr: "0xSeller2", Amount: "0.010000"},
	}
	mse, err := svc.LockSteps(ctx, "0xBuyer", "0.020000", 2, planned)
	if err != nil {
		t.Fatalf("LockSteps: %v", err)
	}

	// Non-buyer tries to refund
	_, err = svc.RefundRemaining(ctx, mse.ID, "0xStranger")
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got %v", err)
	}
}

func TestMultiStep_StepOutOfRange(t *testing.T) {
	svc, _ := newMultiStepTestService()
	ctx := context.Background()

	planned := []PlannedStep{
		{SellerAddr: "0xSeller1", Amount: "0.010000"},
		{SellerAddr: "0xSeller2", Amount: "0.010000"},
	}
	mse, err := svc.LockSteps(ctx, "0xBuyer", "0.020000", 2, planned)
	if err != nil {
		t.Fatalf("LockSteps: %v", err)
	}

	// Step index negative
	_, err = svc.ConfirmStep(ctx, mse.ID, -1, "0xSeller1", "0.010000")
	if !errors.Is(err, ErrStepOutOfRange) {
		t.Fatalf("expected ErrStepOutOfRange for -1, got %v", err)
	}

	// Step index >= totalSteps
	_, err = svc.ConfirmStep(ctx, mse.ID, 2, "0xSeller1", "0.010000")
	if !errors.Is(err, ErrStepOutOfRange) {
		t.Fatalf("expected ErrStepOutOfRange for 2, got %v", err)
	}
}

func TestMultiStep_InvalidInputs(t *testing.T) {
	svc, _ := newMultiStepTestService()
	ctx := context.Background()

	planned := []PlannedStep{
		{SellerAddr: "0xSeller1", Amount: "0.500000"},
		{SellerAddr: "0xSeller2", Amount: "0.500000"},
	}

	// Zero steps
	_, err := svc.LockSteps(ctx, "0xBuyer", "1.000000", 0, nil)
	if err == nil {
		t.Fatal("expected error for zero steps")
	}

	// Invalid amount
	_, err = svc.LockSteps(ctx, "0xBuyer", "-1.000000", 2, planned)
	if err == nil {
		t.Fatal("expected error for negative amount")
	}

	// Empty amount
	_, err = svc.LockSteps(ctx, "0xBuyer", "", 2, planned)
	if err == nil {
		t.Fatal("expected error for empty amount")
	}

	// Mismatched planned steps count
	_, err = svc.LockSteps(ctx, "0xBuyer", "1.000000", 3, planned)
	if err == nil {
		t.Fatal("expected error for mismatched planned steps count")
	}
}

func TestMultiStep_AutoCompleteDustRefund(t *testing.T) {
	svc, ml := newMultiStepTestService()
	ctx := context.Background()

	// Lock 0.030000 for 2 steps, but planned amounts sum to 0.020000
	planned := []PlannedStep{
		{SellerAddr: "0xSeller1", Amount: "0.010000"},
		{SellerAddr: "0xSeller2", Amount: "0.010000"},
	}
	mse, err := svc.LockSteps(ctx, "0xBuyer", "0.030000", 2, planned)
	if err != nil {
		t.Fatalf("LockSteps: %v", err)
	}

	_, err = svc.ConfirmStep(ctx, mse.ID, 0, "0xSeller1", "0.010000")
	if err != nil {
		t.Fatalf("ConfirmStep 0: %v", err)
	}

	// Confirm final step with less than remaining → triggers auto-complete + dust refund
	mse, err = svc.ConfirmStep(ctx, mse.ID, 1, "0xSeller2", "0.010000")
	if err != nil {
		t.Fatalf("ConfirmStep 1: %v", err)
	}
	if mse.Status != MSCompleted {
		t.Fatalf("expected completed, got %s", mse.Status)
	}

	// Dust refund of 0.010000 should have been issued
	dustRef := "mse:" + mse.ID + ":dust"
	if ml.refunded[dustRef] != "0.010000" {
		t.Fatalf("expected dust refund of 0.010000, got %s", ml.refunded[dustRef])
	}
}

func TestMultiStep_NotFound(t *testing.T) {
	svc, _ := newMultiStepTestService()
	ctx := context.Background()

	_, err := svc.Get(ctx, "nonexistent")
	if !errors.Is(err, ErrMultiStepNotFound) {
		t.Fatalf("expected ErrMultiStepNotFound, got %v", err)
	}

	_, err = svc.ConfirmStep(ctx, "nonexistent", 0, "0xSeller", "0.010000")
	if !errors.Is(err, ErrMultiStepNotFound) {
		t.Fatalf("expected ErrMultiStepNotFound, got %v", err)
	}

	_, err = svc.RefundRemaining(ctx, "nonexistent", "0xBuyer")
	if !errors.Is(err, ErrMultiStepNotFound) {
		t.Fatalf("expected ErrMultiStepNotFound, got %v", err)
	}
}

func TestMultiStep_SellerMismatch(t *testing.T) {
	svc, _ := newMultiStepTestService()
	ctx := context.Background()

	planned := []PlannedStep{
		{SellerAddr: "0xSeller1", Amount: "0.010000"},
		{SellerAddr: "0xSeller2", Amount: "0.010000"},
	}
	mse, err := svc.LockSteps(ctx, "0xBuyer", "0.020000", 2, planned)
	if err != nil {
		t.Fatalf("LockSteps: %v", err)
	}

	// Try to confirm step 0 with wrong seller
	_, err = svc.ConfirmStep(ctx, mse.ID, 0, "0xAttacker", "0.010000")
	if !errors.Is(err, ErrStepMismatch) {
		t.Fatalf("expected ErrStepMismatch for wrong seller, got %v", err)
	}

	// Try to confirm step 0 with wrong amount
	_, err = svc.ConfirmStep(ctx, mse.ID, 0, "0xSeller1", "0.005000")
	if !errors.Is(err, ErrStepMismatch) {
		t.Fatalf("expected ErrStepMismatch for wrong amount, got %v", err)
	}

	// Correct seller and amount works
	_, err = svc.ConfirmStep(ctx, mse.ID, 0, "0xSeller1", "0.010000")
	if err != nil {
		t.Fatalf("ConfirmStep with correct seller/amount failed: %v", err)
	}
}
