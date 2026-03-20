package chargeback

import (
	"context"
	"math/big"
	"sync"
	"time"

	"github.com/mbd888/alancoin/internal/usdc"
)

// MemoryStore is an in-memory chargeback store for testing.
type MemoryStore struct {
	mu      sync.RWMutex
	centers map[string]*CostCenter
	entries []*SpendEntry
}

// NewMemoryStore creates an in-memory chargeback store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		centers: make(map[string]*CostCenter),
	}
}

func (m *MemoryStore) CreateCostCenter(_ context.Context, cc *CostCenter) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.centers[cc.ID] = cc
	return nil
}

func (m *MemoryStore) GetCostCenter(_ context.Context, id string) (*CostCenter, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cc, ok := m.centers[id]
	if !ok {
		return nil, ErrCostCenterNotFound
	}
	return cc, nil
}

func (m *MemoryStore) ListCostCenters(_ context.Context, tenantID string) ([]*CostCenter, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []*CostCenter
	for _, cc := range m.centers {
		if cc.TenantID == tenantID {
			result = append(result, cc)
		}
	}
	return result, nil
}

func (m *MemoryStore) UpdateCostCenter(_ context.Context, cc *CostCenter) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.centers[cc.ID] = cc
	return nil
}

func (m *MemoryStore) RecordSpend(_ context.Context, entry *SpendEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries = append(m.entries, entry)
	return nil
}

func (m *MemoryStore) GetSpendForPeriod(_ context.Context, costCenterID string, from, to time.Time) ([]*SpendEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []*SpendEntry
	for _, e := range m.entries {
		if e.CostCenterID == costCenterID && !e.Timestamp.Before(from) && e.Timestamp.Before(to) {
			result = append(result, e)
		}
	}
	return result, nil
}

func (m *MemoryStore) GetTotalForPeriod(_ context.Context, costCenterID string, from, to time.Time) (*big.Int, int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	total := new(big.Int)
	count := 0
	for _, e := range m.entries {
		if e.CostCenterID == costCenterID && !e.Timestamp.Before(from) && e.Timestamp.Before(to) {
			v, _ := usdc.Parse(e.Amount)
			total.Add(total, v)
			count++
		}
	}
	return total, count, nil
}
