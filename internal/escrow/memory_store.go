package escrow

import (
	"context"
	"strings"
	"sync"
	"time"
)

// MemoryStore is an in-memory escrow store for demo/development mode.
type MemoryStore struct {
	escrows map[string]*Escrow
	mu      sync.RWMutex
}

// NewMemoryStore creates a new in-memory escrow store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		escrows: make(map[string]*Escrow),
	}
}

func (m *MemoryStore) Create(ctx context.Context, escrow *Escrow) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.escrows[escrow.ID] = escrow
	return nil
}

func (m *MemoryStore) Get(ctx context.Context, id string) (*Escrow, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	escrow, ok := m.escrows[id]
	if !ok {
		return nil, ErrEscrowNotFound
	}
	// Return a deep copy to prevent races on the shared pointer.
	// Shallow copy shares slice backing arrays (e.g. DisputeEvidence),
	// so an append on the copy can mutate the stored escrow.
	cp := *escrow
	if escrow.DisputeEvidence != nil {
		cp.DisputeEvidence = make([]EvidenceEntry, len(escrow.DisputeEvidence))
		copy(cp.DisputeEvidence, escrow.DisputeEvidence)
	}
	return &cp, nil
}

func (m *MemoryStore) Update(ctx context.Context, escrow *Escrow) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.escrows[escrow.ID]; !ok {
		return ErrEscrowNotFound
	}
	m.escrows[escrow.ID] = escrow
	return nil
}

func (m *MemoryStore) ListByAgent(ctx context.Context, agentAddr string, limit int, _ ...ListOption) ([]*Escrow, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	addr := strings.ToLower(agentAddr)
	var result []*Escrow
	for _, e := range m.escrows {
		if e.BuyerAddr == addr || e.SellerAddr == addr {
			cp := *e
			result = append(result, &cp)
			if len(result) >= limit {
				break
			}
		}
	}
	return result, nil
}

func (m *MemoryStore) ListExpired(ctx context.Context, before time.Time, limit int) ([]*Escrow, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*Escrow
	for _, e := range m.escrows {
		if !e.IsTerminal() && e.Status != StatusDisputed && e.Status != StatusArbitrating && e.AutoReleaseAt.Before(before) {
			cp := *e
			result = append(result, &cp)
			if len(result) >= limit {
				break
			}
		}
	}
	return result, nil
}

func (m *MemoryStore) ListByStatus(ctx context.Context, status Status, limit int) ([]*Escrow, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*Escrow
	for _, e := range m.escrows {
		if e.Status == status {
			cp := *e
			result = append(result, &cp)
			if len(result) >= limit {
				break
			}
		}
	}
	return result, nil
}
