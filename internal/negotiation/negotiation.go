// Package negotiation provides autonomous deal-making between agents.
//
// Flow:
//  1. Buyer publishes an RFP with budget range, SLA requirements, deadline
//  2. Sellers discover RFPs and place bids with prices + SLA guarantees
//  3. Optional counter-offers (max N rounds)
//  4. At deadline: auto-select highest-scored bid OR buyer picks manually
//  5. Winner auto-forms a binding contract via contracts.Service
//  6. Losers get notified, all events pushed via webhooks + WebSocket
package negotiation

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math"
	"time"
)

var (
	ErrRFPNotFound      = errors.New("rfp not found")
	ErrBidNotFound      = errors.New("bid not found")
	ErrInvalidStatus    = errors.New("invalid status for this operation")
	ErrUnauthorized     = errors.New("not authorized for this operation")
	ErrBidDeadlinePast  = errors.New("bid deadline has passed")
	ErrSelfBid          = errors.New("buyer cannot bid on own RFP")
	ErrDuplicateBid     = errors.New("seller already has a pending bid on this RFP")
	ErrBudgetOutOfRange = errors.New("bid budget outside RFP range")
	ErrMaxCounterRounds = errors.New("maximum counter-offer rounds exceeded")
	ErrLowReputation    = errors.New("seller does not meet minimum reputation")
	ErrNoBids           = errors.New("no pending bids to select from")
	ErrAlreadyAwarded   = errors.New("rfp already awarded")
)

// RFPStatus represents the state of an RFP.
type RFPStatus string

const (
	RFPStatusOpen      RFPStatus = "open"
	RFPStatusSelecting RFPStatus = "selecting"
	RFPStatusAwarded   RFPStatus = "awarded"
	RFPStatusExpired   RFPStatus = "expired"
	RFPStatusCancelled RFPStatus = "cancelled"
)

// BidStatus represents the state of a bid.
type BidStatus string

const (
	BidStatusPending   BidStatus = "pending"
	BidStatusAccepted  BidStatus = "accepted"
	BidStatusRejected  BidStatus = "rejected"
	BidStatusWithdrawn BidStatus = "withdrawn"
	BidStatusCountered BidStatus = "countered"
)

// ScoringWeights controls how bids are scored.
type ScoringWeights struct {
	Price      float64 `json:"price"`
	Reputation float64 `json:"reputation"`
	SLA        float64 `json:"sla"`
}

// DefaultScoringWeights returns the default scoring weights.
func DefaultScoringWeights() ScoringWeights {
	return ScoringWeights{Price: 0.30, Reputation: 0.40, SLA: 0.30}
}

// RFP represents a Request for Proposal published by a buyer.
type RFP struct {
	ID               string         `json:"id"`
	BuyerAddr        string         `json:"buyerAddr"`
	ServiceType      string         `json:"serviceType"`
	Description      string         `json:"description,omitempty"`
	MinBudget        string         `json:"minBudget"`
	MaxBudget        string         `json:"maxBudget"`
	MaxLatencyMs     int            `json:"maxLatencyMs"`
	MinSuccessRate   float64        `json:"minSuccessRate"`
	Duration         string         `json:"duration"`
	MinVolume        int            `json:"minVolume"`
	BidDeadline      time.Time      `json:"bidDeadline"`
	AutoSelect       bool           `json:"autoSelect"`
	MinReputation    float64        `json:"minReputation"`
	MaxCounterRounds int            `json:"maxCounterRounds"`
	ScoringWeights   ScoringWeights `json:"scoringWeights"`
	Status           RFPStatus      `json:"status"`
	WinningBidID     string         `json:"winningBidId,omitempty"`
	ContractID       string         `json:"contractId,omitempty"`
	BidCount         int            `json:"bidCount"`
	CancelReason     string         `json:"cancelReason,omitempty"`
	AwardedAt        *time.Time     `json:"awardedAt,omitempty"`
	CreatedAt        time.Time      `json:"createdAt"`
	UpdatedAt        time.Time      `json:"updatedAt"`
}

// IsTerminal returns true if the RFP is in a final state.
func (r *RFP) IsTerminal() bool {
	switch r.Status {
	case RFPStatusAwarded, RFPStatusExpired, RFPStatusCancelled:
		return true
	}
	return false
}

// Bid represents a seller's offer on an RFP.
type Bid struct {
	ID            string    `json:"id"`
	RFPID         string    `json:"rfpId"`
	SellerAddr    string    `json:"sellerAddr"`
	PricePerCall  string    `json:"pricePerCall"`
	TotalBudget   string    `json:"totalBudget"`
	MaxLatencyMs  int       `json:"maxLatencyMs"`
	SuccessRate   float64   `json:"successRate"`
	Duration      string    `json:"duration"`
	SellerPenalty string    `json:"sellerPenalty"`
	Status        BidStatus `json:"status"`
	Score         float64   `json:"score"`
	CounterRound  int       `json:"counterRound"`
	ParentBidID   string    `json:"parentBidId,omitempty"`
	CounteredByID string    `json:"counteredById,omitempty"`
	Message       string    `json:"message,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

// PublishRFPRequest contains the parameters for publishing an RFP.
type PublishRFPRequest struct {
	BuyerAddr        string          `json:"buyerAddr" binding:"required"`
	ServiceType      string          `json:"serviceType" binding:"required"`
	Description      string          `json:"description"`
	MinBudget        string          `json:"minBudget" binding:"required"`
	MaxBudget        string          `json:"maxBudget" binding:"required"`
	MaxLatencyMs     int             `json:"maxLatencyMs"`
	MinSuccessRate   float64         `json:"minSuccessRate"`
	Duration         string          `json:"duration" binding:"required"`
	MinVolume        int             `json:"minVolume"`
	BidDeadline      string          `json:"bidDeadline" binding:"required"` // duration (e.g., "24h") or RFC3339
	AutoSelect       bool            `json:"autoSelect"`
	MinReputation    float64         `json:"minReputation"`
	MaxCounterRounds int             `json:"maxCounterRounds"`
	ScoringWeights   *ScoringWeights `json:"scoringWeights"`
}

// PlaceBidRequest contains the parameters for placing a bid.
type PlaceBidRequest struct {
	SellerAddr    string  `json:"sellerAddr" binding:"required"`
	PricePerCall  string  `json:"pricePerCall" binding:"required"`
	TotalBudget   string  `json:"totalBudget" binding:"required"`
	MaxLatencyMs  int     `json:"maxLatencyMs"`
	SuccessRate   float64 `json:"successRate"`
	Duration      string  `json:"duration" binding:"required"`
	SellerPenalty string  `json:"sellerPenalty"`
	Message       string  `json:"message"`
}

// CounterRequest contains the parameters for a counter-offer.
type CounterRequest struct {
	PricePerCall  string  `json:"pricePerCall"`
	TotalBudget   string  `json:"totalBudget"`
	MaxLatencyMs  int     `json:"maxLatencyMs"`
	SuccessRate   float64 `json:"successRate"`
	Duration      string  `json:"duration"`
	SellerPenalty string  `json:"sellerPenalty"`
	Message       string  `json:"message"`
}

// SelectRequest contains the parameters for selecting a winning bid.
type SelectRequest struct {
	BidID string `json:"bidId" binding:"required"`
}

// CancelRequest contains the parameters for cancelling an RFP.
type CancelRequest struct {
	Reason string `json:"reason"`
}

// Store persists negotiation data.
type Store interface {
	// RFP operations
	CreateRFP(ctx context.Context, rfp *RFP) error
	GetRFP(ctx context.Context, id string) (*RFP, error)
	UpdateRFP(ctx context.Context, rfp *RFP) error
	ListOpenRFPs(ctx context.Context, serviceType string, limit int) ([]*RFP, error)
	ListByBuyer(ctx context.Context, buyerAddr string, limit int) ([]*RFP, error)
	ListExpiredRFPs(ctx context.Context, before time.Time, limit int) ([]*RFP, error)
	ListAutoSelectReady(ctx context.Context, before time.Time, limit int) ([]*RFP, error)

	// Bid operations
	CreateBid(ctx context.Context, bid *Bid) error
	GetBid(ctx context.Context, id string) (*Bid, error)
	UpdateBid(ctx context.Context, bid *Bid) error
	ListBidsByRFP(ctx context.Context, rfpID string, limit int) ([]*Bid, error)
	ListActiveBidsByRFP(ctx context.Context, rfpID string) ([]*Bid, error)
	GetBidBySellerAndRFP(ctx context.Context, sellerAddr, rfpID string) (*Bid, error)
	ListBidsBySeller(ctx context.Context, sellerAddr string, limit int) ([]*Bid, error)
}

// ReputationProvider fetches reputation scores for bid scoring.
type ReputationProvider interface {
	GetScore(ctx context.Context, address string) (float64, string, error)
}

// ContractFormer creates binding contracts from winning bids.
type ContractFormer interface {
	FormContract(ctx context.Context, rfp *RFP, bid *Bid) (string, error)
}

// ScoreBid computes a bid's score based on the RFP's scoring weights.
//
//	price_score    = 1 - (bid_budget / max_budget) — lower price = higher
//	reputation     = agent_reputation / 100
//	sla_score      = bid_success_rate / 100
func ScoreBid(bid *Bid, rfp *RFP, reputationScore float64) float64 {
	w := rfp.ScoringWeights

	// Price score: lower total budget relative to max = higher score
	bidBudget := parseFloat(bid.TotalBudget)
	maxBudget := parseFloat(rfp.MaxBudget)
	priceScore := 0.0
	if maxBudget > 0 {
		priceScore = 1.0 - (bidBudget / maxBudget)
		priceScore = math.Max(0, math.Min(1, priceScore))
	}

	// Reputation score: normalized to 0-1
	repScore := math.Max(0, math.Min(1, reputationScore/100.0))

	// SLA score: bid's guaranteed success rate normalized to 0-1
	slaScore := math.Max(0, math.Min(1, bid.SuccessRate/100.0))

	return w.Price*priceScore + w.Reputation*repScore + w.SLA*slaScore
}

func generateRFPID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return fmt.Sprintf("rfp_%x", b)
}

func generateBidID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return fmt.Sprintf("bid_%x", b)
}

func parseFloat(s string) float64 {
	var f float64
	_, _ = fmt.Sscanf(s, "%f", &f)
	return f
}
