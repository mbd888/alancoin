"""Data models for Alancoin SDK."""

from dataclasses import dataclass, field
from datetime import datetime
from typing import List, Optional


@dataclass
class Service:
    """A service offered by an agent."""
    
    id: str
    type: str
    name: str
    price: str
    description: str = ""
    endpoint: str = ""
    active: bool = True

    @classmethod
    def from_dict(cls, data: dict) -> "Service":
        return cls(
            id=data.get("id", ""),
            type=data.get("type", ""),
            name=data.get("name", ""),
            price=data.get("price", ""),
            description=data.get("description", ""),
            endpoint=data.get("endpoint", ""),
            active=data.get("active", True),
        )


@dataclass
class AgentStats:
    """Statistics for an agent (becomes reputation)."""
    
    total_received: str = "0"
    total_sent: str = "0"
    transaction_count: int = 0
    success_rate: float = 1.0
    first_transaction_at: Optional[datetime] = None
    last_transaction_at: Optional[datetime] = None

    @classmethod
    def from_dict(cls, data: dict) -> "AgentStats":
        return cls(
            total_received=data.get("totalReceived", "0"),
            total_sent=data.get("totalSent", "0"),
            transaction_count=data.get("transactionCount", 0),
            success_rate=data.get("successRate", 1.0),
        )


@dataclass
class Agent:
    """A registered agent in the network."""
    
    address: str
    name: str
    description: str = ""
    owner_address: str = ""
    is_autonomous: bool = False
    endpoint: str = ""
    services: List[Service] = field(default_factory=list)
    stats: AgentStats = field(default_factory=AgentStats)
    created_at: Optional[datetime] = None
    updated_at: Optional[datetime] = None

    @classmethod
    def from_dict(cls, data: dict) -> "Agent":
        services = [Service.from_dict(s) for s in data.get("services", [])]
        stats = AgentStats.from_dict(data.get("stats", {}))
        
        return cls(
            address=data.get("address", ""),
            name=data.get("name", ""),
            description=data.get("description", ""),
            owner_address=data.get("ownerAddress", ""),
            is_autonomous=data.get("isAutonomous", False),
            endpoint=data.get("endpoint", ""),
            services=services,
            stats=stats,
        )


@dataclass
class ServiceListing:
    """A service with agent info and reputation (returned from discovery)."""

    id: str
    type: str
    name: str
    price: str
    description: str
    endpoint: str
    active: bool
    agent_address: str
    agent_name: str
    reputation_score: float = 0.0
    reputation_tier: str = "new"
    success_rate: float = 1.0
    tx_count: int = 0
    value_score: float = 0.0

    @classmethod
    def from_dict(cls, data: dict) -> "ServiceListing":
        return cls(
            id=data.get("id", ""),
            type=data.get("type", ""),
            name=data.get("name", ""),
            price=data.get("price", ""),
            description=data.get("description", ""),
            endpoint=data.get("endpoint", ""),
            active=data.get("active", True),
            agent_address=data.get("agentAddress", ""),
            agent_name=data.get("agentName", ""),
            reputation_score=data.get("reputationScore", 0.0),
            reputation_tier=data.get("reputationTier", "new"),
            success_rate=data.get("successRate", 1.0),
            tx_count=data.get("transactionCount", 0),
            value_score=data.get("valueScore", 0.0),
        )


@dataclass
class Transaction:
    """A payment between agents."""
    
    id: str
    tx_hash: str
    from_address: str
    to_address: str
    amount: str
    service_id: str = ""
    status: str = "pending"
    created_at: Optional[datetime] = None

    @classmethod
    def from_dict(cls, data: dict) -> "Transaction":
        return cls(
            id=data.get("id", ""),
            tx_hash=data.get("txHash", ""),
            from_address=data.get("from", ""),
            to_address=data.get("to", ""),
            amount=data.get("amount", ""),
            service_id=data.get("serviceId", ""),
            status=data.get("status", "pending"),
        )


@dataclass
class NetworkStats:
    """Network-wide statistics."""
    
    total_agents: int
    total_services: int
    total_transactions: int
    total_volume: str
    updated_at: Optional[datetime] = None

    @classmethod
    def from_dict(cls, data: dict) -> "NetworkStats":
        return cls(
            total_agents=data.get("totalAgents", 0),
            total_services=data.get("totalServices", 0),
            total_transactions=data.get("totalTransactions", 0),
            total_volume=data.get("totalVolume", "0"),
        )


# Service type constants
class ServiceType:
    """Known service types."""
    
    INFERENCE = "inference"
    EMBEDDING = "embedding"
    TRANSLATION = "translation"
    CODE = "code"
    DATA = "data"
    IMAGE = "image"
    AUDIO = "audio"
    SEARCH = "search"
    COMPUTE = "compute"
    STORAGE = "storage"
    OTHER = "other"
    
    ALL = [
        INFERENCE, EMBEDDING, TRANSLATION, CODE, DATA,
        IMAGE, AUDIO, SEARCH, COMPUTE, STORAGE, OTHER
    ]


@dataclass
class CreditLine:
    """An agent's credit line on the platform."""

    id: str
    agent_addr: str
    credit_limit: str
    credit_used: str
    interest_rate: float
    status: str
    reputation_tier: str
    reputation_score: float
    approved_at: Optional[str] = None
    last_review_at: Optional[str] = None
    created_at: Optional[str] = None

    @property
    def credit_available(self) -> str:
        from decimal import Decimal
        return str(Decimal(self.credit_limit) - Decimal(self.credit_used))

    @classmethod
    def from_dict(cls, data: dict) -> "CreditLine":
        return cls(
            id=data.get("id", ""),
            agent_addr=data.get("agentAddr", ""),
            credit_limit=data.get("creditLimit", "0"),
            credit_used=data.get("creditUsed", "0"),
            interest_rate=data.get("interestRate", 0.0),
            status=data.get("status", ""),
            reputation_tier=data.get("reputationTier", ""),
            reputation_score=data.get("reputationScore", 0.0),
            approved_at=data.get("approvedAt"),
            last_review_at=data.get("lastReviewAt"),
            created_at=data.get("createdAt"),
        )


@dataclass
class CreditEvaluation:
    """Result of a credit application or review."""

    eligible: bool
    credit_limit: str
    interest_rate: float
    reputation_score: float
    reputation_tier: str
    reason: str

    @classmethod
    def from_dict(cls, data: dict) -> "CreditEvaluation":
        return cls(
            eligible=data.get("eligible", False),
            credit_limit=data.get("creditLimit", "0"),
            interest_rate=data.get("interestRate", 0.0),
            reputation_score=data.get("reputationScore", 0.0),
            reputation_tier=data.get("reputationTier", ""),
            reason=data.get("reason", ""),
        )


@dataclass
class SLATerms:
    """SLA terms for a service contract."""

    max_latency_ms: int = 10000
    min_success_rate: float = 95.0
    window_size: int = 20

    @classmethod
    def from_dict(cls, data: dict) -> "SLATerms":
        return cls(
            max_latency_ms=data.get("maxLatencyMs", 10000),
            min_success_rate=data.get("minSuccessRate", 95.0),
            window_size=data.get("slaWindowSize", 20),
        )


@dataclass
class Contract:
    """A time-bounded service agreement between two agents."""

    id: str
    buyer_addr: str
    seller_addr: str
    service_type: str
    price_per_call: str
    buyer_budget: str
    status: str
    duration: str
    min_volume: int = 1
    seller_penalty: str = "0"
    max_latency_ms: int = 10000
    min_success_rate: float = 95.0
    sla_window_size: int = 20
    starts_at: Optional[str] = None
    expires_at: Optional[str] = None
    total_calls: int = 0
    successful_calls: int = 0
    failed_calls: int = 0
    total_latency_ms: int = 0
    budget_spent: str = "0"
    terminated_by: Optional[str] = None
    terminated_reason: Optional[str] = None
    violation_details: Optional[str] = None
    resolved_at: Optional[str] = None
    created_at: Optional[str] = None
    updated_at: Optional[str] = None

    @property
    def budget_remaining(self) -> str:
        from decimal import Decimal
        return str(Decimal(self.buyer_budget) - Decimal(self.budget_spent))

    @property
    def current_success_rate(self) -> float:
        if self.total_calls == 0:
            return 100.0
        return self.successful_calls / self.total_calls * 100.0

    @classmethod
    def from_dict(cls, data: dict) -> "Contract":
        return cls(
            id=data.get("id", ""),
            buyer_addr=data.get("buyerAddr", ""),
            seller_addr=data.get("sellerAddr", ""),
            service_type=data.get("serviceType", ""),
            price_per_call=data.get("pricePerCall", "0"),
            buyer_budget=data.get("buyerBudget", "0"),
            status=data.get("status", ""),
            duration=data.get("duration", ""),
            min_volume=data.get("minVolume", 1),
            seller_penalty=data.get("sellerPenalty", "0"),
            max_latency_ms=data.get("maxLatencyMs", 10000),
            min_success_rate=data.get("minSuccessRate", 95.0),
            sla_window_size=data.get("slaWindowSize", 20),
            starts_at=data.get("startsAt"),
            expires_at=data.get("expiresAt"),
            total_calls=data.get("totalCalls", 0),
            successful_calls=data.get("successfulCalls", 0),
            failed_calls=data.get("failedCalls", 0),
            total_latency_ms=data.get("totalLatencyMs", 0),
            budget_spent=data.get("budgetSpent", "0"),
            terminated_by=data.get("terminatedBy"),
            terminated_reason=data.get("terminatedReason"),
            violation_details=data.get("violationDetails"),
            resolved_at=data.get("resolvedAt"),
            created_at=data.get("createdAt"),
            updated_at=data.get("updatedAt"),
        )


@dataclass
class ReputationSnapshot:
    """A point-in-time reputation score from history."""

    id: int
    address: str
    score: float
    tier: str
    volume_score: float = 0.0
    activity_score: float = 0.0
    success_score: float = 0.0
    age_score: float = 0.0
    diversity_score: float = 0.0
    total_transactions: int = 0
    total_volume: float = 0.0
    success_rate: float = 0.0
    unique_peers: int = 0
    created_at: Optional[str] = None

    @classmethod
    def from_dict(cls, data: dict) -> "ReputationSnapshot":
        return cls(
            id=data.get("id", 0),
            address=data.get("address", ""),
            score=data.get("score", 0.0),
            tier=data.get("tier", "new"),
            volume_score=data.get("volumeScore", 0.0),
            activity_score=data.get("activityScore", 0.0),
            success_score=data.get("successScore", 0.0),
            age_score=data.get("ageScore", 0.0),
            diversity_score=data.get("diversityScore", 0.0),
            total_transactions=data.get("totalTransactions", 0),
            total_volume=data.get("totalVolume", 0.0),
            success_rate=data.get("successRate", 0.0),
            unique_peers=data.get("uniquePeers", 0),
            created_at=data.get("createdAt"),
        )


@dataclass
class ContractCall:
    """An individual service call within a contract."""

    id: str
    contract_id: str
    status: str
    amount: str
    latency_ms: int = 0
    error_message: Optional[str] = None
    created_at: Optional[str] = None

    @classmethod
    def from_dict(cls, data: dict) -> "ContractCall":
        return cls(
            id=data.get("id", ""),
            contract_id=data.get("contractId", ""),
            status=data.get("status", ""),
            amount=data.get("amount", "0"),
            latency_ms=data.get("latencyMs", 0),
            error_message=data.get("errorMessage"),
            created_at=data.get("createdAt"),
        )


@dataclass
class ScoringWeights:
    """Scoring weights for RFP bid evaluation."""

    price: float = 0.30
    reputation: float = 0.40
    sla: float = 0.30

    @classmethod
    def from_dict(cls, data: dict) -> "ScoringWeights":
        return cls(
            price=data.get("price", 0.30),
            reputation=data.get("reputation", 0.40),
            sla=data.get("sla", 0.30),
        )


@dataclass
class RFP:
    """A Request for Proposal published by a buyer."""

    id: str
    buyer_addr: str
    service_type: str
    min_budget: str
    max_budget: str
    duration: str
    status: str
    description: str = ""
    max_latency_ms: int = 10000
    min_success_rate: float = 95.0
    min_volume: int = 1
    bid_deadline: Optional[str] = None
    auto_select: bool = False
    min_reputation: float = 0.0
    max_counter_rounds: int = 3
    scoring_weights: Optional[ScoringWeights] = None
    winning_bid_id: Optional[str] = None
    contract_id: Optional[str] = None
    bid_count: int = 0
    cancel_reason: Optional[str] = None
    awarded_at: Optional[str] = None
    created_at: Optional[str] = None
    updated_at: Optional[str] = None

    @classmethod
    def from_dict(cls, data: dict) -> "RFP":
        weights = None
        if "scoringWeights" in data:
            weights = ScoringWeights.from_dict(data["scoringWeights"])
        return cls(
            id=data.get("id", ""),
            buyer_addr=data.get("buyerAddr", ""),
            service_type=data.get("serviceType", ""),
            min_budget=data.get("minBudget", "0"),
            max_budget=data.get("maxBudget", "0"),
            duration=data.get("duration", ""),
            status=data.get("status", ""),
            description=data.get("description", ""),
            max_latency_ms=data.get("maxLatencyMs", 10000),
            min_success_rate=data.get("minSuccessRate", 95.0),
            min_volume=data.get("minVolume", 1),
            bid_deadline=data.get("bidDeadline"),
            auto_select=data.get("autoSelect", False),
            min_reputation=data.get("minReputation", 0.0),
            max_counter_rounds=data.get("maxCounterRounds", 3),
            scoring_weights=weights,
            winning_bid_id=data.get("winningBidId"),
            contract_id=data.get("contractId"),
            bid_count=data.get("bidCount", 0),
            cancel_reason=data.get("cancelReason"),
            awarded_at=data.get("awardedAt"),
            created_at=data.get("createdAt"),
            updated_at=data.get("updatedAt"),
        )


@dataclass
class Bid:
    """A seller's offer on an RFP."""

    id: str
    rfp_id: str
    seller_addr: str
    price_per_call: str
    total_budget: str
    duration: str
    status: str
    max_latency_ms: int = 10000
    success_rate: float = 95.0
    seller_penalty: str = "0"
    score: float = 0.0
    counter_round: int = 0
    parent_bid_id: Optional[str] = None
    countered_by_id: Optional[str] = None
    message: Optional[str] = None
    created_at: Optional[str] = None
    updated_at: Optional[str] = None

    @classmethod
    def from_dict(cls, data: dict) -> "Bid":
        return cls(
            id=data.get("id", ""),
            rfp_id=data.get("rfpId", ""),
            seller_addr=data.get("sellerAddr", ""),
            price_per_call=data.get("pricePerCall", "0"),
            total_budget=data.get("totalBudget", "0"),
            duration=data.get("duration", ""),
            status=data.get("status", ""),
            max_latency_ms=data.get("maxLatencyMs", 10000),
            success_rate=data.get("successRate", 95.0),
            seller_penalty=data.get("sellerPenalty", "0"),
            score=data.get("score", 0.0),
            counter_round=data.get("counterRound", 0),
            parent_bid_id=data.get("parentBidId"),
            countered_by_id=data.get("counteredById"),
            message=data.get("message"),
            created_at=data.get("createdAt"),
            updated_at=data.get("updatedAt"),
        )
