# Operations MCP Runtime Flow

The runtime package owns high-frequency MCP state that must not enter
Controller Raft.

```text
raw wko_* bearer
  -> parse credential ID and 256-bit secret
  -> read latest Controller-derived DesiredState
  -> require enabled
  -> constant-time SHA-256 digest comparison
  -> bounded Principal
```

Execution budgets and audit records are keyed only by non-secret credential
IDs and stable tool names. Raw tokens, full tool arguments, results, log
keywords, and high-cardinality selectors are never retained.

Ordinary tools allow 60 calls per credential per minute; the two log tools
allow 20. Concurrency is capped at four calls per credential and sixteen per
node. A remote ingress also has a separate 60-request credential budget and
records its forwarding decision. Inactive credential budgets are evicted
after their minute window. Authentication failures have independent source
and node budgets.
Low-cardinality Prometheus metrics record tool/result counts, durations, active
calls, rejections, and authentication failures.

Audits retain the newest 200 summaries in memory and write rotating
`mcp-audit.jsonl` files under the configured log directory. A summary may
contain the credential ID, stable tool, node/Slot/channel-type selectors,
ingress/owner phase, recorder/ingress/owner node IDs, result, duration,
response size, cache hit, and bounded pprof kind/duration.

`Profiler` is the only active observation runtime. It accepts only CPU, heap,
and goroutine profiles; CPU duration is at most 30 seconds. One profile may run
cluster-wide, each node has a full 60-second cooldown after capture completion,
raw profiles are capped in memory, and only parsed top rows leave the execution owner. Heap/goroutine
collection requires a zero duration. The target node rechecks the Controller
MCP owner and revision before capture, then calls that owner to consume an
exact random, one-time, 35-second in-memory profile lease. A node cannot start
profiling by forging the caller node ID because it cannot reconstruct the
owner-held lease. A Controller-derived 30-second stop fence prevents a newly
started owner generation from overlapping a capture that began before the
stop.
