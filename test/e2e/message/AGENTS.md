# message AGENTS

This file is for agents working inside `test/e2e/message`.

## Domain Purpose

This domain covers black-box message delivery scenarios.

## Scenarios

| Scenario | Purpose | Run |
| --- | --- | --- |
| `single_node_send_message` | Prove one fresh single-node cluster can complete a WKProto send/receive closure. | `go test -tags=e2e ./test/e2e/message/single_node_send_message -count=1` |
| `cross_node_closure` | Prove a three-node cluster can complete one cross-node message closure across two follower nodes. | `go test -tags=e2e ./test/e2e/message/cross_node_closure -count=1` |
| `slot_leader_failover` | Prove cross-node delivery still works after the current slot leader stops and a new leader takes over. | `go test -tags=e2e ./test/e2e/message/slot_leader_failover -count=1` |
| `expired_leader_lease` | Reproduce the current cross-node delivery failure that appears once channel runtime metadata is observed with an expired leader lease. | `go test -tags=e2e ./test/e2e/message/expired_leader_lease -count=1` |
| `idle_cross_node_delivery_timeout` | Reproduce the current cross-node delivery timeout after one bootstrap message sits idle until the channel runtime leader lease expires. | `go test -tags=e2e ./test/e2e/message/idle_cross_node_delivery_timeout -count=1` |

## Maintenance Rules

- When adding a new message scenario, create `test/e2e/message/<scenario>/`.
- Give each scenario its own `AGENTS.md` and one primary
  `<scenario>_test.go`.
- If a scenario is added, removed, renamed, or its run command, steps, or
  diagnostics change, update this file and the scenario's `AGENTS.md` in the
  same change.
- Keep one-off helpers inside the scenario directory first. Only promote them
  to `test/e2e/suite` after real multi-scenario reuse appears.
