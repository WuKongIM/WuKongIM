# channel_leader_transfer AGENTS

This file is for agents working on `test/legacy/e2e/message/channel_leader_transfer`.

## Purpose

Prove a real three-node cluster can safely transfer one active person-channel leader to a non-leader ISR replica through the manager channel-cluster API, then continue WKProto delivery.

## Cluster Shape

One three-node cluster. Sender and recipient connect to follower nodes. The channel leader transfer target is selected from authoritative non-leader ISR replica rows.

## External Steps

1. Start a real three-node cluster through `test/legacy/e2e/suite`.
2. Wait for cluster readiness and resolve Slot `1` topology through manager APIs.
3. Connect two WKProto clients to follower nodes.
4. Send a bootstrap person-channel message to activate channel runtime metadata.
5. Poll `/manager/channel-cluster/:type/:id/replicas` until the channel is active and has a non-leader ISR target.
6. POST `/manager/channel-cluster/:type/:id/leader/transfer`.
7. Assert the authoritative leader changes while replica set, ISR, MinISR, status, features, and channel epoch stay stable.
8. Send another WKProto message after transfer and observe `SendAck`, `Recv`, and `RecvAck`.

## Observable Outcome

The manager transfer response reports the selected target as leader and post-transfer message delivery succeeds.

## Failure Diagnostics

- node-scoped stdout/stderr
- node `logs/app.log` and `logs/error.log` tails
- last observed manager slot body
- last observed channel-cluster replica response

## Run

`go test -tags=e2e,legacy_e2e ./test/legacy/e2e/message/channel_leader_transfer -count=1 -timeout 2m`

## Maintenance Rules

- If the manager endpoint, target-selection rule, metadata invariants, run command, or diagnostics change, update this file in the same change.
- The test files in this scenario directory must stay consistent with the behavior described here.
