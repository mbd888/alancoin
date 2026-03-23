package supervisor

import (
	"math"
	"math/big"
	"time"
)

// EWMA implements an exponentially-weighted moving average for spend rate
// estimation. Unlike fixed-window counters that store O(events) per window,
// EWMA uses O(1) memory and O(1) per update.
//
// The EWMA tracks an estimated "spend per time constant τ". When a new
// event arrives, the estimate decays by e^(-Δt/τ) and adds the new amount.
// The resulting value approximates "how much was spent in the last τ seconds",
// with older events contributing exponentially less.
//
// This eliminates:
// - The cliff effect (spending $100 at t-61s vs t-59s in a 60s window)
// - O(n) eviction cost per event
// - O(n) memory for event storage per window
//
// Mathematical properties:
// - Effective half-life = τ × ln(2) ≈ 0.693τ
// - 95% of the weight is on the last 3τ of events
// - For Poisson arrivals with rate λ and mean amount μ, EWMA → λμτ
type EWMA struct {
	Tau      time.Duration // time constant
	Value    float64       // current estimated spend in the last τ seconds
	LastTime time.Time     // time of last update
}

// NewEWMA creates an EWMA with the given time constant.
func NewEWMA(tau time.Duration) *EWMA {
	return &EWMA{
		Tau: tau,
	}
}

// Update decays the current value and adds a new amount.
// The decay factor is e^(-Δt/τ), ensuring smooth exponential weighting.
func (e *EWMA) Update(amount float64, now time.Time) {
	if e.LastTime.IsZero() {
		e.Value = amount
		e.LastTime = now
		return
	}

	dt := now.Sub(e.LastTime).Seconds()
	if dt < 0 {
		dt = 0 // clock skew protection
	}

	tau := e.Tau.Seconds()
	if tau <= 0 {
		tau = 1 // safety: avoid division by zero
	}

	// Decay existing value, add new amount
	decay := math.Exp(-dt / tau)
	e.Value = e.Value*decay + amount
	e.LastTime = now
}

// Estimate returns the current EWMA value decayed to the given time.
// This is a read-only operation that doesn't modify state.
func (e *EWMA) Estimate(now time.Time) float64 {
	if e.LastTime.IsZero() {
		return 0
	}

	dt := now.Sub(e.LastTime).Seconds()
	if dt < 0 {
		dt = 0
	}

	tau := e.Tau.Seconds()
	if tau <= 0 {
		tau = 1
	}

	return e.Value * math.Exp(-dt/tau)
}

// EstimateBigInt returns the EWMA estimate as a *big.Int (USDC 6-decimal).
func (e *EWMA) EstimateBigInt(now time.Time) *big.Int {
	v := e.Estimate(now)
	if v < 0 {
		v = 0
	}
	return big.NewInt(int64(math.Round(v)))
}

// EWMACascade holds multiple EWMAs at different time scales.
// This provides a "wavelet-like" decomposition of the spending signal:
// short τ catches bursts, long τ catches sustained spend.
//
// The cascade uses a geometric progression of time constants:
//
//	τ₀ = 1min  (burst detection)
//	τ₁ = 5min  (short-term rate)
//	τ₂ = 1hr   (velocity limit enforcement)
//
// This matches the old 3-window system in semantics but with O(1) cost.
type EWMACascade struct {
	Scales [3]*EWMA // indexed 0=1min, 1=5min, 2=1hr
}

// NewEWMACascade creates a cascade with the standard time constants.
func NewEWMACascade() *EWMACascade {
	return &EWMACascade{
		Scales: [3]*EWMA{
			NewEWMA(1 * time.Minute),
			NewEWMA(5 * time.Minute),
			NewEWMA(1 * time.Hour),
		},
	}
}

// Update feeds a new spending event into all scales.
func (c *EWMACascade) Update(amount float64, now time.Time) {
	for _, e := range c.Scales {
		e.Update(amount, now)
	}
}

// Estimates returns the current EWMA values at all scales as big.Int.
func (c *EWMACascade) Estimates(now time.Time) [3]*big.Int {
	var out [3]*big.Int
	for i, e := range c.Scales {
		out[i] = e.EstimateBigInt(now)
	}
	return out
}
