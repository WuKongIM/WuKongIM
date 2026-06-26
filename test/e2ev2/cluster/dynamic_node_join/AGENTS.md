# dynamic_node_join AGENTS

This scenario package proves the Stage 2-6 dynamic data-node lifecycle through
a real multi-node `cmd/wukongimv2` cluster.

## Scenario Contract

- Start a static three-node cluster with manager HTTP enabled.
- Start node 4 in seed-join mode with `WK_CLUSTER_SEEDS`,
  `WK_CLUSTER_ADVERTISE_ADDR`, and `WK_CLUSTER_JOIN_TOKEN`.
- Prove seed join and manager activation move node 4 from `joining` to
  `active` without implicit Slot assignment changes.
- Prove an activated dynamic node participates in real online WKProto delivery,
  not only local `SENDACK`.
- Prove bounded Slot onboarding can run while gateway SEND remains available.
- Prove leaving, gateway drain, and empty-node removal can complete safely.
- Prove live-session gateway drain rejects new sessions while existing sessions
  can finish in-flight sends, and final remove waits for runtime counters.
- Prove Slot replica scale-in drain uses manager `plan` and `advance` before
  final removal.
- Prove unsafe scale-in routes return bounded conflicts and removed-node final
  remove is idempotent.
- Prove invalid join tokens and unreachable advertise addresses do not mutate
  membership or Slot assignments beyond their intended safe state.
- Prove concurrent onboarding and scale-in task creation create at most one
  durable task for the same Slot.

## Rules

- Keep tests black-box: do not import `internalv2/app`, `internalv2/usecase`,
  storage internals, or control-plane internals.
- Dynamic join tests must let node 4 call seed join itself; do not shortcut by
  calling manager `JoinNode` directly.
- Keep bounded task requests small, usually `max_slot_moves=1`.
- Prefer polling public manager status over fixed sleeps.
- Run this package serially with `-p=1`.

## Running

```bash
GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_join -count=1 -timeout 10m -p=1
```
