"""
Async wrapper for synchronous Alancoin SDK.

Wraps the sync SDK using ThreadPoolExecutor to enable
async/await usage in the experiment harness.
"""

import asyncio
from concurrent.futures import ThreadPoolExecutor
from typing import Any, Optional

# Import from existing experiments client
import sys
from pathlib import Path

# Add experiments to path for imports
experiments_path = Path(__file__).parent.parent.parent
if str(experiments_path) not in sys.path:
    sys.path.insert(0, str(experiments_path))

from client import AlancoinTestClient, MockAlancoinClient, SessionKey


class AsyncAlancoinClient:
    """
    Async wrapper for the synchronous Alancoin client.

    Uses ThreadPoolExecutor to run sync operations in separate threads,
    allowing async/await usage without blocking the event loop.
    """

    def __init__(
        self,
        sync_client: Optional[AlancoinTestClient] = None,
        use_mock: bool = True,
        max_workers: int = 8,
    ):
        """
        Initialize async wrapper.

        Args:
            sync_client: Existing sync client to wrap (creates new if None)
            use_mock: If True and no client provided, use MockAlancoinClient
            max_workers: Maximum threads in executor pool
        """
        if sync_client is not None:
            self.sync = sync_client
        elif use_mock:
            self.sync = MockAlancoinClient()
        else:
            self.sync = AlancoinTestClient()

        self._executor = ThreadPoolExecutor(max_workers=max_workers)

    async def _run_sync(self, func, *args, **kwargs) -> Any:
        """Run a sync function in the executor."""
        loop = asyncio.get_event_loop()
        return await loop.run_in_executor(
            self._executor,
            lambda: func(*args, **kwargs)
        )

    async def register_agent(
        self,
        name: str = "TestAgent",
        description: str = "Experiment agent",
    ) -> dict:
        """Register a new agent."""
        return await self._run_sync(
            self.sync.register_agent,
            name=name,
            description=description,
        )

    async def create_session_key(
        self,
        max_per_tx: float = 1.0,
        max_per_day: float = 10.0,
        max_total: float = 100.0,
        expires_in: str = "24h",
        allowed_recipients: Optional[list] = None,
        allowed_types: Optional[list] = None,
        label: str = "experiment",
    ) -> SessionKey:
        """Create a new session key with specified permissions."""
        return await self._run_sync(
            self.sync.create_session_key,
            max_per_tx=max_per_tx,
            max_per_day=max_per_day,
            max_total=max_total,
            expires_in=expires_in,
            allowed_recipients=allowed_recipients,
            allowed_types=allowed_types,
            label=label,
        )

    async def sign_transaction(
        self,
        session_key: SessionKey,
        to: str,
        amount: float,
        nonce: Optional[int] = None,
        timestamp: Optional[int] = None,
    ) -> dict:
        """Sign a transaction with a session key."""
        return await self._run_sync(
            self.sync.sign_transaction,
            session_key,
            to,
            amount,
            nonce,
            timestamp,
        )

    async def submit_transaction(
        self,
        session_key: SessionKey,
        transaction: dict,
    ) -> dict:
        """Submit a signed transaction."""
        return await self._run_sync(
            self.sync.submit_transaction,
            session_key,
            transaction,
        )

    async def transact(
        self,
        session_key: SessionKey,
        to: str,
        amount: float,
        nonce: Optional[int] = None,
        timestamp: Optional[int] = None,
    ) -> dict:
        """Sign and submit a transaction in one call."""
        return await self._run_sync(
            self.sync.transact,
            session_key,
            to,
            amount,
            nonce,
            timestamp,
        )

    async def revoke_session_key(self, session_key: SessionKey) -> dict:
        """Revoke a session key."""
        return await self._run_sync(
            self.sync.revoke_session_key,
            session_key,
        )

    async def get_session_key(self, session_key: SessionKey) -> dict:
        """Get session key details including usage."""
        return await self._run_sync(
            self.sync.get_session_key,
            session_key,
        )

    async def get_balance(self) -> dict:
        """Get agent's ledger balance."""
        return await self._run_sync(self.sync.get_balance)

    async def deposit(self, amount: float) -> dict:
        """Simulate a deposit."""
        return await self._run_sync(
            self.sync.deposit,
            amount,
        )

    async def health_check(self) -> bool:
        """Check if the API is reachable."""
        return await self._run_sync(self.sync.health_check)

    @property
    def api_key(self) -> Optional[str]:
        """Get the agent's API key."""
        return self.sync.api_key

    @property
    def agent_address(self) -> Optional[str]:
        """Get the agent's address."""
        return self.sync.agent_address

    def close(self):
        """Shutdown the executor."""
        self._executor.shutdown(wait=True)

    async def __aenter__(self):
        return self

    async def __aexit__(self, exc_type, exc_val, exc_tb):
        self.close()
