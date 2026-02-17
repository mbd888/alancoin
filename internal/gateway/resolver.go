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
}

// NewResolver creates a new service resolver.
func NewResolver(registry RegistryProvider) *Resolver {
	return &Resolver{registry: registry}
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
	// Reputation per unit cost (higher = better deal).
	// price is in USDC base units (6 decimals), so divide by 1e6.
	return c.ReputationScore / (priceF / 1e6)
}
