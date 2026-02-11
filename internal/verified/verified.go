// Package verified implements performance-guaranteed agent verification.
//
// Agents with proven reputation can apply for Verified status, which lets them
// offer platform-backed performance guarantees to buyers. Verified agents post
// a performance bond (via ledger Hold) and commit to a minimum success rate.
// If their rolling success rate drops below the guarantee, the bond is partially
// forfeited to compensate affected buyers.
//
// Flow:
//  1. Agent applies for verification → scorer evaluates eligibility
//  2. Agent posts performance bond → held via ledger
//  3. Agent receives Verified badge → boosted in discovery, commands premium price
//  4. Enforcer monitors rolling success rate via contract calls
//  5. Violation → bond forfeited proportionally, status suspended
//  6. Agent can revoke voluntarily → bond released
package verified

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotEligible       = errors.New("agent not eligible for verification")
	ErrAlreadyVerified   = errors.New("agent already has active verification")
	ErrNotVerified       = errors.New("agent is not verified")
	ErrVerificationFound = errors.New("verification not found")
	ErrInvalidStatus     = errors.New("invalid verification status for this operation")
	ErrBondTooLow        = errors.New("bond amount below minimum requirement")
)

// Status represents the state of a verification.
type Status string

const (
	StatusActive    Status = "active"
	StatusSuspended Status = "suspended" // Temporarily suspended due to SLA breach
	StatusRevoked   Status = "revoked"   // Voluntarily revoked by agent
	StatusForfeited Status = "forfeited" // Bond forfeited due to violation
)

// Verification represents a verified agent's status and bond.
type Verification struct {
	ID                    string  `json:"id"`
	AgentAddr             string  `json:"agentAddr"`
	Status                Status  `json:"status"`
	BondAmount            string  `json:"bondAmount"`            // USDC held as performance bond
	BondReference         string  `json:"bondReference"`         // Ledger hold reference
	GuaranteedSuccessRate float64 `json:"guaranteedSuccessRate"` // e.g. 95.0 means 95%
	SLAWindowSize         int     `json:"slaWindowSize"`         // Rolling window for enforcement
	GuaranteePremiumRate  float64 `json:"guaranteePremiumRate"`  // e.g. 0.05 means 5% surcharge

	// Snapshot at time of verification
	ReputationScore float64 `json:"reputationScore"`
	ReputationTier  string  `json:"reputationTier"`

	// Tracking
	TotalCallsMonitored int       `json:"totalCallsMonitored"`
	ViolationCount      int       `json:"violationCount"`
	LastViolationAt     time.Time `json:"lastViolationAt,omitempty"`
	LastReviewAt        time.Time `json:"lastReviewAt"`

	// Lifecycle
	VerifiedAt time.Time  `json:"verifiedAt"`
	RevokedAt  *time.Time `json:"revokedAt,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
	UpdatedAt  time.Time  `json:"updatedAt"`
}

// IsActive returns true if the verification is currently active.
func (v *Verification) IsActive() bool {
	return v.Status == StatusActive
}

// IsTerminal returns true if the verification is in a final state.
func (v *Verification) IsTerminal() bool {
	return v.Status == StatusRevoked || v.Status == StatusForfeited
}

// ApplyRequest is the request body for applying for verified status.
type ApplyRequest struct {
	AgentAddr  string `json:"agentAddr" binding:"required"`
	BondAmount string `json:"bondAmount" binding:"required"` // USDC amount to post as bond
}

// Store persists verification data.
type Store interface {
	Create(ctx context.Context, v *Verification) error
	Get(ctx context.Context, id string) (*Verification, error)
	GetByAgent(ctx context.Context, agentAddr string) (*Verification, error)
	Update(ctx context.Context, v *Verification) error
	ListActive(ctx context.Context, limit int) ([]*Verification, error)
	ListAll(ctx context.Context, limit int) ([]*Verification, error)
	IsVerified(ctx context.Context, agentAddr string) (bool, error)
}

// ReputationProvider fetches reputation scores for agents.
type ReputationProvider interface {
	GetScore(ctx context.Context, address string) (score float64, tier string, err error)
}

// MetricsProvider fetches transaction metrics for agents.
type MetricsProvider interface {
	GetAgentMetrics(ctx context.Context, address string) (totalTxns int, successRate float64, daysOnNetwork int, totalVolumeUSD float64, err error)
}

// LedgerService manages hold operations for performance bonds.
type LedgerService interface {
	Hold(ctx context.Context, agentAddr, amount, reference string) error
	ConfirmHold(ctx context.Context, agentAddr, amount, reference string) error
	ReleaseHold(ctx context.Context, agentAddr, amount, reference string) error
	Deposit(ctx context.Context, agentAddr, amount, txHash string) error
}
