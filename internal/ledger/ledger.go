// Package ledger tracks agent balances on the platform.
//
// Flow:
//  1. Agent deposits USDC to platform address
//  2. Platform credits agent's balance
//  3. Agent spends via session keys (debits balance)
//  4. Agent withdraws (platform sends USDC)
package ledger

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"time"

	"github.com/lib/pq"
	"github.com/mbd888/alancoin/internal/traces"
	"github.com/mbd888/alancoin/internal/usdc"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

var (
	ErrInsufficientBalance = errors.New("insufficient balance")
	ErrAgentNotFound       = errors.New("agent not found")
	ErrInvalidAmount       = errors.New("invalid amount")
	ErrDuplicateDeposit    = errors.New("deposit already processed")
	ErrDuplicateRefund     = errors.New("refund already processed")
	ErrAlreadyReversed     = errors.New("entry already reversed")
	ErrEntryNotFound       = errors.New("entry not found")
)

// Entry represents a ledger entry
type Entry struct {
	ID          string     `json:"id"`
	AgentAddr   string     `json:"agentAddr"`
	Type        string     `json:"type"` // deposit, withdrawal, spend, refund
	Amount      string     `json:"amount"`
	TxHash      string     `json:"txHash,omitempty"`
	Reference   string     `json:"reference,omitempty"` // session key ID, service ID, etc.
	Description string     `json:"description,omitempty"`
	ReversedAt  *time.Time `json:"reversedAt,omitempty"`
	ReversedBy  string     `json:"reversedBy,omitempty"`
	ReversalOf  string     `json:"reversalOf,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
}

// Balance represents an agent's balance
type Balance struct {
	AgentAddr   string    `json:"agentAddr"`
	Available   string    `json:"available"`   // Can be spent
	Pending     string    `json:"pending"`     // Deposits awaiting confirmation
	Escrowed    string    `json:"escrowed"`    // Locked in escrow awaiting service delivery
	CreditLimit string    `json:"creditLimit"` // Maximum credit available
	CreditUsed  string    `json:"creditUsed"`  // Current credit drawn
	TotalIn     string    `json:"totalIn"`     // Lifetime deposits
	TotalOut    string    `json:"totalOut"`    // Lifetime withdrawals + spending
	UpdatedAt   time.Time `json:"updatedAt"`
}

// Store persists ledger data
type Store interface {
	GetBalance(ctx context.Context, agentAddr string) (*Balance, error)
	Credit(ctx context.Context, agentAddr, amount, txHash, description string) error
	Debit(ctx context.Context, agentAddr, amount, reference, description string) error
	Refund(ctx context.Context, agentAddr, amount, reference, description string) error
	Withdraw(ctx context.Context, agentAddr, amount, txHash string) error
	GetHistory(ctx context.Context, agentAddr string, limit int) ([]*Entry, error)
	HasDeposit(ctx context.Context, txHash string) (bool, error)

	// Two-phase hold operations for safe transaction execution.
	// Hold moves funds from available → pending before on-chain transfer.
	// ConfirmHold moves from pending → total_out after confirmation.
	// ReleaseHold moves from pending → available if transfer fails.
	Hold(ctx context.Context, agentAddr, amount, reference string) error
	ConfirmHold(ctx context.Context, agentAddr, amount, reference string) error
	ReleaseHold(ctx context.Context, agentAddr, amount, reference string) error

	// Escrow operations for buyer-protection payments.
	// EscrowLock moves funds from available → escrowed.
	// ReleaseEscrow moves from buyer's escrowed → seller's available.
	// RefundEscrow moves from escrowed → available (dispute refund).
	EscrowLock(ctx context.Context, agentAddr, amount, reference string) error
	ReleaseEscrow(ctx context.Context, buyerAddr, sellerAddr, amount, reference string) error
	RefundEscrow(ctx context.Context, agentAddr, amount, reference string) error

	// Credit line operations.
	// SetCreditLimit sets the maximum credit for an agent.
	// UseCredit draws from the agent's credit line.
	// RepayCredit reduces outstanding credit usage.
	// GetCreditInfo returns the current credit limit and usage.
	SetCreditLimit(ctx context.Context, agentAddr, limit string) error
	UseCredit(ctx context.Context, agentAddr, amount string) error
	RepayCredit(ctx context.Context, agentAddr, amount string) error
	GetCreditInfo(ctx context.Context, agentAddr string) (creditLimit, creditUsed string, err error)

	// SumAllBalances returns the sum of all agent balances.
	SumAllBalances(ctx context.Context) (available, pending, escrowed string, err error)

	// Atomic transfer: debit sender + credit receiver in a single transaction.
	// Prevents fund loss if the process crashes between debit and credit.
	Transfer(ctx context.Context, fromAddr, toAddr, amount, reference string) error

	// SettleHold atomically moves funds from buyer's pending to seller's available.
	// Single transaction: buyer pending -= amount, buyer total_out += amount,
	// seller available += amount, seller total_in += amount.
	// Does NOT touch credit_draw_hold entries — credit tracking stays for ReleaseHold.
	SettleHold(ctx context.Context, buyerAddr, sellerAddr, amount, reference string) error

	// PartialEscrowSettle atomically splits escrowed funds between seller and buyer.
	// Single transaction: buyer escrowed -= (release+refund), buyer total_out += release,
	// buyer available += refund, seller available += release, seller total_in += release.
	PartialEscrowSettle(ctx context.Context, buyerAddr, sellerAddr, releaseAmount, refundAmount, reference string) error

	// Reversal operations.
	GetEntry(ctx context.Context, entryID string) (*Entry, error)
	Reverse(ctx context.Context, entryID, reason, adminID string) error
}

// Ledger manages agent balances
type Ledger struct {
	store        Store
	eventStore   EventStore
	auditLogger  AuditLogger
	alertChecker *AlertChecker
}

// New creates a new ledger
func New(store Store) *Ledger {
	return &Ledger{store: store}
}

// NewWithEvents creates a ledger with event sourcing.
func NewWithEvents(store Store, eventStore EventStore) *Ledger {
	return &Ledger{store: store, eventStore: eventStore}
}

// WithAuditLogger attaches an audit logger to the ledger.
func (l *Ledger) WithAuditLogger(logger AuditLogger) *Ledger {
	l.auditLogger = logger
	return l
}

// WithAlertChecker attaches an alert checker to the ledger.
func (l *Ledger) WithAlertChecker(checker *AlertChecker) *Ledger {
	l.alertChecker = checker
	return l
}

// EventStore returns the event store (may be nil).
func (l *Ledger) EventStoreRef() EventStore {
	return l.eventStore
}

// StoreRef returns the underlying store.
func (l *Ledger) StoreRef() Store {
	return l.store
}

func (l *Ledger) appendEvent(ctx context.Context, agentAddr, eventType, amount, reference, counterparty string) {
	if l.eventStore == nil {
		return
	}
	if err := l.eventStore.AppendEvent(ctx, &Event{
		AgentAddr:    agentAddr,
		EventType:    eventType,
		Amount:       amount,
		Reference:    reference,
		Counterparty: counterparty,
	}); err != nil {
		slog.Error("failed to append ledger event",
			"agent", agentAddr, "type", eventType, "error", err)
	}
}

func (l *Ledger) logAudit(ctx context.Context, agentAddr, operation, amount, reference string, before, after *Balance) {
	if l.auditLogger == nil {
		return
	}
	actorType, actorID, ip, requestID := actorFromCtx(ctx)
	_ = l.auditLogger.LogAudit(ctx, &AuditEntry{
		AgentAddr:   agentAddr,
		ActorType:   actorType,
		ActorID:     actorID,
		Operation:   operation,
		Amount:      amount,
		Reference:   reference,
		BeforeState: balanceSnapshot(before),
		AfterState:  balanceSnapshot(after),
		RequestID:   requestID,
		IPAddress:   ip,
	})
}

func (l *Ledger) checkAlerts(ctx context.Context, agentAddr string, bal *Balance, operation, amount string) {
	if l.alertChecker == nil {
		return
	}
	go l.alertChecker.Check(context.WithoutCancel(ctx), agentAddr, bal, operation, amount)
}

// GetBalance returns an agent's current balance
func (l *Ledger) GetBalance(ctx context.Context, agentAddr string) (*Balance, error) {
	return l.store.GetBalance(ctx, strings.ToLower(agentAddr))
}

// Deposit credits an agent's balance (called when deposit detected on-chain)
func (l *Ledger) Deposit(ctx context.Context, agentAddr, amount, txHash string) error {
	ctx, span := traces.StartSpan(ctx, "ledger.Deposit",
		traces.AgentAddr(agentAddr), traces.Amount(amount), attribute.String("tx_hash", txHash))
	defer span.End()

	amountBig, ok := usdc.Parse(amount)
	if !ok || amountBig.Sign() <= 0 {
		span.SetStatus(codes.Error, "invalid amount")
		return ErrInvalidAmount
	}

	// Fast-path: check for known duplicate before starting a transaction.
	// The real protection is the UNIQUE index on ledger_entries(tx_hash)
	// WHERE type='deposit', which prevents the TOCTOU race between check and insert.
	exists, err := l.store.HasDeposit(ctx, txHash)
	if err != nil {
		span.RecordError(err)
		return err
	}
	if exists {
		span.SetStatus(codes.Error, "duplicate deposit")
		return ErrDuplicateDeposit
	}

	addr := strings.ToLower(agentAddr)
	done := observeOp("deposit")
	defer done()

	before, _ := l.store.GetBalance(ctx, addr)

	if err := l.store.Credit(ctx, addr, amount, txHash, "deposit"); err != nil {
		// Catch unique constraint violation from the DB (concurrent duplicate deposit)
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == "23505" {
			span.SetStatus(codes.Error, "duplicate deposit (constraint)")
			return ErrDuplicateDeposit
		}
		return err
	}

	l.appendEvent(ctx, addr, "deposit", amount, txHash, "")
	after, _ := l.store.GetBalance(ctx, addr)
	l.logAudit(ctx, addr, "deposit", amount, txHash, before, after)
	l.checkAlerts(ctx, addr, after, "deposit", amount)
	return nil
}

// Spend debits an agent's balance (called by session key transactions)
func (l *Ledger) Spend(ctx context.Context, agentAddr, amount, sessionKeyID string) error {
	ctx, span := traces.StartSpan(ctx, "ledger.Spend",
		traces.AgentAddr(agentAddr), traces.Amount(amount), traces.Reference(sessionKeyID))
	defer span.End()

	amountBig, ok := usdc.Parse(amount)
	if !ok || amountBig.Sign() <= 0 {
		span.SetStatus(codes.Error, "invalid amount")
		return ErrInvalidAmount
	}
	_ = amountBig

	addr := strings.ToLower(agentAddr)
	done := observeOp("spend")
	defer done()

	before, _ := l.store.GetBalance(ctx, addr)

	if err := l.store.Debit(ctx, addr, amount, sessionKeyID, "session_key_spend"); err != nil {
		return err
	}

	l.appendEvent(ctx, addr, "spend", amount, sessionKeyID, "")
	after, _ := l.store.GetBalance(ctx, addr)
	l.logAudit(ctx, addr, "spend", amount, sessionKeyID, before, after)
	l.checkAlerts(ctx, addr, after, "spend", amount)
	return nil
}

// Transfer moves funds between two agents atomically with full event sourcing, audit, and alerts.
// Used for internal transfers like settlement netting.
func (l *Ledger) Transfer(ctx context.Context, from, to, amount, reference string) error {
	amountBig, ok := usdc.Parse(amount)
	if !ok || amountBig.Sign() <= 0 {
		return ErrInvalidAmount
	}
	_ = amountBig

	fromAddr := strings.ToLower(from)
	toAddr := strings.ToLower(to)
	done := observeOp("transfer")
	defer done()

	fromBefore, _ := l.store.GetBalance(ctx, fromAddr)
	toBefore, _ := l.store.GetBalance(ctx, toAddr)

	// Atomic debit+credit in a single database transaction.
	// Prevents fund loss if the process crashes between debit and credit.
	if err := l.store.Transfer(ctx, fromAddr, toAddr, amount, reference); err != nil {
		return fmt.Errorf("transfer %s→%s failed: %w", fromAddr, toAddr, err)
	}

	// Event sourcing, audit, alerts (non-critical — transfer already committed)
	l.appendEvent(ctx, fromAddr, "transfer_out", amount, reference, toAddr)
	l.appendEvent(ctx, toAddr, "transfer_in", amount, reference, fromAddr)

	fromAfter, _ := l.store.GetBalance(ctx, fromAddr)
	toAfter, _ := l.store.GetBalance(ctx, toAddr)

	l.logAudit(ctx, fromAddr, "transfer_out", amount, reference, fromBefore, fromAfter)
	l.logAudit(ctx, toAddr, "transfer_in", amount, reference, toBefore, toAfter)
	l.checkAlerts(ctx, fromAddr, fromAfter, "transfer_out", amount)
	l.checkAlerts(ctx, toAddr, toAfter, "transfer_in", amount)

	return nil
}

// Withdraw processes a withdrawal request
func (l *Ledger) Withdraw(ctx context.Context, agentAddr, amount, txHash string) error {
	amountBig, ok := usdc.Parse(amount)
	if !ok || amountBig.Sign() <= 0 {
		return ErrInvalidAmount
	}
	_ = amountBig

	addr := strings.ToLower(agentAddr)
	done := observeOp("withdrawal")
	defer done()

	before, _ := l.store.GetBalance(ctx, addr)

	if err := l.store.Withdraw(ctx, addr, amount, txHash); err != nil {
		return err
	}

	l.appendEvent(ctx, addr, "withdrawal", amount, txHash, "")
	after, _ := l.store.GetBalance(ctx, addr)
	l.logAudit(ctx, addr, "withdrawal", amount, txHash, before, after)
	l.checkAlerts(ctx, addr, after, "withdrawal", amount)
	return nil
}

// GetHistory returns ledger entries for an agent
func (l *Ledger) GetHistory(ctx context.Context, agentAddr string, limit int) ([]*Entry, error) {
	if limit <= 0 {
		limit = 50
	}
	return l.store.GetHistory(ctx, strings.ToLower(agentAddr), limit)
}

// Refund credits back an agent's balance (used when a transfer fails after debit)
func (l *Ledger) Refund(ctx context.Context, agentAddr, amount, reference string) error {
	amountBig, ok := usdc.Parse(amount)
	if !ok || amountBig.Sign() <= 0 {
		return ErrInvalidAmount
	}
	_ = amountBig

	addr := strings.ToLower(agentAddr)
	done := observeOp("refund")
	defer done()

	before, _ := l.store.GetBalance(ctx, addr)

	if err := l.store.Refund(ctx, addr, amount, reference, "transfer_refund"); err != nil {
		return err
	}

	l.appendEvent(ctx, addr, "refund", amount, reference, "")
	after, _ := l.store.GetBalance(ctx, addr)
	l.logAudit(ctx, addr, "refund", amount, reference, before, after)
	l.checkAlerts(ctx, addr, after, "refund", amount)
	return nil
}

// Hold places a hold on funds before an on-chain transfer.
func (l *Ledger) Hold(ctx context.Context, agentAddr, amount, reference string) error {
	ctx, span := traces.StartSpan(ctx, "ledger.Hold",
		traces.AgentAddr(agentAddr), traces.Amount(amount), traces.Reference(reference))
	defer span.End()

	amountBig, ok := usdc.Parse(amount)
	if !ok || amountBig.Sign() <= 0 {
		span.SetStatus(codes.Error, "invalid amount")
		return ErrInvalidAmount
	}
	_ = amountBig

	addr := strings.ToLower(agentAddr)
	done := observeOp("hold")
	defer done()

	before, _ := l.store.GetBalance(ctx, addr)

	if err := l.store.Hold(ctx, addr, amount, reference); err != nil {
		return err
	}

	l.appendEvent(ctx, addr, "hold", amount, reference, "")
	after, _ := l.store.GetBalance(ctx, addr)
	l.logAudit(ctx, addr, "hold", amount, reference, before, after)
	l.checkAlerts(ctx, addr, after, "hold", amount)
	return nil
}

// ConfirmHold finalizes a held amount after on-chain confirmation.
func (l *Ledger) ConfirmHold(ctx context.Context, agentAddr, amount, reference string) error {
	ctx, span := traces.StartSpan(ctx, "ledger.ConfirmHold",
		traces.AgentAddr(agentAddr), traces.Amount(amount), traces.Reference(reference))
	defer span.End()

	amountBig, ok := usdc.Parse(amount)
	if !ok || amountBig.Sign() <= 0 {
		span.SetStatus(codes.Error, "invalid amount")
		return ErrInvalidAmount
	}
	_ = amountBig

	addr := strings.ToLower(agentAddr)
	done := observeOp("confirm_hold")
	defer done()

	before, _ := l.store.GetBalance(ctx, addr)

	if err := l.store.ConfirmHold(ctx, addr, amount, reference); err != nil {
		return err
	}

	l.appendEvent(ctx, addr, "confirm_hold", amount, reference, "")
	after, _ := l.store.GetBalance(ctx, addr)
	l.logAudit(ctx, addr, "confirm_hold", amount, reference, before, after)
	return nil
}

// ReleaseHold returns held funds to available when a transfer fails.
func (l *Ledger) ReleaseHold(ctx context.Context, agentAddr, amount, reference string) error {
	ctx, span := traces.StartSpan(ctx, "ledger.ReleaseHold",
		traces.AgentAddr(agentAddr), traces.Amount(amount), traces.Reference(reference))
	defer span.End()

	amountBig, ok := usdc.Parse(amount)
	if !ok || amountBig.Sign() <= 0 {
		span.SetStatus(codes.Error, "invalid amount")
		return ErrInvalidAmount
	}
	_ = amountBig

	addr := strings.ToLower(agentAddr)
	done := observeOp("release_hold")
	defer done()

	before, _ := l.store.GetBalance(ctx, addr)

	if err := l.store.ReleaseHold(ctx, addr, amount, reference); err != nil {
		return err
	}

	l.appendEvent(ctx, addr, "release_hold", amount, reference, "")
	after, _ := l.store.GetBalance(ctx, addr)
	l.logAudit(ctx, addr, "release_hold", amount, reference, before, after)
	l.checkAlerts(ctx, addr, after, "release_hold", amount)
	return nil
}

// SettleHold atomically moves funds from buyer's pending to seller's available.
func (l *Ledger) SettleHold(ctx context.Context, buyerAddr, sellerAddr, amount, reference string) error {
	ctx, span := traces.StartSpan(ctx, "ledger.SettleHold",
		attribute.String("buyer.addr", buyerAddr), attribute.String("seller.addr", sellerAddr),
		traces.Amount(amount), traces.Reference(reference))
	defer span.End()

	amountBig, ok := usdc.Parse(amount)
	if !ok || amountBig.Sign() <= 0 {
		span.SetStatus(codes.Error, "invalid amount")
		return ErrInvalidAmount
	}
	_ = amountBig

	buyer := strings.ToLower(buyerAddr)
	seller := strings.ToLower(sellerAddr)
	done := observeOp("settle_hold")
	defer done()

	buyerBefore, _ := l.store.GetBalance(ctx, buyer)

	if err := l.store.SettleHold(ctx, buyer, seller, amount, reference); err != nil {
		return err
	}

	l.appendEvent(ctx, buyer, "settle_hold_out", amount, reference, seller)
	l.appendEvent(ctx, seller, "settle_hold_in", amount, reference, buyer)

	buyerAfter, _ := l.store.GetBalance(ctx, buyer)
	l.logAudit(ctx, buyer, "settle_hold", amount, reference, buyerBefore, buyerAfter)
	return nil
}

// PartialEscrowSettle atomically splits escrowed funds between seller and buyer.
func (l *Ledger) PartialEscrowSettle(ctx context.Context, buyerAddr, sellerAddr, releaseAmount, refundAmount, reference string) error {
	ctx, span := traces.StartSpan(ctx, "ledger.PartialEscrowSettle",
		attribute.String("buyer.addr", buyerAddr), attribute.String("seller.addr", sellerAddr),
		attribute.String("release", releaseAmount), attribute.String("refund", refundAmount),
		traces.Reference(reference))
	defer span.End()

	releaseBig, ok1 := usdc.Parse(releaseAmount)
	refundBig, ok2 := usdc.Parse(refundAmount)
	if !ok1 || !ok2 || releaseBig.Sign() < 0 || refundBig.Sign() < 0 {
		span.SetStatus(codes.Error, "invalid amount")
		return ErrInvalidAmount
	}
	totalBig := new(big.Int).Add(releaseBig, refundBig)
	if totalBig.Sign() <= 0 {
		span.SetStatus(codes.Error, "invalid amount")
		return ErrInvalidAmount
	}

	buyer := strings.ToLower(buyerAddr)
	seller := strings.ToLower(sellerAddr)
	done := observeOp("partial_escrow_settle")
	defer done()

	buyerBefore, _ := l.store.GetBalance(ctx, buyer)

	if err := l.store.PartialEscrowSettle(ctx, buyer, seller, releaseAmount, refundAmount, reference); err != nil {
		return err
	}

	l.appendEvent(ctx, buyer, "escrow_partial_release", releaseAmount, reference, seller)
	l.appendEvent(ctx, buyer, "escrow_partial_refund", refundAmount, reference, "")
	l.appendEvent(ctx, seller, "escrow_partial_receive", releaseAmount, reference, buyer)

	buyerAfter, _ := l.store.GetBalance(ctx, buyer)
	l.logAudit(ctx, buyer, "partial_escrow_settle", releaseAmount, reference, buyerBefore, buyerAfter)
	return nil
}

// CanSpend checks if an agent has sufficient balance (including credit)
func (l *Ledger) CanSpend(ctx context.Context, agentAddr, amount string) (bool, error) {
	amountBig, ok := usdc.Parse(amount)
	if !ok {
		return false, ErrInvalidAmount
	}

	bal, err := l.store.GetBalance(ctx, strings.ToLower(agentAddr))
	if err != nil {
		return false, err
	}

	availableBig, ok1 := usdc.Parse(bal.Available)
	creditLimitBig, ok2 := usdc.Parse(bal.CreditLimit)
	creditUsedBig, ok3 := usdc.Parse(bal.CreditUsed)
	if !ok1 || !ok2 || !ok3 {
		return false, fmt.Errorf("corrupted balance data for %s: available=%q credit_limit=%q credit_used=%q",
			agentAddr, bal.Available, bal.CreditLimit, bal.CreditUsed)
	}

	creditAvailable := new(big.Int).Sub(creditLimitBig, creditUsedBig)
	effective := new(big.Int).Add(availableBig, creditAvailable)

	return effective.Cmp(amountBig) >= 0, nil
}

// EscrowLock locks funds in escrow before service delivery.
func (l *Ledger) EscrowLock(ctx context.Context, agentAddr, amount, reference string) error {
	ctx, span := traces.StartSpan(ctx, "ledger.EscrowLock",
		traces.AgentAddr(agentAddr), traces.Amount(amount), traces.Reference(reference))
	defer span.End()

	amountBig, ok := usdc.Parse(amount)
	if !ok || amountBig.Sign() <= 0 {
		span.SetStatus(codes.Error, "invalid amount")
		return ErrInvalidAmount
	}
	_ = amountBig

	addr := strings.ToLower(agentAddr)
	done := observeOp("escrow_lock")
	defer done()

	before, _ := l.store.GetBalance(ctx, addr)

	if err := l.store.EscrowLock(ctx, addr, amount, reference); err != nil {
		return err
	}

	l.appendEvent(ctx, addr, "escrow_lock", amount, reference, "")
	after, _ := l.store.GetBalance(ctx, addr)
	l.logAudit(ctx, addr, "escrow_lock", amount, reference, before, after)
	l.checkAlerts(ctx, addr, after, "escrow_lock", amount)
	return nil
}

// ReleaseEscrow releases escrowed funds to the seller after confirmation.
func (l *Ledger) ReleaseEscrow(ctx context.Context, buyerAddr, sellerAddr, amount, reference string) error {
	ctx, span := traces.StartSpan(ctx, "ledger.ReleaseEscrow",
		attribute.String("buyer.addr", buyerAddr), attribute.String("seller.addr", sellerAddr),
		traces.Amount(amount), traces.Reference(reference))
	defer span.End()

	amountBig, ok := usdc.Parse(amount)
	if !ok || amountBig.Sign() <= 0 {
		span.SetStatus(codes.Error, "invalid amount")
		return ErrInvalidAmount
	}
	_ = amountBig

	buyer := strings.ToLower(buyerAddr)
	seller := strings.ToLower(sellerAddr)
	done := observeOp("escrow_release")
	defer done()

	buyerBefore, _ := l.store.GetBalance(ctx, buyer)

	if err := l.store.ReleaseEscrow(ctx, buyer, seller, amount, reference); err != nil {
		return err
	}

	l.appendEvent(ctx, buyer, "escrow_release", amount, reference, seller)
	l.appendEvent(ctx, seller, "escrow_receive", amount, reference, buyer)

	buyerAfter, _ := l.store.GetBalance(ctx, buyer)
	l.logAudit(ctx, buyer, "escrow_release", amount, reference, buyerBefore, buyerAfter)
	return nil
}

// RefundEscrow returns escrowed funds to the buyer after a dispute.
func (l *Ledger) RefundEscrow(ctx context.Context, agentAddr, amount, reference string) error {
	amountBig, ok := usdc.Parse(amount)
	if !ok || amountBig.Sign() <= 0 {
		return ErrInvalidAmount
	}
	_ = amountBig

	addr := strings.ToLower(agentAddr)
	done := observeOp("escrow_refund")
	defer done()

	before, _ := l.store.GetBalance(ctx, addr)

	if err := l.store.RefundEscrow(ctx, addr, amount, reference); err != nil {
		return err
	}

	l.appendEvent(ctx, addr, "escrow_refund", amount, reference, "")
	after, _ := l.store.GetBalance(ctx, addr)
	l.logAudit(ctx, addr, "escrow_refund", amount, reference, before, after)
	l.checkAlerts(ctx, addr, after, "escrow_refund", amount)
	return nil
}

// SetCreditLimit sets the maximum credit for an agent
func (l *Ledger) SetCreditLimit(ctx context.Context, agentAddr, limit string) error {
	return l.store.SetCreditLimit(ctx, strings.ToLower(agentAddr), limit)
}

// RepayCredit reduces outstanding credit usage
func (l *Ledger) RepayCredit(ctx context.Context, agentAddr, amount string) error {
	amountBig, ok := usdc.Parse(amount)
	if !ok || amountBig.Sign() <= 0 {
		return ErrInvalidAmount
	}
	_ = amountBig
	return l.store.RepayCredit(ctx, strings.ToLower(agentAddr), amount)
}

// GetCreditInfo returns the current credit limit and usage
func (l *Ledger) GetCreditInfo(ctx context.Context, agentAddr string) (creditLimit, creditUsed string, err error) {
	return l.store.GetCreditInfo(ctx, strings.ToLower(agentAddr))
}

// Reverse creates a compensating entry for an existing entry.
func (l *Ledger) Reverse(ctx context.Context, entryID, reason, adminID string) error {
	done := observeOp("reversal")
	defer done()
	return l.store.Reverse(ctx, entryID, reason, adminID)
}

// BalanceAtTime returns an agent's balance at a specific point in time.
func (l *Ledger) BalanceAtTime(ctx context.Context, agentAddr string, ts time.Time) (*Balance, error) {
	if l.eventStore == nil {
		return nil, errors.New("event store not configured")
	}
	return BalanceAtTime(ctx, l.eventStore, strings.ToLower(agentAddr), ts)
}

// ReconcileAll replays all events and compares against actual balances.
func (l *Ledger) ReconcileAll(ctx context.Context) ([]*ReconciliationResult, error) {
	if l.eventStore == nil {
		return nil, errors.New("event store not configured")
	}
	return ReconcileAll(ctx, l.eventStore, l.store)
}
