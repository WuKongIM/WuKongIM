# WKDB Diff Verify Design

## Goal

Add a local offline `wkdb diff` command that compares two node-local WKDB data directories and reports whether an export/import migration preserved the data covered by WKDB Import Bundle v1.

The command is for operator confidence after migration. It must stay node-local, read-only against both source and target stores, and avoid introducing cluster-level semantics into `cmd/wkdb`.

## Non-Goals

- No cross-node aggregation.
- No online consistency snapshot.
- No repair or mutation.
- No Raft, controller, runtime, migration-task, or plugin-state comparison in the first version.
- No probabilistic sampling mode in the first version.

## Command

```bash
wkdb --hash-slot-count 256 diff \
  --source-data-dir ./node-old \
  --target-data-dir ./node-new \
  --mode summary
```

Strict message-content verification:

```bash
wkdb --hash-slot-count 256 diff \
  --source-data-dir ./node-old \
  --target-data-dir ./node-new \
  --mode full
```

The command also supports explicit paths:

```bash
wkdb --hash-slot-count 256 diff \
  --source-meta-path ./old/data \
  --source-message-path ./old/channellog \
  --target-meta-path ./new/data \
  --target-message-path ./new/channellog
```

Global `--format table|json|jsonl` controls report rendering.

## Scope

The first version compares the data sets represented by WKDB Import Bundle v1:

- `meta.user`
- `meta.device`
- `meta.channel`
- `meta.subscriber`
- `meta.user_channel_membership`
- `meta.conversation`
- `meta.channel_latest`
- `message.channels`
- `message.message`

It intentionally does not compare `channel_runtime_meta`, `plugin_binding`, `channel_migration`, or `hashslot_migration`.

## Architecture

Add `pkg/db/transfer/verify.go` with:

```go
type VerifyMode string

const (
    VerifyModeSummary VerifyMode = "summary"
    VerifyModeFull    VerifyMode = "full"
)

type VerifyOptions struct {
    HashSlotCount uint16
    PageSize      int
    Mode          VerifyMode
}

type VerifyReport struct {
    Equal        bool
    Mode         VerifyMode
    HashSlotCount uint16
    Meta         []VerifyDatasetReport
    Message      MessageVerifyReport
}

func VerifyStores(ctx context.Context, source, target *inspect.Store, opts VerifyOptions) (VerifyReport, error)
```

The transfer layer uses existing read-only `inspect.Store` handles and only calls:

- `meta.InspectScan`
- `message.InspectChannels`
- `message.InspectMessages`

`cmd/wkdb/diff.go` is only CLI parsing, store opening, exit-code mapping, and report rendering.

## Comparison Semantics

### Metadata

For each supported meta table, scan source and target in deterministic order by:

1. `hash_slot`
2. inspect primary-key order

For each table, compute:

- `source_rows`
- `target_rows`
- `source_digest`
- `target_digest`
- `equal`

The digest is a streaming SHA256 over canonical JSON rows, including `hash_slot` so wrong slot placement cannot hide behind identical row payloads.

Metadata verification does not load full tables into memory.

### Message Catalog

Compare `message.channels` in catalog-key order with:

- `source_channels`
- `target_channels`
- `source_digest`
- `target_digest`
- `equal`

The digest includes `channel_key`, `channel_id`, and `channel_type`.

### Messages: Summary Mode

For every channel in the source and target catalog streams, compare per-channel summaries:

- `message_count`
- `min_seq`
- `max_seq`
- digest of `(message_seq, message_id, client_msg_no, from_uid, server_timestamp_ms, payload_hash, payload_size)`

Summary mode intentionally does not hash raw payload bytes. It is fast enough for routine migration checks and still detects sequence gaps, missing rows, ID mismatches, timestamp drift, and payload-hash/size differences.

### Messages: Full Mode

Full mode uses the summary fields and also includes raw `payload` bytes in the message digest. This is the strict migration acceptance mode. It can scan large message stores for a long time, but it is deterministic and bounded-memory.

## Mismatch Reporting

The report should identify the first bounded mismatch examples without accumulating all differences:

- table name
- channel key, when relevant
- source and target row counts
- digest mismatch
- first missing or extra channel key, when detected by parallel catalog walk

First version keeps examples small, with a default maximum of 20 mismatch details.

## Exit Codes

- `0`: compared successfully and source equals target.
- `2`: compared successfully and found mismatches.
- `1`: invalid CLI config or path/open error.
- `3`: unexpected internal failure.

This matches current `wkdb` command classes: config error, query/data mismatch, internal error.

## Performance Constraints

- Bounded memory: scan pages are bounded by `--page-size`.
- No table-wide maps for meta tables or message rows.
- Message channel comparison uses sorted catalog streams and compares one channel at a time.
- Full mode is allowed to scan all payload bytes; summary mode should avoid raw payload hashing.

## Testing

Unit tests cover:

- identical stores return `Equal=true`.
- metadata mismatch returns `Equal=false`.
- missing message channel returns `Equal=false`.
- summary mode detects payload hash/size changes without raw payload digest.
- full mode detects raw payload changes.
- CLI exits `0` for equal and `2` for mismatch.

Smoke test:

1. Build seed data through `scripts/wkdb-import-smoke.sh`.
2. Export and import roundtrip.
3. Run `wkdb diff --mode full`; expect success.
4. Create a modified target store; run `wkdb diff`; expect exit code `2`.

## Documentation

Update:

- `cmd/wkdb/README.md`
- `docs/wiki/operations/wkdb-readonly-cli.md`

Document:

- source/target path flags
- summary versus full mode
- exit codes
- offline/read-only semantics
- non-goals
