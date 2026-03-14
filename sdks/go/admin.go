package alancoin

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// ListStuckSessions retrieves gateway sessions stuck in settlement_failed status.
// This is an admin-only endpoint.
func (c *Client) ListStuckSessions(ctx context.Context, limit int) ([]StuckSession, error) {
	l := "100"
	if limit > 0 {
		l = fmt.Sprintf("%d", limit)
	}
	path := "/v1/admin/gateway/stuck" + buildQuery("limit", l)
	var out listStuckSessionsResponse
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out.Sessions, nil
}

// ResolveStuckSession force-closes a gateway session stuck in settlement_failed
// status by releasing its hold and marking it closed.
func (c *Client) ResolveStuckSession(ctx context.Context, sessionID string) (*ResolveResult, error) {
	var out ResolveResult
	path := fmt.Sprintf("/v1/admin/gateway/sessions/%s/resolve", sessionID)
	if err := c.doJSON(ctx, http.MethodPost, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RetrySettlement retries settlement on a gateway session that previously failed.
func (c *Client) RetrySettlement(ctx context.Context, sessionID string) (*RetryResult, error) {
	var out RetryResult
	path := fmt.Sprintf("/v1/admin/gateway/sessions/%s/retry-settlement", sessionID)
	if err := c.doJSON(ctx, http.MethodPost, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ForceCloseExpiredEscrows triggers force-closure of all expired escrows.
func (c *Client) ForceCloseExpiredEscrows(ctx context.Context) (*ForceCloseResult, error) {
	var out ForceCloseResult
	if err := c.doJSON(ctx, http.MethodPost, "/v1/admin/escrow/force-close-expired", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ForceCloseStaleStreams triggers force-closure of all stale streaming channels.
func (c *Client) ForceCloseStaleStreams(ctx context.Context) (*ForceCloseResult, error) {
	var out ForceCloseResult
	if err := c.doJSON(ctx, http.MethodPost, "/v1/admin/streams/force-close-stale", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Reconcile runs an on-demand cross-subsystem reconciliation check.
// This verifies consistency between the ledger, escrow, streams, and holds.
func (c *Client) Reconcile(ctx context.Context) (*ReconciliationReport, error) {
	var out reconcileResponse
	if err := c.doJSON(ctx, http.MethodPost, "/v1/admin/reconcile", nil, &out); err != nil {
		return nil, err
	}
	return &out.Report, nil
}

// ExportDenials exports denial records for ML training data.
// The since parameter filters records to those created after the given time.
// If since is zero, defaults to last 30 days server-side.
func (c *Client) ExportDenials(ctx context.Context, since time.Time, limit int) (*DenialExport, error) {
	sinceStr := ""
	if !since.IsZero() {
		sinceStr = since.Format(time.RFC3339)
	}
	l := ""
	if limit > 0 {
		l = fmt.Sprintf("%d", limit)
	}
	path := "/v1/admin/denials/export" + buildQuery("since", sinceStr, "limit", l)
	var out DenialExport
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// InspectState returns aggregated operational state from all subsystems.
// The response contains state snapshots keyed by provider name (e.g. "db",
// "websocket", "reconciliation").
func (c *Client) InspectState(ctx context.Context) (*StateInspection, error) {
	var out StateInspection
	if err := c.doJSON(ctx, http.MethodGet, "/v1/admin/state", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
