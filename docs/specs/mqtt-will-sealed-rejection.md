# MQTT sealed Started rejection

Source: `3f64b984eebb07035091dc11381f79796cbf1f66`.
Continue the approved MQTT development and existing independent Paho, public
HTTP, exact owned-process and temporary gofail acceptance seams and the already approved metadata CAS/snapshot contract from
[mqtt-will-preparation](mqtt-will-preparation.md). This addresses
one safe terminal case, not the full unknown-effect or shared-storage obligations.

## Failure inventory before tests and implementation

1. A durably sealed Reserved/version-2 Admitted Started attempt remains pending
   forever after definite permission denial, consuming bounded journal capacity.
2. Missing receipt, elapsed lease, failed authority/permission read, legacy
   Admitted or AppendIssued is mistaken for nonpublication and discharged.
3. Rejection changes the execution tuple, frozen body, server key or lifecycle
   decision; an old worker, changed row or uncertain CAS releases another attempt.
4. A positive receipt is discarded after revocation rather than completed.
5. A rejection written after cancellation, clock regression or unknown seal
   grants completion; late/uncertain terminal writes erase recovery evidence.
6. Permission restoration or executor restart revives the rejected Will, or new
   durable phase bytes cannot be read through the Slot/snapshot path.
7. A capacity test moves work to another executor, retries CONNECT/resubscribes,
   treats a closed receiver as silence or reports before cleanup.

## Durable decision

Keep positive receipt recovery first. Only a successful exact
`SealUndispatched`, followed by explicit `ErrWillDenied` and an unexpired caller
context/monotonic clock check, permits one expected-revision terminal CAS.
The row retains its existing executor tuple, immutable publication and frozen
body. No successor execution or new journal reservation is needed.

Append dispatch phase value 4, `Sealed`, to existing optional column 35. It is
valid only with Rejected/PermissionRevoked and zero receipt/lease. The only new
transition is Executing/Started -> Rejected/Sealed after the old grant expires,
preserving the exact executor and frozen body. The trusted usecase supplies the
durable sealing/current-denial proofs; storage validates shape and exact CAS,
as with existing caller-supplied publication proofs. Legacy phases cannot be
upgraded into this evidence. Repeated exact terminal writes remain idempotent.

Only a definite Applied/Unchanged result for the expected new revision permits
exact cleanup. Unknown/error/conflict retains the journal; existing pressure
reclamation can later confirm a committed terminal row. Old rows/column bounds,
indexes and RPC envelopes stay unchanged. Older binaries reject phase 4: all
cluster runtimes/tools must match before enabling this behavior and rollback
requires a pre-feature data generation. No backfill or mixed-version rollout.

## Process acceptance

Use single-node and three-node clusters, 256 hash Slots, one logical Slot group
and a temporary cap of one to keep actual journal pressure on the captured
executor. Cut a real Started attempt before dispatch (Reserved tracer), revoke
the sender through public policy, and require a second legal Will to encounter
that executor's full journal before recovering capacity and delivering once.
Then restore permission, abruptly join/restart that exact executor, reconnect
the persistent recipient once without SUBSCRIBE and require healthy quiet.
Extend the same fixture to version-2 Admitted before append permission.

Reports run after joined client/process cleanup and retain failed runs. The
baseline must fail on the pending legal Will's business receipt, not missing
instrumentation. Related issued/legacy/positive-receipt recovery and durable
transition/compatibility gates remain required. This is bounded Darwin
acceptance, not production-cap, partition, restore rollback, shared-storage,
throughput or complete MQTT qualification.
