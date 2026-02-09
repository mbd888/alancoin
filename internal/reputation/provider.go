package reputation

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/mbd888/alancoin/internal/registry"
)

// RegistryProvider implements MetricsProvider using the registry store
type RegistryProvider struct {
	store registry.Store
}

// NewRegistryProvider creates a provider backed by the registry
func NewRegistryProvider(store registry.Store) *RegistryProvider {
	return &RegistryProvider{store: store}
}

// GetAgentMetrics fetches metrics for a single agent
func (p *RegistryProvider) GetAgentMetrics(ctx context.Context, address string) (*Metrics, error) {
	address = strings.ToLower(address)

	// Get agent info
	agent, err := p.store.GetAgent(ctx, address)
	if err != nil {
		return nil, err
	}

	// Get transactions involving this agent (capped for memory safety)
	txns, err := p.store.ListTransactions(ctx, address, 1000)
	if err != nil {
		return nil, err
	}

	return p.calculateMetrics(agent, txns), nil
}

// GetAllAgentMetrics fetches metrics for all agents
func (p *RegistryProvider) GetAllAgentMetrics(ctx context.Context) (map[string]*Metrics, error) {
	// Get all agents
	agents, err := p.store.ListAgents(ctx, registry.AgentQuery{Limit: 1000})
	if err != nil {
		return nil, err
	}

	result := make(map[string]*Metrics)
	for _, agent := range agents {
		txns, err := p.store.ListTransactions(ctx, agent.Address, 1000)
		if err != nil {
			continue // Skip agents with errors
		}
		result[agent.Address] = p.calculateMetrics(agent, txns)
	}

	return result, nil
}

// GetScore returns the reputation score and tier for a single agent.
// Implements registry.ReputationProvider.
func (p *RegistryProvider) GetScore(ctx context.Context, address string) (float64, string, error) {
	metrics, err := p.GetAgentMetrics(ctx, address)
	if err != nil {
		return 0, string(TierNew), err
	}
	calc := NewCalculator()
	score := calc.Calculate(address, *metrics)
	return score.Score, string(score.Tier), nil
}

// maxTxPerCounterparty caps how many transactions with the same counterparty
// count toward reputation. This prevents wash-trading between colluding agents.
const maxTxPerCounterparty = 5

func (p *RegistryProvider) calculateMetrics(agent *registry.Agent, txns []*registry.Transaction) *Metrics {
	m := &Metrics{
		FirstSeen: agent.CreatedAt,
	}

	// Calculate days on network
	if !agent.CreatedAt.IsZero() {
		m.DaysOnNetwork = int(time.Since(agent.CreatedAt).Hours() / 24)
		if m.DaysOnNetwork < 1 {
			m.DaysOnNetwork = 1 // At least 1 day
		}
	}

	// Track unique counterparties and per-counterparty tx counts
	counterparties := make(map[string]bool)
	counterpartyCounts := make(map[string]int)

	for _, tx := range txns {
		// Determine counterparty
		var counterparty string
		if strings.EqualFold(tx.From, agent.Address) {
			counterparty = strings.ToLower(tx.To)
		} else {
			counterparty = strings.ToLower(tx.From)
		}
		counterparties[counterparty] = true

		// Cap per-counterparty contribution to prevent wash-trading.
		// Only the first maxTxPerCounterparty transactions with each
		// counterparty count toward reputation metrics.
		counterpartyCounts[counterparty]++
		capped := counterpartyCounts[counterparty] > maxTxPerCounterparty

		if !capped {
			m.TotalTransactions++

			// Parse amount (stored as string like "1.50")
			amount := parseAmount(tx.Amount)
			m.TotalVolumeUSD += amount

			// Track success/failure
			switch tx.Status {
			case "confirmed", "completed":
				m.SuccessfulTxns++
			case "failed", "reverted":
				m.FailedTxns++
			default:
				// Pending or unknown - do not count as successful or failed
				m.TotalTransactions--
			}
		}

		// Track last active (always, regardless of cap)
		if tx.CreatedAt.After(m.LastActive) {
			m.LastActive = tx.CreatedAt
		}
	}

	m.UniqueCounterparties = len(counterparties)

	return m
}

func parseAmount(s string) float64 {
	if s == "" {
		return 0
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return f
}
