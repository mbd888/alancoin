// Package watcher monitors the blockchain for USDC deposits to the platform address
// and automatically credits agent balances via the ledger.
package watcher

import (
	"context"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/mbd888/alancoin/internal/traces"
	"github.com/mbd888/alancoin/internal/usdc"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// ERC20 Transfer event signature: Transfer(address,address,uint256)
var transferEventSig = crypto.Keccak256Hash([]byte("Transfer(address,address,uint256)"))

// Creditor credits an agent's ledger balance.
type Creditor interface {
	Credit(ctx context.Context, agentAddr, amount, txHash, description string) error
	HasDeposit(ctx context.Context, txHash string) (bool, error)
}

// AgentResolver checks whether an address is a registered agent.
type AgentResolver interface {
	IsRegisteredAgent(ctx context.Context, addr string) (bool, error)
}

// CheckpointStore persists the last processed block number across restarts.
type CheckpointStore interface {
	GetLastBlock(ctx context.Context) (uint64, error)
	SetLastBlock(ctx context.Context, blockNum uint64) error
}

// Config holds watcher configuration.
type Config struct {
	RPCURL          string
	USDCContract    common.Address
	PlatformAddress common.Address
	PollInterval    time.Duration
	Confirmations   uint64 // blocks to wait for finality (default 6)
	BatchSize       uint64 // max blocks per filter query (default 1000)
	StartBlock      uint64 // fallback start block if no checkpoint exists
}

// Watcher monitors on-chain USDC Transfer events to the platform address.
type Watcher struct {
	cfg        Config
	client     *ethclient.Client
	creditor   Creditor
	agents     AgentResolver
	checkpoint CheckpointStore
	logger     *slog.Logger
	stop       chan struct{}
	running    atomic.Bool
	mu         sync.Mutex
}

// New creates a new deposit watcher.
func New(cfg Config, creditor Creditor, agents AgentResolver, checkpoint CheckpointStore, logger *slog.Logger) *Watcher {
	if cfg.PollInterval == 0 {
		cfg.PollInterval = 15 * time.Second
	}
	if cfg.Confirmations == 0 {
		cfg.Confirmations = 6
	}
	if cfg.BatchSize == 0 {
		cfg.BatchSize = 1000
	}
	return &Watcher{
		cfg:        cfg,
		creditor:   creditor,
		agents:     agents,
		checkpoint: checkpoint,
		logger:     logger,
		stop:       make(chan struct{}),
	}
}

// Start begins watching for deposits. Blocks until ctx is cancelled or Stop is called.
func (w *Watcher) Start(ctx context.Context) error {
	client, err := ethclient.DialContext(ctx, w.cfg.RPCURL)
	if err != nil {
		return fmt.Errorf("watcher: dial RPC: %w", err)
	}
	w.client = client
	defer client.Close()

	w.running.Store(true)
	defer w.running.Store(false)

	w.logger.Info("deposit watcher started",
		"usdc_contract", w.cfg.USDCContract.Hex(),
		"platform_address", w.cfg.PlatformAddress.Hex(),
		"confirmations", w.cfg.Confirmations,
		"poll_interval", w.cfg.PollInterval)

	ticker := time.NewTicker(w.cfg.PollInterval)
	defer ticker.Stop()

	// Initial poll immediately
	w.safePoll(ctx)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-w.stop:
			return nil
		case <-ticker.C:
			w.safePoll(ctx)
		}
	}
}

// Stop signals the watcher to stop.
func (w *Watcher) Stop() {
	select {
	case w.stop <- struct{}{}:
	default:
	}
}

// Running returns whether the watcher is currently running.
func (w *Watcher) Running() bool {
	return w.running.Load()
}

func (w *Watcher) safePoll(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			w.logger.Error("watcher: panic in poll", "panic", r)
		}
	}()
	if err := w.poll(ctx); err != nil {
		w.logger.Warn("watcher: poll error", "error", err)
	}
}

func (w *Watcher) poll(ctx context.Context) error {
	ctx, span := traces.StartSpan(ctx, "watcher.Poll")
	defer span.End()

	// Get current chain head
	header, err := w.client.HeaderByNumber(ctx, nil)
	if err != nil {
		span.SetStatus(codes.Error, "failed to get chain head")
		return fmt.Errorf("get head: %w", err)
	}
	headBlock := header.Number.Uint64()

	// Safe block = head - confirmations
	if headBlock < w.cfg.Confirmations {
		return nil
	}
	safeBlock := headBlock - w.cfg.Confirmations

	// Get last processed block
	lastBlock, err := w.checkpoint.GetLastBlock(ctx)
	if err != nil {
		return fmt.Errorf("get checkpoint: %w", err)
	}
	if lastBlock == 0 && w.cfg.StartBlock > 0 {
		lastBlock = w.cfg.StartBlock - 1
	}

	fromBlock := lastBlock + 1
	if fromBlock > safeBlock {
		return nil // caught up
	}

	// Process in batches
	totalProcessed := 0
	for fromBlock <= safeBlock {
		toBlock := fromBlock + w.cfg.BatchSize - 1
		if toBlock > safeBlock {
			toBlock = safeBlock
		}

		count, err := w.processBatch(ctx, fromBlock, toBlock)
		if err != nil {
			return fmt.Errorf("process batch [%d-%d]: %w", fromBlock, toBlock, err)
		}
		totalProcessed += count

		// Save checkpoint after each batch
		if err := w.checkpoint.SetLastBlock(ctx, toBlock); err != nil {
			return fmt.Errorf("save checkpoint %d: %w", toBlock, err)
		}

		fromBlock = toBlock + 1
	}

	if totalProcessed > 0 {
		w.logger.Info("watcher: processed deposits",
			"count", totalProcessed,
			"from_block", lastBlock+1,
			"to_block", safeBlock)
	}

	span.SetAttributes(attribute.Int("deposits.processed", totalProcessed))
	return nil
}

func (w *Watcher) processBatch(ctx context.Context, fromBlock, toBlock uint64) (int, error) {
	// Build filter for USDC Transfer events TO the platform address
	// Transfer(address indexed from, address indexed to, uint256 value)
	// Topic[0] = event sig, Topic[2] = to address (indexed)
	platformAddrPadded := common.BytesToHash(w.cfg.PlatformAddress.Bytes())

	query := ethereum.FilterQuery{
		FromBlock: new(big.Int).SetUint64(fromBlock),
		ToBlock:   new(big.Int).SetUint64(toBlock),
		Addresses: []common.Address{w.cfg.USDCContract},
		Topics: [][]common.Hash{
			{transferEventSig},   // event signature
			nil,                  // from: any address
			{platformAddrPadded}, // to: platform address only
		},
	}

	logs, err := w.client.FilterLogs(ctx, query)
	if err != nil {
		return 0, fmt.Errorf("filter logs: %w", err)
	}

	processed := 0
	for _, vLog := range logs {
		if err := w.processTransfer(ctx, vLog); err != nil {
			w.logger.Error("watcher: failed to process transfer",
				"tx_hash", vLog.TxHash.Hex(),
				"block", vLog.BlockNumber,
				"error", err)
			continue // skip individual failures, process rest of batch
		}
		processed++
	}

	return processed, nil
}

func (w *Watcher) processTransfer(ctx context.Context, vLog types.Log) error {
	ctx, span := traces.StartSpan(ctx, "watcher.ProcessTransfer",
		attribute.String("tx_hash", vLog.TxHash.Hex()),
		attribute.Int64("block", int64(vLog.BlockNumber)),
	)
	defer span.End()

	// Validate log structure
	if len(vLog.Topics) < 3 || len(vLog.Data) < 32 {
		return fmt.Errorf("malformed Transfer event: topics=%d data=%d", len(vLog.Topics), len(vLog.Data))
	}

	// Parse sender address from topic[1]
	fromAddr := common.BytesToAddress(vLog.Topics[1].Bytes()).Hex()
	fromAddrLower := strings.ToLower(fromAddr)

	// Parse amount from data (uint256, but USDC uses 6 decimals)
	amount := new(big.Int).SetBytes(vLog.Data[:32])
	amountStr := usdc.Format(amount)

	txHash := vLog.TxHash.Hex()

	span.SetAttributes(
		attribute.String("from", fromAddrLower),
		traces.Amount(amountStr),
	)

	// Skip zero-value transfers
	if amount.Sign() == 0 {
		return nil
	}

	// Check idempotency — skip if already processed
	exists, err := w.creditor.HasDeposit(ctx, txHash)
	if err != nil {
		return fmt.Errorf("check deposit: %w", err)
	}
	if exists {
		return nil // already credited
	}

	// Check if sender is a registered agent
	isAgent, err := w.agents.IsRegisteredAgent(ctx, fromAddrLower)
	if err != nil {
		return fmt.Errorf("check agent: %w", err)
	}
	if !isAgent {
		w.logger.Info("watcher: deposit from non-agent address, skipping",
			"from", fromAddrLower, "amount", amountStr, "tx_hash", txHash)
		return nil
	}

	// Credit the agent's balance
	desc := fmt.Sprintf("on-chain USDC deposit from %s (block %d)", fromAddrLower, vLog.BlockNumber)
	if err := w.creditor.Credit(ctx, fromAddrLower, amountStr, txHash, desc); err != nil {
		span.SetStatus(codes.Error, "credit failed")
		return fmt.Errorf("credit agent %s: %w", fromAddrLower, err)
	}

	w.logger.Info("watcher: credited deposit",
		"agent", fromAddrLower,
		"amount", amountStr,
		"tx_hash", txHash,
		"block", vLog.BlockNumber)

	span.SetStatus(codes.Ok, "deposit credited")
	return nil
}
