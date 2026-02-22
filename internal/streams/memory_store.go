package streams

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"
)

// MemoryStore is an in-memory stream store for demo/development mode.
type MemoryStore struct {
	streams map[string]*Stream
	ticks   map[string][]*Tick // streamID → ticks
	mu      sync.RWMutex
}

// NewMemoryStore creates a new in-memory stream store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		streams: make(map[string]*Stream),
		ticks:   make(map[string][]*Tick),
	}
}

func (m *MemoryStore) Create(_ context.Context, stream *Stream) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.streams[stream.ID] = stream
	return nil
}

func (m *MemoryStore) Get(_ context.Context, id string) (*Stream, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	stream, ok := m.streams[id]
	if !ok {
		return nil, ErrStreamNotFound
	}
	// Return a copy to prevent races on the shared pointer
	cp := *stream
	return &cp, nil
}

func (m *MemoryStore) Update(_ context.Context, stream *Stream) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.streams[stream.ID]; !ok {
		return ErrStreamNotFound
	}
	m.streams[stream.ID] = stream
	return nil
}

func (m *MemoryStore) ListByAgent(_ context.Context, agentAddr string, limit int) ([]*Stream, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	addr := strings.ToLower(agentAddr)
	var result []*Stream
	for _, s := range m.streams {
		if s.BuyerAddr == addr || s.SellerAddr == addr {
			cp := *s
			result = append(result, &cp)
		}
	}

	// Sort by created_at descending
	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt.After(result[j].CreatedAt)
	})

	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (m *MemoryStore) ListByStatus(_ context.Context, status Status, limit int) ([]*Stream, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*Stream
	for _, s := range m.streams {
		if s.Status == status {
			cp := *s
			result = append(result, &cp)
			if len(result) >= limit {
				break
			}
		}
	}
	return result, nil
}

func (m *MemoryStore) ListStale(_ context.Context, before time.Time, limit int) ([]*Stream, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*Stream
	for _, s := range m.streams {
		if s.Status != StatusOpen {
			continue
		}

		// Stream is stale if last activity was before the threshold
		lastActivity := s.CreatedAt
		if s.LastTickAt != nil {
			lastActivity = *s.LastTickAt
		}
		staleThreshold := lastActivity.Add(time.Duration(s.StaleTimeoutSec) * time.Second)
		if staleThreshold.Before(before) {
			cp := *s
			result = append(result, &cp)
			if len(result) >= limit {
				break
			}
		}
	}
	return result, nil
}

func (m *MemoryStore) CreateTick(_ context.Context, tick *Tick) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Enforce unique (stream_id, seq)
	for _, existing := range m.ticks[tick.StreamID] {
		if existing.Seq == tick.Seq {
			return ErrDuplicateTickSeq
		}
	}

	m.ticks[tick.StreamID] = append(m.ticks[tick.StreamID], tick)
	return nil
}

func (m *MemoryStore) ListTicks(_ context.Context, streamID string, limit int) ([]*Tick, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ticks := m.ticks[streamID]
	if len(ticks) > limit {
		// Return most recent ticks
		ticks = ticks[len(ticks)-limit:]
	}

	// Return copies
	result := make([]*Tick, len(ticks))
	for i, t := range ticks {
		cp := *t
		result[i] = &cp
	}
	return result, nil
}

func (m *MemoryStore) GetLastTick(_ context.Context, streamID string) (*Tick, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ticks := m.ticks[streamID]
	if len(ticks) == 0 {
		return nil, nil
	}
	cp := *ticks[len(ticks)-1]
	return &cp, nil
}

// Compile-time assertion that MemoryStore implements Store.
var _ Store = (*MemoryStore)(nil)
