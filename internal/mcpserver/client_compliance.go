package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// --- read endpoints ---

func (c *AlancoinClient) GetComplianceReadiness(ctx context.Context, scope string) (json.RawMessage, error) {
	return c.Get(ctx, "/v1/compliance/"+escapeScope(scope)+"/readiness")
}

// ListIncidentsFilter carries the query-string inputs for ListIncidents.
// Kept as a struct so the handler and callers don't duplicate the empty-string check list.
type ListIncidentsFilter struct {
	Severity    string
	Source      string
	AgentAddr   string
	OnlyUnacked bool
	Limit       int
}

func (c *AlancoinClient) ListComplianceIncidents(ctx context.Context, scope string, f ListIncidentsFilter) (json.RawMessage, error) {
	q := url.Values{}
	if f.Severity != "" {
		q.Set("severity", f.Severity)
	}
	if f.Source != "" {
		q.Set("source", f.Source)
	}
	if f.AgentAddr != "" {
		q.Set("agent", f.AgentAddr)
	}
	if f.OnlyUnacked {
		q.Set("onlyUnacked", "true")
	}
	if f.Limit > 0 {
		q.Set("limit", strconv.Itoa(f.Limit))
	}
	return c.doRequest(ctx, "GET", "/v1/compliance/"+escapeScope(scope)+"/incidents", q, nil)
}

func (c *AlancoinClient) GetChainHead(ctx context.Context, scope string) (json.RawMessage, error) {
	return c.Get(ctx, "/v1/chains/"+escapeScope(scope)+"/head")
}

func (c *AlancoinClient) VerifyReceiptChain(ctx context.Context, scope string, lower, upper int64) (json.RawMessage, error) {
	q := url.Values{}
	if lower > 0 {
		q.Set("from", strconv.FormatInt(lower, 10))
	}
	if upper >= 0 {
		q.Set("to", strconv.FormatInt(upper, 10))
	}
	return c.doRequest(ctx, "GET", "/v1/chains/"+escapeScope(scope)+"/verify", q, nil)
}

func (c *AlancoinClient) ExportAuditBundle(ctx context.Context, scope, since, until string) (json.RawMessage, error) {
	q := url.Values{}
	if since != "" {
		q.Set("since", since)
	}
	if until != "" {
		q.Set("until", until)
	}
	return c.doRequest(ctx, "GET", "/v1/chains/"+escapeScope(scope)+"/bundle", q, nil)
}

// --- write endpoints ---

func (c *AlancoinClient) AcknowledgeIncident(ctx context.Context, incidentID, actor, note string) (json.RawMessage, error) {
	if incidentID == "" {
		return nil, fmt.Errorf("mcpserver: incident_id is required")
	}
	return c.Post(ctx, "/v1/compliance/incidents/"+url.PathEscape(incidentID)+"/ack", map[string]any{
		"actor": actor,
		"note":  note,
	})
}

// --- helpers ---

// escapeScope normalizes + URL-encodes the scope parameter.
// Empty falls back to "global" so callers can omit it safely.
func escapeScope(scope string) string {
	s := strings.TrimSpace(scope)
	if s == "" {
		s = "global"
	}
	return url.PathEscape(s)
}
