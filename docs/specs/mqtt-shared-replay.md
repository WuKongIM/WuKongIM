# MQTT shared replay replica storage

The approved seven-table design places replay bodies in the message data plane.
`pkg/slot/BOUNDARY.md` excludes message logs from Slot metadata replication.
This table is therefore message-domain table **2**, `mqtt_replay_message`, in the
original Channel partition. Metadata table IDs 22–27 remain unchanged. No Slot
command is added for message bodies.

## Durable contract

Primary index 1, family 0: `(source_generation, source_position, content_version)`.
Content version 1 canonically encodes the original message row, including native
flags, timestamps, properties and payload. The non-content `PayloadSize` hint is
set to actual payload length; append/fetch paths may store different hints for
the same publication. Its inner checksum remains bound
to the original message key. The outer version-1 checksummed column envelope uses
columns 4–10 for MessageID, accounted bytes, cumulative accounted bytes, content
SHA-256, prefix digest, original row bytes and cumulative stored bytes. Key columns
are 1–3. Accounted bytes are original payload plus publication metadata. Stored
bytes count original row envelopes; physical engine amplification is separate.

Table 2 System 1 holds one checksummed incarnation/start/copy frontier, cumulative
accounted/stored bytes and prefix digest. The digest chain binds the complete
source key, content version, original bytes and both cumulative counters. It is
independent of copy batch boundaries. Each bounded synchronous copy commits rows
and frontier together. Index **2** stores cumulative accounted/stored bytes and
the prefix digest under `(generation, position)`, in a fixed 48-byte checksummed
value. Range metering reads two small endpoints, independently of body size;
copy and restore maintain the index atomically with each row. Backups rebuild
this derived index from verified rows. No per-session body copy is created.
There is no global MessageID index entry for this table.

Copies require an installed protected source and committed checkpoint, and read
exact contiguous original positions while holding the Channel append fence.
Limits are 256 rows and 16 MiB of original envelopes. Missing positions, changed
incarnations, gaps, overlaps that extend the frontier, oversized first rows and
uncommitted ranges fail. A wholly copied retry reads the existing immutable rows,
including after source history removal. Reads own their bytes. No copy advances
message System 12: a local digest is not a quorum receipt.

Portable message backup version 2 includes the replay frontier and contiguous
rows after each Channel's ordinary messages. Native-only exports retain version 1.
The pinned view, source generation and selected HW must agree. Both import APIs
validate the complete new stream before mutation and restore replay rows without
touching ordinary unique indexes. Restored runtime authority is still fenced by
the future activation protocol. Older tools must reject version 2, not omit it.

[Retired replay storage](mqtt-retired-replay-storage.md) now retains a committed
whole-anchor baseline and separately bounded deletion progress. Only the suffix
above that baseline remains readable/copyable; cumulative counters and hashes do
not restart. Pruned archives use version 3 and may contain zero suffix rows.
Restore publishes the baseline/frontier atomically after the suffix, including
deferring the redundant frontier in a legacy version-2 header. Product retirement
admission and scheduling remain separate prerequisites for activation.

The [bounded transfer primitive](mqtt-replay-transfer.md) now exports pinned
pages and atomically imports against installed committed log identities and an
independently accepted complete-content prefix. Native entry digests omit some
row fields, so sender-supplied page hashes alone cannot authenticate recovery.
This adds no durable schema or wire-format change.

The [Channel preparation facade](mqtt-replay-admission.md) now orders bounded
copy/read admission under recovered leader/epoch/route fences, captures committed
HW and executes through checkpoint workers. Previously copied prefixes return
existing pages before extending; a lost short-page reply remains retryable.
The caller still needs fresh cluster authority and quorum copy coordination.

No generic delete/TTL is provided. Consumer-proof GC, cross-node replication,
learner/migration orchestration, authority recovery and MQTT-state JSONL export remain
required before product activation. This replica storage API alone cannot
authorize SUBACK, advance a distributed copy frontier or open the MQTT listener.

## Failure inventory written before code

1. Copying the same MessageID duplicates/conflicts with the ordinary global index;
   a retry changes content/counters or depends on already deleted source history.
2. Copy admits an uncommitted row, skips a missing position, starts behind lost
   history, overlaps an extension, changes incarnation or silently truncates an
   oversized first row. Failed multi-row copy leaves partial content/frontier.
3. Source cleanup, edit overlays, restart or lease reclamation changes replay;
   returned buffers alias storage; bounded reads or metering exceed their limits.
4. Segmentation or replica-local size hints change prefix proofs; counted bytes omit publication properties;
   counter overflow or corrupt endpoints produces plausible quota evidence.
   Metering reads large bodies or a missing counting index silently falls back.
5. Envelope corruption, wrong-key checksums, absent required columns, unknown
   versions, malformed embedded content or mismatched hashes is accepted.
6. Backup drops replay after ordinary history is gone, uses a cut below replay,
   changes under concurrent append, loses its frontier, or reinstalls duplicate
   global IDs. Corrupt input mutates the target before complete validation.
7. Replay backup/import or local copy is falsely treated as distributed durability;
   mixed writers and unproven consumer GC remain explicitly unsupported.

## Frozen context

Source `7d53fd4c4`; SHA-256:

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `pkg/db/FLOW.md`: `d90cb020a5d487214b0dd3fefdc49533d50c7f37c92e217aa139141c9b85761e`
- `pkg/db/message/FLOW.md`: `56e9a97bd59e4161cd95ce7cc7d91b1a6dbf319b801c314b037e4dfe70ab8bd6`
