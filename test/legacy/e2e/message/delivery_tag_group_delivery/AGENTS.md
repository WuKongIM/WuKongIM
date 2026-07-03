# delivery_tag_group_delivery AGENTS

This file is for agents working on `test/legacy/e2e/message/delivery_tag_group_delivery`.

## Scenario Purpose

Prove a real three-node cluster can deliver group-channel messages through the
leader-authoritative delivery tag path while subscriber membership changes are
applied through the public legacy channel management API.

## Core Steps

### Subscriber Mutation Regression

1. Start a real three-node cluster through `test/legacy/e2e/suite`.
2. Wait for every node to satisfy the ready contract.
3. Connect one sender and group recipients to real WKProto gateway listeners on
   different nodes.
4. Create the group channel with subscribers through `POST /channel`.
5. Send one group message and verify current subscribers receive and ack it.
6. Add a subscriber through `POST /channel/subscriber_add`, send again, and
   verify the new subscriber receives the next message.
7. Remove a subscriber through `POST /channel/subscriber_remove`, send again,
   and verify the removed subscriber does not receive while retained
   subscribers still do.

### Large Group Regression

1. Start a real three-node cluster through `test/legacy/e2e/suite`.
2. Create a group with 1200 subscribers through `POST /channel`, exceeding the
   default channel subscriber chunk size.
3. Connect a sparse set of online subscribers across all three nodes, including
   subscribers from the first page, later pages, and the tail of the list.
4. Send one group message and verify only online subscribers receive it while an
   online non-subscriber does not.
5. Add one online subscriber through `POST /channel/subscriber_add`, send
   again, and verify the newly added subscriber receives the next message.
6. Remove a later-page online subscriber through
   `POST /channel/subscriber_remove`, send again, and verify it does not
   receive while retained online subscribers still do.

### 100k Subscriber Stress

This opt-in scenario only runs when `WK_E2E_100K_GROUP=1`.

1. Start a real three-node cluster through `test/legacy/e2e/suite`.
2. Create a group with 100000 subscribers through `POST /channel`.
3. Connect a sparse set of online subscribers across all three nodes, including
   subscribers from the head, middle, and tail of the 100k list.
4. Send one group message and verify the sparse online subscribers receive it
   while an online non-subscriber does not.
5. Add one online subscriber through `POST /channel/subscriber_add`, send
   again, and verify the added subscriber receives the next message.
6. Remove one middle-list online subscriber through
   `POST /channel/subscriber_remove`, send again, and verify it does not
   receive while retained online subscribers still do.

## Failure Diagnostics

Use `cluster.DumpDiagnostics()` on failures so node config, ready observations,
stdout, stderr, and app logs are included.

## Run

`go test -tags=e2e,legacy_e2e ./test/legacy/e2e/message/delivery_tag_group_delivery -count=1`

Opt-in 100k subscriber stress run:

`WK_E2E_100K_GROUP=1 go test -timeout=10m -tags=e2e,legacy_e2e ./test/legacy/e2e/message/delivery_tag_group_delivery -run TestDeliveryTagHundredKGroupSubscriberStress -count=1 -v`
