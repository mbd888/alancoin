package flywheel

import (
	"context"

	"github.com/mbd888/alancoin/internal/tiermath"
)

// IncentiveEngine computes dynamic fee discounts and discovery boosts
// based on agent reputation. This creates the positive feedback loop:
//
//	better reputation → lower fees + higher discovery ranking →
//	more transactions → even better reputation
//
// All incentive schedules are derived from TierCurve (geometric scaling).
// Two numbers (base, max) define each incentive dimension — no lookup tables.
type IncentiveEngine struct {
	feeDiscount    tiermath.TierCurve // BPS discount: 0 at new, 125 at elite
	discoveryBoost tiermath.TierCurve // Additive boost: 0 at new, 0.50 at elite
}

// NewIncentiveEngine creates an incentive engine with production defaults.
//
// Fee discount schedule (BPS off platform take rate, geometric scaling):
//
//	new:          0 bps
//	emerging:    ~15 bps
//	established: ~40 bps
//	trusted:     ~75 bps
//	elite:      125 bps
//
// Discovery boost (additive to reputation score, geometric scaling):
//
//	new:         +0.00 → 1.00x
//	emerging:    +0.06 → 1.06x
//	established: +0.16 → 1.16x
//	trusted:     +0.31 → 1.31x
//	elite:       +0.50 → 1.50x
func NewIncentiveEngine() *IncentiveEngine {
	return &IncentiveEngine{
		feeDiscount:    tiermath.TierCurve{Base: 0, Max: 125},
		discoveryBoost: tiermath.TierCurve{Base: 0, Max: 0.50},
	}
}

// AdjustFeeBPS applies a reputation-based fee discount to the base take rate.
// Returns the effective take rate after discount (never below 0).
// Unknown tiers get no discount (conservative default).
func (e *IncentiveEngine) AdjustFeeBPS(_ context.Context, tier string, baseBPS int) (int, error) {
	if !isKnownTier(tier) {
		return baseBPS, nil
	}
	discount := e.feeDiscount.AtInt(tier)
	adjusted := baseBPS - discount
	if adjusted < 0 {
		adjusted = 0
	}
	return adjusted, nil
}

// BoostScore applies a reputation-tier discovery boost to a candidate's
// score. Higher-reputation agents receive an additive boost.
// Unknown tiers get no boost (conservative default).
func (e *IncentiveEngine) BoostScore(_ context.Context, tier string, baseScore float64) float64 {
	if !isKnownTier(tier) {
		return baseScore
	}
	boost := e.discoveryBoost.At(tier)
	boosted := baseScore * (1.0 + boost)
	if boosted > 100 {
		boosted = 100
	}
	return boosted
}

// GetFeeDiscountBPS returns the raw discount amount for a tier.
// Unknown tiers return 0.
func (e *IncentiveEngine) GetFeeDiscountBPS(tier string) int {
	if !isKnownTier(tier) {
		return 0
	}
	return e.feeDiscount.AtInt(tier)
}

// GetDiscoveryBoostMultiplier returns the boost multiplier for a tier.
// Unknown tiers return 1.0 (no boost).
func (e *IncentiveEngine) GetDiscoveryBoostMultiplier(tier string) float64 {
	if !isKnownTier(tier) {
		return 1.0
	}
	return 1.0 + e.discoveryBoost.At(tier)
}

func isKnownTier(tier string) bool {
	_, ok := tiermath.TierIndex[tier]
	return ok
}

// IncentiveSummary returns a snapshot of the incentive schedule.
func (e *IncentiveEngine) IncentiveSummary() map[string]interface{} {
	schedule := make([]map[string]interface{}, 0, tiermath.NumTiers)
	for _, tier := range tiermath.TierNames {
		schedule = append(schedule, map[string]interface{}{
			"tier":               tier,
			"feeDiscountBPS":     e.GetFeeDiscountBPS(tier),
			"discoveryBoostMult": e.GetDiscoveryBoostMultiplier(tier),
		})
	}
	return map[string]interface{}{
		"schedule": schedule,
	}
}
