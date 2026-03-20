<p align="center">
  <img src="assets/alancoin.png" alt="Alancoin" width="400">
</p>

<p align="center">
  <a href="https://github.com/mbd888/alancoin/actions/workflows/ci.yml"><img src="https://github.com/mbd888/alancoin/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://goreportcard.com/report/github.com/mbd888/alancoin"><img src="https://goreportcard.com/badge/github.com/mbd888/alancoin?v=2" alt="Go Report Card"></a>
  <a href="https://pkg.go.dev/github.com/mbd888/alancoin"><img src="https://pkg.go.dev/badge/github.com/mbd888/alancoin.svg" alt="Go Reference"></a>
  <a href="https://opensource.org/licenses/Apache-2.0"><img src="https://img.shields.io/badge/License-Apache_2.0-blue.svg" alt="License"></a>
</p>

**The payment layer for AI agents.** Budget-controlled, escrow-backed, compliance-ready settlement for autonomous agent economies.

Alancoin is a transparent payment proxy for autonomous AI agents. An agent sends a request. Alancoin finds the right service, handles payment in USDC, forwards the request, and returns the result. The agent never touches a wallet, signs a transaction, or thinks about money. It just works.

```python
from alancoin import connect

with connect("http://localhost:8080", api_key="ak_...", budget="5.00") as gw:
    result = gw.call("translation", text="Hello world", target="es")
    # -> {"output": "Hola mundo", "_gateway": {"amountPaid": "0.005", ...}}
```

## Why This Exists

Enterprise AI agent deployments are losing money. Gartner documented $400M in unbudgeted agent spend across the Fortune 500 in 2026. 73% of enterprise agent projects exceed their budget. The root cause: agents have compute capabilities but no financial controls.

Alancoin removes the human from the payment loop. An agent gets a budget with hard limits, behavioral constraints, and compliance audit trails. Within those limits, the agent operates without human approval. The platform handles discovery, settlement, escrow, and dispute resolution, with every action recorded for regulatory compliance.

## Financial Safety Guarantees

| Guarantee | How It's Enforced |
|-----------|-------------------|
| **No agent can overspend** | Funds move `available → pending` via two-phase holds before any transfer. `CHECK available >= 0` at the database level prevents overdraft regardless of application logic. |
| **No payment without escrow** | Every service call locks funds in escrow first. Released to seller only on confirmed delivery. Refunded to buyer on dispute. |
| **No unauthorized access** | Session keys use ECDSA signatures with monotonic nonces. Hierarchical delegation with monotonic attenuation. |
| **No silent failures** | Circuit breakers halt payment flow on anomaly detection. Forensics engine scores every transaction against behavioral baselines. Critical alerts auto-pause the agent. |
| **No unaudited transactions** | HMAC-SHA256 signed receipts on every payment path. Hash-chained audit trails for EU AI Act compliance. |
| **No budget surprises** | Per-department cost attribution with monthly budget envelopes enforced in real time. CFO-ready chargeback reports. |

## Quick Start

```bash
git clone https://github.com/mbd888/alancoin.git && cd alancoin
make deps && make run
# Server at http://localhost:8080 -- no database, no config, in-memory mode
```

Run the demo:

```bash
pip install requests
python3 scripts/demo.py --speed fast
```

> For Docker, Fly.io, and production setup, see [Deployment](docs/deployment.md).

## How It Works

```
Agent Request → Budget Check → Service Discovery → Escrow Lock → Forward → Settle → Audit
```

1. **Budget check**: verify the agent's session budget, cost center budget, and spend policies allow this call.
2. **Service discovery**: find matching services ranked by price, reputation, or value.
3. **Escrow lock**: hold the payment amount before any external call is made.
4. **Forward + settle**: proxy the request, settle to seller on success, refund on failure.
5. **Audit**: emit signed receipt, update reputation, publish settlement event to forensics and chargeback engines.

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
