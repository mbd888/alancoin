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
	"math/big"
	"strings"
	"sync"
	"time"
)

var (
	ErrInsufficientBalance = errors.New("insufficient balance")
	ErrAgentNotFound       = errors.New("agent not found")
	ErrInvalidAmount       = errors.New("invalid amount")
	ErrDuplicateDeposit    = errors.New("deposit already processed")
)

// Entry represents a ledger entry
type Entry struct {
	ID          string    `json:"id"`
	AgentAddr   string    `json:"agentAddr"`
	Type        string    `json:"type"` // deposit, withdrawal, spend, refund
	Amount      string    `json:"amount"`
	TxHash      string    `json:"txHash,omitempty"`
	Reference   string    `json:"reference,omitempty"` // session key ID, service ID, etc.
	Description string    `json:"description,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
}

// Balance represents an agent's balance
type Balance struct {
	AgentAddr string    `json:"agentAddr"`
	Available string    `json:"available"` // Can be spent
	Pending   string    `json:"pending"`   // Deposits awaiting confirmation
	TotalIn   string    `json:"totalIn"`   // Lifetime deposits
	TotalOut  string    `json:"totalOut"`  // Lifetime withdrawals + spending
	UpdatedAt time.Time `json:"updatedAt"`
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
}

// Ledger manages agent balances
type Ledger struct {
	store Store
	mu    sync.RWMutex
}

// New creates a new ledger
func New(store Store) *Ledger {
	return &Ledger{store: store}
}

// GetBalance returns an agent's current balance
func (l *Ledger) GetBalance(ctx context.Context, agentAddr string) (*Balance, error) {
	return l.store.GetBalance(ctx, strings.ToLower(agentAddr))
}

// Deposit credits an agent's balance (called when deposit detected on-chain)
func (l *Ledger) Deposit(ctx context.Context, agentAddr, amount, txHash string) error {
	// Check for duplicate
	exists, err := l.store.HasDeposit(ctx, txHash)
	if err != nil {
		return err
	}
	if exists {
		return ErrDuplicateDeposit
	}

	return l.store.Credit(ctx, strings.ToLower(agentAddr), amount, txHash, "deposit")
}

// Spend debits an agent's balance (called by session key transactions)
func (l *Ledger) Spend(ctx context.Context, agentAddr, amount, sessionKeyID string) error {
	// Validate amount
	amountBig, ok := parseUSDC(amount)
	if !ok || amountBig.Sign() <= 0 {
		return ErrInvalidAmount
	}

	// Check balance
	bal, err := l.store.GetBalance(ctx, strings.ToLower(agentAddr))
	if err != nil {
		return err
	}

	availableBig, _ := parseUSDC(bal.Available)
	if availableBig.Cmp(amountBig) < 0 {
		return ErrInsufficientBalance
	}

	return l.store.Debit(ctx, strings.ToLower(agentAddr), amount, sessionKeyID, "session_key_spend")
}

// Withdraw processes a withdrawal request
func (l *Ledger) Withdraw(ctx context.Context, agentAddr, amount, txHash string) error {
	// Validate amount
	amountBig, ok := parseUSDC(amount)
	if !ok || amountBig.Sign() <= 0 {
		return ErrInvalidAmount
	}

	// Check balance
	bal, err := l.store.GetBalance(ctx, strings.ToLower(agentAddr))
	if err != nil {
		return err
	}

	availableBig, _ := parseUSDC(bal.Available)
	if availableBig.Cmp(amountBig) < 0 {
		return ErrInsufficientBalance
	}

	return l.store.Withdraw(ctx, strings.ToLower(agentAddr), amount, txHash)
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
	// Validate amount
	amountBig, ok := parseUSDC(amount)
	if !ok || amountBig.Sign() <= 0 {
		return ErrInvalidAmount
	}

	return l.store.Refund(ctx, strings.ToLower(agentAddr), amount, reference, "transfer_refund")
}

// CanSpend checks if an agent has sufficient balance
func (l *Ledger) CanSpend(ctx context.Context, agentAddr, amount string) (bool, error) {
	amountBig, ok := parseUSDC(amount)
	if !ok {
		return false, ErrInvalidAmount
	}

	bal, err := l.store.GetBalance(ctx, strings.ToLower(agentAddr))
	if err != nil {
		return false, err
	}

	availableBig, _ := parseUSDC(bal.Available)
	return availableBig.Cmp(amountBig) >= 0, nil
}

// USDC helpers (6 decimals)
func parseUSDC(s string) (*big.Int, bool) {
	if s == "" {
		return big.NewInt(0), true
	}

	// Handle decimal format like "1.50"
	parts := strings.Split(s, ".")
	whole := parts[0]
	frac := ""
	if len(parts) > 1 {
		frac = parts[1]
	}

	// Pad or trim to 6 decimals
	for len(frac) < 6 {
		frac += "0"
	}
	frac = frac[:6]

	combined := whole + frac
	result, ok := new(big.Int).SetString(combined, 10)
	return result, ok
}

func formatUSDC(amount *big.Int) string {
	if amount == nil {
		return "0.000000"
	}
	s := amount.String()
	for len(s) < 7 {
		s = "0" + s
	}
	decimal := len(s) - 6
	return s[:decimal] + "." + s[decimal:]
}
