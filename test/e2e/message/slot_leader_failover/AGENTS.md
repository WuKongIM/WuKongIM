# slot_leader_failover AGENTS

This file is for agents working on `test/e2e/message/slot_leader_failover`.

## Purpose

Prove cross-node delivery still works after the current slot leader stops and a new leader takes over.

## Cluster Shape

One three-node cluster where both clients connect to follower nodes before the current slot leader is stopped.

## External Steps

1. Start a real three-node cluster through `test/e2e/suite`.
2. Wait for readiness and resolve slot `1` topology through the manager API.
3. Connect `u1` and `u2` to follower nodes and verify both connections through `/manager/connections`.
4. Stop the current slot leader process.
5. Wait for `/manager/slots/1` to report a new leader with quorum.
6. Send another person-channel message and observe `SendAck`, `Recv`, and `RecvAck`.

## Observable Outcome

After leader failover, both clients remain externally observable and the cross-node message closure still succeeds.

## Failure Diagnostics

- node-scoped stdout/stderr
- node `logs/app.log` and `logs/error.log` tails
- last observed manager slot body
- manager connection observations before and after failover

## Run

`go test -tags=e2e ./test/e2e/message/slot_leader_failover -count=1`

## Maintenance Rules

- If the failover trigger, health checks, connection verification flow, run
  command, or diagnosis entrypoints change, update this file in the same
  change.
- The test files in this scenario directory must stay consistent with the
  behavior described here.
