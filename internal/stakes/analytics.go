package stakes

import (
	"context"
	"log"
	"math"
	"math/big"
	"time"

	"github.com/mbd888/alancoin/internal/usdc"
)

// PortfolioAnalytics provides computed analytics across all holdings for an investor.
type PortfolioAnalytics struct {
	InvestorAddr         string              `json:"investorAddr"`
	TotalInvested        string              `json:"totalInvested"`
	TotalEarned          string              `json:"totalEarned"`
	ROI                  float64             `json:"roi"`                  // percentage
	AnnualizedReturn     float64             `json:"annualizedReturn"`     // percentage
	DiversificationIndex float64             `json:"diversificationIndex"` // 0-1, higher = more diversified
	HoldingCount         int                 `json:"holdingCount"`
	ActiveHoldings       int                 `json:"activeHoldings"`
	Holdings             []*HoldingAnalytics `json:"holdings"`
}

// HoldingAnalytics provides per-holding performance metrics.
type HoldingAnalytics struct {
	HoldingID   string  `json:"holdingId"`
	StakeID     string  `json:"stakeId"`
	AgentAddr   string  `json:"agentAddr"`
	Shares      int     `json:"shares"`
	CostBasis   string  `json:"costBasis"`
	TotalEarned string  `json:"totalEarned"`
	ROI         float64 `json:"roi"` // percentage
	SharePct    float64 `json:"sharePct"`
	Status      string  `json:"status"`
	Vested      bool    `json:"vested"`
}

// StakeNAV provides net asset value computation for a stake.
type StakeNAV struct {
	StakeID        string `json:"stakeId"`
	IssuedShares   int    `json:"issuedShares"`
	TotalRaised    string `json:"totalRaised"`
	Undistributed  string `json:"undistributed"`
	TotalPoolValue string `json:"totalPoolValue"` // totalRaised + undistributed
	NAVPerShare    string `json:"navPerShare"`
	PricePerShare  string `json:"pricePerShare"` // original offering price
	Status         string `json:"status"`
}

// StakePerformance provides performance metrics for a stake offering.
type StakePerformance struct {
	StakeID             string          `json:"stakeId"`
	TotalRaised         string          `json:"totalRaised"`
	TotalDistributed    string          `json:"totalDistributed"`
	Undistributed       string          `json:"undistributed"`
	CumulativeReturn    float64         `json:"cumulativeReturn"` // percentage: distributed / raised
	IssuedShares        int             `json:"issuedShares"`
	HolderCount         int             `json:"holderCount"`
	DistributionCount   int             `json:"distributionCount"`
	AvgPerSharePayout   string          `json:"avgPerSharePayout"`
	RecentDistributions []*Distribution `json:"recentDistributions"`
}

// StakeAnalyticsService computes analytics from existing stake data.
type StakeAnalyticsService struct {
	store Store
}

// NewStakeAnalyticsService creates an analytics service backed by the given store.
func NewStakeAnalyticsService(store Store) *StakeAnalyticsService {
	return &StakeAnalyticsService{store: store}
}

// GetPortfolioAnalytics computes portfolio analytics for an investor.
func (a *StakeAnalyticsService) GetPortfolioAnalytics(ctx context.Context, investorAddr string) (*PortfolioAnalytics, error) {
	holdings, err := a.store.ListHoldingsByInvestor(ctx, investorAddr)
	if err != nil {
		return nil, err
	}

	result := &PortfolioAnalytics{
		InvestorAddr: investorAddr,
		Holdings:     make([]*HoldingAnalytics, 0, len(holdings)),
	}

	totalInvested := new(big.Int)
	totalEarned := new(big.Int)
	now := time.Now()
	var earliestCreated time.Time

	// Per-holding cost basis for Herfindahl computation
	costBases := make([]*big.Int, 0, len(holdings))

	for _, h := range holdings {
		costBig, ok := usdc.Parse(h.CostBasis)
		if !ok {
			log.Printf("WARNING: holding %s has corrupted cost basis: %q", h.ID, h.CostBasis)
			continue
		}
		earnedBig, ok := usdc.Parse(h.TotalEarned)
		if !ok {
			log.Printf("WARNING: holding %s has corrupted total earned: %q", h.ID, h.TotalEarned)
			continue
		}
		totalInvested.Add(totalInvested, costBig)
		totalEarned.Add(totalEarned, earnedBig)
		costBases = append(costBases, costBig)

		if earliestCreated.IsZero() || h.CreatedAt.Before(earliestCreated) {
			earliestCreated = h.CreatedAt
		}

		// Get stake for agent addr and share percentage
		agentAddr := ""
		sharePct := 0.0
		stake, err := a.store.GetStake(ctx, h.StakeID)
		if err == nil {
			agentAddr = stake.AgentAddr
			issuedShares := stake.TotalShares - stake.AvailableShares
			if issuedShares > 0 {
				sharePct = float64(h.Shares) / float64(issuedShares) * 100.0
			}
		}

		// Per-holding ROI
		holdingROI := 0.0
		if costBig.Sign() > 0 {
			earnedF := new(big.Float).SetInt(earnedBig)
			costF := new(big.Float).SetInt(costBig)
			ratio, _ := new(big.Float).Quo(earnedF, costF).Float64()
			holdingROI = ratio * 100
		}

		if h.Status != string(HoldingStatusLiquidated) {
			result.ActiveHoldings++
		}

		result.Holdings = append(result.Holdings, &HoldingAnalytics{
			HoldingID:   h.ID,
			StakeID:     h.StakeID,
			AgentAddr:   agentAddr,
			Shares:      h.Shares,
			CostBasis:   h.CostBasis,
			TotalEarned: h.TotalEarned,
			ROI:         holdingROI,
			SharePct:    sharePct,
			Status:      h.Status,
			Vested:      h.IsVested(now),
		})
	}

	result.TotalInvested = usdc.Format(totalInvested)
	result.TotalEarned = usdc.Format(totalEarned)
	result.HoldingCount = len(holdings)

	// Portfolio ROI
	if totalInvested.Sign() > 0 {
		earnedF := new(big.Float).SetInt(totalEarned)
		investedF := new(big.Float).SetInt(totalInvested)
		ratio, _ := new(big.Float).Quo(earnedF, investedF).Float64()
		result.ROI = ratio * 100

		// Annualized return
		if !earliestCreated.IsZero() {
			daysSince := now.Sub(earliestCreated).Hours() / 24
			if daysSince >= 1 {
				result.AnnualizedReturn = result.ROI * (365.0 / daysSince)
			}
		}

		// Herfindahl-Hirschman Index for diversification
		// HHI = sum((costBasis_i / totalInvested)^2)
		// Diversification = 1 - HHI (0 = all in one, approaching 1 = well diversified)
		if len(costBases) > 1 {
			hhi := 0.0
			investedF := new(big.Float).SetInt(totalInvested)
			for _, cb := range costBases {
				cbF := new(big.Float).SetInt(cb)
				weight, _ := new(big.Float).Quo(cbF, investedF).Float64()
				hhi += weight * weight
			}
			result.DiversificationIndex = 1 - hhi
		}
		// Single holding: diversification stays 0
	}

	return result, nil
}

// GetStakeNAV computes net asset value for a stake offering.
func (a *StakeAnalyticsService) GetStakeNAV(ctx context.Context, stakeID string) (*StakeNAV, error) {
	stake, err := a.store.GetStake(ctx, stakeID)
	if err != nil {
		return nil, err
	}

	issuedShares := stake.TotalShares - stake.AvailableShares

	raisedBig, _ := usdc.Parse(stake.TotalRaised)
	undistBig, _ := usdc.Parse(stake.Undistributed)
	poolValue := new(big.Int).Add(raisedBig, undistBig)

	navPerShare := new(big.Int)
	if issuedShares > 0 {
		navPerShare.Div(poolValue, big.NewInt(int64(issuedShares)))
	}

	return &StakeNAV{
		StakeID:        stake.ID,
		IssuedShares:   issuedShares,
		TotalRaised:    stake.TotalRaised,
		Undistributed:  stake.Undistributed,
		TotalPoolValue: usdc.Format(poolValue),
		NAVPerShare:    usdc.Format(navPerShare),
		PricePerShare:  stake.PricePerShare,
		Status:         stake.Status,
	}, nil
}

// GetStakePerformance computes performance metrics for a stake offering.
func (a *StakeAnalyticsService) GetStakePerformance(ctx context.Context, stakeID string) (*StakePerformance, error) {
	stake, err := a.store.GetStake(ctx, stakeID)
	if err != nil {
		return nil, err
	}

	holdings, err := a.store.ListHoldingsByStake(ctx, stakeID)
	if err != nil {
		return nil, err
	}

	distributions, err := a.store.ListDistributions(ctx, stakeID, 100)
	if err != nil {
		return nil, err
	}

	issuedShares := stake.TotalShares - stake.AvailableShares

	// Cumulative return: totalDistributed / totalRaised
	cumulativeReturn := 0.0
	raisedBig, _ := usdc.Parse(stake.TotalRaised)
	distBig, _ := usdc.Parse(stake.TotalDistributed)
	if raisedBig.Sign() > 0 {
		distF := new(big.Float).SetInt(distBig)
		raisedF := new(big.Float).SetInt(raisedBig)
		ratio, _ := new(big.Float).Quo(distF, raisedF).Float64()
		cumulativeReturn = ratio * 100
	}

	// Average per-share payout across distributions
	avgPerShare := new(big.Int)
	if len(distributions) > 0 {
		totalPerShare := new(big.Int)
		for _, d := range distributions {
			ps, _ := usdc.Parse(d.PerShareAmount)
			totalPerShare.Add(totalPerShare, ps)
		}
		avgPerShare.Div(totalPerShare, big.NewInt(int64(len(distributions))))
	}

	// Count active holders (non-liquidated)
	holderCount := 0
	for _, h := range holdings {
		if h.Status != string(HoldingStatusLiquidated) {
			holderCount++
		}
	}

	// Cap recent distributions to last 10
	recentDists := distributions
	if len(recentDists) > 10 {
		recentDists = recentDists[:10]
	}

	return &StakePerformance{
		StakeID:             stake.ID,
		TotalRaised:         stake.TotalRaised,
		TotalDistributed:    stake.TotalDistributed,
		Undistributed:       stake.Undistributed,
		CumulativeReturn:    math.Round(cumulativeReturn*100) / 100,
		IssuedShares:        issuedShares,
		HolderCount:         holderCount,
		DistributionCount:   len(distributions),
		AvgPerSharePayout:   usdc.Format(avgPerShare),
		RecentDistributions: recentDists,
	}, nil
}
