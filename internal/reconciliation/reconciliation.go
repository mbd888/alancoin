// Package reconciliation compares on-chain balances against ledger totals.
package reconciliation

import (
	"context"
	"fmt"
	"math/big"

	"github.com/mbd888/alancoin/internal/usdc"
)

// BalanceSummer returns the sum of all agent balances in the ledger.
type BalanceSummer interface {
	SumAllBalances(ctx context.Context) (available, pending, escrowed string, err error)
}

// ChainBalanceProvider returns the platform wallet's on-chain USDC balance.
type ChainBalanceProvider interface {
	PlatformUSDCBalance(ctx context.Context) (*big.Int, error)
}

// OnChainResult holds the outcome of an on-chain reconciliation check.
type OnChainResult struct {
	Match           bool   `json:"match"`
	PlatformBalance string `json:"platformBalance"`
	LedgerTotal     string `json:"ledgerTotal"`
	Diff            string `json:"diff"`
}

// Service performs reconciliation between ledger and on-chain state.
type Service struct {
	summer         BalanceSummer
	chain          ChainBalanceProvider
	alertThreshold *big.Int // in USDC smallest units; default $1 = 1_000_000
}

// NewService creates a reconciliation service.
func NewService(summer BalanceSummer, chain ChainBalanceProvider) *Service {
	threshold, _ := usdc.Parse("1.000000")
	return &Service{
		summer:         summer,
		chain:          chain,
		alertThreshold: threshold,
	}
}

// SetAlertThreshold sets the threshold above which mismatches are flagged.
func (s *Service) SetAlertThreshold(amount string) {
	if t, ok := usdc.Parse(amount); ok {
		s.alertThreshold = t
	}
}

// ReconcileOnChain compares the ledger sum against the on-chain USDC balance.
func (s *Service) ReconcileOnChain(ctx context.Context) (*OnChainResult, error) {
	// Sum ledger balances
	availStr, pendStr, escrowStr, err := s.summer.SumAllBalances(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to sum ledger balances: %w", err)
	}

	avail, _ := usdc.Parse(availStr)
	pend, _ := usdc.Parse(pendStr)
	escrow, _ := usdc.Parse(escrowStr)

	ledgerTotal := new(big.Int).Add(avail, new(big.Int).Add(pend, escrow))

	// Get on-chain balance
	chainBal, err := s.chain.PlatformUSDCBalance(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get on-chain balance: %w", err)
	}

	diff := new(big.Int).Sub(chainBal, ledgerTotal)
	absDiff := new(big.Int).Abs(diff)

	return &OnChainResult{
		Match:           absDiff.Cmp(s.alertThreshold) <= 0,
		PlatformBalance: usdc.Format(chainBal),
		LedgerTotal:     usdc.Format(ledgerTotal),
		Diff:            usdc.Format(diff),
	}, nil
}
