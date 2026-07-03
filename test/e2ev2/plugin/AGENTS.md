# plugin AGENTS

This domain owns process-level `.wkp` plugin compatibility coverage for
`cmd/wukongim`.

## Rules

- Keep scenario assertions black-box through real `cmd/wukongim` processes,
  plugin Unix sockets, public HTTP readiness, and files written by plugin
  sandboxes.
- Do not import `internal/app`, `internal/usecase`, or storage internals
  from scenario tests.
- Test plugin binaries under `testdata/` may import the legacy PDK wire proto
  needed to speak host RPC.
