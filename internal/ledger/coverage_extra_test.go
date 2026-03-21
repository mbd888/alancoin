package ledger

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Ledger: NewWithEvents and EventStoreRef
// ---------------------------------------------------------------------------

func TestLedger_NewWithEvents(t *testing.T) {
	store := NewMemoryStore()
	es := NewMemoryEventStore()
	l := NewWithEvents(store, es)

	if l.EventStoreRef() == nil {
		t.Fatal("expected non-nil event store")
	}
	if l.StoreRef() != store {
		t.Fatal("expected matching store")
	}
}

// ---------------------------------------------------------------------------
// Ledger: WithAuditLogger
// ---------------------------------------------------------------------------

func TestLedger_WithAuditLogger(t *testing.T) {
	store := NewMemoryStore()
	l := New(store)

	al := NewMemoryAuditLogger()
	l2 := l.WithAuditLogger(al)

	if l2 != l {
		t.Fatal("expected same ledger instance")
	}
}

// ---------------------------------------------------------------------------
// Ledger: appendEvent with nil event store is no-op
// ---------------------------------------------------------------------------

func TestLedger_AppendEvent_NilEventStore(t *testing.T) {
	store := NewMemoryStore()
	l := New(store) // no event store

	// Should not panic
	l.appendEvent(context.Background(), "0xagent", "deposit", "10.00", "ref", "")
}

// ---------------------------------------------------------------------------
// Ledger: logAudit with nil audit logger is no-op
// ---------------------------------------------------------------------------

func TestLedger_LogAudit_NilLogger(t *testing.T) {
	store := NewMemoryStore()
	l := New(store) // no audit logger

	// Should not panic
	l.logAudit(context.Background(), "0xagent", "deposit", "10.00", "ref", nil, nil)
}

// ---------------------------------------------------------------------------
// Ledger: Deposit with events and audit
// ---------------------------------------------------------------------------

func TestLedger_Deposit_WithEventsAndAudit(t *testing.T) {
	store := NewMemoryStore()
	es := NewMemoryEventStore()
	al := NewMemoryAuditLogger()
	l := NewWithEvents(store, es).WithAuditLogger(al)
	ctx := context.Background()

	agent := "0x1234567890123456789012345678901234567890"
	err := l.Deposit(ctx, agent, "50.00", "tx1")
	if err != nil {
		t.Fatalf("deposit: %v", err)
	}

	// Check event store
	events, _ := es.GetEvents(ctx, "0x1234567890123456789012345678901234567890", time.Time{})
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].EventType != "deposit" {
		t.Fatalf("expected deposit event, got %s", events[0].EventType)
	}

	// Check audit log
	entries := al.Entries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 audit entry, got %d", len(entries))
	}
}

// ---------------------------------------------------------------------------
// Ledger: Spend with events
// ---------------------------------------------------------------------------

func TestLedger_Spend_WithEvents(t *testing.T) {
	store := NewMemoryStore()
	es := NewMemoryEventStore()
	l := NewWithEvents(store, es)
	ctx := context.Background()

	agent := "0x1234567890123456789012345678901234567890"
	l.Deposit(ctx, agent, "100.00", "tx1")
	err := l.Spend(ctx, agent, "10.00", "ref1")
	if err != nil {
		t.Fatalf("spend: %v", err)
	}

	events, _ := es.GetEvents(ctx, agent, time.Time{})
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
}

// ---------------------------------------------------------------------------
// Ledger: Transfer with events and audit
// ---------------------------------------------------------------------------

func TestLedger_Transfer_WithEventsAndAudit(t *testing.T) {
	store := NewMemoryStore()
	es := NewMemoryEventStore()
	al := NewMemoryAuditLogger()
	l := NewWithEvents(store, es).WithAuditLogger(al)
	ctx := context.Background()

	from := "0xfrom000000000000000000000000000000000000"
	to := "0xto00000000000000000000000000000000000000"

	l.Deposit(ctx, from, "100.00", "tx1")
	err := l.Transfer(ctx, from, to, "25.00", "ref1")
	if err != nil {
		t.Fatalf("transfer: %v", err)
	}

	// Should have 2 transfer events (out + in) plus 1 deposit event
	fromEvents, _ := es.GetEvents(ctx, from, time.Time{})
	if len(fromEvents) < 2 {
		t.Fatalf("expected at least 2 events for sender, got %d", len(fromEvents))
	}

	// Audit should have entries for both sides
	entries := al.Entries()
	if len(entries) < 3 { // deposit + transfer_out + transfer_in
		t.Fatalf("expected at least 3 audit entries, got %d", len(entries))
	}
}

// ---------------------------------------------------------------------------
// Ledger: Withdraw with events
// ---------------------------------------------------------------------------

func TestLedger_Withdraw_WithEvents(t *testing.T) {
	store := NewMemoryStore()
	es := NewMemoryEventStore()
	l := NewWithEvents(store, es)
	ctx := context.Background()

	agent := "0x1234567890123456789012345678901234567890"
	l.Deposit(ctx, agent, "100.00", "tx1")
	err := l.Withdraw(ctx, agent, "20.00", "0xtx_withdraw")
	if err != nil {
		t.Fatalf("withdraw: %v", err)
	}

	events, _ := es.GetEvents(ctx, agent, time.Time{})
	found := false
	for _, e := range events {
		if e.EventType == "withdrawal" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected withdrawal event")
	}
}

// ---------------------------------------------------------------------------
// Ledger: Refund with events
// ---------------------------------------------------------------------------

func TestLedger_Refund_WithEvents(t *testing.T) {
	store := NewMemoryStore()
	es := NewMemoryEventStore()
	l := NewWithEvents(store, es)
	ctx := context.Background()

	agent := "0x1234567890123456789012345678901234567890"
	l.Deposit(ctx, agent, "100.00", "tx1")
	l.Spend(ctx, agent, "20.00", "ref1")
	err := l.Refund(ctx, agent, "5.00", "ref_refund")
	if err != nil {
		t.Fatalf("refund: %v", err)
	}

	events, _ := es.GetEvents(ctx, agent, time.Time{})
	found := false
	for _, e := range events {
		if e.EventType == "refund" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected refund event")
	}
}

// ---------------------------------------------------------------------------
// Ledger: Hold, ConfirmHold, ReleaseHold with events
// ---------------------------------------------------------------------------

func TestLedger_Hold_WithEvents(t *testing.T) {
	store := NewMemoryStore()
	es := NewMemoryEventStore()
	l := NewWithEvents(store, es)
	ctx := context.Background()

	agent := "0x1234567890123456789012345678901234567890"
	l.Deposit(ctx, agent, "100.00", "tx1")

	err := l.Hold(ctx, agent, "10.00", "hold_ref")
	if err != nil {
		t.Fatalf("hold: %v", err)
	}

	err = l.ConfirmHold(ctx, agent, "10.00", "hold_ref")
	if err != nil {
		t.Fatalf("confirm hold: %v", err)
	}

	events, _ := es.GetEvents(ctx, agent, time.Time{})
	types := make(map[string]bool)
	for _, e := range events {
		types[e.EventType] = true
	}
	if !types["hold"] {
		t.Fatal("expected hold event")
	}
	if !types["confirm_hold"] {
		t.Fatal("expected confirm_hold event")
	}
}

func TestLedger_ReleaseHold_WithEvents(t *testing.T) {
	store := NewMemoryStore()
	es := NewMemoryEventStore()
	l := NewWithEvents(store, es)
	ctx := context.Background()

	agent := "0x1234567890123456789012345678901234567890"
	l.Deposit(ctx, agent, "100.00", "tx1")
	l.Hold(ctx, agent, "10.00", "hold_ref")

	err := l.ReleaseHold(ctx, agent, "10.00", "hold_ref")
	if err != nil {
		t.Fatalf("release hold: %v", err)
	}

	events, _ := es.GetEvents(ctx, agent, time.Time{})
	found := false
	for _, e := range events {
		if e.EventType == "release_hold" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected release_hold event")
	}
}

// ---------------------------------------------------------------------------
// Ledger: EscrowLock, ReleaseEscrow, RefundEscrow with events
// ---------------------------------------------------------------------------

func TestLedger_EscrowOps_WithEvents(t *testing.T) {
	store := NewMemoryStore()
	es := NewMemoryEventStore()
	l := NewWithEvents(store, es)
	ctx := context.Background()

	buyer := "0xbuyer0000000000000000000000000000000000"
	seller := "0xseller000000000000000000000000000000000"
	l.Deposit(ctx, buyer, "100.00", "tx1")

	l.EscrowLock(ctx, buyer, "30.00", "escrow_ref")

	err := l.ReleaseEscrow(ctx, buyer, seller, "30.00", "escrow_ref")
	if err != nil {
		t.Fatalf("release escrow: %v", err)
	}

	events, _ := es.GetEvents(ctx, buyer, time.Time{})
	types := make(map[string]bool)
	for _, e := range events {
		types[e.EventType] = true
	}
	if !types["escrow_lock"] {
		t.Fatal("expected escrow_lock event")
	}
	if !types["escrow_release"] {
		t.Fatal("expected escrow_release event")
	}
}

func TestLedger_RefundEscrow_WithEvents(t *testing.T) {
	store := NewMemoryStore()
	es := NewMemoryEventStore()
	l := NewWithEvents(store, es)
	ctx := context.Background()

	buyer := "0xbuyer0000000000000000000000000000000000"
	l.Deposit(ctx, buyer, "100.00", "tx1")
	l.EscrowLock(ctx, buyer, "20.00", "escrow_ref")

	err := l.RefundEscrow(ctx, buyer, "20.00", "escrow_ref")
	if err != nil {
		t.Fatalf("refund escrow: %v", err)
	}

	events, _ := es.GetEvents(ctx, buyer, time.Time{})
	found := false
	for _, e := range events {
		if e.EventType == "escrow_refund" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected escrow_refund event")
	}
}

// ---------------------------------------------------------------------------
// Ledger: SettleHold with events
// ---------------------------------------------------------------------------

func TestLedger_SettleHold_WithEvents(t *testing.T) {
	store := NewMemoryStore()
	es := NewMemoryEventStore()
	l := NewWithEvents(store, es)
	ctx := context.Background()

	buyer := "0xbuyer0000000000000000000000000000000000"
	seller := "0xseller000000000000000000000000000000000"
	l.Deposit(ctx, buyer, "100.00", "tx1")
	l.Hold(ctx, buyer, "20.00", "hold_ref")

	err := l.SettleHold(ctx, buyer, seller, "20.00", "hold_ref")
	if err != nil {
		t.Fatalf("settle hold: %v", err)
	}

	buyerEvents, _ := es.GetEvents(ctx, buyer, time.Time{})
	found := false
	for _, e := range buyerEvents {
		if e.EventType == "settle_hold_out" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected settle_hold_out event for buyer")
	}

	sellerEvents, _ := es.GetEvents(ctx, seller, time.Time{})
	found = false
	for _, e := range sellerEvents {
		if e.EventType == "settle_hold_in" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected settle_hold_in event for seller")
	}
}

// ---------------------------------------------------------------------------
// Ledger: SettleHoldWithFee with events
// ---------------------------------------------------------------------------

func TestLedger_SettleHoldWithFee_WithEvents(t *testing.T) {
	store := NewMemoryStore()
	es := NewMemoryEventStore()
	l := NewWithEvents(store, es)
	ctx := context.Background()

	buyer := "0xbuyer0000000000000000000000000000000000"
	seller := "0xseller000000000000000000000000000000000"
	platform := "0xplatform000000000000000000000000000000"
	l.Deposit(ctx, buyer, "100.00", "tx1")
	l.Hold(ctx, buyer, "20.00", "hold_ref")

	err := l.SettleHoldWithFee(ctx, buyer, seller, "18.00", platform, "2.00", "hold_ref")
	if err != nil {
		t.Fatalf("settle hold with fee: %v", err)
	}

	platformEvents, _ := es.GetEvents(ctx, platform, time.Time{})
	found := false
	for _, e := range platformEvents {
		if e.EventType == "fee_in" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected fee_in event for platform")
	}
}

// ---------------------------------------------------------------------------
// Ledger: PartialEscrowSettle with events
// ---------------------------------------------------------------------------

func TestLedger_PartialEscrowSettle_WithEvents(t *testing.T) {
	store := NewMemoryStore()
	es := NewMemoryEventStore()
	l := NewWithEvents(store, es)
	ctx := context.Background()

	buyer := "0xbuyer0000000000000000000000000000000000"
	seller := "0xseller000000000000000000000000000000000"
	l.Deposit(ctx, buyer, "100.00", "tx1")
	l.EscrowLock(ctx, buyer, "30.00", "escrow_ref")

	err := l.PartialEscrowSettle(ctx, buyer, seller, "20.00", "10.00", "escrow_ref")
	if err != nil {
		t.Fatalf("partial escrow settle: %v", err)
	}

	buyerEvents, _ := es.GetEvents(ctx, buyer, time.Time{})
	types := make(map[string]bool)
	for _, e := range buyerEvents {
		types[e.EventType] = true
	}
	if !types["escrow_partial_release"] {
		t.Fatal("expected escrow_partial_release event")
	}
	if !types["escrow_partial_refund"] {
		t.Fatal("expected escrow_partial_refund event")
	}
}

// ---------------------------------------------------------------------------
// Ledger: BalanceAtTime without event store
// ---------------------------------------------------------------------------

func TestLedger_BalanceAtTime_NoEventStoreConfigured(t *testing.T) {
	l := New(NewMemoryStore())
	_, err := l.BalanceAtTime(context.Background(), "0xagent", time.Now())
	if err == nil {
		t.Fatal("expected error when no event store configured")
	}
}

// ---------------------------------------------------------------------------
// Ledger: ReconcileAll without event store
// ---------------------------------------------------------------------------

func TestLedger_ReconcileAll_NoEventStoreConfigured(t *testing.T) {
	l := New(NewMemoryStore())
	_, err := l.ReconcileAll(context.Background())
	if err == nil {
		t.Fatal("expected error when no event store configured")
	}
}

// ---------------------------------------------------------------------------
// Ledger: Reverse delegates to store
// ---------------------------------------------------------------------------

func TestLedger_Reverse(t *testing.T) {
	store := NewMemoryStore()
	l := New(store)
	ctx := context.Background()

	// Deposit and spend to create an entry
	agent := "0x1234567890123456789012345678901234567890"
	l.Deposit(ctx, agent, "100.00", "tx1")
	l.Spend(ctx, agent, "10.00", "ref1")

	// Get the entry ID
	entries, _ := l.GetHistory(ctx, agent, 10)
	if len(entries) == 0 {
		t.Fatal("expected entries")
	}
	spendEntry := entries[0] // most recent first

	err := l.Reverse(ctx, spendEntry.ID, "test reason", "admin1")
	if err != nil {
		t.Fatalf("reverse: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Audit: WithActor, WithAuditIP, WithAuditRequestID
// ---------------------------------------------------------------------------

func TestAuditContextHelpers(t *testing.T) {
	ctx := context.Background()
	ctx = WithActor(ctx, "session_key", "sk_123")
	ctx = WithAuditIP(ctx, "192.168.1.1")
	ctx = WithAuditRequestID(ctx, "req_456")

	actorType, actorID, ip, requestID := actorFromCtx(ctx)
	if actorType != "session_key" {
		t.Errorf("expected session_key, got %s", actorType)
	}
	if actorID != "sk_123" {
		t.Errorf("expected sk_123, got %s", actorID)
	}
	if ip != "192.168.1.1" {
		t.Errorf("expected 192.168.1.1, got %s", ip)
	}
	if requestID != "req_456" {
		t.Errorf("expected req_456, got %s", requestID)
	}
}

// ---------------------------------------------------------------------------
// Audit: balanceSnapshot
// ---------------------------------------------------------------------------

func TestBalanceSnapshot(t *testing.T) {
	bal := &Balance{
		Available: "100.00",
		Pending:   "10.00",
		Escrowed:  "5.00",
	}
	snap := balanceSnapshot(bal)
	var m map[string]string
	json.Unmarshal([]byte(snap), &m)
	if m["available"] != "100.00" {
		t.Errorf("expected available 100.00, got %s", m["available"])
	}
}

// ---------------------------------------------------------------------------
// Audit: MemoryAuditLogger QueryAudit with operation filter
// ---------------------------------------------------------------------------

func TestMemoryAuditLogger_QueryAudit_WithOperation(t *testing.T) {
	al := NewMemoryAuditLogger()
	ctx := context.Background()

	al.LogAudit(ctx, &AuditEntry{AgentAddr: "0xa", Operation: "deposit", Amount: "10.00"})
	al.LogAudit(ctx, &AuditEntry{AgentAddr: "0xa", Operation: "spend", Amount: "5.00"})
	al.LogAudit(ctx, &AuditEntry{AgentAddr: "0xa", Operation: "deposit", Amount: "20.00"})

	entries, _ := al.QueryAudit(ctx, "0xa", time.Time{}, time.Now().Add(time.Hour), "deposit", 10)
	if len(entries) != 2 {
		t.Fatalf("expected 2 deposit entries, got %d", len(entries))
	}
}

func TestMemoryAuditLogger_QueryAudit_DifferentAgent(t *testing.T) {
	al := NewMemoryAuditLogger()
	ctx := context.Background()

	al.LogAudit(ctx, &AuditEntry{AgentAddr: "0xa", Operation: "deposit"})
	al.LogAudit(ctx, &AuditEntry{AgentAddr: "0xb", Operation: "deposit"})

	entries, _ := al.QueryAudit(ctx, "0xa", time.Time{}, time.Now().Add(time.Hour), "", 10)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry for 0xa, got %d", len(entries))
	}
}

// ---------------------------------------------------------------------------
// MemoryStore: EscrowLock and EscrowRefund
// ---------------------------------------------------------------------------

func TestMemoryStore_EscrowLock_NonexistentAgent(t *testing.T) {
	store := NewMemoryStore()
	err := store.EscrowLock(context.Background(), "nonexistent", "10.00", "ref")
	if err != ErrAgentNotFound {
		t.Fatalf("expected ErrAgentNotFound, got %v", err)
	}
}

func TestMemoryStore_EscrowRefund_NonexistentAgent(t *testing.T) {
	store := NewMemoryStore()
	err := store.RefundEscrow(context.Background(), "nonexistent", "10.00", "ref")
	if err != ErrAgentNotFound {
		t.Fatalf("expected ErrAgentNotFound, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// MemoryStore: GetHistoryPage with cursor
// ---------------------------------------------------------------------------

func TestMemoryStore_GetHistoryPage_WithCursor(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	agent := "0xagent"
	store.Credit(ctx, agent, "100.00", "tx1", "deposit")
	store.Debit(ctx, agent, "10.00", "ref1", "spend")
	store.Debit(ctx, agent, "20.00", "ref2", "spend")

	// First page — all entries
	entries, _ := store.GetHistoryPage(ctx, agent, 10, time.Time{}, "")
	if len(entries) < 2 {
		t.Fatalf("expected at least 2 entries, got %d", len(entries))
	}

	// Page with cursor — should skip entries at or after cursor
	cursorEntry := entries[0]
	entries2, _ := store.GetHistoryPage(ctx, agent, 10, cursorEntry.CreatedAt, cursorEntry.ID)
	if len(entries2) >= len(entries) {
		t.Fatal("expected fewer entries with cursor")
	}
}

// ---------------------------------------------------------------------------
// MemoryStore: SetCreditLimit, GetCreditInfo
// ---------------------------------------------------------------------------

func TestMemoryStore_SetAndGetCreditInfo(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	agent := "0xagent"
	store.Credit(ctx, agent, "10.00", "tx1", "deposit")

	err := store.SetCreditLimit(ctx, agent, "50.00")
	if err != nil {
		t.Fatalf("SetCreditLimit: %v", err)
	}

	limit, used, err := store.GetCreditInfo(ctx, agent)
	if err != nil {
		t.Fatalf("GetCreditInfo: %v", err)
	}
	// MemoryStore stores the limit string as provided
	if limit != "50.00" {
		t.Fatalf("expected limit 50.00, got %s", limit)
	}
	if used != "0" && used != "0.000000" {
		t.Fatalf("expected used 0, got %s", used)
	}
}

// ---------------------------------------------------------------------------
// MemoryStore: HasDeposit
// ---------------------------------------------------------------------------

func TestMemoryStore_HasDeposit_NotFound(t *testing.T) {
	store := NewMemoryStore()
	exists, err := store.HasDeposit(context.Background(), "nonexistent_tx")
	if err != nil {
		t.Fatalf("HasDeposit: %v", err)
	}
	if exists {
		t.Fatal("expected false for nonexistent tx")
	}
}

// ---------------------------------------------------------------------------
// MemoryEventStore: basic operations
// ---------------------------------------------------------------------------

func TestMemoryEventStore_AppendAndGet(t *testing.T) {
	es := NewMemoryEventStore()
	ctx := context.Background()

	err := es.AppendEvent(ctx, &Event{
		AgentAddr: "0xagent",
		EventType: "deposit",
		Amount:    "10.00",
		CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}

	events, _ := es.GetEvents(ctx, "0xagent", time.Time{})
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
}

func TestMemoryEventStore_GetAllAgents(t *testing.T) {
	es := NewMemoryEventStore()
	ctx := context.Background()

	es.AppendEvent(ctx, &Event{AgentAddr: "0xa", EventType: "deposit", Amount: "1.00", CreatedAt: time.Now()})
	es.AppendEvent(ctx, &Event{AgentAddr: "0xb", EventType: "deposit", Amount: "2.00", CreatedAt: time.Now()})
	es.AppendEvent(ctx, &Event{AgentAddr: "0xa", EventType: "spend", Amount: "0.50", CreatedAt: time.Now()})

	agents, _ := es.GetAllAgents(ctx)
	if len(agents) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(agents))
	}
}

// ---------------------------------------------------------------------------
// MemoryStore: Debit with credit draw
// ---------------------------------------------------------------------------

func TestMemoryStore_Debit_WithCreditDraw(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	agent := "0xagent"
	store.Credit(ctx, agent, "5.00", "tx1", "deposit")
	store.SetCreditLimit(ctx, agent, "10.00")

	// Debit $8: $5 from available + $3 from credit
	err := store.Debit(ctx, agent, "8.00", "ref1", "spend")
	if err != nil {
		t.Fatalf("debit with credit: %v", err)
	}

	bal, _ := store.GetBalance(ctx, agent)
	if bal.Available != "0.000000" {
		t.Fatalf("expected 0 available, got %s", bal.Available)
	}
	if bal.CreditUsed != "3.000000" {
		t.Fatalf("expected 3.000000 credit used, got %s", bal.CreditUsed)
	}
}

func TestMemoryStore_Debit_InsufficientWithCredit(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	agent := "0xagent"
	store.Credit(ctx, agent, "5.00", "tx1", "deposit")
	store.SetCreditLimit(ctx, agent, "2.00")

	// Debit $8: $5 from available + need $3 from credit but only $2 available
	err := store.Debit(ctx, agent, "8.00", "ref1", "spend")
	if err != ErrInsufficientBalance {
		t.Fatalf("expected ErrInsufficientBalance, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// MemoryStore: Credit auto-repays credit
// ---------------------------------------------------------------------------

func TestMemoryStore_Credit_AutoRepayCredit(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	agent := "0xagent"
	store.Credit(ctx, agent, "5.00", "tx1", "deposit")
	store.SetCreditLimit(ctx, agent, "20.00")

	// Draw credit
	store.Debit(ctx, agent, "10.00", "ref1", "spend") // 5 avail + 5 credit

	// Now deposit — should auto-repay credit
	store.Credit(ctx, agent, "3.00", "tx2", "deposit")

	bal, _ := store.GetBalance(ctx, agent)
	if bal.CreditUsed != "2.000000" {
		t.Fatalf("expected credit used 2.000000 after auto-repay, got %s", bal.CreditUsed)
	}
}

// ---------------------------------------------------------------------------
// MemoryStore: SettleHold nonexistent buyer
// ---------------------------------------------------------------------------

func TestMemoryStore_SettleHold_NonexistentBuyer(t *testing.T) {
	store := NewMemoryStore()
	err := store.SettleHold(context.Background(), "nonexistent", "0xseller", "10.00", "ref")
	if err != ErrAgentNotFound {
		t.Fatalf("expected ErrAgentNotFound, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// MemoryStore: PartialEscrowSettle nonexistent buyer
// ---------------------------------------------------------------------------

func TestMemoryStore_PartialEscrowSettle_NonexistentBuyer(t *testing.T) {
	store := NewMemoryStore()
	err := store.PartialEscrowSettle(context.Background(), "nonexistent", "0xseller", "5.00", "5.00", "ref")
	if err != ErrAgentNotFound {
		t.Fatalf("expected ErrAgentNotFound, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Ledger: CanSpend with credit
// ---------------------------------------------------------------------------

func TestLedger_CanSpend_CreditBoostsEffective(t *testing.T) {
	store := NewMemoryStore()
	l := New(store)
	ctx := context.Background()

	agent := "0x1234567890123456789012345678901234567890"
	l.Deposit(ctx, agent, "5.00", "tx1")
	store.SetCreditLimit(ctx, agent, "10.00")

	// Agent can spend up to $15 (5 available + 10 credit)
	can, err := l.CanSpend(ctx, agent, "12.00")
	if err != nil {
		t.Fatalf("CanSpend: %v", err)
	}
	if !can {
		t.Fatal("expected can spend $12 with credit")
	}

	can, _ = l.CanSpend(ctx, agent, "16.00")
	if can {
		t.Fatal("expected cannot spend $16 with only $15 effective")
	}
}

// ---------------------------------------------------------------------------
// Ledger: GetHistory default limit
// ---------------------------------------------------------------------------

func TestLedger_GetHistory_ZeroLimit(t *testing.T) {
	store := NewMemoryStore()
	l := New(store)
	ctx := context.Background()

	agent := "0x1234567890123456789012345678901234567890"
	l.Deposit(ctx, agent, "100.00", "tx1")

	// Zero limit should default to 50
	entries, err := l.GetHistory(ctx, agent, 0)
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
}

// ---------------------------------------------------------------------------
// Ledger: GetHistoryPage default limit
// ---------------------------------------------------------------------------

func TestLedger_GetHistoryPage_ZeroLimit(t *testing.T) {
	store := NewMemoryStore()
	l := New(store)
	ctx := context.Background()

	agent := "0x1234567890123456789012345678901234567890"
	l.Deposit(ctx, agent, "100.00", "tx1")

	entries, err := l.GetHistoryPage(ctx, agent, 0, time.Time{}, "")
	if err != nil {
		t.Fatalf("GetHistoryPage: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
}

// ---------------------------------------------------------------------------
// Handler: isValidAmount
// ---------------------------------------------------------------------------

func TestIsValidAmount_Coverage(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"0", true},
		{"1.00", true},
		{"100", true},
		{"0.123456", true},
		{"", false},
		{"abc", false},
		{"-1", false},
		{"1.2.3", false},
	}
	for _, tt := range tests {
		got := isValidAmount(tt.input)
		if got != tt.expected {
			t.Errorf("isValidAmount(%q) = %v, want %v", tt.input, got, tt.expected)
		}
	}
}
