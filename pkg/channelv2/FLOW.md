# pkg/channelv2 Flow

## Purpose

`pkg/channelv2` is an experimental multiple-reactor channel log runtime. Phase 2 validates append, fetch, follower replication, ACK, HW commit behavior, and benchmark-visible metrics through multiple reactors plus typed bounded worker pools without replacing `pkg/channel`.

## Package Boundaries

- Root package defines public DTOs, errors, and the `Cluster` interface.
- `service/` is the synchronous facade. It validates requests, routes them to reactors, and waits on futures.
- `reactor/` owns channel-key routing, priority mailboxes, and per-channel state ownership.
- `machine/` owns pure channel state transitions and never performs blocking I/O.
- `store/` exposes the narrow persistence contract plus memory and old-store adapters.
- `transport/` exposes the v0 pull/ack replication protocol.
- `testkit/` provides a memory multi-node cluster harness.

## ApplyMeta

`ApplyMeta` applies the authoritative channel runtime view. It creates local channel state if needed, loads store state, applies leader/follower role, and seeds leader progress from local LEO. It remains the explicit metadata push/refresh path, but Append no longer requires callers to invoke it first when the service is configured with a metadata resolver. It does not elect leaders or repair metadata.

## Append

`Append` and `AppendBatch` route to the owning reactor. Before submitting the append event, the service verifies that the reactor already owns channel state. If not, and a `MetaResolver` is configured, the service resolves authoritative metadata, applies it through the normal `ApplyMeta` path to create and seed the reactor state, then submits the append. Without a resolver, an unloaded channel keeps the existing `ErrChannelNotFound` behavior. The reactor admits leader-ready requests into a bounded per-channel append queue, batches them by record count, bytes, or max wait, and proposes one machine append batch with a reactor-owned fence op id. Store appends run on the bounded store-append worker pool; fenced completions return as high-priority worker results, are applied back in the reactor, and may complete multiple client waiters when local or quorum commit criteria are met. Observer hooks record submitted batch records/bytes/wait and append completion latency for accepted requests, and the async append benchmarks report batch and allocation metrics. Worker-pool backpressure rolls the proposed batch back to the queue and retries on a later tick after the append retry backoff, leaving accepted client futures pending. Caller cancellation after admission is cooperative: the owning reactor tracks cancellable append contexts and sweeps them on event turns and flush attempts. A canceled append is removed from the queue, inflight waiter state, or post-store quorum waiter state, and its future completes with the context error. Already-started durable store writes are allowed to finish; their later completions use the original batch record counts and do not complete canceled client waiters as success.

## Fetch

`Fetch` captures the current HW and submits a `StoreReadCommitted` worker task to the bounded store-read worker pool. The reactor keeps only a fenced waiter, so high-priority metadata changes can proceed while storage is blocked. Worker result observer hooks record the read completion kind, error, and worker duration when the group routes the result. A metadata fence change fails pending fetch waiters with `ErrStaleMeta`, and stale worker completions are ignored without leaking the waiter. Reactor/group close fails pending fetch waiters with `ErrClosed` and cancels store-read worker contexts. Fetch never returns records above HW.

## Replication

Replication is owned by each channel's reactor runtime. `service.Tick` only calls `group.Tick(ctx)`, which submits low-priority tick events; the reactor decides whether an active local follower should pull, apply, or ack. Follower pull offsets are based on local `LEO + 1`, not committed HW or a service-side fetch. RPC pull, store apply, and ACK completions flow through worker result observer hooks, while three-node async benchmarks exercise the worker-pool path with local transport. Leader append completion also sends a best-effort notify RPC to non-local replicas. Notify carries no records or commit state; it only marks the follower dirty so the follower-owned reactor can submit an immediate pull. Short-poll idle retry remains the fallback when notify is dropped, backpressured, or races metadata changes.

```text
leader append stored -> TaskRPCNotify(followers)
follower EventNotify/Tick -> TaskRPCPull(leader, LEO+1)
leader EventPull -> TaskStoreReadLog -> PullResponse(records, leaderHW, leaderLEO)
follower TaskStoreApply -> local LEO/HW
follower TaskRPCAck(matchOffset)
leader EventAck -> AdvanceHW -> complete quorum waiters
```

A follower keeps at most one pull RPC in flight, exactly one pending pull response waiting for store apply, and one ACK RPC in flight. Pull, apply, and ACK completions are fenced by generation, epoch, leader epoch, and op id; stale completions are ignored before they clear or advance runtime state. Apply errors and store-apply backpressure retain the pending pull response for retry. Pending ACKs are retried before new pulls, and ACK errors or ACK backpressure retain the exact stored match offset for retry. Leader pull handling is asynchronous through the store-read worker pool so blocked log reads do not block high-priority metadata events.
Metadata fence changes reset follower pull/apply/ACK inflight and pending state before active followers are marked dirty under the new epoch. Leader-side pull waiters complete with `ErrStaleMeta` on metadata fence changes, the caller context error on cancellation, or `ErrClosed` on close; late store completions are ignored after the waiter is removed. Leader ACK handling ignores stale or regressive matches, so they do not advance HW or complete quorum waiters.

## Backpressure

Mailboxes, append queues, and worker pools are bounded, and observer hooks sample reactor mailbox depths and worker queue depths after cheap submit/drain points. Normal request admission returns `ErrBackpressured` when full. Append queue limits reject new requests before they become waiters; store append worker-pool backpressure keeps already accepted requests pending for retry. Fetch and leader pull log reads use the store-read worker pool, with fetch fail-fast behavior when that pool rejects the task. Follower pull, apply, ACK, and notify use typed bounded worker tasks; store-apply backpressure keeps the single pending pull response for retry, and ACK backpressure keeps the exact match offset for retry rather than issuing duplicate pulls or advancing the offset. Notify failures are observed but do not fail accepted appends because follower short-poll can still catch up.
Low-priority tick mailbox events are droppable/coalesced; if a direct tick submit carries a future and is dropped, the future is completed immediately so callers do not hang. Benchmarks report allocation counts and selected observer-derived batch/queue metrics but do not assert absolute throughput.

## Import Boundary

Only `store/channel_adapter.go` imports old `pkg/channel` or `pkg/channel/store`. Other channelv2 packages must depend only on channelv2 interfaces.
