package usdc

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"
)

// MockChainClient is an in-memory ChainClient for tests.
// Call Mine() to advance the block height, causing queued txs to become
// observable receipts. Call InjectSendError, InjectFeeError, Drop to
// simulate specific failure modes.
type MockChainClient struct {
	mu sync.Mutex

	chainID int64
	head    uint64

	nonces map[string]uint64 // pending nonce per address
	txs    map[string]*mockTx

	// Injected failures. Consumed on next matching call.
	sendErrors []error
	feeErrors  []error

	// Dropped tx hashes return ErrReceiptNotFound from GetReceipt.
	dropped map[string]struct{}
}

type mockTx struct {
	hash        string
	from        string
	to          string
	amount      *big.Int
	nonce       uint64
	submittedAt time.Time
	includedAt  *uint64 // block number when mined; nil = pending
	succeeded   bool
}

// NewMockChainClient returns an empty mock pinned to the given chain ID.
// BlockNumber starts at 0; call Mine() to advance it.
func NewMockChainClient(chainID int64) *MockChainClient {
	return &MockChainClient{
		chainID: chainID,
		nonces:  make(map[string]uint64),
		txs:     make(map[string]*mockTx),
		dropped: make(map[string]struct{}),
	}
}

func (m *MockChainClient) ChainID() int64 { return m.chainID }

func (m *MockChainClient) PendingNonce(_ context.Context, address string) (uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.nonces[strings.ToLower(address)], nil
}

func (m *MockChainClient) EstimateFee(_ context.Context, req TransferRequest) (FeeQuote, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.feeErrors) > 0 {
		err := m.feeErrors[0]
		m.feeErrors = m.feeErrors[1:]
		return FeeQuote{}, err
	}
	// Flat, deterministic quote. Real impl pulls from eth_feeHistory.
	return FeeQuote{
		BaseFee:              big.NewInt(1_000_000_000),
		MaxFeePerGas:         big.NewInt(2_000_000_000),
		MaxPriorityFeePerGas: big.NewInt(100_000_000),
		EstimatedGas:         60_000,
		QuotedAt:             time.Now().UTC(),
	}, nil
}

func (m *MockChainClient) SendTransfer(ctx context.Context, req TransferRequest, wallet Wallet) (SubmittedTx, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.sendErrors) > 0 {
		err := m.sendErrors[0]
		m.sendErrors = m.sendErrors[1:]
		return SubmittedTx{}, err
	}
	if req.ChainID != m.chainID {
		return SubmittedTx{}, NonRetryable(fmt.Errorf("chain mismatch req=%d mock=%d", req.ChainID, m.chainID))
	}
	if !strings.EqualFold(req.FromAddr, wallet.Address()) {
		return SubmittedTx{}, NonRetryable(errors.New("from addr does not match wallet"))
	}

	// Make the signer actually sign so StubWallet behavior is exercised.
	digest := digestFor(req)
	if _, err := wallet.Sign(ctx, digest); err != nil {
		return SubmittedTx{}, NonRetryable(fmt.Errorf("sign: %w", err))
	}

	fromAddr := strings.ToLower(req.FromAddr)
	expected := m.nonces[fromAddr]
	if req.Nonce != expected {
		return SubmittedTx{}, fmt.Errorf("nonce gap: got %d want %d", req.Nonce, expected)
	}
	m.nonces[fromAddr] = req.Nonce + 1

	hash := "0x" + hex.EncodeToString(digest)
	tx := &mockTx{
		hash:        hash,
		from:        fromAddr,
		to:          strings.ToLower(req.ToAddr),
		amount:      new(big.Int).Set(req.Amount),
		nonce:       req.Nonce,
		submittedAt: time.Now().UTC(),
	}
	m.txs[hash] = tx
	return SubmittedTx{
		TxHash:      hash,
		ChainID:     m.chainID,
		Nonce:       req.Nonce,
		SubmittedAt: tx.submittedAt,
	}, nil
}

func (m *MockChainClient) GetReceipt(_ context.Context, txHash string, minConfirmations uint64) (TxReceipt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, dropped := m.dropped[txHash]; dropped {
		return TxReceipt{TxHash: txHash, Status: TxStatusDropped, CheckedAt: time.Now().UTC()}, nil
	}

	tx, ok := m.txs[txHash]
	if !ok {
		return TxReceipt{TxHash: txHash, Status: TxStatusPending, CheckedAt: time.Now().UTC()}, ErrReceiptNotFound
	}
	if tx.includedAt == nil {
		return TxReceipt{TxHash: txHash, Status: TxStatusPending, CheckedAt: time.Now().UTC()}, nil
	}

	conf := uint64(0)
	if m.head >= *tx.includedAt {
		conf = m.head - *tx.includedAt + 1
	}
	status := TxStatusPending
	if conf >= minConfirmations {
		if tx.succeeded {
			status = TxStatusSuccess
		} else {
			status = TxStatusFailed
		}
	}
	return TxReceipt{
		TxHash:        txHash,
		Status:        status,
		BlockNumber:   *tx.includedAt,
		BlockHash:     "0x" + strings.Repeat("1", 64),
		GasUsed:       50_000,
		EffectiveGas:  big.NewInt(1_500_000_000),
		Confirmations: conf,
		CheckedAt:     time.Now().UTC(),
	}, nil
}

// --- test controls ---

// Mine advances the head by n blocks. When includePending is true, any
// pending txs are first included at head+1, so they end up with n
// confirmations rather than 1. A test that mines 3 blocks including
// pending with minConfirmations=2 therefore observes Success, matching
// how real chains count confirmations (tx block = 1 confirmation, each
// subsequent block adds one).
func (m *MockChainClient) Mine(n uint64, includePending bool, success bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if includePending && n > 0 {
		includeAt := m.head + 1
		for _, tx := range m.txs {
			if tx.includedAt == nil {
				blk := includeAt
				tx.includedAt = &blk
				tx.succeeded = success
			}
		}
	}
	m.head += n
}

// Head returns the mock's current block height.
func (m *MockChainClient) Head() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.head
}

// InjectSendError queues a single error to surface from the next SendTransfer call.
func (m *MockChainClient) InjectSendError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sendErrors = append(m.sendErrors, err)
}

// InjectFeeError queues a single error to surface from the next EstimateFee call.
func (m *MockChainClient) InjectFeeError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.feeErrors = append(m.feeErrors, err)
}

// Drop marks the given tx hash as "dropped", causing GetReceipt to report
// TxStatusDropped regardless of whether the tx was previously mined.
// Mimics a deep reorg from the payout service's point of view.
func (m *MockChainClient) Drop(txHash string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dropped[txHash] = struct{}{}
}

// SetPendingNonce forces the on-chain pending nonce for an address.
// Useful for tests that want to simulate an externally-broadcast tx that
// bumped the nonce without going through this mock.
func (m *MockChainClient) SetPendingNonce(address string, nonce uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nonces[strings.ToLower(address)] = nonce
}

// digestFor builds a deterministic 32-byte digest from a transfer request.
// The real implementation keccak256s the RLP-encoded tx; the mock uses
// SHA-256 so tests don't pull in go-ethereum crypto.
func digestFor(req TransferRequest) []byte {
	h := sha256.New()
	var buf [8]byte

	if req.ChainID < 0 {
		panic("negative chain ID")
	}
	binary.BigEndian.PutUint64(buf[:], uint64(req.ChainID)) //nolint:gosec // guarded above
	h.Write(buf[:])
	h.Write([]byte(strings.ToLower(req.FromAddr)))
	h.Write([]byte(strings.ToLower(req.ToAddr)))
	h.Write(req.Amount.Bytes())
	binary.BigEndian.PutUint64(buf[:], req.Nonce)
	h.Write(buf[:])
	h.Write([]byte(req.ClientRef))

	sum := h.Sum(nil)
	return sum
}
