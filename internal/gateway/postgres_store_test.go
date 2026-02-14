//go:build integration

package gateway

import (
	"context"
	"database/sql"
	"log/slog"
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
		t.Fatalf("open database: %v", err)
	}
	if err := db.Ping(); err != nil {
		t.Fatalf("connect to database: %v", err)
	}

	ctx := context.Background()

	// Create tables (mirrors migrations 030 + 032)
	_, err = db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS gateway_sessions (
			id              TEXT PRIMARY KEY,
			agent_addr      TEXT NOT NULL,
			tenant_id       TEXT,
			max_total       NUMERIC(20,6) NOT NULL,
			max_per_request NUMERIC(20,6) NOT NULL,
			total_spent     NUMERIC(20,6) NOT NULL DEFAULT 0,
			request_count   INT NOT NULL DEFAULT 0,
			strategy        TEXT NOT NULL DEFAULT 'cheapest',
			allowed_types   TEXT[],
			warn_at_percent INT NOT NULL DEFAULT 0,
			status          TEXT NOT NULL DEFAULT 'active',
			expires_at      TIMESTAMPTZ NOT NULL,
			created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
		CREATE TABLE IF NOT EXISTS gateway_request_logs (
			id           TEXT PRIMARY KEY,
			session_id   TEXT NOT NULL REFERENCES gateway_sessions(id),
			tenant_id    TEXT,
			service_type TEXT NOT NULL,
			agent_called TEXT NOT NULL DEFAULT '',
			amount       NUMERIC(20,6) NOT NULL DEFAULT 0,
			fee_amount   NUMERIC(20,6) DEFAULT 0,
			status       TEXT NOT NULL,
			latency_ms   BIGINT NOT NULL DEFAULT 0,
			error        TEXT NOT NULL DEFAULT '',
			policy_result JSONB,
			created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`)
	if err != nil {
		t.Fatalf("create tables: %v", err)
	}

	store := NewPostgresStore(db)

	cleanup := func() {
		_, _ = db.ExecContext(ctx, "DELETE FROM gateway_request_logs")
		_, _ = db.ExecContext(ctx, "DELETE FROM gateway_sessions")
		db.Close()
	}

	return store, db, cleanup
}

func TestPostgresStore_SessionCRUD(t *testing.T) {
	store, _, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	now := time.Now().Truncate(time.Microsecond)
	session := &Session{
		ID:            "gw_test_crud",
		AgentAddr:     "0xbuyer",
		MaxTotal:      "10.000000",
		MaxPerRequest: "2.000000",
		TotalSpent:    "0.000000",
		RequestCount:  0,
		Strategy:      "cheapest",
		AllowedTypes:  []string{"translation", "inference"},
		WarnAtPercent: 20,
		Status:        StatusActive,
		ExpiresAt:     now.Add(time.Hour),
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	// Create
	if err := store.CreateSession(ctx, session); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Get
	got, err := store.GetSession(ctx, session.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.AgentAddr != "0xbuyer" {
		t.Errorf("AgentAddr = %q, want %q", got.AgentAddr, "0xbuyer")
	}
	if got.MaxTotal != "10.000000" {
		t.Errorf("MaxTotal = %q, want %q", got.MaxTotal, "10.000000")
	}
	if got.Status != StatusActive {
		t.Errorf("Status = %q, want %q", got.Status, StatusActive)
	}
	if len(got.AllowedTypes) != 2 {
		t.Errorf("AllowedTypes len = %d, want 2", len(got.AllowedTypes))
	}
	if got.WarnAtPercent != 20 {
		t.Errorf("WarnAtPercent = %d, want 20", got.WarnAtPercent)
	}

	// Update
	session.TotalSpent = "3.500000"
	session.RequestCount = 2
	session.Status = StatusClosed
	session.UpdatedAt = time.Now().Truncate(time.Microsecond)
	if err := store.UpdateSession(ctx, session); err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}

	got, err = store.GetSession(ctx, session.ID)
	if err != nil {
		t.Fatalf("GetSession after update: %v", err)
	}
	if got.TotalSpent != "3.500000" {
		t.Errorf("TotalSpent = %q, want %q", got.TotalSpent, "3.500000")
	}
	if got.RequestCount != 2 {
		t.Errorf("RequestCount = %d, want 2", got.RequestCount)
	}
	if got.Status != StatusClosed {
		t.Errorf("Status = %q, want %q", got.Status, StatusClosed)
	}

	// Not found
	_, err = store.GetSession(ctx, "gw_nonexistent")
	if err != ErrSessionNotFound {
		t.Errorf("GetSession nonexistent = %v, want ErrSessionNotFound", err)
	}

	// Update not found
	err = store.UpdateSession(ctx, &Session{ID: "gw_nonexistent"})
	if err != ErrSessionNotFound {
		t.Errorf("UpdateSession nonexistent = %v, want ErrSessionNotFound", err)
	}
}

func TestPostgresStore_ListSessions(t *testing.T) {
	store, _, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	now := time.Now().Truncate(time.Microsecond)
	for i := 0; i < 3; i++ {
		s := &Session{
			ID:            "gw_list_" + string(rune('a'+i)),
			AgentAddr:     "0xagent",
			MaxTotal:      "5.000000",
			MaxPerRequest: "1.000000",
			TotalSpent:    "0.000000",
			Strategy:      "cheapest",
			Status:        StatusActive,
			ExpiresAt:     now.Add(time.Hour),
			CreatedAt:     now.Add(time.Duration(i) * time.Second),
			UpdatedAt:     now,
		}
		if err := store.CreateSession(ctx, s); err != nil {
			t.Fatalf("create session %d: %v", i, err)
		}
	}

	sessions, err := store.ListSessions(ctx, "0xagent", 10)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 3 {
		t.Fatalf("ListSessions count = %d, want 3", len(sessions))
	}
	// Should be ordered by created_at DESC
	if sessions[0].ID != "gw_list_c" {
		t.Errorf("first session = %q, want gw_list_c (newest first)", sessions[0].ID)
	}

	// Limit
	sessions, err = store.ListSessions(ctx, "0xagent", 1)
	if err != nil {
		t.Fatalf("ListSessions limit: %v", err)
	}
	if len(sessions) != 1 {
		t.Errorf("ListSessions limited count = %d, want 1", len(sessions))
	}
}

func TestPostgresStore_ListExpired(t *testing.T) {
	store, _, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	now := time.Now().Truncate(time.Microsecond)

	// Expired session
	expired := &Session{
		ID:            "gw_expired",
		AgentAddr:     "0xbuyer",
		MaxTotal:      "5.000000",
		MaxPerRequest: "1.000000",
		TotalSpent:    "0.000000",
		Strategy:      "cheapest",
		Status:        StatusActive,
		ExpiresAt:     now.Add(-time.Minute),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	// Active (not yet expired)
	active := &Session{
		ID:            "gw_active",
		AgentAddr:     "0xbuyer",
		MaxTotal:      "5.000000",
		MaxPerRequest: "1.000000",
		TotalSpent:    "0.000000",
		Strategy:      "cheapest",
		Status:        StatusActive,
		ExpiresAt:     now.Add(time.Hour),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	// Already closed (should not appear)
	closed := &Session{
		ID:            "gw_closed",
		AgentAddr:     "0xbuyer",
		MaxTotal:      "5.000000",
		MaxPerRequest: "1.000000",
		TotalSpent:    "0.000000",
		Strategy:      "cheapest",
		Status:        StatusClosed,
		ExpiresAt:     now.Add(-time.Minute),
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	for _, s := range []*Session{expired, active, closed} {
		if err := store.CreateSession(ctx, s); err != nil {
			t.Fatalf("create %s: %v", s.ID, err)
		}
	}

	result, err := store.ListExpired(ctx, now, 100)
	if err != nil {
		t.Fatalf("ListExpired: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("ListExpired count = %d, want 1", len(result))
	}
	if result[0].ID != "gw_expired" {
		t.Errorf("ListExpired[0].ID = %q, want gw_expired", result[0].ID)
	}
}

func TestPostgresStore_RequestLogs(t *testing.T) {
	store, _, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	now := time.Now().Truncate(time.Microsecond)
	session := &Session{
		ID:            "gw_log_test",
		AgentAddr:     "0xbuyer",
		MaxTotal:      "5.000000",
		MaxPerRequest: "1.000000",
		TotalSpent:    "0.000000",
		Strategy:      "cheapest",
		Status:        StatusActive,
		ExpiresAt:     now.Add(time.Hour),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := store.CreateSession(ctx, session); err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Log with policy result
	log1 := &RequestLog{
		ID:          "gwlog_1",
		SessionID:   session.ID,
		ServiceType: "translation",
		AgentCalled: "0xseller",
		Amount:      "0.100000",
		Status:      "success",
		LatencyMs:   42,
		CreatedAt:   now,
	}
	log2 := &RequestLog{
		ID:          "gwlog_2",
		SessionID:   session.ID,
		ServiceType: "inference",
		AgentCalled: "",
		Amount:      "0",
		Status:      "policy_denied",
		LatencyMs:   1,
		Error:       "rate limit",
		PolicyResult: &PolicyDecision{
			Evaluated:  3,
			Allowed:    false,
			DeniedBy:   "rate_policy",
			DeniedRule: "tx_count",
			Reason:     "too many requests",
			LatencyUs:  500,
		},
		CreatedAt: now.Add(time.Second),
	}

	for _, l := range []*RequestLog{log1, log2} {
		if err := store.CreateLog(ctx, l); err != nil {
			t.Fatalf("CreateLog %s: %v", l.ID, err)
		}
	}

	logs, err := store.ListLogs(ctx, session.ID, 10)
	if err != nil {
		t.Fatalf("ListLogs: %v", err)
	}
	if len(logs) != 2 {
		t.Fatalf("ListLogs count = %d, want 2", len(logs))
	}

	// Newest first (created_at DESC)
	if logs[0].ID != "gwlog_2" {
		t.Errorf("logs[0].ID = %q, want gwlog_2", logs[0].ID)
	}

	// Policy result round-trip
	if logs[0].PolicyResult == nil {
		t.Fatal("PolicyResult is nil, expected non-nil")
	}
	if logs[0].PolicyResult.DeniedBy != "rate_policy" {
		t.Errorf("PolicyResult.DeniedBy = %q, want rate_policy", logs[0].PolicyResult.DeniedBy)
	}
	if logs[0].PolicyResult.Reason != "too many requests" {
		t.Errorf("PolicyResult.Reason = %q, want 'too many requests'", logs[0].PolicyResult.Reason)
	}

	// Log without policy
	if logs[1].PolicyResult != nil {
		t.Errorf("logs[1].PolicyResult = %+v, want nil", logs[1].PolicyResult)
	}
}

func TestPostgresStore_SessionSurvivesRestart(t *testing.T) {
	// This test simulates a "crash recovery" scenario:
	// 1. Create a session in store1
	// 2. Create a NEW store (simulating process restart)
	// 3. Verify session is still accessible with correct state
	store1, db, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	now := time.Now().Truncate(time.Microsecond)
	session := &Session{
		ID:            "gw_survive",
		AgentAddr:     "0xbuyer",
		MaxTotal:      "10.000000",
		MaxPerRequest: "2.000000",
		TotalSpent:    "3.500000",
		RequestCount:  5,
		Strategy:      "cheapest",
		AllowedTypes:  []string{"translation"},
		WarnAtPercent: 20,
		Status:        StatusActive,
		ExpiresAt:     now.Add(time.Hour),
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	if err := store1.CreateSession(ctx, session); err != nil {
		t.Fatalf("store1 CreateSession: %v", err)
	}

	// Simulate process restart: new store, same database
	store2 := NewPostgresStore(db)

	got, err := store2.GetSession(ctx, session.ID)
	if err != nil {
		t.Fatalf("store2 GetSession: %v", err)
	}
	if got.TotalSpent != "3.500000" {
		t.Errorf("TotalSpent = %q, want 3.500000", got.TotalSpent)
	}
	if got.RequestCount != 5 {
		t.Errorf("RequestCount = %d, want 5", got.RequestCount)
	}
	if got.Status != StatusActive {
		t.Errorf("Status = %q, want active", got.Status)
	}
	if len(got.AllowedTypes) != 1 || got.AllowedTypes[0] != "translation" {
		t.Errorf("AllowedTypes = %v, want [translation]", got.AllowedTypes)
	}
}

func TestPostgresStore_NilAllowedTypes(t *testing.T) {
	store, _, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	now := time.Now().Truncate(time.Microsecond)
	session := &Session{
		ID:            "gw_nil_types",
		AgentAddr:     "0xbuyer",
		MaxTotal:      "5.000000",
		MaxPerRequest: "1.000000",
		TotalSpent:    "0.000000",
		Strategy:      "cheapest",
		AllowedTypes:  nil,
		Status:        StatusActive,
		ExpiresAt:     now.Add(time.Hour),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := store.CreateSession(ctx, session); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	got, err := store.GetSession(ctx, session.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.AllowedTypes != nil {
		t.Errorf("AllowedTypes = %v, want nil", got.AllowedTypes)
	}
}

// reconcileMockLedger tracks ReleaseHold calls for reconciliation testing.
type reconcileMockLedger struct {
	released []reconcileReleaseCall
}

type reconcileReleaseCall struct {
	agentAddr, amount, reference string
}

func (m *reconcileMockLedger) Hold(ctx context.Context, agentAddr, amount, reference string) error {
	return nil
}
func (m *reconcileMockLedger) SettleHold(ctx context.Context, buyerAddr, sellerAddr, amount, reference string) error {
	return nil
}
func (m *reconcileMockLedger) SettleHoldWithFee(ctx context.Context, buyerAddr, sellerAddr, sellerAmount, platformAddr, feeAmount, reference string) error {
	return nil
}
func (m *reconcileMockLedger) ReleaseHold(ctx context.Context, agentAddr, amount, reference string) error {
	m.released = append(m.released, reconcileReleaseCall{agentAddr, amount, reference})
	return nil
}

func TestReconcileOrphanedHolds(t *testing.T) {
	_, db, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	// Ensure ledger_entries table exists (minimal schema for the reconciliation query).
	_, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS ledger_entries (
			id             TEXT PRIMARY KEY,
			agent_address  TEXT NOT NULL,
			type           TEXT NOT NULL,
			amount         NUMERIC(20,6) NOT NULL,
			reference      TEXT,
			description    TEXT,
			tx_hash        TEXT,
			reversed_at    TIMESTAMPTZ,
			reversed_by    TEXT,
			reversal_of    TEXT,
			created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`)
	if err != nil {
		t.Fatalf("create ledger_entries: %v", err)
	}
	defer func() { _, _ = db.ExecContext(ctx, "DELETE FROM ledger_entries") }()

	// Case 1: Orphaned hold — hold entry exists, no session, no settle/release.
	// This simulates a crash between ledger.Hold() and store.CreateSession().
	_, err = db.ExecContext(ctx, `
		INSERT INTO ledger_entries (id, agent_address, type, amount, reference, description, created_at)
		VALUES ('entry_orphan', '0xorphan', 'hold', 5.000000, 'gw_orphan_session', 'pending_transfer', NOW())`)
	if err != nil {
		t.Fatalf("insert orphaned hold: %v", err)
	}

	// Case 2: Normal hold — session exists in gateway_sessions. Should NOT be released.
	now := time.Now().Truncate(time.Microsecond)
	normalSession := &Session{
		ID:            "gw_normal_session",
		AgentAddr:     "0xnormal",
		MaxTotal:      "10.000000",
		MaxPerRequest: "2.000000",
		TotalSpent:    "0.000000",
		Strategy:      "cheapest",
		Status:        StatusActive,
		ExpiresAt:     now.Add(time.Hour),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	store := NewPostgresStore(db)
	if err := store.CreateSession(ctx, normalSession); err != nil {
		t.Fatalf("create normal session: %v", err)
	}
	_, err = db.ExecContext(ctx, `
		INSERT INTO ledger_entries (id, agent_address, type, amount, reference, description, created_at)
		VALUES ('entry_normal', '0xnormal', 'hold', 10.000000, 'gw_normal_session', 'pending_transfer', NOW())`)
	if err != nil {
		t.Fatalf("insert normal hold: %v", err)
	}

	// Case 3: Already settled hold — no session, but settle_hold_out exists. Should NOT be released.
	_, err = db.ExecContext(ctx, `
		INSERT INTO ledger_entries (id, agent_address, type, amount, reference, description, created_at)
		VALUES
			('entry_settled_hold', '0xsettled', 'hold', 3.000000, 'gw_settled_session', 'pending_transfer', NOW()),
			('entry_settled_out', '0xsettled', 'settle_hold_out', 3.000000, 'gw_settled_session', 'settled', NOW())`)
	if err != nil {
		t.Fatalf("insert settled hold: %v", err)
	}

	// Case 4: Non-gateway hold — should be ignored (no gw_ prefix).
	_, err = db.ExecContext(ctx, `
		INSERT INTO ledger_entries (id, agent_address, type, amount, reference, description, created_at)
		VALUES ('entry_other', '0xother', 'hold', 1.000000, 'sk_session_key_123', 'pending_transfer', NOW())`)
	if err != nil {
		t.Fatalf("insert non-gateway hold: %v", err)
	}

	// Run reconciliation.
	mock := &reconcileMockLedger{}
	logger := slog.Default()
	ReconcileOrphanedHolds(ctx, db, mock, logger)

	// Only the orphaned hold (Case 1) should be released.
	if len(mock.released) != 1 {
		t.Fatalf("expected 1 release, got %d: %+v", len(mock.released), mock.released)
	}
	r := mock.released[0]
	if r.agentAddr != "0xorphan" {
		t.Errorf("released agent = %q, want 0xorphan", r.agentAddr)
	}
	if r.amount != "5.000000" {
		t.Errorf("released amount = %q, want 5.000000", r.amount)
	}
	if r.reference != "gw_orphan_session" {
		t.Errorf("released reference = %q, want gw_orphan_session", r.reference)
	}
}
