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

// --- Enterprise Plugin Handlers ---

// HandleVerifyAgent checks an agent's KYA identity certificate.
func (h *Handlers) HandleVerifyAgent(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	addr := req.GetString("agent_address", "")
	if addr == "" {
		return mcp.NewToolResultError("agent_address is required"), nil
	}

	raw, err := h.client.Get(ctx, "/kya/agents/"+addr)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Agent not verified or no certificate found: %v", err)), nil
	}

	var resp struct {
		Certificate map[string]any `json:"certificate"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil || resp.Certificate == nil {
		return mcp.NewToolResultText("No KYA certificate found for this agent. Exercise caution."), nil
	}

	c := resp.Certificate
	var sb strings.Builder
	sb.WriteString("KYA Identity Certificate:\n")
	fmt.Fprintf(&sb, "  DID: %s\n", getString(c, "did"))
	fmt.Fprintf(&sb, "  Status: %s\n", getString(c, "status"))

	if rep, ok := c["reputation"].(map[string]any); ok {
		fmt.Fprintf(&sb, "  Trust Tier: %s\n", getString(rep, "trustTier"))
		if score, ok := getFloat(rep, "traceRankScore"); ok {
			fmt.Fprintf(&sb, "  TraceRank Score: %.1f\n", score)
		}
		if rate, ok := getFloat(rep, "successRate"); ok {
			fmt.Fprintf(&sb, "  Success Rate: %.0f%%\n", rate*100)
		}
	}

	if org, ok := c["org"].(map[string]any); ok {
		fmt.Fprintf(&sb, "  Organization: %s\n", getString(org, "orgName"))
	}

	return mcp.NewToolResultText(sb.String()), nil
}

// HandleCheckBudget checks a cost center's remaining budget.
func (h *Handlers) HandleCheckBudget(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ccID := req.GetString("cost_center_id", "")
	if ccID == "" {
		return mcp.NewToolResultError("cost_center_id is required"), nil
	}

	raw, err := h.client.Get(ctx, "/chargeback/cost-centers/"+ccID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to check budget: %v", err)), nil
	}

	var resp struct {
		CostCenter map[string]any `json:"costCenter"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil || resp.CostCenter == nil {
		return mcp.NewToolResultError("Cost center not found"), nil
	}

	cc := resp.CostCenter
	var sb strings.Builder
	fmt.Fprintf(&sb, "Cost Center: %s\n", getString(cc, "name"))
	fmt.Fprintf(&sb, "  Department: %s\n", getString(cc, "department"))
	fmt.Fprintf(&sb, "  Monthly Budget: %s USDC\n", getString(cc, "monthlyBudget"))
	if active, ok := cc["active"].(bool); ok {
		fmt.Fprintf(&sb, "  Active: %v\n", active)
	}

	return mcp.NewToolResultText(sb.String()), nil
}

// HandleFileDispute files a formal arbitration case.
func (h *Handlers) HandleFileDispute(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	escrowID := req.GetString("escrow_id", "")
	if escrowID == "" {
		return mcp.NewToolResultError("escrow_id is required"), nil
	}
	sellerAddr := req.GetString("seller_address", "")
	if sellerAddr == "" {
		return mcp.NewToolResultError("seller_address is required"), nil
	}
	amount := req.GetString("amount", "")
	if amount == "" {
		return mcp.NewToolResultError("amount is required"), nil
	}
	reason := req.GetString("reason", "")
	if reason == "" {
		return mcp.NewToolResultError("reason is required"), nil
	}
	contractID := req.GetString("contract_id", "")

	body := map[string]any{
		"escrowId":   escrowID,
		"buyerAddr":  h.client.cfg.AgentAddress,
		"sellerAddr": sellerAddr,
		"amount":     amount,
		"reason":     reason,
	}
	if contractID != "" {
		body["contractId"] = contractID
	}

	raw, err := h.client.Post(ctx, "/arbitration/cases", body)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to file dispute: %v", err)), nil
	}

	var resp struct {
		Case map[string]any `json:"case"`
	}
	if err := json.Unmarshal(raw, &resp); err == nil && resp.Case != nil {
		caseID := getString(resp.Case, "id")
		autoResolvable := false
		if v, ok := resp.Case["autoResolvable"].(bool); ok {
			autoResolvable = v
		}

		var sb strings.Builder
		fmt.Fprintf(&sb, "Arbitration case filed successfully.\n")
		fmt.Fprintf(&sb, "  Case ID: %s\n", caseID)
		fmt.Fprintf(&sb, "  Status: open\n")
		fmt.Fprintf(&sb, "  Fee: %s USDC\n", getString(resp.Case, "fee"))
		if autoResolvable {
			sb.WriteString("  Auto-resolution will be attempted using the behavioral contract.\n")
		} else {
			sb.WriteString("  A human arbiter will be assigned to review the case.\n")
		}
		return mcp.NewToolResultText(sb.String()), nil
	}

	return mcp.NewToolResultText("Dispute filed. Check case status via the API."), nil
}

// HandleGetAlerts retrieves spend anomaly alerts.
func (h *Handlers) HandleGetAlerts(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	severity := req.GetString("severity", "")
	limit := 10
	if v := req.GetArguments()["limit"]; v != nil {
		if f, ok := v.(float64); ok {
			limit = int(f)
		}
	}

	path := fmt.Sprintf("/forensics/agents/%s/alerts?limit=%d", h.client.cfg.AgentAddress, limit)
	if severity != "" {
		path += "&severity=" + severity
	}

	raw, err := h.client.Get(ctx, path)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to get alerts: %v", err)), nil
	}

	var resp struct {
		Alerts []map[string]any `json:"alerts"`
		Count  int              `json:"count"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return mcp.NewToolResultText("No alerts found."), nil
	}

	if resp.Count == 0 {
		return mcp.NewToolResultText("No spend anomaly alerts. All clear."), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "%d alert(s) found:\n\n", resp.Count)
	for i, a := range resp.Alerts {
		fmt.Fprintf(&sb, "%d. [%s] %s\n", i+1,
			strings.ToUpper(getString(a, "severity")),
			getString(a, "message"))
		fmt.Fprintf(&sb, "   Type: %s | Score: %s\n", getString(a, "type"), getString(a, "score"))
		if i < len(resp.Alerts)-1 {
			sb.WriteString("\n")
		}
	}

	return mcp.NewToolResultText(sb.String()), nil
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
	fmt.Fprintf(&sb, "Found %d service(s):\n\n", len(services))
	for i, s := range services {
		fmt.Fprintf(&sb, "%d. %s\n", i+1, s.Name)
		fmt.Fprintf(&sb, "   Type: %s | Price: %s USDC\n", s.Type, s.Price)
		fmt.Fprintf(&sb, "   Provider: %s\n", s.Address)
		if s.Tier != "" {
			fmt.Fprintf(&sb, "   Reputation: %.1f (%s) | Success: %.0f%%\n", s.Reputation, s.Tier, s.SuccessRate*100)
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
	fmt.Fprintf(&sb, "  Available: %s USDC\n", getString(bal, "available"))
	if v := getString(bal, "pending"); v != "" && v != "0" && v != "0.000000" {
		fmt.Fprintf(&sb, "  Pending:   %s USDC\n", v)
	}
	if v := getString(bal, "escrowed"); v != "" && v != "0" && v != "0.000000" {
		fmt.Fprintf(&sb, "  Escrowed:  %s USDC\n", v)
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
		fmt.Fprintf(&sb, "  Address: %s\n", v)
	}
	if v, ok := getFloat(m, "score", "reputationScore"); ok {
		fmt.Fprintf(&sb, "  Score: %.1f\n", v)
	}
	if v := getString(m, "tier", "reputationTier"); v != "" {
		fmt.Fprintf(&sb, "  Tier: %s\n", v)
	}
	if v, ok := getFloat(m, "successRate", "success_rate"); ok {
		fmt.Fprintf(&sb, "  Success Rate: %.0f%%\n", v*100)
	}
	if v, ok := getFloat(m, "txCount", "transactionCount"); ok {
		fmt.Fprintf(&sb, "  Transactions: %.0f\n", v)
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
	fmt.Fprintf(&sb, "Found %d agent(s):\n\n", len(resp.Agents))
	for i, a := range resp.Agents {
		name := getString(a, "name")
		addr := getString(a, "address")
		desc := getString(a, "description")
		fmt.Fprintf(&sb, "%d. %s (%s)\n", i+1, name, addr)
		if desc != "" {
			fmt.Fprintf(&sb, "   %s\n", desc)
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

// --- Marketplace / Offers Handlers ---

// HandleBrowseMarketplace lists standing offers on the marketplace.
func (h *Handlers) HandleBrowseMarketplace(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	serviceType := req.GetString("service_type", "")
	limit := int(req.GetFloat("limit", 20))

	raw, err := h.client.ListOffers(ctx, serviceType, limit)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to browse marketplace: %v", err)), nil
	}

	var resp struct {
		Offers []map[string]any `json:"offers"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return mcp.NewToolResultText(string(raw)), nil
	}

	if len(resp.Offers) == 0 {
		msg := "No offers found"
		if serviceType != "" {
			msg += " for service type '" + serviceType + "'"
		}
		return mcp.NewToolResultText(msg + ". Try a different service type or check back later."), nil
	}

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "Found %d marketplace offers:\n\n", len(resp.Offers))
	for i, o := range resp.Offers {
		id, _ := o["id"].(string)
		seller, _ := o["sellerAddr"].(string)
		svcType, _ := o["serviceType"].(string)
		price, _ := o["price"].(string)
		cap, _ := o["remainingCap"].(float64)
		fmt.Fprintf(&buf, "%d. **%s** — %s\n", i+1, svcType, id)
		fmt.Fprintf(&buf, "   Price: $%s USDC | Available: %.0f slots\n", price, cap)
		if seller != "" {
			fmt.Fprintf(&buf, "   Seller: %s\n", seller)
		}
		fmt.Fprintln(&buf)
	}

	return mcp.NewToolResultText(buf.String()), nil
}

// HandlePostOffer creates a standing offer to sell a service.
func (h *Handlers) HandlePostOffer(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	serviceType := req.GetString("service_type", "")
	if serviceType == "" {
		return mcp.NewToolResultError("service_type is required"), nil
	}
	price := req.GetString("price", "")
	if price == "" {
		return mcp.NewToolResultError("price is required"), nil
	}
	capacity := int(req.GetFloat("capacity", 0))
	if capacity <= 0 {
		return mcp.NewToolResultError("capacity must be a positive number"), nil
	}
	description := req.GetString("description", "")
	endpoint := req.GetString("endpoint", "")

	raw, err := h.client.PostOffer(ctx, serviceType, price, capacity, description, endpoint)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to post offer: %v", err)), nil
	}

	var resp map[string]any
	if err := json.Unmarshal(raw, &resp); err != nil {
		return mcp.NewToolResultText(string(raw)), nil
	}

	offer, _ := resp["offer"].(map[string]any)
	if offer == nil {
		offer = resp
	}
	id, _ := offer["id"].(string)

	return mcp.NewToolResultText(fmt.Sprintf(
		"Offer posted successfully!\n\nOffer ID: %s\nService: %s\nPrice: $%s USDC\nCapacity: %d\n\n"+
			"Other agents can now claim this offer via claim_offer.",
		id, serviceType, price, capacity,
	)), nil
}

// HandleClaimOffer claims a standing offer from the marketplace.
func (h *Handlers) HandleClaimOffer(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	offerID := req.GetString("offer_id", "")
	if offerID == "" {
		return mcp.NewToolResultError("offer_id is required"), nil
	}

	raw, err := h.client.ClaimOffer(ctx, offerID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to claim offer: %v", err)), nil
	}

	var resp map[string]any
	if err := json.Unmarshal(raw, &resp); err != nil {
		return mcp.NewToolResultText(string(raw)), nil
	}

	claim, _ := resp["claim"].(map[string]any)
	if claim == nil {
		claim = resp
	}
	claimID, _ := claim["id"].(string)

	return mcp.NewToolResultText(fmt.Sprintf(
		"Offer claimed successfully!\n\nClaim ID: %s\nOffer: %s\n\n"+
			"Your funds are held in escrow. The seller will deliver the service.\n"+
			"After delivery, use complete_claim to release payment.",
		claimID, offerID,
	)), nil
}

// HandleCancelOffer cancels the caller's standing offer.
func (h *Handlers) HandleCancelOffer(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	offerID := req.GetString("offer_id", "")
	if offerID == "" {
		return mcp.NewToolResultError("offer_id is required"), nil
	}

	_, err := h.client.CancelOffer(ctx, offerID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to cancel offer: %v", err)), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("Offer %s cancelled. No new claims will be accepted.", offerID)), nil
}

// HandleDeliverClaim marks a claimed offer as delivered (seller action).
func (h *Handlers) HandleDeliverClaim(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	claimID := req.GetString("claim_id", "")
	if claimID == "" {
		return mcp.NewToolResultError("claim_id is required"), nil
	}

	_, err := h.client.DeliverClaim(ctx, claimID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to mark delivery: %v", err)), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf(
		"Claim %s marked as delivered. Waiting for buyer to confirm and release payment.",
		claimID,
	)), nil
}

// HandleCompleteClaim confirms delivery and releases payment (buyer action).
func (h *Handlers) HandleCompleteClaim(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	claimID := req.GetString("claim_id", "")
	if claimID == "" {
		return mcp.NewToolResultError("claim_id is required"), nil
	}

	_, err := h.client.CompleteClaim(ctx, claimID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to complete claim: %v", err)), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf(
		"Claim %s completed! Payment released from escrow to the seller.",
		claimID,
	)), nil
}
