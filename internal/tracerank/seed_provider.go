package tracerank

import (
	"context"
	"math"
	"strings"
	"time"

	"github.com/mbd888/alancoin/internal/registry"
)

// RegistrySeedProvider generates trust seed signals from agent registry data.
//
// Seed signals anchor the PageRank personalization vector. Agents without
// external trust signals start with zero seed — meaning self-dealing between
// zero-seed agents propagates zero trust regardless of transaction volume.
// This is the core Sybil-resistance property.
//
// The seed combines three signals:
//   - Age on network (max 0.40) — time builds trust, logarithmic
//   - Success rate (max 0.30) — reliable service delivery
//   - Activity proof (max 0.30) — has real transaction history
type RegistrySeedProvider struct {
	store registry.Store
	// verified holds manually verified agent addresses (lowercase).
	// Verified agents receive the full activity proof component.
	verified map[string]bool
}

// NewRegistrySeedProvider creates a seed provider backed by the agent registry.
func NewRegistrySeedProvider(store registry.Store) *RegistrySeedProvider {
	return &RegistrySeedProvider{
		store:    store,
		verified: make(map[string]bool),
	}
}

// WithVerifiedAgents sets the list of manually verified agent addresses.
func (p *RegistrySeedProvider) WithVerifiedAgents(addrs []string) *RegistrySeedProvider {
	for _, addr := range addrs {
		p.verified[strings.ToLower(addr)] = true
	}
	return p
}

// GetSeed returns a trust signal in [0, 1] for a single agent.
func (p *RegistrySeedProvider) GetSeed(ctx context.Context, address string) (float64, error) {
	address = strings.ToLower(address)

	agent, err := p.store.GetAgent(ctx, address)
	if err != nil {
		return 0, nil // Unknown agent → no seed
	}

	return p.computeSeed(agent), nil
}

// GetAllSeeds returns trust signals for all registered agents.
func (p *RegistrySeedProvider) GetAllSeeds(ctx context.Context) (map[string]float64, error) {
	agents, err := p.store.ListAgents(ctx, registry.AgentQuery{Limit: 10000})
	if err != nil {
		return nil, err
	}

	seeds := make(map[string]float64, len(agents))
	for _, agent := range agents {
		seeds[strings.ToLower(agent.Address)] = p.computeSeed(agent)
	}
	return seeds, nil
}

// computeSeed produces a [0, 1] trust signal from agent metadata.
func (p *RegistrySeedProvider) computeSeed(agent *registry.Agent) float64 {
	var seed float64

	// Component 1: Age on network (max 0.40)
	// Logarithmic: 1 day = 0, 7 days ≈ 0.14, 30 days ≈ 0.24, 365 days ≈ 0.40
	if !agent.CreatedAt.IsZero() {
		days := time.Since(agent.CreatedAt).Hours() / 24
		if days > 0 {
			// log10(days+1) / log10(366) gives [0, 1] over [0, 365]
			ageNorm := math.Min(1.0, math.Log10(days+1)/math.Log10(366))
			seed += 0.40 * ageNorm
		}
	}

	// Component 2: Success rate (max 0.30)
	// Requires minimum 5 transactions for reliability
	if agent.Stats.TransactionCount >= 5 {
		rate := agent.Stats.SuccessRate
		if rate > 1 {
			rate = 1
		}
		if rate < 0 {
			rate = 0
		}
		seed += 0.30 * rate
	}

	// Component 3: Activity proof / verification (max 0.30)
	if p.verified[strings.ToLower(agent.Address)] {
		// Verified agents receive the full component
		seed += 0.30
	} else if agent.Stats.TransactionCount > 0 {
		// Unverified but active: partial credit based on transaction count
		// 1 tx = 0.05, 10 tx = 0.10, 100+ tx = 0.20
		txNorm := math.Min(1.0, math.Log10(float64(agent.Stats.TransactionCount)+1)/2.0)
		seed += 0.20 * txNorm
	}

	// Clamp to [0, 1]
	if seed > 1 {
		seed = 1
	}
	if seed < 0 {
		seed = 0
	}

	return seed
}

// Compile-time check.
var _ SeedProvider = (*RegistrySeedProvider)(nil)
