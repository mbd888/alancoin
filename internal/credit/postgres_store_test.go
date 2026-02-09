//go:build integration

package credit

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/lib/pq"
)

func setupTestDB(t *testing.T) (*PostgresStore, func()) {
	t.Helper()

	dbURL := os.Getenv("POSTGRES_URL")
	if dbURL == "" {
		t.Skip("POSTGRES_URL not set, skipping integration test")
	}

	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}

	if err := db.Ping(); err != nil {
		t.Fatalf("Failed to connect to database: %v", err)
	}

	store := NewPostgresStore(db)
	ctx := context.Background()

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Failed to migrate: %v", err)
	}

	cleanup := func() {
		db.ExecContext(ctx, "DELETE FROM credit_lines")
		db.Close()
	}

	return store, cleanup
}

func TestPostgresCredit_CreateAndGet(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now()

	line := &CreditLine{
		ID:              "cl_test001",
		AgentAddr:       "0xaaaa000000000000000000000000000000000001",
		CreditLimit:     "50.000000",
		CreditUsed:      "0.000000",
		InterestRate:    0.10,
		Status:          StatusActive,
		ReputationTier:  "trusted",
		ReputationScore: 72.5,
		ApprovedAt:      now,
		LastReviewAt:    now,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	if err := store.Create(ctx, line); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	got, err := store.Get(ctx, "cl_test001")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if got.AgentAddr != line.AgentAddr {
		t.Errorf("AgentAddr: got %s, want %s", got.AgentAddr, line.AgentAddr)
	}
	if got.CreditLimit != line.CreditLimit {
		t.Errorf("CreditLimit: got %s, want %s", got.CreditLimit, line.CreditLimit)
	}
	if got.Status != StatusActive {
		t.Errorf("Status: got %s, want active", got.Status)
	}
	if got.InterestRate != 0.10 {
		t.Errorf("InterestRate: got %f, want 0.10", got.InterestRate)
	}
	if got.ReputationTier != "trusted" {
		t.Errorf("ReputationTier: got %s, want trusted", got.ReputationTier)
	}
}

func TestPostgresCredit_GetByAgent(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	addr := "0xaaaa000000000000000000000000000000000002"
	now := time.Now()

	// Create two credit lines for the same agent (one closed, one active).
	closedLine := &CreditLine{
		ID: "cl_test010", AgentAddr: addr,
		CreditLimit: "10.000000", CreditUsed: "0.000000",
		InterestRate: 0.10, Status: StatusClosed,
		ReputationTier: "established", ReputationScore: 50,
		ApprovedAt: now.Add(-48 * time.Hour), CreatedAt: now.Add(-48 * time.Hour), UpdatedAt: now.Add(-48 * time.Hour),
	}
	activeLine := &CreditLine{
		ID: "cl_test011", AgentAddr: addr,
		CreditLimit: "25.000000", CreditUsed: "5.000000",
		InterestRate: 0.07, Status: StatusActive,
		ReputationTier: "trusted", ReputationScore: 65,
		ApprovedAt: now, CreatedAt: now, UpdatedAt: now,
	}

	// Insert closed first (older created_at).
	if err := store.Create(ctx, closedLine); err != nil {
		t.Fatalf("Create closed failed: %v", err)
	}
	if err := store.Create(ctx, activeLine); err != nil {
		t.Fatalf("Create active failed: %v", err)
	}

	// GetByAgent should return the most recent (activeLine).
	got, err := store.GetByAgent(ctx, addr)
	if err != nil {
		t.Fatalf("GetByAgent failed: %v", err)
	}
	if got.ID != "cl_test011" {
		t.Errorf("Expected most recent line cl_test011, got %s", got.ID)
	}
	if got.CreditLimit != "25.000000" {
		t.Errorf("CreditLimit: got %s, want 25.000000", got.CreditLimit)
	}
}

func TestPostgresCredit_NotFound(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	_, err := store.Get(ctx, "cl_nonexistent")
	if err != ErrCreditLineNotFound {
		t.Errorf("Expected ErrCreditLineNotFound, got %v", err)
	}

	_, err = store.GetByAgent(ctx, "0x0000000000000000000000000000000000000000")
	if err != ErrCreditLineNotFound {
		t.Errorf("Expected ErrCreditLineNotFound for GetByAgent, got %v", err)
	}
}

func TestPostgresCredit_DuplicatePrevention(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	addr := "0xaaaa000000000000000000000000000000000003"
	now := time.Now()

	line := &CreditLine{
		ID: "cl_test020", AgentAddr: addr,
		CreditLimit: "10.000000", CreditUsed: "0.000000",
		InterestRate: 0.10, Status: StatusActive,
		ReputationTier: "established", ReputationScore: 45,
		ApprovedAt: now, CreatedAt: now, UpdatedAt: now,
	}

	if err := store.Create(ctx, line); err != nil {
		t.Fatalf("First create failed: %v", err)
	}

	// Creating another active line for the same agent should fail.
	dup := &CreditLine{
		ID: "cl_test021", AgentAddr: addr,
		CreditLimit: "20.000000", CreditUsed: "0.000000",
		InterestRate: 0.07, Status: StatusActive,
		ReputationTier: "trusted", ReputationScore: 60,
		ApprovedAt: now, CreatedAt: now, UpdatedAt: now,
	}

	err := store.Create(ctx, dup)
	if err != ErrCreditLineExists {
		t.Errorf("Expected ErrCreditLineExists, got %v", err)
	}
}

func TestPostgresCredit_CreateAfterClosed(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	addr := "0xaaaa000000000000000000000000000000000004"
	now := time.Now()

	// Create and then close a credit line.
	line := &CreditLine{
		ID: "cl_test030", AgentAddr: addr,
		CreditLimit: "10.000000", CreditUsed: "0.000000",
		InterestRate: 0.10, Status: StatusClosed,
		ReputationTier: "established", ReputationScore: 45,
		ApprovedAt: now.Add(-24 * time.Hour), CreatedAt: now.Add(-24 * time.Hour), UpdatedAt: now,
	}
	if err := store.Create(ctx, line); err != nil {
		t.Fatalf("Create closed failed: %v", err)
	}

	// Creating a new active line for the same agent should succeed.
	newLine := &CreditLine{
		ID: "cl_test031", AgentAddr: addr,
		CreditLimit: "25.000000", CreditUsed: "0.000000",
		InterestRate: 0.07, Status: StatusActive,
		ReputationTier: "trusted", ReputationScore: 60,
		ApprovedAt: now, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.Create(ctx, newLine); err != nil {
		t.Fatalf("Create after close should succeed, got: %v", err)
	}
}

func TestPostgresCredit_Update(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now()

	line := &CreditLine{
		ID: "cl_test040", AgentAddr: "0xaaaa000000000000000000000000000000000005",
		CreditLimit: "50.000000", CreditUsed: "10.000000",
		InterestRate: 0.10, Status: StatusActive,
		ReputationTier: "trusted", ReputationScore: 70,
		ApprovedAt: now, LastReviewAt: now, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.Create(ctx, line); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Update: suspend, change limit, set revoked_at.
	line.Status = StatusSuspended
	line.CreditLimit = "25.000000"
	line.ReputationScore = 35
	revokedAt := time.Now()
	line.RevokedAt = revokedAt

	if err := store.Update(ctx, line); err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	got, err := store.Get(ctx, "cl_test040")
	if err != nil {
		t.Fatalf("Get after update failed: %v", err)
	}
	if got.Status != StatusSuspended {
		t.Errorf("Status: got %s, want suspended", got.Status)
	}
	if got.CreditLimit != "25.000000" {
		t.Errorf("CreditLimit: got %s, want 25.000000", got.CreditLimit)
	}
	if got.RevokedAt.IsZero() {
		t.Error("RevokedAt should be set")
	}

	// Update non-existent should return ErrCreditLineNotFound.
	fake := &CreditLine{ID: "cl_nonexistent"}
	err = store.Update(ctx, fake)
	if err != ErrCreditLineNotFound {
		t.Errorf("Expected ErrCreditLineNotFound for fake update, got %v", err)
	}
}

func TestPostgresCredit_ListActive(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now()

	// Create 3 active and 1 suspended.
	for i, s := range []Status{StatusActive, StatusActive, StatusSuspended, StatusActive} {
		line := &CreditLine{
			ID: "cl_list" + string(rune('0'+i)), AgentAddr: "0xaaaa0000000000000000000000000000000000" + string(rune('a'+i)),
			CreditLimit: "10.000000", CreditUsed: "0.000000",
			InterestRate: 0.10, Status: s,
			ReputationTier: "established", ReputationScore: 50,
			ApprovedAt: now, CreatedAt: now.Add(time.Duration(i) * time.Second), UpdatedAt: now,
		}
		if err := store.Create(ctx, line); err != nil {
			t.Fatalf("Create #%d failed: %v", i, err)
		}
	}

	// List all active — should be 3.
	active, err := store.ListActive(ctx, 100)
	if err != nil {
		t.Fatalf("ListActive failed: %v", err)
	}
	if len(active) != 3 {
		t.Errorf("Expected 3 active, got %d", len(active))
	}

	// Test limit.
	limited, err := store.ListActive(ctx, 2)
	if err != nil {
		t.Fatalf("ListActive with limit failed: %v", err)
	}
	if len(limited) != 2 {
		t.Errorf("Expected 2 with limit, got %d", len(limited))
	}
}

func TestPostgresCredit_ListOverdue(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now()

	// Active, credit used, approved 100 days ago -> overdue at 90 days.
	overdue := &CreditLine{
		ID: "cl_overdue1", AgentAddr: "0xaaaa000000000000000000000000000000000010",
		CreditLimit: "50.000000", CreditUsed: "20.000000",
		InterestRate: 0.10, Status: StatusActive,
		ReputationTier: "trusted", ReputationScore: 70,
		ApprovedAt: now.AddDate(0, 0, -100), CreatedAt: now, UpdatedAt: now,
	}
	// Active, credit used, approved 10 days ago -> NOT overdue.
	recent := &CreditLine{
		ID: "cl_recent1", AgentAddr: "0xaaaa000000000000000000000000000000000011",
		CreditLimit: "50.000000", CreditUsed: "5.000000",
		InterestRate: 0.10, Status: StatusActive,
		ReputationTier: "trusted", ReputationScore: 70,
		ApprovedAt: now.AddDate(0, 0, -10), CreatedAt: now, UpdatedAt: now,
	}
	// Active, zero credit used, approved 100 days ago -> NOT overdue (nothing owed).
	nothingOwed := &CreditLine{
		ID: "cl_noowe1", AgentAddr: "0xaaaa000000000000000000000000000000000012",
		CreditLimit: "50.000000", CreditUsed: "0.000000",
		InterestRate: 0.10, Status: StatusActive,
		ReputationTier: "trusted", ReputationScore: 70,
		ApprovedAt: now.AddDate(0, 0, -100), CreatedAt: now, UpdatedAt: now,
	}

	for _, l := range []*CreditLine{overdue, recent, nothingOwed} {
		if err := store.Create(ctx, l); err != nil {
			t.Fatalf("Create %s failed: %v", l.ID, err)
		}
	}

	overdueLines, err := store.ListOverdue(ctx, 90, 100)
	if err != nil {
		t.Fatalf("ListOverdue failed: %v", err)
	}
	if len(overdueLines) != 1 {
		t.Errorf("Expected 1 overdue line, got %d", len(overdueLines))
	}
	if len(overdueLines) > 0 && overdueLines[0].ID != "cl_overdue1" {
		t.Errorf("Expected overdue line cl_overdue1, got %s", overdueLines[0].ID)
	}
}

func TestPostgresCredit_NullableTimestamps(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now()

	// Create a line with zero-value (null) optional timestamps.
	line := &CreditLine{
		ID: "cl_null01", AgentAddr: "0xaaaa000000000000000000000000000000000020",
		CreditLimit: "10.000000", CreditUsed: "0.000000",
		InterestRate: 0.10, Status: StatusActive,
		ReputationTier: "established", ReputationScore: 45,
		CreatedAt: now, UpdatedAt: now,
		// ApprovedAt, LastReviewAt, DefaultedAt, RevokedAt are zero
	}
	if err := store.Create(ctx, line); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	got, err := store.Get(ctx, "cl_null01")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if !got.ApprovedAt.IsZero() {
		t.Errorf("ApprovedAt should be zero, got %v", got.ApprovedAt)
	}
	if !got.DefaultedAt.IsZero() {
		t.Errorf("DefaultedAt should be zero, got %v", got.DefaultedAt)
	}
	if !got.RevokedAt.IsZero() {
		t.Errorf("RevokedAt should be zero, got %v", got.RevokedAt)
	}
}
