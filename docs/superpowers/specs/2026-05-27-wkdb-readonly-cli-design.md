# wkdb Read-Only CLI Design

## Summary

Add a read-only `wkdb` troubleshooting CLI for opening one node data directory
offline and inspecting WuKongIM metadata and message stores with a small
SQL-like query surface.

The first version is an observation tool only. It must not modify data, repair
indexes, rewrite raw Pebble keys, or expose write-oriented APIs.

## Goals

- Open a node data directory or explicit storage paths in read-only mode.
- Let operators inspect metadata tables and channel message logs without
  knowing the physical key layout.
- Hide hash-slot routing details for ordinary point queries by deriving the
  hash slot from the table partition key.
- Keep expensive local scans bounded with `limit`, cursor pagination, and scan
  cost statistics.
- Preserve the storage package boundary: Pebble-specific code remains under
  `pkg/db/internal`, while the CLI consumes a small inspection API.

## Non-Goals

- Full SQL compatibility.
- Cross-node or cluster-wide query semantics.
- `insert`, `update`, `delete`, repair, compaction, or index rebuild commands.
- `join`, `group by`, arbitrary `order by`, expressions, or `offset`.
- Online cluster routing. The CLI reads only the local files it opens.

## CLI Shape

Create a new command under `cmd/wkdb`.

Common forms:

```bash
wkdb --data-dir ./node-1 repl
wkdb --data-dir ./node-1 query "show tables"
wkdb --data-dir ./node-1 query "describe meta.user"
wkdb --config ./wukongim.conf query "select * from meta.user where uid='u1'"
wkdb --data-dir ./node-1 --hash-slot-count 256 query "select * from meta.user limit 100"
wkdb --data-dir ./node-1 --format jsonl query "select * from message.message where channel_key='g1:2' and message_seq >= 100 limit 50"
```

Path resolution:

- `--config` loads the normal node config and reuses the configured storage
  paths and hash-slot count.
- `--data-dir` derives `meta` from `<data-dir>/data` and `message` from
  `<data-dir>/channellog`.
- `--meta-path` and `--message-path` override derived paths.
- `--hash-slot-count` is required for partition-key-derived meta queries when
  no config provides the value.

Output formats:

- `table` by default for interactive troubleshooting.
- `json` for structured single-result output.
- `jsonl` for script-friendly row streams.

## Query Surface

Supported statements:

```sql
show tables;
describe meta.user;
select * from meta.user where uid='u1';
select uid, token from meta.user where hash_slot=12 limit 20;
select * from meta.channel where channel_id='g1' and channel_type=2;
select * from message.channels limit 100;
select * from message.message where channel_key='g1:2' and message_seq >= 100 limit 50;
```

Table names are domain-qualified:

- `meta.<table>` comes from `pkg/db/meta.Tables()`.
- `message.message` exposes channel message rows.
- `message.channels` exposes the message catalog.

The SQL subset intentionally stays small:

- projections by column name or `*`;
- equality filters for keys and ordinary columns;
- range filters only where a table implementation explicitly supports them;
- `limit`;
- opaque cursor continuation.

## Partition Planning

Metadata tables are stored by hash-slot partition. The CLI should not require
users to write `hash_slot` for ordinary point lookups. Instead, the planner
uses a table-specific partition key when present.

Initial partition-key mapping:

| Table | Partition key |
| --- | --- |
| `meta.user` | `uid` |
| `meta.device` | `uid` |
| `meta.channel` | `channel_id` |
| `meta.channel_runtime_meta` | `channel_id` |
| `meta.subscriber` | `channel_id` |
| `meta.conversation` | `uid` |
| `meta.cmd_conversation` | `uid` |
| `meta.plugin_binding` | `uid` |
| `meta.channel_migration` | `channel_id` |
| `meta.hashslot_migration` | `hash_slot` |
| `message.message` | `channel_key` |
| `message.channels` | none |

Planner modes:

- `point-partition`: a partition key is present, so the CLI computes
  `cluster.HashSlotForKey(key, hashSlotCount)` and scans only that hash slot.
- `explicit-partition`: the query contains `hash_slot = N`, so the CLI scans
  only that hash slot.
- `local-bounded`: no partition can be derived. The CLI scans local hash slots
  in `hash_slot ASC, primary_key ASC` order until the query limit is satisfied.
- `message-channel`: message rows require `channel_key` and scan by
  `message_seq` within that channel.
- `message-catalog`: channel catalog scans do not require a partition key.

Local bounded scans are local-file inspections, not global cluster queries.
The CLI should make this visible in output stats.

## Limit And Pagination

`limit` is the total number of rows returned by the query, not a per-hash-slot
limit.

Rules:

- Default limit is `100`.
- Maximum limit is `10000`.
- Single-partition queries apply the limit inside that partition.
- Local bounded scans collect rows across hash slots until the total limit is
  reached.
- `offset` is not supported.

Cursor pagination:

- Cursors are opaque strings, encoded from versioned JSON.
- A cursor records the table, scan mode, current hash slot, last primary key,
  message channel key, last message sequence, and a query hash.
- The query hash prevents reusing a cursor with a different query.
- Cursor continuation resumes after the last returned row.
- Cross-hash-slot cursors continue within the current hash slot, then advance
  to the next hash slot.

Example metadata cursor payload before encoding:

```json
{
  "version": 1,
  "domain": "meta",
  "table": "user",
  "scan_mode": "local-bounded",
  "hash_slot": 37,
  "after_primary": ["u100"],
  "query_hash": "sha256:..."
}
```

## Result Metadata

Every result includes scan metadata. Text output can show it as a footer:

```text
rows=100 has_more=true
scan_mode=local-bounded scanned_hash_slots=0..7 scanned_rows=3921 next_cursor=...
```

Structured output includes the same fields:

```json
{
  "rows": [],
  "stats": {
    "scan_mode": "local-bounded",
    "scanned_hash_slots": ["0..7"],
    "scanned_rows": 3921,
    "returned_rows": 100,
    "has_more": true,
    "next_cursor": "..."
  }
}
```

## Package Boundaries

Add `pkg/db/inspect` as the reusable read-only inspection layer.

Responsibilities:

- open meta and message stores read-only;
- expose table descriptors and column metadata;
- plan a small SQL-like query into typed scan operations;
- decode rows into stable `map[string]any` or structured row values;
- return rows, stats, and cursors.

`cmd/wkdb` responsibilities:

- parse flags and subcommands;
- run one query or a REPL loop;
- render table, JSON, or JSONL output;
- turn errors into clear operator-facing messages.

The CLI must not import `pkg/db/internal/*` directly.

To support read-only opening, extend `pkg/db/internal/engine.Options` with a
documented `ReadOnly` field that maps to Pebble's read-only option. The inspect
package uses that internal capability; `cmd/wkdb` does not import internal
packages directly.

## Error Handling

Important user-facing errors:

- missing data path;
- store cannot be opened read-only;
- query needs `--hash-slot-count` to derive a hash slot;
- unknown table or column;
- unsupported SQL feature;
- cursor does not match query;
- query would require a message channel scan without `channel_key`;
- malformed or corrupt stored row.

Corrupt rows fail the query. Best-effort partial reads are out of scope for v1.

## Testing

Unit tests:

- parser accepts the supported SQL subset and rejects unsupported constructs;
- planner chooses the correct scan mode;
- hash-slot derivation uses `cluster.HashSlotForKey`;
- `limit` is total across hash slots;
- cursor continuation resumes at the expected row;
- cursor query-hash mismatch fails;
- output formatters include stats.

Package tests:

- `pkg/db/inspect` opens temporary meta and message stores in read-only mode;
- meta point lookup works without explicit `hash_slot` when `hash-slot-count`
  is provided;
- explicit `hash_slot` scans only that partition;
- local bounded scans stop after total limit;
- message scans require `channel_key` and page by `message_seq`.

CLI tests:

- `wkdb query "show tables"` prints known tables;
- `wkdb query "describe meta.user"` prints columns and key metadata;
- missing `--hash-slot-count` produces a clear error only when the query needs
  derived hash-slot routing.

## Implementation Order

1. Add read-only engine opening support and focused tests.
2. Add `pkg/db/inspect` table catalog, scan stats, and cursor model.
3. Add minimal query parser and planner.
4. Implement meta scans for a small first table set, then generalize through
   existing table descriptors where safe.
5. Implement message catalog and message row scans.
6. Add `cmd/wkdb` with `query`, `repl`, and output formatters.
7. Document examples in a short CLI README or command help text.
