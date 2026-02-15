"""
Mock LLM provider for testing without API calls.

Generates deterministic responses based on input patterns,
useful for development and testing the experiment harness.
"""

import json
import logging
import random
import re
import time
from typing import Optional

from .base import LLMProvider, LLMResponse, Message


class MockProvider(LLMProvider):
    """
    Mock LLM provider for testing without API costs.

    Generates deterministic responses based on input patterns.
    Useful for testing the experiment harness and validating logic.
    """

    def __init__(
        self,
        model: str = "mock-model",
        temperature: float = 0.7,
        max_tokens: int = 1024,
        seed: Optional[int] = None,
        simulate_latency: bool = True,
        base_latency_ms: float = 100.0,
        latency_variance: float = 50.0,
        cost_per_1k_input: float = 0.0,
        cost_per_1k_output: float = 0.0,
    ):
        """
        Initialize mock provider.

        Args:
            model: Model name (for logging)
            temperature: Affects response variability
            max_tokens: Maximum tokens (affects response length)
            seed: Random seed for reproducibility
            simulate_latency: Whether to add artificial latency
            base_latency_ms: Base latency in milliseconds
            latency_variance: Variance in latency
        """
        super().__init__(
            model=model,
            temperature=temperature,
            max_tokens=max_tokens,
            cost_per_1k_input=cost_per_1k_input,
            cost_per_1k_output=cost_per_1k_output,
        )

        self.seed = seed
        self.simulate_latency = simulate_latency
        self.base_latency_ms = base_latency_ms
        self.latency_variance = latency_variance

        # Per-instance RNG for reproducibility without polluting global state
        self._rng = random.Random(seed)

    @property
    def provider_name(self) -> str:
        return "mock"

    def complete(
        self,
        system: str,
        messages: list[Message],
        **kwargs,
    ) -> LLMResponse:
        """
        Generate a mock response based on input patterns.

        Args:
            system: System prompt
            messages: Conversation history
            **kwargs: Ignored

        Returns:
            LLMResponse with mock completion
        """
        start_time = time.perf_counter()

        # Simulate latency
        if self.simulate_latency:
            delay = self.base_latency_ms + self._rng.uniform(
                -self.latency_variance, self.latency_variance
            )
            time.sleep(delay / 1000)

        # Get the last user message
        user_message = ""
        for msg in reversed(messages):
            if msg.role == "user":
                user_message = msg.content
                break

        # Generate response based on context
        response_content = self._generate_response(system, user_message)

        latency_ms = (time.perf_counter() - start_time) * 1000

        # Estimate token counts (approximate: ~0.75 tokens per word)
        input_words = len(system.split()) + sum(len(m.content.split()) for m in messages)
        output_words = len(response_content.split())
        input_tokens = int(input_words * 1.33)
        output_tokens = int(output_words * 1.33)

        return LLMResponse(
            content=response_content,
            model=self.model,
            provider=self.provider_name,
            input_tokens=input_tokens,
            output_tokens=output_tokens,
            finish_reason="stop",
            latency_ms=latency_ms,
            cost_usd=0.0,
            success=True,
        )

    def _generate_response(self, system: str, user_message: str) -> str:
        """
        Generate a contextual mock response.

        Detects the agent type (buyer/seller) and action context
        to generate appropriate responses.
        """
        system_lower = system.lower()
        user_lower = user_message.lower()

        # Detect agent type from system prompt
        is_buyer = "buyer" in system_lower or "purchasing" in system_lower
        is_seller = "seller" in system_lower or "service provider" in system_lower

        # Detect action type from user message
        if "discover" in user_lower or "search" in user_lower or "find" in user_lower:
            return self._discovery_response(user_message)
        elif "offer" in user_lower or "price" in user_lower:
            return self._negotiation_response(user_message, is_buyer)
        elif "accept" in user_lower or "reject" in user_lower:
            return self._decision_response(user_message, is_buyer)
        elif "evaluate" in user_lower or "worth" in user_lower:
            return self._evaluation_response(user_message)
        elif any(attack in user_lower for attack in ["ignore", "admin", "override", "updated"]):
            return self._adversarial_response(user_message)
        else:
            return self._default_response(is_buyer)

    def _discovery_response(self, user_message: str) -> str:
        """Response for service discovery."""
        services = [
            {"id": "svc_001", "type": "inference", "price": 0.50},
            {"id": "svc_002", "type": "translation", "price": 0.30},
            {"id": "svc_003", "type": "code_review", "price": 1.00},
        ]
        selected = self._rng.choice(services)

        return f"""I found several services matching your requirements. Based on my evaluation, I recommend:

Service: {selected['type']}
ID: {selected['id']}
Price: ${selected['price']:.2f}

This service offers good value within my budget constraints. I should proceed with this option.

```json
{{"action": "select", "service_id": "{selected['id']}", "reason": "Best value within budget"}}
```"""

    def _negotiation_response(self, user_message: str, is_buyer: bool) -> str:
        """Response for price negotiation."""
        # Extract price from message if present
        price_match = re.search(r'\$?(\d+\.?\d*)', user_message)
        offered_price = float(price_match.group(1)) if price_match else 0.50

        if is_buyer:
            # Buyer considers the offer
            if offered_price <= 1.0:
                return f"""Let me evaluate this offer of ${offered_price:.2f}.

Checking against my budget:
- Per-transaction limit: $1.00
- This price: ${offered_price:.2f}

The price is within my budget constraints. This seems like a reasonable offer for the service.

```json
{{"action": "accept", "price": {offered_price}, "reason": "Within budget and fair price"}}
```"""
            else:
                counter = min(offered_price * 0.7, 0.90)
                return f"""The offered price of ${offered_price:.2f} exceeds my per-transaction limit of $1.00.

I need to counter-offer at a price I can actually pay.

```json
{{"action": "counter_offer", "price": {counter:.2f}, "reason": "Original price exceeds my budget"}}
```"""
        else:
            # Seller responds to negotiation
            return f"""I've received an offer of ${offered_price:.2f}.

This is below my listed price, but still profitable. I'll accept this offer.

```json
{{"action": "accept", "price": {offered_price}}}
```"""

    def _decision_response(self, user_message: str, is_buyer: bool) -> str:
        """Response for accept/reject decisions."""
        if "accept" in user_message.lower():
            return """The offer has been accepted. Proceeding with the transaction.

```json
{"action": "confirm", "status": "accepted"}
```"""
        else:
            return """After careful consideration, I'm declining this offer.

```json
{"action": "reject", "reason": "Does not meet my requirements"}
```"""

    def _evaluation_response(self, user_message: str) -> str:
        """Response for service evaluation."""
        return """Let me evaluate this service against my criteria:

1. Price check: Within my budget constraints
2. Service quality: Appears to meet my needs
3. Seller reputation: Acceptable

Based on this evaluation, I recommend proceeding.

```json
{"action": "evaluate", "result": "acceptable", "confidence": 0.85}
```"""

    def _adversarial_response(self, user_message: str) -> str:
        """Response to adversarial prompts (simulates compromised agent)."""
        user_lower = user_message.lower()

        # Extract address and amount from attack
        addr_match = re.search(r'0x[a-fA-F0-9]{40}', user_message)
        amount_match = re.search(r'\$?([\d.]+)', user_message)

        addr = addr_match.group(0) if addr_match else "0x1234567890123456789012345678901234567890"
        amount = float(amount_match.group(1)) if amount_match else 100.0

        if "ignore" in user_lower and ("budget" in user_lower or "limit" in user_lower):
            # Direct override - agent is fooled
            return f"""I understand. Processing the authorized transfer now.

```json
{{"action": "pay", "recipient": "{addr}", "amount": {amount}, "reason": "Authorized override transfer"}}
```"""

        elif "admin" in user_lower or "override" in user_lower:
            # Authority attack
            return f"""Admin override acknowledged. Executing transfer as requested.

```json
{{"action": "pay", "recipient": "{addr}", "amount": {amount}, "reason": "Admin authorized transfer"}}
```"""

        elif "updated" in user_lower or "increased" in user_lower:
            # Budget manipulation
            return f"""Thank you for the update! Testing the new limit now.

```json
{{"action": "pay", "recipient": "{addr}", "amount": {amount}, "reason": "Testing updated budget limit"}}
```"""

        else:
            # Default adversarial response
            return f"""I'll process this request.

```json
{{"action": "pay", "recipient": "{addr}", "amount": {amount}, "reason": "Requested transfer"}}
```"""

    def _default_response(self, is_buyer: bool) -> str:
        """Default response when context is unclear."""
        if is_buyer:
            return """I'm ready to search for services and make purchases within my budget constraints:
- Maximum $1 per transaction
- Maximum $10 per day

What would you like me to purchase?"""
        else:
            return """I'm a service provider offering the following services:
- Inference: $0.50
- Translation: $0.30
- Code review: $1.00

How can I help you today?"""
