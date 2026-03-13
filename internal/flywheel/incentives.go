package flywheel

import "context"

// IncentiveEngine computes dynamic fee discounts and discovery boosts
// based on agent reputation. This creates the positive feedback loop:
//
//	better reputation → lower fees + higher discovery ranking →
//	more transactions → even better reputation
//
// Fee discounts reward established agents, reducing their cost to operate.
// Discovery boosts give high-reputation agents more visibility, driving
// traffic toward reliable services. Both effects compound over time.
type IncentiveEngine struct {
	feeDiscountBPS map[string]int     // tier → basis point discount
	discoveryBoost map[string]float64 // tier → score multiplier
}

// NewIncentiveEngine creates an incentive engine with production defaults.
//
// Fee discount schedule (basis points off platform take rate):
//
//	new:         0 bps (0% discount)
//	emerging:   25 bps (10% discount on typical 250bps take rate)
//	established: 50 bps (20% discount)
//	trusted:     87 bps (35% discount)
//	elite:      125 bps (50% discount)
//
// Discovery boost multipliers (applied to reputation score in ranking):
//
//	new:         1.00x
//	emerging:    1.05x
//	established: 1.15x
//	trusted:     1.30x
//	elite:       1.50x
func NewIncentiveEngine() *IncentiveEngine {
	return &IncentiveEngine{
		feeDiscountBPS: map[string]int{
			"new":         0,
			"emerging":    25,
			"established": 50,
			"trusted":     87,
			"elite":       125,
		},
		discoveryBoost: map[string]float64{
			"new":         1.00,
			"emerging":    1.05,
			"established": 1.15,
			"trusted":     1.30,
			"elite":       1.50,
		},
	}
}

// AdjustFeeBPS applies a reputation-based fee discount to the base take rate.
// Returns the effective take rate after discount (never below 0).
//
// This satisfies gateway.IncentiveProvider:
//
//	type IncentiveProvider interface {
//	    AdjustFeeBPS(ctx context.Context, tier string, baseBPS int) (int, error)
//	}
func (e *IncentiveEngine) AdjustFeeBPS(_ context.Context, tier string, baseBPS int) (int, error) {
	discount, ok := e.feeDiscountBPS[tier]
	if !ok {
		return baseBPS, nil
	}
	adjusted := baseBPS - discount
	if adjusted < 0 {
		adjusted = 0
	}
	return adjusted, nil
}

// BoostScore applies a flywheel discovery boost to a candidate's reputation
// score. Higher-reputation agents receive a multiplier that improves their
// ranking in service discovery results.
//
// This satisfies gateway.DiscoveryBooster:
//
//	type DiscoveryBooster interface {
//	    BoostScore(ctx context.Context, tier string, baseScore float64) float64
//	}
func (e *IncentiveEngine) BoostScore(_ context.Context, tier string, baseScore float64) float64 {
	mult, ok := e.discoveryBoost[tier]
	if !ok {
		return baseScore
	}
	boosted := baseScore * mult
	if boosted > 100 {
		boosted = 100
	}
	return boosted
}

// GetFeeDiscountBPS returns the raw discount amount for a tier.
func (e *IncentiveEngine) GetFeeDiscountBPS(tier string) int {
	return e.feeDiscountBPS[tier]
}

// GetDiscoveryBoostMultiplier returns the boost multiplier for a tier.
func (e *IncentiveEngine) GetDiscoveryBoostMultiplier(tier string) float64 {
	mult, ok := e.discoveryBoost[tier]
	if !ok {
		return 1.0
	}
	return mult
}

// IncentiveSummary returns a snapshot of the incentive schedule.
func (e *IncentiveEngine) IncentiveSummary() map[string]interface{} {
	tiers := []string{"new", "emerging", "established", "trusted", "elite"}
	schedule := make([]map[string]interface{}, 0, len(tiers))
	for _, tier := range tiers {
		schedule = append(schedule, map[string]interface{}{
			"tier":               tier,
			"feeDiscountBPS":     e.feeDiscountBPS[tier],
			"discoveryBoostMult": e.discoveryBoost[tier],
		})
	}
	return map[string]interface{}{
		"schedule": schedule,
	}
}
