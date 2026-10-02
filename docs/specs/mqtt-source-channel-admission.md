# MQTT source admission through Channel

The optional `MQTTSourceActivator.EnsureMQTTSource` capability runs on an already
loaded, recovered Channel leader. Callers supply current Channel/leader epochs
and route generation, plus an allocator-issued control message ID and stable
positive timestamp. This is a source-log capability, not subscription permission
or a SUBACK receipt; authoritative routing and permission must still be fenced
by the source owner and subscription projection.

The facade reserves the Channel against eviction. A normal-priority reactor
query captures its committed HW, persists that HW through the bounded checkpoint
worker pool and reads a consistent activation/source/checkpoint view. Existing
protection returns without another log record. If absent, the facade appends
one explicit canonical control through the ordinary ordered append queue and
quorum owner, then confirms protection through the same query. Concurrent first
requests may append redundant controls; the first durable activation wins.
Ordinary Append/AppendBatch never select a control from payload bytes.

The source's immutable StartAfter is independent of a new subscription's start.
The latter uses the returned committed boundary and persists its first chosen
boundary across intent retries. No committed boundary is accepted from a caller.
Cancellation, generation/epoch changes, route changes, unavailable quorum mode,
write fencing, admission-guard rejection, checkpoint/read failure and malformed
results prevent success. A cancelled accepted control can still commit; retry
observes that protection rather than removing it.

The asynchronous source query shares the bounded worker queues, lookup waiter
cancellation and lifecycle guards. It adds no per-Channel goroutine, historical
scan, subscriber fanout or new metadata/body command. Source reads pin one small
coherent view and require a committed format-4 marker; independently installed
legacy local source state is not treated as replicated activation proof.

## Failure inventory before implementation

1. Generic append can spoof the control, a local-only mode can activate it, or a
   malformed/mixed control reaches the sequencer. Missing/old epochs or route
   generation bypass admission; an unsupported store acknowledges success.
2. Direct QuorumLog access bypasses Channel ordering and leaves reactor HW stale.
   A checkpoint supplied by the caller invents committed progress. A queued query
   follows an unrelated worker result or returns a boundary newer than admitted.
3. Pending activation or a local source CAS is accepted as committed protection.
   Missing/corrupt marker, source or checkpoint is silently accepted. Checkpoint
   failure, later activation beyond the captured HW, and mismatched results are
   mistaken for success.
4. Metadata/write-fence/admission changes or synchronous context cancellation
   during I/O let a late result escape. Cancellation, pool rejection, shutdown or
   eviction leaks a waiter or lets a worker complete a newer runtime incarnation.
5. Every subscriber writes another activation, duplicate first requests reset
   the source identity, or source protection start grants pre-subscription data.
6. Native payloads, ordinary append ordering, worker pool isolation or durable
   recovery regress. Reopen loses the first protection identity or exposes an
   unmaterialized source after the capability returns.

Tests first cover the real Channel service, reactor, quorum runtime and disk
store together, plus deterministic reactor completion races and storage evidence
corruption. In-process integration is not full product process acceptance; the
MQTT listener, distributed subscription projection and shared-copy transfer
remain required before the approved feature is complete.

## Frozen context

Source `2354eaab9`; SHA-256:

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `pkg/channel/FLOW.md`: `1b5ec749921fbf8a6af45047845f242a8b160f03f205acb3f30d7c7f2dcfa18e`
- `pkg/channel/reactor/FLOW.md`: `fa6df60855d02e1dccfad25c32cd8c19c205515a53c777a5f5c8acbcdc0cd2d3`
- `pkg/channel/worker/FLOW.md`: `da9c03f3b2480ae1601938e205ed47981a2b6a6c6248b2f66f88ae930a107752`
- `pkg/db/FLOW.md`: `8b723e6f1402c2361a872e3a6e4021ac4146782ebc9b416cb7b9cc9787bb9d75`
- `pkg/db/message/FLOW.md`: `98d19cb603b9581311aead0cbf86180da78becc06e264ae48b9e400de0d54b8f`
