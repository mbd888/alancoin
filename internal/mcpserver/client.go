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

	"github.com/mbd888/alancoin/internal/retry"
	"github.com/mbd888/alancoin/internal/security"
)

// Config holds the configuration for connecting to the Alancoin platform.
type Config struct {
	APIURL       string `json:"apiURL"`       // Base URL, e.g. "http://localhost:8080"
	APIKey       string `json:"-"`            // API key — excluded from serialization
	AgentAddress string `json:"agentAddress"` // Agent's address, e.g. "0x..."
}

// AlancoinClient is a pure HTTP client for the Alancoin platform API.
type AlancoinClient struct {
	cfg           Config
	httpClient    *http.Client
	allowLoopback bool // test-only: skip SSRF loopback check for httptest servers
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

// Get makes a GET request to the platform API.
func (c *AlancoinClient) Get(ctx context.Context, path string) (json.RawMessage, error) {
	return c.doRequest(ctx, http.MethodGet, path, nil, nil)
}

// Post makes a POST request to the platform API with a JSON body.
func (c *AlancoinClient) Post(ctx context.Context, path string, body any) (json.RawMessage, error) {
	return c.doRequest(ctx, http.MethodPost, path, nil, body)
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

	var bodyData []byte
	if body != nil {
		var marshalErr error
		bodyData, marshalErr = json.Marshal(body)
		if marshalErr != nil {
			return nil, fmt.Errorf("marshal request body: %w", marshalErr)
		}
	}

	var respBody []byte
	err = retry.Do(ctx, 3, 200*time.Millisecond, func() error {
		// Recreate body reader for each attempt (consumed after first read).
		var attemptBody io.Reader
		if bodyData != nil {
			attemptBody = bytes.NewReader(bodyData)
		}

		req, reqErr := http.NewRequestWithContext(ctx, method, u.String(), attemptBody)
		if reqErr != nil {
			return retry.Permanent(fmt.Errorf("create request: %w", reqErr))
		}

		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, doErr := c.httpClient.Do(req) //nolint:gosec // URL constructed from trusted cfg.APIURL + path
		if doErr != nil {
			return fmt.Errorf("request failed: %w", doErr)
		}
		defer func() { _ = resp.Body.Close() }()

		respBody, doErr = io.ReadAll(resp.Body)
		if doErr != nil {
			return fmt.Errorf("read response: %w", doErr)
		}

		if resp.StatusCode >= 500 {
			return fmt.Errorf("server error (%d)", resp.StatusCode)
		}
		if resp.StatusCode >= 400 {
			var apiErr apiError
			if json.Unmarshal(respBody, &apiErr) == nil && apiErr.Message != "" {
				return retry.Permanent(fmt.Errorf("API error (%d): %s", resp.StatusCode, apiErr.Message))
			}
			return retry.Permanent(fmt.Errorf("API error (%d): %s", resp.StatusCode, string(respBody)))
		}

		return nil
	})
	if err != nil {
		return nil, err
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
	if !c.allowLoopback {
		if err := security.ValidateEndpointURL(endpoint); err != nil {
			return nil, fmt.Errorf("invalid service endpoint URL: %w", err)
		}
	}
	parsedURL, err := url.Parse(endpoint)
	if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
		return nil, fmt.Errorf("invalid service endpoint URL: %s", endpoint)
	}

	data, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("marshal params: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, parsedURL.String(), bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Payment-Amount", amount)
	req.Header.Set("X-Payment-From", c.cfg.AgentAddress)
	req.Header.Set("X-Escrow-ID", escrowID)

	resp, err := c.httpClient.Do(req) //nolint:gosec // URL validated above
	if err != nil {
		return nil, fmt.Errorf("service call failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read service response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("service error (%d): %s", resp.StatusCode, string(respBody))
	}

	return json.RawMessage(respBody), nil
}
