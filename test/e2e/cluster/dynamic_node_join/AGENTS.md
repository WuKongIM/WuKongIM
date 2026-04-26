# dynamic_node_join AGENTS

This file is for agents working on `test/e2e/cluster/dynamic_node_join`.

## Purpose

Prove a new data node can join an already running three-node cluster with `WK_CLUSTER_SEEDS`, `WK_CLUSTER_ADVERTISE_ADDR`, and `WK_CLUSTER_JOIN_TOKEN`, without `WK_CLUSTER_NODES` in the joining node config.

## Cluster Shape

One real three-node static cluster that accepts a fourth seed-join data node. Controller voters and existing slot voters remain the original three nodes.

## External Steps

1. Start a real three-node cluster with a shared join token.
2. Wait for every static node to satisfy the ready contract.
3. Start node 4 with seed-join config and no static `WK_CLUSTER_NODES` list.
4. Wait for node 4 to satisfy the ready contract.
5. Poll `/manager/nodes` from an existing node until node 4 is visible as alive with its advertised cluster address and no controller role.
6. Poll `/manager/nodes` from node 4 until it can observe node 1 as alive.
7. Connect one WKProto client through node 1 and one through node 4.
8. Send one person-channel message in each direction and observe `Send`, `SendAck`, `Recv`, and `RecvAck`.

## Observable Outcome

Node 4 appears in manager node membership with the advertised address and `controller.role=none`, and both cross-node message closures complete through public WKProto gateways.

## Failure Diagnostics

- generated configs, stdout, stderr, `logs/app.log`, and `logs/error.log`
- last `/readyz` observations stored by the suite
- last `/manager/nodes` body observed by membership polling
- local `/manager/connections` observations for both connected users

## Run

`go test -tags=e2e ./test/e2e/cluster/dynamic_node_join -count=1`

## Maintenance Rules

- If seed-join config keys, manager node observations, message proof steps, run command, or diagnostics change, update this file in the same change.
- If this scenario adds local helpers, keep them in this directory and keep them consistent with the behavior described here.
