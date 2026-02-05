package ledger

import (
	"context"
	"sync"
	"testing"
	"time"
)

// MemoryStore for testing
type MemoryStore struct {
	balances map[string]*Balance
	entries  []*Entry
	deposits map[string]bool
	mu       sync.RWMutex
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		balances: make(map[string]*Balance),
		entries:  make([]*Entry, 0),
		deposits: make(map[string]bool),
	}
}

func (m *MemoryStore) GetBalance(ctx context.Context, agentAddr string) (*Balance, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if bal, ok := m.balances[agentAddr]; ok {
		return bal, nil
	}
	return &Balance{
		AgentAddr: agentAddr,
		Available: "0",
		Pending:   "0",
		TotalIn:   "0",
		TotalOut:  "0",
		UpdatedAt: time.Now(),
	}, nil
}

func (m *MemoryStore) Credit(ctx context.Context, agentAddr, amount, txHash, description string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	bal, ok := m.balances[agentAddr]
	if !ok {
		bal = &Balance{
			AgentAddr: agentAddr,
			Available: "0",
			Pending:   "0",
			TotalIn:   "0",
			TotalOut:  "0",
		}
		m.balances[agentAddr] = bal
	}

	// Add to available and totalIn
	avail, _ := parseUSDC(bal.Available)
	total, _ := parseUSDC(bal.TotalIn)
	add, _ := parseUSDC(amount)

	avail.Add(avail, add)
	total.Add(total, add)

	bal.Available = formatUSDC(avail)
	bal.TotalIn = formatUSDC(total)
	bal.UpdatedAt = time.Now()

	m.entries = append(m.entries, &Entry{
		ID:          "entry_" + txHash,
		AgentAddr:   agentAddr,
		Type:        "deposit",
		Amount:      amount,
		TxHash:      txHash,
		Description: description,
		CreatedAt:   time.Now(),
	})

	m.deposits[txHash] = true

	return nil
}

func (m *MemoryStore) Debit(ctx context.Context, agentAddr, amount, reference, description string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	bal, ok := m.balances[agentAddr]
	if !ok {
		return ErrAgentNotFound
	}

	avail, _ := parseUSDC(bal.Available)
	totalOut, _ := parseUSDC(bal.TotalOut)
	sub, _ := parseUSDC(amount)

	if avail.Cmp(sub) < 0 {
		return ErrInsufficientBalance
	}

	avail.Sub(avail, sub)
	totalOut.Add(totalOut, sub)

	bal.Available = formatUSDC(avail)
	bal.TotalOut = formatUSDC(totalOut)
	bal.UpdatedAt = time.Now()

	m.entries = append(m.entries, &Entry{
		ID:          "entry_spend",
		AgentAddr:   agentAddr,
		Type:        "spend",
		Amount:      amount,
		Reference:   reference,
		Description: description,
		CreatedAt:   time.Now(),
	})

	return nil
}

func (m *MemoryStore) Withdraw(ctx context.Context, agentAddr, amount, txHash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	bal, ok := m.balances[agentAddr]
	if !ok {
		return ErrAgentNotFound
	}

	avail, _ := parseUSDC(bal.Available)
	totalOut, _ := parseUSDC(bal.TotalOut)
	sub, _ := parseUSDC(amount)

	if avail.Cmp(sub) < 0 {
		return ErrInsufficientBalance
	}

	avail.Sub(avail, sub)
	totalOut.Add(totalOut, sub)

	bal.Available = formatUSDC(avail)
	bal.TotalOut = formatUSDC(totalOut)
	bal.UpdatedAt = time.Now()

	m.entries = append(m.entries, &Entry{
		ID:          "entry_" + txHash,
		AgentAddr:   agentAddr,
		Type:        "withdrawal",
		Amount:      amount,
		TxHash:      txHash,
		Description: "withdrawal",
		CreatedAt:   time.Now(),
	})

	return nil
}

func (m *MemoryStore) Refund(ctx context.Context, agentAddr, amount, reference, description string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	bal, ok := m.balances[agentAddr]
	if !ok {
		return ErrAgentNotFound
	}

	// Credit back the amount
	avail, _ := parseUSDC(bal.Available)
	totalOut, _ := parseUSDC(bal.TotalOut)
	add, _ := parseUSDC(amount)

	avail.Add(avail, add)
	totalOut.Sub(totalOut, add)

	bal.Available = formatUSDC(avail)
	bal.TotalOut = formatUSDC(totalOut)
	bal.UpdatedAt = time.Now()

	m.entries = append(m.entries, &Entry{
		ID:          "entry_refund",
		AgentAddr:   agentAddr,
		Type:        "refund",
		Amount:      amount,
		Reference:   reference,
		Description: description,
		CreatedAt:   time.Now(),
	})

	return nil
}

func (m *MemoryStore) GetHistory(ctx context.Context, agentAddr string, limit int) ([]*Entry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*Entry
	for i := len(m.entries) - 1; i >= 0 && len(result) < limit; i-- {
		if m.entries[i].AgentAddr == agentAddr {
			result = append(result, m.entries[i])
		}
	}
	return result, nil
}

func (m *MemoryStore) HasDeposit(ctx context.Context, txHash string) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.deposits[txHash], nil
}

// Tests

func TestLedger_Deposit(t *testing.T) {
	store := NewMemoryStore()
	ledger := New(store)
	ctx := context.Background()

	agent := "0x1234567890123456789012345678901234567890"
	txHash := "0xabc123"

	// Deposit
	err := ledger.Deposit(ctx, agent, "10.00", txHash)
	if err != nil {
		t.Fatalf("Deposit failed: %v", err)
	}

	// Check balance
	bal, err := ledger.GetBalance(ctx, agent)
	if err != nil {
		t.Fatalf("GetBalance failed: %v", err)
	}

	if bal.Available != "10.000000" {
		t.Errorf("Expected available 10.000000, got %s", bal.Available)
	}
}

func TestLedger_DuplicateDeposit(t *testing.T) {
	store := NewMemoryStore()
	ledger := New(store)
	ctx := context.Background()

	agent := "0x1234567890123456789012345678901234567890"
	txHash := "0xabc123"

	// First deposit
	err := ledger.Deposit(ctx, agent, "10.00", txHash)
	if err != nil {
		t.Fatalf("First deposit failed: %v", err)
	}

	// Duplicate deposit should fail
	err = ledger.Deposit(ctx, agent, "10.00", txHash)
	if err != ErrDuplicateDeposit {
		t.Errorf("Expected ErrDuplicateDeposit, got %v", err)
	}
}

func TestLedger_Spend(t *testing.T) {
	store := NewMemoryStore()
	ledger := New(store)
	ctx := context.Background()

	agent := "0x1234567890123456789012345678901234567890"

	// Deposit first
	err := ledger.Deposit(ctx, agent, "10.00", "0xtx1")
	if err != nil {
		t.Fatalf("Deposit failed: %v", err)
	}

	// Spend
	err = ledger.Spend(ctx, agent, "3.50", "sk_123")
	if err != nil {
		t.Fatalf("Spend failed: %v", err)
	}

	// Check balance
	bal, err := ledger.GetBalance(ctx, agent)
	if err != nil {
		t.Fatalf("GetBalance failed: %v", err)
	}

	if bal.Available != "6.500000" {
		t.Errorf("Expected available 6.500000, got %s", bal.Available)
	}
}

func TestLedger_SpendInsufficientBalance(t *testing.T) {
	store := NewMemoryStore()
	ledger := New(store)
	ctx := context.Background()

	agent := "0x1234567890123456789012345678901234567890"

	// Deposit first
	err := ledger.Deposit(ctx, agent, "5.00", "0xtx1")
	if err != nil {
		t.Fatalf("Deposit failed: %v", err)
	}

	// Try to spend more than available
	err = ledger.Spend(ctx, agent, "10.00", "sk_123")
	if err != ErrInsufficientBalance {
		t.Errorf("Expected ErrInsufficientBalance, got %v", err)
	}
}

func TestLedger_CanSpend(t *testing.T) {
	store := NewMemoryStore()
	ledger := New(store)
	ctx := context.Background()

	agent := "0x1234567890123456789012345678901234567890"

	// Deposit
	ledger.Deposit(ctx, agent, "10.00", "0xtx1")

	// Can spend less than balance
	canSpend, err := ledger.CanSpend(ctx, agent, "5.00")
	if err != nil {
		t.Fatalf("CanSpend failed: %v", err)
	}
	if !canSpend {
		t.Error("Expected CanSpend to return true")
	}

	// Cannot spend more than balance
	canSpend, err = ledger.CanSpend(ctx, agent, "15.00")
	if err != nil {
		t.Fatalf("CanSpend failed: %v", err)
	}
	if canSpend {
		t.Error("Expected CanSpend to return false")
	}
}

func TestLedger_Withdraw(t *testing.T) {
	store := NewMemoryStore()
	ledger := New(store)
	ctx := context.Background()

	agent := "0x1234567890123456789012345678901234567890"

	// Deposit
	ledger.Deposit(ctx, agent, "10.00", "0xtx1")

	// Withdraw
	err := ledger.Withdraw(ctx, agent, "4.00", "0xwithdraw1")
	if err != nil {
		t.Fatalf("Withdraw failed: %v", err)
	}

	// Check balance
	bal, err := ledger.GetBalance(ctx, agent)
	if err != nil {
		t.Fatalf("GetBalance failed: %v", err)
	}

	if bal.Available != "6.000000" {
		t.Errorf("Expected available 6.000000, got %s", bal.Available)
	}
}

func TestLedger_GetHistory(t *testing.T) {
	store := NewMemoryStore()
	ledger := New(store)
	ctx := context.Background()

	agent := "0x1234567890123456789012345678901234567890"

	// Multiple operations
	ledger.Deposit(ctx, agent, "10.00", "0xtx1")
	ledger.Spend(ctx, agent, "2.00", "sk_1")
	ledger.Spend(ctx, agent, "1.00", "sk_2")

	// Get history
	entries, err := ledger.GetHistory(ctx, agent, 10)
	if err != nil {
		t.Fatalf("GetHistory failed: %v", err)
	}

	if len(entries) != 3 {
		t.Errorf("Expected 3 entries, got %d", len(entries))
	}
}

func TestParseUSDC(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"1.00", "1000000"},
		{"0.50", "500000"},
		{"10", "10000000"},
		{"0.000001", "1"},
		{"100.123456", "100123456"},
	}

	for _, tt := range tests {
		result, ok := parseUSDC(tt.input)
		if !ok {
			t.Errorf("parseUSDC(%s) failed", tt.input)
			continue
		}
		if result.String() != tt.expected {
			t.Errorf("parseUSDC(%s) = %s, want %s", tt.input, result.String(), tt.expected)
		}
	}
}
