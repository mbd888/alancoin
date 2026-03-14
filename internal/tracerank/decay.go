package tracerank

import "math"

// DecayFunction enumerates the available edge weight decay strategies.
// Each function maps (daysSinceLastTx) -> weight multiplier in [0, 1].
type DecayFunction int

const (
	// DecayNone disables temporal decay entirely (weight = 1.0 always).
	DecayNone DecayFunction = iota

	// DecayExponential applies exp(-rate * daysSince). This is the original
	// behavior extracted into a named function.
	// rate=0.03 gives ~50% weight after 23 days.
	DecayExponential

	// DecaySCurve applies a sigmoid that drops sharply around the half-life.
	// Better for "recent activity matters, old activity is nearly worthless."
	// Uses: 1 / (1 + exp(steepness * (daysSince - halfLife)))
	DecaySCurve

	// DecayThreshold applies full weight until a threshold number of days,
	// then drops to a residual fraction. Useful for "last 30 days matter,
	// before that counts for 10%."
	DecayThreshold
)

// ExponentialDecay returns exp(-rate * daysSince).
// When rate=0.03, the half-life is ~23 days.
func ExponentialDecay(daysSince, rate float64) float64 {
	if daysSince <= 0 || rate <= 0 {
		return 1.0
	}
	return math.Exp(-rate * daysSince)
}

// SCurveDecay returns a sigmoid decay that drops sharply around halfLife.
// The steepness parameter controls how sharp the transition is.
// Higher steepness = sharper cliff. Typical values: 0.1 (gradual) to 1.0 (sharp).
//
// Formula: 1 / (1 + exp(steepness * (daysSince - halfLife)))
func SCurveDecay(daysSince, halfLife, steepness float64) float64 {
	if halfLife <= 0 {
		return 1.0
	}
	if steepness <= 0 {
		steepness = 0.2 // sensible default
	}
	return 1.0 / (1.0 + math.Exp(steepness*(daysSince-halfLife)))
}

// ThresholdDecay returns 1.0 when daysSince < threshold, then drops to residual.
// Edges older than threshold days carry only `residual` fraction of their weight.
func ThresholdDecay(daysSince, threshold, residual float64) float64 {
	if threshold <= 0 {
		return 1.0
	}
	if daysSince < threshold {
		return 1.0
	}
	if residual < 0 {
		return 0
	}
	if residual > 1 {
		return 1.0
	}
	return residual
}

// applyDecay dispatches to the configured decay function.
// Returns the weight multiplier in [0, 1] for the given edge age.
//
// Backward compatibility: when DecayFunction is DecayNone (the zero value)
// but TemporalDecayRate > 0, exponential decay is used. This preserves the
// original behavior for existing configs that only set TemporalDecayRate.
func applyDecay(daysSince float64, cfg Config) float64 {
	switch cfg.DecayFunction {
	case DecayExponential:
		return ExponentialDecay(daysSince, cfg.TemporalDecayRate)
	case DecaySCurve:
		return SCurveDecay(daysSince, cfg.SCurveHalfLife, cfg.SCurveSteepness)
	case DecayThreshold:
		return ThresholdDecay(daysSince, cfg.ThresholdDays, cfg.ThresholdResidual)
	default:
		// DecayNone or unrecognized: fall back to exponential if rate is set
		// (backward compatibility with pre-DecayFunction configs).
		if cfg.TemporalDecayRate > 0 {
			return ExponentialDecay(daysSince, cfg.TemporalDecayRate)
		}
		return 1.0
	}
}
