# Plugin Migration Design

- Date: 2026-05-15
- Scope: migrate the legacy WuKongIM plugin capability into the new layered
  monorepo as a phased, PDK-compatible subsystem.
- Decision: use a compatibility kernel with new-system layering. Phase 1 keeps
  `.wkp`, `go-pdk`, and `wkrpc` compatible while deferring stream APIs and
  marketplace-style installation.
- References:
  - Official plugin development documentation:
    https://docs.githubim.com/zh/getting-started/learning/plugin-development
  - Legacy implementation under `learn_project/WuKongIM/internal/plugin`
  - Current layering rules in `AGENTS.md`

## 1. Background

The legacy system supports Go plugins built with `github.com/WuKongIM/go-pdk`.
Plugin binaries are `.wkp` processes started by the host with a Unix socket and
sandbox directory. The PDK exposes these host-driven methods:

- `Send`
- `PersistAfter`
- `Receive`
- `Route`
- `ConfigUpdate`
- `Stop`

The PDK also lets plugins call back into the host for message sending, channel
message reads, plugin HTTP forwarding, cluster metadata, conversation channels,
and stream operations.

The new WuKongIM repository has a stricter architecture:

- entry adapters live in `internal/access/*`
- reusable business orchestration lives in `internal/usecase/*`
- node-local runtime infrastructure lives in `internal/runtime/*`
- dependency wiring lives only in `internal/app`
- cluster-authoritative business metadata lives behind slot Raft

The migration must not reintroduce a global `service` layer or node-local
business state that bypasses single-node-cluster or multi-node-cluster
semantics.

## 2. Goals and Non-Goals

## 2.1 Goals

1. Run existing `.wkp` plugins built with the current Go PDK.
2. Preserve the core plugin lifecycle and methods: `Send`, `PersistAfter`,
   `Receive`, `Route`, `ConfigUpdate`, and `Stop`.
3. Preserve the documented public route shape: `/plugins/:plugin/*path`.
4. Keep plugin process management node-local.
5. Store user-to-plugin bindings as cluster-authoritative slot metadata.
6. Route plugin-initiated sends through the normal message usecase and channel
   append path.
7. Add manager APIs for plugin inventory, config, restart, uninstall, and
   bindings.
8. Keep the first phase small enough to validate with fast unit and focused
   integration tests.

## 2.2 Non-Goals

Phase 1 does not implement:

- `/stream/open`, `/stream/write`, or `/stream/close`
- plugin marketplace downloads or remote installation
- a new multi-language plugin protocol
- full legacy manager route compatibility
- committed replay for `PersistAfter` plugin side effects

## 3. Architecture

The design splits the plugin subsystem into bounded units.

### 3.1 `internal/runtime/plugin`

This package owns node-local plugin runtime mechanics:

- scan `.wkp` files from `WK_PLUGIN_DIR`
- start plugin binaries with `--socket` and `--sandbox`
- provide Unix-socket `wkrpc` server primitives for the access adapter
- maintain an in-memory runtime registry
- watch plugin directory changes when hot reload is enabled
- stop plugins gracefully through `/stop`, then kill on timeout
- expose process status, last error, methods, and priority to the usecase layer

It must not register host RPC business handlers, call message/conversation
usecases, or own cluster-authoritative metadata.

### 3.2 `internal/usecase/plugin`

This package owns plugin business semantics:

- local plugin manifest and config persistence
- config template and config update orchestration
- Send hook chaining
- PersistAfter hook dispatch
- Receive hook dispatch
- user binding queries and mutation orchestration
- status DTOs for manager/API adapters

It depends on narrow interfaces for:

- runtime registry and RPC invocation
- cluster-authoritative binding store
- message send and message read host APIs
- cluster and conversation host APIs used by PDK compatibility handlers

### 3.3 `internal/access/plugin`

This adapter owns the PDK host RPC entrypoints over `wkrpc`.

It registers paths such as `/plugin/start`, `/message/send`, and
`/channel/messages` on the runtime-provided socket server, decodes
`pluginproto` requests, calls `internal/usecase/plugin`, and encodes
`pluginproto` responses. It contains no business decisions beyond transport
adaptation.

### 3.4 `internal/access/api`

The public API adapter adds:

- `ANY /plugins/:plugin/*path`

It converts HTTP requests into `pluginproto.HttpRequest`, calls the plugin
Route hook, and writes the `pluginproto.HttpResponse` back to the client.

### 3.5 `internal/access/manager`

The manager adapter adds plugin management routes protected by a new
`cluster.plugin` permission resource.

Suggested routes:

- `GET /manager/nodes/:node_id/plugins`
- `GET /manager/nodes/:node_id/plugins/:plugin_no`
- `PUT /manager/nodes/:node_id/plugins/:plugin_no/config`
- `POST /manager/nodes/:node_id/plugins/:plugin_no/restart`
- `DELETE /manager/nodes/:node_id/plugins/:plugin_no`
- `GET /manager/plugin-bindings?uid=...`
- `GET /manager/plugin-bindings?plugin_no=...`
- `POST /manager/plugin-bindings`
- `DELETE /manager/plugin-bindings`

Legacy admin paths such as `/plugin/bind` can be added later as thin adapters
if an existing UI or script requires them.

Plugin inventory, config, restart, and uninstall routes are node-scoped because
plugin binaries and process state are node-local. Binding routes are not
node-scoped because bindings are cluster-authoritative business metadata.

For node-scoped plugin management, requests for the local node execute locally.
Requests for another node are forwarded through a new node-access RPC adapter
with the same timeout budget as other manager node operations. If the remote
node is unavailable or does not support plugin management RPCs, the manager
returns a clear 503/501-style response without falling back to local state.

### 3.6 `internal/app`

The app composition root wires:

- plugin runtime
- plugin usecase
- plugin wkrpc access adapter
- message Send hook
- owner-routed committed PersistAfter side effect
- delivery offline Receive observer
- API and manager adapters

No other package should create global plugin managers.

## 4. Data Model and Consistency

Plugin data is split by consistency requirements.

### 4.1 Node-Local Plugin Manifest and Config

Plugin binaries and plugin process state are node-local facts. Phase 1 stores
desired local config data in a local durable store owned by the plugin
subsystem.

Durable desired/config fields:

- `No`
- `Config`
- `Enabled`
- `CreatedAt`
- `UpdatedAt`

This state describes the plugin instance on one node. It is not a cluster-wide
source of truth.

Runtime observation stays outside the durable manifest/config record. `Status`,
PID, connection state, `ObservedName`, `ObservedVersion`, `ObservedMethods`,
`ObservedPriority`, `ObservedPersistAfterSync`, `ObservedReplySync`,
`ObservedConfigTemplate`, `LastSeenAt`, and `LastError` come from the latest
handshake and runtime registry. These observed fields may be cached for manager
display and config form rendering, but hook ordering and invocation use the live
runtime registry only. If operators need to disable a plugin persistently, the
stored field is desired state (`Enabled`), not an ephemeral runtime status.

### 4.2 Cluster-Authoritative Plugin User Binding

User-to-plugin binding is business routing state and must be replicated through
slot Raft.

Add a `plugin_user_binding` table to `pkg/slot/meta`:

- primary key: `(uid, plugin_no)`
- secondary index: `idx_plugin_no_uid(plugin_no, uid)` within each hash slot
- fields:
  - `UID`
  - `PluginNo`
  - `CreatedAtMS`
  - `UpdatedAtMS`

Add FSM commands:

- `BindPluginUser`
- `UnbindPluginUser`

Add proxy APIs:

- `BindPluginUser(ctx, uid, pluginNo)`
- `UnbindPluginUser(ctx, uid, pluginNo)`
- `ListPluginBindingsByUID(ctx, uid)`
- `ListPluginBindingsByPluginNo(ctx, pluginNo, cursor, limit)`
- `ExistPluginBindingByUID(ctx, uid)`

The UID is the slot key, so one user's plugin routing facts are owned by that
user's authoritative slot in both single-node clusters and multi-node clusters.

`ListPluginBindingsByPluginNo` is a manager/diagnostics query. It fans out over
authoritative hash slots, scans each slot's `idx_plugin_no_uid`, and returns a
deterministically merged page. Its cursor contains the last scanned hash slot
and `(plugin_no, uid)` tuple, encoded as an opaque string by the proxy layer.

Bindings intentionally have no Raft-level foreign key to node-local plugin
metadata. A binding may reference a plugin that is absent or stopped on a
particular node. Bind-time validation can warn when the current node has no
matching plugin, but it must not make the cluster metadata depend on a local
binary. Runtime selection skips missing, disabled, or offline plugin instances
and records diagnostics. Plugin-centric listing is required so operators can
find stale bindings before uninstall or cleanup.

### 4.3 Priority Rule

Use one rule everywhere:

- larger `Priority` means higher priority

`Receive` selects the highest-priority running plugin bound to the UID.

`Send` and `PersistAfter` invoke matching global plugins from high priority to
low priority.

Equal priorities are ordered by `PluginNo` ascending to keep selection and hook
chains deterministic.

This intentionally fixes the legacy inconsistency where one path sorted
ascending while the documentation described larger priority values as higher.

### 4.4 Caching

`internal/usecase/plugin` may keep short-lived caches for UID bindings and
highest-priority plugin selection, but slot metadata remains authoritative.

Bind and unbind operations must invalidate affected UID cache entries.

The runtime registry represents only the plugins currently running on the local
node.

## 5. Message Hook Design

## 5.1 Send

`Send` hooks run through a narrow `message.SendHook` dependency injected through
`message.Options`. The hook is invoked inside `internal/usecase/message.App.Send`
after basic normalization and permission checks, before the send path branches
into NoPersist, SyncOnce, request-scoped delivery, or durable append.

The message usecase contract is:

```go
type SendOrigin string

const (
	SendOriginClient SendOrigin = "client"
	SendOriginPlugin SendOrigin = "plugin"
)

type SendHook interface {
	BeforeSend(ctx context.Context, cmd SendCommand) (SendCommand, frame.ReasonCode, error)
}
```

`SendCommand` gains explicit `Origin SendOrigin`, `HookDepth int`, and
`SkipPluginHooks bool` fields so recursion and deliberate hook skipping are
visible in tests. Client/API/gateway sends default to `SendOriginClient`.
Plugin host RPC sends set `SendOriginPlugin` and increment `HookDepth`; the
message usecase rejects or skips hooks when the configured maximum depth is
exceeded.

Mapping:

- `message.SendCommand` -> `pluginproto.SendPacket`
- `SenderSessionID`, `DeviceID`, and `DeviceFlag` -> `pluginproto.Conn`
- plugin output payload -> updated `SendCommand.Payload`
- non-success plugin reason -> send is rejected with that reason

RPC errors and timeouts default to fail-closed and return a system-error reason.
`WK_PLUGIN_FAIL_OPEN` may later allow fail-open behavior, but the default keeps
legacy-style safety.

Request-scoped sends participate exactly once. The implementation must avoid the
current early request-scoped branch skipping hooks by either moving hook
invocation ahead of that branch after validation, or by making the request-scoped
path call the same hook helper.

Plugin-originated sends carry this explicit origin metadata. The contract must
cap recursion and make deliberate hook skipping visible in tests.

## 5.2 PersistAfter

`PersistAfter` is implemented as an owner-routed committed side effect, not as a
plain local committed subscriber.

Each `messageevents.MessageCommitted` is converted to a
`pluginproto.MessageBatch`. Phase 1 may use one-message batches while keeping
the PDK-facing batch contract.

The node that handled `message.App.Send` is not necessarily the channel owner.
Therefore the app layer must either:

- integrate plugin PersistAfter invocation into the existing owner-routed
  committed dispatcher path, or
- add a dedicated `pluginCommittedRouter` that resolves the channel owner and
  forwards the event through node RPC before invoking local plugin usecase code.

Only the owner node invokes local PersistAfter plugins for a committed message.
This preserves the legacy behavior that forwarded PersistAfter to the channel
leader and prevents non-owner API nodes from duplicating or missing side
effects.

Failures are logged and observed, but they do not fail the already-committed
send. Phase 1 does not invoke PersistAfter from committed replay to avoid
duplicating external side effects after restart.

## 5.3 Receive

`Receive` is tied to eligible offline recipient UIDs, not just committed
messages.

The legacy plugin Receive behavior was an AI/user-plugin offline trigger. Phase
1 preserves that compatibility rather than calling Receive for every resolved
recipient.

Eligibility matrix:

| Case | Phase 1 behavior |
| --- | --- |
| durable message, recipient offline, sender is not recipient, sender is not system UID, not SyncOnce | invoke `Receive` |
| recipient online | do not invoke `Receive` |
| sender equals recipient | do not invoke `Receive` |
| sender is system UID | do not invoke `Receive` |
| SyncOnce message | do not invoke `Receive` |
| NoPersist realtime message | do not invoke `Receive` in Phase 1 |
| request-scoped command/temp channel | do not invoke `Receive` in Phase 1 unless later documented as a compatibility break |

The implementation should not use the current pre-presence `resolvedUIDObserver`
as-is for Receive. It must either add an offline UID observer after presence
expansion/classification, or pass enough delivery outcome context for the plugin
observer to determine that a UID is offline.

Flow:

1. delivery resolves a page of target UIDs
2. delivery determines which UIDs are offline according to authoritative
   presence
3. the observer filters by the eligibility matrix
4. the observer queries plugin bindings for eligible offline UIDs
5. each UID selects the highest-priority running local plugin with `Receive`
6. the observer invokes `pluginproto.RecvPacket`

Receive failures do not block normal delivery. The observer keeps a short TTL
dedupe key of `messageID + uid` to avoid duplicate plugin calls during retry or
paged resolution.

## 5.4 Plugin-Initiated Send

The PDK host RPC `/message/send` maps to `message.App.Send`.

Plugin sends never bypass the cluster append path. Empty `fromUid` follows the
legacy behavior and uses the configured system UID/default plugin sender.

To avoid recursive plugin storms, Phase 1 adds a recursion-depth guard for
plugin-originated sends.

## 6. Host RPC Compatibility

`internal/access/plugin` registers these PDK-compatible host paths on the
runtime-provided wkrpc server in Phase 1:

- `/plugin/start`
- `/close`
- `/message/send`
- `/channel/messages`
- `/plugin/httpForward`
- `/cluster/config`
- `/cluster/channels/belongNode`
- `/conversation/channels`

These stream paths return explicit `unimplemented` errors in Phase 1:

- `/stream/open`
- `/stream/write`
- `/stream/close`

The host must fail loudly for unsupported stream calls instead of silently
dropping data.

Generated compatibility types live in `internal/usecase/plugin/pluginproto`
using the legacy `plugin.proto` wire schema. The host imports `wkrpc` only from
`internal/runtime/plugin` and `internal/access/plugin`; usecase packages see
plain Go interfaces and `pluginproto` DTOs.

All host RPCs use `WK_PLUGIN_TIMEOUT` unless the request context has a shorter
deadline. Phase 1 also applies a bounded body limit derived from API defaults,
with a plugin-specific override added later only if needed.

| RPC path | Handler layer | Request -> response | Authority and forwarding |
| --- | --- | --- | --- |
| `/plugin/start` | `internal/access/plugin` -> `internal/usecase/plugin` | `PluginInfo` -> `StartupResp` | local only; registers observed runtime manifest, ensures sandbox, returns local node ID and local config |
| `/close` | `internal/access/plugin` -> `internal/usecase/plugin` | empty -> OK | local only; marks runtime connection closed |
| `/message/send` | `internal/access/plugin` -> `internal/usecase/plugin` -> `message.App.Send` | `SendReq` -> `SendResp` | normal message usecase and cluster append path; sets `SendOriginPlugin` |
| `/channel/messages` | `internal/access/plugin` -> message reader interface | `ChannelMessageBatchReq` -> `ChannelMessageBatchResp` | authoritative channel owner reader, reusing the existing local/remote channel message reader pattern |
| `/plugin/httpForward` | `internal/access/plugin` -> plugin Route usecase or node HTTP/RPC forwarding | `ForwardHttpReq` -> `HttpResponse` | `toNodeId=0` local; positive remote node forwarded with timeout/body/header limits; `-1` fanout is deferred unless needed |
| `/cluster/config` | `internal/access/plugin` -> cluster reader interface | empty -> `ClusterConfig` | maps controller nodes and slot assignments into the legacy PDK protobuf shape |
| `/cluster/channels/belongNode` | `internal/access/plugin` -> channel metadata/routing reader | `ClusterChannelBelongNodeReq` -> `ClusterChannelBelongNodeBatchResp` | uses current channel owner/routing metadata, not local guesses |
| `/conversation/channels` | `internal/access/plugin` -> conversation reader interface | `ConversationChannelReq` -> `ConversationChannelResp` | uses conversation usecase/store and preserves authoritative read semantics |
| `/stream/open`, `/stream/write`, `/stream/close` | `internal/access/plugin` | stream request -> error | returns a stable `unimplemented` wkrpc error status and logs plugin number/path |

`/plugin/httpForward` copies request method, path, query, headers, and body
through the `pluginproto.HttpRequest` DTO. Hop-by-hop headers are dropped, and
response status/header/body are copied from `pluginproto.HttpResponse`.

## 7. Configuration

Add `PluginConfig` to `internal/app/config.go` and parse these `WK_` keys:

- `WK_PLUGIN_ENABLE=false`
- `WK_PLUGIN_DIR=`
- `WK_PLUGIN_SOCKET_PATH=`
- `WK_PLUGIN_SANDBOX_DIR=`
- `WK_PLUGIN_STATE_DIR=`
- `WK_PLUGIN_TIMEOUT=5s`
- `WK_PLUGIN_HOT_RELOAD=true`
- `WK_PLUGIN_FAIL_OPEN=false`

`WK_PLUGIN_SOCKET_PATH` defaults to `<WK_NODE_DATA_DIR>/run/plugin.sock`.

`WK_PLUGIN_DIR` defaults to `<WK_NODE_DATA_DIR>/plugins`.

`WK_PLUGIN_SANDBOX_DIR` defaults to `<WK_NODE_DATA_DIR>/plugin-sandbox`.

`WK_PLUGIN_STATE_DIR` defaults to `<WK_NODE_DATA_DIR>/plugin-state`.

Relative plugin paths are resolved relative to the process working directory
only when explicitly configured. Defaults are derived from `WK_NODE_DATA_DIR` so
multiple nodes started from one repository checkout do not accidentally share
plugin binaries, socket paths, sandbox data, or local config stores.

The example config must document that plugins execute local binaries and are
disabled by default for safety.

## 8. Lifecycle

Recommended lifecycle order:

1. cluster
2. managed slot readiness
3. channel meta, presence, and delivery runtime
4. plugin runtime
5. gateway, public API, and manager

On shutdown, entry servers stop before plugin runtime. This prevents new
external requests from entering plugins while the host is shutting down.

Plugin runtime shutdown:

1. stop directory watcher
2. ask each plugin to stop through `/stop`
3. wait up to the configured timeout
4. kill remaining processes
5. close the wkrpc server and state store

## 9. Security

- `WK_PLUGIN_ENABLE` defaults to false.
- Manager plugin APIs require `cluster.plugin` permissions.
- Public plugin routes remain open in Phase 1 for compatibility with the
  documented `/plugins/:plugin/*path` behavior. They are node-local routes
  unless a later phase adds explicit plugin-route federation.
- Secret-looking config fields are redacted in list responses.
- Plugin config is passed through the RPC channel, not injected as environment
  variables.
- Uninstalling a local plugin does not automatically delete cluster-wide user
  bindings.

## 10. Testing Strategy

### 10.1 Unit Tests

`internal/runtime/plugin`:

- registry priority ordering
- start/close status updates
- timeout and error mapping
- hot reload dedupe

`internal/access/plugin`:

- each PDK host RPC decodes and encodes the expected `pluginproto` DTOs
- unsupported stream RPCs return the stable unimplemented status
- remote `httpForward` and node-scoped manager forwarding honor timeouts and
  body limits

`internal/usecase/plugin`:

- manifest/config persistence
- config update and `ConfigUpdate` invocation
- highest-priority Receive selection
- Send hook payload mutation and reason rejection
- request-scoped sends invoke Send hooks exactly once
- Receive dedupe
- Receive eligibility matrix filtering

`pkg/slot/meta`, `pkg/slot/fsm`, and `pkg/slot/proxy`:

- binding table encode/decode
- bind/unbind commands
- UID binding list/query
- plugin-centric binding listing
- idempotent unbind of missing binding

### 10.2 Integration Tests

Fast integration tests should verify a single-node cluster can:

- start plugin runtime when enabled
- accept a plugin `/plugin/start` handshake
- expose plugin status through manager/API paths
- route `/plugins/:plugin/*path`
- send plugin-originated messages through `message.App.Send`

Focused multi-node integration tests should verify:

- send from a non-owner API node and verify only the owner node invokes
  PersistAfter
- node-scoped manager plugin requests either execute on the target node or fail
  with the documented remote unsupported/unavailable response

Real `.wkp` process tests should use the `integration` build tag to avoid
slowing normal unit tests.

## 11. Milestones

### M1: Runtime and Access PDK Compatibility Kernel

- wkrpc server
- plugin process start/stop
- `/plugin/start`
- runtime registry
- config parsing

### M2: Metadata and Binding

- local manifest/config store
- slot Raft plugin binding table
- proxy binding APIs
- manager plugin and binding APIs

### M3: Message Hooks

- Send pre-hook
- owner-routed PersistAfter committed side effect
- offline Receive delivery observer

### M4: Public Route and Host RPCs

- `/plugins/:plugin/*path`
- `/message/send`
- `/channel/messages`
- `/plugin/httpForward`
- `/cluster/config`
- `/cluster/channels/belongNode`
- `/conversation/channels`
- `pluginproto` placement, RPC authority, limits, and mappings

### M5: Documentation and Cleanup

- update `wukongim.conf.example`
- update relevant `FLOW.md` files when changed
- add concise notes to `docs/development/PROJECT_KNOWLEDGE.md`
- document Phase 1 support and stream deferral

## 12. Risks and Mitigations

- Local binary execution: disabled by default.
- Send hook latency: enforce RPC timeouts and bounded chains.
- Send hook failures: default fail-closed; optional fail-open can be added.
- Duplicate PersistAfter side effects: no replay in Phase 1.
- Duplicate Receive calls: short TTL `messageID + uid` dedupe.
- Legacy priority inconsistency: document and standardize larger priority as
  higher.
- Stream-using plugins: return explicit unimplemented errors and document Phase
  2 support.

## 13. Acceptance Criteria

Phase 1 is complete when:

1. an existing Go PDK plugin can register through `/plugin/start`
2. public plugin route forwarding works through `/plugins/:plugin/*path`
3. Send hook can mutate payload and reject a send
4. PersistAfter receives durable messages only on the channel owner node after
   append
5. Receive triggers only for eligible offline bound UIDs
6. user bindings are stored through slot-authoritative metadata
7. plugin-originated sends go through `message.App.Send`
8. relevant unit tests pass for changed packages
9. stream APIs fail with explicit unimplemented errors
10. configuration and support boundaries are documented
