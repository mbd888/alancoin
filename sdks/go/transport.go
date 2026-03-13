package alancoin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
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

	var bodyData []byte
	if body != nil {
		var err error
		bodyData, err = json.Marshal(body)
		if err != nil {
			return fmt.Errorf("alancoin: marshal request: %w", err)
		}
	}

	var lastErr error
	maxAttempts := 1 + c.maxRetries
	for attempt := range maxAttempts {
		var reqBody io.Reader
		if bodyData != nil {
			reqBody = bytes.NewReader(bodyData)
		}

		req, err := http.NewRequestWithContext(ctx, method, u, reqBody)
		if err != nil {
			return fmt.Errorf("alancoin: create request: %w", err)
		}

		req.Header.Set("User-Agent", userAgent)
		if bodyData != nil {
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
			lastErr = &Error{
				Message:  err.Error(),
				Sentinel: ErrNetwork,
			}
			if c.shouldRetry(method, 0, attempt) {
				if waitErr := c.backoff(ctx, attempt, nil); waitErr != nil {
					return lastErr
				}
				continue
			}
			return lastErr
		}

		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = &Error{
				Message:  fmt.Sprintf("read response: %s", err),
				Sentinel: ErrNetwork,
			}
			if c.shouldRetry(method, 0, attempt) {
				if waitErr := c.backoff(ctx, attempt, nil); waitErr != nil {
					return lastErr
				}
				continue
			}
			return lastErr
		}

		if resp.StatusCode >= 400 {
			lastErr = parseErrorResponse(resp.StatusCode, respBody)
			if c.shouldRetry(method, resp.StatusCode, attempt) {
				if waitErr := c.backoff(ctx, attempt, resp); waitErr != nil {
					return lastErr
				}
				continue
			}
			return lastErr
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

	return lastErr
}

// shouldRetry returns true if the request should be retried.
func (c *Client) shouldRetry(method string, statusCode, attempt int) bool {
	if attempt >= c.maxRetries {
		return false
	}
	// Only retry idempotent methods.
	if method != http.MethodGet && method != http.MethodPut &&
		method != http.MethodDelete && method != http.MethodHead &&
		method != http.MethodOptions {
		return false
	}
	// Retry on network errors (statusCode == 0) or transient server errors.
	return statusCode == 0 ||
		statusCode == http.StatusTooManyRequests ||
		statusCode == http.StatusBadGateway ||
		statusCode == http.StatusServiceUnavailable ||
		statusCode == http.StatusGatewayTimeout
}

// backoff waits for an exponentially increasing duration with jitter.
// Respects the Retry-After header if present.
func (c *Client) backoff(ctx context.Context, attempt int, resp *http.Response) error {
	delay := c.retryBase * (1 << attempt)
	if delay > c.retryMax {
		delay = c.retryMax
	}

	// Respect Retry-After header (seconds).
	if resp != nil {
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			if secs, err := strconv.Atoi(ra); err == nil && secs > 0 {
				serverDelay := time.Duration(secs) * time.Second
				if serverDelay > delay {
					delay = serverDelay
				}
			}
		}
	}

	// Add jitter: ±25% of delay.
	jitter := time.Duration(float64(delay) * (0.75 + 0.5*rand.Float64()))

	var apiErr *Error
	if errors.As(ctx.Err(), &apiErr) {
		return ctx.Err()
	}

	t := time.NewTimer(jitter)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
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
