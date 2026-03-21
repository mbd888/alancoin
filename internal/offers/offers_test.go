package offers

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"
)

type mockLedger struct {
	mu       sync.Mutex
	locked   map[string]string
	released map[string]string
	refunded map[string]string
}

func newMockLedger() *mockLedger {
	return &mockLedger{
		locked:   make(map[string]string),
		released: make(map[string]string),
		refunded: make(map[string]string),
	}
}

func (m *mockLedger) EscrowLock(_ context.Context, agentAddr, amount, reference string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.locked[reference] = amount
	return nil
}

func (m *mockLedger) ReleaseEscrow(_ context.Context, buyerAddr, sellerAddr, amount, reference string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.released[reference] = amount
	return nil
}

func (m *mockLedger) RefundEscrow(_ context.Context, agentAddr, amount, reference string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.refunded[reference] = amount
	return nil
}

func newTestService() (*Service, *mockLedger) {
	ml := newMockLedger()
	store := NewMemoryStore()
	svc := NewService(store, ml).WithLogger(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	return svc, ml
}

func TestOffer_PostAndGet(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	offer, err := svc.PostOffer(ctx, "0xSeller", CreateOfferRequest{
		ServiceType: "inference",
		Price:       "0.010000",
		Capacity:    100,
		Description: "GPT-4 inference at $0.01 per call",
	})
	if err != nil {
		t.Fatalf("PostOffer: %v", err)
	}
	if offer.Status != OfferActive {
		t.Fatalf("expected active, got %s", offer.Status)
	}
	if offer.RemainingCap != 100 {
		t.Fatalf("expected 100 remaining, got %d", offer.RemainingCap)
	}
	if offer.SellerAddr != "0xseller" {
		t.Fatalf("expected normalized seller, got %s", offer.SellerAddr)
	}

	got, err := svc.GetOffer(ctx, offer.ID)
	if err != nil {
		t.Fatalf("GetOffer: %v", err)
	}
	if got.ID != offer.ID {
		t.Fatal("wrong offer returned")
	}
}

func TestOffer_ClaimFullLifecycle(t *testing.T) {
	svc, ml := newTestService()
	ctx := context.Background()

	offer, _ := svc.PostOffer(ctx, "0xSeller", CreateOfferRequest{
		ServiceType: "inference",
		Price:       "0.010000",
		Capacity:    5,
	})

	// Claim the offer
	claim, err := svc.ClaimOffer(ctx, offer.ID, "0xBuyer")
	if err != nil {
		t.Fatalf("ClaimOffer: %v", err)
	}
	if claim.Status != ClaimPending {
		t.Fatalf("expected pending, got %s", claim.Status)
	}
	if claim.Amount != "0.010000" {
		t.Fatalf("expected 0.010000, got %s", claim.Amount)
	}

	// Verify funds locked
	if ml.locked[claim.EscrowRef] != "0.010000" {
		t.Fatalf("expected locked 0.010000, got %s", ml.locked[claim.EscrowRef])
	}

	// Check offer capacity decremented
	updated, _ := svc.GetOffer(ctx, offer.ID)
	if updated.RemainingCap != 4 {
		t.Fatalf("expected 4 remaining, got %d", updated.RemainingCap)
	}

	// Complete the claim
	claim, err = svc.CompleteClaim(ctx, claim.ID, "0xBuyer")
	if err != nil {
		t.Fatalf("CompleteClaim: %v", err)
	}
	if claim.Status != ClaimCompleted {
		t.Fatalf("expected completed, got %s", claim.Status)
	}

	// Verify funds released to seller
	if ml.released[claim.EscrowRef] != "0.010000" {
		t.Fatalf("expected released 0.010000, got %s", ml.released[claim.EscrowRef])
	}

	// Verify offer revenue updated
	updated, _ = svc.GetOffer(ctx, offer.ID)
	if updated.TotalRevenue != "0.010000" {
		t.Fatalf("expected revenue 0.010000, got %s", updated.TotalRevenue)
	}
}

func TestOffer_ClaimRefund(t *testing.T) {
	svc, ml := newTestService()
	ctx := context.Background()

	offer, _ := svc.PostOffer(ctx, "0xSeller", CreateOfferRequest{
		ServiceType: "translation",
		Price:       "0.500000",
		Capacity:    1,
	})

	claim, _ := svc.ClaimOffer(ctx, offer.ID, "0xBuyer")

	// Refund the claim
	claim, err := svc.RefundClaim(ctx, claim.ID, "0xBuyer")
	if err != nil {
		t.Fatalf("RefundClaim: %v", err)
	}
	if claim.Status != ClaimRefunded {
		t.Fatalf("expected refunded, got %s", claim.Status)
	}
	if ml.refunded[claim.EscrowRef] != "0.500000" {
		t.Fatalf("expected refund 0.500000, got %s", ml.refunded[claim.EscrowRef])
	}
}

func TestOffer_ExhaustCapacity(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	offer, _ := svc.PostOffer(ctx, "0xSeller", CreateOfferRequest{
		ServiceType: "inference",
		Price:       "0.010000",
		Capacity:    2,
	})

	// Claim twice
	svc.ClaimOffer(ctx, offer.ID, "0xBuyer1")
	svc.ClaimOffer(ctx, offer.ID, "0xBuyer2")

	// Third claim should fail
	_, err := svc.ClaimOffer(ctx, offer.ID, "0xBuyer3")
	if !errors.Is(err, ErrOfferExhausted) {
		t.Fatalf("expected ErrOfferExhausted, got %v", err)
	}

	// Offer should be exhausted
	updated, _ := svc.GetOffer(ctx, offer.ID)
	if updated.Status != OfferExhausted {
		t.Fatalf("expected exhausted, got %s", updated.Status)
	}
}

func TestOffer_SelfClaim(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	offer, _ := svc.PostOffer(ctx, "0xSeller", CreateOfferRequest{
		ServiceType: "inference",
		Price:       "0.010000",
		Capacity:    10,
	})

	_, err := svc.ClaimOffer(ctx, offer.ID, "0xSeller")
	if !errors.Is(err, ErrSelfClaim) {
		t.Fatalf("expected ErrSelfClaim, got %v", err)
	}
}

func TestOffer_Cancel(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	offer, _ := svc.PostOffer(ctx, "0xSeller", CreateOfferRequest{
		ServiceType: "inference",
		Price:       "0.010000",
		Capacity:    10,
	})

	// Cancel
	offer, err := svc.CancelOffer(ctx, offer.ID, "0xSeller")
	if err != nil {
		t.Fatalf("CancelOffer: %v", err)
	}
	if offer.Status != OfferCancelled {
		t.Fatalf("expected cancelled, got %s", offer.Status)
	}

	// Claim cancelled offer should fail
	_, err = svc.ClaimOffer(ctx, offer.ID, "0xBuyer")
	if !errors.Is(err, ErrOfferCancelled) {
		t.Fatalf("expected ErrOfferCancelled, got %v", err)
	}
}

func TestOffer_CancelUnauthorized(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	offer, _ := svc.PostOffer(ctx, "0xSeller", CreateOfferRequest{
		ServiceType: "inference",
		Price:       "0.010000",
		Capacity:    10,
	})

	_, err := svc.CancelOffer(ctx, offer.ID, "0xStranger")
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got %v", err)
	}
}

func TestOffer_CompleteUnauthorized(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	offer, _ := svc.PostOffer(ctx, "0xSeller", CreateOfferRequest{
		ServiceType: "inference",
		Price:       "0.010000",
		Capacity:    10,
	})
	claim, _ := svc.ClaimOffer(ctx, offer.ID, "0xBuyer")

	// Seller can't complete — only buyer
	_, err := svc.CompleteClaim(ctx, claim.ID, "0xSeller")
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got %v", err)
	}
}

func TestOffer_RefundBySeller(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	offer, _ := svc.PostOffer(ctx, "0xSeller", CreateOfferRequest{
		ServiceType: "inference",
		Price:       "0.010000",
		Capacity:    10,
	})
	claim, _ := svc.ClaimOffer(ctx, offer.ID, "0xBuyer")

	// Seller CAN refund (mutual agreement)
	claim, err := svc.RefundClaim(ctx, claim.ID, "0xSeller")
	if err != nil {
		t.Fatalf("RefundClaim by seller: %v", err)
	}
	if claim.Status != ClaimRefunded {
		t.Fatalf("expected refunded, got %s", claim.Status)
	}
}

func TestOffer_DoubleComplete(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	offer, _ := svc.PostOffer(ctx, "0xSeller", CreateOfferRequest{
		ServiceType: "inference",
		Price:       "0.010000",
		Capacity:    10,
	})
	claim, _ := svc.ClaimOffer(ctx, offer.ID, "0xBuyer")

	svc.CompleteClaim(ctx, claim.ID, "0xBuyer")

	_, err := svc.CompleteClaim(ctx, claim.ID, "0xBuyer")
	if !errors.Is(err, ErrClaimNotPending) {
		t.Fatalf("expected ErrClaimNotPending, got %v", err)
	}
}

func TestOffer_InvalidPrice(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	_, err := svc.PostOffer(ctx, "0xSeller", CreateOfferRequest{
		ServiceType: "inference",
		Price:       "-1.000000",
		Capacity:    10,
	})
	if !errors.Is(err, ErrInvalidPrice) {
		t.Fatalf("expected ErrInvalidPrice, got %v", err)
	}

	_, err = svc.PostOffer(ctx, "0xSeller", CreateOfferRequest{
		ServiceType: "inference",
		Price:       "",
		Capacity:    10,
	})
	if !errors.Is(err, ErrInvalidPrice) {
		t.Fatalf("expected ErrInvalidPrice for empty, got %v", err)
	}
}

func TestOffer_ListByServiceType(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	svc.PostOffer(ctx, "0xSeller1", CreateOfferRequest{
		ServiceType: "inference",
		Price:       "0.010000",
		Capacity:    10,
	})
	svc.PostOffer(ctx, "0xSeller2", CreateOfferRequest{
		ServiceType: "translation",
		Price:       "0.050000",
		Capacity:    5,
	})
	svc.PostOffer(ctx, "0xSeller3", CreateOfferRequest{
		ServiceType: "inference",
		Price:       "0.020000",
		Capacity:    20,
	})

	// List inference offers only
	offers, err := svc.ListOffers(ctx, "inference", 50)
	if err != nil {
		t.Fatalf("ListOffers: %v", err)
	}
	if len(offers) != 2 {
		t.Fatalf("expected 2 inference offers, got %d", len(offers))
	}

	// List all
	all, _ := svc.ListOffers(ctx, "", 50)
	if len(all) != 3 {
		t.Fatalf("expected 3 total offers, got %d", len(all))
	}
}

func TestOffer_ListBySeller(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	svc.PostOffer(ctx, "0xSeller1", CreateOfferRequest{
		ServiceType: "inference",
		Price:       "0.010000",
		Capacity:    10,
	})
	svc.PostOffer(ctx, "0xSeller1", CreateOfferRequest{
		ServiceType: "translation",
		Price:       "0.050000",
		Capacity:    5,
	})
	svc.PostOffer(ctx, "0xSeller2", CreateOfferRequest{
		ServiceType: "inference",
		Price:       "0.020000",
		Capacity:    20,
	})

	offers, _ := svc.ListOffersBySeller(ctx, "0xSeller1", 50)
	if len(offers) != 2 {
		t.Fatalf("expected 2 offers for seller1, got %d", len(offers))
	}
}

func TestOffer_ConditionAllowedBuyers(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	offer, _ := svc.PostOffer(ctx, "0xSeller", CreateOfferRequest{
		ServiceType: "inference",
		Price:       "0.010000",
		Capacity:    10,
		Conditions: []Condition{
			{Type: "allowed_buyers", Value: "0xbuyer1,0xbuyer2"},
		},
	})

	// Allowed buyer
	_, err := svc.ClaimOffer(ctx, offer.ID, "0xBuyer1")
	if err != nil {
		t.Fatalf("allowed buyer should claim: %v", err)
	}

	// Disallowed buyer
	_, err = svc.ClaimOffer(ctx, offer.ID, "0xStranger")
	if !errors.Is(err, ErrConditionNotMet) {
		t.Fatalf("expected ErrConditionNotMet, got %v", err)
	}
}

func TestOffer_NotFound(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	_, err := svc.GetOffer(ctx, "nonexistent")
	if !errors.Is(err, ErrOfferNotFound) {
		t.Fatalf("expected ErrOfferNotFound, got %v", err)
	}

	_, err = svc.ClaimOffer(ctx, "nonexistent", "0xBuyer")
	if !errors.Is(err, ErrOfferNotFound) {
		t.Fatalf("expected ErrOfferNotFound, got %v", err)
	}

	_, err = svc.GetClaim(ctx, "nonexistent")
	if !errors.Is(err, ErrClaimNotFound) {
		t.Fatalf("expected ErrClaimNotFound, got %v", err)
	}
}

func TestOffer_ForceExpire(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	// Create offer with very short expiry
	svc.PostOffer(ctx, "0xSeller", CreateOfferRequest{
		ServiceType: "inference",
		Price:       "0.010000",
		Capacity:    10,
		ExpiresIn:   "1ms",
	})

	// Wait for expiry to pass
	time.Sleep(5 * time.Millisecond)

	count, err := svc.ForceExpireOffers(ctx)
	if err != nil {
		t.Fatalf("ForceExpireOffers: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 expired, got %d", count)
	}
}

func TestOffer_ConcurrentClaims(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	offer, _ := svc.PostOffer(ctx, "0xSeller", CreateOfferRequest{
		ServiceType: "inference",
		Price:       "0.010000",
		Capacity:    5,
	})

	// 10 concurrent claims on a capacity-5 offer
	var wg sync.WaitGroup
	successes := make(chan struct{}, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			buyer := "0xbuyer" + string(rune('a'+idx))
			_, err := svc.ClaimOffer(ctx, offer.ID, buyer)
			if err == nil {
				successes <- struct{}{}
			}
		}(i)
	}
	wg.Wait()
	close(successes)

	count := 0
	for range successes {
		count++
	}
	if count != 5 {
		t.Fatalf("expected exactly 5 successful claims, got %d", count)
	}

	// Offer should be exhausted
	updated, _ := svc.GetOffer(ctx, offer.ID)
	if updated.RemainingCap != 0 {
		t.Fatalf("expected 0 remaining, got %d", updated.RemainingCap)
	}
}

func TestOffer_DeepCopy(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	o := &Offer{
		ID:         "ofr_test",
		SellerAddr: "seller",
		Status:     OfferActive,
		Conditions: []Condition{{Type: "min_reputation", Value: "50"}},
	}
	store.CreateOffer(ctx, o)

	// Mutate original
	o.Conditions = append(o.Conditions, Condition{Type: "extra"})

	got, _ := store.GetOffer(ctx, "ofr_test")
	if len(got.Conditions) != 1 {
		t.Fatalf("deep copy broken: got %d conditions", len(got.Conditions))
	}
}

func TestOffer_DeliverThenComplete(t *testing.T) {
	svc, ml := newTestService()
	ctx := context.Background()

	offer, _ := svc.PostOffer(ctx, "0xSeller", CreateOfferRequest{
		ServiceType: "inference",
		Price:       "0.010000",
		Capacity:    10,
	})
	claim, _ := svc.ClaimOffer(ctx, offer.ID, "0xBuyer")

	// Seller delivers
	claim, err := svc.DeliverClaim(ctx, claim.ID, "0xSeller")
	if err != nil {
		t.Fatalf("DeliverClaim: %v", err)
	}
	if claim.Status != ClaimDelivered {
		t.Fatalf("expected delivered, got %s", claim.Status)
	}

	// Buyer completes (from delivered state)
	claim, err = svc.CompleteClaim(ctx, claim.ID, "0xBuyer")
	if err != nil {
		t.Fatalf("CompleteClaim from delivered: %v", err)
	}
	if claim.Status != ClaimCompleted {
		t.Fatalf("expected completed, got %s", claim.Status)
	}
	if ml.released[claim.EscrowRef] != "0.010000" {
		t.Fatalf("expected released 0.010000")
	}
}

func TestOffer_DeliverByBuyer_Unauthorized(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	offer, _ := svc.PostOffer(ctx, "0xSeller", CreateOfferRequest{
		ServiceType: "inference",
		Price:       "0.010000",
		Capacity:    10,
	})
	claim, _ := svc.ClaimOffer(ctx, offer.ID, "0xBuyer")

	// Buyer can't deliver (only seller can)
	_, err := svc.DeliverClaim(ctx, claim.ID, "0xBuyer")
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized for buyer deliver, got %v", err)
	}
}

func TestOffer_DeliverAlreadyDelivered(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	offer, _ := svc.PostOffer(ctx, "0xSeller", CreateOfferRequest{
		ServiceType: "inference",
		Price:       "0.010000",
		Capacity:    10,
	})
	claim, _ := svc.ClaimOffer(ctx, offer.ID, "0xBuyer")
	svc.DeliverClaim(ctx, claim.ID, "0xSeller")

	// Double deliver should fail
	_, err := svc.DeliverClaim(ctx, claim.ID, "0xSeller")
	if !errors.Is(err, ErrClaimNotPending) {
		t.Fatalf("expected ErrClaimNotPending, got %v", err)
	}
}

func TestOffer_RefundFromDelivered(t *testing.T) {
	svc, ml := newTestService()
	ctx := context.Background()

	offer, _ := svc.PostOffer(ctx, "0xSeller", CreateOfferRequest{
		ServiceType: "inference",
		Price:       "0.010000",
		Capacity:    10,
	})
	claim, _ := svc.ClaimOffer(ctx, offer.ID, "0xBuyer")
	svc.DeliverClaim(ctx, claim.ID, "0xSeller")

	// Buyer can refund from delivered state (dispute)
	claim, err := svc.RefundClaim(ctx, claim.ID, "0xBuyer")
	if err != nil {
		t.Fatalf("RefundClaim from delivered: %v", err)
	}
	if claim.Status != ClaimRefunded {
		t.Fatalf("expected refunded, got %s", claim.Status)
	}
	if ml.refunded[claim.EscrowRef] != "0.010000" {
		t.Fatalf("expected refund 0.010000")
	}
}

// --- Additional service-level tests for uncovered paths ---

type mockRecorder struct {
	recorded []string
}

func (m *mockRecorder) RecordTransaction(_ context.Context, txHash, from, to, amount, serviceID, status string) error {
	m.recorded = append(m.recorded, txHash)
	return nil
}

type mockRevenue struct {
	accumulated []string
}

func (m *mockRevenue) AccumulateRevenue(_ context.Context, agentAddr, amount, txRef string) error {
	m.accumulated = append(m.accumulated, txRef)
	return nil
}

func TestOffer_WithRecorderAndRevenue(t *testing.T) {
	ml := newMockLedger()
	store := NewMemoryStore()
	rec := &mockRecorder{}
	rev := &mockRevenue{}
	svc := NewService(store, ml).
		WithLogger(slog.New(slog.NewTextHandler(os.Stderr, nil))).
		WithRecorder(rec).
		WithRevenueAccumulator(rev)

	ctx := context.Background()

	offer, err := svc.PostOffer(ctx, "0xSeller", CreateOfferRequest{
		ServiceType: "inference",
		Price:       "0.010000",
		Capacity:    10,
	})
	if err != nil {
		t.Fatalf("PostOffer: %v", err)
	}

	claim, err := svc.ClaimOffer(ctx, offer.ID, "0xBuyer")
	if err != nil {
		t.Fatalf("ClaimOffer: %v", err)
	}

	// Complete triggers recorder and revenue accumulator
	_, err = svc.CompleteClaim(ctx, claim.ID, "0xBuyer")
	if err != nil {
		t.Fatalf("CompleteClaim: %v", err)
	}

	if len(rec.recorded) != 1 {
		t.Fatalf("expected 1 recorded transaction, got %d", len(rec.recorded))
	}
	if len(rev.accumulated) != 1 {
		t.Fatalf("expected 1 accumulated revenue, got %d", len(rev.accumulated))
	}
}

func TestOffer_ClaimExpiredOffer(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	// Create an offer that expires very quickly
	offer, _ := svc.PostOffer(ctx, "0xSeller", CreateOfferRequest{
		ServiceType: "inference",
		Price:       "0.010000",
		Capacity:    10,
		ExpiresIn:   "1ms",
	})

	time.Sleep(5 * time.Millisecond)

	// Claiming an expired offer
	_, err := svc.ClaimOffer(ctx, offer.ID, "0xBuyer")
	if !errors.Is(err, ErrOfferExpired) {
		t.Fatalf("expected ErrOfferExpired, got %v", err)
	}

	// Verify the offer status was updated to expired
	updated, _ := svc.GetOffer(ctx, offer.ID)
	if updated.Status != OfferExpired {
		t.Fatalf("expected status expired, got %s", updated.Status)
	}
}

func TestOffer_PostOffer_InvalidCapacity(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	tests := []struct {
		name     string
		capacity int
	}{
		{"zero", 0},
		{"negative", -1},
		{"over_max", MaxCapacity + 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := svc.PostOffer(ctx, "0xSeller", CreateOfferRequest{
				ServiceType: "inference",
				Price:       "0.010000",
				Capacity:    tt.capacity,
			})
			if err == nil {
				t.Fatal("expected error for invalid capacity")
			}
		})
	}
}

func TestOffer_PostOffer_EmptyServiceType(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	_, err := svc.PostOffer(ctx, "0xSeller", CreateOfferRequest{
		ServiceType: "",
		Price:       "0.010000",
		Capacity:    10,
	})
	if err == nil {
		t.Fatal("expected error for empty service type")
	}
}

func TestOffer_PostOffer_CustomExpiry(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	offer, err := svc.PostOffer(ctx, "0xSeller", CreateOfferRequest{
		ServiceType: "inference",
		Price:       "0.010000",
		Capacity:    10,
		ExpiresIn:   "48h",
	})
	if err != nil {
		t.Fatalf("PostOffer: %v", err)
	}

	// Verify expiry is ~48h from now, not the default 24h
	expectedExpiry := offer.CreatedAt.Add(48 * time.Hour)
	diff := offer.ExpiresAt.Sub(expectedExpiry)
	if diff < -time.Second || diff > time.Second {
		t.Fatalf("expected expiry near %v, got %v", expectedExpiry, offer.ExpiresAt)
	}
}

func TestOffer_PostOffer_InvalidExpiryFallsBackToDefault(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	offer, err := svc.PostOffer(ctx, "0xSeller", CreateOfferRequest{
		ServiceType: "inference",
		Price:       "0.010000",
		Capacity:    10,
		ExpiresIn:   "notaduration",
	})
	if err != nil {
		t.Fatalf("PostOffer: %v", err)
	}

	// Should fall back to DefaultOfferExpiry (24h)
	expectedExpiry := offer.CreatedAt.Add(DefaultOfferExpiry)
	diff := offer.ExpiresAt.Sub(expectedExpiry)
	if diff < -time.Second || diff > time.Second {
		t.Fatalf("expected default expiry, got %v", offer.ExpiresAt)
	}
}

func TestOffer_PostOffer_StoreError(t *testing.T) {
	ml := newMockLedger()
	store := &failingStore{err: errors.New("store broken"), MemoryStore: NewMemoryStore()}
	svc := NewService(store, ml).WithLogger(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	ctx := context.Background()

	_, err := svc.PostOffer(ctx, "0xSeller", CreateOfferRequest{
		ServiceType: "inference",
		Price:       "0.010000",
		Capacity:    10,
	})
	if err == nil {
		t.Fatal("expected error from store")
	}
}

func TestOffer_CancelAlreadyTerminal(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	offer, _ := svc.PostOffer(ctx, "0xSeller", CreateOfferRequest{
		ServiceType: "inference",
		Price:       "0.010000",
		Capacity:    10,
	})

	// Cancel once
	svc.CancelOffer(ctx, offer.ID, "0xSeller")

	// Cancel again should fail
	_, err := svc.CancelOffer(ctx, offer.ID, "0xSeller")
	if err == nil {
		t.Fatal("expected error cancelling already-cancelled offer")
	}
}

func TestOffer_ListClaimsByOffer(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	offer, _ := svc.PostOffer(ctx, "0xSeller", CreateOfferRequest{
		ServiceType: "inference",
		Price:       "0.010000",
		Capacity:    10,
	})

	svc.ClaimOffer(ctx, offer.ID, "0xBuyer1")
	svc.ClaimOffer(ctx, offer.ID, "0xBuyer2")
	svc.ClaimOffer(ctx, offer.ID, "0xBuyer3")

	claims, err := svc.ListClaimsByOffer(ctx, offer.ID, 0)
	if err != nil {
		t.Fatalf("ListClaimsByOffer: %v", err)
	}
	if len(claims) != 3 {
		t.Fatalf("expected 3 claims, got %d", len(claims))
	}
}

func TestOffer_ListClaimsByOffer_DefaultLimit(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	offer, _ := svc.PostOffer(ctx, "0xSeller", CreateOfferRequest{
		ServiceType: "inference",
		Price:       "0.010000",
		Capacity:    10,
	})

	svc.ClaimOffer(ctx, offer.ID, "0xBuyer1")

	// Limit <= 0 defaults to 50
	claims, err := svc.ListClaimsByOffer(ctx, offer.ID, -1)
	if err != nil {
		t.Fatalf("ListClaimsByOffer: %v", err)
	}
	if len(claims) != 1 {
		t.Fatalf("expected 1 claim, got %d", len(claims))
	}
}

func TestOffer_ListOffers_DefaultLimit(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	svc.PostOffer(ctx, "0xSeller", CreateOfferRequest{
		ServiceType: "inference",
		Price:       "0.010000",
		Capacity:    10,
	})

	offers, err := svc.ListOffers(ctx, "", 0)
	if err != nil {
		t.Fatalf("ListOffers: %v", err)
	}
	if len(offers) != 1 {
		t.Fatalf("expected 1 offer, got %d", len(offers))
	}
}

func TestOffer_ListOffersBySeller_DefaultLimit(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	svc.PostOffer(ctx, "0xSeller", CreateOfferRequest{
		ServiceType: "inference",
		Price:       "0.010000",
		Capacity:    10,
	})

	offers, err := svc.ListOffersBySeller(ctx, "0xSeller", 0)
	if err != nil {
		t.Fatalf("ListOffersBySeller: %v", err)
	}
	if len(offers) != 1 {
		t.Fatalf("expected 1 offer, got %d", len(offers))
	}
}

func TestOffer_RefundByStranger_Unauthorized(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	offer, _ := svc.PostOffer(ctx, "0xSeller", CreateOfferRequest{
		ServiceType: "inference",
		Price:       "0.010000",
		Capacity:    10,
	})
	claim, _ := svc.ClaimOffer(ctx, offer.ID, "0xBuyer")

	// Stranger can't refund
	_, err := svc.RefundClaim(ctx, claim.ID, "0xStranger")
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got %v", err)
	}
}

func TestOffer_IsTerminal(t *testing.T) {
	tests := []struct {
		status   OfferStatus
		terminal bool
	}{
		{OfferActive, false},
		{OfferExhausted, true},
		{OfferCancelled, true},
		{OfferExpired, true},
	}
	for _, tt := range tests {
		o := &Offer{Status: tt.status}
		if o.IsTerminal() != tt.terminal {
			t.Errorf("IsTerminal(%s): expected %v, got %v", tt.status, tt.terminal, o.IsTerminal())
		}
	}
}

func TestOffer_ValidatePrice(t *testing.T) {
	tests := []struct {
		name    string
		price   string
		wantErr bool
	}{
		{"valid", "1.000000", false},
		{"valid_small", "0.000001", false},
		{"empty", "", true},
		{"spaces_only", "   ", true},
		{"negative", "-1.000000", true},
		{"zero", "0.000000", true},
		{"not_a_number", "abc", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePrice(tt.price)
			if (err != nil) != tt.wantErr {
				t.Errorf("validatePrice(%q) error = %v, wantErr %v", tt.price, err, tt.wantErr)
			}
		})
	}
}

// failingStore wraps MemoryStore and lets you inject failures per method.
type failingStore struct {
	*MemoryStore
	err             error
	failCreateClaim bool
	failUpdateOffer bool
}

func (s *failingStore) CreateOffer(ctx context.Context, o *Offer) error {
	if s.err != nil {
		return s.err
	}
	return s.MemoryStore.CreateOffer(ctx, o)
}

func (s *failingStore) CreateClaim(ctx context.Context, c *Claim) error {
	if s.failCreateClaim {
		return errors.New("store: create claim failed")
	}
	return s.MemoryStore.CreateClaim(ctx, c)
}

func (s *failingStore) UpdateOffer(ctx context.Context, o *Offer) error {
	if s.failUpdateOffer {
		return errors.New("store: update offer failed")
	}
	return s.MemoryStore.UpdateOffer(ctx, o)
}

// failingLedger lets you inject failures in EscrowLock.
type failingLedger struct {
	failEscrowLock bool
}

func (f *failingLedger) EscrowLock(_ context.Context, _, _, _ string) error {
	if f.failEscrowLock {
		return errors.New("ledger: insufficient funds")
	}
	return nil
}

func (f *failingLedger) ReleaseEscrow(_ context.Context, _, _, _, _ string) error {
	return nil
}

func (f *failingLedger) RefundEscrow(_ context.Context, _, _, _ string) error {
	return nil
}

func TestOffer_ClaimOffer_EscrowLockFails(t *testing.T) {
	fl := &failingLedger{failEscrowLock: true}
	store := NewMemoryStore()
	svc := NewService(store, fl).WithLogger(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	ctx := context.Background()

	offer, _ := svc.PostOffer(ctx, "0xSeller", CreateOfferRequest{
		ServiceType: "inference",
		Price:       "0.010000",
		Capacity:    10,
	})

	_, err := svc.ClaimOffer(ctx, offer.ID, "0xBuyer")
	if err == nil {
		t.Fatal("expected error from escrow lock failure")
	}
}

func TestOffer_ClaimOffer_UpdateOfferFails(t *testing.T) {
	ml := newMockLedger()
	store := &failingStore{MemoryStore: NewMemoryStore()}
	svc := NewService(store, ml).WithLogger(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	ctx := context.Background()

	offer, _ := svc.PostOffer(ctx, "0xSeller", CreateOfferRequest{
		ServiceType: "inference",
		Price:       "0.010000",
		Capacity:    10,
	})

	// Make UpdateOffer fail after escrow lock succeeds
	store.failUpdateOffer = true

	_, err := svc.ClaimOffer(ctx, offer.ID, "0xBuyer")
	if err == nil {
		t.Fatal("expected error from update offer failure")
	}

	// Verify that funds were refunded
	if len(ml.refunded) == 0 {
		t.Fatal("expected refund after update offer failure")
	}
}

func TestOffer_ClaimOffer_CreateClaimFails(t *testing.T) {
	ml := newMockLedger()
	store := &failingStore{MemoryStore: NewMemoryStore()}
	svc := NewService(store, ml).WithLogger(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	ctx := context.Background()

	offer, _ := svc.PostOffer(ctx, "0xSeller", CreateOfferRequest{
		ServiceType: "inference",
		Price:       "0.010000",
		Capacity:    1,
	})

	// Make CreateClaim fail after capacity is decremented
	store.failCreateClaim = true

	_, err := svc.ClaimOffer(ctx, offer.ID, "0xBuyer")
	if err == nil {
		t.Fatal("expected error from create claim failure")
	}

	// Verify capacity was rolled back
	store.failCreateClaim = false
	updated, _ := svc.GetOffer(ctx, offer.ID)
	if updated.RemainingCap != 1 {
		t.Fatalf("expected capacity rolled back to 1, got %d", updated.RemainingCap)
	}
	// Since capacity was 1 and exhausted temporarily, status should be rolled back to active
	if updated.Status != OfferActive {
		t.Fatalf("expected status active after rollback, got %s", updated.Status)
	}
}

func TestOffer_MemoryStore_ListClaimsByBuyer(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	c1 := &Claim{ID: "clm_1", OfferID: "ofr_1", BuyerAddr: "buyer1", Amount: "1.00"}
	c2 := &Claim{ID: "clm_2", OfferID: "ofr_2", BuyerAddr: "buyer1", Amount: "2.00"}
	c3 := &Claim{ID: "clm_3", OfferID: "ofr_3", BuyerAddr: "buyer2", Amount: "3.00"}
	store.CreateClaim(ctx, c1)
	store.CreateClaim(ctx, c2)
	store.CreateClaim(ctx, c3)

	claims, err := store.ListClaimsByBuyer(ctx, "buyer1", 50)
	if err != nil {
		t.Fatalf("ListClaimsByBuyer: %v", err)
	}
	if len(claims) != 2 {
		t.Fatalf("expected 2 claims for buyer1, got %d", len(claims))
	}

	// Test with limit
	claims, _ = store.ListClaimsByBuyer(ctx, "buyer1", 1)
	if len(claims) != 1 {
		t.Fatalf("expected 1 claim with limit, got %d", len(claims))
	}
}
