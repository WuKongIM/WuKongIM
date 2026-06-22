# Internalv2 Plugin Observability And Manager Readonly Design

## Goal

Expose enough node-local plugin runtime state and PersistAfter hook pressure for
operators to verify the internalv2 plugin migration before migrating Send,
Receive, Route, config mutation, or binding writes.

## Scope

This phase adds read-only visibility only:

- Prometheus metrics for the internalv2 bounded PersistAfter plugin hook worker.
- `GET /manager/nodes/:node_id/plugins` for node-scoped plugin inventory.
- `GET /manager/nodes/:node_id/plugins/:plugin_no` for one node-scoped plugin detail.
- A narrow manager node RPC path so a manager request can read another node's
  local plugin registry through clusterv2.

This phase does not add plugin config updates, restart, uninstall, plugin
binding APIs, Send hooks, Receive hooks, Route hooks, or hot-reload policy
changes.

## Architecture

The plugin runtime remains node-local. `internal/runtime/plugin.Registry` is
the source of observed process state. `internalv2/usecase/plugin.App` exposes
entry-agnostic list/detail read methods over the registry port. The manager
management usecase adapts those read methods into manager DTOs and handles
local-vs-remote node selection. HTTP stays in `internalv2/access/manager`, and
remote reads use `internalv2/access/node` plus `internalv2/infra/cluster`.

The data flow is:

```text
manager HTTP
  -> internalv2/usecase/management.ListNodePlugins/GetNodePlugin
  -> local node: internalv2/usecase/plugin.ListPlugins/GetPlugin
  -> remote node: infra/cluster ManagementPluginReader
  -> access/node Manager Plugin RPC
  -> target node internalv2/usecase/plugin.ListPlugins/GetPlugin
```

The hook metrics flow is:

```text
runtime/pluginhook.Worker
  -> pluginhook.Observer
  -> internalv2/app plugin metrics observer
  -> pkg/metrics.PluginMetrics
  -> /metrics Prometheus scrape
```

## Manager Contract

The HTTP routes reuse the existing legacy/web shape:

```text
GET /manager/nodes/:node_id/plugins
GET /manager/nodes/:node_id/plugins/:plugin_no
```

The list response contains `node_id`, `total`, and `items`. Each item includes:
`node_id`, `plugin_no`, `name`, `version`, `status`, `enabled`, `methods`,
`priority`, `persist_after_sync`, `reply_sync`, `is_ai`, `pid`,
`last_seen_at`, and `last_error`. `config_template`, `config`, `created_at`,
and `updated_at` remain omitted or empty in this phase because config mutation
and desired-state management are outside the read-only migration.

Routes require `cluster.plugin:r` when manager auth is enabled. Write routes
are intentionally not registered in internalv2 during this phase.

## Metrics

Add a new `pkg/metrics.PluginMetrics` domain with low-cardinality labels:

- `wukongim_plugin_hook_enqueue_total{method,result}`
- `wukongim_plugin_hook_enqueue_wait_seconds{method,result}`
- `wukongim_plugin_hook_invoke_total{method,result}`
- `wukongim_plugin_hook_invoke_duration_seconds{method,result}`

The only method value emitted in this phase is `persist_after`. Result values
come from the bounded worker (`accepted`, `full`, `closed`, `ok`, `error`,
`timeout`, `panic`). The app wires this observer only when metrics are enabled.
Prometheus manager monitor queries remain v2-scoped through the generated
`wukongimv2` job.

## Error Handling

Invalid or empty `node_id` returns `400 bad_request`. Unknown plugin numbers
return `404 not_found`. If plugin reads are disabled or unavailable on the
selected node, the manager returns `503 service_unavailable`. Remote RPC decode
or rejected responses map to the same management-level unavailable error. The
manager does not return partial rows for one node's plugin list because the
registry read is single-node and in-memory.

## Performance

The hot SEND path only pays the existing pluginhook observer call when
PersistAfter enqueue/invoke happens. Metrics use fixed method/result labels and
no plugin number labels, avoiding high-cardinality series. Manager list/detail
reads copy registry snapshots and sort by plugin number; benchmark coverage
must include 1, 16, 256, and 1024 plugin entries.

## Testing

Use TDD for each slice:

- `pkg/metrics`: gather metrics after observing enqueue/invoke outcomes.
- `internalv2/runtime/pluginhook`: verify observer calls remain unchanged and
  benchmark the observer-enabled enqueue path.
- `internalv2/usecase/plugin`: list/detail methods clone method slices and
  return not-found errors.
- `internalv2/usecase/management`: local and remote plugin reads select the
  correct port and preserve node id.
- `internalv2/access/node`: plugin RPC codec/client/server round trips list and
  detail requests.
- `internalv2/access/manager`: list/detail HTTP routes match the legacy/web
  response shape and auth resource.
- `internalv2/app`: plugin metrics observer and manager plugin reader are
  wired when plugin and metrics are enabled.

Benchmarks must cover plugin list scaling and metrics observer overhead.
