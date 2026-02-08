// Package watcher monitors the blockchain for deposits to the platform.
//
// When USDC is sent to the platform address, it automatically credits
// the sender's agent balance.
package watcher

import (
	"context"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
)

// ERC20 Transfer event signature
var transferEventSig = common.HexToHash("0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef")

// BalanceCreditor credits agent balances
type BalanceCreditor interface {
	Deposit(ctx context.Context, agentAddr, amount, txHash string) error
}

// AgentChecker verifies if an address is a registered agent
type AgentChecker interface {
	IsAgent(ctx context.Context, address string) bool
}

// Config for the deposit watcher
type Config struct {
	RPCURL          string
	USDCContract    common.Address
	PlatformAddress common.Address
	PollInterval    time.Duration
	StartBlock      uint64 // 0 = latest
}

// DefaultConfig returns sensible defaults
func DefaultConfig() Config {
	return Config{
		PollInterval: 15 * time.Second,
		StartBlock:   0,
	}
}

// Watcher monitors for incoming USDC deposits
type Watcher struct {
	client   *ethclient.Client
	config   Config
	creditor BalanceCreditor
	checker  AgentChecker
	logger   *slog.Logger

	// Track processed transactions
	processed map[string]bool
	mu        sync.Mutex

	// Last processed block
	lastBlock uint64

	// Shutdown
	stop chan struct{}
	done chan struct{}
}

// New creates a new deposit watcher
func New(cfg Config, creditor BalanceCreditor, checker AgentChecker, logger *slog.Logger) (*Watcher, error) {
	client, err := ethclient.Dial(cfg.RPCURL)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to RPC: %w", err)
	}

	return &Watcher{
		client:    client,
		config:    cfg,
		creditor:  creditor,
		checker:   checker,
		logger:    logger,
		processed: make(map[string]bool),
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
	}, nil
}

// Start begins watching for deposits
func (w *Watcher) Start(ctx context.Context) error {
	// Get starting block
	if w.config.StartBlock == 0 {
		block, err := w.client.BlockNumber(ctx)
		if err != nil {
			return fmt.Errorf("failed to get block number: %w", err)
		}
		w.lastBlock = block
	} else {
		w.lastBlock = w.config.StartBlock
	}

	w.logger.Info("deposit watcher started",
		"platform", w.config.PlatformAddress.Hex(),
		"usdc", w.config.USDCContract.Hex(),
		"startBlock", w.lastBlock,
	)

	go w.pollLoop(ctx)
	return nil
}

// Stop stops the watcher
func (w *Watcher) Stop() {
	close(w.stop)
	<-w.done
}

func (w *Watcher) pollLoop(ctx context.Context) {
	defer close(w.done)

	ticker := time.NewTicker(w.config.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stop:
			return
		case <-ticker.C:
			if err := w.checkForDeposits(ctx); err != nil {
				w.logger.Error("deposit check failed", "error", err)
			}
		}
	}
}

func (w *Watcher) checkForDeposits(ctx context.Context) error {
	// Get current block
	currentBlock, err := w.client.BlockNumber(ctx)
	if err != nil {
		return fmt.Errorf("failed to get block number: %w", err)
	}

	// Nothing new
	if currentBlock <= w.lastBlock {
		return nil
	}

	// Query for Transfer events to our platform address
	query := ethereum.FilterQuery{
		FromBlock: big.NewInt(int64(w.lastBlock + 1)),
		ToBlock:   big.NewInt(int64(currentBlock)),
		Addresses: []common.Address{w.config.USDCContract},
		Topics: [][]common.Hash{
			{transferEventSig}, // Transfer event
			nil,                // Any from address
			{common.BytesToHash(w.config.PlatformAddress.Bytes())}, // To platform address
		},
	}

	logs, err := w.client.FilterLogs(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to filter logs: %w", err)
	}

	for _, vLog := range logs {
		if err := w.processTransfer(ctx, vLog); err != nil {
			w.logger.Error("failed to process transfer", "tx", vLog.TxHash.Hex(), "error", err)
		}
	}

	w.lastBlock = currentBlock
	return nil
}

func (w *Watcher) processTransfer(ctx context.Context, vLog types.Log) error {
	txHash := vLog.TxHash.Hex()

	// Skip if already processed
	w.mu.Lock()
	if w.processed[txHash] {
		w.mu.Unlock()
		return nil
	}
	// Mark as in-progress to prevent concurrent duplicate processing.
	// If processing fails, we remove it so the next poll can retry.
	w.processed[txHash] = true
	w.mu.Unlock()

	// On failure, unmark so the transfer is retried on the next poll cycle.
	var succeeded bool
	defer func() {
		if !succeeded {
			w.mu.Lock()
			delete(w.processed, txHash)
			w.mu.Unlock()
		}
	}()

	// Parse the Transfer event
	// Topics[1] = from address (indexed)
	// Topics[2] = to address (indexed)
	// Data = amount
	if len(vLog.Topics) < 3 {
		return fmt.Errorf("invalid transfer event")
	}

	from := common.HexToAddress(vLog.Topics[1].Hex())
	amount := new(big.Int).SetBytes(vLog.Data)

	// Check if sender is a registered agent
	fromAddr := strings.ToLower(from.Hex())
	if w.checker != nil && !w.checker.IsAgent(ctx, fromAddr) {
		w.logger.Info("deposit from non-agent, skipping",
			"from", fromAddr,
			"amount", formatUSDC(amount),
			"tx", txHash,
		)
		return nil
	}

	// Credit the agent's balance
	amountStr := formatUSDC(amount)
	if err := w.creditor.Deposit(ctx, fromAddr, amountStr, txHash); err != nil {
		return fmt.Errorf("failed to credit balance: %w", err)
	}

	w.logger.Info("deposit credited",
		"agent", fromAddr,
		"amount", amountStr,
		"tx", txHash,
	)

	succeeded = true
	return nil
}

// formatUSDC converts raw amount to decimal string (6 decimals)
func formatUSDC(amount *big.Int) string {
	if amount == nil {
		return "0"
	}
	s := amount.String()
	for len(s) < 7 {
		s = "0" + s
	}
	decimal := len(s) - 6
	return s[:decimal] + "." + s[decimal:]
}
