# Delivery Ack Observability Design

## Background

`internalv2` online delivery already has the main runtime pieces for committed
message fanout, owner-local pending recvack tracking, and retry scheduling.
The current cross-node e2e scenario proves that two users connected to
different nodes can exchange online person-channel messages and that the
recipient can send a `RecvAckPacket`.

The missing closure is observability. `Manager.Recvack` clears `AckTracker`
state, but the public delivery view does not reliably reflect that state
change. The current top ack gauge is refreshed by `localOwnerPusher` during
bind, expiry, and write cleanup, not by the runtime ack state transitions
themselves.

This design makes pending recvack state changes explicit runtime events. Top,
Prometheus metrics, and black-box e2e assertions then observe the same fact
source instead of relying on entry- or app-layer refresh patches.

## Goals

- Treat owner-local pending recvack state changes as first-class runtime
  observations.
- Update `/top/v1/snapshot?view=delivery` and Prometheus delivery ack gauges
  from the same ack state event.
- Verify `RECV -> RECVACK -> pending ack cleared` through real `cmd/wukongimv2`
  processes and public WKProto / HTTP APIs.
- Keep retry scheduler observability covered without adding fragile black-box
  fault injection in this step.
- Preserve the current delivery semantics: successful durable append is still
  independent from online delivery, and unknown recvacks remain ignored.
- Keep hot-path cost predictable for many online sessions and large channels.

## Non-Goals

- Add durable delivery replay.
- Add offline delivery state.
- Change `SENDACK` timing or make it depend on online delivery.
- Add old-data compatibility paths.
- Add delivery-specific API endpoints for tests.
- Build a black-box retry fault-injection framework in this step.

## Current Flow

The existing runtime path remains the business flow:

```text
gateway SEND
  -> message append succeeds
  -> MessageCommitted
  -> delivery.Manager.SubmitCommitted
  -> FanoutWorker resolves online recipient routes
  -> local owner writes RecvPacket
  -> Manager.BindPendingAck records pending recvack
  -> recipient sends RecvAckPacket
  -> gateway maps it to delivery.RecvackCommand
  -> Manager.Recvack clears AckTracker state
```

The change is not to this flow. The change is that each pending ack mutation
emits a bounded observation after the mutation has completed.

## Runtime Ack Event

Add a small observer interface in `internalv2/runtime/delivery`:

```go
type AckObserver interface {
    ObserveAck(AckEvent)
}

type AckEvent struct {
    Action       string
    Result       string
    Changed      int
    PendingCount int
}
```

Labels must stay low-cardinality. `Action` should be one of:

- `bind`
- `ack`
- `session_closed`
- `expire`

`Result` should be one of:

- `ok`
- `miss`
- `rejected`
- `noop`

`Changed` is the number of pending ack rows added or removed by the mutation.
`PendingCount` is the owner-local total after the mutation.

No UID, channel ID, session ID, device ID, message ID, or client message number
is included in the event. Those fields are useful for logs in exceptional paths
but are too high-cardinality for the shared observer contract.

## Manager Ownership

`ManagerOptions` gains an optional `AckObserver`. `Manager` emits ack events
from these methods:

- `BindPendingAck`
- `Recvack`
- `SessionClosed`
- `ExpirePendingAcks`

The event must be emitted by `Manager`, not by gateway, usecase, app adapters,
or `localOwnerPusher`. This keeps `AckTracker` as the state owner and `Manager`
as the runtime facade that reports state changes.

`Recvack` continues to ignore unknown acks. Unknown acks emit:

```text
Action=ack Result=miss Changed=0
```

This preserves behavior while making unexpected client feedback visible.

## AckTracker Counting

The current `PendingCount()` scans every shard. That is acceptable for tests but
not a good hot-path dependency if every recvack event needs the count.

`AckTracker` should maintain an internal atomic pending count:

- A new bind increments the count by one.
- A bind that overwrites the same `(uid, session_id, message_id)` does not
  increment the count.
- A successful ack decrements the count by one.
- `SessionClosed` decrements by the number of removed pending acks.
- `Expire` decrements by the number of removed pending acks.
- `PendingCount()` reads the atomic count.

Shard maps remain the source of truth for membership. The atomic counter is a
derived total that is updated while the shard mutation is known.

Observer callbacks must not run while holding an `AckTracker` shard lock. The
runtime should complete the mutation, compute `Changed` and `PendingCount`,
release locks, and only then call observers.

## App Observers

`deliveryMetricsObserver` and `topDeliveryObserver` should implement
`runtime/delivery.AckObserver`.

The mapping is:

```text
AckEvent.PendingCount
  -> topCollector.SetDeliveryAckBindings
  -> metrics.Delivery.SetAckBindings
```

Top and Prometheus should both receive the same pending count. The app layer
does not infer counts by calling `PendingAckCount()` from unrelated paths.

`multiDeliveryObserver` should fan out `AckObserver` events the same way it
currently fans out retry and manager observations.

## localOwnerPusher Responsibility

`localOwnerPusher` should stop owning top ack gauge refreshes. It remains
responsible for:

- validating owner-local routes;
- building `RecvPacket`;
- calling `Manager.BindPendingAck` before writing;
- cleaning the pending ack through `Manager.Recvack` when a write fails after
  binding;
- classifying write failures as dropped or retryable.

Because `Manager` emits ack events for bind, cleanup, and expiry, the pusher
does not need a top collector reference for ack binding gauges.

## Retry Scope

This step keeps retry work focused on observability stability, not end-to-end
fault injection.

The implementation should preserve the existing `RetryScheduler` behavior and
tests. Add or adjust unit tests only where needed to prove:

- retry queue depth observations still update top;
- retry queue depth observations still update Prometheus metrics;
- ack observer additions do not break the existing retry observer composition.

A later delivery-fault-recovery spec can design controlled black-box failures
for remote fanout and owner push retries. That should be separate because it
needs stable fault injection hooks and timing controls.

## E2E Acceptance

Extend the delivery-specific black-box coverage, preferably the existing
`test/e2ev2/message/cross_node_delivery` scenario.

The scenario should:

1. Start a static three-node cluster with `WK_DELIVERY_ENABLE=true`.
2. Explicitly enable top on every node with `WK_TOP_API_ENABLE=true`.
3. Use a short top collection interval such as `100ms` and a valid history
   window to keep the test fast.
4. Connect user A to node 1 and user B to node 2.
5. Send A -> B and assert B receives `RecvPacket`.
6. Wait on B's owner node for delivery `ack_bindings >= 1` through
   `/top/v1/snapshot?view=delivery`.
7. Send B's `RecvAckPacket`.
8. Wait on B's owner node for delivery `ack_bindings == 0`.
9. Repeat the same validation for B -> A.

The assertions must remain black-box:

- WKProto for SEND, SENDACK, RECV, and RECVACK.
- Public HTTP for top snapshots.
- No direct access to `app.deliveryManager` or runtime internals.

If the top collector is warming up, the helper should retry until the scenario
timeout. The helper should include node diagnostics in failure output.

## Test Plan

Unit and focused package tests:

- `internalv2/runtime/delivery`: ack observer emits correct action, result,
  changed count, and pending count for bind, ack, unknown ack, session close,
  expiry, and bind rejection.
- `internalv2/runtime/delivery`: `AckTracker.PendingCount()` remains correct
  across overwrite, ack, close, and expiry.
- `internalv2/app`: top delivery observer maps `AckEvent.PendingCount` to
  `ack_bindings`.
- `internalv2/app`: metrics delivery observer maps `AckEvent.PendingCount` to
  `wukongim_delivery_ack_bindings`.
- `internalv2/app`: combined delivery observer fans out ack events when top and
  metrics observers are both present.

Black-box e2e:

```bash
GOWORK=off go test -tags=e2e ./test/e2ev2/message/cross_node_delivery -count=1 -timeout 2m
```

Targeted regression tests:

```bash
GOWORK=off go test ./internalv2/runtime/delivery ./internalv2/app ./test/e2ev2/suite
```

Broader verification before completion:

```bash
GOWORK=off go test ./internal/... ./internalv2/... ./pkg/...
```

## Performance Notes

- Ack events are emitted only on owner-local pending ack mutations, not per
  subscriber scan item.
- Events use bounded labels and do not include UID, session, channel, or
  message identifiers.
- `PendingCount()` should be O(1) through an atomic derived count.
- Observer calls must happen outside shard locks.
- The top collector receives a gauge update, not a new high-frequency sample
  loop.

## Risks

- Atomic pending count can drift if overwrite, close, or expiry paths do not
  distinguish actual additions/removals. Tests must cover those paths.
- E2E top assertions can be flaky if they depend on default one-second sampling.
  The scenario should explicitly configure a short collect interval.
- If observer fanout is typed incorrectly, top may update while metrics do not,
  or vice versa. App tests should cover combined observer fanout.
- Unknown recvacks should remain behaviorally ignored. Observability must not
  turn a miss into a protocol error.

## Acceptance Criteria

- `Recvack` clears owner-local pending ack state and emits an ack event with
  the updated pending count.
- `/top/v1/snapshot?view=delivery` shows `ack_bindings` rise after accepted
  online delivery and return to zero after `RecvAckPacket`.
- `wukongim_delivery_ack_bindings` is updated from the same ack event.
- Existing delivery retry behavior and retry queue depth observations keep
  passing tests.
- The implementation does not add gateway/app-layer refresh patches or
  delivery-specific test-only endpoints.
