# Plugin Phase 1

## 1. Scope

Phase 1 restores compatibility for existing `.wkp` plugins built with `github.com/WuKongIM/go-pdk` while keeping the new system's layered boundaries:

- plugin processes and desired runtime state are node-local;
- message sends still go through `message.App.Send` and the cluster append path;
- UID to plugin bindings are cluster-authoritative Slot Raft metadata;
- public plugin HTTP routes are node-local and open for Phase 1 compatibility.

Streams are intentionally not implemented in Phase 1. `/stream/open`, `/stream/write`, and `/stream/close` return a stable unimplemented host RPC error.

## 2. Configuration

| Key | Default | Description |
| --- | --- | --- |
| `WK_PLUGIN_ENABLE` | `false` | Enables the node-local plugin runtime. Disabled by default because plugins execute local binaries. |
| `WK_PLUGIN_DIR` | `${WK_NODE_DATA_DIR}/plugins` | Directory scanned for executable `.wkp` files directly under the directory. |
| `WK_PLUGIN_SOCKET_PATH` | `${WK_NODE_DATA_DIR}/run/plugin.sock` | Unix socket path used by go-pdk plugins to connect to the host. Keep short on macOS. |
| `WK_PLUGIN_SANDBOX_DIR` | `${WK_NODE_DATA_DIR}/plugin-sandbox` | Root for per-plugin writable sandbox directories. |
| `WK_PLUGIN_STATE_DIR` | `${WK_NODE_DATA_DIR}/plugin-state` | Node-local desired config/enabled state as one JSON file per plugin. |
| `WK_PLUGIN_TIMEOUT` | `5s` | Bounds plugin RPC calls, Receive work, and graceful stop waits. |
| `WK_PLUGIN_HOT_RELOAD` | `true` | Watches `WK_PLUGIN_DIR` and restarts changed local `.wkp` processes. |
| `WK_PLUGIN_FAIL_OPEN` | `false` | If false, Send hook failures reject sends; if true, hook call failures continue with the original command. |

## 3. Runtime Layout

At startup, each node does the following when `WK_PLUGIN_ENABLE=true`:

1. Creates plugin, socket, sandbox, and state directories.
2. Starts the PDK-compatible Unix socket host RPC server.
3. Scans `WK_PLUGIN_DIR` for executable `.wkp` files.
4. Starts each enabled plugin process as `plugin.wkp --socket <socket> --sandbox <sandbox/plugin_no>`.
5. Waits for the plugin to connect and call `/plugin/start` with its manifest.

`WK_PLUGIN_STATE_DIR` stores node-local desired config and enabled state. It is not a cluster store. Cluster-wide UID bindings are stored in Slot metadata.

## 4. Supported Plugin Methods

| Method | Behavior |
| --- | --- |
| `Send` | Runs before message append through the message usecase hook chain. Plugins are ordered by priority descending, then plugin number. |
| `PersistAfter` | Runs after a durable message is committed, only on the channel owner node. Remote owners are reached through node RPC. |
| `Receive` | Runs for eligible offline recipients that are bound to a local running Receive plugin. Selection uses highest priority. |
| `Route` | Handles public `ANY /plugins/:plugin/*path` requests on the local node. |
| `ConfigUpdate` | Runs when manager updates node-local desired plugin config and the plugin is running. |
| `Stop` | Runtime maps graceful shutdown to the legacy `/stop` plugin call before killing the process if needed. |

Receive eligibility is intentionally narrow: durable messages only, non request-scoped, non `SyncOnce`, non `NoPersist`, non temp-channel, sender is not the same UID, and sender is not a system UID. Duplicate Receive calls are suppressed by `messageID + uid` for a short TTL.

## 5. Host RPC Compatibility

The host RPC adapter registers these go-pdk paths:

- `/plugin/start`
- `/close`
- `/message/send`
- `/channel/messages`
- `/plugin/httpForward`
- `/cluster/config`
- `/cluster/channels/belongNode`
- `/conversation/channels`
- `/stream/open`, `/stream/write`, `/stream/close` as explicit unimplemented errors

Plugin-origin `/message/send` maps to `message.SendCommand{Origin: plugin}` and calls `message.App.Send`. It does not bypass permissions, hook recursion protection, `NoPersist`, `SyncOnce`, or cluster append semantics.

## 6. Manager APIs

Node-local plugin runtime APIs:

- `GET /manager/nodes/:node_id/plugins`
- `GET /manager/nodes/:node_id/plugins/:plugin_no`
- `PUT /manager/nodes/:node_id/plugins/:plugin_no/config`
- `POST /manager/nodes/:node_id/plugins/:plugin_no/restart`
- `DELETE /manager/nodes/:node_id/plugins/:plugin_no`

Cluster-wide UID binding APIs:

- `GET /manager/plugin-bindings?uid=...`
- `GET /manager/plugin-bindings?plugin_no=...`
- `POST /manager/plugin-bindings`
- `DELETE /manager/plugin-bindings`

All manager plugin routes require `cluster.plugin` permission. Reads require `r`; writes require `w`.

## 7. Public Routes

`ANY /plugins/:plugin/*path` converts the HTTP request into a plugin `HttpRequest` and invokes the local plugin's `Route` method. Hop-by-hop headers are removed in both directions, and the body limit is 10 MiB.

This route is open in Phase 1 for legacy compatibility. It is node-local: callers must reach a node where the plugin is running or rely on plugin-side host RPC forwarding.

## 8. Failure Semantics

- `WK_PLUGIN_FAIL_OPEN=false`: Send hook state lookup or invocation failures fail closed with system error; plugin-returned non-success reason rejects the send.
- `WK_PLUGIN_FAIL_OPEN=true`: Send hook lookup/invocation failures continue with the original command; plugin-returned non-success reason still rejects the send.
- PersistAfter failures are logged and do not fail the committed message path.
- Receive failures are logged; dedupe is cleared so later retries can invoke the hook again.
- Stream host RPCs always return the Phase 1 unimplemented error.
