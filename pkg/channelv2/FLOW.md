# pkg/channelv2 Flow

## Purpose

`pkg/channelv2` is an experimental multiple-reactor channel log runtime. Phase 2 validates append, fetch, follower replication, ACK, and HW commit behavior through reactor-owned state and typed bounded worker pools without replacing `pkg/channel`.

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

`Append` and `AppendBatch` route to the owning reactor. The reactor admits leader-ready requests into a bounded per-channel append queue, batches them by record count, bytes, or max wait, and proposes one machine append batch with a reactor-owned fence op id. Store appends run on the bounded store-append worker pool; fenced completions are applied back in the reactor and may complete multiple client waiters when local or quorum commit criteria are met. Worker-pool backpressure rolls the proposed batch back to the queue and retries on a later tick after the append retry backoff, leaving accepted client futures pending. Caller cancellation after admission is cooperative: the owning reactor tracks cancellable append contexts and sweeps them on event turns and flush attempts. A canceled append is removed from the queue, inflight waiter state, or post-store quorum waiter state, and its future completes with the context error. Already-started durable store writes are allowed to finish; their later completions use the original batch record counts and do not complete canceled client waiters as success.

## Fetch

`Fetch` captures the current HW and submits the committed store read to the bounded store-read worker pool. The reactor keeps only a fenced waiter, so high-priority metadata changes can proceed while storage is blocked. A metadata fence change fails pending fetch waiters with `ErrStaleMeta`, and stale worker completions are ignored without leaking the waiter. Reactor/group close fails pending fetch waiters with `ErrClosed` and cancels store-read worker contexts. Fetch never returns records above HW.

## Replication

Replication is owned by each channel's reactor runtime. `service.Tick` only calls `group.Tick(ctx)`, which submits low-priority tick events; the reactor decides whether an active local follower should pull, apply, or ack. Follower pull offsets are based on local `LEO + 1`, not committed HW or a service-side fetch.

```text
follower reactor Tick -> TaskRPCPull(leader, LEO+1)
leader EventPull -> TaskStoreReadLog -> PullResponse(records, leaderHW, leaderLEO)
follower TaskStoreApply -> local LEO/HW
follower TaskRPCAck(matchOffset)
leader EventAck -> AdvanceHW -> complete quorum waiters
```

A follower keeps at most one pull RPC in flight, one pending pull response waiting for store apply, and one ACK RPC in flight. Pending ACKs are retried before new pulls, and ACK retries reuse the stored match offset. Leader pull handling is asynchronous through the store-read worker pool so blocked log reads do not block high-priority metadata events.

## Backpressure

Mailboxes, append queues, and worker pools are bounded. Normal request admission returns `ErrBackpressured` when full. Append queue limits reject new requests before they become waiters; store append worker-pool backpressure keeps already accepted requests pending for retry. Fetch and leader pull log reads use the store-read worker pool. Follower pull, apply, and ACK use typed bounded worker tasks; store-apply backpressure keeps a single pending pull response and retries on later ticks rather than issuing duplicate pulls.

## Import Boundary

Only `store/channel_adapter.go` imports old `pkg/channel` or `pkg/channel/store`. Other channelv2 packages must depend only on channelv2 interfaces.
