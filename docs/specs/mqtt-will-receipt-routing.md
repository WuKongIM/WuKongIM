# MQTT Will receipt reads under current authority

Status: runtime, Node facade and RPC 103 implemented. Actual Will execution and
product admission remain pending. The inventory below preceded implementation.

The retained receipt is publication evidence, never an authorization decision.
An absent result does not prove nonpublication after an uncertain append,
replacement, physical Channel deletion or restore. The Will executor must retain
that uncertainty until it can reconcile the original stable server identity.

## Failure inventory

- Requests contain exact Channel epoch, leader epoch and route generation plus
  sender UID and canonical server Will key. The caller cannot choose HW. Inputs
  and RPC envelopes are bounded and malformed identities fail before I/O.
- The runtime requires a recovered current leader and valid data-plane admission
  even in a single-node cluster. A stable write fence permits this immutable
  read; installing authority, stale routes and changed fences reject it.
- Reactor-owned HW is captured once. The bounded checkpoint worker persists that
  boundary before the optional store read, checks receipt sequence/content shape
  and closes its temporary lease on success, failure, cancellation and panic.
  Unsupported stores fail explicitly before checkpointing.
- Lookup waiters prevent eviction and share bounded admission/cancellation.
  Completion rechecks operation, generation, epochs, route, role, recovered state,
  write fence, cancellation and captured HW. Unrelated worker completions cannot
  consume the waiter. Errors never carry partial publication proof.
- The local facade must work with the real reactor, quorum runtime and disk
  store, preserve the original identity/time through prefix trim and restart,
  and reject old fences after authority renewal.
- Cluster routing uses fresh Slot reads before and after the operation, exact
  leader forwarding and independent serving-node validation. Changed placement,
  deleted Channels, missing capabilities, unsupported peers and gateway swaps
  must not become successful empty results. Node maintenance gates entry.
- RPC replies bind the complete request, use a closed status catalog and reject
  truncation, extra bytes, unknown versions and inconsistent found/receipt/HW
  tuples. Queries introduce no payload fanout or unbounded waiting queue.

Whole-Channel deletion, restore activation, receipt transfer/retirement and
actual Will execution remain separate required product work. These reads do not
promote local receipt absence into authority to repeat a publication.

## Implemented boundary

`Node.ReadChannelWillReceipt` requires foreground admission and resolves the
exact leader through fresh Slot reads. RPC 103 revalidates on the serving node,
binds the complete request in its reply and allows caller cancellation. Both
origin and serving calls have four-slot no-queue admission and a five-second
deadline. The 70 KiB envelope preserves the existing 65,535-byte UID domain.

The local `channel.WillReceiptReader` uses the existing reserved reactor mailbox,
lookup waiter lifecycle and checkpoint worker pool. It requires recovered quorum
authority, captures HW independently and returns only a consistent typed result.
Stable write fences permit reads; changed fences and failed data-plane admission
reject completion. No message payload is transferred by this RPC.

## Verification

Failure-first tests cover request/result bounds, worker lease cleanup and HW,
reactor completion/cancellation/foreign-result fences, fresh metadata and capacity,
RPC request echoes, malformed envelopes, gateway replacement and Node maintenance.
Real single-node cluster runtime coverage verifies durable append, prefix cleanup,
restart, stale routes and immutable reads under a stable write fence.

The real three-node test uses TCP, disk and 256 hash slots. It verifies remote
leader reads, authority renewal, serving-node original cleanup, restart and
rejection after the warmed serving node loses its Slot majority. Receipt identity,
fingerprint and original timestamp stay unchanged. This does not prove receipt
transfer to a newly added replica after all originals were already removed, nor
whole-Channel deletion/restore safety or process-level MQTT acceptance.

```sh
GOWORK=off go test -race -p 2 ./pkg/channel/... ./pkg/cluster/... -count=1
GOWORK=off go test -race -p 2 -tags=integration ./pkg/channel/service -run '^TestWillReceiptServiceSingleNodeClusterTrimAndRestart$' -count=1 -v
GOWORK=off go test -race -p 2 -tags=integration ./pkg/cluster -run '^TestWillReceiptRoutingThreeNodeTrimFailoverRestartAndIsolation$' -count=1 -v
```
