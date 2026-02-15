"""
GatewayMarket — routes transactions through the real Go gateway API.

Implements the same interface as MockMarket so the orchestrator can use
either backend transparently. This is the path that makes the paper's
CBA claim non-tautological: the enforcement happens in Go + PostgreSQL,
not in a Python if-statement.

Requires the platform to be running (``make run``).
"""

from __future__ import annotations

import time
import hashlib
import logging
from dataclasses import dataclass, field
from typing import Any, Optional

import requests

from .mock_market import ServiceType, MockService, MockAgent, MockTransaction

logger = logging.getLogger(__name__)


class GatewayError(Exception):
    """Raised when the gateway API returns a non-2xx status."""

    def __init__(self, status_code: int, body: dict, message: str = ""):
        self.status_code = status_code
        self.body = body
        super().__init__(message or f"Gateway API error {status_code}: {body}")


@dataclass
class GatewaySession:
    """Tracks a gateway session for one buyer agent."""

    session_id: str
    token: str
    agent_id: str
    max_total: float
    max_per_request: float
    total_spent: float = 0.0
    request_count: int = 0


class GatewayMarket:
    """
    Market backend that routes through the real Alancoin gateway.

    Lifecycle:
        1. ``create_agent`` / ``add_service`` — local bookkeeping (same as MockMarket)
        2. ``setup_session(buyer_id)`` — POST /v1/gateway/sessions
        3. ``transact(...)`` — POST /v1/gateway/proxy
        4. ``teardown_session(buyer_id)`` — DELETE /v1/gateway/sessions/:id

    Agents and services are tracked locally so the orchestrator can reference
    them by ID. The gateway handles hold/settle/release and policy evaluation.
    """

    def __init__(
        self,
        api_url: str = "http://localhost:8080",
        api_key: str = "",
        tenant_id: str = "",
        seed: Optional[int] = None,
        cba_enabled: bool = True,
        timeout: float = 30.0,
    ):
        self.api_url = api_url.rstrip("/")
        self.api_key = api_key
        self.tenant_id = tenant_id
        self.cba_enabled = cba_enabled
        self.timeout = timeout

        self.agents: dict[str, MockAgent] = {}
        self.services: dict[str, MockService] = {}
        self.transactions: list[MockTransaction] = []

        self._sessions: dict[str, GatewaySession] = {}  # buyer agent_id → session
        self._agent_counter = 0
        self._service_counter = 0
        self._tx_counter = 0

        self.reference_prices = {
            ServiceType.INFERENCE: 0.50,
            ServiceType.TRANSLATION: 0.30,
            ServiceType.CODE_REVIEW: 1.00,
            ServiceType.DATA_ANALYSIS: 0.75,
            ServiceType.SUMMARIZATION: 0.25,
            ServiceType.EMBEDDING: 0.10,
        }

    # ── HTTP helpers ──────────────────────────────────────────────

    def _headers(self, gateway_token: str = "") -> dict:
        h = {
            "Content-Type": "application/json",
            "Authorization": f"Bearer {self.api_key}",
        }
        if gateway_token:
            h["X-Gateway-Token"] = gateway_token
        return h

    def _post(self, path: str, json_body: dict, gateway_token: str = "") -> dict:
        url = f"{self.api_url}{path}"
        resp = requests.post(
            url,
            json=json_body,
            headers=self._headers(gateway_token),
            timeout=self.timeout,
        )
        body = resp.json() if resp.content else {}
        if resp.status_code >= 400:
            raise GatewayError(resp.status_code, body)
        return body

    def _delete(self, path: str) -> dict:
        url = f"{self.api_url}{path}"
        resp = requests.delete(
            url,
            headers=self._headers(),
            timeout=self.timeout,
        )
        body = resp.json() if resp.content else {}
        if resp.status_code >= 400:
            raise GatewayError(resp.status_code, body)
        return body

    # ── Agent / service bookkeeping (same interface as MockMarket) ──

    def create_agent(
        self,
        name: str,
        role: str,
        balance: float = 100.0,
        max_per_tx: float = 1.0,
        max_per_day: float = 10.0,
        max_total: float = 100.0,
    ) -> MockAgent:
        self._agent_counter += 1
        agent_id = f"agent_{self._agent_counter:03d}"
        agent = MockAgent(
            id=agent_id,
            address=f"0x{hashlib.sha256(name.encode()).hexdigest()[:40]}",
            name=name,
            role=role,
            balance=balance,
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
        results = []
        for service in self.services.values():
            if not service.active:
                continue
            if service_type and service.service_type != service_type:
                continue
            if max_price and service.price > max_price:
                continue
            results.append(service)
        results.sort(key=lambda s: s.price)
        return results[:limit]

    # ── Gateway session management ────────────────────────────────

    def setup_session(self, buyer_id: str) -> GatewaySession:
        """
        Create a gateway session for a buyer.

        Calls POST /v1/gateway/sessions with the buyer's spending limits.
        """
        agent = self.agents.get(buyer_id)
        if not agent:
            raise ValueError(f"Agent {buyer_id} not found")

        body = self._post("/v1/gateway/sessions", {
            "maxTotal": f"{agent.max_total:.6f}",
            "maxPerRequest": f"{agent.max_per_tx:.6f}",
            "strategy": "cheapest",
            "expiresInSecs": 3600,
        })

        session = GatewaySession(
            session_id=body["session"]["id"],
            token=body["token"],
            agent_id=buyer_id,
            max_total=agent.max_total,
            max_per_request=agent.max_per_tx,
        )
        self._sessions[buyer_id] = session
        logger.info("Gateway session created: %s for %s", session.session_id, buyer_id)
        return session

    def teardown_session(self, buyer_id: str) -> dict:
        """
        Close a gateway session.

        Calls DELETE /v1/gateway/sessions/:id. Releases remaining held funds.
        """
        session = self._sessions.get(buyer_id)
        if not session:
            return {}

        result = self._delete(f"/v1/gateway/sessions/{session.session_id}")
        del self._sessions[buyer_id]
        logger.info(
            "Gateway session closed: %s, spent=%s",
            session.session_id,
            result.get("totalSpent", "?"),
        )
        return result

    # ── Transactions via gateway proxy ────────────────────────────

    def transact(
        self,
        sender_id: str,
        recipient_id: str,
        amount: float,
        service_id: Optional[str] = None,
    ) -> MockTransaction:
        """
        Route a transaction through the gateway proxy.

        If the buyer has an active gateway session, calls POST /v1/gateway/proxy.
        The gateway performs hold/settle/release and policy evaluation in Go.
        Falls back to local bookkeeping if no session exists.
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

        session = self._sessions.get(sender_id)
        if session:
            tx = self._proxy_transaction(tx, session, service_id)
        elif self._sessions:
            # Other buyers have sessions but this one doesn't — likely a bug
            raise RuntimeError(
                f"transact() called for {sender_id} without a gateway session, "
                f"but sessions exist for {list(self._sessions.keys())}. "
                f"Call setup_session({sender_id}) first."
            )
        else:
            # No sessions at all — pure local mode (e.g., seller-to-seller)
            tx = self._local_transaction(tx, sender, recipient, amount)

        self.transactions.append(tx)
        return tx

    def _proxy_transaction(
        self,
        tx: MockTransaction,
        session: GatewaySession,
        service_id: Optional[str],
    ) -> MockTransaction:
        """Execute transaction via POST /v1/gateway/proxy."""
        service = self.services.get(service_id) if service_id else None
        service_type = service.service_type.value if service else "inference"

        try:
            result = self._post(
                "/v1/gateway/proxy",
                {
                    "serviceType": service_type,
                    "params": {
                        "action": "purchase",
                        "service_id": service_id or "",
                        "amount": f"{tx.amount:.6f}",
                    },
                    "maxPrice": f"{tx.amount:.6f}",
                },
                gateway_token=session.token,
            )

            # Validate required fields — fail loudly on missing data
            if "totalSpent" not in result:
                raise GatewayError(
                    500, result,
                    "Gateway response missing 'totalSpent' — cannot reconcile session state",
                )
            inner = result.get("result")
            if not isinstance(inner, dict) or "amountPaid" not in inner:
                raise GatewayError(
                    500, result,
                    "Gateway response missing 'result.amountPaid' — cannot reconcile balances",
                )

            paid = float(inner["amountPaid"])

            # Reconciliation warning: flag drift between requested and settled amount
            if abs(paid - tx.amount) > 0.000001:
                logger.warning(
                    "Gateway settled %.6f, requested %.6f (diff=%.6f) for tx %s",
                    paid, tx.amount, abs(paid - tx.amount), tx.id,
                )

            tx.status = "accepted"
            tx.amount = paid  # Record what was actually settled
            session.total_spent = float(result["totalSpent"])
            session.request_count += 1

            # Update local bookkeeping with gateway-authoritative amount
            sender = self.agents[tx.sender_id]
            recipient = self.agents[tx.recipient_id]
            sender.balance -= paid
            sender.spent_today += paid
            sender.total_spent += paid
            recipient.balance += paid
            sender.transactions.append(tx)
            recipient.transactions.append(tx)

        except GatewayError as e:
            if e.status_code in (500, 502, 503, 504):
                # Infrastructure error — don't record as policy rejection
                logger.error(
                    "Gateway infrastructure error %d for tx %s: %s",
                    e.status_code, tx.id, e.body,
                )
                raise  # Propagate — caller decides retry strategy
            tx.status = "rejected"
            error_code = e.body.get("error", "")
            tx.rejection_reason = (
                f"gateway[{e.status_code}]: {error_code}"
                f" — {e.body.get('message', 'no details')}"
            )
            logger.info("Gateway rejected tx %s: %s", tx.id, tx.rejection_reason)

        return tx

    def _local_transaction(
        self,
        tx: MockTransaction,
        sender: MockAgent,
        recipient: MockAgent,
        amount: float,
    ) -> MockTransaction:
        """Fallback to local CBA check when no gateway session."""
        if self.cba_enabled:
            if amount > sender.max_per_tx:
                tx.status = "rejected"
                tx.rejection_reason = f"amount exceeds max_per_tx ({sender.max_per_tx})"
                return tx
            if sender.spent_today + amount > sender.max_per_day:
                tx.status = "rejected"
                tx.rejection_reason = f"amount exceeds daily limit ({sender.max_per_day})"
                return tx
            if sender.max_total > 0 and sender.total_spent + amount > sender.max_total:
                tx.status = "rejected"
                tx.rejection_reason = f"amount exceeds total limit ({sender.max_total})"
                return tx

        if sender.balance < amount:
            tx.status = "rejected"
            tx.rejection_reason = "insufficient balance"
            return tx

        sender.balance -= amount
        sender.spent_today += amount
        sender.total_spent += amount
        recipient.balance += amount
        tx.status = "accepted"
        sender.transactions.append(tx)
        recipient.transactions.append(tx)
        return tx

    def simulate_delivery(self, service_id: str, transaction_id: str) -> dict:
        """Delivery is real when using the gateway — return success stub."""
        service = self.services.get(service_id)
        if not service:
            return {"success": False, "quality": 0.0, "delivery_type": "not_found"}
        return {
            "success": True,
            "quality": service.quality_score,
            "delivery_type": "correct",
        }

    def get_agent_stats(self, agent_id: str) -> dict:
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
        total_volume = sum(
            tx.amount for tx in self.transactions if tx.status == "accepted"
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
            "rejected_volume": sum(
                tx.amount for tx in self.transactions if tx.status == "rejected"
            ),
        }

    def reset_daily_usage(self) -> None:
        for agent in self.agents.values():
            agent.spent_today = 0.0
