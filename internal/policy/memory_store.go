package policy

import (
	"context"
	"sort"
	"sync"
)

// MemoryStore is an in-memory policy store for tests and demo mode.
type MemoryStore struct {
	mu       sync.RWMutex
	policies map[string]*SpendPolicy // by ID
}

// NewMemoryStore creates a new in-memory policy store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		policies: make(map[string]*SpendPolicy),
	}
}

func (m *MemoryStore) Create(_ context.Context, p *SpendPolicy) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check name uniqueness within tenant.
	for _, existing := range m.policies {
		if existing.TenantID == p.TenantID && existing.Name == p.Name {
			return ErrNameTaken
		}
	}

	cp := *p
	cp.Rules = make([]Rule, len(p.Rules))
	copy(cp.Rules, p.Rules)
	m.policies[p.ID] = &cp
	return nil
}

func (m *MemoryStore) Get(_ context.Context, id string) (*SpendPolicy, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	p, ok := m.policies[id]
	if !ok {
		return nil, ErrPolicyNotFound
	}
	cp := *p
	cp.Rules = make([]Rule, len(p.Rules))
	copy(cp.Rules, p.Rules)
	return &cp, nil
}

func (m *MemoryStore) List(_ context.Context, tenantID string) ([]*SpendPolicy, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*SpendPolicy
	for _, p := range m.policies {
		if p.TenantID == tenantID {
			cp := *p
			cp.Rules = make([]Rule, len(p.Rules))
			copy(cp.Rules, p.Rules)
			result = append(result, &cp)
		}
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Priority < result[j].Priority
	})

	return result, nil
}

func (m *MemoryStore) Update(_ context.Context, p *SpendPolicy) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.policies[p.ID]; !ok {
		return ErrPolicyNotFound
	}

	// Check name uniqueness within tenant (excluding self).
	for _, existing := range m.policies {
		if existing.ID != p.ID && existing.TenantID == p.TenantID && existing.Name == p.Name {
			return ErrNameTaken
		}
	}

	cp := *p
	cp.Rules = make([]Rule, len(p.Rules))
	copy(cp.Rules, p.Rules)
	m.policies[p.ID] = &cp
	return nil
}

func (m *MemoryStore) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.policies[id]; !ok {
		return ErrPolicyNotFound
	}
	delete(m.policies, id)
	return nil
}

var _ Store = (*MemoryStore)(nil)
