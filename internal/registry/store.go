package registry

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mbd888/alancoin/internal/wallet"
)

// -----------------------------------------------------------------------------
// Store Interface (swap implementations later)
// -----------------------------------------------------------------------------

// Store defines the persistence interface for the registry
type Store interface {
	// Agents
	CreateAgent(ctx context.Context, agent *Agent) error
	GetAgent(ctx context.Context, address string) (*Agent, error)
	UpdateAgent(ctx context.Context, agent *Agent) error
	ListAgents(ctx context.Context, query AgentQuery) ([]*Agent, error)
	DeleteAgent(ctx context.Context, address string) error

	// Services
	AddService(ctx context.Context, agentAddress string, service *Service) error
	UpdateService(ctx context.Context, agentAddress string, service *Service) error
	RemoveService(ctx context.Context, agentAddress, serviceID string) error
	ListServices(ctx context.Context, query AgentQuery) ([]ServiceListing, error)

	// Transactions (the data moat)
	RecordTransaction(ctx context.Context, tx *Transaction) error
	GetTransaction(ctx context.Context, id string) (*Transaction, error)
	ListTransactions(ctx context.Context, agentAddress string, limit int) ([]*Transaction, error)
	GetRecentTransactions(ctx context.Context, limit int) ([]*Transaction, error) // For public feed
	UpdateAgentStats(ctx context.Context, address string, fn func(*AgentStats)) error

	// Stats
	GetNetworkStats(ctx context.Context) (*NetworkStats, error)
}

// NetworkStats tracks overall network health
type NetworkStats struct {
	TotalAgents       int64     `json:"totalAgents"`
	TotalServices     int64     `json:"totalServices"`
	TotalTransactions int64     `json:"totalTransactions"`
	TotalVolume       string    `json:"totalVolume"` // USDC
	UpdatedAt         time.Time `json:"updatedAt"`
}

// -----------------------------------------------------------------------------
// In-Memory Store (for MVP, swap to Postgres later)
// -----------------------------------------------------------------------------

// MemoryStore is a thread-safe in-memory implementation
type MemoryStore struct {
	mu           sync.RWMutex
	agents       map[string]*Agent       // address -> agent
	transactions map[string]*Transaction // id -> transaction
	stats        NetworkStats
}

// NewMemoryStore creates a new in-memory store
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		agents:       make(map[string]*Agent),
		transactions: make(map[string]*Transaction),
		stats: NetworkStats{
			TotalVolume: "0",
			UpdatedAt:   time.Now(),
		},
	}
}

// Compile-time interface check
var _ Store = (*MemoryStore)(nil)

// -----------------------------------------------------------------------------
// Agent Operations
// -----------------------------------------------------------------------------

func (m *MemoryStore) CreateAgent(ctx context.Context, agent *Agent) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	addr := strings.ToLower(agent.Address)
	if _, exists := m.agents[addr]; exists {
		return ErrAgentExists
	}

	// Normalize
	agent.Address = addr
	agent.CreatedAt = time.Now()
	agent.UpdatedAt = time.Now()
	if agent.Services == nil {
		agent.Services = []Service{}
	}
	agent.Stats = AgentStats{
		TotalReceived: "0",
		TotalSent:     "0",
		SuccessRate:   1.0, // Start optimistic
	}

	m.agents[addr] = agent
	m.stats.TotalAgents++
	m.stats.UpdatedAt = time.Now()

	return nil
}

func (m *MemoryStore) GetAgent(ctx context.Context, address string) (*Agent, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	agent, exists := m.agents[strings.ToLower(address)]
	if !exists {
		return nil, ErrAgentNotFound
	}

	// Return a copy to prevent mutation
	copy := *agent
	return &copy, nil
}

func (m *MemoryStore) UpdateAgent(ctx context.Context, agent *Agent) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	addr := strings.ToLower(agent.Address)
	if _, exists := m.agents[addr]; !exists {
		return ErrAgentNotFound
	}

	agent.Address = addr
	agent.UpdatedAt = time.Now()
	m.agents[addr] = agent

	return nil
}

func (m *MemoryStore) ListAgents(ctx context.Context, query AgentQuery) ([]*Agent, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if query.Limit == 0 {
		query.Limit = 100
	}

	var results []*Agent

	for _, agent := range m.agents {
		// Filter by service type if specified
		if query.ServiceType != "" {
			hasType := false
			for _, svc := range agent.Services {
				if svc.Type == query.ServiceType && (query.Active == nil || *query.Active == svc.Active) {
					hasType = true
					break
				}
			}
			if !hasType {
				continue
			}
		}

		copy := *agent
		results = append(results, &copy)
	}

	// Sort by transaction count (most active first)
	sort.Slice(results, func(i, j int) bool {
		return results[i].Stats.TransactionCount > results[j].Stats.TransactionCount
	})

	// Apply pagination
	if query.Offset >= len(results) {
		return []*Agent{}, nil
	}
	end := query.Offset + query.Limit
	if end > len(results) {
		end = len(results)
	}

	return results[query.Offset:end], nil
}

func (m *MemoryStore) DeleteAgent(ctx context.Context, address string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	addr := strings.ToLower(address)
	if _, exists := m.agents[addr]; !exists {
		return ErrAgentNotFound
	}

	delete(m.agents, addr)
	m.stats.TotalAgents--
	m.stats.UpdatedAt = time.Now()

	return nil
}

// -----------------------------------------------------------------------------
// Service Operations
// -----------------------------------------------------------------------------

func (m *MemoryStore) AddService(ctx context.Context, agentAddress string, service *Service) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	addr := strings.ToLower(agentAddress)
	agent, exists := m.agents[addr]
	if !exists {
		return ErrAgentNotFound
	}

	// Generate service ID if not set
	if service.ID == "" {
		service.ID = generateID()
	}
	service.Active = true

	agent.Services = append(agent.Services, *service)
	agent.UpdatedAt = time.Now()
	m.stats.TotalServices++
	m.stats.UpdatedAt = time.Now()

	return nil
}

func (m *MemoryStore) UpdateService(ctx context.Context, agentAddress string, service *Service) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	addr := strings.ToLower(agentAddress)
	agent, exists := m.agents[addr]
	if !exists {
		return ErrAgentNotFound
	}

	for i, svc := range agent.Services {
		if svc.ID == service.ID {
			agent.Services[i] = *service
			agent.UpdatedAt = time.Now()
			return nil
		}
	}

	return ErrServiceNotFound
}

func (m *MemoryStore) RemoveService(ctx context.Context, agentAddress, serviceID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	addr := strings.ToLower(agentAddress)
	agent, exists := m.agents[addr]
	if !exists {
		return ErrAgentNotFound
	}

	for i, svc := range agent.Services {
		if svc.ID == serviceID {
			agent.Services = append(agent.Services[:i], agent.Services[i+1:]...)
			agent.UpdatedAt = time.Now()
			m.stats.TotalServices--
			m.stats.UpdatedAt = time.Now()
			return nil
		}
	}

	return ErrServiceNotFound
}

func (m *MemoryStore) ListServices(ctx context.Context, query AgentQuery) ([]ServiceListing, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if query.Limit == 0 {
		query.Limit = 100
	}

	var results []ServiceListing

	for _, agent := range m.agents {
		for _, svc := range agent.Services {
			// Filter by type
			if query.ServiceType != "" && svc.Type != query.ServiceType {
				continue
			}

			// Filter by active
			if query.Active != nil && *query.Active != svc.Active {
				continue
			}

			// Filter by price range
			if query.MinPrice != "" {
				minAmount, _ := wallet.ParseUSDC(query.MinPrice)
				svcAmount, _ := wallet.ParseUSDC(svc.Price)
				if minAmount != nil && svcAmount != nil && svcAmount.Cmp(minAmount) < 0 {
					continue
				}
			}
			if query.MaxPrice != "" {
				maxAmount, _ := wallet.ParseUSDC(query.MaxPrice)
				svcAmount, _ := wallet.ParseUSDC(svc.Price)
				if maxAmount != nil && svcAmount != nil && svcAmount.Cmp(maxAmount) > 0 {
					continue
				}
			}

			results = append(results, ServiceListing{
				Service:      svc,
				AgentAddress: agent.Address,
				AgentName:    agent.Name,
			})
		}
	}

	// Sort by price (cheapest first)
	sort.Slice(results, func(i, j int) bool {
		priceI, _ := wallet.ParseUSDC(results[i].Price)
		priceJ, _ := wallet.ParseUSDC(results[j].Price)
		if priceI == nil || priceJ == nil {
			return false
		}
		return priceI.Cmp(priceJ) < 0
	})

	// Apply pagination
	if query.Offset >= len(results) {
		return []ServiceListing{}, nil
	}
	end := query.Offset + query.Limit
	if end > len(results) {
		end = len(results)
	}

	return results[query.Offset:end], nil
}

// -----------------------------------------------------------------------------
// Transaction Operations (the data moat)
// -----------------------------------------------------------------------------

func (m *MemoryStore) RecordTransaction(ctx context.Context, tx *Transaction) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if tx.ID == "" {
		tx.ID = generateID()
	}
	tx.CreatedAt = time.Now()

	m.transactions[tx.ID] = tx
	m.stats.TotalTransactions++

	// Update volume
	if tx.Status == "confirmed" {
		currentVolume, _ := wallet.ParseUSDC(m.stats.TotalVolume)
		txAmount, _ := wallet.ParseUSDC(tx.Amount)
		if currentVolume != nil && txAmount != nil {
			currentVolume.Add(currentVolume, txAmount)
			m.stats.TotalVolume = wallet.FormatUSDC(currentVolume)
		}
	}

	m.stats.UpdatedAt = time.Now()

	return nil
}

func (m *MemoryStore) GetTransaction(ctx context.Context, id string) (*Transaction, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	tx, exists := m.transactions[id]
	if !exists {
		return nil, fmt.Errorf("transaction not found")
	}

	copy := *tx
	return &copy, nil
}

func (m *MemoryStore) ListTransactions(ctx context.Context, agentAddress string, limit int) ([]*Transaction, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if limit == 0 {
		limit = 100
	}

	addr := strings.ToLower(agentAddress)
	var results []*Transaction

	for _, tx := range m.transactions {
		if strings.ToLower(tx.From) == addr || strings.ToLower(tx.To) == addr {
			copy := *tx
			results = append(results, &copy)
		}
	}

	// Sort by time (newest first)
	sort.Slice(results, func(i, j int) bool {
		return results[i].CreatedAt.After(results[j].CreatedAt)
	})

	if len(results) > limit {
		results = results[:limit]
	}

	return results, nil
}

// GetRecentTransactions returns recent transactions across ALL agents (for public feed)
func (m *MemoryStore) GetRecentTransactions(ctx context.Context, limit int) ([]*Transaction, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if limit == 0 {
		limit = 50
	}

	var results []*Transaction
	for _, tx := range m.transactions {
		copy := *tx
		results = append(results, &copy)
	}

	// Sort by time (newest first)
	sort.Slice(results, func(i, j int) bool {
		return results[i].CreatedAt.After(results[j].CreatedAt)
	})

	if len(results) > limit {
		results = results[:limit]
	}

	return results, nil
}

func (m *MemoryStore) UpdateAgentStats(ctx context.Context, address string, fn func(*AgentStats)) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	addr := strings.ToLower(address)
	agent, exists := m.agents[addr]
	if !exists {
		return ErrAgentNotFound
	}

	fn(&agent.Stats)
	agent.UpdatedAt = time.Now()

	return nil
}

func (m *MemoryStore) GetNetworkStats(ctx context.Context) (*NetworkStats, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	copy := m.stats
	return &copy, nil
}
