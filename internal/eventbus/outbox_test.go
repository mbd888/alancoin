package eventbus

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestOutbox_PublishBatch_PublishesAndMarks(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	bus := NewMemoryBus(100, slog.Default())
	outbox := NewOutbox(db, slog.Default())

	// Mock SELECT FOR UPDATE returning 2 rows
	rows := sqlmock.NewRows([]string{"id", "topic", "key", "payload", "request_id", "created_at"}).
		AddRow("evt_1", TopicSettlement, "0xBuyer", []byte(`{"sessionId":"s1"}`), nil, time.Now()).
		AddRow("evt_2", TopicSettlement, "0xBuyer", []byte(`{"sessionId":"s2"}`), nil, time.Now())

	mock.ExpectQuery("SELECT id, topic, key, payload, request_id, created_at").
		WillReturnRows(rows)

	// Expect batch UPDATE for all published events
	mock.ExpectExec("UPDATE eventbus_outbox SET published = TRUE").
		WillReturnResult(sqlmock.NewResult(0, 2))

	outbox.publishBatch(context.Background(), bus)

	// Verify bus received events
	m := bus.Metrics()
	if m.Published != 2 {
		t.Errorf("published=%d, want 2", m.Published)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet mock expectations: %v", err)
	}
}

func TestOutbox_PublishBatch_EmptyResult(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	bus := NewMemoryBus(100, slog.Default())
	outbox := NewOutbox(db, slog.Default())

	rows := sqlmock.NewRows([]string{"id", "topic", "key", "payload", "request_id", "created_at"})
	mock.ExpectQuery("SELECT").WillReturnRows(rows)

	outbox.publishBatch(context.Background(), bus)

	m := bus.Metrics()
	if m.Published != 0 {
		t.Errorf("published=%d, want 0 for empty batch", m.Published)
	}
}

func TestOutbox_PendingCount(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	outbox := NewOutbox(db, slog.Default())

	mock.ExpectQuery("SELECT COUNT").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(42))

	count, err := outbox.PendingCount(context.Background())
	if err != nil {
		t.Fatalf("PendingCount: %v", err)
	}
	if count != 42 {
		t.Errorf("count=%d, want 42", count)
	}
}

func TestOutbox_Cleanup(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	outbox := NewOutbox(db, slog.Default())

	mock.ExpectExec("DELETE FROM eventbus_outbox").
		WillReturnResult(sqlmock.NewResult(0, 15))

	deleted, err := outbox.Cleanup(context.Background(), 24*time.Hour)
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if deleted != 15 {
		t.Errorf("deleted=%d, want 15", deleted)
	}
}

func TestOutbox_Poll_ContextCancellation(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	bus := NewMemoryBus(100, slog.Default())
	outbox := NewOutbox(db, slog.Default())

	// Allow the final drain poll
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"id", "topic", "key", "payload", "request_id", "created_at"}))

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		outbox.Poll(ctx, bus, 50*time.Millisecond)
		close(done)
	}()

	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// Exited
	case <-time.After(2 * time.Second):
		t.Error("Poll did not exit after context cancellation")
	}
}
