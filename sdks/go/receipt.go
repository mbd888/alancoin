package alancoin

import (
	"context"
	"fmt"
	"net/http"
)

// GetReceipt retrieves an HMAC-signed payment receipt by ID.
func (c *Client) GetReceipt(ctx context.Context, receiptID string) (*Receipt, error) {
	var out struct {
		Receipt Receipt `json:"receipt"`
	}
	if err := c.doJSON(ctx, http.MethodGet, fmt.Sprintf("/v1/receipts/%s", receiptID), nil, &out); err != nil {
		return nil, err
	}
	return &out.Receipt, nil
}

// VerifyReceipt checks the HMAC signature and expiry of a receipt.
func (c *Client) VerifyReceipt(ctx context.Context, receiptID string) (*ReceiptVerifyResponse, error) {
	req := ReceiptVerifyRequest{ReceiptID: receiptID}
	var out struct {
		Result ReceiptVerifyResponse `json:"result"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/v1/receipts/verify", &req, &out); err != nil {
		return nil, err
	}
	return &out.Result, nil
}

// ListReceipts retrieves receipts for an agent.
func (c *Client) ListReceipts(ctx context.Context, agentAddr string, limit int) ([]Receipt, error) {
	l := "50"
	if limit > 0 {
		l = fmt.Sprintf("%d", limit)
	}
	path := fmt.Sprintf("/v1/agents/%s/receipts", agentAddr) + buildQuery("limit", l)
	var out listReceiptsResponse
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out.Receipts, nil
}
