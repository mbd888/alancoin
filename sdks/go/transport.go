package alancoin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const userAgent = "alancoin-go/0.1.0"

// doJSON performs an HTTP request with JSON body and decodes the JSON response.
// If body is nil, no request body is sent. If out is nil, the response body is discarded.
func (c *Client) doJSON(ctx context.Context, method, path string, body, out any) error {
	return c.doJSONWithHeaders(ctx, method, path, body, out, nil)
}

// doJSONWithHeaders is like doJSON but allows extra headers (e.g., X-Gateway-Token).
func (c *Client) doJSONWithHeaders(ctx context.Context, method, path string, body, out any, headers map[string]string) error {
	u := strings.TrimRight(c.baseURL, "/") + path

	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("alancoin: marshal request: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, u, reqBody)
	if err != nil {
		return fmt.Errorf("alancoin: create request: %w", err)
	}

	req.Header.Set("User-Agent", userAgent)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.apiKey != "" {
		req.Header.Set("X-API-Key", c.apiKey)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return &Error{
			Message:  err.Error(),
			Sentinel: ErrNetwork,
		}
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return &Error{
			Message:  fmt.Sprintf("read response: %s", err),
			Sentinel: ErrNetwork,
		}
	}

	if resp.StatusCode >= 400 {
		return parseErrorResponse(resp.StatusCode, respBody)
	}

	// 204 No Content — nothing to decode.
	if resp.StatusCode == http.StatusNoContent || out == nil {
		return nil
	}

	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("alancoin: decode response: %w", err)
	}
	return nil
}

// parseErrorResponse constructs an *Error from an error response body.
func parseErrorResponse(status int, body []byte) *Error {
	// Try to parse structured error JSON.
	var raw struct {
		Error   string         `json:"error"`
		Code    string         `json:"code"`
		Message string         `json:"message"`
		Details map[string]any `json:"details"`
	}
	_ = json.Unmarshal(body, &raw)

	msg := raw.Message
	if msg == "" {
		msg = raw.Error
	}
	if msg == "" {
		msg = http.StatusText(status)
	}

	code := raw.Code
	sentinel := sentinelForStatus(status, code)

	return &Error{
		StatusCode: status,
		Code:       code,
		Message:    msg,
		Details:    raw.Details,
		Sentinel:   sentinel,
	}
}

// buildQuery constructs a query string from key/value pairs, skipping empty values.
func buildQuery(pairs ...string) string {
	if len(pairs)%2 != 0 {
		return ""
	}
	v := url.Values{}
	for i := 0; i < len(pairs); i += 2 {
		if pairs[i+1] != "" {
			v.Set(pairs[i], pairs[i+1])
		}
	}
	q := v.Encode()
	if q == "" {
		return ""
	}
	return "?" + q
}
