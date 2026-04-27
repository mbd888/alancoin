package flywheel

import (
	"context"

	"github.com/mbd888/alancoin/internal/tiermath"
)

// IncentiveEngine computes per-tier fee discounts and discovery boosts.
// Schedules are derived from TierCurve (geometric scaling between Base and Max).
type IncentiveEngine struct {
	feeDiscount    tiermath.TierCurve // basis points off the platform take rate
	discoveryBoost tiermath.TierCurve // additive multiplier on reputation score
}

func NewIncentiveEngine() *IncentiveEngine {
	return &IncentiveEngine{
		feeDiscount:    tiermath.TierCurve{Base: 0, Max: 125},
		discoveryBoost: tiermath.TierCurve{Base: 0, Max: 0.50},
	}
}

// AdjustFeeBPS subtracts the tier's fee discount from baseBPS, clamped at 0.
// Unknown tiers pass through unchanged.
func (e *IncentiveEngine) AdjustFeeBPS(_ context.Context, tier string, baseBPS int) (int, error) {
	if !isKnownTier(tier) {
		return baseBPS, nil
	}
	adjusted := baseBPS - e.feeDiscount.AtInt(tier)
	if adjusted < 0 {
		adjusted = 0
	}
	return adjusted, nil
}

// BoostScore multiplies baseScore by (1 + tier boost), clamped at 100.
// Unknown tiers pass through unchanged.
func (e *IncentiveEngine) BoostScore(_ context.Context, tier string, baseScore float64) float64 {
	if !isKnownTier(tier) {
		return baseScore
	}
	boosted := baseScore * (1.0 + e.discoveryBoost.At(tier))
	if boosted > 100 {
		boosted = 100
	}
	return boosted
}

func (e *IncentiveEngine) GetFeeDiscountBPS(tier string) int {
	if !isKnownTier(tier) {
		return 0
	}
	return e.feeDiscount.AtInt(tier)
}

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
