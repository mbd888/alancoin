package eventbus

import (
	"context"
	"database/sql"
	"log/slog"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestCDC_PollOnce_PublishesEntries(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	bus := NewMemoryBus(100, slog.Default())
	cdc := NewCDC(db, bus, slog.Default())

	rows := sqlmock.NewRows([]string{"id", "agent_address", "type", "amount", "reference", "tx_hash", "created_at"}).
		AddRow("entry_1", "0xAgent1", "credit", "10.000000", "ref1", sql.NullString{}, "2025-01-01T00:00:00Z").
		AddRow("entry_2", "0xAgent2", "debit", "5.000000", "ref2", sql.NullString{String: "0xTxHash", Valid: true}, "2025-01-01T00:00:01Z")

	mock.ExpectQuery("SELECT id, agent_address, type, amount, reference, tx_hash, created_at").
		WithArgs("").
		WillReturnRows(rows)

	// Subscribe to capture events
	var received []Event
	bus.Subscribe("ledger.credit", "test", 10, 100*time.Millisecond, func(_ context.Context, events []Event) error {
		received = append(received, events...)
		return nil
	})
	bus.Subscribe("ledger.debit", "test2", 10, 100*time.Millisecond, func(_ context.Context, events []Event) error {
		received = append(received, events...)
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	go bus.Start(ctx)

	cdc.pollOnce(ctx)

	// Wait for events to be consumed
	time.Sleep(200 * time.Millisecond)
	cancel()

	if cdc.lastID != "entry_2" {
		t.Errorf("lastID=%q, want entry_2", cdc.lastID)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet mock expectations: %v", err)
	}
}

func TestCDC_PollOnce_EmptyResult(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	bus := NewMemoryBus(100, slog.Default())
	cdc := NewCDC(db, bus, slog.Default())

	rows := sqlmock.NewRows([]string{"id", "agent_address", "type", "amount", "reference", "tx_hash", "created_at"})
	mock.ExpectQuery("SELECT").WithArgs("").WillReturnRows(rows)

	cdc.pollOnce(context.Background())

	if cdc.lastID != "" {
		t.Errorf("lastID should be empty for no results, got %q", cdc.lastID)
	}
}

func TestCDC_PollOnce_UpdatesLastID(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	bus := NewMemoryBus(1000, slog.Default())
	cdc := NewCDC(db, bus, slog.Default())

	rows := sqlmock.NewRows([]string{"id", "agent_address", "type", "amount", "reference", "tx_hash", "created_at"}).
		AddRow("entry_100", "0xA", "credit", "1.0", "ref", sql.NullString{}, "2025-01-01T00:00:00Z")

	mock.ExpectQuery("SELECT").WithArgs("").WillReturnRows(rows)

	cdc.pollOnce(context.Background())

	if cdc.lastID != "entry_100" {
		t.Errorf("lastID=%q, want entry_100", cdc.lastID)
	}

	// Second poll should use the new lastID
	rows2 := sqlmock.NewRows([]string{"id", "agent_address", "type", "amount", "reference", "tx_hash", "created_at"}).
		AddRow("entry_200", "0xB", "debit", "2.0", "ref2", sql.NullString{}, "2025-01-01T00:00:01Z")

	mock.ExpectQuery("SELECT").WithArgs("entry_100").WillReturnRows(rows2)

	cdc.pollOnce(context.Background())

	if cdc.lastID != "entry_200" {
		t.Errorf("lastID=%q, want entry_200", cdc.lastID)
	}
}
