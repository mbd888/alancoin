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
