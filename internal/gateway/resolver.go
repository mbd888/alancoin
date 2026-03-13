package gateway

import (
	"context"
	"fmt"
	"math/big"
	"sort"

	"github.com/mbd888/alancoin/internal/usdc"
)

// Resolver discovers and ranks service candidates.
type Resolver struct {
	registry RegistryProvider
	booster  DiscoveryBooster // flywheel: reputation-based discovery boost
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

// Resolve finds and ranks services for a proxy request.
// Returns up to MaxRetries candidates sorted by the given strategy.
func (r *Resolver) Resolve(ctx context.Context, req ProxyRequest, strategy, maxPerRequest string) ([]ServiceCandidate, error) {
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

	// Sort by strategy
	sortCandidates(filtered, strategy)

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

func sortCandidates(candidates []ServiceCandidate, strategy string) {
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

func valueScore(c ServiceCandidate) float64 {
	price, _ := usdc.Parse(c.Price)
	if price == nil || price.Sign() == 0 {
		return 0
	}
	// Use big.Float to avoid Int64() truncation on large values.
	priceF, _ := new(big.Float).SetInt(price).Float64()
	if priceF == 0 {
		return 0
	}
	// Weighted reputation per unit cost (higher = better deal).
	// price is in USDC base units (6 decimals), so divide by 1e6.
	// Reputation is weighted 70% and inverse-price 30% to favor trusted agents.
	repWeight := 0.7
	priceWeight := 0.3
	repComponent := repWeight * c.ReputationScore
	priceComponent := priceWeight * (100.0 / (priceF / 1e6)) // normalize: cheaper = higher score
	return repComponent + priceComponent
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
