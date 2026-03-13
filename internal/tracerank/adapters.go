package tracerank

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/mbd888/alancoin/internal/registry"
)

// RegistryTransactionSource extracts payment edges from the registry
// transaction history for TraceRank graph construction.
//
// It deduplicates transactions seen from both buyer and seller sides,
// aggregates per-pair volume, and filters to confirmed transactions only.
type RegistryTransactionSource struct {
	store registry.Store
}

// NewRegistryTransactionSource creates a transaction source backed by the registry.
func NewRegistryTransactionSource(store registry.Store) *RegistryTransactionSource {
	return &RegistryTransactionSource{store: store}
}

// GetPaymentEdges returns aggregated payment edges from registry transaction data.
// If since is non-zero, only transactions after that time are included.
func (r *RegistryTransactionSource) GetPaymentEdges(ctx context.Context, since time.Time) ([]PaymentEdge, error) {
	agents, err := r.store.ListAgents(ctx, registry.AgentQuery{Limit: 10000})
	if err != nil {
		return nil, err
	}

	// Collect all transactions, deduplicating by transaction ID.
	// Each transaction appears in both the buyer's and seller's lists,
	// so we track seen IDs to avoid double-counting.
	seen := make(map[string]bool)
	type txRecord struct {
		from      string
		to        string
		amount    float64
		createdAt time.Time
	}
	var records []txRecord

	for _, agent := range agents {
		txns, err := r.store.ListTransactions(ctx, agent.Address, 10000)
		if err != nil {
			continue // Skip agents with query errors
		}
		for _, tx := range txns {
			if seen[tx.ID] {
				continue
			}
			seen[tx.ID] = true

			// Only confirmed/completed transactions carry trust signal
			if tx.Status != "confirmed" && tx.Status != "completed" {
				continue
			}

			// Apply time filter
			if !since.IsZero() && tx.CreatedAt.Before(since) {
				continue
			}

			amount := parseAmount(tx.Amount)
			if amount <= 0 {
				continue
			}

			records = append(records, txRecord{
				from:      strings.ToLower(tx.From),
				to:        strings.ToLower(tx.To),
				amount:    amount,
				createdAt: tx.CreatedAt,
			})
		}
	}

	// Aggregate into payment edges per (from, to) pair
	type edgeKey struct{ from, to string }
	edgeMap := make(map[edgeKey]*PaymentEdge)

	for _, rec := range records {
		key := edgeKey{from: rec.from, to: rec.to}
		edge, ok := edgeMap[key]
		if !ok {
			edge = &PaymentEdge{From: rec.from, To: rec.to}
			edgeMap[key] = edge
		}
		edge.Volume += rec.amount
		edge.TxCount++
		if rec.createdAt.After(edge.LastTxAt) {
			edge.LastTxAt = rec.createdAt
		}
	}

	edges := make([]PaymentEdge, 0, len(edgeMap))
	for _, e := range edgeMap {
		edges = append(edges, *e)
	}
	return edges, nil
}

// parseAmount converts a USDC amount string to float64.
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

// Compile-time check.
var _ TransactionSource = (*RegistryTransactionSource)(nil)

// RegistryAgentInfoProvider implements AgentInfoProvider using the registry store.
// It maps registered agents to AgentInfo structs for seed signal computation.
type RegistryAgentInfoProvider struct {
	store registry.Store
}

// NewRegistryAgentInfoProvider creates an AgentInfoProvider backed by the registry.
func NewRegistryAgentInfoProvider(store registry.Store) *RegistryAgentInfoProvider {
	return &RegistryAgentInfoProvider{store: store}
}

// GetAllAgentInfo returns agent metadata for all registered agents.
func (r *RegistryAgentInfoProvider) GetAllAgentInfo(ctx context.Context) ([]AgentInfo, error) {
	agents, err := r.store.ListAgents(ctx, registry.AgentQuery{Limit: 1000})
	if err != nil {
		return nil, err
	}

	result := make([]AgentInfo, 0, len(agents))
	for _, agent := range agents {
		info := AgentInfo{
			Address:   strings.ToLower(agent.Address),
			CreatedAt: agent.CreatedAt,
			Verified:  false, // Default; can be extended with verification data
		}
		result = append(result, info)
	}

	return result, nil
}

// StoreScoreProvider implements ScoreProvider by delegating to a Store.
// This wraps the persistence layer to satisfy the ScoreProvider interface
// used by consumers that only need read access to scores.
type StoreScoreProvider struct {
	store Store
}

// NewStoreScoreProvider creates a ScoreProvider backed by a Store.
func NewStoreScoreProvider(store Store) *StoreScoreProvider {
	return &StoreScoreProvider{store: store}
}

// GetScore returns the latest TraceRank score for an agent.
func (s *StoreScoreProvider) GetScore(ctx context.Context, address string) (*AgentScore, error) {
	return s.store.GetScore(ctx, strings.ToLower(address))
}

// GetScores returns scores for multiple agents.
func (s *StoreScoreProvider) GetScores(ctx context.Context, addresses []string) (map[string]*AgentScore, error) {
	return s.store.GetScores(ctx, addresses)
}

// GetTopScores returns the top N agents by TraceRank score.
func (s *StoreScoreProvider) GetTopScores(ctx context.Context, limit int) ([]*AgentScore, error) {
	return s.store.GetTopScores(ctx, limit)
}
