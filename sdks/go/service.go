package alancoin

import (
	"context"
	"fmt"
	"net/http"
)

// AddService adds a service to an agent.
func (c *Client) AddService(ctx context.Context, agentAddr string, req AddServiceRequest) (*Service, error) {
	var out Service
	path := fmt.Sprintf("/v1/agents/%s/services", agentAddr)
	if err := c.doJSON(ctx, http.MethodPost, path, &req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateService updates an existing service.
func (c *Client) UpdateService(ctx context.Context, agentAddr, serviceID string, req UpdateServiceRequest) (*Service, error) {
	var out Service
	path := fmt.Sprintf("/v1/agents/%s/services/%s", agentAddr, serviceID)
	if err := c.doJSON(ctx, http.MethodPut, path, &req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RemoveService deletes a service from an agent.
func (c *Client) RemoveService(ctx context.Context, agentAddr, serviceID string) error {
	path := fmt.Sprintf("/v1/agents/%s/services/%s", agentAddr, serviceID)
	return c.doJSON(ctx, http.MethodDelete, path, nil, nil)
}
