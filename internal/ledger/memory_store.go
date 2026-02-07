package ledger

import (
	"context"
	"sync"
	"time"
)

// MemoryStore is an in-memory ledger store for demo/development mode.
type MemoryStore struct {
	balances map[string]*Balance
	entries  []*Entry
	deposits map[string]bool
	mu       sync.RWMutex
}

// NewMemoryStore creates a new in-memory ledger store.
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
		Escrowed:  "0",
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
			Escrowed:  "0",
			TotalIn:   "0",
			TotalOut:  "0",
		}
		m.balances[agentAddr] = bal
	}

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

func (m *MemoryStore) Hold(ctx context.Context, agentAddr, amount, reference string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	bal, ok := m.balances[agentAddr]
	if !ok {
		return ErrAgentNotFound
	}

	avail, _ := parseUSDC(bal.Available)
	pend, _ := parseUSDC(bal.Pending)
	sub, _ := parseUSDC(amount)

	if avail.Cmp(sub) < 0 {
		return ErrInsufficientBalance
	}

	avail.Sub(avail, sub)
	pend.Add(pend, sub)

	bal.Available = formatUSDC(avail)
	bal.Pending = formatUSDC(pend)
	bal.UpdatedAt = time.Now()

	m.entries = append(m.entries, &Entry{
		ID:          "entry_hold_" + reference,
		AgentAddr:   agentAddr,
		Type:        "hold",
		Amount:      amount,
		Reference:   reference,
		Description: "pending_transfer",
		CreatedAt:   time.Now(),
	})

	return nil
}

func (m *MemoryStore) ConfirmHold(ctx context.Context, agentAddr, amount, reference string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	bal, ok := m.balances[agentAddr]
	if !ok {
		return ErrAgentNotFound
	}

	pend, _ := parseUSDC(bal.Pending)
	totalOut, _ := parseUSDC(bal.TotalOut)
	sub, _ := parseUSDC(amount)

	pend.Sub(pend, sub)
	totalOut.Add(totalOut, sub)

	bal.Pending = formatUSDC(pend)
	bal.TotalOut = formatUSDC(totalOut)
	bal.UpdatedAt = time.Now()

	m.entries = append(m.entries, &Entry{
		ID:          "entry_confirm_" + reference,
		AgentAddr:   agentAddr,
		Type:        "spend",
		Amount:      amount,
		Reference:   reference,
		Description: "transfer_confirmed",
		CreatedAt:   time.Now(),
	})

	return nil
}

func (m *MemoryStore) ReleaseHold(ctx context.Context, agentAddr, amount, reference string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	bal, ok := m.balances[agentAddr]
	if !ok {
		return ErrAgentNotFound
	}

	avail, _ := parseUSDC(bal.Available)
	pend, _ := parseUSDC(bal.Pending)
	sub, _ := parseUSDC(amount)

	avail.Add(avail, sub)
	pend.Sub(pend, sub)

	bal.Available = formatUSDC(avail)
	bal.Pending = formatUSDC(pend)
	bal.UpdatedAt = time.Now()

	m.entries = append(m.entries, &Entry{
		ID:          "entry_release_" + reference,
		AgentAddr:   agentAddr,
		Type:        "release",
		Amount:      amount,
		Reference:   reference,
		Description: "hold_released",
		CreatedAt:   time.Now(),
	})

	return nil
}

func (m *MemoryStore) EscrowLock(ctx context.Context, agentAddr, amount, reference string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	bal, ok := m.balances[agentAddr]
	if !ok {
		return ErrAgentNotFound
	}

	avail, _ := parseUSDC(bal.Available)
	escrow, _ := parseUSDC(bal.Escrowed)
	sub, _ := parseUSDC(amount)

	if avail.Cmp(sub) < 0 {
		return ErrInsufficientBalance
	}

	avail.Sub(avail, sub)
	escrow.Add(escrow, sub)

	bal.Available = formatUSDC(avail)
	bal.Escrowed = formatUSDC(escrow)
	bal.UpdatedAt = time.Now()

	m.entries = append(m.entries, &Entry{
		ID:          "entry_escrow_lock_" + reference,
		AgentAddr:   agentAddr,
		Type:        "escrow_lock",
		Amount:      amount,
		Reference:   reference,
		Description: "escrow_locked",
		CreatedAt:   time.Now(),
	})

	return nil
}

func (m *MemoryStore) ReleaseEscrow(ctx context.Context, buyerAddr, sellerAddr, amount, reference string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	buyerBal, ok := m.balances[buyerAddr]
	if !ok {
		return ErrAgentNotFound
	}

	escrow, _ := parseUSDC(buyerBal.Escrowed)
	totalOut, _ := parseUSDC(buyerBal.TotalOut)
	sub, _ := parseUSDC(amount)

	if escrow.Cmp(sub) < 0 {
		return ErrInsufficientBalance
	}

	escrow.Sub(escrow, sub)
	totalOut.Add(totalOut, sub)
	buyerBal.Escrowed = formatUSDC(escrow)
	buyerBal.TotalOut = formatUSDC(totalOut)
	buyerBal.UpdatedAt = time.Now()

	// Credit seller
	sellerBal, ok := m.balances[sellerAddr]
	if !ok {
		sellerBal = &Balance{
			AgentAddr: sellerAddr,
			Available: "0",
			Pending:   "0",
			Escrowed:  "0",
			TotalIn:   "0",
			TotalOut:  "0",
		}
		m.balances[sellerAddr] = sellerBal
	}

	sellerAvail, _ := parseUSDC(sellerBal.Available)
	sellerTotalIn, _ := parseUSDC(sellerBal.TotalIn)
	sellerAvail.Add(sellerAvail, sub)
	sellerTotalIn.Add(sellerTotalIn, sub)
	sellerBal.Available = formatUSDC(sellerAvail)
	sellerBal.TotalIn = formatUSDC(sellerTotalIn)
	sellerBal.UpdatedAt = time.Now()

	m.entries = append(m.entries, &Entry{
		ID:          "entry_escrow_release_" + reference,
		AgentAddr:   buyerAddr,
		Type:        "escrow_release",
		Amount:      amount,
		Reference:   reference,
		Description: "escrow_released_to_seller",
		CreatedAt:   time.Now(),
	})

	return nil
}

func (m *MemoryStore) RefundEscrow(ctx context.Context, agentAddr, amount, reference string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	bal, ok := m.balances[agentAddr]
	if !ok {
		return ErrAgentNotFound
	}

	avail, _ := parseUSDC(bal.Available)
	escrow, _ := parseUSDC(bal.Escrowed)
	sub, _ := parseUSDC(amount)

	if escrow.Cmp(sub) < 0 {
		return ErrInsufficientBalance
	}

	escrow.Sub(escrow, sub)
	avail.Add(avail, sub)

	bal.Available = formatUSDC(avail)
	bal.Escrowed = formatUSDC(escrow)
	bal.UpdatedAt = time.Now()

	m.entries = append(m.entries, &Entry{
		ID:          "entry_escrow_refund_" + reference,
		AgentAddr:   agentAddr,
		Type:        "escrow_refund",
		Amount:      amount,
		Reference:   reference,
		Description: "escrow_refunded",
		CreatedAt:   time.Now(),
	})

	return nil
}
