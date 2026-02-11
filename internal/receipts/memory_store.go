package receipts

import (
	"context"
	"sort"
	"strings"
	"sync"
)

// MemoryStore is an in-memory receipt store for demo/development mode.
type MemoryStore struct {
	receipts map[string]*Receipt
	mu       sync.RWMutex
}

// NewMemoryStore creates a new in-memory receipt store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		receipts: make(map[string]*Receipt),
	}
}

func (m *MemoryStore) Create(_ context.Context, r *Receipt) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.receipts[r.ID] = r
	return nil
}

func (m *MemoryStore) Get(_ context.Context, id string) (*Receipt, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	r, ok := m.receipts[id]
	if !ok {
		return nil, ErrReceiptNotFound
	}
	cp := *r
	return &cp, nil
}

func (m *MemoryStore) ListByAgent(_ context.Context, agentAddr string, limit int) ([]*Receipt, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	addr := strings.ToLower(agentAddr)
	var result []*Receipt
	for _, r := range m.receipts {
		if r.From == addr || r.To == addr {
			cp := *r
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

func (m *MemoryStore) ListByReference(_ context.Context, reference string) ([]*Receipt, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*Receipt
	for _, r := range m.receipts {
		if r.Reference == reference {
			cp := *r
			result = append(result, &cp)
		}
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt.After(result[j].CreatedAt)
	})

	return result, nil
}

var _ Store = (*MemoryStore)(nil)
