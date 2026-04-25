# idle_cross_node_delivery_timeout AGENTS

This file is for agents working on `test/e2e/message/idle_cross_node_delivery_timeout`.

## Purpose

Reproduce the current three-node cross-node person-message delivery failure where
one bootstrap message succeeds, both users stay connected on different nodes,
and the next cross-node message after the channel runtime leader lease expires
never reaches the recipient.

## Cluster Shape

One real three-node cluster where controller, slot, and channel all keep their
default three-replica behavior.

## External Steps

1. Start a real three-node cluster through `test/e2e/suite`.
2. Wait for every node to satisfy the ready contract.
3. Resolve slot `1` topology through the manager API.
4. Connect `safe1` and `safe2` to two different follower nodes and verify both
   local connections through `/manager/connections`.
5. Send one successful person-channel message from `safe1` to `safe2` to
   bootstrap runtime metadata.
6. Poll `/manager/channel-runtime-meta/1/<channel>` while also checking both
   `/manager/connections` views until the leader lease is expired but both
   clients are still online.
7. Send another person-channel message across the same two nodes.
8. Observe whether `SendAck`, `Recv`, and `RecvAck` still complete.

## Observable Outcome

The scenario expects the post-expiry cross-node message to still deliver, so the
current bug reproduces as a failing e2e when the sender receives `SendAck` but
the recipient never receives `Recv` within the client timeout window.

## Failure Diagnostics

- last observed `/readyz` output
- last observed slot topology body
- last observed channel runtime metadata body
- generated configs
- node stdout/stderr
- node-scoped logs under `logs/`

## Run

`go test -tags=e2e ./test/e2e/message/idle_cross_node_delivery_timeout -count=1`

For log-driven debugging, this scenario writes artifacts under a stable temp
root:

- workspace root: `/tmp` equivalent + `wukongim-e2e-debug/idle_cross_node_delivery_timeout/artifacts`
- log root: `/tmp` equivalent + `wukongim-e2e-debug/idle_cross_node_delivery_timeout/logs`

Each run still gets its own test-scoped workspace subdirectory under those
roots.

## Maintenance Rules

- If the idle-window trigger, channel runtime metadata polling, node selection,
  run command, or diagnosis entrypoints change, update this file in the same
  change.
- If this scenario adds local helpers, keep them in this directory and keep
  them consistent with the behavior described here.
