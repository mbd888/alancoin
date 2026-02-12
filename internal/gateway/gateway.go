// Package gateway provides transparent payment proxy for AI agents.
//
// Flow:
//  1. Agent creates session → funds moved: available → pending (hold)
//  2. Agent sends proxy requests → gateway discovers, pays, and forwards
//  3. Per request: ConfirmHold(buyer, price) + Deposit(seller, price) + HTTP forward
//  4. Session closed → ReleaseHold remaining unspent funds
package gateway

import (
	"context"
	"errors"
	"time"
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
)

// Status represents session state.
type Status string

const (
	StatusActive  Status = "active"
	StatusClosed  Status = "closed"
	StatusExpired Status = "expired"
)

// Constants
const (
	MaxRetries         = 3
	DefaultHTTPTimeout = 30 * time.Second
)

// Session represents a gateway payment session.
type Session struct {
	ID            string    `json:"id"`
	AgentAddr     string    `json:"agentAddr"`
	MaxTotal      string    `json:"maxTotal"`      // Total budget held
	MaxPerRequest string    `json:"maxPerRequest"` // Max per single proxy call
	TotalSpent    string    `json:"totalSpent"`    // Accumulated spend
	RequestCount  int       `json:"requestCount"`
	Strategy      string    `json:"strategy"`               // cheapest, reputation, best_value
	AllowedTypes  []string  `json:"allowedTypes,omitempty"` // Empty = all types allowed
	Status        Status    `json:"status"`
	ExpiresAt     time.Time `json:"expiresAt"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

// IsExpired returns true if the session has passed its expiration time.
func (s *Session) IsExpired() bool {
	return !s.ExpiresAt.IsZero() && time.Now().After(s.ExpiresAt)
}

// ProxyRequest is the payload for a proxy call.
type ProxyRequest struct {
	ServiceType string                 `json:"serviceType" binding:"required"`
	Params      map[string]interface{} `json:"params"`
	MaxPrice    string                 `json:"maxPrice,omitempty"`    // Override per-request max
	PreferAgent string                 `json:"preferAgent,omitempty"` // Preferred seller address
}

// ProxyResult is the response from a successful proxy call.
type ProxyResult struct {
	Response    map[string]interface{} `json:"response"`
	ServiceUsed string                 `json:"serviceUsed"` // Agent address that served
	ServiceName string                 `json:"serviceName"`
	AmountPaid  string                 `json:"amountPaid"`
	LatencyMs   int64                  `json:"latencyMs"`
	Retries     int                    `json:"retries"`
}

// RequestLog records a single proxy attempt.
type RequestLog struct {
	ID          string    `json:"id"`
	SessionID   string    `json:"sessionId"`
	ServiceType string    `json:"serviceType"`
	AgentCalled string    `json:"agentCalled"`
	Amount      string    `json:"amount"`
	Status      string    `json:"status"` // "success", "forward_failed", "no_service"
	LatencyMs   int64     `json:"latencyMs"`
	Error       string    `json:"error,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
}

// CreateSessionRequest is the HTTP payload for session creation.
type CreateSessionRequest struct {
	MaxTotal      string   `json:"maxTotal" binding:"required"`
	MaxPerRequest string   `json:"maxPerRequest" binding:"required"`
	Strategy      string   `json:"strategy"` // default: "cheapest"
	AllowedTypes  []string `json:"allowedTypes,omitempty"`
	ExpiresInSec  int      `json:"expiresInSecs,omitempty"` // 0 = 1 hour default
}

// LedgerService abstracts ledger operations.
type LedgerService interface {
	Hold(ctx context.Context, agentAddr, amount, reference string) error
	SettleHold(ctx context.Context, buyerAddr, sellerAddr, amount, reference string) error
	ReleaseHold(ctx context.Context, agentAddr, amount, reference string) error
}

// RegistryProvider abstracts service discovery.
type RegistryProvider interface {
	ListServices(ctx context.Context, serviceType, maxPrice string) ([]ServiceCandidate, error)
}

// TransactionRecorder records transactions for reputation tracking.
type TransactionRecorder interface {
	RecordTransaction(ctx context.Context, txHash, from, to, amount, serviceID, status string) error
}

// RevenueAccumulator intercepts payments for revenue staking.
type RevenueAccumulator interface {
	AccumulateRevenue(ctx context.Context, agentAddr, amount, txRef string) error
}

// VerificationChecker checks if a seller is verified and returns guarantee terms.
type VerificationChecker interface {
	IsVerified(ctx context.Context, agentAddr string) (bool, error)
	GetGuarantee(ctx context.Context, agentAddr string) (guaranteedSuccessRate float64, premiumRate float64, err error)
}

// ContractManager creates and records calls for micro-contracts.
type ContractManager interface {
	// EnsureContract returns an existing active contract for buyer→seller or creates one.
	EnsureContract(ctx context.Context, buyerAddr, sellerAddr, serviceType, pricePerCall string, guaranteedSuccessRate float64, slaWindowSize int) (contractID string, err error)
	// RecordCall records a call result against a contract.
	RecordCall(ctx context.Context, contractID string, status string, latencyMs int) error
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
