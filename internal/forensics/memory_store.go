package forensics

import (
	"context"
	"sync"
)

// MemoryStore is an in-memory forensics store for testing.
type MemoryStore struct {
	mu        sync.RWMutex
	baselines map[string]*Baseline
	alerts    []*Alert
}

// NewMemoryStore creates an in-memory forensics store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{baselines: make(map[string]*Baseline)}
}

func (m *MemoryStore) GetBaseline(_ context.Context, agentAddr string) (*Baseline, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	b, ok := m.baselines[agentAddr]
	if !ok {
		return nil, ErrAgentNotTracked
	}
	return b, nil
}

func (m *MemoryStore) SaveBaseline(_ context.Context, b *Baseline) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.baselines[b.AgentAddr] = b
	return nil
}

func (m *MemoryStore) SaveAlert(_ context.Context, a *Alert) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.alerts = append(m.alerts, a)
	return nil
}

func (m *MemoryStore) ListAlerts(_ context.Context, agentAddr string, limit int) ([]*Alert, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []*Alert
	// Reverse order (newest first)
	for i := len(m.alerts) - 1; i >= 0; i-- {
		if m.alerts[i].AgentAddr == agentAddr {
			result = append(result, m.alerts[i])
			if len(result) >= limit {
				break
			}
		}
	}
	return result, nil
}

func (m *MemoryStore) ListAllAlerts(_ context.Context, severity AlertSeverity, limit int) ([]*Alert, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []*Alert
	for i := len(m.alerts) - 1; i >= 0; i-- {
		if severity == "" || m.alerts[i].Severity == severity {
			result = append(result, m.alerts[i])
			if len(result) >= limit {
				break
			}
		}
	}
	return result, nil
}

func (m *MemoryStore) AcknowledgeAlert(_ context.Context, alertID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, a := range m.alerts {
		if a.ID == alertID {
			a.Acknowledged = true
			return nil
		}
	}
	return ErrAlertNotFound
}
