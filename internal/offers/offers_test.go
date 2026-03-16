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
