# MQTT inbox admission checkpoint

This storage slice implements the approved first-person-write admission seam.
It does not yet install the product append hook or initial inbox projection.

The source Slot stores one checkpoint for a canonical person channel in metadata
`mqtt_source_binding` (table 26), System 1. It adds no business-table columns.
Two participant phases follow the canonical channel's decoded UID order; phase
2 means both scans finished. Each phase persists the full UID-qualification
candidate cursor in encoded primary-key order. The application must first commit
both UID directory registrations, prepare each selected inbox source, and only
then advance this checkpoint. Storage CAS is not evidence of those remote steps.

A checkpoint binds the runtime's directory generation. Its revision never resets:
physical runtime deletion atomically retains an invalidated checkpoint with
incremented revision and directory generation zero. Recreated runtime metadata
cannot accept old exact retries even when its generation restarts at one.
Business-channel deletion advances the existing runtime directory generation;
reads expose both records so a stale completion cannot authorize a new append.

Failure inventory for the existing metadata/FSM/authoritative-read seams:

- Missing runtime, stale directory incarnation, skipped phase, backward candidate,
  changed participant, revision or timestamp regression must not advance progress.
- Encoded string order is length-before-bytes; lexical comparisons can skip clients.
- Exact retry succeeds only while its runtime incarnation remains current.
- Runtime create, checkpoint CAS, deletion and recreation in one batch must see
  earlier staged state. A failed batch must publish neither progress nor deletion.
- Both direct and compatibility runtime deletion must invalidate progress;
  repeated deletion must not wrap revisions or erase the invalidation witness.
- Pinned reads must pair runtime/checkpoint from one snapshot, bypassing live cache.
- Corrupt/key-swapped/truncated or oversized values fail closed. Missing state is
  distinct from invalidated state. Noncanonical channels and foreign cursors fail.
- FSM routing, bounded command decoding, committed receipts, applied indexes and
  snapshot restoration must preserve monotonicity. Read replies reject mixed
  result kinds and preserve the wire shape of older reads.

New System values and command/read kinds require matching writers/tools; old
writers do not invalidate this witness. Product activation remains disabled until
the complete admission, recovery and capability contracts are wired and tested.
MQTT JSONL state transfer remains outstanding. Rollback needs a pre-feature backup.

## Stored and routed contract

Slot command **74**, version 1, carries the complete next checkpoint and expected
revision in a strict JSON body bounded to 32 KiB including its two-byte header.
Missing runtime or stale progress returns a conditional conflict, not a partial
write. Exact retries return Unchanged only after runtime incarnation validation.
The Node facade preserves foreground/maintenance admission and routes by the
canonical Channel ID through existing Slot proposal handling.

RPC **91**, read kind **21**, accepts only `admission_channel` and pins the
business Channel, runtime and optional checkpoint in one snapshot after a fresh authority/apply
barrier. An absent runtime may have no checkpoint or an invalidated checkpoint;
live progress without runtime is corruption. Stale positive generations remain
visible for reset, never automatically interpreted as readiness. New fields are
omitted from older request/reply kinds.

The System key is the existing metadata/hash-Slot/table-26/System-1 prefix plus
one native length-prefixed Channel-ID string. Channel type is fixed at 1. Its
checksummed fixed envelope version 1 contains, in order, big-endian uint64
directory generation, revision and positive timestamp, a uint8 participant,
two uint16-length-prefixed strings (namespace and ClientID), then uint64 Session
and subscription generations. Empty cursor fields are all zero. Its UID owner
is derived from the canonical participant. Each string is bounded to 1,024
bytes; the complete value is bounded to 2,200 bytes. Unknown/trailing data and
key-bound checksum mismatches fail closed. Old absent values need no backfill.

Validation exercises storage CAS with staged runtime creation, both participant
cursors, encoded ordering, fixed incarnations and both forms of deletion;
FSM batch rollback, recreation, exact retry, ownership and snapshot restore;
and 256-hash-Slot two-node proxy fixtures with deliberately absent origin data,
fresh apply barriers and changed authority. These deterministic tests do not
prove product first-append admission or a full MQTT process E2E run.

Read kind 21 now also carries the optional native Channel row from that same
snapshot. The usecase requires its directory Ready marker and generation to
match the runtime before scanning qualifications. This is an added read field,
with no stored-format change; matching MQTT peers remain required.
