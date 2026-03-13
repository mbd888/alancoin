package tracerank

import (
	"context"
	"strings"
	"sync"
	"time"
)

// AgentInfo holds the data needed to compute a seed signal.
type AgentInfo struct {
	Address   string
	CreatedAt time.Time
	Verified  bool // admin-verified or has performance guarantee
}

// AgentInfoProvider fetches agent metadata for seed computation.
type AgentInfoProvider interface {
	GetAllAgentInfo(ctx context.Context) ([]AgentInfo, error)
}

// TimeSeedProvider computes seed signals based on time-on-network
// and verification status. This is the default seed provider.
//
// Seed values:
//   - 0.0: brand new, no history
//   - 0.1: > 7 days on network
//   - 0.2: > 30 days on network
//   - 0.4: > 90 days on network
//   - 0.6: > 180 days on network
//   - +0.3: admin-verified (stacks with age)
//   - max: 1.0
//
// These thresholds ensure that Sybil agents created in bulk have
// near-zero seed and cannot bootstrap reputation through self-dealing.
type TimeSeedProvider struct {
	agents AgentInfoProvider

	mu    sync.RWMutex
	cache map[string]float64
	ttl   time.Time
}

// NewTimeSeedProvider creates a seed provider based on agent age and verification.
func NewTimeSeedProvider(agents AgentInfoProvider) *TimeSeedProvider {
	return &TimeSeedProvider{
		agents: agents,
		cache:  make(map[string]float64),
	}
}

// GetSeed returns the seed signal for a single agent.
func (p *TimeSeedProvider) GetSeed(ctx context.Context, address string) (float64, error) {
	address = strings.ToLower(address)

	p.mu.RLock()
	if time.Now().Before(p.ttl) {
		if s, ok := p.cache[address]; ok {
			p.mu.RUnlock()
			return s, nil
		}
	}
	p.mu.RUnlock()

	// Cache miss or expired — recompute all
	if _, err := p.GetAllSeeds(ctx); err != nil {
		return 0, err
	}

	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.cache[address], nil
}

// GetAllSeeds returns seed signals for all agents.
func (p *TimeSeedProvider) GetAllSeeds(ctx context.Context) (map[string]float64, error) {
	p.mu.RLock()
	if time.Now().Before(p.ttl) {
		result := make(map[string]float64, len(p.cache))
		for k, v := range p.cache {
			result[k] = v
		}
		p.mu.RUnlock()
		return result, nil
	}
	p.mu.RUnlock()

	agents, err := p.agents.GetAllAgentInfo(ctx)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	seeds := make(map[string]float64, len(agents))

	for _, a := range agents {
		addr := strings.ToLower(a.Address)
		seeds[addr] = computeTimeSeed(a, now)
	}

	p.mu.Lock()
	p.cache = seeds
	p.ttl = now.Add(5 * time.Minute) // cache for 5 minutes
	p.mu.Unlock()

	result := make(map[string]float64, len(seeds))
	for k, v := range seeds {
		result[k] = v
	}
	return result, nil
}

func computeTimeSeed(a AgentInfo, now time.Time) float64 {
	seed := 0.0

	if !a.CreatedAt.IsZero() {
		age := now.Sub(a.CreatedAt)
		switch {
		case age >= 180*24*time.Hour:
			seed = 0.6
		case age >= 90*24*time.Hour:
			seed = 0.4
		case age >= 30*24*time.Hour:
			seed = 0.2
		case age >= 7*24*time.Hour:
			seed = 0.1
		}
	}

	if a.Verified {
		seed += 0.3
	}

	if seed > 1.0 {
		seed = 1.0
	}

	return seed
}

// StaticSeedProvider returns fixed seed values. Useful for testing.
type StaticSeedProvider struct {
	Seeds map[string]float64
}

func (p *StaticSeedProvider) GetSeed(_ context.Context, address string) (float64, error) {
	return p.Seeds[strings.ToLower(address)], nil
}

func (p *StaticSeedProvider) GetAllSeeds(_ context.Context) (map[string]float64, error) {
	result := make(map[string]float64, len(p.Seeds))
	for k, v := range p.Seeds {
		result[strings.ToLower(k)] = v
	}
	return result, nil
}
