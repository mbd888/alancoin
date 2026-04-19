package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// HandleGetComplianceStatus returns a readiness report for the given scope.
func (h *Handlers) HandleGetComplianceStatus(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	scope := req.GetString("scope", "")
	raw, err := h.client.GetComplianceReadiness(ctx, scope)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to fetch readiness: %v", err)), nil
	}
	return mcp.NewToolResultText(formatReadiness(raw)), nil
}

// HandleListIncidents returns filtered compliance incidents.
func (h *Handlers) HandleListIncidents(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	scope := req.GetString("scope", "")
	filter := ListIncidentsFilter{
		Severity:    req.GetString("severity", ""),
		Source:      req.GetString("source", ""),
		AgentAddr:   req.GetString("agent", ""),
		OnlyUnacked: strings.EqualFold(req.GetString("only_unacked", ""), "true"),
		Limit:       int(req.GetFloat("limit", 0)),
	}
	raw, err := h.client.ListComplianceIncidents(ctx, scope, filter)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to list incidents: %v", err)), nil
	}
	return mcp.NewToolResultText(formatIncidents(raw)), nil
}

// HandleAcknowledgeIncident marks an incident as acknowledged.
func (h *Handlers) HandleAcknowledgeIncident(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id := req.GetString("incident_id", "")
	actor := req.GetString("actor", "")
	note := req.GetString("note", "")
	if id == "" {
		return mcp.NewToolResultError("incident_id is required"), nil
	}
	if actor == "" {
		return mcp.NewToolResultError("actor is required"), nil
	}
	raw, err := h.client.AcknowledgeIncident(ctx, id, actor, note)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to acknowledge: %v", err)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("Incident %s acknowledged by %s.\n%s", id, actor, string(raw))), nil
}

// HandleGetChainHead returns the receipt chain head.
func (h *Handlers) HandleGetChainHead(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	scope := req.GetString("scope", "")
	raw, err := h.client.GetChainHead(ctx, scope)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to fetch chain head: %v", err)), nil
	}
	return mcp.NewToolResultText(formatChainHead(raw)), nil
}

// HandleVerifyChain walks the chain and reports integrity.
func (h *Handlers) HandleVerifyChain(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	scope := req.GetString("scope", "")
	lower := int64(req.GetFloat("from", 0))
	upper := int64(req.GetFloat("to", -1))
	raw, err := h.client.VerifyReceiptChain(ctx, scope, lower, upper)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Verify failed: %v", err)), nil
	}
	return mcp.NewToolResultText(formatVerifyReport(raw)), nil
}

// HandleExportAuditBundle returns a signed audit bundle.
// The bundle is echoed back to the caller verbatim so any MCP client can
// persist it to disk and share it with a third-party auditor.
func (h *Handlers) HandleExportAuditBundle(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	scope := req.GetString("scope", "")
	since := req.GetString("since", "")
	until := req.GetString("until", "")
	raw, err := h.client.ExportAuditBundle(ctx, scope, since, until)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Export failed: %v", err)), nil
	}
	header := formatBundleHeader(raw)
	return mcp.NewToolResultText(header + "\n\n" + string(raw)), nil
}

// --- formatting ---

// formatReadiness renders a compact readiness summary for LLM consumption.
// On parse failure, falls back to the raw JSON so debugging stays possible.
func formatReadiness(raw json.RawMessage) string {
	var wrapper struct {
		Report struct {
			Scope         string `json:"scope"`
			EnabledCount  int    `json:"enabledCount"`
			DegradedCount int    `json:"degradedCount"`
			DisabledCount int    `json:"disabledCount"`
			Incidents     struct {
				Info     int `json:"info"`
				Warning  int `json:"warning"`
				Critical int `json:"critical"`
				Open     int `json:"open"`
			} `json:"incidents"`
			ChainHeadHash  string `json:"chainHeadHash"`
			ChainHeadIndex int64  `json:"chainHeadIndex"`
			ChainReceipts  int64  `json:"chainReceipts"`
			OldestOpen     string `json:"oldestOpen"`
		} `json:"report"`
	}
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		return string(raw)
	}
	r := wrapper.Report
	var sb strings.Builder
	fmt.Fprintf(&sb, "Compliance readiness for scope '%s'\n", r.Scope)
	fmt.Fprintf(&sb, "  Controls: %d enabled, %d degraded, %d disabled\n", r.EnabledCount, r.DegradedCount, r.DisabledCount)
	fmt.Fprintf(&sb, "  Incidents: %d open (info=%d warning=%d critical=%d)\n",
		r.Incidents.Open, r.Incidents.Info, r.Incidents.Warning, r.Incidents.Critical)
	if r.ChainHeadHash != "" {
		fmt.Fprintf(&sb, "  Receipt chain: %d receipts, head=%s\n", r.ChainReceipts, shortHash(r.ChainHeadHash))
	} else {
		sb.WriteString("  Receipt chain: empty\n")
	}
	if r.OldestOpen != "" {
		fmt.Fprintf(&sb, "  Oldest open incident: %s\n", r.OldestOpen)
	}
	return sb.String()
}

func formatIncidents(raw json.RawMessage) string {
	var wrapper struct {
		Count     int `json:"count"`
		Incidents []struct {
			ID           string `json:"id"`
			Scope        string `json:"scope"`
			Source       string `json:"source"`
			Severity     string `json:"severity"`
			Kind         string `json:"kind"`
			Title        string `json:"title"`
			AgentAddr    string `json:"agentAddr"`
			OccurredAt   string `json:"occurredAt"`
			Acknowledged bool   `json:"acknowledged"`
		} `json:"incidents"`
	}
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		return string(raw)
	}
	if wrapper.Count == 0 {
		return "No incidents match the filter."
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%d incident(s):\n", wrapper.Count)
	for _, inc := range wrapper.Incidents {
		ack := ""
		if inc.Acknowledged {
			ack = " [ack]"
		}
		fmt.Fprintf(&sb, "- %s %s/%s (%s)%s %s — %s\n",
			inc.ID, inc.Source, inc.Severity, inc.OccurredAt, ack, inc.AgentAddr, inc.Title)
	}
	return sb.String()
}

func formatChainHead(raw json.RawMessage) string {
	var wrapper struct {
		Head struct {
			Scope     string `json:"scope"`
			HeadHash  string `json:"headHash"`
			HeadIndex int64  `json:"headIndex"`
			ReceiptID string `json:"receiptId"`
			UpdatedAt string `json:"updatedAt"`
		} `json:"head"`
	}
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		return string(raw)
	}
	h := wrapper.Head
	if h.HeadIndex < 0 || h.HeadHash == "" {
		return fmt.Sprintf("Chain scope '%s' is empty.", h.Scope)
	}
	return fmt.Sprintf("Chain scope '%s': head index %d, head hash %s (receipt %s, updated %s)",
		h.Scope, h.HeadIndex, shortHash(h.HeadHash), h.ReceiptID, h.UpdatedAt)
}

func formatVerifyReport(raw json.RawMessage) string {
	var wrapper struct {
		Report struct {
			Scope        string `json:"scope"`
			Status       string `json:"status"`
			Count        int    `json:"count"`
			LastIndex    int64  `json:"lastIndex"`
			LastHash     string `json:"lastHash"`
			BreakAtIndex *int64 `json:"breakAtIndex"`
			BreakReceipt string `json:"breakReceipt"`
			Message      string `json:"message"`
		} `json:"report"`
	}
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		return string(raw)
	}
	r := wrapper.Report
	if r.Status == "intact" {
		return fmt.Sprintf("Chain '%s' intact across %d receipts (last hash %s).",
			r.Scope, r.Count, shortHash(r.LastHash))
	}
	if r.Status == "empty" {
		return fmt.Sprintf("Chain '%s' is empty.", r.Scope)
	}
	var breakAt string
	if r.BreakAtIndex != nil {
		breakAt = fmt.Sprintf("index %d", *r.BreakAtIndex)
	} else {
		breakAt = "unknown"
	}
	return fmt.Sprintf("Chain '%s' BROKEN (%s) at %s (receipt %s): %s",
		r.Scope, r.Status, breakAt, r.BreakReceipt, r.Message)
}

func formatBundleHeader(raw json.RawMessage) string {
	var wrapper struct {
		Manifest struct {
			Scope        string `json:"scope"`
			ReceiptCount int    `json:"receiptCount"`
			MerkleRoot   string `json:"merkleRoot"`
			LowerIndex   int64  `json:"lowerIndex"`
			UpperIndex   int64  `json:"upperIndex"`
			GeneratedAt  string `json:"generatedAt"`
			Signature    string `json:"signature"`
		} `json:"manifest"`
	}
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		return "Audit bundle exported (unable to parse manifest for summary)."
	}
	m := wrapper.Manifest
	return fmt.Sprintf(
		"Audit bundle for '%s': %d receipts, indices %d-%d, merkle=%s, generated %s.",
		m.Scope, m.ReceiptCount, m.LowerIndex, m.UpperIndex, shortHash(m.MerkleRoot), m.GeneratedAt,
	)
}

// shortHash returns the first 10 hex chars, prefixed, so LLM output stays
// compact but still distinguishable.
func shortHash(s string) string {
	if len(s) <= 10 {
		return s
	}
	return s[:10] + "…"
}
