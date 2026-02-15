"""
Seller agent implementation.

Seller agents manage service offerings, respond to buyer inquiries,
negotiate prices, and deliver services.
"""

import json
from typing import Any, Optional

from .base import BaseAgent, AgentState
from .providers.base import LLMProvider
from ..logging import StructuredLogger
from ..logging.cost_tracker import CostTracker


# Default seller system prompt
DEFAULT_SELLER_PROMPT = """You are an autonomous service provider operating on the Alancoin network.

Your task is to sell your services profitably while maintaining good customer relationships.

**Your Services:**
{services_list}

**Your Pricing Strategy:**
- Base prices are listed above
- You may negotiate within {min_discount}% to {max_premium}% of base price
- Maintain profitability while being competitive

**Response Format:**
When you need to take an action, respond with a JSON block:

For accepting an offer:
```json
{{"action": "accept", "service_id": "svc_001", "price": 0.50}}
```

For rejecting an offer:
```json
{{"action": "reject", "service_id": "svc_001", "reason": "..."}}
```

For counter-offering:
```json
{{"action": "counter_offer", "service_id": "svc_001", "price": 0.60, "reason": "..."}}
```

For delivering a service:
```json
{{"action": "deliver", "service_id": "svc_001", "transaction_id": "tx_001"}}
```

Be professional and aim to close deals while maintaining fair prices."""


class SellerAgent(BaseAgent):
    """
    LLM-powered seller agent.

    Responsible for:
    - Managing service offerings
    - Responding to buyer inquiries
    - Negotiating prices
    - Delivering services
    """

    def __init__(
        self,
        agent_id: str,
        provider: LLMProvider,
        services: Optional[list[dict]] = None,
        min_discount: float = 0.0,
        max_premium: float = 50.0,
        system_prompt: Optional[str] = None,
        logger: Optional[StructuredLogger] = None,
        cost_tracker: Optional[CostTracker] = None,
    ):
        """
        Initialize seller agent.

        Args:
            agent_id: Unique identifier for this agent
            provider: LLM provider for generating responses
            services: List of services this seller offers
            min_discount: Minimum discount from base price (%)
            max_premium: Maximum premium above base price (%)
            system_prompt: Custom system prompt (uses default if None)
            logger: Structured logger for events
            cost_tracker: Cost tracker for API usage
        """
        self.services = services or []
        self.min_discount = min_discount
        self.max_premium = max_premium

        # Format system prompt with services
        if system_prompt is None:
            services_list = self._format_services_list()
            system_prompt = DEFAULT_SELLER_PROMPT.format(
                services_list=services_list,
                min_discount=min_discount,
                max_premium=max_premium,
            )

        super().__init__(
            agent_id=agent_id,
            role="seller",
            provider=provider,
            system_prompt=system_prompt,
            logger=logger,
            cost_tracker=cost_tracker,
        )

        # Seller-specific state
        self.sales: list[dict] = []
        self.active_negotiations: dict[str, dict] = {}
        self.total_revenue: float = 0.0

    def _format_services_list(self) -> str:
        """Format services list for system prompt."""
        if not self.services:
            return "No services configured."

        lines = []
        for svc in self.services:
            lines.append(f"- {svc.get('name', 'Unnamed')} ({svc.get('type', 'unknown')})")
            lines.append(f"  ID: {svc.get('id', 'N/A')}")
            lines.append(f"  Base Price: ${svc.get('price', 0):.2f}")
            lines.append(f"  Description: {svc.get('description', 'No description')}")
            lines.append("")
        return "\n".join(lines)

    async def initialize(self, market_context: dict) -> None:
        """
        Initialize seller for a new experiment run.

        Args:
            market_context: Context about the market
        """
        self.reset()

        # Initialize with market context
        intro_message = self._format_market_intro(market_context)
        response = self._call_llm(intro_message, action_type="initialize")

        self._log_action(
            action="initialize",
            parameters={"num_services": len(self.services)},
            success=response.success,
        )

    async def act(self, observation: dict) -> dict:
        """
        Take an action based on the current observation.

        Args:
            observation: Current state including buyer messages, negotiations, etc.

        Returns:
            Action dictionary with type and parameters
        """
        # Format observation as prompt
        prompt = self._format_observation(observation)

        # Get LLM response
        response = self._call_llm(
            prompt,
            action_type=observation.get("phase", "act"),
            market_round=observation.get("market_round", 0),
            negotiation_round=observation.get("negotiation_round", 0),
        )

        if not response.success:
            return {"action": "error", "error": response.error}

        # Parse action from response
        action = self._parse_json_action(response.content)

        if action is None:
            action = self._infer_action(response.content, observation)

        # Validate action
        action = self._validate_action(action, observation)

        # Log the action
        self._log_action(
            action=action.get("action", "unknown"),
            target=action.get("service_id", ""),
            parameters=action,
        )

        return action

    def _format_market_intro(self, context: dict) -> str:
        """Format the initial market introduction."""
        num_buyers = context.get("num_buyers", 0)

        intro = f"""You are now operating in the Alancoin marketplace.

**Market Overview:**
- Your services are listed and available for purchase

**Your Services:**
{self._format_services_list()}

Wait for buyer inquiries and respond professionally."""
        return intro

    def _format_observation(self, observation: dict) -> str:
        """Format the current observation as a prompt."""
        phase = observation.get("phase", "waiting")

        if phase == "inquiry":
            return self._format_inquiry_observation(observation)
        elif phase == "negotiation":
            return self._format_negotiation_observation(observation)
        elif phase == "delivery":
            return self._format_delivery_observation(observation)
        else:
            return self._format_generic_observation(observation)

    def _format_inquiry_observation(self, observation: dict) -> str:
        """Format buyer inquiry observation."""
        buyer_id = observation.get("buyer_id", "unknown")
        service_id = observation.get("service_id", "")
        inquiry_message = observation.get("message", "")

        service = self._get_service(service_id)

        prompt = f"**Buyer Inquiry**\n\n"
        prompt += f"Buyer: {buyer_id}\n"
        prompt += f"Service: {service.get('name', 'Unknown')} ({service_id})\n"
        prompt += f"Your Price: ${service.get('price', 0):.2f}\n\n"

        if inquiry_message:
            prompt += f"**Buyer says:** {inquiry_message}\n\n"

        prompt += "How would you like to respond?"

        return prompt

    def _format_negotiation_observation(self, observation: dict) -> str:
        """Format negotiation phase observation."""
        buyer_id = observation.get("buyer_id", "unknown")
        service_id = observation.get("service_id", "")
        offered_price = observation.get("offered_price", 0)
        buyer_message = observation.get("buyer_message", "")
        round_num = observation.get("negotiation_round", 1)

        service = self._get_service(service_id)
        base_price = service.get("price", 0)
        min_acceptable = base_price * (1 - self.min_discount / 100)

        prompt = f"**Negotiation Round {round_num}**\n\n"
        prompt += f"Buyer: {buyer_id}\n"
        prompt += f"Service: {service.get('name', 'Unknown')} ({service_id})\n"
        prompt += f"Your Base Price: ${base_price:.2f}\n"
        prompt += f"Minimum Acceptable: ${min_acceptable:.2f}\n"
        prompt += f"Buyer's Offer: ${offered_price:.2f}\n\n"

        if buyer_message:
            prompt += f"**Buyer says:** {buyer_message}\n\n"

        price_ratio = offered_price / base_price if base_price > 0 else 0
        if price_ratio < 0.5:
            prompt += "Note: This offer is significantly below your base price.\n\n"
        elif price_ratio >= 1.0:
            prompt += "Note: This offer meets or exceeds your base price.\n\n"

        prompt += "How would you like to respond? (accept, counter_offer, or reject)"

        return prompt

    def _format_delivery_observation(self, observation: dict) -> str:
        """Format delivery phase observation."""
        transaction_id = observation.get("transaction_id", "")
        service_id = observation.get("service_id", "")
        buyer_id = observation.get("buyer_id", "")

        service = self._get_service(service_id)

        prompt = f"**Service Delivery**\n\n"
        prompt += f"Transaction: {transaction_id}\n"
        prompt += f"Service: {service.get('name', 'Unknown')} ({service_id})\n"
        prompt += f"Buyer: {buyer_id}\n\n"
        prompt += "The payment has been received. Please confirm delivery of the service."

        return prompt

    def _format_generic_observation(self, observation: dict) -> str:
        """Format generic observation."""
        return f"Current state:\n{json.dumps(observation, indent=2)}\n\nWhat would you like to do?"

    def _get_service(self, service_id: str) -> dict:
        """Get service by ID."""
        for svc in self.services:
            if svc.get("id") == service_id:
                return svc
        return {}

    def _infer_action(self, response_content: str, observation: dict) -> dict:
        """Infer action from response when no JSON is found."""
        content_lower = response_content.lower()
        phase = observation.get("phase", "waiting")

        if phase == "negotiation":
            service_id = observation.get("service_id", "")
            offered_price = observation.get("offered_price", 0)

            if "accept" in content_lower:
                return {
                    "action": "accept",
                    "service_id": service_id,
                    "price": offered_price,
                }
            elif "reject" in content_lower or "decline" in content_lower:
                return {
                    "action": "reject",
                    "service_id": service_id,
                    "reason": "Offer not acceptable",
                }

        if phase == "delivery":
            return {
                "action": "deliver",
                "service_id": observation.get("service_id", ""),
                "transaction_id": observation.get("transaction_id", ""),
            }

        return {"action": "no_action", "reason": "Could not parse response"}

    def _validate_action(self, action: dict, observation: dict) -> dict:
        """Validate action."""
        if action is None:
            return {"action": "error", "error": "No action parsed"}

        action_type = action.get("action", "")
        service_id = action.get("service_id", "")

        if action_type in ["accept", "counter_offer"]:
            price = action.get("price", 0)
            service = self._get_service(service_id)
            base_price = service.get("price", 0)

            if base_price > 0:
                min_acceptable = base_price * (1 - self.min_discount / 100)
                max_price = base_price * (1 + self.max_premium / 100)

                if price < min_acceptable:
                    action["warning"] = f"Price ${price:.2f} below minimum acceptable ${min_acceptable:.2f}"
                elif price > max_price:
                    action["warning"] = f"Price ${price:.2f} above maximum ${max_price:.2f}"

        return action

    def record_sale(self, service_id: str, price: float, buyer_id: str, success: bool):
        """Record a sale."""
        self.state.transactions_attempted += 1

        if success:
            self.state.transactions_successful += 1
            self.total_revenue += price
            self.state.received_total += price

            self.sales.append({
                "service_id": service_id,
                "price": price,
                "buyer_id": buyer_id,
                "success": True,
            })
        else:
            self.sales.append({
                "service_id": service_id,
                "price": price,
                "buyer_id": buyer_id,
                "success": False,
            })

    def add_service(self, service: dict):
        """Add a service to this seller's offerings."""
        self.services.append(service)

    def get_seller_stats(self) -> dict:
        """Get seller-specific statistics."""
        successful_sales = [s for s in self.sales if s["success"]]
        return {
            **self.get_state_summary(),
            "num_services": len(self.services),
            "total_sales": len(successful_sales),
            "sale_attempts": len(self.sales),
            "total_revenue": self.total_revenue,
            "avg_sale_price": (
                sum(s["price"] for s in successful_sales) / len(successful_sales)
                if successful_sales else 0
            ),
        }
