package contracts

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"
)

// MemoryStore is an in-memory contract store for demo/development mode.
type MemoryStore struct {
	contracts map[string]*Contract
	calls     map[string][]*ContractCall // contractID -> calls
	mu        sync.RWMutex
}

// NewMemoryStore creates a new in-memory contract store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		contracts: make(map[string]*Contract),
		calls:     make(map[string][]*ContractCall),
	}
}

func (m *MemoryStore) Create(ctx context.Context, contract *Contract) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.contracts[contract.ID] = contract
	return nil
}

func (m *MemoryStore) Get(ctx context.Context, id string) (*Contract, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	contract, ok := m.contracts[id]
	if !ok {
		return nil, ErrContractNotFound
	}
	// Return a copy to prevent races on the shared pointer
	cp := *contract
	return &cp, nil
}

func (m *MemoryStore) Update(ctx context.Context, contract *Contract) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.contracts[contract.ID]; !ok {
		return ErrContractNotFound
	}
	m.contracts[contract.ID] = contract
	return nil
}

func (m *MemoryStore) ListByAgent(ctx context.Context, agentAddr string, status string, limit int) ([]*Contract, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	addr := strings.ToLower(agentAddr)
	var result []*Contract
	for _, c := range m.contracts {
		if c.BuyerAddr == addr || c.SellerAddr == addr {
			if status != "" && string(c.Status) != status {
				continue
			}
			cp := *c
			result = append(result, &cp)
			if len(result) >= limit {
				break
			}
		}
	}
	return result, nil
}

func (m *MemoryStore) ListExpiring(ctx context.Context, before time.Time, limit int) ([]*Contract, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*Contract
	for _, c := range m.contracts {
		if c.Status == StatusActive && c.ExpiresAt != nil && c.ExpiresAt.Before(before) {
			cp := *c
			result = append(result, &cp)
			if len(result) >= limit {
				break
			}
		}
	}
	return result, nil
}

func (m *MemoryStore) ListActive(ctx context.Context, limit int) ([]*Contract, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*Contract
	for _, c := range m.contracts {
		if c.Status == StatusActive {
			cp := *c
			result = append(result, &cp)
			if len(result) >= limit {
				break
			}
		}
	}
	return result, nil
}

func (m *MemoryStore) RecordCall(ctx context.Context, call *ContractCall) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.calls[call.ContractID] = append(m.calls[call.ContractID], call)
	return nil
}

func (m *MemoryStore) ListCalls(ctx context.Context, contractID string, limit int) ([]*ContractCall, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	calls := m.calls[contractID]
	var result []*ContractCall
	for _, c := range calls {
		cp := *c
		result = append(result, &cp)
		if len(result) >= limit {
			break
		}
	}
	return result, nil
}

func (m *MemoryStore) GetRecentCalls(ctx context.Context, contractID string, windowSize int) ([]*ContractCall, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	calls := m.calls[contractID]
	if len(calls) == 0 {
		return nil, nil
	}

	// Sort by CreatedAt descending (most recent first)
	sorted := make([]*ContractCall, len(calls))
	copy(sorted, calls)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].CreatedAt.After(sorted[j].CreatedAt)
	})

	// Take the most recent windowSize calls
	if len(sorted) > windowSize {
		sorted = sorted[:windowSize]
	}

	// Return copies
	var result []*ContractCall
	for _, c := range sorted {
		cp := *c
		result = append(result, &cp)
	}
	return result, nil
}
