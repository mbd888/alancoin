package intelligence

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"
)

// MemoryStore implements Store in memory for development and testing.
type MemoryStore struct {
	mu         sync.RWMutex
	profiles   map[string]*AgentProfile
	history    map[string][]*ScoreHistoryPoint
	benchmarks *NetworkBenchmarks
}

// NewMemoryStore creates an in-memory intelligence store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		profiles: make(map[string]*AgentProfile),
		history:  make(map[string][]*ScoreHistoryPoint),
	}
}

func (m *MemoryStore) SaveProfile(_ context.Context, profile *AgentProfile) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	addr := strings.ToLower(profile.Address)
	cp := *profile
	cp.Address = addr
	m.profiles[addr] = &cp
	return nil
}

func (m *MemoryStore) SaveProfileBatch(_ context.Context, profiles []*AgentProfile) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, pr := range profiles {
		addr := strings.ToLower(pr.Address)
		cp := *pr
		cp.Address = addr
		m.profiles[addr] = &cp
	}
	return nil
}

func (m *MemoryStore) GetProfile(_ context.Context, address string) (*AgentProfile, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	pr, ok := m.profiles[strings.ToLower(address)]
	if !ok {
		return nil, nil
	}
	cp := *pr
	return &cp, nil
}

func (m *MemoryStore) GetProfiles(_ context.Context, addresses []string) (map[string]*AgentProfile, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string]*AgentProfile, len(addresses))
	for _, addr := range addresses {
		if pr, ok := m.profiles[strings.ToLower(addr)]; ok {
			cp := *pr
			result[strings.ToLower(addr)] = &cp
		}
	}
	return result, nil
}

func (m *MemoryStore) GetTopProfiles(_ context.Context, limit int) ([]*AgentProfile, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	list := make([]*AgentProfile, 0, len(m.profiles))
	for _, pr := range m.profiles {
		cp := *pr
		list = append(list, &cp)
	}

	sort.Slice(list, func(i, j int) bool {
		return list[i].CompositeScore > list[j].CompositeScore
	})

	if limit > 0 && len(list) > limit {
		list = list[:limit]
	}
	return list, nil
}

func (m *MemoryStore) SaveScoreHistory(_ context.Context, points []*ScoreHistoryPoint) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, pt := range points {
		addr := strings.ToLower(pt.Address)
		cp := *pt
		cp.Address = addr
		m.history[addr] = append(m.history[addr], &cp)
	}
	return nil
}

func (m *MemoryStore) GetScoreHistory(_ context.Context, address string, from, to time.Time, limit int) ([]*ScoreHistoryPoint, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	addr := strings.ToLower(address)
	all := m.history[addr]

	var result []*ScoreHistoryPoint
	for i := len(all) - 1; i >= 0; i-- {
		pt := all[i]
		if (pt.CreatedAt.Equal(from) || pt.CreatedAt.After(from)) &&
			(pt.CreatedAt.Equal(to) || pt.CreatedAt.Before(to)) {
			cp := *pt
			result = append(result, &cp)
		}
		if limit > 0 && len(result) >= limit {
			break
		}
	}
	return result, nil
}

func (m *MemoryStore) DeleteScoreHistoryBefore(_ context.Context, before time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var deleted int64
	for addr, points := range m.history {
		var kept []*ScoreHistoryPoint
		for _, pt := range points {
			if pt.CreatedAt.Before(before) {
				deleted++
			} else {
				kept = append(kept, pt)
			}
		}
		m.history[addr] = kept
	}
	return deleted, nil
}

func (m *MemoryStore) SaveBenchmarks(_ context.Context, b *NetworkBenchmarks) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cp := *b
	m.benchmarks = &cp
	return nil
}

func (m *MemoryStore) GetLatestBenchmarks(_ context.Context) (*NetworkBenchmarks, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.benchmarks == nil {
		return nil, nil
	}
	cp := *m.benchmarks
	return &cp, nil
}
