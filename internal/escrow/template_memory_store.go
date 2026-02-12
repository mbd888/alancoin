package escrow

import (
	"context"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// TemplateMemoryStore is an in-memory implementation of TemplateStore.
type TemplateMemoryStore struct {
	mu         sync.RWMutex
	templates  map[string]*EscrowTemplate
	milestones map[string][]*EscrowMilestone // escrowID → milestones
	nextID     atomic.Int64
}

// NewTemplateMemoryStore creates a new in-memory template store.
func NewTemplateMemoryStore() *TemplateMemoryStore {
	return &TemplateMemoryStore{
		templates:  make(map[string]*EscrowTemplate),
		milestones: make(map[string][]*EscrowMilestone),
	}
}

func (s *TemplateMemoryStore) CreateTemplate(_ context.Context, t *EscrowTemplate) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *t
	cp.Milestones = make([]Milestone, len(t.Milestones))
	copy(cp.Milestones, t.Milestones)
	s.templates[t.ID] = &cp
	return nil
}

func (s *TemplateMemoryStore) GetTemplate(_ context.Context, id string) (*EscrowTemplate, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.templates[id]
	if !ok {
		return nil, ErrTemplateNotFound
	}
	cp := *t
	cp.Milestones = make([]Milestone, len(t.Milestones))
	copy(cp.Milestones, t.Milestones)
	return &cp, nil
}

func (s *TemplateMemoryStore) ListTemplates(_ context.Context, limit int) ([]*EscrowTemplate, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*EscrowTemplate, 0, len(s.templates))
	for _, t := range s.templates {
		cp := *t
		cp.Milestones = make([]Milestone, len(t.Milestones))
		copy(cp.Milestones, t.Milestones)
		result = append(result, &cp)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt.After(result[j].CreatedAt)
	})
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (s *TemplateMemoryStore) ListTemplatesByCreator(_ context.Context, creatorAddr string, limit int) ([]*EscrowTemplate, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*EscrowTemplate
	for _, t := range s.templates {
		if strings.EqualFold(t.CreatorAddr, creatorAddr) {
			cp := *t
			cp.Milestones = make([]Milestone, len(t.Milestones))
			copy(cp.Milestones, t.Milestones)
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

func (s *TemplateMemoryStore) CreateMilestone(_ context.Context, m *EscrowMilestone) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *m
	cp.ID = int(s.nextID.Add(1))
	*m = cp // write back ID
	s.milestones[m.EscrowID] = append(s.milestones[m.EscrowID], &cp)
	return nil
}

func (s *TemplateMemoryStore) GetMilestone(_ context.Context, escrowID string, index int) (*EscrowMilestone, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, m := range s.milestones[escrowID] {
		if m.MilestoneIndex == index {
			cp := *m
			return &cp, nil
		}
	}
	return nil, ErrMilestoneNotFound
}

func (s *TemplateMemoryStore) UpdateMilestone(_ context.Context, m *EscrowMilestone) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, existing := range s.milestones[m.EscrowID] {
		if existing.MilestoneIndex == m.MilestoneIndex {
			cp := *m
			s.milestones[m.EscrowID][i] = &cp
			return nil
		}
	}
	return ErrMilestoneNotFound
}

func (s *TemplateMemoryStore) ListMilestones(_ context.Context, escrowID string) ([]*EscrowMilestone, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	stored := s.milestones[escrowID]
	result := make([]*EscrowMilestone, 0, len(stored))
	for _, m := range stored {
		cp := *m
		result = append(result, &cp)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].MilestoneIndex < result[j].MilestoneIndex
	})
	return result, nil
}
