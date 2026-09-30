# Gateway deferred completion ownership — product adapter still pending

Gateway core now supports an optional `DeferredSendBatchHandler`. Its existing
mailbox worker joins handler preparation and can then prepare the next batch.
Core owns per-session publication chains across those batches. This complements
the ordered Channel submission owner and asynchronous message preparation port.
The product access adapter and app composition still use the original joined
handler: the original EOF is not yet repaired, and R2/R6 remain incomplete.

## Fixed ownership budget

For deferred handlers, the configured global and shard capacities count waiting,
executing and completed-but-unpublished records. Dispatch does not release those
reservations or create an additional accepted-work allowance. This is stricter
than the old queued-only occupancy (which excluded the executing batch); the
configured limits are unchanged. Existing joined handlers keep their existing
behavior. Pressure observations report these exact held reservations.

Task descriptors are copied before mailbox storage can be reused. One session
publishes in input order across batches; independent sessions on the same shard
can publish concurrently. The shard mutex protects links and ownership only,
not user publication calls. Empty chains are deleted, popped successor links
are cleared, and each accepted record retains at most one active chain entry.
Publication release is O(1) per record, with one batch scan at each of the
preparation-return and completion fences. No new worker/goroutine pool is added.

A record releases only after publication, handler completion/error handling, and
preparation return. Drain closes new admission and joins these same reservations;
a caller timeout cannot discard/reset them. Missing results, immediate rejection
and publication panic fail closed. Publication uses ordinary Session writes so
its existing terminal seal remains authoritative. Core reports asynchronous
write errors to the owning session; publish's return value reports callback
admission validity, not eventual write success.

## Test-first corrections

Failure contracts and executor integration tests preceded implementation. Initial
RED was the missing optional interface, not the original pressure reproduction.
Review then found two concrete lifecycle defects in the initial implementation:

1. Setting completion at callback entry let preparation return release every
   record while error handling was still running. The new regression observed
   DrainSends returning nil while that handler was blocked. Completion is now
   sealed first but marked finished only after error handling/publication work.
2. With CloseOnHandlerError=false, one two-record terminal batch error reported
   four errors for one session. The regression reproduced that exact count.
   Batch errors now deduplicate physical sessions; item cleanup does not repeat
   the already reported batch failure.

Both RED outputs remain archived. Final eight TestDeferredSend scenarios passed
20 repetitions with the race detector in local Docker, Linux arm64 Go 1.25.11.
They cover batch-independent preparation, cross-batch order, original capacity,
inline completion, timed-out drain continuation, independent session publication
while another write blocks, missing/error/rejected/panic cleanup, invalid/duplicate
publication, concurrent same-session results, descriptor/reply-token preservation,
empty lane retirement and the two lifecycle regressions.

The full default gateway subtree and access/gateway suites passed before the last
error-report correction; final core and access/gateway default suites passed again
after it. Earlier successful and failed runs remain separate artifacts. No measured
SEND workload ran concurrently with these checks.

The named flow-doc-contracts check passed after index regeneration. Gateway FLOW
is now 105 lines versus its 100-line target; the documented SHOULD deviation is
to retain existing protocol/drain/seal contracts alongside the new deferred
ownership rules. No validator was weakened.

## Remaining integration

The product adapter must map results into deferred publication closures and call
the new message preparation port without holding its own result state across
unsynchronized callbacks. App composition must supply the ordered admission owner,
release it on constructor rollback, and drain it before append/storage dependencies,
including terminal and restore flows. Add entry/composition coverage and the
operator Changelog entry with that activation. Then execute the unchanged
5000-channel/4500-SEND/s/cap32 failure loop, clean R2, and three fresh complete R6
comparison pairs. These executor checks are not those qualifications.

Artifacts: `assets/send-ban-deferred-gateway-20260928/manifest.json`.
