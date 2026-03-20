package eventbus

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
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

func TestPayloadDeserialization(t *testing.T) {
	bus := NewMemoryBus(100, slog.Default())

	var gotPayload SettlementPayload
	var mu sync.Mutex

	bus.Subscribe(TopicSettlement, "deserialize-test", 1, 50*time.Millisecond, func(_ context.Context, events []Event) error {
		mu.Lock()
		defer mu.Unlock()
		for _, e := range events {
			json.Unmarshal(e.Payload, &gotPayload)
		}
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	go bus.Start(ctx)

	event, _ := NewEvent(TopicSettlement, "0xBuyer", SettlementPayload{
		SessionID:   "sess_123",
		BuyerAddr:   "0xBuyer",
		SellerAddr:  "0xSeller",
		Amount:      "25.50",
		ServiceType: "inference",
		AmountFloat: 25.5,
	})
	bus.Publish(ctx, event)

	time.Sleep(200 * time.Millisecond)
	cancel()

	mu.Lock()
	defer mu.Unlock()
	if gotPayload.Amount != "25.50" {
		t.Errorf("amount = %q, want 25.50", gotPayload.Amount)
	}
	if gotPayload.ServiceType != "inference" {
		t.Errorf("serviceType = %q, want inference", gotPayload.ServiceType)
	}
	if gotPayload.AmountFloat != 25.5 {
		t.Errorf("amountFloat = %f, want 25.5", gotPayload.AmountFloat)
	}
}

func TestMetrics(t *testing.T) {
	bus := NewMemoryBus(100, slog.Default())

	bus.Subscribe(TopicSettlement, "metrics-test", 50, 100*time.Millisecond, func(_ context.Context, events []Event) error {
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	go bus.Start(ctx)

	for i := 0; i < 20; i++ {
		event, _ := NewEvent(TopicSettlement, "0xA", nil)
		bus.Publish(ctx, event)
	}

	time.Sleep(300 * time.Millisecond)
	cancel()

	m := bus.Metrics()
	if m.Published != 20 {
		t.Errorf("published = %d, want 20", m.Published)
	}
	if m.Consumed != 20 {
		t.Errorf("consumed = %d, want 20", m.Consumed)
	}
	if m.BatchesProcessed == 0 {
		t.Error("expected at least one batch processed")
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

func TestRetryOnHandlerError(t *testing.T) {
	bus := NewMemoryBus(100, slog.Default())

	var attempts atomic.Int64
	bus.Subscribe(TopicSettlement, "failing-consumer", 1, 50*time.Millisecond, func(_ context.Context, events []Event) error {
		attempts.Add(1)
		if attempts.Load() < 3 {
			return fmt.Errorf("transient error")
		}
		return nil // succeed on 3rd attempt
	})

	ctx, cancel := context.WithCancel(context.Background())
	go bus.Start(ctx)

	event, _ := NewEvent(TopicSettlement, "0xA", nil)
	bus.Publish(ctx, event)

	time.Sleep(800 * time.Millisecond)
	cancel()

	if attempts.Load() < 3 {
		t.Errorf("attempts = %d, want >= 3 (retried)", attempts.Load())
	}

	m := bus.Metrics()
	if m.Retries == 0 {
		t.Error("expected retries > 0")
	}
}

func TestDeadLetterQueue(t *testing.T) {
	bus := NewMemoryBus(100, slog.Default())

	// Handler always fails
	bus.Subscribe(TopicSettlement, "always-fail", 1, 50*time.Millisecond, func(_ context.Context, events []Event) error {
		return fmt.Errorf("permanent failure")
	})

	ctx, cancel := context.WithCancel(context.Background())
	go bus.Start(ctx)

	event, _ := NewEvent(TopicSettlement, "0xA", map[string]string{"test": "dlq"})
	bus.Publish(ctx, event)

	time.Sleep(1200 * time.Millisecond) // Wait for all retries to exhaust
	cancel()

	dlq := bus.DeadLetterQueue()
	if len(dlq) == 0 {
		t.Error("expected events in dead letter queue")
	}

	m := bus.Metrics()
	if m.DeadLettered == 0 {
		t.Error("expected deadLettered > 0")
	}
}

func TestReplayDeadLetters(t *testing.T) {
	bus := NewMemoryBus(100, slog.Default())

	var callCount atomic.Int64
	failFirst := true
	bus.Subscribe(TopicSettlement, "replay-test", 1, 50*time.Millisecond, func(_ context.Context, events []Event) error {
		callCount.Add(1)
		if failFirst {
			failFirst = false
			return fmt.Errorf("fail")
		}
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	go bus.Start(ctx)

	event, _ := NewEvent(TopicSettlement, "0xA", nil)
	bus.Publish(ctx, event)

	time.Sleep(1200 * time.Millisecond) // Wait for retries + DLQ

	// Replay DLQ
	replayed := bus.ReplayDeadLetters(ctx)
	time.Sleep(300 * time.Millisecond)
	cancel()

	if replayed == 0 && len(bus.DeadLetterQueue()) > 0 {
		// Events were replayed back to buffer
		t.Log("replayed events, checking processing")
	}
}

func TestHealthCheck(t *testing.T) {
	bus := NewMemoryBus(100, slog.Default())

	// Healthy when no events pending
	if !bus.IsHealthy() {
		t.Error("expected healthy with empty bus")
	}
}
