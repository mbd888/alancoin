package usdc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"sync"
	"time"
)

// Payout errors. Keep the exported error vars stable so callers can branch
// on specific conditions via errors.Is.
var (
	ErrChainMismatch      = errors.New("usdc: wallet chain does not match request chain")
	ErrAmountNonPositive  = errors.New("usdc: transfer amount must be positive")
	ErrBadRecipient       = errors.New("usdc: invalid recipient address")
	ErrDuplicateClientRef = errors.New("usdc: duplicate client reference")
	ErrReceiptNotFound    = errors.New("usdc: receipt not found")
)

// PayoutConfig tunes the payout service's retry/timeout behavior.
// Defaults are applied by (*PayoutService).Send when a field is zero.
type PayoutConfig struct {
	Confirmations      uint64        // blocks to wait before declaring Success; default 12
	ReceiptPoll        time.Duration // interval between GetReceipt polls; default 4s
	ReceiptTimeout     time.Duration // abandon waiting after this; default 90s
	DropDetectionGrace time.Duration // treat Pending as Dropped after this when mempool has no record; default 5 min
	MaxSubmitAttempts  int           // attempts for transient submission errors; default 3
}

// Payout is a record of a single outbound USDC transfer attempt.
// It's the unit of idempotency exposed to callers: re-calling Send with
// the same ClientRef returns the existing record instead of a new tx.
type Payout struct {
	ClientRef   string
	ChainID     int64
	From        string
	To          string
	Amount      *big.Int
	Nonce       uint64
	TxHash      string
	Status      TxStatus
	SubmittedAt time.Time
	FinalizedAt *time.Time
	Receipt     *TxReceipt
	LastError   string // non-empty when Status=TxStatusFailed or Dropped
}

// PayoutStore persists Payout records so re-sends are idempotent across
// process restarts. The memory implementation in this file is fine for
// tests; production uses a Postgres-backed one.
type PayoutStore interface {
	Put(ctx context.Context, p *Payout) error
	GetByClientRef(ctx context.Context, ref string) (*Payout, error)
}

// PayoutService orchestrates a single outbound transfer end-to-end.
// It is intentionally stateless — all durable state lives in the
// PayoutStore and the on-chain ledger.
type PayoutService struct {
	chain  Chain
	client ChainClient
	wallet Wallet
	nonces NonceManager
	store  PayoutStore
	cfg    PayoutConfig
	logger *slog.Logger
}

// NewPayoutService wires the pieces needed to send a USDC transfer and
// track it to finality. The wallet must match chain.ID (checked via the
// client's ChainID()) and must be the sender for all requests — there is
// no multi-wallet routing here by design.
func NewPayoutService(
	chain Chain,
	client ChainClient,
	wallet Wallet,
	nonces NonceManager,
	store PayoutStore,
	cfg PayoutConfig,
	logger *slog.Logger,
) (*PayoutService, error) {
	if err := chain.Validate(); err != nil {
		return nil, err
	}
	if client == nil || wallet == nil || nonces == nil || store == nil {
		return nil, errors.New("usdc: client, wallet, nonces, and store are required")
	}
	if client.ChainID() != chain.ID {
		return nil, fmt.Errorf("%w: chain.ID=%d client.ChainID()=%d",
			ErrChainMismatch, chain.ID, client.ChainID())
	}
	if logger == nil {
		logger = slog.Default()
	}
	cfg = applyPayoutDefaults(cfg)
	return &PayoutService{
		chain:  chain,
		client: client,
		wallet: wallet,
		nonces: nonces,
		store:  store,
		cfg:    cfg,
		logger: logger,
	}, nil
}

// ChainID returns the chain the service is bound to. Handy for callers
// that need to build TransferRequests without re-reading config.
func (s *PayoutService) ChainID() int64 { return s.chain.ID }

// GetByClientRef returns a payout by its idempotency key, or (nil, nil)
// when no record exists. Exposed so handlers don't reach into the store.
func (s *PayoutService) GetByClientRef(ctx context.Context, ref string) (*Payout, error) {
	return s.store.GetByClientRef(ctx, ref)
}

func applyPayoutDefaults(cfg PayoutConfig) PayoutConfig {
	if cfg.Confirmations == 0 {
		cfg.Confirmations = 12
	}
	if cfg.ReceiptPoll == 0 {
		cfg.ReceiptPoll = 4 * time.Second
	}
	if cfg.ReceiptTimeout == 0 {
		cfg.ReceiptTimeout = 90 * time.Second
	}
	if cfg.DropDetectionGrace == 0 {
		cfg.DropDetectionGrace = 5 * time.Minute
	}
	if cfg.MaxSubmitAttempts == 0 {
		cfg.MaxSubmitAttempts = 3
	}
	return cfg
}

// Send submits a USDC transfer and blocks until finality (success, failure,
// or drop). ClientRef is required and acts as the idempotency key: calling
// Send twice with the same ref returns the existing Payout record without
// hitting the chain a second time.
func (s *PayoutService) Send(ctx context.Context, req TransferRequest) (*Payout, error) {
	if req.ClientRef == "" {
		return nil, fmt.Errorf("usdc: TransferRequest.ClientRef is required")
	}
	if req.Amount == nil || req.Amount.Sign() <= 0 {
		return nil, ErrAmountNonPositive
	}
	if !isHexAddress(req.ToAddr) {
		return nil, ErrBadRecipient
	}
	if req.ChainID != s.chain.ID {
		return nil, fmt.Errorf("%w: request chain=%d service chain=%d",
			ErrChainMismatch, req.ChainID, s.chain.ID)
	}
	req.FromAddr = s.wallet.Address()

	// Idempotency: if a payout with this ref already exists, return it.
	if existing, err := s.store.GetByClientRef(ctx, req.ClientRef); err == nil && existing != nil {
		return existing, nil
	}

	submitted, err := s.submitWithRetry(ctx, &req)
	if err != nil {
		return nil, err
	}

	payout := &Payout{
		ClientRef:   req.ClientRef,
		ChainID:     req.ChainID,
		From:        strings.ToLower(req.FromAddr),
		To:          strings.ToLower(req.ToAddr),
		Amount:      new(big.Int).Set(req.Amount),
		Nonce:       req.Nonce,
		TxHash:      submitted.TxHash,
		Status:      TxStatusPending,
		SubmittedAt: submitted.SubmittedAt,
	}
	if err := s.store.Put(ctx, payout); err != nil {
		return nil, fmt.Errorf("persist payout: %w", err)
	}

	receipt, err := s.waitForReceipt(ctx, submitted.TxHash)
	if err != nil {
		payout.LastError = err.Error()
		payout.Status = TxStatusDropped
		now := time.Now().UTC()
		payout.FinalizedAt = &now
		if storeErr := s.store.Put(ctx, payout); storeErr != nil {
			s.logger.Error("failed to persist dropped payout status",
				"payout", payout.ClientRef, "tx", payout.TxHash, "err", storeErr)
		}
		return payout, err
	}

	payout.Receipt = &receipt
	payout.Status = receipt.Status
	now := time.Now().UTC()
	payout.FinalizedAt = &now
	if err := s.store.Put(ctx, payout); err != nil {
		return nil, fmt.Errorf("persist final payout: %w", err)
	}
	return payout, nil
}

// submitWithRetry issues a fresh nonce + fee quote and submits the tx.
// Transient send errors (mempool full, RPC hiccup) retry with a new quote;
// non-retryable errors (bad signature, chain mismatch) surface immediately.
func (s *PayoutService) submitWithRetry(ctx context.Context, req *TransferRequest) (SubmittedTx, error) {
	var lastErr error
	for attempt := 0; attempt < s.cfg.MaxSubmitAttempts; attempt++ {
		onchainNonce, err := s.client.PendingNonce(ctx, s.wallet.Address())
		if err != nil {
			lastErr = fmt.Errorf("pending nonce: %w", err)
			continue
		}
		nonce, err := s.nonces.Next(ctx, s.wallet.Address(), onchainNonce)
		if err != nil {
			lastErr = fmt.Errorf("next nonce: %w", err)
			continue
		}
		req.Nonce = nonce

		quote, err := s.client.EstimateFee(ctx, *req)
		if err != nil {
			s.nonces.Release(s.wallet.Address(), nonce, false)
			lastErr = fmt.Errorf("estimate fee: %w", err)
			continue
		}
		req.FeeQuote = quote

		submitted, err := s.client.SendTransfer(ctx, *req, s.wallet)
		if err != nil {
			s.nonces.Release(s.wallet.Address(), nonce, false)
			if isNonRetryable(err) {
				return SubmittedTx{}, err
			}
			lastErr = fmt.Errorf("send transfer (attempt %d): %w", attempt+1, err)
			continue
		}
		return submitted, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("usdc: exhausted submit attempts (%d)", s.cfg.MaxSubmitAttempts)
	}
	return SubmittedTx{}, lastErr
}

// waitForReceipt polls GetReceipt until the tx is past confirmation depth
// or until ReceiptTimeout elapses. Returns the receipt in its final state.
//
// A receipt with Status=Pending beyond DropDetectionGrace is treated as
// dropped and returned with Status=TxStatusDropped.
func (s *PayoutService) waitForReceipt(ctx context.Context, txHash string) (TxReceipt, error) {
	deadline := time.Now().Add(s.cfg.ReceiptTimeout)
	start := time.Now()

	timer := time.NewTimer(s.cfg.ReceiptPoll)
	defer timer.Stop()

	for {
		receipt, err := s.client.GetReceipt(ctx, txHash, s.cfg.Confirmations)
		if err != nil && !errors.Is(err, ErrReceiptNotFound) {
			return TxReceipt{}, err
		}
		switch receipt.Status {
		case TxStatusSuccess, TxStatusFailed, TxStatusDropped:
			return receipt, nil
		case TxStatusPending:
			if time.Since(start) >= s.cfg.DropDetectionGrace {
				receipt.Status = TxStatusDropped
				return receipt, nil
			}
		}
		if time.Now().After(deadline) {
			receipt.Status = TxStatusPending
			return receipt, fmt.Errorf("usdc: receipt timeout after %s", s.cfg.ReceiptTimeout)
		}
		select {
		case <-ctx.Done():
			return TxReceipt{}, ctx.Err()
		case <-timer.C:
			timer.Reset(s.cfg.ReceiptPoll)
		}
	}
}

// isNonRetryable classifies errors that should not trigger a retry.
// The ChainClient impl is expected to wrap non-retryable errors with
// fmt.Errorf("non-retryable: ...") (or use NonRetryable below); everything
// else is treated as transient.
func isNonRetryable(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "non-retryable")
}

// NonRetryable wraps err with a sentinel prefix so isNonRetryable picks it
// up without a custom error type. ChainClient implementations should use
// this for: invalid signature, chain-mismatch, insufficient-balance,
// recipient-blocklisted, or any condition that will fail identically on retry.
func NonRetryable(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("non-retryable: %w", err)
}

// --- in-memory PayoutStore ---

// MemoryPayoutStore is a PayoutStore for dev/test. Replace in production.
type MemoryPayoutStore struct {
	mu    sync.RWMutex
	byRef map[string]*Payout
}

// NewMemoryPayoutStore returns an empty store.
func NewMemoryPayoutStore() *MemoryPayoutStore {
	return &MemoryPayoutStore{byRef: make(map[string]*Payout)}
}

func (s *MemoryPayoutStore) Put(_ context.Context, p *Payout) error {
	if p == nil || p.ClientRef == "" {
		return errors.New("usdc: payout requires ClientRef")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *p
	if p.Amount != nil {
		cp.Amount = new(big.Int).Set(p.Amount)
	}
	if p.Receipt != nil {
		rc := *p.Receipt
		cp.Receipt = &rc
	}
	s.byRef[p.ClientRef] = &cp
	return nil
}

func (s *MemoryPayoutStore) GetByClientRef(_ context.Context, ref string) (*Payout, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.byRef[ref]
	if !ok {
		return nil, nil
	}
	cp := *p
	if p.Amount != nil {
		cp.Amount = new(big.Int).Set(p.Amount)
	}
	if p.Receipt != nil {
		rc := *p.Receipt
		cp.Receipt = &rc
	}
	return &cp, nil
}
