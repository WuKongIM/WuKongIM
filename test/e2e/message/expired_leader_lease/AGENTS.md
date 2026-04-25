# expired_leader_lease AGENTS

This file is for agents working on `test/e2e/message/expired_leader_lease`.

## Purpose

Reproduce the current three-node person-channel delivery failure that appears
after channel runtime metadata is left with an expired leader lease.

## Cluster Shape

One real three-node cluster with slot topology discovered through `/manager/slots/1`.

## External Steps

1. Start a three-node cluster through `test/e2e/suite`.
2. Wait for every node to satisfy the ready contract.
3. Resolve slot `1` topology through the manager API.
4. Connect `ooo1` and `ooo2` to two different follower nodes.
5. Send one successful person-channel message to bootstrap runtime metadata.
6. Transfer slot `1` leadership to the recipient-side follower through
   `/manager/slots/1/leader/transfer` so the original channel leader and the
   current slot leader diverge.
7. Poll `/manager/channel-runtime-meta/1/<channel>` on the new slot leader
   until the old channel leader lease is almost expired.
8. Stop the old channel leader process just before the observed lease crosses
   the wall clock.
9. Keep polling `/manager/channel-runtime-meta/1/<channel>` until it still
   points at the stopped leader with an expired `lease_until_ms`.
10. Send another cross-node message after the lease is expired and the old
    leader is already down.
11. Observe whether `SendAck`, `Recv`, and `RecvAck` still complete.

## Observable Outcome

The scenario records an expired `lease_until_ms` that still points at the
stopped previous leader, then expects the second cross-node send/receive
closure to still succeed so the current bug reproduces as a failing e2e test.

## Failure Diagnostics

- last observed `/readyz` output
- last observed slot topology body
- last observed channel runtime metadata body
- generated configs
- node stdout/stderr
- node-scoped logs under `logs/`

## Run

`go test -tags=e2e ./test/e2e/message/expired_leader_lease -count=1`

## Maintenance Rules

- If the follower-selection logic, slot-leader transfer step, channel runtime
  metadata polling, run command, or diagnosis entrypoints change, update this
  file in the same change.
- If this scenario adds local helpers, keep them in this directory and keep
  them consistent with the behavior described here.
