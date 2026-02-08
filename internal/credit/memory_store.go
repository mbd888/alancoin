package credit

import (
	"context"
	"strings"
	"sync"
	"time"
)

// MemoryStore is an in-memory credit store for demo/development mode.
type MemoryStore struct {
	lines   map[string]*CreditLine // by ID
	byAgent map[string]string      // agentAddr → ID
	mu      sync.RWMutex
}

// NewMemoryStore creates a new in-memory credit store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		lines:   make(map[string]*CreditLine),
		byAgent: make(map[string]string),
	}
}

func (m *MemoryStore) Create(ctx context.Context, line *CreditLine) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	addr := strings.ToLower(line.AgentAddr)
	if existingID, ok := m.byAgent[addr]; ok {
		existing := m.lines[existingID]
		if existing.Status == StatusActive || existing.Status == StatusSuspended {
			return ErrCreditLineExists
		}
	}

	m.lines[line.ID] = line
	m.byAgent[addr] = line.ID
	return nil
}

func (m *MemoryStore) Get(ctx context.Context, id string) (*CreditLine, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	line, ok := m.lines[id]
	if !ok {
		return nil, ErrCreditLineNotFound
	}
	cp := *line
	return &cp, nil
}

func (m *MemoryStore) GetByAgent(ctx context.Context, agentAddr string) (*CreditLine, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	addr := strings.ToLower(agentAddr)
	id, ok := m.byAgent[addr]
	if !ok {
		return nil, ErrCreditLineNotFound
	}
	line := m.lines[id]
	cp := *line
	return &cp, nil
}

func (m *MemoryStore) Update(ctx context.Context, line *CreditLine) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.lines[line.ID]; !ok {
		return ErrCreditLineNotFound
	}
	line.UpdatedAt = time.Now()
	m.lines[line.ID] = line
	return nil
}

func (m *MemoryStore) ListActive(ctx context.Context, limit int) ([]*CreditLine, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*CreditLine
	for _, line := range m.lines {
		if line.Status == StatusActive {
			cp := *line
			result = append(result, &cp)
			if len(result) >= limit {
				break
			}
		}
	}
	return result, nil
}

func (m *MemoryStore) ListOverdue(ctx context.Context, overdueDays int, limit int) ([]*CreditLine, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	cutoff := time.Now().AddDate(0, 0, -overdueDays)
	var result []*CreditLine
	for _, line := range m.lines {
		if line.Status == StatusActive && line.CreditUsed != "0" && line.CreditUsed != "0.000000" && line.ApprovedAt.Before(cutoff) {
			cp := *line
			result = append(result, &cp)
			if len(result) >= limit {
				break
			}
		}
	}
	return result, nil
}
