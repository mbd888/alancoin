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
