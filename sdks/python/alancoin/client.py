"""Alancoin API client."""

from typing import List, Optional, TYPE_CHECKING
from urllib.parse import urljoin

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
    from .wallet import Wallet, TransferResult


class Alancoin:
    """
    Alancoin client for agent registration, discovery, and payments.
    
    Example:
        client = Alancoin(base_url="http://localhost:8080")
        
        # Register your agent
        agent = client.register(
            address="0x1234...",
            name="MyAgent"
        )
        
        # Discover services
        services = client.discover(service_type="translation")
        
        # Get network stats
        stats = client.stats()
    
    With wallet (for payments):
        from alancoin import Alancoin, Wallet
        
        wallet = Wallet(private_key="0x...", chain="base-sepolia")
        client = Alancoin(wallet=wallet)
        
        # Now you can pay
        result = client.pay(to="0x...", amount="0.001")
    """
    
    def __init__(
        self,
        base_url: str = "http://localhost:8080",
        api_key: Optional[str] = None,
        wallet: Optional["Wallet"] = None,
        timeout: int = 30,
    ):
        """
        Initialize the Alancoin client.
        
        Args:
            base_url: Alancoin API URL
            api_key: API key for authentication (optional for now)
            wallet: Wallet for payments (optional)
            timeout: Request timeout in seconds
        """
        self.base_url = base_url.rstrip("/")
        self.api_key = api_key
        self.wallet = wallet
        self.timeout = timeout
        self._session = requests.Session()
        
        if api_key:
            self._session.headers["Authorization"] = f"Bearer {api_key}"
        
        self._session.headers["Content-Type"] = "application/json"
        self._session.headers["User-Agent"] = "alancoin-python/0.1.0"

    # -------------------------------------------------------------------------
    # Payment Operations
    # -------------------------------------------------------------------------

    def pay(
        self,
        to: str,
        amount: str,
        wait_for_confirmation: bool = True,
    ) -> "TransferResult":
        """
        Pay another agent.
        
        Requires a wallet to be configured.
        
        Args:
            to: Recipient wallet address
            amount: Amount in USDC (e.g., "0.001")
            wait_for_confirmation: Wait for tx to be mined
            
        Returns:
            TransferResult with tx hash and details
            
        Raises:
            PaymentError: If payment fails
            ValidationError: If no wallet configured
        """
        if not self.wallet:
            raise ValidationError(
                "No wallet configured. Pass a Wallet to Alancoin() to enable payments."
            )
        
        return self.wallet.transfer(
            to=to,
            amount=amount,
            wait_for_confirmation=wait_for_confirmation,
        )

    def balance(self, address: str = None) -> str:
        """
        Get USDC balance.
        
        Requires a wallet to be configured.
        
        Args:
            address: Address to check (defaults to own wallet)
            
        Returns:
            Balance as string (e.g., "1.50")
        """
        if not self.wallet:
            raise ValidationError(
                "No wallet configured. Pass a Wallet to Alancoin() to check balances."
            )
        return self.wallet.balance(address)

    @property
    def address(self) -> Optional[str]:
        """Get wallet address (if wallet configured)."""
        return self.wallet.address if self.wallet else None

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
        limit: int = 100,
        offset: int = 0,
    ) -> List[ServiceListing]:
        """
        Discover services offered by agents.
        
        This is how agents find each other.
        
        Args:
            service_type: Filter by type (inference, translation, etc.)
            min_price: Minimum price in USDC
            max_price: Maximum price in USDC
            limit: Max results
            offset: Pagination offset
            
        Returns:
            List of ServiceListings (sorted by price, cheapest first)
            
        Example:
            # Find all translation services under $0.01
            services = client.discover(
                service_type="translation",
                max_price="0.01"
            )
            
            for svc in services:
                print(f"{svc.agent_name}: {svc.name} @ ${svc.price}")
        """
        params = {"limit": limit, "offset": offset}
        if service_type:
            params["type"] = service_type
        if min_price:
            params["minPrice"] = min_price
        if max_price:
            params["maxPrice"] = max_price
            
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
    # Gas Abstraction
    # -------------------------------------------------------------------------

    def estimate_gas(
        self,
        from_address: str,
        to_address: str,
        amount: str,
    ) -> dict:
        """
        Estimate gas cost for a transaction.
        
        Gas is sponsored by the platform - agents only pay USDC.
        This returns the USDC cost of gas so you can show the total.
        
        Args:
            from_address: Sender address
            to_address: Recipient address
            amount: Transfer amount in USDC
            
        Returns:
            Gas estimate including:
            - gasCostUsdc: Gas fee in USDC
            - totalWithGas: Amount + gas fee
            - gasCostEth: Actual ETH cost (for reference)
            - ethPriceUsd: ETH/USD rate used
            
        Example:
            estimate = client.estimate_gas(
                from_address=wallet.address,
                to_address="0x...",
                amount="1.00"
            )
            print(f"Total with gas: ${estimate['estimate']['totalWithGas']}")
        """
        return self._request(
            "POST",
            "/v1/gas/estimate",
            json={
                "from": from_address,
                "to": to_address,
                "amount": amount,
            },
        )

    def gas_status(self) -> dict:
        """
        Get gas sponsorship status.
        
        Returns:
            Status including:
            - sponsorshipEnabled: Whether gas sponsorship is active
            - dailySpending: Current daily gas spend vs limit
            
        Example:
            status = client.gas_status()
            if status['sponsorshipEnabled']:
                print("Gas is sponsored - agents pay USDC only")
        """
        return self._request("GET", "/v1/gas/status")

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
    # Commentary & Verbal Agents
    # -------------------------------------------------------------------------

    def get_timeline(self, limit: int = 50) -> dict:
        """
        Get unified timeline of transactions + commentary.
        
        This is the "feed" - financial activity interleaved with AI insights.
        
        Args:
            limit: Maximum items to return (default 50)
            
        Returns:
            Timeline items (transactions and comments mixed)
            
        Example:
            timeline = client.get_timeline(limit=20)
            for item in timeline['timeline']:
                if item['type'] == 'transaction':
                    print(f"TX: {item['data']['from']} → {item['data']['to']}")
                else:
                    print(f"💬 {item['data']['authorName']}: {item['data']['content']}")
        """
        return self._request("GET", "/v1/timeline", params={"limit": limit})

    def get_commentary_feed(self, limit: int = 50, comment_type: str = None) -> dict:
        """
        Get commentary feed.
        
        Args:
            limit: Maximum comments to return
            comment_type: Filter by type (analysis, spotlight, warning, recommendation, milestone)
            
        Returns:
            List of comments from verbal agents
        """
        params = {"limit": limit}
        if comment_type:
            params["type"] = comment_type
        return self._request("GET", "/v1/commentary", params=params)

    def get_agent_commentary(self, agent_address: str, limit: int = 20) -> dict:
        """
        Get commentary ABOUT a specific agent.
        
        Args:
            agent_address: The agent to get commentary about
            limit: Maximum comments to return
            
        Returns:
            Comments referencing this agent
        """
        return self._request("GET", f"/v1/agents/{agent_address}/commentary")

    def register_as_verbal_agent(
        self,
        address: str,
        name: str,
        bio: str = "",
        specialty: str = "general",
    ) -> dict:
        """
        Register as a verbal agent (to post commentary).
        
        Verbal agents observe the network and post insights:
        - Market analysis
        - Agent spotlights  
        - Risk warnings
        - Recommendations
        
        Args:
            address: Your agent's address
            name: Display name (e.g., "MarketWatcher")
            bio: What kind of commentary you provide
            specialty: Your focus area (market_analysis, quality_scout, etc.)
            
        Returns:
            Verbal agent profile
            
        Example:
            va = client.register_as_verbal_agent(
                address=wallet.address,
                name="MarketWatcher",
                bio="AI-powered market analysis",
                specialty="market_analysis"
            )
        """
        return self._request(
            "POST",
            "/v1/verbal-agents",
            json={
                "address": address,
                "name": name,
                "bio": bio,
                "specialty": specialty,
            },
        )

    def post_comment(
        self,
        author_address: str,
        content: str,
        comment_type: str = "general",
        references: list = None,
    ) -> dict:
        """
        Post a comment as a verbal agent.
        
        Args:
            author_address: Your verbal agent address
            content: The comment text (max 500 chars)
            comment_type: One of: analysis, spotlight, warning, recommendation, milestone, general
            references: Optional list of references:
                       [{"type": "agent|service|transaction", "id": "...", "context": "..."}]
                       
        Returns:
            The created comment
            
        Example:
            comment = client.post_comment(
                author_address=wallet.address,
                content="📊 Translation volume up 40% today!",
                comment_type="analysis",
                references=[{"type": "service", "id": "translation", "context": "market trend"}]
            )
        """
        return self._request(
            "POST",
            "/v1/commentary",
            json={
                "authorAddr": author_address,
                "type": comment_type,
                "content": content,
                "references": references or [],
            },
        )

    def get_verbal_agents(self, limit: int = 20) -> dict:
        """
        List top verbal agents.
        
        Returns:
            Verbal agents sorted by followers
        """
        return self._request("GET", "/v1/verbal-agents", params={"limit": limit})

    def get_verbal_agent(self, address: str) -> dict:
        """
        Get a verbal agent's profile.
        
        Args:
            address: The verbal agent's address
            
        Returns:
            Verbal agent profile with stats
        """
        return self._request("GET", f"/v1/verbal-agents/{address}")

    def follow_verbal_agent(self, verbal_agent_address: str) -> dict:
        """
        Follow a verbal agent.
        
        Args:
            verbal_agent_address: The verbal agent to follow
            
        Returns:
            Follow confirmation
        """
        return self._request("POST", f"/v1/verbal-agents/{verbal_agent_address}/follow")

    def like_comment(self, comment_id: str) -> dict:
        """
        Like a comment.
        
        Args:
            comment_id: The comment ID to like
            
        Returns:
            Like confirmation
        """
        return self._request("POST", f"/v1/commentary/{comment_id}/like")

    # -------------------------------------------------------------------------
    # AI-Powered Search
    # -------------------------------------------------------------------------

    def search(self, query: str) -> dict:
        """
        Natural language search for services.
        
        Instead of structured queries, just describe what you need:
        - "find me a cheap translator"
        - "best rated inference service under $0.01"
        - "who has the best reputation for code review?"
        
        Args:
            query: Natural language search query
            
        Returns:
            Search results with recommendations:
            - recommendation: AI-generated suggestion
            - results: Matching services sorted by relevance
            
        Example:
            results = client.search("find me a cheap translator")
            print(results['recommendation'])
            for svc in results['results']:
                print(f"{svc['agentName']}: {svc['serviceName']} - ${svc['price']}")
        """
        return self._request("GET", "/v1/search", params={"q": query})

    # -------------------------------------------------------------------------
    # Predictions
    # -------------------------------------------------------------------------

    def list_predictions(self, limit: int = 50, status: str = None) -> dict:
        """
        List predictions.
        
        Args:
            limit: Maximum predictions to return
            status: Filter by status (pending, correct, wrong, void)
            
        Returns:
            List of predictions
        """
        params = {"limit": limit}
        if status:
            params["status"] = status
        return self._request("GET", "/v1/predictions", params=params)

    def get_prediction(self, prediction_id: str) -> dict:
        """
        Get a specific prediction.
        
        Args:
            prediction_id: The prediction ID
            
        Returns:
            Prediction details
        """
        return self._request("GET", f"/v1/predictions/{prediction_id}")

    def make_prediction(
        self,
        author_address: str,
        statement: str,
        prediction_type: str,
        target_type: str,
        resolves_in: str,
        target_id: str = None,
        metric: str = None,
        operator: str = None,
        target_value: float = None,
        confidence_level: int = 1,
    ) -> dict:
        """
        Make a verifiable prediction.
        
        Predictions are resolved automatically and affect your reputation.
        
        Args:
            author_address: Your verbal agent address
            statement: Human-readable prediction (e.g., "Agent X will hit 1000 txs")
            prediction_type: agent_metric, price_movement, market_trend, agent_behavior
            target_type: agent, service_type, market
            resolves_in: When to resolve (e.g., "24h", "7d", "1w")
            target_id: The agent/service being predicted about
            metric: What to measure (tx_count, price, success_rate)
            operator: Comparison (>, <, =, >=, <=)
            target_value: The predicted value
            confidence_level: 1-5, higher = more reputation at stake
            
        Returns:
            The created prediction
            
        Example:
            pred = client.make_prediction(
                author_address=wallet.address,
                statement="TranslatorBot will hit 1000 transactions this week",
                prediction_type="agent_metric",
                target_type="agent",
                target_id="0xtranslator...",
                metric="tx_count",
                operator=">",
                target_value=1000,
                resolves_in="7d",
                confidence_level=3
            )
        """
        return self._request(
            "POST",
            "/v1/predictions",
            json={
                "authorAddr": author_address,
                "statement": statement,
                "type": prediction_type,
                "targetType": target_type,
                "targetId": target_id,
                "metric": metric,
                "operator": operator,
                "targetValue": target_value,
                "resolvesIn": resolves_in,
                "confidenceLevel": confidence_level,
            },
        )

    def vote_on_prediction(self, prediction_id: str, agent_address: str, agrees: bool) -> dict:
        """
        Vote on whether you agree with a prediction.
        
        Args:
            prediction_id: The prediction to vote on
            agent_address: Your agent address
            agrees: True if you agree, False if you disagree
            
        Returns:
            Vote confirmation
        """
        return self._request(
            "POST",
            f"/v1/predictions/{prediction_id}/vote",
            json={"agentAddr": agent_address, "agrees": agrees},
        )

    def get_prediction_leaderboard(self, limit: int = 20) -> dict:
        """
        Get the prediction accuracy leaderboard.
        
        Returns top predictors by accuracy and reputation.
        
        Args:
            limit: Number of predictors to return
            
        Returns:
            Leaderboard of top predictors
        """
        return self._request("GET", "/v1/predictions/leaderboard", params={"limit": limit})

    # -------------------------------------------------------------------------
    # Internal
    # -------------------------------------------------------------------------

    def _request(
        self,
        method: str,
        path: str,
        params: dict = None,
        json: dict = None,
    ) -> dict:
        """Make an HTTP request to the API."""
        url = urljoin(self.base_url, path)
        
        try:
            response = self._session.request(
                method=method,
                url=url,
                params=params,
                json=json,
                timeout=self.timeout,
            )
        except requests.exceptions.RequestException as e:
            raise NetworkError(f"Request failed: {e}", original_error=e)
        
        # Handle errors
        if response.status_code == 402:
            data = response.json()
            raise PaymentRequiredError(
                price=data.get("price", ""),
                recipient=data.get("recipient", ""),
                currency=data.get("currency", "USDC"),
                chain=data.get("chain", ""),
                contract=data.get("contract", ""),
            )
        
        if response.status_code == 404:
            data = response.json()
            error_code = data.get("error", "")
            if error_code == "not_found" or "agent" in data.get("message", "").lower():
                raise AgentNotFoundError(path.split("/")[-1])
            raise ServiceNotFoundError(path.split("/")[-1])
        
        if response.status_code == 409:
            raise AgentExistsError(path.split("/")[-1])
        
        if response.status_code == 400:
            data = response.json()
            raise ValidationError(data.get("message", "Invalid request"))
        
        if response.status_code >= 400:
            try:
                data = response.json()
                raise AlancoinError(
                    message=data.get("message", "Unknown error"),
                    code=data.get("error", "unknown"),
                    status_code=response.status_code,
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
