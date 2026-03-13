package alancoin

import (
	"context"
	"fmt"
	"net/http"
)

// CreateEscrow creates a new buyer-protection escrow.
func (c *Client) CreateEscrow(ctx context.Context, req CreateEscrowRequest) (*Escrow, error) {
	var out escrowResponse
	if err := c.doJSON(ctx, http.MethodPost, "/v1/escrow", &req, &out); err != nil {
		return nil, err
	}
	return &out.Escrow, nil
}

// GetEscrow retrieves an escrow by ID.
func (c *Client) GetEscrow(ctx context.Context, escrowID string) (*Escrow, error) {
	var out escrowResponse
	if err := c.doJSON(ctx, http.MethodGet, fmt.Sprintf("/v1/escrow/%s", escrowID), nil, &out); err != nil {
		return nil, err
	}
	return &out.Escrow, nil
}

// ConfirmEscrow confirms delivery and releases funds to the seller.
func (c *Client) ConfirmEscrow(ctx context.Context, escrowID string) (*Escrow, error) {
	var out escrowResponse
	if err := c.doJSON(ctx, http.MethodPost, fmt.Sprintf("/v1/escrow/%s/confirm", escrowID), nil, &out); err != nil {
		return nil, err
	}
	return &out.Escrow, nil
}

// DisputeEscrow disputes an escrow, providing a reason.
func (c *Client) DisputeEscrow(ctx context.Context, escrowID, reason string) (*Escrow, error) {
	req := struct {
		Reason string `json:"reason"`
	}{Reason: reason}
	var out escrowResponse
	if err := c.doJSON(ctx, http.MethodPost, fmt.Sprintf("/v1/escrow/%s/dispute", escrowID), &req, &out); err != nil {
		return nil, err
	}
	return &out.Escrow, nil
}

// DeliverEscrow marks an escrow as delivered by the seller.
func (c *Client) DeliverEscrow(ctx context.Context, escrowID string) (*Escrow, error) {
	var out escrowResponse
	if err := c.doJSON(ctx, http.MethodPost, fmt.Sprintf("/v1/escrow/%s/deliver", escrowID), nil, &out); err != nil {
		return nil, err
	}
	return &out.Escrow, nil
}

// ListEscrows retrieves escrows for an agent.
func (c *Client) ListEscrows(ctx context.Context, agentAddr string, limit int) ([]Escrow, error) {
	l := "50"
	if limit > 0 {
		l = fmt.Sprintf("%d", limit)
	}
	path := fmt.Sprintf("/v1/agents/%s/escrows", agentAddr) + buildQuery("limit", l)
	var out listEscrowsResponse
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out.Escrows, nil
}
