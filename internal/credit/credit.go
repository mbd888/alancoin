// Package credit implements agent credit lines for Alancoin.
//
// Agents with proven reputation can spend on credit, repaying from future earnings.
// Credit limits are based on reputation tier, transaction history, and volume.
package credit

import (
	"context"
	"errors"
	"time"
)

var (
	ErrCreditLineNotFound  = errors.New("credit line not found")
	ErrNotEligible         = errors.New("agent not eligible for credit")
	ErrCreditLineExists    = errors.New("agent already has an active credit line")
	ErrCreditLineRevoked   = errors.New("credit line has been revoked")
	ErrCreditLineDefaulted = errors.New("credit line has been defaulted")
)

// Status represents the state of a credit line.
type Status string

const (
	StatusActive    Status = "active"
	StatusSuspended Status = "suspended"
	StatusDefaulted Status = "defaulted"
	StatusRevoked   Status = "revoked"
	StatusClosed    Status = "closed"
)

// CreditLine represents an agent's credit facility.
type CreditLine struct {
	ID              string    `json:"id"`
	AgentAddr       string    `json:"agentAddr"`
	CreditLimit     string    `json:"creditLimit"`
	CreditUsed      string    `json:"creditUsed"`
	InterestRate    float64   `json:"interestRate"`
	Status          Status    `json:"status"`
	ReputationTier  string    `json:"reputationTier"`
	ReputationScore float64   `json:"reputationScore"`
	ApprovedAt      time.Time `json:"approvedAt"`
	LastReviewAt    time.Time `json:"lastReviewAt"`
	DefaultedAt     time.Time `json:"defaultedAt,omitempty"`
	RevokedAt       time.Time `json:"revokedAt,omitempty"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

// CreditApplication is the request body for applying for credit.
type CreditApplication struct {
	AgentAddr string `json:"agentAddr" binding:"required"`
}

// RepaymentRequest is the request body for manual repayment.
type RepaymentRequest struct {
	Amount string `json:"amount" binding:"required"`
}

// Store persists credit line data.
type Store interface {
	Create(ctx context.Context, line *CreditLine) error
	Get(ctx context.Context, id string) (*CreditLine, error)
	GetByAgent(ctx context.Context, agentAddr string) (*CreditLine, error)
	Update(ctx context.Context, line *CreditLine) error
	ListActive(ctx context.Context, limit int) ([]*CreditLine, error)
	ListOverdue(ctx context.Context, overdueDays int, limit int) ([]*CreditLine, error)
}

// ReputationProvider fetches reputation scores for agents.
type ReputationProvider interface {
	GetScore(ctx context.Context, address string) (score float64, tier string, err error)
}

// MetricsProvider fetches transaction metrics for agents.
type MetricsProvider interface {
	GetAgentMetrics(ctx context.Context, address string) (totalTxns int, successRate float64, daysOnNetwork int, totalVolumeUSD float64, err error)
}

// LedgerService manages credit-related balance operations.
type LedgerService interface {
	GetCreditInfo(ctx context.Context, agentAddr string) (creditLimit, creditUsed string, err error)
	SetCreditLimit(ctx context.Context, agentAddr, limit string) error
	RepayCredit(ctx context.Context, agentAddr, amount string) error
}
