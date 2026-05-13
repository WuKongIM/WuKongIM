# Channel Leader Transfer E2E Design

## Goal

Add one process-level black-box E2E scenario that proves P0.6 single-channel explicit channel leader transfer works in a real three-node cluster and does not break WKProto message delivery.

## Context

P0.6 already added `POST /manager/channel-cluster/:type/:id/leader/transfer` and unit coverage across runtime, node RPC, management usecase, manager HTTP, app wiring, and web API/UI. The remaining confidence gap is end-to-end behavior through:

- real `cmd/wukongim` child processes
- real `wukongim.conf` files
- real manager HTTP endpoints
- real WKProto client connections
- authoritative channel runtime metadata
- real cross-node message delivery after leader movement

The existing `test/e2e` layer is the correct place for this because it is already a real-process black-box harness. It must not import `internal/app` or use internal runtime hooks.

## Scope

In scope:

- Add one new scenario under `test/e2e/message/channel_leader_transfer`.
- Start one real three-node cluster with `suite.StartThreeNodeCluster`.
- Activate one person channel through real WKProto sends.
- Discover authoritative channel runtime metadata through manager APIs.
- Transfer that one channel leader to a proven non-leader ISR replica through manager HTTP.
- Assert metadata invariants around the transfer.
- Send more messages after transfer to prove `Send -> SendAck -> Recv -> RecvAck` still works.
- Add reusable black-box manager helpers in `test/e2e/suite` only where they are reusable across future channel-cluster scenarios.
- Update `test/e2e` AGENTS catalogs.

Out of scope:

- Batch leader drain.
- Browser/UI E2E.
- Direct store inspection.
- Direct runtime or `internal/app` imports.
- Exhaustive manager error-code coverage. Unit tests already cover invalid target, missing target, permissions, inactive channel, and no-safe-candidate mappings.
- New production APIs or test-only shortcuts.

## Recommended Approach

Use a new `message` domain scenario:

```text
test/e2e/message/channel_leader_transfer/
  AGENTS.md
  channel_leader_transfer_test.go
```

This scenario belongs in `message` because the user-visible risk is message delivery across a channel leadership change. The manager API is only the trigger.

Add small manager client helpers in `test/e2e/suite/manager_client.go`:

- `FetchChannelClusterReplicas`
- `TransferChannelClusterLeader`
- `WaitForChannelClusterReplicas`
- `PersonChannelID`

Keep scenario-specific assertions in the scenario package. Promote only reusable HTTP and decoding code to `suite`.

## Data Flow

1. The test starts a three-node cluster through the existing suite harness.
2. The test waits for `/readyz` and WKProto readiness on each node.
3. The test resolves Slot `1` topology through `/manager/slots/1`.
4. Sender and recipient connect to two follower nodes.
5. Sender sends one or more person-channel messages to recipient.
6. The test derives the canonical person channel ID from the two UIDs.
7. The test polls `/manager/channel-cluster/:type/:id/replicas` until the channel is active and has at least one non-leader ISR target.
8. The test posts `/manager/channel-cluster/:type/:id/leader/transfer`.
9. The manager route delegates to the existing P0.6 stack. The test observes only HTTP response metadata.
10. The test polls replica detail again until the new leader is visible.
11. Sender and recipient exchange more WKProto messages after transfer.

## Scenario Assertions

Before transfer, the test records:

- `channel.channel_id`
- `channel.channel_type`
- `channel.channel_epoch`
- `channel.leader_epoch`
- `channel.leader`
- `channel.replicas`
- `channel.isr`
- `channel.min_isr`
- `channel.status`
- `channel.features`

The target must be selected from replica rows where:

- `is_leader == false`
- `in_isr == true`

The successful transfer response must satisfy:

- `changed == true`
- `channel.leader == target_node_id`
- `channel.leader_epoch > before.leader_epoch`
- `channel.channel_epoch == before.channel_epoch`
- `channel.replicas == before.replicas`
- `channel.isr == before.isr`
- `channel.min_isr == before.min_isr`
- `channel.status == "active"`
- `channel.features == before.features`

After transfer, the test must prove delivery by sending at least one message after leader movement and observing:

- sender receives `SendAck` with `ReasonSuccess`
- recipient receives `Recv`
- `Recv.FromUID` matches sender UID
- `Recv.ChannelID` matches sender UID for person-channel delivery
- `Recv.ChannelType == frame.ChannelTypePerson`
- payload matches
- `Recv.MessageID` and `Recv.MessageSeq` match `SendAck`
- recipient sends `RecvAck` successfully

## Error Handling And Diagnostics

The test should use bounded diagnostics already provided by `cluster.DumpDiagnostics()`. Assertion failures should include:

- node-scoped stdout/stderr
- app/error log tails
- last observed manager Slot body
- last observed channel replica body where practical

HTTP helper errors should include status code and trimmed response body. Polling helpers should return the last response body when decoding succeeds but the predicate does not.

## File Responsibilities

- `test/e2e/suite/manager_client.go`
  - Owns reusable manager HTTP DTOs and helpers.
  - Must stay black-box and should not know scenario-specific assertions.
- `test/e2e/suite/manager_client_test.go`
  - Owns DTO decode tests and small pure helper tests.
- `test/e2e/message/channel_leader_transfer/channel_leader_transfer_test.go`
  - Owns the scenario flow and assertions.
- `test/e2e/message/channel_leader_transfer/AGENTS.md`
  - Documents scenario purpose, external steps, diagnostics, and run command.
- `test/e2e/AGENTS.md`
  - Adds the scenario to the top-level catalog.
- `test/e2e/message/AGENTS.md`
  - Adds the scenario to the message-domain catalog.

## Test Commands

Focused suite helper checks:

```bash
go test -tags=e2e ./test/e2e/suite -run 'Test(DecodeChannelClusterReplicaDetail|DecodeChannelLeaderTransferResponse|PersonChannelID)' -count=1
```

Scenario:

```bash
go test -tags=e2e ./test/e2e/message/channel_leader_transfer -count=1 -timeout 2m
```

Targeted E2E confidence set:

```bash
go test -tags=e2e ./test/e2e/suite -count=1
go test -tags=e2e ./test/e2e/message/channel_leader_transfer -count=1 -timeout 2m
go test -tags=e2e ./test/e2e/message/cross_node_closure ./test/e2e/message/slot_leader_failover -count=1 -timeout 5m
```

Full E2E, when needed:

```bash
go test -tags=e2e ./test/e2e/... -count=1
```

## Risks

- The channel may not become active immediately after the bootstrap send. Use polling against manager channel-cluster replica detail instead of sleeps.
- The current leader may already be the only ISR row if a cluster is still converging. Poll until there is a non-leader ISR target.
- Message delivery after transfer may race with metadata propagation. The test should poll for the new authoritative leader before sending the post-transfer message.
- E2E runtime can be noisy on slow machines. Keep the scenario narrow and use a bounded timeout.

## Acceptance Criteria

- `test/e2e` remains black-box and imports no `internal/app`.
- The new scenario passes by driving real manager HTTP and real WKProto traffic.
- The scenario proves both metadata invariants and post-transfer delivery.
- The new helper tests pass without starting processes.
- E2E AGENTS catalogs include the new scenario and run command.
