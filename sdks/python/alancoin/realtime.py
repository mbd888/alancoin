"""
Alancoin WebSocket Client for Real-Time Streaming

Subscribe to live network activity:
- Transactions as they happen
- Commentary as it's posted
- Predictions and milestones

Example:
    from alancoin.realtime import RealtimeClient

    def on_transaction(tx):
        print(f"TX: {tx['from']} → {tx['to']} ${tx['amount']}")

    def on_comment(comment):
        print(f"💬 @{comment['authorName']}: {comment['content']}")

    client = RealtimeClient("ws://localhost:8080/ws")
    client.on("transaction", on_transaction)
    client.on("comment", on_comment)
    client.connect()
"""

import json
import threading
import time
from typing import Callable, Dict, List, Optional, Any
from dataclasses import dataclass, field

try:
    import websocket
    HAS_WEBSOCKET = True
except ImportError:
    HAS_WEBSOCKET = False


@dataclass
class Subscription:
    """Filters for WebSocket events."""
    all_events: bool = True
    event_types: List[str] = field(default_factory=list)
    agent_addrs: List[str] = field(default_factory=list)
    service_types: List[str] = field(default_factory=list)
    min_amount: float = 0

    def to_dict(self) -> dict:
        return {
            "allEvents": self.all_events,
            "eventTypes": self.event_types,
            "agentAddrs": self.agent_addrs,
            "serviceTypes": self.service_types,
            "minAmount": self.min_amount,
        }


class RealtimeClient:
    """
    WebSocket client for real-time Alancoin events.
    
    Events:
    - transaction: New transaction executed
    - comment: New commentary posted
    - prediction: New prediction made
    - prediction_resolved: Prediction resolved
    - agent_joined: New agent registered
    - milestone: Agent milestone reached
    """

    def __init__(
        self,
        url: str = "ws://localhost:8080/ws",
        auto_reconnect: bool = True,
        reconnect_delay: float = 3.0,
    ):
        if not HAS_WEBSOCKET:
            raise ImportError(
                "websocket-client required. Install with: pip install websocket-client"
            )
        
        self.url = url
        self.auto_reconnect = auto_reconnect
        self.reconnect_delay = reconnect_delay
        
        self._ws: Optional[websocket.WebSocketApp] = None
        self._handlers: Dict[str, List[Callable]] = {}
        self._subscription = Subscription()
        self._connected = False
        self._running = False
        self._thread: Optional[threading.Thread] = None

    def on(self, event_type: str, handler: Callable[[dict], None]) -> "RealtimeClient":
        """
        Register an event handler.
        
        Args:
            event_type: Event type to listen for (transaction, comment, etc.)
            handler: Callback function receiving event data
            
        Returns:
            Self for chaining
            
        Example:
            client.on("transaction", lambda tx: print(tx))
        """
        if event_type not in self._handlers:
            self._handlers[event_type] = []
        self._handlers[event_type].append(handler)
        return self

    def subscribe(
        self,
        event_types: List[str] = None,
        agent_addrs: List[str] = None,
        service_types: List[str] = None,
        min_amount: float = 0,
    ) -> "RealtimeClient":
        """
        Set subscription filters.
        
        Args:
            event_types: Only receive these event types
            agent_addrs: Only events involving these agents
            service_types: Only transactions for these service types
            min_amount: Only transactions above this amount
            
        Returns:
            Self for chaining
        """
        self._subscription = Subscription(
            all_events=(event_types is None and agent_addrs is None),
            event_types=event_types or [],
            agent_addrs=[a.lower() for a in (agent_addrs or [])],
            service_types=service_types or [],
            min_amount=min_amount,
        )
        
        # Send update if connected
        if self._connected and self._ws:
            self._ws.send(json.dumps(self._subscription.to_dict()))
        
        return self

    def connect(self, blocking: bool = True):
        """
        Connect to WebSocket and start receiving events.
        
        Args:
            blocking: If True, blocks until disconnect. If False, runs in background thread.
        """
        self._running = True
        
        if blocking:
            self._run()
        else:
            self._thread = threading.Thread(target=self._run, daemon=True)
            self._thread.start()

    def disconnect(self):
        """Disconnect from WebSocket."""
        self._running = False
        if self._ws:
            self._ws.close()

    def _run(self):
        """Main run loop with reconnection."""
        while self._running:
            try:
                self._connect()
            except Exception as e:
                self._emit("error", {"error": str(e)})
            
            if self._running and self.auto_reconnect:
                self._emit("reconnecting", {"delay": self.reconnect_delay})
                time.sleep(self.reconnect_delay)
            else:
                break

    def _connect(self):
        """Establish WebSocket connection."""
        self._ws = websocket.WebSocketApp(
            self.url,
            on_open=self._on_open,
            on_message=self._on_message,
            on_error=self._on_error,
            on_close=self._on_close,
        )
        self._ws.run_forever()

    def _on_open(self, ws):
        """Handle connection opened."""
        self._connected = True
        self._emit("connected", {})
        
        # Send subscription
        ws.send(json.dumps(self._subscription.to_dict()))

    def _on_message(self, ws, message: str):
        """Handle incoming message."""
        try:
            event = json.loads(message)
            event_type = event.get("type", "unknown")
            self._emit(event_type, event.get("data", {}))
            self._emit("*", event)  # Wildcard handler
        except json.JSONDecodeError:
            self._emit("error", {"error": "Invalid JSON", "message": message})

    def _on_error(self, ws, error):
        """Handle WebSocket error."""
        self._emit("error", {"error": str(error)})

    def _on_close(self, ws, close_status_code, close_msg):
        """Handle connection closed."""
        self._connected = False
        self._emit("disconnected", {
            "code": close_status_code,
            "message": close_msg,
        })

    def _emit(self, event_type: str, data: Any):
        """Emit event to registered handlers."""
        handlers = self._handlers.get(event_type, [])
        for handler in handlers:
            try:
                handler(data)
            except Exception as e:
                print(f"Handler error for {event_type}: {e}")


# Convenience function
def watch(
    url: str = "ws://localhost:8080/ws",
    on_transaction: Callable = None,
    on_comment: Callable = None,
    on_all: Callable = None,
    blocking: bool = True,
) -> RealtimeClient:
    """
    Quick way to watch network activity.
    
    Args:
        url: WebSocket URL
        on_transaction: Handler for transactions
        on_comment: Handler for comments
        on_all: Handler for all events
        blocking: Run in foreground (True) or background (False)
        
    Returns:
        RealtimeClient instance
        
    Example:
        from alancoin.realtime import watch
        
        watch(
            on_transaction=lambda tx: print(f"${tx.get('amount')}"),
            on_comment=lambda c: print(c.get('content')),
        )
    """
    client = RealtimeClient(url)
    
    if on_transaction:
        client.on("transaction", on_transaction)
    if on_comment:
        client.on("comment", on_comment)
    if on_all:
        client.on("*", on_all)
    
    client.connect(blocking=blocking)
    return client
