# pkg/channelv2 Flow

## Purpose

`pkg/channelv2` is an experimental multiple-reactor channel log runtime. V0 validates append, fetch, follower apply, ACK, and HW commit behavior without replacing `pkg/channel`.

## Package Boundaries

- Root package defines public DTOs, errors, and the `Cluster` interface.
- `service/` is the synchronous facade. It validates requests, routes them to reactors, and waits on futures.
- `reactor/` owns channel-key routing, priority mailboxes, and per-channel state ownership.
- `machine/` owns pure channel state transitions and never performs blocking I/O.
- `store/` exposes the narrow persistence contract plus memory and old-store adapters.
- `transport/` exposes the v0 pull/ack replication protocol.
- `testkit/` provides a memory multi-node cluster harness.

## ApplyMeta

`ApplyMeta` applies the authoritative channel runtime view. It creates local channel state if needed, loads store state, applies leader/follower role, and seeds leader progress from local LEO. It does not elect leaders or repair metadata.

## Append

`Append` and `AppendBatch` route to the owning reactor. The machine validates leader state and emits a store append task. V0 executes the store task synchronously inside the reactor implementation for simplicity, then applies the fenced result and completes waiters when HW covers the appended range.

## Fetch

`Fetch` captures the current HW and reads committed messages up to that bound. It never returns records above HW.

## Replication

V0 uses follower pull and explicit ACK:

```text
follower Tick -> Pull(leader, nextOffset)
leader -> records + leaderHW
follower ApplyFollower -> local LEO/HW
follower -> Ack(leader, matchOffset)
leader -> AdvanceHW -> complete waiters
```

The test harness drives ticks in the background. Future production work should replace this with a reactor-owned scheduler and bounded RPC workers.

## Backpressure

Mailboxes and worker pools are bounded. Normal request admission returns `ErrBackpressured` when full. V0 still has synchronous store execution inside reactors; later phases should move those calls into worker pools before production use.

## Import Boundary

Only `store/channel_adapter.go` imports old `pkg/channel` or `pkg/channel/store`. Other channelv2 packages must depend only on channelv2 interfaces.
