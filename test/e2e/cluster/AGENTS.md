# cluster AGENTS

This file is for agents working inside `test/e2e/cluster`.

## Domain Purpose

This domain covers black-box cluster membership and control-plane behavior that is observable through real node processes and public APIs.

## Scenarios

| Scenario | Purpose | Run |
| --- | --- | --- |
| `dynamic_node_join` | Prove a fourth data node can join a running three-node cluster through seed config, exchange WKProto person messages with an existing node, receive Slot resources through manager onboarding, and optionally catch up from a large Slot snapshot. | `go test -tags=e2e ./test/e2e/cluster/dynamic_node_join -count=1` |
| `controller_snapshot` | Prove a stopped Controller follower can rejoin after leader compaction by restoring a large Controller Raft snapshot generated through the test-data API. | `go test -tags=e2e ./test/e2e/cluster/controller_snapshot -count=1` |
| `node_scalein_channel_drain` | Prove manager-driven data-node scale-in drains Slot ownership, channel replicas, active channel migrations, and runtime sessions before reporting the node safe to remove while WKProto delivery continues. | `go test -tags=e2e ./test/e2e/cluster/node_scalein_channel_drain -count=1 -timeout 3m` |
| `gofail_transport` | Opt-in smoke that starts a gofail-enabled single-node cluster and verifies transport failpoints are exposed through `GOFAIL_HTTP`. | `WK_E2E_BINARY=/tmp/wukongim-gofail WK_E2E_GOFAIL_TRANSPORT_SMOKE=1 go test -tags=e2e ./test/e2e/cluster/gofail_transport -count=1` |

## Maintenance Rules

- When adding a new cluster scenario, create `test/e2e/cluster/<scenario>/`.
- Give each scenario its own `AGENTS.md` and one primary `<scenario>_test.go`.
- If a scenario is added, removed, renamed, or its run command, steps, or diagnostics change, update this file and the scenario's `AGENTS.md` in the same change.
- Keep one-off helpers inside the scenario directory first. Only promote them to `test/e2e/suite` after real multi-scenario reuse appears.
