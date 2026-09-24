# pkg/db Schema Compatibility

This document explains how to add fields to existing `pkg/db` tables without
breaking old data, rolling upgrades, rollback, snapshots, or diagnostics.

The short rule is: make durable formats append-only, make new fields optional
when reading old rows, and do not make new indexes or new field semantics
correctness-critical until old rows and mixed-version nodes are handled.

## Compatibility Targets

Every existing-table field change must define what remains compatible:

- Old on-disk rows must be readable by the new code.
- New rows should be readable by old code during rolling upgrade whenever the
  table can be touched by old and new nodes at the same time.
- A rollback should not turn rows written by the new binary into corrupt rows
  for the old binary.
- Hash-slot snapshots, channel snapshots, indexes, inspect APIs, and legacy
  compatibility wrappers must remain internally consistent.

If any of these cannot be true, the change is not a simple field add. Treat it
as a format migration and gate it behind an explicit rollout plan.

## Stable Durable IDs

Subscriber table 5 keeps its primary key and adds optional column 4,
`incarnation`, in a key-bound version-1 column envelope. Empty legacy values
normalize to incarnation 1. System 1 under this table stores a version-1,
checksummed fixed uint64 allocation high water per hash Slot; it survives member
and channel deletion. Native snapshots preserve both. JSONL adds optional
`incarnation` and a nonempty-only `meta.subscriber_sequences` dataset, represented
as decimal strings. Imports require sequence witnesses for nonlegacy members and
never allocate replacement identities. Old writers erase these values, and old
tools reject the new dataset: all writers/tools must match before deployment;
rollback requires a pre-feature backup. See [subscriber incarnations](../../docs/specs/mqtt-member-incarnation.md).

MQTT publication groundwork adds optional message column 21 (`publication_metadata`),
compatibility record codec 2 and exact proposal format 3. Absent metadata keeps
native record bytes and format-1/2 hashes unchanged. Binary backups preserve the
column and exact proposal identities; product send and owner-push DTO/RPC carry
the content. JSONL preserves message identities and publication metadata.
JSONL omits `publication_metadata_b64` on native rows; older strict readers reject
populated records. Preflight validates bounded metadata and a positive source
timestamp, import byte budgets include metadata, and summary/full verification
bind its SHA-256 without changing native digests. MQTT state transfer, restore
owner fencing and capability gating remain incomplete, so access stays disabled.
New data requires matched runtimes and tools and a pre-feature backup for rollback.
See [the publication format](../../docs/specs/mqtt-publication-metadata.md).

Message System ID 12 stores version-1 key-bound source protection and shared-copy
receipt references. Physical retention respects it; logical history stays
independent. Binary backups validate/preserve it against their selected HW. Old
writers ignore this safety state, so replicated activation requires matched
runtimes and restored-owner fencing; local storage apply is not that activation.
JSONL transfer and shared-replay integration remain pending. See
[the source contract](../../docs/specs/mqtt-source-protection.md).

Exact proposal format 4 introduces one explicitly tagged MQTT source activation
record with its own hash domain; business format selection remains 1–3. Message
System ID 13 stores the first activation manifest in a key-bound version-1 fixed
envelope. Pending activation clamps physical trim; the HW commit atomically
materializes System 12, whose `mqtt-log-v1:` generation is reserved for that
projection. Local CAS cannot create or replace it. Uncommitted suffix replacement
can replace the pending marker; committed activation cannot reset. Portable
backups omit pending controls and preflight the matching committed manifest,
source state and exact identities. Existing backup framing and manifest widths
are unchanged, but old validators reject format 4. Matching writers/tools are
required; JSONL transfer and product restore activation remain incomplete. See
[log activation](../../docs/specs/mqtt-source-log-activation.md).

Exact proposal format 5 stores one explicitly tagged MQTT replay anchor with a
separate hash domain and fixed version-1 source/prefix/digest payload. Message
System ID 14 retains a key-bound version-1 journal entry per control position,
atomically with the exact append. Committed reads verify source activation,
checkpoint and complete proposal/entry proofs; prefix cleanup retains journals,
while uncommitted suffix replacement removes them. Backups omit pending entries
and require a matching journal for every committed anchor, with identical
proposal/entry format versions. This changes neither System 12 release nor local
copy coverage. Business version selection remains 1–3. Old validators reject
format 5; matching writers and tools and pre-feature rollback backups are required.
Runtime copy-receipt admission and routed anchored repair preserve these proofs. See
[replay anchors](../../docs/specs/mqtt-replay-anchor.md).

Exact proposal format 6 records one explicitly selected replay-retirement
decision under a new hash domain. Its closed payload binds a complete accepted
anchor, its original position and proposal digest. Message System ID 15 journals
the canonical control envelope in a key-bound version-1 checksum value. Append
and recovery validate source activation, the covered anchor and monotonic prefix;
committed reads independently verify the full proposal/entry/reference chain.
Backup keeps only HW-covered decisions and validates their journals and anchors;
suffix replacement removes only uncommitted journals. No replay row, meter or
source-release encoding changes, and no physical reclamation is enabled here.
Business format selection remains 1–3. Old validators reject format 6, so all
writers/tools must match and rollback requires a pre-feature backup. Product
consumer admission remains required; explicit local materialization is described below. See
[replay retirement](../../docs/specs/mqtt-replay-retirement.md).

Explicit source release can now derive System 12 progress from a locally
committed format-5 anchor and independently verified local replay coverage.
It stores the anchor manifest digest in the existing receipt field and advances
the local materialization revision, with no encoding or ID changes. Background
target recovery requests this through RPC 99 v2 with explicit intent and reply
acknowledgement; v1 remains ordinary recovery and old servers reject v2. See
[anchor-derived release](../../docs/specs/mqtt-source-anchor-release.md).

MQTT plan RPC 97 keeps request v1 and uses reply v2 only when carrying an
explicit bounded maintenance-tail assertion; ordinary/error replies remain v1.
Older readers reject v2, and matched runtime deployment is required. This adds
no stored metadata or native log format.

RPC 99 version 3 explicitly applies the latest locally committed retirement
before repair planning; a separate flag reports bounded cleanup still pending.
Versions 1/2 preserve their bytes and behavior; old peers reject version 3, and
callers must not downgrade the requested effect. No new storage encoding is added.

MQTT metadata RPC 91 read kind 17 discovers replay sources through existing
primary binding rows, including Removed tombstones. Kind 16 retains active-source
semantics. This adds no table/index/row layout or backfill; matched runtimes are
required because older peers reject the new read kind. Workers cannot downgrade
to kind 16 and lose cleanup after the last consumer leaves. Retention decisions
still use the strict consumer index, not these discovery hints.

Message table 2 System 2 materializes a verified format-6 retirement. Its fixed
version-1, checksummed envelope stores two uint64 values: the retirement control
position and the engine-deleted-through cursor. Its counters/digest are resolved
from the independently verified committed journal. Baseline/frontier and bounded
primary/meter range deletion commit atomically; the original System 1 frontier
encoding stays unchanged and may now include retired responsibility. Old writers
cannot interpret this state and must not operate on a pruned database.
Pruned backups use version 3: each channel's replay section begins with an
optional baseline marker, followed by the existing frontier and only its retained
suffix. The archive normalizes cleanup progress to complete, and zero suffix rows
are valid. Version-1/2 export bytes remain unchanged. Import verifies all journal
references and suffix hashes, rejects retirement regression, and publishes the
frontier/marker only after content is installed. Redundant legacy version-2 header
frontiers are validated but no longer installed before their replay section.
Restoring a fully pruned archive may finish equivalent partial cleanup with range
tombstones; physical disk reclamation remains engine compaction's responsibility.
Matching tools and pre-feature rollback backups are required. See
[retired replay storage](../../docs/specs/mqtt-retired-replay-storage.md).

Message-domain table 2 stores immutable MQTT shared replay by source incarnation,
position and content version. Index 2 provides bounded cumulative counters;
System 1 publishes cumulative copied/retired coverage. Neither copying nor a local digest grants
source-release authority. Canonical content normalizes replica-local size hints.
Unpruned replay-bearing binary backups use version 2, validate complete digest chains and
existing target coverage before writes, then rebuild the counting index without
ordinary global-ID entries. Native-only backups retain version 1. Older binaries
and tools cannot restore versions 2/3. Product retirement admission/scheduling,
MQTT-state JSONL and restored-owner fencing remain required before activation.
See [the replay contract](../../docs/specs/mqtt-shared-replay.md).

MQTT groundwork adds metadata tables 22 (`mqtt_session`), 23
(`mqtt_subscription`), 24 (`mqtt_delivery_cursor`), 25 (`mqtt_inflight`), 26
(`mqtt_source_binding`) and 27 (`mqtt_will`), with Slot commands 67–73.
Command 69 operation 3 explicitly initializes empty cancelled preparation after
closed/replaced subscription intent. It preserves existing row/envelope formats
and ordinary Init/Account semantics; older nodes reject it, requiring matched
participants. No index or data backfill is introduced.
Command 69 operation 4 persists qualified backlog receipts under table 24 System 1,
with key-bound fixed envelope version 1. Optional cursor columns 25–27 are an
all-or-none version/head/tail tuple; old rows retain legacy version 0. Upgrade
requires no unadmitted legacy backlog. RPC 91 read kind 18 pins the head alongside
Session/cursor; old peers reject the operation/read. All writers and tools must
match before use; old writers cannot preserve this System state, so rollback
requires a pre-feature backup. Hash-Slot snapshots preserve row/index/System
spans together; MQTT JSONL transfer remains outstanding. See
[qualified accounting](../../docs/specs/mqtt-qualified-accounting.md).
Command 73 atomically resolves Session/Will transitions and records optional
Session column 29 for exact retry; older rows default to an empty receipt.
Generic CAS cannot bypass referenced Will lifecycle. Message index 8 selects the
separate server Will identity derived from publication metadata v2; ordinary
index 4 stays unchanged and nonunique client-number index 3 also covers Wills.
Only new keyed records use index 8, so no legacy backfill is needed. All writers,
deletions and portable imports maintain it; older readers reject v2 metadata.
Product execution/authority wiring remains required. Source bindings
have separate Channel/UID ownership and retain removal tombstones; their proof revisions do
not establish remote authority by themselves. Optional window columns retain
legacy zero defaults; lifecycle CAS preserves delivery-owned counters and
allocators within a generation. Rows use key-bound checksum column envelopes;
commands have bounded, explicitly versioned bodies. Snapshots and inspection
preserve this state. Product MQTT access remains disabled: routing/activation, distributed shared replay, offline
transfer, restored-owner fencing and capability gates are still required before
this feature can be enabled. Details and frozen IDs are in
[the MQTT storage contract](../../docs/specs/mqtt-storage-contract.md).

Message editing adds metadata tables 18–21 (latest content, channel heads,
idempotency results, pending notification checkpoints) and Slot command 66.
Existing message log encodings remain unchanged. These are new-code-only
projections: before activation, current Slot peers must support the edit RPC
format, and operators must keep matching binaries on rejoining nodes. The stored
replica-ID proof does not authorize rolling downgrade or attest the peer build.
Snapshots and offline inspect/export/import/compare include all four tables;
nonempty `meta.message_updates` transfer datasets require a matching CLI.
Rollback after activation requires a compatible pre-feature backup. See
[the rollout and SDK contract](../../docs/specs/message-update-api.md).

The following IDs are part of the stored format and must be treated as permanent:

- table IDs, such as `meta.TableID*` and `message.TableIDMessage`
- row family IDs
- primary and secondary index IDs
- column IDs
- primary-key and index key layouts
- system key IDs
- value codec versions and fixed binary payload layouts

Do not renumber, reuse, reorder with a different meaning, or delete these IDs.
When adding a field, allocate a new column ID and keep all old IDs unchanged.
Prefer assigning the next highest column ID so `rowcodec.Writer` calls can stay
in ascending order.

## Prefer Column Values For New Fields

`pkg/db/internal/rowcodec` is the safest format for compatible field additions:

- values are stored by stable column ID
- unknown columns are ignored by older decoders
- missing columns naturally decode to Go zero values
- values are wrapped with a checksum-bound envelope

For tables already encoded with `rowcodec.CodecColumns`, add fields this way:

1. Add a new column ID constant.
2. Add a `schema.Column` with `Required: false`.
3. Add the column to the correct `schema.Family`.
4. Add the field to the typed row struct.
5. Encode the column in ascending column-ID order.
6. Decode the column in the `switch`; keep the `default` case ignoring unknown
   columns.
7. Normalize missing old-row values after decode when zero is not the desired
   in-memory behavior.
8. Add tests that decode a pre-change payload without the new column.
9. Add tests that append an unknown future column and confirm current decoders
   ignore it when the format is expected to be rolling-upgrade safe.

Use an existing `rowcodec.Type` for rolling-upgrade-safe field additions.
Adding a new encoded value type is a codec change; old scanners may not be able
to skip it.

Do not bump the value envelope version just because an optional column was
added. Keep the same version so old and new code can share the row format.

Only bump a value version for an incompatible encoding change, and then the
decoder must support every older version that can still exist on disk before
the writer starts emitting the new version.

## Required Columns

Never mark a newly added column on an existing table as `Required: true`.
Old rows do not contain that column.

Use `Required: true` only for:

- columns that were already effectively required in all existing rows
- primary key columns already encoded in the durable key
- new tables that have no old rows

If the new behavior needs a non-zero value, compute it in decode/normalize logic
from existing fields or use an explicit backfill before relying on it.

## Fixed Or Raw Binary Values

Some metadata tables still use custom fixed or raw binary payloads, for example
`appendValue*` / `readValue*` helpers in `pkg/db/meta`, catalog values in
`pkg/db/message`, and several system records.

These formats are not automatically forward-compatible. A decoder that requires
`len(rest) == 0` will reject new tail bytes written by a newer binary.

For these tables, choose one of these patterns:

- Best: move only the new field into a separate optional rowcodec/system record,
  leaving the old value bytes untouched.
- Good: convert the value to an envelope/column codec only when old binaries
  cannot read or write the row during the rollout, and keep a dual decoder for
  the old value bytes.
- Acceptable for new-code-only compatibility: append the field at the tail and
  update the new decoder to accept both the old shorter length and the new
  longer length.

Appending to fixed/raw values is not enough for rolling-upgrade compatibility
unless the previous released decoder already ignores unknown tail bytes.

When a custom binary decoder is changed, write tests for all supported lengths
or versions. Keep corruption checks strict for malformed payloads that do not
match any supported version.

## Primary Keys And Existing Indexes

Do not change an existing primary key or index layout in place:

- do not insert a field into an existing key
- do not reorder key parts
- do not change sort direction or uniqueness
- do not change what a key part means

Such changes move rows to different keyspaces and require a new table or a new
index ID plus a migration/backfill plan.

## Adding A Secondary Index

Adding an index is more than adding a schema descriptor. Existing rows will not
have index entries until they are backfilled or rewritten.

Safe index additions need a plan for:

- writing the new index entry on all creates, updates, batch writes, deletes,
  truncates, retention deletes, and snapshot installs that touch the table
- backfilling or lazily repairing index entries for existing rows
- validating uniqueness before a unique index becomes authoritative
- keeping reads correct while the index is incomplete
- deleting old index entries whenever indexed columns change
- including the index keyspace in snapshots or import/export behavior

Do not make a new index the only read path until old rows have been covered.
During rollout, prefer a fallback scan or a verified repair path.

## Message Table Fields

For `pkg/db/message` message rows:

- update `messageRow`, `MessageTable`, `encodeMessageHeader` or
  `encodeMessagePayload`, `decodeMessageHeaderColumn`, and materialization logic
  such as `messageFromRow`
- keep `messageValueVersion` unchanged for optional rowcodec columns
- keep header and payload families separate unless the new field really belongs
  with payload bytes
- update append/apply-fetch/compat paths that construct `messageRow`
- update every index maintenance path if the field participates in an index
- update inspect output only if the field should be visible to diagnostics

The message reader combines header and payload families by sequence. A new field
must not make old messages fail `validateMaterializedMessageRow`.

## Metadata Table Fields

For `pkg/db/meta` table-runtime tables:

- update the table's `TableSpec` columns and families
- update the typed struct and English field comments for exported fields
- update `EncodeValue` / `EncodeValueWithKey`
- update `DecodeValue` / `DecodeValueWithKey`
- update `Validate` only for invariants that old rows can satisfy
- update typed methods, batch overlays, cache invalidation, and compatibility
  wrappers when they read or write the field
- update inspect row mapping if diagnostics should expose the field

For rowcodec-backed metadata values, normalize missing columns after decode.
For custom binary metadata values, follow the fixed/raw rules above.

## Snapshots And Import

Metadata hash-slot snapshots export raw row, index, and system keyspaces. Field
additions inside an existing row value usually do not need snapshot version
changes, but the imported value must be decodable by the receiving binary.

Check these cases:

- a snapshot produced before the field existed imports into new code
- a snapshot produced by new code imports into any old binary that may still be
  supported during rollback
- preserving imports still preserve or replace the intended table spans
- new indexes or system records are included in the appropriate keyspace

Do not bump `slotSnapshotVersion` for an ordinary optional field. Bump it only
when the snapshot payload format itself changes.

## Tests To Add

At minimum, add focused tests near the changed package:

- schema validation still passes
- old encoded row/value without the new field decodes successfully
- new encoded row/value round-trips with the field
- unknown future rowcodec columns are ignored when expected
- default/normalize behavior for missing fields is explicit
- any changed index is written, read, deleted, and repaired/backfilled as planned
- snapshot/export/import behavior covers the new field when relevant
- legacy compatibility wrappers still preserve the field or intentionally ignore
  it with documented behavior

Keep these as unit tests unless the change requires real multi-node rollout
behavior. Longer rollout or backfill tests should use the integration tag.

## Review Checklist

Before merging an existing-table field add, verify:

- no durable ID was reused or renumbered
- the field is optional for old rows
- old rows decode with a well-defined default
- new rows do not become corrupt for supported rollback or mixed-version readers
- primary/index key layouts were not changed in place
- all write paths populate or intentionally omit the field
- all read paths handle both missing and present values
- inspect, snapshots, caches, and compatibility adapters are updated when needed
- related tests were run, at least `go test ./pkg/db/...`
