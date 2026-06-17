# wukongimv2 Application Log Monitor Design

## Summary

Add a lightweight application log monitor to the web manager console for
`cmd/wukongimv2`. The monitor shows ordinary WuKongIM process logs written by
the zap/lumberjack logger in `internalv2/log`; it does not inspect Controller,
Slot, Channel, or any other Raft/distributed logs.

The first version supports one selected node at a time. The web page can switch
nodes quickly, load a bounded recent tail, and keep a live stream open for new
lines from the selected node and source.

## Goals

- Provide a practical operator view for `app.log`, `warn.log`, `error.log`, and
  `debug.log` under the node's configured `WK_LOG_DIR`.
- Keep the implementation small: no Loki, Elasticsearch, Promtail, sidecar, or
  new long-running log service.
- Reuse the existing manager authentication, permission, CORS, and node RPC
  patterns.
- Keep file access safe by exposing only known log sources, never arbitrary
  filesystem paths.
- Keep single-node cluster and multi-node cluster behavior consistent: a single
  node is still selected explicitly and read through the same manager surface.

## Non-Goals

- No multi-node merged live stream in the first version.
- No full-text search across rotated history.
- No reads from gzip-compressed rotated files.
- No runtime log-level mutation.
- No Raft log inspection changes. Existing Controller and Slot log pages keep
  their current semantics and route names.

## Existing Context

`internalv2/log` currently creates rolling files:

- `app.log` for info and above.
- `warn.log` for warnings.
- `error.log` for errors and above.
- `debug.log` when debug logging is enabled.

The manager surface already has the pieces this feature should reuse:

- `internalv2/access/manager` owns manager HTTP routing, JWT auth, permissions,
  and response envelopes.
- `internalv2/usecase/management` owns entry-independent manager read models.
- `internalv2/infra/cluster` routes selected-node manager reads locally or over
  clusterv2 node RPC.
- `internalv2/access/node` already transports manager connection, distributed
  log, channel, and DB Inspect reads over typed node RPC.
- `web/src/pages/cluster/diagnostics/page.tsx` already groups tracing, network,
  Controller Raft logs, and Slot Raft logs under one Diagnostics page.

## Recommended Approach

Use a manager HTTP read API backed by a node-local file reader and optional node
RPC forwarding. The browser connects to the manager API for the selected node;
the manager handler chooses local or remote execution below the HTTP layer.

Use HTTP streaming with `fetch` and `ReadableStream`, not EventSource. The web
manager API already relies on JWT bearer tokens, and `fetch` can send the same
`Authorization` header used by existing JSON requests.

## Architecture

```text
web Diagnostics App Logs tab
  -> GET /manager/app-logs/sources?node_id=N
  -> GET /manager/app-logs?node_id=N&source=app&limit=200&cursor=...
  -> GET /manager/app-logs/stream?node_id=N&source=app&cursor=...
  -> internalv2/access/manager
  -> internalv2/usecase/management.ApplicationLogs
  -> local node: internalv2/log file reader
  -> remote node: internalv2/infra/cluster ApplicationLogReader
       -> internalv2/access/node manager app-log RPC
       -> target node local file reader
```

The live stream is only between browser and the manager HTTP handler. Remote
node reads remain bounded request-response calls so the node RPC layer does not
need long-lived streaming state.

## Backend Design

### Log Sources

Expose a fixed enum:

| Source | File |
|--------|------|
| `app` | `app.log` |
| `warn` | `warn.log` |
| `error` | `error.log` |
| `debug` | `debug.log` |

`debug` is listed as unavailable when the file does not exist. Source metadata
includes whether the file exists, current size, and latest modified time. The
web API does not display absolute log paths in the first version. The API must
never accept arbitrary file paths as input.

### Cursor

Use an opaque cursor string at the HTTP boundary. Internally it records:

- source
- byte offset
- file identity best effort: size and modified time, plus platform file ID
  where available
- cursor version

The cursor should be base64url-encoded JSON with a short prefix such as
`v1:`. It is not a security boundary; it is a pagination token. The reader must
validate that the cursor source matches the requested source.

### Initial Page API

`GET /manager/app-logs`

Query parameters:

- `node_id`: required positive node ID.
- `source`: one of `app`, `warn`, `error`, `debug`; default `app`.
- `limit`: default `200`, max `1000`.
- `cursor`: optional opaque cursor. Empty means tail the active file.
- `keyword`: optional substring filter, applied to the bounded page only.
- `levels`: optional comma-separated level filter for parsed structured lines.

Response:

```json
{
  "node_id": 1,
  "source": "app",
  "cursor": "v1:...",
  "next_cursor": "v1:...",
  "rotated": false,
  "items": [
    {
      "seq": 12345,
      "offset": 67890,
      "time": "2026-06-17T10:15:30.123+08:00",
      "level": "INFO",
      "module": "access.gateway",
      "caller": "gateway/handler.go:42",
      "message": "session started",
      "fields": {"event": "internalv2.access.gateway.session_started"},
      "raw": "2026-06-17 10:15:30.123\tINFO\t[access.gateway]\t..."
    }
  ]
}
```

For console format, parsing is best effort and `raw` remains authoritative. For
JSON format, parse structured fields when possible and preserve `raw`.

### Stream API

`GET /manager/app-logs/stream`

Query parameters:

- `node_id`: required positive node ID.
- `source`: one fixed source.
- `cursor`: optional cursor from the initial page.
- `keyword`: optional substring filter.
- `levels`: optional level filter.

Response format: newline-delimited JSON events with content type
`application/x-ndjson`.

Events:

- `line`: one log entry.
- `heartbeat`: sent on idle intervals so the browser can show connection
  liveness.
- `rotation`: current file rotated or shrank; the handler continues from the
  new active file and emits the new cursor.
- `error`: bounded error payload before closing the stream.

The handler stops when the request context is canceled. It should flush after
each event batch, not after every single byte, to avoid unnecessary overhead.

### File Reader

Add a small node-local reader under `internalv2/log`. The reader belongs beside
the zap/lumberjack layout because the first version is stateless and only needs
fixed-source file access. If a later version grows long-lived indexing or
sampling state, it can be moved behind the same management port.

Reader responsibilities:

- Resolve `source` to a fixed filename under `WK_LOG_DIR`.
- Read the last N lines from the active file without loading the whole file.
- Read forward from a cursor offset for incremental polling.
- Detect truncation or rotation when file size is smaller than the cursor
  offset or file identity changes.
- Bound line length, page size, and bytes scanned per request.
- Return typed errors: invalid source, unavailable file, invalid cursor,
  context canceled, and read failure.

Recommended limits:

- Max page limit: `1000`.
- Default page limit: `200`.
- Max line bytes: `64 KiB`.
- Max bytes scanned for one tail page: `8 MiB`.
- Stream poll interval: `500 ms` to `1 s`.
- Heartbeat interval: `15 s`.

### Usecase Layer

Extend `internalv2/usecase/management` with an `ApplicationLogReader` port:

```go
type ApplicationLogReader interface {
    ApplicationLogSources(context.Context, ApplicationLogSourcesRequest) (ApplicationLogSourcesResponse, error)
    ApplicationLogEntries(context.Context, ApplicationLogEntriesRequest) (ApplicationLogEntriesResponse, error)
}
```

Keep request validation here:

- `node_id` must be positive.
- `source` must be known.
- `limit` must be non-negative and capped.
- cursor/source mismatch returns invalid argument.

The usecase remains entry-independent and does not import gin or web DTOs.

### Manager HTTP Layer

Add routes under `/manager`:

```text
GET /manager/app-logs/sources
GET /manager/app-logs
GET /manager/app-logs/stream
```

Permission: `cluster.log:r`.

HTTP handler responsibilities:

- Parse query strings.
- Map typed errors to existing manager JSON error envelopes.
- For `/stream`, write NDJSON and flush events until context cancellation.
- Keep all file path and source validation below the HTTP layer.

### Remote Node Reads

Add a new typed manager app-log RPC instead of overloading the existing
distributed Raft log RPC. This avoids confusing ordinary application logs with
Controller/Slot Raft logs in naming and codec contracts.

The RPC supports bounded request-response operations:

- sources for one node
- entries for one node/source/cursor

The manager HTTP stream repeatedly calls the same bounded entries operation
when the selected node is remote. This keeps clusterv2 RPC simple and prevents
long-lived remote tail goroutines.

## Web Design

Add a new Diagnostics tab:

- `tab=app-logs`
- title: `Application Logs`
- legacy shortcut redirect: optional `/app-logs` to
  `/cluster/diagnostics?tab=app-logs`

Controls:

- Node selector using existing `NodeFilter`.
- Source segmented control: `app`, `warn`, `error`, `debug`.
- Live/Pause toggle.
- Keyword input.
- Level filter.
- Clear visible lines.
- Copy visible lines.

Main view:

- A fixed-height log viewport with monospaced rows.
- Rows are color-tinted by level.
- Each row shows timestamp, level, module, message, and raw/fields expansion.
- A small connection status indicator shows loading, live, paused, reconnecting,
  or unavailable.
- When rotation occurs, insert a visible separator row rather than silently
  losing context.

The page should cap retained rows in memory, for example 2,000 visible rows.
Pausing stops appending to the visible buffer but keeps the last cursor; resuming
continues from that cursor.

## Error Handling

- Missing `debug.log`: show source as unavailable and return a controlled
  `not_found` response if selected.
- Invalid cursor: return `bad_request`; web resets to a fresh tail after user
  confirmation or a retry action.
- Remote node unavailable: return `service_unavailable`; web keeps the selected
  node and shows retry.
- File rotation: stream emits `rotation`; web inserts a separator and continues.
- Oversized line: truncate display text and mark the row as truncated.
- Browser stream failure: web retries with backoff using the latest cursor.

## Security

- No arbitrary file path input.
- No directory listing.
- No access to rotated `.gz` files in the first version.
- Existing JWT auth and manager permissions apply.
- Default example users with `*:*` continue to work; least-privilege operators
  can receive `cluster.log:r`.
- Responses should avoid exposing absolute log paths unless that is already
  accepted by operator policy. If shown, treat it as display metadata only and
  never accept it back as input.

## Testing

Backend unit tests:

- Tail last N lines from a small file.
- Tail bounded large file without loading all contents.
- Read forward from cursor.
- Detect rotation/truncation.
- Reject unknown sources and cursor/source mismatch.
- Parse JSON logs and preserve raw console lines.
- Manager handler maps errors to status codes.
- Remote reader routes local and remote node IDs correctly.

Web tests:

- Diagnostics tab renders App Logs.
- Initial page fetch uses selected node/source/limit.
- Live stream appends NDJSON line events.
- Pause prevents visible append and resume continues from cursor.
- Rotation event inserts separator.
- Permission and service-unavailable states render through `ResourceState`.

Manual verification:

- Run one `cmd/wukongimv2` node with the manager service.
- Open web diagnostics App Logs tab.
- Confirm `app`, `warn`, and `error` sources display current log lines.
- In a three-node local cluster, select each node and confirm remote reads
  return that node's local log file.

## Implementation Order

1. Add the node-local fixed-source log reader and tests.
2. Add management usecase request/response types and validation.
3. Add cluster infra local/remote application log reader.
4. Add access/node manager app-log RPC codec, client, handler, and tests.
5. Wire the reader and RPC in `internalv2/app`.
6. Add manager HTTP JSON and NDJSON stream routes.
7. Add web API types and client helpers.
8. Add `app-logs` Diagnostics tab and page component.
9. Add i18n messages, navigation redirect, and `web/README.md` matrix update.

## Deferred Follow-Ups

- Multi-source `source=all` merging can be added after single-source streaming
  is stable.
- Rotated-file history search can be designed separately if operators need
  more than the active file tail.
