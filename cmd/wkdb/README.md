# wkdb

`wkdb` is a local offline CLI for one WuKongIM node data directory. It has two
operation classes:

- Read-only inspection: `query` and `repl` open metadata/message stores in
  read-only mode.
- Offline export: `export` opens source metadata/message stores in read-only
  mode and writes a WKDB Import Bundle v1 to an output directory.
- Offline import: `import` validates a WKDB Import Bundle v1 and explicitly
  writes it into an offline target store.

Full operations guide: [docs/wiki/operations/wkdb-readonly-cli.md](../../docs/wiki/operations/wkdb-readonly-cli.md)

Inspection examples:

```bash
wkdb --data-dir ./node-1 query "show tables"
wkdb --data-dir ./node-1 --hash-slot-count 256 query "select * from meta.user where uid='u1'"
wkdb --data-dir ./node-1 --hash-slot-count 256 query "select * from meta.user limit 100"
wkdb --data-dir ./node-1 query "select * from message.channels limit 20"
wkdb --data-dir ./node-1 query "select * from message.message where channel_key='g1:2' limit 50"
```

Import examples:

```bash
# Global flags must appear before the command; import flags appear after it.
wkdb --data-dir ./node-new --hash-slot-count 256 import --input ./wkdb-dump --dry-run
wkdb --data-dir ./node-new --hash-slot-count 256 import --input ./wkdb-dump --require-empty
```

Export examples:

```bash
# Export the current node-local store into the same bundle format accepted by import.
wkdb --data-dir ./node-1 --hash-slot-count 256 export --output ./wkdb-dump
wkdb --data-dir ./node-1 --hash-slot-count 256 export --output ./wkdb-dump --overwrite
```

Diff examples:

```bash
# Verify that two offline node-local stores contain the same bundle-v1 data.
wkdb --hash-slot-count 256 diff --source-data-dir ./node-old --target-data-dir ./node-new
wkdb --hash-slot-count 256 diff --source-data-dir ./node-old --target-data-dir ./node-new --mode full
```

`summary` mode is the default and compares rows plus payload checksums. `full`
mode also hashes message payload bytes. `diff` opens both source and target
stores in read-only mode. Exit `0` means equal; exit `2` means a verified
mismatch was found.

Common flags:

- `--config ./wukongim.toml` reads the storage paths and hash-slot count from a TOML config file. `WK_` environment variables override file values.
- `--data-dir ./node-1` derives metadata from `./node-1/data` and message logs from `./node-1/channellog`.
- `--meta-path` and `--message-path` override derived or configured paths.
- `--format table|json|jsonl` selects the output format.

Metadata rows are stored by hash slot. When a query includes a table partition key such as `uid` or `channel_id`, `wkdb` derives the hash slot automatically from `--hash-slot-count` or `--config`. Queries without a partition key perform a bounded local scan over this node's files in `hash_slot, primary_key` order.

`limit` is the total number of rows returned by the query. It is not applied once per hash slot. `offset` is not supported; use the returned cursor for pagination:

```bash
wkdb --data-dir ./node-1 --hash-slot-count 256 query "select * from meta.user limit 100 cursor '<next_cursor>'"
```

`jsonl` output writes one JSON object per row and then a final stats record:

```json
{"uid":"u1"}
{"type":"stats","stats":{"has_more":true,"next_cursor":"..."}}
```

`wkdb` is local-only. `query`, `repl`, and the source side of `export` open the
metadata and message stores in read-only mode, do not contact cluster peers, and
do not return a global cluster view. `export` writes only to its `--output`
directory. `import` is the only command that writes WKDB storage; it does not
connect to cluster nodes and is intended for fresh/offline target directories.

WKDB Import Bundle v1 is a directory with one manifest and JSONL data files:

```text
wkdb-dump/
  manifest.json
  meta/
    users.jsonl
    devices.jsonl
    channels.jsonl
    subscribers.jsonl
    user_channel_memberships.jsonl
    conversations.jsonl
    channel_latest.jsonl
  message/
    channels.jsonl
    messages-000001.jsonl
```

`manifest.json` uses `format="wkdb-import-bundle"`, `version=1`,
`hash_slot_count`, and `files[]` entries with `path`, `kind`, `rows`, and
`sha256`. Supported file kinds are `meta.users`, `meta.devices`,
`meta.channels`, `meta.subscribers`, `meta.user_channel_memberships`,
`meta.conversations`, `meta.channel_latest`, `message.channels`, and
`message.messages`.

The first `export` implementation intentionally does not aggregate data across
cluster nodes, create online consistency snapshots, export incremental changes,
compress the bundle, or include Raft/controller/runtime state. Run it against a
stopped node, a filesystem snapshot, or a copied node data directory when exact
source consistency matters.
