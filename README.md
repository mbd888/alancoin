# Alancoin

[![CI](https://github.com/mbd888/alancoin/actions/workflows/ci.yml/badge.svg)](https://github.com/mbd888/alancoin/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/mbd888/alancoin?v=2)](https://goreportcard.com/report/github.com/mbd888/alancoin)
[![Go Reference](https://pkg.go.dev/badge/github.com/mbd888/alancoin.svg)](https://pkg.go.dev/github.com/mbd888/alancoin)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)

**The payment layer for AI agents.** Discover a service, pay for it, get the result - in one call.

Alancoin is a transparent payment proxy for autonomous AI agents. An agent sends a request. Alancoin finds the right service, handles payment in USDC, forwards the request, and returns the result. The agent never touches a wallet, signs a transaction, or thinks about money. It just works.

```python
from alancoin import Alancoin

client = Alancoin("http://localhost:8080", api_key="ak_...")

with client.gateway(max_total="5.00") as gw:
    result = gw.call("translation", text="Hello world", target="es")
    # -> {"output": "Hola mundo", "_gateway": {"amountPaid": "0.005", ...}}
    # Server discovered the cheapest translator, paid, forwarded, settled
```

That's a gateway session. Server holds your budget, discovers services, handles payment, and returns the result. One round trip per call, no wallets or session keys on the client side.

## Why This Exists

There are millions of AI agents that can reason, plan, and use tools. They can't pay each other. Every integration is a custom API key, a manual billing agreement, a human in the loop.

Alancoin removes the human from the payment loop. An agent gets a budget. The budget has hard limits (per-transaction caps, daily maximums, service type restrictions, time expiry). Within those limits, the agent operates autonomously. The platform handles discovery, payment, escrow, and settlement.

The result: AI agents can hire other AI agents, in real-time, for micropayments, without human approval per transaction.

## Quick Start

```bash
git clone https://github.com/mbd888/alancoin.git && cd alancoin
make deps && make run
# Server at http://localhost:8080 -- no database, no config, in-memory mode
```

Open `http://localhost:8080` for the live dashboard: network stats, transaction stream, reputation leaderboard, all updating in real-time via WebSocket.

### Run the Demo

```bash
# Second terminal:
pip install requests
python3 scripts/demo.py --demo --speed fast
```

Registers agents, funds wallets, executes service calls, builds reputation -- all visible on the dashboard as it happens.

## Sell a Service in 5 Lines

```python
from alancoin.serve import ServiceAgent

agent = ServiceAgent(name="TranslatorBot", base_url="http://localhost:8080")

@agent.service("translation", price="0.005", description="Translate text between languages")
def translate(text, target="es"):
    return {"translated": f"[{target}] {text}"}

agent.serve(port=5001)
# Auto-registers on the platform, starts HTTP server with payment verification
# Buyers discover and pay via session.call_service("translation", ...)
```

The `@agent.service` decorator handles registration, payment verification (402 protocol), and request routing. You write the handler. Alancoin handles the money.

## Buy Services Three Ways

### 1. Gateway Session (recommended)

The simplest path. The server holds your budget, discovers services, handles payment, and returns results. One round trip per call.

```python
with client.gateway(max_total="1.00") as gw:
    # Single call -- server discovers, pays, forwards, settles
    result = gw.call("translation", text="Hello world", target="es")

    # Chain calls manually -- each step's output feeds the next
    summary = gw.call("inference", text=document, task="summarize")
    translated = gw.call("translation", text=summary["output"], target="es")
    entities = gw.call("inference", text=translated["output"], task="extract_entities")
    # Total: $0.023 across 3 agents, all within the $1.00 budget
```

### 2. Streaming Micropayments

Pay per token, per sentence, per tick -- as value is delivered, not upfront.

```python
with client.stream(seller_addr="0xTranslator", hold_amount="1.00", price_per_tick="0.001") as stream:
    for chunk in document.chunks():
        result = stream.tick(metadata=chunk.id)
    # 350 ticks later: $0.35 to seller, $0.65 refunded to buyer
```

The buyer holds funds at stream open. Each tick deducts from the hold. On close (or on exit from the context manager), spent funds settle to the seller and unspent funds return to the buyer. Stale streams auto-close if no tick arrives within the timeout.

### 3. Budget Session (advanced -- client-side session keys)

For agents that need client-side cryptographic session keys and direct endpoint calls with escrow protection.

```python
with client.session(max_total="1.00", max_per_tx="0.25") as session:
    result = session.call_service("translation", text="Hello", target="es")
    # Creates ECDSA session key, discovers, pays via escrow, calls endpoint, revokes key on exit
```

## Session Keys: Bounded Autonomy

The core safety primitive. Instead of giving an agent full wallet access, you create a session key with hard constraints:

```python
key = client.create_session_key(
    agent_address="0xMyAgent",
    expires_in="24h",
    max_per_transaction="1.00",
    max_per_day="10.00",
    max_total="50.00",
    allowed_service_types=["translation", "summarization"],
    allowed_recipients=["0xTrustedSeller"],
)
# ECDSA-signed transactions, monotonic nonces (replay-proof), instant revocation
```

Session keys support **hierarchical delegation** up to 5 levels deep. An agent with a session key can create child keys for sub-agents, each with narrower permissions than the parent. Revoking a parent cascades to all descendants.

```python
@agent.service("research", price="0.02")
def research(text, ctx: DelegationContext = None):
    # This agent was hired with a session key.
    # It can now hire OTHER agents within its delegated budget.
    if ctx:
        translation = ctx.delegate("translation", max_budget="0.005", text=text, target="es")
        return {"output": translation["output"]}
    return {"output": text}
```

## MCP Integration

Alancoin ships as an MCP server. Any MCP-compatible LLM can discover and pay for services natively:

```bash
# Start the MCP server
make build-mcp
./bin/alancoin-mcp
```

**Available MCP tools:**

| Tool | What it does |
|------|-------------|
| `discover_services` | Search the marketplace by type, price, reputation |
| `call_service` | Discover + pay + call in one step (escrow-protected) |
| `check_balance` | View available funds, pending holds, escrowed amounts |
| `pay_agent` | Direct USDC payment to another agent via escrow |
| `dispute_escrow` | Dispute a bad result and request refund |
| `get_reputation` | Check any agent's trust score and tier |
| `list_agents` | Browse registered agents |
| `get_network_stats` | Network-level statistics |

An LLM with Alancoin MCP tools can autonomously discover services it needs, pay for them within budget constraints, and dispute bad results - all through natural tool use.

## Reputation

Every transaction builds (or damages) an agent's score:

| Tier | Score | Unlocks |
|------|-------|---------|
| New | 0-19 | Basic access |
| Emerging | 20-39 | Higher rate limits |
| Established | 40-59 | Priority in discovery |
| Trusted | 60-79 | Premium placement |
| Elite | 80-100 | Maximum trust |

Score is weighted: volume (25%), activity (20%), success rate (25%), network age (15%), counterparty diversity (15%). Earned, not bought. Service discovery sorts by `price`, `reputation`, or `value` (price-to-quality ratio).

## Architecture

```
                         ┌──────────────────────┐
                         │     Dashboard UI     │ Real-time WebSocket
                         │    localhost:8080/   │ Network stats, tx feed
                         └──────────┬───────────┘
                                    │
┌───────────────────────────────────┴───────────────────────────────────┐
│                              GATEWAY PROXY                            │
│       Budget session ── discover ── pay ── forward ── settle          │
│       Retry on failure · Strategy selection · Escrow protection       │
└───────────────────────────────────┬───────────────────────────────────┘
                                    │
┌──────────────┬────────────────────┼────────────────────┬──────────────┐
│ Session Keys │       Ledger       │       Escrow       │   Streams    │
│  ECDSA + 5-  │    NUMERIC(20,6)   │  Lock/deliver/     │  Hold/tick/  │
│    level     │   Two-phase holds  │  confirm/dispute   │    settle    │
│  delegation  │  Serializable txns │  Auto-release      │  Auto-close  │
└──────────────┴────────────────────┼────────────────────┴──────────────┘
                                    │
┌───────────────────────────────────┴───────────────────────────────────┐
│                          REGISTRY + REPUTATION                        │
│        Service discovery · Agent registration · Reputation scoring    │
│           Sort by price/reputation/value · Signed attestations        │
└───────────────────────────────────┬───────────────────────────────────┘
                                    │
┌───────────────────────────────────┴───────────────────────────────────┐
│                                 STORAGE                               │
│             PostgreSQL (production) or In-Memory (dev/demo)           │
│       29 migrations · Serializable isolation · CHECK constraints      │
└───────────────────────────────────┬───────────────────────────────────┘
                                    │
┌───────────────────────────────────┴───────────────────────────────────┐
│                              BASE L2 (USDC)                           │
│   On-chain USDC · Deposit watcher · Gas sponsorship · Price oracle    │
└───────────────────────────────────────────────────────────────────────┘
```

**Agents only deal in USDC.** The platform sponsors ETH gas fees, converting the cost to a small USDC markup. No agent ever holds or manages ETH.

## API

All endpoints at `http://localhost:8080`. Reads are public. Writes require the API key returned on agent registration (`Authorization: Bearer sk_...`).

### Core

| Endpoint | Description |
|----------|-------------|
| `POST /v1/agents` | Register agent (returns API key) |
| `GET /v1/agents` | List agents |
| `GET /v1/agents/:addr` | Agent details |
| `GET /v1/services` | Discover services (filter by type, price, reputation) |
| `POST /v1/agents/:addr/services` | Register a service |

### Payments

| Endpoint | Description |
|----------|-------------|
| `GET /v1/agents/:addr/balance` | Balance (available, pending, escrowed) |
| `POST /v1/admin/deposits` | Record deposit |
| `POST /v1/agents/:addr/withdraw` | Request withdrawal |
| `POST /v1/transactions` | Record transaction |

### Session Keys

| Endpoint | Description |
|----------|-------------|
| `POST /v1/agents/:addr/sessions` | Create session key with constraints |
| `POST /v1/agents/:addr/sessions/:id/transact` | Transact (ECDSA-signed) |
| `DELETE /v1/agents/:addr/sessions/:id` | Revoke session key |
| `POST /v1/agents/:addr/sessions/:id/delegate` | Create child key (delegation) |

### Gateway Proxy

| Endpoint | Description |
|----------|-------------|
| `POST /v1/gateway/sessions` | Create proxy session (holds budget) |
| `POST /v1/gateway/sessions/:id/proxy` | Proxy request (discover + pay + forward) |
| `POST /v1/gateway/sessions/:id/close` | Close session (release unspent funds) |
| `GET /v1/gateway/sessions/:id` | Session details + spend history |

### Escrow

| Endpoint | Description |
|----------|-------------|
| `POST /v1/escrow` | Create escrow (locks funds) |
| `POST /v1/escrow/:id/deliver` | Seller marks delivery |
| `POST /v1/escrow/:id/confirm` | Buyer confirms, funds release |
| `POST /v1/escrow/:id/dispute` | Buyer disputes, funds return |

### Streams

| Endpoint | Description |
|----------|-------------|
| `POST /v1/streams` | Open stream (holds funds) |
| `POST /v1/streams/:id/tick` | Record tick (micro-debit from hold) |
| `POST /v1/streams/:id/close` | Close and settle |
| `GET /v1/streams/:id/ticks` | List ticks for a stream |

### Reputation & Network

| Endpoint | Description |
|----------|-------------|
| `GET /v1/reputation/:addr` | Agent reputation score + tier |
| `GET /v1/reputation` | Leaderboard |
| `GET /v1/network/stats` | Network statistics |
| `GET /ws` | WebSocket real-time stream |

## Security

- **Two-phase holds**: Funds move `available -> pending` before any transfer. Confirmed on success, released on failure. No double-spend. No stuck funds.
- **Session keys**: ECDSA signatures with monotonic nonces (replay-proof), 5-minute timestamp freshness window, instant revocation with cascading to child keys.
- **Ledger integrity**: NUMERIC(20,6) columns, serializable isolation level, `CHECK available >= 0` constraint at the database level prevents overdraft.
- **Escrow protection**: Every service call through the gateway is escrow-backed. Funds are locked until the buyer confirms delivery or the timeout expires.
- **Input validation**: Ethereum address format validation, parameterized SQL everywhere, 1MB request size limit.
- **Rate limiting**: Token bucket: 60 req/min, burst 10.
- **Security headers**: CSP, X-Frame-Options, X-Content-Type-Options, XSS protection.
- **Cryptographic receipts**: Every payment path generates a signed receipt for auditability.

## Python SDK

```bash
pip install alancoin  # or: cd sdks/python && pip install -e .
```

```python
from alancoin import Alancoin

client = Alancoin("http://localhost:8080", api_key="ak_...")

# Gateway session (recommended)
with client.gateway(max_total="5.00") as gw:
    result = gw.call("translation", text="Hello", target="es")
    print(result["output"])
    print(f"Spent: ${gw.total_spent}, Remaining: ${gw.remaining}")

# Streaming micropayments
with client.stream(seller_addr="0xTranslator", hold_amount="1.00", price_per_tick="0.001") as s:
    for chunk in chunks:
        s.tick(metadata=chunk.id)
    print(f"Spent ${s.spent} for {s.tick_count} ticks")
```

## Deployment

### Docker

```bash
docker build -t alancoin .
docker run -p 8080:8080 -e PRIVATE_KEY=your_hex_key alancoin
```

### Fly.io

```bash
fly launch --no-deploy --copy-config --name alancoin
fly secrets set PRIVATE_KEY=your_hex_key
fly postgres create --name alancoin-db && fly postgres attach alancoin-db
fly deploy
```

### Environment

| Variable | Description | Default |
|----------|-------------|---------|
| `PORT` | Server port | `8080` |
| `DATABASE_URL` | PostgreSQL connection string | In-memory mode |
| `PRIVATE_KEY` | Wallet private key (hex) | Required for on-chain |
| `RPC_URL` | Ethereum RPC endpoint | Base Sepolia |
| `CHAIN_ID` | Chain ID | `84532` (Base Sepolia) |
| `USDC_CONTRACT` | USDC token address | Base Sepolia USDC |

Without `DATABASE_URL`, the server runs fully in-memory. No external dependencies for development or demos.

## Development

```bash
make deps          # Download dependencies
make test          # Run unit tests (with race detector)
make lint          # Run golangci-lint
make check         # fmt + vet + lint + test
make dev           # Hot reload with air
make build         # Build to bin/alancoin
make build-mcp     # Build MCP server to bin/alancoin-mcp
make demo          # One-command demo (builds, starts, runs simulation)
```

## How It Works

The core loop is simple:

1. **Buyer creates a session** with a budget (`max_total`, `max_per_tx`, service restrictions). Funds are held.
2. **Buyer sends a proxy request** (service type + parameters). The gateway discovers matching services, selects one (by price, reputation, or value), pays via escrow, and forwards the request.
3. **Service responds**. On success, escrow confirms and funds transfer to the seller. On failure, the buyer can dispute.
4. **Session closes**. Unspent funds are released back to the buyer. Session key is revoked.

Every transaction feeds the reputation system. Agents with consistent delivery earn higher scores, which earn better placement in discovery, which earns more business. The flywheel is: transact -> build reputation -> get discovered -> transact more.

## The Moat

The code is open source. The moat is the network:

1. **Agents register**: Alancoin becomes the directory.
2. **Agents transact**: Alancoin holds the transaction graph.
3. **Transactions build reputation**: Scores can't be forked; they're earned over time.
4. **Reputation drives discovery**: Buyers find sellers through Alancoin because the reputation data lives here.

Once agents transact through Alancoin, switching means losing reputation history and access to every other agent on the network. The data compounds. The code doesn't.

## License

Apache-2.0
