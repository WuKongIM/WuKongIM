# MQTT source release from committed replay anchors

`ReleaseMQTTSourceAtAnchor(generation, position)` derives one replica's source
cleanup boundary from its own committed format-5 journal and its own durable
shared replay prefix. Callers supply neither a release watermark nor a digest.
The existing anchor is the replicated content decision; System 12 materializes
its local cleanup effect. No new table, control format or checkpoint is needed.

The operation holds append then checkpoint ownership through synchronous commit.
It verifies the activation, source incarnation, committed checkpoint, paired
proposal/entry identities, local replay tail and cumulative anchor endpoint.
Only then may it advance copied-through to the anchor's covered position,
increment the local materialization revision and retain the anchor manifest
digest as the receipt reference. Replicas may materialize different intermediate
anchors; revision is a local CAS sequence, not a cluster-wide commit index.
An exact or older retry verifies evidence before returning the current state.
It never regresses progress, copies content, advances HW or deletes any rows.
Existing physical retention performs deletion independently and remains clamped.

This is a bounded storage operation, not a fresh membership/owner receipt or a
consumer acknowledgement. It relies on the immutable, atomically maintained
replay prefix and bounded endpoint proofs; it does not audit every historical
body for disk corruption. Shared content cannot be reclaimed by this operation.
Future consumer-proof GC must preserve or explicitly replace these proofs.
[Cluster routing and bounded scheduling](mqtt-source-release-routing.md) now
invoke this primitive with explicit release intent and fresh authority checks.
MQTT product admission remains disabled pending the full approved implementation.

## Failure inventory before implementation

1. A local copy without a committed anchor, an uncommitted/absent anchor, a wrong
   generation, or a missing activation/checkpoint/paired log proof releases data.
2. Absent/partial shared content, a broken tail or cumulative meter, or a
   mismatched content digest is accepted as full copy coverage.
3. A caller substitutes a watermark/digest, release advances HW, or cleanup
   crosses the selected anchor and destroys the remaining protected suffix.
4. Exact/older retries regress or increment state; concurrent release/retention
   races lose progress; a revision overflow wraps into a valid initial state.
5. Retry skips proof validation, ignores cancellation/closed leases, or changes
   source state after an error. Physical retention beyond prior protection is
   silently accepted.
6. Trim, close/reopen or binary backup/import loses the receipt/protected suffix
   or makes anchored shared content unreadable after deleting original rows.

## Frozen context

Source `35441d1c396126a8bd9b7804fb4e0792ae57cef3`; SHA-256 digests:

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `pkg/db/FLOW.md`: `8b723e6f1402c2361a872e3a6e4021ac4146782ebc9b416cb7b9cc9787bb9d75`
- `pkg/db/message/FLOW.md`: `d4b05311077d9c293749ab1d95a4bf5c2fa3e945fba2e29bc1626196e2ef5131`
