package eventbus_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/segmentio/kafka-go"

	"github.com/mbd888/alancoin/internal/eventbus"
)

// These tests require a running Kafka broker.
// Run with: KAFKA_TEST_BROKERS=localhost:29092 go test -run TestKafka -v ./internal/eventbus/
//
// To start Kafka locally:
//   cd experiments && docker compose up -d kafka

func kafkaBrokers(t *testing.T) []string {
	t.Helper()
	brokers := os.Getenv("KAFKA_TEST_BROKERS")
	if brokers == "" {
		t.Skip("KAFKA_TEST_BROKERS not set, skipping Kafka integration test")
	}
	return strings.Split(brokers, ",")
}

// ensureTopic creates a topic if it doesn't exist, best-effort.
func ensureTopic(t *testing.T, brokers []string, topic string) {
	t.Helper()
	conn, err := kafka.Dial("tcp", brokers[0])
	if err != nil {
		t.Fatalf("dial kafka: %v", err)
	}
	defer conn.Close()

	_ = conn.CreateTopics(kafka.TopicConfig{
		Topic:             topic,
		NumPartitions:     4,
		ReplicationFactor: 1,
	})
}

func TestKafkaBusPublishSubscribe(t *testing.T) {
	brokers := kafkaBrokers(t)
	topic := "test.settlement.completed"
	kafkaTopic := "test." + topic

	ensureTopic(t, brokers, kafkaTopic)

	cfg := eventbus.KafkaConfig{
		Brokers:       brokers,
		ConsumerGroup: "test-pub-sub",
		ClientID:      "test-client-1",
		TopicPrefix:   "test.",
		BatchSize:     10,
		FlushInterval: 200,
		MaxRetries:    2,
	}

	bus, err := eventbus.NewKafkaBus(cfg, slog.Default())
	if err != nil {
		t.Fatalf("NewKafkaBus: %v", err)
	}

	var consumed atomic.Int64
	receivedCh := make(chan eventbus.SettlementPayload, 10)

	bus.Subscribe(topic, "test-consumer", 5, 200*time.Millisecond,
		func(_ context.Context, events []eventbus.Event) error {
			for _, e := range events {
				var p eventbus.SettlementPayload
				if err := json.Unmarshal(e.Payload, &p); err != nil {
					t.Errorf("unmarshal: %v", err)
					continue
				}
				consumed.Add(1)
				select {
				case receivedCh <- p:
				default:
				}
			}
			return nil
		})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	go bus.Start(ctx)

	// Give consumer time to join the group
	time.Sleep(2 * time.Second)

	// Publish events
	for i := 0; i < 5; i++ {
		evt, err := eventbus.NewEvent(topic, "0xBuyer", eventbus.SettlementPayload{
			SessionID:  "sess_kafka_test",
			BuyerAddr:  "0xBuyer",
			SellerAddr: "0xSeller",
			Amount:     "1.000000",
			Fee:        "0.010000",
		})
		if err != nil {
			t.Fatalf("NewEvent: %v", err)
		}
		if err := bus.Publish(ctx, evt); err != nil {
			t.Fatalf("Publish: %v", err)
		}
	}

	// Wait for consumption
	deadline := time.After(15 * time.Second)
	for consumed.Load() < 5 {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for consumption, got %d/5", consumed.Load())
		case <-time.After(100 * time.Millisecond):
		}
	}

	// Verify payload integrity
	select {
	case p := <-receivedCh:
		if p.BuyerAddr != "0xBuyer" {
			t.Errorf("got BuyerAddr=%q, want 0xBuyer", p.BuyerAddr)
		}
		if p.Amount != "1.000000" {
			t.Errorf("got Amount=%q, want 1.000000", p.Amount)
		}
	default:
		t.Error("no payloads received")
	}

	// Verify metrics
	m := bus.Metrics()
	if m.Published != 5 {
		t.Errorf("published=%d, want 5", m.Published)
	}
	if m.Consumed < 5 {
		t.Errorf("consumed=%d, want >=5", m.Consumed)
	}
	if !bus.IsHealthy() {
		t.Error("bus should be healthy")
	}

	cancel()
}

func TestKafkaBusMultipleConsumerGroups(t *testing.T) {
	brokers := kafkaBrokers(t)
	topic := "test.multi.settlement"
	kafkaTopic := "test." + topic

	ensureTopic(t, brokers, kafkaTopic)

	cfg := eventbus.KafkaConfig{
		Brokers:       brokers,
		ConsumerGroup: "test-multi",
		ClientID:      "test-client-multi",
		TopicPrefix:   "test.",
		BatchSize:     5,
		FlushInterval: 200,
		MaxRetries:    2,
	}

	bus, err := eventbus.NewKafkaBus(cfg, slog.Default())
	if err != nil {
		t.Fatalf("NewKafkaBus: %v", err)
	}

	var forensicsCount atomic.Int64
	var chargebackCount atomic.Int64

	bus.Subscribe(topic, "forensics", 5, 200*time.Millisecond,
		func(_ context.Context, events []eventbus.Event) error {
			forensicsCount.Add(int64(len(events)))
			return nil
		})

	bus.Subscribe(topic, "chargeback", 5, 200*time.Millisecond,
		func(_ context.Context, events []eventbus.Event) error {
			chargebackCount.Add(int64(len(events)))
			return nil
		})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	go bus.Start(ctx)
	time.Sleep(2 * time.Second)

	// Publish 3 events
	for i := 0; i < 3; i++ {
		evt, _ := eventbus.NewEvent(topic, "0xAgent", eventbus.SettlementPayload{
			SessionID: "sess_multi",
			Amount:    "2.000000",
		})
		if err := bus.Publish(ctx, evt); err != nil {
			t.Fatalf("Publish: %v", err)
		}
	}

	// Wait for both consumer groups to process all events
	deadline := time.After(15 * time.Second)
	for forensicsCount.Load() < 3 || chargebackCount.Load() < 3 {
		select {
		case <-deadline:
			t.Fatalf("timeout: forensics=%d chargeback=%d (want 3 each)",
				forensicsCount.Load(), chargebackCount.Load())
		case <-time.After(100 * time.Millisecond):
		}
	}

	t.Logf("forensics processed: %d, chargeback processed: %d",
		forensicsCount.Load(), chargebackCount.Load())

	cancel()
}

func TestKafkaBusConfig(t *testing.T) {
	// Test that NewKafkaBus validates config
	_, err := eventbus.NewKafkaBus(eventbus.KafkaConfig{}, slog.Default())
	if err == nil {
		t.Error("expected error for empty brokers")
	}

	// Test defaults
	cfg := eventbus.DefaultKafkaConfig()
	if len(cfg.Brokers) == 0 {
		t.Error("default config should have brokers")
	}
	if cfg.BatchSize != 100 {
		t.Errorf("default batch size=%d, want 100", cfg.BatchSize)
	}
}
