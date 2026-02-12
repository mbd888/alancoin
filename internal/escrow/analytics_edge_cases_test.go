package escrow

import (
	"context"
	"math"
	"testing"
	"time"
)

func TestEscrowAnalytics_TopSellers_Truncation(t *testing.T) {
	store := NewMemoryStore()
	analytics := NewEscrowAnalyticsService(store)

	// Create 15 sellers (should truncate to top 10)
	for i := 1; i <= 15; i++ {
		amount := "10.000000"
		if i == 1 {
			amount = "1000.000000" // Make seller 1 the top
		}
		store.Create(context.Background(), &Escrow{
			ID:            "esc_" + string(rune('a'+i)),
			BuyerAddr:     "0xbuyer",
			SellerAddr:    "0xseller" + string(rune('0'+i)),
			Amount:        amount,
			Status:        StatusPending,
			CreatedAt:     time.Now(),
			AutoReleaseAt: time.Now().Add(time.Hour),
		})
	}

	result, err := analytics.GetAnalytics(context.Background(), AnalyticsFilter{})
	if err != nil {
		t.Fatalf("analytics: %v", err)
	}

	if len(result.TopSellers) != 10 {
		t.Errorf("expected top 10 sellers, got %d", len(result.TopSellers))
	}

	// Verify top seller is seller1
	if result.TopSellers[0].SellerAddr != "0xseller1" {
		t.Errorf("expected top seller to be seller1, got %s", result.TopSellers[0].SellerAddr)
	}
	if result.TopSellers[0].TotalVolume != "1000.000000" {
		t.Errorf("expected top volume 1000.000000, got %s", result.TopSellers[0].TotalVolume)
	}
}

func TestEscrowAnalytics_FilterByServiceID(t *testing.T) {
	store := NewMemoryStore()
	analytics := NewEscrowAnalyticsService(store)

	store.Create(context.Background(), &Escrow{
		ID:            "esc_1",
		BuyerAddr:     "0xbuyer",
		SellerAddr:    "0xseller",
		Amount:        "10.000000",
		ServiceID:     "svc_alpha",
		Status:        StatusPending,
		CreatedAt:     time.Now(),
		AutoReleaseAt: time.Now().Add(time.Hour),
	})
	store.Create(context.Background(), &Escrow{
		ID:            "esc_2",
		BuyerAddr:     "0xbuyer",
		SellerAddr:    "0xseller",
		Amount:        "20.000000",
		ServiceID:     "svc_beta",
		Status:        StatusPending,
		CreatedAt:     time.Now(),
		AutoReleaseAt: time.Now().Add(time.Hour),
	})
	store.Create(context.Background(), &Escrow{
		ID:            "esc_3",
		BuyerAddr:     "0xbuyer",
		SellerAddr:    "0xseller",
		Amount:        "30.000000",
		ServiceID:     "svc_alpha",
		Status:        StatusPending,
		CreatedAt:     time.Now(),
		AutoReleaseAt: time.Now().Add(time.Hour),
	})

	result, err := analytics.GetAnalytics(context.Background(), AnalyticsFilter{
		ServiceID: "svc_alpha",
	})
	if err != nil {
		t.Fatalf("analytics: %v", err)
	}

	if result.TotalCount != 2 {
		t.Errorf("expected 2 escrows for svc_alpha, got %d", result.TotalCount)
	}
	if result.TotalVolume != "40.000000" {
		t.Errorf("expected volume 40.000000, got %s", result.TotalVolume)
	}
}

func TestEscrowAnalytics_RefundedCountsAsDispute(t *testing.T) {
	store := NewMemoryStore()
	analytics := NewEscrowAnalyticsService(store)

	// Create escrows with various statuses
	store.Create(context.Background(), &Escrow{
		ID: "esc_refunded", BuyerAddr: "0xbuyer", SellerAddr: "0xseller",
		Amount: "10.000000", Status: StatusRefunded,
		CreatedAt: time.Now(), AutoReleaseAt: time.Now().Add(time.Hour),
	})
	store.Create(context.Background(), &Escrow{
		ID: "esc_disputed", BuyerAddr: "0xbuyer", SellerAddr: "0xseller",
		Amount: "10.000000", Status: StatusDisputed,
		CreatedAt: time.Now(), AutoReleaseAt: time.Now().Add(time.Hour),
	})
	store.Create(context.Background(), &Escrow{
		ID: "esc_arbitrating", BuyerAddr: "0xbuyer", SellerAddr: "0xseller",
		Amount: "10.000000", Status: StatusArbitrating,
		CreatedAt: time.Now(), AutoReleaseAt: time.Now().Add(time.Hour),
	})
	store.Create(context.Background(), &Escrow{
		ID: "esc_released", BuyerAddr: "0xbuyer", SellerAddr: "0xseller",
		Amount: "10.000000", Status: StatusReleased,
		CreatedAt: time.Now(), AutoReleaseAt: time.Now().Add(time.Hour),
	})

	result, err := analytics.GetAnalytics(context.Background(), AnalyticsFilter{})
	if err != nil {
		t.Fatalf("analytics: %v", err)
	}

	// 3 out of 4 are dispute-related (refunded, disputed, arbitrating) = 75%
	if math.Abs(result.DisputeRate-75.0) > 0.01 {
		t.Errorf("expected 75%% dispute rate, got %.2f%%", result.DisputeRate)
	}

	if result.ByStatus["refunded"] != 1 {
		t.Errorf("expected 1 refunded, got %d", result.ByStatus["refunded"])
	}
	if result.ByStatus["disputed"] != 1 {
		t.Errorf("expected 1 disputed, got %d", result.ByStatus["disputed"])
	}
	if result.ByStatus["arbitrating"] != 1 {
		t.Errorf("expected 1 arbitrating, got %d", result.ByStatus["arbitrating"])
	}
}

func TestEscrowAnalytics_DeliveryTime_IgnoresNegative(t *testing.T) {
	store := NewMemoryStore()
	analytics := NewEscrowAnalyticsService(store)

	now := time.Now()
	// Create escrow where deliveredAt is BEFORE createdAt (data corruption scenario)
	badDelivery := now.Add(-60 * time.Second)

	store.Create(context.Background(), &Escrow{
		ID: "esc_bad", BuyerAddr: "0xbuyer", SellerAddr: "0xseller",
		Amount: "10.000000", Status: StatusDelivered,
		CreatedAt: now, DeliveredAt: &badDelivery,
		AutoReleaseAt: now.Add(time.Hour),
	})

	goodDelivery := now.Add(120 * time.Second)
	store.Create(context.Background(), &Escrow{
		ID: "esc_good", BuyerAddr: "0xbuyer", SellerAddr: "0xseller",
		Amount: "10.000000", Status: StatusDelivered,
		CreatedAt: now, DeliveredAt: &goodDelivery,
		AutoReleaseAt: now.Add(time.Hour),
	})

	result, err := analytics.GetAnalytics(context.Background(), AnalyticsFilter{})
	if err != nil {
		t.Fatalf("analytics: %v", err)
	}

	// Should only count the good delivery time (120s)
	if math.Abs(result.AvgDeliveryTimeSecs-120.0) > 1.0 {
		t.Errorf("expected ~120s avg delivery (ignoring negative), got %.2f", result.AvgDeliveryTimeSecs)
	}
}

func TestEscrowAnalytics_FilterCombination(t *testing.T) {
	store := NewMemoryStore()
	analytics := NewEscrowAnalyticsService(store)

	now := time.Now()
	old := now.Add(-48 * time.Hour)

	// Seller1, old, service A
	store.Create(context.Background(), &Escrow{
		ID: "esc_1", BuyerAddr: "0xbuyer", SellerAddr: "0xseller1",
		Amount: "10.000000", ServiceID: "svc_a", Status: StatusPending,
		CreatedAt: old, AutoReleaseAt: now.Add(time.Hour),
	})
	// Seller1, recent, service A
	store.Create(context.Background(), &Escrow{
		ID: "esc_2", BuyerAddr: "0xbuyer", SellerAddr: "0xseller1",
		Amount: "20.000000", ServiceID: "svc_a", Status: StatusPending,
		CreatedAt: now, AutoReleaseAt: now.Add(time.Hour),
	})
	// Seller2, recent, service A
	store.Create(context.Background(), &Escrow{
		ID: "esc_3", BuyerAddr: "0xbuyer", SellerAddr: "0xseller2",
		Amount: "30.000000", ServiceID: "svc_a", Status: StatusPending,
		CreatedAt: now, AutoReleaseAt: now.Add(time.Hour),
	})
	// Seller1, recent, service B
	store.Create(context.Background(), &Escrow{
		ID: "esc_4", BuyerAddr: "0xbuyer", SellerAddr: "0xseller1",
		Amount: "40.000000", ServiceID: "svc_b", Status: StatusPending,
		CreatedAt: now, AutoReleaseAt: now.Add(time.Hour),
	})

	// Filter: seller1 + service A + recent (last 24h)
	from := now.Add(-24 * time.Hour)
	result, err := analytics.GetAnalytics(context.Background(), AnalyticsFilter{
		SellerAddr: "0xseller1",
		ServiceID:  "svc_a",
		From:       &from,
	})
	if err != nil {
		t.Fatalf("analytics: %v", err)
	}

	// Should only match esc_2
	if result.TotalCount != 1 {
		t.Errorf("expected 1 escrow matching all filters, got %d", result.TotalCount)
	}
	if result.TotalVolume != "20.000000" {
		t.Errorf("expected volume 20.000000, got %s", result.TotalVolume)
	}
}

func TestEscrowAnalytics_SingleSeller(t *testing.T) {
	store := NewMemoryStore()
	analytics := NewEscrowAnalyticsService(store)

	store.Create(context.Background(), &Escrow{
		ID: "esc_1", BuyerAddr: "0xbuyer", SellerAddr: "0xseller",
		Amount: "100.000000", Status: StatusPending,
		CreatedAt: time.Now(), AutoReleaseAt: time.Now().Add(time.Hour),
	})

	result, err := analytics.GetAnalytics(context.Background(), AnalyticsFilter{})
	if err != nil {
		t.Fatalf("analytics: %v", err)
	}

	if len(result.TopSellers) != 1 {
		t.Errorf("expected 1 top seller, got %d", len(result.TopSellers))
	}
	if result.TopSellers[0].EscrowCount != 1 {
		t.Errorf("expected escrow count 1, got %d", result.TopSellers[0].EscrowCount)
	}
	if result.TopSellers[0].TotalVolume != "100.000000" {
		t.Errorf("expected volume 100.000000, got %s", result.TopSellers[0].TotalVolume)
	}
}

func TestEscrowAnalytics_NoDeliveredEscrows(t *testing.T) {
	store := NewMemoryStore()
	analytics := NewEscrowAnalyticsService(store)

	store.Create(context.Background(), &Escrow{
		ID: "esc_pending", BuyerAddr: "0xbuyer", SellerAddr: "0xseller",
		Amount: "10.000000", Status: StatusPending,
		CreatedAt: time.Now(), AutoReleaseAt: time.Now().Add(time.Hour),
	})

	result, err := analytics.GetAnalytics(context.Background(), AnalyticsFilter{})
	if err != nil {
		t.Fatalf("analytics: %v", err)
	}

	// AvgDeliveryTimeSecs should be 0 when no escrows have delivery times
	if result.AvgDeliveryTimeSecs != 0 {
		t.Errorf("expected 0 avg delivery time, got %.2f", result.AvgDeliveryTimeSecs)
	}
}

func TestMemoryStore_QueryForAnalytics_LimitRespected(t *testing.T) {
	store := NewMemoryStore()

	// Create 100 escrows
	for i := 0; i < 100; i++ {
		store.Create(context.Background(), &Escrow{
			ID:        "esc_" + string(rune('0'+i)),
			BuyerAddr: "0xbuyer", SellerAddr: "0xseller",
			Amount: "10.000000", Status: StatusPending,
			CreatedAt: time.Now(), AutoReleaseAt: time.Now().Add(time.Hour),
		})
	}

	result, err := store.QueryForAnalytics(context.Background(), AnalyticsFilter{}, 50)
	if err != nil {
		t.Fatalf("query: %v", err)
	}

	if len(result) != 50 {
		t.Errorf("expected limit 50 to be respected, got %d", len(result))
	}
}

func TestEscrowAnalytics_FilterTo_ExcludesAfter(t *testing.T) {
	store := NewMemoryStore()
	analytics := NewEscrowAnalyticsService(store)

	now := time.Now()
	before := now.Add(-2 * time.Hour)
	after := now.Add(2 * time.Hour)

	store.Create(context.Background(), &Escrow{
		ID: "esc_before", BuyerAddr: "0xbuyer", SellerAddr: "0xseller",
		Amount: "10.000000", Status: StatusPending,
		CreatedAt: before, AutoReleaseAt: now.Add(time.Hour),
	})
	store.Create(context.Background(), &Escrow{
		ID: "esc_after", BuyerAddr: "0xbuyer", SellerAddr: "0xseller",
		Amount: "20.000000", Status: StatusPending,
		CreatedAt: after, AutoReleaseAt: now.Add(time.Hour),
	})

	// Filter: only escrows before 'now'
	result, err := analytics.GetAnalytics(context.Background(), AnalyticsFilter{
		To: &now,
	})
	if err != nil {
		t.Fatalf("analytics: %v", err)
	}

	if result.TotalCount != 1 {
		t.Errorf("expected 1 escrow before cutoff, got %d", result.TotalCount)
	}
	if result.TotalVolume != "10.000000" {
		t.Errorf("expected volume 10.000000, got %s", result.TotalVolume)
	}
}
