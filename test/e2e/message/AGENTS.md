# message AGENTS

This file is for agents working inside `test/e2e/message`.

## Domain Purpose

This domain covers black-box message delivery scenarios.

## Scenarios

| Scenario | Purpose | Run |
| --- | --- | --- |
| `single_node_send_message` | Prove one fresh single-node cluster can complete a WKProto send/receive closure. | `go test -tags=e2e ./test/e2e/message/single_node_send_message -count=1` |
| `wukongimv2_single_node_send` | Prove `cmd/wukongimv2` can complete one single-node cluster WKProto SEND -> SENDACK closure and expose sender/receiver rows through `/conversation/list`. | `GOWORK=off go test -tags=e2e ./test/e2e/message/wukongimv2_single_node_send -count=1` |
| `wukongimv2_recipient_authority` | Prove `cmd/wukongimv2` routes committed group messages through recipient UID authority, updates subscriber-owned `/conversation/list` rows, and exposes low-cardinality authority metrics, with an opt-in 100k subscriber stress path. | `GOWORK=off go test -tags=e2e ./test/e2e/message/wukongimv2_recipient_authority -count=1` |
| `cross_node_closure` | Prove a three-node cluster can complete one cross-node message closure across two follower nodes. | `go test -tags=e2e ./test/e2e/message/cross_node_closure -count=1` |
| `delivery_tag_group_delivery` | Prove group-channel delivery uses delivery tag partitions, refreshes after public subscriber mutations, and supports opt-in 100k subscriber stress. | `go test -tags=e2e ./test/e2e/message/delivery_tag_group_delivery -count=1` |
| `request_scoped_subscriber_delivery` | Prove request-scoped `/message/send` subscribers deliver only to the requested online subscribers across nodes. | `go test -tags=e2e ./test/e2e/message/request_scoped_subscriber_delivery -count=1` |
| `channel_activation_profile` | Opt-in profile scenario that activates configurable waves of 100 concurrent group channels and writes pprof CPU/heap analysis artifacts. | `WK_E2E_CHANNEL_ACTIVATION_PROFILE=1 go test -tags=e2e ./test/e2e/message/channel_activation_profile -count=1` |
| `channel_leader_transfer` | Prove an active person-channel leader can be transferred to a non-leader ISR replica through manager APIs while WKProto delivery continues. | `go test -tags=e2e ./test/e2e/message/channel_leader_transfer -count=1 -timeout 2m` |
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
