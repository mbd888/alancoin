package alancoin

import (
	"context"
	"fmt"
	"net/http"
)

// GetBalance retrieves the balance for an agent.
func (c *Client) GetBalance(ctx context.Context, agentAddr string) (*Balance, error) {
	var out BalanceResponse
	if err := c.doJSON(ctx, http.MethodGet, fmt.Sprintf("/v1/agents/%s/balance", agentAddr), nil, &out); err != nil {
		return nil, err
	}
	return &out.Balance, nil
}

// LedgerHistory retrieves ledger entries for an agent.
func (c *Client) LedgerHistory(ctx context.Context, agentAddr string, limit int) ([]LedgerEntry, error) {
	l := "100"
	if limit > 0 {
		l = fmt.Sprintf("%d", limit)
	}
	path := fmt.Sprintf("/v1/agents/%s/ledger", agentAddr) + buildQuery("limit", l)
	var out LedgerResponse
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out.Entries, nil
}

// RequestWithdrawal initiates a withdrawal of funds from the platform.
func (c *Client) RequestWithdrawal(ctx context.Context, agentAddr, amount string) (*WithdrawalResponse, error) {
	req := struct {
		Amount string `json:"amount"`
	}{Amount: amount}
	var out WithdrawalResponse
	if err := c.doJSON(ctx, http.MethodPost, fmt.Sprintf("/v1/agents/%s/withdraw", agentAddr), &req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
