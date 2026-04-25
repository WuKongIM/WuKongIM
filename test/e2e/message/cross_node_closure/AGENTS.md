# cross_node_closure AGENTS

This file is for agents working on `test/e2e/message/cross_node_closure`.

## Purpose

Prove a real three-node cluster can deliver ten person-channel messages in each direction when both users connect to different follower nodes.

## Cluster Shape

One real three-node cluster with slot topology discovered through `/manager/slots/1`.

## External Steps

1. Start a three-node cluster through `test/e2e/suite`.
2. Wait for every node to satisfy the ready contract.
3. Resolve slot `1` topology through the manager API.
4. Connect `u1` and `u2` to two different follower nodes.
5. Send ten person-channel messages from `u1 -> u2`.
6. Send ten person-channel messages from `u2 -> u1`.
7. Observe `Send`, `SendAck`, `Recv`, and `RecvAck`.

## Observable Outcome

Each user receives ten successful `SendAck` packets for its outbound sends, and each peer receives the same ten payloads and message identifiers across nodes.

## Failure Diagnostics

- last observed `/readyz` output
- last observed slot topology body
- generated configs
- node stdout/stderr
- node-scoped logs under `logs/`

## Run

`go test -tags=e2e ./test/e2e/message/cross_node_closure -count=1`

## Maintenance Rules

- If the follower-selection logic, topology discovery flow, run command, or
  diagnosis entrypoints change, update this file in the same change.
- If this scenario adds local helpers, keep them in this directory and keep
  them consistent with the behavior described here.
