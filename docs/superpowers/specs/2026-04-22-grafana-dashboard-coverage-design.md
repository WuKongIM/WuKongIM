# Grafana Dashboard Coverage Design

## Background

WuKongIM already exports a broad set of Prometheus metrics, but the current Grafana assets under `docker/observability/grafana` expose only a subset of them. The gap is most visible in transport/RPC congestion metrics and in deeper runtime metrics for gateway, channel, slot, controller, and storage behavior. This makes troubleshooting slower because operators have to inspect raw Prometheus queries instead of navigating purpose-built dashboards.

## Goals

- Cover all currently exported `wukongim_*` business metrics in Grafana.
- Keep the landing dashboard focused on global health and first-look SLI signals.
- Split deep-dive transport/RPC diagnostics away from runtime/storage internals.
- Preserve the current provisioning flow so `docker-compose` can auto-load the dashboards without manual Grafana import.

## Non-Goals

- No Prometheus recording rules or alert rules in this change.
- No new metrics emission in Go code; the work only consumes metrics already exported by the application.
- No high-cardinality templating such as `slot_id` variables.

## Dashboard Layout

### 1. `wukongim-overview.json`

Purpose: first-look operational dashboard.

Characteristics:
- Small number of panels (roughly 10-12).
- Prioritizes cluster health, user traffic, core latency, and a small number of high-signal error indicators.
- Includes a transport/RPC timeout signal so obvious RPC regressions appear on the landing page.

Planned panels:
- Alive Nodes
- Active Gateway Connections
- Incoming Message QPS
- Channel Append P99
- Active Migrations
- Active Controller Tasks
- Total Disk Usage
- RPC Client Timeout Rate
- Gateway Connections by Protocol
- Gateway Message Flow
- Transport Throughput
- Controller Node Health

### 2. `wukongim-transport-rpc.json`

Purpose: diagnose forwarding, node-to-node RPC, congestion, and connectivity issues.

Panel groups:
- Summary: RPC server/client QPS, timeout rate, queue-full rate, dial-error rate, inflight.
- Latency: RPC server P95/P99, RPC client P95/P99, dial P95/P99.
- Failures: result breakdowns by `service`, `result`, `target_node`, and `kind`.
- Connectivity: pool connection state and transport throughput.

Covered metrics:
- `wukongim_transport_rpc_total`
- `wukongim_transport_rpc_duration_seconds`
- `wukongim_transport_rpc_client_total`
- `wukongim_transport_rpc_client_duration_seconds`
- `wukongim_transport_rpc_inflight`
- `wukongim_transport_enqueue_total`
- `wukongim_transport_dial_total`
- `wukongim_transport_dial_duration_seconds`
- `wukongim_transport_sent_bytes_total`
- `wukongim_transport_received_bytes_total`
- `wukongim_transport_connections_pool_active`
- `wukongim_transport_connections_pool_idle`

### 3. `wukongim-runtime-storage.json`

Purpose: inspect internal runtime behavior without mixing it with transport diagnostics.

Panel groups:
- Gateway: connection lifecycle, auth status/latency, frame handling latency, bytes in/out.
- Channel: append result/latency, fetch rate/latency, active channels.
- Slot: proposal rate, apply latency, leader-election rate, top active slots.
- Controller: decision rate/latency, active/completed tasks, migration completion, node health.
- Storage: total and per-store usage.

Covered metrics:
- `wukongim_gateway_connections_total`
- `wukongim_gateway_auth_total`
- `wukongim_gateway_auth_duration_seconds`
- `wukongim_gateway_messages_received_bytes_total`
- `wukongim_gateway_messages_delivered_bytes_total`
- `wukongim_gateway_frame_handle_duration_seconds`
- `wukongim_channel_append_total`
- `wukongim_channel_append_duration_seconds`
- `wukongim_channel_fetch_total`
- `wukongim_channel_fetch_duration_seconds`
- `wukongim_channel_active_channels`
- `wukongim_slot_proposals_total`
- `wukongim_slot_apply_duration_seconds`
- `wukongim_slot_leader_elections_total`
- `wukongim_controller_decisions_total`
- `wukongim_controller_decision_duration_seconds`
- `wukongim_controller_tasks_active`
- `wukongim_controller_tasks_completed_total`
- `wukongim_controller_hashslot_migrations_active`
- `wukongim_controller_hashslot_migrations_total`
- `wukongim_controller_nodes_alive`
- `wukongim_controller_nodes_suspect`
- `wukongim_controller_nodes_dead`
- `wukongim_storage_disk_usage_bytes`

## Variable Strategy

Shared variable:
- `node_name` on all dashboards.

Transport/RPC dashboard additional variables:
- `service`
- `target_node`

Runtime/storage dashboard additional variables:
- `protocol`
- `store`

Design rules:
- Use low-cardinality labels only.
- Do not add a `slot_id` variable.
- Keep `includeAll=true` behavior consistent with the current dashboard.

## Query and Visualization Rules

- Use `sum(...)` for cluster-level stat panels.
- Use `sum by (...)` for breakdown panels.
- Use `histogram_quantile()` over `_bucket` series for P95/P99 latency panels.
- Keep legends aligned with stable labels such as `service`, `result`, `target_node`, `protocol`, and `store`.
- Reuse current dashboard styling patterns where practical so the new dashboards feel native to the repo.

## Validation Strategy

- Add an automated dashboard asset test that parses every dashboard JSON file.
- Verify that every exported `wukongim_*` metric from `pkg/metrics/*.go` appears in at least one dashboard file.
- Verify the expected set of dashboard files exists and can be loaded from provisioning.
- Run focused verification locally after asset changes.

## Risks and Mitigations

- Risk: dashboard sprawl.
  - Mitigation: keep overview small and move deep-dive content into the two dedicated dashboards.
- Risk: panels query labels that do not exist on some metrics.
  - Mitigation: centralize metric coverage validation and reuse actual label names from the metric definitions.
- Risk: future metrics get added but dashboards lag behind again.
  - Mitigation: keep the coverage test in-repo so missing dashboard references fail validation quickly.
