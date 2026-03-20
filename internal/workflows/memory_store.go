package workflows

import (
	"context"
	"sync"
)

// MemoryStore is an in-memory workflow store for demo/development.
type MemoryStore struct {
	workflows map[string]*Workflow
	mu        sync.RWMutex
}

// NewMemoryStore creates a new in-memory workflow store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		workflows: make(map[string]*Workflow),
	}
}

func (m *MemoryStore) Create(ctx context.Context, w *Workflow) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.workflows[w.ID] = copyWorkflow(w)
	return nil
}

func (m *MemoryStore) Get(ctx context.Context, id string) (*Workflow, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	w, ok := m.workflows[id]
	if !ok {
		return nil, ErrWorkflowNotFound
	}
	return copyWorkflow(w), nil
}

func (m *MemoryStore) Update(ctx context.Context, w *Workflow) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.workflows[w.ID]; !ok {
		return ErrWorkflowNotFound
	}
	m.workflows[w.ID] = copyWorkflow(w)
	return nil
}

func (m *MemoryStore) ListByOwner(ctx context.Context, ownerAddr string, limit int) ([]*Workflow, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []*Workflow
	for _, w := range m.workflows {
		if w.OwnerAddr == ownerAddr {
			result = append(result, copyWorkflow(w))
			if len(result) >= limit {
				break
			}
		}
	}
	return result, nil
}

func copyWorkflow(w *Workflow) *Workflow {
	cp := *w
	if w.Steps != nil {
		cp.Steps = make([]WorkflowStep, len(w.Steps))
		for i, s := range w.Steps {
			cp.Steps[i] = s
			if s.StartedAt != nil {
				t := *s.StartedAt
				cp.Steps[i].StartedAt = &t
			}
			if s.CompletedAt != nil {
				t := *s.CompletedAt
				cp.Steps[i].CompletedAt = &t
			}
		}
	}
	if w.AuditTrail != nil {
		cp.AuditTrail = make([]AuditEntry, len(w.AuditTrail))
		copy(cp.AuditTrail, w.AuditTrail)
	}
	if w.ClosedAt != nil {
		t := *w.ClosedAt
		cp.ClosedAt = &t
	}
	return &cp
}

var _ Store = (*MemoryStore)(nil)
