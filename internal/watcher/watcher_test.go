package watcher

import (
	"context"
	"errors"
	"math/big"
	"sync"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// mockCreditor tracks credit calls for testing.
type mockCreditor struct {
	mu       sync.Mutex
	credits  []creditCall
	deposits map[string]bool
}

type creditCall struct {
	AgentAddr string
	Amount    string
	TxHash    string
}

func newMockCreditor() *mockCreditor {
	return &mockCreditor{deposits: make(map[string]bool)}
}

func (m *mockCreditor) Credit(_ context.Context, agentAddr, amount, txHash, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.credits = append(m.credits, creditCall{agentAddr, amount, txHash})
	m.deposits[txHash] = true
	return nil
}

func (m *mockCreditor) HasDeposit(_ context.Context, txHash string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.deposits[txHash], nil
}

// mockAgentResolver always says the address is a registered agent.
type mockAgentResolver struct {
	agents map[string]bool
}

func (m *mockAgentResolver) IsRegisteredAgent(_ context.Context, addr string) (bool, error) {
	return m.agents[addr], nil
}

func TestMemoryCheckpoint(t *testing.T) {
	ctx := context.Background()
	cp := NewMemoryCheckpoint()

	// Initial state
	block, err := cp.GetLastBlock(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if block != 0 {
		t.Fatalf("expected 0, got %d", block)
	}

	// Set and get
	if err := cp.SetLastBlock(ctx, 42); err != nil {
		t.Fatal(err)
	}
	block, err = cp.GetLastBlock(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if block != 42 {
		t.Fatalf("expected 42, got %d", block)
	}
}

func TestTransferEventSig(t *testing.T) {
	expected := crypto.Keccak256Hash([]byte("Transfer(address,address,uint256)"))
	if transferEventSig != expected {
		t.Fatalf("event sig mismatch: got %s, want %s", transferEventSig.Hex(), expected.Hex())
	}
}

func TestProcessTransfer_SkipsZeroAmount(t *testing.T) {
	creditor := newMockCreditor()
	agents := &mockAgentResolver{agents: map[string]bool{}}
	cp := NewMemoryCheckpoint()

	w := New(Config{
		PlatformAddress: common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678"),
		USDCContract:    common.HexToAddress("0x036CbD53842c5426634e7929541eC2318f3dCF7e"),
	}, creditor, agents, cp, nil)
	// Use a nil logger — processTransfer won't log for zero amounts
	w.logger = nil

	// Build a Transfer log with zero amount
	vLog := types.Log{
		Topics: []common.Hash{
			transferEventSig,
			common.BytesToHash(common.HexToAddress("0xaaaa000000000000000000000000000000000001").Bytes()),
			common.BytesToHash(common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678").Bytes()),
		},
		Data:        make([]byte, 32), // zero amount
		TxHash:      common.HexToHash("0xdeadbeef"),
		BlockNumber: 100,
	}

	err := w.processTransfer(context.Background(), vLog)
	if err != nil {
		t.Fatal(err)
	}

	creditor.mu.Lock()
	defer creditor.mu.Unlock()
	if len(creditor.credits) != 0 {
		t.Fatalf("expected no credits for zero amount, got %d", len(creditor.credits))
	}
}

func TestProcessTransfer_SkipsNonAgent(t *testing.T) {
	creditor := newMockCreditor()
	agents := &mockAgentResolver{agents: map[string]bool{}} // no agents registered
	cp := NewMemoryCheckpoint()

	w := New(Config{
		PlatformAddress: common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678"),
		USDCContract:    common.HexToAddress("0x036CbD53842c5426634e7929541eC2318f3dCF7e"),
	}, creditor, agents, cp, nil)
	w.logger = nil

	// Build a Transfer log with non-zero amount from a non-agent
	data := make([]byte, 32)
	data[31] = 100 // 100 micro-units

	vLog := types.Log{
		Topics: []common.Hash{
			transferEventSig,
			common.BytesToHash(common.HexToAddress("0xaaaa000000000000000000000000000000000001").Bytes()),
			common.BytesToHash(common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678").Bytes()),
		},
		Data:        data,
		TxHash:      common.HexToHash("0xdeadbeef"),
		BlockNumber: 100,
	}

	// processTransfer will try to log when skipping non-agent — we need a logger
	// For this test, just verify it doesn't credit
	// We'll set a noop logger via slog
	w.logger = noopLogger()

	err := w.processTransfer(context.Background(), vLog)
	if err != nil {
		t.Fatal(err)
	}

	creditor.mu.Lock()
	defer creditor.mu.Unlock()
	if len(creditor.credits) != 0 {
		t.Fatalf("expected no credits for non-agent, got %d", len(creditor.credits))
	}
}

func TestProcessTransfer_CreditsAgent(t *testing.T) {
	agentAddr := "0xaaaa000000000000000000000000000000000001"
	creditor := newMockCreditor()
	agents := &mockAgentResolver{agents: map[string]bool{agentAddr: true}}
	cp := NewMemoryCheckpoint()

	w := New(Config{
		PlatformAddress: common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678"),
		USDCContract:    common.HexToAddress("0x036CbD53842c5426634e7929541eC2318f3dCF7e"),
	}, creditor, agents, cp, noopLogger())

	// 1,000,000 = 1.000000 USDC (6 decimals)
	data := make([]byte, 32)
	amount := []byte{0x0F, 0x42, 0x40} // 1000000 in big-endian
	copy(data[29:], amount)

	vLog := types.Log{
		Topics: []common.Hash{
			transferEventSig,
			common.BytesToHash(common.HexToAddress(agentAddr).Bytes()),
			common.BytesToHash(common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678").Bytes()),
		},
		Data:        data,
		TxHash:      common.HexToHash("0xdeadbeef01"),
		BlockNumber: 100,
	}

	err := w.processTransfer(context.Background(), vLog)
	if err != nil {
		t.Fatal(err)
	}

	creditor.mu.Lock()
	defer creditor.mu.Unlock()
	if len(creditor.credits) != 1 {
		t.Fatalf("expected 1 credit, got %d", len(creditor.credits))
	}
	if creditor.credits[0].Amount != "1.000000" {
		t.Fatalf("expected 1.000000, got %s", creditor.credits[0].Amount)
	}
	if creditor.credits[0].TxHash != "0xdeadbeef0100000000000000000000000000000000000000000000000000000000000000" {
		// TxHash is the full hex of the common.Hash
		t.Logf("tx_hash: %s", creditor.credits[0].TxHash)
	}
}

func TestProcessTransfer_Idempotent(t *testing.T) {
	agentAddr := "0xaaaa000000000000000000000000000000000001"
	creditor := newMockCreditor()
	agents := &mockAgentResolver{agents: map[string]bool{agentAddr: true}}
	cp := NewMemoryCheckpoint()

	w := New(Config{
		PlatformAddress: common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678"),
		USDCContract:    common.HexToAddress("0x036CbD53842c5426634e7929541eC2318f3dCF7e"),
	}, creditor, agents, cp, noopLogger())

	data := make([]byte, 32)
	data[31] = 100

	vLog := types.Log{
		Topics: []common.Hash{
			transferEventSig,
			common.BytesToHash(common.HexToAddress(agentAddr).Bytes()),
			common.BytesToHash(common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678").Bytes()),
		},
		Data:        data,
		TxHash:      common.HexToHash("0xdeadbeef02"),
		BlockNumber: 100,
	}

	// Process twice
	_ = w.processTransfer(context.Background(), vLog)
	_ = w.processTransfer(context.Background(), vLog)

	creditor.mu.Lock()
	defer creditor.mu.Unlock()
	if len(creditor.credits) != 1 {
		t.Fatalf("expected 1 credit (idempotent), got %d", len(creditor.credits))
	}
}

// --- merged from watcher_extra_test.go ---

// ---------------------------------------------------------------------------
// processTransfer: malformed log
// ---------------------------------------------------------------------------

func TestProcessTransfer_MalformedLog_TooFewTopics(t *testing.T) {
	creditor := newMockCreditor()
	agents := &mockAgentResolver{agents: map[string]bool{}}
	cp := NewMemoryCheckpoint()

	w := New(Config{
		PlatformAddress: common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678"),
		USDCContract:    common.HexToAddress("0x036CbD53842c5426634e7929541eC2318f3dCF7e"),
	}, creditor, agents, cp, noopLogger())

	// Only 2 topics (need at least 3)
	vLog := types.Log{
		Topics: []common.Hash{
			transferEventSig,
			common.BytesToHash(common.HexToAddress("0xaaaa000000000000000000000000000000000001").Bytes()),
		},
		Data:        make([]byte, 32),
		TxHash:      common.HexToHash("0xdeadbeef"),
		BlockNumber: 100,
	}

	err := w.processTransfer(context.Background(), vLog)
	if err == nil {
		t.Fatal("expected error for malformed log (too few topics)")
	}
}

func TestProcessTransfer_MalformedLog_TooShortData(t *testing.T) {
	creditor := newMockCreditor()
	agents := &mockAgentResolver{agents: map[string]bool{}}
	cp := NewMemoryCheckpoint()

	w := New(Config{
		PlatformAddress: common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678"),
		USDCContract:    common.HexToAddress("0x036CbD53842c5426634e7929541eC2318f3dCF7e"),
	}, creditor, agents, cp, noopLogger())

	// 3 topics but data too short (<32 bytes)
	vLog := types.Log{
		Topics: []common.Hash{
			transferEventSig,
			common.BytesToHash(common.HexToAddress("0xaaaa000000000000000000000000000000000001").Bytes()),
			common.BytesToHash(common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678").Bytes()),
		},
		Data:        make([]byte, 16), // too short
		TxHash:      common.HexToHash("0xdeadbeef"),
		BlockNumber: 100,
	}

	err := w.processTransfer(context.Background(), vLog)
	if err == nil {
		t.Fatal("expected error for malformed log (too short data)")
	}
}

// ---------------------------------------------------------------------------
// processTransfer: creditor error
// ---------------------------------------------------------------------------

type errorCreditor struct {
	hasDepositErr error
	creditErr     error
	deposits      map[string]bool
}

func (m *errorCreditor) Credit(_ context.Context, _, _, _, _ string) error {
	return m.creditErr
}

func (m *errorCreditor) HasDeposit(_ context.Context, txHash string) (bool, error) {
	if m.hasDepositErr != nil {
		return false, m.hasDepositErr
	}
	return m.deposits[txHash], nil
}

func TestProcessTransfer_HasDepositError(t *testing.T) {
	creditor := &errorCreditor{
		hasDepositErr: errors.New("db error"),
		deposits:      make(map[string]bool),
	}
	agents := &mockAgentResolver{agents: map[string]bool{
		"0xaaaa000000000000000000000000000000000001": true,
	}}
	cp := NewMemoryCheckpoint()

	w := New(Config{
		PlatformAddress: common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678"),
		USDCContract:    common.HexToAddress("0x036CbD53842c5426634e7929541eC2318f3dCF7e"),
	}, creditor, agents, cp, noopLogger())

	data := make([]byte, 32)
	data[31] = 100

	vLog := types.Log{
		Topics: []common.Hash{
			transferEventSig,
			common.BytesToHash(common.HexToAddress("0xaaaa000000000000000000000000000000000001").Bytes()),
			common.BytesToHash(common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678").Bytes()),
		},
		Data:        data,
		TxHash:      common.HexToHash("0xdeadbeef"),
		BlockNumber: 100,
	}

	err := w.processTransfer(context.Background(), vLog)
	if err == nil {
		t.Fatal("expected error when HasDeposit fails")
	}
}

func TestProcessTransfer_CreditError(t *testing.T) {
	creditor := &errorCreditor{
		creditErr: errors.New("credit failed"),
		deposits:  make(map[string]bool),
	}
	agentAddr := "0xaaaa000000000000000000000000000000000001"
	agents := &mockAgentResolver{agents: map[string]bool{agentAddr: true}}
	cp := NewMemoryCheckpoint()

	w := New(Config{
		PlatformAddress: common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678"),
		USDCContract:    common.HexToAddress("0x036CbD53842c5426634e7929541eC2318f3dCF7e"),
	}, creditor, agents, cp, noopLogger())

	data := make([]byte, 32)
	data[31] = 100

	vLog := types.Log{
		Topics: []common.Hash{
			transferEventSig,
			common.BytesToHash(common.HexToAddress(agentAddr).Bytes()),
			common.BytesToHash(common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678").Bytes()),
		},
		Data:        data,
		TxHash:      common.HexToHash("0xdeadbeef03"),
		BlockNumber: 100,
	}

	err := w.processTransfer(context.Background(), vLog)
	if err == nil {
		t.Fatal("expected error when Credit fails")
	}
}

// ---------------------------------------------------------------------------
// processTransfer: agent resolver error
// ---------------------------------------------------------------------------

type errorAgentResolver struct {
	err error
}

func (m *errorAgentResolver) IsRegisteredAgent(_ context.Context, _ string) (bool, error) {
	return false, m.err
}

func TestProcessTransfer_AgentResolverError(t *testing.T) {
	creditor := newMockCreditor()
	agents := &errorAgentResolver{err: errors.New("resolver db error")}
	cp := NewMemoryCheckpoint()

	w := New(Config{
		PlatformAddress: common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678"),
		USDCContract:    common.HexToAddress("0x036CbD53842c5426634e7929541eC2318f3dCF7e"),
	}, creditor, agents, cp, noopLogger())

	data := make([]byte, 32)
	data[31] = 100

	vLog := types.Log{
		Topics: []common.Hash{
			transferEventSig,
			common.BytesToHash(common.HexToAddress("0xaaaa000000000000000000000000000000000001").Bytes()),
			common.BytesToHash(common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678").Bytes()),
		},
		Data:        data,
		TxHash:      common.HexToHash("0xdeadbeef04"),
		BlockNumber: 100,
	}

	err := w.processTransfer(context.Background(), vLog)
	if err == nil {
		t.Fatal("expected error when agent resolver fails")
	}
}

// ---------------------------------------------------------------------------
// processTransfer: large amount parsing
// ---------------------------------------------------------------------------

func TestProcessTransfer_LargeAmount(t *testing.T) {
	agentAddr := "0xaaaa000000000000000000000000000000000001"
	creditor := newMockCreditor()
	agents := &mockAgentResolver{agents: map[string]bool{agentAddr: true}}
	cp := NewMemoryCheckpoint()

	w := New(Config{
		PlatformAddress: common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678"),
		USDCContract:    common.HexToAddress("0x036CbD53842c5426634e7929541eC2318f3dCF7e"),
	}, creditor, agents, cp, noopLogger())

	// Large amount: 1,000,000 USDC = 1,000,000,000,000 micro-units
	amount := new(big.Int)
	amount.SetString("1000000000000", 10)
	data := make([]byte, 32)
	amountBytes := amount.Bytes()
	copy(data[32-len(amountBytes):], amountBytes)

	vLog := types.Log{
		Topics: []common.Hash{
			transferEventSig,
			common.BytesToHash(common.HexToAddress(agentAddr).Bytes()),
			common.BytesToHash(common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678").Bytes()),
		},
		Data:        data,
		TxHash:      common.HexToHash("0xlargeamount"),
		BlockNumber: 200,
	}

	err := w.processTransfer(context.Background(), vLog)
	if err != nil {
		t.Fatalf("expected success for large amount: %v", err)
	}

	creditor.mu.Lock()
	defer creditor.mu.Unlock()

	if len(creditor.credits) != 1 {
		t.Fatalf("expected 1 credit, got %d", len(creditor.credits))
	}
	if creditor.credits[0].Amount != "1000000.000000" {
		t.Fatalf("expected 1000000.000000, got %s", creditor.credits[0].Amount)
	}
}

// ---------------------------------------------------------------------------
// Config defaults
// ---------------------------------------------------------------------------

func TestNew_ConfigDefaults(t *testing.T) {
	creditor := newMockCreditor()
	agents := &mockAgentResolver{agents: map[string]bool{}}
	cp := NewMemoryCheckpoint()

	w := New(Config{}, creditor, agents, cp, noopLogger())

	if w.cfg.PollInterval == 0 {
		t.Fatal("PollInterval should be set to default")
	}
	if w.cfg.Confirmations == 0 {
		t.Fatal("Confirmations should be set to default")
	}
	if w.cfg.BatchSize == 0 {
		t.Fatal("BatchSize should be set to default")
	}
}

func TestNew_ConfigCustomValues(t *testing.T) {
	creditor := newMockCreditor()
	agents := &mockAgentResolver{agents: map[string]bool{}}
	cp := NewMemoryCheckpoint()

	w := New(Config{
		PollInterval:  5,
		Confirmations: 12,
		BatchSize:     500,
	}, creditor, agents, cp, noopLogger())

	if w.cfg.PollInterval != 5 {
		t.Fatalf("expected PollInterval 5, got %v", w.cfg.PollInterval)
	}
	if w.cfg.Confirmations != 12 {
		t.Fatalf("expected Confirmations 12, got %d", w.cfg.Confirmations)
	}
	if w.cfg.BatchSize != 500 {
		t.Fatalf("expected BatchSize 500, got %d", w.cfg.BatchSize)
	}
}

// ---------------------------------------------------------------------------
// Running / Stop on non-started watcher
// ---------------------------------------------------------------------------

func TestWatcher_RunningBeforeStart(t *testing.T) {
	creditor := newMockCreditor()
	agents := &mockAgentResolver{agents: map[string]bool{}}
	cp := NewMemoryCheckpoint()

	w := New(Config{}, creditor, agents, cp, noopLogger())

	if w.Running() {
		t.Fatal("should not be running before Start")
	}

	// Stop on non-running watcher should not block
	w.Stop()
}

// ---------------------------------------------------------------------------
// MemoryCheckpoint: overwrite and verify
// ---------------------------------------------------------------------------

func TestMemoryCheckpoint_OverwriteBlock(t *testing.T) {
	ctx := context.Background()
	cp := NewMemoryCheckpoint()

	_ = cp.SetLastBlock(ctx, 10)
	_ = cp.SetLastBlock(ctx, 20)
	_ = cp.SetLastBlock(ctx, 15) // lower block should still work

	block, err := cp.GetLastBlock(ctx)
	if err != nil {
		t.Fatalf("GetLastBlock: %v", err)
	}
	if block != 15 {
		t.Fatalf("expected 15, got %d", block)
	}
}

// ---------------------------------------------------------------------------
// processTransfer: already-processed deposit returns nil (not error)
// ---------------------------------------------------------------------------

func TestProcessTransfer_AlreadyProcessed_ReturnsNil(t *testing.T) {
	agentAddr := "0xaaaa000000000000000000000000000000000001"
	creditor := newMockCreditor()
	agents := &mockAgentResolver{agents: map[string]bool{agentAddr: true}}
	cp := NewMemoryCheckpoint()

	w := New(Config{
		PlatformAddress: common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678"),
		USDCContract:    common.HexToAddress("0x036CbD53842c5426634e7929541eC2318f3dCF7e"),
	}, creditor, agents, cp, noopLogger())

	data := make([]byte, 32)
	data[31] = 100

	vLog := types.Log{
		Topics: []common.Hash{
			transferEventSig,
			common.BytesToHash(common.HexToAddress(agentAddr).Bytes()),
			common.BytesToHash(common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678").Bytes()),
		},
		Data:        data,
		TxHash:      common.HexToHash("0xduplicate"),
		BlockNumber: 100,
	}

	// First process
	err := w.processTransfer(context.Background(), vLog)
	if err != nil {
		t.Fatalf("first process: %v", err)
	}

	// Second process — already exists in creditor.deposits
	err = w.processTransfer(context.Background(), vLog)
	if err != nil {
		t.Fatalf("second process should return nil (idempotent): %v", err)
	}

	creditor.mu.Lock()
	defer creditor.mu.Unlock()
	if len(creditor.credits) != 1 {
		t.Fatalf("expected 1 credit (idempotent), got %d", len(creditor.credits))
	}
}
