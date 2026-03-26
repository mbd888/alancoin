package mcpserver

import "github.com/mark3labs/mcp-go/mcp"

// Tool definitions for the Alancoin MCP server.
// Descriptions are what the LLM reads to decide which tool to use.

var ToolDiscoverServices = mcp.NewTool("discover_services",
	mcp.WithDescription(
		"Search the Alancoin service marketplace for AI agent services. "+
			"Returns available services with pricing in USDC, reputation scores, and endpoints. "+
			"Use this to find services before calling them."),
	mcp.WithString("service_type",
		mcp.Description("Filter by service type (e.g. 'translation', 'inference', 'summarization')")),
	mcp.WithString("max_price",
		mcp.Description("Maximum price in USDC (e.g. '0.10'). Only returns services at or below this price.")),
	mcp.WithString("sort_by",
		mcp.Description("Sort results: 'price' (cheapest first), 'reputation' (highest rated), or 'value' (best price-to-quality ratio)"),
		mcp.Enum("price", "reputation", "value")),
	mcp.WithString("query",
		mcp.Description("Free-text search query to find services by name or description")),
)

var ToolCallService = mcp.NewTool("call_service",
	mcp.WithDescription(
		"Discover, pay for, and call an AI agent service in one step. "+
			"Automatically finds the best matching service, holds your USDC in escrow, "+
			"calls the service, and auto-confirms payment on success. "+
			"If the service call fails, your funds remain in escrow — use dispute_escrow to request a refund."),
	mcp.WithString("service_type",
		mcp.Required(),
		mcp.Description("Type of service to call (e.g. 'translation', 'inference', 'summarization')")),
	mcp.WithString("max_price",
		mcp.Description("Maximum USDC price you're willing to pay. If omitted, uses the cheapest available service.")),
	mcp.WithString("prefer",
		mcp.Description("Service selection strategy: 'cheapest', 'reputation' (best rated), or 'best_value'"),
		mcp.Enum("cheapest", "reputation", "best_value")),
	mcp.WithObject("params",
		mcp.Description("Parameters to pass to the service (varies by service type). For translation: {\"text\": \"hello\", \"target_language\": \"es\"}")),
)

var ToolCheckBalance = mcp.NewTool("check_balance",
	mcp.WithDescription(
		"Check your agent's current USDC balance on Alancoin. "+
			"Shows available funds, pending holds, and escrowed amounts."),
)

var ToolGetReputation = mcp.NewTool("get_reputation",
	mcp.WithDescription(
		"Get the reputation score and tier for any agent on the Alancoin network. "+
			"Shows success rate, transaction count, and trust tier (new/emerging/established/trusted/elite)."),
	mcp.WithString("agent_address",
		mcp.Required(),
		mcp.Description("The agent's address (e.g. '0x1234...')")),
)

var ToolListAgents = mcp.NewTool("list_agents",
	mcp.WithDescription(
		"Browse registered agents on the Alancoin network. "+
			"Optionally filter by service type to find agents offering specific capabilities."),
	mcp.WithString("service_type",
		mcp.Description("Filter agents by service type (e.g. 'translation')")),
	mcp.WithNumber("limit",
		mcp.Description("Maximum number of agents to return (default 20)")),
)

var ToolGetNetworkStats = mcp.NewTool("get_network_stats",
	mcp.WithDescription(
		"Get Alancoin network statistics including platform info, supported chain, and deposit address."),
)

var ToolPayAgent = mcp.NewTool("pay_agent",
	mcp.WithDescription(
		"Send a direct USDC payment to another agent via escrow. "+
			"Your funds are held in escrow until you confirm delivery or dispute."),
	mcp.WithString("recipient",
		mcp.Required(),
		mcp.Description("Recipient agent's address (e.g. '0x1234...')")),
	mcp.WithString("amount",
		mcp.Required(),
		mcp.Description("Amount in USDC to pay (e.g. '1.50')")),
	mcp.WithString("memo",
		mcp.Description("Optional memo or description for the payment")),
)

var ToolDisputeEscrow = mcp.NewTool("dispute_escrow",
	mcp.WithDescription(
		"Dispute a service call or payment and request a refund. "+
			"Use this when a service delivered a bad result or failed to deliver. "+
			"The escrowed USDC will be refunded to your balance."),
	mcp.WithString("escrow_id",
		mcp.Required(),
		mcp.Description("The escrow ID from a previous call_service or pay_agent result")),
	mcp.WithString("reason",
		mcp.Required(),
		mcp.Description("Explanation of why the service result was unsatisfactory")),
)

// --- Enterprise Plugin Tools ---

var ToolVerifyAgent = mcp.NewTool("verify_agent",
	mcp.WithDescription(
		"Verify an agent's KYA (Know Your Agent) identity certificate. "+
			"Returns the agent's trust tier (AAA-D), organizational binding, "+
			"permission scope, and reputation snapshot. Use this before transacting "+
			"with an unknown agent to assess trustworthiness."),
	mcp.WithString("agent_address",
		mcp.Required(),
		mcp.Description("The agent's address to verify (e.g. '0x1234...')")),
)

var ToolCheckBudget = mcp.NewTool("check_budget",
	mcp.WithDescription(
		"Check the remaining budget for a cost center. "+
			"Returns current month spend, budget limit, and utilization percentage. "+
			"Use this before making expensive service calls to ensure budget is available."),
	mcp.WithString("cost_center_id",
		mcp.Required(),
		mcp.Description("The cost center ID to check (e.g. 'cc_abc123')")),
)

var ToolFileDispute = mcp.NewTool("file_dispute",
	mcp.WithDescription(
		"File a formal arbitration case for a disputed escrow payment. "+
			"This escalates beyond a simple dispute to a structured resolution process. "+
			"If a behavioral contract was attached, auto-resolution may be attempted first."),
	mcp.WithString("escrow_id",
		mcp.Required(),
		mcp.Description("The escrow ID being disputed")),
	mcp.WithString("seller_address",
		mcp.Required(),
		mcp.Description("The seller agent's address")),
	mcp.WithString("amount",
		mcp.Required(),
		mcp.Description("The disputed amount in USDC")),
	mcp.WithString("reason",
		mcp.Required(),
		mcp.Description("Detailed reason for the dispute")),
	mcp.WithString("contract_id",
		mcp.Description("Optional: behavioral contract ID for auto-resolution")),
)

// --- Marketplace / Offers Tools ---

var ToolBrowseMarketplace = mcp.NewTool("browse_marketplace",
	mcp.WithDescription(
		"Browse the Alancoin marketplace for standing service offers. "+
			"Shows available offers with pricing, capacity, and seller details. "+
			"Use this to find pre-posted offers before claiming them with claim_offer."),
	mcp.WithString("service_type",
		mcp.Description("Filter by service type (e.g. 'translation', 'inference')")),
	mcp.WithNumber("limit",
		mcp.Description("Maximum number of offers to return (default 20)")),
)

var ToolPostOffer = mcp.NewTool("post_offer",
	mcp.WithDescription(
		"Post a standing offer to sell a service on the Alancoin marketplace. "+
			"Other agents (or LLMs) can claim your offer to purchase your service. "+
			"Set a price per claim, total capacity, and an endpoint to receive work requests."),
	mcp.WithString("service_type",
		mcp.Required(),
		mcp.Description("Type of service you're offering (e.g. 'translation', 'inference', 'summarization')")),
	mcp.WithString("price",
		mcp.Required(),
		mcp.Description("Price per claim in USDC (e.g. '0.50')")),
	mcp.WithNumber("capacity",
		mcp.Required(),
		mcp.Description("Total number of claims this offer can accept")),
	mcp.WithString("description",
		mcp.Description("Human-readable description of what the service does")),
	mcp.WithString("endpoint",
		mcp.Description("HTTPS URL where work requests will be sent when the offer is claimed")),
)

var ToolClaimOffer = mcp.NewTool("claim_offer",
	mcp.WithDescription(
		"Claim a standing offer from the marketplace. "+
			"This atomically locks your USDC in escrow and reserves capacity on the offer. "+
			"The seller will then deliver the service. Use complete_claim to release payment after delivery."),
	mcp.WithString("offer_id",
		mcp.Required(),
		mcp.Description("The offer ID to claim (from browse_marketplace results)")),
)

var ToolCancelOffer = mcp.NewTool("cancel_offer",
	mcp.WithDescription(
		"Cancel your own standing offer. Only the seller who posted the offer can cancel it. "+
			"Existing claims are not affected — only prevents new claims."),
	mcp.WithString("offer_id",
		mcp.Required(),
		mcp.Description("The offer ID to cancel")),
)

var ToolDeliverClaim = mcp.NewTool("deliver_claim",
	mcp.WithDescription(
		"Mark a claimed offer as delivered (seller action). "+
			"Call this after you've completed the work for a claimed offer. "+
			"The buyer can then confirm and release payment with complete_claim."),
	mcp.WithString("claim_id",
		mcp.Required(),
		mcp.Description("The claim ID to mark as delivered")),
)

var ToolCompleteClaim = mcp.NewTool("complete_claim",
	mcp.WithDescription(
		"Confirm delivery and release payment to the seller (buyer action). "+
			"Call this after the seller has delivered the service for your claimed offer. "+
			"USDC is released from escrow to the seller's balance."),
	mcp.WithString("claim_id",
		mcp.Required(),
		mcp.Description("The claim ID to complete")),
)

var ToolGetAlerts = mcp.NewTool("get_alerts",
	mcp.WithDescription(
		"Get spend anomaly alerts for your agent from the forensics engine. "+
			"Returns any detected anomalies such as unusual spending patterns, "+
			"new counterparties, or service type deviations."),
	mcp.WithString("severity",
		mcp.Description("Filter by severity: 'info', 'warning', or 'critical'"),
		mcp.Enum("info", "warning", "critical")),
	mcp.WithNumber("limit",
		mcp.Description("Maximum alerts to return (default 10)")),
)
