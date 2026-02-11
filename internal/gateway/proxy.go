package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const maxResponseSize = 5 * 1024 * 1024 // 5MB

// ForwardRequest is the input to the HTTP forwarder.
type ForwardRequest struct {
	Endpoint  string
	Params    map[string]interface{}
	FromAddr  string
	Amount    string
	Reference string
}

// ForwardResponse is the HTTP forwarding result.
type ForwardResponse struct {
	StatusCode int
	Body       map[string]interface{}
	LatencyMs  int64
}

// Forwarder sends HTTP requests to service endpoints.
type Forwarder struct {
	client *http.Client
}

// NewForwarder creates a new HTTP forwarder.
// Pass timeout=0 to use DefaultHTTPTimeout.
func NewForwarder(timeout time.Duration) *Forwarder {
	if timeout == 0 {
		timeout = DefaultHTTPTimeout
	}
	return &Forwarder{
		client: &http.Client{Timeout: timeout},
	}
}

// Forward sends a POST request to the service endpoint.
func (f *Forwarder) Forward(ctx context.Context, req ForwardRequest) (*ForwardResponse, error) {
	body, err := json.Marshal(req.Params)
	if err != nil {
		return nil, fmt.Errorf("marshal params: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, req.Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Payment-Amount", req.Amount)
	httpReq.Header.Set("X-Payment-From", req.FromAddr)
	httpReq.Header.Set("X-Payment-TxHash", req.Reference)

	start := time.Now()
	resp, err := f.client.Do(httpReq)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		return nil, fmt.Errorf("http request failed: %w", err)
	}
	defer resp.Body.Close()

	limited := io.LimitReader(resp.Body, maxResponseSize)
	respBody, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var parsed map[string]interface{}
	if len(respBody) > 0 {
		if err := json.Unmarshal(respBody, &parsed); err != nil {
			// If not JSON, wrap raw body
			parsed = map[string]interface{}{
				"raw": string(respBody),
			}
		}
	}

	fwdResp := &ForwardResponse{
		StatusCode: resp.StatusCode,
		Body:       parsed,
		LatencyMs:  latency,
	}

	// Treat 5xx as an error so the gateway retry loop can try the next candidate.
	if resp.StatusCode >= 500 {
		return fwdResp, fmt.Errorf("service returned HTTP %d", resp.StatusCode)
	}

	return fwdResp, nil
}
