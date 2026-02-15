"""
Event dataclasses for structured logging.

Each event type captures a specific type of occurrence during experiment execution.
Events are serialized to JSONL format for analysis.
"""

from dataclasses import dataclass, field, asdict
from datetime import datetime
from typing import Any, Optional
from enum import Enum


class EventType(str, Enum):
    """Types of events logged during experiments."""
    LLM_CALL = "llm_call"
    TRANSACTION = "transaction"
    NEGOTIATION = "negotiation"
    MARKET_ROUND = "market_round"
    DELIVERY = "delivery"
    REASONING_TRACE = "reasoning_trace"
    AGENT_ACTION = "agent_action"
    ERROR = "error"


@dataclass
class Event:
    """Base event class with common fields."""

    event_type: EventType
    timestamp: str = field(default_factory=lambda: datetime.utcnow().isoformat())
    run_id: str = ""
    study: str = ""

    def to_dict(self) -> dict:
        """Convert event to dictionary for JSON serialization."""
        data = asdict(self)
        # Convert enum to string
        if isinstance(data.get("event_type"), EventType):
            data["event_type"] = data["event_type"].value
        return data


@dataclass
class LLMCallEvent(Event):
    """
    Records a single LLM API call.

    This is the primary data source for reasoning trace analysis.
    The completion field contains the raw model output.
    """

    event_type: EventType = field(default=EventType.LLM_CALL)

    # Call identification
    agent_id: str = ""
    agent_role: str = ""  # "buyer" or "seller"
    model: str = ""
    provider: str = ""

    # Request
    system_prompt: str = ""
    messages: list[dict] = field(default_factory=list)  # Conversation history
    prompt: str = ""  # The final user message

    # Response
    completion: str = ""  # Raw model output - THE KEY DATA FOR ANALYSIS
    finish_reason: str = ""

    # Metrics
    input_tokens: int = 0
    output_tokens: int = 0
    latency_ms: float = 0.0
    cost_usd: float = 0.0

    # Context
    market_round: int = 0
    negotiation_round: int = 0
    action_type: str = ""  # "discover", "negotiate", "decide", etc.

    # Error handling
    error: Optional[str] = None
    retry_count: int = 0


@dataclass
class TransactionEvent(Event):
    """Records a transaction attempt and result."""

    event_type: EventType = field(default=EventType.TRANSACTION)

    # Parties
    sender_id: str = ""
    sender_address: str = ""
    recipient_id: str = ""
    recipient_address: str = ""

    # Transaction details
    amount: float = 0.0
    service_id: str = ""
    service_type: str = ""

    # Result
    status: str = ""  # "pending", "accepted", "rejected"
    accepted: bool = False
    rejection_reason: str = ""
    tx_hash: str = ""

    # CBA context
    session_key_id: str = ""
    cba_enforced: bool = True
    constraint_triggered: str = ""  # Which limit was hit

    # Reference for analysis
    reference_price: float = 0.0
    price_ratio: float = 0.0  # amount / reference_price


@dataclass
class NegotiationEvent(Event):
    """Records a single negotiation message."""

    event_type: EventType = field(default=EventType.NEGOTIATION)

    # Context
    negotiation_id: str = ""
    round_number: int = 0
    market_round: int = 0

    # Parties
    buyer_id: str = ""
    seller_id: str = ""
    sender_role: str = ""  # "buyer" or "seller"

    # Message
    message_type: str = ""  # "offer", "counter_offer", "accept", "reject"
    proposed_price: float = 0.0
    service_id: str = ""

    # Outcome
    negotiation_status: str = ""  # "ongoing", "accepted", "rejected", "timeout"


@dataclass
class MarketRoundEvent(Event):
    """Records summary of a complete market round."""

    event_type: EventType = field(default=EventType.MARKET_ROUND)

    round_number: int = 0

    # Participants
    num_buyers: int = 0
    num_sellers: int = 0
    num_services: int = 0

    # Activity
    num_discoveries: int = 0
    num_negotiations_started: int = 0
    num_negotiations_completed: int = 0
    num_transactions_attempted: int = 0
    num_transactions_accepted: int = 0

    # Economic metrics
    total_volume: float = 0.0
    avg_price: float = 0.0
    avg_price_ratio: float = 0.0

    # Timing
    duration_ms: float = 0.0


@dataclass
class DeliveryEvent(Event):
    """Records service delivery attempt and quality."""

    event_type: EventType = field(default=EventType.DELIVERY)

    # Context
    transaction_id: str = ""
    service_id: str = ""
    service_type: str = ""

    # Parties
    buyer_id: str = ""
    seller_id: str = ""

    # Delivery
    delivery_status: str = ""  # "success", "failure", "partial", "wrong"
    delivery_quality: float = 0.0  # 0-1 quality score
    delivery_type: str = ""  # "correct", "garbage", "empty", "bait_switch"

    # Adversary context (if applicable)
    adversary_type: str = ""
    adversary_intensity: float = 0.0


@dataclass
class ReasoningTraceEvent(Event):
    """
    Records coded reasoning patterns from an LLM completion.

    This is a post-processed event created by analyzing LLMCallEvents.
    Contains the extracted reasoning patterns and coding confidence.
    """

    event_type: EventType = field(default=EventType.REASONING_TRACE)

    # Reference to original LLM call
    llm_call_timestamp: str = ""
    agent_id: str = ""
    model: str = ""

    # The raw completion being analyzed
    completion: str = ""

    # Coded patterns
    patterns_detected: list[str] = field(default_factory=list)  # ReasoningPattern values
    dominant_pattern: str = ""
    pattern_confidence: float = 0.0  # 0-1 confidence in coding

    # Decision outcome
    decision_type: str = ""  # "purchase", "skip", "negotiate", "reject"
    decision_outcome: str = ""  # What actually happened

    # Coding metadata
    coding_method: str = ""  # "rule_based" or "llm_assisted"
    coder_model: str = ""  # If LLM-assisted, which model


@dataclass
class AgentActionEvent(Event):
    """Records a high-level agent action."""

    event_type: EventType = field(default=EventType.AGENT_ACTION)

    agent_id: str = ""
    agent_role: str = ""
    model: str = ""

    action: str = ""  # "discover", "evaluate", "negotiate", "purchase", "skip"
    target: str = ""  # Service ID or seller ID
    parameters: dict = field(default_factory=dict)

    success: bool = True
    result: Any = None
    error: Optional[str] = None


@dataclass
class ErrorEvent(Event):
    """Records an error during experiment execution."""

    event_type: EventType = field(default=EventType.ERROR)

    error_type: str = ""
    error_message: str = ""
    stack_trace: str = ""

    # Context
    agent_id: str = ""
    action: str = ""
    recoverable: bool = True
