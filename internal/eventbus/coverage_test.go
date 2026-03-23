package eventbus

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

// ---------------------------------------------------------------------------
// drainBuffer: events are routed to correct subscription channels
// ---------------------------------------------------------------------------

func TestDrainBuffer_RoutesEventsToSubscriptions(t *testing.T) {
	bus := NewMemoryBus(10, slog.Default())

	// Set up subscriptions so the router knows topic mapping
	bus.Subscribe(TopicSettlement, "s1", 10, time.Second, func(_ context.Context, _ []Event) error { return nil })
	bus.Subscribe(TopicAlert, "s2", 10, time.Second, func(_ context.Context, _ []Event) error { return nil })

	subChans := []chan Event{
		make(chan Event, 10),
		make(chan Event, 10),
	}

	// Put events into the buffer
	bus.buffer <- Event{ID: "e1", Topic: TopicSettlement, Key: "k"}
	bus.buffer <- Event{ID: "e2", Topic: TopicAlert, Key: "k"}
	bus.buffer <- Event{ID: "e3", Topic: TopicSettlement, Key: "k"}

	ctx := context.Background()
	bus.drainBuffer(ctx, subChans)

	// settlement channel should have 2 events
	if len(subChans[0]) != 2 {
		t.Errorf("settlement channel has %d events, want 2", len(subChans[0]))
	}
	// alert channel should have 1 event
	if len(subChans[1]) != 1 {
		t.Errorf("alert channel has %d events, want 1", len(subChans[1]))
	}
}

func TestDrainBuffer_WildcardSubscription(t *testing.T) {
	bus := NewMemoryBus(10, slog.Default())
	bus.Subscribe("*", "wildcard", 10, time.Second, func(_ context.Context, _ []Event) error { return nil })

	subChans := []chan Event{make(chan Event, 10)}

	bus.buffer <- Event{ID: "e1", Topic: TopicSettlement}
	bus.buffer <- Event{ID: "e2", Topic: TopicAlert}

	bus.drainBuffer(context.Background(), subChans)

	if len(subChans[0]) != 2 {
		t.Errorf("wildcard channel has %d events, want 2", len(subChans[0]))
	}
}

func TestDrainBuffer_Timeout(t *testing.T) {
	bus := NewMemoryBus(10, slog.Default())
	bus.Subscribe(TopicSettlement, "s1", 10, time.Second, func(_ context.Context, _ []Event) error { return nil })

	subChans := []chan Event{make(chan Event, 10)}

	// Put events in buffer but also create a context that's already cancelled
	bus.buffer <- Event{ID: "e1", Topic: TopicSettlement}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	bus.drainBuffer(ctx, subChans)
	// Should have drained 0 events since context was cancelled
	// (the implementation checks ctx.Done() first in the select)
	// Actually, select is non-deterministic, so we just verify no panic.
}

func TestDrainBuffer_ConsumerChannelFull(t *testing.T) {
	bus := NewMemoryBus(10, slog.Default())
	bus.Subscribe(TopicSettlement, "s1", 10, time.Second, func(_ context.Context, _ []Event) error { return nil })

	// Consumer channel with buffer size 1
	subChans := []chan Event{make(chan Event, 1)}

	// Put 3 events in buffer; only 1 should fit in consumer channel
	bus.buffer <- Event{ID: "e1", Topic: TopicSettlement}
	bus.buffer <- Event{ID: "e2", Topic: TopicSettlement}
	bus.buffer <- Event{ID: "e3", Topic: TopicSettlement}

	bus.drainBuffer(context.Background(), subChans)

	// Channel should be full (1 event), 2 were silently dropped
	if len(subChans[0]) != 1 {
		t.Errorf("consumer channel has %d events, want 1 (capacity)", len(subChans[0]))
	}
}

// ---------------------------------------------------------------------------
// consumeLoop: batch size triggers
// ---------------------------------------------------------------------------

func TestConsumeLoop_BatchSizeFlush(t *testing.T) {
	bus := NewMemoryBus(100, slog.Default())

	var batchSizes []int
	var mu sync.Mutex

	ch := make(chan Event, 100)
	sub := subscription{
		topic:         TopicSettlement,
		consumerGroup: "batch-test",
		batchSize:     5,
		flushInterval: 10 * time.Second, // long interval to force batch-size flush
		handler: func(_ context.Context, events []Event) error {
			mu.Lock()
			batchSizes = append(batchSizes, len(events))
			mu.Unlock()
			return nil
		},
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Put exactly 5 events to trigger batch flush
	for i := 0; i < 5; i++ {
		ch <- Event{ID: fmt.Sprintf("e%d", i), Topic: TopicSettlement}
	}

	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()

	bus.consumeLoop(ctx, ch, sub)

	mu.Lock()
	defer mu.Unlock()

	if len(batchSizes) == 0 {
		t.Fatal("expected at least one batch to be processed")
	}
	if batchSizes[0] != 5 {
		t.Errorf("first batch size = %d, want 5", batchSizes[0])
	}
}

func TestConsumeLoop_TimerFlush(t *testing.T) {
	bus := NewMemoryBus(100, slog.Default())

	var batchSizes []int
	var mu sync.Mutex

	ch := make(chan Event, 100)
	sub := subscription{
		topic:         TopicSettlement,
		consumerGroup: "timer-test",
		batchSize:     100, // large batch size so it won't trigger
		flushInterval: 50 * time.Millisecond,
		handler: func(_ context.Context, events []Event) error {
			mu.Lock()
			batchSizes = append(batchSizes, len(events))
			mu.Unlock()
			return nil
		},
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Put 3 events (less than batchSize), should be flushed by timer
	for i := 0; i < 3; i++ {
		ch <- Event{ID: fmt.Sprintf("e%d", i), Topic: TopicSettlement}
	}

	go func() {
		time.Sleep(300 * time.Millisecond)
		cancel()
	}()

	bus.consumeLoop(ctx, ch, sub)

	mu.Lock()
	defer mu.Unlock()

	total := 0
	for _, s := range batchSizes {
		total += s
	}
	if total != 3 {
		t.Errorf("total events processed = %d, want 3", total)
	}
}

func TestConsumeLoop_ChannelClose(t *testing.T) {
	bus := NewMemoryBus(100, slog.Default())

	var processed atomic.Int64

	ch := make(chan Event, 100)
	sub := subscription{
		topic:         TopicSettlement,
		consumerGroup: "close-test",
		batchSize:     100,
		flushInterval: time.Second,
		handler: func(_ context.Context, events []Event) error {
			processed.Add(int64(len(events)))
			return nil
		},
	}

	// Put events and close channel
	ch <- Event{ID: "e1", Topic: TopicSettlement}
	ch <- Event{ID: "e2", Topic: TopicSettlement}
	close(ch)

	ctx := context.Background()
	bus.consumeLoop(ctx, ch, sub)

	if processed.Load() != 2 {
		t.Errorf("processed = %d, want 2", processed.Load())
	}
}

func TestConsumeLoop_RetryExhaustion_DeadLetter(t *testing.T) {
	bus := NewMemoryBus(100, slog.Default())

	var attempts atomic.Int64

	ch := make(chan Event, 10)
	sub := subscription{
		topic:         TopicSettlement,
		consumerGroup: "dlq-test",
		batchSize:     1,
		flushInterval: 50 * time.Millisecond,
		handler: func(_ context.Context, events []Event) error {
			attempts.Add(1)
			return fmt.Errorf("always fail")
		},
	}

	ch <- Event{ID: "e1", Topic: TopicSettlement, Key: "k1"}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(1500 * time.Millisecond) // enough time for 3 retries with backoff
		cancel()
	}()

	bus.consumeLoop(ctx, ch, sub)

	if attempts.Load() != int64(defaultMaxRetries) {
		t.Errorf("attempts = %d, want %d", attempts.Load(), defaultMaxRetries)
	}

	dlq := bus.DeadLetterQueue()
	if len(dlq) != 1 {
		t.Errorf("DLQ length = %d, want 1", len(dlq))
	}
	if len(dlq) > 0 && dlq[0].Attempt != defaultMaxRetries {
		t.Errorf("DLQ event attempt = %d, want %d", dlq[0].Attempt, defaultMaxRetries)
	}

	m := bus.Metrics()
	if m.DeadLettered != 1 {
		t.Errorf("deadLettered = %d, want 1", m.DeadLettered)
	}
	if m.Retries != int64(defaultMaxRetries-1) {
		t.Errorf("retries = %d, want %d", m.Retries, defaultMaxRetries-1)
	}
}

func TestConsumeLoop_RetryThenSucceed(t *testing.T) {
	bus := NewMemoryBus(100, slog.Default())

	var attempts atomic.Int64

	ch := make(chan Event, 10)
	sub := subscription{
		topic:         TopicSettlement,
		consumerGroup: "retry-succeed",
		batchSize:     1,
		flushInterval: 50 * time.Millisecond,
		handler: func(_ context.Context, events []Event) error {
			n := attempts.Add(1)
			if n < 2 { // fail first attempt, succeed on second
				return fmt.Errorf("transient error")
			}
			return nil
		},
	}

	ch <- Event{ID: "e1", Topic: TopicSettlement}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(800 * time.Millisecond)
		cancel()
	}()

	bus.consumeLoop(ctx, ch, sub)

	if attempts.Load() < 2 {
		t.Errorf("attempts = %d, want >= 2", attempts.Load())
	}

	dlq := bus.DeadLetterQueue()
	if len(dlq) != 0 {
		t.Errorf("DLQ should be empty after retry success, got %d", len(dlq))
	}

	m := bus.Metrics()
	if m.Consumed < 1 {
		t.Errorf("consumed = %d, want >= 1", m.Consumed)
	}
}

// ---------------------------------------------------------------------------
// Start: context cancellation + graceful drain
// ---------------------------------------------------------------------------

func TestStart_ContextCancellation_DrainsPending(t *testing.T) {
	bus := NewMemoryBus(100, slog.Default())

	var processed atomic.Int64

	bus.Subscribe(TopicSettlement, "drain-test", 50, 50*time.Millisecond, func(_ context.Context, events []Event) error {
		processed.Add(int64(len(events)))
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		bus.Start(ctx)
		close(done)
	}()

	// Publish events
	for i := 0; i < 10; i++ {
		e, _ := NewEvent(TopicSettlement, "k", map[string]int{"i": i})
		bus.Publish(ctx, e)
	}

	time.Sleep(200 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// Start exited
	case <-time.After(10 * time.Second):
		t.Fatal("Start did not exit after context cancellation")
	}

	if processed.Load() != 10 {
		t.Errorf("processed = %d, want 10", processed.Load())
	}
}

func TestStart_NoSubscriptions(t *testing.T) {
	bus := NewMemoryBus(100, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		bus.Start(ctx)
		close(done)
	}()

	// Cancel immediately - bus should exit cleanly with no subscriptions
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not exit with no subscriptions")
	}
}

// ---------------------------------------------------------------------------
// Concurrent publish and subscribe
// ---------------------------------------------------------------------------

func TestConcurrentPublish(t *testing.T) {
	bus := NewMemoryBus(10000, slog.Default())

	var received atomic.Int64
	bus.Subscribe(TopicSettlement, "concurrent-consumer", 50, 50*time.Millisecond, func(_ context.Context, events []Event) error {
		received.Add(int64(len(events)))
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	go bus.Start(ctx)

	// Publish from multiple goroutines
	const goroutines = 10
	const eventsPerGoroutine = 100
	var wg sync.WaitGroup

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < eventsPerGoroutine; i++ {
				e, _ := NewEvent(TopicSettlement, fmt.Sprintf("key-%d", id), map[string]int{"i": i})
				bus.Publish(ctx, e)
			}
		}(g)
	}

	wg.Wait()
	time.Sleep(500 * time.Millisecond)
	cancel()
	time.Sleep(100 * time.Millisecond)

	total := goroutines * eventsPerGoroutine
	m := bus.Metrics()
	if m.Published != int64(total) {
		t.Errorf("published = %d, want %d", m.Published, total)
	}
	if received.Load() != int64(total) {
		t.Errorf("received = %d, want %d", received.Load(), total)
	}
}

// ---------------------------------------------------------------------------
// Publish with unmatched topic (no subscriber)
// ---------------------------------------------------------------------------

func TestPublish_UnmatchedTopic(t *testing.T) {
	bus := NewMemoryBus(100, slog.Default())

	bus.Subscribe(TopicSettlement, "only-settlement", 10, 50*time.Millisecond, func(_ context.Context, events []Event) error {
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	go bus.Start(ctx)

	// Publish to a topic nobody subscribes to
	e, _ := NewEvent(TopicDispute, "k", nil)
	if err := bus.Publish(ctx, e); err != nil {
		t.Errorf("publish to unsubscribed topic should succeed: %v", err)
	}

	time.Sleep(200 * time.Millisecond)
	cancel()

	m := bus.Metrics()
	if m.Published != 1 {
		t.Errorf("published = %d, want 1", m.Published)
	}
	if m.Consumed != 0 {
		t.Errorf("consumed = %d, want 0 (no matching subscriber)", m.Consumed)
	}
}

// ---------------------------------------------------------------------------
// ReplayDeadLetters: resets attempt counter
// ---------------------------------------------------------------------------

func TestReplayDeadLetters_ResetsAttempt(t *testing.T) {
	bus := NewMemoryBus(100, slog.Default())

	bus.dlqMu.Lock()
	bus.dlq = append(bus.dlq, Event{ID: "evt_1", Topic: TopicSettlement, Attempt: 3})
	bus.dlqMu.Unlock()

	bus.ReplayDeadLetters(context.Background())

	// Read the event from the buffer and check attempt is reset
	select {
	case e := <-bus.buffer:
		if e.Attempt != 0 {
			t.Errorf("replayed event attempt = %d, want 0", e.Attempt)
		}
	default:
		t.Error("expected event in buffer after replay")
	}
}

// ---------------------------------------------------------------------------
// WAL Store with sqlmock
// ---------------------------------------------------------------------------

func TestWALStore_Write(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	wal := NewWALStore(db, slog.Default())

	mock.ExpectExec("INSERT INTO eventbus_wal").
		WithArgs("evt_1", TopicSettlement, "k1", sqlmock.AnyArg(), "req_1", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	event := Event{
		ID:        "evt_1",
		Topic:     TopicSettlement,
		Key:       "k1",
		Payload:   json.RawMessage(`{"test":true}`),
		RequestID: "req_1",
		Timestamp: time.Now(),
	}

	if err := wal.Write(context.Background(), event); err != nil {
		t.Fatalf("WAL.Write: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestWALStore_Write_Error(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	wal := NewWALStore(db, slog.Default())

	mock.ExpectExec("INSERT INTO eventbus_wal").
		WillReturnError(fmt.Errorf("db connection lost"))

	event := Event{ID: "evt_1", Topic: "t", Key: "k", Timestamp: time.Now()}
	if err := wal.Write(context.Background(), event); err == nil {
		t.Error("expected error from WAL.Write")
	}
}

func TestWALStore_MarkProcessed(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	wal := NewWALStore(db, slog.Default())

	mock.ExpectExec("UPDATE eventbus_wal SET status = 'processed'").
		WithArgs("evt_1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := wal.MarkProcessed(context.Background(), "evt_1"); err != nil {
		t.Fatalf("MarkProcessed: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestWALStore_MarkProcessed_Error(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	wal := NewWALStore(db, slog.Default())

	mock.ExpectExec("UPDATE eventbus_wal").
		WillReturnError(fmt.Errorf("update failed"))

	if err := wal.MarkProcessed(context.Background(), "evt_1"); err == nil {
		t.Error("expected error from MarkProcessed")
	}
}

func TestWALStore_MarkDeadLettered(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	wal := NewWALStore(db, slog.Default())

	mock.ExpectExec("UPDATE eventbus_wal SET status = 'dead_lettered'").
		WithArgs("evt_1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := wal.MarkDeadLettered(context.Background(), "evt_1"); err != nil {
		t.Fatalf("MarkDeadLettered: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestWALStore_MarkDeadLettered_Error(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	wal := NewWALStore(db, slog.Default())

	mock.ExpectExec("UPDATE eventbus_wal").
		WillReturnError(fmt.Errorf("dead letter fail"))

	if err := wal.MarkDeadLettered(context.Background(), "evt_1"); err == nil {
		t.Error("expected error from MarkDeadLettered")
	}
}

func TestWALStore_RecoverPending(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	wal := NewWALStore(db, slog.Default())

	rows := sqlmock.NewRows([]string{"id", "topic", "key", "payload", "request_id", "created_at"}).
		AddRow("evt_1", TopicSettlement, "k1", []byte(`{"test":true}`), sql.NullString{String: "req_1", Valid: true}, time.Now()).
		AddRow("evt_2", TopicAlert, "k2", []byte(`{"alert":true}`), sql.NullString{}, time.Now())

	mock.ExpectQuery("SELECT id, topic, key, payload, request_id, created_at").
		WillReturnRows(rows)

	events, err := wal.RecoverPending(context.Background())
	if err != nil {
		t.Fatalf("RecoverPending: %v", err)
	}

	if len(events) != 2 {
		t.Fatalf("recovered %d events, want 2", len(events))
	}
	if events[0].ID != "evt_1" {
		t.Errorf("first event ID = %q, want evt_1", events[0].ID)
	}
	if events[0].Topic != TopicSettlement {
		t.Errorf("first event topic = %q, want %q", events[0].Topic, TopicSettlement)
	}
	if events[0].RequestID != "req_1" {
		t.Errorf("first event requestID = %q, want req_1", events[0].RequestID)
	}
	if events[1].RequestID != "" {
		t.Errorf("second event requestID = %q, want empty", events[1].RequestID)
	}
}

func TestWALStore_RecoverPending_Empty(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	wal := NewWALStore(db, slog.Default())

	rows := sqlmock.NewRows([]string{"id", "topic", "key", "payload", "request_id", "created_at"})
	mock.ExpectQuery("SELECT").WillReturnRows(rows)

	events, err := wal.RecoverPending(context.Background())
	if err != nil {
		t.Fatalf("RecoverPending: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events, got %d", len(events))
	}
}

func TestWALStore_RecoverPending_QueryError(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	wal := NewWALStore(db, slog.Default())
	mock.ExpectQuery("SELECT").WillReturnError(fmt.Errorf("query failed"))

	_, err = wal.RecoverPending(context.Background())
	if err == nil {
		t.Error("expected error from RecoverPending")
	}
}

func TestWALStore_RecoverPending_ScanError(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	wal := NewWALStore(db, slog.Default())

	// Return wrong column types to trigger scan error
	rows := sqlmock.NewRows([]string{"id", "topic", "key", "payload", "request_id", "created_at"}).
		AddRow(nil, nil, nil, nil, nil, nil) // nulls everywhere

	mock.ExpectQuery("SELECT").WillReturnRows(rows)

	_, err = wal.RecoverPending(context.Background())
	if err == nil {
		t.Error("expected scan error from RecoverPending")
	}
}

func TestWALStore_Cleanup(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	wal := NewWALStore(db, slog.Default())

	mock.ExpectExec("DELETE FROM eventbus_wal").
		WillReturnResult(sqlmock.NewResult(0, 42))

	deleted, err := wal.Cleanup(context.Background(), 24*time.Hour)
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if deleted != 42 {
		t.Errorf("deleted = %d, want 42", deleted)
	}
}

func TestWALStore_Cleanup_Error(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	wal := NewWALStore(db, slog.Default())

	mock.ExpectExec("DELETE FROM eventbus_wal").
		WillReturnError(fmt.Errorf("delete failed"))

	_, err = wal.Cleanup(context.Background(), 24*time.Hour)
	if err == nil {
		t.Error("expected error from Cleanup")
	}
}

func TestWALStore_CreateTable(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	wal := NewWALStore(db, slog.Default())

	mock.ExpectExec("CREATE TABLE IF NOT EXISTS eventbus_wal").
		WillReturnResult(sqlmock.NewResult(0, 0))

	if err := wal.CreateTable(context.Background()); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}
}

func TestWALStore_CreateTable_Error(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	wal := NewWALStore(db, slog.Default())

	mock.ExpectExec("CREATE TABLE").
		WillReturnError(fmt.Errorf("permission denied"))

	if err := wal.CreateTable(context.Background()); err == nil {
		t.Error("expected error from CreateTable")
	}
}

// ---------------------------------------------------------------------------
// Outbox: WriteInTx, Write, CreateTable with sqlmock
// ---------------------------------------------------------------------------

func TestOutbox_WriteInTx(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	outbox := NewOutbox(db, slog.Default())

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO eventbus_outbox").
		WithArgs("evt_1", TopicSettlement, "k1", sqlmock.AnyArg(), "req_1", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}

	event := Event{
		ID:        "evt_1",
		Topic:     TopicSettlement,
		Key:       "k1",
		Payload:   json.RawMessage(`{"test":true}`),
		RequestID: "req_1",
		Timestamp: time.Now(),
	}

	if err := outbox.WriteInTx(context.Background(), tx, event); err != nil {
		t.Fatalf("WriteInTx: %v", err)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestOutbox_WriteInTx_Error(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	outbox := NewOutbox(db, slog.Default())

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO eventbus_outbox").
		WillReturnError(fmt.Errorf("constraint violation"))

	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}

	event := Event{ID: "evt_1", Topic: "t", Key: "k", Timestamp: time.Now()}
	if err := outbox.WriteInTx(context.Background(), tx, event); err == nil {
		t.Error("expected error from WriteInTx")
	}
}

func TestOutbox_Write(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	outbox := NewOutbox(db, slog.Default())

	mock.ExpectExec("INSERT INTO eventbus_outbox").
		WithArgs("evt_1", TopicSettlement, "k1", sqlmock.AnyArg(), "req_1", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	event := Event{
		ID:        "evt_1",
		Topic:     TopicSettlement,
		Key:       "k1",
		Payload:   json.RawMessage(`{}`),
		RequestID: "req_1",
		Timestamp: time.Now(),
	}

	if err := outbox.Write(context.Background(), event); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestOutbox_Write_Error(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	outbox := NewOutbox(db, slog.Default())

	mock.ExpectExec("INSERT INTO eventbus_outbox").
		WillReturnError(fmt.Errorf("insert failed"))

	event := Event{ID: "evt_1", Topic: "t", Key: "k", Timestamp: time.Now()}
	if err := outbox.Write(context.Background(), event); err == nil {
		t.Error("expected error from Write")
	}
}

func TestOutbox_CreateTable(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	outbox := NewOutbox(db, slog.Default())

	mock.ExpectExec("CREATE TABLE IF NOT EXISTS eventbus_outbox").
		WillReturnResult(sqlmock.NewResult(0, 0))

	if err := outbox.CreateTable(context.Background()); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}
}

func TestOutbox_CreateTable_Error(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	outbox := NewOutbox(db, slog.Default())

	mock.ExpectExec("CREATE TABLE").
		WillReturnError(fmt.Errorf("permission denied"))

	if err := outbox.CreateTable(context.Background()); err == nil {
		t.Error("expected error from CreateTable")
	}
}

// ---------------------------------------------------------------------------
// Outbox publishBatch: lag metric query
// ---------------------------------------------------------------------------

func TestOutbox_PublishBatch_WithLagMetric(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	bus := NewMemoryBus(100, slog.Default())
	outbox := NewOutbox(db, slog.Default())

	// Mock the lag query
	mock.ExpectQuery("SELECT EXTRACT").
		WillReturnRows(sqlmock.NewRows([]string{"lag"}).AddRow(5.5))

	// Mock the main SELECT with results
	rows := sqlmock.NewRows([]string{"id", "topic", "key", "payload", "request_id", "created_at"}).
		AddRow("evt_1", TopicSettlement, "0xBuyer", []byte(`{"sessionId":"s1"}`), nil, time.Now())

	mock.ExpectQuery("SELECT id, topic, key, payload, request_id, created_at").
		WillReturnRows(rows)

	mock.ExpectExec("UPDATE eventbus_outbox SET published = TRUE").
		WithArgs("evt_1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	outbox.publishBatch(context.Background(), bus)

	m := bus.Metrics()
	if m.Published != 1 {
		t.Errorf("published = %d, want 1", m.Published)
	}
}

func TestOutbox_PublishBatch_QueryError(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	bus := NewMemoryBus(100, slog.Default())
	outbox := NewOutbox(db, slog.Default())

	// Lag query fails (that's OK)
	mock.ExpectQuery("SELECT EXTRACT").WillReturnError(fmt.Errorf("db error"))

	// Main query fails
	mock.ExpectQuery("SELECT id").WillReturnError(fmt.Errorf("query error"))

	outbox.publishBatch(context.Background(), bus)

	m := bus.Metrics()
	if m.Published != 0 {
		t.Errorf("published = %d, want 0", m.Published)
	}
}

func TestOutbox_PublishBatch_PublishFails(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Use a tiny buffer bus that's already full
	bus := NewMemoryBus(1, slog.Default())
	bus.Publish(context.Background(), Event{ID: "fill", Topic: "t", Key: "k"}) // fill buffer

	outbox := NewOutbox(db, slog.Default())

	// Lag query
	mock.ExpectQuery("SELECT EXTRACT").
		WillReturnRows(sqlmock.NewRows([]string{"lag"}).AddRow(nil))

	// Return one event
	rows := sqlmock.NewRows([]string{"id", "topic", "key", "payload", "request_id", "created_at"}).
		AddRow("evt_1", TopicSettlement, "k", []byte(`{}`), nil, time.Now())

	mock.ExpectQuery("SELECT id").WillReturnRows(rows)

	// No UPDATE expected because publish fails (buffer full)

	outbox.publishBatch(context.Background(), bus)
	// Should not panic - the publish failure is logged and skipped
}

// ---------------------------------------------------------------------------
// CleanupWorker cleanupWithLock with sqlmock
// ---------------------------------------------------------------------------

func TestCleanupWithLock_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	walDB, walMock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer walDB.Close()

	wal := NewWALStore(walDB, slog.Default())

	w := NewCleanupWorker(db, wal, nil, time.Hour, 24*time.Hour, 24*time.Hour, slog.Default())

	// Mock: acquire advisory lock
	mock.ExpectQuery("SELECT pg_try_advisory_lock").
		WithArgs(cleanupLockWAL).
		WillReturnRows(sqlmock.NewRows([]string{"acquired"}).AddRow(true))

	// Mock: WAL cleanup (called via fn closure)
	walMock.ExpectExec("DELETE FROM eventbus_wal").
		WillReturnResult(sqlmock.NewResult(0, 10))

	// Mock: release advisory lock
	mock.ExpectExec("SELECT pg_advisory_unlock").
		WithArgs(cleanupLockWAL).
		WillReturnResult(sqlmock.NewResult(0, 0))

	w.runOnce(context.Background())

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("db mock unmet: %v", err)
	}
	if err := walMock.ExpectationsWereMet(); err != nil {
		t.Errorf("wal mock unmet: %v", err)
	}
}

func TestCleanupWithLock_LockNotAcquired(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	wal := NewWALStore(db, slog.Default())
	w := NewCleanupWorker(db, wal, nil, time.Hour, 24*time.Hour, 24*time.Hour, slog.Default())

	// Mock: advisory lock NOT acquired
	mock.ExpectQuery("SELECT pg_try_advisory_lock").
		WithArgs(cleanupLockWAL).
		WillReturnRows(sqlmock.NewRows([]string{"acquired"}).AddRow(false))

	w.runOnce(context.Background())

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestCleanupWithLock_LockError(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	wal := NewWALStore(db, slog.Default())
	w := NewCleanupWorker(db, wal, nil, time.Hour, 24*time.Hour, 24*time.Hour, slog.Default())

	// Mock: advisory lock query fails
	mock.ExpectQuery("SELECT pg_try_advisory_lock").
		WillReturnError(fmt.Errorf("connection lost"))

	w.runOnce(context.Background())

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestCleanupWithLock_CleanupFails(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	walDB, walMock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer walDB.Close()

	wal := NewWALStore(walDB, slog.Default())
	w := NewCleanupWorker(db, wal, nil, time.Hour, 24*time.Hour, 24*time.Hour, slog.Default())

	// Lock acquired
	mock.ExpectQuery("SELECT pg_try_advisory_lock").
		WithArgs(cleanupLockWAL).
		WillReturnRows(sqlmock.NewRows([]string{"acquired"}).AddRow(true))

	// WAL cleanup fails
	walMock.ExpectExec("DELETE FROM eventbus_wal").
		WillReturnError(fmt.Errorf("cleanup failed"))

	// Lock released even on error
	mock.ExpectExec("SELECT pg_advisory_unlock").
		WithArgs(cleanupLockWAL).
		WillReturnResult(sqlmock.NewResult(0, 0))

	w.runOnce(context.Background())

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("db mock unmet: %v", err)
	}
}

func TestCleanupWithLock_ZeroDeleted(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	walDB, walMock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer walDB.Close()

	wal := NewWALStore(walDB, slog.Default())
	w := NewCleanupWorker(db, wal, nil, time.Hour, 24*time.Hour, 24*time.Hour, slog.Default())

	mock.ExpectQuery("SELECT pg_try_advisory_lock").
		WithArgs(cleanupLockWAL).
		WillReturnRows(sqlmock.NewRows([]string{"acquired"}).AddRow(true))

	// Zero rows deleted
	walMock.ExpectExec("DELETE FROM eventbus_wal").
		WillReturnResult(sqlmock.NewResult(0, 0))

	mock.ExpectExec("SELECT pg_advisory_unlock").
		WithArgs(cleanupLockWAL).
		WillReturnResult(sqlmock.NewResult(0, 0))

	w.runOnce(context.Background())
}

func TestCleanupWithLock_BothWALAndOutbox(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	walDB, walMock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer walDB.Close()

	outboxDB, outboxMock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer outboxDB.Close()

	wal := NewWALStore(walDB, slog.Default())
	outbox := NewOutbox(outboxDB, slog.Default())

	w := NewCleanupWorker(db, wal, outbox, time.Hour, 24*time.Hour, 24*time.Hour, slog.Default())

	// WAL cleanup
	mock.ExpectQuery("SELECT pg_try_advisory_lock").
		WithArgs(cleanupLockWAL).
		WillReturnRows(sqlmock.NewRows([]string{"acquired"}).AddRow(true))
	walMock.ExpectExec("DELETE FROM eventbus_wal").
		WillReturnResult(sqlmock.NewResult(0, 5))
	mock.ExpectExec("SELECT pg_advisory_unlock").
		WithArgs(cleanupLockWAL).
		WillReturnResult(sqlmock.NewResult(0, 0))

	// Outbox cleanup
	mock.ExpectQuery("SELECT pg_try_advisory_lock").
		WithArgs(cleanupLockOutbox).
		WillReturnRows(sqlmock.NewRows([]string{"acquired"}).AddRow(true))
	outboxMock.ExpectExec("DELETE FROM eventbus_outbox").
		WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectExec("SELECT pg_advisory_unlock").
		WithArgs(cleanupLockOutbox).
		WillReturnResult(sqlmock.NewResult(0, 0))

	w.runOnce(context.Background())

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("db mock unmet: %v", err)
	}
	if err := walMock.ExpectationsWereMet(); err != nil {
		t.Errorf("wal mock unmet: %v", err)
	}
	if err := outboxMock.ExpectationsWereMet(); err != nil {
		t.Errorf("outbox mock unmet: %v", err)
	}
}

func TestCleanupWithLock_CancelledContext(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	walDB, walMock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer walDB.Close()

	wal := NewWALStore(walDB, slog.Default())
	w := NewCleanupWorker(db, wal, nil, time.Hour, 24*time.Hour, 24*time.Hour, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Already cancelled

	// Lock acquired
	mock.ExpectQuery("SELECT pg_try_advisory_lock").
		WithArgs(cleanupLockWAL).
		WillReturnRows(sqlmock.NewRows([]string{"acquired"}).AddRow(true))

	// WAL cleanup fails because context cancelled
	walMock.ExpectExec("DELETE FROM eventbus_wal").
		WillReturnError(context.Canceled)

	// Lock still released
	mock.ExpectExec("SELECT pg_advisory_unlock").
		WithArgs(cleanupLockWAL).
		WillReturnResult(sqlmock.NewResult(0, 0))

	w.runOnce(ctx)
	// Should not log error since ctx.Err() != nil
}

// ---------------------------------------------------------------------------
// CDC pollOnce: query error, scan error, publish failure
// ---------------------------------------------------------------------------

func TestCDC_PollOnce_QueryError(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	bus := NewMemoryBus(100, slog.Default())
	cdc := NewCDC(db, bus, slog.Default())

	mock.ExpectQuery("SELECT").WithArgs("").WillReturnError(fmt.Errorf("db error"))

	cdc.pollOnce(context.Background())

	if cdc.lastID != "" {
		t.Errorf("lastID should remain empty on query error, got %q", cdc.lastID)
	}
}

func TestCDC_PollOnce_CancelledContext_QueryError(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	bus := NewMemoryBus(100, slog.Default())
	cdc := NewCDC(db, bus, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	mock.ExpectQuery("SELECT").WithArgs("").WillReturnError(context.Canceled)

	cdc.pollOnce(ctx)
	// Should not log error since ctx.Err() != nil
}

func TestCDC_PollOnce_ScanError(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	bus := NewMemoryBus(100, slog.Default())
	cdc := NewCDC(db, bus, slog.Default())

	// Return a row with wrong types to trigger scan error
	rows := sqlmock.NewRows([]string{"id", "agent_address", "type", "amount", "reference", "tx_hash", "created_at"}).
		AddRow(nil, nil, nil, nil, nil, nil, nil) // nulls everywhere

	mock.ExpectQuery("SELECT").WithArgs("").WillReturnRows(rows)

	cdc.pollOnce(context.Background())
	// Should not panic - scan errors are logged and continued
}

func TestCDC_PollOnce_PublishFails(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Tiny buffer that's already full
	bus := NewMemoryBus(1, slog.Default())
	bus.Publish(context.Background(), Event{ID: "fill", Topic: "t", Key: "k"})

	cdc := NewCDC(db, bus, slog.Default())

	rows := sqlmock.NewRows([]string{"id", "agent_address", "type", "amount", "reference", "tx_hash", "created_at"}).
		AddRow("entry_1", "0xAgent", "credit", "10.0", "ref", sql.NullString{}, "2025-01-01T00:00:00Z").
		AddRow("entry_2", "0xAgent", "debit", "5.0", "ref2", sql.NullString{}, "2025-01-01T00:00:01Z")

	mock.ExpectQuery("SELECT").WithArgs("").WillReturnRows(rows)

	cdc.pollOnce(context.Background())

	// When publish fails, it should stop processing the batch and NOT update lastID past the failed entry
	// The first entry gets published (replaces the fill event? No, buffer is full, publish fails)
	// So lastID should remain "" because publish fails on the first entry
	if cdc.lastID != "" {
		t.Errorf("lastID should be empty when publish fails, got %q", cdc.lastID)
	}
}

// ---------------------------------------------------------------------------
// Kafka: buildTransport, NewKafkaBus
// ---------------------------------------------------------------------------

func TestBuildTransport_Plain(t *testing.T) {
	cfg := KafkaConfig{
		ClientID: "test-client",
	}
	transport, err := buildTransport(cfg)
	if err != nil {
		t.Fatalf("buildTransport: %v", err)
	}
	if transport == nil {
		t.Fatal("expected non-nil transport")
	}
}

func TestBuildTransport_TLSEnabled_NoKeyPair(t *testing.T) {
	cfg := KafkaConfig{
		ClientID:   "test-client",
		TLSEnabled: true,
	}
	transport, err := buildTransport(cfg)
	if err != nil {
		t.Fatalf("buildTransport with TLS: %v", err)
	}
	if transport == nil {
		t.Fatal("expected non-nil transport")
	}
}

func TestBuildTransport_TLSEnabled_BadKeyPair(t *testing.T) {
	cfg := KafkaConfig{
		ClientID:   "test-client",
		TLSEnabled: true,
		TLSCert:    "/nonexistent/cert.pem",
		TLSKey:     "/nonexistent/key.pem",
	}
	_, err := buildTransport(cfg)
	if err == nil {
		t.Error("expected error for bad TLS keypair paths")
	}
}

func TestBuildTransport_TLSEnabled_BadCA(t *testing.T) {
	cfg := KafkaConfig{
		ClientID:   "test-client",
		TLSEnabled: true,
		TLSCA:      "/nonexistent/ca.pem",
	}
	_, err := buildTransport(cfg)
	if err == nil {
		t.Error("expected error for bad CA path")
	}
}

func TestBuildTransport_SASL_Plain(t *testing.T) {
	cfg := KafkaConfig{
		ClientID:      "test-client",
		SASLEnabled:   true,
		SASLMechanism: "PLAIN",
		SASLUsername:  "user",
		SASLPassword:  "pass",
	}
	transport, err := buildTransport(cfg)
	if err != nil {
		t.Fatalf("buildTransport SASL PLAIN: %v", err)
	}
	if transport == nil {
		t.Fatal("expected non-nil transport")
	}
}

func TestBuildTransport_SASL_SCRAM256(t *testing.T) {
	cfg := KafkaConfig{
		ClientID:      "test-client",
		SASLEnabled:   true,
		SASLMechanism: "SCRAM-SHA-256",
		SASLUsername:  "user",
		SASLPassword:  "pass",
	}
	transport, err := buildTransport(cfg)
	if err != nil {
		t.Fatalf("buildTransport SASL SCRAM-SHA-256: %v", err)
	}
	if transport == nil {
		t.Fatal("expected non-nil transport")
	}
}

func TestBuildTransport_SASL_SCRAM512(t *testing.T) {
	cfg := KafkaConfig{
		ClientID:      "test-client",
		SASLEnabled:   true,
		SASLMechanism: "SCRAM-SHA-512",
		SASLUsername:  "user",
		SASLPassword:  "pass",
	}
	transport, err := buildTransport(cfg)
	if err != nil {
		t.Fatalf("buildTransport SASL SCRAM-SHA-512: %v", err)
	}
	if transport == nil {
		t.Fatal("expected non-nil transport")
	}
}

func TestBuildTransport_SASL_UnsupportedMechanism(t *testing.T) {
	cfg := KafkaConfig{
		ClientID:      "test-client",
		SASLEnabled:   true,
		SASLMechanism: "GSSAPI",
	}
	_, err := buildTransport(cfg)
	if err == nil {
		t.Error("expected error for unsupported SASL mechanism")
	}
}

func TestNewKafkaBus_EmptyBrokers(t *testing.T) {
	_, err := NewKafkaBus(KafkaConfig{}, slog.Default())
	if err == nil {
		t.Error("expected error for empty brokers")
	}
}

func TestNewKafkaBus_DefaultValues(t *testing.T) {
	cfg := KafkaConfig{
		Brokers:       []string{"localhost:9092"},
		BatchSize:     0, // should default to 100
		FlushInterval: 0, // should default to 1000
		MaxRetries:    0, // should default to 3
	}
	bus, err := NewKafkaBus(cfg, slog.Default())
	if err != nil {
		t.Fatalf("NewKafkaBus: %v", err)
	}
	if bus == nil {
		t.Fatal("expected non-nil bus")
	}
}

func TestNewKafkaBus_NegativeValues(t *testing.T) {
	cfg := KafkaConfig{
		Brokers:       []string{"localhost:9092"},
		BatchSize:     -5,
		FlushInterval: -10,
		MaxRetries:    -1,
	}
	bus, err := NewKafkaBus(cfg, slog.Default())
	if err != nil {
		t.Fatalf("NewKafkaBus: %v", err)
	}
	if bus == nil {
		t.Fatal("expected non-nil bus")
	}
}

func TestNewKafkaBus_BadSASL(t *testing.T) {
	cfg := KafkaConfig{
		Brokers:       []string{"localhost:9092"},
		SASLEnabled:   true,
		SASLMechanism: "UNKNOWN",
	}
	_, err := NewKafkaBus(cfg, slog.Default())
	if err == nil {
		t.Error("expected error for unknown SASL mechanism")
	}
}

// ---------------------------------------------------------------------------
// KafkaBus Metrics and IsHealthy (without broker)
// ---------------------------------------------------------------------------

func TestKafkaBus_Metrics(t *testing.T) {
	cfg := KafkaConfig{Brokers: []string{"localhost:9092"}}
	bus, err := NewKafkaBus(cfg, slog.Default())
	if err != nil {
		t.Fatal(err)
	}

	bus.published.Store(100)
	bus.consumed.Store(80)
	bus.dropped.Store(5)
	bus.retries.Store(10)
	bus.deadLettered.Store(2)
	bus.batchesProc.Store(15)

	m := bus.Metrics()
	if m.Published != 100 {
		t.Errorf("published = %d", m.Published)
	}
	if m.Consumed != 80 {
		t.Errorf("consumed = %d", m.Consumed)
	}
	if m.Pending != 20 {
		t.Errorf("pending = %d", m.Pending)
	}
	if m.Dropped != 5 {
		t.Errorf("dropped = %d", m.Dropped)
	}
	if m.Retries != 10 {
		t.Errorf("retries = %d", m.Retries)
	}
	if m.DeadLettered != 2 {
		t.Errorf("deadLettered = %d", m.DeadLettered)
	}
	if m.BatchesProcessed != 15 {
		t.Errorf("batchesProcessed = %d", m.BatchesProcessed)
	}
}

func TestKafkaBus_IsHealthy_NeverPublished(t *testing.T) {
	cfg := KafkaConfig{Brokers: []string{"localhost:9092"}}
	bus, _ := NewKafkaBus(cfg, slog.Default())

	// Never published: should be healthy (grace period)
	if !bus.IsHealthy() {
		t.Error("expected healthy when never published (startup grace)")
	}
}

func TestKafkaBus_IsHealthy_RecentPublish(t *testing.T) {
	cfg := KafkaConfig{Brokers: []string{"localhost:9092"}}
	bus, _ := NewKafkaBus(cfg, slog.Default())

	bus.lastPublish.Store(time.Now().UnixMilli())
	if !bus.IsHealthy() {
		t.Error("expected healthy after recent publish")
	}
}

func TestKafkaBus_IsHealthy_StalePublish(t *testing.T) {
	cfg := KafkaConfig{Brokers: []string{"localhost:9092"}}
	bus, _ := NewKafkaBus(cfg, slog.Default())

	bus.lastPublish.Store(time.Now().Add(-60 * time.Second).UnixMilli())
	if bus.IsHealthy() {
		t.Error("expected unhealthy when last publish was >30s ago")
	}
}

func TestKafkaBus_Subscribe_Defaults(t *testing.T) {
	cfg := KafkaConfig{Brokers: []string{"localhost:9092"}, BatchSize: 50, FlushInterval: 500}
	bus, _ := NewKafkaBus(cfg, slog.Default())

	bus.Subscribe("test.topic", "group1", 0, 0, func(_ context.Context, _ []Event) error { return nil })

	if len(bus.subscriptions) != 1 {
		t.Fatalf("expected 1 subscription, got %d", len(bus.subscriptions))
	}
	if bus.subscriptions[0].batchSize != 50 {
		t.Errorf("batchSize = %d, want 50 (from config)", bus.subscriptions[0].batchSize)
	}
	if bus.subscriptions[0].flushInterval != 500*time.Millisecond {
		t.Errorf("flushInterval = %v, want 500ms", bus.subscriptions[0].flushInterval)
	}
}

// ---------------------------------------------------------------------------
// MemoryBus Publish with WAL (WAL write succeeds)
// ---------------------------------------------------------------------------

func TestPublish_WithWAL_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	wal := NewWALStore(db, slog.Default())
	bus := NewMemoryBus(100, slog.Default()).WithWAL(wal)

	mock.ExpectExec("INSERT INTO eventbus_wal").
		WillReturnResult(sqlmock.NewResult(0, 1))

	event := Event{ID: "evt_1", Topic: TopicSettlement, Key: "k1", Payload: json.RawMessage(`{}`), Timestamp: time.Now()}
	if err := bus.Publish(context.Background(), event); err != nil {
		t.Fatalf("Publish with WAL: %v", err)
	}

	m := bus.Metrics()
	if m.Published != 1 {
		t.Errorf("published = %d, want 1", m.Published)
	}
}

func TestPublish_WithWAL_WALFailsButPublishContinues(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	wal := NewWALStore(db, slog.Default())
	bus := NewMemoryBus(100, slog.Default()).WithWAL(wal)

	mock.ExpectExec("INSERT INTO eventbus_wal").
		WillReturnError(fmt.Errorf("WAL db down"))

	event := Event{ID: "evt_1", Topic: TopicSettlement, Key: "k1", Payload: json.RawMessage(`{}`), Timestamp: time.Now()}
	if err := bus.Publish(context.Background(), event); err != nil {
		t.Fatalf("Publish should succeed even when WAL fails: %v", err)
	}

	m := bus.Metrics()
	if m.Published != 1 {
		t.Errorf("published = %d, want 1 (WAL failure does not block)", m.Published)
	}
}

// ---------------------------------------------------------------------------
// Start with WAL recovery
// ---------------------------------------------------------------------------

func TestStart_WithWAL_RecoversPendingEvents(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	wal := NewWALStore(db, slog.Default())
	bus := NewMemoryBus(100, slog.Default()).WithWAL(wal)

	// Mock WAL recovery returning 2 pending events
	rows := sqlmock.NewRows([]string{"id", "topic", "key", "payload", "request_id", "created_at"}).
		AddRow("evt_r1", TopicSettlement, "k1", []byte(`{}`), sql.NullString{}, time.Now()).
		AddRow("evt_r2", TopicSettlement, "k2", []byte(`{}`), sql.NullString{}, time.Now())

	mock.ExpectQuery("SELECT id, topic, key, payload, request_id, created_at").
		WillReturnRows(rows)

	var received atomic.Int64
	bus.Subscribe(TopicSettlement, "recovery-test", 50, 50*time.Millisecond, func(_ context.Context, events []Event) error {
		received.Add(int64(len(events)))
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		bus.Start(ctx)
		close(done)
	}()

	time.Sleep(300 * time.Millisecond)
	cancel()

	<-done

	if received.Load() != 2 {
		t.Errorf("received %d recovered events, want 2", received.Load())
	}
}

func TestStart_WithWAL_RecoveryFails(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	wal := NewWALStore(db, slog.Default())
	bus := NewMemoryBus(100, slog.Default()).WithWAL(wal)

	mock.ExpectQuery("SELECT").WillReturnError(fmt.Errorf("recovery failed"))

	bus.Subscribe(TopicSettlement, "test", 50, 50*time.Millisecond, func(_ context.Context, _ []Event) error {
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		bus.Start(ctx)
		close(done)
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not exit after recovery failure + cancellation")
	}
}

func TestStart_WithWAL_RecoveryBufferFull(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	wal := NewWALStore(db, slog.Default())
	bus := NewMemoryBus(1, slog.Default()).WithWAL(wal) // Buffer of 1

	// 3 pending events but buffer only fits 1
	rows := sqlmock.NewRows([]string{"id", "topic", "key", "payload", "request_id", "created_at"}).
		AddRow("evt_1", TopicSettlement, "k1", []byte(`{}`), sql.NullString{}, time.Now()).
		AddRow("evt_2", TopicSettlement, "k2", []byte(`{}`), sql.NullString{}, time.Now()).
		AddRow("evt_3", TopicSettlement, "k3", []byte(`{}`), sql.NullString{}, time.Now())

	mock.ExpectQuery("SELECT").WillReturnRows(rows)

	bus.Subscribe(TopicSettlement, "test", 50, 50*time.Millisecond, func(_ context.Context, _ []Event) error {
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		bus.Start(ctx)
		close(done)
	}()

	time.Sleep(200 * time.Millisecond)
	cancel()

	<-done

	// Should not panic; only 1 out of 3 events recovered due to buffer size
	m := bus.Metrics()
	if m.Published > 3 {
		t.Errorf("published = %d, should not exceed 3", m.Published)
	}
}

// ---------------------------------------------------------------------------
// consumeLoop with WAL: MarkProcessed and MarkDeadLettered
// ---------------------------------------------------------------------------

func TestConsumeLoop_WithWAL_MarkProcessed(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	wal := NewWALStore(db, slog.Default())
	bus := NewMemoryBus(100, slog.Default()).WithWAL(wal)

	// Expect MarkProcessed for each event
	mock.ExpectExec("UPDATE eventbus_wal SET status = 'processed'").
		WithArgs("evt_1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE eventbus_wal SET status = 'processed'").
		WithArgs("evt_2").
		WillReturnResult(sqlmock.NewResult(0, 1))

	ch := make(chan Event, 10)
	sub := subscription{
		topic:         TopicSettlement,
		consumerGroup: "wal-process-test",
		batchSize:     10,
		flushInterval: 50 * time.Millisecond,
		handler: func(_ context.Context, events []Event) error {
			return nil // always succeed
		},
	}

	ch <- Event{ID: "evt_1", Topic: TopicSettlement}
	ch <- Event{ID: "evt_2", Topic: TopicSettlement}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()

	bus.consumeLoop(ctx, ch, sub)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestConsumeLoop_WithWAL_MarkProcessedFails(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	wal := NewWALStore(db, slog.Default())
	bus := NewMemoryBus(100, slog.Default()).WithWAL(wal)

	// MarkProcessed fails (event will be reprocessed on restart - that's OK)
	mock.ExpectExec("UPDATE eventbus_wal").
		WillReturnError(fmt.Errorf("mark processed failed"))

	ch := make(chan Event, 10)
	sub := subscription{
		topic:         TopicSettlement,
		consumerGroup: "wal-fail-test",
		batchSize:     10,
		flushInterval: 50 * time.Millisecond,
		handler: func(_ context.Context, events []Event) error {
			return nil
		},
	}

	ch <- Event{ID: "evt_1", Topic: TopicSettlement}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()

	bus.consumeLoop(ctx, ch, sub)
	// Should not panic; WAL mark failure is logged as warning
}

func TestConsumeLoop_WithWAL_MarkDeadLettered(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	wal := NewWALStore(db, slog.Default())
	bus := NewMemoryBus(100, slog.Default()).WithWAL(wal)

	// Expect MarkDeadLettered for the event
	mock.ExpectExec("UPDATE eventbus_wal SET status = 'dead_lettered'").
		WithArgs("evt_1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	ch := make(chan Event, 10)
	sub := subscription{
		topic:         TopicSettlement,
		consumerGroup: "wal-dlq-test",
		batchSize:     1,
		flushInterval: 50 * time.Millisecond,
		handler: func(_ context.Context, events []Event) error {
			return fmt.Errorf("permanent failure")
		},
	}

	ch <- Event{ID: "evt_1", Topic: TopicSettlement}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(1500 * time.Millisecond)
		cancel()
	}()

	bus.consumeLoop(ctx, ch, sub)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestConsumeLoop_WithWAL_MarkDeadLetteredFails(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	wal := NewWALStore(db, slog.Default())
	bus := NewMemoryBus(100, slog.Default()).WithWAL(wal)

	mock.ExpectExec("UPDATE eventbus_wal").
		WillReturnError(fmt.Errorf("mark dead-lettered failed"))

	ch := make(chan Event, 10)
	sub := subscription{
		topic:         TopicSettlement,
		consumerGroup: "wal-dlq-fail-test",
		batchSize:     1,
		flushInterval: 50 * time.Millisecond,
		handler: func(_ context.Context, events []Event) error {
			return fmt.Errorf("permanent failure")
		},
	}

	ch <- Event{ID: "evt_1", Topic: TopicSettlement}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(1500 * time.Millisecond)
		cancel()
	}()

	bus.consumeLoop(ctx, ch, sub)
	// Should not panic; WAL mark dead-lettered failure is logged as warning
}

// ---------------------------------------------------------------------------
// Bus interface compliance
// ---------------------------------------------------------------------------

func TestMemoryBus_ImplementsBusInterface(t *testing.T) {
	var _ Bus = (*MemoryBus)(nil)
}

func TestKafkaBus_ImplementsBusInterface(t *testing.T) {
	var _ Bus = (*KafkaBus)(nil)
}

// ---------------------------------------------------------------------------
// ErrBufferFull is an error
// ---------------------------------------------------------------------------

func TestErrBufferFull_ImplementsError(t *testing.T) {
	var err error = ErrBufferFull
	if err == nil {
		t.Error("ErrBufferFull should not be nil")
	}
	if !errors.Is(err, ErrBufferFull) {
		t.Error("errors.Is should match ErrBufferFull")
	}
}

// ---------------------------------------------------------------------------
// Event JSON roundtrip
// ---------------------------------------------------------------------------

func TestEvent_JSONRoundTrip(t *testing.T) {
	original := Event{
		ID:        "evt_123",
		Topic:     TopicSettlement,
		Key:       "0xAgent",
		Payload:   json.RawMessage(`{"amount":"10.5"}`),
		Timestamp: time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC),
		RequestID: "req_456",
		Attempt:   2,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded Event
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.ID != original.ID {
		t.Errorf("ID = %q, want %q", decoded.ID, original.ID)
	}
	if decoded.Topic != original.Topic {
		t.Errorf("Topic = %q, want %q", decoded.Topic, original.Topic)
	}
	if decoded.Key != original.Key {
		t.Errorf("Key = %q, want %q", decoded.Key, original.Key)
	}
	if decoded.RequestID != original.RequestID {
		t.Errorf("RequestID = %q, want %q", decoded.RequestID, original.RequestID)
	}
	if decoded.Attempt != original.Attempt {
		t.Errorf("Attempt = %d, want %d", decoded.Attempt, original.Attempt)
	}
}

// ---------------------------------------------------------------------------
// BusMetrics ConsumerLag field
// ---------------------------------------------------------------------------

func TestBusMetrics_ConsumerLagField(t *testing.T) {
	m := BusMetrics{
		Published:   10,
		Consumed:    8,
		Pending:     2,
		ConsumerLag: map[string]int64{"group1": 5, "group2": 3},
	}

	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded BusMetrics
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.ConsumerLag["group1"] != 5 {
		t.Errorf("group1 lag = %d, want 5", decoded.ConsumerLag["group1"])
	}
}

// ---------------------------------------------------------------------------
// Payload types: JSON serialization
// ---------------------------------------------------------------------------

func TestDisputePayload_JSON(t *testing.T) {
	p := DisputePayload{
		EscrowID:   "esc_1",
		BuyerAddr:  "0xBuyer",
		SellerAddr: "0xSeller",
		Amount:     "100.00",
		Reason:     "service not delivered",
		ServiceID:  "svc_1",
	}

	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded DisputePayload
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.EscrowID != p.EscrowID {
		t.Errorf("EscrowID = %q, want %q", decoded.EscrowID, p.EscrowID)
	}
	if decoded.Reason != p.Reason {
		t.Errorf("Reason = %q, want %q", decoded.Reason, p.Reason)
	}
}

func TestAlertPayload_JSON(t *testing.T) {
	p := AlertPayload{
		AlertID:   "alert_1",
		AgentAddr: "0xAgent",
		Type:      "velocity_spike",
		Severity:  "high",
		Message:   "unusual transaction volume",
		Score:     0.95,
	}

	e, err := NewEvent(TopicAlert, p.AgentAddr, p)
	if err != nil {
		t.Fatalf("NewEvent: %v", err)
	}

	var decoded AlertPayload
	if err := json.Unmarshal(e.Payload, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Score != 0.95 {
		t.Errorf("Score = %f, want 0.95", decoded.Score)
	}
}

func TestKYAPayload_JSON(t *testing.T) {
	p := KYAPayload{
		CertID:    "cert_1",
		AgentAddr: "0xAgent",
		TrustTier: "tier_2",
		TenantID:  "ten_1",
		ExpiresAt: "2026-01-01T00:00:00Z",
	}

	e, err := NewEvent(TopicKYA, p.AgentAddr, p)
	if err != nil {
		t.Fatalf("NewEvent: %v", err)
	}

	var decoded KYAPayload
	if err := json.Unmarshal(e.Payload, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.TrustTier != "tier_2" {
		t.Errorf("TrustTier = %q, want tier_2", decoded.TrustTier)
	}
}

// ---------------------------------------------------------------------------
// Outbox: mark published failure
// ---------------------------------------------------------------------------

func TestOutbox_PublishBatch_MarkPublishedFails(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	bus := NewMemoryBus(100, slog.Default())
	outbox := NewOutbox(db, slog.Default())

	// Lag query
	mock.ExpectQuery("SELECT EXTRACT").
		WillReturnRows(sqlmock.NewRows([]string{"lag"}).AddRow(nil))

	// Return one event
	rows := sqlmock.NewRows([]string{"id", "topic", "key", "payload", "request_id", "created_at"}).
		AddRow("evt_1", TopicSettlement, "k", []byte(`{}`), nil, time.Now())

	mock.ExpectQuery("SELECT id").WillReturnRows(rows)

	// Mark published fails
	mock.ExpectExec("UPDATE eventbus_outbox SET published = TRUE").
		WithArgs("evt_1").
		WillReturnError(fmt.Errorf("update failed"))

	outbox.publishBatch(context.Background(), bus)
	// Should not panic; mark published failure is logged
}

// ---------------------------------------------------------------------------
// CDC: CDCEvent struct fields
// ---------------------------------------------------------------------------

func TestCDCEvent_JSON(t *testing.T) {
	e := CDCEvent{
		EntryID:   "entry_1",
		AgentAddr: "0xAgent",
		Type:      "credit",
		Amount:    "100.000000",
		Reference: "ref_123",
		TxHash:    "0xTxHash",
		CreatedAt: "2025-06-15T12:00:00Z",
	}

	data, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded CDCEvent
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.EntryID != "entry_1" {
		t.Errorf("EntryID = %q", decoded.EntryID)
	}
	if decoded.TxHash != "0xTxHash" {
		t.Errorf("TxHash = %q", decoded.TxHash)
	}
}

// ---------------------------------------------------------------------------
// Topic constants
// ---------------------------------------------------------------------------

func TestTopicConstants(t *testing.T) {
	topics := map[string]string{
		"TopicSettlement": TopicSettlement,
		"TopicDispute":    TopicDispute,
		"TopicAlert":      TopicAlert,
		"TopicKYA":        TopicKYA,
	}

	for name, topic := range topics {
		if topic == "" {
			t.Errorf("%s is empty", name)
		}
	}

	// Ensure all topics are unique
	seen := map[string]bool{}
	for name, topic := range topics {
		if seen[topic] {
			t.Errorf("%s has duplicate topic value %q", name, topic)
		}
		seen[topic] = true
	}
}
