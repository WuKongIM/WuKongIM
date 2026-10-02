# MQTT replay anchor reactor integration

Anchor admission uses the existing bounded append mailbox, reservation and queue.
A typed task runs the durable sequencer's anchor operation in the append worker
pool. It never performs store I/O on the reactor goroutine or creates a separate
per-Channel worker. Queue bytes include owned membership/acknowledgement slices.

The public request owns its copied metadata and receipt slices. Admission and
queue flush require the exact recovered leader, epochs, route, placement, status,
write guard, capable storage and anchor committer. New controls and historical
proof retries both serialize with business appends. A typed control completion
advances monotonic committed progress and removes append waiters without assigning
the new request's message identity to an old control row. It never populates the
recent-record cache with request data. Proofs are returned on a separate result.

Caller cancellation may remove queued work or its observer. Already-started
commit work remains owned by the worker and updates current reactor progress even
when its observer has gone. Stale generation/authority or foreign operation
completions cannot change a replacement runtime. A current durable completion
must not roll back progress merely because a write guard or caller expired after
the commit. The caller receives the current guard/cancellation failure instead
of a proof in that case.

## Failure inventory before implementation

1. Anchor work bypasses queue bounds, append ordering, recovered admission or
   lifecycle ownership; caller-owned metadata/receipt slices outlive cancellation.
2. A retry creates a phantom record in the recent cache or returns the new caller
   ID as the old control's identity. Old proofs regress LEO/HW/checkpoint progress.
3. Cancellation after admission cancels a durable worker or prevents progress
   publication; queued cancellation still writes, or orphan waiters block eviction.
4. A foreign operation, replaced generation, changed route/placement, malformed
   proof, wrong source/content or unsupported capability publishes a proof/HW.
5. A worker is sent to the read/checkpoint pool, batched as ordinary store appends,
   or performs its blocking effect on the reactor goroutine.
6. Restart loses anchor idempotency; subsequent ordinary appends reuse positions;
   an anchor-only tail generates another anchor or rewrites cached content.

Tests precede implementation. This service integration is not fresh Slot routing,
Node RPC admission, source release, shared-copy recovery readiness or MQTT product
acceptance. Those required paths must call the same reactor-owned facade.

## Frozen context

Source `38e19e035`; SHA-256:

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `pkg/channel/FLOW.md`: `0297bf92078d6eefc90689c0684c56ebea1f5085e601d3c9d0a9c4bbc0977019`
- `pkg/channel/reactor/FLOW.md`: `d7f89e9e87b085c3b95ef941b1b5f7d142a76d38b0dd2cd6b355de8ba4083e47`
- `pkg/channel/worker/FLOW.md`: `8640c8da5dce68b0d45b5075c946f6e0d4c038ed48be6c5751e571c2773022e0`
