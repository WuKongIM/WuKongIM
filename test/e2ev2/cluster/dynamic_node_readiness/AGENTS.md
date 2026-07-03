# dynamic_node_readiness AGENTS

This package proves Stage 9 production readiness for internalv2 dynamic data
nodes under real traffic.

## Scenario Contract

- Start a static three-node `cmd/wukongim` cluster with manager HTTP, metrics,
  bench API, gateway listeners, and short test-only health report intervals.
- Keep real WKProto `SEND -> SENDACK` traffic running while membership changes.
- Start node 4 through seed join and wait for manager-visible `joining` plus
  fresh health evidence.
- Activate node 4 and prove it becomes schedulable only after fresh alive health.
- Start one bounded Slot onboarding move to node 4 while traffic continues.
- Mark node 4 leaving, enable gateway drain, advance scale-in Slot drain, wait
  for safe-to-remove, and remove node 4.
- Prove public manager status and public metrics explain health freshness and
  lifecycle blockers throughout the flow.

## Rules

- Keep tests black-box: do not import `internalv2/app`, `internalv2/usecase`,
  storage internals, ControllerV2 internals, or clusterv2 internals.
- Use public manager HTTP, public `/metrics`, WKProto clients, and process
  handles from `test/e2ev2/suite`.
- Prefer polling public status over fixed sleeps.
- Keep task fanout bounded: usually `max_slot_moves=1`.
- Run this package serially with `-p=1`.

## Running

```bash
GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_readiness -count=1 -timeout 12m -p=1
```
