//go:build integration

package offers

import (
	"context"
	"testing"
	"time"

	"github.com/mbd888/alancoin/internal/testutil"
)

func setupPgStore(t *testing.T) (*PostgresStore, func()) {
	t.Helper()
	db, cleanup := testutil.PGTest(t)
	return NewPostgresStore(db), cleanup
}

func makeOffer(id, seller, svcType string) *Offer {
	now := time.Now().Truncate(time.Microsecond)
	return &Offer{
		ID:           id,
		SellerAddr:   seller,
		ServiceType:  svcType,
		Description:  "test offer",
		Price:        "10.000000",
		Capacity:     5,
		RemainingCap: 5,
		Conditions: []Condition{
			{Type: "min_reputation", Value: "0.5"},
		},
		Status:       OfferActive,
		TotalClaims:  0,
		TotalRevenue: "0.000000",
		Endpoint:     "https://example.com/serve",
		ExpiresAt:    now.Add(24 * time.Hour),
		CreatedAt:    now,
		UpdatedAt:    now,
	}
}

func makeClaim(id, offerID, buyer, seller string) *Claim {
	now := time.Now().Truncate(time.Microsecond)
	return &Claim{
		ID:         id,
		OfferID:    offerID,
		BuyerAddr:  buyer,
		SellerAddr: seller,
		Amount:     "10.000000",
		Status:     ClaimPending,
		EscrowRef:  "esc_" + id,
		CreatedAt:  now,
	}
}

// ---------------------------------------------------------------------------
// Offer CRUD
// ---------------------------------------------------------------------------

func TestPostgres_CreateAndGetOffer(t *testing.T) {
	store, cleanup := setupPgStore(t)
	defer cleanup()
	ctx := context.Background()

	o := makeOffer("off_pg_001", "0xseller1", "translation")
	if err := store.CreateOffer(ctx, o); err != nil {
		t.Fatalf("CreateOffer: %v", err)
	}

	got, err := store.GetOffer(ctx, "off_pg_001")
	if err != nil {
		t.Fatalf("GetOffer: %v", err)
	}

	if got.ID != o.ID {
		t.Errorf("ID: want %s, got %s", o.ID, got.ID)
	}
	if got.Price != "10.000000" {
		t.Errorf("Price: want 10.000000, got %s", got.Price)
	}
	if got.Status != OfferActive {
		t.Errorf("Status: want active, got %s", got.Status)
	}
	if len(got.Conditions) != 1 {
		t.Errorf("Conditions: want 1, got %d", len(got.Conditions))
	}
	if got.Endpoint != "https://example.com/serve" {
		t.Errorf("Endpoint: want example.com, got %s", got.Endpoint)
	}
}

func TestPostgres_GetOffer_NotFound(t *testing.T) {
	store, cleanup := setupPgStore(t)
	defer cleanup()

	_, err := store.GetOffer(context.Background(), "nonexistent")
	if err != ErrOfferNotFound {
		t.Errorf("want ErrOfferNotFound, got %v", err)
	}
}

func TestPostgres_UpdateOffer(t *testing.T) {
	store, cleanup := setupPgStore(t)
	defer cleanup()
	ctx := context.Background()

	o := makeOffer("off_pg_002", "0xseller2", "llm")
	store.CreateOffer(ctx, o)

	o.Status = OfferExhausted
	o.RemainingCap = 0
	o.TotalClaims = 5
	o.TotalRevenue = "50.000000"
	o.UpdatedAt = time.Now().Truncate(time.Microsecond)
	if err := store.UpdateOffer(ctx, o); err != nil {
		t.Fatalf("UpdateOffer: %v", err)
	}

	got, _ := store.GetOffer(ctx, o.ID)
	if got.Status != OfferExhausted {
		t.Errorf("Status: want exhausted, got %s", got.Status)
	}
	if got.TotalRevenue != "50.000000" {
		t.Errorf("TotalRevenue: want 50.000000, got %s", got.TotalRevenue)
	}
}

func TestPostgres_ListOffers_ByServiceType(t *testing.T) {
	store, cleanup := setupPgStore(t)
	defer cleanup()
	ctx := context.Background()

	o1 := makeOffer("off_list_1", "0xs1", "llm")
	o1.Price = "5.000000"
	store.CreateOffer(ctx, o1)

	o2 := makeOffer("off_list_2", "0xs2", "llm")
	o2.Price = "3.000000"
	store.CreateOffer(ctx, o2)

	o3 := makeOffer("off_list_3", "0xs3", "embedding")
	store.CreateOffer(ctx, o3)

	got, err := store.ListOffers(ctx, "llm", 10)
	if err != nil {
		t.Fatalf("ListOffers: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 llm offers, got %d", len(got))
	}
	// Should be sorted by price ASC
	if got[0].Price != "3.000000" {
		t.Errorf("first should be cheapest (3.000000), got %s", got[0].Price)
	}
}

func TestPostgres_ListOffers_OnlyActive(t *testing.T) {
	store, cleanup := setupPgStore(t)
	defer cleanup()
	ctx := context.Background()

	active := makeOffer("off_active", "0xs1", "code")
	store.CreateOffer(ctx, active)

	cancelled := makeOffer("off_cancel", "0xs2", "code")
	cancelled.Status = OfferCancelled
	store.CreateOffer(ctx, cancelled)

	got, _ := store.ListOffers(ctx, "code", 10)
	if len(got) != 1 {
		t.Errorf("want 1 active offer, got %d", len(got))
	}
}

func TestPostgres_ListOffersBySeller(t *testing.T) {
	store, cleanup := setupPgStore(t)
	defer cleanup()
	ctx := context.Background()

	store.CreateOffer(ctx, makeOffer("off_s1", "0xsellerA", "llm"))
	store.CreateOffer(ctx, makeOffer("off_s2", "0xsellerA", "code"))
	store.CreateOffer(ctx, makeOffer("off_s3", "0xsellerB", "llm"))

	got, _ := store.ListOffersBySeller(ctx, "0xsellerA", 10)
	if len(got) != 2 {
		t.Errorf("want 2 offers for sellerA, got %d", len(got))
	}
}

func TestPostgres_ListExpiredOffers(t *testing.T) {
	store, cleanup := setupPgStore(t)
	defer cleanup()
	ctx := context.Background()

	past := makeOffer("off_exp", "0xs1", "data")
	past.ExpiresAt = time.Now().Add(-1 * time.Hour)
	store.CreateOffer(ctx, past)

	future := makeOffer("off_fut", "0xs2", "data")
	future.ExpiresAt = time.Now().Add(24 * time.Hour)
	store.CreateOffer(ctx, future)

	got, _ := store.ListExpiredOffers(ctx, time.Now(), 10)
	if len(got) != 1 {
		t.Errorf("want 1 expired offer, got %d", len(got))
	}
	if got[0].ID != "off_exp" {
		t.Errorf("want off_exp, got %s", got[0].ID)
	}
}

func TestPostgres_CreateOffer_NilConditions(t *testing.T) {
	store, cleanup := setupPgStore(t)
	defer cleanup()
	ctx := context.Background()

	o := makeOffer("off_nil", "0xs1", "llm")
	o.Conditions = nil
	store.CreateOffer(ctx, o)

	got, _ := store.GetOffer(ctx, o.ID)
	if got.Conditions == nil {
		t.Error("Conditions should be non-nil empty slice after round-trip")
	}
}

// ---------------------------------------------------------------------------
// Claim CRUD
// ---------------------------------------------------------------------------

func TestPostgres_CreateAndGetClaim(t *testing.T) {
	store, cleanup := setupPgStore(t)
	defer cleanup()
	ctx := context.Background()

	// Create parent offer first (FK requirement)
	o := makeOffer("off_claim_parent", "0xseller", "llm")
	store.CreateOffer(ctx, o)

	c := makeClaim("clm_pg_001", "off_claim_parent", "0xbuyer1", "0xseller")
	if err := store.CreateClaim(ctx, c); err != nil {
		t.Fatalf("CreateClaim: %v", err)
	}

	got, err := store.GetClaim(ctx, "clm_pg_001")
	if err != nil {
		t.Fatalf("GetClaim: %v", err)
	}

	if got.OfferID != "off_claim_parent" {
		t.Errorf("OfferID: want off_claim_parent, got %s", got.OfferID)
	}
	if got.Amount != "10.000000" {
		t.Errorf("Amount: want 10.000000, got %s", got.Amount)
	}
	if got.Status != ClaimPending {
		t.Errorf("Status: want pending, got %s", got.Status)
	}
	if got.ResolvedAt != nil {
		t.Errorf("ResolvedAt: want nil, got %v", got.ResolvedAt)
	}
}

func TestPostgres_GetClaim_NotFound(t *testing.T) {
	store, cleanup := setupPgStore(t)
	defer cleanup()

	_, err := store.GetClaim(context.Background(), "nonexistent")
	if err != ErrClaimNotFound {
		t.Errorf("want ErrClaimNotFound, got %v", err)
	}
}

func TestPostgres_UpdateClaim(t *testing.T) {
	store, cleanup := setupPgStore(t)
	defer cleanup()
	ctx := context.Background()

	o := makeOffer("off_clm_upd", "0xseller", "llm")
	store.CreateOffer(ctx, o)

	c := makeClaim("clm_pg_upd", "off_clm_upd", "0xbuyer", "0xseller")
	store.CreateClaim(ctx, c)

	resolved := time.Now().Truncate(time.Microsecond)
	c.Status = ClaimCompleted
	c.ResolvedAt = &resolved
	if err := store.UpdateClaim(ctx, c); err != nil {
		t.Fatalf("UpdateClaim: %v", err)
	}

	got, _ := store.GetClaim(ctx, c.ID)
	if got.Status != ClaimCompleted {
		t.Errorf("Status: want completed, got %s", got.Status)
	}
	if got.ResolvedAt == nil {
		t.Error("ResolvedAt: want non-nil")
	}
}

func TestPostgres_ListClaimsByOffer(t *testing.T) {
	store, cleanup := setupPgStore(t)
	defer cleanup()
	ctx := context.Background()

	o := makeOffer("off_clm_list", "0xseller", "llm")
	store.CreateOffer(ctx, o)

	for i := 0; i < 3; i++ {
		c := makeClaim("clm_list_"+string(rune('a'+i)), "off_clm_list", "0xbuyer"+string(rune('1'+i)), "0xseller")
		c.CreatedAt = time.Now().Add(time.Duration(-i) * time.Minute).Truncate(time.Microsecond)
		store.CreateClaim(ctx, c)
	}

	got, err := store.ListClaimsByOffer(ctx, "off_clm_list", 10)
	if err != nil {
		t.Fatalf("ListClaimsByOffer: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("want 3 claims, got %d", len(got))
	}
}

func TestPostgres_CreateClaim_InvalidOfferID(t *testing.T) {
	store, cleanup := setupPgStore(t)
	defer cleanup()

	c := makeClaim("clm_fk_fail", "nonexistent_offer", "0xbuyer", "0xseller")
	err := store.CreateClaim(context.Background(), c)
	if err == nil {
		t.Error("CreateClaim with invalid offer_id should fail (FK violation)")
	}
}
