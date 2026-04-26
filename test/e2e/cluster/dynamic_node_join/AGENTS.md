# dynamic_node_join AGENTS

This file is for agents working on `test/e2e/cluster/dynamic_node_join`.

## Purpose

Prove a new data node can join an already running three-node cluster with `WK_CLUSTER_SEEDS`, `WK_CLUSTER_ADVERTISE_ADDR`, and `WK_CLUSTER_JOIN_TOKEN`, without `WK_CLUSTER_NODES` in the joining node config. Also prove the manager onboarding flow can explicitly allocate Slot resources to that newly joined data node.

## Cluster Shape

One real three-node static cluster that accepts a fourth seed-join data node. Controller voters and existing slot voters remain the original three nodes.

## External Steps

### Dynamic Join Message Closure

1. Start a real three-node cluster with a shared join token.
2. Wait for every static node to satisfy the ready contract.
3. Start node 4 with seed-join config and no static `WK_CLUSTER_NODES` list.
4. Wait for node 4 to satisfy the ready contract.
5. Poll `/manager/nodes` from an existing node until node 4 is visible as alive with its advertised cluster address and no controller role.
6. Poll `/manager/nodes` from node 4 until it can observe node 1 as alive.
7. Connect one WKProto client through node 1 and one through node 4.
8. Send one person-channel message in each direction and observe `Send`, `SendAck`, `Recv`, and `RecvAck`.

### Onboarding Resource Allocation

1. Start a real three-node cluster with a shared join token.
2. Wait for every static node to satisfy the ready contract and resolve slot `1` runtime topology.
3. Start node 4 with seed-join config and no static `WK_CLUSTER_NODES` list.
4. Wait for node 4 to appear in `/manager/nodes` as alive with zero assigned Slots.
5. Fetch `/manager/node-onboarding/candidates` and verify node 4 appears.
6. Create a plan with `POST /manager/node-onboarding/plan`.
7. Start the plan with `POST /manager/node-onboarding/jobs/<job_id>/start`.
8. Poll `/manager/node-onboarding/jobs/<job_id>` until it completes.
9. Poll `/manager/nodes` until node 4 has at least one assigned Slot.

## Observable Outcome

Node 4 appears in manager node membership with the advertised address and `controller.role=none`, both cross-node message closures complete through public WKProto gateways, and the onboarding job completes with node 4 receiving Slot resources.

## Failure Diagnostics

- generated configs, stdout, stderr, `logs/app.log`, and `logs/error.log`
- last `/readyz` observations stored by the suite
- last `/manager/nodes` body observed by membership or allocation polling
- last `/manager/node-onboarding/**` response body observed by onboarding polling
- local `/manager/connections` observations for both connected users

## Run

`go test -tags=e2e ./test/e2e/cluster/dynamic_node_join -count=1`

## Maintenance Rules

- If seed-join config keys, manager node observations, message proof steps, onboarding proof steps, run command, or diagnostics change, update this file in the same change.
- If this scenario adds local helpers, keep them in this directory and keep them consistent with the behavior described here.
