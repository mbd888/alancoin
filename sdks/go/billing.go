package alancoin

import (
	"context"
	"fmt"
	"net/http"
)

// Subscribe creates a billing subscription for a tenant.
func (c *Client) Subscribe(ctx context.Context, tenantID string, req SubscribeRequest) (*SubscriptionInfo, error) {
	var out struct {
		Subscription SubscriptionInfo `json:"subscription"`
	}
	path := fmt.Sprintf("/v1/tenants/%s/billing/subscribe", tenantID)
	if err := c.doJSON(ctx, http.MethodPost, path, &req, &out); err != nil {
		return nil, err
	}
	return &out.Subscription, nil
}

// UpgradePlan upgrades the subscription plan for a tenant.
func (c *Client) UpgradePlan(ctx context.Context, tenantID, plan string) (*SubscriptionInfo, error) {
	req := struct {
		Plan string `json:"plan"`
	}{Plan: plan}
	var out struct {
		Subscription SubscriptionInfo `json:"subscription"`
	}
	path := fmt.Sprintf("/v1/tenants/%s/billing/upgrade", tenantID)
	if err := c.doJSON(ctx, http.MethodPost, path, &req, &out); err != nil {
		return nil, err
	}
	return &out.Subscription, nil
}

// CancelSubscription cancels the billing subscription for a tenant.
func (c *Client) CancelSubscription(ctx context.Context, tenantID string) error {
	path := fmt.Sprintf("/v1/tenants/%s/billing/cancel", tenantID)
	return c.doJSON(ctx, http.MethodPost, path, nil, nil)
}

// GetSubscription retrieves subscription details for a tenant.
func (c *Client) GetSubscription(ctx context.Context, tenantID string) (*SubscriptionInfo, error) {
	var out struct {
		Subscription SubscriptionInfo `json:"subscription"`
	}
	path := fmt.Sprintf("/v1/tenants/%s/billing/subscription", tenantID)
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out.Subscription, nil
}
