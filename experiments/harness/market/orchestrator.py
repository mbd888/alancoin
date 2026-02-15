"""
Market orchestrator for multi-agent simulation.

Manages the flow of market rounds including discovery, negotiation,
transaction, and delivery phases.
"""

import asyncio
import time
from dataclasses import dataclass, field
from datetime import datetime
from typing import Optional
import uuid

from ..agents.base import BaseAgent
from ..agents.buyer import BuyerAgent
from ..agents.seller import SellerAgent
from ..logging import (
    StructuredLogger,
    MarketRoundEvent,
    TransactionEvent,
    NegotiationEvent,
)
from ..logging.cost_tracker import CostTracker
from ..clients.mock_market import MockMarket, ServiceType
from ..clients.gateway_market import GatewayMarket

from .negotiation import (
    NegotiationProtocol,
    NegotiationState,
    NegotiationMessage,
    MessageType,
    NegotiationStatus,
)
from .delivery import DeliverySimulator


@dataclass
class MarketRoundResult:
    """Results from a single market round."""

    round_number: int
    duration_ms: float

    # Activity counts
    discoveries: int = 0
    negotiations_started: int = 0
    negotiations_completed: int = 0
    transactions_attempted: int = 0
    transactions_accepted: int = 0
    transactions_rejected: int = 0

    # Economic metrics
    total_volume: float = 0.0
    prices: list[float] = field(default_factory=list)

    # Detailed results
    negotiation_results: list[dict] = field(default_factory=list)
    transaction_results: list[dict] = field(default_factory=list)


class MarketOrchestrator:
    """
    Orchestrates multi-agent market simulation.

    Manages the lifecycle of market rounds:
    1. Discovery phase: Buyers discover available services
    2. Negotiation phase: Buyers and sellers negotiate prices
    3. Transaction phase: Execute agreed purchases
    4. Delivery phase: Simulate service delivery
    """

    def __init__(
        self,
        mock_market: Optional[MockMarket | GatewayMarket] = None,
        negotiation_protocol: Optional[NegotiationProtocol] = None,
        delivery_simulator: Optional[DeliverySimulator] = None,
        logger: Optional[StructuredLogger] = None,
        cost_tracker: Optional[CostTracker] = None,
        cba_enabled: bool = True,
    ):
        """
        Initialize market orchestrator.

        Args:
            mock_market: Market backend (MockMarket or GatewayMarket). Creates MockMarket if None.
            negotiation_protocol: Negotiation protocol (creates new if None)
            delivery_simulator: Delivery simulator (creates new if None)
            logger: Structured logger for events
            cost_tracker: Cost tracker for API usage
            cba_enabled: Whether CBA constraints are enforced
        """
        self.market = mock_market or MockMarket(cba_enabled=cba_enabled)
        self.negotiation = negotiation_protocol or NegotiationProtocol()
        self.delivery = delivery_simulator or DeliverySimulator()
        self.logger = logger
        self.cost_tracker = cost_tracker
        self.cba_enabled = cba_enabled

        self.buyers: dict[str, BuyerAgent] = {}
        self.sellers: dict[str, SellerAgent] = {}

        self.round_number = 0
        self.round_results: list[MarketRoundResult] = []

    def register_buyer(self, buyer: BuyerAgent, market_agent_id: str):
        """Register a buyer agent with the market."""
        self.buyers[market_agent_id] = buyer

    def register_seller(self, seller: SellerAgent, market_agent_id: str):
        """Register a seller agent with the market."""
        self.sellers[market_agent_id] = seller

    async def run_session(self, num_rounds: int) -> dict:
        """
        Run a complete market session.

        Args:
            num_rounds: Number of market rounds to execute

        Returns:
            Session results summary
        """
        session_start = time.perf_counter()
        session_id = f"session_{uuid.uuid4().hex[:8]}"

        # Initialize all agents
        await self._initialize_agents()

        # Run market rounds
        for round_num in range(1, num_rounds + 1):
            self.round_number = round_num
            round_result = await self._run_round(round_num)
            self.round_results.append(round_result)

            # Reset daily counters at end of each "day" (every round for simplicity)
            if round_num % 10 == 0:
                self.market.reset_daily_usage()

        session_duration = (time.perf_counter() - session_start) * 1000

        return self._compile_session_results(session_id, session_duration)

    async def _initialize_agents(self):
        """Initialize all agents with market context."""
        market_context = {
            "num_buyers": len(self.buyers),
            "num_sellers": len(self.sellers),
            "service_types": [st.value for st in ServiceType],
            "services": [
                {
                    "id": svc.id,
                    "name": svc.name,
                    "type": svc.service_type.value,
                    "price": svc.price,
                    "description": svc.description,
                    "seller_id": svc.seller_id,
                }
                for svc in self.market.services.values()
            ],
        }

        # Initialize buyers and sellers concurrently
        init_tasks = []
        for buyer in self.buyers.values():
            init_tasks.append(buyer.initialize(market_context))
        for seller in self.sellers.values():
            init_tasks.append(seller.initialize(market_context))

        results = await asyncio.gather(*init_tasks, return_exceptions=True)
        # Check for failed initializations
        for i, result in enumerate(results):
            if isinstance(result, Exception):
                agent_id = list(self.buyers.values()) + list(self.sellers.values())
                raise RuntimeError(f"Agent initialization failed: {result}") from result

    async def _run_round(self, round_number: int) -> MarketRoundResult:
        """Run a single market round."""
        round_start = time.perf_counter()

        result = MarketRoundResult(round_number=round_number, duration_ms=0)

        # Phase 1: Discovery
        discoveries = await self._discovery_phase(round_number)
        result.discoveries = len(discoveries)

        # Phase 2: Negotiation
        negotiations = await self._negotiation_phase(round_number, discoveries)
        result.negotiations_started = len(negotiations)
        result.negotiations_completed = sum(
            1 for n in negotiations if n.is_complete()
        )
        result.negotiation_results = [n.to_dict() for n in negotiations]

        # Phase 3: Transactions
        transactions = await self._transaction_phase(round_number, negotiations)
        result.transactions_attempted = len(transactions)
        result.transactions_accepted = sum(
            1 for t in transactions if t.get("accepted")
        )
        result.transactions_rejected = len(transactions) - result.transactions_accepted
        result.transaction_results = transactions

        # Calculate economic metrics
        accepted_txs = [t for t in transactions if t.get("accepted")]
        result.total_volume = sum(t.get("amount", 0) for t in accepted_txs)
        result.prices = [t.get("amount", 0) for t in accepted_txs]

        # Phase 4: Delivery
        await self._delivery_phase(round_number, accepted_txs)

        result.duration_ms = (time.perf_counter() - round_start) * 1000

        # Log market round event
        if self.logger:
            event = MarketRoundEvent(
                round_number=round_number,
                num_buyers=len(self.buyers),
                num_sellers=len(self.sellers),
                num_services=len(self.market.services),
                num_discoveries=result.discoveries,
                num_negotiations_started=result.negotiations_started,
                num_negotiations_completed=result.negotiations_completed,
                num_transactions_attempted=result.transactions_attempted,
                num_transactions_accepted=result.transactions_accepted,
                total_volume=result.total_volume,
                avg_price=sum(result.prices) / len(result.prices) if result.prices else 0,
                duration_ms=result.duration_ms,
            )
            self.logger.log(event)

        return result

    async def _discovery_phase(self, round_number: int) -> list[dict]:
        """
        Run discovery phase where buyers search for services.

        Returns:
            List of discovery results (buyer_id, services found)
        """
        discoveries = []

        for buyer_id, buyer in self.buyers.items():
            # Get buyer's discovery action
            observation = {
                "phase": "discovery",
                "market_round": round_number,
                "services": [
                    {
                        "id": svc.id,
                        "name": svc.name,
                        "type": svc.service_type.value,
                        "price": svc.price,
                        "description": svc.description,
                        "seller_id": svc.seller_id,
                    }
                    for svc in self.market.services.values()
                    if svc.active
                ],
            }

            action = await buyer.act(observation)

            if action.get("action") == "discover":
                # Perform discovery
                service_type = action.get("service_type")
                max_price = action.get("max_price")

                # Filter services
                found_services = self.market.discover_services(
                    service_type=ServiceType(service_type) if service_type else None,
                    max_price=max_price,
                )

                discoveries.append({
                    "buyer_id": buyer_id,
                    "services": [
                        {
                            "id": svc.id,
                            "name": svc.name,
                            "type": svc.service_type.value,
                            "price": svc.price,
                            "seller_id": svc.seller_id,
                        }
                        for svc in found_services
                    ],
                })

            elif action.get("action") in ["offer", "select"]:
                # Buyer selected a service directly
                discoveries.append({
                    "buyer_id": buyer_id,
                    "selected_service": action.get("service_id"),
                    "offered_price": action.get("price", 0),
                })

        return discoveries

    async def _negotiation_phase(
        self,
        round_number: int,
        discoveries: list[dict],
    ) -> list[NegotiationState]:
        """
        Run negotiation phase between buyers and sellers.

        Returns:
            List of completed negotiations
        """
        negotiations = []

        for discovery in discoveries:
            buyer_id = discovery.get("buyer_id")
            buyer = self.buyers.get(buyer_id)
            if not buyer:
                continue

            # Check if buyer selected a service
            service_id = discovery.get("selected_service")
            offered_price = discovery.get("offered_price", 0)

            if not service_id:
                # Buyer needs to select from discovered services
                services = discovery.get("services", [])
                if not services:
                    continue

                # Get buyer's selection
                observation = {
                    "phase": "selection",
                    "market_round": round_number,
                    "services": services,
                }
                action = await buyer.act(observation)

                if action.get("action") not in ["offer", "select"]:
                    continue

                service_id = action.get("service_id")
                offered_price = action.get("price", 0)

            if not service_id:
                continue

            # Get service and seller
            service = self.market.services.get(service_id)
            if not service:
                continue

            seller_id = service.seller_id
            seller = self.sellers.get(seller_id)
            if not seller:
                continue

            # Start negotiation
            negotiation = self.negotiation.start_negotiation(
                service_id=service_id,
                buyer_id=buyer_id,
                seller_id=seller_id,
                listed_price=service.price,
                initial_offer=offered_price or service.price,
            )

            # Run negotiation rounds
            negotiation = await self._run_negotiation(
                negotiation, buyer, seller, round_number
            )
            negotiations.append(negotiation)

        return negotiations

    async def _run_negotiation(
        self,
        state: NegotiationState,
        buyer: BuyerAgent,
        seller: SellerAgent,
        market_round: int,
    ) -> NegotiationState:
        """Run a complete negotiation between buyer and seller."""
        while not state.is_complete():
            whose_turn = self.negotiation.get_whose_turn(state)

            if whose_turn == "seller":
                # Seller responds
                observation = self.negotiation.format_for_seller(state)
                observation["market_round"] = market_round
                observation["service"] = {
                    "id": state.service_id,
                    "price": state.listed_price,
                }

                action = await seller.act(observation)
            else:
                # Buyer responds
                observation = self.negotiation.format_for_buyer(state)
                observation["market_round"] = market_round
                observation["service"] = {
                    "id": state.service_id,
                    "price": state.listed_price,
                }

                action = await buyer.act(observation)

            # Convert action to negotiation message
            message = self._action_to_message(action, whose_turn, state)

            # Process the message
            state = self.negotiation.process_response(
                state.negotiation_id, message
            )

            # Log negotiation event
            if self.logger:
                event = NegotiationEvent(
                    negotiation_id=state.negotiation_id,
                    round_number=state.round_number,
                    market_round=market_round,
                    buyer_id=state.buyer_id,
                    seller_id=state.seller_id,
                    sender_role=whose_turn,
                    message_type=message.message_type.value,
                    proposed_price=message.price,
                    service_id=state.service_id,
                    negotiation_status=state.status.value,
                )
                self.logger.log(event)

        return state

    def _action_to_message(
        self,
        action: dict,
        sender_role: str,
        state: NegotiationState,
    ) -> NegotiationMessage:
        """Convert agent action to negotiation message."""
        action_type = action.get("action", "").lower()

        if action_type == "accept":
            return NegotiationMessage(
                message_type=MessageType.ACCEPT,
                sender_id=state.buyer_id if sender_role == "buyer" else state.seller_id,
                sender_role=sender_role,
                price=action.get("price", state.current_price),
                reason=action.get("reason", "Accepted"),
            )
        elif action_type == "reject":
            return NegotiationMessage(
                message_type=MessageType.REJECT,
                sender_id=state.buyer_id if sender_role == "buyer" else state.seller_id,
                sender_role=sender_role,
                price=0,
                reason=action.get("reason", "Rejected"),
            )
        elif action_type in ["counter_offer", "offer"]:
            return NegotiationMessage(
                message_type=MessageType.COUNTER_OFFER,
                sender_id=state.buyer_id if sender_role == "buyer" else state.seller_id,
                sender_role=sender_role,
                price=action.get("price", state.current_price),
                reason=action.get("reason", "Counter offer"),
            )
        else:
            # Default to reject for unrecognized actions
            return NegotiationMessage(
                message_type=MessageType.REJECT,
                sender_id=state.buyer_id if sender_role == "buyer" else state.seller_id,
                sender_role=sender_role,
                reason=f"Unrecognized action: {action_type}",
            )

    async def _transaction_phase(
        self,
        round_number: int,
        negotiations: list[NegotiationState],
    ) -> list[dict]:
        """
        Execute transactions for successful negotiations.

        Returns:
            List of transaction results
        """
        transactions = []

        for negotiation in negotiations:
            if negotiation.status != NegotiationStatus.ACCEPTED:
                continue

            if negotiation.final_price is None:
                continue

            # Execute transaction through mock market
            tx = self.market.transact(
                sender_id=negotiation.buyer_id,
                recipient_id=negotiation.seller_id,
                amount=negotiation.final_price,
                service_id=negotiation.service_id,
            )

            # Get reference price for analysis
            service = self.market.services.get(negotiation.service_id)
            reference_price = service.reference_price if service else negotiation.listed_price

            tx_result = {
                "transaction_id": tx.id,
                "buyer_id": negotiation.buyer_id,
                "seller_id": negotiation.seller_id,
                "service_id": negotiation.service_id,
                "amount": negotiation.final_price,
                "accepted": tx.status == "accepted",
                "rejected": tx.status == "rejected",
                "rejection_reason": tx.rejection_reason,
                "reference_price": reference_price,
                "price_ratio": negotiation.final_price / reference_price if reference_price > 0 else 0,
            }
            transactions.append(tx_result)

            # Update agent records
            buyer = self.buyers.get(negotiation.buyer_id)
            seller = self.sellers.get(negotiation.seller_id)

            if buyer:
                buyer.record_purchase(
                    negotiation.service_id,
                    negotiation.final_price,
                    tx.status == "accepted",
                )
            if seller:
                seller.record_sale(
                    negotiation.service_id,
                    negotiation.final_price,
                    negotiation.buyer_id,
                    tx.status == "accepted",
                )

            # Log transaction event
            if self.logger:
                event = TransactionEvent(
                    sender_id=negotiation.buyer_id,
                    sender_address=tx.sender_address,
                    recipient_id=negotiation.seller_id,
                    recipient_address=tx.recipient_address,
                    amount=negotiation.final_price,
                    service_id=negotiation.service_id,
                    status=tx.status,
                    accepted=tx.status == "accepted",
                    rejection_reason=tx.rejection_reason,
                    cba_enforced=self.cba_enabled,
                    reference_price=reference_price,
                    price_ratio=tx_result["price_ratio"],
                )
                self.logger.log(event)

        return transactions

    async def _delivery_phase(
        self,
        round_number: int,
        transactions: list[dict],
    ):
        """
        Simulate service delivery for accepted transactions.
        """
        for tx in transactions:
            if not tx.get("accepted"):
                continue

            service_id = tx.get("service_id")
            transaction_id = tx.get("transaction_id")

            # Simulate delivery
            delivery_result = self.delivery.deliver(
                service_id=service_id,
                transaction_id=transaction_id,
                market=self.market,
            )

            tx["delivery"] = delivery_result.to_dict()

    def _compile_session_results(
        self,
        session_id: str,
        duration_ms: float,
    ) -> dict:
        """Compile results from all rounds into session summary."""
        total_volume = sum(r.total_volume for r in self.round_results)
        total_transactions = sum(r.transactions_accepted for r in self.round_results)
        total_attempted = sum(r.transactions_attempted for r in self.round_results)
        all_prices = [p for r in self.round_results for p in r.prices]

        return {
            "session_id": session_id,
            "duration_ms": duration_ms,
            "num_rounds": len(self.round_results),
            "num_buyers": len(self.buyers),
            "num_sellers": len(self.sellers),
            "num_services": len(self.market.services),
            "summary": {
                "total_volume": total_volume,
                "total_transactions": total_transactions,
                "total_attempted": total_attempted,
                "acceptance_rate": total_transactions / total_attempted if total_attempted > 0 else 0,
                "avg_price": sum(all_prices) / len(all_prices) if all_prices else 0,
                "total_negotiations": sum(r.negotiations_started for r in self.round_results),
            },
            "buyer_stats": {
                buyer_id: buyer.get_buyer_stats()
                for buyer_id, buyer in self.buyers.items()
            },
            "seller_stats": {
                seller_id: seller.get_seller_stats()
                for seller_id, seller in self.sellers.items()
            },
            "market_stats": self.market.get_market_stats(),
            "round_results": [
                {
                    "round": r.round_number,
                    "volume": r.total_volume,
                    "transactions": r.transactions_accepted,
                    "negotiations": r.negotiations_completed,
                }
                for r in self.round_results
            ],
        }
