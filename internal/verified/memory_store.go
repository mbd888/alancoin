package verified

import (
	"context"
	"strings"
	"sync"
)

// MemoryStore implements Store using in-memory maps (for demo/testing).
type MemoryStore struct {
	mu            sync.RWMutex
	verifications map[string]*Verification // id → verification
	byAgent       map[string]string        // agentAddr → verification id
}

// NewMemoryStore creates an in-memory verification store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		verifications: make(map[string]*Verification),
		byAgent:       make(map[string]string),
	}
}

func (s *MemoryStore) Create(_ context.Context, v *Verification) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	addr := strings.ToLower(v.AgentAddr)
	if existingID, ok := s.byAgent[addr]; ok {
		existing := s.verifications[existingID]
		if existing != nil && !existing.IsTerminal() {
			return ErrAlreadyVerified
		}
	}

	s.verifications[v.ID] = v
	s.byAgent[addr] = v.ID
	return nil
}

func (s *MemoryStore) Get(_ context.Context, id string) (*Verification, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	v, ok := s.verifications[id]
	if !ok {
		return nil, ErrVerificationFound
	}
	return v, nil
}

func (s *MemoryStore) GetByAgent(_ context.Context, agentAddr string) (*Verification, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	addr := strings.ToLower(agentAddr)
	id, ok := s.byAgent[addr]
	if !ok {
		return nil, ErrVerificationFound
	}
	v, ok := s.verifications[id]
	if !ok {
		return nil, ErrVerificationFound
	}
	return v, nil
}

func (s *MemoryStore) Update(_ context.Context, v *Verification) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.verifications[v.ID]; !ok {
		return ErrVerificationFound
	}
	s.verifications[v.ID] = v
	return nil
}

func (s *MemoryStore) ListActive(_ context.Context, limit int) ([]*Verification, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*Verification
	for _, v := range s.verifications {
		if v.Status == StatusActive {
			result = append(result, v)
			if len(result) >= limit {
				break
			}
		}
	}
	return result, nil
}

func (s *MemoryStore) ListAll(_ context.Context, limit int) ([]*Verification, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*Verification
	for _, v := range s.verifications {
		result = append(result, v)
		if len(result) >= limit {
			break
		}
	}
	return result, nil
}

func (s *MemoryStore) IsVerified(_ context.Context, agentAddr string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	addr := strings.ToLower(agentAddr)
	id, ok := s.byAgent[addr]
	if !ok {
		return false, nil
	}
	v, ok := s.verifications[id]
	if !ok {
		return false, nil
	}
	return v.IsActive(), nil
}
