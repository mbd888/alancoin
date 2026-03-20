package eventbus

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
)

// TestSettlementFlowIntegration simulates the full production settlement flow:
// Gateway publishes settlement event → forensics consumer analyzes → chargeback consumer attributes cost.
// This verifies that the event bus correctly fans out to multiple consumers with batching.
func TestSettlementFlowIntegration(t *testing.T) {
	bus := NewMemoryBus(1000, slog.Default())

	var forensicsProcessed atomic.Int64
	var chargebackProcessed atomic.Int64
	var webhookProcessed atomic.Int64

	// Simulate forensics consumer
	bus.Subscribe(TopicSettlement, "forensics", 10, 100*time.Millisecond,
		func(_ context.Context, events []Event) error {
			for _, e := range events {
				var p SettlementPayload
				if err := json.Unmarshal(e.Payload, &p); err != nil {
					t.Errorf("forensics: unmarshal error: %v", err)
					continue
				}
				if p.BuyerAddr == "" || p.SellerAddr == "" {
					t.Error("forensics: missing buyer/seller in payload")
				}
				forensicsProcessed.Add(1)
			}
			return nil
		})

	// Simulate chargeback consumer
	bus.Subscribe(TopicSettlement, "chargeback", 10, 100*time.Millisecond,
		func(_ context.Context, events []Event) error {
			for _, e := range events {
				var p SettlementPayload
				if err := json.Unmarshal(e.Payload, &p); err != nil {
					t.Errorf("chargeback: unmarshal error: %v", err)
					continue
				}
				if p.TenantID == "" {
					continue // skip events without tenant
				}
				chargebackProcessed.Add(1)
			}
			return nil
		})

	// Simulate webhook consumer
	bus.Subscribe(TopicSettlement, "webhooks", 5, 100*time.Millisecond,
		func(_ context.Context, events []Event) error {
			webhookProcessed.Add(int64(len(events)))
			return nil
		})

	ctx, cancel := context.WithCancel(context.Background())
	go bus.Start(ctx)

	// Simulate 100 settlement events (mixed: some with tenant, some without)
	for i := 0; i < 100; i++ {
		tenantID := ""
		if i%2 == 0 {
			tenantID = "ten_1" // 50 events have a tenant
		}
		event, err := NewEvent(TopicSettlement, "0xBuyer", SettlementPayload{
			SessionID:   "sess_" + string(rune('A'+i%26)),
			TenantID:    tenantID,
			BuyerAddr:   "0xBuyer",
			SellerAddr:  "0xSeller",
			Amount:      "1.50",
			ServiceType: "inference",
			AmountFloat: 1.5,
		})
		if err != nil {
			t.Fatalf("NewEvent: %v", err)
		}
		if err := bus.Publish(ctx, event); err != nil {
			t.Fatalf("Publish %d: %v", i, err)
		}
	}

	// Wait for all consumers to process
	time.Sleep(800 * time.Millisecond)
	cancel()
	time.Sleep(200 * time.Millisecond) // drain

	// Verify: all consumers got all 100 events
	if got := forensicsProcessed.Load(); got != 100 {
		t.Errorf("forensics processed = %d, want 100", got)
	}

	// Chargeback only processes events with tenant (50)
	if got := chargebackProcessed.Load(); got != 50 {
		t.Errorf("chargeback processed = %d, want 50 (events with tenant)", got)
	}

	// Webhooks should get all 100
	if got := webhookProcessed.Load(); got != 100 {
		t.Errorf("webhook processed = %d, want 100", got)
	}

	// Verify metrics
	m := bus.Metrics()
	if m.Published != 100 {
		t.Errorf("published = %d, want 100", m.Published)
	}
	if m.Consumed != 300 { // 100 per consumer × 3 consumers
		t.Errorf("consumed = %d, want 300", m.Consumed)
	}
	if m.Dropped != 0 {
		t.Errorf("dropped = %d, want 0", m.Dropped)
	}
	if m.DeadLettered != 0 {
		t.Errorf("dead_lettered = %d, want 0", m.DeadLettered)
	}
}

// TestSettlementPayloadRoundTrip verifies that SettlementPayload survives
// serialization through the event bus without data loss.
func TestSettlementPayloadRoundTrip(t *testing.T) {
	original := SettlementPayload{
		SessionID:   "sess_abc123",
		TenantID:    "ten_prod",
		BuyerAddr:   "0xBuyerAddr",
		SellerAddr:  "0xSellerAddr",
		Amount:      "25.500000",
		ServiceType: "translation",
		ServiceID:   "svc_xyz",
		Fee:         "0.637500",
		Reference:   "gw_settle:ref_123",
		LatencyMs:   142,
		AmountFloat: 25.5,
	}

	event, err := NewEvent(TopicSettlement, original.BuyerAddr, original)
	if err != nil {
		t.Fatalf("NewEvent: %v", err)
	}

	// Verify event metadata
	if event.Topic != TopicSettlement {
		t.Errorf("topic = %q", event.Topic)
	}
	if event.Key != original.BuyerAddr {
		t.Errorf("key = %q", event.Key)
	}
	if event.ID == "" {
		t.Error("event ID empty")
	}

	// Deserialize payload
	var decoded SettlementPayload
	if err := json.Unmarshal(event.Payload, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.SessionID != original.SessionID {
		t.Errorf("sessionId = %q", decoded.SessionID)
	}
	if decoded.Amount != original.Amount {
		t.Errorf("amount = %q", decoded.Amount)
	}
	if decoded.Fee != original.Fee {
		t.Errorf("fee = %q", decoded.Fee)
	}
	if decoded.LatencyMs != original.LatencyMs {
		t.Errorf("latencyMs = %d", decoded.LatencyMs)
	}
	if decoded.AmountFloat != original.AmountFloat {
		t.Errorf("amountFloat = %f", decoded.AmountFloat)
	}
}

// TestHighThroughputSettlement simulates production load: 10K events, 3 consumers.
func TestHighThroughputSettlement(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping high-throughput test in short mode")
	}

	bus := NewMemoryBus(20000, slog.Default())
	const eventCount = 10000

	var total atomic.Int64
	for i := 0; i < 3; i++ {
		bus.Subscribe(TopicSettlement, "consumer-"+string(rune('A'+i)), 200, 100*time.Millisecond,
			func(_ context.Context, events []Event) error {
				total.Add(int64(len(events)))
				return nil
			})
	}

	ctx, cancel := context.WithCancel(context.Background())
	go bus.Start(ctx)

	// Publish 10K events as fast as possible
	for i := 0; i < eventCount; i++ {
		event, _ := NewEvent(TopicSettlement, "0xA", SettlementPayload{Amount: "1.00"})
		bus.Publish(ctx, event)
	}

	// Wait for consumption
	time.Sleep(2 * time.Second)
	cancel()
	time.Sleep(200 * time.Millisecond)

	expected := int64(eventCount * 3) // 3 consumers × 10K events
	got := total.Load()
	if got < expected*9/10 { // Allow 10% tolerance for timing
		t.Errorf("total consumed = %d, want ~%d (90%% threshold)", got, expected)
	}

	m := bus.Metrics()
	t.Logf("throughput: published=%d consumed=%d dropped=%d batches=%d",
		m.Published, m.Consumed, m.Dropped, m.BatchesProcessed)
}
