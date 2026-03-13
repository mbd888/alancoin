package flywheel

import (
	"context"
	"sync"
)

// MemoryStore implements SnapshotStore with in-memory storage.
type MemoryStore struct {
	mu        sync.RWMutex
	snapshots []*State
}

// NewMemoryStore creates an in-memory flywheel snapshot store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{}
}

// Save appends a snapshot to the in-memory list.
func (s *MemoryStore) Save(_ context.Context, state *State) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *state
	s.snapshots = append(s.snapshots, &cp)
	// Keep bounded to prevent unbounded growth
	if len(s.snapshots) > 1000 {
		s.snapshots = s.snapshots[len(s.snapshots)-1000:]
	}
	return nil
}

// Recent returns the most recent snapshots, newest first.
func (s *MemoryStore) Recent(_ context.Context, limit int) ([]*State, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	n := len(s.snapshots)
	if limit > n {
		limit = n
	}

	results := make([]*State, limit)
	for i := 0; i < limit; i++ {
		cp := *s.snapshots[n-1-i]
		results[i] = &cp
	}
	return results, nil
}
