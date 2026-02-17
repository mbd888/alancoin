package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// Handlers holds the handler functions for each MCP tool.
type Handlers struct {
	client *AlancoinClient
}

// NewHandlers creates a new Handlers instance.
func NewHandlers(client *AlancoinClient) *Handlers {
	return &Handlers{client: client}
}

// HandleDiscoverServices searches the marketplace.
func (h *Handlers) HandleDiscoverServices(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	serviceType := req.GetString("service_type", "")
	maxPrice := req.GetString("max_price", "")
	sortBy := req.GetString("sort_by", "")
	query := req.GetString("query", "")

	raw, err := h.client.DiscoverServices(ctx, serviceType, maxPrice, sortBy, query)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to discover services: %v", err)), nil
	}

	text, err := formatServiceList(raw)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to parse services: %v", err)), nil
	}

	return mcp.NewToolResultText(text), nil
}

// HandleCallService discovers, pays for, and calls a service in one step.
func (h *Handlers) HandleCallService(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	serviceType := req.GetString("service_type", "")
	if serviceType == "" {
		return mcp.NewToolResultError("service_type is required"), nil
	}
	maxPrice := req.GetString("max_price", "")
	prefer := req.GetString("prefer", "cheapest")

	// Extract params as map
	params := make(map[string]any)
	if raw := req.GetArguments()["params"]; raw != nil {
		if m, ok := raw.(map[string]any); ok {
			params = m
		}
	}

	// 1. Discover services
	raw, err := h.client.DiscoverServices(ctx, serviceType, maxPrice, "", "")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Discovery failed: %v", err)), nil
	}

	svc, err := selectService(raw, prefer)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	// 2. Create escrow
	escrowRaw, err := h.client.CreateEscrow(ctx, svc.Address, svc.Price, svc.ID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Escrow creation failed: %v", err)), nil
	}

	escrowID, err := extractEscrowID(escrowRaw)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to parse escrow: %v", err)), nil
	}

	// 3. Call the service endpoint
	result, err := h.client.CallEndpoint(ctx, svc.Endpoint, params, escrowID, svc.Price)
	if err != nil {
		// Service failed — funds still in escrow, not settled
		return mcp.NewToolResultText(fmt.Sprintf(
			"Service call failed. Your funds are safe in escrow.\n\n"+
				"Error: %v\n"+
				"Escrow ID: %s\n"+
				"Amount held: %s USDC\n\n"+
				"Use dispute_escrow with this escrow_id to get a refund.",
			err, escrowID, svc.Price)), nil
	}

	// 4. Auto-confirm escrow on success
	_, confirmErr := h.client.ConfirmEscrow(ctx, escrowID)

	var sb strings.Builder
	fmt.Fprintf(&sb, "Service: %s (%s)\n", svc.Name, svc.Address)
	fmt.Fprintf(&sb, "Cost: %s USDC\n", svc.Price)

	if confirmErr != nil {
		fmt.Fprintf(&sb, "Payment: Escrow created but auto-confirm failed (ID: %s)\n", escrowID)
	} else {
		sb.WriteString("Payment: Confirmed\n")
	}

	fmt.Fprintf(&sb, "\nResult:\n%s", formatJSON(result))

	return mcp.NewToolResultText(sb.String()), nil
}

// HandleCheckBalance returns the agent's USDC balance.
func (h *Handlers) HandleCheckBalance(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	raw, err := h.client.GetBalance(ctx)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to check balance: %v", err)), nil
	}

	text, err := formatBalance(raw)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to parse balance: %v", err)), nil
	}

	return mcp.NewToolResultText(text), nil
}

// HandleGetReputation returns the reputation score for an agent.
func (h *Handlers) HandleGetReputation(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	address := req.GetString("agent_address", "")
	if address == "" {
		return mcp.NewToolResultError("agent_address is required"), nil
	}

	raw, err := h.client.GetReputation(ctx, address)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to get reputation: %v", err)), nil
	}

	text, err := formatReputation(raw)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to parse reputation: %v", err)), nil
	}

	return mcp.NewToolResultText(text), nil
}

// HandleListAgents lists registered agents.
func (h *Handlers) HandleListAgents(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	serviceType := req.GetString("service_type", "")
	limit := req.GetInt("limit", 20)

	raw, err := h.client.ListAgents(ctx, serviceType, limit)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to list agents: %v", err)), nil
	}

	text, err := formatAgentList(raw)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to parse agents: %v", err)), nil
	}

	return mcp.NewToolResultText(text), nil
}

// HandleGetNetworkStats returns platform statistics.
func (h *Handlers) HandleGetNetworkStats(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	raw, err := h.client.GetNetworkStats(ctx)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to get network stats: %v", err)), nil
	}

	return mcp.NewToolResultText(formatJSON(raw)), nil
}

// HandlePayAgent sends a direct payment via escrow.
func (h *Handlers) HandlePayAgent(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	recipient := req.GetString("recipient", "")
	if recipient == "" {
		return mcp.NewToolResultError("recipient is required"), nil
	}
	amount := req.GetString("amount", "")
	if amount == "" {
		return mcp.NewToolResultError("amount is required"), nil
	}
	memo := req.GetString("memo", "")

	serviceID := "direct-payment"
	if memo != "" {
		serviceID = "direct-payment:" + memo
	}

	escrowRaw, err := h.client.CreateEscrow(ctx, recipient, amount, serviceID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Payment failed: %v", err)), nil
	}

	escrowID, err := extractEscrowID(escrowRaw)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to parse escrow: %v", err)), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf(
		"Escrow created for %s USDC to %s\n"+
			"Escrow ID: %s\n"+
			"Status: Funds held in escrow\n\n"+
			"The payment is held until the recipient delivers. "+
			"Use dispute_escrow if you need a refund.",
		amount, recipient, escrowID)), nil
}

// HandleDisputeEscrow disputes an escrow for a refund.
func (h *Handlers) HandleDisputeEscrow(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	escrowID := req.GetString("escrow_id", "")
	if escrowID == "" {
		return mcp.NewToolResultError("escrow_id is required"), nil
	}
	reason := req.GetString("reason", "")
	if reason == "" {
		return mcp.NewToolResultError("reason is required"), nil
	}

	_, err := h.client.DisputeEscrow(ctx, escrowID, reason)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Dispute failed: %v", err)), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf(
		"Escrow %s disputed successfully.\n"+
			"Reason: %s\n"+
			"Status: Funds refunded to your balance.",
		escrowID, reason)), nil
}

// --- Formatting helpers ---

type serviceInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Address     string `json:"address"`
	Type        string `json:"type"`
	Price       string `json:"price"`
	Endpoint    string `json:"endpoint"`
	Reputation  float64
	SuccessRate float64
	Tier        string
}

func formatServiceList(raw json.RawMessage) (string, error) {
	services, err := parseServices(raw)
	if err != nil {
		return "", err
	}
	if len(services) == 0 {
		return "No services found matching your criteria.", nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d service(s):\n\n", len(services)))
	for i, s := range services {
		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, s.Name))
		sb.WriteString(fmt.Sprintf("   Type: %s | Price: %s USDC\n", s.Type, s.Price))
		sb.WriteString(fmt.Sprintf("   Provider: %s\n", s.Address))
		if s.Tier != "" {
			sb.WriteString(fmt.Sprintf("   Reputation: %.1f (%s) | Success: %.0f%%\n", s.Reputation, s.Tier, s.SuccessRate*100))
		}
		if i < len(services)-1 {
			sb.WriteString("\n")
		}
	}
	return sb.String(), nil
}

func parseServices(raw json.RawMessage) ([]serviceInfo, error) {
	// Try as {"services": [...]}
	var wrapper struct {
		Services []json.RawMessage `json:"services"`
	}
	if err := json.Unmarshal(raw, &wrapper); err == nil && wrapper.Services != nil {
		return parseServiceItems(wrapper.Services)
	}

	// Try as direct array
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err == nil {
		return parseServiceItems(arr)
	}

	return nil, fmt.Errorf("unexpected services response format")
}

func parseServiceItems(items []json.RawMessage) ([]serviceInfo, error) {
	var services []serviceInfo
	for _, item := range items {
		var m map[string]any
		if err := json.Unmarshal(item, &m); err != nil {
			continue
		}
		s := serviceInfo{
			ID:       getString(m, "id", "serviceId"),
			Name:     getString(m, "name", "serviceName"),
			Address:  getString(m, "address", "agentAddress", "providerAddress"),
			Type:     getString(m, "type", "serviceType"),
			Price:    getString(m, "price"),
			Endpoint: getString(m, "endpoint", "serviceEndpoint"),
		}
		if v, ok := getFloat(m, "reputationScore", "reputation_score"); ok {
			s.Reputation = v
		}
		if v, ok := getFloat(m, "successRate", "success_rate"); ok {
			s.SuccessRate = v
		}
		s.Tier = getString(m, "reputationTier", "reputation_tier")
		services = append(services, s)
	}
	return services, nil
}

func selectService(raw json.RawMessage, strategy string) (serviceInfo, error) {
	services, err := parseServices(raw)
	if err != nil {
		return serviceInfo{}, fmt.Errorf("no services found: %v", err)
	}
	if len(services) == 0 {
		return serviceInfo{}, fmt.Errorf("no services found matching your criteria")
	}

	switch strategy {
	case "reputation":
		best := services[0]
		for _, s := range services[1:] {
			if s.Reputation > best.Reputation {
				best = s
			}
		}
		return best, nil
	case "best_value":
		// Already sorted by value if sortBy=value was used
		// Otherwise fall through to first
		return services[0], nil
	default: // "cheapest" or empty
		return services[0], nil
	}
}

func extractEscrowID(raw json.RawMessage) (string, error) {
	var resp map[string]any
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", err
	}
	// Try resp.escrow.id
	if escrow, ok := resp["escrow"].(map[string]any); ok {
		if id, ok := escrow["id"].(string); ok {
			return id, nil
		}
	}
	// Try resp.id
	if id, ok := resp["id"].(string); ok {
		return id, nil
	}
	// Try resp.escrowId
	if id, ok := resp["escrowId"].(string); ok {
		return id, nil
	}
	return "", fmt.Errorf("no escrow ID in response: %s", string(raw))
}

func formatBalance(raw json.RawMessage) (string, error) {
	var resp map[string]any
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", err
	}

	// Balance might be at top level or nested under "balance"
	bal := resp
	if b, ok := resp["balance"].(map[string]any); ok {
		bal = b
	}

	var sb strings.Builder
	sb.WriteString("USDC Balance:\n")
	sb.WriteString(fmt.Sprintf("  Available: %s USDC\n", getString(bal, "available")))
	if v := getString(bal, "pending"); v != "" && v != "0" && v != "0.000000" {
		sb.WriteString(fmt.Sprintf("  Pending:   %s USDC\n", v))
	}
	if v := getString(bal, "escrowed"); v != "" && v != "0" && v != "0.000000" {
		sb.WriteString(fmt.Sprintf("  Escrowed:  %s USDC\n", v))
	}

	return sb.String(), nil
}

func formatReputation(raw json.RawMessage) (string, error) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return "", err
	}

	var sb strings.Builder
	sb.WriteString("Agent Reputation:\n")
	if v := getString(m, "address", "agentAddress"); v != "" {
		sb.WriteString(fmt.Sprintf("  Address: %s\n", v))
	}
	if v, ok := getFloat(m, "score", "reputationScore"); ok {
		sb.WriteString(fmt.Sprintf("  Score: %.1f\n", v))
	}
	if v := getString(m, "tier", "reputationTier"); v != "" {
		sb.WriteString(fmt.Sprintf("  Tier: %s\n", v))
	}
	if v, ok := getFloat(m, "successRate", "success_rate"); ok {
		sb.WriteString(fmt.Sprintf("  Success Rate: %.0f%%\n", v*100))
	}
	if v, ok := getFloat(m, "txCount", "transactionCount"); ok {
		sb.WriteString(fmt.Sprintf("  Transactions: %.0f\n", v))
	}

	return sb.String(), nil
}

func formatAgentList(raw json.RawMessage) (string, error) {
	var resp struct {
		Agents []map[string]any `json:"agents"`
	}
	// Try as {"agents": [...]}
	if err := json.Unmarshal(raw, &resp); err != nil || resp.Agents == nil {
		// Try as direct array
		if err := json.Unmarshal(raw, &resp.Agents); err != nil {
			return "", fmt.Errorf("unexpected agents response format")
		}
	}

	if len(resp.Agents) == 0 {
		return "No agents found.", nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d agent(s):\n\n", len(resp.Agents)))
	for i, a := range resp.Agents {
		name := getString(a, "name")
		addr := getString(a, "address")
		desc := getString(a, "description")
		sb.WriteString(fmt.Sprintf("%d. %s (%s)\n", i+1, name, addr))
		if desc != "" {
			sb.WriteString(fmt.Sprintf("   %s\n", desc))
		}
	}
	return sb.String(), nil
}

func formatJSON(raw json.RawMessage) string {
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, raw, "", "  "); err != nil {
		return string(raw)
	}
	return pretty.String()
}

// getString extracts a string value from a map, trying multiple key names.
func getString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok {
				return s
			}
			if f, ok := v.(float64); ok {
				return fmt.Sprintf("%g", f)
			}
		}
	}
	return ""
}

// getFloat extracts a float64 value from a map, trying multiple key names.
func getFloat(m map[string]any, keys ...string) (float64, bool) {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if f, ok := v.(float64); ok {
				return f, true
			}
		}
	}
	return 0, false
}
