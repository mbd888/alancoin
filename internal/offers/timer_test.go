package offers

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"
)

func TestTimer_StartStop(t *testing.T) {
	svc, _ := newTestService()
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	timer := NewTimer(svc, logger)

	if timer.Running() {
		t.Fatal("timer should not be running before Start")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go timer.Start(ctx)

	// Wait briefly for goroutine to start
	time.Sleep(10 * time.Millisecond)
	if !timer.Running() {
		t.Fatal("timer should be running after Start")
	}

	timer.Stop()
	time.Sleep(10 * time.Millisecond)
	if timer.Running() {
		t.Fatal("timer should not be running after Stop")
	}
}

func TestTimer_ContextCancel(t *testing.T) {
	svc, _ := newTestService()
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	timer := NewTimer(svc, logger)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		timer.Start(ctx)
		close(done)
	}()

	time.Sleep(10 * time.Millisecond)
	if !timer.Running() {
		t.Fatal("timer should be running")
	}

	cancel()

	select {
	case <-done:
		// ok
	case <-time.After(time.Second):
		t.Fatal("timer did not stop after context cancel")
	}

	if timer.Running() {
		t.Fatal("timer should not be running after cancel")
	}
}

func TestTimer_SafeExpire(t *testing.T) {
	svc, _ := newTestService()
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	timer := NewTimer(svc, logger)

	ctx := context.Background()

	// Create an offer that expires immediately
	svc.PostOffer(ctx, "0xSeller", CreateOfferRequest{
		ServiceType: "inference",
		Price:       "0.010000",
		Capacity:    10,
		ExpiresIn:   "1ms",
	})

	time.Sleep(5 * time.Millisecond)

	// Call safeExpire directly to test it without waiting for the ticker
	timer.safeExpire(ctx)

	// Verify the offer got expired
	offers, _ := svc.ListOffers(ctx, "", 50)
	if len(offers) != 0 {
		t.Fatalf("expected 0 active offers after safeExpire, got %d", len(offers))
	}
}
