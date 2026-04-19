package usdc

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
)

// rpcBackend is the subset of go-ethereum's ethclient.Client that the
// EthClient needs. Declaring it here lets tests substitute a stub without
// spinning up an actual RPC endpoint.
type rpcBackend interface {
	PendingNonceAt(ctx context.Context, account common.Address) (uint64, error)
	SuggestGasTipCap(ctx context.Context) (*big.Int, error)
	EstimateGas(ctx context.Context, msg ethereum.CallMsg) (uint64, error)
	HeaderByNumber(ctx context.Context, number *big.Int) (*types.Header, error)
	SendTransaction(ctx context.Context, tx *types.Transaction) error
	TransactionReceipt(ctx context.Context, txHash common.Hash) (*types.Receipt, error)
	BlockNumber(ctx context.Context) (uint64, error)
}

// EthClient implements ChainClient against a go-ethereum RPC backend.
// It assumes EIP-1559 fee semantics; pre-London chains are not supported.
type EthClient struct {
	chain    Chain
	backend  rpcBackend
	usdcAddr common.Address
	chainID  *big.Int
	// feeBumpPct is how much headroom is added above the current base fee
	// when building MaxFeePerGas. 25% is a common default for typical L2
	// volatility; configurable via WithFeeBumpPct.
	feeBumpPct uint64
	// gasLimitFallback is used when EstimateGas fails. ERC-20 transfer is
	// typically ~55k gas; 90k gives plenty of headroom without wasting.
	gasLimitFallback uint64
}

// Option tweaks EthClient construction without bloating NewEthClient's signature.
type Option func(*EthClient)

// WithFeeBumpPct sets the % above base fee used for MaxFeePerGas (default 25).
func WithFeeBumpPct(p uint64) Option {
	return func(c *EthClient) { c.feeBumpPct = p }
}

// WithGasLimitFallback sets the gas limit used when eth_estimateGas fails (default 90000).
func WithGasLimitFallback(g uint64) Option {
	return func(c *EthClient) { c.gasLimitFallback = g }
}

// NewEthClient dials the chain's RPC URL and returns a ready-to-use client.
// Closing is the caller's responsibility via Close().
func NewEthClient(ctx context.Context, chain Chain, opts ...Option) (*EthClient, error) {
	if err := chain.Validate(); err != nil {
		return nil, err
	}
	rpc, err := ethclient.DialContext(ctx, chain.RPCURL)
	if err != nil {
		return nil, fmt.Errorf("usdc: dial RPC: %w", err)
	}
	return newEthClientWithBackend(chain, rpc, opts...), nil
}

// newEthClientWithBackend is the constructor tests use with a stub backend.
// Keeping it unexported preserves the public surface (NewEthClient only).
func newEthClientWithBackend(chain Chain, backend rpcBackend, opts ...Option) *EthClient {
	c := &EthClient{
		chain:            chain,
		backend:          backend,
		usdcAddr:         common.HexToAddress(chain.USDCContract),
		chainID:          big.NewInt(chain.ID),
		feeBumpPct:       25,
		gasLimitFallback: 90_000,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Close releases the underlying RPC connection. Safe to call on a client
// created with a stub backend (no-op in that case).
func (c *EthClient) Close() {
	if rc, ok := c.backend.(*ethclient.Client); ok {
		rc.Close()
	}
}

// ChainID implements ChainClient.
func (c *EthClient) ChainID() int64 { return c.chain.ID }

// PendingNonce implements ChainClient.
func (c *EthClient) PendingNonce(ctx context.Context, address string) (uint64, error) {
	if !isHexAddress(address) {
		return 0, fmt.Errorf("usdc: invalid address %q", address)
	}
	return c.backend.PendingNonceAt(ctx, common.HexToAddress(address))
}

// EstimateFee implements ChainClient.
//
// Strategy:
//   - priorityFee := SuggestGasTipCap (node-suggested tip)
//   - baseFee := latest block header.BaseFee
//   - maxFee := baseFee * (1 + feeBumpPct/100) + priorityFee
//   - gas := EstimateGas of the transfer call, or gasLimitFallback on error
func (c *EthClient) EstimateFee(ctx context.Context, req TransferRequest) (FeeQuote, error) {
	tip, err := c.backend.SuggestGasTipCap(ctx)
	if err != nil {
		return FeeQuote{}, fmt.Errorf("usdc: suggest tip: %w", err)
	}
	head, err := c.backend.HeaderByNumber(ctx, nil)
	if err != nil {
		return FeeQuote{}, fmt.Errorf("usdc: head: %w", err)
	}
	if head.BaseFee == nil {
		return FeeQuote{}, errors.New("usdc: chain does not support EIP-1559 (nil BaseFee)")
	}

	// maxFee = baseFee + baseFee*feeBumpPct/100 + tip
	bump := new(big.Int).Mul(head.BaseFee, big.NewInt(int64(c.feeBumpPct))) //nolint:gosec // fee bump pct fits int64
	bump.Div(bump, big.NewInt(100))
	maxFee := new(big.Int).Add(head.BaseFee, bump)
	maxFee.Add(maxFee, tip)

	// Try eth_estimateGas; fall back to the configured default if the node
	// rejects the call (e.g. due to insufficient balance at estimation time).
	fromAddr := common.HexToAddress(req.FromAddr)
	data, err := EncodeTransferCall(req.ToAddr, req.Amount)
	if err != nil {
		return FeeQuote{}, err
	}
	callMsg := ethereum.CallMsg{
		From: fromAddr,
		To:   &c.usdcAddr,
		Data: data,
	}
	gas, estErr := c.backend.EstimateGas(ctx, callMsg)
	if estErr != nil || gas == 0 {
		gas = c.gasLimitFallback
	}

	return FeeQuote{
		BaseFee:              new(big.Int).Set(head.BaseFee),
		MaxFeePerGas:         maxFee,
		MaxPriorityFeePerGas: new(big.Int).Set(tip),
		EstimatedGas:         gas,
		QuotedAt:             time.Now().UTC(),
	}, nil
}

// SendTransfer implements ChainClient.
//
// Builds an EIP-1559 tx calling USDC.transfer(to, amount), has the Wallet
// sign the canonical digest, attaches the signature via tx.WithSignature,
// and broadcasts. Any signature-length / encoding error is tagged as
// non-retryable because retrying won't change the outcome.
func (c *EthClient) SendTransfer(ctx context.Context, req TransferRequest, wallet Wallet) (SubmittedTx, error) {
	if !strings.EqualFold(req.FromAddr, wallet.Address()) {
		return SubmittedTx{}, NonRetryable(errors.New("usdc: wallet address does not match req.FromAddr"))
	}
	if req.FeeQuote.MaxFeePerGas == nil || req.FeeQuote.MaxPriorityFeePerGas == nil {
		return SubmittedTx{}, NonRetryable(errors.New("usdc: TransferRequest missing fee quote"))
	}
	if req.FeeQuote.EstimatedGas == 0 {
		return SubmittedTx{}, NonRetryable(errors.New("usdc: TransferRequest missing gas estimate"))
	}

	data, err := EncodeTransferCall(req.ToAddr, req.Amount)
	if err != nil {
		return SubmittedTx{}, NonRetryable(err)
	}

	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   c.chainID,
		Nonce:     req.Nonce,
		GasTipCap: req.FeeQuote.MaxPriorityFeePerGas,
		GasFeeCap: req.FeeQuote.MaxFeePerGas,
		Gas:       req.FeeQuote.EstimatedGas,
		To:        &c.usdcAddr,
		Value:     big.NewInt(0),
		Data:      data,
	})

	signer := types.LatestSignerForChainID(c.chainID)
	digest := signer.Hash(tx).Bytes()
	sig, err := wallet.Sign(ctx, digest)
	if err != nil {
		return SubmittedTx{}, NonRetryable(fmt.Errorf("usdc: wallet sign: %w", err))
	}
	if len(sig) != 65 {
		return SubmittedTx{}, NonRetryable(fmt.Errorf("usdc: expected 65-byte signature, got %d", len(sig)))
	}

	signedTx, err := tx.WithSignature(signer, sig)
	if err != nil {
		return SubmittedTx{}, NonRetryable(fmt.Errorf("usdc: attach signature: %w", err))
	}

	if err := c.backend.SendTransaction(ctx, signedTx); err != nil {
		if ctx.Err() != nil {
			return SubmittedTx{}, NonRetryable(fmt.Errorf("usdc: send tx: %w", ctx.Err()))
		}
		return SubmittedTx{}, fmt.Errorf("usdc: send tx: %w", err)
	}

	return SubmittedTx{
		TxHash:      signedTx.Hash().Hex(),
		ChainID:     c.chain.ID,
		Nonce:       req.Nonce,
		SubmittedAt: time.Now().UTC(),
	}, nil
}

// GetReceipt implements ChainClient.
//
// If the receipt is not yet available, returns (pending, ErrReceiptNotFound).
// If available but below confirmation depth, returns (pending, nil).
// If mined with status=0, returns (failed, nil).
// If mined and past confirmation depth, returns (success, nil).
func (c *EthClient) GetReceipt(ctx context.Context, txHash string, minConfirmations uint64) (TxReceipt, error) {
	hash := common.HexToHash(txHash)
	rc, err := c.backend.TransactionReceipt(ctx, hash)
	if err != nil {
		// go-ethereum returns a specific NotFound error; we treat any
		// error as "not yet mined" and let the caller's retry loop drive.
		return TxReceipt{
			TxHash:    txHash,
			Status:    TxStatusPending,
			CheckedAt: time.Now().UTC(),
		}, ErrReceiptNotFound
	}
	if rc == nil {
		return TxReceipt{
			TxHash:    txHash,
			Status:    TxStatusPending,
			CheckedAt: time.Now().UTC(),
		}, ErrReceiptNotFound
	}

	head, err := c.backend.BlockNumber(ctx)
	if err != nil {
		return TxReceipt{}, fmt.Errorf("usdc: block number: %w", err)
	}

	conf := uint64(0)
	if head >= rc.BlockNumber.Uint64() {
		conf = head - rc.BlockNumber.Uint64() + 1
	}

	status := TxStatusPending
	if conf >= minConfirmations {
		if rc.Status == types.ReceiptStatusSuccessful {
			status = TxStatusSuccess
		} else {
			status = TxStatusFailed
		}
	}

	effective := big.NewInt(0)
	if rc.EffectiveGasPrice != nil {
		effective = new(big.Int).Set(rc.EffectiveGasPrice)
	}

	return TxReceipt{
		TxHash:        txHash,
		Status:        status,
		BlockNumber:   rc.BlockNumber.Uint64(),
		BlockHash:     rc.BlockHash.Hex(),
		GasUsed:       rc.GasUsed,
		EffectiveGas:  effective,
		Confirmations: conf,
		CheckedAt:     time.Now().UTC(),
	}, nil
}
