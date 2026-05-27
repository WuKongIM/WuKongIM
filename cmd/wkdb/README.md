# wkdb

`wkdb` is a read-only offline inspection CLI for one WuKongIM node data directory.

Examples:

```bash
wkdb --data-dir ./node-1 query "show tables"
wkdb --data-dir ./node-1 --hash-slot-count 256 query "select * from meta.user where uid='u1'"
wkdb --data-dir ./node-1 --hash-slot-count 256 query "select * from meta.user limit 100"
wkdb --data-dir ./node-1 query "select * from message.channels limit 20"
wkdb --data-dir ./node-1 query "select * from message.message where channel_key='g1:2' limit 50"
```

Common flags:

- `--config ./wukongim.conf` reads the storage paths and hash-slot count from a `KEY=value` config file. `WK_` environment variables override file values.
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

`wkdb` is local-only. It opens the metadata and message stores in read-only mode, does not contact cluster peers, and does not return a global cluster view.
