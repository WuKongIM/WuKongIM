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
paths. Local Compose uses `StaticRunInspector`; cloud phases replace only that
port with provider inventory proof. Phase 1 can exercise that same boundary by
opening the persistent fake-provider inventory with `ProviderRunInspector`.
The provider inspector requires a valid Run Locator and matches provider,
region, account hash, repository, source SHA, scenario digest, creation time,
and lease before returning inventory. A static inspector cannot claim a
released run.
