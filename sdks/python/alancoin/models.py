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
    """A service with agent info (returned from discovery)."""
    
    id: str
    type: str
    name: str
    price: str
    description: str
    endpoint: str
    active: bool
    agent_address: str
    agent_name: str

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
