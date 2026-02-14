package supervisor

import (
	"context"
	"math/big"
	"sync"
	"time"
)

// MemoryBaselineStore is an in-memory BaselineStore for tests and demo mode.
type MemoryBaselineStore struct {
	mu        sync.RWMutex
	baselines map[string]*AgentBaseline
	events    []*SpendEventRecord
	denials   []*DenialRecord
	nextID    int64
}

// Compile-time check.
var _ BaselineStore = (*MemoryBaselineStore)(nil)

// NewMemoryBaselineStore creates an empty in-memory store.
func NewMemoryBaselineStore() *MemoryBaselineStore {
	return &MemoryBaselineStore{
		baselines: make(map[string]*AgentBaseline),
	}
}

func (s *MemoryBaselineStore) SaveBaselineBatch(_ context.Context, baselines []*AgentBaseline) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, b := range baselines {
		s.baselines[b.AgentAddr] = b
	}
	return nil
}

func (s *MemoryBaselineStore) GetAllBaselines(_ context.Context) ([]*AgentBaseline, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*AgentBaseline, 0, len(s.baselines))
	for _, b := range s.baselines {
		out = append(out, b)
	}
	return out, nil
}

func (s *MemoryBaselineStore) AppendSpendEvent(_ context.Context, ev *SpendEventRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	ev.ID = s.nextID
	s.events = append(s.events, ev)
	return nil
}

func (s *MemoryBaselineStore) AppendSpendEventBatch(_ context.Context, events []*SpendEventRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ev := range events {
		s.nextID++
		ev.ID = s.nextID
		s.events = append(s.events, ev)
	}
	return nil
}

func (s *MemoryBaselineStore) GetRecentSpendEvents(_ context.Context, since time.Time) ([]*SpendEventRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*SpendEventRecord
	for _, ev := range s.events {
		if !ev.CreatedAt.Before(since) {
			out = append(out, ev)
		}
	}
	return out, nil
}

func (s *MemoryBaselineStore) GetAllAgentsWithEvents(_ context.Context, since time.Time) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	seen := make(map[string]bool)
	for _, ev := range s.events {
		if !ev.CreatedAt.Before(since) {
			seen[ev.AgentAddr] = true
		}
	}
	out := make([]string, 0, len(seen))
	for addr := range seen {
		out = append(out, addr)
	}
	return out, nil
}

func (s *MemoryBaselineStore) GetHourlyTotals(_ context.Context, agentAddr string, since time.Time) (map[time.Time]*big.Int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	totals := make(map[time.Time]*big.Int)
	for _, ev := range s.events {
		if ev.AgentAddr != agentAddr || ev.CreatedAt.Before(since) {
			continue
		}
		hour := ev.CreatedAt.Truncate(time.Hour)
		if totals[hour] == nil {
			totals[hour] = new(big.Int)
		}
		totals[hour].Add(totals[hour], ev.Amount)
	}
	return totals, nil
}

func (s *MemoryBaselineStore) PruneOldEvents(_ context.Context, before time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var kept []*SpendEventRecord
	var pruned int64
	for _, ev := range s.events {
		if ev.CreatedAt.Before(before) {
			pruned++
		} else {
			kept = append(kept, ev)
		}
	}
	s.events = kept
	return pruned, nil
}

func (s *MemoryBaselineStore) LogDenial(_ context.Context, rec *DenialRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	rec.ID = s.nextID
	s.denials = append(s.denials, rec)
	return nil
}

// GetDenials returns all logged denials (test helper).
func (s *MemoryBaselineStore) GetDenials() []*DenialRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cp := make([]*DenialRecord, len(s.denials))
	copy(cp, s.denials)
	return cp
}

// GetEvents returns all logged spend events (test helper).
func (s *MemoryBaselineStore) GetEvents() []*SpendEventRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cp := make([]*SpendEventRecord, len(s.events))
	copy(cp, s.events)
	return cp
}
