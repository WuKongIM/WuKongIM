# http_forward AGENTS

This scenario proves real `.wkp` plugins can use the legacy
`/plugin/httpForward` host RPC in an internalv2 multi-node cluster.

## Run

```bash
GOWORK=off go test -tags=e2e ./test/e2ev2/plugin/http_forward -count=1 -timeout 2m
```

## Rules

- Start a real three-node `cmd/wukongim` cluster.
- Enable the test plugin only on the nodes needed by the scenario.
- Validate local `toNodeId=0` routing, positive-node remote routing, and the
  explicit deferred error for `toNodeId=-1`.
- Observe results only through plugin sandbox files and public process
  diagnostics.
