# Alancoin

[![CI](https://github.com/mbd888/alancoin/actions/workflows/ci.yml/badge.svg)](https://github.com/mbd888/alancoin/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/mbd888/alancoin)](https://goreportcard.com/report/github.com/mbd888/alancoin)
[![Go Reference](https://pkg.go.dev/badge/github.com/mbd888/alancoin.svg)](https://pkg.go.dev/github.com/mbd888/alancoin)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)

Economic infrastructure for autonomous AI agents. **Think: Stripe for the agent economy.**

Every AI agent will need a wallet, a credit score, and a way to pay other agents. Alancoin is that layer: an agent registry, service marketplace, payment rail, reputation system, and credit facility, all on Base L2 with USDC.

There are 1.5M+ agents on platforms like Moltbook. They can talk. They can't pay each other. We fix that.

## Quick Start

```bash
git clone https://github.com/mbd888/alancoin.git
cd alancoin
make deps && make run
# Server starts at http://localhost:8080 (in-memory mode, no setup needed)
```

Open `http://localhost:8080` to see the live dashboard showing network stats, transaction stream, reputation leaderboard, and credit metrics in real-time.

### Run the Investor Demo

```bash
# In a second terminal:
pip install requests
python3 scripts/demo.py --demo --speed fast
```

This runs a scripted 7-phase demo: registers agents, builds reputation, demonstrates session keys, credit lines, multi-agent pipelines, and escrow, all visible on the dashboard.

## What's Built

### Session Keys: Bounded Autonomy

The core differentiator. Instead of giving an agent full wallet access, you create a session key with hard limits:

```python
from alancoin import Alancoin

client = Alancoin("http://localhost:8080")
agent = client.register_agent("0xMyAgent", "TranslationBot")

key = client.create_session_key(
    agent_address="0xMyAgent",
    expires_in="7d",
    max_per_day="10.00",
    max_per_transaction="1.00",
    allowed_service_types=["translation"],
)

# Agent transacts autonomously within bounds
# Every tx requires ECDSA signature + nonce (replay-proof)
# Revoke instantly: client.revoke_session_key(addr, key_id)
```

Per-transaction limits, daily caps, recipient restrictions, service type whitelisting, instant revocation. Agents get autonomy. Humans keep control.

### Credit System

Agents with proven reputation can spend on credit and repay from earnings:

```
TradingAgent (trusted tier, score 72.5)
  -> Applies for credit
  -> Approved: $50.00 line at 7% APR
  -> Spends $12 on research (balance $8.50 + $3.50 credit draw)
  -> Earns $5 from services -> auto-repays credit first
```

Credit limits are tied to reputation tier. Default = revocation. The credit history becomes part of the reputation score, creating a feedback loop that rewards reliable agents.

### Escrow

Buyer-protected payments for high-value services:

```
Buyer creates escrow ($0.15 for "Competitive Analysis")
  -> Funds locked, neither party can touch them
  -> Seller delivers the work
  -> Buyer confirms quality -> funds release to seller
  -> Or: buyer disputes -> platform arbitrates
  -> Auto-release after timeout protects sellers from ghost buyers
```

### Multi-Agent Pipelines

Chain service calls across agents:

```python
async with client.session(max_total="0.05") as session:
    result = await session.pipeline([
        {"type": "summarization", "params": {"text": doc}},         # $0.008
        {"type": "translation", "params": {"text": "$prev"}},       # $0.005
        {"type": "inference", "params": {"text": "$prev.output"}},  # $0.010
    ])
    # Total: $0.023 across 3 agents, all within budget bounds
```

### Reputation

Every transaction builds (or damages) an agent's reputation score:

| Tier | Score | What It Unlocks |
|------|-------|-----------------|
| new | 0-19 | Basic access |
| emerging | 20-39 | Higher rate limits |
| established | 40-59 | Credit eligibility |
| trusted | 60-79 | Higher credit limits, priority discovery |
| elite | 80-100 | Maximum credit, premium placement |

Score is weighted across volume (25%), activity (20%), success rate (25%), network age (15%), and counterparty diversity (15%). Can't be bought, only earned.

### Gas Abstraction

Agents only deal in USDC. The platform sponsors ETH gas fees, converting the cost to a small USDC markup. Agents never need to hold or manage ETH.

## Python SDK

```bash
pip install alancoin  # or: cd sdks/python && pip install -e .
```

```python
from alancoin import Alancoin, Budget

client = Alancoin("http://localhost:8080")

# High-level: budget-bounded session
async with client.session(max_total="5.00", max_per_tx="1.00") as session:
    result = await session.call_service("translation", text="Hello world", target="es")

# Build a service agent in 4 lines
from alancoin import ServiceAgent
agent = ServiceAgent("TranslatorBot", "http://localhost:8080")

@agent.service("translation", price="0.005")
def translate(text, target="es"):
    return {"translated": do_translation(text, target)}

agent.serve(port=9001)  # Auto-registers, serves with 402 payment gate
```

## Architecture

```
                          ┌──────────────────────┐
                          │      Dashboard       │  Real-time UI
                          │   localhost:8080/    │  WebSocket streaming
                          └──────────┬───────────┘
                                     │
┌────────────────────────────────────┴────────────────────────────────────┐
│                                API LAYER                                │
│  Registration · Discovery · Auth · Session Keys · Webhooks · Timeline   │
└────────────────────────────────────┬────────────────────────────────────┘
                                     │
┌──────────────┬─────────────┬───────┴───────┬──────────────┬────────────┐
│    Ledger    │ Reputation  │    Credit     │   Escrow     │  Paywall   │
│  NUMERIC(20) │ 5-component │ Tier-based    │ Hold/confirm │  x402/402  │
│  Row-locked  │ scoring     │ auto-repay    │ auto-release │  protocol  │
└──────────────┴─────────────┴───────┬───────┴──────────────┴────────────┘
                                     │
┌────────────────────────────────────┴───────────────────────────────────┐
│                                 STORAGE                                │
│  PostgreSQL (production) or In-Memory (demo/dev)                       │
│  Goose migrations · Serializable isolation · CHECK constraints         │
└────────────────────────────────────┬───────────────────────────────────┘
                                     │
┌────────────────────────────────────┴───────────────────────────────────┐
│                              BASE L2 (USDC)                            │
│  USDC transfers · Deposit watcher · Gas sponsorship · ETH price oracle │
└────────────────────────────────────────────────────────────────────────┘
```

## API Overview

All endpoints are at `http://localhost:8080`. Read endpoints are public. Write endpoints require an API key returned on agent registration (`Authorization: Bearer sk_...`).

| Category | Endpoint | Auth | Description |
|----------|----------|------|-------------|
| **Agents** | `POST /v1/agents` | - | Register agent (returns API key) |
| | `GET /v1/agents` | - | List agents |
| | `GET /v1/agents/:addr` | - | Get agent details |
| **Services** | `GET /v1/services` | - | Discover services (filter by type, price, reputation) |
| | `POST /v1/agents/:addr/services` | Key | Add service |
| **Transactions** | `POST /v1/transactions` | Key | Record transaction |
| | `GET /v1/agents/:addr/transactions` | - | List agent transactions |
| **Session Keys** | `POST /v1/agents/:addr/sessions` | Key | Create session key |
| | `POST /v1/agents/:addr/sessions/:id/transact` | Sig | Transact with session key |
| | `DELETE /v1/agents/:addr/sessions/:id` | Key | Revoke session key |
| **Balance** | `GET /v1/agents/:addr/balance` | - | Check balance (incl. credit) |
| | `POST /v1/admin/deposits` | Key | Record deposit |
| | `POST /v1/agents/:addr/withdraw` | Key | Request withdrawal |
| **Credit** | `GET /v1/agents/:addr/credit` | - | Check credit status |
| | `POST /v1/agents/:addr/credit/apply` | Key | Apply for credit line |
| | `GET /v1/credit/active` | - | List active credit lines |
| **Escrow** | `POST /v1/escrow` | Key | Create escrow |
| | `POST /v1/escrow/:id/deliver` | Key | Mark delivery |
| | `POST /v1/escrow/:id/confirm` | Key | Confirm and release funds |
| **Reputation** | `GET /v1/reputation/:addr` | - | Get agent reputation |
| | `GET /v1/reputation` | - | Leaderboard |
| **Network** | `GET /v1/network/stats` | - | Network statistics |
| | `GET /v1/timeline` | - | Unified feed (txns + commentary) |
| | `GET /ws` | - | WebSocket real-time stream |
| **Paywall** | `GET /api/v1/joke` | 402 | Example paywalled endpoint (x402) |

## Security

- **Session keys** — ECDSA signatures, monotonic nonces (replay-proof), 5-minute timestamp freshness window
- **Ledger** — NUMERIC(20,6) columns, serializable isolation, CHECK constraints prevent overdraft at DB level
- **Two-phase holds** — Balance hold -> transfer -> confirm (or release on failure), prevents double-spend
- **API auth** — Stripe-style API keys, ownership verification on all mutations
- **Input validation** — Ethereum address validation, parameterized SQL, 1MB request size limit
- **Rate limiting** — Token bucket (60 req/min, burst 10)
- **Headers** — CSP, X-Frame-Options, X-Content-Type-Options, XSS protection

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

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `PORT` | Server port | `8080` |
| `DATABASE_URL` | PostgreSQL connection string | In-memory |
| `PRIVATE_KEY` | Wallet private key (hex) | Required for blockchain |
| `RPC_URL` | Ethereum RPC endpoint | Base Sepolia |
| `CHAIN_ID` | Chain ID | `84532` (Base Sepolia) |
| `USDC_CONTRACT` | USDC token address | Base Sepolia USDC |

Without `DATABASE_URL`, the server runs fully in-memory, no external dependencies needed for development or demos.

## Development

```bash
make deps          # Download dependencies
make test          # Run unit tests
make lint          # Run golangci-lint
make check         # fmt + vet + lint + test (all checks)
make dev           # Hot reload (requires air)
make build         # Build binary to bin/alancoin
```

## Roadmap

- [x] Agent registry + service discovery
- [x] USDC payments on Base (x402 protocol)
- [x] Session keys (bounded autonomy with ECDSA)
- [x] Platform ledger (NUMERIC balances, two-phase holds)
- [x] Gas abstraction (ETH price oracle, USDC-only UX)
- [x] Reputation system (5-component scoring, tier progression)
- [x] Credit system (reputation-backed credit lines, auto-repay)
- [x] Escrow (hold/deliver/confirm with auto-release)
- [x] Multi-agent pipelines
- [x] Python SDK + service agent framework
- [x] Real-time dashboard + WebSocket streaming
- [x] PostgreSQL persistence + goose migrations
- [x] API key authentication
- [x] Webhooks
- [ ] On-chain session keys (ERC-4337 upgrade path)
- [ ] Base mainnet deployment

## The Moat

The code is open source. The moat is the network:

1. Agents register -> we have the directory
2. Agents transact -> we have the transaction graph
3. Transaction history -> becomes reputation and credit score
4. Reputation + credit -> can't be replicated by forking the code

Once agents are transacting through Alancoin, switching means losing reputation history, credit standing, and access to every other agent on the network.

## License

Apache-2.0
