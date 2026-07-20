# Cloud Analysis HTTP Adapters

`internal/infra/cloudanalysis` implements the usecase's narrow `Sources` port
over startup-configured private origins only:

```text
manager -> nodes, workqueues, app logs, diagnostics, task audits, redacted config
prometheus -> /api/v1/query_range with usecase-resolved PromQL, including
              node-exporter textfile evidence for service cgroup memory
node APIs -> /debug/pprof for allowlisted node IDs
```

The manager client authenticates with a dedicated run-scoped capability user or
pre-issued bearer token and caches only the short-lived JWT. It does not reuse a
human manager session. HTTP bodies and profile retention are size bounded.

Raw profiles remain in an in-memory bounded store on the gateway. MCP consumers
receive metadata or symbolized top rows, never raw profile bytes or filesystem
paths. Heap summaries may select only `inuse_space` or `alloc_space`, so retained
and transient cumulative allocation evidence stay explicit without widening the
profile surface. Local Compose uses `StaticRunInspector`; the cloud Analysis Workflow
proves provider inventory and Run Locator identity before opening ingress, then
the host-local gateway reports runtime state without receiving a cloud role.
Phase 1 can exercise a provider-backed inspector locally with
`ProviderRunInspector`. That inspector requires a valid Run Locator and matches
provider, region, account hash, repository, source SHA, scenario digest,
creation time, and lease. A static inspector cannot claim a released run.
The workload source strictly parses the bounded final `diagnostic-summary.json`,
including actual phase windows, structured failed workers, and the measured-run
successful send count used as the storage-growth denominator. It never reads
the raw report or human `summary.md`. Non-truncated failure evidence must account
for every worker included in `summary.worker_failed`; otherwise the source rejects
the document instead of reporting complete evidence. Failure detail accepts only
fixed reason-code templates or an explicit redaction marker, so forged producer
text cannot cross the MCP boundary. The optional failed-worker `operation` also
uses a reason-bound allowlist: person/group send, sendack, recv, recvack, or
sendack-lock for session failures, and `worker_status` or `phase_completion` for
phase timeouts. `worker_stop_failed` is accepted only with phase `stop`; unknown
operations and mismatched reason/phase tuples are rejected rather than exposed
through the MCP. Any structured worker failure also requires a failed status,
non-zero exit code, and non-passed stability verdict. A `worker_stop_failed`
record further requires worker-failure exit code `4` and verdict
`harness_invalid`, so stop evidence cannot coexist with a passing or unrelated
terminal outcome.

Node hosts sample the `wukongim.service` cgroup once per second from one bounded
collector process and again from
`ExecStopPost`. The textfile collector preserves the maximum observed
native peak, the effective limit and swap settings, and monotonic OOM event
totals across service restarts. It detects both the Alibaba Cloud Linux 3
default cgroup v1 memory controller and unified cgroup v2; the Bootstrap Gate
requires readable memory evidence on all three nodes, not merely an active
collector unit. This closes the evidence gap left by the normal 15-second
Prometheus interval without granting the Analysis MCP arbitrary PromQL or
systemd access.
