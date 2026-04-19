package mcpserver

import "github.com/mark3labs/mcp-go/mcp"

// Compliance + audit-trail MCP tools. Kept in a separate file so the
// surface area is reviewable and future plugin-specific tool batches
// land in their own files.

var ToolGetComplianceStatus = mcp.NewTool("get_compliance_status",
	mcp.WithDescription(
		"Return the current compliance posture for a scope: control counts "+
			"(enabled/degraded/disabled), open incident rollup by severity, "+
			"oldest unacknowledged incident, and the receipt chain head. "+
			"Use this to answer 'is this tenant audit-ready right now?'"),
	mcp.WithString("scope",
		mcp.Description("Chain scope (tenant id or 'global'). Defaults to 'global' when omitted.")),
)

var ToolListIncidents = mcp.NewTool("list_incidents",
	mcp.WithDescription(
		"List compliance incidents for a scope with optional filters. "+
			"Incidents are auto-created from forensics alerts and can also "+
			"be posted directly. Useful before acknowledging a batch or "+
			"before exporting an audit bundle."),
	mcp.WithString("scope",
		mcp.Description("Chain scope (tenant id or 'global'). Defaults to 'global'.")),
	mcp.WithString("severity",
		mcp.Description("Minimum severity to include."),
		mcp.Enum("info", "warning", "critical")),
	mcp.WithString("source",
		mcp.Description("Filter by source subsystem (e.g. 'forensics', 'policy')")),
	mcp.WithString("agent",
		mcp.Description("Filter by agent address")),
	mcp.WithString("only_unacked",
		mcp.Description("Set to 'true' to return only unacknowledged incidents")),
	mcp.WithNumber("limit",
		mcp.Description("Maximum number of incidents to return (default 50, max 500)")),
)

var ToolAcknowledgeIncident = mcp.NewTool("acknowledge_incident",
	mcp.WithDescription(
		"Acknowledge a compliance incident. The actor and optional note "+
			"are persisted on the record so the audit trail shows who "+
			"reviewed the incident and when."),
	mcp.WithString("incident_id",
		mcp.Required(),
		mcp.Description("The incident ID (format: 'inc_...')")),
	mcp.WithString("actor",
		mcp.Required(),
		mcp.Description("Identifier of the human or agent doing the acknowledgement (email, handle, or address)")),
	mcp.WithString("note",
		mcp.Description("Optional free-text note explaining the acknowledgement (e.g. 'investigated, false positive')")),
)

var ToolGetChainHead = mcp.NewTool("get_chain_head",
	mcp.WithDescription(
		"Return the receipt chain head for a scope: latest PayloadHash, "+
			"ChainIndex, and total receipt count. Use as a tamper-evidence "+
			"fingerprint that can be posted publicly and compared later."),
	mcp.WithString("scope",
		mcp.Description("Chain scope (tenant id or 'global'). Defaults to 'global'.")),
)

var ToolExportAuditBundle = mcp.NewTool("export_audit_bundle",
	mcp.WithDescription(
		"Produce a signed audit bundle containing all receipts in a scope "+
			"for a time range, plus a Merkle root committing to them. "+
			"The bundle is a self-contained artifact a regulator or "+
			"third-party auditor can independently verify."),
	mcp.WithString("scope",
		mcp.Description("Chain scope (tenant id or 'global'). Defaults to 'global'.")),
	mcp.WithString("since",
		mcp.Description("Inclusive lower-bound timestamp (RFC3339, e.g. '2026-04-01T00:00:00Z'). Omit for 'since creation'.")),
	mcp.WithString("until",
		mcp.Description("Inclusive upper-bound timestamp (RFC3339). Omit for 'through now'.")),
)

var ToolVerifyChain = mcp.NewTool("verify_chain",
	mcp.WithDescription(
		"Walk the receipt chain for a scope and report the first integrity "+
			"break, if any. Returns 'intact' when every signature, payload "+
			"hash, and prev-hash link is consistent across the range."),
	mcp.WithString("scope",
		mcp.Description("Chain scope (tenant id or 'global'). Defaults to 'global'.")),
	mcp.WithNumber("from",
		mcp.Description("Inclusive lower chain index (default 0)")),
	mcp.WithNumber("to",
		mcp.Description("Inclusive upper chain index. Pass -1 or omit to walk through HEAD.")),
)
