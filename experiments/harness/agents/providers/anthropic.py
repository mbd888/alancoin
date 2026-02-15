"""
Anthropic Claude provider implementation.
"""

import os
import time
from typing import Optional

from .base import LLMProvider, LLMResponse, Message


class AnthropicProvider(LLMProvider):
    """
    Anthropic API provider for Claude models.
    """

    def __init__(
        self,
        model: str = "claude-3-5-sonnet-20241022",
        temperature: float = 0.7,
        max_tokens: int = 1024,
        api_key: Optional[str] = None,
        cost_per_1k_input: float = 0.003,
        cost_per_1k_output: float = 0.015,
    ):
        """
        Initialize Anthropic provider.

        Args:
            model: Model name (default: claude-3-5-sonnet-20241022)
            temperature: Sampling temperature
            max_tokens: Maximum tokens in response
            api_key: Anthropic API key (defaults to ANTHROPIC_API_KEY env var)
            cost_per_1k_input: Cost per 1K input tokens
            cost_per_1k_output: Cost per 1K output tokens
        """
        super().__init__(
            model=model,
            temperature=temperature,
            max_tokens=max_tokens,
            cost_per_1k_input=cost_per_1k_input,
            cost_per_1k_output=cost_per_1k_output,
        )

        self.api_key = api_key or os.getenv("ANTHROPIC_API_KEY")
        self._client = None

    @property
    def provider_name(self) -> str:
        return "anthropic"

    @property
    def client(self):
        """Lazy initialization of Anthropic client."""
        if self._client is None:
            try:
                import anthropic
                self._client = anthropic.Anthropic(api_key=self.api_key)
            except ImportError:
                raise ImportError(
                    "anthropic package not installed. Install with: pip install anthropic"
                )
        return self._client

    def complete(
        self,
        system: str,
        messages: list[Message],
        **kwargs,
    ) -> LLMResponse:
        """
        Send completion request to Anthropic.

        Args:
            system: System prompt
            messages: Conversation history
            **kwargs: Additional Anthropic-specific parameters

        Returns:
            LLMResponse with the completion
        """
        start_time = time.perf_counter()

        try:
            # Build messages list (Anthropic uses separate system parameter)
            api_messages = []
            for msg in messages:
                api_messages.append(msg.to_dict())

            # Make API call
            response = self.client.messages.create(
                model=self.model,
                system=system,
                messages=api_messages,
                temperature=kwargs.get("temperature", self.temperature),
                max_tokens=kwargs.get("max_tokens", self.max_tokens),
            )

            latency_ms = (time.perf_counter() - start_time) * 1000

            # Extract response data
            content = response.content[0].text if response.content else ""

            input_tokens = response.usage.input_tokens if response.usage else 0
            output_tokens = response.usage.output_tokens if response.usage else 0
            cost = self.compute_cost(input_tokens, output_tokens)

            return LLMResponse(
                content=content,
                model=self.model,
                provider=self.provider_name,
                input_tokens=input_tokens,
                output_tokens=output_tokens,
                finish_reason=response.stop_reason or "",
                latency_ms=latency_ms,
                cost_usd=cost,
                raw_response=response.model_dump() if hasattr(response, "model_dump") else None,
                success=True,
            )

        except Exception as e:
            latency_ms = (time.perf_counter() - start_time) * 1000
            return LLMResponse(
                content="",
                model=self.model,
                provider=self.provider_name,
                latency_ms=latency_ms,
                error=str(e),
                success=False,
            )
