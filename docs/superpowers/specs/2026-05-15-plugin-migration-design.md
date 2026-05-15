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
- host the Unix-socket `wkrpc` server
- register PDK-compatible RPC paths
- maintain an in-memory runtime registry
- watch plugin directory changes when hot reload is enabled
- stop plugins gracefully through `/stop`, then kill on timeout
- expose process status, last error, methods, and priority to the usecase layer

It must not own user binding business rules or cluster-authoritative metadata.

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

### 3.3 `internal/access/api`

The public API adapter adds:

- `ANY /plugins/:plugin/*path`

It converts HTTP requests into `pluginproto.HttpRequest`, calls the plugin
Route hook, and writes the `pluginproto.HttpResponse` back to the client.

### 3.4 `internal/access/manager`

The manager adapter adds plugin management routes protected by a new
`cluster.plugin` permission resource.

Suggested routes:

- `GET /manager/plugins`
- `GET /manager/plugins/:plugin_no`
- `PUT /manager/plugins/:plugin_no/config`
- `POST /manager/plugins/:plugin_no/restart`
- `DELETE /manager/plugins/:plugin_no`
- `GET /manager/plugin-bindings?uid=...`
- `POST /manager/plugin-bindings`
- `DELETE /manager/plugin-bindings`

Legacy admin paths such as `/plugin/bind` can be added later as thin adapters
if an existing UI or script requires them.

### 3.5 `internal/app`

The app composition root wires:

- plugin runtime
- plugin usecase
- message Send hook
- committed PersistAfter subscriber
- delivery Receive observer
- API and manager adapters

No other package should create global plugin managers.

## 4. Data Model and Consistency

Plugin data is split by consistency requirements.

### 4.1 Node-Local Plugin Manifest and Config

Plugin binaries and plugin process state are node-local facts. Phase 1 stores
manifest/config data in a local durable store owned by the plugin subsystem.

Fields:

- `No`
- `Name`
- `Version`
- `Methods`
- `Priority`
- `PersistAfterSync`
- `ReplySync`
- `ConfigTemplate`
- `Config`
- `Status`
- `CreatedAt`
- `UpdatedAt`
- `LastError`

This state describes the plugin instance on one node. It is not a cluster-wide
source of truth.

### 4.2 Cluster-Authoritative Plugin User Binding

User-to-plugin binding is business routing state and must be replicated through
slot Raft.

Add a `plugin_user_binding` table to `pkg/slot/meta`:

- primary key: `(uid, plugin_no)`
- secondary index: by `uid`
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
- `ExistPluginBindingByUID(ctx, uid)`

The UID is the slot key, so one user's plugin routing facts are owned by that
user's authoritative slot in both single-node clusters and multi-node clusters.

### 4.3 Priority Rule

Use one rule everywhere:

- larger `Priority` means higher priority

`Receive` selects the highest-priority running plugin bound to the UID.

`Send` and `PersistAfter` invoke matching global plugins from high priority to
low priority.

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

`Send` hooks run inside `internal/usecase/message.App.Send` after basic
normalization and permission checks, before the send path branches into
NoPersist, SyncOnce, or durable append.

Mapping:

- `message.SendCommand` -> `pluginproto.SendPacket`
- plugin output payload -> updated `SendCommand.Payload`
- non-success plugin reason -> send is rejected with that reason

RPC errors and timeouts default to fail-closed and return a system-error reason.
`WK_PLUGIN_FAIL_OPEN` may later allow fail-open behavior, but the default keeps
legacy-style safety.

## 5.2 PersistAfter

`PersistAfter` is implemented as a committed-message subscriber.

Each `messageevents.MessageCommitted` is converted to a
`pluginproto.MessageBatch`. Phase 1 may use one-message batches while keeping
the PDK-facing batch contract.

Failures are logged and observed, but they do not fail the already-committed
send. Phase 1 does not invoke PersistAfter from committed replay to avoid
duplicating external side effects after restart.

## 5.3 Receive

`Receive` is tied to resolved recipient UIDs, not just committed messages.

Use the existing delivery resolver observer shape and add a plugin receive
observer:

1. delivery resolves a page of target UIDs
2. the observer queries plugin bindings for those UIDs
3. each UID selects the highest-priority running local plugin with `Receive`
4. the observer invokes `pluginproto.RecvPacket`

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

The runtime wkrpc server registers these PDK-compatible host paths in Phase 1:

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

## 7. Configuration

Add `PluginConfig` to `internal/app/config.go` and parse these `WK_` keys:

- `WK_PLUGIN_ENABLE=false`
- `WK_PLUGIN_DIR=./plugindir`
- `WK_PLUGIN_SOCKET_PATH=`
- `WK_PLUGIN_SANDBOX_DIR=`
- `WK_PLUGIN_TIMEOUT=5s`
- `WK_PLUGIN_HOT_RELOAD=true`
- `WK_PLUGIN_FAIL_OPEN=false`

`WK_PLUGIN_SOCKET_PATH` defaults to `<WK_NODE_DATA_DIR>/run/plugin.sock`.

`WK_PLUGIN_SANDBOX_DIR` defaults to `<WK_PLUGIN_DIR>/plugindata`.

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
5. close the wkrpc server and local store

## 9. Security

- `WK_PLUGIN_ENABLE` defaults to false.
- Manager plugin APIs require `cluster.plugin` permissions.
- Public plugin routes remain open in Phase 1 for compatibility with the
  documented `/plugins/:plugin/*path` behavior.
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

`internal/usecase/plugin`:

- manifest/config persistence
- config update and `ConfigUpdate` invocation
- highest-priority Receive selection
- Send hook payload mutation and reason rejection
- Receive dedupe

`pkg/slot/meta`, `pkg/slot/fsm`, and `pkg/slot/proxy`:

- binding table encode/decode
- bind/unbind commands
- UID binding list/query
- idempotent unbind of missing binding

### 10.2 Integration Tests

Fast integration tests should verify a single-node cluster can:

- start plugin runtime when enabled
- accept a plugin `/plugin/start` handshake
- expose plugin status through manager/API paths
- route `/plugins/:plugin/*path`
- send plugin-originated messages through `message.App.Send`

Real `.wkp` process tests should use the `integration` build tag to avoid
slowing normal unit tests.

## 11. Milestones

### M1: Runtime and PDK Compatibility Kernel

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
- PersistAfter committed subscriber
- Receive delivery UID observer

### M4: Public Route and Host RPCs

- `/plugins/:plugin/*path`
- `/message/send`
- `/channel/messages`
- `/plugin/httpForward`
- `/cluster/config`
- `/cluster/channels/belongNode`
- `/conversation/channels`

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
4. PersistAfter receives durable messages after append
5. Receive triggers for bound UIDs during delivery resolution
6. user bindings are stored through slot-authoritative metadata
7. plugin-originated sends go through `message.App.Send`
8. relevant unit tests pass for changed packages
9. stream APIs fail with explicit unimplemented errors
10. configuration and support boundaries are documented
