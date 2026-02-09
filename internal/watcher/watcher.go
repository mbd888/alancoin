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
	"github.com/mbd888/alancoin/internal/usdc"
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

	// Track processed transactions (txHash:logIndex → true)
	processed map[string]bool
	mu        sync.Mutex

	// Last processed block (protected by mu)
	lastBlock uint64

	// Reorg safety: how many blocks back to re-scan
	reorgDepth uint64

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
		client:     client,
		config:     cfg,
		creditor:   creditor,
		checker:    checker,
		logger:     logger,
		processed:  make(map[string]bool),
		reorgDepth: 12, // Re-scan last 12 blocks to handle reorgs
		stop:       make(chan struct{}),
		done:       make(chan struct{}),
	}, nil
}

// Start begins watching for deposits
func (w *Watcher) Start(ctx context.Context) error {
	// Get starting block
	w.mu.Lock()
	if w.config.StartBlock == 0 {
		block, err := w.client.BlockNumber(ctx)
		if err != nil {
			w.mu.Unlock()
			return fmt.Errorf("failed to get block number: %w", err)
		}
		w.lastBlock = block
	} else {
		w.lastBlock = w.config.StartBlock
	}
	startBlock := w.lastBlock
	w.mu.Unlock()

	w.logger.Info("deposit watcher started",
		"platform", w.config.PlatformAddress.Hex(),
		"usdc", w.config.USDCContract.Hex(),
		"startBlock", startBlock,
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

	w.mu.Lock()
	// Re-scan from reorgDepth blocks back to catch reorgs.
	// The dedup in processTransfer prevents double-crediting.
	fromBlock := w.lastBlock + 1
	if w.reorgDepth > 0 && fromBlock > w.reorgDepth {
		safeFrom := w.lastBlock - w.reorgDepth + 1
		if safeFrom < fromBlock {
			fromBlock = safeFrom
		}
	}
	w.mu.Unlock()

	// Nothing new
	if currentBlock < fromBlock {
		return nil
	}

	// Query for Transfer events to our platform address
	query := ethereum.FilterQuery{
		FromBlock: new(big.Int).SetUint64(fromBlock),
		ToBlock:   new(big.Int).SetUint64(currentBlock),
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
		// Skip removed logs (reorg indicator)
		if vLog.Removed {
			w.logger.Warn("reorged transfer event detected, skipping",
				"tx", vLog.TxHash.Hex(),
				"block", vLog.BlockNumber,
			)
			continue
		}
		if err := w.processTransfer(ctx, vLog); err != nil {
			w.logger.Error("failed to process transfer", "tx", vLog.TxHash.Hex(), "error", err)
		}
	}

	w.mu.Lock()
	w.lastBlock = currentBlock
	w.mu.Unlock()
	return nil
}

func (w *Watcher) processTransfer(ctx context.Context, vLog types.Log) error {
	txHash := vLog.TxHash.Hex()
	// Use txHash:logIndex as dedup key to handle multi-transfer transactions
	dedupKey := fmt.Sprintf("%s:%d", txHash, vLog.Index)

	// Skip if already processed
	w.mu.Lock()
	if w.processed[dedupKey] {
		w.mu.Unlock()
		return nil
	}
	// Mark as in-progress to prevent concurrent duplicate processing.
	// If processing fails, we remove it so the next poll can retry.
	w.processed[dedupKey] = true
	w.mu.Unlock()

	// On failure, unmark so the transfer is retried on the next poll cycle.
	var succeeded bool
	defer func() {
		if !succeeded {
			w.mu.Lock()
			delete(w.processed, dedupKey)
			w.mu.Unlock()
		}
	}()

	// Parse the Transfer event
	// Topics[1] = from address (indexed)
	// Topics[2] = to address (indexed)
	// Data = amount (uint256, must be exactly 32 bytes)
	if len(vLog.Topics) < 3 {
		return fmt.Errorf("invalid transfer event: expected 3 topics, got %d", len(vLog.Topics))
	}
	if len(vLog.Data) != 32 {
		return fmt.Errorf("invalid transfer event data: expected 32 bytes, got %d", len(vLog.Data))
	}

	from := common.HexToAddress(vLog.Topics[1].Hex())
	amount := new(big.Int).SetBytes(vLog.Data)

	// Check if sender is a registered agent
	fromAddr := strings.ToLower(from.Hex())
	if w.checker != nil && !w.checker.IsAgent(ctx, fromAddr) {
		w.logger.Info("deposit from non-agent, skipping",
			"from", fromAddr,
			"amount", usdc.Format(amount),
			"tx", txHash,
		)
		return nil
	}

	// Credit the agent's balance
	amountStr := usdc.Format(amount)
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
