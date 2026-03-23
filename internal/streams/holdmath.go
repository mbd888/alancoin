// holdmath.go provides optimal stream hold computation using Poisson statistics.
//
// A streaming micropayment is a compound Poisson process:
//   - Ticks arrive at rate λ (ticks per second)
//   - Each tick costs p (price per tick)
//   - Total spend after T seconds: S(T) = N(T) × p where N(T) ~ Poisson(λT)
//
// The optimal hold amount H* for confidence level α is:
//
//	H* = p × F⁻¹(α, λT)
//
// where F⁻¹ is the inverse Poisson CDF. This means: with probability α,
// the stream will NOT exhaust the hold before time T.
//
// Example: service ticks at 2/sec, $0.001/tick, for 5 min, 99% confidence:
//
//	λT = 2 × 300 = 600 ticks expected
//	F⁻¹(0.99, 600) ≈ 658 ticks (99th percentile of Poisson(600))
//	H* = $0.001 × 658 = $0.658
//
// Without this, buyers must guess. Too low → hold exhaustion (service stops).
// Too high → capital locked unnecessarily.
package streams

import "math"

// RecommendHold computes the optimal hold amount for a streaming payment.
//
// Parameters:
//   - pricePerTick: cost of each tick in USDC (as float64)
//   - tickRate: expected ticks per second (λ)
//   - durationSec: expected service duration in seconds (T)
//   - confidence: probability that the hold won't be exhausted (e.g., 0.95)
//
// Returns the recommended hold amount in USDC.
// Returns 0 if any input is invalid.
func RecommendHold(pricePerTick, tickRate, durationSec, confidence float64) float64 {
	if pricePerTick <= 0 || tickRate <= 0 || durationSec <= 0 {
		return 0
	}
	if confidence <= 0 || confidence >= 1 {
		return 0
	}

	// Expected number of ticks
	lambda := tickRate * durationSec

	// Find the quantile of Poisson(lambda) at the given confidence level.
	// This is the smallest k such that P(N ≤ k) ≥ confidence.
	k := poissonQuantile(lambda, confidence)

	return pricePerTick * float64(k)
}

// ExpectedCost returns the expected total cost: E[S] = λ × T × p.
func ExpectedCost(pricePerTick, tickRate, durationSec float64) float64 {
	if pricePerTick <= 0 || tickRate <= 0 || durationSec <= 0 {
		return 0
	}
	return pricePerTick * tickRate * durationSec
}

// HoldEfficiency returns what fraction of the hold is expected to be used.
// efficiency = ExpectedCost / holdAmount. Values near 1.0 mean tight holds
// (risk of exhaustion), values near 0.5 mean loose holds (capital inefficient).
func HoldEfficiency(holdAmount, pricePerTick, tickRate, durationSec float64) float64 {
	if holdAmount <= 0 {
		return 0
	}
	expected := ExpectedCost(pricePerTick, tickRate, durationSec)
	if expected <= 0 {
		return 0
	}
	eff := expected / holdAmount
	if eff > 1 {
		eff = 1
	}
	return eff
}

// poissonQuantile returns the smallest integer k such that
// P(X ≤ k) ≥ p where X ~ Poisson(lambda).
//
// Uses the CDF summation: P(X ≤ k) = Σ_{i=0}^{k} e^{-λ} × λ^i / i!
//
// For large lambda, uses the normal approximation:
// X ≈ Normal(λ, λ) → quantile ≈ λ + z_p × √λ
func poissonQuantile(lambda, p float64) int {
	if lambda <= 0 {
		return 0
	}

	// For large lambda (> 100), use normal approximation for speed
	if lambda > 100 {
		z := normalQuantile(p)
		return int(math.Ceil(lambda + z*math.Sqrt(lambda)))
	}

	// Exact CDF summation for small lambda
	cdf := 0.0
	logLambda := math.Log(lambda)
	logFactorial := 0.0 // log(0!) = 0

	for k := 0; k <= int(lambda*3)+100; k++ {
		if k > 0 {
			logFactorial += math.Log(float64(k))
		}
		// P(X = k) = exp(-lambda + k*log(lambda) - log(k!))
		logProb := -lambda + float64(k)*logLambda - logFactorial
		cdf += math.Exp(logProb)

		if cdf >= p {
			return k
		}
	}

	// Fallback (shouldn't reach here for valid inputs)
	return int(math.Ceil(lambda + 3*math.Sqrt(lambda)))
}

// normalQuantile returns the z-value for the given probability using
// the rational approximation (Abramowitz & Stegun, formula 26.2.23).
// Accurate to ~4.5e-4 for 0 < p < 1.
func normalQuantile(p float64) float64 {
	if p <= 0 || p >= 1 {
		return 0
	}
	if p == 0.5 {
		return 0
	}

	// Work with the tail probability
	if p > 0.5 {
		return -normalQuantile(1 - p)
	}

	t := math.Sqrt(-2 * math.Log(p))

	// Rational approximation coefficients
	c0 := 2.515517
	c1 := 0.802853
	c2 := 0.010328
	d1 := 1.432788
	d2 := 0.189269
	d3 := 0.001308

	return -(t - (c0+c1*t+c2*t*t)/(1+d1*t+d2*t*t+d3*t*t*t))
}
