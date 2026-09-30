# MQTT subscription orchestration

This usecase coordinates the existing owner-fenced subscription commands; it
does not implement replicated source activation or expose SUBACK by itself.
Product admission remains closed until real source projection and delivery exist.

## Failure inventory before implementation

1. A client chooses another UID's inbox, supplies a forged principal, changes a
   target behind an existing topic or uses a stale owner. Derive UID from Owners,
   validate current Session evidence, enforce own-inbox identity and require
   current authorization before any establishment or option replacement.
2. A local row or callback is mistaken for recoverable source coverage. Persist
   Preparing before projection, require a matching committed projection receipt,
   reread Session/child and reauthorize before changing to Active. Projection is
   an injected capability with an explicit distributed durability contract.
3. Crash/cancellation after intent/projection loses work or chooses a new boundary
   on retry. Stable operation/generation survives retries and owner resume;
   reconcile the exact Preparing/Removing row without recreating it.
4. Option replacement resets cursors or crosses membership incarnation. Active
   replacements keep generation, operation, target and authorization version.
   A different authorization version fails closed for revocation handling.
5. UNSUBSCRIBE deletes unacknowledged QoS 1 exchanges or releases content early.
   Commit Removing first; the projection closes new matching work while retaining
   previously admitted exchanges and required content. Mark Removed only after
   matching durable removal evidence. These methods never mutate inflight rows.
6. Renewal changes Session revision during a cross-Slot projection. Completion
   rereads current owner and exact child; a harmless parent renewal is allowed,
   while changed child/owner evidence cannot authorize stale completion.
7. Counting subscriptions races another writer or scans an unbounded tombstone
   history. Count bounded authoritative pages at one Session revision and commit
   against that revision. Pending/removing rows count toward the limit; removed
   rows do not. Exhausting scan/time budgets fails closed and requires cleanup.
8. Lost/contradictory commit receipts, forged projection receipts, changed cursors,
   dependency errors, panics, cancellation, clock regression or lease expiry are
   treated as success. Verify each boundary, retain pending state and release
   operation scopes on every exit. No background goroutine or retry loop is added.
9. Recovery or unsubscribe needs current group permission and therefore cannot
   remove revoked subscriptions. Removal needs exact owner evidence but does not
   require receive permission. Preparation recovery always reauthorizes.

## Projection port contract

Establish success means exact subscription intent has authoritative, recoverable
source bindings and initialized consumption boundaries, replicated source
protection and shared-content recovery on every eligible future owner. An inbox
also needs durable discovery and the future-person-source admission handshake.
Replica-local storage writes, an in-memory callback or a list of today's sources
cannot implement this capability. The receipt binds the complete persisted intent
identity and revision; it is not a cryptographic or independently verified proof.
App may only inject an implementation that establishes this contract.

Remove success means no new deliveries can be matched by the removed intent.
Existing QoS exchanges and their content remain independently recoverable until
ACK or explicit Session termination; removal is not permission to discard them.
All projection writes must be generation/operation fenced and monotonic. A late
or uncertain operation can only retain conservative obligations, never release a
successor's content or activate an authoritative subscription by itself.

[SourceDrain](mqtt-source-drain.md) supplies the concrete per-source sealing and
unadmitted-quota release step, including cancelled preparation. It preserves
inflight exchanges; final binding release remains SourceRemoval's responsibility.
[GroupProjection](mqtt-group-projection.md) now composes group establishment and
removal through real protection, shared recovery and drain ports. Inbox projection
and complete recovery scheduling remain required.

Default bound: 128 retained subscriptions, 16 pages of at most 64 rows and five
seconds per usecase call. Configuration may allow up to 1024 subscriptions and
64 scan pages. The Session revision CAS is the concurrent quota guard; there is
no per-connection lock or all-members scan. QoS 2 requests are granted at most 1.
Entry adapters own canonical topic mapping and retain their reply operation
through SUBACK/UNSUBACK enqueue. This usecase performs no wire writes.

One deterministic regression delays parent cancellation callbacks after the
parent's Err is already visible. Relying on context.AfterFunc alone incorrectly
allowed Active completion; the usecase now checks that parent synchronously at
every effect boundary. Final metadata scan pages preserve their input cursor,
while non-final pages must advance in encoded topic order.

Real single-node cluster integration verifies durable intent, unavailable-source
failure, owner resume, concurrent parent renewal and removal after membership
revocation. Its projection receipt is explicitly controlled by the test. Neither
that fixture nor matching receipt fields prove distributed source durability.
The concrete group projection is separate from that fixture. Inbox projection,
background offline reconciliation, permission-incarnation ordering, safe tombstone
GC and product entry composition remain required.

## Frozen context

Source `2a0e94b0a`; SHA-256:

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `internal/usecase/mqttsession/FLOW.md`: `b58f90f775c8a49eadc7dfb0ce562cd0cffd958b129b6df5be81a5787970b70d`
- `internal/runtime/mqttsession/FLOW.md`: `cd6b01821860621d502db203c9dbd29604e9bab459d78c584022ce43802c6320`
- `internal/app/FLOW.md`: `0d8a01b8041db4a3109bdea5e61c324006e2d6f65bd5638790d6e39acef19e92`
- `internal/contracts/mqttsession/FLOW.md`: `5bd4d98da6fa4d59615b62f31f98056add1127274a29816c8d5a181015329841`
- `pkg/db/FLOW.md`: `8b723e6f1402c2361a872e3a6e4021ac4146782ebc9b416cb7b9cc9787bb9d75`
- `pkg/db/meta/FLOW.md`: `e05d11e22fc0c4afa0898a9e6498b45b9cc7f594fd4b8529587056cd784ecbb7`

## Completion CAS contention failure inventory (2026-09-30)

The full process workload has shown both subscribe and unsubscribe conflict
closures after fanout. The fixed-point unit reproduction independently proves
that renewal between completion's fresh read and final CAS rejects an otherwise
identical child. This does not identify every process closure's origin.

Before changing completion, cover these failures for Preparing-to-Active and
Removing-to-Removed: one unrelated renewal prevents completion; repeated renewal
creates an unbounded retry; changed child options or Owner are adopted; a conflict
without a newer parent is blindly retried; a port error or lost applied reply is
mistaken for definite rejection; cancellation admits another write; receive
revocation is ignored on establishment or blocks removal; and another remover's
exact final commit is rejected as an ambiguous replacement.

Only the private definite-CAS-rejection classification permits at most three
completion proposals inside the original scope/deadline. Fresh same-Owner reads
must preserve the complete captured child and advance the parent revision.
Projection is not repeated and its original receipt remains pinned. Establishment
reauthorizes the identical incarnation before each proposal. Removal may confirm
only the identical already-Removed child, including after a definite rejection.
Unknown writes/errors never enter this path. New-intent admission, quota scans
and source-binding/cursor conflicts retain their existing rules.

Test-first validation: the baseline failed bounded renewal completion and
exact competing removal confirmation. The new nineteen-case matrix and existing
subscription/removal checks passed with race; the full usecase suite and real
single-node cluster subscription/request integration passed. The `flow-doc-contracts`
named check and vet passed. Full load results remain separate from this narrowly
proved contention behavior.
