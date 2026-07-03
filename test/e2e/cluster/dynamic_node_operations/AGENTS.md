# dynamic_node_operations AGENTS

This package proves Stage 11 operator workflows for internal dynamic data
nodes through `wkcli node`.

## Scenario Contract

- Start a real three-node `cmd/wukongim` cluster with manager HTTP, metrics,
  bench API, gateway listeners, and short test-only health report intervals.
- Start node 4 as a real seed-join process; do not shortcut by writing manager
  state from the test.
- Use `go run ./cmd/wkcli node ...` or a test-built wkcli binary for lifecycle
  operations after node 4 appears in manager state.
- Keep real WKProto `SEND -> SENDACK` traffic running while the CLI performs
  activate, onboarding, scale-in, gateway drain, and remove.
- Assert CLI stdout includes root-cause evidence such as `safe_to_remove`,
  `blocked_reasons`, health freshness, control revision, and gateway counters.

## Rules

- Keep tests black-box: do not import `internal/app`, `internal/usecase`,
  storage internals, Controller internals, or cluster internals.
- Use public manager HTTP, public `/metrics`, WKProto clients, and process
  handles from `test/e2e/suite`.
- Prefer polling public status over fixed sleeps.
- Keep task fanout bounded with `--max-slot-moves 1`.
- Run this package serially with `-p=1`.

## Running

```bash
GOWORK=off go test -tags=e2e ./test/e2e/cluster/dynamic_node_operations -count=1 -timeout 12m -p=1
```
