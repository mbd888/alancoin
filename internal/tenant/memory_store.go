package tenant

import (
	"context"
	"strings"
	"sync"
)

// MemoryStore is an in-memory tenant store for demo/development.
type MemoryStore struct {
	mu      sync.RWMutex
	tenants map[string]*Tenant // by ID
	slugs   map[string]string  // slug → ID
	// agentTenants tracks agent→tenant binding via "api_keys" simulation
	agentTenants map[string]string // agentAddr → tenantID
}

// NewMemoryStore creates a new in-memory tenant store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		tenants:      make(map[string]*Tenant),
		slugs:        make(map[string]string),
		agentTenants: make(map[string]string),
	}
}

func (m *MemoryStore) Create(_ context.Context, t *Tenant) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.slugs[t.Slug]; exists {
		return ErrSlugTaken
	}

	cp := *t
	m.tenants[t.ID] = &cp
	m.slugs[t.Slug] = t.ID
	return nil
}

func (m *MemoryStore) Get(_ context.Context, id string) (*Tenant, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	t, ok := m.tenants[id]
	if !ok {
		return nil, ErrTenantNotFound
	}
	cp := *t
	return &cp, nil
}

func (m *MemoryStore) GetBySlug(_ context.Context, slug string) (*Tenant, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	id, ok := m.slugs[slug]
	if !ok {
		return nil, ErrTenantNotFound
	}
	t := m.tenants[id]
	cp := *t
	return &cp, nil
}

func (m *MemoryStore) Update(_ context.Context, t *Tenant) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.tenants[t.ID]; !ok {
		return ErrTenantNotFound
	}
	cp := *t
	m.tenants[t.ID] = &cp
	return nil
}

func (m *MemoryStore) ListAgents(_ context.Context, tenantID string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var agents []string
	for addr, tid := range m.agentTenants {
		if tid == tenantID {
			agents = append(agents, addr)
		}
	}
	return agents, nil
}

func (m *MemoryStore) CountAgents(_ context.Context, tenantID string) (int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	count := 0
	for _, tid := range m.agentTenants {
		if tid == tenantID {
			count++
		}
	}
	return count, nil
}

// BindAgent associates an agent address with a tenant (used by handlers in demo mode).
func (m *MemoryStore) BindAgent(agentAddr, tenantID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.agentTenants[strings.ToLower(agentAddr)] = tenantID
}

var _ Store = (*MemoryStore)(nil)
