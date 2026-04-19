package usdc

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"sync"
	"testing"
	"time"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// --- encoding tests ---

func TestEncodeTransferCall_KnownVectors(t *testing.T) {
	to := "0x2222222222222222222222222222222222222222"
	amount := big.NewInt(1_500_000) // 1.500000 USDC

	data, err := EncodeTransferCall(to, amount)
	if err != nil {
		t.Fatalf("EncodeTransferCall: %v", err)
	}
	if len(data) != 68 {
		t.Fatalf("expected 68 bytes, got %d", len(data))
	}

	// Selector is first 4 bytes of keccak256("transfer(address,uint256)")
	// = 0xa9059cbb. Verify directly.
	if got := hex.EncodeToString(data[0:4]); got != "a9059cbb" {
		t.Errorf("selector = %s, want a9059cbb", got)
	}
	// The 'to' address lives in bytes [16:36] of the first arg slot.
	if got := hex.EncodeToString(data[4+12 : 36]); got != strings.TrimPrefix(to, "0x") {
		t.Errorf("encoded address = %s, want %s", got, strings.TrimPrefix(to, "0x"))
	}
	// Amount is big-endian in the last 32 bytes.
	amtDecoded := new(big.Int).SetBytes(data[36:68])
	if amtDecoded.Cmp(amount) != 0 {
		t.Errorf("decoded amount = %s, want %s", amtDecoded, amount)
	}
}

func TestDecodeTransferCall_RoundTrip(t *testing.T) {
	to := "0x2222222222222222222222222222222222222222"
	amt := big.NewInt(42_000_000)
	data, err := EncodeTransferCall(to, amt)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	gotTo, gotAmt, err := DecodeTransferCall(data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if gotTo != strings.ToLower(to) {
		t.Errorf("to = %s, want %s", gotTo, to)
	}
	if gotAmt.Cmp(amt) != 0 {
		t.Errorf("amount round-trip mismatch")
	}
}

func TestEncodeTransferCall_RejectsBadAddress(t *testing.T) {
	_, err := EncodeTransferCall("not-hex", big.NewInt(1))
	if !errors.Is(err, ErrBadRecipient) {
		t.Errorf("expected ErrBadRecipient, got %v", err)
	}
}

func TestEncodeTransferCall_RejectsZeroAmount(t *testing.T) {
	_, err := EncodeTransferCall(
		"0x2222222222222222222222222222222222222222",
		big.NewInt(0),
	)
	if !errors.Is(err, ErrAmountNonPositive) {
		t.Errorf("expected ErrAmountNonPositive, got %v", err)
	}
}

// --- wallet tests ---

func TestEthWallet_AddressDeterministic(t *testing.T) {
	const testKey = "b71c71a67e1177ad4e901695e1b4b9ee17ae16c6668d313eac2f96dbcda3f291"
	w, err := NewEthWalletFromHex(testKey)
	if err != nil {
		t.Fatalf("NewEthWalletFromHex: %v", err)
	}
	// Address derived from this key is well-known from go-ethereum examples.
	// Assert it's 42 chars and lower-cased.
	if len(w.Address()) != 42 {
		t.Errorf("address length = %d, want 42", len(w.Address()))
	}
	if w.Address() != strings.ToLower(w.Address()) {
		t.Error("address should be lowercased")
	}
}

func TestEthWallet_SignsConsistentLength(t *testing.T) {
	w, err := GenerateEthWallet()
	if err != nil {
		t.Fatalf("GenerateEthWallet: %v", err)
	}
	digest := crypto.Keccak256([]byte("hello"))
	sig, err := w.Sign(context.Background(), digest)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if len(sig) != 65 {
		t.Errorf("sig len = %d, want 65", len(sig))
	}
}

func TestEthWallet_SignRejectsWrongDigestLength(t *testing.T) {
	w, _ := GenerateEthWallet()
	_, err := w.Sign(context.Background(), []byte("too short"))
	if err == nil {
		t.Error("expected error for wrong-length digest")
	}
}

// --- EthClient tests with a stub backend ---

type stubBackend struct {
	mu sync.Mutex

	chainID     *big.Int
	nonces      map[common.Address]uint64
	baseFee     *big.Int
	tip         *big.Int
	gasEstimate uint64
	gasErr      error
	head        uint64

	submitted  []*types.Transaction
	sendErr    error
	receipts   map[common.Hash]*types.Receipt
	receiptErr error
}

func newStubBackend(chainID int64) *stubBackend {
	return &stubBackend{
		chainID:     big.NewInt(chainID),
		nonces:      make(map[common.Address]uint64),
		baseFee:     big.NewInt(1_000_000_000), // 1 gwei
		tip:         big.NewInt(100_000_000),   // 0.1 gwei
		gasEstimate: 55_000,
		head:        100,
		receipts:    make(map[common.Hash]*types.Receipt),
	}
}

func (s *stubBackend) PendingNonceAt(_ context.Context, account common.Address) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.nonces[account], nil
}
func (s *stubBackend) SuggestGasTipCap(_ context.Context) (*big.Int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return new(big.Int).Set(s.tip), nil
}
func (s *stubBackend) EstimateGas(_ context.Context, _ ethereum.CallMsg) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gasErr != nil {
		return 0, s.gasErr
	}
	return s.gasEstimate, nil
}
func (s *stubBackend) HeaderByNumber(_ context.Context, _ *big.Int) (*types.Header, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return &types.Header{
		Number:  new(big.Int).SetUint64(s.head),
		BaseFee: new(big.Int).Set(s.baseFee),
	}, nil
}
func (s *stubBackend) SendTransaction(_ context.Context, tx *types.Transaction) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sendErr != nil {
		return s.sendErr
	}
	s.submitted = append(s.submitted, tx)
	// Advance the nonce for the sender. go-ethereum parses the sender from
	// the signature; we reconstruct it so the stub behaves like a real node.
	if signer := types.LatestSignerForChainID(s.chainID); signer != nil {
		if from, err := types.Sender(signer, tx); err == nil {
			s.nonces[from] = tx.Nonce() + 1
		}
	}
	return nil
}
func (s *stubBackend) TransactionReceipt(_ context.Context, hash common.Hash) (*types.Receipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.receiptErr != nil {
		return nil, s.receiptErr
	}
	rc, ok := s.receipts[hash]
	if !ok {
		return nil, errors.New("not found")
	}
	return rc, nil
}
func (s *stubBackend) BlockNumber(_ context.Context) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.head, nil
}

// setReceipt parks a receipt for the given tx hash at the given block.
// Tests call this after SendTransaction to simulate inclusion.
func (s *stubBackend) setReceipt(hash common.Hash, block uint64, success bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	status := uint64(1)
	if !success {
		status = 0
	}
	s.receipts[hash] = &types.Receipt{
		TxHash:            hash,
		Status:            status,
		BlockNumber:       new(big.Int).SetUint64(block),
		BlockHash:         common.HexToHash(fmt.Sprintf("0x%064x", block)),
		GasUsed:           50_000,
		EffectiveGasPrice: big.NewInt(1_500_000_000),
	}
}

func (s *stubBackend) advanceHead(n uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.head += n
}

func newEthTestClient(t *testing.T) (*EthClient, *stubBackend, *EthWallet) {
	t.Helper()
	chain := Chain{
		ID:           testChainID,
		Name:         "base-mainnet",
		RPCURL:       "https://example.invalid",
		USDCContract: testUSDC,
	}
	backend := newStubBackend(testChainID)
	client := newEthClientWithBackend(chain, backend)
	wallet, err := GenerateEthWallet()
	if err != nil {
		t.Fatalf("GenerateEthWallet: %v", err)
	}
	return client, backend, wallet
}

func TestEthClient_SignAndSubmit(t *testing.T) {
	client, backend, wallet := newEthTestClient(t)
	ctx := context.Background()

	req := TransferRequest{
		ChainID:   testChainID,
		FromAddr:  wallet.Address(),
		ToAddr:    testToAddr,
		Amount:    big.NewInt(1_000_000),
		ClientRef: "ref-1",
		Nonce:     0,
	}

	quote, err := client.EstimateFee(ctx, req)
	if err != nil {
		t.Fatalf("EstimateFee: %v", err)
	}
	if quote.MaxFeePerGas.Sign() <= 0 || quote.MaxPriorityFeePerGas.Sign() <= 0 {
		t.Errorf("fee quote has non-positive fields: %+v", quote)
	}
	if quote.EstimatedGas == 0 {
		t.Error("fee quote missing gas estimate")
	}
	req.FeeQuote = quote

	submitted, err := client.SendTransfer(ctx, req, wallet)
	if err != nil {
		t.Fatalf("SendTransfer: %v", err)
	}
	if submitted.TxHash == "" {
		t.Error("empty tx hash")
	}
	if len(backend.submitted) != 1 {
		t.Fatalf("expected 1 submitted tx, got %d", len(backend.submitted))
	}

	// Sanity-check the submitted tx was signed correctly and recovers the
	// wallet's address as sender.
	signer := types.LatestSignerForChainID(big.NewInt(testChainID))
	sender, err := types.Sender(signer, backend.submitted[0])
	if err != nil {
		t.Fatalf("Sender: %v", err)
	}
	if !strings.EqualFold(sender.Hex(), wallet.Address()) {
		t.Errorf("recovered sender %s != wallet %s", sender.Hex(), wallet.Address())
	}
}

func TestEthClient_GetReceiptLifecycle(t *testing.T) {
	client, backend, wallet := newEthTestClient(t)
	ctx := context.Background()

	quote, _ := client.EstimateFee(ctx, TransferRequest{
		FromAddr: wallet.Address(), ToAddr: testToAddr, Amount: big.NewInt(1),
	})
	submitted, err := client.SendTransfer(ctx, TransferRequest{
		ChainID: testChainID, FromAddr: wallet.Address(),
		ToAddr: testToAddr, Amount: big.NewInt(1_000_000),
		ClientRef: "r", Nonce: 0, FeeQuote: quote,
	}, wallet)
	if err != nil {
		t.Fatalf("SendTransfer: %v", err)
	}

	// Before the stub records a receipt, GetReceipt returns pending+not-found.
	rc, err := client.GetReceipt(ctx, submitted.TxHash, 3)
	if !errors.Is(err, ErrReceiptNotFound) {
		t.Errorf("expected ErrReceiptNotFound, got err=%v status=%s", err, rc.Status)
	}

	// Once included at block=head, confirmations = 1 < 3 → pending.
	backend.setReceipt(common.HexToHash(submitted.TxHash), backend.head, true)
	rc, err = client.GetReceipt(ctx, submitted.TxHash, 3)
	if err != nil {
		t.Fatalf("GetReceipt: %v", err)
	}
	if rc.Status != TxStatusPending {
		t.Errorf("status=%s want pending (1 confirmation, need 3)", rc.Status)
	}

	// Advance head by 3 → confirmations = 4 ≥ 3 → success.
	backend.advanceHead(3)
	rc, err = client.GetReceipt(ctx, submitted.TxHash, 3)
	if err != nil {
		t.Fatalf("GetReceipt: %v", err)
	}
	if rc.Status != TxStatusSuccess {
		t.Errorf("status=%s want success", rc.Status)
	}
	if rc.Confirmations < 3 {
		t.Errorf("confirmations=%d want >=3", rc.Confirmations)
	}
}

func TestEthClient_FailedReceipt(t *testing.T) {
	client, backend, wallet := newEthTestClient(t)
	ctx := context.Background()

	quote, _ := client.EstimateFee(ctx, TransferRequest{
		FromAddr: wallet.Address(), ToAddr: testToAddr, Amount: big.NewInt(1),
	})
	submitted, _ := client.SendTransfer(ctx, TransferRequest{
		ChainID: testChainID, FromAddr: wallet.Address(),
		ToAddr: testToAddr, Amount: big.NewInt(1_000_000),
		ClientRef: "r", Nonce: 0, FeeQuote: quote,
	}, wallet)

	backend.setReceipt(common.HexToHash(submitted.TxHash), backend.head, false) // status=0
	backend.advanceHead(5)

	rc, err := client.GetReceipt(ctx, submitted.TxHash, 1)
	if err != nil {
		t.Fatalf("GetReceipt: %v", err)
	}
	if rc.Status != TxStatusFailed {
		t.Errorf("status=%s want failed", rc.Status)
	}
}

// --- end-to-end through PayoutService ---

func TestPayoutService_WithEthClient(t *testing.T) {
	chain := Chain{
		ID: testChainID, Name: "base-mainnet",
		RPCURL: "https://example.invalid", USDCContract: testUSDC,
	}
	backend := newStubBackend(testChainID)
	client := newEthClientWithBackend(chain, backend)
	wallet, err := GenerateEthWallet()
	if err != nil {
		t.Fatalf("GenerateEthWallet: %v", err)
	}
	store := NewMemoryPayoutStore()
	nonces := NewInMemoryNonceManager()

	svc, err := NewPayoutService(chain, client, wallet, nonces, store, PayoutConfig{
		Confirmations:      2,
		ReceiptPoll:        5 * time.Millisecond,
		ReceiptTimeout:     2 * time.Second,
		DropDetectionGrace: 500 * time.Millisecond,
		MaxSubmitAttempts:  2,
	}, slog.Default())
	if err != nil {
		t.Fatalf("NewPayoutService: %v", err)
	}

	done := make(chan *Payout, 1)
	errCh := make(chan error, 1)
	go func() {
		p, err := svc.Send(context.Background(), TransferRequest{
			ChainID:   testChainID,
			ToAddr:    testToAddr,
			Amount:    big.NewInt(5_000_000),
			ClientRef: "e2e-ref",
		})
		errCh <- err
		done <- p
	}()

	// Wait for the stub to see the submission.
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		backend.mu.Lock()
		n := len(backend.submitted)
		backend.mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if len(backend.submitted) != 1 {
		t.Fatalf("expected 1 submitted tx, got %d", len(backend.submitted))
	}

	// Include the tx at the current head, then advance so confirmations ≥ 2.
	backend.setReceipt(backend.submitted[0].Hash(), backend.head, true)
	backend.advanceHead(3)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Send: %v", err)
		}
		p := <-done
		if p.Status != TxStatusSuccess {
			t.Errorf("status=%s want success", p.Status)
		}
		if p.TxHash == "" {
			t.Error("empty tx hash")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Send did not return in time")
	}
}
