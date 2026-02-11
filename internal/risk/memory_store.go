package risk

import (
	"context"
	"sync"
)

// MemoryStore is an in-memory implementation of Store for demo/test use.
type MemoryStore struct {
	mu          sync.RWMutex
	assessments map[string][]*RiskAssessment // keyID → assessments
}

// NewMemoryStore creates an in-memory risk assessment store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		assessments: make(map[string][]*RiskAssessment),
	}
}

func (s *MemoryStore) Record(ctx context.Context, assessment *RiskAssessment) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Deep-copy factors
	factors := make(map[string]float64, len(assessment.Factors))
	for k, v := range assessment.Factors {
		factors[k] = v
	}
	a := *assessment
	a.Factors = factors

	s.assessments[assessment.KeyID] = append(s.assessments[assessment.KeyID], &a)
	return nil
}

func (s *MemoryStore) ListByKey(ctx context.Context, keyID string, limit int) ([]*RiskAssessment, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	all := s.assessments[keyID]
	if len(all) == 0 {
		return nil, nil
	}

	// Return most recent first, up to limit
	start := len(all) - limit
	if start < 0 {
		start = 0
	}

	result := make([]*RiskAssessment, 0, len(all)-start)
	for i := len(all) - 1; i >= start; i-- {
		a := *all[i]
		factors := make(map[string]float64, len(a.Factors))
		for k, v := range a.Factors {
			factors[k] = v
		}
		a.Factors = factors
		result = append(result, &a)
	}
	return result, nil
}
