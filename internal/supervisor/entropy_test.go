package supervisor

import (
	"context"
	"math/big"
	"testing"

	"github.com/mbd888/alancoin/internal/ledger"
)

// mockBalanceService returns a fixed balance for testing.
type mockBalanceService struct {
	mockLedgerService
	balance *ledger.Balance
}

func (m *mockBalanceService) GetBalance(_ context.Context, _ string) (*ledger.Balance, error) {
	return m.balance, nil
}

func TestBalanceConcentration_HighPending(t *testing.T) {
	mock := &mockBalanceService{
		balance: &ledger.Balance{
			Available: "0.500000",
			Pending:   "9.500000",
			Escrowed:  "0.000000",
		},
	}

	rule := &BalanceConcentrationRule{
		BalanceProvider: mock,
		MinTotal:        big.NewInt(1_000_000),
	}

	ec := &EvalContext{
		AgentAddr: "0xAlice",
		Amount:    big.NewInt(1_000_000),
		OpType:    "hold",
		Tier:      "established",
	}

	verdict := rule.Evaluate(context.Background(), nil, ec)
	if verdict == nil || verdict.Action != Flag {
		t.Fatal("expected flag for 95% pending concentration")
	}
}

func TestBalanceConcentration_HighEscrow(t *testing.T) {
	mock := &mockBalanceService{
		balance: &ledger.Balance{
			Available: "0.500000",
			Pending:   "0.000000",
			Escrowed:  "9.500000",
		},
	}

	rule := &BalanceConcentrationRule{
		BalanceProvider: mock,
		MinTotal:        big.NewInt(1_000_000),
	}

	ec := &EvalContext{
		AgentAddr: "0xAlice",
		Amount:    big.NewInt(1_000_000),
		OpType:    "escrow_lock",
		Tier:      "established",
	}

	verdict := rule.Evaluate(context.Background(), nil, ec)
	if verdict == nil || verdict.Action != Flag {
		t.Fatal("expected flag for 95% escrow concentration")
	}
}

func TestBalanceConcentration_Healthy(t *testing.T) {
	mock := &mockBalanceService{
		balance: &ledger.Balance{
			Available: "50.000000",
			Pending:   "30.000000",
			Escrowed:  "20.000000",
		},
	}

	rule := &BalanceConcentrationRule{
		BalanceProvider: mock,
		MinTotal:        big.NewInt(1_000_000),
	}

	ec := &EvalContext{
		AgentAddr: "0xAlice",
		Amount:    big.NewInt(1_000_000),
		OpType:    "hold",
		Tier:      "established",
	}

	verdict := rule.Evaluate(context.Background(), nil, ec)
	if verdict != nil && verdict.Action == Flag {
		t.Fatalf("should not flag healthy distribution: %s", verdict.Reason)
	}
}

func TestBalanceConcentration_BelowMinTotal(t *testing.T) {
	mock := &mockBalanceService{
		balance: &ledger.Balance{
			Available: "0.000000",
			Pending:   "0.500000",
			Escrowed:  "0.000000",
		},
	}

	rule := &BalanceConcentrationRule{
		BalanceProvider: mock,
		MinTotal:        big.NewInt(10_000_000),
	}

	ec := &EvalContext{
		AgentAddr: "0xAlice",
		Amount:    big.NewInt(100_000),
		OpType:    "hold",
		Tier:      "new",
	}

	verdict := rule.Evaluate(context.Background(), nil, ec)
	if verdict != nil && verdict.Action == Flag {
		t.Fatal("should not flag small balance agent")
	}
}

func TestBalanceConcentration_NilProvider(t *testing.T) {
	rule := &BalanceConcentrationRule{}
	ec := &EvalContext{
		AgentAddr: "0xAlice",
		Amount:    big.NewInt(1_000_000),
	}
	verdict := rule.Evaluate(context.Background(), nil, ec)
	if verdict != nil {
		t.Fatal("nil provider should return nil verdict")
	}
}
