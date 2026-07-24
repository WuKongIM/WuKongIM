# Cloud Analysis Flow

`internal/usecase/cloudanalysis` is the entry-independent Analysis MCP usecase.
Every tool call is bound to one exact Run Identity and first proves the run has
not been released before touching live observability sources. `run_inspect`
also returns the canonical effective `wkbench/v1` scenario digest, non-zero
deterministic seed, full effective scenario, source commit, and 256-slot
identity; inconsistent contracts fail closed.

```text
MCP tool input
  -> validate run, node, selector, time/range/count bounds
  -> InspectRun (released inventory stops here)
  -> one narrow Sources method
  -> Observation envelope with an explicit source or point-in-time window
  -> JSON response-size gate
```

Metrics select server-owned query IDs rather than accepting PromQL, including
per-node memory, OOM counters, process start times, active gateway connections,
active channels, recipient-delivery queue/execution/conservation pressure,
channel-append post-commit handoff/retry pressure, and data-disk used bytes for
process-continuity guards and bounded storage-growth calibration. These
delivery queries preserve only bounded node and result dimensions, never UID,
channel, authority-target, or Slot labels. Logs and
diagnostics use fixed private API selectors and opaque cursors. A diagnostics
query can combine an exact physical `slot_id` with the stable PreferredLeader
reconciliation stage without widening Prometheus label cardinality. Those
events are transition evidence with at-most-30-second unchanged resampling, not
frequency counters; reconciliation rates remain Prometheus evidence. Active
diagnostics are serialized across expiring trace rules and all profile kinds,
so only one node is perturbed at a time. Profiles select only `cpu`, `heap`, or
`goroutine`; CPU capture is limited to 30 seconds per call and 60 seconds per
Analysis Session.

The recipient pipeline query contract also exposes the three aggregate stages
that precede or follow the recipient worker. Recipient-authority resolution
rates retain only `instance`, `node_name`, and bounded `result`; exact-target
presence lookup rates retain those node dimensions plus bounded `path`,
`outcome`, and `stale_retry`; ACK batch cumulative counters and P99 retain only
the node dimensions plus bounded `phase` and `outcome`. Counter queries
aggregate away the metric's internal `node_id`, and no query exposes a UID,
authority target, route, physical hash slot, logical Slot, session, or message
identifier. ACK cumulative values are intended for bounded endpoint deltas;
the duration queries use a fixed one-minute histogram window.

`workload_inspect` returns the bounded diagnostic summary contract rather than
raw worker reports. It includes actual ingress QPS and successful send count.
Its actual phase windows and structured worker failures let
consumers choose the narrowest next observation without parsing Markdown or
guessing a failed worker from aggregate counts. A failed worker may include a
reason-bound low-cardinality person/group operation or the `worker_status` /
`phase_completion` timeout control stage; missing operation remains explicitly
unknown and never falls back to parsing detail text. Terminal stop failures use
the exact `phase=stop`, `reason_code=worker_stop_failed` tuple.
Diagnosis references preserve three workload lifecycle shapes: terminal
`complete=true + completed + passed|failed`, running
`complete=false + in_progress + null`, and incomplete source/tool failure with
`complete=false` plus null state and status. Nullable lifecycle and note keys
remain present so unknown evidence is never rewritten as an empty string or zero.

The package owns no HTTP, MCP protocol, cloud SDK, shell, filesystem, restart,
configuration-write, or cleanup behavior.
