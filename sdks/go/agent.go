package alancoin

import (
	"context"
	"fmt"
	"net/http"
)

// Register creates a new agent on the network.
func (c *Client) Register(ctx context.Context, req RegisterAgentRequest) (*RegisterAgentResponse, error) {
	var out RegisterAgentResponse
	if err := c.doJSON(ctx, http.MethodPost, "/v1/agents", &req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetAgent retrieves an agent by address.
func (c *Client) GetAgent(ctx context.Context, address string) (*Agent, error) {
	var out Agent
	if err := c.doJSON(ctx, http.MethodGet, fmt.Sprintf("/v1/agents/%s", address), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListAgents lists agents with optional filtering.
func (c *Client) ListAgents(ctx context.Context, limit, offset int, serviceType string) ([]Agent, error) {
	l := "100"
	if limit > 0 {
		l = fmt.Sprintf("%d", limit)
	}
	o := "0"
	if offset > 0 {
		o = fmt.Sprintf("%d", offset)
	}
	path := "/v1/agents" + buildQuery("limit", l, "offset", o, "serviceType", serviceType)
	var out ListAgentsResponse
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out.Agents, nil
}

// DeleteAgent removes an agent by address.
func (c *Client) DeleteAgent(ctx context.Context, address string) error {
	return c.doJSON(ctx, http.MethodDelete, fmt.Sprintf("/v1/agents/%s", address), nil, nil)
}

// ListTransactions retrieves transaction history for an agent.
func (c *Client) ListTransactions(ctx context.Context, agentAddr string, limit int) ([]Transaction, error) {
	l := "100"
	if limit > 0 {
		l = fmt.Sprintf("%d", limit)
	}
	path := fmt.Sprintf("/v1/agents/%s/transactions", agentAddr) + buildQuery("limit", l)
	var out listTransactionsResponse
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out.Transactions, nil
}
