//go:build integration

package escrow

import (
	"context"
	"database/sql"
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

	// Ensure table exists (mirrors migration 003_escrow.sql)
	_, err = db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS escrows (
			id               VARCHAR(36) PRIMARY KEY,
			buyer_addr       VARCHAR(42) NOT NULL,
			seller_addr      VARCHAR(42) NOT NULL,
			amount           NUMERIC(20,6) NOT NULL,
			service_id       VARCHAR(255),
			session_key_id   VARCHAR(255),
			status           VARCHAR(20) NOT NULL DEFAULT 'pending',
			auto_release_at  TIMESTAMPTZ NOT NULL,
			delivered_at     TIMESTAMPTZ,
			resolved_at      TIMESTAMPTZ,
			dispute_reason   TEXT,
			resolution       TEXT,
			created_at       TIMESTAMPTZ DEFAULT NOW(),
			updated_at       TIMESTAMPTZ DEFAULT NOW()
		)`)
	if err != nil {
		t.Fatalf("Failed to create escrows table: %v", err)
	}

	cleanup := func() {
		db.ExecContext(ctx, "DELETE FROM escrows")
		db.Close()
	}

	return store, db, cleanup
}

func TestPostgresEscrow_CreateAndGet(t *testing.T) {
	store, _, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now().Truncate(time.Microsecond)
	autoRelease := now.Add(5 * time.Minute)

	e := &Escrow{
		ID:            "esc_test001",
		BuyerAddr:     "0xbuyer0000000000000000000000000000000001",
		SellerAddr:    "0xseller000000000000000000000000000000001",
		Amount:        "10.500000",
		ServiceID:     "svc_abc",
		SessionKeyID:  "sk_123",
		Status:        StatusPending,
		AutoReleaseAt: autoRelease,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	if err := store.Create(ctx, e); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	got, err := store.Get(ctx, "esc_test001")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if got.ID != e.ID {
		t.Errorf("ID: got %s, want %s", got.ID, e.ID)
	}
	if got.BuyerAddr != e.BuyerAddr {
		t.Errorf("BuyerAddr: got %s, want %s", got.BuyerAddr, e.BuyerAddr)
	}
	if got.SellerAddr != e.SellerAddr {
		t.Errorf("SellerAddr: got %s, want %s", got.SellerAddr, e.SellerAddr)
	}
	if got.Amount != e.Amount {
		t.Errorf("Amount: got %s, want %s", got.Amount, e.Amount)
	}
	if got.ServiceID != e.ServiceID {
		t.Errorf("ServiceID: got %s, want %s", got.ServiceID, e.ServiceID)
	}
	if got.SessionKeyID != e.SessionKeyID {
		t.Errorf("SessionKeyID: got %s, want %s", got.SessionKeyID, e.SessionKeyID)
	}
	if got.Status != StatusPending {
		t.Errorf("Status: got %s, want %s", got.Status, StatusPending)
	}
	if got.DeliveredAt != nil {
		t.Errorf("DeliveredAt should be nil, got %v", got.DeliveredAt)
	}
	if got.ResolvedAt != nil {
		t.Errorf("ResolvedAt should be nil, got %v", got.ResolvedAt)
	}
}

func TestPostgresEscrow_GetNotFound(t *testing.T) {
	store, _, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	_, err := store.Get(ctx, "esc_nonexistent")
	if err != ErrEscrowNotFound {
		t.Errorf("Expected ErrEscrowNotFound, got %v", err)
	}
}

func TestPostgresEscrow_Update(t *testing.T) {
	store, _, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now().Truncate(time.Microsecond)

	e := &Escrow{
		ID:            "esc_test002",
		BuyerAddr:     "0xbuyer0000000000000000000000000000000002",
		SellerAddr:    "0xseller000000000000000000000000000000002",
		Amount:        "5.000000",
		Status:        StatusPending,
		AutoReleaseAt: now.Add(5 * time.Minute),
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	if err := store.Create(ctx, e); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Update to delivered
	deliveredAt := now.Add(1 * time.Minute).Truncate(time.Microsecond)
	e.Status = StatusDelivered
	e.DeliveredAt = &deliveredAt
	e.UpdatedAt = now.Add(1 * time.Minute).Truncate(time.Microsecond)

	if err := store.Update(ctx, e); err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	got, err := store.Get(ctx, e.ID)
	if err != nil {
		t.Fatalf("Get after update failed: %v", err)
	}

	if got.Status != StatusDelivered {
		t.Errorf("Status: got %s, want %s", got.Status, StatusDelivered)
	}
	if got.DeliveredAt == nil {
		t.Error("DeliveredAt should not be nil after update")
	}
}

func TestPostgresEscrow_UpdateNotFound(t *testing.T) {
	store, _, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now()

	e := &Escrow{
		ID:        "esc_nonexistent",
		Status:    StatusReleased,
		UpdatedAt: now,
	}

	err := store.Update(ctx, e)
	if err != ErrEscrowNotFound {
		t.Errorf("Expected ErrEscrowNotFound, got %v", err)
	}
}

func TestPostgresEscrow_ListByAgent(t *testing.T) {
	store, _, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now().Truncate(time.Microsecond)
	buyer := "0xbuyer0000000000000000000000000000000003"
	seller := "0xseller000000000000000000000000000000003"

	// Create 3 escrows: 2 as buyer, 1 as seller (with distinct CreatedAt for ordering)
	escrowList := []*Escrow{
		{ID: "esc_list_a", BuyerAddr: buyer, SellerAddr: "0xother0000000000000000000000000000000001", Amount: "1.000000", Status: StatusPending, AutoReleaseAt: now.Add(5 * time.Minute), CreatedAt: now, UpdatedAt: now},
		{ID: "esc_list_b", BuyerAddr: buyer, SellerAddr: seller, Amount: "2.000000", Status: StatusPending, AutoReleaseAt: now.Add(5 * time.Minute), CreatedAt: now.Add(1 * time.Second), UpdatedAt: now},
		{ID: "esc_list_c", BuyerAddr: "0xother0000000000000000000000000000000002", SellerAddr: buyer, Amount: "3.000000", Status: StatusPending, AutoReleaseAt: now.Add(5 * time.Minute), CreatedAt: now.Add(2 * time.Second), UpdatedAt: now},
	}
	for _, e := range escrowList {
		if err := store.Create(ctx, e); err != nil {
			t.Fatalf("Create %s failed: %v", e.ID, err)
		}
	}

	results, err := store.ListByAgent(ctx, buyer, 10)
	if err != nil {
		t.Fatalf("ListByAgent failed: %v", err)
	}

	if len(results) != 3 {
		t.Errorf("Expected 3 results, got %d", len(results))
	}

	// Test limit
	results, err = store.ListByAgent(ctx, buyer, 2)
	if err != nil {
		t.Fatalf("ListByAgent with limit failed: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("Expected 2 results with limit, got %d", len(results))
	}
}

func TestPostgresEscrow_ListExpired(t *testing.T) {
	store, _, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now().Truncate(time.Microsecond)

	// Create escrows: 1 expired-pending, 1 expired-released (terminal), 1 not yet expired
	escrows := []*Escrow{
		{ID: "esc_exp_a", BuyerAddr: "0xbuyer0000000000000000000000000000000004", SellerAddr: "0xseller000000000000000000000000000000004", Amount: "1.000000", Status: StatusPending, AutoReleaseAt: now.Add(-1 * time.Minute), CreatedAt: now, UpdatedAt: now},
		{ID: "esc_exp_b", BuyerAddr: "0xbuyer0000000000000000000000000000000005", SellerAddr: "0xseller000000000000000000000000000000005", Amount: "2.000000", Status: StatusReleased, AutoReleaseAt: now.Add(-1 * time.Minute), CreatedAt: now, UpdatedAt: now},
		{ID: "esc_exp_c", BuyerAddr: "0xbuyer0000000000000000000000000000000006", SellerAddr: "0xseller000000000000000000000000000000006", Amount: "3.000000", Status: StatusPending, AutoReleaseAt: now.Add(10 * time.Minute), CreatedAt: now, UpdatedAt: now},
	}
	for _, e := range escrows {
		if err := store.Create(ctx, e); err != nil {
			t.Fatalf("Create %s failed: %v", e.ID, err)
		}
	}

	results, err := store.ListExpired(ctx, now, 10)
	if err != nil {
		t.Fatalf("ListExpired failed: %v", err)
	}

	// Only esc_exp_a should match (pending + past auto_release_at)
	if len(results) != 1 {
		t.Fatalf("Expected 1 expired escrow, got %d", len(results))
	}
	if results[0].ID != "esc_exp_a" {
		t.Errorf("Expected esc_exp_a, got %s", results[0].ID)
	}
}

func TestPostgresEscrow_NullableFields(t *testing.T) {
	store, _, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now().Truncate(time.Microsecond)
	deliveredAt := now.Add(1 * time.Minute).Truncate(time.Microsecond)
	resolvedAt := now.Add(2 * time.Minute).Truncate(time.Microsecond)

	e := &Escrow{
		ID:            "esc_nullable",
		BuyerAddr:     "0xbuyer0000000000000000000000000000000007",
		SellerAddr:    "0xseller000000000000000000000000000000007",
		Amount:        "7.500000",
		Status:        StatusRefunded,
		AutoReleaseAt: now.Add(5 * time.Minute),
		DeliveredAt:   &deliveredAt,
		ResolvedAt:    &resolvedAt,
		DisputeReason: "bad quality",
		Resolution:    "auto_refund",
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	if err := store.Create(ctx, e); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	got, err := store.Get(ctx, e.ID)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if got.DeliveredAt == nil {
		t.Error("DeliveredAt should not be nil")
	}
	if got.ResolvedAt == nil {
		t.Error("ResolvedAt should not be nil")
	}
	if got.DisputeReason != "bad quality" {
		t.Errorf("DisputeReason: got %s, want 'bad quality'", got.DisputeReason)
	}
	if got.Resolution != "auto_refund" {
		t.Errorf("Resolution: got %s, want 'auto_refund'", got.Resolution)
	}

	// Also test with empty optional fields
	e2 := &Escrow{
		ID:            "esc_nullable_empty",
		BuyerAddr:     "0xbuyer0000000000000000000000000000000008",
		SellerAddr:    "0xseller000000000000000000000000000000008",
		Amount:        "1.000000",
		Status:        StatusPending,
		AutoReleaseAt: now.Add(5 * time.Minute),
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	if err := store.Create(ctx, e2); err != nil {
		t.Fatalf("Create e2 failed: %v", err)
	}

	got2, err := store.Get(ctx, e2.ID)
	if err != nil {
		t.Fatalf("Get e2 failed: %v", err)
	}

	if got2.ServiceID != "" {
		t.Errorf("ServiceID should be empty, got %s", got2.ServiceID)
	}
	if got2.SessionKeyID != "" {
		t.Errorf("SessionKeyID should be empty, got %s", got2.SessionKeyID)
	}
	if got2.DeliveredAt != nil {
		t.Errorf("DeliveredAt should be nil, got %v", got2.DeliveredAt)
	}
}
