# message AGENTS

This file is for agents working inside `test/e2ev2/message`.

## Domain Purpose

This domain covers black-box message and conversation behavior for
`cmd/wukongimv2`.

## Scenarios

| Scenario | Purpose | Run |
| --- | --- | --- |
| `single_node_send` | Prove `cmd/wukongimv2` can complete one single-node cluster WKProto `SEND -> SENDACK` closure and expose sender/receiver rows through `/conversation/list`. | `GOWORK=off go test -tags=e2e ./test/e2ev2/message/single_node_send -count=1` |
| `send_permission` | Prove `cmd/wukongimv2` enforces migrated legacy send-permission decisions through public channel-management and `/message/send` HTTP APIs. | `GOWORK=off go test -tags=e2e ./test/e2ev2/message/send_permission -count=1` |
| `recipient_authority` | Prove `cmd/wukongimv2` routes committed group messages through recipient UID authority, updates subscriber-owned `/conversation/list` rows, and exposes low-cardinality authority metrics, with an opt-in 100k subscriber stress path. | `GOWORK=off go test -tags=e2e ./test/e2ev2/message/recipient_authority -count=1` |
| `cross_node_delivery` | Prove a static three-node `cmd/wukongimv2` cluster can deliver person-channel messages between users connected to different nodes in both directions. | `GOWORK=off go test -tags=e2e ./test/e2ev2/message/cross_node_delivery -count=1 -timeout 2m` |
| `cmd_sync` | Prove unified normal/CMD conversation projection isolation, `/message/sync` delivery, and `/message/syncack` draining in a single-node cluster. | `GOWORK=off go test -tags=e2e ./test/e2ev2/message/cmd_sync -count=1 -timeout 2m` |
| `message_retention` | Prove a static three-node `cmd/wukongimv2` cluster forwards manager retention requests to the channel leader and all replicas consistently hide retained message sequences after leader restart. | `GOWORK=off go test -tags=e2e ./test/e2ev2/message/message_retention -count=1 -timeout 2m -p=1` |

## Maintenance Rules

- When adding a new v2 message scenario, create
  `test/e2ev2/message/<scenario>/`.
- Give each scenario its own `AGENTS.md` and one primary
  `<scenario>_test.go`.
- If a scenario is added, removed, renamed, or its run command, steps, or
  diagnostics change, update this file and the scenario's `AGENTS.md` in the
  same change.
- Keep one-off helpers inside the scenario directory first. Only promote them
  to `test/e2ev2/suite` after real multi-scenario reuse appears.
