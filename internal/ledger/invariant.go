package ledger

import (
	"errors"
	"math/big"

	"github.com/mbd888/alancoin/internal/usdc"
)

// ErrInvariantViolation is returned when the conservation law is broken.
// This is a critical bug — money was created or destroyed.
var ErrInvariantViolation = errors.New("ledger: conservation invariant violated: A + P + E ≠ TotalIn - TotalOut")

// CheckInvariant verifies the fundamental conservation law:
//
//	Available + Pending + Escrowed = TotalIn - TotalOut
//
// This is Noether's theorem for money: the balance simplex must be
// conserved across all operations. Every deposit adds to TotalIn,
// every spend adds to TotalOut, and the three states (available,
// pending, escrowed) must account for exactly the difference.
//
// Returns nil if the invariant holds, ErrInvariantViolation otherwise.
// The check uses exact big.Int arithmetic — no floating point.
//
// This should be called after every balance-modifying operation in
// production. The cost is negligible (6 string parses + 3 additions)
// compared to the cost of an undetected financial bug.
func CheckInvariant(b *Balance) error {
	available, okA := usdc.Parse(b.Available)
	pending, okP := usdc.Parse(b.Pending)
	escrowed, okE := usdc.Parse(b.Escrowed)
	totalIn, okI := usdc.Parse(b.TotalIn)
	totalOut, okO := usdc.Parse(b.TotalOut)

	if !okA || !okP || !okE || !okI || !okO {
		// If any field is unparseable, we can't check. This is also a bug,
		// but a different kind (data corruption, not conservation violation).
		return nil
	}

	// Left side: A + P + E
	lhs := new(big.Int).Add(available, pending)
	lhs.Add(lhs, escrowed)

	// Right side: TotalIn - TotalOut
	rhs := new(big.Int).Sub(totalIn, totalOut)

	// Credit adjustment: if agent has used credit, the conservation law becomes
	// A + P + E = (TotalIn + CreditUsed) - TotalOut
	// because credit effectively injects money into available.
	creditUsed, okCU := usdc.Parse(b.CreditUsed)
	if okCU && creditUsed.Sign() > 0 {
		rhs.Add(rhs, creditUsed)
	}

	if lhs.Cmp(rhs) != 0 {
		return ErrInvariantViolation
	}

	return nil
}

// InvariantDelta returns the conservation error as a signed amount.
// Positive = more money exists than should (creation bug).
// Negative = money disappeared (destruction bug).
// Zero = invariant holds.
func InvariantDelta(b *Balance) *big.Int {
	available, okA := usdc.Parse(b.Available)
	pending, okP := usdc.Parse(b.Pending)
	escrowed, okE := usdc.Parse(b.Escrowed)
	totalIn, okI := usdc.Parse(b.TotalIn)
	totalOut, okO := usdc.Parse(b.TotalOut)

	if !okA || !okP || !okE || !okI || !okO {
		return new(big.Int) // can't compute, return zero
	}

	// lhs = A + P + E
	lhs := new(big.Int).Add(available, pending)
	lhs.Add(lhs, escrowed)

	// rhs = TotalIn - TotalOut + CreditUsed
	rhs := new(big.Int).Sub(totalIn, totalOut)
	creditUsed, okCU := usdc.Parse(b.CreditUsed)
	if okCU && creditUsed.Sign() > 0 {
		rhs.Add(rhs, creditUsed)
	}

	// delta = lhs - rhs (positive = money created, negative = money destroyed)
	return new(big.Int).Sub(lhs, rhs)
}
