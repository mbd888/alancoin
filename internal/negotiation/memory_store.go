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
	rfps      map[string]*RFP
	bids      map[string]*Bid
	templates map[string]*RFPTemplate
	mu        sync.RWMutex
}

// NewMemoryStore creates a new in-memory negotiation store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		rfps:      make(map[string]*RFP),
		bids:      make(map[string]*Bid),
		templates: make(map[string]*RFPTemplate),
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

func (m *MemoryStore) ListStaleSelecting(_ context.Context, before time.Time, limit int) ([]*RFP, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*RFP
	for _, r := range m.rfps {
		if r.Status != RFPStatusSelecting {
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

func (m *MemoryStore) GetAnalytics(_ context.Context) (*Analytics, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	a := &Analytics{}
	a.TotalRFPs = len(m.rfps)

	var totalAwardSecs float64
	var awardCount int
	var zeroBidExpired int

	for _, r := range m.rfps {
		switch r.Status {
		case RFPStatusOpen:
			a.OpenRFPs++
		case RFPStatusAwarded:
			a.AwardedRFPs++
			if r.AwardedAt != nil {
				totalAwardSecs += r.AwardedAt.Sub(r.CreatedAt).Seconds()
				awardCount++
			}
		case RFPStatusExpired:
			a.ExpiredRFPs++
			if r.BidCount == 0 {
				zeroBidExpired++
			}
		case RFPStatusCancelled:
			a.CancelledRFPs++
		}
	}

	if awardCount > 0 {
		a.AvgTimeToAwardSecs = totalAwardSecs / float64(awardCount)
	}
	terminal := a.ExpiredRFPs + a.AwardedRFPs
	if terminal > 0 {
		a.AbandonmentRate = float64(zeroBidExpired) / float64(terminal)
	}

	// Bids per RFP
	bidsByRFP := make(map[string]int)
	sellerStats := make(map[string]*SellerWinSummary)
	var totalSpread float64
	var spreadCount int
	counterRFPs := make(map[string]bool)

	for _, b := range m.bids {
		bidsByRFP[b.RFPID]++

		ss, ok := sellerStats[b.SellerAddr]
		if !ok {
			ss = &SellerWinSummary{SellerAddr: b.SellerAddr}
			sellerStats[b.SellerAddr] = ss
		}
		ss.TotalBids++
		if b.Status == BidStatusAccepted {
			ss.Wins++
		}
		if b.Status == BidStatusCountered {
			counterRFPs[b.RFPID] = true
		}

		// Bid-to-ask spread
		if rfp, ok := m.rfps[b.RFPID]; ok {
			maxB := parseFloat(rfp.MaxBudget)
			if maxB > 0 {
				bidB := parseFloat(b.TotalBudget)
				minB := parseFloat(rfp.MinBudget)
				totalSpread += (bidB - minB) / maxB
				spreadCount++
			}
		}
	}

	if len(bidsByRFP) > 0 {
		total := 0
		for _, c := range bidsByRFP {
			total += c
		}
		a.AvgBidsPerRFP = float64(total) / float64(len(bidsByRFP))
	}
	if spreadCount > 0 {
		a.AvgBidToAskSpread = totalSpread / float64(spreadCount)
	}

	// Counter-offer efficiency
	if len(counterRFPs) > 0 {
		awarded := 0
		for rfpID := range counterRFPs {
			if rfp, ok := m.rfps[rfpID]; ok && rfp.Status == RFPStatusAwarded {
				awarded++
			}
		}
		a.CounterEfficiency = float64(awarded) / float64(len(counterRFPs))
	}

	// Top sellers sorted by wins desc
	a.TopSellers = make([]SellerWinSummary, 0, len(sellerStats))
	for _, ss := range sellerStats {
		if ss.TotalBids > 0 {
			ss.WinRate = float64(ss.Wins) / float64(ss.TotalBids)
		}
		a.TopSellers = append(a.TopSellers, *ss)
	}
	sort.Slice(a.TopSellers, func(i, j int) bool {
		if a.TopSellers[i].Wins != a.TopSellers[j].Wins {
			return a.TopSellers[i].Wins > a.TopSellers[j].Wins
		}
		return a.TopSellers[i].TotalBids > a.TopSellers[j].TotalBids
	})
	if len(a.TopSellers) > 10 {
		a.TopSellers = a.TopSellers[:10]
	}

	return a, nil
}

func (m *MemoryStore) CreateTemplate(_ context.Context, tmpl *RFPTemplate) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.templates[tmpl.ID] = tmpl
	return nil
}

func (m *MemoryStore) GetTemplate(_ context.Context, id string) (*RFPTemplate, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	tmpl, ok := m.templates[id]
	if !ok {
		return nil, ErrTemplateNotFound
	}
	cp := *tmpl
	return &cp, nil
}

func (m *MemoryStore) ListTemplates(_ context.Context, ownerAddr string, limit int) ([]*RFPTemplate, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	addr := strings.ToLower(ownerAddr)
	var result []*RFPTemplate
	for _, t := range m.templates {
		// Return system templates (ownerAddr="") and templates owned by this buyer
		if t.OwnerAddr == "" || t.OwnerAddr == addr {
			cp := *t
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

func (m *MemoryStore) DeleteTemplate(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.templates[id]; !ok {
		return ErrTemplateNotFound
	}
	delete(m.templates, id)
	return nil
}

// Compile-time assertion that MemoryStore implements Store.
var _ Store = (*MemoryStore)(nil)
