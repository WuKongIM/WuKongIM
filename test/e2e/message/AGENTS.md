# message AGENTS

This file is for agents working inside `test/e2e/message`.

## Domain Purpose

This domain covers black-box message and conversation behavior for
`cmd/wukongim`.

## Scenarios

| Scenario | Purpose | Run |
| --- | --- | --- |
| `single_node_send` | Prove `cmd/wukongim` can complete one single-node cluster WKProto `SEND -> SENDACK` closure and expose sender/receiver rows through `/conversation/list`. | `GOWORK=off go test -tags=e2e ./test/e2e/message/single_node_send -count=1` |
| `webhook` | Prove a single-node cluster posts `user.onlinestatus`, `msg.notify`, and `msg.offline` webhook callbacks to an external HTTP endpoint after a real WKProto SEND. | `GOWORK=off go test -tags=e2e ./test/e2e/message/webhook -count=1 -timeout 2m -p=1` |
| `send_permission` | Prove `cmd/wukongim` enforces migrated legacy send-permission decisions through public channel-management and `/message/send` HTTP APIs. | `GOWORK=off go test -tags=e2e ./test/e2e/message/send_permission -count=1` |
| `message_event_stream` | Prove `/message/event` buffers stream deltas in the Slot-leader cache, forwards from non-leader nodes, fails closed after Slot-leader cache loss, proposes one finish batch, exposes public metrics, and survives restart through `/channel/messagesync` event summaries. | `GOWORK=off go test -tags=e2e ./test/e2e/message/message_event_stream -count=1 -timeout 2m` |
| `recipient_authority` | Prove `cmd/wukongim` routes committed group messages through recipient UID authority, updates subscriber-owned `/conversation/list` rows, and exposes low-cardinality authority metrics, with an opt-in 100k subscriber stress path. | `GOWORK=off go test -tags=e2e ./test/e2e/message/recipient_authority -count=1` |
| `cross_node_delivery` | Prove a static three-node `cmd/wukongim` cluster can deliver person-channel messages between users connected to different nodes in both directions. | `GOWORK=off go test -tags=e2e ./test/e2e/message/cross_node_delivery -count=1 -timeout 2m` |
| `cmd_sync` | Prove unified normal/CMD conversation projection isolation, `/message/sync` delivery, and `/message/syncack` draining in a single-node cluster. | `GOWORK=off go test -tags=e2e ./test/e2e/message/cmd_sync -count=1 -timeout 2m` |
| `message_retention` | Prove a static three-node `cmd/wukongim` cluster forwards manager retention requests to the channel leader, physically cleans retained local message rows when enabled, and consistently hides retained message sequences after leader restart. | `GOWORK=off go test -tags=e2e ./test/e2e/message/message_retention -count=1 -timeout 2m -p=1` |
| `channel_failover` | Prove a static three-node `cmd/wukongim` cluster preserves Channel quorum-acknowledged messages after one data node stops, fails over affected channel leaders, and fails closed for new placement while a required replica is unavailable. | `GOWORK=off go test -tags=e2e ./test/e2e/message/channel_failover -count=1 -timeout 3m -p=1` |

## Maintenance Rules

- When adding a new message scenario, create
  `test/e2e/message/<scenario>/`.
- Give each scenario its own `AGENTS.md` and one primary
  `<scenario>_test.go`.
- If a scenario is added, removed, renamed, or its run command, steps, or
  diagnostics change, update this file and the scenario's `AGENTS.md` in the
  same change.
- Keep one-off helpers inside the scenario directory first. Only promote them
  to `test/e2e/suite` after real multi-scenario reuse appears.
