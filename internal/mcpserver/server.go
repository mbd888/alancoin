package mcpserver

import (
	"github.com/mark3labs/mcp-go/server"
)

// NewMCPServer creates a configured MCP server with all Alancoin tools registered.
func NewMCPServer(cfg Config) *server.MCPServer {
	s := server.NewMCPServer("alancoin", "1.0.0")
	client := NewAlancoinClient(cfg)
	h := NewHandlers(client)

	s.AddTool(ToolDiscoverServices, h.HandleDiscoverServices)
	s.AddTool(ToolCallService, h.HandleCallService)
	s.AddTool(ToolCheckBalance, h.HandleCheckBalance)
	s.AddTool(ToolGetReputation, h.HandleGetReputation)
	s.AddTool(ToolListAgents, h.HandleListAgents)
	s.AddTool(ToolGetNetworkStats, h.HandleGetNetworkStats)
	s.AddTool(ToolPayAgent, h.HandlePayAgent)
	s.AddTool(ToolDisputeEscrow, h.HandleDisputeEscrow)

	// Marketplace / Offers tools
	s.AddTool(ToolBrowseMarketplace, h.HandleBrowseMarketplace)
	s.AddTool(ToolPostOffer, h.HandlePostOffer)
	s.AddTool(ToolClaimOffer, h.HandleClaimOffer)
	s.AddTool(ToolCancelOffer, h.HandleCancelOffer)
	s.AddTool(ToolDeliverClaim, h.HandleDeliverClaim)
	s.AddTool(ToolCompleteClaim, h.HandleCompleteClaim)

	// Enterprise plugin tools
	s.AddTool(ToolVerifyAgent, h.HandleVerifyAgent)
	s.AddTool(ToolCheckBudget, h.HandleCheckBudget)
	s.AddTool(ToolFileDispute, h.HandleFileDispute)
	s.AddTool(ToolGetAlerts, h.HandleGetAlerts)

	// Compliance + audit-trail tools
	s.AddTool(ToolGetComplianceStatus, h.HandleGetComplianceStatus)
	s.AddTool(ToolListIncidents, h.HandleListIncidents)
	s.AddTool(ToolAcknowledgeIncident, h.HandleAcknowledgeIncident)
	s.AddTool(ToolGetChainHead, h.HandleGetChainHead)
	s.AddTool(ToolVerifyChain, h.HandleVerifyChain)
	s.AddTool(ToolExportAuditBundle, h.HandleExportAuditBundle)

	return s
}
