package flywheel

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"testing"
)

func TestRevenueAccumulator_Basic(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	ra := NewRevenueAccumulator(logger)
	ctx := context.Background()

	if err := ra.AccumulateRevenue(ctx, "0xSELLER", "10.50", "gateway:sess_1"); err != nil {
		t.Fatal(err)
	}
	if err := ra.AccumulateRevenue(ctx, "0xSELLER", "5.25", "escrow:esc_1"); err != nil {
		t.Fatal(err)
	}
	if err := ra.AccumulateRevenue(ctx, "0xOTHER", "1.00", "stream:str_1"); err != nil {
		t.Fatal(err)
	}

	// Check totals
	if got := ra.TotalRevenue("0xSELLER"); got != 15.75 {
		t.Errorf("TotalRevenue(0xSELLER) = %f, want 15.75", got)
	}
	if got := ra.TotalRevenue("0xOTHER"); got != 1.0 {
		t.Errorf("TotalRevenue(0xOTHER) = %f, want 1.0", got)
	}
	if got := ra.TotalRevenue("0xNONE"); got != 0 {
		t.Errorf("TotalRevenue(0xNONE) = %f, want 0", got)
	}
}

func TestRevenueAccumulator_DrainPending(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	ra := NewRevenueAccumulator(logger)
	ctx := context.Background()

	ra.AccumulateRevenue(ctx, "0xA", "1.00", "ref1")
	ra.AccumulateRevenue(ctx, "0xB", "2.00", "ref2")

	entries := ra.DrainPending()
	if len(entries) != 2 {
		t.Fatalf("DrainPending len = %d, want 2", len(entries))
	}
	if entries[0].AgentAddr != "0xA" || entries[0].Amount != "1.00" {
		t.Errorf("entries[0] = %+v", entries[0])
	}

	// Drain again should be empty
	entries2 := ra.DrainPending()
	if len(entries2) != 0 {
		t.Errorf("second DrainPending len = %d, want 0", len(entries2))
	}

	// But totals should still be there
	if got := ra.TotalRevenue("0xA"); got != 1.0 {
		t.Errorf("TotalRevenue after drain = %f, want 1.0", got)
	}
}

func TestRevenueAccumulator_Concurrent(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	ra := NewRevenueAccumulator(logger)
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ra.AccumulateRevenue(ctx, "0xCONCURRENT", "0.01", "ref")
		}()
	}
	wg.Wait()

	got := ra.TotalRevenue("0xCONCURRENT")
	if got < 0.99 || got > 1.01 {
		t.Errorf("TotalRevenue after concurrent writes = %f, want ~1.0", got)
	}

	entries := ra.DrainPending()
	if len(entries) != 100 {
		t.Errorf("DrainPending len = %d, want 100", len(entries))
	}
}
