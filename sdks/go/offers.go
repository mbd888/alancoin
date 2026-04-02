package alancoin

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// Offer represents a standing marketplace offer from a seller agent.
type Offer struct {
	ID           string    `json:"id"`
	SellerAddr   string    `json:"sellerAddr"`
	ServiceType  string    `json:"serviceType"`
	Description  string    `json:"description,omitempty"`
	Price        string    `json:"price"`
	Capacity     int       `json:"capacity"`
	RemainingCap int       `json:"remainingCap"`
	Status       string    `json:"status"`
	TotalClaims  int       `json:"totalClaims"`
	TotalRevenue string    `json:"totalRevenue"`
	Endpoint     string    `json:"endpoint,omitempty"`
	ExpiresAt    time.Time `json:"expiresAt"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// Claim represents a buyer's claim against a standing offer.
type Claim struct {
	ID         string     `json:"id"`
	OfferID    string     `json:"offerId"`
	BuyerAddr  string     `json:"buyerAddr"`
	SellerAddr string     `json:"sellerAddr"`
	Amount     string     `json:"amount"`
	Status     string     `json:"status"`
	EscrowRef  string     `json:"escrowRef"`
	CreatedAt  time.Time  `json:"createdAt"`
	ResolvedAt *time.Time `json:"resolvedAt,omitempty"`
}

// PostOfferRequest is the input for creating a standing offer.
type PostOfferRequest struct {
	ServiceType string `json:"serviceType"`
	Price       string `json:"price"`
	Capacity    int    `json:"capacity"`
	Description string `json:"description,omitempty"`
	Endpoint    string `json:"endpoint,omitempty"`
}

type offerResponse struct {
	Offer Offer `json:"offer"`
}

type listOffersResponse struct {
	Offers []Offer `json:"offers"`
}

type claimResponse struct {
	Claim Claim `json:"claim"`
}

type listClaimsResponse struct {
	Claims []Claim `json:"claims"`
}

// PostOffer creates a standing offer to sell a service on the marketplace.
func (c *Client) PostOffer(ctx context.Context, req PostOfferRequest) (*Offer, error) {
	var out offerResponse
	if err := c.doJSON(ctx, http.MethodPost, "/v1/offers", &req, &out); err != nil {
		return nil, err
	}
	return &out.Offer, nil
}

// GetOffer retrieves a specific offer by ID.
func (c *Client) GetOffer(ctx context.Context, offerID string) (*Offer, error) {
	var out Offer
	if err := c.doJSON(ctx, http.MethodGet, fmt.Sprintf("/v1/offers/%s", offerID), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListOffers returns active marketplace offers, optionally filtered by service type.
func (c *Client) ListOffers(ctx context.Context, serviceType string, limit int) ([]Offer, error) {
	l := "50"
	if limit > 0 {
		l = fmt.Sprintf("%d", limit)
	}
	path := "/v1/offers" + buildQuery("type", serviceType, "limit", l)
	var out listOffersResponse
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out.Offers, nil
}

// CancelOffer cancels the caller's standing offer.
func (c *Client) CancelOffer(ctx context.Context, offerID string) error {
	return c.doJSON(ctx, http.MethodPost, fmt.Sprintf("/v1/offers/%s/cancel", offerID), nil, nil)
}

// ClaimOffer claims a standing offer, locking escrow and reserving capacity.
func (c *Client) ClaimOffer(ctx context.Context, offerID string) (*Claim, error) {
	var out claimResponse
	if err := c.doJSON(ctx, http.MethodPost, fmt.Sprintf("/v1/offers/%s/claim", offerID), nil, &out); err != nil {
		return nil, err
	}
	return &out.Claim, nil
}

// GetClaim retrieves a specific claim by ID.
func (c *Client) GetClaim(ctx context.Context, claimID string) (*Claim, error) {
	var out Claim
	if err := c.doJSON(ctx, http.MethodGet, fmt.Sprintf("/v1/claims/%s", claimID), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListClaims returns claims for a specific offer.
func (c *Client) ListClaims(ctx context.Context, offerID string, limit int) ([]Claim, error) {
	l := "50"
	if limit > 0 {
		l = fmt.Sprintf("%d", limit)
	}
	path := fmt.Sprintf("/v1/offers/%s/claims", offerID) + buildQuery("limit", l)
	var out listClaimsResponse
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out.Claims, nil
}

// DeliverClaim marks a claimed offer as delivered (seller action).
func (c *Client) DeliverClaim(ctx context.Context, claimID string) (*Claim, error) {
	var out Claim
	if err := c.doJSON(ctx, http.MethodPost, fmt.Sprintf("/v1/claims/%s/deliver", claimID), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CompleteClaim confirms delivery and releases payment (buyer action).
func (c *Client) CompleteClaim(ctx context.Context, claimID string) (*Claim, error) {
	var out Claim
	if err := c.doJSON(ctx, http.MethodPost, fmt.Sprintf("/v1/claims/%s/complete", claimID), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RefundClaim refunds a claim, returning escrowed funds to buyer.
func (c *Client) RefundClaim(ctx context.Context, claimID string) (*Claim, error) {
	var out Claim
	if err := c.doJSON(ctx, http.MethodPost, fmt.Sprintf("/v1/claims/%s/refund", claimID), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
