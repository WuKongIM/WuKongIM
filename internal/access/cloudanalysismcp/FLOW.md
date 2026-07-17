# Cloud Analysis MCP Flow

`internal/access/cloudanalysismcp` is a protocol adapter only. It registers the
approved tool names with the official Go MCP SDK and delegates every call to
`internal/usecase/cloudanalysis`.

```text
HTTPS Streamable MCP request
  -> exact run-scoped bearer token and expiry (static token locally, dynamic GitHub OIDC session in cloud)
  -> cross-origin protection
  -> inferred JSON Schema validation
  -> cloudanalysis.Service
  -> structured Observation
```

The MCP endpoint is stateless and JSON-response-only. `workload_inspect` exposes
only the bounded, parsed wkbench diagnostic summary: threshold measurements,
actual phase windows, structured failed workers, and the measured-run successful
send count. Failure details are bounded and redacted; raw report content, URLs,
messages, and paths are never returned. The tool list contains no
shell, file, URL, process, service restart, configuration write, cloud resource,
or deletion operation. `trace_start` and `profile_capture` are annotated as
active non-destructive diagnostics; all other tools are read-only and closed
world.

`POST /analysis/token` is also owned by this access adapter. It parses the
GitHub OIDC bearer request and serializes the bounded token response, while
`cmd/wkanalysis` supplies the claim verifier and session issuer as narrow
callbacks. The adapter never owns OIDC keys or Analysis Token storage.
