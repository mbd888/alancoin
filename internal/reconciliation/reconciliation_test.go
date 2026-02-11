package reconciliation

import (
	"context"
	"math/big"
	"testing"

	"github.com/mbd888/alancoin/internal/usdc"
)

type mockSummer struct {
	available, pending, escrowed string
}

func (m *mockSummer) SumAllBalances(_ context.Context) (string, string, string, error) {
	return m.available, m.pending, m.escrowed, nil
}

type mockChainProvider struct {
	balance *big.Int
}

func (m *mockChainProvider) PlatformUSDCBalance(_ context.Context) (*big.Int, error) {
	return new(big.Int).Set(m.balance), nil
}

func TestReconcileOnChain_Match(t *testing.T) {
	// Ledger: 100 available + 20 pending + 5 escrowed = 125 total
	summer := &mockSummer{available: "100.000000", pending: "20.000000", escrowed: "5.000000"}

	// On-chain: 125 USDC
	chainBal, _ := usdc.Parse("125.000000")
	chain := &mockChainProvider{balance: chainBal}

	svc := NewService(summer, chain)
	result, err := svc.ReconcileOnChain(context.Background())
	if err != nil {
		t.Fatalf("ReconcileOnChain failed: %v", err)
	}

	if !result.Match {
		t.Errorf("expected match, got mismatch: platform=%s ledger=%s diff=%s",
			result.PlatformBalance, result.LedgerTotal, result.Diff)
	}
}

func TestReconcileOnChain_Mismatch(t *testing.T) {
	summer := &mockSummer{available: "100.000000", pending: "20.000000", escrowed: "5.000000"}

	// On-chain: 130 (5 more than ledger)
	chainBal, _ := usdc.Parse("130.000000")
	chain := &mockChainProvider{balance: chainBal}

	svc := NewService(summer, chain)
	result, err := svc.ReconcileOnChain(context.Background())
	if err != nil {
		t.Fatalf("ReconcileOnChain failed: %v", err)
	}

	if result.Match {
		t.Error("expected mismatch when chain has 5 more than ledger")
	}
}

func TestReconcileOnChain_WithinThreshold(t *testing.T) {
	summer := &mockSummer{available: "100.000000", pending: "0.000000", escrowed: "0.000000"}

	// On-chain: 100.50 — diff of 0.50 which is within default $1 threshold
	chainBal, _ := usdc.Parse("100.500000")
	chain := &mockChainProvider{balance: chainBal}

	svc := NewService(summer, chain)
	result, err := svc.ReconcileOnChain(context.Background())
	if err != nil {
		t.Fatalf("ReconcileOnChain failed: %v", err)
	}

	if !result.Match {
		t.Error("expected match — diff 0.50 is within $1 threshold")
	}
}

func TestReconcileOnChain_CustomThreshold(t *testing.T) {
	summer := &mockSummer{available: "100.000000", pending: "0.000000", escrowed: "0.000000"}

	// On-chain: 100.50 — diff of 0.50
	chainBal, _ := usdc.Parse("100.500000")
	chain := &mockChainProvider{balance: chainBal}

	svc := NewService(summer, chain)
	svc.SetAlertThreshold("0.100000") // Tighter threshold: $0.10

	result, err := svc.ReconcileOnChain(context.Background())
	if err != nil {
		t.Fatalf("ReconcileOnChain failed: %v", err)
	}

	if result.Match {
		t.Error("expected mismatch — diff 0.50 exceeds $0.10 threshold")
	}
}

func TestReconcileOnChain_ChainBelow(t *testing.T) {
	summer := &mockSummer{available: "100.000000", pending: "0.000000", escrowed: "0.000000"}

	// On-chain: 95 — ledger thinks there's more
	chainBal, _ := usdc.Parse("95.000000")
	chain := &mockChainProvider{balance: chainBal}

	svc := NewService(summer, chain)
	result, err := svc.ReconcileOnChain(context.Background())
	if err != nil {
		t.Fatalf("ReconcileOnChain failed: %v", err)
	}

	if result.Match {
		t.Error("expected mismatch — chain 5 below ledger")
	}
	// Diff should be negative (chain - ledger = -5)
	if result.Diff == "0.000000" {
		t.Error("expected non-zero diff")
	}
}
