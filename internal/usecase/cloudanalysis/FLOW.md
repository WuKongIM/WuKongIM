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

Metrics select server-owned query IDs rather than accepting PromQL. Logs and
diagnostics use fixed private API selectors and opaque cursors. Active
diagnostics are serialized across expiring trace rules and all profile kinds,
so only one node is perturbed at a time. Profiles select only `cpu`, `heap`, or
`goroutine`; CPU capture is limited to 30 seconds per call and 60 seconds per
Analysis Session.

The package owns no HTTP, MCP protocol, cloud SDK, shell, filesystem, restart,
configuration-write, or cleanup behavior.
