# Operations Observation Flow

`opsobserve` owns the entry-independent validation and response contract for
the embedded operations MCP.

```text
MCP tool
  -> opsobserve.Service validates the closed-world request
  -> narrow Source method
  -> bounded SourceResult
  -> wukongim/ops-observation/v1 envelope
```

The package never accepts a URL, filesystem path, command, PromQL expression,
SQL statement, or a general Controller writer. Source failures become explicit
`unavailable` observations with an `unknown` verdict; missing evidence is never
reported as zero or healthy.

The frozen registry contains:

- `cluster_health`, `node_inspect`, `slot_inspect`,
  `channel_runtime_inspect`, and `controller_tasks_query`;
- `metrics_query_range` using server-owned query IDs only;
- `logs_search` and `logs_context` over fixed application-log sources;
- `diagnostics_query`, `config_read_redacted`, and `backup_inspect`;
- `pprof_analyze`.

Every request is closed-world decoded and every response is capped at 1 MiB.
Logs use opaque cursors, return raw lines capped at 8 KiB each, default to 100
and cap at 200 lines, and mark the content untrusted. Metric ranges are at most
24 hours, 100 series, and 2,000 points per series. Point lookups for channel
runtime state never scan the channel catalog. Short inventory reads use a
three-second singleflight cache and redacted configuration uses a 30-second
cache; logs, diagnostics, and pprof are not cached.

`pprof_analyze` is the only active observation. Its request is still
closed-world and bounded to one node, one supported profile kind, at most 30
CPU seconds, and at most 100 parsed rows.
