package ledger

import (
	"math/big"
	"testing"
)

func TestCheckInvariant_Valid(t *testing.T) {
	// $100 deposited, $30 spent, $20 held, $10 escrowed
	// Available = 100 - 30 - 20 - 10 = 40
	// TotalIn = 100, TotalOut = 30
	// A + P + E = 40 + 20 + 10 = 70 = 100 - 30 ✓
	b := &Balance{
		Available: "40.000000",
		Pending:   "20.000000",
		Escrowed:  "10.000000",
		TotalIn:   "100.000000",
		TotalOut:  "30.000000",
	}

	if err := CheckInvariant(b); err != nil {
		t.Fatalf("valid balance should pass invariant: %v", err)
	}
}

func TestCheckInvariant_Violated(t *testing.T) {
	// Money was created: A+P+E > TotalIn - TotalOut
	b := &Balance{
		Available: "50.000000",
		Pending:   "20.000000",
		Escrowed:  "10.000000",
		TotalIn:   "100.000000",
		TotalOut:  "30.000000",
	}
	// A+P+E = 80, TotalIn-TotalOut = 70 → violation

	err := CheckInvariant(b)
	if err == nil {
		t.Fatal("violated balance should return error")
	}
	if err != ErrInvariantViolation {
		t.Fatalf("expected ErrInvariantViolation, got: %v", err)
	}
}

func TestCheckInvariant_ZeroBalance(t *testing.T) {
	b := &Balance{
		Available: "0.000000",
		Pending:   "0.000000",
		Escrowed:  "0.000000",
		TotalIn:   "0.000000",
		TotalOut:  "0.000000",
	}

	if err := CheckInvariant(b); err != nil {
		t.Fatalf("zero balance should pass: %v", err)
	}
}

func TestCheckInvariant_AllDeposited(t *testing.T) {
	// All money available, nothing spent
	b := &Balance{
		Available: "1000.000000",
		Pending:   "0.000000",
		Escrowed:  "0.000000",
		TotalIn:   "1000.000000",
		TotalOut:  "0.000000",
	}

	if err := CheckInvariant(b); err != nil {
		t.Fatalf("all-available should pass: %v", err)
	}
}

func TestCheckInvariant_AllSpent(t *testing.T) {
	// Everything deposited and spent
	b := &Balance{
		Available: "0.000000",
		Pending:   "0.000000",
		Escrowed:  "0.000000",
		TotalIn:   "500.000000",
		TotalOut:  "500.000000",
	}

	if err := CheckInvariant(b); err != nil {
		t.Fatalf("fully-spent should pass: %v", err)
	}
}

func TestCheckInvariant_WithCredit(t *testing.T) {
	// Agent has $50 credit used, adjusting the equation:
	// A + P + E = TotalIn - TotalOut + CreditUsed
	// 120 + 0 + 0 = 100 - 30 + 50 = 120 ✓
	b := &Balance{
		Available:  "120.000000",
		Pending:    "0.000000",
		Escrowed:   "0.000000",
		TotalIn:    "100.000000",
		TotalOut:   "30.000000",
		CreditUsed: "50.000000",
	}

	if err := CheckInvariant(b); err != nil {
		t.Fatalf("credit-adjusted balance should pass: %v", err)
	}
}

func TestInvariantDelta_Valid(t *testing.T) {
	b := &Balance{
		Available: "70.000000",
		Pending:   "0.000000",
		Escrowed:  "0.000000",
		TotalIn:   "100.000000",
		TotalOut:  "30.000000",
	}

	delta := InvariantDelta(b)
	if delta.Sign() != 0 {
		t.Errorf("valid balance should have zero delta, got %s", delta.String())
	}
}

func TestInvariantDelta_MoneyCreated(t *testing.T) {
	b := &Balance{
		Available: "80.000000",
		Pending:   "0.000000",
		Escrowed:  "0.000000",
		TotalIn:   "100.000000",
		TotalOut:  "30.000000",
	}
	// A+P+E = 80, TotalIn-TotalOut = 70 → delta = +10 (money created)

	delta := InvariantDelta(b)
	expected := big.NewInt(10_000_000) // $10 in 6-decimal
	if delta.Cmp(expected) != 0 {
		t.Errorf("delta = %s, want %s", delta.String(), expected.String())
	}
}

func TestInvariantDelta_MoneyDestroyed(t *testing.T) {
	b := &Balance{
		Available: "60.000000",
		Pending:   "0.000000",
		Escrowed:  "0.000000",
		TotalIn:   "100.000000",
		TotalOut:  "30.000000",
	}
	// A+P+E = 60, TotalIn-TotalOut = 70 → delta = -10 (money destroyed)

	delta := InvariantDelta(b)
	expected := big.NewInt(-10_000_000) // -$10
	if delta.Cmp(expected) != 0 {
		t.Errorf("delta = %s, want %s", delta.String(), expected.String())
	}
}
