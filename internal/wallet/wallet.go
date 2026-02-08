// Package wallet handles all blockchain interactions for USDC transfers
package wallet

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

// -----------------------------------------------------------------------------
// Errors - typed errors for programmatic handling
// -----------------------------------------------------------------------------

var (
	ErrInvalidPrivateKey   = errors.New("wallet: invalid private key")
	ErrInvalidAddress      = errors.New("wallet: invalid address")
	ErrInvalidAmount       = errors.New("wallet: invalid amount")
	ErrInsufficientBalance = errors.New("wallet: insufficient balance")
	ErrTransactionFailed   = errors.New("wallet: transaction failed")
	ErrTimeout             = errors.New("wallet: operation timed out")
	ErrRPCConnection       = errors.New("wallet: RPC connection failed")
)

// TransferError wraps transfer failures with context
type TransferError struct {
	Op     string // Operation that failed
	TxHash string // Transaction hash if available
	Err    error  // Underlying error
}

func (e *TransferError) Error() string {
	if e.TxHash != "" {
		return fmt.Sprintf("wallet: %s failed (tx: %s): %v", e.Op, e.TxHash, e.Err)
	}
	return fmt.Sprintf("wallet: %s failed: %v", e.Op, e.Err)
}

func (e *TransferError) Unwrap() error { return e.Err }

// -----------------------------------------------------------------------------
// Interfaces - for testability and flexibility
// -----------------------------------------------------------------------------

// Transactor executes blockchain transactions
type Transactor interface {
	Transfer(ctx context.Context, to common.Address, amount *big.Int) (*TransferResult, error)
	WaitForConfirmation(ctx context.Context, txHash string, timeout time.Duration) (*TransferResult, error)
}

// BalanceChecker reads blockchain state
type BalanceChecker interface {
	BalanceOf(ctx context.Context, addr common.Address) (*big.Int, error)
}

// PaymentVerifier verifies on-chain payments
type PaymentVerifier interface {
	VerifyPayment(ctx context.Context, from string, minAmount string, txHash string) (bool, error)
}

// Wallet combines all wallet operations
type WalletService interface {
	Transactor
	BalanceChecker
	PaymentVerifier
	Address() string
	Balance(ctx context.Context) (string, error)
	WaitForConfirmationAny(ctx context.Context, txHash string, timeout time.Duration) (interface{}, error)
	Close() error
}

// EthClient abstracts go-ethereum client for testing
type EthClient interface {
	PendingNonceAt(ctx context.Context, account common.Address) (uint64, error)
	SuggestGasPrice(ctx context.Context) (*big.Int, error)
	EstimateGas(ctx context.Context, call ethereum.CallMsg) (uint64, error)
	SendTransaction(ctx context.Context, tx *types.Transaction) error
	TransactionReceipt(ctx context.Context, txHash common.Hash) (*types.Receipt, error)
	CallContract(ctx context.Context, call ethereum.CallMsg, blockNumber *big.Int) ([]byte, error)
	NetworkID(ctx context.Context) (*big.Int, error)
	Close()
}

// -----------------------------------------------------------------------------
// Constants
// -----------------------------------------------------------------------------

// ERC20 minimal ABI for transfer and balanceOf
const erc20ABI = `[
	{"constant":false,"inputs":[{"name":"to","type":"address"},{"name":"value","type":"uint256"}],"name":"transfer","outputs":[{"name":"","type":"bool"}],"type":"function"},
	{"constant":true,"inputs":[{"name":"owner","type":"address"}],"name":"balanceOf","outputs":[{"name":"","type":"uint256"}],"type":"function"},
	{"constant":true,"inputs":[],"name":"decimals","outputs":[{"name":"","type":"uint8"}],"type":"function"},
	{"anonymous":false,"inputs":[{"indexed":true,"name":"from","type":"address"},{"indexed":true,"name":"to","type":"address"},{"indexed":false,"name":"value","type":"uint256"}],"name":"Transfer","type":"event"}
]`

const (
	// USDCDecimals is the decimal precision of USDC
	USDCDecimals = 6

	// DefaultGasLimit for ERC20 transfers
	DefaultGasLimit = uint64(100000)

	// DefaultConfirmationTimeout for waiting on transactions
	DefaultConfirmationTimeout = 30 * time.Second

	// ConfirmationPollInterval between receipt checks
	ConfirmationPollInterval = 2 * time.Second
)

// -----------------------------------------------------------------------------
// Types
// -----------------------------------------------------------------------------

// Config for creating a new wallet
type Config struct {
	RPCURL       string
	PrivateKey   string // Hex string, no 0x prefix
	ChainID      int64
	USDCContract string
}

// Option configures the wallet
type Option func(*Wallet)

// WithClient sets a custom Ethereum client (useful for testing)
func WithClient(client EthClient) Option {
	return func(w *Wallet) {
		w.client = client
	}
}

// TransferResult contains details of a completed transfer
type TransferResult struct {
	TxHash      string
	From        string
	To          string
	Amount      string // Human-readable USDC amount
	AmountRaw   *big.Int
	BlockNumber uint64
	GasUsed     uint64
	Nonce       uint64
}

// Wallet handles USDC transfers on Base
type Wallet struct {
	client       EthClient
	privateKey   *ecdsa.PrivateKey
	address      common.Address
	chainID      *big.Int
	usdcContract common.Address
	usdcABI      abi.ABI
}

// Compile-time interface check
var _ WalletService = (*Wallet)(nil)

// New creates a new Wallet instance
func New(cfg Config, opts ...Option) (*Wallet, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}

	privateKey, err := crypto.HexToECDSA(strings.TrimPrefix(cfg.PrivateKey, "0x"))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidPrivateKey, err)
	}

	publicKey := privateKey.Public()
	publicKeyECDSA, ok := publicKey.(*ecdsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("%w: failed to derive public key", ErrInvalidPrivateKey)
	}

	parsedABI, err := abi.JSON(strings.NewReader(erc20ABI))
	if err != nil {
		return nil, fmt.Errorf("failed to parse ERC20 ABI: %w", err)
	}

	w := &Wallet{
		privateKey:   privateKey,
		address:      crypto.PubkeyToAddress(*publicKeyECDSA),
		chainID:      big.NewInt(cfg.ChainID),
		usdcContract: common.HexToAddress(cfg.USDCContract),
		usdcABI:      parsedABI,
	}

	// Apply options
	for _, opt := range opts {
		opt(w)
	}

	// Connect to RPC if no client provided
	if w.client == nil {
		client, err := ethclient.Dial(cfg.RPCURL)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrRPCConnection, err)
		}
		w.client = client
	}

	return w, nil
}

func validateConfig(cfg Config) error {
	if cfg.RPCURL == "" {
		return fmt.Errorf("%w: RPC URL required", ErrRPCConnection)
	}
	if cfg.PrivateKey == "" {
		return fmt.Errorf("%w: private key required", ErrInvalidPrivateKey)
	}
	// Allow both with and without 0x prefix
	key := strings.TrimPrefix(cfg.PrivateKey, "0x")
	if len(key) != 64 {
		return fmt.Errorf("%w: must be 64 hex characters", ErrInvalidPrivateKey)
	}
	if cfg.ChainID == 0 {
		return fmt.Errorf("chain ID required")
	}
	if cfg.USDCContract == "" {
		return fmt.Errorf("USDC contract address required")
	}
	return nil
}

// Address returns the wallet's address
func (w *Wallet) Address() string {
	return w.address.Hex()
}

// Balance returns the USDC balance as a human-readable string
func (w *Wallet) Balance(ctx context.Context) (string, error) {
	raw, err := w.BalanceOf(ctx, w.address)
	if err != nil {
		return "", err
	}
	return FormatUSDC(raw), nil
}

// BalanceOf returns the USDC balance of any address
func (w *Wallet) BalanceOf(ctx context.Context, addr common.Address) (*big.Int, error) {
	data, err := w.usdcABI.Pack("balanceOf", addr)
	if err != nil {
		return nil, fmt.Errorf("failed to pack balanceOf call: %w", err)
	}

	result, err := w.client.CallContract(ctx, ethereum.CallMsg{
		To:   &w.usdcContract,
		Data: data,
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to call balanceOf: %w", err)
	}

	balance := new(big.Int)
	balance.SetBytes(result)
	return balance, nil
}

// Transfer sends USDC to a recipient
// amount is in human-readable format (e.g., "1.50" for $1.50)
func (w *Wallet) Transfer(ctx context.Context, to common.Address, amount *big.Int) (*TransferResult, error) {
	// Build transfer calldata
	data, err := w.usdcABI.Pack("transfer", to, amount)
	if err != nil {
		return nil, &TransferError{Op: "pack", Err: err}
	}

	// Get nonce
	nonce, err := w.client.PendingNonceAt(ctx, w.address)
	if err != nil {
		return nil, &TransferError{Op: "nonce", Err: err}
	}

	// Get gas price
	gasPrice, err := w.client.SuggestGasPrice(ctx)
	if err != nil {
		return nil, &TransferError{Op: "gas_price", Err: err}
	}

	// Estimate gas
	gasLimit, err := w.client.EstimateGas(ctx, ethereum.CallMsg{
		From:  w.address,
		To:    &w.usdcContract,
		Value: big.NewInt(0),
		Data:  data,
	})
	if err != nil {
		// Use default if estimation fails
		gasLimit = DefaultGasLimit
	}

	// Create transaction
	tx := types.NewTransaction(nonce, w.usdcContract, big.NewInt(0), gasLimit, gasPrice, data)

	// Sign transaction
	signedTx, err := types.SignTx(tx, types.NewEIP155Signer(w.chainID), w.privateKey)
	if err != nil {
		return nil, &TransferError{Op: "sign", Err: err}
	}

	// Send transaction
	if err := w.client.SendTransaction(ctx, signedTx); err != nil {
		return nil, &TransferError{Op: "send", TxHash: signedTx.Hash().Hex(), Err: err}
	}

	return &TransferResult{
		TxHash:    signedTx.Hash().Hex(),
		From:      w.address.Hex(),
		To:        to.Hex(),
		Amount:    FormatUSDC(amount),
		AmountRaw: amount,
		Nonce:     nonce,
	}, nil
}

// WaitForConfirmation waits for a transaction to be mined
func (w *Wallet) WaitForConfirmation(ctx context.Context, txHash string, timeout time.Duration) (*TransferResult, error) {
	hash := common.HexToHash(txHash)

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(ConfirmationPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return nil, fmt.Errorf("%w: waiting for tx %s", ErrTimeout, txHash)
			}
			return nil, ctx.Err()

		case <-ticker.C:
			receipt, err := w.client.TransactionReceipt(ctx, hash)
			if err != nil {
				// Transaction not yet mined, continue waiting
				continue
			}

			if receipt.Status == 0 {
				return nil, &TransferError{
					Op:     "confirm",
					TxHash: txHash,
					Err:    ErrTransactionFailed,
				}
			}

			return &TransferResult{
				TxHash:      txHash,
				BlockNumber: receipt.BlockNumber.Uint64(),
				GasUsed:     receipt.GasUsed,
			}, nil
		}
	}
}

// VerifyPayment checks if a payment was received from a specific address
func (w *Wallet) VerifyPayment(ctx context.Context, from string, minAmount string, txHash string) (bool, error) {
	fromAddr := common.HexToAddress(from)
	minAmountRaw, err := ParseUSDC(minAmount)
	if err != nil {
		return false, fmt.Errorf("%w: %v", ErrInvalidAmount, err)
	}

	hash := common.HexToHash(txHash)

	receipt, err := w.client.TransactionReceipt(ctx, hash)
	if err != nil {
		return false, fmt.Errorf("failed to get receipt: %w", err)
	}

	if receipt.Status == 0 {
		return false, nil
	}

	// Parse Transfer events from logs
	for _, log := range receipt.Logs {
		if log.Address != w.usdcContract {
			continue
		}
		if len(log.Topics) < 3 {
			continue
		}

		eventFrom := common.HexToAddress(log.Topics[1].Hex())
		eventTo := common.HexToAddress(log.Topics[2].Hex())
		eventAmount := new(big.Int).SetBytes(log.Data)

		if eventFrom == fromAddr && eventTo == w.address && eventAmount.Cmp(minAmountRaw) >= 0 {
			return true, nil
		}
	}

	return false, nil
}

// Close closes the client connection
func (w *Wallet) Close() error {
	if w.client != nil {
		w.client.Close()
	}
	return nil
}

// WaitForConfirmationAny wraps WaitForConfirmation to satisfy interfaces that don't need the result type
// This is the Go-idiomatic way to handle interface adaptation
func (w *Wallet) WaitForConfirmationAny(ctx context.Context, txHash string, timeout time.Duration) (interface{}, error) {
	return w.WaitForConfirmation(ctx, txHash, timeout)
}

// FormatUSDC converts raw USDC amount to human-readable string
func FormatUSDC(amount *big.Int) string {
	if amount == nil {
		return "0"
	}

	// USDC has 6 decimals
	divisor := new(big.Int).Exp(big.NewInt(10), big.NewInt(USDCDecimals), nil)

	whole := new(big.Int).Div(amount, divisor)
	remainder := new(big.Int).Mod(amount, divisor)

	if remainder.Sign() == 0 {
		return whole.String()
	}

	// Format with leading zeros for decimals
	return fmt.Sprintf("%s.%06d", whole.String(), remainder.Int64())
}

// ParseUSDC converts human-readable USDC string to raw amount
func ParseUSDC(amount string) (*big.Int, error) {
	// Handle empty string
	if amount == "" {
		return nil, fmt.Errorf("empty amount")
	}

	// Split on decimal point
	parts := strings.Split(amount, ".")

	var whole, decimal string
	switch len(parts) {
	case 1:
		whole = parts[0]
		decimal = ""
	case 2:
		whole = parts[0]
		decimal = parts[1]
	default:
		return nil, fmt.Errorf("invalid amount format")
	}

	// Parse whole part
	wholeBig, ok := new(big.Int).SetString(whole, 10)
	if !ok {
		return nil, fmt.Errorf("invalid whole number")
	}

	// Reject negative amounts
	if wholeBig.Sign() < 0 {
		return nil, fmt.Errorf("negative amounts not allowed")
	}

	// Multiply whole by 10^6
	multiplier := new(big.Int).Exp(big.NewInt(10), big.NewInt(USDCDecimals), nil)
	result := new(big.Int).Mul(wholeBig, multiplier)

	// Handle decimal part
	if decimal != "" {
		// Pad or truncate to 6 digits
		if len(decimal) > USDCDecimals {
			decimal = decimal[:USDCDecimals]
		}
		for len(decimal) < USDCDecimals {
			decimal += "0"
		}

		decimalBig, ok := new(big.Int).SetString(decimal, 10)
		if !ok {
			return nil, fmt.Errorf("invalid decimal number")
		}
		result.Add(result, decimalBig)
	}

	return result, nil
}
