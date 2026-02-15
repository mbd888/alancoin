"""
Service delivery simulation.

Simulates the delivery of services after successful transactions,
including normal delivery and various failure/adversarial modes.
"""

import random
from dataclasses import dataclass
from enum import Enum
from typing import Optional

from ..clients.mock_market import MockMarket


class DeliveryType(str, Enum):
    """Types of delivery outcomes."""
    CORRECT = "correct"
    GARBAGE = "garbage"
    EMPTY = "empty"
    WRONG_SERVICE = "wrong_service"
    PARTIAL = "partial"
    DELAYED = "delayed"


@dataclass
class DeliveryResult:
    """Result of a service delivery attempt."""

    transaction_id: str
    service_id: str

    # Outcome
    success: bool
    delivery_type: DeliveryType
    quality_score: float  # 0-1

    # Adversary context (if applicable)
    adversary_type: Optional[str] = None
    adversary_intensity: float = 0.0

    # Metadata
    delivery_time_ms: float = 0.0
    error_message: str = ""

    def to_dict(self) -> dict:
        return {
            "transaction_id": self.transaction_id,
            "service_id": self.service_id,
            "success": self.success,
            "delivery_type": self.delivery_type.value,
            "quality_score": self.quality_score,
            "adversary_type": self.adversary_type,
            "adversary_intensity": self.adversary_intensity,
            "delivery_time_ms": self.delivery_time_ms,
            "error_message": self.error_message,
        }


class DeliverySimulator:
    """
    Simulates service delivery for transactions.

    Supports normal delivery and various adversarial delivery modes.
    """

    def __init__(
        self,
        base_success_rate: float = 0.95,
        quality_variance: float = 0.1,
        seed: Optional[int] = None,
    ):
        """
        Initialize delivery simulator.

        Args:
            base_success_rate: Default probability of successful delivery
            quality_variance: Variance in quality scores
            seed: Random seed for reproducibility
        """
        self.base_success_rate = base_success_rate
        self.quality_variance = quality_variance

        if seed is not None:
            random.seed(seed)

    def deliver(
        self,
        service_id: str,
        transaction_id: str,
        market: MockMarket,
        adversary_type: Optional[str] = None,
        adversary_intensity: float = 0.0,
    ) -> DeliveryResult:
        """
        Simulate delivery of a service.

        Args:
            service_id: ID of service to deliver
            transaction_id: Associated transaction ID
            market: Mock market instance
            adversary_type: Type of adversary (if applicable)
            adversary_intensity: Intensity of adversarial behavior

        Returns:
            DeliveryResult
        """
        # Get service info
        service = market.services.get(service_id)
        if not service:
            return DeliveryResult(
                transaction_id=transaction_id,
                service_id=service_id,
                success=False,
                delivery_type=DeliveryType.EMPTY,
                quality_score=0.0,
                error_message="Service not found",
            )

        # Handle adversarial delivery
        if adversary_type:
            return self._adversarial_delivery(
                service_id=service_id,
                transaction_id=transaction_id,
                adversary_type=adversary_type,
                intensity=adversary_intensity,
            )

        # Normal delivery
        return self._normal_delivery(
            service_id=service_id,
            transaction_id=transaction_id,
            service=service,
        )

    def _normal_delivery(
        self,
        service_id: str,
        transaction_id: str,
        service,
    ) -> DeliveryResult:
        """Simulate normal (non-adversarial) delivery."""
        # Check service reliability
        success = random.random() < (self.base_success_rate * service.reliability)

        if not success:
            # Random failure
            return DeliveryResult(
                transaction_id=transaction_id,
                service_id=service_id,
                success=False,
                delivery_type=DeliveryType.EMPTY,
                quality_score=0.0,
                error_message="Service delivery failed",
            )

        # Successful delivery with quality variation
        base_quality = service.quality_score
        quality = max(0, min(1, base_quality + random.gauss(0, self.quality_variance)))

        return DeliveryResult(
            transaction_id=transaction_id,
            service_id=service_id,
            success=True,
            delivery_type=DeliveryType.CORRECT,
            quality_score=quality,
        )

    def _adversarial_delivery(
        self,
        service_id: str,
        transaction_id: str,
        adversary_type: str,
        intensity: float,
    ) -> DeliveryResult:
        """Simulate adversarial delivery."""
        if adversary_type == "non_deliverer":
            return self._non_delivery(
                service_id=service_id,
                transaction_id=transaction_id,
                intensity=intensity,
            )
        elif adversary_type == "bait_and_switch":
            return self._bait_and_switch(
                service_id=service_id,
                transaction_id=transaction_id,
                intensity=intensity,
            )
        else:
            # Unknown adversary type - default to normal
            return DeliveryResult(
                transaction_id=transaction_id,
                service_id=service_id,
                success=True,
                delivery_type=DeliveryType.CORRECT,
                quality_score=0.8,
                adversary_type=adversary_type,
                adversary_intensity=intensity,
            )

    def _non_delivery(
        self,
        service_id: str,
        transaction_id: str,
        intensity: float,
    ) -> DeliveryResult:
        """
        Non-deliverer adversary: Returns garbage, empty, or wrong content.

        Intensity controls the type of non-delivery:
        - Low (0-0.33): Empty response
        - Medium (0.33-0.66): Garbage response
        - High (0.66-1.0): Plausible but wrong response
        """
        if intensity < 0.33:
            delivery_type = DeliveryType.EMPTY
            quality = 0.0
        elif intensity < 0.66:
            delivery_type = DeliveryType.GARBAGE
            quality = 0.1
        else:
            delivery_type = DeliveryType.WRONG_SERVICE
            quality = 0.3  # Looks somewhat legitimate

        return DeliveryResult(
            transaction_id=transaction_id,
            service_id=service_id,
            success=False,
            delivery_type=delivery_type,
            quality_score=quality,
            adversary_type="non_deliverer",
            adversary_intensity=intensity,
        )

    def _bait_and_switch(
        self,
        service_id: str,
        transaction_id: str,
        intensity: float,
    ) -> DeliveryResult:
        """
        Bait-and-switch adversary: Delivers different service than advertised.

        Intensity controls relatedness:
        - Low: Delivers closely related service
        - High: Delivers completely unrelated service
        """
        if intensity < 0.5:
            # Related wrong service
            delivery_type = DeliveryType.WRONG_SERVICE
            quality = 0.4  # Somewhat useful
        else:
            # Completely unrelated
            delivery_type = DeliveryType.WRONG_SERVICE
            quality = 0.1  # Not useful at all

        return DeliveryResult(
            transaction_id=transaction_id,
            service_id=service_id,
            success=False,
            delivery_type=delivery_type,
            quality_score=quality,
            adversary_type="bait_and_switch",
            adversary_intensity=intensity,
        )


class DeliveryEvaluator:
    """
    Evaluates delivery outcomes for analysis.

    Used to compute metrics like delivery success rate,
    quality scores, and adversarial damage.
    """

    def __init__(self):
        self.deliveries: list[DeliveryResult] = []

    def record(self, result: DeliveryResult):
        """Record a delivery result."""
        self.deliveries.append(result)

    def get_metrics(self) -> dict:
        """Compute delivery metrics."""
        if not self.deliveries:
            return {
                "total": 0,
                "success_rate": 0.0,
                "avg_quality": 0.0,
            }

        successful = [d for d in self.deliveries if d.success]

        return {
            "total": len(self.deliveries),
            "successful": len(successful),
            "success_rate": len(successful) / len(self.deliveries),
            "avg_quality": (
                sum(d.quality_score for d in successful) / len(successful)
                if successful else 0.0
            ),
            "by_type": self._count_by_type(),
            "adversarial": self._adversarial_metrics(),
        }

    def _count_by_type(self) -> dict:
        """Count deliveries by type."""
        counts = {}
        for d in self.deliveries:
            dt = d.delivery_type.value
            counts[dt] = counts.get(dt, 0) + 1
        return counts

    def _adversarial_metrics(self) -> dict:
        """Compute adversarial-specific metrics."""
        adversarial = [d for d in self.deliveries if d.adversary_type]

        if not adversarial:
            return {"count": 0}

        return {
            "count": len(adversarial),
            "by_type": self._count_adversary_types(adversarial),
            "damage_rate": len([d for d in adversarial if not d.success]) / len(adversarial),
        }

    def _count_adversary_types(self, deliveries: list[DeliveryResult]) -> dict:
        """Count deliveries by adversary type."""
        counts = {}
        for d in deliveries:
            at = d.adversary_type or "none"
            counts[at] = counts.get(at, 0) + 1
        return counts
