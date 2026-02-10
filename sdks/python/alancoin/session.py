"""
High-level session management for Alancoin agents.

Provides bounded spending sessions with automatic session key lifecycle.
This is the "3 lines of code" experience:

    from alancoin import Alancoin, Wallet

    client = Alancoin(api_key="ak_...", wallet=Wallet(private_key="0x..."))

    with client.session(max_total="5.00", max_per_tx="0.50") as s:
        result = s.call_service("translation", text="Hello", target="es")

Internally, session() creates a session key with the budget constraints,
call_service() discovers → selects → pays → calls in one step, and the
context manager revokes the session key on exit.
"""

import logging
import threading
from dataclasses import dataclass, field
from decimal import Decimal, InvalidOperation
from typing import TYPE_CHECKING, Callable, Dict, List, Optional, Union

import requests

logger = logging.getLogger(__name__)

from .exceptions import AlancoinError, ValidationError
from .session_keys import SessionKeyManager


def _parse_decimal(value: str, field_name: str = "amount") -> Decimal:
    """Safely parse a string to Decimal, raising ValidationError on failure."""
    try:
        return Decimal(value)
    except (InvalidOperation, TypeError, ValueError):
        raise ValidationError(f"Invalid {field_name}: {value!r} is not a valid decimal")

if TYPE_CHECKING:
    from .client import Alancoin
    from .models import ServiceListing


@dataclass
class Budget:
    """Spending constraints for a session.

    All amounts are in USDC (e.g., "5.00" = $5.00).

    Args:
        max_total: Maximum total spend for the entire session.
        max_per_tx: Maximum spend per individual transaction.
        max_per_day: Maximum daily spend (useful for long-running sessions).
        expires_in: Session duration (e.g., "1h", "24h", "7d").
        allowed_services: Restrict to these service types (e.g., ["translation"]).
        allowed_recipients: Restrict to these agent addresses.
    """

    max_total: str = "10.00"
    max_per_tx: str = "1.00"
    max_per_day: Optional[str] = None
    expires_in: str = "1h"
    allowed_services: Optional[List[str]] = None
    allowed_recipients: Optional[List[str]] = None


class ServiceResult:
    """Result of calling a service via call_service().

    Behaves like a dict for accessing response data, but also carries
    the transaction hash and the service that was called.
    """

    def __init__(
        self,
        data: dict,
        tx_hash: str = None,
        service: "ServiceListing" = None,
        escrow_id: str = None,
    ):
        self._data = data
        self.tx_hash = tx_hash
        self.service = service
        self.escrow_id = escrow_id

    def __getitem__(self, key):
        return self._data[key]

    def __contains__(self, key):
        return key in self._data

    def get(self, key, default=None):
        return self._data.get(key, default)

    def keys(self):
        return self._data.keys()

    def values(self):
        return self._data.values()

    def items(self):
        return self._data.items()

    def __repr__(self):
        svc = f", service={self.service.name}" if self.service else ""
        return f"ServiceResult({self._data}{svc})"


class BudgetSession:
    """A bounded spending session with automatic session key management.

    Created via ``client.session()``. Handles:
    - Session key creation with budget constraints
    - Service discovery and selection
    - Payment via session key (platform balance)
    - HTTP calls to service endpoints with payment proof
    - Session key revocation on exit

    Example::

        with client.session(max_total="5.00") as s:
            # Pay an agent directly
            s.pay("0xRecipient...", "0.50")

            # Or discover + pay + call in one step
            result = s.call_service("translation", text="Hello", target="es")

            # Check remaining budget
            print(f"Spent: ${s.total_spent}, Remaining: ${s.remaining}")
    """

    def __init__(self, client: "Alancoin", budget: Budget):
        self._client = client
        self._budget = budget
        self._skm: Optional[SessionKeyManager] = None
        self._key_id: Optional[str] = None
        self._total_spent = Decimal("0")
        self._tx_count = 0
        self._active = False
        self._lock = threading.Lock()

    # -- Properties -----------------------------------------------------------

    @property
    def total_spent(self) -> str:
        """Total USDC spent in this session so far."""
        return str(self._total_spent)

    @property
    def remaining(self) -> str:
        """USDC remaining in this session's budget."""
        return str(Decimal(self._budget.max_total) - self._total_spent)

    @property
    def tx_count(self) -> int:
        """Number of transactions executed in this session."""
        return self._tx_count

    @property
    def is_active(self) -> bool:
        """Whether the session is currently active."""
        return self._active

    @property
    def budget(self) -> Budget:
        """The budget constraints for this session."""
        return self._budget

    # -- Context manager ------------------------------------------------------

    def __enter__(self):
        self._start()
        return self

    def __exit__(self, exc_type, exc_val, exc_tb):
        self._stop()
        return False

    def _start(self):
        """Create session key and activate the session."""
        if not self._client.address:
            raise ValidationError(
                "Wallet required for sessions. Pass a Wallet to Alancoin()."
            )

        self._skm = SessionKeyManager()

        key = self._client.create_session_key(
            agent_address=self._client.address,
            public_key=self._skm.public_key,
            expires_in=self._budget.expires_in,
            max_per_transaction=self._budget.max_per_tx,
            max_per_day=self._budget.max_per_day,
            max_total=self._budget.max_total,
            allowed_service_types=self._budget.allowed_services,
            allowed_recipients=self._budget.allowed_recipients,
            allow_any=(
                self._budget.allowed_recipients is None
                and self._budget.allowed_services is None
            ),
            label=f"sdk-session-{self._budget.expires_in}",
        )

        self._key_id = key.get("id") or key.get("session", {}).get("id")
        self._skm.set_key_id(self._key_id)
        self._active = True

    def _stop(self):
        """Revoke session key and deactivate."""
        with self._lock:
            if not self._active:
                return
            self._active = False
        if self._key_id:
            try:
                self._client.revoke_session_key(
                    self._client.address, self._key_id
                )
            except Exception as e:
                logger.warning("Failed to revoke session key %s: %s", self._key_id, e)
        self._skm = None

    # -- Payment --------------------------------------------------------------

    def pay(self, to: str, amount: str, service_id: str = None) -> dict:
        """Pay an agent directly using this session's budget.

        Args:
            to: Recipient address.
            amount: Amount in USDC (e.g., "0.50").
            service_id: Optional service ID for tracking.

        Returns:
            Transaction result from the server.

        Raises:
            AlancoinError: If session inactive, budget exceeded, or payment fails.
        """
        if not self._active:
            raise AlancoinError("Session is not active", code="session_inactive")

        amount_dec = _parse_decimal(amount, "payment amount")
        if self._budget.max_per_tx and amount_dec > _parse_decimal(self._budget.max_per_tx, "max_per_tx"):
            raise AlancoinError(
                f"Payment of ${amount} exceeds per-transaction limit of ${self._budget.max_per_tx}",
                code="per_tx_limit_exceeded",
            )
        if self._total_spent + amount_dec > _parse_decimal(self._budget.max_total, "max_total"):
            raise AlancoinError(
                f"Payment of ${amount} would exceed session budget "
                f"(spent: ${self._total_spent}, limit: ${self._budget.max_total})",
                code="budget_exceeded",
            )

        result = self._skm.transact(
            self._client,
            self._client.address,
            to,
            amount,
            service_id=service_id,
        )

        self._total_spent += amount_dec
        self._tx_count += 1
        return result

    # -- Service calls --------------------------------------------------------

    def call_service(
        self,
        service_type: str,
        max_price: str = None,
        prefer: str = "cheapest",
        escrow: bool = True,
        **params,
    ) -> ServiceResult:
        """Discover, pay, and call a service in one step.

        1. Discovers services matching the type and price limit.
        2. Picks the best one (cheapest by default).
        3. Creates escrow (or direct payment if escrow=False).
        4. Calls the service endpoint with payment proof.
        5. Auto-confirms escrow on success.

        Args:
            service_type: Type of service ("translation", "inference", etc.).
            max_price: Max price in USDC (defaults to budget's max_per_tx).
            prefer: Selection strategy - "cheapest", "reputation", or "best_value".
            escrow: If True (default), use escrow for buyer protection.
                    If False, use direct fire-and-forget payment.
            **params: Parameters forwarded to the service endpoint.

        Returns:
            ServiceResult with response data, tx_hash, service info, and escrow_id.

        Raises:
            AlancoinError: If no services found, budget exceeded, or call fails.

        Example::

            result = session.call_service(
                "translation",
                text="Hello world",
                target="es",
            )
            print(result["output"])
            # Escrow was auto-confirmed on success
        """
        if not self._active:
            raise AlancoinError("Session is not active", code="session_inactive")

        # 1. Discover
        price_limit = max_price or self._budget.max_per_tx
        listings = self._client.discover(
            service_type=service_type,
            max_price=price_limit,
        )

        if not listings:
            raise AlancoinError(
                f"No {service_type} services found under ${price_limit}",
                code="no_services",
            )

        # 2. Select
        service = self._select_service(listings, prefer)

        # 3. Budget check
        price_dec = _parse_decimal(service.price, "service price")
        if self._budget.max_per_tx and price_dec > _parse_decimal(self._budget.max_per_tx, "max_per_tx"):
            raise AlancoinError(
                f"Service costs ${service.price} which exceeds per-transaction limit of ${self._budget.max_per_tx}",
                code="per_tx_limit_exceeded",
            )
        if self._total_spent + price_dec > _parse_decimal(self._budget.max_total, "max_total"):
            raise AlancoinError(
                f"Service costs ${service.price} but only "
                f"${self.remaining} remaining in budget",
                code="budget_exceeded",
            )

        if not escrow:
            # Fire-and-forget payment (original flow)
            tx_result = self.pay(
                to=service.agent_address,
                amount=service.price,
                service_id=service.id,
            )
            escrow_id = None
        else:
            # Escrow-protected payment
            escrow_resp = self._client.create_escrow(
                buyer_addr=self._client.address,
                seller_addr=service.agent_address,
                amount=service.price,
                service_id=service.id,
            )
            escrow_id = escrow_resp.get("escrow", {}).get("id")
            tx_result = {"escrowId": escrow_id, "amount": service.price}
            self._total_spent += price_dec
            self._tx_count += 1

        # 4. Call endpoint (if the service has one)
        if service.endpoint:
            response_data = self._call_endpoint(service, tx_result, params)

            # Auto-confirm escrow on successful endpoint call
            if escrow and escrow_id and "error" not in response_data:
                try:
                    self._client.confirm_escrow(escrow_id)
                except Exception as e:
                    logger.warning("Escrow confirmation failed for %s: %s - funds may be stuck", escrow_id, e)

            return ServiceResult(
                data=response_data,
                tx_hash=tx_result.get("txHash"),
                service=service,
                escrow_id=escrow_id,
            )

        # Auto-confirm escrow when there's no endpoint
        if escrow and escrow_id:
            try:
                self._client.confirm_escrow(escrow_id)
            except Exception as e:
                logger.warning("Escrow confirmation failed for %s: %s - funds may be stuck", escrow_id, e)

        # No endpoint — return payment confirmation
        return ServiceResult(
            data={
                "paid": True,
                "amount": service.price,
                "to": service.agent_address,
                "service": service.name,
                "tx": tx_result,
            },
            tx_hash=tx_result.get("txHash"),
            service=service,
            escrow_id=escrow_id,
        )

    def discover(
        self, service_type: str, max_price: str = None
    ) -> List["ServiceListing"]:
        """Discover services without paying. Useful for browsing first.

        Args:
            service_type: Type of service to find.
            max_price: Maximum price filter.

        Returns:
            List of ServiceListings sorted by price (cheapest first).
        """
        return self._client.discover(
            service_type=service_type,
            max_price=max_price or self._budget.max_per_tx,
        )

    # -- Contracts (service agreements) ----------------------------------------

    def propose_contract(
        self,
        seller_addr: str,
        service_type: str,
        price_per_call: str,
        budget: str,
        duration: str,
        **sla_params,
    ) -> dict:
        """Propose a service contract within this session's budget.

        Validates the contract budget fits within the session's remaining
        budget before submitting the proposal.

        Args:
            seller_addr: Seller's wallet address.
            service_type: Type of service (e.g., "translation").
            price_per_call: Price per call in USDC (e.g., "0.005").
            budget: Total contract budget in USDC (e.g., "1.00").
            duration: Contract duration (e.g., "7d", "24h").
            **sla_params: Optional SLA parameters (min_volume, seller_penalty,
                max_latency_ms, min_success_rate, sla_window_size).

        Returns:
            Contract proposal response.

        Raises:
            AlancoinError: If session is inactive.
            ValidationError: If budget exceeds session remaining.
        """
        if not self._active:
            raise AlancoinError("Session is not active", code="session_inactive")

        budget_dec = Decimal(budget)
        remaining = Decimal(self._budget.max_total) - self._total_spent
        if budget_dec > remaining:
            raise ValidationError(
                f"Contract budget {budget} exceeds session remaining {remaining}"
            )

        return self._client.propose_contract(
            buyer_addr=self._client.address,
            seller_addr=seller_addr,
            service_type=service_type,
            price_per_call=price_per_call,
            buyer_budget=budget,
            duration=duration,
            **sla_params,
        )

    # -- Pipeline (service composition) ----------------------------------------

    def pipeline(
        self,
        steps: List[Dict],
        prefer: str = "cheapest",
    ) -> List[ServiceResult]:
        """Chain multiple service calls where each step's output feeds the next.

        Each step is a dict with:
            - service_type (str): Required. Service type to call.
            - params (dict): Parameters for this step. Use ``"$prev"`` as a
              value to reference the previous step's ``"output"`` field, or
              ``"$prev.key"`` to reference a specific key.
            - max_price (str): Optional price limit for this step.

        Args:
            steps: List of step dicts describing the pipeline.
            prefer: Selection strategy for all steps.

        Returns:
            List of ServiceResults, one per step.

        Raises:
            AlancoinError: If any step fails or budget is exhausted.

        Example::

            results = session.pipeline([
                {"service_type": "inference", "params": {"text": doc, "task": "summarize"}},
                {"service_type": "translation", "params": {"text": "$prev", "target": "es"}},
                {"service_type": "inference", "params": {"text": "$prev", "task": "extract_entities"}},
            ])
            entities = results[-1]["output"]
        """
        if not self._active:
            raise AlancoinError("Session is not active", code="session_inactive")

        results: List[ServiceResult] = []
        prev_output = None

        for i, step in enumerate(steps):
            service_type = step.get("service_type")
            if not service_type:
                raise ValidationError(f"Step {i}: missing 'service_type'")

            params = dict(step.get("params", {}))

            # Resolve $prev references
            if prev_output is not None:
                params = self._resolve_refs(params, prev_output)

            result = self.call_service(
                service_type=service_type,
                max_price=step.get("max_price"),
                prefer=prefer,
                **params,
            )
            results.append(result)

            # Extract output for next step
            prev_output = result.get("output", result._data)

        return results

    @staticmethod
    def _resolve_refs(params: dict, prev_output) -> dict:
        """Replace $prev references with actual values from previous output."""
        resolved = {}
        for key, val in params.items():
            if isinstance(val, str):
                if val == "$prev":
                    resolved[key] = prev_output
                elif val.startswith("$prev."):
                    ref_key = val[len("$prev."):]
                    if isinstance(prev_output, dict):
                        if ref_key not in prev_output:
                            raise AlancoinError(
                                f"Pipeline reference '{val}' not found in previous output "
                                f"(available keys: {list(prev_output.keys())})",
                                code="pipeline_ref_error",
                            )
                        resolved[key] = prev_output[ref_key]
                    else:
                        raise AlancoinError(
                            f"Pipeline reference '{val}' requires dict output from previous step, "
                            f"got {type(prev_output).__name__}",
                            code="pipeline_ref_error",
                        )
                else:
                    resolved[key] = val
            else:
                resolved[key] = val
        return resolved

    # -- Delegation -----------------------------------------------------------

    def call_service_with_delegation(
        self,
        service_type: str,
        delegation_budget: str,
        max_price: str = None,
        prefer: str = "cheapest",
        **params,
    ) -> ServiceResult:
        """Call a service and pass delegation credentials for sub-hiring.

        Like ``call_service()`` but creates a child session key and passes
        it to the service agent via headers so the agent can further delegate
        work to other agents.

        Args:
            service_type: Type of service ("research", "analysis", etc.).
            delegation_budget: Max budget the service agent can spend on
                sub-tasks (must be within this session's remaining budget).
            max_price: Max price for this service (defaults to budget max_per_tx).
            prefer: Selection strategy.
            **params: Parameters forwarded to the service endpoint.

        Returns:
            ServiceResult with response data.
        """
        if not self._active:
            raise AlancoinError("Session is not active", code="session_inactive")

        # 1. Discover
        price_limit = max_price or self._budget.max_per_tx
        listings = self._client.discover(
            service_type=service_type,
            max_price=price_limit,
        )
        if not listings:
            raise AlancoinError(
                f"No {service_type} services found under ${price_limit}",
                code="no_services",
            )

        # 2. Select
        service = self._select_service(listings, prefer)

        # 3. Budget check (service price + delegation budget)
        price_dec = _parse_decimal(service.price, "service price")
        del_dec = _parse_decimal(delegation_budget, "delegation budget")
        total_needed = price_dec + del_dec
        if self._total_spent + total_needed > _parse_decimal(self._budget.max_total, "max_total"):
            raise AlancoinError(
                f"Service (${service.price}) + delegation (${delegation_budget}) "
                f"exceeds remaining budget (${self.remaining})",
                code="budget_exceeded",
            )

        # 4. Pay for the service
        tx_result = self.pay(
            to=service.agent_address,
            amount=service.price,
            service_id=service.id,
        )

        # 5. Create child session key for delegation
        child_skm = SessionKeyManager()
        signed = self._skm.sign_delegation(child_skm.public_key, delegation_budget)
        child_resp = self._client.create_child_session_key(
            parent_key_id=self._key_id,
            delegation_label=f"delegate:{service_type}",
            allowed_service_types=self._budget.allowed_services,
            allow_any=self._budget.allowed_services is None,
            **signed,
        )
        child_key = child_resp.get("childKey", {})
        child_key_id = child_key.get("id")

        # 6. Call endpoint with delegation context in body (not headers)
        if service.endpoint:
            import requests
            headers = {
                "Content-Type": "application/json",
                "X-Payment-TxHash": tx_result.get("txHash", ""),
                "X-Payment-Amount": service.price,
                "X-Payment-From": self._client.address,
            }
            body = dict(params)
            body["_delegation_key_id"] = child_key_id
            body["_delegation_budget"] = delegation_budget
            body["_delegation_private_key"] = child_skm.private_key
            body["_delegation_depth"] = child_key.get("depth", 1)
            try:
                resp = requests.post(
                    service.endpoint,
                    json=body,
                    headers=headers,
                    timeout=60,
                )
                resp.raise_for_status()
                return ServiceResult(
                    data=resp.json(),
                    tx_hash=tx_result.get("txHash"),
                    service=service,
                )
            except Exception as e:
                logger.warning("Delegation endpoint call failed: %s", e)
                return ServiceResult(
                    data={
                        "error": "endpoint_call_failed",
                        "paid": True,
                        "amount": service.price,
                    },
                    tx_hash=tx_result.get("txHash"),
                    service=service,
                )

        return ServiceResult(
            data={
                "paid": True,
                "amount": service.price,
                "to": service.agent_address,
                "delegation_key": child_key_id,
            },
            tx_hash=tx_result.get("txHash"),
            service=service,
        )

    # -- Dispute --------------------------------------------------------------

    def dispute(self, escrow_id: str, reason: str) -> dict:
        """Dispute an escrow, refunding funds to the buyer.

        Use this when a service returns garbage or fails to deliver.
        The seller's reputation is penalized.

        Args:
            escrow_id: The escrow ID from a ServiceResult.escrow_id.
            reason: Why the service was unsatisfactory.

        Returns:
            Updated escrow with status 'refunded'.
        """
        if not self._active:
            raise AlancoinError("Session is not active", code="session_inactive")
        return self._client.dispute_escrow(escrow_id, reason)

    # -- Credit-aware balance -------------------------------------------------

    def get_effective_balance(self) -> str:
        """Get effective balance including available credit.

        Returns the sum of available platform balance plus unused credit line.
        This is the total amount the agent could spend right now.

        Returns:
            Effective balance as a string (e.g., "55.00").
        """
        balance_resp = self._client.get_platform_balance(self._client.address)
        balance = balance_resp.get("balance", {})
        available = Decimal(balance.get("available", "0"))
        credit_limit = Decimal(balance.get("creditLimit", "0"))
        credit_used = Decimal(balance.get("creditUsed", "0"))
        return str(available + (credit_limit - credit_used))

    # -- Internals ------------------------------------------------------------

    def _select_service(
        self, listings: List["ServiceListing"], strategy: str
    ) -> "ServiceListing":
        """Pick the best service from discovery results.

        Strategies:
            cheapest: Lowest price (default).
            reputation: Highest reputation score.
            best_value: Best reputation-to-price ratio.
        """
        if not listings:
            raise AlancoinError("No services to select from", code="no_services")
        if strategy == "reputation":
            return max(listings, key=lambda s: s.reputation_score)
        if strategy == "best_value":
            def value_key(s):
                price = Decimal(s.price) if s.price else Decimal("999")
                if price <= 0:
                    price = Decimal("0.000001")
                return float(s.reputation_score) / float(price)
            return max(listings, key=value_key)
        # Default: cheapest
        return min(listings, key=lambda s: Decimal(s.price))

    def _call_endpoint(
        self,
        service: "ServiceListing",
        tx_result: dict,
        params: dict,
    ) -> dict:
        """Call a service's HTTP endpoint with payment proof headers."""
        headers = {
            "Content-Type": "application/json",
            "X-Payment-TxHash": tx_result.get("txHash", ""),
            "X-Payment-Amount": service.price,
            "X-Payment-From": self._client.address,
        }

        try:
            resp = requests.post(
                service.endpoint,
                json=params,
                headers=headers,
                timeout=30,
            )
            resp.raise_for_status()
            try:
                return resp.json()
            except ValueError:
                return {"raw_response": resp.text, "paid": True, "amount": service.price}
        except requests.exceptions.RequestException as e:
            logger.warning(
                "Service endpoint call failed for %s: %s", service.endpoint, e
            )
            # Payment was already made — return error context (no internal details)
            return {
                "error": "endpoint_call_failed",
                "paid": True,
                "amount": service.price,
                "endpoint": service.endpoint,
                "note": "Payment succeeded but endpoint call failed",
            }


@dataclass
class StreamResult:
    """Result of a tick in a streaming session.

    Carries the tick data plus running totals.
    """

    tick: dict
    stream: dict
    tick_count: int
    spent: str
    remaining: str

    def __repr__(self):
        return (
            f"StreamResult(tick={self.tick_count}, "
            f"spent=${self.spent}, remaining=${self.remaining})"
        )


class StreamingSession:
    """A streaming micropayment session for continuous services.

    Opens a payment stream with a held amount, then delivers value
    via ticks. On close (or context exit), the spent amount settles
    to the seller and the unused hold returns to the buyer.

    Example::

        with client.stream(
            seller_addr="0xSeller...",
            hold_amount="1.00",
            price_per_tick="0.0001",
        ) as stream:
            for token in generate_tokens():
                result = stream.tick(metadata=f"token:{token}")
                if result.remaining == "0.000000":
                    break  # Budget exhausted

        # Stream auto-closed, unused funds returned
    """

    def __init__(
        self,
        client: "Alancoin",
        seller_addr: str,
        hold_amount: str,
        price_per_tick: str,
        service_id: str = None,
        stale_timeout_secs: int = 60,
    ):
        self._client = client
        self._seller_addr = seller_addr
        self._hold_amount = hold_amount
        self._price_per_tick = price_per_tick
        self._service_id = service_id
        self._stale_timeout_secs = stale_timeout_secs
        self._stream_id: Optional[str] = None
        self._tick_count = 0
        self._spent = Decimal("0")
        self._active = False

    # -- Properties -----------------------------------------------------------

    @property
    def stream_id(self) -> Optional[str]:
        """The server-assigned stream ID."""
        return self._stream_id

    @property
    def tick_count(self) -> int:
        """Number of ticks recorded so far."""
        return self._tick_count

    @property
    def spent(self) -> str:
        """Total USDC spent so far."""
        return str(self._spent)

    @property
    def remaining(self) -> str:
        """USDC remaining in the hold."""
        return str(Decimal(self._hold_amount) - self._spent)

    @property
    def is_active(self) -> bool:
        """Whether the stream is currently open."""
        return self._active

    # -- Context manager ------------------------------------------------------

    def __enter__(self):
        self._open()
        return self

    def __exit__(self, exc_type, exc_val, exc_tb):
        if self._active:
            self.close()
        return False

    def _open(self):
        """Open the payment stream."""
        if not self._client.address:
            raise ValidationError(
                "Wallet required for streams. Pass a Wallet to Alancoin()."
            )

        resp = self._client.open_stream(
            buyer_addr=self._client.address,
            seller_addr=self._seller_addr,
            hold_amount=self._hold_amount,
            price_per_tick=self._price_per_tick,
            service_id=self._service_id,
            stale_timeout_secs=self._stale_timeout_secs,
        )

        stream = resp.get("stream", {})
        self._stream_id = stream.get("id")
        self._active = True
        logger.info(
            "Stream opened: %s (hold=%s, tick=%s)",
            self._stream_id,
            self._hold_amount,
            self._price_per_tick,
        )

    # -- Tick -----------------------------------------------------------------

    def tick(
        self,
        amount: str = None,
        metadata: str = None,
    ) -> StreamResult:
        """Record a micropayment tick.

        Args:
            amount: Tick amount (omit to use price_per_tick).
            metadata: Optional payload (e.g., token count, chunk ID).

        Returns:
            StreamResult with tick data and running totals.

        Raises:
            AlancoinError: If stream is closed or hold is exhausted.
        """
        if not self._active:
            raise AlancoinError("Stream is not active", code="stream_inactive")

        resp = self._client.tick_stream(
            stream_id=self._stream_id,
            amount=amount,
            metadata=metadata,
        )

        tick_data = resp.get("tick", {})
        stream_data = resp.get("stream", {})

        self._tick_count = stream_data.get("tickCount", self._tick_count + 1)
        self._spent = Decimal(stream_data.get("spentAmount", str(self._spent)))

        return StreamResult(
            tick=tick_data,
            stream=stream_data,
            tick_count=self._tick_count,
            spent=str(self._spent),
            remaining=self.remaining,
        )

    # -- Close ----------------------------------------------------------------

    def close(self, reason: str = None) -> dict:
        """Close the stream, settling funds.

        The spent amount goes to the seller. The unused hold returns
        to the buyer.

        Args:
            reason: Optional close reason.

        Returns:
            Settled stream with final amounts.
        """
        if not self._active:
            raise AlancoinError("Stream is not active", code="stream_inactive")

        resp = self._client.close_stream(
            stream_id=self._stream_id,
            reason=reason,
        )
        self._active = False

        stream = resp.get("stream", {})
        logger.info(
            "Stream closed: %s (spent=%s, ticks=%d)",
            self._stream_id,
            stream.get("spentAmount", self.spent),
            self._tick_count,
        )
        return resp

    # -- Info -----------------------------------------------------------------

    def get_ticks(self, limit: int = 100) -> list:
        """Get the tick history for this stream.

        Args:
            limit: Maximum ticks to return.

        Returns:
            List of tick records.
        """
        resp = self._client.list_stream_ticks(self._stream_id, limit=limit)
        return resp.get("ticks", [])

    def refresh(self) -> dict:
        """Refresh stream state from the server.

        Returns:
            Current stream data.
        """
        resp = self._client.get_stream(self._stream_id)
        stream = resp.get("stream", {})
        self._tick_count = stream.get("tickCount", self._tick_count)
        self._spent = Decimal(stream.get("spentAmount", str(self._spent)))
        self._active = stream.get("status") == "open"
        return stream
