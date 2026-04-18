package usdc

import (
	"context"
	"errors"
	"log/slog"
	"math/big"
	"strings"
	"testing"
	"time"
)

const (
	testChainID   = 8453 // Base mainnet chain ID
	testUSDC      = "0x833589fcd6edb6e08f4c7c32d4f71b54bda02913"
	testFromAddr  = "0x1111111111111111111111111111111111111111"
	testToAddr    = "0x2222222222222222222222222222222222222222"
	testWalletKey = "test-wallet-secret"
)

func newTestChain() Chain {
	return Chain{
		ID:           testChainID,
		Name:         "base-mainnet",
		RPCURL:       "https://example.invalid",
		USDCContract: testUSDC,
	}
}

func newTestService(t *testing.T) (*PayoutService, *MockChainClient, *MemoryPayoutStore, *StubWallet) {
	t.Helper()
	wallet, err := NewStubWallet(testFromAddr, testWalletKey)
	if err != nil {
		t.Fatalf("NewStubWallet: %v", err)
	}
	client := NewMockChainClient(testChainID)
	store := NewMemoryPayoutStore()
	nonces := NewInMemoryNonceManager()

	// Tight timeouts so tests don't pay the default 90s ceiling.
	cfg := PayoutConfig{
		Confirmations:      2,
		ReceiptPoll:        5 * time.Millisecond,
		ReceiptTimeout:     2 * time.Second,
		DropDetectionGrace: 500 * time.Millisecond,
		MaxSubmitAttempts:  3,
	}
	svc, err := NewPayoutService(newTestChain(), client, wallet, nonces, store, cfg, slog.Default())
	if err != nil {
		t.Fatalf("NewPayoutService: %v", err)
	}
	return svc, client, store, wallet
}

func sendOne(t *testing.T, svc *PayoutService, ref string) *Payout {
	t.Helper()
	p, err := svc.Send(context.Background(), TransferRequest{
		ChainID:   testChainID,
		ToAddr:    testToAddr,
		Amount:    big.NewInt(1_000_000), // 1.000000 USDC
		ClientRef: ref,
	})
	if err != nil {
		t.Fatalf("Send(%q): %v", ref, err)
	}
	return p
}

func TestPayout_SuccessPath(t *testing.T) {
	svc, client, store, _ := newTestService(t)

	done := make(chan *Payout, 1)
	go func() { done <- sendOne(t, svc, "ref-1") }()

	// Give the service a moment to submit the tx, then mine it.
	waitForSubmitted(t, client)
	client.Mine(3, true, true) // 3 blocks, include pending, success

	select {
	case p := <-done:
		if p.Status != TxStatusSuccess {
			t.Fatalf("status=%s message=%q", p.Status, p.LastError)
		}
		if p.TxHash == "" {
			t.Fatal("empty tx hash")
		}
		if p.Receipt == nil || p.Receipt.Confirmations < 2 {
			t.Errorf("unexpected receipt: %+v", p.Receipt)
		}
		if p.From != strings.ToLower(testFromAddr) || p.To != strings.ToLower(testToAddr) {
			t.Errorf("address mismatch from=%s to=%s", p.From, p.To)
		}
		// Store round-trip
		got, err := store.GetByClientRef(context.Background(), "ref-1")
		if err != nil || got == nil || got.TxHash != p.TxHash {
			t.Errorf("store did not round-trip: %+v err=%v", got, err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Send did not return in time")
	}
}

func TestPayout_IdempotentByClientRef(t *testing.T) {
	svc, client, _, _ := newTestService(t)

	done1 := make(chan *Payout, 1)
	go func() { done1 <- sendOne(t, svc, "same-ref") }()
	waitForSubmitted(t, client)
	client.Mine(3, true, true)

	p1 := <-done1

	// Re-send with the same ref. Must return the existing payout without
	// touching the chain (nonce should not advance).
	prevNonce := client.pendingNonce(testFromAddr)
	p2, err := svc.Send(context.Background(), TransferRequest{
		ChainID: testChainID, ToAddr: testToAddr,
		Amount: big.NewInt(1_000_000), ClientRef: "same-ref",
	})
	if err != nil {
		t.Fatalf("second Send: %v", err)
	}
	if p1.TxHash != p2.TxHash {
		t.Errorf("idempotency violated: %s vs %s", p1.TxHash, p2.TxHash)
	}
	if got := client.pendingNonce(testFromAddr); got != prevNonce {
		t.Errorf("nonce advanced on idempotent resend: %d -> %d", prevNonce, got)
	}
}

func TestPayout_RetriesTransientSendError(t *testing.T) {
	svc, client, _, _ := newTestService(t)
	client.InjectSendError(errors.New("mempool full")) // retryable

	done := make(chan *Payout, 1)
	go func() { done <- sendOne(t, svc, "ref-retry") }()
	waitForSubmitted(t, client)
	client.Mine(3, true, true)

	p := <-done
	if p.Status != TxStatusSuccess {
		t.Fatalf("expected success after retry, got status=%s", p.Status)
	}
}

func TestPayout_NonRetryableErrorSurfaces(t *testing.T) {
	svc, client, _, _ := newTestService(t)
	client.InjectSendError(NonRetryable(errors.New("bad signature")))

	_, err := svc.Send(context.Background(), TransferRequest{
		ChainID:   testChainID,
		ToAddr:    testToAddr,
		Amount:    big.NewInt(1_000_000),
		ClientRef: "ref-bad-sig",
	})
	if err == nil {
		t.Fatal("expected non-retryable error to surface")
	}
	if !strings.Contains(err.Error(), "bad signature") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPayout_ReceiptTimeoutReportsDropped(t *testing.T) {
	svc, client, _, _ := newTestService(t)

	done := make(chan *Payout, 1)
	errCh := make(chan error, 1)
	go func() {
		p, err := svc.Send(context.Background(), TransferRequest{
			ChainID: testChainID, ToAddr: testToAddr,
			Amount: big.NewInt(1_000_000), ClientRef: "ref-drop",
		})
		errCh <- err
		done <- p
	}()

	waitForSubmitted(t, client)
	client.Drop(firstTxHash(client))

	// The drop causes GetReceipt to return TxStatusDropped, finalizing
	// the payout with that status.
	select {
	case err := <-errCh:
		p := <-done
		if err != nil {
			t.Fatalf("expected nil err on clean drop, got: %v", err)
		}
		if p.Status != TxStatusDropped {
			t.Errorf("status=%s want dropped", p.Status)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Send did not return in time")
	}
}

func TestPayout_RejectsAmountNonPositive(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	_, err := svc.Send(context.Background(), TransferRequest{
		ChainID: testChainID, ToAddr: testToAddr,
		Amount: big.NewInt(0), ClientRef: "ref-zero",
	})
	if !errors.Is(err, ErrAmountNonPositive) {
		t.Errorf("expected ErrAmountNonPositive, got %v", err)
	}
}

func TestPayout_RejectsBadRecipient(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	_, err := svc.Send(context.Background(), TransferRequest{
		ChainID: testChainID, ToAddr: "not-an-address",
		Amount: big.NewInt(1), ClientRef: "ref-bad-to",
	})
	if !errors.Is(err, ErrBadRecipient) {
		t.Errorf("expected ErrBadRecipient, got %v", err)
	}
}

func TestPayout_ChainMismatchAtConstruct(t *testing.T) {
	wallet, _ := NewStubWallet(testFromAddr, testWalletKey)
	client := NewMockChainClient(999) // different chain
	_, err := NewPayoutService(
		newTestChain(),
		client, wallet,
		NewInMemoryNonceManager(),
		NewMemoryPayoutStore(),
		PayoutConfig{}, slog.Default(),
	)
	if !errors.Is(err, ErrChainMismatch) {
		t.Errorf("expected ErrChainMismatch, got %v", err)
	}
}

func TestNonceManager_MonotonicUnderConcurrency(t *testing.T) {
	m := NewInMemoryNonceManager()
	ctx := context.Background()
	const workers = 16
	const perWorker = 8

	got := make(chan uint64, workers*perWorker)
	for i := 0; i < workers; i++ {
		go func() {
			for j := 0; j < perWorker; j++ {
				n, err := m.Next(ctx, testFromAddr, 0)
				if err != nil {
					t.Errorf("Next: %v", err)
					return
				}
				got <- n
			}
		}()
	}
	seen := make(map[uint64]bool)
	for i := 0; i < workers*perWorker; i++ {
		n := <-got
		if seen[n] {
			t.Errorf("duplicate nonce %d", n)
		}
		seen[n] = true
	}
	if len(seen) != workers*perWorker {
		t.Errorf("expected %d unique nonces, got %d", workers*perWorker, len(seen))
	}
}

func TestNonceManager_ReleaseRewindsOnFailure(t *testing.T) {
	m := NewInMemoryNonceManager()
	ctx := context.Background()
	n1, _ := m.Next(ctx, testFromAddr, 5)
	if n1 != 5 {
		t.Fatalf("expected 5, got %d", n1)
	}
	m.Release(testFromAddr, n1, false) // broadcast failed
	n2, _ := m.Next(ctx, testFromAddr, 5)
	if n2 != 5 {
		t.Errorf("expected nonce 5 to be reused after failed release, got %d", n2)
	}
}

// --- helpers ---

// waitForSubmitted waits for at least one tx to appear in the mock client.
// Keeps Send goroutine driving while the test prepares to Mine/Drop.
func waitForSubmitted(t *testing.T, c *MockChainClient) {
	t.Helper()
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		n := len(c.txs)
		c.mu.Unlock()
		if n > 0 {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("no tx submitted within 1s")
}

// firstTxHash returns an arbitrary submitted tx hash (there should be only
// one in these tests). Used by tests that need to drop or inspect a tx.
func firstTxHash(c *MockChainClient) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	for h := range c.txs {
		return h
	}
	return ""
}

// pendingNonce is a test-only accessor to avoid racing on ctx across the
// public PendingNonce method.
func (m *MockChainClient) pendingNonce(address string) uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.nonces[strings.ToLower(address)]
}
