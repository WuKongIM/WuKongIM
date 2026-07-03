# request_scoped_subscriber_delivery AGENTS

This file is for agents working on `test/legacy/e2e/message/request_scoped_subscriber_delivery`.

## Purpose

Prove a real three-node cluster can deliver `/message/send` request-scoped `subscribers` messages to exactly the requested online subscribers across nodes.

## Cluster Shape

One real three-node cluster started through `test/legacy/e2e/suite`. The HTTP API request is sent to node 1 while subscribers are connected through WKProto gateways on other nodes.

## External Steps

1. Start a three-node cluster through `test/legacy/e2e/suite`.
2. Wait for every node to satisfy the ready contract.
3. Connect two requested subscribers and one outsider through real WKProto gateway listeners on different nodes.
4. Send a durable request-scoped message through `POST /message/send` with `subscribers` and `header.sync_once=1`.
5. Verify each requested subscriber receives one `Recv`, the outsider receives none, and the client channel view does not leak the internal `____cmd` suffix.
6. Send a non-durable request-scoped message with `header.sync_once=1` and `header.no_persist=1`.
7. Verify requested subscribers receive the non-durable `Recv` with `SyncOnce`, `NoPersist`, and `MessageSeq=0`; the outsider receives none.

## Observable Outcome

The HTTP API returns successful responses, requested subscribers receive matching payloads and message identifiers, duplicate subscribers do not cause duplicate receives, and non-subscribers do not receive request-scoped messages.

## Failure Diagnostics

Use `cluster.DumpDiagnostics()` on failures so node config, ready observations, stdout, stderr, and app logs are included.

## Run

`go test -tags=e2e,legacy_e2e ./test/legacy/e2e/message/request_scoped_subscriber_delivery -count=1`

## Maintenance Rules

- Keep assertions black-box through public HTTP and WKProto entrypoints.
- Do not import `internal/app` or read stores directly.
- If scenario steps, run command, or diagnostics change, update this file and the parent e2e catalogs in the same change.
