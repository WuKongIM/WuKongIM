# Cloud Analysis HTTP Adapters

`internal/infra/cloudanalysis` implements the usecase's narrow `Sources` port
over startup-configured private origins only:

```text
manager -> nodes, workqueues, app logs, diagnostics, task audits, redacted config
prometheus -> /api/v1/query_range with usecase-resolved PromQL
node APIs -> /debug/pprof for allowlisted node IDs
```

The manager client authenticates with a dedicated run-scoped capability user or
pre-issued bearer token and caches only the short-lived JWT. It does not reuse a
human manager session. HTTP bodies and profile retention are size bounded.

Raw profiles remain in an in-memory bounded store on the gateway. MCP consumers
receive metadata or symbolized top rows, never raw profile bytes or filesystem
paths. Local Compose uses `StaticRunInspector`; the cloud Analysis Workflow
proves provider inventory and Run Locator identity before opening ingress, then
the host-local gateway reports runtime state without receiving a cloud role.
Phase 1 can exercise a provider-backed inspector locally with
`ProviderRunInspector`. That inspector requires a valid Run Locator and matches
provider, region, account hash, repository, source SHA, scenario digest,
creation time, and lease. A static inspector cannot claim a released run.
The workload source strictly parses the bounded final `diagnostic-summary.json`,
including actual phase windows, structured failed workers, and the measured-run
successful send count used as the storage-growth denominator. It never reads
the raw report or human `summary.md`.
