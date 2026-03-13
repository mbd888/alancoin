// Package alancoin provides a Go client for the Alancoin agent payment network.
//
// The recommended usage pattern is through [GatewaySession], which handles
// service discovery, payment, and request forwarding automatically:
//
//	c := alancoin.NewClient("https://api.alancoin.io",
//	    alancoin.WithAPIKey("ak_..."),
//	)
//	gw, err := c.Gateway(ctx, alancoin.GatewayConfig{MaxTotal: "5.00"})
//	if err != nil { ... }
//	defer gw.Close(ctx)
//	result, err := gw.Call(ctx, "inference", nil, map[string]any{"prompt": "hello"})
//
// For one-shot calls, use the top-level [Spend] convenience function:
//
//	result, err := alancoin.Spend(ctx, url, apiKey, "inference", "1.00", params)
package alancoin

import (
	"context"
	"net/http"
	"time"
)

// Client is the main entry point for interacting with the Alancoin API.
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// Option configures a [Client].
type Option func(*Client)

// WithAPIKey sets the API key for authentication.
func WithAPIKey(key string) Option {
	return func(c *Client) {
		c.apiKey = key
	}
}

// WithTimeout sets the HTTP client timeout.
func WithTimeout(d time.Duration) Option {
	return func(c *Client) {
		c.httpClient.Timeout = d
	}
}

// WithHTTPClient replaces the default HTTP client entirely.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) {
		c.httpClient = hc
	}
}

// NewClient creates a new Alancoin API client.
func NewClient(baseURL string, opts ...Option) *Client {
	c := &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Health checks the API server health.
func (c *Client) Health(ctx context.Context) (map[string]any, error) {
	var out map[string]any
	err := c.doJSON(ctx, http.MethodGet, "/health", nil, &out)
	return out, err
}

// Platform returns platform info (deposit address, chain, USDC contract).
func (c *Client) Platform(ctx context.Context) (map[string]any, error) {
	var out map[string]any
	err := c.doJSON(ctx, http.MethodGet, "/v1/platform", nil, &out)
	return out, err
}

// NetworkStats returns network-wide statistics.
func (c *Client) NetworkStats(ctx context.Context) (*NetworkStats, error) {
	var out NetworkStats
	err := c.doJSON(ctx, http.MethodGet, "/v1/network/stats", nil, &out)
	return &out, err
}

// Gateway creates a new gateway session for transparent payment proxying.
func (c *Client) Gateway(ctx context.Context, cfg GatewayConfig) (*GatewaySession, error) {
	var resp createSessionResponse
	if err := c.doJSON(ctx, http.MethodPost, "/v1/gateway/sessions", &cfg, &resp); err != nil {
		return nil, err
	}
	return &GatewaySession{
		client: c,
		info:   resp.Session,
	}, nil
}

// SingleCall performs a one-shot gateway call: creates an ephemeral session,
// proxies the request, and closes the session in one round-trip.
func (c *Client) SingleCall(ctx context.Context, serviceType, maxPrice string, params map[string]any) (*SingleCallResult, error) {
	req := SingleCallRequest{
		MaxPrice:    maxPrice,
		ServiceType: serviceType,
		Params:      params,
	}
	var out SingleCallResult
	if err := c.doJSON(ctx, http.MethodPost, "/v1/gateway/call", &req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Connect is a convenience function that creates a client and opens a gateway session.
func Connect(ctx context.Context, baseURL, apiKey, maxTotal string) (*GatewaySession, error) {
	c := NewClient(baseURL, WithAPIKey(apiKey))
	return c.Gateway(ctx, GatewayConfig{MaxTotal: maxTotal})
}

// Spend is a convenience function that performs a single gateway call without
// managing a session. It creates a client, makes a one-shot call, and returns the result.
func Spend(ctx context.Context, baseURL, apiKey, serviceType, maxPrice string, params map[string]any) (*SingleCallResult, error) {
	c := NewClient(baseURL, WithAPIKey(apiKey))
	return c.SingleCall(ctx, serviceType, maxPrice, params)
}
