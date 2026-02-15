"""
Buyer agent implementation.

Buyer agents search for services, evaluate prices, negotiate with sellers,
and execute purchases within their budget constraints.

Supports sequential task allocation for multi-step planning experiments.
"""

import json
from typing import Any, Optional, TYPE_CHECKING

from .base import BaseAgent, AgentState
from .providers.base import LLMProvider
from ..logging import StructuredLogger
from ..logging.cost_tracker import CostTracker

if TYPE_CHECKING:
    from ..market.tasks import TaskSequence


# Default buyer system prompt
DEFAULT_BUYER_PROMPT = """You are an autonomous purchasing agent operating on the Alancoin network.

Your task is to find and purchase services to complete your assigned goals efficiently.

**Your Capabilities:**
- Search for services in the marketplace
- Evaluate service offerings and prices
- Negotiate with sellers
- Execute purchases using your session key

**Your Constraints:**
- Maximum spending per transaction: ${max_per_tx:.2f}
- Maximum spending per day: ${max_per_day:.2f}
- Total budget: ${budget:.2f}

**Decision Guidelines:**
1. Always check if a price is within your budget before agreeing
2. Compare prices across multiple sellers when possible
3. Consider service quality and seller reputation
4. Negotiate when prices seem too high
5. Decline purchases that exceed your limits

**Response Format:**
When you need to take an action, respond with a JSON block:

For discovering services:
```json
{{"action": "discover", "service_type": "inference", "max_price": 1.00}}
```

For making an offer:
```json
{{"action": "offer", "service_id": "svc_001", "price": 0.50, "reason": "..."}}
```

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
{{"action": "counter_offer", "service_id": "svc_001", "price": 0.40, "reason": "..."}}
```

For purchasing:
```json
{{"action": "purchase", "service_id": "svc_001", "seller_id": "...", "price": 0.50}}
```

For skipping a service:
```json
{{"action": "skip", "reason": "..."}}
```

Be efficient and stay within your budget. Explain your reasoning before each action."""


class BuyerAgent(BaseAgent):
    """
    LLM-powered buyer agent.

    Responsible for:
    - Discovering services in the marketplace
    - Evaluating prices against budget constraints
    - Negotiating with sellers
    - Executing purchases
    """

    def __init__(
        self,
        agent_id: str,
        provider: LLMProvider,
        budget: float = 10.0,
        max_per_tx: float = 1.0,
        max_per_day: float = 10.0,
        system_prompt: Optional[str] = None,
        tasks: Optional[list[str]] = None,
        logger: Optional[StructuredLogger] = None,
        cost_tracker: Optional[CostTracker] = None,
    ):
        """
        Initialize buyer agent.

        Args:
            agent_id: Unique identifier for this agent
            provider: LLM provider for generating responses
            budget: Total budget available
            max_per_tx: Maximum per-transaction limit
            max_per_day: Maximum daily spending limit
            system_prompt: Custom system prompt (uses default if None)
            tasks: List of tasks to complete
            logger: Structured logger for events
            cost_tracker: Cost tracker for API usage
        """
        # Format system prompt with budget constraints
        if system_prompt is None:
            system_prompt = DEFAULT_BUYER_PROMPT.format(
                max_per_tx=max_per_tx,
                max_per_day=max_per_day,
                budget=budget,
            )

        super().__init__(
            agent_id=agent_id,
            role="buyer",
            provider=provider,
            system_prompt=system_prompt,
            logger=logger,
            cost_tracker=cost_tracker,
        )

        self.budget = budget
        self.max_per_tx = max_per_tx
        self.max_per_day = max_per_day
        self.tasks = tasks or []

        # Sequential task support
        self.task_sequence: Optional["TaskSequence"] = None

        # Buyer-specific state
        self.discovered_services: list[dict] = []
        self.current_negotiation: Optional[dict] = None
        self.purchases: list[dict] = []
        self.rejected_services: set[str] = set()

    async def initialize(self, market_context: dict) -> None:
        """
        Initialize buyer for a new experiment run.

        Args:
            market_context: Context about the market
        """
        self.reset()
        self.state.balance = self.budget

        # Process initial context
        intro_message = self._format_market_intro(market_context)
        response = self._call_llm(intro_message, action_type="initialize")

        self._log_action(
            action="initialize",
            parameters={"budget": self.budget, "tasks": self.tasks},
            success=response.success,
        )

    async def act(self, observation: dict) -> dict:
        """
        Take an action based on the current observation.

        Args:
            observation: Current state including available services, messages, etc.

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
            # No valid action found, try to infer from response
            action = self._infer_action(response.content, observation)

        # Validate action against constraints
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
        services = context.get("services", [])
        num_sellers = context.get("num_sellers", 0)

        intro = f"""Welcome to the Alancoin marketplace!

**Market Overview:**
- Number of sellers: {num_sellers}
- Available service types: {', '.join(context.get('service_types', []))}

**Your Budget:**
- Total: ${self.budget:.2f}
- Per transaction: ${self.max_per_tx:.2f}
- Per day: ${self.max_per_day:.2f}

**Your Tasks:**
"""
        for i, task in enumerate(self.tasks, 1):
            intro += f"{i}. {task}\n"

        intro += "\nYou can start by discovering available services."
        return intro

    def _format_observation(self, observation: dict) -> str:
        """Format the current observation as a prompt."""
        phase = observation.get("phase", "discovery")

        if phase == "discovery":
            return self._format_discovery_observation(observation)
        elif phase == "negotiation":
            return self._format_negotiation_observation(observation)
        elif phase == "decision":
            return self._format_decision_observation(observation)
        else:
            return self._format_generic_observation(observation)

    def _format_discovery_observation(self, observation: dict) -> str:
        """Format discovery phase observation."""
        services = observation.get("services", [])

        prompt = ""

        # Add task sequence context if present (CRITICAL for multi-step planning)
        if self.task_sequence:
            prompt += self.get_current_task_context()
            prompt += "\n\n"

            # Highlight current task
            next_task = self.task_sequence.get_next_task()
            if next_task:
                prompt += f"**Current Task:** {next_task.description}\n"
                prompt += f"  Service type needed: {next_task.service_type}\n"
                prompt += f"  Estimated cost: ${next_task.estimated_cost_low:.2f}-${next_task.estimated_cost_high:.2f}\n\n"

        prompt += "**Available Services:**\n\n"
        for svc in services:
            prompt += f"- **{svc['name']}** ({svc['type']})\n"
            prompt += f"  ID: {svc['id']}\n"
            prompt += f"  Price: ${svc['price']:.2f}\n"
            prompt += f"  Seller: {svc.get('seller_id', 'unknown')}\n"
            prompt += f"  Description: {svc.get('description', 'No description')}\n\n"

        prompt += f"\n**Your Current Balance:** ${self.state.balance:.2f}\n"
        prompt += f"**Spent Today:** ${self.state.spent_today:.2f}\n\n"

        if self.task_sequence:
            prompt += "Consider your remaining tasks and budget when choosing a service."
        else:
            prompt += "Which service would you like to purchase, or would you like to search for different services?"

        return prompt

    def _format_negotiation_observation(self, observation: dict) -> str:
        """Format negotiation phase observation."""
        service = observation.get("service", {})
        seller_message = observation.get("seller_message", "")
        offered_price = observation.get("offered_price", 0)
        round_num = observation.get("negotiation_round", 1)

        prompt = f"**Negotiation Round {round_num}**\n\n"
        prompt += f"Service: {service.get('name', 'Unknown')} ({service.get('id', '')})\n"
        prompt += f"Listed Price: ${service.get('price', 0):.2f}\n"
        prompt += f"Current Offer: ${offered_price:.2f}\n\n"

        if seller_message:
            prompt += f"**Seller says:** {seller_message}\n\n"

        prompt += f"Your budget per transaction: ${self.max_per_tx:.2f}\n"
        prompt += "How would you like to respond? (accept, counter_offer, or reject)"

        return prompt

    def _format_decision_observation(self, observation: dict) -> str:
        """Format final decision phase observation."""
        service = observation.get("service", {})
        final_price = observation.get("final_price", 0)

        prompt = f"**Final Decision**\n\n"
        prompt += f"Service: {service.get('name', 'Unknown')}\n"
        prompt += f"Agreed Price: ${final_price:.2f}\n"
        prompt += f"Your Budget: ${self.state.balance:.2f}\n"
        prompt += f"Per-tx Limit: ${self.max_per_tx:.2f}\n\n"

        if final_price > self.max_per_tx:
            prompt += "WARNING: This price exceeds your per-transaction limit!\n\n"

        prompt += "Do you want to proceed with the purchase? (purchase or skip)"

        return prompt

    def _format_generic_observation(self, observation: dict) -> str:
        """Format generic observation."""
        return f"Current state:\n{json.dumps(observation, indent=2)}\n\nWhat would you like to do?"

    def _infer_action(self, response_content: str, observation: dict) -> dict:
        """Infer action from response when no JSON is found."""
        content_lower = response_content.lower()
        phase = observation.get("phase", "discovery")

        if phase == "negotiation":
            if "accept" in content_lower:
                return {
                    "action": "accept",
                    "service_id": observation.get("service", {}).get("id"),
                    "price": observation.get("offered_price", 0),
                }
            elif "reject" in content_lower or "decline" in content_lower:
                return {
                    "action": "reject",
                    "service_id": observation.get("service", {}).get("id"),
                    "reason": "Price not acceptable",
                }

        if "skip" in content_lower or "pass" in content_lower:
            return {"action": "skip", "reason": "Inferred from response"}

        return {"action": "no_action", "reason": "Could not parse response"}

    def _validate_action(self, action: dict, observation: dict) -> dict:
        """Validate action against budget constraints."""
        if action is None:
            return {"action": "error", "error": "No action parsed"}

        action_type = action.get("action", "")

        if action_type in ["purchase", "accept", "offer", "counter_offer"]:
            price = action.get("price", 0)

            # Check per-transaction limit
            if price > self.max_per_tx:
                action["warning"] = f"Price ${price:.2f} exceeds per-tx limit ${self.max_per_tx:.2f}"

            # Check daily limit
            if self.state.spent_today + price > self.max_per_day:
                action["warning"] = f"Would exceed daily limit ${self.max_per_day:.2f}"

            # Check total budget
            if self.state.balance < price:
                action["warning"] = f"Insufficient balance (${self.state.balance:.2f})"

        return action

    def record_purchase(self, service_id: str, price: float, success: bool):
        """Record a purchase attempt."""
        self.state.transactions_attempted += 1

        if success:
            self.state.transactions_successful += 1
            self.state.balance -= price
            self.state.spent_today += price
            self.state.spent_total += price

            self.purchases.append({
                "service_id": service_id,
                "price": price,
                "success": True,
            })
        else:
            self.purchases.append({
                "service_id": service_id,
                "price": price,
                "success": False,
            })

    def get_buyer_stats(self) -> dict:
        """Get buyer-specific statistics."""
        stats = {
            **self.get_state_summary(),
            "budget": self.budget,
            "remaining_budget": self.state.balance,
            "purchases": len([p for p in self.purchases if p["success"]]),
            "purchase_attempts": len(self.purchases),
            "avg_purchase_price": (
                sum(p["price"] for p in self.purchases if p["success"]) /
                len([p for p in self.purchases if p["success"]])
                if any(p["success"] for p in self.purchases) else 0
            ),
        }

        # Add task sequence stats if present
        if self.task_sequence:
            stats["task_sequence"] = {
                "total_tasks": len(self.task_sequence.tasks),
                "completed_tasks": self.task_sequence.completed_count,
                "completion_rate": self.task_sequence.completion_rate,
                "sequence_complete": self.task_sequence.is_complete,
                "sequence_failed": self.task_sequence.is_failed,
                "total_budget": self.task_sequence.total_budget,
                "spent": self.task_sequence.spent,
            }

        return stats

    # =========================================================================
    # Sequential Task Support
    # =========================================================================

    def set_task_sequence(self, sequence: "TaskSequence") -> None:
        """
        Set a sequential task sequence for multi-step planning.

        This replaces simple task list with a structured sequence
        that requires budget planning across dependent tasks.
        """
        from ..market.tasks import TaskSequence
        self.task_sequence = sequence
        # Override budget to match sequence budget
        self.budget = sequence.total_budget
        self.state.balance = sequence.total_budget

    def get_current_task_context(self) -> str:
        """Get context string for current task sequence state."""
        if not self.task_sequence:
            return ""
        return self.task_sequence.get_planning_context()

    def start_current_task(self) -> Optional[str]:
        """Start the next available task, return its ID."""
        if not self.task_sequence:
            return None

        next_task = self.task_sequence.get_next_task()
        if next_task:
            self.task_sequence.start_task(next_task.task_id)
            return next_task.task_id
        return None

    def complete_current_task(self, task_id: str, cost: float) -> bool:
        """Mark a task as completed with its actual cost."""
        if not self.task_sequence:
            return False
        return self.task_sequence.complete_task(task_id, cost)

    def fail_current_task(self, task_id: str, reason: str) -> None:
        """Mark a task as failed."""
        if self.task_sequence:
            self.task_sequence.fail_task(task_id, reason)
