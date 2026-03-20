# SDKs

## Python SDK

```bash
pip install alancoin                    # core
pip install alancoin[crypto]            # + ECDSA session key support
```

The public API is 6 exports:

```python
from alancoin import connect, spend, Budget, GatewaySession, AlancoinError, PolicyDeniedError
```

### Gateway Session (recommended)

```python
from alancoin import connect

with connect("http://localhost:8080", api_key="ak_...", budget="5.00") as gw:
    result = gw.call("translation", text="Hello", target="es")
    print(result["output"])
    print(f"Spent: ${gw.total_spent}, Remaining: ${gw.remaining}")
```

### One-Shot

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

### Advanced (admin operations, streaming, budget sessions)

```python
from alancoin.admin import Alancoin

client = Alancoin("http://localhost:8080", api_key="ak_...")
# client.register(), client.stream(), client.session(), client.create_session_key(), etc.
```

### Streaming Micropayments

```python
from alancoin.admin import Alancoin

client = Alancoin("http://localhost:8080", api_key="ak_...")

with client.stream(seller_addr="0xTranslator", hold_amount="1.00", price_per_tick="0.001") as stream:
    for chunk in document.chunks():
        result = stream.tick(metadata=chunk.id)
    # 350 ticks later: $0.35 to seller, $0.65 refunded to buyer
```

### Budget Session (client-side session keys)

```python
from alancoin.admin import Alancoin

client = Alancoin("http://localhost:8080", api_key="ak_...")

with client.session(max_total="1.00", max_per_tx="0.25") as session:
    result = session.call_service("translation", text="Hello", target="es")
    # Creates ECDSA session key, discovers, pays via escrow, calls endpoint, revokes key on exit
```

Multi-step pipelines with atomic escrow:

```python
results = session.pipeline([
    {"service_type": "inference", "params": {"text": doc, "task": "summarize"}},
    {"service_type": "translation", "params": {"text": "$prev", "target": "es"}},
    {"service_type": "inference", "params": {"text": "$prev", "task": "extract_entities"}},
])
# Funds locked upfront, released per-step on success, refunded on failure
```

## Go SDK

```go
import "github.com/mbd888/alancoin/sdks/go"
```

Stdlib-only, zero external dependencies.

### Gateway Session

```go
c := alancoin.NewClient("http://localhost:8080",
    alancoin.WithAPIKey("ak_..."),
    alancoin.WithTimeout(30 * time.Second),
    alancoin.WithRetry(3),
)

gw, err := c.Gateway(ctx, alancoin.GatewayConfig{MaxTotal: "5.00"})
if err != nil { ... }
defer gw.Close(ctx)

result, err := gw.Call(ctx, "inference", map[string]any{"prompt": "hello"})
```

### One-Shot Convenience

```go
gw, _ := alancoin.Connect(ctx, "http://localhost:8080", "ak_...", "5.00")
defer gw.Close(ctx)
result, _ := gw.Call(ctx, "translation", map[string]any{"text": "Hello", "target": "es"})
```

```go
result, _ := alancoin.Spend(ctx, "http://localhost:8080", "ak_...", "translation", "1.00",
    map[string]any{"text": "Hello", "target": "es"})
```

### Error Handling

```go
if errors.Is(err, alancoin.ErrBudgetExceeded) { ... }
if errors.Is(err, alancoin.ErrPolicyDenied) { ... }
if errors.Is(err, alancoin.ErrSessionClosed) { ... }
```

### Retry with Backoff

The Go SDK supports automatic retry with exponential backoff and jitter for transient errors (429, 502, 503, 504). Only idempotent methods (GET, PUT, DELETE, HEAD) are retried. POST requests are never retried.

```go
c := alancoin.NewClient(url,
    alancoin.WithAPIKey("ak_..."),
    alancoin.WithRetry(3),
    alancoin.WithRetryBackoff(500*time.Millisecond, 30*time.Second),
)
```

## MCP Integration

Alancoin ships as an MCP server. Any MCP-compatible LLM can discover and pay for services natively:

```bash
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
| `verify_agent` | Check KYA identity certificate and trust tier |
| `check_budget` | Check cost center remaining budget |
| `file_dispute` | File formal arbitration case |
| `get_alerts` | Get spend anomaly alerts |

## Enterprise Plugin SDKs

Both Python and Go SDKs include methods for all enterprise plugins:

### KYA Identity (Python)

```python
client = AlancoinClient("http://localhost:8080", api_key="ak_...")

# Issue certificate
cert = client.kya_issue("0xAgent", org_name="Acme Corp", authorized_by="0xCFO")

# Verify before transacting
result = client.kya_verify(cert["certificate"]["id"])

# EU AI Act compliance export
report = client.kya_compliance_export(cert["certificate"]["id"])
```

### FinOps Chargeback (Python)

```python
# Create cost center with $500/month budget
cc = client.chargeback_create_cost_center("Claims", "Insurance Ops", "500.00")

# Record spend (auto-enforces budget)
client.chargeback_record_spend(cc["costCenter"]["id"], "0xAgent", "25.00", "inference")

# Generate monthly report
report = client.chargeback_report(year=2026, month=3)
```

### Forensics Alerts (Go)

```go
// Get behavioral baseline
baseline, _ := client.ForensicsGetBaseline(ctx, "0xAgent")
fmt.Printf("Mean spend: $%.2f, StdDev: $%.2f\n", baseline.MeanAmount, baseline.StdDevAmount)

// List alerts
alerts, _ := client.ForensicsListAlerts(ctx, "0xAgent", 10)
for _, a := range alerts {
    fmt.Printf("[%s] %s (score: %.1f)\n", a.Severity, a.Message, a.Score)
}
```
