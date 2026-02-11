package escrow_test

import (
	"context"
	"math/big"
	"strings"
	"sync"
	"testing"

	"github.com/mbd888/alancoin/internal/escrow"
	"github.com/mbd888/alancoin/internal/ledger"
)

// ---------------------------------------------------------------------------
// Integration tests: real ledger MemoryStore + escrow + adapter
// ---------------------------------------------------------------------------

// ledgerAdapter mirrors the server's escrowLedgerAdapter
type ledgerAdapter struct {
	l *ledger.Ledger
}

func (a *ledgerAdapter) EscrowLock(ctx context.Context, agentAddr, amount, reference string) error {
	return a.l.EscrowLock(ctx, agentAddr, amount, reference)
}

func (a *ledgerAdapter) ReleaseEscrow(ctx context.Context, buyerAddr, sellerAddr, amount, reference string) error {
	return a.l.ReleaseEscrow(ctx, buyerAddr, sellerAddr, amount, reference)
}

func (a *ledgerAdapter) RefundEscrow(ctx context.Context, agentAddr, amount, reference string) error {
	return a.l.RefundEscrow(ctx, agentAddr, amount, reference)
}

func (a *ledgerAdapter) PartialEscrowSettle(ctx context.Context, buyerAddr, sellerAddr, releaseAmount, refundAmount, reference string) error {
	return a.l.PartialEscrowSettle(ctx, buyerAddr, sellerAddr, releaseAmount, refundAmount, reference)
}

var _ escrow.LedgerService = (*ledgerAdapter)(nil)

func setupIntegration() (*escrow.Service, *ledger.Ledger) {
	store := ledger.NewMemoryStore()
	l := ledger.New(store)
	escrowStore := escrow.NewMemoryStore()
	adapter := &ledgerAdapter{l: l}
	svc := escrow.NewService(escrowStore, adapter)
	return svc, l
}

func TestIntegration_CreateConfirmBalanceChange(t *testing.T) {
	svc, l := setupIntegration()
	ctx := context.Background()

	buyer := "0xbuyer_integration"
	seller := "0xseller_integration"

	// Fund buyer
	l.Deposit(ctx, buyer, "100.00", "0xdeposit1")

	// Verify initial balance
	buyerBal, _ := l.GetBalance(ctx, buyer)
	if buyerBal.Available != "100.000000" {
		t.Fatalf("Expected 100.000000, got %s", buyerBal.Available)
	}

	// Create escrow for $15
	esc, err := svc.Create(ctx, escrow.CreateRequest{
		BuyerAddr:  buyer,
		SellerAddr: seller,
		Amount:     "15.00",
		ServiceID:  "svc_integration",
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Buyer: available=85, escrowed=15
	buyerBal, _ = l.GetBalance(ctx, buyer)
	if buyerBal.Available != "85.000000" {
		t.Errorf("After lock: expected buyer available 85.000000, got %s", buyerBal.Available)
	}
	if buyerBal.Escrowed != "15.000000" {
		t.Errorf("After lock: expected buyer escrowed 15.000000, got %s", buyerBal.Escrowed)
	}

	// Seller should not exist yet (no balance created)
	sellerBal, _ := l.GetBalance(ctx, seller)
	if sellerBal.Available != "0" {
		t.Errorf("Seller should have 0 before confirm, got %s", sellerBal.Available)
	}

	// Confirm escrow
	_, err = svc.Confirm(ctx, esc.ID, buyer)
	if err != nil {
		t.Fatalf("Confirm failed: %v", err)
	}

	// Buyer: available=85, escrowed=0, totalOut=15
	buyerBal, _ = l.GetBalance(ctx, buyer)
	if buyerBal.Available != "85.000000" {
		t.Errorf("After confirm: expected buyer available 85.000000, got %s", buyerBal.Available)
	}
	if buyerBal.Escrowed != "0.000000" {
		t.Errorf("After confirm: expected buyer escrowed 0.000000, got %s", buyerBal.Escrowed)
	}
	if buyerBal.TotalOut != "15.000000" {
		t.Errorf("After confirm: expected buyer totalOut 15.000000, got %s", buyerBal.TotalOut)
	}

	// Seller: available=15, totalIn=15
	sellerBal, _ = l.GetBalance(ctx, seller)
	if sellerBal.Available != "15.000000" {
		t.Errorf("After confirm: expected seller available 15.000000, got %s", sellerBal.Available)
	}
	if sellerBal.TotalIn != "15.000000" {
		t.Errorf("After confirm: expected seller totalIn 15.000000, got %s", sellerBal.TotalIn)
	}

	// Fund conservation
	assertConservation(t, buyerBal, "buyer after confirm")
	assertConservation(t, sellerBal, "seller after confirm")
}

func TestIntegration_CreateDisputeRefund(t *testing.T) {
	svc, l := setupIntegration()
	ctx := context.Background()

	buyer := "0xbuyer_dispute"
	seller := "0xseller_dispute"

	l.Deposit(ctx, buyer, "50.00", "0xdeposit1")

	// Create escrow for $8
	esc, err := svc.Create(ctx, escrow.CreateRequest{
		BuyerAddr:  buyer,
		SellerAddr: seller,
		Amount:     "8.00",
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Buyer: 50 -> available=42, escrowed=8
	buyerBal, _ := l.GetBalance(ctx, buyer)
	if buyerBal.Available != "42.000000" {
		t.Errorf("After lock: expected 42.000000, got %s", buyerBal.Available)
	}

	// Dispute → enters "disputed" state, funds stay locked
	_, err = svc.Dispute(ctx, esc.ID, buyer, "service returned garbage")
	if err != nil {
		t.Fatalf("Dispute failed: %v", err)
	}

	// Funds still locked in escrow (disputed, not yet refunded)
	buyerBal, _ = l.GetBalance(ctx, buyer)
	if buyerBal.Escrowed != "8.000000" {
		t.Errorf("After dispute: expected escrowed 8.000000, got %s", buyerBal.Escrowed)
	}

	// Resolve arbitration → refund to buyer
	_, err = svc.ResolveArbitration(ctx, esc.ID, "", escrow.ResolveRequest{
		Resolution: "refund",
		Reason:     "seller failed to deliver",
	})
	if err != nil {
		t.Fatalf("ResolveArbitration failed: %v", err)
	}

	// Buyer should be back to full balance
	buyerBal, _ = l.GetBalance(ctx, buyer)
	if buyerBal.Available != "50.000000" {
		t.Errorf("After refund: expected 50.000000, got %s", buyerBal.Available)
	}
	if buyerBal.Escrowed != "0.000000" {
		t.Errorf("After refund: expected escrowed 0.000000, got %s", buyerBal.Escrowed)
	}
	if buyerBal.TotalOut != "0.000000" && buyerBal.TotalOut != "0" {
		t.Errorf("After refund: expected totalOut 0, got %s", buyerBal.TotalOut)
	}

	// Seller should have nothing
	sellerBal, _ := l.GetBalance(ctx, seller)
	if sellerBal.Available != "0" {
		t.Errorf("Seller should have 0 after refund, got %s", sellerBal.Available)
	}

	assertConservation(t, buyerBal, "buyer after dispute")
}

func TestIntegration_InsufficientFundsRejectsEscrow(t *testing.T) {
	svc, l := setupIntegration()
	ctx := context.Background()

	buyer := "0xpoor_buyer"
	l.Deposit(ctx, buyer, "5.00", "0xdeposit1")

	// Try to escrow more than available
	_, err := svc.Create(ctx, escrow.CreateRequest{
		BuyerAddr:  buyer,
		SellerAddr: "0xseller",
		Amount:     "10.00",
	})
	if err == nil {
		t.Fatal("Expected error when escrow exceeds balance")
	}

	// Balance should be unchanged
	bal, _ := l.GetBalance(ctx, buyer)
	if bal.Available != "5.000000" {
		t.Errorf("Balance should be unchanged at 5.000000, got %s", bal.Available)
	}
}

func TestIntegration_MultipleEscrowsExhaustBudget(t *testing.T) {
	svc, l := setupIntegration()
	ctx := context.Background()

	buyer := "0xbudget_buyer"
	l.Deposit(ctx, buyer, "10.00", "0xdeposit1")

	// Create 3 escrows: $3 + $4 + $3 = $10 (exactly exhausts balance)
	e1, err := svc.Create(ctx, escrow.CreateRequest{BuyerAddr: buyer, SellerAddr: "0xs1", Amount: "3.00"})
	if err != nil {
		t.Fatalf("Escrow 1 failed: %v", err)
	}
	e2, err := svc.Create(ctx, escrow.CreateRequest{BuyerAddr: buyer, SellerAddr: "0xs2", Amount: "4.00"})
	if err != nil {
		t.Fatalf("Escrow 2 failed: %v", err)
	}
	e3, err := svc.Create(ctx, escrow.CreateRequest{BuyerAddr: buyer, SellerAddr: "0xs3", Amount: "3.00"})
	if err != nil {
		t.Fatalf("Escrow 3 failed: %v", err)
	}

	// Balance should be exhausted
	bal, _ := l.GetBalance(ctx, buyer)
	if bal.Available != "0.000000" {
		t.Errorf("Expected available 0.000000, got %s", bal.Available)
	}
	if bal.Escrowed != "10.000000" {
		t.Errorf("Expected escrowed 10.000000, got %s", bal.Escrowed)
	}

	// Cannot create another escrow
	_, err = svc.Create(ctx, escrow.CreateRequest{BuyerAddr: buyer, SellerAddr: "0xs4", Amount: "0.01"})
	if err == nil {
		t.Fatal("Expected error when balance exhausted")
	}

	// Confirm 1, dispute+resolve 2, confirm 3
	svc.Confirm(ctx, e1.ID, buyer)
	svc.Dispute(ctx, e2.ID, buyer, "bad")
	svc.ResolveArbitration(ctx, e2.ID, "", escrow.ResolveRequest{Resolution: "refund"})
	svc.Confirm(ctx, e3.ID, buyer)

	// Buyer: available=4 (refunded from e2), escrowed=0, totalOut=6
	bal, _ = l.GetBalance(ctx, buyer)
	if bal.Available != "4.000000" {
		t.Errorf("Expected available 4.000000, got %s", bal.Available)
	}
	if bal.Escrowed != "0.000000" {
		t.Errorf("Expected escrowed 0.000000, got %s", bal.Escrowed)
	}
	if bal.TotalOut != "6.000000" {
		t.Errorf("Expected totalOut 6.000000, got %s", bal.TotalOut)
	}
	assertConservation(t, bal, "buyer after mixed operations")

	// Sellers got their money
	s1Bal, _ := l.GetBalance(ctx, "0xs1")
	if s1Bal.Available != "3.000000" {
		t.Errorf("Seller1: expected 3.000000, got %s", s1Bal.Available)
	}
	s2Bal, _ := l.GetBalance(ctx, "0xs2")
	if s2Bal.Available != "0" {
		t.Errorf("Seller2: expected 0 (disputed), got %s", s2Bal.Available)
	}
	s3Bal, _ := l.GetBalance(ctx, "0xs3")
	if s3Bal.Available != "3.000000" {
		t.Errorf("Seller3: expected 3.000000, got %s", s3Bal.Available)
	}
}

func TestIntegration_EscrowCoexistsWithHold(t *testing.T) {
	svc, l := setupIntegration()
	ctx := context.Background()

	agent := "0xmulti_op_agent"
	l.Deposit(ctx, agent, "20.00", "0xdeposit1")

	// Hold $5 (on-chain transfer)
	l.Hold(ctx, agent, "5.00", "hold_1")

	// Escrow $3 (service payment)
	esc, err := svc.Create(ctx, escrow.CreateRequest{
		BuyerAddr:  agent,
		SellerAddr: "0xservice_provider",
		Amount:     "3.00",
	})
	if err != nil {
		t.Fatalf("Create escrow failed: %v", err)
	}

	// available=12, pending=5, escrowed=3
	bal, _ := l.GetBalance(ctx, agent)
	if bal.Available != "12.000000" {
		t.Errorf("Expected available 12.000000, got %s", bal.Available)
	}
	if bal.Pending != "5.000000" {
		t.Errorf("Expected pending 5.000000, got %s", bal.Pending)
	}
	if bal.Escrowed != "3.000000" {
		t.Errorf("Expected escrowed 3.000000, got %s", bal.Escrowed)
	}
	assertConservation(t, bal, "mid-operations")

	// Confirm hold (on-chain done)
	l.ConfirmHold(ctx, agent, "5.00", "hold_1")

	// Confirm escrow
	svc.Confirm(ctx, esc.ID, agent)

	// available=12, pending=0, escrowed=0, totalOut=8
	bal, _ = l.GetBalance(ctx, agent)
	if bal.Available != "12.000000" {
		t.Errorf("Expected available 12.000000, got %s", bal.Available)
	}
	if bal.Pending != "0.000000" {
		t.Errorf("Expected pending 0.000000, got %s", bal.Pending)
	}
	if bal.Escrowed != "0.000000" {
		t.Errorf("Expected escrowed 0.000000, got %s", bal.Escrowed)
	}
	if bal.TotalOut != "8.000000" {
		t.Errorf("Expected totalOut 8.000000, got %s", bal.TotalOut)
	}
	assertConservation(t, bal, "after all operations")
}

func TestIntegration_ConcurrentEscrowCreation(t *testing.T) {
	svc, l := setupIntegration()
	ctx := context.Background()

	buyer := "0xconcurrent_buyer"
	l.Deposit(ctx, buyer, "100.00", "0xdeposit1")

	// Create 20 escrows of $5 each concurrently
	var wg sync.WaitGroup
	errs := make([]error, 20)
	ids := make([]string, 20)

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			esc, err := svc.Create(ctx, escrow.CreateRequest{
				BuyerAddr:  buyer,
				SellerAddr: "0xseller",
				Amount:     "5.00",
			})
			errs[idx] = err
			if esc != nil {
				ids[idx] = esc.ID
			}
		}(i)
	}
	wg.Wait()

	// Count successes and failures
	successes := 0
	for _, err := range errs {
		if err == nil {
			successes++
		}
	}

	// All should succeed since 20 * $5 = $100 = exact balance
	// But due to race conditions in the in-memory store (no CAS), some might
	// interleave reads/writes and see stale available amounts. With the mutex
	// in MemoryStore, this should work correctly.
	if successes != 20 {
		t.Errorf("Expected all 20 escrows to succeed, got %d", successes)
	}

	bal, _ := l.GetBalance(ctx, buyer)
	if bal.Available != "0.000000" {
		t.Errorf("Expected available 0.000000 after 20x$5, got %s", bal.Available)
	}
	if bal.Escrowed != "100.000000" {
		t.Errorf("Expected escrowed 100.000000, got %s", bal.Escrowed)
	}
	assertConservation(t, bal, "after concurrent creation")
}

func TestIntegration_ConcurrentCreateAndConfirm(t *testing.T) {
	svc, l := setupIntegration()
	ctx := context.Background()

	buyer := "0xrapid_buyer"
	l.Deposit(ctx, buyer, "50.00", "0xdeposit1")

	// Create 10 escrows sequentially
	escrows := make([]*escrow.Escrow, 10)
	for i := 0; i < 10; i++ {
		esc, err := svc.Create(ctx, escrow.CreateRequest{
			BuyerAddr:  buyer,
			SellerAddr: "0xseller",
			Amount:     "5.00",
		})
		if err != nil {
			t.Fatalf("Create %d failed: %v", i, err)
		}
		escrows[i] = esc
	}

	// Confirm all concurrently
	var wg sync.WaitGroup
	confirmErrs := make([]error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, confirmErrs[idx] = svc.Confirm(ctx, escrows[idx].ID, buyer)
		}(i)
	}
	wg.Wait()

	for i, err := range confirmErrs {
		if err != nil {
			t.Errorf("Confirm %d failed: %v", i, err)
		}
	}

	// Buyer: available=0, escrowed=0, totalOut=50
	bal, _ := l.GetBalance(ctx, buyer)
	if bal.Available != "0.000000" {
		t.Errorf("Expected available 0.000000, got %s", bal.Available)
	}
	if bal.Escrowed != "0.000000" {
		t.Errorf("Expected escrowed 0.000000, got %s", bal.Escrowed)
	}
	if bal.TotalOut != "50.000000" {
		t.Errorf("Expected totalOut 50.000000, got %s", bal.TotalOut)
	}

	// Seller: available=50
	sellerBal, _ := l.GetBalance(ctx, "0xseller")
	if sellerBal.Available != "50.000000" {
		t.Errorf("Expected seller available 50.000000, got %s", sellerBal.Available)
	}

	assertConservation(t, bal, "buyer after concurrent confirms")
	assertConservation(t, sellerBal, "seller after concurrent confirms")
}

func TestIntegration_HighVolumeStressTest(t *testing.T) {
	svc, l := setupIntegration()
	ctx := context.Background()

	buyer := "0xstress_buyer"
	l.Deposit(ctx, buyer, "1000.00", "0xdeposit1")

	// Create 100 escrows of $1 each
	escrows := make([]*escrow.Escrow, 100)
	for i := 0; i < 100; i++ {
		esc, err := svc.Create(ctx, escrow.CreateRequest{
			BuyerAddr:  buyer,
			SellerAddr: "0xseller",
			Amount:     "1.00",
		})
		if err != nil {
			t.Fatalf("Create %d failed: %v", i, err)
		}
		escrows[i] = esc
	}

	bal, _ := l.GetBalance(ctx, buyer)
	if bal.Available != "900.000000" {
		t.Errorf("Expected 900.000000, got %s", bal.Available)
	}
	if bal.Escrowed != "100.000000" {
		t.Errorf("Expected escrowed 100.000000, got %s", bal.Escrowed)
	}

	// Confirm even, dispute+resolve odd
	for i, esc := range escrows {
		if i%2 == 0 {
			_, err := svc.Confirm(ctx, esc.ID, buyer)
			if err != nil {
				t.Fatalf("Confirm %d failed: %v", i, err)
			}
		} else {
			_, err := svc.Dispute(ctx, esc.ID, buyer, "bad")
			if err != nil {
				t.Fatalf("Dispute %d failed: %v", i, err)
			}
			_, err = svc.ResolveArbitration(ctx, esc.ID, "", escrow.ResolveRequest{Resolution: "refund"})
			if err != nil {
				t.Fatalf("ResolveArbitration %d failed: %v", i, err)
			}
		}
	}

	// 50 confirmed ($50 to seller, $50 out for buyer)
	// 50 disputed+resolved ($50 refunded to buyer)
	// Buyer: available = 900 + 50 (refunded) = 950, totalOut = 50
	bal, _ = l.GetBalance(ctx, buyer)
	if bal.Available != "950.000000" {
		t.Errorf("Expected buyer available 950.000000, got %s", bal.Available)
	}
	if bal.TotalOut != "50.000000" {
		t.Errorf("Expected buyer totalOut 50.000000, got %s", bal.TotalOut)
	}
	if bal.Escrowed != "0.000000" {
		t.Errorf("Expected buyer escrowed 0.000000, got %s", bal.Escrowed)
	}
	assertConservation(t, bal, "buyer after stress test")

	sellerBal, _ := l.GetBalance(ctx, "0xseller")
	if sellerBal.Available != "50.000000" {
		t.Errorf("Expected seller available 50.000000, got %s", sellerBal.Available)
	}
	assertConservation(t, sellerBal, "seller after stress test")
}

func TestIntegration_EscrowThenRegularSpend(t *testing.T) {
	svc, l := setupIntegration()
	ctx := context.Background()

	agent := "0xmixed_agent"
	l.Deposit(ctx, agent, "20.00", "0xdeposit1")

	// Escrow $5
	esc, _ := svc.Create(ctx, escrow.CreateRequest{
		BuyerAddr:  agent,
		SellerAddr: "0xseller",
		Amount:     "5.00",
	})

	// Regular spend $3
	err := l.Spend(ctx, agent, "3.00", "sk_1")
	if err != nil {
		t.Fatalf("Spend failed: %v", err)
	}

	// available=12, escrowed=5, totalOut=3
	bal, _ := l.GetBalance(ctx, agent)
	if bal.Available != "12.000000" {
		t.Errorf("Expected available 12.000000, got %s", bal.Available)
	}

	// Confirm escrow
	svc.Confirm(ctx, esc.ID, agent)

	// available=12, escrowed=0, totalOut=8
	bal, _ = l.GetBalance(ctx, agent)
	if bal.Available != "12.000000" {
		t.Errorf("Expected available 12.000000, got %s", bal.Available)
	}
	if bal.TotalOut != "8.000000" {
		t.Errorf("Expected totalOut 8.000000, got %s", bal.TotalOut)
	}
	assertConservation(t, bal, "after mixed escrow+spend")
}

func TestIntegration_SmallAmounts(t *testing.T) {
	svc, l := setupIntegration()
	ctx := context.Background()

	buyer := "0xmicro_buyer"
	l.Deposit(ctx, buyer, "0.01", "0xdeposit1")

	// Escrow the smallest reasonable amount
	esc, err := svc.Create(ctx, escrow.CreateRequest{
		BuyerAddr:  buyer,
		SellerAddr: "0xmicro_seller",
		Amount:     "0.000001",
	})
	if err != nil {
		t.Fatalf("Micro escrow failed: %v", err)
	}

	// Confirm
	_, err = svc.Confirm(ctx, esc.ID, buyer)
	if err != nil {
		t.Fatalf("Micro confirm failed: %v", err)
	}

	buyerBal, _ := l.GetBalance(ctx, buyer)
	if buyerBal.Available != "0.009999" {
		t.Errorf("Expected 0.009999, got %s", buyerBal.Available)
	}
	assertConservation(t, buyerBal, "buyer after micro escrow")

	sellerBal, _ := l.GetBalance(ctx, "0xmicro_seller")
	if sellerBal.Available != "0.000001" {
		t.Errorf("Expected 0.000001, got %s", sellerBal.Available)
	}
}

func TestIntegration_LargeAmounts(t *testing.T) {
	svc, l := setupIntegration()
	ctx := context.Background()

	buyer := "0xlarge_buyer"
	l.Deposit(ctx, buyer, "99999999999999.999999", "0xdeposit1")

	esc, err := svc.Create(ctx, escrow.CreateRequest{
		BuyerAddr:  buyer,
		SellerAddr: "0xlarge_seller",
		Amount:     "50000000000000.500000",
	})
	if err != nil {
		t.Fatalf("Large escrow failed: %v", err)
	}

	_, err = svc.Confirm(ctx, esc.ID, buyer)
	if err != nil {
		t.Fatalf("Large confirm failed: %v", err)
	}

	buyerBal, _ := l.GetBalance(ctx, buyer)
	assertConservation(t, buyerBal, "buyer after large escrow")

	sellerBal, _ := l.GetBalance(ctx, "0xlarge_seller")
	if sellerBal.Available != "50000000000000.500000" {
		t.Errorf("Expected 50000000000000.500000, got %s", sellerBal.Available)
	}
}

func TestIntegration_AutoReleaseWithRealLedger(t *testing.T) {
	svc, l := setupIntegration()
	ctx := context.Background()

	buyer := "0xauto_buyer"
	seller := "0xauto_seller"
	l.Deposit(ctx, buyer, "10.00", "0xdeposit1")

	esc, err := svc.Create(ctx, escrow.CreateRequest{
		BuyerAddr:   buyer,
		SellerAddr:  seller,
		Amount:      "5.00",
		AutoRelease: "1ms",
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Auto-release (simulating timer)
	err = svc.AutoRelease(ctx, esc)
	if err != nil {
		t.Fatalf("AutoRelease failed: %v", err)
	}

	// Buyer: available=5, totalOut=5
	buyerBal, _ := l.GetBalance(ctx, buyer)
	if buyerBal.Available != "5.000000" {
		t.Errorf("Expected buyer available 5.000000, got %s", buyerBal.Available)
	}
	if buyerBal.TotalOut != "5.000000" {
		t.Errorf("Expected buyer totalOut 5.000000, got %s", buyerBal.TotalOut)
	}

	// Seller: available=5
	sellerBal, _ := l.GetBalance(ctx, seller)
	if sellerBal.Available != "5.000000" {
		t.Errorf("Expected seller available 5.000000, got %s", sellerBal.Available)
	}

	assertConservation(t, buyerBal, "buyer after auto-release")
	assertConservation(t, sellerBal, "seller after auto-release")
}

// assertConservation verifies totalIn - totalOut = available + pending + escrowed
func assertConservation(t *testing.T, bal *ledger.Balance, ctx string) {
	t.Helper()

	parse := func(s string) *big.Int {
		// Handle both "0" and "0.000000"
		if s == "" || s == "0" {
			return big.NewInt(0)
		}
		parts := strings.Split(s, ".")
		whole := parts[0]
		frac := ""
		if len(parts) > 1 {
			frac = parts[1]
		}
		for len(frac) < 6 {
			frac += "0"
		}
		frac = frac[:6]
		result, ok := new(big.Int).SetString(whole+frac, 10)
		if !ok {
			t.Fatalf("%s: failed to parse amount %q", ctx, s)
		}
		return result
	}

	totalIn := parse(bal.TotalIn)
	totalOut := parse(bal.TotalOut)
	available := parse(bal.Available)
	pending := parse(bal.Pending)
	escrowed := parse(bal.Escrowed)

	net := new(big.Int).Sub(totalIn, totalOut)
	sum := new(big.Int).Add(available, pending)
	sum.Add(sum, escrowed)

	if net.Cmp(sum) != 0 {
		t.Errorf("%s: FUND CONSERVATION VIOLATED: totalIn(%s) - totalOut(%s) != available(%s) + pending(%s) + escrowed(%s)",
			ctx, bal.TotalIn, bal.TotalOut, bal.Available, bal.Pending, bal.Escrowed)
	}
}
