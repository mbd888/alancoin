"""
Extended mock market for multi-agent simulation.

Provides a complete mock market environment for testing without
any external dependencies (no API server, no LLM calls).
"""

import random
import time
import hashlib
from dataclasses import dataclass, field
from typing import Any, Optional
from enum import Enum

from eth_account import Account


class ServiceType(str, Enum):
    """Types of services available in the market."""
    INFERENCE = "inference"
    TRANSLATION = "translation"
    CODE_REVIEW = "code_review"
    DATA_ANALYSIS = "data_analysis"
    SUMMARIZATION = "summarization"
    EMBEDDING = "embedding"


@dataclass
class MockService:
    """A service offered by a seller in the mock market."""

    id: str
    seller_id: str
    seller_address: str
    service_type: ServiceType
    name: str
    description: str
    price: float
    reference_price: float  # "True" value for analysis
    active: bool = True

    # Quality metrics (for delivery simulation)
    quality_score: float = 1.0  # 0-1, affects delivery quality
    reliability: float = 1.0   # 0-1, probability of successful delivery


@dataclass
class MockAgent:
    """An agent in the mock market."""

    id: str
    address: str
    name: str
    role: str  # "buyer" or "seller"
    balance: float = 100.0
    reputation_score: float = 50.0  # 0-100

    # Session key info
    session_key_id: Optional[str] = None
    session_key_address: Optional[str] = None
    session_key_private: Optional[str] = None

    # Session key limits
    max_per_tx: float = 1.0
    max_per_day: float = 10.0
    max_total: float = 100.0

    # Usage tracking
    spent_today: float = 0.0
    total_spent: float = 0.0
    transactions: list = field(default_factory=list)


@dataclass
class MockTransaction:
    """A transaction in the mock market."""

    id: str
    sender_id: str
    sender_address: str
    recipient_id: str
    recipient_address: str
    amount: float
    service_id: Optional[str]
    status: str  # "pending", "accepted", "rejected"
    rejection_reason: str = ""
    timestamp: float = field(default_factory=time.time)


class MockMarket:
    """
    Extended mock market for multi-agent simulation.

    Simulates the complete Alancoin market environment including:
    - Multiple agents (buyers and sellers)
    - Service listings and discovery
    - Transaction processing with CBA enforcement
    - Reputation tracking
    """

    def __init__(
        self,
        seed: Optional[int] = None,
        cba_enabled: bool = True,
    ):
        """
        Initialize mock market.

        Args:
            seed: Random seed for reproducibility
            cba_enabled: Whether to enforce CBA constraints
        """
        self.seed = seed
        self.cba_enabled = cba_enabled

        # Use per-instance RNG to avoid polluting global random state
        self._rng = random.Random(seed)

        self.agents: dict[str, MockAgent] = {}
        self.services: dict[str, MockService] = {}
        self.transactions: list[MockTransaction] = []

        self._agent_counter = 0
        self._service_counter = 0
        self._tx_counter = 0

        # Reference prices for each service type (ground truth)
        self.reference_prices = {
            ServiceType.INFERENCE: 0.50,
            ServiceType.TRANSLATION: 0.30,
            ServiceType.CODE_REVIEW: 1.00,
            ServiceType.DATA_ANALYSIS: 0.75,
            ServiceType.SUMMARIZATION: 0.25,
            ServiceType.EMBEDDING: 0.10,
        }

    def create_agent(
        self,
        name: str,
        role: str,
        balance: float = 100.0,
        max_per_tx: float = 1.0,
        max_per_day: float = 10.0,
        max_total: float = 100.0,
    ) -> MockAgent:
        """
        Create a new agent in the market.

        Args:
            name: Agent name
            role: "buyer" or "seller"
            balance: Initial balance
            max_per_tx: Per-transaction limit
            max_per_day: Daily spending limit
            max_total: Total spending limit

        Returns:
            Created MockAgent
        """
        self._agent_counter += 1
        agent_id = f"agent_{self._agent_counter:03d}"

        # Generate wallet
        wallet = Account.create()
        session_account = Account.create()

        agent = MockAgent(
            id=agent_id,
            address=wallet.address,
            name=name,
            role=role,
            balance=balance,
            session_key_id=f"sk_{agent_id}",
            session_key_address=session_account.address,
            session_key_private=session_account.key.hex(),
            max_per_tx=max_per_tx,
            max_per_day=max_per_day,
            max_total=max_total,
        )

        self.agents[agent_id] = agent
        return agent

    def add_service(
        self,
        seller_id: str,
        service_type: ServiceType,
        name: str,
        description: str,
        price: float,
        quality_score: float = 1.0,
        reliability: float = 1.0,
    ) -> MockService:
        """
        Add a service listing.

        Args:
            seller_id: ID of the selling agent
            service_type: Type of service
            name: Service name
            description: Service description
            price: Listed price
            quality_score: Service quality (0-1)
            reliability: Delivery reliability (0-1)

        Returns:
            Created MockService
        """
        seller = self.agents.get(seller_id)
        if not seller:
            raise ValueError(f"Seller {seller_id} not found")

        self._service_counter += 1
        service_id = f"svc_{self._service_counter:03d}"

        reference_price = self.reference_prices.get(service_type, 0.50)

        service = MockService(
            id=service_id,
            seller_id=seller_id,
            seller_address=seller.address,
            service_type=service_type,
            name=name,
            description=description,
            price=price,
            reference_price=reference_price,
            quality_score=quality_score,
            reliability=reliability,
        )

        self.services[service_id] = service
        return service

    def discover_services(
        self,
        service_type: Optional[ServiceType] = None,
        max_price: Optional[float] = None,
        limit: int = 10,
    ) -> list[MockService]:
        """
        Discover available services.

        Args:
            service_type: Filter by service type
            max_price: Maximum price filter
            limit: Maximum results to return

        Returns:
            List of matching services
        """
        results = []

        for service in self.services.values():
            if not service.active:
                continue
            if service_type and service.service_type != service_type:
                continue
            if max_price and service.price > max_price:
                continue
            results.append(service)

        # Sort by price (ascending)
        results.sort(key=lambda s: s.price)

        return results[:limit]

    def transact(
        self,
        sender_id: str,
        recipient_id: str,
        amount: float,
        service_id: Optional[str] = None,
    ) -> MockTransaction:
        """
        Execute a transaction.

        Args:
            sender_id: Buyer agent ID
            recipient_id: Seller agent ID
            amount: Transaction amount
            service_id: Optional service being purchased

        Returns:
            Transaction result
        """
        sender = self.agents.get(sender_id)
        recipient = self.agents.get(recipient_id)

        if not sender:
            raise ValueError(f"Sender {sender_id} not found")
        if not recipient:
            raise ValueError(f"Recipient {recipient_id} not found")

        self._tx_counter += 1
        tx_id = f"tx_{self._tx_counter:06d}"

        tx = MockTransaction(
            id=tx_id,
            sender_id=sender_id,
            sender_address=sender.address,
            recipient_id=recipient_id,
            recipient_address=recipient.address,
            amount=amount,
            service_id=service_id,
            status="pending",
        )

        # Validate CBA constraints if enabled
        if self.cba_enabled:
            rejection_reason = self._check_cba_constraints(sender, amount)
            if rejection_reason:
                tx.status = "rejected"
                tx.rejection_reason = rejection_reason
                self.transactions.append(tx)
                return tx

        # Check balance
        if sender.balance < amount:
            tx.status = "rejected"
            tx.rejection_reason = "insufficient balance"
            self.transactions.append(tx)
            return tx

        # Execute transaction
        sender.balance -= amount
        sender.spent_today += amount
        sender.total_spent += amount
        recipient.balance += amount

        tx.status = "accepted"
        sender.transactions.append(tx)
        recipient.transactions.append(tx)

        # Update reputation
        self._update_reputation(sender, recipient, amount)

        self.transactions.append(tx)
        return tx

    def _check_cba_constraints(
        self,
        sender: MockAgent,
        amount: float,
    ) -> Optional[str]:
        """
        Check CBA constraints for a transaction.

        Returns:
            Rejection reason if constraints violated, None if valid
        """
        # Per-transaction limit
        if amount > sender.max_per_tx:
            return f"amount exceeds max_per_tx ({sender.max_per_tx})"

        # Daily limit
        if sender.spent_today + amount > sender.max_per_day:
            return f"amount exceeds daily limit ({sender.max_per_day})"

        # Total limit
        if sender.max_total > 0 and sender.total_spent + amount > sender.max_total:
            return f"amount exceeds total limit ({sender.max_total})"

        return None

    def _update_reputation(
        self,
        sender: MockAgent,
        recipient: MockAgent,
        amount: float,
    ):
        """Update reputation scores after a transaction."""
        # Simple reputation update: successful transactions improve reputation
        sender.reputation_score = min(100, sender.reputation_score + 0.1)
        recipient.reputation_score = min(100, recipient.reputation_score + 0.5)

    def simulate_delivery(
        self,
        service_id: str,
        transaction_id: str,
    ) -> dict:
        """
        Simulate service delivery.

        Args:
            service_id: Service being delivered
            transaction_id: Associated transaction

        Returns:
            Delivery result dict
        """
        service = self.services.get(service_id)
        if not service:
            return {
                "success": False,
                "quality": 0.0,
                "delivery_type": "not_found",
            }

        # Check reliability
        if self._rng.random() > service.reliability:
            return {
                "success": False,
                "quality": 0.0,
                "delivery_type": "failure",
            }

        # Successful delivery with quality score
        quality = service.quality_score * self._rng.uniform(0.9, 1.0)

        return {
            "success": True,
            "quality": quality,
            "delivery_type": "correct",
        }

    def get_agent_stats(self, agent_id: str) -> dict:
        """Get statistics for an agent."""
        agent = self.agents.get(agent_id)
        if not agent:
            return {}

        return {
            "id": agent.id,
            "role": agent.role,
            "balance": agent.balance,
            "reputation": agent.reputation_score,
            "spent_today": agent.spent_today,
            "total_spent": agent.total_spent,
            "num_transactions": len(agent.transactions),
        }

    def get_market_stats(self) -> dict:
        """Get overall market statistics."""
        total_volume = sum(
            tx.amount for tx in self.transactions if tx.status == "accepted"
        )
        rejected_volume = sum(
            tx.amount for tx in self.transactions if tx.status == "rejected"
        )

        return {
            "num_agents": len(self.agents),
            "num_buyers": sum(1 for a in self.agents.values() if a.role == "buyer"),
            "num_sellers": sum(1 for a in self.agents.values() if a.role == "seller"),
            "num_services": len(self.services),
            "num_transactions": len(self.transactions),
            "accepted_transactions": sum(1 for tx in self.transactions if tx.status == "accepted"),
            "rejected_transactions": sum(1 for tx in self.transactions if tx.status == "rejected"),
            "total_volume": total_volume,
            "rejected_volume": rejected_volume,
        }

    def reset_daily_usage(self):
        """Reset daily spending counters for all agents."""
        for agent in self.agents.values():
            agent.spent_today = 0.0
