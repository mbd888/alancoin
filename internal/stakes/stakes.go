// Package stakes provides agent revenue staking — invest in AI agents and
// earn from their revenue.
//
// Flow:
//  1. Agent creates offering → specifies revenue share, total shares, price
//  2. Investor buys shares → payment debited, holding created with vesting
//  3. Agent earns revenue → platform escrows the revenue share portion
//  4. Distribution timer fires → escrowed revenue paid proportionally to holders
//  5. After vesting, holders can trade shares on secondary market
package stakes

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"
)

// Errors
var (
	ErrStakeNotFound     = errors.New("stake not found")
	ErrHoldingNotFound   = errors.New("holding not found")
	ErrOrderNotFound     = errors.New("order not found")
	ErrStakeClosed       = errors.New("stake offering is closed")
	ErrInsufficientShare = errors.New("not enough available shares")
	ErrSelfInvestment    = errors.New("agent cannot invest in their own stake")
	ErrMaxRevenueShare   = errors.New("total revenue share would exceed 50%")
	ErrInvalidAmount     = errors.New("invalid amount")
	ErrNotVested         = errors.New("shares have not vested yet")
	ErrInsufficientHeld  = errors.New("holding does not have enough shares")
	ErrUnauthorized      = errors.New("not authorized for this operation")
	ErrOrderNotOpen      = errors.New("order is not open")
)

// MaxRevenueShareBPS is the maximum total revenue share across all of an
// agent's active stakes (50% = 5000 basis points).
const MaxRevenueShareBPS = 5000

// StakeStatus represents the lifecycle state of a stake offering.
type StakeStatus string

const (
	StakeStatusOpen   StakeStatus = "open"
	StakeStatusPaused StakeStatus = "paused"
	StakeStatusClosed StakeStatus = "closed"
)

// HoldingStatus represents the lifecycle state of a holding.
type HoldingStatus string

const (
	HoldingStatusVesting    HoldingStatus = "vesting"
	HoldingStatusActive     HoldingStatus = "active"
	HoldingStatusLiquidated HoldingStatus = "liquidated"
)

// OrderStatus represents the state of a secondary market order.
type OrderStatus string

const (
	OrderStatusOpen      OrderStatus = "open"
	OrderStatusFilled    OrderStatus = "filled"
	OrderStatusCancelled OrderStatus = "cancelled"
)

// Stake is a revenue-sharing offering by an agent.
type Stake struct {
	ID                string     `json:"id"`
	AgentAddr         string     `json:"agentAddr"`
	RevenueShareBPS   int        `json:"revenueShareBps"`
	TotalShares       int        `json:"totalShares"`
	AvailableShares   int        `json:"availableShares"`
	PricePerShare     string     `json:"pricePerShare"`
	VestingPeriod     string     `json:"vestingPeriod"`
	DistributionFreq  string     `json:"distributionFreq"`
	Status            string     `json:"status"`
	TotalRaised       string     `json:"totalRaised"`
	TotalDistributed  string     `json:"totalDistributed"`
	Undistributed     string     `json:"undistributed"`
	LastDistributedAt *time.Time `json:"lastDistributedAt,omitempty"`
	CreatedAt         time.Time  `json:"createdAt"`
	UpdatedAt         time.Time  `json:"updatedAt"`
}

// Holding represents shares owned by an investor in a stake offering.
type Holding struct {
	ID           string    `json:"id"`
	StakeID      string    `json:"stakeId"`
	InvestorAddr string    `json:"investorAddr"`
	Shares       int       `json:"shares"`
	CostBasis    string    `json:"costBasis"`
	VestedAt     time.Time `json:"vestedAt"`
	Status       string    `json:"status"`
	TotalEarned  string    `json:"totalEarned"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// IsVested returns true if the holding has passed its vesting date.
func (h *Holding) IsVested(now time.Time) bool {
	return !now.Before(h.VestedAt)
}

// Distribution records a revenue payout event for a stake.
type Distribution struct {
	ID             string    `json:"id"`
	StakeID        string    `json:"stakeId"`
	AgentAddr      string    `json:"agentAddr"`
	RevenueAmount  string    `json:"revenueAmount"`
	ShareAmount    string    `json:"shareAmount"`
	PerShareAmount string    `json:"perShareAmount"`
	ShareCount     int       `json:"shareCount"`
	HoldingCount   int       `json:"holdingCount"`
	Status         string    `json:"status"`
	CreatedAt      time.Time `json:"createdAt"`
}

// Order is a sell order on the secondary market.
type Order struct {
	ID            string    `json:"id"`
	StakeID       string    `json:"stakeId"`
	HoldingID     string    `json:"holdingId"`
	SellerAddr    string    `json:"sellerAddr"`
	Shares        int       `json:"shares"`
	PricePerShare string    `json:"pricePerShare"`
	Status        string    `json:"status"`
	FilledShares  int       `json:"filledShares"`
	BuyerAddr     string    `json:"buyerAddr,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

// Store persists stakes data.
type Store interface {
	// Stakes
	CreateStake(ctx context.Context, stake *Stake) error
	GetStake(ctx context.Context, id string) (*Stake, error)
	UpdateStake(ctx context.Context, stake *Stake) error
	ListByAgent(ctx context.Context, agentAddr string) ([]*Stake, error)
	ListOpen(ctx context.Context, limit int) ([]*Stake, error)
	ListDueForDistribution(ctx context.Context, now time.Time, limit int) ([]*Stake, error)
	GetAgentTotalShareBPS(ctx context.Context, agentAddr string) (int, error)

	// Holdings
	CreateHolding(ctx context.Context, h *Holding) error
	GetHolding(ctx context.Context, id string) (*Holding, error)
	UpdateHolding(ctx context.Context, h *Holding) error
	ListHoldingsByStake(ctx context.Context, stakeID string) ([]*Holding, error)
	ListHoldingsByInvestor(ctx context.Context, investorAddr string) ([]*Holding, error)
	GetHoldingByInvestorAndStake(ctx context.Context, investorAddr, stakeID string) (*Holding, error)

	// Distributions
	CreateDistribution(ctx context.Context, d *Distribution) error
	ListDistributions(ctx context.Context, stakeID string, limit int) ([]*Distribution, error)

	// Orders
	CreateOrder(ctx context.Context, o *Order) error
	GetOrder(ctx context.Context, id string) (*Order, error)
	UpdateOrder(ctx context.Context, o *Order) error
	ListOrdersByStake(ctx context.Context, stakeID string, status string, limit int) ([]*Order, error)
	ListOrdersBySeller(ctx context.Context, sellerAddr string, limit int) ([]*Order, error)
}

// LedgerService abstracts ledger operations so stakes doesn't import ledger.
type LedgerService interface {
	EscrowLock(ctx context.Context, agentAddr, amount, reference string) error
	ReleaseEscrow(ctx context.Context, fromAddr, toAddr, amount, reference string) error
	RefundEscrow(ctx context.Context, agentAddr, amount, reference string) error
	Deposit(ctx context.Context, agentAddr, amount, reference string) error
	Spend(ctx context.Context, agentAddr, amount, reference string) error
	// Two-phase holds: Hold moves available → pending, ConfirmHold moves
	// pending → total_out, ReleaseHold moves pending → available.
	Hold(ctx context.Context, agentAddr, amount, reference string) error
	ConfirmHold(ctx context.Context, agentAddr, amount, reference string) error
	ReleaseHold(ctx context.Context, agentAddr, amount, reference string) error
}

// Request/response types for handlers.

// CreateStakeRequest is the request body for POST /v1/stakes.
type CreateStakeRequest struct {
	AgentAddr     string  `json:"agentAddr" binding:"required"`
	RevenueShare  float64 `json:"revenueShare" binding:"required"`
	TotalShares   int     `json:"totalShares" binding:"required"`
	PricePerShare string  `json:"pricePerShare" binding:"required"`
	VestingPeriod string  `json:"vestingPeriod"`
	Distribution  string  `json:"distribution"`
}

// InvestRequest is the request body for POST /v1/stakes/:id/invest.
type InvestRequest struct {
	InvestorAddr string `json:"investorAddr" binding:"required"`
	Shares       int    `json:"shares" binding:"required"`
}

// PlaceOrderRequest is the request body for POST /v1/stakes/orders.
type PlaceOrderRequest struct {
	SellerAddr    string `json:"sellerAddr" binding:"required"`
	HoldingID     string `json:"holdingId" binding:"required"`
	Shares        int    `json:"shares" binding:"required"`
	PricePerShare string `json:"pricePerShare" binding:"required"`
}

// FillOrderRequest is the request body for POST /v1/stakes/orders/:id/fill.
type FillOrderRequest struct {
	BuyerAddr string `json:"buyerAddr" binding:"required"`
}

// PortfolioResponse is the response for GET /v1/agents/:address/portfolio.
type PortfolioResponse struct {
	TotalInvested string           `json:"totalInvested"`
	TotalEarned   string           `json:"totalEarned"`
	Holdings      []PortfolioEntry `json:"holdings"`
}

// PortfolioEntry is one holding in a portfolio response.
type PortfolioEntry struct {
	AgentAddr string   `json:"agentAddr"`
	Holding   *Holding `json:"holding"`
	SharePct  float64  `json:"sharePct"`
}

// --- ID generators ---

func generateStakeID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return fmt.Sprintf("stk_%x", b)
}

func generateHoldingID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return fmt.Sprintf("hld_%x", b)
}

func generateDistributionID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return fmt.Sprintf("dist_%x", b)
}

func generateOrderID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return fmt.Sprintf("ord_%x", b)
}

// freqToDuration maps distribution frequency strings to durations.
func freqToDuration(freq string) time.Duration {
	switch freq {
	case "daily":
		return 24 * time.Hour
	case "weekly":
		return 7 * 24 * time.Hour
	case "monthly":
		return 30 * 24 * time.Hour
	default:
		return 7 * 24 * time.Hour // default to weekly
	}
}

// parseVestingPeriod parses a vesting period string like "90d" into a duration.
func parseVestingPeriod(period string) (time.Duration, error) {
	if len(period) < 2 {
		return 0, fmt.Errorf("invalid vesting period: %s", period)
	}
	unit := period[len(period)-1]
	numStr := period[:len(period)-1]
	var num int
	if _, err := fmt.Sscanf(numStr, "%d", &num); err != nil {
		return 0, fmt.Errorf("invalid vesting period: %s", period)
	}
	switch unit {
	case 'd':
		return time.Duration(num) * 24 * time.Hour, nil
	case 'w':
		return time.Duration(num) * 7 * 24 * time.Hour, nil
	default:
		return 0, fmt.Errorf("unsupported vesting unit: %c (use d or w)", unit)
	}
}
