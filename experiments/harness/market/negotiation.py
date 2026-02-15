"""
Negotiation protocol for buyer-seller interactions.

Defines the message types and state machine for price negotiation.
"""

from dataclasses import dataclass, field
from datetime import datetime
from enum import Enum
from typing import Optional
import uuid


class MessageType(str, Enum):
    """Types of negotiation messages."""
    OFFER = "offer"
    COUNTER_OFFER = "counter_offer"
    ACCEPT = "accept"
    REJECT = "reject"
    INQUIRY = "inquiry"
    WITHDRAW = "withdraw"


class NegotiationStatus(str, Enum):
    """Status of a negotiation."""
    PENDING = "pending"
    ONGOING = "ongoing"
    ACCEPTED = "accepted"
    REJECTED = "rejected"
    TIMEOUT = "timeout"
    WITHDRAWN = "withdrawn"


@dataclass
class NegotiationMessage:
    """A single message in a negotiation."""

    message_type: MessageType
    sender_id: str
    sender_role: str  # "buyer" or "seller"
    price: float = 0.0
    reason: str = ""
    timestamp: str = field(default_factory=lambda: datetime.utcnow().isoformat())
    metadata: dict = field(default_factory=dict)

    def to_dict(self) -> dict:
        return {
            "type": self.message_type.value,
            "sender_id": self.sender_id,
            "sender_role": self.sender_role,
            "price": self.price,
            "reason": self.reason,
            "timestamp": self.timestamp,
            "metadata": self.metadata,
        }


@dataclass
class NegotiationState:
    """State of an ongoing negotiation."""

    negotiation_id: str
    service_id: str
    buyer_id: str
    seller_id: str

    # Price tracking
    listed_price: float
    current_price: float  # Most recent proposed price
    buyer_last_offer: float = 0.0
    seller_last_offer: float = 0.0

    # Status
    status: NegotiationStatus = NegotiationStatus.PENDING
    round_number: int = 0
    max_rounds: int = 5

    # History
    messages: list[NegotiationMessage] = field(default_factory=list)

    # Timestamps
    started_at: str = field(default_factory=lambda: datetime.utcnow().isoformat())
    ended_at: Optional[str] = None

    # Outcome
    final_price: Optional[float] = None
    accepted_by: Optional[str] = None  # ID of agent who accepted

    def is_complete(self) -> bool:
        """Check if negotiation has concluded."""
        return self.status in [
            NegotiationStatus.ACCEPTED,
            NegotiationStatus.REJECTED,
            NegotiationStatus.TIMEOUT,
            NegotiationStatus.WITHDRAWN,
        ]

    def add_message(self, message: NegotiationMessage):
        """Add a message to the negotiation history."""
        self.messages.append(message)

        # Update price tracking
        if message.sender_role == "buyer":
            self.buyer_last_offer = message.price
        else:
            self.seller_last_offer = message.price

        if message.price > 0:
            self.current_price = message.price

    def to_dict(self) -> dict:
        return {
            "negotiation_id": self.negotiation_id,
            "service_id": self.service_id,
            "buyer_id": self.buyer_id,
            "seller_id": self.seller_id,
            "listed_price": self.listed_price,
            "current_price": self.current_price,
            "status": self.status.value,
            "round_number": self.round_number,
            "max_rounds": self.max_rounds,
            "messages": [m.to_dict() for m in self.messages],
            "final_price": self.final_price,
        }


class NegotiationProtocol:
    """
    Manages negotiation between buyer and seller agents.

    Implements a turn-based negotiation protocol with:
    - Maximum round limit to prevent infinite loops
    - Message validation
    - State tracking
    - Timeout handling
    """

    def __init__(
        self,
        max_rounds: int = 5,
        timeout_seconds: float = 30.0,
    ):
        """
        Initialize negotiation protocol.

        Args:
            max_rounds: Maximum negotiation rounds before timeout
            timeout_seconds: Timeout for each message response
        """
        self.max_rounds = max_rounds
        self.timeout_seconds = timeout_seconds
        self.active_negotiations: dict[str, NegotiationState] = {}

    def start_negotiation(
        self,
        service_id: str,
        buyer_id: str,
        seller_id: str,
        listed_price: float,
        initial_offer: float,
    ) -> NegotiationState:
        """
        Start a new negotiation.

        Args:
            service_id: ID of service being negotiated
            buyer_id: Buyer agent ID
            seller_id: Seller agent ID
            listed_price: Seller's listed price
            initial_offer: Buyer's initial offer

        Returns:
            New NegotiationState
        """
        negotiation_id = f"neg_{uuid.uuid4().hex[:12]}"

        state = NegotiationState(
            negotiation_id=negotiation_id,
            service_id=service_id,
            buyer_id=buyer_id,
            seller_id=seller_id,
            listed_price=listed_price,
            current_price=initial_offer,
            max_rounds=self.max_rounds,
            status=NegotiationStatus.ONGOING,
            round_number=1,
        )

        # Record initial offer
        initial_message = NegotiationMessage(
            message_type=MessageType.OFFER,
            sender_id=buyer_id,
            sender_role="buyer",
            price=initial_offer,
            reason="Initial offer",
        )
        state.add_message(initial_message)
        state.buyer_last_offer = initial_offer

        self.active_negotiations[negotiation_id] = state
        return state

    def process_response(
        self,
        negotiation_id: str,
        message: NegotiationMessage,
    ) -> NegotiationState:
        """
        Process a negotiation response.

        Args:
            negotiation_id: ID of the negotiation
            message: Response message from buyer or seller

        Returns:
            Updated NegotiationState
        """
        state = self.active_negotiations.get(negotiation_id)
        if not state:
            raise ValueError(f"Negotiation {negotiation_id} not found")

        if state.is_complete():
            raise ValueError(f"Negotiation {negotiation_id} already complete")

        # Add message to history
        state.add_message(message)

        # Process based on message type
        if message.message_type == MessageType.ACCEPT:
            state.status = NegotiationStatus.ACCEPTED
            state.final_price = state.current_price
            state.accepted_by = message.sender_id
            state.ended_at = datetime.utcnow().isoformat()

        elif message.message_type == MessageType.REJECT:
            state.status = NegotiationStatus.REJECTED
            state.ended_at = datetime.utcnow().isoformat()

        elif message.message_type == MessageType.WITHDRAW:
            state.status = NegotiationStatus.WITHDRAWN
            state.ended_at = datetime.utcnow().isoformat()

        elif message.message_type in [MessageType.COUNTER_OFFER, MessageType.OFFER]:
            state.round_number += 1

            # Check round limit
            if state.round_number > self.max_rounds:
                state.status = NegotiationStatus.TIMEOUT
                state.ended_at = datetime.utcnow().isoformat()

        return state

    def get_negotiation(self, negotiation_id: str) -> Optional[NegotiationState]:
        """Get negotiation state by ID."""
        return self.active_negotiations.get(negotiation_id)

    def timeout_negotiation(self, negotiation_id: str) -> NegotiationState:
        """Mark a negotiation as timed out."""
        state = self.active_negotiations.get(negotiation_id)
        if state and not state.is_complete():
            state.status = NegotiationStatus.TIMEOUT
            state.ended_at = datetime.utcnow().isoformat()
        return state

    def get_whose_turn(self, state: NegotiationState) -> str:
        """Determine whose turn it is to respond."""
        if not state.messages:
            return "buyer"

        last_message = state.messages[-1]
        if last_message.sender_role == "buyer":
            return "seller"
        return "buyer"

    def format_for_buyer(self, state: NegotiationState) -> dict:
        """Format negotiation state for buyer agent."""
        return {
            "phase": "negotiation",
            "negotiation_id": state.negotiation_id,
            "service_id": state.service_id,
            "seller_id": state.seller_id,
            "listed_price": state.listed_price,
            "offered_price": state.seller_last_offer or state.listed_price,
            "your_last_offer": state.buyer_last_offer,
            "negotiation_round": state.round_number,
            "max_rounds": state.max_rounds,
            "seller_message": state.messages[-1].reason if state.messages else "",
        }

    def format_for_seller(self, state: NegotiationState) -> dict:
        """Format negotiation state for seller agent."""
        return {
            "phase": "negotiation",
            "negotiation_id": state.negotiation_id,
            "service_id": state.service_id,
            "buyer_id": state.buyer_id,
            "listed_price": state.listed_price,
            "offered_price": state.buyer_last_offer,
            "your_last_offer": state.seller_last_offer or state.listed_price,
            "negotiation_round": state.round_number,
            "max_rounds": state.max_rounds,
            "buyer_message": state.messages[-1].reason if state.messages else "",
        }
