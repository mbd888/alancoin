// Package gateway provides transparent payment proxy for AI agents.
//
// Flow:
//  1. Agent creates session → funds moved: available → pending (hold)
//  2. Agent sends proxy requests → gateway discovers, pays, and forwards
//  3. Per request: HTTP forward → on success: SettleHold(buyer→seller, price)
//  4. Session closed → ReleaseHold remaining unspent funds
package gateway

import (
	"context"
	"errors"
	"math/big"
	"time"

	"github.com/mbd888/alancoin/internal/usdc"
)

// Errors
var (
	ErrSessionNotFound    = errors.New("gateway: session not found")
	ErrSessionClosed      = errors.New("gateway: session closed")
	ErrSessionExpired     = errors.New("gateway: session expired")
	ErrBudgetExceeded     = errors.New("gateway: request would exceed session budget")
	ErrPerRequestExceeded = errors.New("gateway: service price exceeds per-request limit")
	ErrNoServiceAvailable = errors.New("gateway: no service available matching criteria")
	ErrProxyFailed        = errors.New("gateway: all service candidates failed")
	ErrInvalidAmount      = errors.New("gateway: invalid amount")
	ErrUnauthorized       = errors.New("gateway: not authorized for this session")
	ErrPolicyDenied       = errors.New("gateway: policy denied request")
	ErrRateLimited        = errors.New("gateway: rate limit exceeded")
	ErrTenantSuspended    = errors.New("gateway: tenant is suspended or cancelled")
)

// Status represents session state.
type Status string

const (
	StatusActive           Status = "active"
	StatusClosed           Status = "closed"
	StatusExpired          Status = "expired"
	StatusSettlementFailed Status = "settlement_failed" // Funds moved but status update failed; requires manual resolution
)

// Constants
const (
	MaxRetries         = 3
	DefaultHTTPTimeout = 30 * time.Second
)

// Session represents a gateway payment session.
type Session struct {
	ID                   string          `json:"id"`
	AgentAddr            string          `json:"agentAddr"`
	TenantID             string          `json:"tenantId,omitempty"` // Tenant that owns this session (empty = no tenant)
	MaxTotal             string          `json:"maxTotal"`           // Total budget held
	MaxPerRequest        string          `json:"maxPerRequest"`      // Max per single proxy call
	TotalSpent           string          `json:"totalSpent"`         // Accumulated spend
	RequestCount         int             `json:"requestCount"`
	Strategy             string          `json:"strategy"`                       // cheapest, reputation, best_value
	AllowedTypes         []string        `json:"allowedTypes,omitempty"`         // Empty = all types allowed
	allowedTypesSet      map[string]bool `json:"-"`                              // lazy O(1) lookup; not serialized
	WarnAtPercent        int             `json:"warnAtPercent,omitempty"`        // Alert when remaining drops below this % (e.g., 20)
	MaxRequestsPerMinute int             `json:"maxRequestsPerMinute,omitempty"` // 0 = default (100)
	Status               Status          `json:"status"`
	ExpiresAt            time.Time       `json:"expiresAt"`
	CreatedAt            time.Time       `json:"createdAt"`
	UpdatedAt            time.Time       `json:"updatedAt"`
}

// IsExpired returns true if the session has passed its expiration time.
func (s *Session) IsExpired() bool {
	return !s.ExpiresAt.IsZero() && time.Now().After(s.ExpiresAt)
}

// BuildAllowedTypesSet initializes the O(1) lookup set from the AllowedTypes slice.
// Must be called after creating or loading a session (not concurrently).
func (s *Session) BuildAllowedTypesSet() {
	if len(s.AllowedTypes) == 0 {
		s.allowedTypesSet = nil
		return
	}
	s.allowedTypesSet = make(map[string]bool, len(s.AllowedTypes))
	for _, t := range s.AllowedTypes {
		s.allowedTypesSet[t] = true
	}
}

// IsTypeAllowed returns true if the service type is permitted by this session.
// An empty AllowedTypes list means all types are allowed.
// The set must be pre-built via BuildAllowedTypesSet — this method is safe for
// concurrent reads (no lazy init, no writes).
func (s *Session) IsTypeAllowed(serviceType string) bool {
	if len(s.AllowedTypes) == 0 {
		return true
	}
	return s.allowedTypesSet[serviceType]
}

// Remaining returns the unspent portion of the session budget as a USDC string.
func (s *Session) Remaining() string {
	spentBig, _ := usdc.Parse(s.TotalSpent)
	totalBig, _ := usdc.Parse(s.MaxTotal)
	if spentBig == nil {
		spentBig = new(big.Int)
	}
	if totalBig == nil {
		totalBig = new(big.Int)
	}
	rem := new(big.Int).Sub(totalBig, spentBig)
	if rem.Sign() < 0 {
		rem.SetInt64(0)
	}
	return usdc.Format(rem)
}

// ProxyRequest is the payload for a proxy call.
type ProxyRequest struct {
	ServiceType    string                 `json:"serviceType" binding:"required"`
	Params         map[string]interface{} `json:"params"`
	MaxPrice       string                 `json:"maxPrice,omitempty"`       // Override per-request max
	PreferAgent    string                 `json:"preferAgent,omitempty"`    // Preferred seller address
	IdempotencyKey string                 `json:"idempotencyKey,omitempty"` // Client-provided dedup key
}

// ProxyResult is the response from a successful proxy call.
type ProxyResult struct {
	Response    map[string]interface{} `json:"response"`
	ServiceUsed string                 `json:"serviceUsed"` // Agent address that served
	ServiceName string                 `json:"serviceName"`
	AmountPaid  string                 `json:"amountPaid"`
	TotalSpent  string                 `json:"totalSpent"`
	Remaining   string                 `json:"remaining"`
	BudgetLow   bool                   `json:"budgetLow,omitempty"` // True when remaining < WarnAtPercent
	LatencyMs   int64                  `json:"latencyMs"`
	Retries     int                    `json:"retries"`
}

// RequestLog records a single proxy attempt.
type RequestLog struct {
	ID           string          `json:"id"`
	SessionID    string          `json:"sessionId"`
	TenantID     string          `json:"tenantId,omitempty"`
	ServiceType  string          `json:"serviceType"`
	AgentCalled  string          `json:"agentCalled"`
	Amount       string          `json:"amount"`
	FeeAmount    string          `json:"feeAmount,omitempty"` // platform fee deducted (basis-point take rate)
	Status       string          `json:"status"`              // "success", "forward_failed", "no_service", "policy_denied"
	LatencyMs    int64           `json:"latencyMs"`
	Error        string          `json:"error,omitempty"`
	PolicyResult *PolicyDecision `json:"policyResult,omitempty"`
	CreatedAt    time.Time       `json:"createdAt"`
}

// CreateSessionRequest is the HTTP payload for session creation.
type CreateSessionRequest struct {
	MaxTotal             string   `json:"maxTotal" binding:"required"`
	MaxPerRequest        string   `json:"maxPerRequest" binding:"required"`
	Strategy             string   `json:"strategy"` // default: "cheapest"
	AllowedTypes         []string `json:"allowedTypes,omitempty"`
	ExpiresInSec         int      `json:"expiresInSecs,omitempty"`        // 0 = 1 hour default
	WarnAtPercent        int      `json:"warnAtPercent,omitempty"`        // Alert when remaining drops below this %
	MaxRequestsPerMinute int      `json:"maxRequestsPerMinute,omitempty"` // 0 = default (100), max 1000
}

// LedgerService abstracts ledger operations.
type LedgerService interface {
	Hold(ctx context.Context, agentAddr, amount, reference string) error
	SettleHold(ctx context.Context, buyerAddr, sellerAddr, amount, reference string) error
	SettleHoldWithFee(ctx context.Context, buyerAddr, sellerAddr, sellerAmount, platformAddr, feeAmount, reference string) error
	ReleaseHold(ctx context.Context, agentAddr, amount, reference string) error
}

// TenantSettingsProvider looks up tenant settings for fee computation and status checks.
type TenantSettingsProvider interface {
	GetTakeRateBPS(ctx context.Context, tenantID string) (int, error)
	GetTenantStatus(ctx context.Context, tenantID string) (string, error)
}

// RegistryProvider abstracts service discovery.
type RegistryProvider interface {
	ListServices(ctx context.Context, serviceType, maxPrice string) ([]ServiceCandidate, error)
}

// TransactionRecorder records transactions for reputation tracking.
type TransactionRecorder interface {
	RecordTransaction(ctx context.Context, txHash, from, to, amount, serviceID, status string) error
}

// ReceiptIssuer issues cryptographic receipts for payments.
type ReceiptIssuer interface {
	IssueReceipt(ctx context.Context, path, reference, from, to, amount, serviceID, status, metadata string) error
}

// MoneyError wraps an error with funds-state context so callers know
// whether their money is safe and what to do next.
type MoneyError struct {
	Err         error
	FundsStatus string // no_change, held_pending, spent_not_delivered, held_safe, settled_safe
	Recovery    string // human-readable next step
	Amount      string // amount involved
	Reference   string // session/escrow/tx ID for support
}

func (e *MoneyError) Error() string { return e.Err.Error() }
func (e *MoneyError) Unwrap() error { return e.Err }

// SingleCallRequest is the payload for a one-shot gateway call.
// Creates an ephemeral session, proxies, and closes in one round trip.
type SingleCallRequest struct {
	MaxPrice    string                 `json:"maxPrice" binding:"required"`
	ServiceType string                 `json:"serviceType" binding:"required"`
	Params      map[string]interface{} `json:"params"`
}

// SingleCallResult wraps the proxy result with session lifecycle info.
type SingleCallResult struct {
	Response    map[string]interface{} `json:"response"`
	ServiceUsed string                 `json:"serviceUsed"`
	ServiceName string                 `json:"serviceName"`
	AmountPaid  string                 `json:"amountPaid"`
	LatencyMs   int64                  `json:"latencyMs"`
}

// BillingSummaryRow holds raw aggregation values from the request logs.
type BillingSummaryRow struct {
	TotalRequests   int64
	SettledRequests int64
	SettledVolume   string // USDC string
	FeesCollected   string // USDC string
}

// ServiceCandidate is a discovered service suitable for proxying.
type ServiceCandidate struct {
	AgentAddress    string  `json:"agentAddress"`
	AgentName       string  `json:"agentName"`
	ServiceID       string  `json:"serviceId"`
	ServiceName     string  `json:"serviceName"`
	Price           string  `json:"price"`
	Endpoint        string  `json:"endpoint"`
	ReputationScore float64 `json:"reputationScore"`
}
