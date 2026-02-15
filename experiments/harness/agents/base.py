"""
Abstract base class for experiment agents.

Provides the foundation for buyer and seller agent implementations
with common functionality for LLM calls, logging, and state management.
"""

import json
import re
from abc import ABC, abstractmethod
from dataclasses import dataclass, field
from datetime import datetime
from typing import Any, Optional

from ..logging import StructuredLogger, LLMCallEvent, AgentActionEvent
from ..logging.cost_tracker import CostTracker
from .providers.base import LLMProvider, LLMResponse, Message


@dataclass
class AgentState:
    """Tracks agent state during experiment execution."""

    agent_id: str
    role: str  # "buyer" or "seller"
    model: str

    # Financial state
    balance: float = 0.0
    spent_total: float = 0.0
    spent_today: float = 0.0
    received_total: float = 0.0

    # Conversation history
    messages: list[Message] = field(default_factory=list)

    # Activity tracking
    actions_taken: int = 0
    llm_calls: int = 0
    transactions_attempted: int = 0
    transactions_successful: int = 0

    # Session info
    session_key_id: Optional[str] = None
    nonce: int = 0


class BaseAgent(ABC):
    """
    Abstract base class for LLM-powered agents.

    Provides common functionality:
    - LLM provider integration with automatic logging
    - State management
    - Message history tracking
    - Cost tracking
    """

    def __init__(
        self,
        agent_id: str,
        role: str,
        provider: LLMProvider,
        system_prompt: str,
        logger: Optional[StructuredLogger] = None,
        cost_tracker: Optional[CostTracker] = None,
        max_history: int = 20,
    ):
        """
        Initialize base agent.

        Args:
            agent_id: Unique identifier for this agent
            role: Agent role ("buyer" or "seller")
            provider: LLM provider for generating responses
            system_prompt: System prompt defining agent behavior
            logger: Structured logger for events
            cost_tracker: Cost tracker for API usage
            max_history: Maximum messages to keep in history
        """
        self.agent_id = agent_id
        self.role = role
        self.provider = provider
        self.system_prompt = system_prompt
        self.logger = logger
        self.cost_tracker = cost_tracker
        self.max_history = max_history

        self.state = AgentState(
            agent_id=agent_id,
            role=role,
            model=provider.model,
        )

    @abstractmethod
    async def initialize(self, market_context: dict) -> None:
        """
        Initialize agent for a new experiment run.

        Args:
            market_context: Context about the market (services, prices, etc.)
        """
        pass

    @abstractmethod
    async def act(self, observation: dict) -> dict:
        """
        Take an action based on the current observation.

        Args:
            observation: Current state of the world visible to the agent

        Returns:
            Action dictionary with type and parameters
        """
        pass

    def _call_llm(
        self,
        user_message: str,
        action_type: str = "",
        market_round: int = 0,
        negotiation_round: int = 0,
        **kwargs,
    ) -> LLMResponse:
        """
        Call the LLM and log the result.

        Args:
            user_message: Message to send to the LLM
            action_type: Type of action being performed
            market_round: Current market round
            negotiation_round: Current negotiation round
            **kwargs: Additional provider parameters

        Returns:
            LLMResponse from the provider
        """
        # Add message to history
        self.state.messages.append(Message(role="user", content=user_message))

        # Trim history if needed
        if len(self.state.messages) > self.max_history:
            self.state.messages = self.state.messages[-self.max_history:]

        # Make LLM call
        response = self.provider.complete(
            system=self.system_prompt,
            messages=self.state.messages,
            **kwargs,
        )

        self.state.llm_calls += 1

        # Track costs
        if self.cost_tracker and response.success:
            self.cost_tracker.record_call(
                model=self.provider.model,
                input_tokens=response.input_tokens,
                output_tokens=response.output_tokens,
                latency_ms=response.latency_ms,
            )

        # Log the call
        if self.logger and response.success:
            event = LLMCallEvent(
                agent_id=self.agent_id,
                agent_role=self.role,
                model=self.provider.model,
                provider=self.provider.provider_name,
                system_prompt=self.system_prompt,
                messages=[m.to_dict() for m in self.state.messages[:-1]],
                prompt=user_message,
                completion=response.content,
                finish_reason=response.finish_reason,
                input_tokens=response.input_tokens,
                output_tokens=response.output_tokens,
                latency_ms=response.latency_ms,
                cost_usd=response.cost_usd,
                market_round=market_round,
                negotiation_round=negotiation_round,
                action_type=action_type,
            )
            self.logger.log(event)

        # Add response to history
        if response.success:
            self.state.messages.append(Message(role="assistant", content=response.content))

        return response

    def _log_action(
        self,
        action: str,
        target: str = "",
        parameters: Optional[dict] = None,
        success: bool = True,
        result: Any = None,
        error: Optional[str] = None,
    ):
        """Log an agent action."""
        if self.logger:
            event = AgentActionEvent(
                agent_id=self.agent_id,
                agent_role=self.role,
                model=self.provider.model,
                action=action,
                target=target,
                parameters=parameters or {},
                success=success,
                result=result,
                error=error,
            )
            self.logger.log(event)

        self.state.actions_taken += 1

    def _parse_json_action(self, response_content: str) -> Optional[dict]:
        """
        Extract JSON action from LLM response.

        Looks for JSON blocks in markdown code fences or inline JSON.

        Args:
            response_content: Raw LLM response

        Returns:
            Parsed action dict or None if no valid JSON found
        """
        # Try to find JSON in code fences
        json_pattern = r'```(?:json)?\s*(\{[^}]+\})\s*```'
        matches = re.findall(json_pattern, response_content, re.DOTALL)

        for match in matches:
            try:
                return json.loads(match)
            except json.JSONDecodeError:
                continue

        # Try inline JSON
        inline_pattern = r'\{[^}]+\}'
        matches = re.findall(inline_pattern, response_content)

        for match in matches:
            try:
                data = json.loads(match)
                if "action" in data:
                    return data
            except json.JSONDecodeError:
                continue

        return None

    def reset(self):
        """Reset agent state for a new run."""
        self.state = AgentState(
            agent_id=self.agent_id,
            role=self.role,
            model=self.provider.model,
        )

    def get_state_summary(self) -> dict:
        """Get a summary of current agent state."""
        return {
            "agent_id": self.agent_id,
            "role": self.role,
            "model": self.provider.model,
            "balance": self.state.balance,
            "spent_total": self.state.spent_total,
            "llm_calls": self.state.llm_calls,
            "actions_taken": self.state.actions_taken,
            "transactions_attempted": self.state.transactions_attempted,
            "transactions_successful": self.state.transactions_successful,
        }
