package arbitration

import (
	"context"
	"sync"
)

// MemoryStore is an in-memory arbitration store for testing.
type MemoryStore struct {
	mu    sync.RWMutex
	cases map[string]*Case
}

// NewMemoryStore creates an in-memory arbitration store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{cases: make(map[string]*Case)}
}

func (m *MemoryStore) Create(_ context.Context, c *Case) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cases[c.ID] = c
	return nil
}

func (m *MemoryStore) Get(_ context.Context, id string) (*Case, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c, ok := m.cases[id]
	if !ok {
		return nil, ErrCaseNotFound
	}
	return c, nil
}

func (m *MemoryStore) Update(_ context.Context, c *Case) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cases[c.ID] = c
	return nil
}

func (m *MemoryStore) ListByEscrow(_ context.Context, escrowID string) ([]*Case, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []*Case
	for _, c := range m.cases {
		if c.EscrowID == escrowID {
			result = append(result, c)
		}
	}
	return result, nil
}

func (m *MemoryStore) ListOpen(_ context.Context, limit int) ([]*Case, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []*Case
	for _, c := range m.cases {
		if c.Status == CSOpen || c.Status == CSAssigned {
			result = append(result, c)
			if len(result) >= limit {
				break
			}
		}
	}
	return result, nil
}
