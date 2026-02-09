//go:build integration

package contracts

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/lib/pq"
)

func setupTestDB(t *testing.T) (*PostgresStore, *sql.DB, func()) {
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

	// Ensure tables exist (mirrors migration 007_contracts.sql)
	_, err = db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS contracts (
			id                  VARCHAR(36) PRIMARY KEY,
			buyer_addr          VARCHAR(42) NOT NULL,
			seller_addr         VARCHAR(42) NOT NULL,
			service_type        VARCHAR(100) NOT NULL,
			price_per_call      NUMERIC(20,6) NOT NULL,
			min_volume          INTEGER NOT NULL DEFAULT 1,
			buyer_budget        NUMERIC(20,6) NOT NULL,
			seller_penalty      NUMERIC(20,6) NOT NULL DEFAULT 0,
			max_latency_ms      INTEGER NOT NULL DEFAULT 10000,
			min_success_rate    NUMERIC(5,2) NOT NULL DEFAULT 95.00,
			sla_window_size     INTEGER NOT NULL DEFAULT 20,
			status              VARCHAR(20) NOT NULL DEFAULT 'proposed',
			duration            VARCHAR(20) NOT NULL,
			starts_at           TIMESTAMPTZ,
			expires_at          TIMESTAMPTZ,
			total_calls         INTEGER NOT NULL DEFAULT 0,
			successful_calls    INTEGER NOT NULL DEFAULT 0,
			failed_calls        INTEGER NOT NULL DEFAULT 0,
			total_latency_ms    BIGINT NOT NULL DEFAULT 0,
			budget_spent        NUMERIC(20,6) NOT NULL DEFAULT 0,
			terminated_by       VARCHAR(42),
			terminated_reason   TEXT,
			violation_details   TEXT,
			resolved_at         TIMESTAMPTZ,
			created_at          TIMESTAMPTZ DEFAULT NOW(),
			updated_at          TIMESTAMPTZ DEFAULT NOW()
		)`)
	if err != nil {
		t.Fatalf("Failed to create contracts table: %v", err)
	}

	_, err = db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS contract_calls (
			id              VARCHAR(36) PRIMARY KEY,
			contract_id     VARCHAR(36) NOT NULL REFERENCES contracts(id),
			status          VARCHAR(20) NOT NULL DEFAULT 'pending',
			latency_ms      INTEGER,
			error_message   TEXT,
			amount          NUMERIC(20,6) NOT NULL,
			created_at      TIMESTAMPTZ DEFAULT NOW()
		)`)
	if err != nil {
		t.Fatalf("Failed to create contract_calls table: %v", err)
	}

	cleanup := func() {
		db.ExecContext(ctx, "DELETE FROM contract_calls")
		db.ExecContext(ctx, "DELETE FROM contracts")
		db.Close()
	}

	return store, db, cleanup
}

func makeContract(id string, now time.Time) *Contract {
	return &Contract{
		ID:             id,
		BuyerAddr:      "0xbuyer0000000000000000000000000000000001",
		SellerAddr:     "0xseller000000000000000000000000000000001",
		ServiceType:    "translation",
		PricePerCall:   "0.005000",
		MinVolume:      10,
		BuyerBudget:    "1.000000",
		SellerPenalty:  "0.100000",
		MaxLatencyMs:   5000,
		MinSuccessRate: 95.00,
		SLAWindowSize:  20,
		Status:         StatusProposed,
		Duration:       "7d",
		BudgetSpent:    "0.000000",
		CreatedAt:      now,
		UpdatedAt:      now,
	}
}

func TestPostgresContract_CreateAndGet(t *testing.T) {
	store, _, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now().Truncate(time.Microsecond)
	c := makeContract("ct_test001", now)

	if err := store.Create(ctx, c); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	got, err := store.Get(ctx, "ct_test001")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if got.ID != c.ID {
		t.Errorf("ID: got %s, want %s", got.ID, c.ID)
	}
	if got.ServiceType != "translation" {
		t.Errorf("ServiceType: got %s, want translation", got.ServiceType)
	}
	if got.PricePerCall != "0.005000" {
		t.Errorf("PricePerCall: got %s, want 0.005000", got.PricePerCall)
	}
	if got.MinVolume != 10 {
		t.Errorf("MinVolume: got %d, want 10", got.MinVolume)
	}
	if got.MinSuccessRate != 95.00 {
		t.Errorf("MinSuccessRate: got %f, want 95.00", got.MinSuccessRate)
	}
	if got.Status != StatusProposed {
		t.Errorf("Status: got %s, want %s", got.Status, StatusProposed)
	}
	if got.StartsAt != nil {
		t.Errorf("StartsAt should be nil, got %v", got.StartsAt)
	}
	if got.ExpiresAt != nil {
		t.Errorf("ExpiresAt should be nil, got %v", got.ExpiresAt)
	}
}

func TestPostgresContract_GetNotFound(t *testing.T) {
	store, _, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	_, err := store.Get(ctx, "ct_nonexistent")
	if err != ErrContractNotFound {
		t.Errorf("Expected ErrContractNotFound, got %v", err)
	}
}

func TestPostgresContract_Update(t *testing.T) {
	store, _, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now().Truncate(time.Microsecond)
	c := makeContract("ct_test002", now)

	if err := store.Create(ctx, c); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Activate
	startsAt := now.Add(1 * time.Minute).Truncate(time.Microsecond)
	expiresAt := now.Add(7 * 24 * time.Hour).Truncate(time.Microsecond)
	c.Status = StatusActive
	c.StartsAt = &startsAt
	c.ExpiresAt = &expiresAt
	c.TotalCalls = 5
	c.SuccessfulCalls = 4
	c.FailedCalls = 1
	c.TotalLatencyMs = 12500
	c.BudgetSpent = "0.025000"
	c.UpdatedAt = now.Add(1 * time.Minute).Truncate(time.Microsecond)

	if err := store.Update(ctx, c); err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	got, err := store.Get(ctx, c.ID)
	if err != nil {
		t.Fatalf("Get after update failed: %v", err)
	}

	if got.Status != StatusActive {
		t.Errorf("Status: got %s, want %s", got.Status, StatusActive)
	}
	if got.StartsAt == nil {
		t.Error("StartsAt should not be nil after activation")
	}
	if got.TotalCalls != 5 {
		t.Errorf("TotalCalls: got %d, want 5", got.TotalCalls)
	}
	if got.BudgetSpent != "0.025000" {
		t.Errorf("BudgetSpent: got %s, want 0.025000", got.BudgetSpent)
	}
}

func TestPostgresContract_UpdateNotFound(t *testing.T) {
	store, _, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now()

	c := &Contract{
		ID:          "ct_nonexistent",
		Status:      StatusCompleted,
		BudgetSpent: "0.000000",
		UpdatedAt:   now,
	}

	err := store.Update(ctx, c)
	if err != ErrContractNotFound {
		t.Errorf("Expected ErrContractNotFound, got %v", err)
	}
}

func TestPostgresContract_ListByAgent(t *testing.T) {
	store, _, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now().Truncate(time.Microsecond)
	buyer := "0xbuyer0000000000000000000000000000000002"

	// Create contracts: 2 proposed, 1 active
	contracts := []*Contract{
		func() *Contract {
			c := makeContract("ct_list_a", now)
			c.BuyerAddr = buyer
			c.CreatedAt = now
			return c
		}(),
		func() *Contract {
			c := makeContract("ct_list_b", now.Add(1*time.Second))
			c.BuyerAddr = buyer
			c.Status = StatusActive
			c.CreatedAt = now.Add(1 * time.Second)
			return c
		}(),
		func() *Contract {
			c := makeContract("ct_list_c", now.Add(2*time.Second))
			c.SellerAddr = buyer // buyer is seller here
			c.BuyerAddr = "0xother0000000000000000000000000000000001"
			c.CreatedAt = now.Add(2 * time.Second)
			return c
		}(),
	}

	for _, c := range contracts {
		if err := store.Create(ctx, c); err != nil {
			t.Fatalf("Create %s failed: %v", c.ID, err)
		}
	}

	// All contracts for buyer (as buyer or seller)
	results, err := store.ListByAgent(ctx, buyer, "", 10)
	if err != nil {
		t.Fatalf("ListByAgent failed: %v", err)
	}
	if len(results) != 3 {
		t.Errorf("Expected 3 results, got %d", len(results))
	}

	// Filter by status
	results, err = store.ListByAgent(ctx, buyer, "active", 10)
	if err != nil {
		t.Fatalf("ListByAgent with status filter failed: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("Expected 1 active result, got %d", len(results))
	}
}

func TestPostgresContract_ListExpiring(t *testing.T) {
	store, _, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now().Truncate(time.Microsecond)

	pastExpiry := now.Add(-1 * time.Hour)
	futureExpiry := now.Add(24 * time.Hour)

	contracts := []*Contract{
		func() *Contract {
			c := makeContract("ct_exp_a", now)
			c.Status = StatusActive
			c.ExpiresAt = &pastExpiry
			return c
		}(),
		func() *Contract {
			c := makeContract("ct_exp_b", now.Add(1*time.Second))
			c.BuyerAddr = "0xbuyer0000000000000000000000000000000003"
			c.Status = StatusActive
			c.ExpiresAt = &futureExpiry
			return c
		}(),
		func() *Contract {
			c := makeContract("ct_exp_c", now.Add(2*time.Second))
			c.BuyerAddr = "0xbuyer0000000000000000000000000000000004"
			c.Status = StatusCompleted // terminal
			c.ExpiresAt = &pastExpiry
			return c
		}(),
	}

	for _, c := range contracts {
		if err := store.Create(ctx, c); err != nil {
			t.Fatalf("Create %s failed: %v", c.ID, err)
		}
	}

	results, err := store.ListExpiring(ctx, now, 10)
	if err != nil {
		t.Fatalf("ListExpiring failed: %v", err)
	}

	// Only ct_exp_a: active + expired
	if len(results) != 1 {
		t.Fatalf("Expected 1 expiring, got %d", len(results))
	}
	if results[0].ID != "ct_exp_a" {
		t.Errorf("Expected ct_exp_a, got %s", results[0].ID)
	}
}

func TestPostgresContract_ListActive(t *testing.T) {
	store, _, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now().Truncate(time.Microsecond)

	contracts := []*Contract{
		func() *Contract {
			c := makeContract("ct_active_a", now)
			c.Status = StatusActive
			return c
		}(),
		func() *Contract {
			c := makeContract("ct_active_b", now.Add(1*time.Second))
			c.BuyerAddr = "0xbuyer0000000000000000000000000000000005"
			c.Status = StatusActive
			return c
		}(),
		func() *Contract {
			c := makeContract("ct_active_c", now.Add(2*time.Second))
			c.BuyerAddr = "0xbuyer0000000000000000000000000000000006"
			c.Status = StatusProposed // not active
			return c
		}(),
	}

	for _, c := range contracts {
		if err := store.Create(ctx, c); err != nil {
			t.Fatalf("Create %s failed: %v", c.ID, err)
		}
	}

	results, err := store.ListActive(ctx, 10)
	if err != nil {
		t.Fatalf("ListActive failed: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("Expected 2 active, got %d", len(results))
	}
}

func TestPostgresContract_RecordCall(t *testing.T) {
	store, _, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now().Truncate(time.Microsecond)
	c := makeContract("ct_call001", now)

	if err := store.Create(ctx, c); err != nil {
		t.Fatalf("Create contract failed: %v", err)
	}

	call := &ContractCall{
		ID:         "cc_test001",
		ContractID: c.ID,
		Status:     "success",
		LatencyMs:  250,
		Amount:     "0.005000",
		CreatedAt:  now,
	}

	if err := store.RecordCall(ctx, call); err != nil {
		t.Fatalf("RecordCall failed: %v", err)
	}

	// Verify via ListCalls
	calls, err := store.ListCalls(ctx, c.ID, 10)
	if err != nil {
		t.Fatalf("ListCalls failed: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("Expected 1 call, got %d", len(calls))
	}
	if calls[0].ID != "cc_test001" {
		t.Errorf("Call ID: got %s, want cc_test001", calls[0].ID)
	}
	if calls[0].LatencyMs != 250 {
		t.Errorf("LatencyMs: got %d, want 250", calls[0].LatencyMs)
	}
}

func TestPostgresContract_ListCalls(t *testing.T) {
	store, _, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now().Truncate(time.Microsecond)
	c := makeContract("ct_calls_list", now)

	if err := store.Create(ctx, c); err != nil {
		t.Fatalf("Create contract failed: %v", err)
	}

	// Insert 5 calls
	for i := 0; i < 5; i++ {
		call := &ContractCall{
			ID:         fmt.Sprintf("cc_list_%d", i),
			ContractID: c.ID,
			Status:     "success",
			LatencyMs:  100 + i*50,
			Amount:     "0.005000",
			CreatedAt:  now.Add(time.Duration(i) * time.Second),
		}
		if err := store.RecordCall(ctx, call); err != nil {
			t.Fatalf("RecordCall %d failed: %v", i, err)
		}
	}

	// List with limit
	calls, err := store.ListCalls(ctx, c.ID, 3)
	if err != nil {
		t.Fatalf("ListCalls failed: %v", err)
	}
	if len(calls) != 3 {
		t.Errorf("Expected 3 calls with limit, got %d", len(calls))
	}

	// Most recent first (DESC order)
	if calls[0].CreatedAt.Before(calls[1].CreatedAt) {
		t.Error("Expected DESC order by created_at")
	}
}

func TestPostgresContract_GetRecentCalls(t *testing.T) {
	store, _, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now().Truncate(time.Microsecond)
	c := makeContract("ct_recent_calls", now)

	if err := store.Create(ctx, c); err != nil {
		t.Fatalf("Create contract failed: %v", err)
	}

	// Insert 10 calls
	for i := 0; i < 10; i++ {
		status := "success"
		if i%3 == 0 {
			status = "failed"
		}
		call := &ContractCall{
			ID:         fmt.Sprintf("cc_recent_%d", i),
			ContractID: c.ID,
			Status:     status,
			LatencyMs:  200,
			Amount:     "0.005000",
			CreatedAt:  now.Add(time.Duration(i) * time.Second),
		}
		if err := store.RecordCall(ctx, call); err != nil {
			t.Fatalf("RecordCall %d failed: %v", i, err)
		}
	}

	// Get recent 5
	calls, err := store.GetRecentCalls(ctx, c.ID, 5)
	if err != nil {
		t.Fatalf("GetRecentCalls failed: %v", err)
	}
	if len(calls) != 5 {
		t.Fatalf("Expected 5 recent calls, got %d", len(calls))
	}

	// Should be DESC order
	if calls[0].CreatedAt.Before(calls[1].CreatedAt) {
		t.Error("Expected DESC order by created_at")
	}
}
