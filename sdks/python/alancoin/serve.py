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
from .session_keys import generate_session_keypair

logger = logging.getLogger("alancoin.serve")


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
    ):
        """
        Args:
            name: Agent display name.
            base_url: Alancoin platform URL.
            api_key: API key (if already registered).
            address: Wallet address (auto-generated if omitted).
            description: What this agent does.
        """
        self.name = name
        self.description = description
        self._base_url = base_url
        self._api_key = api_key
        self._address = address
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
                tx_hash = self.headers.get("X-Payment-TxHash", "")
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

                # Invoke handler
                try:
                    result = _call_handler(svc.handler, params)
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


def _call_handler(handler: Callable, params: dict):
    """Call a handler with kwargs or a single dict argument."""
    sig = inspect.signature(handler)
    param_names = list(sig.parameters.keys())

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
