# Observability

## Prometheus Metrics

Available at `/metrics`. Covers:

- HTTP request latency and status codes
- Ledger operations (credits, debits, holds)
- Escrow lifecycle (created, delivered, confirmed, disputed)
- Stream ticks and settlements
- Webhook delivery attempts and failures
- Session key usage and revocations
- Database connection pool stats
- Circuit breaker state transitions
- Flywheel health scores

## Grafana Dashboards

Pre-configured dashboards are included in `deploy/grafana/` with Prometheus datasource. Available at `:3000` when running with Docker Compose.

## OpenTelemetry Tracing

Set `OTEL_EXPORTER_OTLP_ENDPOINT` to enable distributed traces across all payment operations via OTLP.

## Health Checks

| Endpoint | Purpose |
|----------|---------|
| `GET /health` | Full status including database connectivity |
| `GET /health/live` | Liveness probe for orchestrators |
| `GET /health/ready` | Readiness probe with subsystem checks |

## Admin State Inspection

`GET /v1/admin/state` exposes runtime state:

- Database connection pool utilization
- WebSocket connection count
- Reconciliation report (ledger vs escrow vs stream consistency)

## WebSocket Feed

`GET /ws` provides a real-time stream of network events (transactions, registrations, reputation changes). 10K connection cap.

## Reconciliation

- `GET /v1/admin/ledger/reconcile` -- trigger ledger reconciliation
- `GET /v1/admin/ledger/audit` -- full audit log
- `POST /v1/admin/reconcile` -- cross-subsystem reconciliation (ledger vs escrow vs stream)

## Denial Logging

The supervisor logs every denial with feature vectors, exportable via `GET /v1/admin/denials` for ML training data.

## Event Bus Metrics

The settlement event bus exposes operational metrics at `/metrics`:

| Metric | Type | Description |
|--------|------|-------------|
| `alancoin_eventbus_published_total` | Counter | Events published to bus |
| `alancoin_eventbus_consumed_total` | Counter | Events consumed by handlers |
| `alancoin_eventbus_dropped_total` | Counter | Events dropped (buffer full) |
| `alancoin_eventbus_consumer_errors_total` | Counter | Consumer handler errors |
| `alancoin_eventbus_batches_processed_total` | Counter | Batches processed |
| `alancoin_eventbus_pending` | Gauge | Events pending in buffer |

Admin endpoint: `GET /v1/admin/eventbus/stats` returns published/consumed/pending counts and per-consumer lag.

## Enterprise Plugin Metrics

| Metric | Description |
|--------|-------------|
| `alancoin_kya_certificates_issued_total` | KYA certificates issued |
| `alancoin_kya_certificates_revoked_total` | KYA certificates revoked |
| `alancoin_chargeback_spend_total` | Spend recorded by service type |
| `alancoin_chargeback_budget_exceeded_total` | Budget exceeded events |
| `alancoin_arbitration_cases_filed_total` | Arbitration cases filed |
| `alancoin_arbitration_cases_resolved_total` | Cases resolved by outcome |
| `alancoin_forensics_alerts_total` | Anomaly alerts by severity |
| `alancoin_forensics_events_ingested_total` | Spend events analyzed |
| `alancoin_matview_refresh_duration_seconds` | Materialized view refresh time |

## Materialized View Refresh

Pre-aggregated views (`billing_timeseries_hourly`, `chargeback_summary_monthly`) are refreshed every 5 minutes when PostgreSQL is configured. Refresh duration is tracked via `matview_refresh_duration_seconds` histogram.

## Alerting

Rules are declared in `deploy/prometheus/rules.yml` and loaded via `rule_files:` in `prometheus.yml`. Each alert sets `runbook_url` to a heading below.

### LedgerInvariantViolated

Conservation law `Available + Pending + Escrow = TotalIn - TotalOut` failed for at least one agent in the last reconciliation run. Treat as a financial-correctness incident.

1. Pull the latest reconciliation report: `curl /v1/admin/state` (admin auth) and inspect `reconciliation`.
2. Identify the offending agents from logs (`reconciliation: invariant check`).
3. Pause payouts (`PAYOUTS_ENABLED=false`) until ledger consistency is restored.
4. Re-run the invariant check after fixes; the gauge clears on the next successful run.

### LedgerMismatch

Replaying ledger events does not reproduce stored balances for one or more agents. Could indicate a missed event, a partial transaction, or out-of-band balance writes.

1. Identify the affected agents from `reconciliation: ledger check failed` logs.
2. Compare event-stream replay vs `agent_balances` rows for each agent.
3. Decide whether to trust replay or stored balance and reconcile manually.

### StuckEscrowsBacklog

More than 50 escrows are past their auto-release deadline. The escrow timer may be stalled or unable to commit.

1. Check escrow timer logs and `alancoin_escrow_auto_released_total` rate.
2. Verify DB writes are succeeding (`alancoin_db_in_use_connections`).
3. If the timer is dead, restart the server; it auto-resumes on boot.

### OrphanedHolds

Ledger holds exist with no matching session, stream, or escrow. Likely a bug in a recent code path that creates holds without parent records.

1. List the orphans via SQL on `ledger_holds` joined against the parent tables.
2. Decide whether to release (refund) or attribute them to a recovered parent.

### OutboxLagging

Eventbus outbox poll lag is above 60 seconds. Downstream consumers (webhooks, CDC) are behind.

1. Check `alancoin_outbox_poll_lag_seconds` trend — slowly growing or sudden spike?
2. Look for slow `publishBatch` SQL or downstream consumer backpressure.
3. Increase `EVENT_BUS_BUFFER_SIZE` if the spike is buffer-related.

### EventBusDeadLetters

Events failed all retries and entered the DLQ. Each entry needs investigation.

1. List DLQ contents via the admin endpoint.
2. Inspect the originating event payload + last error.
3. Replay via `POST /v1/admin/eventbus/dlq/replay` after fixing the root cause.

### PanicRate

`recovery.LogPanic` captured at least one panic in the last 15 minutes.

1. Search logs for `panic recovered` with the subsystem label from the panic counter.
2. Reproduce locally if possible; treat as a bug fix, not just a noise alert.

### HTTP5xxRate

Server-side 5xx rate exceeds 2%.

1. Identify hot paths: `topk(5, sum by (path) (rate(alancoin_http_requests_total{status=~"5.."}[5m])))`.
2. Correlate with logs and traces (OTLP) for the affected paths.

### DBPoolSaturated

In-use connections exceed 90% of open connections. Either raise `POSTGRES_MAX_OPEN_CONNS` or fix the slow queries holding connections.

1. Check `pg_stat_activity` for long-running statements.
2. Verify `POSTGRES_STATEMENT_TIMEOUT` is honored.
3. Audit for connection leaks if the gauge stays high under low load.

### GatewaySettlementRetries

Gateway settlements are retrying frequently. Often correlated with eventbus or ledger backpressure.

1. Cross-check `OutboxLagging` and `LedgerMismatch`.
2. Inspect gateway logs for the failing settlement reason.

### WatcherReorgDetected

The deposit watcher rewound after detecting a chain reorg deeper than the configured confirmation moat.

1. The watcher auto-recovers — `processTransfer` is idempotent on `tx_hash`, so re-scanning the rewound range will not double-credit.
2. If reorgs are frequent, raise `Confirmations` (default 6) so fewer reorgs require rewinds.
3. Sustained increases in `alancoin_watcher_reorgs_detected_total` signal an unhealthy upstream chain.
