package ledger

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// MemoryEventStore implements EventStore with in-memory storage.
type MemoryEventStore struct {
	events []*Event
	nextID atomic.Int64
	mu     sync.RWMutex
}

// NewMemoryEventStore creates a new in-memory event store.
func NewMemoryEventStore() *MemoryEventStore {
	return &MemoryEventStore{}
}

func (s *MemoryEventStore) AppendEvent(_ context.Context, event *Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cp := *event
	cp.ID = s.nextID.Add(1)
	if cp.CreatedAt.IsZero() {
		cp.CreatedAt = time.Now()
	}
	s.events = append(s.events, &cp)
	return nil
}

func (s *MemoryEventStore) GetEvents(_ context.Context, agentAddr string, since time.Time) ([]*Event, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*Event
	for _, e := range s.events {
		if e.AgentAddr == agentAddr && !e.CreatedAt.Before(since) {
			cp := *e
			result = append(result, &cp)
		}
	}
	return result, nil
}

func (s *MemoryEventStore) GetAllAgents(_ context.Context) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	seen := make(map[string]bool)
	var agents []string
	for _, e := range s.events {
		if !seen[e.AgentAddr] {
			seen[e.AgentAddr] = true
			agents = append(agents, e.AgentAddr)
		}
	}
	return agents, nil
}
