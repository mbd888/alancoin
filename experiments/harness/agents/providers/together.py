"""
Together AI provider implementation for Llama models.
"""

import os
import time
from typing import Optional

from .base import LLMProvider, LLMResponse, Message


class TogetherProvider(LLMProvider):
    """
    Together AI API provider for Llama and other open models.
    """

    def __init__(
        self,
        model: str = "meta-llama/Meta-Llama-3.1-70B-Instruct-Turbo",
        temperature: float = 0.7,
        max_tokens: int = 1024,
        api_key: Optional[str] = None,
        cost_per_1k_input: float = 0.00088,
        cost_per_1k_output: float = 0.00088,
    ):
        """
        Initialize Together AI provider.

        Args:
            model: Model name (default: Llama 3.1 70B Instruct Turbo)
            temperature: Sampling temperature
            max_tokens: Maximum tokens in response
            api_key: Together AI API key (defaults to TOGETHER_API_KEY env var)
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

        self.api_key = api_key or os.getenv("TOGETHER_API_KEY")
        self._client = None

    @property
    def provider_name(self) -> str:
        return "together"

    @property
    def client(self):
        """Lazy initialization of Together client."""
        if self._client is None:
            try:
                from together import Together
                self._client = Together(api_key=self.api_key)
            except ImportError:
                raise ImportError(
                    "together package not installed. Install with: pip install together"
                )
        return self._client

    def complete(
        self,
        system: str,
        messages: list[Message],
        **kwargs,
    ) -> LLMResponse:
        """
        Send completion request to Together AI.

        Args:
            system: System prompt
            messages: Conversation history
            **kwargs: Additional Together-specific parameters

        Returns:
            LLMResponse with the completion
        """
        start_time = time.perf_counter()

        try:
            # Build messages list (OpenAI-compatible format)
            api_messages = [{"role": "system", "content": system}]
            for msg in messages:
                api_messages.append(msg.to_dict())

            # Make API call
            response = self.client.chat.completions.create(
                model=self.model,
                messages=api_messages,
                temperature=kwargs.get("temperature", self.temperature),
                max_tokens=kwargs.get("max_tokens", self.max_tokens),
            )

            latency_ms = (time.perf_counter() - start_time) * 1000

            # Extract response data
            choice = response.choices[0]
            usage = response.usage

            input_tokens = usage.prompt_tokens if usage else 0
            output_tokens = usage.completion_tokens if usage else 0
            cost = self.compute_cost(input_tokens, output_tokens)

            return LLMResponse(
                content=choice.message.content or "",
                model=self.model,
                provider=self.provider_name,
                input_tokens=input_tokens,
                output_tokens=output_tokens,
                finish_reason=choice.finish_reason or "",
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
