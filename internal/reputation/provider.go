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

	// Get all transactions involving this agent
	txns, err := p.store.ListTransactions(ctx, address, 10000) // Get all
	if err != nil {
		return nil, err
	}

	return p.calculateMetrics(agent, txns), nil
}

// GetAllAgentMetrics fetches metrics for all agents
func (p *RegistryProvider) GetAllAgentMetrics(ctx context.Context) (map[string]*Metrics, error) {
	// Get all agents
	agents, err := p.store.ListAgents(ctx, registry.AgentQuery{Limit: 10000})
	if err != nil {
		return nil, err
	}

	result := make(map[string]*Metrics)
	for _, agent := range agents {
		txns, err := p.store.ListTransactions(ctx, agent.Address, 10000)
		if err != nil {
			continue // Skip agents with errors
		}
		result[agent.Address] = p.calculateMetrics(agent, txns)
	}

	return result, nil
}

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

	// Track unique counterparties
	counterparties := make(map[string]bool)

	for _, tx := range txns {
		m.TotalTransactions++

		// Parse amount (stored as string like "1.50")
		amount := parseAmount(tx.Amount)
		m.TotalVolumeUSD += amount

		// Track success/failure
		if tx.Status == "confirmed" || tx.Status == "completed" {
			m.SuccessfulTxns++
		} else if tx.Status == "failed" || tx.Status == "reverted" {
			m.FailedTxns++
		} else {
			// Pending or unknown - count as successful for now
			m.SuccessfulTxns++
		}

		// Track counterparties (the other side of the txn)
		if strings.EqualFold(tx.From, agent.Address) {
			counterparties[strings.ToLower(tx.To)] = true
		} else {
			counterparties[strings.ToLower(tx.From)] = true
		}

		// Track last active
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
