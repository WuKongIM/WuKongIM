# MQTT offline drain authority

Interrupted UNSUBSCRIBE must release unadmitted backlog after the connection
has closed. Existing window/cancel-init commands currently require Active
Session state; this blocks the later background recovery usecase. This step
supplies the required atomic metadata contract, not automatic recovery by itself.

## Failure inventory before implementation

1. Offline closed subscriptions cannot release backlog or create an empty
   cancellation cursor, so a recovery worker cannot resume durable intent.
2. Relaxing Active also permits offline admission, PUBACK fabrication, skipping
   live subscription backlog or initializing a deliverable cancellation cursor.
3. An older Session lifetime, owner tuple or revision mutates a successor. Same
   subscription generations must preserve authorization; only a strictly newer
   subscription generation can witness the old intent's closure.
4. Cleanup erases inflight exchanges or ACK gaps, releases inaccurate qualified
   charges, crosses more than one accounting receipt, or resets existing cursors.
5. A lost response is applied twice; conflict partially changes Session/cursor
   or inflight state. Existing digest/revision checks and atomic batches remain.
6. Offline cleanup changes expiry/lease/Will semantics, reactivates a Session,
   or drains an explicitly ended lifetime through the wrong contract.
7. A schema-compatible command is assumed safe on mixed binaries although the
   replicated FSM's acceptance semantics differ. This path requires matched
   cluster writers; no mixed-version fallback is introduced.

Tests use actual metadata batches and reads to prove atomic state changes and
negative cases before production changes. Product background discovery, closed
intent orchestration, cursorless source proof and final subscription completion
remain required; existing process quota tests are regression evidence only.

## Implemented metadata behavior

The existing Window Advance operation permits an Offline Session only after
loading the exact cursor's topic subscription in the same atomic batch. The
subscription must be the same generation in Removing/Removed with the cursor's
authorization version, or a strictly newer generation. Missing or older intent
rejects the mutation. This extra point read occurs only for offline advancement.
Active advancement retains its existing behavior, including normal QoS 0 skips.
Offline Admit/ACK and every ended-Session mutation remain rejected.

CancelInit also allows Offline with its existing same-generation closed intent
or strictly newer replacement witness. It creates only a missing empty cursor,
never resets one or adds backlog. Both operations preserve exact Session lifetime,
owner tuple and revision checks, digest retries and atomic Session/cursor updates.
They do not consult wall time, extend expiry, change Will state, reactivate the
Session, infer source protection or authorize network effects.

Qualified advancement consumes at most one existing bounded accounting range
with exact original charges. It retains PacketID, delivery order, body reference,
inflight count/bytes and contiguous ACK gaps. Lost replies can use the original
digest only while the exact resulting Session revision still matches.

No table, index, column, JSON member, command ID, operation ID or encoding changes.
The Slot FSM acceptance semantics change, so all participating nodes must use
matching binaries. This is not evidence that mixed-version replication is safe.

## Next required composition

Background source draining must derive a fresh exact parent and closed intent
through routed reads, bound every effect by cancellation/revision checks, and
reuse source protection plus persisted seal/range checkpoints. It must not create
a fake local Owner or activate an Offline Session to reuse the foreground path.
After source draining, the UID checkpoint and subscription completion still need
authoritative, bounded continuation, including interrupted cursorless preparation.
The existing consumer cohort and pending-intent indexes should be reused where
appropriate. Full interrupted-UNSUBSCRIBE process acceptance remains outstanding.

## Evidence and limits

Before implementation, closed Offline advance and CancelInit returned Conflict;
a separate test reproduced the same denial for the first bounded accounting
range. After implementation the focused race suite passes in 5.114s. Complete
metadata and MQTT-usecase race suites pass in 37.962s and 95.513s respectively.
Tests verify exact debit, retained inflight identity/ACK gap, unchanged Offline
expiry, rejection of live intent/stale authority/ended lifetimes and range bounds.

Eight existing process quota/completion scenarios pass in 207.193s across
single-node and three-node 256-hash-Slot clusters. They guard current behavior;
they do not prove the still-unwired interrupted-unsubscribe path. All eight
JSON artifacts are embedded in the [evidence report](../reports/mqtt-offline-drain.json),
with exact source context, implementation hashes and repeatable commands.
`flow-doc-contracts` passes with 87 compliant files, zero invalid files and the
same nine existing warnings. Schema/FSM participants still require matching builds.
