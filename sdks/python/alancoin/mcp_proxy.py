"""MCP Payment Proxy — wrap any MCP server with Alancoin escrow payments.

Architecture:
    LLM Client <--MCP/stdio--> AlancoinProxy <--MCP/subprocess--> Upstream MCP Server

The proxy discovers upstream tools, enriches their descriptions with pricing,
and gates every tool call through an escrow: lock funds -> forward call ->
confirm on success / dispute on failure.
"""

from __future__ import annotations

import abc
import argparse
import asyncio
import json
import sys
import uuid
from dataclasses import dataclass, field
from decimal import Decimal
from typing import Any, Sequence

from mcp import ClientSession, StdioServerParameters
from mcp.client.stdio import stdio_client
from mcp.server.lowlevel import Server
from mcp.server.stdio import stdio_server
import mcp.types as types


# ---------------------------------------------------------------------------
# Payment backend
# ---------------------------------------------------------------------------

@dataclass
class EscrowResult:
    escrow_id: str
    status: str  # created | confirmed | disputed
    amount: str


class PaymentBackend(abc.ABC):
    @abc.abstractmethod
    async def create_escrow(self, buyer: str, seller: str, amount: str) -> EscrowResult: ...

    @abc.abstractmethod
    async def confirm_escrow(self, escrow_id: str) -> EscrowResult: ...

    @abc.abstractmethod
    async def dispute_escrow(self, escrow_id: str, reason: str) -> EscrowResult: ...

    @abc.abstractmethod
    async def get_balance(self, address: str) -> str: ...


class DemoBackend(PaymentBackend):
    """In-memory escrow backend for local development and demos."""

    def __init__(
        self,
        buyer_addr: str = "demo_buyer",
        seller_addr: str = "demo_seller",
        starting_balance: str = "100.00",
    ):
        self.balances: dict[str, Decimal] = {
            buyer_addr: Decimal(starting_balance),
            seller_addr: Decimal("0"),
        }
        self.escrows: dict[str, dict[str, Any]] = {}

    async def create_escrow(self, buyer: str, seller: str, amount: str) -> EscrowResult:
        amt = Decimal(amount)
        bal = self.balances.get(buyer, Decimal(0))
        if bal < amt:
            raise ValueError(f"Insufficient balance: {bal} < {amount}")

        escrow_id = f"demo_escrow_{uuid.uuid4().hex[:8]}"
        self.balances[buyer] -= amt
        self.escrows[escrow_id] = {
            "buyer": buyer, "seller": seller, "amount": amt, "status": "created",
        }
        _log(f"[escrow] Created {escrow_id}: {buyer} -> {seller}, ${amount} USDC")
        _log(f"[balance] {buyer}: ${self.balances[buyer]}")
        return EscrowResult(escrow_id=escrow_id, status="created", amount=amount)

    async def confirm_escrow(self, escrow_id: str) -> EscrowResult:
        escrow = self._get(escrow_id)
        escrow["status"] = "confirmed"
        seller = escrow["seller"]
        self.balances.setdefault(seller, Decimal(0))
        self.balances[seller] += escrow["amount"]
        _log(f"[escrow] Confirmed {escrow_id}: ${escrow['amount']} -> {seller}")
        _log(f"[balance] {seller}: ${self.balances[seller]}")
        return EscrowResult(escrow_id=escrow_id, status="confirmed", amount=str(escrow["amount"]))

    async def dispute_escrow(self, escrow_id: str, reason: str) -> EscrowResult:
        escrow = self._get(escrow_id)
        escrow["status"] = "disputed"
        buyer = escrow["buyer"]
        self.balances[buyer] += escrow["amount"]
        _log(f"[escrow] Disputed {escrow_id}: ${escrow['amount']} refunded to {buyer} ({reason})")
        return EscrowResult(escrow_id=escrow_id, status="disputed", amount=str(escrow["amount"]))

    async def get_balance(self, address: str) -> str:
        return str(self.balances.get(address, Decimal(0)))

    def _get(self, escrow_id: str) -> dict[str, Any]:
        escrow = self.escrows.get(escrow_id)
        if not escrow:
            raise ValueError(f"Escrow not found: {escrow_id}")
        return escrow


class AlancoinBackend(PaymentBackend):
    """Backend that uses real Alancoin platform escrow via the Python SDK client."""

    def __init__(self, client: Any, buyer_addr: str, seller_addr: str):
        self._client = client
        self.buyer_addr = buyer_addr
        self.seller_addr = seller_addr

    async def create_escrow(self, buyer: str, seller: str, amount: str) -> EscrowResult:
        result = await asyncio.to_thread(
            self._client.create_escrow,
            buyer_addr=buyer, seller_addr=seller, amount=amount,
        )
        escrow_id = result["escrow"]["id"]
        return EscrowResult(escrow_id=escrow_id, status="created", amount=amount)

    async def confirm_escrow(self, escrow_id: str) -> EscrowResult:
        result = await asyncio.to_thread(self._client.confirm_escrow, escrow_id)
        return EscrowResult(
            escrow_id=escrow_id, status="confirmed",
            amount=result.get("escrow", {}).get("amount", "?"),
        )

    async def dispute_escrow(self, escrow_id: str, reason: str) -> EscrowResult:
        result = await asyncio.to_thread(self._client.dispute_escrow, escrow_id, reason)
        return EscrowResult(
            escrow_id=escrow_id, status="disputed",
            amount=result.get("escrow", {}).get("amount", "?"),
        )

    async def get_balance(self, address: str) -> str:
        result = await asyncio.to_thread(self._client.get_balance, address)
        return result.get("balance", {}).get("available", "?")


# ---------------------------------------------------------------------------
# Pricing
# ---------------------------------------------------------------------------

@dataclass
class ToolPricing:
    default_price: Decimal
    per_tool: dict[str, Decimal] = field(default_factory=dict)

    def price_for(self, tool_name: str) -> Decimal:
        return self.per_tool.get(tool_name, self.default_price)

    @classmethod
    def from_flat(cls, price: str) -> ToolPricing:
        return cls(default_price=Decimal(price))

    @classmethod
    def from_json_file(cls, path: str) -> ToolPricing:
        with open(path) as f:
            data = json.load(f)
        return cls(
            default_price=Decimal(str(data.get("default", "0.001"))),
            per_tool={k: Decimal(str(v)) for k, v in data.get("tools", {}).items()},
        )


# ---------------------------------------------------------------------------
# Proxy
# ---------------------------------------------------------------------------

class MCPPaymentProxy:
    """MCP proxy that gates upstream tool calls through Alancoin escrow."""

    def __init__(
        self,
        upstream_command: str,
        pricing: ToolPricing,
        backend: PaymentBackend,
        buyer_addr: str = "demo_buyer",
        seller_addr: str = "demo_seller",
    ):
        self.upstream_command = upstream_command
        self.pricing = pricing
        self.backend = backend
        self.buyer_addr = buyer_addr
        self.seller_addr = seller_addr
        self.server = Server("alancoin-mcp-proxy")

    def _parse_command(self) -> StdioServerParameters:
        parts = self.upstream_command.split()
        return StdioServerParameters(command=parts[0], args=parts[1:])

    def _enrich_tool(self, tool: types.Tool) -> types.Tool:
        price = self.pricing.price_for(tool.name)
        desc = tool.description or ""
        desc += f"\n[Alancoin: ${price} USDC per call]"
        return types.Tool(name=tool.name, description=desc, inputSchema=tool.inputSchema)

    async def run(self) -> None:
        params = self._parse_command()

        async with stdio_client(params) as (up_read, up_write):
            async with ClientSession(up_read, up_write) as upstream:
                await upstream.initialize()
                tools_result = await upstream.list_tools()
                upstream_tools = tools_result.tools
                enriched = [self._enrich_tool(t) for t in upstream_tools]

                _log(f"[proxy] Discovered {len(enriched)} upstream tools: "
                     f"{', '.join(t.name for t in enriched)}")

                @self.server.list_tools()
                async def handle_list_tools() -> list[types.Tool]:
                    return enriched

                @self.server.call_tool()
                async def handle_call_tool(
                    name: str, arguments: dict | None
                ) -> Sequence[types.TextContent | types.ImageContent | types.EmbeddedResource]:
                    return await self._handle_call(upstream, name, arguments or {})

                async with stdio_server() as (down_read, down_write):
                    await self.server.run(
                        down_read, down_write,
                        self.server.create_initialization_options(),
                    )

    async def _handle_call(
        self,
        upstream: ClientSession,
        name: str,
        arguments: dict,
    ) -> list[types.TextContent]:
        price = str(self.pricing.price_for(name))

        # 1. Create escrow (lock funds)
        try:
            escrow = await self.backend.create_escrow(
                self.buyer_addr, self.seller_addr, price,
            )
        except Exception as e:
            return [types.TextContent(
                type="text",
                text=(
                    f"Payment failed: {e}\n"
                    f"Funds status: no funds were locked\n"
                    f"Recovery: check your balance or add funds"
                ),
            )]

        # 2. Forward to upstream
        try:
            result = await upstream.call_tool(name, arguments)
        except Exception as e:
            await _safe_dispute(self.backend, escrow.escrow_id, f"upstream_error: {e}")
            return [types.TextContent(
                type="text",
                text=(
                    f"Tool call failed: {e}\n"
                    f"Funds status: escrow {escrow.escrow_id} disputed, ${price} USDC refunded\n"
                    f"Recovery: retry the tool call"
                ),
            )]

        # 3. Upstream returned error — dispute
        if result.isError:
            await _safe_dispute(self.backend, escrow.escrow_id, "tool_returned_error")
            error_text = _extract_text(result)
            return [types.TextContent(
                type="text",
                text=(
                    f"{error_text}\n\n"
                    f"--- Payment ---\n"
                    f"Funds status: escrow {escrow.escrow_id} disputed, ${price} USDC refunded\n"
                    f"Recovery: fix the input and retry"
                ),
            )]

        # 4. Success — confirm escrow
        confirm_note = "confirmed"
        try:
            await self.backend.confirm_escrow(escrow.escrow_id)
        except Exception as ce:
            confirm_note = f"auto-confirm failed ({ce}), will auto-release after timeout"
            _log(f"[warn] confirm failed for {escrow.escrow_id}: {ce}")

        result_text = _extract_text(result)
        return [types.TextContent(
            type="text",
            text=(
                f"{result_text}\n\n"
                f"--- Payment ---\n"
                f"Cost: ${price} USDC\n"
                f"Escrow: {escrow.escrow_id}\n"
                f"Status: {confirm_note}"
            ),
        )]


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

def _log(msg: str) -> None:
    print(msg, file=sys.stderr)


def _extract_text(result: types.CallToolResult) -> str:
    parts = []
    for item in result.content or []:
        if hasattr(item, "text"):
            parts.append(item.text)
        else:
            parts.append(str(item))
    return "\n".join(parts) if parts else "(no content)"


async def _safe_dispute(backend: PaymentBackend, escrow_id: str, reason: str) -> None:
    try:
        await backend.dispute_escrow(escrow_id, reason)
    except Exception as e:
        _log(f"[warn] dispute failed for {escrow_id}: {e}")


# ---------------------------------------------------------------------------
# CLI
# ---------------------------------------------------------------------------

def main() -> None:
    parser = argparse.ArgumentParser(
        description="Wrap any MCP server with Alancoin payments",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog=(
            "Examples:\n"
            "  # Demo mode (in-memory ledger)\n"
            '  alancoin-mcp-proxy --upstream "python my_server.py" --price 0.001 --demo\n'
            "\n"
            "  # Per-tool pricing\n"
            '  alancoin-mcp-proxy --upstream "python my_server.py" --pricing pricing.json --demo\n'
            "\n"
            "  # Platform mode (real escrow)\n"
            "  ALANCOIN_API_KEY=ak_... alancoin-mcp-proxy \\\n"
            '    --upstream "python my_server.py" --price 0.001 \\\n'
            "    --seller-addr 0x... --buyer-addr 0x...\n"
        ),
    )
    parser.add_argument("--upstream", required=True, help="Command to launch upstream MCP server")
    parser.add_argument("--price", help="Flat price per tool call in USDC (e.g., 0.001)")
    parser.add_argument("--pricing", help="Path to JSON pricing file")
    parser.add_argument("--demo", action="store_true", help="Use in-memory demo backend")
    parser.add_argument("--seller-addr", help="Seller wallet address (platform mode)")
    parser.add_argument("--buyer-addr", help="Buyer wallet address (platform mode)")
    parser.add_argument("--api-url", default="http://localhost:8080", help="Alancoin API URL")

    args = parser.parse_args()

    # Pricing
    if args.pricing:
        pricing = ToolPricing.from_json_file(args.pricing)
    elif args.price:
        pricing = ToolPricing.from_flat(args.price)
    else:
        parser.error("--price or --pricing is required")

    # Backend
    if args.demo:
        buyer = args.buyer_addr or "demo_buyer"
        seller = args.seller_addr or "demo_seller"
        backend: PaymentBackend = DemoBackend(
            buyer_addr=buyer, seller_addr=seller,
        )
    else:
        import os
        api_key = os.environ.get("ALANCOIN_API_KEY")
        if not api_key:
            parser.error("ALANCOIN_API_KEY env var required (or use --demo)")
        if not args.seller_addr:
            parser.error("--seller-addr required in platform mode")
        if not args.buyer_addr:
            parser.error("--buyer-addr required in platform mode")

        from .client import Alancoin
        client = Alancoin(base_url=args.api_url, api_key=api_key)
        buyer = args.buyer_addr
        seller = args.seller_addr
        backend = AlancoinBackend(client=client, buyer_addr=buyer, seller_addr=seller)

    proxy = MCPPaymentProxy(
        upstream_command=args.upstream,
        pricing=pricing,
        backend=backend,
        buyer_addr=buyer,
        seller_addr=seller,
    )

    _log("[proxy] Starting MCP Payment Proxy")
    _log(f"[proxy] Upstream: {args.upstream}")
    _log(f"[proxy] Mode: {'demo' if args.demo else 'platform'}")
    _log(f"[proxy] Default price: ${pricing.default_price} USDC")

    asyncio.run(proxy.run())


if __name__ == "__main__":
    main()
