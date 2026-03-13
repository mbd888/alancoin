package alancoin

import (
	"context"
	"fmt"
	"net/http"
)

// CreateMultiStepEscrow locks funds for an N-step pipeline with per-step release.
func (c *Client) CreateMultiStepEscrow(ctx context.Context, req CreateMultiStepEscrowRequest) (*MultiStepEscrow, error) {
	var out multiStepEscrowResponse
	if err := c.doJSON(ctx, http.MethodPost, "/v1/escrow/multistep", &req, &out); err != nil {
		return nil, err
	}
	return &out.Escrow, nil
}

// GetMultiStepEscrow retrieves a multistep escrow by ID.
func (c *Client) GetMultiStepEscrow(ctx context.Context, escrowID string) (*MultiStepEscrow, error) {
	var out multiStepEscrowResponse
	if err := c.doJSON(ctx, http.MethodGet, fmt.Sprintf("/v1/escrow/multistep/%s", escrowID), nil, &out); err != nil {
		return nil, err
	}
	return &out.Escrow, nil
}

// ConfirmStep confirms and releases funds for one step of a multistep escrow.
func (c *Client) ConfirmStep(ctx context.Context, escrowID string, req ConfirmStepRequest) (*MultiStepEscrow, error) {
	var out multiStepEscrowResponse
	path := fmt.Sprintf("/v1/escrow/multistep/%s/confirm-step", escrowID)
	if err := c.doJSON(ctx, http.MethodPost, path, &req, &out); err != nil {
		return nil, err
	}
	return &out.Escrow, nil
}

// RefundMultiStep refunds the remaining locked funds and closes the escrow.
func (c *Client) RefundMultiStep(ctx context.Context, escrowID string) (*MultiStepEscrow, error) {
	var out multiStepEscrowResponse
	path := fmt.Sprintf("/v1/escrow/multistep/%s/refund", escrowID)
	if err := c.doJSON(ctx, http.MethodPost, path, nil, &out); err != nil {
		return nil, err
	}
	return &out.Escrow, nil
}
