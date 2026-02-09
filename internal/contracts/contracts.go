// Package contracts provides formalized, time-bounded service agreements
// between agents with measurable SLAs and automatic penalty enforcement.
//
// Flow:
//  1. Buyer proposes a contract → status: proposed (no funds locked)
//  2. Seller accepts → buyer budget + seller penalty locked in escrow → status: active
//  3. Buyer records service calls → micro-release per successful call
//  4. SLA window monitored → violation triggers penalty transfer
//  5. Natural completion when budget exhausted and min volume met → status: completed
//  6. Either party can terminate early with defined consequences
package contracts

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

var (
	ErrContractNotFound = errors.New("contract not found")
	ErrInvalidStatus    = errors.New("invalid contract status for this operation")
	ErrUnauthorized     = errors.New("not authorized for this contract operation")
	ErrAlreadyResolved  = errors.New("contract already resolved")
	ErrSLAViolation     = errors.New("SLA violation detected")
	ErrBudgetExhausted  = errors.New("contract budget exhausted")
)

// Status represents the state of a contract.
type Status string

const (
	StatusProposed   Status = "proposed"
	StatusAccepted   Status = "accepted"
	StatusActive     Status = "active"
	StatusCompleted  Status = "completed"
	StatusTerminated Status = "terminated"
	StatusViolated   Status = "violated"
	StatusRejected   Status = "rejected"
)

// Contract represents a time-bounded service agreement between two agents.
type Contract struct {
	ID               string     `json:"id"`
	BuyerAddr        string     `json:"buyerAddr"`
	SellerAddr       string     `json:"sellerAddr"`
	ServiceType      string     `json:"serviceType"`
	PricePerCall     string     `json:"pricePerCall"`
	MinVolume        int        `json:"minVolume"`
	BuyerBudget      string     `json:"buyerBudget"`
	SellerPenalty    string     `json:"sellerPenalty"`
	MaxLatencyMs     int        `json:"maxLatencyMs"`
	MinSuccessRate   float64    `json:"minSuccessRate"`
	SLAWindowSize    int        `json:"slaWindowSize"`
	Status           Status     `json:"status"`
	Duration         string     `json:"duration"`
	StartsAt         *time.Time `json:"startsAt,omitempty"`
	ExpiresAt        *time.Time `json:"expiresAt,omitempty"`
	TotalCalls       int        `json:"totalCalls"`
	SuccessfulCalls  int        `json:"successfulCalls"`
	FailedCalls      int        `json:"failedCalls"`
	TotalLatencyMs   int64      `json:"totalLatencyMs"`
	BudgetSpent      string     `json:"budgetSpent"`
	TerminatedBy     string     `json:"terminatedBy,omitempty"`
	TerminatedReason string     `json:"terminatedReason,omitempty"`
	ViolationDetails string     `json:"violationDetails,omitempty"`
	ResolvedAt       *time.Time `json:"resolvedAt,omitempty"`
	CreatedAt        time.Time  `json:"createdAt"`
	UpdatedAt        time.Time  `json:"updatedAt"`
}

// IsTerminal returns true if the contract is in a final state.
func (c *Contract) IsTerminal() bool {
	switch c.Status {
	case StatusCompleted, StatusTerminated, StatusViolated, StatusRejected:
		return true
	}
	return false
}

// CurrentSuccessRate returns the success rate as a percentage (0-100).
func (c *Contract) CurrentSuccessRate() float64 {
	if c.TotalCalls == 0 {
		return 100.0
	}
	return float64(c.SuccessfulCalls) / float64(c.TotalCalls) * 100.0
}

// AverageLatencyMs returns the average latency across all calls.
func (c *Contract) AverageLatencyMs() float64 {
	if c.TotalCalls == 0 {
		return 0
	}
	return float64(c.TotalLatencyMs) / float64(c.TotalCalls)
}

// ContractCall represents an individual service call within a contract.
type ContractCall struct {
	ID         string    `json:"id"`
	ContractID string    `json:"contractId"`
	Status     string    `json:"status"` // "pending", "success", "failed"
	LatencyMs  int       `json:"latencyMs"`
	ErrorMsg   string    `json:"errorMessage,omitempty"`
	Amount     string    `json:"amount"`
	CreatedAt  time.Time `json:"createdAt"`
}

// Store persists contract data.
type Store interface {
	Create(ctx context.Context, contract *Contract) error
	Get(ctx context.Context, id string) (*Contract, error)
	Update(ctx context.Context, contract *Contract) error
	ListByAgent(ctx context.Context, agentAddr string, status string, limit int) ([]*Contract, error)
	ListExpiring(ctx context.Context, before time.Time, limit int) ([]*Contract, error)
	ListActive(ctx context.Context, limit int) ([]*Contract, error)
	RecordCall(ctx context.Context, call *ContractCall) error
	ListCalls(ctx context.Context, contractID string, limit int) ([]*ContractCall, error)
	GetRecentCalls(ctx context.Context, contractID string, windowSize int) ([]*ContractCall, error)
}

// LedgerService abstracts ledger operations so contracts doesn't import ledger.
type LedgerService interface {
	EscrowLock(ctx context.Context, agentAddr, amount, reference string) error
	ReleaseEscrow(ctx context.Context, buyerAddr, sellerAddr, amount, reference string) error
	RefundEscrow(ctx context.Context, agentAddr, amount, reference string) error
}

// ProposeRequest contains the parameters for proposing a contract.
type ProposeRequest struct {
	BuyerAddr      string  `json:"buyerAddr" binding:"required"`
	SellerAddr     string  `json:"sellerAddr" binding:"required"`
	ServiceType    string  `json:"serviceType" binding:"required"`
	PricePerCall   string  `json:"pricePerCall" binding:"required"`
	BuyerBudget    string  `json:"buyerBudget" binding:"required"`
	Duration       string  `json:"duration" binding:"required"`
	MinVolume      int     `json:"minVolume"`
	SellerPenalty  string  `json:"sellerPenalty"`
	MaxLatencyMs   int     `json:"maxLatencyMs"`
	MinSuccessRate float64 `json:"minSuccessRate"`
	SLAWindowSize  int     `json:"slaWindowSize"`
}

// RecordCallRequest contains the parameters for recording a contract call.
type RecordCallRequest struct {
	Status       string `json:"status" binding:"required"` // "success" or "failed"
	LatencyMs    int    `json:"latencyMs"`
	ErrorMessage string `json:"errorMessage"`
}

// TerminateRequest contains the parameters for terminating a contract.
type TerminateRequest struct {
	Reason string `json:"reason" binding:"required"`
}

// Service implements contract business logic.
type Service struct {
	store  Store
	ledger LedgerService
	locks  sync.Map // per-contract ID locks to prevent race conditions
}

// contractLock returns a mutex for the given contract ID.
func (s *Service) contractLock(id string) *sync.Mutex {
	v, _ := s.locks.LoadOrStore(id, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// NewService creates a new contract service.
func NewService(store Store, ledger LedgerService) *Service {
	return &Service{
		store:  store,
		ledger: ledger,
	}
}

func generateContractID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return fmt.Sprintf("ct_%x", b)
}

func generateCallID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return fmt.Sprintf("cc_%x", b)
}

// parseDuration parses duration strings like "7d", "24h", "30m".
func parseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if len(s) == 0 {
		return 0, errors.New("empty duration")
	}

	// Handle "d" suffix for days (not supported by time.ParseDuration)
	if strings.HasSuffix(s, "d") {
		numStr := s[:len(s)-1]
		var days int
		if _, err := fmt.Sscanf(numStr, "%d", &days); err != nil {
			return 0, fmt.Errorf("invalid duration: %s", s)
		}
		if days <= 0 {
			return 0, fmt.Errorf("duration must be positive: %s", s)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}

	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, err
	}
	if d <= 0 {
		return 0, fmt.Errorf("duration must be positive: %s", s)
	}
	return d, nil
}
