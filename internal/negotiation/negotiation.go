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
	"strings"
	"time"
)

var (
	ErrRFPNotFound         = errors.New("rfp not found")
	ErrBidNotFound         = errors.New("bid not found")
	ErrInvalidStatus       = errors.New("invalid status for this operation")
	ErrUnauthorized        = errors.New("not authorized for this operation")
	ErrBidDeadlinePast     = errors.New("bid deadline has passed")
	ErrSelfBid             = errors.New("buyer cannot bid on own RFP")
	ErrDuplicateBid        = errors.New("seller already has a pending bid on this RFP")
	ErrBudgetOutOfRange    = errors.New("bid budget outside RFP range")
	ErrMaxCounterRounds    = errors.New("maximum counter-offer rounds exceeded")
	ErrLowReputation       = errors.New("seller does not meet minimum reputation")
	ErrNoBids              = errors.New("no pending bids to select from")
	ErrAlreadyAwarded      = errors.New("rfp already awarded")
	ErrBondRequired        = errors.New("bid bond required but ledger not configured")
	ErrInsufficientBond    = errors.New("insufficient balance for bid bond")
	ErrWithdrawalBlocked   = errors.New("withdrawals blocked during no-withdrawal window")
	ErrBidAlreadyWithdrawn = errors.New("bid already withdrawn")
	ErrTemplateNotFound    = errors.New("template not found")
	ErrTooManyWinners      = errors.New("more winners selected than maxWinners allows")
	ErrSealedNoCounter     = errors.New("counter-offers not allowed on sealed-bid RFPs")
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

// BondStatus represents the state of a bid bond.
type BondStatus string

const (
	BondStatusNone      BondStatus = "none"
	BondStatusHeld      BondStatus = "held"
	BondStatusReleased  BondStatus = "released"
	BondStatusForfeited BondStatus = "forfeited"
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
	RequiredBondPct  float64        `json:"requiredBondPct"`  // 0-100, percentage of bid total required as bond
	NoWithdrawWindow string         `json:"noWithdrawWindow"` // duration before deadline where withdrawals are blocked (e.g., "6h")
	MaxWinners       int            `json:"maxWinners"`
	SealedBids       bool           `json:"sealedBids"`
	ScoringWeights   ScoringWeights `json:"scoringWeights"`
	Status           RFPStatus      `json:"status"`
	WinningBidID     string         `json:"winningBidId,omitempty"`
	WinningBidIDs    []string       `json:"winningBidIds,omitempty"`
	ContractID       string         `json:"contractId,omitempty"`
	ContractIDs      []string       `json:"contractIds,omitempty"`
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
	ID            string     `json:"id"`
	RFPID         string     `json:"rfpId"`
	SellerAddr    string     `json:"sellerAddr"`
	PricePerCall  string     `json:"pricePerCall"`
	TotalBudget   string     `json:"totalBudget"`
	MaxLatencyMs  int        `json:"maxLatencyMs"`
	SuccessRate   float64    `json:"successRate"`
	Duration      string     `json:"duration"`
	SellerPenalty string     `json:"sellerPenalty"`
	Status        BidStatus  `json:"status"`
	Score         float64    `json:"score"`
	CounterRound  int        `json:"counterRound"`
	ParentBidID   string     `json:"parentBidId,omitempty"`
	CounteredByID string     `json:"counteredById,omitempty"`
	BondAmount    string     `json:"bondAmount"` // Amount held as bond (may be "0")
	BondStatus    BondStatus `json:"bondStatus"` // none, held, released, forfeited
	Message       string     `json:"message,omitempty"`
	CreatedAt     time.Time  `json:"createdAt"`
	UpdatedAt     time.Time  `json:"updatedAt"`
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
	MaxWinners       int             `json:"maxWinners"`
	SealedBids       bool            `json:"sealedBids"`
	RequiredBondPct  float64         `json:"requiredBondPct"`  // 0-100, percentage of bid total required as bond
	NoWithdrawWindow string          `json:"noWithdrawWindow"` // duration before deadline where withdrawals are blocked
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

// SelectWinnersRequest contains the parameters for selecting multiple winning bids.
type SelectWinnersRequest struct {
	BidIDs []string `json:"bidIds" binding:"required"`
}

// CancelRequest contains the parameters for cancelling an RFP.
type CancelRequest struct {
	Reason string `json:"reason"`
}

// RFPTemplate is a reusable RFP configuration saved by buyers.
type RFPTemplate struct {
	ID               string          `json:"id"`
	OwnerAddr        string          `json:"ownerAddr"` // empty = system template
	Name             string          `json:"name"`
	ServiceType      string          `json:"serviceType"`
	Description      string          `json:"description,omitempty"`
	MinBudget        string          `json:"minBudget"`
	MaxBudget        string          `json:"maxBudget"`
	MaxLatencyMs     int             `json:"maxLatencyMs"`
	MinSuccessRate   float64         `json:"minSuccessRate"`
	Duration         string          `json:"duration"`
	MinVolume        int             `json:"minVolume"`
	BidDeadline      string          `json:"bidDeadline"`
	AutoSelect       bool            `json:"autoSelect"`
	MinReputation    float64         `json:"minReputation"`
	MaxWinners       int             `json:"maxWinners"`
	SealedBids       bool            `json:"sealedBids"`
	MaxCounterRounds int             `json:"maxCounterRounds"`
	RequiredBondPct  float64         `json:"requiredBondPct"`
	NoWithdrawWindow string          `json:"noWithdrawWindow,omitempty"`
	ScoringWeights   *ScoringWeights `json:"scoringWeights,omitempty"`
	CreatedAt        time.Time       `json:"createdAt"`
}

// CreateTemplateRequest contains parameters for saving an RFP template.
type CreateTemplateRequest struct {
	Name             string          `json:"name" binding:"required"`
	ServiceType      string          `json:"serviceType" binding:"required"`
	Description      string          `json:"description"`
	MinBudget        string          `json:"minBudget" binding:"required"`
	MaxBudget        string          `json:"maxBudget" binding:"required"`
	MaxLatencyMs     int             `json:"maxLatencyMs"`
	MinSuccessRate   float64         `json:"minSuccessRate"`
	Duration         string          `json:"duration" binding:"required"`
	MinVolume        int             `json:"minVolume"`
	BidDeadline      string          `json:"bidDeadline" binding:"required"`
	AutoSelect       bool            `json:"autoSelect"`
	MinReputation    float64         `json:"minReputation"`
	MaxWinners       int             `json:"maxWinners"`
	SealedBids       bool            `json:"sealedBids"`
	MaxCounterRounds int             `json:"maxCounterRounds"`
	RequiredBondPct  float64         `json:"requiredBondPct"`
	NoWithdrawWindow string          `json:"noWithdrawWindow"`
	ScoringWeights   *ScoringWeights `json:"scoringWeights"`
}

// PublishFromTemplateRequest contains overrides when creating an RFP from a template.
type PublishFromTemplateRequest struct {
	BuyerAddr     string  `json:"buyerAddr" binding:"required"`
	MinBudget     string  `json:"minBudget"`     // override
	MaxBudget     string  `json:"maxBudget"`     // override
	Duration      string  `json:"duration"`      // override
	BidDeadline   string  `json:"bidDeadline"`   // override
	Description   string  `json:"description"`   // override
	MinReputation float64 `json:"minReputation"` // override (0 = use template)
	MaxWinners    int     `json:"maxWinners"`    // override (0 = use template)
}

// Analytics contains marketplace health metrics.
type Analytics struct {
	TotalRFPs          int                `json:"totalRfps"`
	OpenRFPs           int                `json:"openRfps"`
	AwardedRFPs        int                `json:"awardedRfps"`
	ExpiredRFPs        int                `json:"expiredRfps"`
	CancelledRFPs      int                `json:"cancelledRfps"`
	AvgBidsPerRFP      float64            `json:"avgBidsPerRfp"`
	AvgBidToAskSpread  float64            `json:"avgBidToAskSpread"`  // avg (bidBudget - rfpMinBudget) / rfpMaxBudget
	AvgTimeToAwardSecs float64            `json:"avgTimeToAwardSecs"` // avg seconds from publish to award
	AbandonmentRate    float64            `json:"abandonmentRate"`    // expired with 0 bids / (expired + awarded)
	CounterEfficiency  float64            `json:"counterEfficiency"`  // % of countered bids that led to awards
	TopSellers         []SellerWinSummary `json:"topSellers"`
}

// SellerWinSummary tracks a seller's win rate.
type SellerWinSummary struct {
	SellerAddr string  `json:"sellerAddr"`
	TotalBids  int     `json:"totalBids"`
	Wins       int     `json:"wins"`
	WinRate    float64 `json:"winRate"`
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
	ListStaleSelecting(ctx context.Context, before time.Time, limit int) ([]*RFP, error)

	// Bid operations
	CreateBid(ctx context.Context, bid *Bid) error
	GetBid(ctx context.Context, id string) (*Bid, error)
	UpdateBid(ctx context.Context, bid *Bid) error
	ListBidsByRFP(ctx context.Context, rfpID string, limit int) ([]*Bid, error)
	ListActiveBidsByRFP(ctx context.Context, rfpID string) ([]*Bid, error)
	GetBidBySellerAndRFP(ctx context.Context, sellerAddr, rfpID string) (*Bid, error)
	ListBidsBySeller(ctx context.Context, sellerAddr string, limit int) ([]*Bid, error)

	// Analytics
	GetAnalytics(ctx context.Context) (*Analytics, error)

	// Templates
	CreateTemplate(ctx context.Context, tmpl *RFPTemplate) error
	GetTemplate(ctx context.Context, id string) (*RFPTemplate, error)
	ListTemplates(ctx context.Context, ownerAddr string, limit int) ([]*RFPTemplate, error)
	DeleteTemplate(ctx context.Context, id string) error
}

// ReputationProvider fetches reputation scores for bid scoring.
type ReputationProvider interface {
	GetScore(ctx context.Context, address string) (float64, string, error)
}

// ContractFormer creates binding contracts from winning bids.
type ContractFormer interface {
	FormContract(ctx context.Context, rfp *RFP, bid *Bid) (string, error)
}

// LedgerService holds and releases bid bonds.
type LedgerService interface {
	Hold(ctx context.Context, agentAddr, amount, reference string) error
	ConfirmHold(ctx context.Context, agentAddr, amount, reference string) error
	ReleaseHold(ctx context.Context, agentAddr, amount, reference string) error
	Deposit(ctx context.Context, agentAddr, amount, reference string) error
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

func generateTemplateID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return fmt.Sprintf("tmpl_%x", b)
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

// encodeIDs joins a string slice into a comma-separated string for DB storage.
func encodeIDs(ids []string) string {
	return strings.Join(ids, ",")
}

// decodeIDs splits a comma-separated string into a string slice.
func decodeIDs(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}

// SealBid returns a copy of a bid with sensitive fields redacted for sealed-bid RFPs.
func SealBid(b *Bid) *Bid {
	sealed := *b
	sealed.PricePerCall = ""
	sealed.TotalBudget = ""
	sealed.MaxLatencyMs = 0
	sealed.SuccessRate = 0
	sealed.Score = 0
	sealed.SellerPenalty = ""
	sealed.Message = ""
	return &sealed
}
