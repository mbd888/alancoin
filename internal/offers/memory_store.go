package offers

import (
	"context"
	"strings"
	"sync"
	"time"
)

// MemoryStore is an in-memory store for demo/development.
type MemoryStore struct {
	offers map[string]*Offer
	claims map[string]*Claim
	mu     sync.RWMutex
}

// NewMemoryStore creates a new in-memory offers store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		offers: make(map[string]*Offer),
		claims: make(map[string]*Claim),
	}
}

func (m *MemoryStore) CreateOffer(ctx context.Context, o *Offer) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.offers[o.ID] = copyOffer(o)
	return nil
}

func (m *MemoryStore) GetOffer(ctx context.Context, id string) (*Offer, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	o, ok := m.offers[id]
	if !ok {
		return nil, ErrOfferNotFound
	}
	return copyOffer(o), nil
}

func (m *MemoryStore) UpdateOffer(ctx context.Context, o *Offer) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.offers[o.ID]; !ok {
		return ErrOfferNotFound
	}
	m.offers[o.ID] = copyOffer(o)
	return nil
}

func (m *MemoryStore) ListOffers(ctx context.Context, serviceType string, limit int) ([]*Offer, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	st := strings.ToLower(serviceType)
	var result []*Offer
	for _, o := range m.offers {
		if o.Status != OfferActive {
			continue
		}
		if st != "" && strings.ToLower(o.ServiceType) != st {
			continue
		}
		result = append(result, copyOffer(o))
		if len(result) >= limit {
			break
		}
	}
	return result, nil
}

func (m *MemoryStore) ListOffersBySeller(ctx context.Context, sellerAddr string, limit int) ([]*Offer, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []*Offer
	for _, o := range m.offers {
		if o.SellerAddr == sellerAddr {
			result = append(result, copyOffer(o))
			if len(result) >= limit {
				break
			}
		}
	}
	return result, nil
}

func (m *MemoryStore) ListExpiredOffers(ctx context.Context, before time.Time, limit int) ([]*Offer, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []*Offer
	for _, o := range m.offers {
		if !o.IsTerminal() && o.ExpiresAt.Before(before) {
			result = append(result, copyOffer(o))
			if len(result) >= limit {
				break
			}
		}
	}
	return result, nil
}

func (m *MemoryStore) CreateClaim(ctx context.Context, c *Claim) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.claims[c.ID] = copyClaim(c)
	return nil
}

func (m *MemoryStore) GetClaim(ctx context.Context, id string) (*Claim, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c, ok := m.claims[id]
	if !ok {
		return nil, ErrClaimNotFound
	}
	return copyClaim(c), nil
}

func (m *MemoryStore) UpdateClaim(ctx context.Context, c *Claim) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.claims[c.ID]; !ok {
		return ErrClaimNotFound
	}
	m.claims[c.ID] = copyClaim(c)
	return nil
}

func (m *MemoryStore) ListClaimsByOffer(ctx context.Context, offerID string, limit int) ([]*Claim, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []*Claim
	for _, c := range m.claims {
		if c.OfferID == offerID {
			result = append(result, copyClaim(c))
			if len(result) >= limit {
				break
			}
		}
	}
	return result, nil
}

func (m *MemoryStore) ListClaimsByBuyer(ctx context.Context, buyerAddr string, limit int) ([]*Claim, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []*Claim
	for _, c := range m.claims {
		if c.BuyerAddr == buyerAddr {
			result = append(result, copyClaim(c))
			if len(result) >= limit {
				break
			}
		}
	}
	return result, nil
}

func copyOffer(o *Offer) *Offer {
	cp := *o
	if o.Conditions != nil {
		cp.Conditions = make([]Condition, len(o.Conditions))
		copy(cp.Conditions, o.Conditions)
	}
	return &cp
}

func copyClaim(c *Claim) *Claim {
	cp := *c
	if c.ResolvedAt != nil {
		t := *c.ResolvedAt
		cp.ResolvedAt = &t
	}
	return &cp
}

var _ Store = (*MemoryStore)(nil)
