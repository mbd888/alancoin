// Package withdrawals composes the ledger's two-phase hold primitive with
// the on-chain payout service to implement "move USDC from a platform-held
// Postgres balance to an external wallet".
//
// Flow:
//  1. Ledger.Hold(agent, amount, ref)                 — pending
//  2. Payouts.Send(toAddr, amount, ref)               — on-chain tx
//     3a. Success  → Ledger.ConfirmHold(agent, ...)      — debit finalized
//     3b. Fail/drop → Ledger.ReleaseHold(agent, ...)     — funds returned
//
// The service is intentionally stateless. All durability lives in the
// ledger (event log + balances) and the payout store (tx records).
package withdrawals

import (
	"context"
	"errors"
	"time"
)

// Ledger is the minimal surface the Withdrawer needs from the ledger
// package. Declared here so tests can substitute without importing ledger.
type Ledger interface {
	Hold(ctx context.Context, agentAddr, amount, reference string) error
	ConfirmHold(ctx context.Context, agentAddr, amount, reference string) error
	ReleaseHold(ctx context.Context, agentAddr, amount, reference string) error
}

// Payouts is the minimal surface we need from usdc.PayoutService.
// Returning an opaque Result avoids importing usdc types into this package
// and keeps the interface test-friendly.
type Payouts interface {
	Send(ctx context.Context, to, amount, clientRef string) (Result, error)
}

// Result is the withdraw-relevant projection of a usdc.Payout.
// Kept small so we don't leak usdc internals into the withdrawal API.
type Result struct {
	ChainID     int64
	TxHash      string
	Status      string // "success" | "failed" | "dropped" | "pending"
	BlockNumber uint64
	SubmittedAt time.Time
	FinalizedAt *time.Time
	Error       string
}

// Request is a caller's intent to withdraw. ClientRef is the idempotency
// key — repeating a request with the same ref returns the existing record.
type Request struct {
	AgentAddr string
	ToAddr    string
	Amount    string // decimal USDC string, e.g. "1.500000"
	ClientRef string
}

// Withdrawal is the durable record of one attempt, returned to the caller.
type Withdrawal struct {
	ClientRef   string
	AgentAddr   string
	ToAddr      string
	Amount      string
	Status      string // "success" | "failed" | "dropped" | "pending"
	TxHash      string
	SubmittedAt time.Time
	FinalizedAt *time.Time
	Error       string
}

// Errors surfaced from the service layer. Handlers classify these into
// 400/409/500 as appropriate.
var (
	ErrBadAmount    = errors.New("withdrawals: amount must be positive")
	ErrBadAgent     = errors.New("withdrawals: invalid agent address")
	ErrBadRecipient = errors.New("withdrawals: invalid recipient address")
	ErrMissingRef   = errors.New("withdrawals: clientRef is required")
	ErrLedgerHold   = errors.New("withdrawals: ledger hold failed")
	ErrPayoutFailed = errors.New("withdrawals: payout failed and hold was released")
	// ErrPayoutPending signals that the on-chain status is unknown (typically
	// a receipt-poll timeout). The hold is intentionally retained so a tx
	// that settles after the timeout cannot double-credit the agent. A
	// reconciler must resolve the payout out-of-band.
	ErrPayoutPending = errors.New("withdrawals: payout pending; hold retained for resolution")
)

// holdReference returns the ledger reference string used for the
// two-phase hold. Prefixed so ledger audit consumers can distinguish
// withdrawal holds from gateway/stream holds.
func holdReference(clientRef string) string {
	return "withdraw:" + clientRef
}
