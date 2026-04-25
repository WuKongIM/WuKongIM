# single_node_send_message AGENTS

This file is for agents working on `test/e2e/message/single_node_send_message`.

## Purpose

Prove one fresh single-node cluster can complete a real WKProto `Send -> SendAck -> Recv -> RecvAck` closure.

## Cluster Shape

One single-node cluster started from the real `cmd/wukongim` binary.

## External Steps

1. Start one real node through `test/e2e/suite`.
2. Connect `u1` and `u2` through the public WKProto gateway.
3. Send one person-channel message from `u1` to `u2`.
4. Observe `SendAck`, `Recv`, and `RecvAck`.

## Observable Outcome

The sender receives a successful `SendAck`, and the recipient receives the same payload and message identifiers.

## Failure Diagnostics

- generated node config
- node stdout/stderr
- node-scoped logs under `logs/`

## Run

`go test -tags=e2e ./test/e2e/message/single_node_send_message -count=1`

## Maintenance Rules

- If the scenario steps, key assertions, run command, or diagnosis entrypoints
  change, update this file in the same change.
- If extra test files are added under this scenario directory, keep them
  aligned with the behavior described here.
