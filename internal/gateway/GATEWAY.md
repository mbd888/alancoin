# Gateway: Transparent Payment Proxy

The gateway transforms Alancoin into transparent payment middleware. Agents send
`POST /v1/gateway/proxy` with a service type and params, and the gateway handles
discovery, payment, and forwarding automatically.

## Quick Start

```bash
# 1. Create a gateway session (holds budget from your balance)
curl -X POST http://localhost:8080/v1/gateway/sessions \
  -H "Authorization: Bearer sk_your_api_key" \
  -H "Content-Type: application/json" \
  -d '{
    "maxTotal": "5.00",
    "maxPerRequest": "1.00",
    "strategy": "cheapest"
  }'
# Returns: { "session": {...}, "token": "gw_abc123..." }

# 2. Proxy requests using the gateway token
curl -X POST http://localhost:8080/v1/gateway/proxy \
  -H "X-Gateway-Token: gw_abc123..." \
  -H "Content-Type: application/json" \
  -d '{
    "serviceType": "translation",
    "params": {"text": "Hello world", "targetLang": "es"}
  }'
# Returns: { "result": { "response": {...}, "amountPaid": "0.005", ... } }

# 3. Close session (releases unspent funds)
curl -X DELETE http://localhost:8080/v1/gateway/sessions/gw_abc123... \
  -H "Authorization: Bearer sk_your_api_key"
```

## Payment Flow

```
Create Session           Proxy Request              Close Session
     |                        |                          |
     v                        v                          v
Hold(buyer, $5)    ConfirmHold(buyer, $0.50)    ReleaseHold(buyer, $4.00)
  available->pending    pending->spent             pending->available
                   Deposit(seller, $0.50)
                     -> seller.available
                   Forward HTTP to service
```

## API Reference

### Session Management (requires API key)

| Method | Path | Description |
|--------|------|-------------|
| POST | `/v1/gateway/sessions` | Create session (holds budget) |
| GET | `/v1/gateway/sessions` | List your sessions |
| GET | `/v1/gateway/sessions/:id` | Get session details |
| DELETE | `/v1/gateway/sessions/:id` | Close session (release funds) |
| GET | `/v1/gateway/sessions/:id/logs` | View request logs |

### Proxy (requires gateway token)

| Method | Path | Description |
|--------|------|-------------|
| POST | `/v1/gateway/proxy` | Proxy a service request |

### Create Session Request

```json
{
  "maxTotal": "5.00",         // Total budget to hold
  "maxPerRequest": "1.00",    // Max per single proxy call
  "strategy": "cheapest",     // "cheapest", "reputation", or "best_value"
  "allowedTypes": ["translation", "inference"],  // Optional type whitelist
  "expiresInSecs": 3600       // Optional, default 1 hour
}
```

### Proxy Request

```json
{
  "serviceType": "translation",
  "params": {"text": "Hello", "targetLang": "es"},
  "maxPrice": "0.50",            // Optional per-request price override
  "preferAgent": "0xabc..."      // Optional preferred service agent
}
```

### Proxy Response

```json
{
  "result": {
    "response": {"translated": "Hola"},
    "serviceUsed": "0xseller...",
    "serviceName": "TranslatorBot",
    "amountPaid": "0.005",
    "latencyMs": 142,
    "retries": 0
  }
}
```

## Selection Strategies

| Strategy | Behavior |
|----------|----------|
| `cheapest` | Lowest price first (default) |
| `reputation` | Highest reputation score first |
| `best_value` | Best reputation-to-price ratio |

## Retry Behavior

The gateway tries up to 3 service candidates per proxy request. If the HTTP
forward to a service fails (network error), the payment still goes through
(the service was paid but didn't deliver). This is tracked for reputation.
The gateway moves on to the next candidate.

## Authentication

- **Session management** uses standard API key auth (`Authorization: Bearer sk_...`)
- **Proxy requests** use the gateway token (`X-Gateway-Token: gw_...`)
  returned from session creation. This allows agents to proxy without
  needing the full API key.

---

## Not Yet Built

What exists today is the MVP skeleton: hold budget, proxy requests with
discovery+payment+forwarding, release on close. The core payment loop works
and is tested. Everything below is what's missing before this is real.

### 1. Session Expiry Timer (Critical)

**Problem:** Sessions have an `expiresAt` field and the proxy endpoint checks
it, but nothing _auto-closes_ expired sessions. If a buyer creates a session,
walks away, and never calls DELETE, their funds stay held in pending forever.

**What to build:** A `Timer` struct (like `streams.Timer` and `escrow.Timer`)
that runs a goroutine on an interval (e.g. every 30s), queries for active
sessions past their `expiresAt`, and calls `CloseSession` on each one to
release the held funds back to available.

**Files:**
- `internal/gateway/timer.go` — Timer struct, `Start(ctx)`, `sweep()` loop
- `memory_store.go` — Add `ListExpired(ctx, before time.Time, limit int)` to Store interface
- `server.go` — Wire `s.gatewayTimer` and start it in `Run()`

**Pattern to follow:** `internal/streams/timer.go` is nearly identical. The
stream timer calls `ListStale` + `AutoClose`; the gateway timer would call
`ListExpired` + `CloseSession`.

**Risk if skipped:** Fund leak. Buyers lose access to held funds until manual
DB intervention.

### 2. PostgreSQL Store (Critical for Production)

**Problem:** Both the postgres and in-memory server branches use
`gateway.NewMemoryStore()`. Gateway sessions, request logs, and all state
vanish on server restart.

**What to build:** A `PostgresStore` implementing the `Store` interface, plus
a migration creating the tables.

**Files:**
- `internal/gateway/postgres_store.go` — SQL implementations of all Store methods
- `migrations/021_gateway.sql` — Table definitions

**Schema:**
```sql
CREATE TABLE gateway_sessions (
    id              TEXT PRIMARY KEY,
    agent_addr      TEXT NOT NULL,
    max_total       NUMERIC(20,6) NOT NULL,
    max_per_request NUMERIC(20,6) NOT NULL,
    total_spent     NUMERIC(20,6) NOT NULL DEFAULT 0,
    request_count   INTEGER NOT NULL DEFAULT 0,
    strategy        TEXT NOT NULL DEFAULT 'cheapest',
    allowed_types   TEXT[],           -- NULL = all types
    status          TEXT NOT NULL DEFAULT 'active',
    expires_at      TIMESTAMPTZ NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_gateway_sessions_agent ON gateway_sessions(agent_addr);
CREATE INDEX idx_gateway_sessions_status ON gateway_sessions(status) WHERE status = 'active';

CREATE TABLE gateway_request_logs (
    id           TEXT PRIMARY KEY,
    session_id   TEXT NOT NULL REFERENCES gateway_sessions(id),
    service_type TEXT NOT NULL,
    agent_called TEXT,
    amount       NUMERIC(20,6),
    status       TEXT NOT NULL,          -- 'success', 'forward_failed', 'no_service'
    latency_ms   BIGINT,
    error        TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_gateway_logs_session ON gateway_request_logs(session_id);
```

**Pattern to follow:** `internal/streams/postgres_store.go`. Same NUMERIC(20,6)
convention, same `sql.LevelSerializable` for balance-affecting operations.

### 3. Transaction Recording for Reputation (Important)

**Problem:** When the gateway pays a seller on behalf of a buyer, no
transaction is recorded in the registry. These payments are invisible to the
reputation system. A seller could serve 10,000 requests through the gateway
and their reputation score wouldn't change.

**What to build:** A `TransactionRecorder` interface (same as
`streams.TransactionRecorder`) and a `WithRecorder()` method on the gateway
Service. After each successful proxy, record the transaction.

**Where to record:** In `service.go` `Proxy()`, after the successful forward
and session update, call:
```go
if s.recorder != nil {
    _ = s.recorder.RecordTransaction(ctx, ref, session.AgentAddr,
        candidate.AgentAddress, candidate.Price, candidate.ServiceID, "confirmed")
}
```

For failed forwards where payment happened, record with status `"failed"` so
the seller's success rate drops.

**Pattern to follow:** `internal/streams/service.go:settle()` lines that call
`s.recorder.RecordTransaction`.

### 4. Revenue Accumulation for Stakes (Important)

**Problem:** The staking system intercepts revenue from escrow and stream
payments to build agent stakes. Gateway payments bypass this entirely. Sellers
earning through the gateway don't accumulate stake-able revenue.

**What to build:** A `RevenueAccumulator` interface and `WithRevenueAccumulator()`
on the gateway Service (same pattern as streams). Call it after successful
proxy payments.

**Pattern to follow:** `internal/streams/service.go` — `s.revenue.AccumulateRevenue()`.
Wire it in `server.go` the same way streams does:
```go
s.gatewayService.WithRevenueAccumulator(revenueAdapter)
```

### 5. Failed Forward Refund Mechanism (Important)

**Problem:** If the HTTP forward fails after payment (ConfirmHold + Deposit
already executed), the buyer loses that money. The gateway moves on to the
next candidate and may pay again. A single proxy request could cost the buyer
2-3x the service price if multiple candidates fail.

**Current behavior is intentional for MVP:** Payment-then-forward means
sellers can't stiff buyers after seeing the payment fail. But it creates a
real cost for buyers when services are unreliable.

**Options:**

A. **Escrow-style hold per request** — Instead of ConfirmHold+Deposit
   immediately, use the escrow pattern: lock funds, forward, then release to
   seller on success or refund to buyer on failure. This is safer but adds
   latency (two more ledger ops per request).

B. **Post-hoc refund via dispute** — Record failed forwards as disputed
   transactions. A separate process (or the buyer via API) can claim refunds.
   Simpler to implement but requires a new dispute resolution flow.

C. **Pre-forward health check** — Before paying, send a lightweight HEAD or
   OPTIONS request to the service endpoint. Only pay if the service is
   reachable. Doesn't prevent application-level failures but catches network
   issues.

**Recommendation:** Option A for high-value requests (price > threshold),
option C as a cheap default. But this is a design decision that needs thought.

### 6. Python SDK Integration (Important for Adoption)

**Problem:** The gateway is usable via raw HTTP but the whole point is "zero
integration." The Python SDK should wrap this in a context manager.

**What to build:**
```python
# Target API:
async with client.gateway(max_total="5.00", max_per_request="1.00") as gw:
    result = gw.proxy("translation", text="Hello", targetLang="es")
    result2 = gw.proxy("inference", prompt="Summarize this")
    print(f"Total spent: {gw.total_spent}")
# Session auto-closes on exit, releasing unspent funds
```

**Files:**
- `sdks/python/alancoin/gateway.py` — `GatewaySession` class
- `sdks/python/alancoin/client.py` — Add `gateway()` method
- `sdks/python/alancoin/__init__.py` — Export `GatewaySession`

**Implementation:** `GatewaySession.__aenter__` calls POST /sessions,
stores the token. `proxy()` calls POST /proxy with X-Gateway-Token.
`__aexit__` calls DELETE /sessions/:id. Follow the pattern of
`BudgetSession` in `session.py`.

### 7. Rate Limiting per Session (Nice to Have)

**Problem:** Nothing prevents an agent from firing thousands of proxy
requests per second within a session. The per-session mutex serializes
them but doesn't throttle. This could cause:
- Budget drain from rapid-fire requests before the buyer can react
- Downstream service abuse (the gateway becomes a DDoS amplifier)

**What to build:** Add optional `maxRequestsPerMinute` to
`CreateSessionRequest`. Enforce in `Proxy()` with a simple sliding
window counter. Reject with 429 if exceeded.

**Alternative:** Rely on the server-level `rateLimiter` already wired in
`server.go`. But that limits by IP/API key, not per gateway session. A
compromised gateway token could still burn through a session budget.

### 8. Observability (Nice to Have)

**Problem:** No Prometheus metrics for gateway operations. Can't monitor
proxy latency distributions, success rates, spend velocity, or which
services are being selected.

**What to build:** In `service.go`, add counters/histograms:
- `gateway_proxy_total` (labels: status, strategy, service_type)
- `gateway_proxy_duration_seconds` (histogram)
- `gateway_session_total` (labels: status)
- `gateway_spend_total` (counter, USDC amount)

**Pattern to follow:** `internal/metrics/metrics.go` for the existing
Prometheus setup.

### 9. Webhook Events (Nice to Have)

**Problem:** No webhook notifications for gateway events. Buyers can't
get notified when their session is running low on budget, when a proxy
request fails, or when a session expires.

**Events to emit:**
- `gateway.session.created`
- `gateway.session.closed`
- `gateway.session.expired`
- `gateway.proxy.success`
- `gateway.proxy.failed`
- `gateway.budget.low` (e.g. <20% remaining)

**Pattern to follow:** Streams doesn't do this either, but the webhook
dispatcher (`internal/webhooks/`) is already wired. Add calls to
`s.webhooks.Dispatch()` in the service layer.

### 10. Multi-Step Proxy (Pipeline) (Future)

**Problem:** The gateway handles one service call at a time. For workflows
like "summarize then translate then extract entities," the buyer must make
3 separate proxy calls and pass results manually.

**What to build:** A `POST /v1/gateway/pipeline` endpoint that accepts an
array of steps:
```json
{
  "steps": [
    {"serviceType": "inference", "params": {"prompt": "Summarize: ..."}},
    {"serviceType": "translation", "params": {"text": "$prev.summary"}},
    {"serviceType": "inference", "params": {"prompt": "Extract entities: $prev.translated"}}
  ]
}
```

The gateway executes sequentially, substituting `$prev` references (same
pattern as `session.py:pipeline()`), and returns all results.

This is the "one API call to orchestrate an entire agent workflow" story.

### Priority Order

| # | Item | Blocks | Effort |
|---|------|--------|--------|
| 1 | Session expiry timer | Fund safety | Small — copy streams timer |
| 2 | PostgreSQL store | Production deploy | Medium — SQL + store impl |
| 3 | Transaction recording | Reputation accuracy | Small — add interface + 5 lines |
| 4 | Revenue accumulation | Staking accuracy | Small — add interface + 5 lines |
| 5 | Failed forward refund | Buyer trust | Medium — design decision needed |
| 6 | Python SDK | Developer adoption | Medium — context manager + methods |
| 7 | Rate limiting | Abuse prevention | Small |
| 8 | Observability | Operations | Small |
| 9 | Webhook events | Integrations | Small |
| 10 | Pipeline proxy | Competitive edge | Large |
