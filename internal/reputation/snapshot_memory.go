package reputation

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"
)

// MemorySnapshotStore implements SnapshotStore in memory.
type MemorySnapshotStore struct {
	mu        sync.RWMutex
	snapshots []*Snapshot
	nextID    int
}

// NewMemorySnapshotStore creates an in-memory snapshot store.
func NewMemorySnapshotStore() *MemorySnapshotStore {
	return &MemorySnapshotStore{
		nextID: 1,
	}
}

func (m *MemorySnapshotStore) Save(_ context.Context, snap *Snapshot) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	snap.ID = m.nextID
	m.nextID++
	if snap.CreatedAt.IsZero() {
		snap.CreatedAt = time.Now()
	}
	m.snapshots = append(m.snapshots, snap)
	return nil
}

func (m *MemorySnapshotStore) SaveBatch(ctx context.Context, snaps []*Snapshot) error {
	for _, s := range snaps {
		if err := m.Save(ctx, s); err != nil {
			return err
		}
	}
	return nil
}

func (m *MemorySnapshotStore) Query(_ context.Context, q HistoryQuery) ([]*Snapshot, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	addr := strings.ToLower(q.Address)
	var results []*Snapshot
	for _, s := range m.snapshots {
		if strings.ToLower(s.Address) != addr {
			continue
		}
		if !q.From.IsZero() && s.CreatedAt.Before(q.From) {
			continue
		}
		if !q.To.IsZero() && s.CreatedAt.After(q.To) {
			continue
		}
		results = append(results, s)
	}

	// Sort by created_at descending
	sort.Slice(results, func(i, j int) bool {
		return results[i].CreatedAt.After(results[j].CreatedAt)
	})

	limit := q.Limit
	if limit <= 0 {
		limit = 100
	}
	if len(results) > limit {
		results = results[:limit]
	}

	return results, nil
}

func (m *MemorySnapshotStore) Latest(_ context.Context, address string) (*Snapshot, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	addr := strings.ToLower(address)
	var latest *Snapshot
	for _, s := range m.snapshots {
		if strings.ToLower(s.Address) != addr {
			continue
		}
		if latest == nil || s.CreatedAt.After(latest.CreatedAt) {
			latest = s
		}
	}
	return latest, nil
}
