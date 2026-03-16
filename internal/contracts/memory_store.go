package contracts

import (
	"context"
	"sync"
)

// MemoryStore is an in-memory contract store for demo/development.
type MemoryStore struct {
	contracts map[string]*Contract
	mu        sync.RWMutex
}

// NewMemoryStore creates a new in-memory contract store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		contracts: make(map[string]*Contract),
	}
}

func (m *MemoryStore) Create(ctx context.Context, c *Contract) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.contracts[c.ID] = deepCopyContract(c)
	return nil
}

func (m *MemoryStore) Get(ctx context.Context, id string) (*Contract, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	c, ok := m.contracts[id]
	if !ok {
		return nil, ErrContractNotFound
	}
	return deepCopyContract(c), nil
}

func (m *MemoryStore) Update(ctx context.Context, c *Contract) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.contracts[c.ID]; !ok {
		return ErrContractNotFound
	}
	m.contracts[c.ID] = deepCopyContract(c)
	return nil
}

func (m *MemoryStore) GetByEscrow(ctx context.Context, escrowID string) (*Contract, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, c := range m.contracts {
		if c.BoundEscrowID == escrowID {
			return deepCopyContract(c), nil
		}
	}
	return nil, ErrContractNotFound
}

func deepCopyContract(c *Contract) *Contract {
	cp := *c
	if c.Preconditions != nil {
		cp.Preconditions = make([]Condition, len(c.Preconditions))
		for i, cond := range c.Preconditions {
			cp.Preconditions[i] = deepCopyCondition(cond)
		}
	}
	if c.Invariants != nil {
		cp.Invariants = make([]Condition, len(c.Invariants))
		for i, cond := range c.Invariants {
			cp.Invariants[i] = deepCopyCondition(cond)
		}
	}
	if c.Violations != nil {
		cp.Violations = make([]Violation, len(c.Violations))
		copy(cp.Violations, c.Violations)
	}
	return &cp
}

func deepCopyCondition(c Condition) Condition {
	cp := c
	if c.Params != nil {
		cp.Params = make(map[string]interface{}, len(c.Params))
		for k, v := range c.Params {
			cp.Params[k] = v
		}
	}
	return cp
}

var _ Store = (*MemoryStore)(nil)
