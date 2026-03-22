package eventbus

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/segmentio/kafka-go"
	"github.com/segmentio/kafka-go/sasl/plain"
	"github.com/segmentio/kafka-go/sasl/scram"

	"github.com/mbd888/alancoin/internal/metrics"
)

// KafkaConfig holds Kafka connection settings.
type KafkaConfig struct {
	Brokers       []string // Kafka broker addresses
	ConsumerGroup string   // Consumer group ID for offset tracking
	ClientID      string   // Unique client identifier
	TopicPrefix   string   // Prefix for topic names (e.g. "alancoin.")

	// TLS
	TLSEnabled bool
	TLSCert    string
	TLSKey     string
	TLSCA      string

	// SASL authentication
	SASLEnabled   bool
	SASLMechanism string // "PLAIN", "SCRAM-SHA-256", "SCRAM-SHA-512"
	SASLUsername  string
	SASLPassword  string

	// Tuning
	BatchSize     int // Max events per batch (default: 100)
	FlushInterval int // Flush interval in milliseconds (default: 1000)
	MaxRetries    int // Max retries before DLQ (default: 3)
	BufferSize    int // Internal buffer size (default: 10000)
}

// DefaultKafkaConfig returns sensible defaults for Kafka configuration.
func DefaultKafkaConfig() KafkaConfig {
	return KafkaConfig{
		Brokers:       []string{"localhost:9092"},
		ConsumerGroup: "alancoin",
		ClientID:      "alancoin-1",
		TopicPrefix:   "alancoin.",
		BatchSize:     100,
		FlushInterval: 1000,
		MaxRetries:    3,
		BufferSize:    10000,
	}
}

// KafkaBus implements the Bus interface backed by Apache Kafka.
//
// Architecture:
//   - One shared kafka.Writer for publishing (thread-safe, handles batching)
//   - One kafka.Reader per subscription (each with its own consumer group)
//   - DLQ topic per source topic (e.g. alancoin.settlement.completed.dlq)
//   - Health based on last successful produce/consume within threshold
type KafkaBus struct {
	cfg    KafkaConfig
	logger *slog.Logger

	writer *kafka.Writer

	subscriptions []kafkaSubscription

	// DLQ writer (writes to *.dlq topics)
	dlqWriter *kafka.Writer

	// Stats (atomic for lock-free reads)
	published    atomic.Int64
	consumed     atomic.Int64
	dropped      atomic.Int64
	retries      atomic.Int64
	deadLettered atomic.Int64
	batchesProc  atomic.Int64

	// Health tracking
	lastPublish atomic.Int64 // unix millis
	lastConsume atomic.Int64 // unix millis
}

type kafkaSubscription struct {
	topic         string
	consumerGroup string
	batchSize     int
	flushInterval time.Duration
	handler       Handler
}

// NewKafkaBus creates a Kafka-backed event bus.
func NewKafkaBus(cfg KafkaConfig, logger *slog.Logger) (*KafkaBus, error) {
	if len(cfg.Brokers) == 0 {
		return nil, fmt.Errorf("eventbus: at least one Kafka broker is required")
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 100
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = 1000
	}
	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = 3
	}

	transport, err := buildTransport(cfg)
	if err != nil {
		return nil, fmt.Errorf("eventbus: kafka transport: %w", err)
	}

	writer := &kafka.Writer{
		Addr:         kafka.TCP(cfg.Brokers...),
		Balancer:     &kafka.Hash{}, // partition by key
		BatchSize:    cfg.BatchSize,
		BatchTimeout: time.Duration(cfg.FlushInterval) * time.Millisecond,
		RequiredAcks: kafka.RequireAll, // wait for all ISR
		Async:        false,            // synchronous for durability
		Transport:    transport,
		MaxAttempts:  cfg.MaxRetries,
	}

	dlqWriter := &kafka.Writer{
		Addr:         kafka.TCP(cfg.Brokers...),
		Balancer:     &kafka.Hash{},
		RequiredAcks: kafka.RequireAll,
		Transport:    transport,
	}

	b := &KafkaBus{
		cfg:       cfg,
		logger:    logger,
		writer:    writer,
		dlqWriter: dlqWriter,
	}

	return b, nil
}

// Publish sends an event to the appropriate Kafka topic.
func (b *KafkaBus) Publish(ctx context.Context, event Event) error {
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("eventbus: marshal event: %w", err)
	}

	topic := b.cfg.TopicPrefix + event.Topic
	msg := kafka.Message{
		Topic: topic,
		Key:   []byte(event.Key),
		Value: data,
		Headers: []kafka.Header{
			{Key: "event_id", Value: []byte(event.ID)},
			{Key: "request_id", Value: []byte(event.RequestID)},
		},
	}

	if err := b.writer.WriteMessages(ctx, msg); err != nil {
		b.dropped.Add(1)
		metrics.EventBusDropped.Inc()
		b.logger.Warn("eventbus: kafka publish failed",
			"topic", topic, "key", event.Key, "error", err)
		return fmt.Errorf("eventbus: kafka publish: %w", err)
	}

	b.published.Add(1)
	b.lastPublish.Store(time.Now().UnixMilli())
	metrics.EventBusPublished.Inc()
	return nil
}

// Subscribe registers a handler for events on a topic.
// Each subscription gets its own Kafka consumer group for independent offset tracking.
func (b *KafkaBus) Subscribe(topic, consumerGroup string, batchSize int, flushInterval time.Duration, handler Handler) {
	if batchSize <= 0 {
		batchSize = b.cfg.BatchSize
	}
	if flushInterval <= 0 {
		flushInterval = time.Duration(b.cfg.FlushInterval) * time.Millisecond
	}
	b.subscriptions = append(b.subscriptions, kafkaSubscription{
		topic:         topic,
		consumerGroup: consumerGroup,
		batchSize:     batchSize,
		flushInterval: flushInterval,
		handler:       handler,
	})
}

// Start launches consumer goroutines for all subscriptions and blocks until ctx is cancelled.
func (b *KafkaBus) Start(ctx context.Context) {
	// Periodic metrics gauge update
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				pending := b.published.Load() - b.consumed.Load()
				if pending < 0 {
					pending = 0
				}
				metrics.EventBusPending.Set(float64(pending))
			}
		}
	}()

	var wg sync.WaitGroup
	for _, sub := range b.subscriptions {
		wg.Add(1)
		go func(s kafkaSubscription) {
			defer wg.Done()
			b.consumeLoop(ctx, s)
		}(sub)
	}

	wg.Wait()

	// Shutdown writers
	if err := b.writer.Close(); err != nil {
		b.logger.Error("eventbus: kafka writer close error", "error", err)
	}
	if err := b.dlqWriter.Close(); err != nil {
		b.logger.Error("eventbus: kafka DLQ writer close error", "error", err)
	}
}

func (b *KafkaBus) consumeLoop(ctx context.Context, sub kafkaSubscription) {
	kafkaTopic := b.cfg.TopicPrefix + sub.topic

	dialer := &kafka.Dialer{
		Timeout:   10 * time.Second,
		DualStack: true,
	}

	if b.cfg.TLSEnabled {
		tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
		dialer.TLS = tlsCfg
	}

	groupID := b.cfg.ConsumerGroup + "-" + sub.consumerGroup

	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:        b.cfg.Brokers,
		Topic:          kafkaTopic,
		GroupID:        groupID,
		MinBytes:       1,
		MaxBytes:       10e6, // 10MB
		MaxWait:        sub.flushInterval,
		CommitInterval: sub.flushInterval,
		StartOffset:    kafka.LastOffset,
		Dialer:         dialer,
	})
	defer func() {
		if err := reader.Close(); err != nil {
			b.logger.Error("eventbus: kafka reader close error",
				"consumer", groupID, "error", err)
		}
	}()

	b.logger.Info("eventbus: kafka consumer started",
		"topic", kafkaTopic, "group", groupID, "batch_size", sub.batchSize)

	batch := make([]Event, 0, sub.batchSize)
	ticker := time.NewTicker(sub.flushInterval)
	defer ticker.Stop()

	flush := func() {
		if len(batch) == 0 {
			return
		}
		toProcess := make([]Event, len(batch))
		copy(toProcess, batch)
		batch = batch[:0]

		var lastErr error
		for attempt := 0; attempt < b.cfg.MaxRetries; attempt++ {
			if attempt > 0 {
				b.retries.Add(1)
				metrics.EventBusRetries.Inc()
				delay := time.Duration(defaultRetryBaseMs*(1<<attempt)) * time.Millisecond
				select {
				case <-ctx.Done():
					return
				case <-time.After(delay):
				}
			}

			handlerCtx, handlerCancel := context.WithTimeout(ctx, defaultHandlerTimeout)
			lastErr = sub.handler(handlerCtx, toProcess)
			handlerCancel()

			if lastErr == nil {
				b.consumed.Add(int64(len(toProcess)))
				b.batchesProc.Add(1)
				b.lastConsume.Store(time.Now().UnixMilli())
				metrics.EventBusBatchesProcessed.Inc()
				metrics.EventBusConsumed.Add(float64(len(toProcess)))
				return
			}
		}

		// All retries exhausted — dead letter
		b.logger.Error("eventbus: consumer failed after retries, dead-lettering to Kafka DLQ",
			"consumer", sub.consumerGroup,
			"batch_size", len(toProcess),
			"attempts", b.cfg.MaxRetries,
			"error", lastErr,
		)
		metrics.EventBusErrors.Inc()

		dlqTopic := kafkaTopic + ".dlq"
		for _, e := range toProcess {
			e.Attempt = b.cfg.MaxRetries
			data, _ := json.Marshal(e)
			dlqMsg := kafka.Message{
				Topic: dlqTopic,
				Key:   []byte(e.Key),
				Value: data,
				Headers: []kafka.Header{
					{Key: "event_id", Value: []byte(e.ID)},
					{Key: "original_topic", Value: []byte(kafkaTopic)},
					{Key: "error", Value: []byte(lastErr.Error())},
				},
			}
			if err := b.dlqWriter.WriteMessages(ctx, dlqMsg); err != nil {
				b.logger.Error("eventbus: DLQ write failed",
					"event_id", e.ID, "error", err)
			}
		}
		b.deadLettered.Add(int64(len(toProcess)))
		metrics.EventBusDeadLettered.Add(float64(len(toProcess)))
	}

	for {
		select {
		case <-ctx.Done():
			flush()
			return
		case <-ticker.C:
			flush()
		default:
		}

		// Non-blocking read with short timeout to allow ticker/ctx checks
		readCtx, readCancel := context.WithTimeout(ctx, sub.flushInterval)
		msg, err := reader.ReadMessage(readCtx)
		readCancel()

		if err != nil {
			if ctx.Err() != nil {
				flush()
				return
			}
			// Timeout is expected when no messages are available
			continue
		}

		var event Event
		if err := json.Unmarshal(msg.Value, &event); err != nil {
			b.logger.Warn("eventbus: kafka unmarshal failed, skipping message",
				"topic", msg.Topic, "offset", msg.Offset, "error", err)
			continue
		}

		batch = append(batch, event)
		if len(batch) >= sub.batchSize {
			flush()
		}
	}
}

// IsHealthy returns true if the bus has produced or consumed within the last 30 seconds,
// or if no messages have been sent yet (startup grace).
func (b *KafkaBus) IsHealthy() bool {
	now := time.Now().UnixMilli()
	lastPub := b.lastPublish.Load()

	// Grace period: if we've never published, we're healthy (just started)
	if lastPub == 0 {
		return true
	}

	threshold := int64(30_000) // 30 seconds
	if (now - lastPub) > threshold {
		return false
	}
	return true
}

// Metrics returns operational statistics.
func (b *KafkaBus) Metrics() BusMetrics {
	return BusMetrics{
		Published:        b.published.Load(),
		Consumed:         b.consumed.Load(),
		Pending:          b.published.Load() - b.consumed.Load(),
		Dropped:          b.dropped.Load(),
		Retries:          b.retries.Load(),
		DeadLettered:     b.deadLettered.Load(),
		BatchesProcessed: b.batchesProc.Load(),
	}
}

// buildTransport creates a kafka.Transport with TLS and SASL if configured.
func buildTransport(cfg KafkaConfig) (kafka.RoundTripper, error) {
	t := &kafka.Transport{
		ClientID: cfg.ClientID,
	}

	if cfg.TLSEnabled {
		tlsCfg := &tls.Config{
			MinVersion: tls.VersionTLS12,
		}

		if cfg.TLSCert != "" && cfg.TLSKey != "" {
			cert, err := tls.LoadX509KeyPair(cfg.TLSCert, cfg.TLSKey)
			if err != nil {
				return nil, fmt.Errorf("load TLS keypair: %w", err)
			}
			tlsCfg.Certificates = []tls.Certificate{cert}
		}

		if cfg.TLSCA != "" {
			caCert, err := os.ReadFile(cfg.TLSCA)
			if err != nil {
				return nil, fmt.Errorf("read CA cert: %w", err)
			}
			pool := x509.NewCertPool()
			pool.AppendCertsFromPEM(caCert)
			tlsCfg.RootCAs = pool
		}

		t.TLS = tlsCfg
	}

	if cfg.SASLEnabled {
		switch cfg.SASLMechanism {
		case "PLAIN":
			t.SASL = plain.Mechanism{
				Username: cfg.SASLUsername,
				Password: cfg.SASLPassword,
			}
		case "SCRAM-SHA-256":
			mechanism, err := scram.Mechanism(scram.SHA256, cfg.SASLUsername, cfg.SASLPassword)
			if err != nil {
				return nil, fmt.Errorf("SCRAM-SHA-256: %w", err)
			}
			t.SASL = mechanism
		case "SCRAM-SHA-512":
			mechanism, err := scram.Mechanism(scram.SHA512, cfg.SASLUsername, cfg.SASLPassword)
			if err != nil {
				return nil, fmt.Errorf("SCRAM-SHA-512: %w", err)
			}
			t.SASL = mechanism
		default:
			return nil, fmt.Errorf("unsupported SASL mechanism: %s", cfg.SASLMechanism)
		}
	}

	return t, nil
}
