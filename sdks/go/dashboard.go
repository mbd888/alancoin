package alancoin

import (
	"context"
	"fmt"
	"net/http"
)

// DashboardOverviewGet retrieves the dashboard overview for a tenant.
func (c *Client) DashboardOverviewGet(ctx context.Context, tenantID string) (*DashboardOverview, error) {
	var out DashboardOverview
	path := fmt.Sprintf("/v1/tenants/%s/dashboard/overview", tenantID)
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DashboardUsageOptions controls the usage time-series query.
type DashboardUsageOptions struct {
	Interval string // "hour", "day", "week"
	From     string // RFC3339
	To       string // RFC3339
}

// DashboardUsageGet retrieves the usage time-series for a tenant.
func (c *Client) DashboardUsageGet(ctx context.Context, tenantID string, opts DashboardUsageOptions) (*DashboardUsage, error) {
	var out DashboardUsage
	path := fmt.Sprintf("/v1/tenants/%s/dashboard/usage", tenantID) +
		buildQuery("interval", opts.Interval, "from", opts.From, "to", opts.To)
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DashboardTopServices retrieves the top services by volume for a tenant.
func (c *Client) DashboardTopServices(ctx context.Context, tenantID string, limit int) ([]TopService, error) {
	l := ""
	if limit > 0 {
		l = fmt.Sprintf("%d", limit)
	}
	path := fmt.Sprintf("/v1/tenants/%s/dashboard/top-services", tenantID) + buildQuery("limit", l)
	var out struct {
		Services []TopService `json:"services"`
		Count    int          `json:"count"`
	}
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out.Services, nil
}

// DashboardDenials retrieves policy denial logs for a tenant.
func (c *Client) DashboardDenials(ctx context.Context, tenantID string, limit int) ([]DashboardDenial, error) {
	l := ""
	if limit > 0 {
		l = fmt.Sprintf("%d", limit)
	}
	path := fmt.Sprintf("/v1/tenants/%s/dashboard/denials", tenantID) + buildQuery("limit", l)
	var out struct {
		Denials []DashboardDenial `json:"denials"`
		Count   int               `json:"count"`
	}
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out.Denials, nil
}

// DashboardSessions retrieves gateway sessions for a tenant dashboard.
func (c *Client) DashboardSessions(ctx context.Context, tenantID string, limit int) ([]GatewaySessionInfo, error) {
	l := ""
	if limit > 0 {
		l = fmt.Sprintf("%d", limit)
	}
	path := fmt.Sprintf("/v1/tenants/%s/dashboard/sessions", tenantID) + buildQuery("limit", l)
	var out struct {
		Sessions []GatewaySessionInfo `json:"sessions"`
		Count    int                  `json:"count"`
	}
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out.Sessions, nil
}
