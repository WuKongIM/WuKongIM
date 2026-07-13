# Analysis MCP Tool Contract

Use only the run-specific MCP configured by the Analysis Workflow. Every input includes the exact `run_id`. Parameters select fixed IDs and private resources, never URLs, paths, commands, or processes.

## Read tools

| Tool | Purpose | Key bounds |
|---|---|---|
| `run_inspect` | Prove exact run state and inventory | Always first |
| `cluster_snapshot` | Nodes and workqueues | Aggregate, bounded response |
| `metrics_query_range` | Server-owned PromQL by `query_id` | Maximum 72 hours, 5,000 samples/series, step 1–900 seconds |
| `logs_search` | Literal log search on one node | Sources `app` or `error`, maximum 200 lines |
| `logs_context` | Cursor page from one node/source | Opaque returned cursor, maximum 200 combined entries |
| `diagnostics_query` | Retained diagnostics filters | Maximum 500 events |
| `task_audits_query` | Retained Controller task history | Maximum 200 tasks |
| `trace_query` | Events for an exact trace ID | Maximum 500 events |
| `profile_top` | Symbolized rows for a gateway profile ID | Maximum 100 rows |
| `profile_list` | Profile metadata | Maximum 100 captures |
| `config_read_redacted` | Allowlisted effective node config | One node; already redacted |

## Active tools

`trace_start` accepts one node and either:

- `target=sender_uid` with `uid`; or
- `target=channel` with `channel_id` and positive `channel_type`.

TTL is 1–900 seconds. The tool cannot change global sampling or log level.

`profile_capture` accepts one node and `cpu`, `heap`, or `goroutine`. CPU requires `seconds=1..30`; snapshots omit seconds. Raw profile bytes and file paths are never returned.

## Metric query IDs

- `targets_up`
- `send_rate`
- `deliver_rate`
- `append_ok_rate`
- `append_error_rate`
- `gateway_queue_depth`
- `runtime_queue_pressure`
- `storage_commit_queue_depth`
- `delivery_retry_queue_depth`
- `process_cpu_rate`
- `process_resident_memory`
- `go_goroutines`

Use RFC3339 `start` and `end` plus integer `step_seconds`. Begin with a small query set and widen only when the result changes the diagnosis.

## Error interpretation

- `run released`: stop as released; no historical data is promised.
- `run identity mismatch`: stop as `unknown_run` or configuration mismatch.
- `run contract mismatch`: stop; locator, inventory, or effective scenario identity is inconsistent.
- `response too large`: narrow the range, filters, or limit.
- `diagnostic busy`: wait for the current active capture to finish; do not parallelize.
- `diagnostic budget exceeded`: stop active profiling and report the missing evidence.
- private source timeout/unreachable with non-empty inventory: `insufficient_evidence`, not `released`.
