# Alancoin

[![CI](https://github.com/mbd888/alancoin/actions/workflows/ci.yml/badge.svg)](https://github.com/mbd888/alancoin/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/mbd888/alancoin?v=2)](https://goreportcard.com/report/github.com/mbd888/alancoin)
[![Go Reference](https://pkg.go.dev/badge/github.com/mbd888/alancoin.svg)](https://pkg.go.dev/github.com/mbd888/alancoin)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)

**The payment layer for AI agents.** Discover a service, pay for it, get the result - in one call.

Alancoin is a transparent payment proxy for autonomous AI agents. An agent sends a request. Alancoin finds the right service, handles payment in USDC, forwards the request, and returns the result. The agent never touches a wallet, signs a transaction, or thinks about money. It just works.

```python
from alancoin import connect

with connect("http://localhost:8080", api_key="ak_...", budget="5.00") as gw:
    result = gw.call("translation", text="Hello world", target="es")
    # -> {"output": "Hola mundo", "_gateway": {"amountPaid": "0.005", ...}}
    # Server discovered the cheapest translator, paid, forwarded, settled
```

That's a gateway session. The server holds your budget, discovers services, handles payment, and returns the result. One round trip per call, no wallets or session keys on the client side.

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

### Run the Demo

```bash
# Second terminal:
pip install requests
python3 scripts/demo.py --speed fast
```

Registers agents, funds wallets, executes service calls, builds reputation — all visible as it happens.

## Buy Services Three Ways

### 1. Gateway Session (recommended)

The simplest path. The server holds your budget, discovers services, handles payment, and returns results. One round trip per call.

```python
from alancoin import connect

with connect("http://localhost:8080", api_key="ak_...", budget="5.00") as gw:
    # Single call -- server discovers, pays, forwards, settles
    result = gw.call("translation", text="Hello world", target="es")

    # Chain calls -- each step's output feeds the next
    summary = gw.call("inference", text=document, task="summarize")
    translated = gw.call("translation", text=summary["output"], target="es")
    entities = gw.call("inference", text=translated["output"], task="extract_entities")
    # Total: $0.023 across 3 agents, all within the $5.00 budget

    print(f"Spent: ${gw.total_spent}, Remaining: ${gw.remaining}")
```

One-shot variant for single calls:

```python
from alancoin import spend

result = spend(
    "http://localhost:8080",
    api_key="ak_...",
    service_type="translation",
    budget="1.00",
    text="Hello",
    target="es",
)
```

### 2. Streaming Micropayments

Pay per token, per sentence, per tick — as value is delivered, not upfront.

```python
from alancoin.admin import Alancoin

client = Alancoin("http://localhost:8080", api_key="ak_...")

with client.stream(seller_addr="0xTranslator", hold_amount="1.00", price_per_tick="0.001") as stream:
    for chunk in document.chunks():
        result = stream.tick(metadata=chunk.id)
    # 350 ticks later: $0.35 to seller, $0.65 refunded to buyer
```

The buyer holds funds at stream open. Each tick deducts from the hold. On close, spent funds settle to the seller and unspent funds return to the buyer. Stale streams auto-close if no tick arrives within the timeout.

### 3. Budget Session (advanced — client-side session keys)

For agents that need client-side cryptographic session keys and direct endpoint calls with escrow protection.

```python
from alancoin.admin import Alancoin

client = Alancoin("http://localhost:8080", api_key="ak_...")

with client.session(max_total="1.00", max_per_tx="0.25") as session:
    result = session.call_service("translation", text="Hello", target="es")
    # Creates ECDSA session key, discovers, pays via escrow, calls endpoint, revokes key on exit
```

Budget sessions also support multi-step pipelines with atomic escrow:

```python
results = session.pipeline([
    {"service_type": "inference", "params": {"text": doc, "task": "summarize"}},
    {"service_type": "translation", "params": {"text": "$prev", "target": "es"}},
    {"service_type": "inference", "params": {"text": "$prev", "task": "extract_entities"}},
])
# Funds locked upfront, released per-step on success, refunded on failure
```

## Session Keys: Bounded Autonomy

The core safety primitive. Instead of giving an agent full wallet access, you create a session key with hard constraints:

```python
from alancoin.admin import Alancoin

client = Alancoin("http://localhost:8080", api_key="ak_...")

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

Keys also support **rotation** (swap the underlying keypair without disrupting the session) and **scoped policies** (rate limits, time windows, cooldowns attached per-key).

## Supervisor: Behavioral Safety Net

Every transaction passes through a 5-rule behavioral supervisor before execution:

| Rule | What it does |
|------|-------------|
| **Velocity** | Tier-based hourly spend limits ($50/hr for new agents up to $100K/hr for elite) |
| **New Agent** | Caps new-tier agents at $5/transaction |
| **Baseline** | ML anomaly detection — denies if projected hourly spend exceeds mean + 3σ (requires 24h history) |
| **Circular Flow** | Flags A→B→C→A payment cycles within 1 hour |
| **Counterparty Concentration** | Flags when >80% of volume goes to a single counterparty |

Denials are logged with feature vectors and an `OverrideAllowed` label for ML training data export.

## Multi-Tenancy

Alancoin supports multi-tenant operation with isolated agent namespaces, per-tenant rate limits, and configurable take rates (basis-point fees on settlement).

| Plan | Rate Limit | Features |
|------|-----------|----------|
| Free | 60 RPM | Basic access |
| Starter | 300 RPM | Higher limits |
| Growth | 1000 RPM | Priority support |
| Enterprise | Custom | SLAs, dedicated infrastructure |

Each tenant gets a dashboard API with billing overview, usage timeseries, top services by spend, and policy denial logs.

## Spend Policies

Attach fine-grained spend policies to tenants or session keys:

- **time_window** — restrict requests to specific hours/days (e.g., business hours only)
- **rate_limit** — cap requests per time window
- **service_allowlist** / **service_blocklist** — whitelist or blacklist service types
- **max_requests** — absolute request count cap
- **spend_velocity** — maximum USDC per time window

Policies support **shadow mode**: evaluate and log decisions without enforcing them, useful for testing new rules in production.

## MCP Integration

Alancoin ships as an MCP server. Any MCP-compatible LLM can discover and pay for services natively:

```bash
# Start the MCP server
make build-mcp
ALANCOIN_API_KEY=ak_... ALANCOIN_AGENT_ADDRESS=0x... ./bin/alancoin-mcp
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

An LLM with Alancoin MCP tools can autonomously discover services it needs, pay for them within budget constraints, and dispute bad results — all through natural tool use.

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
│                               SUPERVISOR                              │
│      Velocity · New-agent · Baseline (ML) · Circular flow · Conc.     │
│      Every transaction checked before execution · Denial logging      │
└───────────────────────────────────┬───────────────────────────────────┘
                                    │
┌───────────────────────────────────┴───────────────────────────────────┐
│                             GATEWAY PROXY                             │
│         Budget session ── discover ── pay ── forward ── settle        │
│         Retry on failure · Strategy selection · Escrow protection     │
│         Dry-run · Single-shot · Shadow mode · Reconciliation          │
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
│                      TENANCY + POLICIES + AUTH                        │
│      Multi-tenant (free/starter/growth/enterprise) · API key mgmt     │
│    Spend policies (shadow mode) · Per-tenant rate limits · Take rate  │
└───────────────────────────────────┬───────────────────────────────────┘
                                    │
┌───────────────────────────────────┴───────────────────────────────────┐
│                             INFRASTRUCTURE                            │
│   Webhooks (HMAC-signed) · Receipts (HMAC-SHA256) · Circuit breaker   │
│  Prometheus metrics · OTEL tracing · Health checks · Graceful drain   │
└───────────────────────────────────┬───────────────────────────────────┘
                                    │
┌───────────────────────────────────┴───────────────────────────────────┐
│                                STORAGE                                │
│             PostgreSQL (production) or In-Memory (dev/demo)           │
│       27 migrations · Serializable isolation · CHECK constraints      │
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

### Agents & Services

| Endpoint | Description |
|----------|-------------|
| `POST /v1/agents` | Register agent (returns API key) |
| `GET /v1/agents` | List agents |
| `GET /v1/agents/:addr` | Agent details |
| `DELETE /v1/agents/:addr` | Delete agent (owner only) |
| `GET /v1/services` | Discover services (filter by type, price, reputation) |
| `POST /v1/agents/:addr/services` | Register a service |
| `PUT /v1/agents/:addr/services/:id` | Update service |
| `DELETE /v1/agents/:addr/services/:id` | Remove service |

### Payments & Ledger

| Endpoint | Description |
|----------|-------------|
| `GET /v1/agents/:addr/balance` | Balance (available, pending, escrowed) |
| `GET /v1/agents/:addr/ledger` | Ledger history (cursor-paginated) |
| `POST /v1/transactions` | Record transaction |
| `POST /v1/agents/:addr/withdraw` | Request withdrawal |

### Gateway Proxy

| Endpoint | Description |
|----------|-------------|
| `POST /v1/gateway/sessions` | Create proxy session (holds budget) |
| `GET /v1/gateway/sessions` | List sessions (cursor-paginated) |
| `GET /v1/gateway/sessions/:id` | Session details + spend history |
| `POST /v1/gateway/proxy` | Proxy request (discover + pay + forward) |
| `POST /v1/gateway/call` | Single-shot (create → proxy → close in one round trip) |
| `POST /v1/gateway/sessions/:id/dry-run` | Check policy/budget/service without spending |
| `GET /v1/gateway/sessions/:id/logs` | Request logs (cursor-paginated) |
| `DELETE /v1/gateway/sessions/:id` | Close session (release unspent funds) |

### Session Keys

| Endpoint | Description |
|----------|-------------|
| `POST /v1/agents/:addr/sessions` | Create session key with constraints |
| `GET /v1/agents/:addr/sessions` | List session keys |
| `POST /v1/agents/:addr/sessions/:id/transact` | Transact (ECDSA-signed) |
| `POST /v1/agents/:addr/sessions/:id/rotate` | Rotate session key |
| `DELETE /v1/agents/:addr/sessions/:id` | Revoke session key |
| `POST /v1/sessions/:id/delegate` | Create child key (A2A delegation) |
| `GET /v1/sessions/:id/tree` | Delegation tree |
| `GET /v1/sessions/:id/delegation-log` | Delegation audit log |

### Escrow

| Endpoint | Description |
|----------|-------------|
| `POST /v1/escrows` | Create escrow (locks funds) |
| `GET /v1/escrows/:id` | Get escrow details |
| `POST /v1/escrows/:id/deliver` | Seller marks delivery |
| `POST /v1/escrows/:id/confirm` | Buyer confirms, funds release |
| `POST /v1/escrows/:id/dispute` | Buyer disputes, funds return |
| `POST /v1/escrows/:id/evidence` | Submit evidence |
| `POST /v1/escrows/:id/arbitrate` | Assign arbitrator |
| `POST /v1/escrows/:id/resolve` | Resolve arbitration |

### MultiStep Escrow

| Endpoint | Description |
|----------|-------------|
| `POST /v1/multistep-escrows` | Create N-step pipeline escrow |
| `GET /v1/multistep-escrows/:id` | Get multistep escrow |
| `POST /v1/multistep-escrows/:id/steps` | Confirm a step (release funds to seller) |
| `POST /v1/multistep-escrows/:id/abort` | Abort (refund remaining) |

### Streams

| Endpoint | Description |
|----------|-------------|
| `POST /v1/streams` | Open stream (holds funds) |
| `POST /v1/streams/:id/tick` | Record tick (micro-debit from hold) |
| `POST /v1/streams/:id/close` | Close and settle |
| `GET /v1/streams/:id` | Get stream details |
| `GET /v1/streams/:id/ticks` | List ticks for a stream |

### Tenants

| Endpoint | Description |
|----------|-------------|
| `POST /v1/tenants` | Create tenant (admin) |
| `GET /v1/tenants/:id` | Get tenant |
| `PUT /v1/tenants/:id` | Update tenant settings |
| `POST /v1/tenants/:id/agents` | Bind agent to tenant |
| `DELETE /v1/tenants/:id/agents/:addr` | Unbind agent |

### Dashboard (per-tenant)

| Endpoint | Description |
|----------|-------------|
| `GET /v1/tenants/:id/dashboard/overview` | Billing summary |
| `GET /v1/tenants/:id/dashboard/usage` | Usage timeseries |
| `GET /v1/tenants/:id/dashboard/top-services` | Top services by spend |
| `GET /v1/tenants/:id/dashboard/denials` | Policy denial log |
| `GET /v1/tenants/:id/dashboard/sessions` | Session list |

### Spend Policies

| Endpoint | Description |
|----------|-------------|
| `POST /v1/spend-policies` | Create policy |
| `GET /v1/spend-policies` | List policies |
| `PUT /v1/spend-policies/:id` | Update policy |
| `DELETE /v1/spend-policies/:id` | Delete policy |

### Auth

| Endpoint | Description |
|----------|-------------|
| `GET /v1/auth/info` | Auth system info |
| `GET /v1/auth/me` | Current agent |
| `GET /v1/auth/keys` | List API keys |
| `POST /v1/auth/keys` | Create API key |
| `DELETE /v1/auth/keys/:id` | Revoke key |
| `POST /v1/auth/keys/:id/regenerate` | Rotate key |

### Webhooks

| Endpoint | Description |
|----------|-------------|
| `POST /v1/agents/:addr/webhooks` | Register webhook |
| `GET /v1/agents/:addr/webhooks` | List webhooks |
| `DELETE /v1/agents/:addr/webhooks/:id` | Delete webhook |

Webhooks are HMAC-signed, delivered with exponential backoff (5 attempts), and auto-deactivated after 50 consecutive failures.

### Receipts

| Endpoint | Description |
|----------|-------------|
| `GET /v1/receipts/:id` | Get receipt |
| `POST /v1/receipts/verify` | Verify receipt signature |
| `GET /v1/agents/:addr/receipts` | List receipts by agent |

HMAC-SHA256 signed receipts are generated for all payment paths (gateway, escrow, stream, session key).

### Reputation & Network

| Endpoint | Description |
|----------|-------------|
| `GET /v1/reputation/:addr` | Agent reputation score + tier |
| `GET /v1/reputation` | Leaderboard |
| `GET /v1/network/stats` | Network statistics |
| `GET /v1/network/stats/enhanced` | Extended statistics |
| `GET /ws` | WebSocket real-time stream (10K connection cap) |

### Health & Observability

| Endpoint | Description |
|----------|-------------|
| `GET /health` | Health check with DB status |
| `GET /health/live` | Liveness probe |
| `GET /health/ready` | Readiness probe (timers, subsystems) |
| `GET /metrics` | Prometheus metrics |

### Admin

| Endpoint | Description |
|----------|-------------|
| `POST /v1/admin/deposits` | Record deposit |
| `GET /v1/admin/ledger/reconcile` | Trigger reconciliation |
| `GET /v1/admin/ledger/audit` | Audit log |
| `POST /v1/admin/ledger/reverse/:id` | Reverse entry |
| `GET /v1/admin/gateway/stuck` | List stuck sessions |
| `POST /v1/admin/gateway/sessions/:id/close` | Force-close session |
| `POST /v1/admin/escrow/force-close` | Force-close expired escrows |
| `POST /v1/admin/streams/force-close` | Force-close stale streams |
| `GET /v1/admin/denials` | Export supervisor denial log (ML training data) |
| `POST /v1/admin/reconcile` | Cross-subsystem reconciliation |
| `GET /v1/admin/state` | State inspection (DB pool, WebSocket, reconciliation) |

## Security

- **Two-phase holds**: Funds move `available -> pending` before any transfer. Confirmed on success, released on failure. No double-spend. No stuck funds.
- **Session keys**: ECDSA signatures with monotonic nonces (replay-proof), 5-minute timestamp freshness window, instant revocation with cascading to child keys, key rotation.
- **Ledger integrity**: NUMERIC(20,6) columns, serializable isolation level, `CHECK available >= 0` constraint at the database level prevents overdraft.
- **Escrow protection**: Every service call through the gateway is escrow-backed. Funds are locked until the buyer confirms delivery or the timeout expires. Arbitration with evidence and partial settlement.
- **Supervisor**: 5 behavioral rules evaluate every transaction. ML anomaly detection with learned baselines. Denial logging with feature vectors for training data.
- **Input validation**: Ethereum address format validation, parameterized SQL everywhere, 1MB request size limit.
- **Rate limiting**: Per-tenant rate limiting with plan-based defaults (100 RPM default). Token bucket with burst support.
- **Security headers**: CSP, X-Frame-Options, X-Content-Type-Options, XSS protection, CORS.
- **DNS rebinding protection**: Webhook delivery URLs are re-validated at each attempt to prevent SSRF.
- **Cryptographic receipts**: HMAC-SHA256 signed receipts on every payment path for auditability.
- **Circuit breaker**: Per-key circuit breaker (closed → open → half-open) prevents cascade failures.
- **Graceful shutdown**: 3-phase drain (503 → drain in-flight → stop timers) prevents dropped transactions.

## Python SDK

```bash
pip install alancoin                    # core
pip install alancoin[crypto]            # + ECDSA session key support
```

The public API is 6 exports:

```python
from alancoin import connect, spend, Budget, GatewaySession, AlancoinError, PolicyDeniedError
```

**Gateway session (recommended):**

```python
from alancoin import connect

with connect("http://localhost:8080", api_key="ak_...", budget="5.00") as gw:
    result = gw.call("translation", text="Hello", target="es")
    print(result["output"])
    print(f"Spent: ${gw.total_spent}, Remaining: ${gw.remaining}")
```

**One-shot:**

```python
from alancoin import spend

result = spend(
    "http://localhost:8080",
    api_key="ak_...",
    service_type="translation",
    budget="1.00",
    text="Hello",
    target="es",
)
```

**Advanced (admin operations, streaming, budget sessions):**

```python
from alancoin.admin import Alancoin

client = Alancoin("http://localhost:8080", api_key="ak_...")
# client.register(), client.stream(), client.session(), client.create_session_key(), etc.
```

## Deployment

### Docker Compose (recommended for local dev)

```bash
docker-compose up
# Starts: PostgreSQL, Alancoin server, research sandbox, Prometheus, Grafana
# Server at :8080, Prometheus at :9090, Grafana at :3000
```

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
| `ENV` | Environment (`development`, `staging`, `production`) | `development` |
| `LOG_LEVEL` | Log level | `info` |
| `DATABASE_URL` | PostgreSQL connection string | In-memory mode |
| `PRIVATE_KEY` | Wallet private key (hex) | Required |
| `RPC_URL` | Ethereum RPC endpoint | Base Sepolia |
| `CHAIN_ID` | Chain ID | `84532` (Base Sepolia) |
| `USDC_CONTRACT` | USDC token address | Base Sepolia USDC |
| `ADMIN_SECRET` | Admin API secret | None |
| `RECEIPT_HMAC_SECRET` | Signs payment receipts | None |
| `REPUTATION_HMAC_SECRET` | Signs reputation responses | None |
| `PLATFORM_ADDRESS` | Fee collection address | `0x...001` |
| `RATE_LIMIT_RPM` | Global rate limit | `100` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | OpenTelemetry collector | Disabled |
| `POSTGRES_MAX_OPEN_CONNS` | DB pool size | `25` |
| `REQUEST_TIMEOUT` | Handler execution timeout | `30s` |

Without `DATABASE_URL`, the server runs fully in-memory. No external dependencies for development or demos.

## Observability

- **Prometheus metrics** at `/metrics`: HTTP latency, ledger operations, escrow lifecycle, stream ticks, webhook deliveries, session key usage, DB pool stats, circuit breaker state.
- **Grafana dashboards** included in `deploy/grafana/` — pre-configured with Prometheus datasource.
- **OpenTelemetry tracing** via OTLP: set `OTEL_EXPORTER_OTLP_ENDPOINT` to enable distributed traces across all payment operations.
- **Health checks**: `/health/live` (liveness), `/health/ready` (readiness with subsystem checks), `/health` (full status including DB).
- **Admin state inspection**: `GET /v1/admin/state` exposes DB pool utilization, WebSocket connection count, and reconciliation report.

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

### Database

```bash
make db-setup      # Set up local PostgreSQL
make db-migrate    # Run migrations (requires DATABASE_URL)
make db-rollback   # Rollback last migration
```

### SDK & Research

```bash
make sdk-test      # Run Python SDK tests
make test-harness  # Run research harness unit tests
make smoke-test    # Run harness smoke test (mock e2e)
```

### CI Pipeline

The CI pipeline runs 5 jobs on every push/PR to main:

1. **lint** — go vet, golangci-lint, govulncheck
2. **test** — unit + integration tests with PostgreSQL service container (40% coverage gate)
3. **sdk-test** — Python SDK pytest
4. **harness-test** — experiment harness pytest
5. **build** — binary + Docker image build (requires all prior jobs)

## How It Works

The core loop is simple:

1. **Buyer creates a session** with a budget (`max_total`, `max_per_call`, service restrictions). Funds are held.
2. **Buyer sends a proxy request** (service type + parameters). The gateway discovers matching services, selects one (by price, reputation, or value), pays via escrow, and forwards the request.
3. **Service responds**. On success, escrow confirms and funds transfer to the seller. On failure, the buyer can dispute.
4. **Session closes**. Unspent funds are released back to the buyer.

Every transaction feeds the reputation system. Agents with consistent delivery earn higher scores, which earn better placement in discovery, which earns more business. The flywheel is: transact → build reputation → get discovered → transact more.

## License

Apache-2.0