package kya

import (
	"context"
	"sync"
)

// MemoryStore is an in-memory KYA certificate store for testing.
type MemoryStore struct {
	mu      sync.RWMutex
	certs   map[string]*Certificate // keyed by ID
	byAgent map[string]string       // agentAddr -> cert ID (latest)
}

// NewMemoryStore creates an in-memory store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		certs:   make(map[string]*Certificate),
		byAgent: make(map[string]string),
	}
}

func (m *MemoryStore) Create(_ context.Context, cert *Certificate) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.certs[cert.ID] = cert
	m.byAgent[cert.AgentAddr] = cert.ID
	return nil
}

func (m *MemoryStore) Get(_ context.Context, id string) (*Certificate, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cert, ok := m.certs[id]
	if !ok {
		return nil, ErrCertNotFound
	}
	return cert, nil
}

func (m *MemoryStore) GetByAgent(_ context.Context, agentAddr string) (*Certificate, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	id, ok := m.byAgent[agentAddr]
	if !ok {
		return nil, ErrCertNotFound
	}
	cert, ok := m.certs[id]
	if !ok {
		return nil, ErrCertNotFound
	}
	return cert, nil
}

func (m *MemoryStore) Update(_ context.Context, cert *Certificate) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.certs[cert.ID] = cert
	return nil
}

func (m *MemoryStore) ListByTenant(_ context.Context, tenantID string, limit int) ([]*Certificate, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []*Certificate
	for _, cert := range m.certs {
		if cert.Org.TenantID == tenantID {
			result = append(result, cert)
			if len(result) >= limit {
				break
			}
		}
	}
	return result, nil
}
