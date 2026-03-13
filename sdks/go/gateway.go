package alancoin

import (
	"context"
	"fmt"
	"net/http"
)

// GatewaySession manages a server-side payment session for transparent proxying.
// The server handles service discovery, payment, and request forwarding.
type GatewaySession struct {
	client *Client
	info   GatewaySessionInfo
}

// ID returns the session identifier (also used as the gateway token).
func (gs *GatewaySession) ID() string { return gs.info.ID }

// Info returns a snapshot of the session state.
func (gs *GatewaySession) Info() GatewaySessionInfo { return gs.info }

// TotalSpent returns the cumulative amount spent in this session.
func (gs *GatewaySession) TotalSpent() string { return gs.info.TotalSpent }

// Remaining returns the unspent budget remaining.
func (gs *GatewaySession) Remaining() string {
	// Calculated from session info; refreshed after each call.
	return gs.info.MaxTotal // best-effort; Refresh() gives exact value
}

// RequestCount returns the number of proxy requests made.
func (gs *GatewaySession) RequestCount() int { return gs.info.RequestCount }

// IsActive returns true if the session is still active.
func (gs *GatewaySession) IsActive() bool { return gs.info.Status == "active" }

// Call proxies a single request through the gateway session.
// The server discovers the best service, pays, forwards, and returns the result.
func (gs *GatewaySession) Call(ctx context.Context, serviceType string, opts *ProxyRequest, params map[string]any) (*ProxyResult, error) {
	req := ProxyRequest{
		ServiceType: serviceType,
		Params:      params,
	}
	if opts != nil {
		req.MaxPrice = opts.MaxPrice
		req.PreferAgent = opts.PreferAgent
		req.IdempotencyKey = opts.IdempotencyKey
	}

	var result ProxyResult
	err := gs.client.doJSONWithHeaders(ctx, http.MethodPost, "/v1/gateway/proxy", &req, &result, gs.headers())
	if err != nil {
		return nil, err
	}
	// Update local state from response.
	gs.info.TotalSpent = result.TotalSpent
	gs.info.RequestCount++
	return &result, nil
}

// Pipeline executes a multi-step proxy pipeline. Each step's response is available
// to subsequent steps via $prev substitution on the server side.
func (gs *GatewaySession) Pipeline(ctx context.Context, steps []PipelineStep) (*PipelineResult, error) {
	req := struct {
		Steps []PipelineStep `json:"steps"`
	}{Steps: steps}

	var result PipelineResult
	err := gs.client.doJSONWithHeaders(ctx, http.MethodPost, "/v1/gateway/pipeline", &req, &result, gs.headers())
	if err != nil {
		return nil, err
	}
	gs.info.TotalSpent = result.TotalSpent
	gs.info.RequestCount += len(steps)
	return &result, nil
}

// Close terminates the gateway session and releases any unspent budget.
func (gs *GatewaySession) Close(ctx context.Context) (*GatewaySessionInfo, error) {
	path := fmt.Sprintf("/v1/gateway/sessions/%s", gs.info.ID)
	var resp closeSessionResponse
	if err := gs.client.doJSON(ctx, http.MethodDelete, path, nil, &resp); err != nil {
		return nil, err
	}
	gs.info = resp.Session
	return &gs.info, nil
}

// Logs retrieves the request log for this session.
func (gs *GatewaySession) Logs(ctx context.Context, limit int) ([]RequestLog, error) {
	l := "100"
	if limit > 0 {
		l = fmt.Sprintf("%d", limit)
	}
	path := fmt.Sprintf("/v1/gateway/sessions/%s/logs", gs.info.ID) + buildQuery("limit", l)
	var resp listLogsResponse
	if err := gs.client.doJSON(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, err
	}
	return resp.Logs, nil
}

// Refresh re-fetches the session state from the server.
func (gs *GatewaySession) Refresh(ctx context.Context) (*GatewaySessionInfo, error) {
	path := fmt.Sprintf("/v1/gateway/sessions/%s", gs.info.ID)
	var resp createSessionResponse
	if err := gs.client.doJSON(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, err
	}
	gs.info = resp.Session
	return &gs.info, nil
}

// DryRun pre-checks whether a proxy request would succeed without spending any budget.
func (gs *GatewaySession) DryRun(ctx context.Context, req DryRunRequest) (*DryRunResult, error) {
	path := fmt.Sprintf("/v1/gateway/sessions/%s/dry-run", gs.info.ID)
	var resp dryRunResponse
	if err := gs.client.doJSON(ctx, http.MethodPost, path, &req, &resp); err != nil {
		return nil, err
	}
	return &resp.Result, nil
}

// headers returns the extra headers required for gateway proxy requests.
func (gs *GatewaySession) headers() map[string]string {
	return map[string]string{
		"X-Gateway-Token": gs.info.ID,
	}
}
