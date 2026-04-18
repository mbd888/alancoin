package usdc

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
)

// Chain configures a single L1/L2 the payout path can operate on.
// USDCContract and Decimals are the two bits of protocol-specific data the
// payout builder needs; everything else (chain ID, RPC URL, name) is
// operational metadata for logging and tx assembly.
type Chain struct {
	ID            int64  // EIP-155 chain ID
	Name          string // e.g. "base-mainnet", "base-sepolia"
	RPCURL        string // HTTP or WSS endpoint
	USDCContract  string // 0x-prefixed, lowercase
	CCTPMessenger string // optional, 0x-prefixed; empty disables CCTP
	ExplorerURL   string // optional; used only in log messages
}

// Validate checks that the Chain has the fields a ChainClient needs.
// Callers build Chain structs from config and should call this once at
// startup, not on every request.
func (c Chain) Validate() error {
	if c.ID == 0 {
		return errors.New("usdc: chain ID is required")
	}
	if c.RPCURL == "" {
		return errors.New("usdc: RPC URL is required")
	}
	if !isHexAddress(c.USDCContract) {
		return fmt.Errorf("usdc: invalid USDC contract address %q", c.USDCContract)
	}
	if c.CCTPMessenger != "" && !isHexAddress(c.CCTPMessenger) {
		return fmt.Errorf("usdc: invalid CCTP messenger address %q", c.CCTPMessenger)
	}
	return nil
}

// FeeQuote is the set of EIP-1559 fee parameters for a single tx.
// MaxFeePerGas is the absolute ceiling the sender is willing to pay per gas.
// MaxPriorityFeePerGas is the tip paid to the validator. BaseFee is the
// block's base fee at quote time, included so callers can reason about
// future bumps.
type FeeQuote struct {
	BaseFee              *big.Int
	MaxFeePerGas         *big.Int
	MaxPriorityFeePerGas *big.Int
	EstimatedGas         uint64
	QuotedAt             time.Time
}

// TxStatus is the post-inclusion state of an on-chain transaction.
type TxStatus string

const (
	TxStatusPending TxStatus = "pending" // submitted but not mined, or mined but below confirmation depth
	TxStatusSuccess TxStatus = "success" // mined with status=1 and past confirmation depth
	TxStatusFailed  TxStatus = "failed"  // mined with status=0 (reverted)
	TxStatusDropped TxStatus = "dropped" // not found after a grace period (nonce gap, replaced, or reorged out)
)

// TxReceipt is what the ChainClient returns once a tx is past the
// configured confirmation depth. BlockNumber == 0 means the tx has not
// been mined yet; in that case Status is TxStatusPending.
type TxReceipt struct {
	TxHash        string
	Status        TxStatus
	BlockNumber   uint64
	BlockHash     string
	GasUsed       uint64
	EffectiveGas  *big.Int
	Confirmations uint64
	CheckedAt     time.Time
}

// TransferRequest is the high-level request shape the payout service hands
// to the ChainClient. Amount is in the smallest USDC unit (6-decimal).
// ClientRef is a caller-supplied idempotency key that the ChainClient
// implementation may use to dedupe retries at its own layer.
type TransferRequest struct {
	ChainID   int64
	FromAddr  string // must match the Wallet's address; provided for sanity
	ToAddr    string
	Amount    *big.Int
	ClientRef string
	Nonce     uint64
	FeeQuote  FeeQuote
}

// SubmittedTx is the result of a successful SendTransfer call.
// The tx hash is deterministic over (chain, from, nonce, payload, fees)
// so callers can persist it alongside ClientRef for reconciliation.
type SubmittedTx struct {
	TxHash      string
	ChainID     int64
	Nonce       uint64
	SubmittedAt time.Time
}

// ChainClient abstracts the on-chain operations the payout service needs.
// Real implementations wrap go-ethereum's ethclient.Client; the mock in
// mock_client.go uses an in-memory simulator.
//
// All methods must be safe for concurrent use from multiple goroutines.
type ChainClient interface {
	// ChainID returns the EIP-155 chain ID. Used to sanity-check the client
	// matches the configured Chain before sending.
	ChainID() int64

	// PendingNonce returns the next nonce for the given address, taking
	// into account txs still in the mempool.
	PendingNonce(ctx context.Context, address string) (uint64, error)

	// EstimateFee returns a fresh EIP-1559 quote for a USDC transfer of the
	// given size. Callers should re-quote on reorg or stuck-tx recovery
	// rather than reusing an old quote.
	EstimateFee(ctx context.Context, req TransferRequest) (FeeQuote, error)

	// SendTransfer submits a signed USDC transfer to the chain. The caller
	// has already filled req.Nonce and req.FeeQuote; the implementation
	// handles ABI encoding, signing, and broadcast.
	SendTransfer(ctx context.Context, req TransferRequest, wallet Wallet) (SubmittedTx, error)

	// GetReceipt returns the current on-chain state of a tx hash.
	// Returns TxStatusPending when the tx is known but below minConfirmations.
	// Returns TxStatusDropped when the tx has been absent for longer than
	// the implementation's drop-detection threshold.
	GetReceipt(ctx context.Context, txHash string, minConfirmations uint64) (TxReceipt, error)
}

// isHexAddress is a minimal 0x-prefixed 20-byte hex check.
// Avoids importing go-ethereum from this file so the types compile in
// environments without cgo/keccak deps.
func isHexAddress(s string) bool {
	if !strings.HasPrefix(s, "0x") && !strings.HasPrefix(s, "0X") {
		return false
	}
	if len(s) != 42 {
		return false
	}
	for _, r := range s[2:] {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		case r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}
