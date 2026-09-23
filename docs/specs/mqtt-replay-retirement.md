# MQTT replicated replay retirement journal

A replay-retirement decision retires an entire previously committed content
anchor. The selected anchor must be at or below the coherent consumer floor;
rounding to an anchor preserves an independently committed prefix digest and
cumulative accounting baseline for later suffix-only repair. Admission must
perform the ordered authority/consumer protocol before proposing this decision.
Decoding a payload or appending a raw record does not prove that admission.

## Durable contract

Proposal format 6 is an explicit single internal SyncOnce record with a separate
hash domain. Ordinary business format selection never chooses it from payload
bytes. Its closed version-1 payload contains the complete format-5 anchor value,
its original log position and its exact proposal digest. The referenced anchor
must precede the retirement record and agree with the source activation. No
partial-anchor cut or caller-invented prefix is accepted by storage.

Message System 15 journals the canonical retirement control envelope by original
log position in the same synchronous batch as its proposal/entry identities.
Staging validates the referenced anchor, including anchors earlier in the same
recovery batch covered by the supplied commit frontier. Existing pending controls
are retained; only suffix replacement may discard their journal. Retirement
prefixes never regress, and a repeated prefix must refer to the same anchor.

Point reads return only a HW-covered decision with matching activation, source,
anchor, paired proposal and entry identity. Journals survive ordinary history
retention. Backup excludes uncommitted decisions and preflights all retained
references and identities before restore. Stores without explicit retirement
journal capability must reject format 6 on append and recovery replacement.
Matched runtimes/tools are required; binary-only rollback after such writes is
unsupported. Existing business/control encodings remain unchanged.

The native sequencer explicitly selects format 6 and preserves that intent across
retained/pending retries; ordinary business retries and mixed control flags fail.
Three-voter/one-learner disk/wire integration uses controlled consumer admission
and verifies the committed journal across restart and authority recovery.

This stage persists and verifies the decision only. It does not wire its product
consumer-admission producer, advance replay coverage, discard content/meters, change readiness,
release binding responsibilities or open MQTT product admission. Subsequent
work must materialize the retired accounting/hash baseline, recover only the
remaining suffix, persist bounded physical deletion progress, preserve backup
and restore of pruned content, and route admission through current authority.

The subsequent [retired storage contract](mqtt-retired-replay-storage.md) now
implements baseline materialization, bounded local cleanup, suffix recovery and
version-3 backup/restore. Product admission/current-authority routing and scheduling
remain unwired; the original journal itself still has no automatic deletion effect.

## Failure inventory before code

1. Business payload selects internal retirement semantics; multi-record or
   business-only fields evade the closed control format; hashes cross domains.
2. Missing, uncommitted, foreign or altered anchor references become retirement
   proof; recovery batches cannot carry a covered anchor and its decision together.
3. Pending retirement is exposed before HW, survives a replaced suffix, regresses
   the retired prefix or changes an equal-prefix reference.
4. Missing journal, primary row, paired manifest, entry or source state is confused
   with valid absence; original-history deletion destroys the independent proof.
5. Backup includes pending decisions or restore accepts missing/changed references;
   restart or exact retries change the decision. Unsupported stores silently keep
   only the ordinary record and lose its journal during cleanup.
6. Persisting a decision accidentally deletes shared content or authorizes product
   admission before recovery, readiness and complete producer wiring exist.

## Frozen context

Source `6d3c73899f5dd889703402035c127693d8a2cb6f`; SHA-256:

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `pkg/db/FLOW.md`: `8b723e6f1402c2361a872e3a6e4021ac4146782ebc9b416cb7b9cc9787bb9d75`
- `pkg/db/message/FLOW.md`: `1e9e0234dbc430276126713453ff797188115f09a477123ead6fef3e49666adf`
- `pkg/channel/FLOW.md`: `7ae69d0c02737dee2ad08839ed154f9dcb46bd060f594a938fc37b9562833f19`
