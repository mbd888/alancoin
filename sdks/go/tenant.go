package alancoin

import (
	"context"
	"fmt"
	"net/http"
)

// CreateTenant creates a new tenant (admin only).
func (c *Client) CreateTenant(ctx context.Context, req CreateTenantRequest) (*CreateTenantResponse, error) {
	var out CreateTenantResponse
	if err := c.doJSON(ctx, http.MethodPost, "/v1/tenants", &req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetTenant retrieves a tenant by ID.
func (c *Client) GetTenant(ctx context.Context, tenantID string) (*Tenant, error) {
	var out tenantResponse
	path := fmt.Sprintf("/v1/tenants/%s", tenantID)
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out.Tenant, nil
}

// UpdateTenant updates tenant settings.
func (c *Client) UpdateTenant(ctx context.Context, tenantID string, req UpdateTenantRequest) (*Tenant, error) {
	var out tenantResponse
	path := fmt.Sprintf("/v1/tenants/%s", tenantID)
	if err := c.doJSON(ctx, http.MethodPatch, path, &req, &out); err != nil {
		return nil, err
	}
	return &out.Tenant, nil
}

// ListTenantAgents lists agent addresses registered to a tenant.
func (c *Client) ListTenantAgents(ctx context.Context, tenantID string) ([]string, error) {
	var out listTenantAgentsResponse
	path := fmt.Sprintf("/v1/tenants/%s/agents", tenantID)
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out.Agents, nil
}

// RegisterTenantAgent registers a new agent under a tenant.
func (c *Client) RegisterTenantAgent(ctx context.Context, tenantID string, req TenantAgentRequest) (*TenantAgentResponse, error) {
	var out TenantAgentResponse
	path := fmt.Sprintf("/v1/tenants/%s/agents", tenantID)
	if err := c.doJSON(ctx, http.MethodPost, path, &req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListTenantKeys lists API keys scoped to a tenant.
func (c *Client) ListTenantKeys(ctx context.Context, tenantID string) ([]TenantKeyInfo, error) {
	var out listTenantKeysResponse
	path := fmt.Sprintf("/v1/tenants/%s/keys", tenantID)
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out.Keys, nil
}

// CreateTenantKey creates a tenant-scoped API key.
func (c *Client) CreateTenantKey(ctx context.Context, tenantID string, req TenantKeyRequest) (*CreateAPIKeyResponse, error) {
	var out CreateAPIKeyResponse
	path := fmt.Sprintf("/v1/tenants/%s/keys", tenantID)
	if err := c.doJSON(ctx, http.MethodPost, path, &req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RevokeTenantKey revokes a tenant-scoped API key.
func (c *Client) RevokeTenantKey(ctx context.Context, tenantID, keyID string) error {
	path := fmt.Sprintf("/v1/tenants/%s/keys/%s", tenantID, keyID)
	return c.doJSON(ctx, http.MethodDelete, path, nil, nil)
}

// GetTenantBilling retrieves billing summary for a tenant.
func (c *Client) GetTenantBilling(ctx context.Context, tenantID string) (*TenantBilling, error) {
	var out tenantBillingResponse
	path := fmt.Sprintf("/v1/tenants/%s/billing", tenantID)
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out.Billing, nil
}
