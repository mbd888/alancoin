package escrow

import (
	"context"
	"log"
	"math/big"
	"sort"
	"time"

	"github.com/mbd888/alancoin/internal/usdc"
)

// EscrowAnalytics provides aggregate metrics across escrows.
type EscrowAnalytics struct {
	TotalCount          int            `json:"totalCount"`
	AvgDeliveryTimeSecs float64        `json:"avgDeliveryTimeSecs"`
	DisputeRate         float64        `json:"disputeRate"`     // 0-100
	AutoReleaseRate     float64        `json:"autoReleaseRate"` // 0-100
	AvgAmount           string         `json:"avgAmount"`
	TotalVolume         string         `json:"totalVolume"`
	ByStatus            map[string]int `json:"byStatus"`
	TopSellers          []SellerStats  `json:"topSellers"`
}

// SellerStats provides per-seller aggregate info.
type SellerStats struct {
	SellerAddr  string `json:"sellerAddr"`
	EscrowCount int    `json:"escrowCount"`
	TotalVolume string `json:"totalVolume"`
}

// AnalyticsFilter allows filtering escrow analytics.
type AnalyticsFilter struct {
	SellerAddr string
	ServiceID  string
	From       *time.Time
	To         *time.Time
}

// AnalyticsQuerier provides read access to escrows for analytics.
type AnalyticsQuerier interface {
	QueryForAnalytics(ctx context.Context, filter AnalyticsFilter, limit int) ([]*Escrow, error)
}

// EscrowAnalyticsService computes analytics from escrow data.
type EscrowAnalyticsService struct {
	querier AnalyticsQuerier
}

// NewEscrowAnalyticsService creates an analytics service.
func NewEscrowAnalyticsService(q AnalyticsQuerier) *EscrowAnalyticsService {
	return &EscrowAnalyticsService{querier: q}
}

// GetAnalytics computes aggregate escrow analytics.
func (a *EscrowAnalyticsService) GetAnalytics(ctx context.Context, filter AnalyticsFilter) (*EscrowAnalytics, error) {
	escrows, err := a.querier.QueryForAnalytics(ctx, filter, 10000)
	if err != nil {
		return nil, err
	}

	result := &EscrowAnalytics{
		ByStatus: make(map[string]int),
	}

	totalVolume := new(big.Int)
	var deliveryTimes []float64
	disputeCount := 0
	autoReleaseCount := 0
	sellerVolumes := make(map[string]*big.Int)
	sellerCounts := make(map[string]int)

	for _, e := range escrows {
		result.TotalCount++
		result.ByStatus[string(e.Status)]++

		amountBig, ok := usdc.Parse(e.Amount)
		if !ok {
			log.Printf("WARNING: escrow %s has corrupted amount: %q", e.ID, e.Amount)
			continue
		}
		totalVolume.Add(totalVolume, amountBig)

		// Track seller stats
		if _, ok := sellerVolumes[e.SellerAddr]; !ok {
			sellerVolumes[e.SellerAddr] = new(big.Int)
		}
		sellerVolumes[e.SellerAddr].Add(sellerVolumes[e.SellerAddr], amountBig)
		sellerCounts[e.SellerAddr]++

		// Track delivery time (from creation to delivery)
		if e.DeliveredAt != nil {
			deliveryDuration := e.DeliveredAt.Sub(e.CreatedAt).Seconds()
			if deliveryDuration > 0 {
				deliveryTimes = append(deliveryTimes, deliveryDuration)
			}
		}

		// Track disputes and auto-releases
		if e.Status == StatusDisputed || e.Status == StatusArbitrating || e.Status == StatusRefunded {
			disputeCount++
		}
		if e.Status == StatusExpired {
			autoReleaseCount++
		}
	}

	result.TotalVolume = usdc.Format(totalVolume)

	if result.TotalCount > 0 {
		// Average amount
		avg := new(big.Int).Div(totalVolume, big.NewInt(int64(result.TotalCount)))
		result.AvgAmount = usdc.Format(avg)

		// Dispute rate
		result.DisputeRate = float64(disputeCount) / float64(result.TotalCount) * 100

		// Auto-release rate
		result.AutoReleaseRate = float64(autoReleaseCount) / float64(result.TotalCount) * 100
	} else {
		result.AvgAmount = "0.000000"
	}

	// Average delivery time
	if len(deliveryTimes) > 0 {
		sum := 0.0
		for _, dt := range deliveryTimes {
			sum += dt
		}
		result.AvgDeliveryTimeSecs = sum / float64(len(deliveryTimes))
	}

	// Top sellers by volume (top 10)
	type sellerEntry struct {
		addr   string
		volume *big.Int
		count  int
	}
	var sellers []sellerEntry
	for addr, vol := range sellerVolumes {
		sellers = append(sellers, sellerEntry{addr, vol, sellerCounts[addr]})
	}
	sort.Slice(sellers, func(i, j int) bool {
		return sellers[i].volume.Cmp(sellers[j].volume) > 0
	})
	if len(sellers) > 10 {
		sellers = sellers[:10]
	}
	result.TopSellers = make([]SellerStats, 0, len(sellers))
	for _, s := range sellers {
		result.TopSellers = append(result.TopSellers, SellerStats{
			SellerAddr:  s.addr,
			EscrowCount: s.count,
			TotalVolume: usdc.Format(s.volume),
		})
	}

	return result, nil
}
