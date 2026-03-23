package eventbus

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
)

func TestPublishAndConsume(t *testing.T) {
	bus := NewMemoryBus(100, slog.Default())

	var received atomic.Int64
	bus.Subscribe(TopicSettlement, "test-consumer", 10, 100*time.Millisecond, func(_ context.Context, events []Event) error {
		received.Add(int64(len(events)))
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	go bus.Start(ctx)

	// Publish 50 events
	for i := 0; i < 50; i++ {
		event, _ := NewEvent(TopicSettlement, "0xAgent", SettlementPayload{
			BuyerAddr:  "0xBuyer",
			SellerAddr: "0xSeller",
			Amount:     "1.00",
		})
		if err := bus.Publish(ctx, event); err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
	}

	// Wait for consumption
	time.Sleep(500 * time.Millisecond)
	cancel()

	got := received.Load()
	if got != 50 {
		t.Errorf("consumed = %d, want 50", got)
	}
}

func TestBatchFlush(t *testing.T) {
	bus := NewMemoryBus(100, slog.Default())

	var batchCount atomic.Int64
	bus.Subscribe(TopicSettlement, "batch-consumer", 10, 50*time.Millisecond, func(_ context.Context, events []Event) error {
		batchCount.Add(1)
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	go bus.Start(ctx)

	// Publish 25 events — should flush in 3 batches (10, 10, 5)
	for i := 0; i < 25; i++ {
		event, _ := NewEvent(TopicSettlement, "0xAgent", map[string]string{"i": "val"})
		bus.Publish(ctx, event)
	}

	time.Sleep(300 * time.Millisecond)
	cancel()

	batches := batchCount.Load()
	if batches < 2 || batches > 5 {
		t.Errorf("batches = %d, want 2-5", batches)
	}
}

func TestTopicRouting(t *testing.T) {
	bus := NewMemoryBus(100, slog.Default())

	var settlementCount, alertCount atomic.Int64

	bus.Subscribe(TopicSettlement, "settlement-handler", 50, 100*time.Millisecond, func(_ context.Context, events []Event) error {
		settlementCount.Add(int64(len(events)))
		return nil
	})
	bus.Subscribe(TopicAlert, "alert-handler", 50, 100*time.Millisecond, func(_ context.Context, events []Event) error {
		alertCount.Add(int64(len(events)))
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	go bus.Start(ctx)

	// Publish to different topics
	for i := 0; i < 10; i++ {
		e1, _ := NewEvent(TopicSettlement, "0xA", nil)
		bus.Publish(ctx, e1)
	}
	for i := 0; i < 5; i++ {
		e2, _ := NewEvent(TopicAlert, "0xB", nil)
		bus.Publish(ctx, e2)
	}

	time.Sleep(300 * time.Millisecond)
	cancel()

	if settlementCount.Load() != 10 {
		t.Errorf("settlement events = %d, want 10", settlementCount.Load())
	}
	if alertCount.Load() != 5 {
		t.Errorf("alert events = %d, want 5", alertCount.Load())
	}
}

func TestBackpressure(t *testing.T) {
	bus := NewMemoryBus(5, slog.Default()) // Very small buffer

	// Don't start consumers — buffer should fill up
	ctx := context.Background()

	// Fill the buffer
	for i := 0; i < 5; i++ {
		event, _ := NewEvent(TopicSettlement, "0xA", nil)
		if err := bus.Publish(ctx, event); err != nil {
			t.Fatalf("publish %d should succeed: %v", i, err)
		}
	}

	// 6th publish should fail (buffer full)
	event, _ := NewEvent(TopicSettlement, "0xA", nil)
	err := bus.Publish(ctx, event)
	if err != ErrBufferFull {
		t.Errorf("expected ErrBufferFull, got %v", err)
	}
}

func TestMultipleConsumersSameTopic(t *testing.T) {
	bus := NewMemoryBus(100, slog.Default())

	var count1, count2 atomic.Int64

	bus.Subscribe(TopicSettlement, "consumer-1", 50, 100*time.Millisecond, func(_ context.Context, events []Event) error {
		count1.Add(int64(len(events)))
		return nil
	})
	bus.Subscribe(TopicSettlement, "consumer-2", 50, 100*time.Millisecond, func(_ context.Context, events []Event) error {
		count2.Add(int64(len(events)))
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	go bus.Start(ctx)

	for i := 0; i < 10; i++ {
		event, _ := NewEvent(TopicSettlement, "0xA", nil)
		bus.Publish(ctx, event)
	}

	time.Sleep(300 * time.Millisecond)
	cancel()

	// Both consumers should get all events (fan-out, not competing consumers)
	if count1.Load() != 10 {
		t.Errorf("consumer-1 = %d, want 10", count1.Load())
	}
	if count2.Load() != 10 {
		t.Errorf("consumer-2 = %d, want 10", count2.Load())
	}
}

func TestHealthCheck(t *testing.T) {
	bus := NewMemoryBus(100, slog.Default())

	// Healthy when no events pending
	if !bus.IsHealthy() {
		t.Error("expected healthy with empty bus")
	}
}

// --- merged from coverage_extra_test.go ---

// ---------------------------------------------------------------------------
// NewEvent coverage
// ---------------------------------------------------------------------------

func TestNewEvent_Success(t *testing.T) {
	e, err := NewEvent("test.topic", "key1", map[string]string{"a": "b"})
	if err != nil {
		t.Fatalf("NewEvent: %v", err)
	}
	if e.Topic != "test.topic" {
		t.Errorf("topic = %q, want test.topic", e.Topic)
	}
	if e.Key != "key1" {
		t.Errorf("key = %q, want key1", e.Key)
	}
	if e.ID == "" {
		t.Error("expected non-empty ID")
	}
	if e.Timestamp.IsZero() {
		t.Error("expected non-zero timestamp")
	}
	var payload map[string]string
	if err := json.Unmarshal(e.Payload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload["a"] != "b" {
		t.Errorf("payload[a] = %q, want b", payload["a"])
	}
}

func TestNewEvent_MarshalError(t *testing.T) {
	// Channels cannot be marshalled to JSON
	_, err := NewEvent("test", "key", make(chan int))
	if err == nil {
		t.Error("expected marshal error for channel type")
	}
}

func TestNewEvent_NilPayload(t *testing.T) {
	e, err := NewEvent("test", "key", nil)
	if err != nil {
		t.Fatalf("NewEvent(nil): %v", err)
	}
	if string(e.Payload) != "null" {
		t.Errorf("expected null payload, got %s", string(e.Payload))
	}
}

// ---------------------------------------------------------------------------
// NewMemoryBus defaults
// ---------------------------------------------------------------------------

func TestNewMemoryBus_DefaultBufferSize(t *testing.T) {
	bus := NewMemoryBus(0, slog.Default())
	if bus == nil {
		t.Fatal("expected non-nil bus")
	}
	if cap(bus.buffer) != defaultBufferSize {
		t.Errorf("buffer cap = %d, want %d", cap(bus.buffer), defaultBufferSize)
	}
}

func TestNewMemoryBus_NegativeBufferSize(t *testing.T) {
	bus := NewMemoryBus(-1, slog.Default())
	if cap(bus.buffer) != defaultBufferSize {
		t.Errorf("buffer cap = %d, want %d", cap(bus.buffer), defaultBufferSize)
	}
}

// ---------------------------------------------------------------------------
// Subscribe defaults
// ---------------------------------------------------------------------------

func TestSubscribe_DefaultBatchSize(t *testing.T) {
	bus := NewMemoryBus(100, slog.Default())
	bus.Subscribe("topic", "group", 0, 0, func(_ context.Context, _ []Event) error { return nil })

	if len(bus.subscriptions) != 1 {
		t.Fatalf("expected 1 subscription, got %d", len(bus.subscriptions))
	}
	if bus.subscriptions[0].batchSize != 100 {
		t.Errorf("batchSize = %d, want 100 (default)", bus.subscriptions[0].batchSize)
	}
	if bus.subscriptions[0].flushInterval != time.Second {
		t.Errorf("flushInterval = %v, want 1s (default)", bus.subscriptions[0].flushInterval)
	}
}

// ---------------------------------------------------------------------------
// WithWAL
// ---------------------------------------------------------------------------

func TestWithWAL(t *testing.T) {
	bus := NewMemoryBus(100, slog.Default())
	if bus.wal != nil {
		t.Error("expected nil WAL initially")
	}
	result := bus.WithWAL(nil)
	if result != bus {
		t.Error("WithWAL should return the same bus for chaining")
	}
}

// ---------------------------------------------------------------------------
// IsHealthy
// ---------------------------------------------------------------------------

func TestIsHealthy_HighLoad(t *testing.T) {
	bus := NewMemoryBus(10000, slog.Default())
	// Simulate high load by directly adjusting atomic counters
	bus.published.Store(6000)
	bus.consumed.Store(0)
	if bus.IsHealthy() {
		t.Error("expected unhealthy when pending > threshold")
	}
	bus.consumed.Store(5999)
	if !bus.IsHealthy() {
		t.Error("expected healthy when pending < threshold")
	}
}

// ---------------------------------------------------------------------------
// Metrics
// ---------------------------------------------------------------------------

func TestMetrics_AllFields(t *testing.T) {
	bus := NewMemoryBus(100, slog.Default())
	bus.published.Store(10)
	bus.consumed.Store(7)
	bus.dropped.Store(2)
	bus.retries.Store(3)
	bus.deadLettered.Store(1)
	bus.batchesProc.Store(5)

	m := bus.Metrics()
	if m.Published != 10 {
		t.Errorf("published = %d", m.Published)
	}
	if m.Consumed != 7 {
		t.Errorf("consumed = %d", m.Consumed)
	}
	if m.Pending != 3 {
		t.Errorf("pending = %d", m.Pending)
	}
	if m.Dropped != 2 {
		t.Errorf("dropped = %d", m.Dropped)
	}
	if m.Retries != 3 {
		t.Errorf("retries = %d", m.Retries)
	}
	if m.DeadLettered != 1 {
		t.Errorf("deadLettered = %d", m.DeadLettered)
	}
	if m.BatchesProcessed != 5 {
		t.Errorf("batchesProcessed = %d", m.BatchesProcessed)
	}
}

// ---------------------------------------------------------------------------
// DeadLetterQueue / ReplayDeadLetters
// ---------------------------------------------------------------------------

func TestDeadLetterQueue_Empty(t *testing.T) {
	bus := NewMemoryBus(100, slog.Default())
	dlq := bus.DeadLetterQueue()
	if len(dlq) != 0 {
		t.Errorf("expected empty DLQ, got %d", len(dlq))
	}
}

func TestReplayDeadLetters_Empty(t *testing.T) {
	bus := NewMemoryBus(100, slog.Default())
	replayed := bus.ReplayDeadLetters(context.Background())
	if replayed != 0 {
		t.Errorf("expected 0 replayed, got %d", replayed)
	}
}

func TestReplayDeadLetters_RequeuesEvents(t *testing.T) {
	bus := NewMemoryBus(100, slog.Default())

	// Manually put events in DLQ
	bus.dlqMu.Lock()
	bus.dlq = append(bus.dlq,
		Event{ID: "evt_1", Topic: TopicSettlement, Key: "k1"},
		Event{ID: "evt_2", Topic: TopicSettlement, Key: "k2"},
	)
	bus.dlqMu.Unlock()

	replayed := bus.ReplayDeadLetters(context.Background())
	if replayed != 2 {
		t.Errorf("expected 2 replayed, got %d", replayed)
	}

	// DLQ should be empty after replay
	if len(bus.DeadLetterQueue()) != 0 {
		t.Error("DLQ should be empty after replay")
	}
}

func TestReplayDeadLetters_BufferFull(t *testing.T) {
	bus := NewMemoryBus(1, slog.Default())

	// Fill the buffer
	bus.Publish(context.Background(), Event{ID: "fill", Topic: "t", Key: "k"})

	// Put an event in DLQ
	bus.dlqMu.Lock()
	bus.dlq = append(bus.dlq, Event{ID: "evt_1", Topic: "t", Key: "k"})
	bus.dlqMu.Unlock()

	replayed := bus.ReplayDeadLetters(context.Background())
	if replayed != 0 {
		t.Errorf("expected 0 replayed (buffer full), got %d", replayed)
	}
}

// ---------------------------------------------------------------------------
// Partition coverage: NewPartitionedChannels defaults, Count, Channel
// ---------------------------------------------------------------------------

func TestNewPartitionedChannels_DefaultCount(t *testing.T) {
	pc := NewPartitionedChannels(0, 10)
	if pc.Count() != NumPartitions {
		t.Errorf("count = %d, want %d", pc.Count(), NumPartitions)
	}
	pc.Close()
}

func TestPartitionedChannels_ChannelRead(t *testing.T) {
	pc := NewPartitionedChannels(4, 10)
	e := Event{Key: "test"}
	pc.Route(e)
	idx := Partition("test", 4)
	ch := pc.Channel(idx)
	select {
	case got := <-ch:
		if got.Key != "test" {
			t.Errorf("key = %q, want test", got.Key)
		}
	default:
		t.Error("expected event on channel")
	}
	pc.Close()
}

func TestPartitionZeroPartitions(t *testing.T) {
	p := Partition("key", 0)
	if p != 0 {
		t.Errorf("Partition with 0 partitions: got %d, want 0", p)
	}
}

// ---------------------------------------------------------------------------
// SchemaRegistry: WrapWithVersion
// ---------------------------------------------------------------------------

func TestSchemaRegistry_WrapWithVersion(t *testing.T) {
	r := NewSchemaRegistry()
	r.Register("test.event", 1, struct{}{})
	r.Register("test.event", 2, struct{}{})

	e := Event{Topic: "test.event", ID: "e1"}
	ve := r.WrapWithVersion(e)
	if ve.SchemaVersion != 2 {
		t.Errorf("schemaVersion = %d, want 2", ve.SchemaVersion)
	}
	if ve.ID != "e1" {
		t.Errorf("ID = %q, want e1", ve.ID)
	}
}

func TestSchemaRegistry_WrapWithVersion_UnknownTopic(t *testing.T) {
	r := NewSchemaRegistry()
	e := Event{Topic: "unknown", ID: "e1"}
	ve := r.WrapWithVersion(e)
	if ve.SchemaVersion != 0 {
		t.Errorf("schemaVersion = %d, want 0 for unknown topic", ve.SchemaVersion)
	}
}

// ---------------------------------------------------------------------------
// SchemaRegistry: migration with error
// ---------------------------------------------------------------------------

func TestSchemaRegistry_MigrationError(t *testing.T) {
	r := NewSchemaRegistry()
	r.Register("test.event", 1, struct{}{})
	r.Register("test.event", 2, struct{}{})
	r.RegisterMigration("test.event", 1, 2, func(old json.RawMessage) (json.RawMessage, error) {
		return nil, context.DeadlineExceeded
	})

	payload, _ := json.Marshal(map[string]string{"x": "y"})
	_, version, err := r.Migrate("test.event", 1, payload)
	if err == nil {
		t.Fatal("expected migration error")
	}
	if version != 1 {
		t.Errorf("version = %d, want 1 (unchanged on error)", version)
	}
}

// ---------------------------------------------------------------------------
// KafkaConfig coverage
// ---------------------------------------------------------------------------

func TestDefaultKafkaConfig(t *testing.T) {
	cfg := DefaultKafkaConfig()
	if len(cfg.Brokers) != 1 || cfg.Brokers[0] != "localhost:9092" {
		t.Errorf("brokers = %v", cfg.Brokers)
	}
	if cfg.ConsumerGroup != "alancoin" {
		t.Errorf("consumer group = %q", cfg.ConsumerGroup)
	}
	if cfg.BatchSize != 100 {
		t.Errorf("batch size = %d", cfg.BatchSize)
	}
	if cfg.MaxRetries != 3 {
		t.Errorf("max retries = %d", cfg.MaxRetries)
	}
}

// ---------------------------------------------------------------------------
// Tracing functions
// ---------------------------------------------------------------------------

func TestStartPublishSpan(t *testing.T) {
	e := Event{ID: "e1", Topic: "test", Key: "k"}
	ctx, span := StartPublishSpan(context.Background(), e)
	if ctx == nil {
		t.Error("expected non-nil context")
	}
	span.End()
}

func TestStartConsumeSpan(t *testing.T) {
	ctx, span := StartConsumeSpan(context.Background(), "test-group", 10)
	if ctx == nil {
		t.Error("expected non-nil context")
	}
	span.End()
}

func TestInjectTraceContext(t *testing.T) {
	e := Event{ID: "e1"}
	InjectTraceContext(context.Background(), &e)
	// With no active span, RequestID remains empty
	// Just verify no panic
}

// ---------------------------------------------------------------------------
// CDC coverage (struct creation only - no DB)
// ---------------------------------------------------------------------------

func TestNewCDC(t *testing.T) {
	cdc := NewCDC(nil, nil, slog.Default())
	if cdc == nil {
		t.Fatal("expected non-nil CDC")
	}
	if cdc.pollInterval != 500*time.Millisecond {
		t.Errorf("pollInterval = %v", cdc.pollInterval)
	}
}

// ---------------------------------------------------------------------------
// MatviewRefresher coverage (struct creation only - no DB)
// ---------------------------------------------------------------------------

func TestNewMatviewRefresher(t *testing.T) {
	r := NewMatviewRefresher(nil, 5*time.Minute, slog.Default())
	if r == nil {
		t.Fatal("expected non-nil refresher")
	}
	if len(r.views) != 2 {
		t.Errorf("views count = %d, want 2", len(r.views))
	}
}

// ---------------------------------------------------------------------------
// Outbox coverage (struct creation only - no DB)
// ---------------------------------------------------------------------------

func TestNewOutbox(t *testing.T) {
	o := NewOutbox(nil, slog.Default())
	if o == nil {
		t.Fatal("expected non-nil outbox")
	}
}

// ---------------------------------------------------------------------------
// WALStore coverage (struct creation only - no DB)
// ---------------------------------------------------------------------------

func TestNewWALStore(t *testing.T) {
	w := NewWALStore(nil, slog.Default())
	if w == nil {
		t.Fatal("expected non-nil WAL store")
	}
}

// ---------------------------------------------------------------------------
// DrainBuffer coverage (empty buffer)
// ---------------------------------------------------------------------------

func TestDrainBuffer_Empty(t *testing.T) {
	bus := NewMemoryBus(10, slog.Default())
	subChans := []chan Event{make(chan Event, 10)}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	// Should return immediately on empty buffer
	bus.drainBuffer(ctx, subChans)
}
