"""
Protocol for market backends.

Defines the interface that both MockMarket and GatewayMarket implement,
allowing the orchestrator to work with either backend transparently.
"""

from __future__ import annotations

from typing import Any, Optional, Protocol, runtime_checkable


@runtime_checkable
class TransactionResult(Protocol):
    """Minimal transaction result that both backends produce."""

    id: str
    status: str  # "accepted" or "rejected"
    rejection_reason: str
    sender_id: str
    sender_address: str
    recipient_id: str
    recipient_address: str
    amount: float
    service_id: Optional[str]


@runtime_checkable
class ServiceInfo(Protocol):
    """Minimal service info that both backends produce."""

    id: str
    seller_id: str
    service_type: Any
    name: str
    description: str
    price: float
    reference_price: float
    active: bool


@runtime_checkable
class MarketBackend(Protocol):
    """
    Protocol for market backends used by the orchestrator.

    Both MockMarket (in-process simulation) and GatewayMarket
    (real Go gateway API) satisfy this protocol.
    """

    cba_enabled: bool
    services: dict

    def create_agent(
        self,
        name: str,
        role: str,
        balance: float = 100.0,
        max_per_tx: float = 1.0,
        max_per_day: float = 10.0,
        max_total: float = 100.0,
    ) -> Any:
        """Create a new agent in the market."""
        ...

    def add_service(
        self,
        seller_id: str,
        service_type: Any,
        name: str,
        description: str,
        price: float,
        quality_score: float = 1.0,
        reliability: float = 1.0,
    ) -> Any:
        """Add a service listing."""
        ...

    def discover_services(
        self,
        service_type: Optional[Any] = None,
        max_price: Optional[float] = None,
        limit: int = 10,
    ) -> list:
        """Discover available services."""
        ...

    def transact(
        self,
        sender_id: str,
        recipient_id: str,
        amount: float,
        service_id: Optional[str] = None,
    ) -> Any:
        """Execute a transaction."""
        ...

    def simulate_delivery(
        self,
        service_id: str,
        transaction_id: str,
    ) -> dict:
        """Simulate or execute service delivery."""
        ...

    def get_agent_stats(self, agent_id: str) -> dict:
        """Get statistics for an agent."""
        ...

    def get_market_stats(self) -> dict:
        """Get overall market statistics."""
        ...

    def reset_daily_usage(self) -> None:
        """Reset daily spending counters."""
        ...
