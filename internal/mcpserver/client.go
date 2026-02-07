package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Config holds the configuration for connecting to the Alancoin platform.
type Config struct {
	APIURL       string // Base URL, e.g. "http://localhost:8080"
	APIKey       string // API key, e.g. "sk_..."
	AgentAddress string // Agent's address, e.g. "0x..."
}

// AlancoinClient is a pure HTTP client for the Alancoin platform API.
type AlancoinClient struct {
	cfg        Config
	httpClient *http.Client
}

// NewAlancoinClient creates a new client for the Alancoin platform.
func NewAlancoinClient(cfg Config) *AlancoinClient {
	return &AlancoinClient{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// apiError represents an error response from the platform.
type apiError struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// doRequest makes an HTTP request to the platform and returns the response body.
func (c *AlancoinClient) doRequest(ctx context.Context, method, path string, query url.Values, body any) (json.RawMessage, error) {
	u, err := url.Parse(c.cfg.APIURL + path)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}
	if query != nil {
		u.RawQuery = query.Encode()
	}

	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, u.String(), reqBody)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		var apiErr apiError
		if json.Unmarshal(respBody, &apiErr) == nil && apiErr.Message != "" {
			return nil, fmt.Errorf("API error (%d): %s", resp.StatusCode, apiErr.Message)
		}
		return nil, fmt.Errorf("API error (%d): %s", resp.StatusCode, string(respBody))
	}

	return json.RawMessage(respBody), nil
}

// DiscoverServices searches the service marketplace.
func (c *AlancoinClient) DiscoverServices(ctx context.Context, serviceType, maxPrice, sortBy, queryStr string) (json.RawMessage, error) {
	q := url.Values{}
	if serviceType != "" {
		q.Set("type", serviceType)
	}
	if maxPrice != "" {
		q.Set("maxPrice", maxPrice)
	}
	if sortBy != "" {
		q.Set("sortBy", sortBy)
	}
	return c.doRequest(ctx, http.MethodGet, "/v1/services", q, nil)
}

// GetBalance returns the agent's current USDC balance.
func (c *AlancoinClient) GetBalance(ctx context.Context) (json.RawMessage, error) {
	path := "/v1/agents/" + c.cfg.AgentAddress + "/balance"
	return c.doRequest(ctx, http.MethodGet, path, nil, nil)
}

// GetReputation returns the reputation score for a given agent address.
func (c *AlancoinClient) GetReputation(ctx context.Context, address string) (json.RawMessage, error) {
	path := "/v1/reputation/" + address
	return c.doRequest(ctx, http.MethodGet, path, nil, nil)
}

// ListAgents lists registered agents, optionally filtered by service type.
func (c *AlancoinClient) ListAgents(ctx context.Context, serviceType string, limit int) (json.RawMessage, error) {
	q := url.Values{}
	if serviceType != "" {
		q.Set("serviceType", serviceType)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	return c.doRequest(ctx, http.MethodGet, "/v1/agents", q, nil)
}

// GetNetworkStats returns platform-wide statistics.
func (c *AlancoinClient) GetNetworkStats(ctx context.Context) (json.RawMessage, error) {
	return c.doRequest(ctx, http.MethodGet, "/v1/platform", nil, nil)
}

// CreateEscrow creates a new escrow for buyer protection.
func (c *AlancoinClient) CreateEscrow(ctx context.Context, sellerAddr, amount, serviceID string) (json.RawMessage, error) {
	body := map[string]string{
		"buyerAddr":  c.cfg.AgentAddress,
		"sellerAddr": sellerAddr,
		"amount":     amount,
		"serviceId":  serviceID,
	}
	return c.doRequest(ctx, http.MethodPost, "/v1/escrow", nil, body)
}

// ConfirmEscrow confirms an escrow, releasing funds to the seller.
func (c *AlancoinClient) ConfirmEscrow(ctx context.Context, escrowID string) (json.RawMessage, error) {
	path := "/v1/escrow/" + escrowID + "/confirm"
	return c.doRequest(ctx, http.MethodPost, path, nil, nil)
}

// DisputeEscrow disputes an escrow, refunding the buyer.
func (c *AlancoinClient) DisputeEscrow(ctx context.Context, escrowID, reason string) (json.RawMessage, error) {
	path := "/v1/escrow/" + escrowID + "/dispute"
	body := map[string]string{
		"reason": reason,
	}
	return c.doRequest(ctx, http.MethodPost, path, nil, body)
}

// CallEndpoint makes a direct HTTP POST to a service endpoint with payment headers.
func (c *AlancoinClient) CallEndpoint(ctx context.Context, endpoint string, params map[string]any, escrowID, amount string) (json.RawMessage, error) {
	data, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("marshal params: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Payment-Amount", amount)
	req.Header.Set("X-Payment-From", c.cfg.AgentAddress)
	req.Header.Set("X-Escrow-ID", escrowID)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("service call failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read service response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("service error (%d): %s", resp.StatusCode, string(respBody))
	}

	return json.RawMessage(respBody), nil
}
