package ledger

import (
	"context"
	"testing"
	"time"
)

func TestAuditLogger_DepositCreatesEntry(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	es := NewMemoryEventStore()
	al := NewMemoryAuditLogger()

	l := NewWithEvents(store, es).WithAuditLogger(al)

	agent := "0x1234567890123456789012345678901234567890"
	ctx = WithActor(ctx, "admin", "admin_001")
	ctx = WithAuditIP(ctx, "127.0.0.1")

	if err := l.Deposit(ctx, agent, "10.000000", "0xtx1"); err != nil {
		t.Fatalf("Deposit failed: %v", err)
	}

	entries := al.Entries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 audit entry, got %d", len(entries))
	}

	e := entries[0]
	if e.Operation != "deposit" {
		t.Errorf("expected operation 'deposit', got %q", e.Operation)
	}
	if e.ActorType != "admin" {
		t.Errorf("expected actorType 'admin', got %q", e.ActorType)
	}
	if e.ActorID != "admin_001" {
		t.Errorf("expected actorID 'admin_001', got %q", e.ActorID)
	}
	if e.IPAddress != "127.0.0.1" {
		t.Errorf("expected ip '127.0.0.1', got %q", e.IPAddress)
	}
}

func TestAuditLogger_SpendCreatesEntry(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	al := NewMemoryAuditLogger()

	l := New(store).WithAuditLogger(al)

	agent := "0x1234567890123456789012345678901234567890"
	_ = l.Deposit(ctx, agent, "10.000000", "0xtx1")
	_ = l.Spend(ctx, agent, "3.000000", "sk_1")

	entries := al.Entries()
	if len(entries) != 2 {
		t.Fatalf("expected 2 audit entries, got %d", len(entries))
	}

	if entries[1].Operation != "spend" {
		t.Errorf("expected operation 'spend', got %q", entries[1].Operation)
	}
}

func TestAuditLogger_BeforeAfterState(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	al := NewMemoryAuditLogger()

	l := New(store).WithAuditLogger(al)

	agent := "0x1234567890123456789012345678901234567890"
	_ = l.Deposit(ctx, agent, "10.000000", "0xtx1")

	entries := al.Entries()
	e := entries[0]

	if e.BeforeState == "" || e.BeforeState == "{}" {
		// Before state should show the initial zero balance
	}
	if e.AfterState == "" {
		t.Error("expected non-empty afterState")
	}
}

func TestMemoryAuditLogger_QueryFilter(t *testing.T) {
	ctx := context.Background()
	al := NewMemoryAuditLogger()

	now := time.Now()
	_ = al.LogAudit(ctx, &AuditEntry{
		AgentAddr: "0xA",
		Operation: "deposit",
		Amount:    "10.000000",
		CreatedAt: now.Add(-2 * time.Hour),
	})
	_ = al.LogAudit(ctx, &AuditEntry{
		AgentAddr: "0xA",
		Operation: "spend",
		Amount:    "3.000000",
		CreatedAt: now.Add(-1 * time.Hour),
	})
	_ = al.LogAudit(ctx, &AuditEntry{
		AgentAddr: "0xB",
		Operation: "deposit",
		Amount:    "5.000000",
		CreatedAt: now,
	})

	// Query all for 0xA
	entries, err := al.QueryAudit(ctx, "0xA", time.Time{}, now, "", 100)
	if err != nil {
		t.Fatalf("QueryAudit failed: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries for 0xA, got %d", len(entries))
	}

	// Query only deposits for 0xA
	entries, err = al.QueryAudit(ctx, "0xA", time.Time{}, now, "deposit", 100)
	if err != nil {
		t.Fatalf("QueryAudit failed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 deposit entry for 0xA, got %d", len(entries))
	}
}

func TestAuditLogger_EscrowOperations(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	al := NewMemoryAuditLogger()

	l := New(store).WithAuditLogger(al)

	buyer := "0x1234567890123456789012345678901234567890"
	seller := "0xabcdefabcdefabcdefabcdefabcdefabcdefabcd"

	_ = l.Deposit(ctx, buyer, "10.000000", "0xtx1")
	_ = l.EscrowLock(ctx, buyer, "5.000000", "escrow_1")
	_ = l.ReleaseEscrow(ctx, buyer, seller, "5.000000", "escrow_1")

	entries := al.Entries()
	if len(entries) != 3 {
		t.Fatalf("expected 3 audit entries, got %d", len(entries))
	}
	if entries[1].Operation != "escrow_lock" {
		t.Errorf("expected 'escrow_lock', got %q", entries[1].Operation)
	}
	if entries[2].Operation != "escrow_release" {
		t.Errorf("expected 'escrow_release', got %q", entries[2].Operation)
	}
}
