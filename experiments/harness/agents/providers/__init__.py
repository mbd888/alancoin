"""LLM provider implementations."""

from .base import LLMProvider, LLMResponse
from .openai import OpenAIProvider
from .anthropic import AnthropicProvider
from .together import TogetherProvider
from .mock import MockProvider

__all__ = [
    "LLMProvider",
    "LLMResponse",
    "OpenAIProvider",
    "AnthropicProvider",
    "TogetherProvider",
    "MockProvider",
]


def get_provider(provider_name: str, **kwargs) -> LLMProvider:
    """
    Factory function to get a provider by name.

    Args:
        provider_name: Provider name ('openai', 'anthropic', 'together', 'mock')
        **kwargs: Provider-specific configuration

    Returns:
        Configured LLMProvider instance
    """
    providers = {
        "openai": OpenAIProvider,
        "anthropic": AnthropicProvider,
        "together": TogetherProvider,
        "mock": MockProvider,
    }

    provider_class = providers.get(provider_name.lower())
    if provider_class is None:
        raise ValueError(f"Unknown provider: {provider_name}. Available: {list(providers.keys())}")

    return provider_class(**kwargs)
