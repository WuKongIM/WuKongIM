# Cloud Analysis MCP Flow

`internal/access/cloudanalysismcp` is a protocol adapter only. It registers the
approved tool names with the official Go MCP SDK and delegates every call to
`internal/usecase/cloudanalysis`.

```text
HTTPS Streamable MCP request
  -> exact run-scoped bearer token and expiry
  -> cross-origin protection
  -> inferred JSON Schema validation
  -> cloudanalysis.Service
  -> structured Observation
```

The endpoint is stateless and JSON-response-only. The tool list contains no
shell, file, URL, process, service restart, configuration write, cloud resource,
or deletion operation. `trace_start` and `profile_capture` are annotated as
active non-destructive diagnostics; all other tools are read-only and closed
world.
