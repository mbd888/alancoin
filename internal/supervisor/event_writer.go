package supervisor

import (
	"context"
	"fmt"
	"log/slog"
	"math/big"
	"sync/atomic"
	"time"
)

const (
	eventWriterChanSize  = 4096
	eventWriterBatchSize = 100
	eventWriterFlushMs   = 500
)

// eventMsg is the internal message passed through the channel.
type eventMsg struct {
	AgentAddr    string
	Counterparty string
	Amount       *big.Int
	At           time.Time
}

// EventWriter asynchronously batches spend events to a BaselineStore.
type EventWriter struct {
	store   BaselineStore
	logger  *slog.Logger
	ch      chan eventMsg
	stop    chan struct{}
	running atomic.Bool
	dropped atomic.Int64
}

// NewEventWriter creates a new async event writer.
func NewEventWriter(store BaselineStore, logger *slog.Logger) *EventWriter {
	return &EventWriter{
		store:  store,
		logger: logger,
		ch:     make(chan eventMsg, eventWriterChanSize),
		stop:   make(chan struct{}),
	}
}

// Send enqueues a spend event. Non-blocking: drops and increments a counter
// if the channel is full.
func (w *EventWriter) Send(agent, counterparty string, amount *big.Int, at time.Time) {
	msg := eventMsg{
		AgentAddr:    agent,
		Counterparty: counterparty,
		Amount:       new(big.Int).Set(amount),
		At:           at,
	}
	select {
	case w.ch <- msg:
	default:
		w.dropped.Add(1)
	}
}

// Dropped returns the number of events dropped due to a full channel.
func (w *EventWriter) Dropped() int64 {
	return w.dropped.Load()
}

// Start begins draining the channel and flushing batches. Call in a goroutine.
func (w *EventWriter) Start(ctx context.Context) {
	w.running.Store(true)
	defer w.running.Store(false)

	ticker := time.NewTicker(time.Duration(eventWriterFlushMs) * time.Millisecond)
	defer ticker.Stop()

	var buf []*SpendEventRecord

	for {
		select {
		case <-ctx.Done():
			w.flush(buf)
			return
		case <-w.stop:
			w.flush(buf)
			return
		case msg := <-w.ch:
			buf = append(buf, &SpendEventRecord{
				AgentAddr:    msg.AgentAddr,
				Counterparty: msg.Counterparty,
				Amount:       msg.Amount,
				CreatedAt:    msg.At,
			})
			if len(buf) >= eventWriterBatchSize {
				w.flush(buf)
				buf = nil
			}
		case <-ticker.C:
			if len(buf) > 0 {
				w.flush(buf)
				buf = nil
			}
		}
	}
}

// Stop signals the writer to flush remaining events and exit.
func (w *EventWriter) Stop() {
	select {
	case w.stop <- struct{}{}:
	default:
	}
}

// Running reports whether the writer loop is active.
func (w *EventWriter) Running() bool {
	return w.running.Load()
}

func (w *EventWriter) flush(buf []*SpendEventRecord) {
	if len(buf) == 0 {
		return
	}
	w.safeFlush(buf)
}

func (w *EventWriter) safeFlush(buf []*SpendEventRecord) {
	defer func() {
		if r := recover(); r != nil {
			w.logger.Error("panic in event writer flush", "panic", fmt.Sprint(r))
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := w.store.AppendSpendEventBatch(ctx, buf); err != nil {
		w.logger.Error("event writer flush failed", "error", err, "count", len(buf))
	}
}
