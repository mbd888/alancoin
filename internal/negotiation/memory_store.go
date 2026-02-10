package negotiation

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"
)

// MemoryStore is an in-memory negotiation store for demo/development mode.
type MemoryStore struct {
	rfps map[string]*RFP
	bids map[string]*Bid
	mu   sync.RWMutex
}

// NewMemoryStore creates a new in-memory negotiation store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		rfps: make(map[string]*RFP),
		bids: make(map[string]*Bid),
	}
}

func (m *MemoryStore) CreateRFP(_ context.Context, rfp *RFP) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rfps[rfp.ID] = rfp
	return nil
}

func (m *MemoryStore) GetRFP(_ context.Context, id string) (*RFP, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	rfp, ok := m.rfps[id]
	if !ok {
		return nil, ErrRFPNotFound
	}
	cp := *rfp
	return &cp, nil
}

func (m *MemoryStore) UpdateRFP(_ context.Context, rfp *RFP) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.rfps[rfp.ID]; !ok {
		return ErrRFPNotFound
	}
	m.rfps[rfp.ID] = rfp
	return nil
}

func (m *MemoryStore) ListOpenRFPs(_ context.Context, serviceType string, limit int) ([]*RFP, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*RFP
	for _, r := range m.rfps {
		if r.Status != RFPStatusOpen {
			continue
		}
		if serviceType != "" && !strings.EqualFold(r.ServiceType, serviceType) {
			continue
		}
		cp := *r
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

func (m *MemoryStore) ListByBuyer(_ context.Context, buyerAddr string, limit int) ([]*RFP, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	addr := strings.ToLower(buyerAddr)
	var result []*RFP
	for _, r := range m.rfps {
		if r.BuyerAddr == addr {
			cp := *r
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

func (m *MemoryStore) ListExpiredRFPs(_ context.Context, before time.Time, limit int) ([]*RFP, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*RFP
	for _, r := range m.rfps {
		if r.Status != RFPStatusOpen || r.AutoSelect {
			continue
		}
		if r.BidDeadline.Before(before) {
			cp := *r
			result = append(result, &cp)
			if len(result) >= limit {
				break
			}
		}
	}
	return result, nil
}

func (m *MemoryStore) ListAutoSelectReady(_ context.Context, before time.Time, limit int) ([]*RFP, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*RFP
	for _, r := range m.rfps {
		if r.Status != RFPStatusOpen || !r.AutoSelect {
			continue
		}
		if r.BidDeadline.Before(before) {
			cp := *r
			result = append(result, &cp)
			if len(result) >= limit {
				break
			}
		}
	}
	return result, nil
}

func (m *MemoryStore) CreateBid(_ context.Context, bid *Bid) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.bids[bid.ID] = bid
	return nil
}

func (m *MemoryStore) GetBid(_ context.Context, id string) (*Bid, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	bid, ok := m.bids[id]
	if !ok {
		return nil, ErrBidNotFound
	}
	cp := *bid
	return &cp, nil
}

func (m *MemoryStore) UpdateBid(_ context.Context, bid *Bid) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.bids[bid.ID]; !ok {
		return ErrBidNotFound
	}
	m.bids[bid.ID] = bid
	return nil
}

func (m *MemoryStore) ListBidsByRFP(_ context.Context, rfpID string, limit int) ([]*Bid, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*Bid
	for _, b := range m.bids {
		if b.RFPID == rfpID {
			cp := *b
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

func (m *MemoryStore) ListActiveBidsByRFP(_ context.Context, rfpID string) ([]*Bid, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*Bid
	for _, b := range m.bids {
		if b.RFPID == rfpID && b.Status == BidStatusPending {
			cp := *b
			result = append(result, &cp)
		}
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Score > result[j].Score
	})
	return result, nil
}

func (m *MemoryStore) GetBidBySellerAndRFP(_ context.Context, sellerAddr, rfpID string) (*Bid, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	addr := strings.ToLower(sellerAddr)
	for _, b := range m.bids {
		if b.RFPID == rfpID && b.SellerAddr == addr && b.Status == BidStatusPending {
			cp := *b
			return &cp, nil
		}
	}
	return nil, ErrBidNotFound
}

func (m *MemoryStore) ListBidsBySeller(_ context.Context, sellerAddr string, limit int) ([]*Bid, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	addr := strings.ToLower(sellerAddr)
	var result []*Bid
	for _, b := range m.bids {
		if b.SellerAddr == addr {
			cp := *b
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

// Compile-time assertion that MemoryStore implements Store.
var _ Store = (*MemoryStore)(nil)
