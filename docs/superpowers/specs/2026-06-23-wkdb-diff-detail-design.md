# WKDB Diff Detail Design

## Goal

Add an optional `wkdb diff --detail` mode that pinpoints the first bounded row-level differences between two node-local WKDB stores after migration.

The existing `wkdb diff` already answers whether the source and target are equal for WKDB Import Bundle v1 data sets. `--detail` should make mismatches actionable by identifying which logical row differs, without turning the default diff path into a heavy row-by-row comparator.

## Non-Goals

- No cross-node aggregation or global cluster view.
- No online consistency snapshot.
- No repair, merge, mutation, or compensation bundle generation.
- No comparison of Raft, controller, runtime, migration-task, or plugin state.
- No full external diff format that prints entire source and target rows by default.

## Command

Default summary behavior stays unchanged:

```bash
wkdb --hash-slot-count 256 diff \
  --source-data-dir ./node-old \
  --target-data-dir ./node-new
```

Detailed mismatch localization is explicit:

```bash
wkdb --hash-slot-count 256 diff \
  --source-data-dir ./node-old \
  --target-data-dir ./node-new \
  --mode full \
  --detail \
  --max-mismatches 20
```

`--max-mismatches` defaults to `20`. A value of `0` uses the default; negative values are CLI config errors. It bounds retained detail rows, not scanned rows.

## Scope

`--detail` covers every data set represented by WKDB Import Bundle v1:

- `meta.users`
- `meta.devices`
- `meta.channels`
- `meta.subscribers`
- `meta.user_channel_memberships`
- `meta.conversations`
- `meta.channel_latest`
- `message.channels`
- `message.messages`

This matches the current `VerifyStores` summary scope. Future bundle versions can add new detail comparators with explicit tests.

## Recommended Approach

Use summary-first verification:

1. Run the current digest-based `VerifyStores` pass.
2. If `--detail` is false, return the current report.
3. If `--detail` is true and every data set is equal, return the current report with no extra scan.
4. If `--detail` is true and one or more data sets differ, run row-level streaming compare only for the mismatched data sets.

This keeps routine migration checks fast and gives operators precise mismatch locations only when needed. The cost is that mismatched data sets are scanned a second time in detail mode, which is acceptable because detail mode is for diagnosis rather than the default health check path.

## Transfer API

Extend `pkg/db/transfer` types:

```go
type VerifyOptions struct {
    HashSlotCount uint16
    PageSize      int
    Mode          VerifyMode
    MaxMismatches int
    Detail        bool
}

type VerifyMismatch struct {
    Scope  string            `json:"scope"`
    Kind   VerifyMismatchKind `json:"kind"`
    Key    map[string]any     `json:"key,omitempty"`
    Detail string             `json:"detail"`
}

type VerifyMismatchKind string

const (
    VerifyMismatchDigest        VerifyMismatchKind = "digest_mismatch"
    VerifyMismatchMissingSource VerifyMismatchKind = "missing_source"
    VerifyMismatchMissingTarget VerifyMismatchKind = "missing_target"
    VerifyMismatchRow           VerifyMismatchKind = "row_mismatch"
)
```

Existing summary mismatches use `digest_mismatch` and keep `Detail` compatible with current text. Detail mismatches add structured `Key` fields for operator and script consumption.

## CLI Changes

Extend `cmd/wkdb/diff.go` local flags:

- `--detail`
- `--max-mismatches N`

`diffCommandFlags.transferOptions` passes `Detail` and `MaxMismatches` into `transfer.VerifyOptions`.

Exit codes remain unchanged:

- `0`: compared successfully and equal.
- `2`: compared successfully and mismatched.
- `1`: invalid CLI config or path/open error.
- `3`: unexpected internal failure.

## Detail Comparison Model

Each detail comparator is a merge over sorted streams. It should not load a full table, channel, or store into memory.

The comparator reads a bounded page from source and target, compares current rows by canonical row key, advances the lower key on missing-row cases, and advances both streams on equal keys. It stops collecting mismatch details once `MaxMismatches` is reached, but it may stop scanning immediately after the bound is reached because the report only promises the first bounded examples.

### Metadata Tables

Meta detail comparison scans one table at a time, slot by slot:

1. Iterate `hash_slot` from `0` to `HashSlotCount - 1`.
2. For each slot, page source and target with `meta.InspectScan` using `HashSlotSet=true`.
3. Compare rows in inspect primary-key order.

Each meta table has a canonical key:

- `meta.users`: `hash_slot`, `uid`
- `meta.devices`: `hash_slot`, `uid`, `device_flag`
- `meta.channels`: `hash_slot`, `channel_id`, `channel_type`
- `meta.subscribers`: `hash_slot`, `channel_id`, `channel_type`, `uid`
- `meta.user_channel_memberships`: `hash_slot`, `uid`, `channel_id`, `channel_type`
- `meta.conversations`: `hash_slot`, `uid`, `kind`, `channel_id`, `channel_type`
- `meta.channel_latest`: `hash_slot`, `channel_id`, `channel_type`

Rows with the same key are compared through the same canonical JSON representation used for digesting. A byte-for-byte canonical mismatch becomes `row_mismatch`.

### Message Channel Catalog

`message.channels` detail comparison walks `message.InspectChannels` in `channel_key` order.

Canonical key:

- `channel_key`

Rows with the same key compare `channel_key`, `channel_id`, and `channel_type`.

### Message Rows

`message.messages` detail comparison uses the message catalog to avoid building a global channel map:

1. Walk source and target channel catalogs by `channel_key`.
2. If a channel exists only on one side, emit a missing channel mismatch under `message.channels` or `message.messages` and advance that side.
3. If a channel exists on both sides, walk `message.InspectMessages` for that channel in `message_seq` order.
4. Compare rows by `channel_key`, `message_seq`.

Canonical key:

- `channel_key`
- `message_seq`

Summary mode row comparison uses:

- `message_seq`
- `message_id`
- `client_msg_no`
- `from_uid`
- `server_timestamp_ms`
- `payload_hash`
- `payload_size`

Full mode additionally includes raw `payload` bytes.

This preserves the current mode semantics: summary localizes payload-hash/size differences, while full verifies raw payload bytes.

## Output

Table output keeps the existing first summary line:

```text
equal=false mode=full meta=7 message=2 mismatches=3
```

When detail mismatches exist, append one line per mismatch:

```text
mismatch scope=meta.users kind=row_mismatch key=hash_slot=231,uid=smoke-u1 detail=different row content
mismatch scope=message.messages kind=missing_target key=channel_key=smoke-g1:2,message_seq=10 detail=source row missing from target
```

JSON output remains `VerifyReport`, with structured `mismatches`.

JSONL output keeps existing record types and emits one structured `mismatch` record per detail:

```json
{"type":"mismatch","mismatch":{"scope":"meta.users","kind":"row_mismatch","key":{"hash_slot":231,"uid":"smoke-u1"},"detail":"different row content"}}
```

## Error Handling

- Unknown `--mode` remains a config error before stores are opened.
- Negative `--page-size` or `--max-mismatches` is rejected by the CLI as a config error. `--max-mismatches 0` uses the transfer-layer default.
- Missing source or target storage paths remain config errors.
- Inspect API failures return internal or validation-flavored errors according to the current transfer error mapping.
- Detail compare must preserve context in errors, including scope, hash slot, and channel key where applicable.

## Performance Constraints

- Default `wkdb diff` must not become row-level compare.
- `--detail` must use bounded pages and avoid table-wide maps.
- Metadata compare holds only source and target pages for one table/slot.
- Message compare holds only source and target catalog pages and one channel's message pages.
- `--max-mismatches` bounds retained detail examples.
- Full mode may read payload bytes, but only because the operator requested strict verification.

## Testing

Transfer tests:

- Equal roundtrip with `Detail=true` returns no detail mismatches.
- Meta row mismatch returns `row_mismatch` with table key, for example `meta.users uid`.
- Meta missing row returns `missing_target` or `missing_source`.
- Message catalog missing channel returns a `message.channels` mismatch with `channel_key`.
- Message row mismatch returns `message.messages row_mismatch` with `channel_key` and `message_seq`.
- Message missing row returns `missing_target` or `missing_source`.
- `MaxMismatches` bounds detail rows.

CLI tests:

- `wkdb diff --detail` returns exit `0` and no detail rows for equal stores.
- `wkdb diff --detail` returns exit `2` and prints structured mismatch keys for unequal stores.
- JSON and JSONL outputs include `kind` and `key`.

Smoke script update:

- Extend `scripts/wkdb-diff-smoke.sh` to run `wkdb diff --detail` for the mismatch target and assert `diff mismatch ok` plus a key-bearing mismatch line.

## Documentation

Update:

- `cmd/wkdb/README.md`
- `docs/wiki/operations/wkdb-readonly-cli.md`

Document:

- `--detail`
- `--max-mismatches`
- structured mismatch kinds
- performance cost of detail mode
- examples for table and JSONL output

## Rollout Notes

Implement this as an additive extension to the existing `wkdb diff` command. Existing command lines and existing output summary should remain compatible. Scripts that only check `equal=true` or `equal=false` should keep working.
