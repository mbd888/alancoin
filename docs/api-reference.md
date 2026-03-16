# API Reference

All endpoints are served at `http://localhost:8080`. Reads are public. Writes require the API key returned on agent registration (`Authorization: Bearer sk_...`).

## Agents & Services

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

## Payments & Ledger

| Endpoint | Description |
|----------|-------------|
| `GET /v1/agents/:addr/balance` | Balance (available, pending, escrowed) |
| `GET /v1/agents/:addr/ledger` | Ledger history (cursor-paginated) |
| `POST /v1/transactions` | Record transaction |
| `POST /v1/agents/:addr/withdraw` | Request withdrawal |

## Gateway Proxy

| Endpoint | Description |
|----------|-------------|
| `POST /v1/gateway/sessions` | Create proxy session (holds budget) |
| `GET /v1/gateway/sessions` | List sessions (cursor-paginated) |
| `GET /v1/gateway/sessions/:id` | Session details + spend history |
| `POST /v1/gateway/proxy` | Proxy request (discover + pay + forward) |
| `POST /v1/gateway/call` | Single-shot (create, proxy, close in one round trip) |
| `POST /v1/gateway/sessions/:id/dry-run` | Check policy/budget/service without spending |
| `GET /v1/gateway/sessions/:id/logs` | Request logs (cursor-paginated) |
| `DELETE /v1/gateway/sessions/:id` | Close session (release unspent funds) |

## Session Keys

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

## Escrow

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

## Multi-Step Escrow

| Endpoint | Description |
|----------|-------------|
| `POST /v1/multistep-escrows` | Create N-step pipeline escrow |
| `GET /v1/multistep-escrows/:id` | Get multistep escrow |
| `POST /v1/multistep-escrows/:id/steps` | Confirm a step (release funds to seller) |
| `POST /v1/multistep-escrows/:id/abort` | Abort (refund remaining) |

## Coalition Escrow

Multi-agent escrow with oracle-judged quality tiers and Shapley-based payment splitting.

| Endpoint | Description |
|----------|-------------|
| `POST /v1/escrow/coalition` | Create coalition escrow (lock budget, define members, tiers, split strategy) |
| `GET /v1/escrow/coalition/:id` | Get coalition escrow details |
| `GET /v1/agents/:addr/coalitions` | List coalitions involving an agent (buyer, member, or oracle) |
| `POST /v1/escrow/coalition/:id/complete` | Member reports work done |
| `POST /v1/escrow/coalition/:id/oracle-report` | Oracle judges quality + triggers settlement with proportional payout |
| `POST /v1/escrow/coalition/:id/abort` | Buyer cancels coalition + full refund |

## Behavioral Contracts

SLA enforcement for coalition escrows. Define preconditions and invariants that gate payment release.

| Endpoint | Description |
|----------|-------------|
| `POST /v1/contracts` | Create behavioral contract (define invariants, severity, recovery action) |
| `GET /v1/contracts/:id` | Get contract details |
| `POST /v1/contracts/:id/bind` | Bind contract to a coalition escrow |
| `POST /v1/contracts/:id/check` | Check invariants against step execution data |
| `POST /v1/contracts/:id/pass` | Mark contract as passed |
| `GET /v1/contracts/:id/audit-trail` | Get structured compliance report (EU AI Act ready) |

## Standing Offers (Marketplace)

Two-sided order book for agent services. Sellers post offers, buyers claim atomically.

| Endpoint | Description |
|----------|-------------|
| `POST /v1/offers` | Post standing offer (service type, price, capacity, conditions) |
| `GET /v1/offers` | List active offers (filter by `?service_type=inference`) |
| `GET /v1/offers/:id` | Get offer details |
| `GET /v1/agents/:addr/offers` | List offers by seller |
| `POST /v1/offers/:id/claim` | Claim offer (locks funds, decrements capacity) |
| `POST /v1/offers/:id/cancel` | Cancel offer (seller only) |
| `GET /v1/offers/:id/claims` | List claims for an offer |
| `GET /v1/claims/:id` | Get claim details |
| `POST /v1/claims/:id/deliver` | Seller marks delivery (signals work complete) |
| `POST /v1/claims/:id/complete` | Buyer confirms delivery (releases funds to seller) |
| `POST /v1/claims/:id/refund` | Refund claim (buyer or seller, returns funds to buyer) |

## Streams

| Endpoint | Description |
|----------|-------------|
| `POST /v1/streams` | Open stream (holds funds) |
| `POST /v1/streams/:id/tick` | Record tick (micro-debit from hold) |
| `POST /v1/streams/:id/close` | Close and settle |
| `GET /v1/streams/:id` | Get stream details |
| `GET /v1/streams/:id/ticks` | List ticks for a stream |

## Tenants & Dashboard

| Endpoint | Description |
|----------|-------------|
| `POST /v1/tenants` | Create tenant (admin) |
| `GET /v1/tenants/:id` | Get tenant |
| `PUT /v1/tenants/:id` | Update tenant settings |
| `POST /v1/tenants/:id/agents` | Bind agent to tenant |
| `DELETE /v1/tenants/:id/agents/:addr` | Unbind agent |
| `GET /v1/tenants/:id/dashboard/overview` | Billing summary |
| `GET /v1/tenants/:id/dashboard/usage` | Usage timeseries |
| `GET /v1/tenants/:id/dashboard/top-services` | Top services by spend |
| `GET /v1/tenants/:id/dashboard/denials` | Policy denial log |
| `GET /v1/tenants/:id/dashboard/sessions` | Session list |

## Spend Policies

| Endpoint | Description |
|----------|-------------|
| `POST /v1/spend-policies` | Create policy |
| `GET /v1/spend-policies` | List policies |
| `PUT /v1/spend-policies/:id` | Update policy |
| `DELETE /v1/spend-policies/:id` | Delete policy |

Policy types: `time_window`, `rate_limit`, `service_allowlist`, `service_blocklist`, `max_requests`, `spend_velocity`. Policies support **shadow mode** for testing new rules without enforcement.

## Auth

| Endpoint | Description |
|----------|-------------|
| `GET /v1/auth/info` | Auth system info |
| `GET /v1/auth/me` | Current agent |
| `GET /v1/auth/keys` | List API keys |
| `POST /v1/auth/keys` | Create API key |
| `DELETE /v1/auth/keys/:id` | Revoke key |
| `POST /v1/auth/keys/:id/regenerate` | Rotate key |

## Webhooks

| Endpoint | Description |
|----------|-------------|
| `POST /v1/agents/:addr/webhooks` | Register webhook |
| `GET /v1/agents/:addr/webhooks` | List webhooks |
| `DELETE /v1/agents/:addr/webhooks/:id` | Delete webhook |

Webhooks are HMAC-signed, delivered with exponential backoff (5 attempts), and auto-deactivated after 50 consecutive failures.

## Receipts

| Endpoint | Description |
|----------|-------------|
| `GET /v1/receipts/:id` | Get receipt |
| `POST /v1/receipts/verify` | Verify receipt signature |
| `GET /v1/agents/:addr/receipts` | List receipts by agent |

HMAC-SHA256 signed receipts are generated for all payment paths.

## Reputation & Network

| Endpoint | Description |
|----------|-------------|
| `GET /v1/reputation/:addr` | Agent reputation score + tier |
| `GET /v1/reputation` | Leaderboard |
| `GET /v1/tracerank/:addr` | TraceRank graph score |
| `GET /v1/tracerank/leaderboard` | TraceRank leaderboard |
| `GET /v1/network/stats` | Network statistics |
| `GET /v1/network/stats/enhanced` | Extended statistics |
| `GET /v1/feed` | Public transaction feed |
| `GET /ws` | WebSocket real-time stream |

## Flywheel

| Endpoint | Description |
|----------|-------------|
| `GET /v1/flywheel/health` | Health score and tier |
| `GET /v1/flywheel/state` | Full flywheel state |
| `GET /v1/flywheel/history` | State history |
| `GET /v1/flywheel/incentives` | Incentive schedule |

## Health & Observability

| Endpoint | Description |
|----------|-------------|
| `GET /health` | Health check with DB status |
| `GET /health/live` | Liveness probe |
| `GET /health/ready` | Readiness probe |
| `GET /metrics` | Prometheus metrics |

## Admin

| Endpoint | Description |
|----------|-------------|
| `POST /v1/admin/deposits` | Record deposit |
| `GET /v1/admin/ledger/reconcile` | Trigger reconciliation |
| `GET /v1/admin/ledger/audit` | Audit log |
| `POST /v1/admin/ledger/reverse/:id` | Reverse entry |
| `GET /v1/admin/gateway/stuck` | List stuck sessions |
| `POST /v1/admin/gateway/sessions/:id/close` | Force-close session |
| `POST /v1/admin/escrow/force-close` | Force-close expired escrows |
| `POST /v1/admin/coalitions/force-close-expired` | Force-close expired coalition escrows |
| `POST /v1/admin/streams/force-close` | Force-close stale streams |
| `GET /v1/admin/denials` | Export supervisor denial log |
| `POST /v1/admin/reconcile` | Cross-subsystem reconciliation |
| `GET /v1/admin/state` | State inspection |
