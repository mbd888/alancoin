package reputation

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mbd888/alancoin/internal/registry"
)

// disputeRecord tracks dispute/confirm outcomes for an agent.
type disputeRecord struct {
	Confirmed int
	Disputed  int
}

// RegistryProvider implements MetricsProvider using the registry store.
// It also implements escrow.ReputationImpactor for dispute tracking.
type RegistryProvider struct {
	store    registry.Store
	disputes sync.Map // address → *disputeRecord
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
// If dispute data is available, a penalty is applied for high dispute rates.
func (p *RegistryProvider) GetScore(ctx context.Context, address string) (float64, string, error) {
	metrics, err := p.GetAgentMetrics(ctx, address)
	if err != nil {
		return 0, string(TierNew), err
	}
	calc := NewCalculator()
	score := calc.Calculate(address, *metrics)

	// Apply dispute penalty: high dispute rates reduce the score.
	// >50% dispute rate → up to 30% score reduction.
	dr := p.DisputeRate(address)
	if dr > 0.1 {
		// Linear penalty: 10% dispute rate = 0 penalty, 60%+ = 30% reduction
		penalty := (dr - 0.1) * 0.6 // maxes at 0.3 when dr=0.6
		if penalty > 0.3 {
			penalty = 0.3
		}
		score.Score *= (1.0 - penalty)
		score.Tier = getTier(score.Score)
	}

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
			// Only count finalized transactions (not pending/unknown)
			switch tx.Status {
			case "confirmed", "completed":
				m.TotalTransactions++
				m.SuccessfulTxns++
				m.TotalVolumeUSD += parseAmount(tx.Amount)
			case "failed", "reverted":
				m.TotalTransactions++
				m.FailedTxns++
			default:
				// Pending or unknown - skip entirely
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

// RecordDispute records an escrow dispute or confirmation outcome for
// the given seller address. This data is factored into reputation scoring
// as a penalty for high dispute rates.
func (p *RegistryProvider) RecordDispute(_ context.Context, sellerAddr string, outcome string, _ string) error {
	sellerAddr = strings.ToLower(sellerAddr)
	val, _ := p.disputes.LoadOrStore(sellerAddr, &disputeRecord{})
	rec := val.(*disputeRecord)
	switch outcome {
	case "confirmed":
		rec.Confirmed++
	case "disputed":
		rec.Disputed++
	}
	return nil
}

// DisputeRate returns the fraction of escrow outcomes that were disputes
// for the given address. Returns 0 if no data is available.
func (p *RegistryProvider) DisputeRate(address string) float64 {
	val, ok := p.disputes.Load(strings.ToLower(address))
	if !ok {
		return 0
	}
	rec := val.(*disputeRecord)
	total := rec.Confirmed + rec.Disputed
	if total == 0 {
		return 0
	}
	return float64(rec.Disputed) / float64(total)
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
