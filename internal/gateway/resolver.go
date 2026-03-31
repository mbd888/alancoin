package gateway

import (
	"context"
	"fmt"
	"math"
	"math/big"
	"sort"

	"github.com/mbd888/alancoin/internal/usdc"
)

// Resolver discovers and ranks service candidates.
type Resolver struct {
	registry     RegistryProvider
	booster      DiscoveryBooster     // flywheel: reputation-based discovery boost
	intelligence IntelligenceProvider // intelligence: credit-based discovery boost
}

// NewResolver creates a new service resolver.
func NewResolver(registry RegistryProvider) *Resolver {
	return &Resolver{registry: registry}
}

// WithDiscoveryBooster adds flywheel-based discovery score boosting.
func (r *Resolver) WithDiscoveryBooster(b DiscoveryBooster) *Resolver {
	r.booster = b
	return r
}

// WithIntelligenceRanker adds intelligence-based discovery boost.
// Agents with higher intelligence tiers get boosted in discovery results,
// directly closing the flywheel: better score → more visibility → more transactions.
func (r *Resolver) WithIntelligenceRanker(ip IntelligenceProvider) *Resolver {
	r.intelligence = ip
	return r
}

// Resolve finds and ranks services for a proxy request.
// Returns up to MaxRetries candidates sorted by the given strategy.
// budgetUtilization (0.0–1.0) is the fraction of session budget already spent;
// it is only used by the "budget" strategy and ignored by all others.
func (r *Resolver) Resolve(ctx context.Context, req ProxyRequest, strategy, maxPerRequest string, budgetUtilization float64) ([]ServiceCandidate, error) {
	// Use request-level maxPrice override or session maxPerRequest
	maxPrice := req.MaxPrice
	if maxPrice == "" {
		maxPrice = maxPerRequest
	}

	candidates, err := r.registry.ListServices(ctx, req.ServiceType, maxPrice)
	if err != nil {
		return nil, fmt.Errorf("registry query failed: %w", err)
	}

	// Filter out candidates without endpoints
	var filtered []ServiceCandidate
	for _, c := range candidates {
		if c.Endpoint != "" {
			filtered = append(filtered, c)
		}
	}

	if len(filtered) == 0 {
		return nil, ErrNoServiceAvailable
	}

	// Apply flywheel discovery boost: higher-reputation agents get a score
	// multiplier that improves their ranking. This closes the flywheel loop:
	// better reputation → higher discovery placement → more transactions.
	if r.booster != nil {
		for i := range filtered {
			tier := scoreTier(filtered[i].ReputationScore)
			filtered[i].ReputationScore = r.booster.BoostScore(ctx, tier, filtered[i].ReputationScore)
		}
	}

	// Apply intelligence-based discovery boost: agents with higher credit tiers
	// get an additional boost, creating switching costs (leave = lose ranking).
	if r.intelligence != nil {
		for i := range filtered {
			tier, _, err := r.intelligence.GetCreditTier(ctx, filtered[i].AgentAddress)
			if err == nil && tier != "" {
				filtered[i].ReputationScore += intelligenceDiscoveryBoost(tier)
			}
		}
	}

	// Sort by strategy
	sortCandidates(filtered, strategy, budgetUtilization)

	// Move preferred agent to front if specified
	if req.PreferAgent != "" {
		for i, c := range filtered {
			if c.AgentAddress == req.PreferAgent {
				// Swap to front
				filtered[0], filtered[i] = filtered[i], filtered[0]
				break
			}
		}
	}

	// Limit to MaxRetries
	if len(filtered) > MaxRetries {
		filtered = filtered[:MaxRetries]
	}

	return filtered, nil
}

func sortCandidates(candidates []ServiceCandidate, strategy string, budgetUtilization float64) {
	switch strategy {
	case "reputation":
		sort.Slice(candidates, func(i, j int) bool {
			return candidates[i].ReputationScore > candidates[j].ReputationScore
		})
	case "tracerank":
		sort.Slice(candidates, func(i, j int) bool {
			return candidates[i].TraceRankScore > candidates[j].TraceRankScore
		})
	case "best_value":
		sort.Slice(candidates, func(i, j int) bool {
			scoreI := valueScore(candidates[i])
			scoreJ := valueScore(candidates[j])
			return scoreI > scoreJ
		})
	case "budget":
		sort.Slice(candidates, func(i, j int) bool {
			scoreI := budgetValueScore(candidates[i], budgetUtilization)
			scoreJ := budgetValueScore(candidates[j], budgetUtilization)
			return scoreI > scoreJ
		})
	default: // "cheapest"
		sort.Slice(candidates, func(i, j int) bool {
			pi, _ := usdc.Parse(candidates[i].Price)
			pj, _ := usdc.Parse(candidates[j].Price)
			if pi == nil || pj == nil {
				return false
			}
			return pi.Cmp(pj) < 0
		})
	}
}

// valueScore computes a Cobb-Douglas utility for ranking service candidates
// by "best value" — combining reputation quality and price efficiency.
//
// The Cobb-Douglas function U = rep^α × (1/price)^(1-α) is the standard
// microeconomic way to combine two desirable attributes. It has key properties
// the old linear formula lacked:
//
//   - No singularity: when price=0 or rep=0, the score is 0 (not infinite)
//   - Scale-invariant: doubling all prices doesn't change relative rankings
//   - Diminishing returns: going from rep 80→90 matters less than 10→20
//   - The α parameter has clear meaning: fraction of preference on quality
//
// α = 0.65 slightly favors reputation over price, matching the platform's
// trust-first philosophy.
func valueScore(c ServiceCandidate) float64 {
	price, _ := usdc.Parse(c.Price)
	if price == nil || price.Sign() == 0 {
		return 0
	}

	rep := c.ReputationScore
	if rep <= 0 {
		return 0
	}

	// Convert price to dollars for the utility computation.
	priceF, _ := new(big.Float).SetInt(price).Float64()
	if priceF <= 0 {
		return 0
	}
	priceDollars := priceF / 1e6

	// Cobb-Douglas: U = rep^α × (1/price)^(1-α)
	// α = 0.65: 65% weight on reputation, 35% on price efficiency
	const alpha = 0.65
	return math.Pow(rep, alpha) * math.Pow(1.0/priceDollars, 1.0-alpha)
}

// budgetValueScore computes a Cobb-Douglas utility with a dynamic quality/cost
// tradeoff that adapts to budget utilization. When the session has plenty of
// budget remaining, it favors higher-quality (higher-reputation) providers.
// As the budget depletes, it progressively shifts toward cheaper providers.
//
// The alpha parameter slides linearly:
//
//	utilization 0%   → α = 0.80 (strongly prefer quality)
//	utilization 50%  → α = 0.575 (balanced)
//	utilization 100% → α = 0.35 (strongly prefer cheapness)
//
// This reuses the same Cobb-Douglas framework as valueScore but makes
// the preference dynamic rather than fixed.
func budgetValueScore(c ServiceCandidate, utilization float64) float64 {
	price, _ := usdc.Parse(c.Price)
	if price == nil || price.Sign() == 0 {
		return 0
	}

	rep := c.ReputationScore
	if rep <= 0 {
		return 0
	}

	priceF, _ := new(big.Float).SetInt(price).Float64()
	if priceF <= 0 {
		return 0
	}
	priceDollars := priceF / 1e6

	// Clamp utilization to [0, 1].
	if utilization < 0 {
		utilization = 0
	}
	if utilization > 1 {
		utilization = 1
	}

	// Dynamic alpha: 0.80 at 0% utilization → 0.35 at 100% utilization.
	alpha := 0.80 - 0.45*utilization

	return math.Pow(rep, alpha) * math.Pow(1.0/priceDollars, 1.0-alpha)
}

// intelligenceDiscoveryBoost returns a reputation score boost based on
// the agent's intelligence tier. This ensures high-intelligence agents
// appear earlier in discovery results regardless of sorting strategy.
func intelligenceDiscoveryBoost(tier string) float64 {
	switch tier {
	case "diamond":
		return 15.0 // +15 reputation points
	case "platinum":
		return 10.0
	case "gold":
		return 5.0
	case "silver":
		return 2.0
	default:
		return 0
	}
}

// adjustRPMByRisk adjusts the per-session rate limit based on intelligence tier.
// High-risk (low-tier) agents get reduced limits; high-credit agents get a boost.
// This creates a graduated throttle: bronze/unknown get halved, diamond gets 50% more.
func adjustRPMByRisk(tier string, baseRPM int) int {
	switch tier {
	case "diamond":
		return baseRPM + baseRPM/2 // +50%: trusted agents get headroom
	case "platinum":
		return baseRPM + baseRPM/4 // +25%
	case "gold":
		return baseRPM // unchanged
	case "silver":
		return baseRPM * 3 / 4 // -25%
	case "bronze":
		return baseRPM / 2 // -50%: untrusted agents throttled
	default:
		return baseRPM // unknown = no adjustment
	}
}

// scoreTier converts a 0-100 reputation score to a tier string.
// Mirrors reputation.getTier thresholds.
func scoreTier(score float64) string {
	switch {
	case score >= 80:
		return "elite"
	case score >= 60:
		return "trusted"
	case score >= 40:
		return "established"
	case score >= 20:
		return "emerging"
	default:
		return "new"
	}
}
