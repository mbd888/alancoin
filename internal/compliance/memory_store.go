package compliance

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mbd888/alancoin/internal/idgen"
)

// MemoryStore is an in-memory implementation of Store for demo/dev/test.
type MemoryStore struct {
	mu        sync.RWMutex
	incidents map[string]*Incident // keyed by incident ID
	controls  map[ControlID]Control
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		incidents: make(map[string]*Incident),
		controls:  make(map[ControlID]Control),
	}
}

func (m *MemoryStore) RecordIncident(_ context.Context, in IncidentInput) (*Incident, error) {
	occurred := in.OccurredAt
	if occurred.IsZero() {
		occurred = time.Now().UTC()
	}
	inc := &Incident{
		ID:         idgen.WithPrefix("inc_"),
		Scope:      scopeOrDefault(in.Scope),
		Source:     in.Source,
		Severity:   in.Severity,
		Kind:       in.Kind,
		Title:      in.Title,
		Detail:     in.Detail,
		AgentAddr:  strings.ToLower(in.AgentAddr),
		ReceiptRef: in.ReceiptRef,
		OccurredAt: occurred,
	}

	m.mu.Lock()
	m.incidents[inc.ID] = inc
	m.mu.Unlock()

	cp := *inc
	return &cp, nil
}

func (m *MemoryStore) GetIncident(_ context.Context, id string) (*Incident, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	inc, ok := m.incidents[id]
	if !ok {
		return nil, ErrIncidentNotFound
	}
	cp := *inc
	return &cp, nil
}

func (m *MemoryStore) ListIncidents(_ context.Context, filter IncidentFilter) ([]*Incident, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	scope := scopeOrDefault(filter.Scope)
	addr := strings.ToLower(filter.AgentAddr)

	var result []*Incident
	for _, inc := range m.incidents {
		if inc.Scope != scope {
			continue
		}
		if filter.Source != "" && inc.Source != filter.Source {
			continue
		}
		if filter.MinSeverity != "" && !inc.Severity.IsAtLeast(filter.MinSeverity) {
			continue
		}
		if addr != "" && inc.AgentAddr != addr {
			continue
		}
		if filter.OnlyUnacked && inc.Acknowledged {
			continue
		}
		if !filter.Since.IsZero() && inc.OccurredAt.Before(filter.Since) {
			continue
		}
		if !filter.Until.IsZero() && inc.OccurredAt.After(filter.Until) {
			continue
		}
		cp := *inc
		result = append(result, &cp)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].OccurredAt.After(result[j].OccurredAt)
	})

	if filter.Limit > 0 && len(result) > filter.Limit {
		result = result[:filter.Limit]
	}
	return result, nil
}

func (m *MemoryStore) CountBySeverity(_ context.Context, scope string) (SeverityCounts, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	scope = scopeOrDefault(scope)
	var counts SeverityCounts
	for _, inc := range m.incidents {
		if inc.Scope != scope {
			continue
		}
		switch inc.Severity {
		case SeverityCritical:
			counts.Critical++
		case SeverityWarning:
			counts.Warning++
		case SeverityInfo:
			counts.Info++
		}
		if !inc.Acknowledged {
			counts.Open++
		}
	}
	return counts, nil
}

func (m *MemoryStore) OldestOpen(_ context.Context, scope string) (*time.Time, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	scope = scopeOrDefault(scope)
	var oldest *time.Time
	for _, inc := range m.incidents {
		if inc.Scope != scope || inc.Acknowledged {
			continue
		}
		if oldest == nil || inc.OccurredAt.Before(*oldest) {
			t := inc.OccurredAt
			oldest = &t
		}
	}
	return oldest, nil
}

func (m *MemoryStore) AcknowledgeIncident(_ context.Context, id, actor, note string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	inc, ok := m.incidents[id]
	if !ok {
		return ErrIncidentNotFound
	}
	if inc.Acknowledged {
		return nil
	}
	now := time.Now().UTC()
	inc.Acknowledged = true
	inc.AckBy = actor
	inc.AckNote = note
	inc.AckAt = &now
	return nil
}

func (m *MemoryStore) UpsertControl(_ context.Context, c Control) error {
	if c.LastChecked.IsZero() {
		c.LastChecked = time.Now().UTC()
	}
	m.mu.Lock()
	m.controls[c.ID] = c
	m.mu.Unlock()
	return nil
}

func (m *MemoryStore) ListControls(_ context.Context) ([]Control, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Control, 0, len(m.controls))
	for _, c := range m.controls {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Group != out[j].Group {
			return out[i].Group < out[j].Group
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// scopeOrDefault mirrors the receipts-package helper so empty scope strings
// consolidate into one well-known bucket instead of a phantom scope.
func scopeOrDefault(s string) string {
	if s == "" {
		return "global"
	}
	return s
}

var _ Store = (*MemoryStore)(nil)
