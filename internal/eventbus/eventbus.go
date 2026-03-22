// Package eventbus provides a durable, ordered event bus for decoupling
// payment settlement from downstream processing.
//
// Architecture:
//
//	Producer (gateway/escrow/stream) → Bus → Topic → Consumer(s)
//
// Production features:
//   - Backpressure (bounded buffer, non-blocking publish with drop counter)
//   - Batching (N events → 1 handler call for throughput)
//   - Retry with exponential backoff (3 attempts before dead-letter)
//   - Dead letter queue (failed events stored for manual replay)
//   - Graceful drain (flush all pending events before shutdown, 5s timeout)
//   - Request ID propagation (events carry the originating request's trace context)
//   - Health check integration (pending > threshold = degraded)
//   - Per-consumer lag tracking via Prometheus
package eventbus

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mbd888/alancoin/internal/idgen"
	"github.com/mbd888/alancoin/internal/metrics"
)

// Topic names for event routing.
const (
	TopicSettlement = "settlement.completed"
	TopicDispute    = "escrow.disputed"
	TopicAlert      = "forensics.alert"
	TopicKYA        = "kya.issued"
)

// Event is the unit of data flowing through the bus.
type Event struct {
	ID        string          `json:"id"`
	Topic     string          `json:"topic"`
	Key       string          `json:"key"` // Partition key (e.g. agentAddr) for ordering
	Payload   json.RawMessage `json:"payload"`
	Timestamp time.Time       `json:"timestamp"`
	RequestID string          `json:"requestId,omitempty"` // Propagated from originating HTTP request
	Attempt   int             `json:"attempt,omitempty"`   // Retry attempt counter
}

// SettlementPayload is the payload for TopicSettlement events.
type SettlementPayload struct {
	SessionID   string  `json:"sessionId"`
	TenantID    string  `json:"tenantId"`
	BuyerAddr   string  `json:"buyerAddr"`
	SellerAddr  string  `json:"sellerAddr"`
	Amount      string  `json:"amount"`
	ServiceType string  `json:"serviceType"`
	ServiceID   string  `json:"serviceId"`
	Fee         string  `json:"fee"`
	Reference   string  `json:"reference"`
	LatencyMs   int64   `json:"latencyMs"`
	AmountFloat float64 `json:"amountFloat"`
}

// Handler processes a batch of events. Return error to trigger retry.
type Handler func(ctx context.Context, events []Event) error

// Bus is the event bus interface. Implementations: MemoryBus, KafkaBus.
type Bus interface {
	Publish(ctx context.Context, event Event) error
	Subscribe(topic, consumerGroup string, batchSize int, flushInterval time.Duration, handler Handler)
	Start(ctx context.Context)
	Metrics() BusMetrics
	IsHealthy() bool
}

// BusMetrics exposes operational statistics.
type BusMetrics struct {
	Published        int64            `json:"published"`
	Consumed         int64            `json:"consumed"`
	Pending          int64            `json:"pending"`
	Dropped          int64            `json:"dropped"`
	Retries          int64            `json:"retries"`
	DeadLettered     int64            `json:"deadLettered"`
	ConsumerLag      map[string]int64 `json:"consumerLag"`
	BatchesProcessed int64            `json:"batchesProcessed"`
}

// NewEvent creates a new event with a random ID and current timestamp.
func NewEvent(topic, key string, payload interface{}) (Event, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return Event{}, err
	}
	return Event{
		ID:        idgen.WithPrefix("evt_"),
		Topic:     topic,
		Key:       key,
		Payload:   data,
		Timestamp: time.Now(),
	}, nil
}

// --- Configuration ---

const (
	defaultBufferSize      = 10000
	defaultMaxRetries      = 3
	defaultRetryBaseMs     = 100
	defaultDrainTimeout    = 5 * time.Second
	defaultHandlerTimeout  = 30 * time.Second // max time a handler can run before being cancelled
	healthPendingThreshold = 5000             // events pending before health degrades
)

// --- In-Memory Implementation ---

type subscription struct {
	topic         string
	consumerGroup string
	batchSize     int
	flushInterval time.Duration
	handler       Handler
}

// MemoryBus is a channel-based in-memory event bus with production features.
type MemoryBus struct {
	buffer        chan Event
	subscriptions []subscription
	logger        *slog.Logger
	drainTimeout  time.Duration
	wal           *WALStore // optional: write-ahead log for crash recovery

	// Dead letter queue
	dlq   []Event
	dlqMu sync.Mutex

	// Stats (atomic for lock-free reads)
	published    atomic.Int64
	consumed     atomic.Int64
	dropped      atomic.Int64
	retries      atomic.Int64
	deadLettered atomic.Int64
	batchesProc  atomic.Int64
}

// NewMemoryBus creates an in-memory event bus with the given buffer size.
func NewMemoryBus(bufferSize int, logger *slog.Logger) *MemoryBus {
	if bufferSize <= 0 {
		bufferSize = defaultBufferSize
	}
	return &MemoryBus{
		buffer:       make(chan Event, bufferSize),
		logger:       logger,
		drainTimeout: defaultDrainTimeout,
	}
}

// WithWAL adds a write-ahead log for crash recovery.
// When configured, events are persisted to postgres before entering the buffer.
// On startup, pending events are automatically recovered and republished.
func (b *MemoryBus) WithWAL(wal *WALStore) *MemoryBus {
	b.wal = wal
	return b
}

func (b *MemoryBus) Publish(ctx context.Context, event Event) error {
	// WAL: persist before buffering for crash recovery
	if b.wal != nil {
		if err := b.wal.Write(ctx, event); err != nil {
			b.logger.Error("eventbus: WAL write failed", "event_id", event.ID, "error", err)
			// Continue — don't block the bus if WAL is down
		}
	}

	select {
	case b.buffer <- event:
		b.published.Add(1)
		metrics.EventBusPublished.Inc()
		return nil
	default:
		b.dropped.Add(1)
		metrics.EventBusDropped.Inc()
		b.logger.Warn("eventbus: buffer full, event dropped",
			"topic", event.Topic, "key", event.Key, "event_id", event.ID)
		return ErrBufferFull
	}
}

func (b *MemoryBus) Subscribe(topic, consumerGroup string, batchSize int, flushInterval time.Duration, handler Handler) {
	if batchSize <= 0 {
		batchSize = 100
	}
	if flushInterval <= 0 {
		flushInterval = time.Second
	}
	b.subscriptions = append(b.subscriptions, subscription{
		topic:         topic,
		consumerGroup: consumerGroup,
		batchSize:     batchSize,
		flushInterval: flushInterval,
		handler:       handler,
	})
}

func (b *MemoryBus) Start(ctx context.Context) {
	// WAL recovery: replay events that were in-flight when the process last crashed.
	if b.wal != nil {
		recovered, err := b.wal.RecoverPending(ctx)
		if err != nil {
			b.logger.Error("eventbus: WAL recovery failed", "error", err)
		} else if len(recovered) > 0 {
			for _, e := range recovered {
				select {
				case b.buffer <- e:
					b.published.Add(1)
				default:
					b.logger.Warn("eventbus: WAL recovery dropped event (buffer full)", "event_id", e.ID)
				}
			}
			b.logger.Info("eventbus: WAL recovery complete", "recovered", len(recovered))
		}
	}

	// Periodic metrics gauge update
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				metrics.EventBusPending.Set(float64(b.published.Load() - b.consumed.Load()))
			}
		}
	}()
	subChans := make([]chan Event, len(b.subscriptions))
	for i := range b.subscriptions {
		subChans[i] = make(chan Event, 1000)
	}

	// Router goroutine
	routerDone := make(chan struct{})
	go func() { //nolint:gosec // G118: intentional Background context for drain after shutdown
		defer close(routerDone)
		for {
			select {
			case <-ctx.Done():
				// Graceful drain: read remaining events from buffer before closing
				drainCtx, cancel := context.WithTimeout(context.Background(), b.drainTimeout) //nolint:gosec // intentional: drain runs after request context is cancelled
				b.drainBuffer(drainCtx, subChans)
				cancel()
				for _, ch := range subChans {
					close(ch)
				}
				return
			case event := <-b.buffer:
				for i, sub := range b.subscriptions {
					if sub.topic == event.Topic || sub.topic == "*" {
						select {
						case subChans[i] <- event: //nolint:gosec // i bounded by range over b.subscriptions
						default:
							b.logger.Warn("eventbus: consumer channel full",
								"consumer", sub.consumerGroup, "topic", event.Topic)
						}
					}
				}
			}
		}
	}()

	// Consumer goroutines
	var wg sync.WaitGroup
	for i, sub := range b.subscriptions {
		wg.Add(1)
		go func(idx int, s subscription) {
			defer wg.Done()
			b.consumeLoop(ctx, subChans[idx], s)
		}(i, sub)
	}

	wg.Wait()
}

// drainBuffer reads all remaining events from the buffer during shutdown.
func (b *MemoryBus) drainBuffer(ctx context.Context, subChans []chan Event) {
	drained := 0
	for {
		select {
		case <-ctx.Done():
			b.logger.Info("eventbus: drain timeout", "drained", drained)
			return
		case event, ok := <-b.buffer:
			if !ok {
				return
			}
			for i, sub := range b.subscriptions {
				if sub.topic == event.Topic || sub.topic == "*" {
					ch := subChans[i] //nolint:gosec // i bounded by range over b.subscriptions
					select {
					case ch <- event:
					default:
					}
				}
			}
			drained++
		default:
			if drained > 0 {
				b.logger.Info("eventbus: drain complete", "drained", drained)
			}
			return
		}
	}
}

func (b *MemoryBus) consumeLoop(ctx context.Context, ch <-chan Event, sub subscription) {
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

		// Retry with exponential backoff + handler timeout
		var lastErr error
		for attempt := 0; attempt < defaultMaxRetries; attempt++ {
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

			// Handler timeout: prevent a single slow handler from blocking the consumer forever.
			handlerCtx, handlerCancel := context.WithTimeout(ctx, defaultHandlerTimeout)
			lastErr = sub.handler(handlerCtx, toProcess)
			handlerCancel()

			if lastErr == nil {
				b.consumed.Add(int64(len(toProcess)))
				b.batchesProc.Add(1)
				metrics.EventBusBatchesProcessed.Inc()
				metrics.EventBusConsumed.Add(float64(len(toProcess)))
				// WAL: mark events as processed
				if b.wal != nil {
					for _, e := range toProcess {
						if err := b.wal.MarkProcessed(ctx, e.ID); err != nil {
							b.logger.Warn("eventbus: WAL mark processed failed (event may be reprocessed on restart)",
								"event_id", e.ID, "error", err)
						}
					}
				}
				return
			}
		}

		// All retries exhausted → dead letter queue
		b.logger.Error("eventbus: consumer failed after retries, dead-lettering",
			"consumer", sub.consumerGroup,
			"batch_size", len(toProcess),
			"attempts", defaultMaxRetries,
			"error", lastErr,
		)
		metrics.EventBusErrors.Inc()
		b.dlqMu.Lock()
		for i := range toProcess {
			toProcess[i].Attempt = defaultMaxRetries
			b.dlq = append(b.dlq, toProcess[i])
			// WAL: mark as dead-lettered
			if b.wal != nil {
				if err := b.wal.MarkDeadLettered(ctx, toProcess[i].ID); err != nil {
					b.logger.Warn("eventbus: WAL mark dead-lettered failed",
						"event_id", toProcess[i].ID, "error", err)
				}
			}
		}
		b.deadLettered.Add(int64(len(toProcess)))
		b.dlqMu.Unlock()
		metrics.EventBusDeadLettered.Add(float64(len(toProcess)))
	}

	for {
		select {
		case <-ctx.Done():
			flush()
			return
		case event, ok := <-ch:
			if !ok {
				flush()
				return
			}
			batch = append(batch, event)
			if len(batch) >= sub.batchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

// IsHealthy returns false when pending events exceed threshold.
func (b *MemoryBus) IsHealthy() bool {
	pending := b.published.Load() - b.consumed.Load()
	return pending < healthPendingThreshold
}

// DeadLetterQueue returns events that failed all retry attempts.
func (b *MemoryBus) DeadLetterQueue() []Event {
	b.dlqMu.Lock()
	defer b.dlqMu.Unlock()
	out := make([]Event, len(b.dlq))
	copy(out, b.dlq)
	return out
}

// ReplayDeadLetters moves dead-lettered events back to the main buffer.
func (b *MemoryBus) ReplayDeadLetters(ctx context.Context) int {
	b.dlqMu.Lock()
	events := b.dlq
	b.dlq = nil
	b.dlqMu.Unlock()

	replayed := 0
	for _, e := range events {
		e.Attempt = 0
		if err := b.Publish(ctx, e); err == nil {
			replayed++
		}
	}
	return replayed
}

func (b *MemoryBus) Metrics() BusMetrics {
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

// --- Errors ---

var ErrBufferFull = &busError{"eventbus: buffer full"}

type busError struct{ msg string }

func (e *busError) Error() string { return e.msg }
