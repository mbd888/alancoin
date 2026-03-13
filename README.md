# Alancoin

[![CI](https://github.com/mbd888/alancoin/actions/workflows/ci.yml/badge.svg)](https://github.com/mbd888/alancoin/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/mbd888/alancoin?v=2)](https://goreportcard.com/report/github.com/mbd888/alancoin)
[![Go Reference](https://pkg.go.dev/badge/github.com/mbd888/alancoin.svg)](https://pkg.go.dev/github.com/mbd888/alancoin)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)

**The payment layer for AI agents.** Discover a service, pay for it, get the result -- in one call.

Alancoin is a transparent payment proxy for autonomous AI agents. An agent sends a request. Alancoin finds the right service, handles payment in USDC, forwards the request, and returns the result. The agent never touches a wallet, signs a transaction, or thinks about money. It just works.

```python
from alancoin import connect

with connect("http://localhost:8080", api_key="ak_...", budget="5.00") as gw:
    result = gw.call("translation", text="Hello world", target="es")
    # -> {"output": "Hola mundo", "_gateway": {"amountPaid": "0.005", ...}}
```

## Why This Exists

There are millions of AI agents that can reason, plan, and use tools. They can't pay each other. Every integration is a custom API key, a manual billing agreement, a human in the loop.

Alancoin removes the human from the payment loop. An agent gets a budget with hard limits. Within those limits, the agent operates autonomously. The platform handles discovery, payment, escrow, and settlement.

**The result**: AI agents can hire other AI agents, in real-time, for micropayments, without human approval per transaction.

## Quick Start

```bash
git clone https://github.com/mbd888/alancoin.git && cd alancoin
make deps && make run
# Server at http://localhost:8080 -- no database, no config, in-memory mode
```

Run the demo in a second terminal:

```bash
pip install requests
python3 scripts/demo.py --speed fast
```

> For Docker, Fly.io, and production setup, see [Deployment](docs/deployment.md).

## How It Works

1. **Buyer creates a session** with a budget and constraints. Funds are held.
2. **Buyer sends a proxy request**. The gateway discovers matching services, selects one (by price, reputation, or value), pays via escrow, and forwards the request.
3. **Service responds**. On success, escrow confirms and funds transfer. On failure, the buyer can dispute.
4. **Session closes**. Unspent funds are released back to the buyer.

Every transaction feeds the reputation system. Agents with consistent delivery earn higher scores, better placement in discovery, and more business. The flywheel: transact -> build reputation -> get discovered -> transact more.

> See [Architecture](docs/architecture.md) for the full system diagram and subsystem details.

## Three Ways to Pay

**Gateway session** (recommended) -- server handles everything:

```python
with connect("http://localhost:8080", api_key="ak_...", budget="5.00") as gw:
    summary = gw.call("inference", text=document, task="summarize")
    translated = gw.call("translation", text=summary["output"], target="es")
    # $0.015 across 2 agents, all within the $5.00 budget
```

**Streaming micropayments** -- pay per tick as value is delivered:

```python
with client.stream(seller_addr="0xTranslator", hold_amount="1.00", price_per_tick="0.001") as stream:
    for chunk in document.chunks():
        stream.tick(metadata=chunk.id)
    # 350 ticks: $0.35 to seller, $0.65 refunded
```

**Budget sessions** -- client-side ECDSA session keys with escrow protection. See [SDKs](docs/sdks.md).

> Full code examples for Python, Go, and MCP in the [SDK docs](docs/sdks.md). All endpoints in the [API Reference](docs/api-reference.md).

## Key Concepts

| Concept | What it does |
|---------|-------------|
| **Gateway Proxy** | Discover + pay + forward in one call. Supports cheapest, reputation, tracerank, and best_value strategies |
| **Session Keys** | ECDSA-signed bounded autonomy with [hierarchical delegation](docs/session-keys.md) up to 5 levels deep |
| **Supervisor** | 5-rule behavioral safety net: velocity, new-agent caps, ML anomaly detection, circular flow, counterparty concentration. [Details](docs/security.md#supervisor) |
| **Escrow** | Lock/deliver/confirm/dispute lifecycle with auto-release timeouts and arbitration |
| **Reputation** | Weighted composite score (volume, activity, success rate, age, diversity) across 5 tiers |
| **TraceRank** | PageRank on the payment graph -- uses payment flows as implicit trust endorsements |
| **Flywheel** | Measures network health (velocity, growth, density, effectiveness, retention) and amplifies it with incentives |
| **Multi-Tenancy** | Isolated namespaces, per-tenant rate limits, configurable take rates (free/starter/growth/enterprise) |
| **MCP Server** | Any MCP-compatible LLM can discover and pay for services natively. [Setup](docs/sdks.md#mcp-integration) |

## SDKs

| SDK | Install | Docs |
|-----|---------|------|
| Python | `pip install alancoin` | [SDK docs](docs/sdks.md#python-sdk) |
| Go | `go get github.com/mbd888/alancoin/sdks/go` | [SDK docs](docs/sdks.md#go-sdk) |
| MCP | `make build-mcp` | [MCP docs](docs/sdks.md#mcp-integration) |

## Development

```bash
make deps          # Download dependencies
make test          # Run unit tests (with race detector)
make lint          # Run golangci-lint
make check         # fmt + vet + lint + test
make dev           # Hot reload with air
make build         # Build to bin/alancoin
make demo          # One-command demo (builds, starts, runs simulation)
```

> Database setup, CI pipeline, and production config in [Deployment](docs/deployment.md). Metrics and tracing in [Observability](docs/observability.md).

## Documentation

| Document | Contents |
|----------|----------|
| [Architecture](docs/architecture.md) | System diagram, core loop, subsystem descriptions |
| [API Reference](docs/api-reference.md) | All 100+ REST endpoints |
| [SDKs](docs/sdks.md) | Python SDK, Go SDK, and MCP integration |
| [Session Keys](docs/session-keys.md) | Delegation, HMAC-chain proofs, spend policies |
| [Security](docs/security.md) | Two-phase holds, escrow, supervisor, rate limiting |
| [Deployment](docs/deployment.md) | Docker, Fly.io, environment variables, CI pipeline |
| [Observability](docs/observability.md) | Prometheus, Grafana, OpenTelemetry, health checks |

## License

Apache-2.0
