# Wkdb Import Bundle Design

## Goal

Add an explicit `wkdb import` workflow that imports node-local metadata and
message data into the current `pkg/db` storage from a versioned transfer bundle.
The old database side does not need to share storage internals with the new
database. It only needs to export records in the `WKDB Import Bundle v1` format
defined here.

## Scope

- Define `WKDB Import Bundle v1` as the canonical offline exchange format.
- Add a write-capable `wkdb import` subcommand that is separate from the current
  read-only `query` and `repl` commands.
- Import metadata through current typed `pkg/db/meta` APIs so indexes, cache
  invalidation, subscriber counts, and conversation semantics are rebuilt by the
  current code.
- Import message logs through current typed `pkg/db/message` APIs so primary
  rows, payload rows, message-id indexes, client-message indexes, idempotency
  indexes, catalog rows, and LEO state are rebuilt by the current code.
- Support `--dry-run` validation with no writes.
- Default to importing into an empty target store only.
- Document the bundle format so older versions can implement exporters without
  understanding the current Pebble key layout.

Out of scope for the first version:

- Reading arbitrary old WuKongIM storage layouts.
- Merging into a non-empty target database.
- Online import while a WuKongIM process is using the same store.
- Importing Raft logs, controller state, presence runtime state, online session
  state, channel migration tasks, hash-slot migration tasks, or node runtime
  leadership state.
- Repairing semantically inconsistent source data. The importer validates and
  rejects invalid bundles instead of guessing fixes.

## Command Shape

```bash
wkdb import --data-dir ./node-new --input ./wkdb-dump --hash-slot-count 256 --dry-run
wkdb import --data-dir ./node-new --input ./wkdb-dump --hash-slot-count 256 --require-empty
```

`wkdb import` uses the existing config resolution rules for `--config`,
`--data-dir`, `--meta-path`, `--message-path`, and `--hash-slot-count`. It opens
the target store for writes, unlike `query` and `repl`, which continue to use
the read-only inspect store.

The first implementation must require `--require-empty` unless `--dry-run` is
set. The empty check rejects a target when either metadata rows or message
catalog entries already exist.

## Bundle Layout

```text
wkdb-dump/
  manifest.json
  meta/users.jsonl
  meta/devices.jsonl
  meta/channels.jsonl
  meta/subscribers.jsonl
  meta/user_channel_memberships.jsonl
  meta/conversations.jsonl
  meta/channel_latest.jsonl
  message/channels.jsonl
  message/messages-000001.jsonl
  message/messages-000002.jsonl
```

`manifest.json` is required:

```json
{
  "format": "wkdb-import-bundle",
  "version": 1,
  "hash_slot_count": 256,
  "created_at_ms": 1710000000000,
  "files": [
    {
      "path": "meta/users.jsonl",
      "kind": "meta.users",
      "rows": 1000,
      "sha256": "hex-encoded-sha256"
    },
    {
      "path": "message/messages-000001.jsonl",
      "kind": "message.messages",
      "rows": 500000,
      "sha256": "hex-encoded-sha256"
    }
  ]
}
```

Rules:

- Paths are relative to the bundle root and must not escape the bundle
  directory.
- Files are newline-delimited JSON. Each line is one JSON object.
- Binary payloads use base64 fields with a `_b64` suffix.
- Unsigned 64-bit values may be encoded as JSON numbers when safe or as decimal
  strings. The importer accepts both and writes exact `uint64` values.
- `hash_slot` is required for UID-owned and channel-owned metadata rows. The
  importer recomputes the expected hash slot from the current hash-slot count
  and rejects mismatches.
- Message files may be split arbitrarily, but records must be globally ordered
  by `channel_key`, then `message_seq`.

## Record Contracts

### Metadata

`meta.users.jsonl`

```json
{"hash_slot":12,"uid":"u1","token":"t1","device_flag":0,"device_level":0}
```

`meta.devices.jsonl`

```json
{"hash_slot":12,"uid":"u1","device_flag":1,"token":"mobile-token","device_level":0}
```

`meta.channels.jsonl`

```json
{"hash_slot":34,"channel_id":"g1","channel_type":2,"ban":0,"disband":0,"send_ban":0,"allow_stranger":1,"large":1,"subscriber_mutation_version":88}
```

`meta.subscribers.jsonl`

```json
{"hash_slot":34,"channel_id":"g1","channel_type":2,"uid":"u1"}
```

`meta.user_channel_memberships.jsonl`

```json
{"hash_slot":12,"uid":"u1","channel_id":"g1","channel_type":2,"created_at_ms":1710000000000,"updated_at_ms":1710000000000}
```

`meta.conversations.jsonl`

```json
{"hash_slot":12,"uid":"u1","kind":"normal","channel_id":"g1","channel_type":2,"read_seq":10,"deleted_to_seq":0,"active_at":1710000000000,"updated_at":1710000000000,"sparse_active":false}
```

`kind` is required and must be `normal` or `cmd`. The importer maps those values
to the current unified conversation table and must not infer CMD state from a
channel-name suffix.

`meta.channel_latest.jsonl`

```json
{"hash_slot":34,"channel_id":"g1","channel_type":2,"last_message_seq":99,"last_message_id":10099,"last_client_msg_no":"c99","last_from_uid":"u2","last_server_timestamp_ms":1710000009999,"last_payload_b64":"..."}
```

### Messages

`message/channels.jsonl`

```json
{"channel_key":"g1:2","channel_id":"g1","channel_type":2}
```

`message/messages-*.jsonl`

```json
{"channel_key":"g1:2","message_seq":1,"message_id":10001,"client_msg_no":"c1","from_uid":"u1","server_timestamp_ms":1710000000000,"payload_b64":"..."}
```

Rules:

- `message_seq` starts at 1 for each channel and must be contiguous.
- Duplicate `message_id` values inside one channel are rejected.
- Duplicate `(from_uid, client_msg_no)` pairs inside one channel are rejected
  when both fields are non-empty.
- `payload_b64` is required and may encode an empty byte slice.
- The importer writes batches with `message.ChannelLog.ApplyFetch` and
  `AppendTrustedContiguous` semantics so the source sequence is preserved.

## Import Flow

1. Load and validate `manifest.json`.
2. Resolve target paths using existing `wkdb` config rules.
3. For `--dry-run`, open no write handles and validate all files with the same
   parser and semantic checks used by real import.
4. For real import, open the current `pkg/db.NodeStore`.
5. Reject non-empty targets unless the future command explicitly supports merge.
6. Import users and devices.
7. Import channels before subscribers.
8. Import subscribers grouped by `(hash_slot, channel_id, channel_type)`.
   Subscriber rows are applied through existing subscriber APIs so
   `SubscriberCount` is rebuilt by the current storage code.
9. Import user channel memberships, conversations, and channel latest rows.
10. Import message channels and message records. Message files are streamed;
    each channel accumulates bounded batches and flushes through `ApplyFetch`.
11. Emit progress and a final summary to stderr:
    records validated, records written, bytes read, channels imported, and
    message rows imported.

## Failure Semantics

- Any manifest, checksum, path, type, hash-slot, ordering, duplicate, or storage
  error fails the command with a non-zero exit code.
- Real import is not an all-database transaction. If a write fails after earlier
  writes, the target directory is considered partially imported and must be
  discarded or restored from backup before retry.
- To reduce partial-import risk, the recommended operator workflow is to import
  into a fresh offline data directory, run validation queries, then start the
  node with that directory.
- The importer never contacts cluster peers and never tries to build a global
  cluster view.
- The importer never rewrites `wukongim.conf`.

## Performance Notes

- JSONL is streamed; the importer must not load all rows into memory.
- Subscriber import groups one channel at a time so large groups can be flushed
  in bounded chunks.
- Message import flushes bounded per-channel batches. V1 uses a conservative
  constant such as 1024 records or a payload-byte cap.
- Hash-slot validation is O(rows) and uses the same hash-slot function as the
  current metadata writers.
- XLSX is intentionally not the canonical format because it is not suitable for
  very large groups or message histories.

## Documentation Updates

- Update `cmd/wkdb/README.md` to say `query` and `repl` are read-only, while
  `import` is an explicit offline write operation.
- Add an operations document section with the bundle layout, old-version export
  obligations, import examples, dry-run workflow, and recovery guidance.
- Keep any new exported Go structs and fields documented with English comments.

## Test Plan

- Manifest tests:
  - accepts a valid manifest
  - rejects unknown format version
  - rejects path traversal
  - rejects checksum mismatch
  - rejects mismatched hash-slot count
- Parser tests:
  - accepts JSON numbers and decimal strings for `uint64`
  - rejects missing required fields
  - decodes base64 payloads exactly
  - rejects unknown conversation kind
- Import dry-run tests:
  - validates a complete bundle without creating target Pebble files
  - returns the same validation errors as real import
- Import write tests:
  - imports users, devices, channels, subscribers, memberships,
    conversations, channel latest, and messages into a fresh store
  - verifies imported rows through `pkg/db/inspect`
  - verifies subscriber counts through metadata channel reads
  - verifies message-id and idempotency lookups through message APIs
- Safety tests:
  - rejects non-empty target stores
  - rejects out-of-order message rows
  - rejects non-contiguous message sequences
  - rejects duplicate message IDs within a channel
  - rejects hash-slot mismatches

Focused command:

```bash
go test ./cmd/wkdb ./pkg/db/transfer ./pkg/db/...
```

## Self Review

- The design defines the import format rather than depending on old storage
  internals.
- The import path is explicit and separate from the existing read-only query
  path.
- The first version defaults to empty-target import, which avoids ambiguous
  merge semantics.
- Metadata and messages are imported through typed current APIs so indexes and
  derived fields are rebuilt by current code.
- Runtime and cluster-state data are intentionally out of scope because they are
  not portable node-local business data.
