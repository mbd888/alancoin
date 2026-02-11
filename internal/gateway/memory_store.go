package gateway

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"
)

// MemoryStore is an in-memory gateway store for demo/development mode.
type MemoryStore struct {
	sessions map[string]*Session
	logs     map[string][]*RequestLog // sessionID → logs
	mu       sync.RWMutex
}

// NewMemoryStore creates a new in-memory gateway store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		sessions: make(map[string]*Session),
		logs:     make(map[string][]*RequestLog),
	}
}

func (m *MemoryStore) CreateSession(_ context.Context, session *Session) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[session.ID] = session
	return nil
}

func (m *MemoryStore) GetSession(_ context.Context, id string) (*Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	session, ok := m.sessions[id]
	if !ok {
		return nil, ErrSessionNotFound
	}
	cp := *session
	if session.AllowedTypes != nil {
		cp.AllowedTypes = make([]string, len(session.AllowedTypes))
		copy(cp.AllowedTypes, session.AllowedTypes)
	}
	return &cp, nil
}

func (m *MemoryStore) UpdateSession(_ context.Context, session *Session) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.sessions[session.ID]; !ok {
		return ErrSessionNotFound
	}
	m.sessions[session.ID] = session
	return nil
}

func (m *MemoryStore) ListSessions(_ context.Context, agentAddr string, limit int) ([]*Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	addr := strings.ToLower(agentAddr)
	var result []*Session
	for _, s := range m.sessions {
		if s.AgentAddr == addr {
			cp := *s
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

func (m *MemoryStore) ListExpired(_ context.Context, before time.Time, limit int) ([]*Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*Session
	for _, s := range m.sessions {
		if s.Status == StatusActive && !s.ExpiresAt.IsZero() && s.ExpiresAt.Before(before) {
			cp := *s
			if s.AllowedTypes != nil {
				cp.AllowedTypes = make([]string, len(s.AllowedTypes))
				copy(cp.AllowedTypes, s.AllowedTypes)
			}
			result = append(result, &cp)
			if len(result) >= limit {
				break
			}
		}
	}
	return result, nil
}

func (m *MemoryStore) CreateLog(_ context.Context, log *RequestLog) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.logs[log.SessionID] = append(m.logs[log.SessionID], log)
	return nil
}

func (m *MemoryStore) ListLogs(_ context.Context, sessionID string, limit int) ([]*RequestLog, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	logs := m.logs[sessionID]
	if len(logs) > limit {
		logs = logs[len(logs)-limit:]
	}

	result := make([]*RequestLog, len(logs))
	for i, l := range logs {
		cp := *l
		result[i] = &cp
	}
	return result, nil
}

// Compile-time assertion.
var _ Store = (*MemoryStore)(nil)
