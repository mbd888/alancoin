package ledger

import (
	"context"
	"math/big"
	"sync"
	"time"

	"github.com/mbd888/alancoin/internal/usdc"
)

// MemoryStore is an in-memory ledger store for demo/development mode.
type MemoryStore struct {
	balances        map[string]*Balance
	entries         []*Entry
	deposits        map[string]bool
	refunds         map[string]bool   // "addr:ref" -> already refunded
	holdCreditDraws map[string]string // "addr:ref" -> credit drawn amount during Hold
	mu              sync.RWMutex
}

// NewMemoryStore creates a new in-memory ledger store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		balances:        make(map[string]*Balance),
		entries:         make([]*Entry, 0),
		deposits:        make(map[string]bool),
		refunds:         make(map[string]bool),
		holdCreditDraws: make(map[string]string),
	}
}

func (m *MemoryStore) GetBalance(ctx context.Context, agentAddr string) (*Balance, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if bal, ok := m.balances[agentAddr]; ok {
		cp := *bal
		return &cp, nil
	}
	return &Balance{
		AgentAddr:   agentAddr,
		Available:   "0",
		Pending:     "0",
		Escrowed:    "0",
		CreditLimit: "0",
		CreditUsed:  "0",
		TotalIn:     "0",
		TotalOut:    "0",
		UpdatedAt:   time.Now(),
	}, nil
}

func (m *MemoryStore) Credit(ctx context.Context, agentAddr, amount, txHash, description string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	bal, ok := m.balances[agentAddr]
	if !ok {
		bal = &Balance{
			AgentAddr:   agentAddr,
			Available:   "0",
			Pending:     "0",
			Escrowed:    "0",
			CreditLimit: "0",
			CreditUsed:  "0",
			TotalIn:     "0",
			TotalOut:    "0",
		}
		m.balances[agentAddr] = bal
	}

	total, _ := usdc.Parse(bal.TotalIn)
	add, _ := usdc.Parse(amount)
	total.Add(total, add)
	bal.TotalIn = usdc.Format(total)

	// Auto-repay credit if there's outstanding usage
	creditUsed, _ := usdc.Parse(bal.CreditUsed)
	if creditUsed.Sign() > 0 {
		repayment := new(big.Int).Set(add)
		if repayment.Cmp(creditUsed) > 0 {
			repayment.Set(creditUsed)
		}
		creditUsed.Sub(creditUsed, repayment)
		add.Sub(add, repayment)
		bal.CreditUsed = usdc.Format(creditUsed)

		m.entries = append(m.entries, &Entry{
			ID:          "entry_credit_repay_" + txHash,
			AgentAddr:   agentAddr,
			Type:        "credit_repay",
			Amount:      usdc.Format(repayment),
			TxHash:      txHash,
			Description: "auto_repay_from_deposit",
			CreatedAt:   time.Now(),
		})
	}

	// Credit remainder to available
	avail, _ := usdc.Parse(bal.Available)
	avail.Add(avail, add)
	bal.Available = usdc.Format(avail)
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

	avail, _ := usdc.Parse(bal.Available)
	totalOut, _ := usdc.Parse(bal.TotalOut)
	sub, _ := usdc.Parse(amount)

	if avail.Cmp(sub) >= 0 {
		// Normal debit from available
		avail.Sub(avail, sub)
		bal.Available = usdc.Format(avail)
	} else {
		// Credit-aware: debit all available, draw gap from credit
		creditLimit, _ := usdc.Parse(bal.CreditLimit)
		creditUsed, _ := usdc.Parse(bal.CreditUsed)
		creditAvailable := new(big.Int).Sub(creditLimit, creditUsed)
		gap := new(big.Int).Sub(sub, avail)

		if creditAvailable.Cmp(gap) < 0 {
			return ErrInsufficientBalance
		}

		creditUsed.Add(creditUsed, gap)
		avail.SetInt64(0) // All available consumed when drawing credit
		bal.Available = usdc.Format(avail)
		bal.CreditUsed = usdc.Format(creditUsed)

		m.entries = append(m.entries, &Entry{
			ID:          "entry_credit_draw_" + reference,
			AgentAddr:   agentAddr,
			Type:        "credit_draw",
			Amount:      usdc.Format(gap),
			Reference:   reference,
			Description: "credit_draw_for_spend",
			CreatedAt:   time.Now(),
		})
	}

	totalOut.Add(totalOut, sub)
	bal.TotalOut = usdc.Format(totalOut)
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

	avail, _ := usdc.Parse(bal.Available)
	totalOut, _ := usdc.Parse(bal.TotalOut)
	sub, _ := usdc.Parse(amount)

	if avail.Cmp(sub) < 0 {
		return ErrInsufficientBalance
	}

	avail.Sub(avail, sub)
	totalOut.Add(totalOut, sub)

	bal.Available = usdc.Format(avail)
	bal.TotalOut = usdc.Format(totalOut)
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

	// Idempotency: prevent duplicate refunds for the same reference
	refundKey := agentAddr + ":" + reference
	if m.refunds[refundKey] {
		return ErrDuplicateRefund
	}

	bal, ok := m.balances[agentAddr]
	if !ok {
		return ErrAgentNotFound
	}

	avail, _ := usdc.Parse(bal.Available)
	totalOut, _ := usdc.Parse(bal.TotalOut)
	add, _ := usdc.Parse(amount)

	// Cap totalOut reduction to prevent negative values
	if totalOut.Cmp(add) < 0 {
		add.Set(totalOut)
	}

	avail.Add(avail, add)
	totalOut.Sub(totalOut, add)

	bal.Available = usdc.Format(avail)
	bal.TotalOut = usdc.Format(totalOut)
	bal.UpdatedAt = time.Now()

	m.refunds[refundKey] = true

	m.entries = append(m.entries, &Entry{
		ID:          "entry_refund_" + reference,
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

	avail, _ := usdc.Parse(bal.Available)
	pend, _ := usdc.Parse(bal.Pending)
	sub, _ := usdc.Parse(amount)

	if avail.Cmp(sub) >= 0 {
		// Normal hold from available
		avail.Sub(avail, sub)
		bal.Available = usdc.Format(avail)
	} else {
		// Credit-aware: hold available + draw gap from credit
		creditLimit, _ := usdc.Parse(bal.CreditLimit)
		creditUsed, _ := usdc.Parse(bal.CreditUsed)
		creditAvailable := new(big.Int).Sub(creditLimit, creditUsed)
		gap := new(big.Int).Sub(sub, avail)

		if creditAvailable.Cmp(gap) < 0 {
			return ErrInsufficientBalance
		}

		creditUsed.Add(creditUsed, gap)
		avail.SetInt64(0) // All available consumed when drawing credit
		bal.Available = usdc.Format(avail)
		bal.CreditUsed = usdc.Format(creditUsed)

		// Track credit draw so ReleaseHold can reverse it
		m.holdCreditDraws[agentAddr+":"+reference] = usdc.Format(gap)

		m.entries = append(m.entries, &Entry{
			ID:          "entry_credit_draw_" + reference,
			AgentAddr:   agentAddr,
			Type:        "credit_draw",
			Amount:      usdc.Format(gap),
			Reference:   reference,
			Description: "credit_draw_for_hold",
			CreatedAt:   time.Now(),
		})
	}

	pend.Add(pend, sub)
	bal.Pending = usdc.Format(pend)
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

	pend, _ := usdc.Parse(bal.Pending)
	totalOut, _ := usdc.Parse(bal.TotalOut)
	sub, _ := usdc.Parse(amount)

	if pend.Cmp(sub) < 0 {
		return ErrInsufficientBalance
	}

	pend.Sub(pend, sub)
	totalOut.Add(totalOut, sub)

	bal.Pending = usdc.Format(pend)
	bal.TotalOut = usdc.Format(totalOut)
	bal.UpdatedAt = time.Now()

	// Clean up credit draw tracking (credit stays drawn — this is a confirmed spend)
	delete(m.holdCreditDraws, agentAddr+":"+reference)

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

	avail, _ := usdc.Parse(bal.Available)
	pend, _ := usdc.Parse(bal.Pending)
	holdAmount, _ := usdc.Parse(amount)

	if pend.Cmp(holdAmount) < 0 {
		return ErrInsufficientBalance
	}

	// Determine how much to return to available vs reverse from credit
	returnToAvail := new(big.Int).Set(holdAmount)

	key := agentAddr + ":" + reference
	if creditDrawStr, found := m.holdCreditDraws[key]; found {
		creditDraw, _ := usdc.Parse(creditDrawStr)
		creditUsed, _ := usdc.Parse(bal.CreditUsed)
		creditUsed.Sub(creditUsed, creditDraw)
		bal.CreditUsed = usdc.Format(creditUsed)

		// Only the non-credit portion returns to available
		returnToAvail.Sub(returnToAvail, creditDraw)
		delete(m.holdCreditDraws, key)

		m.entries = append(m.entries, &Entry{
			ID:          "entry_credit_reverse_" + reference,
			AgentAddr:   agentAddr,
			Type:        "credit_reverse",
			Amount:      creditDrawStr,
			Reference:   reference,
			Description: "credit_draw_reversed_on_release",
			CreatedAt:   time.Now(),
		})
	}

	avail.Add(avail, returnToAvail)
	pend.Sub(pend, holdAmount)

	bal.Available = usdc.Format(avail)
	bal.Pending = usdc.Format(pend)
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

	avail, _ := usdc.Parse(bal.Available)
	escrow, _ := usdc.Parse(bal.Escrowed)
	sub, _ := usdc.Parse(amount)

	if avail.Cmp(sub) < 0 {
		return ErrInsufficientBalance
	}

	avail.Sub(avail, sub)
	escrow.Add(escrow, sub)

	bal.Available = usdc.Format(avail)
	bal.Escrowed = usdc.Format(escrow)
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

	escrow, _ := usdc.Parse(buyerBal.Escrowed)
	totalOut, _ := usdc.Parse(buyerBal.TotalOut)
	sub, _ := usdc.Parse(amount)

	if escrow.Cmp(sub) < 0 {
		return ErrInsufficientBalance
	}

	escrow.Sub(escrow, sub)
	totalOut.Add(totalOut, sub)
	buyerBal.Escrowed = usdc.Format(escrow)
	buyerBal.TotalOut = usdc.Format(totalOut)
	buyerBal.UpdatedAt = time.Now()

	// Credit seller
	sellerBal, ok := m.balances[sellerAddr]
	if !ok {
		sellerBal = &Balance{
			AgentAddr:   sellerAddr,
			Available:   "0",
			Pending:     "0",
			Escrowed:    "0",
			CreditLimit: "0",
			CreditUsed:  "0",
			TotalIn:     "0",
			TotalOut:    "0",
		}
		m.balances[sellerAddr] = sellerBal
	}

	sellerAvail, _ := usdc.Parse(sellerBal.Available)
	sellerTotalIn, _ := usdc.Parse(sellerBal.TotalIn)
	sellerAvail.Add(sellerAvail, sub)
	sellerTotalIn.Add(sellerTotalIn, sub)
	sellerBal.Available = usdc.Format(sellerAvail)
	sellerBal.TotalIn = usdc.Format(sellerTotalIn)
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

	avail, _ := usdc.Parse(bal.Available)
	escrow, _ := usdc.Parse(bal.Escrowed)
	sub, _ := usdc.Parse(amount)

	if escrow.Cmp(sub) < 0 {
		return ErrInsufficientBalance
	}

	escrow.Sub(escrow, sub)
	avail.Add(avail, sub)

	bal.Available = usdc.Format(avail)
	bal.Escrowed = usdc.Format(escrow)
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

func (m *MemoryStore) SetCreditLimit(ctx context.Context, agentAddr, limit string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	bal, ok := m.balances[agentAddr]
	if !ok {
		bal = &Balance{
			AgentAddr:   agentAddr,
			Available:   "0",
			Pending:     "0",
			Escrowed:    "0",
			CreditLimit: "0",
			CreditUsed:  "0",
			TotalIn:     "0",
			TotalOut:    "0",
		}
		m.balances[agentAddr] = bal
	}

	bal.CreditLimit = limit
	bal.UpdatedAt = time.Now()

	m.entries = append(m.entries, &Entry{
		ID:          "entry_credit_limit_set",
		AgentAddr:   agentAddr,
		Type:        "credit_limit_set",
		Amount:      limit,
		Description: "credit_limit_set",
		CreatedAt:   time.Now(),
	})

	return nil
}

func (m *MemoryStore) UseCredit(ctx context.Context, agentAddr, amount string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	bal, ok := m.balances[agentAddr]
	if !ok {
		return ErrAgentNotFound
	}

	creditLimit, _ := usdc.Parse(bal.CreditLimit)
	creditUsed, _ := usdc.Parse(bal.CreditUsed)
	add, _ := usdc.Parse(amount)

	newUsed := new(big.Int).Add(creditUsed, add)
	if newUsed.Cmp(creditLimit) > 0 {
		return ErrInsufficientBalance
	}

	bal.CreditUsed = usdc.Format(newUsed)
	bal.UpdatedAt = time.Now()

	m.entries = append(m.entries, &Entry{
		ID:          "entry_credit_draw",
		AgentAddr:   agentAddr,
		Type:        "credit_draw",
		Amount:      amount,
		Description: "credit_draw",
		CreatedAt:   time.Now(),
	})

	return nil
}

func (m *MemoryStore) RepayCredit(ctx context.Context, agentAddr, amount string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	bal, ok := m.balances[agentAddr]
	if !ok {
		return ErrAgentNotFound
	}

	creditUsed, _ := usdc.Parse(bal.CreditUsed)
	sub, _ := usdc.Parse(amount)

	if creditUsed.Cmp(sub) < 0 {
		// Can only repay what's owed
		sub.Set(creditUsed)
	}

	creditUsed.Sub(creditUsed, sub)
	bal.CreditUsed = usdc.Format(creditUsed)
	bal.UpdatedAt = time.Now()

	m.entries = append(m.entries, &Entry{
		ID:          "entry_credit_repay",
		AgentAddr:   agentAddr,
		Type:        "credit_repay",
		Amount:      usdc.Format(sub),
		Description: "credit_repay",
		CreatedAt:   time.Now(),
	})

	return nil
}

func (m *MemoryStore) GetCreditInfo(ctx context.Context, agentAddr string) (string, string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	bal, ok := m.balances[agentAddr]
	if !ok {
		return "0", "0", nil
	}
	return bal.CreditLimit, bal.CreditUsed, nil
}
