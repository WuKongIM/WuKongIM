# MQTT connection delivery coordinator

Status: connection coordination implemented and tested; product access is not enabled.

The approved [IM design](mqtt-im-access.md) requires automatic bounded discovery,
accounting and delivery, not caller-supplied cursor keys. The existing public
usecase/runtime and native app integration test seams remain in scope.

## Contract

One ConnectionDelivery owns one Sender stream opened before any new exchange.
It implements the body-free runtime DeliveryTask contract. Each nonconcurrent,
bounded turn first attempts old-exchange recovery/private QoS-0 completion. Only
when that is idle does it select one active subscription and one source cursor,
account one protected page and attempt one Sender turn. No new table/RPC is needed.

Subscription paging advances once per selection, including a source-specific
failure. Each subscription with more source cursors retains one continuation,
bounded by the configured subscription quota (default 128, maximum 1024).
Completed source scans discard their hint. A complete subscription rotation
prunes hints for removed/replaced subscriptions. Hints are never authority or
durable progress; discovery checks current exact ownership, and Accounting and
Sender recheck current child/source/receive authority before their effects.

Busy/idle sources yield to other subscriptions. Progress anywhere in a completed
rotation requests another pass; an entirely idle pass yields to bounded polling.
Incomplete source scans request another pass. Malformed pages cannot dispatch
work or install their returned cursors. New subscriptions and cursors are found
on subsequent rotations; wakes only accelerate this process.

Accounting runs inside an admitted exact-owner scope. Its completion must name
that Owner. Definitive revocation ends the exact Session after the scope releases;
quota accounting already ends its durable row but still requires physical
isolation. Unavailable evidence never becomes revocation. Failed end cleanup
retains a continuation even after local admission is fenced. Ordinary connection
closure is not a durable lifetime-end decision.

## Failure inventory (before implementation)

1. An unaccounted source never sends without an externally supplied cursor key.
2. Reconnect admits new exchanges before recovering old ones, or retransmits a
   new same-connection exchange with DUP after discovery resets the stream.
3. A many-source subscription starves a later subscription; unavailable/idle
   sources pin iteration; idle last subscription suppresses earlier progress.
4. Missing/removed subscriptions or tied source identities skip or resurrect
   another generation; unlimited churn leaks hint capacity.
5. Malformed/cross-owner pages, stalled cursors, extra data, cancellation or
   dependency panic dispatch unproved work or mutate scheduling state unsafely.
6. Revocation discovered during accounting only retries forever; quota end
   mistakes durable state for socket isolation; cleanup waits for its own scope.
7. An uncertain accounting commit loses a quota-end decision; takeover causes
   old work to account/end a successor; failed cleanup is forgotten by the task.
8. Window pressure prevents other sources (including eligible QoS 0) from being
   visited; task reentry starts simultaneous turns.

Validation combines native metadata/usecase tests with the existing real
three-node Sender/Slot scenario. Inbox *future source admission*, offline
maintenance, listener hooks and process acceptance remain separate required
work; paging existing cursors cannot establish them.

## Validation evidence

- Source revision `b57528137fc816fc2dcb18abb350ccfd1c112101`; applicable AGENTS/FLOW
  digests frozen before edits in `/tmp/mqtt-delivery-coordinator-source.json`.
- Initial RED: missing coordinator, `/tmp/mqtt-delivery-coordinator-red.log`.
  Terminal-path RED: accounting revocation did not end and quota cleanup emitted
  transport-only feedback, `/tmp/mqtt-delivery-coordinator-ending-red.log`.
  The coordinator now retains cleanup and checks current durable state before
  recovering/sending, including a quota commit whose reply was lost. Sender
  preserves the exact terminal reason through cleanup retries.
- App composition RED: missing constructor,
  `/tmp/mqtt-delivery-coordinator-app-red.log`. A separate constructor RED
  rejected accepting zero-value dependencies,
  `/tmp/mqtt-delivery-coordinator-options-red.log`.
- Whole usecase race suite passed (99.995s),
  `/tmp/mqtt-delivery-coordinator-suite-race.log`:
  `GOWORK=off go test -race -p 2 ./internal/usecase/mqttsession -count=1`.
  Final focused race validation passed (7.089s) and additionally covers the constructor guard and
  original QoS 0 at a full QoS 1 window; its log is
  `/tmp/mqtt-delivery-coordinator-final-race.log`.
- Other coordinator cases cover recovery order/DUP, incomplete inbox-source
  bindings yielding to a healthy group, tied source identities with distinct
  generations, malformed pages, cancellation/panic, authority unavailability,
  reentry, takeover and failed exact-owner cleanup after fencing.
- Three-node / 256-hash-Slot race integration passed (28.094s),
  `/tmp/mqtt-delivery-coordinator-cluster-race.log`:
  `GOWORK=off go test -race -tags=integration -p 2 ./internal/app
  -run '^TestMQTTGroupSourcePreparationThreeNodeRecovery$' -count=1 -timeout=4m -v`.
  The same runtime now accepts ConnectionDelivery directly; the prepared-key task
  adapter and manual accounting were removed. Real Session-Slot pages discover
  cursors, protected original content is accounted/sent, and native ACK gaps,
  retirement, rejoin and independent replica reopen remain verified. The sink is
  controlled; this is not product socket or process-level acceptance.
- FLOW render/check passed: 86 compliant, zero invalid, nine existing length
  warnings. `git diff --check` passed. No storage/RPC/configuration format changed.
