"""
Abstract base class for LLM providers.

Defines the interface that all LLM providers must implement.
"""

from abc import ABC, abstractmethod
from dataclasses import dataclass, field
from typing import Optional


@dataclass
class LLMResponse:
    """Response from an LLM provider."""

    content: str
    model: str
    provider: str

    # Token counts
    input_tokens: int = 0
    output_tokens: int = 0

    # Metadata
    finish_reason: str = ""
    latency_ms: float = 0.0

    # Cost (computed by provider based on its pricing)
    cost_usd: float = 0.0

    # Raw response for debugging
    raw_response: Optional[dict] = None

    # Error handling
    error: Optional[str] = None
    success: bool = True


@dataclass
class Message:
    """A single message in a conversation."""

    role: str  # "system", "user", "assistant"
    content: str

    def to_dict(self) -> dict:
        return {"role": self.role, "content": self.content}


class LLMProvider(ABC):
    """
    Abstract base class for LLM providers.

    Implementations must provide:
    - complete(): Send a request and get a response
    - cost_per_1k_input: Pricing for input tokens
    - cost_per_1k_output: Pricing for output tokens
    """

    def __init__(
        self,
        model: str,
        temperature: float = 0.7,
        max_tokens: int = 1024,
        cost_per_1k_input: float = 0.0,
        cost_per_1k_output: float = 0.0,
    ):
        """
        Initialize the provider.

        Args:
            model: Model identifier
            temperature: Sampling temperature
            max_tokens: Maximum tokens in response
            cost_per_1k_input: Cost per 1000 input tokens
            cost_per_1k_output: Cost per 1000 output tokens
        """
        self.model = model
        self.temperature = temperature
        self.max_tokens = max_tokens
        self._cost_per_1k_input = cost_per_1k_input
        self._cost_per_1k_output = cost_per_1k_output

    @property
    def cost_per_1k_input(self) -> float:
        """Cost per 1000 input tokens in USD."""
        return self._cost_per_1k_input

    @property
    def cost_per_1k_output(self) -> float:
        """Cost per 1000 output tokens in USD."""
        return self._cost_per_1k_output

    @property
    @abstractmethod
    def provider_name(self) -> str:
        """Name of this provider (e.g., 'openai', 'anthropic')."""
        pass

    @abstractmethod
    def complete(
        self,
        system: str,
        messages: list[Message],
        **kwargs,
    ) -> LLMResponse:
        """
        Send a completion request to the LLM.

        Args:
            system: System prompt
            messages: Conversation history (list of Message objects)
            **kwargs: Provider-specific parameters

        Returns:
            LLMResponse with the completion
        """
        pass

    def compute_cost(self, input_tokens: int, output_tokens: int) -> float:
        """Compute cost for given token counts."""
        input_cost = (input_tokens / 1000) * self.cost_per_1k_input
        output_cost = (output_tokens / 1000) * self.cost_per_1k_output
        return input_cost + output_cost

    def chat(self, system: str, user_message: str, **kwargs) -> LLMResponse:
        """
        Convenience method for single-turn chat.

        Args:
            system: System prompt
            user_message: User message
            **kwargs: Additional parameters

        Returns:
            LLMResponse with the completion
        """
        messages = [Message(role="user", content=user_message)]
        return self.complete(system, messages, **kwargs)
