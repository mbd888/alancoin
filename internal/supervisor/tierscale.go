package supervisor

import (
	"math"
	"math/big"

	"github.com/mbd888/alancoin/internal/tiermath"
)

// Supervisor-specific tier curves. Each is defined by (base, max) endpoints.
var (
	// VelocityCurve: hourly spend limit. $50/hr at new, $100K/hr at elite.
	VelocityCurve = tiermath.TierCurve{Base: 50, Max: 100_000}

	// ConcurrencyCurve: max simultaneous holds+escrows. 3 at new, 100 at elite.
	ConcurrencyCurve = tiermath.TierCurve{Base: 3, Max: 100}

	// PerTxCurve: max per-transaction amount. $5 at new, $50K at elite.
	PerTxCurve = tiermath.TierCurve{Base: 5, Max: 50_000}
)

// These package-level aliases preserve backward compatibility with existing
// code that references the supervisor-local types and functions.

// TierIndex is re-exported from tiermath for backward compatibility.
var TierIndex = tiermath.TierIndex

// TierCount is re-exported from tiermath for backward compatibility.
const TierCount = tiermath.NumTiers

// GeometricScale delegates to tiermath.Scale.
func GeometricScale(base, max float64, tier string) float64 {
	return tiermath.Scale(base, max, tier)
}

// VelocityLimitForTier returns the hourly spend limit in 6-decimal USDC.
func VelocityLimitForTier(tier string) *big.Int {
	dollars := VelocityCurve.At(tier)
	return big.NewInt(int64(math.Round(dollars * 1_000_000)))
}

// ConcurrencyLimitForTier returns the max simultaneous holds+escrows.
func ConcurrencyLimitForTier(tier string) int {
	return ConcurrencyCurve.AtInt(tier)
}

// PerTxLimitForTier returns the per-transaction limit in 6-decimal USDC.
func PerTxLimitForTier(tier string) *big.Int {
	dollars := PerTxCurve.At(tier)
	return big.NewInt(int64(math.Round(dollars * 1_000_000)))
}
