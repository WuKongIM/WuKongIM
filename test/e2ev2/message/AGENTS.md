# message AGENTS

This file is for agents working inside `test/e2ev2/message`.

## Domain Purpose

This domain covers black-box message and conversation behavior for
`cmd/wukongim`.

## Scenarios

| Scenario | Purpose | Run |
| --- | --- | --- |
| `single_node_send` | Prove `cmd/wukongim` can complete one single-node cluster WKProto `SEND -> SENDACK` closure and expose sender/receiver rows through `/conversation/list`. | `GOWORK=off go test -tags=e2e ./test/e2ev2/message/single_node_send -count=1` |
| `webhook` | Prove a single-node cluster posts `user.onlinestatus`, `msg.notify`, and `msg.offline` webhook callbacks to an external HTTP endpoint after a real WKProto SEND. | `GOWORK=off go test -tags=e2e ./test/e2ev2/message/webhook -count=1 -timeout 2m -p=1` |
| `send_permission` | Prove `cmd/wukongim` enforces migrated legacy send-permission decisions through public channel-management and `/message/send` HTTP APIs. | `GOWORK=off go test -tags=e2e ./test/e2ev2/message/send_permission -count=1` |
| `recipient_authority` | Prove `cmd/wukongim` routes committed group messages through recipient UID authority, updates subscriber-owned `/conversation/list` rows, and exposes low-cardinality authority metrics, with an opt-in 100k subscriber stress path. | `GOWORK=off go test -tags=e2e ./test/e2ev2/message/recipient_authority -count=1` |
| `cross_node_delivery` | Prove a static three-node `cmd/wukongim` cluster can deliver person-channel messages between users connected to different nodes in both directions. | `GOWORK=off go test -tags=e2e ./test/e2ev2/message/cross_node_delivery -count=1 -timeout 2m` |
| `cmd_sync` | Prove unified normal/CMD conversation projection isolation, `/message/sync` delivery, and `/message/syncack` draining in a single-node cluster. | `GOWORK=off go test -tags=e2e ./test/e2ev2/message/cmd_sync -count=1 -timeout 2m` |
| `message_retention` | Prove a static three-node `cmd/wukongim` cluster forwards manager retention requests to the channel leader, physically cleans retained local message rows when enabled, and consistently hides retained message sequences after leader restart. | `GOWORK=off go test -tags=e2e ./test/e2ev2/message/message_retention -count=1 -timeout 2m -p=1` |
| `channelv2_failover` | Prove a static three-node `cmd/wukongim` cluster preserves ChannelV2 quorum-acknowledged messages after one data node stops, fails over affected channel leaders, and fails closed for new placement while a required replica is unavailable. | `GOWORK=off go test -tags=e2e ./test/e2ev2/message/channelv2_failover -count=1 -timeout 3m -p=1` |

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
