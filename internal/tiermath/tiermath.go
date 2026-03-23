// Package tiermath provides the universal geometric scaling function for
// all tier-based parameters across the Alancoin system.
//
// The core insight: every tier-based limit, discount, boost, or threshold
// should follow the same geometric progression. Each tier upgrade gives
// the same multiplicative improvement, making the system predictable,
// fair, and expressible as a single formula:
//
//	value(tier) = base × (max/base)^(tier_index/4)
//
// This eliminates hand-tuned lookup tables scattered across packages.
// A TierCurve is fully specified by just two numbers: the value at
// "new" (tier 0) and the value at "elite" (tier 4).
package tiermath

import "math"

// TierIndex maps reputation tier strings to a numeric index [0..4].
var TierIndex = map[string]int{
	"new":         0,
	"emerging":    1,
	"established": 2,
	"trusted":     3,
	"elite":       4,
}

// TierNames is the ordered list of tier names.
var TierNames = []string{"new", "emerging", "established", "trusted", "elite"}

// NumTiers is the count of tiers.
const NumTiers = 5

// Index returns the numeric index for a tier string, defaulting
// to the "established" index (2) for unknown tiers.
func Index(tier string) int {
	idx, ok := TierIndex[tier]
	if !ok {
		return TierIndex["established"]
	}
	return idx
}

// Scale computes base × (max/base)^(t/4) for the given tier.
//
// Properties:
//   - Scale("new") = base (exactly)
//   - Scale("elite") = max (exactly)
//   - Each tier step multiplies by the same constant ratio
//   - Unknown tiers default to "established" (index 2)
//
// Special cases:
//   - If base == 0 and max > 0: returns 0 for tier 0, interpolates for others
//   - If base == max: returns base for all tiers
//   - If base < 0 or max < 0: returns base (no scaling)
func Scale(base, max float64, tier string) float64 {
	t := float64(Index(tier))
	if base < 0 || max < 0 {
		return base
	}
	if base == 0 {
		if max == 0 {
			return 0
		}
		// Linear interpolation when base is 0 (can't take ratio)
		return max * t / float64(NumTiers-1)
	}
	if base == max {
		return base
	}
	ratio := max / base
	return base * math.Pow(ratio, t/float64(NumTiers-1))
}

// TierCurve defines a geometric progression from Base to Max across tiers.
// This is the universal config primitive: two numbers specify everything.
type TierCurve struct {
	Base float64 // value at tier "new" (index 0)
	Max  float64 // value at tier "elite" (index 4)
}

// At returns the curve value for the given tier.
func (c TierCurve) At(tier string) float64 {
	return Scale(c.Base, c.Max, tier)
}

// AtInt returns the curve value rounded to the nearest integer.
func (c TierCurve) AtInt(tier string) int {
	return int(math.Round(c.At(tier)))
}

// All returns values for all 5 tiers in order.
func (c TierCurve) All() [NumTiers]float64 {
	var out [NumTiers]float64
	for i, name := range TierNames {
		_ = i
		out[TierIndex[name]] = c.At(name)
	}
	return out
}
