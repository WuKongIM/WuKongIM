# DB Inspect Management Design

## Summary

Add a read-only DB inspect page to the Web manager shell under `/system/db`.
The feature targets the current `wukongimv2` entry point and exposes bounded,
node-local storage inspection through the existing manager API.

The first version is a diagnostic query console only. It reuses
`pkg/db/inspect` for `show tables`, `describe`, and a small read-only `select`
subset. It does not mutate storage, repair data, compact Pebble, import or
export snapshots, or introduce any deployment path outside normal single-node
cluster and multi-node cluster semantics.

## Goals

- Add a Web DB management surface for `pkg/db` under `web/`.
- Route all browser requests through the `wukongimv2` manager API.
- Reuse the existing `pkg/db/inspect` read-only query engine.
- Support local and selected remote node inspection with an explicit `node_id`.
- Keep expensive scans bounded with `limit`, cursor pagination, and inspect
  stats.
- Keep HTTP handlers thin and place orchestration in
  `internalv2/usecase/management`.
- Avoid exposing Pebble raw keys, internal package APIs, or write operations.

## Non-Goals

- No write, repair, delete, truncate, compaction, checkpoint, or retention
  operation.
- No full SQL compatibility.
- No joins, ordering, grouping, offset pagination, expressions, or arbitrary
  predicates.
- No cluster-wide merged DB query. Each request inspects exactly one node.
- No new `wukongimv2` config key in v1.
- No legacy `cmd/wukongim` manager support.
- No direct Web access to `cmd/wkdb` or filesystem paths.

## Current Context

`web/` already provides the authenticated manager shell and calls `/manager/*`
through `web/src/lib/manager-api.ts`.

`cmd/wukongimv2` is the active entry for this feature. It configures a dedicated
manager listener with `WK_MANAGER_LISTEN_ADDR` and stores node data under
`WK_NODE_DATA_DIR`.

`pkg/clusterv2` derives the storage paths used by the default v2 runtime:

- metadata DB: `<WK_NODE_DATA_DIR>/slotmeta`
- channel message DB: `<WK_NODE_DATA_DIR>/messages`

`pkg/db/inspect` already provides the reusable read-only inspection layer:

- `OpenStore(Options)` opens meta and message stores in read-only mode;
- `Query(ctx, raw)` executes `show tables`, `describe`, and bounded `select`;
- result stats include scan mode, scanned hash slots, scanned rows, returned
  rows, `has_more`, and `next_cursor`;
- metadata scans understand hash-slot partitioning;
- `message.message` scans require `channel_key`.

## Recommended Approach

Add a manager-facing DB inspect usecase, HTTP adapter, node RPC adapter, and Web
page. The Web page sends queries to a selected node. The local node handles
local queries directly; non-local queries are forwarded over the existing
clusterv2 manager RPC pattern.

This keeps the feature aligned with existing boundaries:

```text
web /system/db
  -> internalv2/access/manager
  -> internalv2/usecase/management
  -> local DBInspectReader or remote DBInspectReader
  -> pkg/db/inspect on the target node
```

The page is an operational tool, not a business data model. It should be useful
for troubleshooting storage rows, but it should not become a parallel user,
channel, or message management API.

## Backend Design

### Management Usecase

Add `internalv2/usecase/management/db_inspect.go`.

New ports:

```go
// DBInspectReader executes read-only inspect queries against one node-local DB.
type DBInspectReader interface {
    QueryDBInspect(ctx context.Context, req DBInspectQueryRequest) (DBInspectQueryResponse, error)
}

// RemoteDBInspectReader executes read-only inspect queries on peer nodes.
type RemoteDBInspectReader interface {
    NodeDBInspectQuery(ctx context.Context, req DBInspectQueryRequest) (DBInspectQueryResponse, error)
}
```

New request and response DTOs:

```go
type DBInspectQueryRequest struct {
    NodeID uint64
    Query  string
}

type DBInspectQueryResponse struct {
    NodeID      uint64
    GeneratedAt time.Time
    Rows        []map[string]any
    Stats       DBInspectStats
}

type DBInspectStats struct {
    ScanMode         string
    ScannedHashSlots []uint16
    ScannedRows      int
    ReturnedRows     int
    HasMore          bool
    NextCursor       string
}
```

The management app exposes:

- `ListDBInspectTables(ctx, nodeID uint64)`
- `DescribeDBInspectTable(ctx, nodeID uint64, domain string, table string)`
- `QueryDBInspect(ctx, req DBInspectQueryRequest)`

The first two methods are convenience wrappers around:

- `show tables`
- `describe <domain>.<table>`

All three methods use the same local-or-remote routing rule:

- empty or local `node_id`: call the local reader;
- non-local `node_id`: call the remote reader;
- missing local reader: return `ErrDBInspectUnavailable`;
- missing remote reader: return `ErrDBInspectUnavailable`;
- unknown target node: return a validation error before attempting RPC.

The usecase validates only request envelope fields such as `node_id`, empty
query, and table path segments. SQL syntax and scan planning stay in
`pkg/db/inspect`.

### Local Inspect Reader

Add an app-level adapter in `internalv2/app`, for example
`db_inspect.go`.

The adapter is constructed from the existing app config:

- `nodeID`: `Config.NodeID`
- `metaPath`: `filepath.Join(Config.DataDir, "slotmeta")`
- `messagePath`: `filepath.Join(Config.DataDir, "messages")`
- `hashSlotCount`: `Config.Cluster.Slots.HashSlotCount`, after clusterv2
  defaults are applied
- limits: use `pkg/db/inspect` defaults in v1

The reader opens a short-lived `inspect.Store` per query and closes it after
execution. This keeps lifecycle simple, avoids sharing an inspect handle across
runtime restarts, and gives each query a fresh read-only view of the current
local files. If read-only open fails because storage is unavailable, the
manager endpoint returns `service_unavailable`.

The adapter must not import `pkg/db/internal/*`; only `pkg/db/inspect` is used.

### Manager HTTP Routes

Extend `internalv2/access/manager.Management` with the new DB inspect methods.

Add routes:

```text
GET  /manager/db/inspect/tables?node_id=
GET  /manager/db/inspect/tables/:domain/:table?node_id=
POST /manager/db/inspect/query
```

The write-like HTTP verb on `query` is used only because the SQL string can be
long and contains cursor values. It remains a read-only operation and requires
read permission.

Request body:

```json
{
  "node_id": 1,
  "query": "select * from meta.user where uid='u1' limit 20"
}
```

Successful response:

```json
{
  "node_id": 1,
  "generated_at": "2026-06-17T10:00:00Z",
  "rows": [
    {
      "uid": "u1",
      "token": "t1"
    }
  ],
  "stats": {
    "scan_mode": "point-partition",
    "scanned_hash_slots": [3],
    "scanned_rows": 1,
    "returned_rows": 1,
    "has_more": false,
    "next_cursor": ""
  }
}
```

Permission:

- resource: `cluster.db`
- action: `r`

The route must also allow `*:*` grants through the existing permission
mechanism. Existing example configs use wildcard admin permissions, so no
config example update is required for v1.

### Error Mapping

HTTP handlers map errors to the existing manager envelope style:

- malformed JSON or missing query: `400 invalid_request`;
- invalid table path or unsupported SQL: `400 invalid_request`;
- cursor mismatch: `400 invalid_cursor`;
- context cancellation or deadline: existing context handling;
- local or remote inspect reader missing: `503 service_unavailable`;
- read-only storage open failure: `503 service_unavailable`;
- unexpected query execution error: `500 internal_error`.

The response should not expose filesystem paths for storage-open failures.
Detailed errors can be logged with low-cardinality event names.

### Node RPC

Add a manager DB inspect RPC following the existing manager connection, log, and
channel RPC pattern.

Add a new clusterv2 service ID:

```go
RPCManagerDBInspect
```

Add files under `internalv2/access/node`:

- `manager_db_inspect_rpc.go`
- `manager_db_inspect_codec.go`
- tests for both

RPC shape:

```text
remote manager DB inspect reader
  -> encode W K V I 1 request
  -> clusterv2 RPCManagerDBInspect
  -> Adapter.HandleManagerDBInspectRPC
  -> local management DBInspectReader
  -> encode W K V i 1 response
```

The outer RPC frame should stay deterministic and versioned. Because inspect
rows are dynamic maps, the response can carry a JSON-encoded result payload
inside the stable binary envelope. Go's JSON encoding handles `[]byte` payload
columns as base64 strings, which is acceptable for this diagnostic surface.

The RPC status mapping mirrors HTTP semantics:

- `ok`
- `context_canceled`
- `context_deadline_exceeded`
- `invalid_request`
- `invalid_cursor`
- `unavailable`
- `rejected`

The server always inspects its own local DB. It does not interpret the requested
node as a second routing hop.

### App Wiring

In `internalv2/app.wireManager` and `newManagerManagement`:

- construct the local DB inspect reader when manager is enabled and `DataDir`
  is set;
- pass it to `management.Options`;
- register the manager DB inspect RPC handler when node RPC is available;
- pass the remote DB inspect client to management options when the cluster node
  can call manager RPCs.

The app remains the only composition root. HTTP handlers do not derive paths or
open stores themselves.

### FLOW.md Updates

Update these files when implementing:

- `internalv2/usecase/management/FLOW.md`
- `internalv2/access/manager/FLOW.md`
- `internalv2/access/node/FLOW.md`
- `internalv2/app/FLOW.md`

The flow descriptions should explicitly call the feature node-local DB inspect,
not cluster-wide DB query.

## Query Surface

The supported SQL subset is exactly the `pkg/db/inspect` subset in v1.

Examples:

```sql
show tables
describe meta.user
select * from meta.user where uid='u1' limit 20
select * from meta.channel where hash_slot=3 limit 50
select * from message.channels limit 50
select * from message.message where channel_key='g1:2' limit 20
select * from message.message where channel_key='g1:2' limit 20 cursor '...'
```

Important constraints:

- queries are read-only;
- `message.message` requires `channel_key`;
- metadata tables can use `hash_slot` or table partition keys;
- `join`, `group by`, `order by`, and `offset` are unsupported;
- cursor values are opaque and bound to the original query shape.

The manager layer should not fork or extend SQL semantics in v1. If a future
query feature is needed, it should first be added to `pkg/db/inspect` and then
become available through manager.

## Frontend Design

### Navigation

Add a System navigation item:

- route: `/system/db`
- title: DB Inspect
- icon: `Database`
- legacy redirect: `/db-inspect -> /system/db`

System placement is intentional: this is a low-level operational inspection
tool, not a business channel or message management page.

### Page Structure

Add `web/src/pages/db-inspect/page.tsx`.

The page should be dense and operational:

- node selector using `GET /manager/nodes`;
- table list grouped by `meta` and `message`;
- query editor with compact template buttons;
- result table with dynamic columns;
- table detail drawer or side panel driven by `describe`;
- next-page button using `stats.next_cursor`;
- status strip showing scan mode, scanned rows, returned rows, and scanned hash
  slots.

Template buttons:

- `show tables`
- `describe meta.user`
- `select * from meta.user where uid='...' limit 20`
- `select * from meta.channel where channel_id='...' limit 20`
- `select * from message.channels limit 50`
- `select * from message.message where channel_key='...' limit 20`

The UI should avoid large instructional blocks. Templates, labels, placeholders,
and error states are enough.

### Result Rendering

Rows are dynamic objects, so the table derives columns from the current result:

- preserve backend column order when the query is a `describe` or explicit
  projection;
- otherwise use the first row's keys sorted by the backend's JSON order where
  available, falling back to stable alphabetic order;
- render arrays as compact comma-separated values;
- render byte/base64 payload values as a truncated monospace preview with a
  payload-size hint when present;
- render `null` and missing values as `-`.

The page must keep query text and selected node when an error occurs.

### Web API Client

Extend `web/src/lib/manager-api.types.ts` with:

- `ManagerDBInspectTablesResponse`
- `ManagerDBInspectDescribeResponse`
- `ManagerDBInspectQueryInput`
- `ManagerDBInspectQueryResponse`
- `ManagerDBInspectStats`

Extend `web/src/lib/manager-api.ts` with:

- `getDBInspectTables(params?: { nodeId?: number })`
- `getDBInspectTable(domain: string, table: string, params?: { nodeId?: number })`
- `queryDBInspect(input: ManagerDBInspectQueryInput)`

The client should URL-encode `domain` and `table` path segments.

### I18n

Add `en` and `zh-CN` copy for:

- navigation item and breadcrumb;
- query controls;
- template labels;
- result stats;
- empty state;
- error state.

The Chinese copy should use "单节点集群" when describing deployment shape, and
"本节点" or "节点本地" only when referring to storage locality.

## Security And Safety

- Manager auth and permissions guard every route when auth is enabled.
- The feature never accepts filesystem paths from the Web request.
- The backend derives storage paths from the running `wukongimv2` config.
- Query execution is bounded by `pkg/db/inspect` limits and cursor pagination.
- SQL remains read-only by parser design.
- Errors returned to Web avoid leaking local path details.
- No UID, channel ID, or message payload is added as a metric label.

## Testing Plan

Backend unit tests:

- management local table list, describe, and query;
- management remote routing by `node_id`;
- unknown remote node rejection;
- missing local or remote reader returns unavailable;
- cursor mismatch maps to invalid cursor;
- manager HTTP auth and permission checks for `cluster.db:r`;
- manager HTTP query success and error envelopes;
- node RPC request/response codec roundtrip;
- node RPC status-to-error mapping;
- app wiring attaches local and remote DB inspect readers.

Frontend tests:

- API URL builders and request bodies;
- route registration and navigation metadata;
- page renders node selector, table list, query editor, and results;
- describe panel renders columns;
- query error keeps input state;
- next-page button submits the cursor query;
- i18n coverage for both locales.

Verification commands:

```bash
go test ./internalv2/usecase/management ./internalv2/access/manager ./internalv2/access/node ./internalv2/app ./pkg/db/inspect
cd web && bun run test
```

Full `go test ./...` remains the broad pre-merge verification when time allows.

## Documentation Updates

Update `web/README.md` page/API matrix:

- `/system/db`
- `GET /manager/db/inspect/tables`
- `GET /manager/db/inspect/tables/:domain/:table`
- `POST /manager/db/inspect/query`

No `cmd/wukongimv2/*.conf.example` update is needed because no config key is
added.

## Rollout

Implement in small slices:

1. Add management usecase and local inspect reader.
2. Add manager HTTP routes and tests.
3. Add node RPC for remote node queries.
4. Wire app composition.
5. Add Web API client and `/system/db` page.
6. Update FLOW docs and Web README.

The feature can ship with local-node queries first if remote RPC lands later,
but the public API should keep the `node_id` field from the start so the Web
surface does not need to change.
