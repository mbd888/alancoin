"""Alancoin API client."""

from typing import List, Optional, TYPE_CHECKING
from urllib.parse import urljoin

import re

import requests

from .models import Agent, Service, ServiceListing, Transaction, NetworkStats
from .exceptions import (
    AlancoinError,
    AgentNotFoundError,
    AgentExistsError,
    ServiceNotFoundError,
    PaymentRequiredError,
    NetworkError,
    ValidationError,
    PaymentError,
)

if TYPE_CHECKING:
    from .session import Budget, BudgetSession


_DURATION_RE = re.compile(r"^(\d+)\s*(s|m|h|d)$", re.IGNORECASE)
_DURATION_UNITS = {"s": 1, "m": 60, "h": 3600, "d": 86400}


def _parse_duration_to_secs(s: str) -> int:
    """Parse a duration string like '1h', '30m', '2d' to seconds."""
    m = _DURATION_RE.match(s.strip())
    if not m:
        try:
            return int(s)
        except ValueError:
            return 3600  # default 1h
    return int(m.group(1)) * _DURATION_UNITS[m.group(2).lower()]


class Alancoin:
    """
    Alancoin client for agent registration, discovery, and payments.

    Quickstart (gateway session)::

        from alancoin import Alancoin

        client = Alancoin("http://localhost:8080", api_key="ak_...")

        with client.gateway(max_total="5.00") as gw:
            result = gw.call("translation", text="Hello", target="es")

    Advanced (client-side session keys)::

        with client.session(max_total="5.00", max_per_tx="0.50") as s:
            result = s.call_service("translation", text="Hello", target="es")
    """
    
    def __init__(
        self,
        base_url: str = "http://localhost:8080",
        api_key: Optional[str] = None,
        timeout: int = 30,
    ):
        """
        Initialize the Alancoin client.

        Args:
            base_url: Alancoin API URL
            api_key: API key for authentication (optional for now)
            timeout: Request timeout in seconds
        """
        self.base_url = base_url.rstrip("/")
        self.api_key = api_key
        self.timeout = timeout
        self._session = requests.Session()
        
        if api_key:
            self._session.headers["Authorization"] = f"Bearer {api_key}"
        
        self._session.headers["Content-Type"] = "application/json"
        self._session.headers["User-Agent"] = "alancoin-python/0.1.0"

    # -------------------------------------------------------------------------
    # Platform Info
    # -------------------------------------------------------------------------

    def get_platform_info(self) -> dict:
        """
        Get platform information including deposit address.
        
        Returns:
            Platform info including:
            - platform.depositAddress: Where to send USDC deposits
            - platform.chain: Blockchain network
            - platform.usdcContract: USDC token contract address
            - instructions: How to deposit, withdraw, spend
            
        Example:
            info = client.get_platform_info()
            deposit_addr = info['platform']['depositAddress']
            print(f"Send USDC to: {deposit_addr}")
        """
        return self._request("GET", "/v1/platform")

    # -------------------------------------------------------------------------
    # Agent Operations
    # -------------------------------------------------------------------------

    def register(
        self,
        address: str,
        name: str,
        description: str = "",
        owner_address: str = "",
        endpoint: str = "",
    ) -> dict:
        """
        Register a new agent in the network.
        
        Returns a dict containing:
        - agent: The registered Agent object
        - apiKey: API key for authentication (STORE THIS - only shown once)
        - keyId: ID of the API key
        - usage: How to use the API key
        
        Args:
            address: Agent's wallet address (0x...)
            name: Human-readable name
            description: What this agent does
            owner_address: Owner's wallet (for session key agents)
            endpoint: x402-compatible API endpoint
            
        Returns:
            Dict with 'agent' and 'apiKey'
            
        Raises:
            AgentExistsError: If agent already registered
            ValidationError: If address is invalid
            
        Example:
            result = client.register(
                address="0x...",
                name="MyAgent"
            )
            print(f"Agent registered: {result['agent'].name}")
            print(f"API Key (save this!): {result['apiKey']}")
            
            # Use the API key for future requests
            client = Alancoin(api_key=result['apiKey'])
        """
        data = {
            "address": address,
            "name": name,
            "description": description,
        }
        if owner_address:
            data["ownerAddress"] = owner_address
        if endpoint:
            data["endpoint"] = endpoint
            
        response = self._request("POST", "/v1/agents", json=data)
        
        # Return both agent and API key
        result = {
            "agent": Agent.from_dict(response.get("agent", response)),
            "apiKey": response.get("apiKey"),
            "keyId": response.get("keyId"),
            "usage": response.get("usage"),
        }
        
        # Auto-configure this client with the new API key
        if result["apiKey"] and not self.api_key:
            self.api_key = result["apiKey"]
            self._session.headers["Authorization"] = f"Bearer {result['apiKey']}"
        
        return result

    def get_agent(self, address: str) -> Agent:
        """
        Get an agent by address.
        
        Args:
            address: Agent's wallet address
            
        Returns:
            The Agent
            
        Raises:
            AgentNotFoundError: If agent not found
        """
        response = self._request("GET", f"/v1/agents/{address}")
        return Agent.from_dict(response)

    def list_agents(
        self,
        service_type: str = None,
        limit: int = 100,
        offset: int = 0,
    ) -> List[Agent]:
        """
        List registered agents.
        
        Args:
            service_type: Filter by service type
            limit: Max results (default 100)
            offset: Pagination offset
            
        Returns:
            List of Agents
        """
        params = {"limit": limit, "offset": offset}
        if service_type:
            params["serviceType"] = service_type
            
        response = self._request("GET", "/v1/agents", params=params)
        return [Agent.from_dict(a) for a in response.get("agents", [])]

    def delete_agent(self, address: str) -> None:
        """
        Delete an agent from the registry.
        
        Args:
            address: Agent's wallet address
            
        Raises:
            AgentNotFoundError: If agent not found
        """
        self._request("DELETE", f"/v1/agents/{address}")

    # -------------------------------------------------------------------------
    # Service Operations
    # -------------------------------------------------------------------------

    def add_service(
        self,
        agent_address: str,
        service_type: str,
        name: str,
        price: str,
        description: str = "",
        endpoint: str = "",
    ) -> Service:
        """
        Add a service to an agent.
        
        Args:
            agent_address: Agent's wallet address
            service_type: Type (inference, translation, code, etc.)
            name: Service name
            price: Price in USDC (e.g., "0.001")
            description: What this service does
            endpoint: API endpoint for this service
            
        Returns:
            The created Service
            
        Raises:
            AgentNotFoundError: If agent not found
        """
        data = {
            "type": service_type,
            "name": name,
            "price": price,
            "description": description,
            "endpoint": endpoint,
        }
        
        response = self._request(
            "POST",
            f"/v1/agents/{agent_address}/services",
            json=data,
        )
        return Service.from_dict(response)

    def update_service(
        self,
        agent_address: str,
        service_id: str,
        **kwargs,
    ) -> Service:
        """
        Update a service.
        
        Args:
            agent_address: Agent's wallet address
            service_id: Service ID
            **kwargs: Fields to update (type, name, price, description, endpoint, active)
            
        Returns:
            The updated Service
        """
        response = self._request(
            "PUT",
            f"/v1/agents/{agent_address}/services/{service_id}",
            json=kwargs,
        )
        return Service.from_dict(response)

    def remove_service(self, agent_address: str, service_id: str) -> None:
        """
        Remove a service from an agent.
        
        Args:
            agent_address: Agent's wallet address
            service_id: Service ID
        """
        self._request(
            "DELETE",
            f"/v1/agents/{agent_address}/services/{service_id}",
        )

    # -------------------------------------------------------------------------
    # Discovery (the key feature)
    # -------------------------------------------------------------------------

    def discover(
        self,
        service_type: str = None,
        min_price: str = None,
        max_price: str = None,
        sort_by: str = "price",
        limit: int = 100,
        offset: int = 0,
    ) -> List[ServiceListing]:
        """
        Discover services offered by agents.

        This is how agents find each other. Results include reputation data.

        Args:
            service_type: Filter by type (inference, translation, etc.)
            min_price: Minimum price in USDC
            max_price: Maximum price in USDC
            sort_by: Sort order - "price" (default), "reputation", or "value"
            limit: Max results
            offset: Pagination offset

        Returns:
            List of ServiceListings with reputation scores

        Example:
            # Find best-value translation services
            services = client.discover(
                service_type="translation",
                max_price="0.01",
                sort_by="value",
            )

            for svc in services:
                print(f"{svc.agent_name}: {svc.name} @ ${svc.price} "
                      f"(rep: {svc.reputation_score}, tier: {svc.reputation_tier})")
        """
        params = {"limit": limit, "offset": offset}
        if service_type:
            params["type"] = service_type
        if min_price:
            params["minPrice"] = min_price
        if max_price:
            params["maxPrice"] = max_price
        if sort_by != "price":
            params["sortBy"] = sort_by

        response = self._request("GET", "/v1/services", params=params)
        return [ServiceListing.from_dict(s) for s in response.get("services", [])]

    # -------------------------------------------------------------------------
    # Transactions
    # -------------------------------------------------------------------------

    def transactions(
        self,
        agent_address: str,
        limit: int = 100,
    ) -> List[Transaction]:
        """
        Get transaction history for an agent.
        
        Args:
            agent_address: Agent's wallet address
            limit: Max results
            
        Returns:
            List of Transactions (newest first)
        """
        params = {"limit": limit}
        response = self._request(
            "GET",
            f"/v1/agents/{agent_address}/transactions",
            params=params,
        )
        return [Transaction.from_dict(t) for t in response.get("transactions", [])]

    # -------------------------------------------------------------------------
    # Network Stats
    # -------------------------------------------------------------------------

    def stats(self) -> NetworkStats:
        """
        Get network-wide statistics.
        
        Returns:
            NetworkStats with total agents, services, transactions, volume
        """
        response = self._request("GET", "/v1/network/stats")
        return NetworkStats.from_dict(response)

    def feed(self, limit: int = 50) -> dict:
        """
        Get the public transaction feed.
        
        This is the real-time feed of agents paying each other.
        
        Args:
            limit: Max transactions to return
            
        Returns:
            Dict with 'feed' (list of transactions) and 'stats'
        """
        response = self._request("GET", "/v1/feed", params={"limit": limit})
        return response

    # -------------------------------------------------------------------------
    # Health
    # -------------------------------------------------------------------------

    def health(self) -> dict:
        """
        Check API health.
        
        Returns:
            Health status dict
        """
        return self._request("GET", "/health")

    # -------------------------------------------------------------------------
    # Session Keys (Bounded Autonomy)
    # -------------------------------------------------------------------------

    def create_session_key(
        self,
        agent_address: str,
        public_key: str,  # Required: the session key's address
        expires_in: str = "24h",
        max_per_transaction: str = None,
        max_per_day: str = None,
        max_total: str = None,
        allowed_recipients: list = None,
        allowed_service_types: list = None,
        allow_any: bool = False,
        label: str = None,
    ) -> dict:
        """
        Create a session key with bounded permissions.
        
        Session keys are ECDSA keypairs that enable agents to transact with limits:
        - Spending caps (per tx, per day, total)
        - Time bounds (expires after duration)
        - Recipient restrictions (only pay specific addresses/service types)
        
        The public_key parameter is REQUIRED. Generate a keypair first:
        
            from alancoin.session_keys import generate_session_keypair
            private_key, public_key = generate_session_keypair()
            
            key = client.create_session_key(
                agent_address=wallet.address,
                public_key=public_key,  # Required!
                max_per_day="10.00",
            )
            # Store private_key securely - you need it to sign transactions
        
        Args:
            agent_address: The agent creating the session key
            public_key: The session key's Ethereum address (from keypair)
            expires_in: Duration string (e.g., "24h", "7d", "1h")
            max_per_transaction: Max USDC per transaction (e.g., "1.00")
            max_per_day: Max USDC per day (e.g., "10.00")
            max_total: Max total USDC for this key (e.g., "100.00")
            allowed_recipients: List of allowed recipient addresses
            allowed_service_types: List of allowed service types (e.g., ["translation"])
            allow_any: If True, no recipient restrictions
            label: Human-readable label
            
        Returns:
            Session key object with id, permissions, and usage tracking
            
        Example:
            from alancoin.session_keys import SessionKeyManager
            
            # Create keypair and manager
            skm = SessionKeyManager()
            
            # Register with server
            key = client.create_session_key(
                agent_address=wallet.address,
                public_key=skm.public_key,
                expires_in="7d",
                max_per_day="10.00",
                allowed_service_types=["translation"],
                label="Translation budget"
            )
            skm.set_key_id(key["id"])
            
            # Now use skm.transact() for signed transactions
        """
        if not public_key:
            raise ValueError(
                "public_key is required. Generate a keypair with:\n"
                "  from alancoin.session_keys import generate_session_keypair\n"
                "  private_key, public_key = generate_session_keypair()"
            )
        
        payload = {
            "publicKey": public_key,
            "expiresIn": expires_in,
            "allowAny": allow_any,
        }
        if max_per_transaction:
            payload["maxPerTransaction"] = max_per_transaction
        if max_per_day:
            payload["maxPerDay"] = max_per_day
        if max_total:
            payload["maxTotal"] = max_total
        if allowed_recipients:
            payload["allowedRecipients"] = allowed_recipients
        if allowed_service_types:
            payload["allowedServiceTypes"] = allowed_service_types
        if label:
            payload["label"] = label

        return self._request(
            "POST",
            f"/v1/agents/{agent_address}/sessions",
            json=payload,
        )

    def list_session_keys(self, agent_address: str) -> list:
        """
        List all session keys for an agent.
        
        Args:
            agent_address: The agent's address
            
        Returns:
            List of session key objects with status
        """
        response = self._request("GET", f"/v1/agents/{agent_address}/sessions")
        return response.get("sessions", [])

    def get_session_key(self, agent_address: str, key_id: str) -> dict:
        """
        Get a specific session key.
        
        Args:
            agent_address: The agent's address
            key_id: The session key ID
            
        Returns:
            Session key object with status and usage
        """
        response = self._request(
            "GET",
            f"/v1/agents/{agent_address}/sessions/{key_id}",
        )
        return response.get("session", response)

    def revoke_session_key(self, agent_address: str, key_id: str) -> dict:
        """
        Revoke a session key immediately.
        
        The key will no longer be valid for any transactions.
        
        Args:
            agent_address: The agent's address
            key_id: The session key ID to revoke
            
        Returns:
            Confirmation message
        """
        return self._request(
            "DELETE",
            f"/v1/agents/{agent_address}/sessions/{key_id}",
        )

    def transact_with_session_key(
        self,
        agent_address: str,
        key_id: str,
        to: str,
        amount: str,
        nonce: int,
        timestamp: int,
        signature: str,
        service_id: str = None,
    ) -> dict:
        """
        Execute a cryptographically signed transaction using a session key.
        
        The signature proves the agent controls the session key's private key.
        This is required for all session key transactions.
        
        Args:
            agent_address: The agent executing the transaction
            key_id: The session key ID (starts with "sk_")
            to: Recipient address
            amount: Amount in USDC (e.g., "0.50")
            nonce: Unique number per transaction (must be > last used)
            timestamp: Unix timestamp (must be within 5 min of server time)
            signature: ECDSA signature (hex string)
            service_id: Optional service ID being paid for
            
        Returns:
            Transaction result with verification details
            
        Raises:
            AlancoinError: If signature invalid or permissions violated
            
        Example:
            from alancoin.session_keys import SessionKeyManager
            
            # Create session key manager with your private key
            skm = SessionKeyManager(private_key=my_private_key)
            
            # Sign and submit (easiest way)
            result = skm.transact(client, wallet.address, "0x...", "0.50")
            
            # Or manually:
            signed = skm.sign("0x...", "0.50")
            result = client.transact_with_session_key(
                agent_address=wallet.address,
                key_id=skm.key_id,
                **signed,
            )
        """
        payload = {
            "to": to,
            "amount": amount,
            "nonce": nonce,
            "timestamp": timestamp,
            "signature": signature,
        }
        if service_id:
            payload["serviceId"] = service_id

        return self._request(
            "POST",
            f"/v1/agents/{agent_address}/sessions/{key_id}/transact",
            json=payload,
        )

    # -------------------------------------------------------------------------
    # Delegation (A2A)
    # -------------------------------------------------------------------------

    def create_child_session_key(
        self,
        parent_key_id: str,
        public_key: str,
        max_total: str,
        nonce: int,
        timestamp: int,
        signature: str,
        max_per_transaction: str = None,
        max_per_day: str = None,
        expires_in: str = None,
        allowed_recipients: list = None,
        allowed_service_types: list = None,
        allow_any: bool = False,
        delegation_label: str = None,
    ) -> dict:
        """
        Create a child session key delegated from a parent key.

        The child key's budget is a strict subset of the parent's remaining
        budget. Spending cascades upward -- when the child spends, all
        ancestor budgets are decremented.

        Authentication is via ECDSA signature from the parent key.

        Args:
            parent_key_id: The parent session key ID
            public_key: Child key's Ethereum address
            max_total: Maximum total budget for child
            nonce: Unique nonce (must be > parent's last nonce)
            timestamp: Unix timestamp (within 5 min)
            signature: ECDSA signature from parent key
            max_per_transaction: Per-tx limit (must be <= parent's)
            max_per_day: Daily limit
            expires_in: Duration (cannot exceed parent's expiry)
            allowed_recipients: Subset of parent's allowed recipients
            allowed_service_types: Subset of parent's service types
            allow_any: Only works if parent also allows any
            delegation_label: What task was delegated

        Returns:
            Child session key with delegation info

        Example:
            from alancoin.session_keys import SessionKeyManager

            parent_skm = SessionKeyManager(private_key=parent_private_key)
            child_skm = SessionKeyManager()

            signed = parent_skm.sign_delegation(child_skm.public_key, "2.00")
            child_key = client.create_child_session_key(
                parent_key_id=parent_key_id,
                delegation_label="translate summary",
                **signed,
            )
        """
        payload = {
            "publicKey": public_key,
            "maxTotal": max_total,
            "nonce": nonce,
            "timestamp": timestamp,
            "signature": signature,
            "allowAny": allow_any,
        }
        if max_per_transaction:
            payload["maxPerTransaction"] = max_per_transaction
        if max_per_day:
            payload["maxPerDay"] = max_per_day
        if expires_in:
            payload["expiresIn"] = expires_in
        if allowed_recipients:
            payload["allowedRecipients"] = allowed_recipients
        if allowed_service_types:
            payload["allowedServiceTypes"] = allowed_service_types
        if delegation_label:
            payload["delegationLabel"] = delegation_label

        return self._request(
            "POST",
            f"/v1/sessions/{parent_key_id}/delegate",
            json=payload,
        )

    def get_delegation_tree(self, key_id: str) -> dict:
        """
        Get the delegation tree rooted at a session key.

        Returns a nested tree structure showing each key's ID, label,
        depth, budget, spent, and children.

        Args:
            key_id: The session key ID to get the tree for

        Returns:
            Delegation tree with nested children
        """
        return self._request("GET", f"/v1/sessions/{key_id}/tree")

    # -------------------------------------------------------------------------
    # Platform Balance (Ledger)
    # -------------------------------------------------------------------------

    def get_platform_balance(self, agent_address: str) -> dict:
        """
        Get an agent's platform balance.
        
        This is the balance held by Alancoin that can be spent via session keys.
        Different from on-chain wallet balance.
        
        Args:
            agent_address: The agent's address
            
        Returns:
            Balance info:
            - available: Amount that can be spent
            - pending: Deposits awaiting confirmation
            - totalIn: Lifetime deposits
            - totalOut: Lifetime withdrawals + spending
            
        Example:
            balance = client.get_platform_balance(wallet.address)
            print(f"Available: ${balance['balance']['available']}")
        """
        return self._request("GET", f"/v1/agents/{agent_address}/balance")

    def get_ledger_history(self, agent_address: str, limit: int = 50) -> dict:
        """
        Get transaction history for an agent's platform balance.
        
        Args:
            agent_address: The agent's address
            limit: Maximum entries to return (default 50)
            
        Returns:
            List of ledger entries (deposits, spends, withdrawals)
            
        Example:
            history = client.get_ledger_history(wallet.address)
            for entry in history['entries']:
                print(f"{entry['type']}: ${entry['amount']}")
        """
        return self._request("GET", f"/v1/agents/{agent_address}/ledger")

    def request_withdrawal(self, agent_address: str, amount: str) -> dict:
        """
        Request a withdrawal from platform balance.
        
        Withdrawals are processed within 24 hours. The amount is immediately
        debited from available balance and sent to the agent's address.
        
        Args:
            agent_address: The agent's address (must be authenticated)
            amount: Amount in USDC to withdraw
            
        Returns:
            Withdrawal status (pending, completed, or error)
            
        Example:
            result = client.request_withdrawal(wallet.address, "5.00")
            print(f"Status: {result['status']}")
        """
        return self._request(
            "POST",
            f"/v1/agents/{agent_address}/withdraw",
            json={"amount": amount},
        )

    # -------------------------------------------------------------------------
    # Reputation
    # -------------------------------------------------------------------------

    def get_reputation(self, address: str) -> dict:
        """
        Get reputation score for an agent.
        
        Reputation is calculated from on-chain behavior:
        - Transaction volume and count
        - Success rate
        - Time on network
        - Unique counterparties
        
        Args:
            address: Agent's wallet address
            
        Returns:
            Reputation including:
            - score: 0-100 numeric score
            - tier: new/emerging/established/trusted/elite
            - components: Score breakdown by factor
            - metrics: Raw transaction metrics
            
        Example:
            rep = client.get_reputation("0x...")
            print(f"Score: {rep['reputation']['score']}")
            print(f"Tier: {rep['reputation']['tier']}")
        """
        return self._request("GET", f"/v1/reputation/{address}")

    def get_batch_reputation(self, addresses: list) -> dict:
        """
        Batch lookup reputation scores for multiple agents.

        More efficient than individual lookups when checking many agents.
        Maximum 100 addresses per request.

        Args:
            addresses: List of agent wallet addresses (max 100)

        Returns:
            Batch response with scores for each address, plus optional
            HMAC signature for verification.

        Example:
            result = client.get_batch_reputation(["0xaaa", "0xbbb"])
            for entry in result['scores']:
                rep = entry['reputation']
                print(f"{rep['address']}: {rep['score']} ({rep['tier']})")
        """
        return self._request(
            "POST",
            "/v1/reputation/batch",
            json={"addresses": addresses},
        )

    def get_reputation_history(
        self,
        address: str,
        from_time: str = None,
        to_time: str = None,
        limit: int = 100,
    ) -> dict:
        """
        Get historical reputation snapshots for an agent.

        Snapshots are taken periodically (hourly in production, every 10s in demo).

        Args:
            address: Agent's wallet address
            from_time: Start time (RFC3339, e.g., "2024-01-01T00:00:00Z")
            to_time: End time (RFC3339)
            limit: Maximum snapshots to return (default 100, max 1000)

        Returns:
            List of reputation snapshots over time

        Example:
            history = client.get_reputation_history("0x...")
            for snap in history['snapshots']:
                print(f"{snap['createdAt']}: {snap['score']} ({snap['tier']})")
        """
        params = {"limit": limit}
        if from_time:
            params["from"] = from_time
        if to_time:
            params["to"] = to_time
        return self._request(
            "GET",
            f"/v1/reputation/{address}/history",
            params=params,
        )

    def compare_agents(self, addresses: list) -> dict:
        """
        Compare reputation scores of 2-10 agents side-by-side.

        Returns full score breakdowns for each agent and identifies the best.

        Args:
            addresses: List of 2-10 agent wallet addresses

        Returns:
            Comparison with full scores and best agent identifier

        Example:
            result = client.compare_agents(["0xaaa", "0xbbb", "0xccc"])
            print(f"Best: {result['best']}")
            for agent in result['agents']:
                print(f"  {agent['address']}: {agent['score']}")
        """
        return self._request(
            "POST",
            "/v1/reputation/compare",
            json={"addresses": addresses},
        )

    def get_leaderboard(
        self,
        limit: int = 20,
        min_score: float = None,
        tier: str = None,
    ) -> dict:
        """
        Get reputation leaderboard.
        
        Args:
            limit: Max results (default 20, max 100)
            min_score: Minimum reputation score (0-100)
            tier: Filter by tier (new/emerging/established/trusted/elite)
            
        Returns:
            Leaderboard including:
            - leaderboard: Ranked list of agents
            - total: Total agents in network
            - tiers: Count of agents per tier
            
        Example:
            # Get top 10 trusted+ agents
            lb = client.get_leaderboard(limit=10, min_score=60)
            for entry in lb['leaderboard']:
                print(f"#{entry['rank']} {entry['address']}: {entry['score']}")
        """
        params = {"limit": limit}
        if min_score is not None:
            params["minScore"] = min_score
        if tier:
            params["tier"] = tier
            
        return self._request("GET", "/v1/reputation", params=params)

    # -------------------------------------------------------------------------
    # Webhooks
    # -------------------------------------------------------------------------

    def create_webhook(
        self,
        agent_address: str,
        url: str,
        events: list,
    ) -> dict:
        """
        Create a webhook subscription.
        
        Webhooks notify your server when events occur (payments, session key usage).
        
        Args:
            agent_address: The agent to receive webhooks for
            url: Your server's webhook endpoint URL
            events: List of event types to subscribe to:
                   - payment.received
                   - payment.sent
                   - session_key.used
                   - session_key.created
                   - session_key.revoked
                   - balance.deposit
                   - balance.withdraw
                   
        Returns:
            Webhook details including secret (shown once!)
            
        Example:
            wh = client.create_webhook(
                agent_address=wallet.address,
                url="https://myserver.com/webhooks/alancoin",
                events=["payment.received", "balance.deposit"]
            )
            secret = wh['secret']  # Store this securely!
        """
        return self._request(
            "POST",
            f"/v1/agents/{agent_address}/webhooks",
            json={"url": url, "events": events},
        )

    def list_webhooks(self, agent_address: str) -> dict:
        """
        List webhook subscriptions for an agent.
        
        Args:
            agent_address: The agent's address
            
        Returns:
            List of webhook subscriptions
        """
        return self._request("GET", f"/v1/agents/{agent_address}/webhooks")

    def delete_webhook(self, agent_address: str, webhook_id: str) -> dict:
        """
        Delete a webhook subscription.
        
        Args:
            agent_address: The agent's address
            webhook_id: The webhook ID to delete
            
        Returns:
            Deletion confirmation
        """
        return self._request("DELETE", f"/v1/agents/{agent_address}/webhooks/{webhook_id}")

    # -------------------------------------------------------------------------
    # Timeline
    # -------------------------------------------------------------------------

    def get_timeline(self, limit: int = 50) -> dict:
        """
        Get unified timeline of recent activity.

        Args:
            limit: Maximum items to return (default 50)

        Returns:
            Timeline items

        Example:
            timeline = client.get_timeline(limit=20)
            for item in timeline['timeline']:
                print(f"TX: {item['data']['from']} -> {item['data']['to']}")
        """
        return self._request("GET", "/v1/timeline", params={"limit": limit})

    # -------------------------------------------------------------------------
    # Escrow (Buyer Protection)
    # -------------------------------------------------------------------------

    def create_escrow(
        self,
        buyer_addr: str,
        seller_addr: str,
        amount: str,
        service_id: str = None,
        auto_release: str = "5m",
    ) -> dict:
        """
        Create an escrow to protect a service payment.

        Funds are locked from the buyer's available balance. They are released
        to the seller on confirmation, or refunded to the buyer on dispute.
        If neither happens, funds auto-release to the seller after the timeout.

        Args:
            buyer_addr: Buyer's wallet address
            seller_addr: Seller's wallet address
            amount: Amount in USDC (e.g., "0.005")
            service_id: Optional service ID for tracking
            auto_release: Auto-release timeout (e.g., "5m", "1h")

        Returns:
            Created escrow object

        Example:
            escrow = client.create_escrow(
                buyer_addr=wallet.address,
                seller_addr="0xSeller...",
                amount="0.005",
                service_id="svc_123",
            )
            escrow_id = escrow['escrow']['id']
        """
        payload = {
            "buyerAddr": buyer_addr,
            "sellerAddr": seller_addr,
            "amount": amount,
            "autoRelease": auto_release,
        }
        if service_id:
            payload["serviceId"] = service_id
        return self._request("POST", "/v1/escrow", json=payload)

    def get_escrow(self, escrow_id: str) -> dict:
        """
        Get an escrow by ID.

        Args:
            escrow_id: The escrow ID

        Returns:
            Escrow details including status and amounts
        """
        return self._request("GET", f"/v1/escrow/{escrow_id}")

    def confirm_escrow(self, escrow_id: str) -> dict:
        """
        Confirm an escrow, releasing funds to the seller.

        Only the buyer can confirm. Call this after verifying the service
        delivered a satisfactory result.

        Args:
            escrow_id: The escrow ID to confirm

        Returns:
            Updated escrow with status 'released'
        """
        return self._request("POST", f"/v1/escrow/{escrow_id}/confirm")

    def dispute_escrow(self, escrow_id: str, reason: str) -> dict:
        """
        Dispute an escrow, refunding funds to the buyer.

        Only the buyer can dispute. The seller's reputation is penalized.

        Args:
            escrow_id: The escrow ID to dispute
            reason: Why the service was unsatisfactory

        Returns:
            Updated escrow with status 'refunded'
        """
        return self._request(
            "POST",
            f"/v1/escrow/{escrow_id}/dispute",
            json={"reason": reason},
        )

    def deliver_escrow(self, escrow_id: str) -> dict:
        """
        Mark an escrow as delivered (seller action).

        The seller calls this after delivering the service result.
        The buyer can then confirm or dispute.

        Args:
            escrow_id: The escrow ID to mark as delivered

        Returns:
            Updated escrow with status 'delivered'
        """
        return self._request("POST", f"/v1/escrow/{escrow_id}/deliver")

    def list_escrows(self, agent_address: str, limit: int = 50) -> dict:
        """
        List escrows involving an agent (as buyer or seller).

        Args:
            agent_address: The agent's address
            limit: Maximum escrows to return

        Returns:
            List of escrows
        """
        return self._request(
            "GET",
            f"/v1/agents/{agent_address}/escrows",
            params={"limit": limit},
        )

    # -------------------------------------------------------------------------
    # MultiStep Escrow (atomic N-step pipeline payments)
    # -------------------------------------------------------------------------

    def create_multistep_escrow(
        self,
        total_amount: str,
        total_steps: int,
        planned_steps: list,
    ) -> dict:
        """
        Create a multistep escrow that locks funds for an N-step pipeline.

        Funds are locked upfront from the buyer's balance. Each step is
        confirmed individually, releasing that step's amount to the seller.
        If a step fails, call refund_multistep_escrow to return unspent funds.

        Args:
            total_amount: Total USDC to lock (e.g., "0.030")
            total_steps: Number of pipeline steps
            planned_steps: List of dicts with ``sellerAddr`` and ``amount`` for
                each step. The server validates these during confirm-step.

        Returns:
            Created multistep escrow object
        """
        return self._request("POST", "/v1/escrow/multistep", json={
            "totalAmount": total_amount,
            "totalSteps": total_steps,
            "plannedSteps": planned_steps,
        })

    def confirm_multistep_step(
        self, escrow_id: str, step_index: int, seller_addr: str, amount: str
    ) -> dict:
        """
        Confirm a single step, releasing funds to the seller.

        Args:
            escrow_id: The multistep escrow ID
            step_index: Zero-based step index
            seller_addr: Seller's address for this step
            amount: Amount in USDC for this step

        Returns:
            Updated multistep escrow
        """
        return self._request(
            "POST",
            f"/v1/escrow/multistep/{escrow_id}/confirm-step",
            json={
                "stepIndex": step_index,
                "sellerAddr": seller_addr,
                "amount": amount,
            },
        )

    def refund_multistep_escrow(self, escrow_id: str) -> dict:
        """
        Abort a multistep escrow and refund remaining locked funds.

        Only the buyer can call this. Already-confirmed steps are not reversed.

        Args:
            escrow_id: The multistep escrow ID

        Returns:
            Updated multistep escrow with status 'aborted'
        """
        return self._request("POST", f"/v1/escrow/multistep/{escrow_id}/refund")

    def get_multistep_escrow(self, escrow_id: str) -> dict:
        """
        Get a multistep escrow by ID.

        Args:
            escrow_id: The multistep escrow ID

        Returns:
            Multistep escrow details
        """
        return self._request("GET", f"/v1/escrow/multistep/{escrow_id}")

    # -------------------------------------------------------------------------
    # Streaming Micropayments
    # -------------------------------------------------------------------------

    def open_stream(
        self,
        buyer_addr: str,
        seller_addr: str,
        hold_amount: str,
        price_per_tick: str,
        service_id: str = None,
        stale_timeout_secs: int = 60,
    ) -> dict:
        """
        Open a streaming micropayment channel.

        Holds funds from the buyer's balance. The seller delivers value in
        ticks, each costing price_per_tick. On close, the spent amount goes
        to the seller and the unused hold returns to the buyer.

        Args:
            buyer_addr: Buyer's wallet address
            seller_addr: Seller's wallet address
            hold_amount: Maximum USDC to hold (e.g., "1.00")
            price_per_tick: Cost per tick unit (e.g., "0.0001")
            service_id: Optional service ID for tracking
            stale_timeout_secs: Seconds of inactivity before auto-close (default 60)

        Returns:
            Created stream object

        Example:
            stream = client.open_stream(
                buyer_addr=wallet.address,
                seller_addr="0xSeller...",
                hold_amount="0.50",
                price_per_tick="0.0001",
            )
            stream_id = stream['stream']['id']
        """
        payload = {
            "buyerAddr": buyer_addr,
            "sellerAddr": seller_addr,
            "holdAmount": hold_amount,
            "pricePerTick": price_per_tick,
            "staleTimeoutSecs": stale_timeout_secs,
        }
        if service_id:
            payload["serviceId"] = service_id
        return self._request("POST", "/v1/streams", json=payload)

    def tick_stream(
        self,
        stream_id: str,
        amount: str = None,
        metadata: str = None,
    ) -> dict:
        """
        Record a micropayment tick on an open stream.

        Each tick increments the spent amount. If amount is omitted, the
        stream's price_per_tick is used.

        Args:
            stream_id: The stream ID
            amount: Tick amount in USDC (omit for price_per_tick)
            metadata: Optional metadata (e.g., token count, chunk ID)

        Returns:
            Tick details and updated stream state

        Example:
            result = client.tick_stream("str_abc123")
            print(f"Spent so far: ${result['stream']['spentAmount']}")
        """
        payload = {}
        if amount:
            payload["amount"] = amount
        if metadata:
            payload["metadata"] = metadata
        return self._request("POST", f"/v1/streams/{stream_id}/tick", json=payload)

    def close_stream(self, stream_id: str, reason: str = None) -> dict:
        """
        Close a stream, settling funds between buyer and seller.

        The spent amount goes to the seller's available balance. The unused
        portion of the hold returns to the buyer.

        Either the buyer or seller can close the stream.

        Args:
            stream_id: The stream ID to close
            reason: Optional close reason

        Returns:
            Settled stream with final amounts
        """
        payload = {}
        if reason:
            payload["reason"] = reason
        return self._request("POST", f"/v1/streams/{stream_id}/close", json=payload)

    def get_stream(self, stream_id: str) -> dict:
        """
        Get a stream by ID.

        Args:
            stream_id: The stream ID

        Returns:
            Stream details including status, spent/held amounts, tick count
        """
        return self._request("GET", f"/v1/streams/{stream_id}")

    def list_stream_ticks(self, stream_id: str, limit: int = 100) -> dict:
        """
        List ticks for a stream.

        Args:
            stream_id: The stream ID
            limit: Maximum ticks to return

        Returns:
            List of ticks with sequence numbers and cumulative amounts
        """
        return self._request(
            "GET",
            f"/v1/streams/{stream_id}/ticks",
            params={"limit": limit},
        )

    def list_streams(self, agent_address: str, limit: int = 50) -> dict:
        """
        List streams involving an agent (as buyer or seller).

        Args:
            agent_address: The agent's address
            limit: Maximum streams to return

        Returns:
            List of streams
        """
        return self._request(
            "GET",
            f"/v1/agents/{agent_address}/streams",
            params={"limit": limit},
        )

    # -------------------------------------------------------------------------
    # Gateway (Transparent Payment Proxy)
    # -------------------------------------------------------------------------

    def create_gateway_session(
        self,
        max_total: str,
        expires_in: str = "1h",
        allowed_services: list = None,
        allowed_recipients: list = None,
        max_per_tx: str = None,
    ) -> dict:
        """
        Create a gateway session with a held budget.

        The gateway holds funds upfront and proxies service calls server-side
        (discover -> pay -> forward -> settle) in a single round trip.

        Args:
            max_total: Maximum USDC to hold for this session (e.g., "5.00")
            expires_in: Session duration (e.g., "1h", "24h")
            allowed_services: Restrict to these service types
            allowed_recipients: Restrict to these seller addresses
            max_per_tx: Maximum USDC per proxy request

        Returns:
            Gateway session with id (also used as X-Gateway-Token)
        """
        # Parse duration string (e.g. "1h", "30m", "2h") to seconds
        expires_secs = _parse_duration_to_secs(expires_in)

        payload = {
            "maxTotal": max_total,
            "maxPerRequest": max_per_tx or max_total,
        }
        if expires_secs > 0:
            payload["expiresInSecs"] = expires_secs
        if allowed_services:
            payload["allowedTypes"] = allowed_services
        return self._request("POST", "/v1/gateway/sessions", json=payload)

    def gateway_proxy(
        self,
        token: str,
        service_type: str,
        idempotency_key: str = None,
        **params,
    ) -> dict:
        """
        Proxy a service call through the gateway (one round trip).

        Server-side: discover -> select -> pay -> forward -> settle.

        Args:
            token: Gateway session token (from create_gateway_session)
            service_type: Type of service to call
            idempotency_key: Client-provided key to prevent double-charges on retry
            **params: Parameters forwarded to the service endpoint

        Returns:
            Service response with payment metadata
        """
        payload = {"serviceType": service_type}
        if params:
            payload["params"] = params
        if idempotency_key:
            payload["idempotencyKey"] = idempotency_key
        return self._request(
            "POST",
            "/v1/gateway/proxy",
            json=payload,
            extra_headers={"X-Gateway-Token": token},
        )

    def close_gateway_session(self, session_id: str) -> dict:
        """
        Close a gateway session, releasing unspent funds.

        Args:
            session_id: The gateway session ID

        Returns:
            Closed session with final spent/refunded amounts
        """
        return self._request("DELETE", f"/v1/gateway/sessions/{session_id}")

    def get_gateway_session(self, session_id: str) -> dict:
        """
        Get gateway session status.

        Args:
            session_id: The gateway session ID

        Returns:
            Session details including budget, spent, remaining, status
        """
        return self._request("GET", f"/v1/gateway/sessions/{session_id}")

    def list_gateway_sessions(self, limit: int = 50) -> dict:
        """
        List gateway sessions for the authenticated agent.

        Args:
            limit: Maximum sessions to return

        Returns:
            List of gateway sessions
        """
        return self._request(
            "GET", "/v1/gateway/sessions", params={"limit": limit}
        )

    def list_gateway_logs(self, session_id: str, limit: int = 100) -> dict:
        """
        List request logs for a gateway session.

        Args:
            session_id: The gateway session ID
            limit: Maximum log entries to return

        Returns:
            List of proxy request logs with timing, cost, and status
        """
        return self._request(
            "GET",
            f"/v1/gateway/sessions/{session_id}/logs",
            params={"limit": limit},
        )

    # -------------------------------------------------------------------------
    # Sessions (High-Level API)
    # -------------------------------------------------------------------------

    def session(
        self,
        max_total: str = "10.00",
        max_per_tx: str = "1.00",
        max_per_day: str = None,
        expires_in: str = "1h",
        allowed_services: list = None,
        allowed_recipients: list = None,
        budget: "Budget" = None,
    ) -> "BudgetSession":
        """
        Create a bounded spending session.

        Returns a context manager that auto-creates a session key on entry
        and revokes it on exit. Use call_service() for one-step discover +
        pay + call, or pay() for direct transfers.

        Args:
            max_total: Max total USDC for this session (e.g., "5.00")
            max_per_tx: Max USDC per transaction (e.g., "0.50")
            max_per_day: Max daily USDC (optional)
            expires_in: Session duration (e.g., "1h", "24h", "7d")
            allowed_services: Restrict to service types (e.g., ["translation"])
            allowed_recipients: Restrict to recipient addresses
            budget: Pre-built Budget object (overrides other args)

        Returns:
            BudgetSession context manager

        Example::

            with client.session(max_total="5.00", max_per_tx="0.50") as s:
                result = s.call_service("translation", text="Hello", target="es")
                print(result["output"])
                print(f"Spent: ${s.total_spent}")
        """
        from .session import Budget as _Budget, BudgetSession

        if budget is None:
            budget = _Budget(
                max_total=max_total,
                max_per_tx=max_per_tx,
                max_per_day=max_per_day,
                expires_in=expires_in,
                allowed_services=allowed_services,
                allowed_recipients=allowed_recipients,
            )

        return BudgetSession(self, budget)

    def stream(
        self,
        seller_addr: str,
        hold_amount: str,
        price_per_tick: str,
        service_id: str = None,
        stale_timeout_secs: int = 60,
    ) -> "StreamingSession":
        """
        Create a streaming micropayment session.

        Returns a context manager that opens a payment stream on entry
        and settles it on exit. Use tick() to record micropayments.

        Args:
            seller_addr: Seller's wallet address
            hold_amount: Maximum USDC to hold (e.g., "1.00")
            price_per_tick: Cost per tick (e.g., "0.0001")
            service_id: Optional service ID for tracking
            stale_timeout_secs: Auto-close after this many idle seconds

        Returns:
            StreamingSession context manager

        Example::

            with client.stream(
                seller_addr="0xSeller...",
                hold_amount="0.50",
                price_per_tick="0.0001",
            ) as s:
                for token in tokens:
                    result = s.tick(metadata=f"token:{token}")
                print(f"Total: ${s.spent} for {s.tick_count} ticks")
        """
        from .session import StreamingSession

        return StreamingSession(
            client=self,
            seller_addr=seller_addr,
            hold_amount=hold_amount,
            price_per_tick=price_per_tick,
            service_id=service_id,
            stale_timeout_secs=stale_timeout_secs,
        )

    def gateway(
        self,
        max_total: str = "10.00",
        max_per_tx: str = None,
        expires_in: str = "1h",
        allowed_services: list = None,
        allowed_recipients: list = None,
    ) -> "GatewaySession":
        """
        Create a gateway session (server-side payment proxy).

        Returns a context manager that creates a gateway session on entry
        and closes it on exit. Use call() for one-step proxy calls where
        the server handles discover -> pay -> forward -> settle.

        This is the recommended path for most AI agents: fewer round trips,
        no client-side session key management, and built-in settlement.

        Args:
            max_total: Maximum USDC for this session (e.g., "5.00")
            max_per_tx: Maximum USDC per proxy request
            expires_in: Session duration (e.g., "1h", "24h")
            allowed_services: Restrict to service types (e.g., ["translation"])
            allowed_recipients: Restrict to seller addresses

        Returns:
            GatewaySession context manager

        Example::

            with client.gateway(max_total="5.00") as gw:
                result = gw.call("translation", text="Hello", target="es")
                print(result["output"])
                print(f"Spent: ${gw.total_spent}, Remaining: ${gw.remaining}")
        """
        from .session import GatewaySession

        return GatewaySession(
            client=self,
            max_total=max_total,
            max_per_tx=max_per_tx,
            expires_in=expires_in,
            allowed_services=allowed_services,
            allowed_recipients=allowed_recipients,
        )

    # -------------------------------------------------------------------------
    # Internal
    # -------------------------------------------------------------------------

    def _request(
        self,
        method: str,
        path: str,
        params: dict = None,
        json: dict = None,
        extra_headers: dict = None,
    ) -> dict:
        """Make an HTTP request to the API."""
        base = self.base_url if self.base_url.endswith("/") else self.base_url + "/"
        url = urljoin(base, path.lstrip("/"))

        # Merge extra_headers with session headers (session headers take priority
        # for Authorization/Content-Type, extra_headers add new ones like X-Gateway-Token)
        merged = None
        if extra_headers:
            merged = dict(self._session.headers)
            merged.update(extra_headers)

        try:
            response = self._session.request(
                method=method,
                url=url,
                params=params,
                json=json,
                timeout=self.timeout,
                headers=merged,
            )
        except requests.exceptions.RequestException as e:
            raise NetworkError(f"Request failed: {e}", original_error=e)
        
        # Handle errors
        if response.status_code == 402:
            try:
                data = response.json()
            except (ValueError, KeyError):
                raise PaymentRequiredError(price="", recipient="")
            raise PaymentRequiredError(
                price=data.get("price", ""),
                recipient=data.get("recipient", ""),
                currency=data.get("currency", "USDC"),
                chain=data.get("chain", ""),
                contract=data.get("contract", ""),
            )

        if response.status_code == 404:
            try:
                data = response.json()
            except (ValueError, KeyError):
                raise ServiceNotFoundError(path.split("/")[-1])
            error_code = data.get("error", "")
            if error_code == "not_found" or "agent" in data.get("message", "").lower():
                raise AgentNotFoundError(path.split("/")[-1])
            raise ServiceNotFoundError(path.split("/")[-1])

        if response.status_code == 409:
            raise AgentExistsError(path.split("/")[-1])

        if response.status_code == 400:
            try:
                data = response.json()
            except (ValueError, KeyError):
                raise ValidationError("Invalid request")
            raise ValidationError(data.get("message", "Invalid request"))
        
        if response.status_code >= 400:
            try:
                data = response.json()
                # Extract extra server fields (funds_status, recovery, etc.)
                # beyond the standard error/message pair.
                details = {k: v for k, v in data.items() if k not in ("error", "message")}
                raise AlancoinError(
                    message=data.get("message", "Unknown error"),
                    code=data.get("error", "unknown"),
                    status_code=response.status_code,
                    details=details if details else None,
                )
            except (ValueError, KeyError):
                raise AlancoinError(
                    message=response.text or "Unknown error",
                    status_code=response.status_code,
                )
        
        # Success - return JSON or empty dict
        if response.status_code == 204:
            return {}
        
        try:
            return response.json()
        except ValueError:
            return {}

    def close(self):
        """Close the client session."""
        self._session.close()

    def __enter__(self):
        return self

    def __exit__(self, exc_type, exc_val, exc_tb):
        self.close()
