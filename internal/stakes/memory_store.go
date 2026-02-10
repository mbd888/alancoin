package stakes

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"
)

// Compile-time assertion.
var _ Store = (*MemoryStore)(nil)

// MemoryStore is an in-memory Store implementation for demo/testing.
type MemoryStore struct {
	stakes        map[string]*Stake
	holdings      map[string]*Holding
	distributions map[string]*Distribution
	orders        map[string]*Order
	mu            sync.RWMutex
}

// NewMemoryStore creates a new in-memory stakes store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		stakes:        make(map[string]*Stake),
		holdings:      make(map[string]*Holding),
		distributions: make(map[string]*Distribution),
		orders:        make(map[string]*Order),
	}
}

// --- Stakes ---

func (m *MemoryStore) CreateStake(_ context.Context, stake *Stake) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *stake
	m.stakes[stake.ID] = &cp
	return nil
}

func (m *MemoryStore) GetStake(_ context.Context, id string) (*Stake, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.stakes[id]
	if !ok {
		return nil, ErrStakeNotFound
	}
	cp := *s
	return &cp, nil
}

func (m *MemoryStore) UpdateStake(_ context.Context, stake *Stake) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.stakes[stake.ID]; !ok {
		return ErrStakeNotFound
	}
	cp := *stake
	m.stakes[stake.ID] = &cp
	return nil
}

func (m *MemoryStore) ListByAgent(_ context.Context, agentAddr string) ([]*Stake, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	addr := strings.ToLower(agentAddr)
	var result []*Stake
	for _, s := range m.stakes {
		if strings.ToLower(s.AgentAddr) == addr {
			cp := *s
			result = append(result, &cp)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt.After(result[j].CreatedAt)
	})
	return result, nil
}

func (m *MemoryStore) ListOpen(_ context.Context, limit int) ([]*Stake, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []*Stake
	for _, s := range m.stakes {
		if s.Status == string(StakeStatusOpen) {
			cp := *s
			result = append(result, &cp)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt.After(result[j].CreatedAt)
	})
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (m *MemoryStore) ListDueForDistribution(_ context.Context, now time.Time, limit int) ([]*Stake, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var due []*Stake
	for _, s := range m.stakes {
		if s.Status != string(StakeStatusOpen) || s.Undistributed == "0" || s.Undistributed == "0.000000" {
			continue
		}
		freq := freqToDuration(s.DistributionFreq)
		if s.LastDistributedAt == nil || now.Sub(*s.LastDistributedAt) >= freq {
			cp := *s
			due = append(due, &cp)
		}
	}
	sort.Slice(due, func(i, j int) bool {
		return due[i].CreatedAt.Before(due[j].CreatedAt)
	})
	if len(due) > limit {
		due = due[:limit]
	}
	return due, nil
}

func (m *MemoryStore) GetAgentTotalShareBPS(_ context.Context, agentAddr string) (int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	addr := strings.ToLower(agentAddr)
	total := 0
	for _, s := range m.stakes {
		if strings.ToLower(s.AgentAddr) == addr && s.Status != string(StakeStatusClosed) {
			total += s.RevenueShareBPS
		}
	}
	return total, nil
}

// --- Holdings ---

func (m *MemoryStore) CreateHolding(_ context.Context, h *Holding) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *h
	m.holdings[h.ID] = &cp
	return nil
}

func (m *MemoryStore) GetHolding(_ context.Context, id string) (*Holding, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	h, ok := m.holdings[id]
	if !ok {
		return nil, ErrHoldingNotFound
	}
	cp := *h
	return &cp, nil
}

func (m *MemoryStore) UpdateHolding(_ context.Context, h *Holding) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.holdings[h.ID]; !ok {
		return ErrHoldingNotFound
	}
	cp := *h
	m.holdings[h.ID] = &cp
	return nil
}

func (m *MemoryStore) ListHoldingsByStake(_ context.Context, stakeID string) ([]*Holding, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []*Holding
	for _, h := range m.holdings {
		if h.StakeID == stakeID && h.Status != string(HoldingStatusLiquidated) {
			cp := *h
			result = append(result, &cp)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt.Before(result[j].CreatedAt)
	})
	return result, nil
}

func (m *MemoryStore) ListHoldingsByInvestor(_ context.Context, investorAddr string) ([]*Holding, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	addr := strings.ToLower(investorAddr)
	var result []*Holding
	for _, h := range m.holdings {
		if strings.ToLower(h.InvestorAddr) == addr {
			cp := *h
			result = append(result, &cp)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt.After(result[j].CreatedAt)
	})
	return result, nil
}

func (m *MemoryStore) GetHoldingByInvestorAndStake(_ context.Context, investorAddr, stakeID string) (*Holding, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	addr := strings.ToLower(investorAddr)
	for _, h := range m.holdings {
		if strings.ToLower(h.InvestorAddr) == addr && h.StakeID == stakeID && h.Status != string(HoldingStatusLiquidated) {
			cp := *h
			return &cp, nil
		}
	}
	return nil, ErrHoldingNotFound
}

// --- Distributions ---

func (m *MemoryStore) CreateDistribution(_ context.Context, d *Distribution) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *d
	m.distributions[d.ID] = &cp
	return nil
}

func (m *MemoryStore) ListDistributions(_ context.Context, stakeID string, limit int) ([]*Distribution, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []*Distribution
	for _, d := range m.distributions {
		if d.StakeID == stakeID {
			cp := *d
			result = append(result, &cp)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt.After(result[j].CreatedAt)
	})
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

// --- Orders ---

func (m *MemoryStore) CreateOrder(_ context.Context, o *Order) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *o
	m.orders[o.ID] = &cp
	return nil
}

func (m *MemoryStore) GetOrder(_ context.Context, id string) (*Order, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	o, ok := m.orders[id]
	if !ok {
		return nil, ErrOrderNotFound
	}
	cp := *o
	return &cp, nil
}

func (m *MemoryStore) UpdateOrder(_ context.Context, o *Order) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.orders[o.ID]; !ok {
		return ErrOrderNotFound
	}
	cp := *o
	m.orders[o.ID] = &cp
	return nil
}

func (m *MemoryStore) ListOrdersByStake(_ context.Context, stakeID string, status string, limit int) ([]*Order, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []*Order
	for _, o := range m.orders {
		if o.StakeID == stakeID {
			if status != "" && o.Status != status {
				continue
			}
			cp := *o
			result = append(result, &cp)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt.After(result[j].CreatedAt)
	})
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (m *MemoryStore) ListOrdersBySeller(_ context.Context, sellerAddr string, limit int) ([]*Order, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	addr := strings.ToLower(sellerAddr)
	var result []*Order
	for _, o := range m.orders {
		if strings.ToLower(o.SellerAddr) == addr {
			cp := *o
			result = append(result, &cp)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt.After(result[j].CreatedAt)
	})
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}
