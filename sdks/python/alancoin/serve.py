"""
Framework for creating Alancoin service agents.

Sell a service in 5 lines:

    from alancoin.serve import ServiceAgent

    agent = ServiceAgent(name="TranslatorBot", base_url="http://localhost:8080")

    @agent.service("translation", price="0.005", description="Translate text")
    def translate(text, target="es"):
        return {"output": f"[{target}] {text}"}

    agent.serve(port=5001)

The agent auto-registers on the Alancoin platform, starts an HTTP server
with x402 payment verification, and routes requests to your handler functions.
Buyers call your service via ``session.call_service("translation", ...)``.
"""

import inspect
import json
import logging
import threading
from http.server import HTTPServer, BaseHTTPRequestHandler
from socketserver import ThreadingMixIn
from dataclasses import dataclass
from typing import Callable, Dict, List, Optional

from .client import Alancoin
from .session_keys import generate_session_keypair, SessionKeyManager

logger = logging.getLogger("alancoin.serve")


@dataclass
class DelegationContext:
    """Context passed to service handlers that support delegation.

    When a service agent receives a request with delegation headers,
    a ``DelegationContext`` is injected into the handler (if it accepts
    a ``ctx`` parameter).  The service can then autonomously hire other
    agents within the delegated budget.

    Example::

        @agent.service("research", price="0.02")
        def research(text, ctx: DelegationContext = None):
            if ctx:
                result = ctx.delegate("translation", max_budget="0.005", text=text, target="es")
                return {"output": result["output"]}
            return {"output": text}
    """

    client: Alancoin
    parent_skm: SessionKeyManager
    parent_key_id: str
    owner_address: str
    remaining_budget: str
    depth: int

    def delegate(
        self,
        service_type: str,
        max_budget: str,
        prefer: str = "cheapest",
        **params,
    ) -> dict:
        """Discover, pay, and call another agent within the delegated budget.

        1. Discovers services matching type + budget
        2. Selects by preference
        3. Creates child session key (signed by parent key)
        4. Pays the selected service via child key
        5. Calls the service endpoint with payment proof
        6. Returns response

        Args:
            service_type: Type of service to call.
            max_budget: Maximum USDC to allocate for this sub-task.
            prefer: Selection strategy ("cheapest", "reputation", "best_value").
            **params: Parameters forwarded to the service endpoint.

        Returns:
            Service response dict.
        """
        import requests as _requests
        from decimal import Decimal

        # 1. Discover
        listings = self.client.discover(
            service_type=service_type,
            max_price=max_budget,
        )
        if not listings:
            raise ValueError(f"No {service_type} services found under ${max_budget}")

        # 2. Select
        if prefer == "reputation":
            service = max(listings, key=lambda s: s.reputation_score)
        elif prefer == "best_value":
            def value_key(s):
                price = Decimal(s.price) if s.price else Decimal("999")
                if price <= 0:
                    price = Decimal("0.000001")
                return float(s.reputation_score) / float(price)
            service = max(listings, key=value_key)
        else:
            service = min(listings, key=lambda s: Decimal(s.price))

        # 3. Create child session key
        child_skm = SessionKeyManager()
        signed = self.parent_skm.sign_delegation(child_skm.public_key, max_budget)
        child_resp = self.client.create_child_session_key(
            parent_key_id=self.parent_key_id,
            delegation_label=f"delegate:{service_type}",
            allowed_service_types=[service_type],
            **signed,
        )
        child_key = child_resp.get("childKey", {})
        child_key_id = child_key.get("id")
        child_skm.set_key_id(child_key_id)

        # 4. Pay
        tx_result = child_skm.transact(
            self.client,
            self.owner_address,
            service.agent_address,
            service.price,
            service_id=service.id,
        )

        # 5. Call endpoint
        if service.endpoint:
            headers = {
                "Content-Type": "application/json",
                "X-Payment-TxHash": tx_result.get("txHash", ""),
                "X-Payment-Amount": service.price,
                "X-Payment-From": self.owner_address,
            }
            try:
                resp = _requests.post(
                    service.endpoint,
                    json=params,
                    headers=headers,
                    timeout=30,
                )
                resp.raise_for_status()
                return resp.json()
            except _requests.exceptions.RequestException as e:
                logger.warning("Delegate endpoint call failed: %s", e)
                return {
                    "error": "endpoint_call_failed",
                    "paid": True,
                    "amount": service.price,
                }

        return {
            "paid": True,
            "amount": service.price,
            "to": service.agent_address,
            "service": service.name,
        }


@dataclass
class ServiceDef:
    """Definition of a registered service."""

    service_type: str
    name: str
    price: str
    description: str
    handler: Callable
    service_id: Optional[str] = None


class _ThreadingHTTPServer(ThreadingMixIn, HTTPServer):
    """HTTPServer that handles each request in a new thread."""

    daemon_threads = True


class ServiceAgent:
    """Framework for creating Alancoin service agents.

    A ``ServiceAgent`` wraps an HTTP server that:
    - Auto-registers as an agent on the Alancoin platform
    - Registers each decorated function as a priced service
    - Serves requests at ``/services/{type}`` with x402 payment gating
    - Returns 402 with payment details when no proof is provided
    - Routes paid requests to your handler and returns JSON

    Example::

        agent = ServiceAgent(name="TranslatorBot")

        @agent.service("translation", price="0.005")
        def translate(text, target="es"):
            return {"output": f"[{target}] {text}"}

        agent.serve(port=5001)
    """

    def __init__(
        self,
        name: str,
        base_url: str = "http://localhost:8080",
        api_key: str = None,
        address: str = None,
        description: str = "",
        enable_delegation: bool = False,
    ):
        """
        Args:
            name: Agent display name.
            base_url: Alancoin platform URL.
            api_key: API key (if already registered).
            address: Wallet address (auto-generated if omitted).
            description: What this agent does.
            enable_delegation: If True, inject DelegationContext into handlers
                that accept a ``ctx`` parameter when delegation headers are present.
        """
        self.name = name
        self.description = description
        self._base_url = base_url
        self._api_key = api_key
        self._address = address
        self._enable_delegation = enable_delegation
        self._services: Dict[str, ServiceDef] = {}
        self._client: Optional[Alancoin] = None
        self._server: Optional[_ThreadingHTTPServer] = None
        self._thread: Optional[threading.Thread] = None
        self._endpoint_base: str = ""

    # -- Decorator ------------------------------------------------------------

    def service(
        self,
        service_type: str,
        price: str,
        name: str = None,
        description: str = "",
    ):
        """Register a function as a priced service.

        The decorated function receives request parameters as keyword
        arguments and returns a dict (serialized to JSON).

        Args:
            service_type: Service category ("translation", "inference", etc.).
            price: Price in USDC per call (e.g., "0.005").
            name: Display name (defaults to function name, title-cased).
            description: What this service does.

        Example::

            @agent.service("translation", price="0.005")
            def translate(text, target="es"):
                return {"output": f"[{target}] {text}"}
        """

        def decorator(func):
            svc_name = name or func.__name__.replace("_", " ").title()
            self._services[service_type] = ServiceDef(
                service_type=service_type,
                name=svc_name,
                price=price,
                description=description or func.__doc__ or "",
                handler=func,
            )
            return func

        return decorator

    # -- Server ---------------------------------------------------------------

    def serve(self, host: str = "0.0.0.0", port: int = 5001):
        """Start the agent (blocking).

        Registers on the platform, adds services, and starts the HTTP server.

        Args:
            host: Bind address.
            port: Bind port.
        """
        self._boot(host, port)

        print(f"\n  {self.name} serving at http://localhost:{port}")
        print(f"  Address: {self._address}")
        print(f"  Services:")
        for stype, svc in self._services.items():
            print(f"    {stype}: {svc.name} @ ${svc.price} USDC")
        print()

        try:
            self._server.serve_forever()
        except KeyboardInterrupt:
            print(f"\n  {self.name} stopped")
            self._server.shutdown()

    def start(self, host: str = "0.0.0.0", port: int = 5001):
        """Start the agent in a background thread (non-blocking).

        Returns immediately. Call ``stop()`` to shut down.

        Args:
            host: Bind address.
            port: Bind port.
        """
        self._boot(host, port)
        self._thread = threading.Thread(
            target=self._server.serve_forever, daemon=True
        )
        self._thread.start()

    def stop(self):
        """Stop a background agent started with ``start()``."""
        if self._server:
            self._server.shutdown()
            self._server = None
        if self._thread:
            self._thread.join(timeout=5)
            self._thread = None

    # -- Properties -----------------------------------------------------------

    @property
    def address(self) -> str:
        """Agent's wallet address."""
        return self._address or ""

    @property
    def services(self) -> List[str]:
        """Registered service types."""
        return list(self._services.keys())

    # -- Internals ------------------------------------------------------------

    def _boot(self, host: str, port: int):
        """Register on platform and start HTTP server."""
        self._endpoint_base = f"http://localhost:{port}"
        self._client = Alancoin(
            base_url=self._base_url, api_key=self._api_key
        )
        self._register()

        handler_cls = self._make_handler()
        self._server = _ThreadingHTTPServer((host, port), handler_cls)

    def _register(self):
        """Register this agent and its services on the platform."""
        if not self._address:
            _, self._address = generate_session_keypair()

        if not self._api_key:
            try:
                result = self._client.register(
                    address=self._address,
                    name=self.name,
                    description=self.description,
                )
                self._api_key = result.get("apiKey")
                if self._api_key:
                    self._client.api_key = self._api_key
                    self._client._session.headers[
                        "Authorization"
                    ] = f"Bearer {self._api_key}"
            except Exception as e:
                logger.warning("Agent registration: %s", e)

        for stype, svc in self._services.items():
            endpoint = f"{self._endpoint_base}/services/{stype}"
            try:
                result = self._client.add_service(
                    agent_address=self._address,
                    service_type=stype,
                    name=svc.name,
                    price=svc.price,
                    description=svc.description,
                    endpoint=endpoint,
                )
                if hasattr(result, "id"):
                    svc.service_id = result.id
            except Exception as e:
                logger.warning("Service registration (%s): %s", stype, e)

    def _make_handler(self):
        """Build the HTTP request handler class."""
        agent = self

        class _Handler(BaseHTTPRequestHandler):
            def do_POST(self):
                parts = self.path.rstrip("/").split("/")
                if len(parts) >= 3 and parts[1] == "services":
                    stype = parts[2]
                    if stype in agent._services:
                        self._handle_service(stype)
                        return
                self._json(404, {"error": "not_found"})

            def do_GET(self):
                if self.path.rstrip("/") == "/health":
                    self._json(
                        200,
                        {
                            "status": "ok",
                            "agent": agent.name,
                            "address": agent._address,
                            "services": list(agent._services.keys()),
                        },
                    )
                    return
                self._json(404, {"error": "not_found"})

            def _handle_service(self, stype):
                svc = agent._services[stype]

                # Parse body
                length = int(self.headers.get("Content-Length", 0))
                raw = self.rfile.read(length) if length > 0 else b"{}"
                try:
                    params = json.loads(raw)
                except json.JSONDecodeError:
                    self._json(400, {"error": "invalid_json"})
                    return

                # Check payment proof
                tx_hash = self.headers.get("X-Payment-TxHash", "").strip()
                if not tx_hash:
                    self._json(
                        402,
                        {
                            "price": svc.price,
                            "currency": "USDC",
                            "recipient": agent._address,
                            "description": svc.description,
                        },
                    )
                    return

                # Validate tx_hash format (must look like a hex hash, not arbitrary string)
                stripped = tx_hash.removeprefix("0x")
                if not stripped or not all(c in "0123456789abcdefABCDEF" for c in stripped):
                    self._json(400, {"error": "invalid_tx_hash", "message": "Invalid transaction hash format"})
                    return

                # Verify the payment against the platform if a client is available
                if agent._client:
                    try:
                        txns = agent._client.transactions(agent._address, limit=50)
                        if not any(t.tx_hash == tx_hash for t in txns):
                            self._json(402, {
                                "error": "payment_not_found",
                                "message": "Transaction not found on platform",
                                "price": svc.price,
                                "currency": "USDC",
                                "recipient": agent._address,
                            })
                            return
                    except Exception as e:
                        logger.warning("Payment verification failed for tx %s: %s", tx_hash[:16], e)
                        self._json(503, {
                            "error": "verification_unavailable",
                            "message": "Payment verification service is unavailable. Please retry.",
                        })
                        return

                # Build delegation context if enabled and headers present
                delegation_ctx = None
                if agent._enable_delegation:
                    del_key_id = self.headers.get("X-Delegation-KeyId", "").strip()
                    del_budget = self.headers.get("X-Delegation-Budget", "").strip()
                    del_private_key = self.headers.get("X-Delegation-PrivateKey", "").strip()
                    del_depth = self.headers.get("X-Delegation-Depth", "0").strip()
                    if del_key_id and del_budget and del_private_key:
                        delegation_ctx = DelegationContext(
                            client=agent._client,
                            parent_skm=SessionKeyManager(private_key=del_private_key),
                            parent_key_id=del_key_id,
                            owner_address=self.headers.get("X-Payment-From", ""),
                            remaining_budget=del_budget,
                            depth=int(del_depth),
                        )

                # Invoke handler
                try:
                    result = _call_handler(svc.handler, params, delegation_ctx)
                except Exception as e:
                    logger.exception("Service handler error for %s", stype)
                    self._json(500, {"error": "internal_error", "message": "Service handler failed"})
                    return

                self._json(200, result)

            def _json(self, status, data):
                body = json.dumps(data).encode()
                self.send_response(status)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)

            def log_message(self, fmt, *args):
                # Suppress default stderr logging
                pass

        return _Handler


def _call_handler(handler: Callable, params: dict, delegation_ctx=None):
    """Call a handler with kwargs or a single dict argument.

    If the handler accepts a ``ctx`` parameter and a delegation context is
    available, it is injected automatically.
    """
    sig = inspect.signature(handler)
    param_names = list(sig.parameters.keys())

    # Inject delegation context if handler accepts 'ctx'
    if delegation_ctx is not None and "ctx" in param_names:
        params = dict(params)
        params["ctx"] = delegation_ctx

    # If handler takes a single "request"-like param, pass the dict
    if (
        len(param_names) == 1
        and param_names[0] in ("request", "req", "data", "params", "body")
    ):
        return handler(params)

    # Otherwise unpack as kwargs
    try:
        return handler(**params)
    except TypeError:
        # Fallback: pass as positional dict
        return handler(params)
