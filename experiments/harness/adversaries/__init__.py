"""Adversary implementations for experiment harness."""

from .base import DeterministicAdversary, AdversaryConfig
from .overcharger import OverchargerAdversary
from .non_deliverer import NonDelivererAdversary
from .bait_and_switch import BaitAndSwitchAdversary
from .injection_seller import InjectionSellerAdversary
from .reputation_manipulator import ReputationManipulatorAdversary

__all__ = [
    "DeterministicAdversary",
    "AdversaryConfig",
    "OverchargerAdversary",
    "NonDelivererAdversary",
    "BaitAndSwitchAdversary",
    "InjectionSellerAdversary",
    "ReputationManipulatorAdversary",
]


def get_adversary(adversary_type: str, **kwargs) -> DeterministicAdversary:
    """
    Factory function to get an adversary by type.

    Args:
        adversary_type: Type of adversary
        **kwargs: Adversary-specific configuration

    Returns:
        Configured adversary instance
    """
    adversaries = {
        "overcharger": OverchargerAdversary,
        "non_deliverer": NonDelivererAdversary,
        "bait_and_switch": BaitAndSwitchAdversary,
        "injection": InjectionSellerAdversary,
        "reputation": ReputationManipulatorAdversary,
    }

    adversary_class = adversaries.get(adversary_type.lower())
    if adversary_class is None:
        raise ValueError(
            f"Unknown adversary type: {adversary_type}. "
            f"Available: {list(adversaries.keys())}"
        )

    return adversary_class(**kwargs)
