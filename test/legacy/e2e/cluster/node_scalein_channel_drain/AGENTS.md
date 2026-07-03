# node_scalein_channel_drain AGENTS

This scenario proves that manager-driven data-node scale-in drains channel
ownership before reporting the node as safe to remove.

## Run

```bash
go test -tags=e2e,legacy_e2e ./test/legacy/e2e/cluster/node_scalein_channel_drain -count=1 -timeout 3m
```

## Scenario Contract

- Start a three-node cluster with two controller voters and two Slot replicas
  so node 3 is a removable data-node tail while still hosting some managed Slot
  replicas.
- Activate a real WKProto person channel whose authoritative channel metadata
  includes node 3.
- Keep a real WKProto gateway session connected to node 3 so the scenario
  proves `waiting_connections` is reached before safe removal; close it through
  the client only after manager reports connection drain is the remaining gate.
- Start `/manager/nodes/3/scale-in/start` with StatefulSet tail confirmation.
- Poll public manager scale-in status and bounded advance actions until
  `ready_to_remove`.
- Assert node 3 has no Slot replicas, Slot leaders, channel leaders, channel
  replicas, active channel migrations, or active runtime sessions before
  `safe_to_remove` becomes true.
- Verify the activated channel no longer lists node 3 as a replica and can
  still deliver a real WKProto message.

## Diagnostics

- Include `cluster.DumpDiagnostics()` in failures.
- Include manager scale-in and channel replica response bodies when assertions
  depend on those responses.
- Do not use fixed sleeps; use the suite polling helpers or local polling loops.
