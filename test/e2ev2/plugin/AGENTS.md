# plugin AGENTS

This domain owns process-level `.wkp` plugin compatibility coverage for
`cmd/wukongimv2`.

## Rules

- Keep scenario assertions black-box through real `cmd/wukongimv2` processes,
  plugin Unix sockets, public HTTP readiness, and files written by plugin
  sandboxes.
- Do not import `internalv2/app`, `internalv2/usecase`, or storage internals
  from scenario tests.
- Test plugin binaries under `testdata/` may import the legacy PDK wire proto
  needed to speak host RPC.
