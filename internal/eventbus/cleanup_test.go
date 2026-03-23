package eventbus

import (
	"context"
	"log/slog"
	"testing"
	"time"
)

func TestCleanupWorker_RunOnce_NilComponents(t *testing.T) {
	// With both WAL and outbox nil, runOnce should not panic
	w := NewCleanupWorker(nil, nil, nil, time.Hour, 24*time.Hour, 24*time.Hour, slog.Default())
	w.runOnce(context.Background())
}

func TestCleanupWorker_ContextCancellation(t *testing.T) {
	w := NewCleanupWorker(nil, nil, nil, 50*time.Millisecond, 24*time.Hour, 24*time.Hour, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		w.Start(ctx)
		close(done)
	}()

	// Cancel after a short delay
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// OK, exited promptly
	case <-time.After(2 * time.Second):
		t.Error("cleanup worker did not exit after context cancellation")
	}
}
