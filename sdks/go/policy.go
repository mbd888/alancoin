package alancoin

import (
	"context"
	"fmt"
	"net/http"
)

// CreatePolicy creates a spend policy for a tenant.
func (c *Client) CreatePolicy(ctx context.Context, tenantID string, req CreatePolicyRequest) (*SpendPolicy, error) {
	var out struct {
		Policy SpendPolicy `json:"policy"`
	}
	path := fmt.Sprintf("/v1/tenants/%s/policies", tenantID)
	if err := c.doJSON(ctx, http.MethodPost, path, &req, &out); err != nil {
		return nil, err
	}
	return &out.Policy, nil
}

// GetPolicy retrieves a spend policy by ID.
func (c *Client) GetPolicy(ctx context.Context, tenantID, policyID string) (*SpendPolicy, error) {
	var out struct {
		Policy SpendPolicy `json:"policy"`
	}
	path := fmt.Sprintf("/v1/tenants/%s/policies/%s", tenantID, policyID)
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out.Policy, nil
}

// ListPolicies lists all spend policies for a tenant.
func (c *Client) ListPolicies(ctx context.Context, tenantID string) ([]SpendPolicy, error) {
	path := fmt.Sprintf("/v1/tenants/%s/policies", tenantID)
	var out listPoliciesResponse
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out.Policies, nil
}

// UpdatePolicy updates an existing spend policy.
func (c *Client) UpdatePolicy(ctx context.Context, tenantID, policyID string, req UpdatePolicyRequest) (*SpendPolicy, error) {
	var out struct {
		Policy SpendPolicy `json:"policy"`
	}
	path := fmt.Sprintf("/v1/tenants/%s/policies/%s", tenantID, policyID)
	if err := c.doJSON(ctx, http.MethodPut, path, &req, &out); err != nil {
		return nil, err
	}
	return &out.Policy, nil
}

// DeletePolicy removes a spend policy.
func (c *Client) DeletePolicy(ctx context.Context, tenantID, policyID string) error {
	path := fmt.Sprintf("/v1/tenants/%s/policies/%s", tenantID, policyID)
	return c.doJSON(ctx, http.MethodDelete, path, nil, nil)
}
