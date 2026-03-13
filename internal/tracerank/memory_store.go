package tracerank

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"
)

// MemoryStore implements Store in memory for development and testing.
type MemoryStore struct {
	mu     sync.RWMutex
	scores map[string]*AgentScore // address -> latest score
	runs   []*RunMetadata
}

// NewMemoryStore creates an in-memory TraceRank score store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		scores: make(map[string]*AgentScore),
	}
}

func (m *MemoryStore) SaveScores(_ context.Context, scores []*AgentScore, runID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	maxScore := 0.0
	totalScore := 0.0

	for _, s := range scores {
		addr := strings.ToLower(s.Address)
		cp := *s
		cp.Address = addr
		m.scores[addr] = &cp
		if s.GraphScore > maxScore {
			maxScore = s.GraphScore
		}
		totalScore += s.GraphScore
	}

	meanScore := 0.0
	if len(scores) > 0 {
		meanScore = totalScore / float64(len(scores))
	}

	meta := &RunMetadata{
		RunID:      runID,
		NodeCount:  len(scores),
		MaxScore:   maxScore,
		MeanScore:  meanScore,
		ComputedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if len(scores) > 0 {
		meta.Iterations = scores[0].Iterations
	}
	m.runs = append(m.runs, meta)

	return nil
}

func (m *MemoryStore) GetScore(_ context.Context, address string) (*AgentScore, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	s, ok := m.scores[strings.ToLower(address)]
	if !ok {
		return nil, nil
	}
	cp := *s
	return &cp, nil
}

func (m *MemoryStore) GetScores(_ context.Context, addresses []string) (map[string]*AgentScore, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string]*AgentScore, len(addresses))
	for _, addr := range addresses {
		if s, ok := m.scores[strings.ToLower(addr)]; ok {
			cp := *s
			result[strings.ToLower(addr)] = &cp
		}
	}
	return result, nil
}

func (m *MemoryStore) GetTopScores(_ context.Context, limit int) ([]*AgentScore, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	list := make([]*AgentScore, 0, len(m.scores))
	for _, s := range m.scores {
		cp := *s
		list = append(list, &cp)
	}

	sort.Slice(list, func(i, j int) bool {
		return list[i].GraphScore > list[j].GraphScore
	})

	if limit > 0 && len(list) > limit {
		list = list[:limit]
	}
	return list, nil
}

func (m *MemoryStore) GetRunHistory(_ context.Context, limit int) ([]*RunMetadata, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if limit <= 0 {
		limit = 10
	}

	start := len(m.runs) - limit
	if start < 0 {
		start = 0
	}

	result := make([]*RunMetadata, 0, limit)
	for i := len(m.runs) - 1; i >= start; i-- {
		cp := *m.runs[i]
		result = append(result, &cp)
	}
	return result, nil
}
