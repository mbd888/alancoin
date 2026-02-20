// Package streams provides streaming micropayments for continuous services.
//
// Flow:
//  1. Buyer opens stream → funds moved: available → pending (hold)
//  2. Service delivers value continuously → ticks accumulate spent amount
//  3. Either party closes → settle: spent → seller, unused → buyer (release hold)
//  4. Stale timeout → auto-close if no tick within stale_timeout_secs
package streams

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"
)

var (
	ErrStreamNotFound   = errors.New("streams: not found")
	ErrInvalidStatus    = errors.New("streams: invalid status for this operation")
	ErrUnauthorized     = errors.New("streams: not authorized")
	ErrInvalidAmount    = errors.New("streams: invalid amount")
	ErrHoldExhausted    = errors.New("streams: hold exhausted — tick would exceed held amount")
	ErrAlreadyClosed    = errors.New("streams: already closed")
	ErrInvalidTickSeq   = errors.New("streams: tick sequence number invalid")
	ErrDuplicateTickSeq = errors.New("streams: duplicate tick sequence number")
)

// Status represents the state of a stream.
type Status string

const (
	StatusOpen             Status = "open"              // Active, accepting ticks
	StatusClosed           Status = "closed"            // Settled by buyer or seller
	StatusStaleClosed      Status = "stale_closed"      // Auto-closed due to inactivity
	StatusDisputed         Status = "disputed"          // Buyer disputed quality
	StatusSettlementFailed Status = "settlement_failed" // Funds moved but status update failed; requires manual resolution
)

// DefaultStaleTimeout is the default inactivity threshold before auto-closing.
const DefaultStaleTimeout = 60 * time.Second

// Stream represents an open or closed payment stream.
type Stream struct {
	ID              string     `json:"id"`
	BuyerAddr       string     `json:"buyerAddr"`
	SellerAddr      string     `json:"sellerAddr"`
	ServiceID       string     `json:"serviceId,omitempty"`
	SessionKeyID    string     `json:"sessionKeyId,omitempty"`
	HoldAmount      string     `json:"holdAmount"`   // Total held from buyer
	SpentAmount     string     `json:"spentAmount"`  // Accumulated tick value
	PricePerTick    string     `json:"pricePerTick"` // Cost per tick unit
	TickCount       int        `json:"tickCount"`    // Number of ticks received
	Status          Status     `json:"status"`
	StaleTimeoutSec int        `json:"staleTimeoutSecs"` // Seconds before auto-close
	LastTickAt      *time.Time `json:"lastTickAt,omitempty"`
	ClosedAt        *time.Time `json:"closedAt,omitempty"`
	CloseReason     string     `json:"closeReason,omitempty"`
	CreatedAt       time.Time  `json:"createdAt"`
	UpdatedAt       time.Time  `json:"updatedAt"`
}

// IsTerminal returns true if the stream is in a final state.
func (s *Stream) IsTerminal() bool {
	switch s.Status {
	case StatusClosed, StatusStaleClosed, StatusDisputed, StatusSettlementFailed:
		return true
	}
	return false
}

// Tick represents a single micropayment event in a stream.
type Tick struct {
	ID         string    `json:"id"`
	StreamID   string    `json:"streamId"`
	Seq        int       `json:"seq"`                // Monotonically increasing sequence number
	Amount     string    `json:"amount"`             // This tick's charge
	Cumulative string    `json:"cumulative"`         // Running total after this tick
	Metadata   string    `json:"metadata,omitempty"` // Optional payload (e.g., token count)
	CreatedAt  time.Time `json:"createdAt"`
}

// Store persists stream data.
type Store interface {
	Create(ctx context.Context, stream *Stream) error
	Get(ctx context.Context, id string) (*Stream, error)
	Update(ctx context.Context, stream *Stream) error
	ListByAgent(ctx context.Context, agentAddr string, limit int) ([]*Stream, error)
	ListStale(ctx context.Context, before time.Time, limit int) ([]*Stream, error)

	CreateTick(ctx context.Context, tick *Tick) error
	ListTicks(ctx context.Context, streamID string, limit int) ([]*Tick, error)
	GetLastTick(ctx context.Context, streamID string) (*Tick, error)
}

// LedgerService abstracts ledger operations so streams doesn't import ledger.
type LedgerService interface {
	Hold(ctx context.Context, agentAddr, amount, reference string) error
	SettleHold(ctx context.Context, buyerAddr, sellerAddr, amount, reference string) error
	ReleaseHold(ctx context.Context, agentAddr, amount, reference string) error
}

// TransactionRecorder records transactions for reputation tracking.
type TransactionRecorder interface {
	RecordTransaction(ctx context.Context, txHash, from, to, amount, serviceID, status string) error
}

// RevenueAccumulator intercepts payments for revenue staking.
type RevenueAccumulator interface {
	AccumulateRevenue(ctx context.Context, agentAddr, amount, txRef string) error
}

// ReceiptIssuer issues cryptographic receipts for payments.
type ReceiptIssuer interface {
	IssueReceipt(ctx context.Context, path, reference, from, to, amount, serviceID, status, metadata string) error
}

// OpenRequest contains the parameters for opening a stream.
type OpenRequest struct {
	BuyerAddr       string `json:"buyerAddr" binding:"required"`
	SellerAddr      string `json:"sellerAddr" binding:"required"`
	HoldAmount      string `json:"holdAmount" binding:"required"`
	PricePerTick    string `json:"pricePerTick" binding:"required"`
	ServiceID       string `json:"serviceId"`
	SessionKeyID    string `json:"sessionKeyId"`
	StaleTimeoutSec int    `json:"staleTimeoutSecs"` // 0 = use default (60s)
}

// TickRequest contains the parameters for recording a tick.
type TickRequest struct {
	Seq      int    `json:"seq"`    // Caller-supplied sequence number for idempotency (0 = auto-increment)
	Amount   string `json:"amount"` // Amount for this tick (or omit for pricePerTick)
	Metadata string `json:"metadata"`
}

// CloseRequest contains optional close parameters.
type CloseRequest struct {
	Reason string `json:"reason"`
}

func generateStreamID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return fmt.Sprintf("str_%x", b)
}

func generateTickID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return fmt.Sprintf("tick_%x", b)
}
