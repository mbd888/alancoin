package escrow

import (
	"context"
	"log/slog"
	"math"
	"os"
	"testing"
	"time"
)

func createTestEscrow(svc *Service, buyer, seller, amount string) *Escrow {
	e, _ := svc.Create(context.Background(), CreateRequest{
		BuyerAddr:  buyer,
		SellerAddr: seller,
		Amount:     amount,
	})
	return e
}

func TestEscrowAnalytics_Basic(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger).WithLogger(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	analytics := NewEscrowAnalyticsService(store)

	createTestEscrow(svc, "0xbuyer1", "0xseller1", "10.000000")
	createTestEscrow(svc, "0xbuyer2", "0xseller1", "20.000000")
	createTestEscrow(svc, "0xbuyer1", "0xseller2", "30.000000")

	result, err := analytics.GetAnalytics(context.Background(), AnalyticsFilter{})
	if err != nil {
		t.Fatalf("analytics: %v", err)
	}

	if result.TotalCount != 3 {
		t.Errorf("expected 3 total, got %d", result.TotalCount)
	}
	if result.TotalVolume != "60.000000" {
		t.Errorf("expected volume 60.000000, got %s", result.TotalVolume)
	}
	if result.AvgAmount != "20.000000" {
		t.Errorf("expected avg 20.000000, got %s", result.AvgAmount)
	}
	if result.ByStatus["pending"] != 3 {
		t.Errorf("expected 3 pending, got %d", result.ByStatus["pending"])
	}

	// Top sellers: seller2 has 30, seller1 has 30
	if len(result.TopSellers) != 2 {
		t.Errorf("expected 2 sellers, got %d", len(result.TopSellers))
	}
}

func TestEscrowAnalytics_DisputeRate(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger).WithLogger(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	analytics := NewEscrowAnalyticsService(store)

	// Create 4 escrows: 1 disputed, 1 expired, 2 pending
	createTestEscrow(svc, "0xbuyer", "0xseller", "10.000000")
	createTestEscrow(svc, "0xbuyer", "0xseller", "10.000000")

	e3 := createTestEscrow(svc, "0xbuyer", "0xseller", "10.000000")
	// Mark delivered then dispute
	svc.MarkDelivered(context.Background(), e3.ID, "0xseller")
	svc.Dispute(context.Background(), e3.ID, "0xbuyer", "bad service")

	e4 := createTestEscrow(svc, "0xbuyer", "0xseller", "10.000000")
	// Make it expired by setting auto-release to past
	e4fetched, _ := store.Get(context.Background(), e4.ID)
	e4fetched.Status = StatusExpired
	store.Update(context.Background(), e4fetched)

	result, err := analytics.GetAnalytics(context.Background(), AnalyticsFilter{})
	if err != nil {
		t.Fatalf("analytics: %v", err)
	}

	if result.TotalCount != 4 {
		t.Errorf("expected 4 total, got %d", result.TotalCount)
	}
	// 1 disputed out of 4 = 25%
	if math.Abs(result.DisputeRate-25.0) > 0.01 {
		t.Errorf("expected 25%% dispute rate, got %.2f%%", result.DisputeRate)
	}
	// 1 expired out of 4 = 25%
	if math.Abs(result.AutoReleaseRate-25.0) > 0.01 {
		t.Errorf("expected 25%% auto-release rate, got %.2f%%", result.AutoReleaseRate)
	}
}

func TestEscrowAnalytics_DeliveryTime(t *testing.T) {
	store := NewMemoryStore()
	analytics := NewEscrowAnalyticsService(store)

	now := time.Now()
	delivery1 := now.Add(60 * time.Second)
	delivery2 := now.Add(120 * time.Second)

	store.Create(context.Background(), &Escrow{
		ID:            "esc_1",
		BuyerAddr:     "0xbuyer",
		SellerAddr:    "0xseller",
		Amount:        "10.000000",
		Status:        StatusDelivered,
		CreatedAt:     now,
		DeliveredAt:   &delivery1,
		AutoReleaseAt: now.Add(time.Hour),
	})
	store.Create(context.Background(), &Escrow{
		ID:            "esc_2",
		BuyerAddr:     "0xbuyer",
		SellerAddr:    "0xseller",
		Amount:        "10.000000",
		Status:        StatusDelivered,
		CreatedAt:     now,
		DeliveredAt:   &delivery2,
		AutoReleaseAt: now.Add(time.Hour),
	})

	result, err := analytics.GetAnalytics(context.Background(), AnalyticsFilter{})
	if err != nil {
		t.Fatalf("analytics: %v", err)
	}

	// Average delivery: (60 + 120) / 2 = 90 seconds
	if math.Abs(result.AvgDeliveryTimeSecs-90.0) > 1.0 {
		t.Errorf("expected ~90s avg delivery, got %.2f", result.AvgDeliveryTimeSecs)
	}
}

func TestEscrowAnalytics_FilterBySeller(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger).WithLogger(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	analytics := NewEscrowAnalyticsService(store)

	createTestEscrow(svc, "0xbuyer", "0xseller1", "10.000000")
	createTestEscrow(svc, "0xbuyer", "0xseller2", "20.000000")
	createTestEscrow(svc, "0xbuyer", "0xseller1", "30.000000")

	result, err := analytics.GetAnalytics(context.Background(), AnalyticsFilter{SellerAddr: "0xseller1"})
	if err != nil {
		t.Fatalf("analytics: %v", err)
	}

	if result.TotalCount != 2 {
		t.Errorf("expected 2 for seller1, got %d", result.TotalCount)
	}
	if result.TotalVolume != "40.000000" {
		t.Errorf("expected volume 40.000000, got %s", result.TotalVolume)
	}
}

func TestEscrowAnalytics_Empty(t *testing.T) {
	store := NewMemoryStore()
	analytics := NewEscrowAnalyticsService(store)

	result, err := analytics.GetAnalytics(context.Background(), AnalyticsFilter{})
	if err != nil {
		t.Fatalf("analytics: %v", err)
	}

	if result.TotalCount != 0 {
		t.Errorf("expected 0 total, got %d", result.TotalCount)
	}
	if result.AvgAmount != "0.000000" {
		t.Errorf("expected avg 0.000000, got %s", result.AvgAmount)
	}
	if result.TotalVolume != "0.000000" {
		t.Errorf("expected volume 0.000000, got %s", result.TotalVolume)
	}
	if result.DisputeRate != 0 {
		t.Errorf("expected 0%% dispute rate, got %.2f%%", result.DisputeRate)
	}
}

func TestEscrowAnalytics_FilterByTimeRange(t *testing.T) {
	store := NewMemoryStore()
	analytics := NewEscrowAnalyticsService(store)

	now := time.Now()
	old := now.Add(-48 * time.Hour)
	recent := now.Add(-1 * time.Hour)

	store.Create(context.Background(), &Escrow{
		ID: "esc_old", BuyerAddr: "0xbuyer", SellerAddr: "0xseller",
		Amount: "10.000000", Status: StatusPending,
		CreatedAt: old, AutoReleaseAt: now.Add(time.Hour),
	})
	store.Create(context.Background(), &Escrow{
		ID: "esc_recent", BuyerAddr: "0xbuyer", SellerAddr: "0xseller",
		Amount: "20.000000", Status: StatusPending,
		CreatedAt: recent, AutoReleaseAt: now.Add(time.Hour),
	})

	// Filter: only last 24h
	from := now.Add(-24 * time.Hour)
	result, err := analytics.GetAnalytics(context.Background(), AnalyticsFilter{From: &from})
	if err != nil {
		t.Fatalf("analytics: %v", err)
	}

	if result.TotalCount != 1 {
		t.Errorf("expected 1 recent escrow, got %d", result.TotalCount)
	}
	if result.TotalVolume != "20.000000" {
		t.Errorf("expected volume 20.000000, got %s", result.TotalVolume)
	}
}
