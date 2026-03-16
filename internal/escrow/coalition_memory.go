package escrow

import (
	"context"
	"strings"
	"sync"
	"time"
)

// CoalitionMemoryStore is an in-memory coalition escrow store for demo/development.
type CoalitionMemoryStore struct {
	escrows map[string]*CoalitionEscrow
	mu      sync.RWMutex
}

// NewCoalitionMemoryStore creates a new in-memory coalition escrow store.
func NewCoalitionMemoryStore() *CoalitionMemoryStore {
	return &CoalitionMemoryStore{
		escrows: make(map[string]*CoalitionEscrow),
	}
}

func (m *CoalitionMemoryStore) Create(ctx context.Context, ce *CoalitionEscrow) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.escrows[ce.ID] = deepCopyCoalition(ce)
	return nil
}

func (m *CoalitionMemoryStore) Get(ctx context.Context, id string) (*CoalitionEscrow, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ce, ok := m.escrows[id]
	if !ok {
		return nil, ErrCoalitionNotFound
	}
	return deepCopyCoalition(ce), nil
}

func (m *CoalitionMemoryStore) Update(ctx context.Context, ce *CoalitionEscrow) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.escrows[ce.ID]; !ok {
		return ErrCoalitionNotFound
	}
	m.escrows[ce.ID] = deepCopyCoalition(ce)
	return nil
}

func (m *CoalitionMemoryStore) ListByAgent(ctx context.Context, agentAddr string, limit int) ([]*CoalitionEscrow, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	addr := strings.ToLower(agentAddr)
	var result []*CoalitionEscrow
	for _, ce := range m.escrows {
		if ce.BuyerAddr == addr || ce.OracleAddr == addr || coalitionHasMember(ce, addr) {
			result = append(result, deepCopyCoalition(ce))
			if len(result) >= limit {
				break
			}
		}
	}
	return result, nil
}

func (m *CoalitionMemoryStore) ListExpired(ctx context.Context, before time.Time, limit int) ([]*CoalitionEscrow, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*CoalitionEscrow
	for _, ce := range m.escrows {
		if !ce.IsTerminal() && ce.AutoSettleAt.Before(before) {
			result = append(result, deepCopyCoalition(ce))
			if len(result) >= limit {
				break
			}
		}
	}
	return result, nil
}

func coalitionHasMember(ce *CoalitionEscrow, addr string) bool {
	for _, m := range ce.Members {
		if m.AgentAddr == addr {
			return true
		}
	}
	return false
}

// deepCopyCoalition returns a deep copy to prevent races on shared pointers.
func deepCopyCoalition(ce *CoalitionEscrow) *CoalitionEscrow {
	cp := *ce

	// Deep copy members
	if ce.Members != nil {
		cp.Members = make([]CoalitionMember, len(ce.Members))
		for i, m := range ce.Members {
			cp.Members[i] = m
			if m.CompletedAt != nil {
				t := *m.CompletedAt
				cp.Members[i].CompletedAt = &t
			}
		}
	}

	// Deep copy quality tiers
	if ce.QualityTiers != nil {
		cp.QualityTiers = make([]QualityTier, len(ce.QualityTiers))
		copy(cp.QualityTiers, ce.QualityTiers)
	}

	// Deep copy contributions map
	if ce.Contributions != nil {
		cp.Contributions = make(map[string]float64, len(ce.Contributions))
		for k, v := range ce.Contributions {
			cp.Contributions[k] = v
		}
	}

	// Deep copy optional time pointers
	if ce.QualityScore != nil {
		q := *ce.QualityScore
		cp.QualityScore = &q
	}
	if ce.SettledAt != nil {
		t := *ce.SettledAt
		cp.SettledAt = &t
	}

	return &cp
}

// Compile-time assertion.
var _ CoalitionStore = (*CoalitionMemoryStore)(nil)
