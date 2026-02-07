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

	return s
}
