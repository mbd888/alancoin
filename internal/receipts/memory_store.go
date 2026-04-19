package receipts

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"
)

// MemoryStore is an in-memory receipt store for demo/development mode.
type MemoryStore struct {
	receipts map[string]*Receipt
	heads    map[string]*ChainHead // key = scope
	mu       sync.RWMutex
}

// NewMemoryStore creates a new in-memory receipt store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		receipts: make(map[string]*Receipt),
		heads:    make(map[string]*ChainHead),
	}
}

func (m *MemoryStore) Create(_ context.Context, r *Receipt) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.receipts[r.ID] = r
	return nil
}

func (m *MemoryStore) GetChainHead(_ context.Context, scope string) (*ChainHead, error) {
	scope = scopeOrDefault(scope)
	m.mu.RLock()
	defer m.mu.RUnlock()
	if h, ok := m.heads[scope]; ok {
		cp := *h
		return &cp, nil
	}
	return &ChainHead{Scope: scope, HeadHash: "", HeadIndex: -1}, nil
}

func (m *MemoryStore) AppendReceipt(_ context.Context, r *Receipt) error {
	scope := scopeOrDefault(r.Scope)
	m.mu.Lock()
	defer m.mu.Unlock()

	head := m.heads[scope]
	var curHash string
	var curIndex int64 = -1
	if head != nil {
		curHash = head.HeadHash
		curIndex = head.HeadIndex
	}
	if r.PrevHash != curHash || r.ChainIndex != curIndex+1 {
		return ErrChainHeadStale
	}

	m.receipts[r.ID] = r
	m.heads[scope] = &ChainHead{
		Scope:     scope,
		HeadHash:  r.PayloadHash,
		HeadIndex: r.ChainIndex,
		ReceiptID: r.ID,
		UpdatedAt: time.Now(),
	}
	return nil
}

func (m *MemoryStore) ListByChain(_ context.Context, scope string, lowerIndex, upperIndex int64) ([]*Receipt, error) {
	scope = scopeOrDefault(scope)
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*Receipt
	for _, r := range m.receipts {
		if scopeOrDefault(r.Scope) != scope {
			continue
		}
		if r.ChainIndex < lowerIndex {
			continue
		}
		if upperIndex >= 0 && r.ChainIndex > upperIndex {
			continue
		}
		cp := *r
		result = append(result, &cp)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].ChainIndex < result[j].ChainIndex
	})
	return result, nil
}

func (m *MemoryStore) ListByChainTime(_ context.Context, scope string, since, until time.Time) ([]*Receipt, error) {
	scope = scopeOrDefault(scope)
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*Receipt
	for _, r := range m.receipts {
		if scopeOrDefault(r.Scope) != scope {
			continue
		}
		if !since.IsZero() && r.IssuedAt.Before(since) {
			continue
		}
		if !until.IsZero() && r.IssuedAt.After(until) {
			continue
		}
		cp := *r
		result = append(result, &cp)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].ChainIndex < result[j].ChainIndex
	})
	return result, nil
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
var _ ChainStore = (*MemoryStore)(nil)
