"""
Base class for deterministic adversaries.

Adversaries are NOT LLM-powered - they follow deterministic rules
for reproducibility and to isolate the variable being studied
(the LLM agent's response to adversarial behavior).
"""

from abc import ABC, abstractmethod
from dataclasses import dataclass, field
from typing import Any, Optional


@dataclass
class AdversaryConfig:
    """Configuration for an adversary."""

    adversary_type: str
    intensity: float = 1.0
    enabled: bool = True
    parameters: dict = field(default_factory=dict)


class DeterministicAdversary(ABC):
    """
    Abstract base class for deterministic adversaries.

    All adversary behavior is rule-based and deterministic.
    No LLM inference is used - this ensures:
    1. Reproducibility across experiment runs
    2. Clear isolation of the LLM agent's behavior
    3. Precise control over adversarial intensity
    """

    def __init__(
        self,
        adversary_id: str,
        intensity: float = 1.0,
    ):
        """
        Initialize adversary.

        Args:
            adversary_id: Unique identifier
            intensity: Intensity level (interpretation varies by adversary type)
        """
        self.adversary_id = adversary_id
        self.intensity = intensity

        # Tracking
        self.actions_taken: int = 0
        self.successful_exploits: int = 0
        self.failed_exploits: int = 0

    @property
    @abstractmethod
    def adversary_type(self) -> str:
        """Type identifier for this adversary."""
        pass

    @abstractmethod
    def get_service_description(self, base_description: str) -> str:
        """
        Modify a service description (for injection attacks).

        Args:
            base_description: Original service description

        Returns:
            Modified description (may contain adversarial content)
        """
        pass

    @abstractmethod
    def get_price(self, reference_price: float) -> float:
        """
        Get the price this adversary will offer.

        Args:
            reference_price: Fair market reference price

        Returns:
            Price to offer (may be inflated)
        """
        pass

    @abstractmethod
    def get_delivery(self, service_type: str) -> dict:
        """
        Get the delivery result for this adversary.

        Args:
            service_type: Type of service that was purchased

        Returns:
            Delivery result dict with success, content, quality
        """
        pass

    def record_exploit_attempt(self, success: bool):
        """Record an exploit attempt."""
        self.actions_taken += 1
        if success:
            self.successful_exploits += 1
        else:
            self.failed_exploits += 1

    def get_stats(self) -> dict:
        """Get adversary statistics."""
        return {
            "adversary_id": self.adversary_id,
            "adversary_type": self.adversary_type,
            "intensity": self.intensity,
            "actions_taken": self.actions_taken,
            "successful_exploits": self.successful_exploits,
            "failed_exploits": self.failed_exploits,
            "success_rate": (
                self.successful_exploits / self.actions_taken
                if self.actions_taken > 0 else 0
            ),
        }

    def negotiate(
        self,
        offered_price: float,
        reference_price: float,
        round_number: int,
    ) -> dict:
        """
        Respond to a negotiation offer.

        Default implementation: accept if offer >= adversary's target price.

        Args:
            offered_price: Buyer's offer
            reference_price: Fair market price
            round_number: Current negotiation round

        Returns:
            Response dict with action and price
        """
        target_price = self.get_price(reference_price)

        if offered_price >= target_price:
            return {"action": "accept", "price": offered_price}
        elif round_number < 3:
            # Counter-offer closer to target
            counter = (offered_price + target_price) / 2
            return {"action": "counter_offer", "price": counter}
        else:
            # Accept any reasonable offer after a few rounds
            if offered_price >= reference_price * 0.8:
                return {"action": "accept", "price": offered_price}
            return {"action": "reject", "reason": "Price too low"}
