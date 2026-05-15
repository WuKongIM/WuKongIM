# Plugin Migration Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build Phase 1 of the WuKongIM plugin subsystem so existing `.wkp` plugins built with `github.com/WuKongIM/go-pdk` can run in the new layered monorepo with core methods, host RPC compatibility, node-scoped management, cluster-authoritative user bindings, and message hook integration.

**Architecture:** Keep plugin process/socket mechanics node-local in `internal/runtime/plugin`, PDK host RPC transport adaptation in `internal/access/plugin`, and plugin business orchestration in `internal/usecase/plugin`. Store plugin-user bindings through Slot Raft metadata; wire all dependencies only from `internal/app`; never bypass cluster semantics, even in a single-node cluster.

**Tech Stack:** Go 1.23, gin, Viper config, `github.com/WuKongIM/wkrpc`, protobuf-generated `pluginproto`, fsnotify, Slot Raft metadata (`pkg/slot/meta`, `pkg/slot/fsm`, `pkg/slot/proxy`), existing message/delivery/conversation/manager/node adapters.

---

## Source References

- Spec: `docs/superpowers/specs/2026-05-15-plugin-migration-design.md`
- Official plugin docs: `https://docs.githubim.com/zh/getting-started/learning/plugin-development`
- Legacy plugin implementation: `learn_project/WuKongIM/internal/plugin/`
- Legacy protobuf schema: `learn_project/WuKongIM/internal/types/pluginproto/plugin.proto`
- Local Go PDK clone: `/Users/tt/Desktop/work/go/go-pdk`
- Architecture rules: `AGENTS.md`, `internal/FLOW.md`, `pkg/slot/FLOW.md`

## Scope Boundary

Phase 1 includes:

- `.wkp` process scanning/start/stop and PDK `wkrpc` socket compatibility.
- Plugin methods: `Send`, `PersistAfter`, `Receive`, `Route`, `ConfigUpdate`, `Stop`.
- Host RPC paths: `/plugin/start`, `/close`, `/message/send`, `/channel/messages`, `/plugin/httpForward`, `/cluster/config`, `/cluster/channels/belongNode`, `/conversation/channels`.
- Stable unimplemented errors for `/stream/open`, `/stream/write`, `/stream/close`.
- Public route `ANY /plugins/:plugin/*path`.
- Node-scoped manager plugin inventory/config/restart/delete APIs.
- Cluster-authoritative user binding metadata and manager binding APIs.
- Send hook, owner-routed PersistAfter, and offline Receive observer.

Phase 1 excludes marketplace install, plugin downloads, stream APIs, and committed replay for plugin side effects.

## Implementation Safety Rules

- Do not touch or revert unrelated dirty worktree files. Stage/commit only files from the current task.
- Before editing any package, check whether that package has `FLOW.md`; update it if behavior changes.
- Keep `wkrpc` imports only in `internal/runtime/plugin` and `internal/access/plugin`.
- Keep `internal/runtime/plugin` byte-oriented; it must not import `internal/usecase/plugin` or `pluginproto`.
- Keep `internal/runtime/plugin` free of business usecase calls.
- Keep `internal/access/plugin`, `internal/access/api`, and `internal/access/manager` as adapters only.
- Add English comments for key structs, fields, exported types, and config fields.
- Update `wukongim.conf.example` whenever new config keys are parsed.
- Use "single-node cluster" in docs/comments/tests when describing deployment shape.
- Run focused tests after every task; run boundary tests whenever imports change.

## File Structure

### New Packages

- `internal/usecase/plugin/`
  - `types.go` — plugin method/status DTOs, durable manifest/config DTOs, hook commands, manager response DTOs.
  - `deps.go` — narrow interfaces for runtime registry/invoker, binding store, message sender/reader, cluster reader, conversation reader, HTTP/node forwarding.
  - `app.go` — plugin usecase constructor and option validation.
  - `registry_view.go` — deterministic running plugin filtering, priority ordering, redaction helpers.
  - `config.go` — local config update orchestration and `ConfigUpdate` invocation.
  - `binding.go` — user binding command/query orchestration and cache invalidation.
  - `send_hook.go` — `message.SendHook` implementation.
  - `persist_after.go` — committed message to `pluginproto.MessageBatch` conversion and invocation.
  - `receive.go` — offline Receive selection, eligibility filtering, and TTL dedupe.
  - `host_rpc.go` — usecase-side host RPC operations for `message/send`, channel messages, cluster config, channel owner lookup, conversation channels, HTTP forward.
  - `mapping.go` — conversions between message/channel/conversation DTOs and `pluginproto` DTOs.
  - `pluginproto/` — legacy wire schema, generated Go files, and marshal helpers.

- `internal/runtime/plugin/`
  - `types.go` — runtime plugin status, observed manifest, process spec, runtime options.
  - `registry.go` — concurrency-safe running plugin registry and deterministic priority indexes.
  - `invoker.go` — byte-oriented `wkrpc` request/send primitives by plugin number, path, and message type; no `pluginproto` imports.
  - `socket.go` — Unix socket `wkrpc` server wrapper and route registration surface.
  - `process.go` — `.wkp` process start/stop with `--socket` and `--sandbox` args.
  - `scanner.go` — `.wkp` discovery and path validation.
  - `watcher.go` — fsnotify hot reload dedupe.
  - `store.go` — node-local durable desired config/enable state, stored under `WK_PLUGIN_STATE_DIR`.
  - `lifecycle.go` — runtime start/stop orchestration.

- `internal/access/plugin/`
  - `server.go` — PDK host RPC adapter constructor and route registration.
  - `codec.go` — `pluginproto` request/response marshal helpers, body size limits, and host RPC timeout helper.
  - `handlers_lifecycle.go` — `/plugin/start`, `/close`, stream unimplemented handlers.
  - `handlers_message.go` — `/message/send`, `/channel/messages`.
  - `handlers_cluster.go` — `/cluster/config`, `/cluster/channels/belongNode`.
  - `handlers_http.go` — `/plugin/httpForward`.
  - `handlers_conversation.go` — `/conversation/channels`.

### Existing Packages To Modify

- `go.mod`, `go.sum` — add `github.com/WuKongIM/wkrpc v0.0.0-20250312122115-5e44de72d2c8`; fsnotify already exists indirectly and may become direct.
- `cmd/wukongim/config.go`, `cmd/wukongim/config_test.go` — parse `WK_PLUGIN_*` keys.
- `internal/app/config.go`, `internal/app/config_test.go` — add `PluginConfig`, defaults, validation, config-key coverage.
- `wukongim.conf.example` — document all `WK_PLUGIN_*` keys and disabled-by-default safety note.
- `internal/app/app.go`, `internal/app/build.go`, `internal/app/lifecycle_components.go`, `internal/app/*plugin*.go` — wire plugin runtime/usecase/access, hooks, and lifecycle.
- `internal/usecase/message/command.go`, `internal/usecase/message/deps.go`, `internal/usecase/message/send.go`, `internal/usecase/message/send_test.go` — add send hook contract and recursion controls.
- `internal/runtime/delivery/` and/or `internal/app/deliveryrouting.go` — expose offline UID observer and owner-routed plugin committed side effect.
- `internal/access/api/options.go`, `internal/access/api/routes.go`, `internal/access/api/plugin.go`, tests — public `/plugins/:plugin/*path` adapter.
- `internal/access/manager/options.go`, `internal/access/manager/routes.go`, `internal/access/manager/plugin.go`, `internal/access/manager/permissions.go`, tests — manager plugin and binding APIs.
- `internal/access/node/options.go`, `internal/access/node/service_ids.go`, `internal/access/node/plugin_rpc.go`, `internal/access/node/plugin_codec.go`, tests — remote node-scoped plugin management and optional HTTP forward support.
- `internal/usecase/management/app.go`, `internal/usecase/management/plugin.go`, tests — manager aggregation around local/remote plugin usecase and binding store.
- `pkg/slot/meta/catalog.go`, `pkg/slot/meta/plugin_binding.go`, tests — `plugin_user_binding` table and indexes.
- `pkg/slot/meta/batch.go`, `pkg/slot/meta/snapshot*.go`, tests — write batch and snapshot support for plugin bindings.
- `pkg/slot/fsm/command.go`, `pkg/slot/fsm/plugin_binding_cmds.go`, tests — bind/unbind commands.
- `pkg/slot/proxy/store.go`, `pkg/slot/proxy/plugin_binding_rpc.go`, `pkg/slot/proxy/plugin_binding_codec.go`, tests — authoritative binding APIs and plugin-centric fanout listing.
- `internal/FLOW.md`, `pkg/slot/FLOW.md`, `docs/development/PROJECT_KNOWLEDGE.md` — concise docs updates after behavior changes.

## Task 1: Config And Disabled Lifecycle Skeleton

**Files:**
- Modify: `internal/app/config.go`
- Modify: `cmd/wukongim/config.go`
- Modify: `internal/app/config_test.go`
- Modify: `cmd/wukongim/config_test.go`
- Modify: `wukongim.conf.example`

- [ ] **Step 1: Write config default tests**

Add tests proving plugin config is disabled by default and derives paths from `WK_NODE_DATA_DIR`:

```go
func TestConfigApplyDefaultsDerivesPluginPathsFromDataDir(t *testing.T) {
    cfg := validMinimalConfig(t)
    cfg.Plugin.Enable = true
    require.NoError(t, cfg.ApplyDefaultsAndValidate())
    require.Equal(t, filepath.Join(cfg.Node.DataDir, "plugins"), cfg.Plugin.Dir)
    require.Equal(t, filepath.Join(cfg.Node.DataDir, "run", "plugin.sock"), cfg.Plugin.SocketPath)
    require.Equal(t, filepath.Join(cfg.Node.DataDir, "plugin-sandbox"), cfg.Plugin.SandboxDir)
    require.Equal(t, filepath.Join(cfg.Node.DataDir, "plugin-state"), cfg.Plugin.StateDir)
    require.Equal(t, 5*time.Second, cfg.Plugin.Timeout)
    require.True(t, cfg.Plugin.HotReload)
    require.False(t, cfg.Plugin.FailOpen)
}
```

- [ ] **Step 2: Run default tests and verify failure**

Run: `go test ./internal/app -run 'TestConfigApplyDefaultsDerivesPluginPathsFromDataDir|TestConfigExampleMentionsEverySupportedKey' -count=1`

Expected: FAIL because `Config.Plugin` and `WK_PLUGIN_*` keys do not exist.

- [ ] **Step 3: Write config parsing tests**

Add `cmd/wukongim` tests for config file values and environment override:

```go
func TestLoadConfigParsesPluginConfig(t *testing.T) {
    dir := t.TempDir()
    cfg := loadConfigFromLines(t,
        "WK_NODE_ID=1",
        "WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
        "WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
        `WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
        "WK_CLUSTER_SLOT_COUNT=1",
        "WK_GATEWAY_LISTENERS=[]",
        "WK_PLUGIN_ENABLE=true",
        "WK_PLUGIN_DIR="+filepath.Join(dir, "plugins"),
        "WK_PLUGIN_SOCKET_PATH="+filepath.Join(dir, "run", "plugin.sock"),
        "WK_PLUGIN_SANDBOX_DIR="+filepath.Join(dir, "sandbox"),
        "WK_PLUGIN_STATE_DIR="+filepath.Join(dir, "state"),
        "WK_PLUGIN_TIMEOUT=7s",
        "WK_PLUGIN_HOT_RELOAD=false",
        "WK_PLUGIN_FAIL_OPEN=true",
    )
    require.True(t, cfg.Plugin.Enable)
    require.Equal(t, 7*time.Second, cfg.Plugin.Timeout)
    require.False(t, cfg.Plugin.HotReload)
    require.True(t, cfg.Plugin.FailOpen)
}
```

- [ ] **Step 4: Run parsing tests and verify failure**

Run: `go test ./cmd/wukongim -run TestLoadConfigParsesPluginConfig -count=1`

Expected: FAIL because parser ignores `WK_PLUGIN_*`.

- [ ] **Step 5: Implement `PluginConfig`**

Add to `internal/app/config.go`:

```go
// PluginConfig controls node-local PDK-compatible plugin execution.
type PluginConfig struct {
    // Enable starts the node-local plugin runtime. It defaults to false because plugins execute local binaries.
    Enable bool
    // Dir contains local .wkp plugin binaries for this node.
    Dir string
    // SocketPath is the Unix socket path used by PDK plugins to call the host.
    SocketPath string
    // SandboxDir is the per-plugin writable directory root passed to plugin processes.
    SandboxDir string
    // StateDir stores node-local desired plugin config and enable state.
    StateDir string
    // Timeout bounds plugin RPC calls and graceful stop waits.
    Timeout time.Duration
    // HotReload watches Dir for .wkp changes and restarts affected local plugins.
    HotReload bool
    // FailOpen lets sends continue when Send hooks fail; false rejects sends on plugin errors.
    FailOpen bool

    hotReloadSet bool
    failOpenSet  bool
}
```

Add `Plugin PluginConfig` to `Config` with English field comment.

- [ ] **Step 6: Implement defaults and validation**

In `ApplyDefaultsAndValidate`:

```go
if c.Plugin.Dir == "" { c.Plugin.Dir = filepath.Join(c.Node.DataDir, "plugins") }
if c.Plugin.SocketPath == "" { c.Plugin.SocketPath = filepath.Join(c.Node.DataDir, "run", "plugin.sock") }
if c.Plugin.SandboxDir == "" { c.Plugin.SandboxDir = filepath.Join(c.Node.DataDir, "plugin-sandbox") }
if c.Plugin.StateDir == "" { c.Plugin.StateDir = filepath.Join(c.Node.DataDir, "plugin-state") }
if c.Plugin.Timeout <= 0 { c.Plugin.Timeout = 5 * time.Second }
if !c.Plugin.HotReload && !c.Plugin.hotReloadSet { c.Plugin.HotReload = true }
if c.Plugin.Timeout <= 0 { return fmt.Errorf("%w: plugin timeout must be positive", ErrInvalidConfig) }
```

Normalize plugin paths using the existing storage path normalizer. Do not add plugin paths to strict storage overlap isolation unless the code intentionally requires plugins to never live under `Node.DataDir`; plugin defaults are subdirectories of `Node.DataDir` by design.

- [ ] **Step 7: Parse `WK_PLUGIN_*` keys**

In `cmd/wukongim/config.go`, parse:

```text
WK_PLUGIN_ENABLE
WK_PLUGIN_DIR
WK_PLUGIN_SOCKET_PATH
WK_PLUGIN_SANDBOX_DIR
WK_PLUGIN_STATE_DIR
WK_PLUGIN_TIMEOUT
WK_PLUGIN_HOT_RELOAD
WK_PLUGIN_FAIL_OPEN
```

Set explicit flags for hot reload and fail-open booleans.

- [ ] **Step 8: Update `wukongim.conf.example`**

Add a plugin section with all keys, disabled by default:

```env
# Plugins execute local .wkp binaries and are disabled by default for safety.
WK_PLUGIN_ENABLE=false
WK_PLUGIN_DIR=
WK_PLUGIN_SOCKET_PATH=
WK_PLUGIN_SANDBOX_DIR=
WK_PLUGIN_STATE_DIR=
WK_PLUGIN_TIMEOUT=5s
WK_PLUGIN_HOT_RELOAD=true
WK_PLUGIN_FAIL_OPEN=false
```

- [ ] **Step 9: Run focused config tests**

Run: `go test ./internal/app ./cmd/wukongim -run 'Plugin|ExampleMentionsEverySupportedKey|LoadConfigParsesPluginConfig' -count=1`

Expected: PASS.

- [ ] **Step 10: Commit config skeleton**

```bash
git add internal/app/config.go internal/app/config_test.go cmd/wukongim/config.go cmd/wukongim/config_test.go wukongim.conf.example
git commit -m "feat: add plugin runtime config"
```

## Task 2: Plugin Protocol Compatibility Package

**Files:**
- Create: `internal/usecase/plugin/pluginproto/plugin.proto`
- Create: `internal/usecase/plugin/pluginproto/plugin.pb.go`
- Create: `internal/usecase/plugin/pluginproto/plugin.go`
- Create: `internal/usecase/plugin/pluginproto/README.md`
- Modify: `go.mod`, `go.sum` if generated protobuf tooling changes module metadata

- [ ] **Step 1: Write wire compatibility tests**

Create `internal/usecase/plugin/pluginproto/compat_test.go` with tests that marshal/unmarshal `PluginInfo`, `SendPacket`, `StartupResp`, `ForwardHttpReq`, stream DTOs, and `SendReq`. This package is used only by usecase/access code; runtime remains byte-oriented.

Use legacy generated package from `learn_project/WuKongIM/internal/types/pluginproto` only as a test fixture if importable; otherwise embed golden bytes generated from the old package in test data.

- [ ] **Step 2: Run protocol tests and verify failure**

Run: `go test ./internal/usecase/plugin/pluginproto -run TestPluginProtoCompatibility -count=1`

Expected: FAIL because the package does not exist.

- [ ] **Step 3: Copy legacy schema**

Copy schema from `learn_project/WuKongIM/internal/types/pluginproto/plugin.proto` into `internal/usecase/plugin/pluginproto/plugin.proto`. Keep field numbers exactly unchanged. Set:

```proto
option go_package = "github.com/WuKongIM/WuKongIM/internal/usecase/plugin/pluginproto;pluginproto";
```

- [ ] **Step 4: Generate or copy generated Go**

Preferred:

```bash
protoc --go_out=. --go_opt=paths=source_relative internal/usecase/plugin/pluginproto/plugin.proto
```

If local `protoc` is unavailable, copy `plugin.pb.go` and `plugin.go` from the legacy tree and update only package path/source comments as needed. Do not change wire fields.

- [ ] **Step 5: Add marshal helper coverage**

Ensure `plugin.go` exposes the same helper methods the PDK expects in host-side code:

```go
func (p *PluginInfo) Marshal() ([]byte, error)
func (p *PluginInfo) Unmarshal([]byte) error
func (s *SendPacket) Marshal() ([]byte, error)
func (s *SendPacket) Unmarshal([]byte) error
// Repeat for every message used by Phase 1 host RPCs and stream unimplemented decoders.
```

- [ ] **Step 6: Run protocol tests**

Run: `go test ./internal/usecase/plugin/pluginproto -count=1`

Expected: PASS.

- [ ] **Step 7: Commit protocol package**

```bash
git add internal/usecase/plugin/pluginproto go.mod go.sum
git commit -m "feat: add plugin protocol compatibility types"
```

## Task 3: Runtime Registry, Local Store, And Plugin Invoker

**Files:**
- Create: `internal/runtime/plugin/types.go`
- Create: `internal/runtime/plugin/registry.go`
- Create: `internal/runtime/plugin/registry_test.go`
- Create: `internal/runtime/plugin/store.go`
- Create: `internal/runtime/plugin/store_test.go`
- Create: `internal/runtime/plugin/invoker.go`
- Create: `internal/runtime/plugin/invoker_test.go`
- Modify: `go.mod`, `go.sum` for `github.com/WuKongIM/wkrpc v0.0.0-20250312122115-5e44de72d2c8`

- [ ] **Step 1: Write registry priority tests**

Test that larger priority sorts first, equal priority sorts by plugin number ascending, disabled/offline plugins are skipped, and method filtering works:

```go
func TestRegistryRunningByMethodOrdersByPriorityThenPluginNo(t *testing.T) {
    r := NewRegistry()
    r.Upsert(ObservedPlugin{No: "b", Priority: 10, Methods: []Method{MethodSend}, Status: StatusRunning, Enabled: true})
    r.Upsert(ObservedPlugin{No: "a", Priority: 10, Methods: []Method{MethodSend}, Status: StatusRunning, Enabled: true})
    r.Upsert(ObservedPlugin{No: "c", Priority: 11, Methods: []Method{MethodSend}, Status: StatusRunning, Enabled: true})
    got := r.RunningByMethod(MethodSend)
    require.Equal(t, []string{"c", "a", "b"}, pluginNos(got))
}
```

- [ ] **Step 2: Run registry tests and verify failure**

Run: `go test ./internal/runtime/plugin -run TestRegistry -count=1`

Expected: FAIL because package is missing.

- [ ] **Step 3: Implement runtime types and registry**

Define:

```go
type Method string
const (
    MethodSend Method = "Send"
    MethodPersistAfter Method = "PersistAfter"
    MethodReceive Method = "Receive"
    MethodRoute Method = "Route"
    MethodConfigUpdate Method = "ConfigUpdate"
)

type Status string
const (
    StatusStarting Status = "starting"
    StatusRunning Status = "running"
    StatusOffline Status = "offline"
    StatusError Status = "error"
    StatusDisabled Status = "disabled"
)

type ObservedPlugin struct { /* No, observed fields, PID, Enabled, LastSeenAt, LastError */ }
```

Implement concurrency-safe `Registry` with `Upsert`, `Remove`, `Get`, `List`, `RunningByMethod`, `HighestRunningForUID(candidates []string, method Method)`.

- [ ] **Step 4: Write local store tests**

Test atomic save/load, missing plugin returns empty disabled/default record, corrupt JSON returns error, plugin number path traversal is rejected.

- [ ] **Step 5: Implement node-local state store**

Use one JSON file per plugin under `WK_PLUGIN_STATE_DIR`:

```go
type DesiredState struct {
    No string `json:"no"`
    Config json.RawMessage `json:"config,omitempty"`
    Enabled bool `json:"enabled"`
    CreatedAt time.Time `json:"created_at"`
    UpdatedAt time.Time `json:"updated_at"`
}
```

Validate `No` with a strict filename-safe rule (`[A-Za-z0-9._-]+`, no slash). Write through temp file + `os.Rename`.

- [ ] **Step 6: Write byte invoker tests with fake transport**

Define a narrow byte-oriented `RPCClient` interface so tests do not need real sockets and `internal/runtime/plugin` never imports `internal/usecase/*`:

```go
type RPCClient interface {
    Request(ctx context.Context, uid, path string, body []byte) ([]byte, error)
    Send(uid string, msgType uint32, body []byte) error
}
```

Test `RequestPlugin(ctx, no, path, body)` preserves path/body, `SendPlugin(no, msgType, body)` preserves message type/body, and `Stop(ctx, no)` uses `/stop`. Do not reference `pluginproto` DTOs in runtime tests.

- [ ] **Step 7: Implement byte invoker**

Implement only generic primitives:

```go
func (i *Invoker) RequestPlugin(ctx context.Context, no, path string, body []byte) ([]byte, error)
func (i *Invoker) SendPlugin(no string, msgType uint32, body []byte) error
func (i *Invoker) Stop(ctx context.Context, no string) error // calls /stop
```

Use `context.WithTimeout` with the runtime timeout option derived from `cfg.Plugin.Timeout` in `internal/app`; `internal/runtime/plugin` must not import `internal/app`. Preserve a shorter caller deadline when one already exists. Legacy method path/message-type mapping and `pluginproto` marshaling belong in `internal/usecase/plugin`, not in runtime.

- [ ] **Step 8: Add dependency**

Run: `go get github.com/WuKongIM/wkrpc@v0.0.0-20250312122115-5e44de72d2c8`

Expected: `go.mod` adds `wkrpc`; `go.sum` updates.

- [ ] **Step 9: Run runtime tests**

Run: `go test ./internal/runtime/plugin -count=1`

Expected: PASS.

- [ ] **Step 10: Run import boundary test**

Run: `GOWORK=off go test ./internal -run TestInternalImportBoundaries -count=1`

Expected: PASS; runtime package must not import usecase/access/app or `internal/usecase/plugin/pluginproto`.

- [ ] **Step 11: Commit runtime core**

```bash
git add internal/runtime/plugin go.mod go.sum
git commit -m "feat: add plugin runtime registry and invoker"
```

## Task 4: Runtime Socket, Process Lifecycle, Scanner, And Watcher

**Files:**
- Create: `internal/runtime/plugin/socket.go`
- Create: `internal/runtime/plugin/socket_test.go`
- Create: `internal/runtime/plugin/process.go`
- Create: `internal/runtime/plugin/process_test.go`
- Create: `internal/runtime/plugin/scanner.go`
- Create: `internal/runtime/plugin/scanner_test.go`
- Create: `internal/runtime/plugin/watcher.go`
- Create: `internal/runtime/plugin/watcher_test.go`
- Create: `internal/runtime/plugin/lifecycle.go`
- Create: `internal/runtime/plugin/lifecycle_test.go`

- [ ] **Step 1: Write scanner tests**

Test discovery includes executable `.wkp` files, ignores directories/non-`.wkp`, sorts by path, rejects paths outside configured dir after symlink resolution.

- [ ] **Step 2: Implement scanner**

Implement `ScanPlugins(dir string) ([]ProcessSpec, error)` using `os.ReadDir`, `filepath.Clean`, `filepath.EvalSymlinks` where possible.

- [ ] **Step 3: Write process lifecycle tests**

Use a tiny test helper binary pattern (`go test` re-exec with `WK_PLUGIN_PROCESS_HELPER=1`) to assert args contain `--socket` and `--sandbox`, sandbox dir is per plugin, and graceful stop kills after timeout.

- [ ] **Step 4: Implement process manager**

Define:

```go
type ProcessSpec struct { No string; Path string }
type ProcessHandle struct { Spec ProcessSpec; PID int; StartedAt time.Time }
```

Start with `exec.CommandContext(ctx, spec.Path, "--socket", socketPath, "--sandbox", sandboxPath)`. Do not inject plugin config as env vars.

- [ ] **Step 5: Write socket server tests**

Test Unix socket path parent dir is created, stale socket is removed, route registration delegates to `wkrpc`, start/stop are idempotent.

- [ ] **Step 6: Implement socket wrapper**

Wrap `wkrpc.New("unix://"+socketPath)`, expose:

```go
type SocketServer interface {
    Route(path string, handler func(*wkrpc.Context))
    Start() error
    Stop()
    RequestWithContext(ctx context.Context, uid, path string, body []byte) ([]byte, error)
    Send(uid string, msg *proto.Message) error
}
```

Keep this package as one of the only `wkrpc` import sites.

- [ ] **Step 7: Write watcher tests**

Test duplicate create/write events for same `.wkp` are debounced into one restart request and non-plugin files are ignored.

- [ ] **Step 8: Implement watcher**

Use `fsnotify` with bounded debounce. Inject a test clock/timer so watcher tests never sleep. Only enable when `WK_PLUGIN_HOT_RELOAD=true`.

- [ ] **Step 9: Write lifecycle tests**

Test disabled runtime does nothing; enabled runtime creates dirs, starts socket before processes, stops watcher before processes, sends `/stop` then kills on timeout.

- [ ] **Step 10: Implement lifecycle orchestration**

Create `Runtime` with `Start(ctx)`, `Stop(ctx)`, `Restart(ctx, pluginNo)`, `Uninstall(ctx, pluginNo)`.

- [ ] **Step 11: Run runtime lifecycle tests**

Run: `go test ./internal/runtime/plugin -count=1`

Expected: PASS.

- [ ] **Step 12: Commit runtime lifecycle**

```bash
git add internal/runtime/plugin
git commit -m "feat: add plugin process runtime lifecycle"
```

## Task 5: Access Plugin Host RPC Adapter Kernel

**Files:**
- Create: `internal/access/plugin/server.go`
- Create: `internal/access/plugin/codec.go`
- Create: `internal/access/plugin/handlers_lifecycle.go`
- Create: `internal/access/plugin/server_test.go`
- Create: `internal/access/plugin/handlers_lifecycle_test.go`
- Create: `internal/access/plugin/codec_test.go`

- [ ] **Step 1: Write adapter route registration test**

Use a fake route registrar and assert these paths are registered:

```text
/plugin/start
/close
/message/send
/channel/messages
/plugin/httpForward
/cluster/config
/cluster/channels/belongNode
/conversation/channels
/stream/open
/stream/write
/stream/close
```

- [ ] **Step 2: Run registration test and verify failure**

Run: `go test ./internal/access/plugin -run TestServerRegistersPDKHostRoutes -count=1`

Expected: FAIL because package does not exist.

- [ ] **Step 3: Implement adapter constructor**

Define `Options` with `Routes`, `Usecase`, `MaxBodyBytes`, `Timeout`, `Logger`, `Now`. Default `MaxBodyBytes` to `DefaultHostRPCMaxBodyBytes = 10 << 20` and require `Timeout > 0` from `WK_PLUGIN_TIMEOUT` when the adapter is built from `internal/app`.

- [ ] **Step 4: Add host RPC timeout helper tests**

Test every plugin-origin host RPC handler wraps usecase calls with `WK_PLUGIN_TIMEOUT` unless the incoming context already has a shorter deadline. Include at least one table-driven test covering `/plugin/start`, `/close`, `/message/send`, `/channel/messages`, `/cluster/config`, `/cluster/channels/belongNode`, `/conversation/channels`, and `/plugin/httpForward` by asserting the fake usecase observes a deadline no later than the configured timeout.

- [ ] **Step 5: Implement host RPC timeout helper**

Add one adapter helper used by all handlers:

```go
func (s *Server) hostRPCContext(ctx context.Context) (context.Context, context.CancelFunc) {
    if s.timeout <= 0 {
        return context.WithCancel(ctx)
    }
    if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) <= s.timeout {
        return context.WithCancel(ctx)
    }
    return context.WithTimeout(ctx, s.timeout)
}
```

Use this helper before calling the usecase from every Phase 1 host RPC handler.

- [ ] **Step 6: Write `/plugin/start` tests**

Test valid `PluginInfo` calls usecase, returns `StartupResp` with node ID, sandbox dir, config. Test empty plugin number returns transport error.

- [ ] **Step 7: Implement `/plugin/start` and `/close` handlers**

Decode `pluginproto.PluginInfo`; call usecase `StartPlugin(ctx, info, callerUID)`; encode `StartupResp`. `/close` uses caller UID/plugin number from `wkrpc.Context`.

- [ ] **Step 8: Write stream unimplemented tests**

Test `/stream/open`, `/stream/write`, `/stream/close` return a stable unimplemented error body/status and log path/plugin number.

- [ ] **Step 9: Implement stable stream errors**

Use one helper:

```go
func writeUnimplemented(c rpcContext, path string) {
    c.WriteErr(ErrUnimplementedStreamRPC)
}
```

Ensure tests assert the error string is stable, e.g. `plugin stream rpc unimplemented in phase 1`.

- [ ] **Step 10: Run access/plugin kernel tests**

Run: `go test ./internal/access/plugin -run 'Register|Lifecycle|Stream|Codec' -count=1`

Expected: PASS.

- [ ] **Step 11: Run import boundary test**

Run: `GOWORK=off go test ./internal -run TestInternalImportBoundaries -count=1`

Expected: PASS; access may import usecase/runtime, usecase must not import access.

- [ ] **Step 12: Commit adapter kernel**

```bash
git add internal/access/plugin
git commit -m "feat: add plugin host rpc adapter kernel"
```

## Task 6: Plugin Usecase Lifecycle, Config, Selection, And Redaction

**Files:**
- Create: `internal/usecase/plugin/types.go`
- Create: `internal/usecase/plugin/deps.go`
- Create: `internal/usecase/plugin/app.go`
- Create: `internal/usecase/plugin/registry_view.go`
- Create: `internal/usecase/plugin/config.go`
- Create: `internal/usecase/plugin/binding.go`
- Create: `internal/usecase/plugin/app_test.go`
- Create: `internal/usecase/plugin/config_test.go`
- Create: `internal/usecase/plugin/selection_test.go`
- Create: `internal/usecase/plugin/redaction_test.go`

- [ ] **Step 1: Write startup/config tests**

Test `StartPlugin` saves observed info in runtime, creates sandbox dir, loads existing desired config, redacts secret fields in list output, and returns JSON config bytes.

- [ ] **Step 2: Run tests and verify failure**

Run: `go test ./internal/usecase/plugin -run 'StartPlugin|Config|Redact' -count=1`

Expected: FAIL because usecase package is missing.

- [ ] **Step 3: Implement usecase types and deps**

Define narrow interfaces, including:

```go
type Runtime interface {
    RegisterObserved(ctx context.Context, info ObservedPlugin) error
    Get(no string) (ObservedPlugin, bool)
    List() []ObservedPlugin
    Restart(ctx context.Context, no string) error
    Uninstall(ctx context.Context, no string) error
    SandboxDir(no string) (string, error)
}

type DesiredStore interface {
    Get(ctx context.Context, no string) (DesiredPlugin, error)
    Save(ctx context.Context, state DesiredPlugin) error
    Delete(ctx context.Context, no string) error
}

type Invoker interface {
    RequestPlugin(ctx context.Context, no, path string, body []byte) ([]byte, error)
    SendPlugin(no string, msgType uint32, body []byte) error
    Stop(ctx context.Context, no string) error
}
```

Usecase methods wrap this byte invoker with legacy mappings:

```text
Send -> marshal SendPacket -> /plugin/send -> unmarshal SendPacket
PersistAfter sync -> marshal MessageBatch -> /plugin/persist_after
PersistAfter async -> SendPlugin msg type 2
Receive sync -> marshal RecvPacket -> /plugin/receive
Receive async -> SendPlugin msg type 3
Route -> marshal HttpRequest -> /plugin/route -> unmarshal HttpResponse
ConfigUpdate -> JSON bytes -> /plugin/config_update
Stop -> runtime Stop(/stop)
```

- [ ] **Step 4: Implement lifecycle methods**

Implement `StartPlugin`, `ClosePlugin`, `ListLocalPlugins`, `GetLocalPlugin`, `UpdateLocalConfig`, `RestartLocalPlugin`, `UninstallLocalPlugin`.

Rules:

- durable desired fields only: `No`, `Config`, `Enabled`, `CreatedAt`, `UpdatedAt`.
- newly discovered `.wkp` files default to `Enabled=true` only when `WK_PLUGIN_ENABLE=true`; disabling a plugin stores `Enabled=false` and stops the process.
- observed fields come only from runtime registry.
- config update invokes plugin `ConfigUpdate` when running and method exists.
- manager `DELETE /manager/nodes/:node_id/plugins/:plugin_no` stops the local process, marks desired state disabled, removes the local `.wkp` binary when it is under `WK_PLUGIN_DIR`, and leaves cluster-wide user bindings untouched.

- [ ] **Step 5: Write selection tests**

Test Send/PersistAfter global plugin order and Receive highest-priority bound plugin selection. Include equal-priority plugin number tie-break.

- [ ] **Step 6: Implement selection helpers**

Implement high-priority ordering once and reuse everywhere.

- [ ] **Step 7: Run usecase lifecycle tests**

Run: `go test ./internal/usecase/plugin -run 'StartPlugin|Config|Selection|Redact' -count=1`

Expected: PASS.

- [ ] **Step 8: Commit usecase lifecycle**

```bash
git add internal/usecase/plugin
git commit -m "feat: add plugin usecase lifecycle"
```

## Task 7: Slot Metadata Plugin Binding Table And FSM Commands

**Files:**
- Modify: `pkg/slot/meta/catalog.go`
- Create: `pkg/slot/meta/plugin_binding.go`
- Create: `pkg/slot/meta/plugin_binding_test.go`
- Modify: `pkg/slot/meta/batch.go`
- Modify: `pkg/slot/meta/snapshot.go`
- Modify: `pkg/slot/meta/snapshot_codec.go`
- Modify: `pkg/slot/meta/snapshot_test.go`
- Modify: `pkg/slot/fsm/command.go`
- Create: `pkg/slot/fsm/plugin_binding_cmds.go`
- Create: `pkg/slot/fsm/plugin_binding_cmds_test.go`
- Modify: `pkg/slot/fsm/state_machine_test.go`
- Modify: `pkg/slot/FLOW.md`

- [ ] **Step 1: Write meta table tests**

Test primary key `(uid, plugin_no)`, secondary index `(plugin_no, uid)`, idempotent bind, idempotent missing unbind, list by UID, scan by plugin number.

- [ ] **Step 2: Run meta tests and verify failure**

Run: `go test ./pkg/slot/meta -run PluginBinding -count=1`

Expected: FAIL because table is missing.

- [ ] **Step 3: Add catalog table**

Add `TableIDPluginUserBinding uint32 = 10` and table desc:

```text
plugin_user_binding
primary key: uid, plugin_no
secondary index: idx_plugin_no_uid(plugin_no, uid)
columns: uid, plugin_no, created_at_ms, updated_at_ms
```

- [ ] **Step 4: Implement meta operations**

Add:

```go
type PluginUserBinding struct {
    UID string
    PluginNo string
    CreatedAtMS int64
    UpdatedAtMS int64
}
```

Implement `BindPluginUser`, `UnbindPluginUser`, `ListPluginBindingsByUID`, `ScanPluginBindingsByPluginNo`, `ExistPluginBindingByUID` on `ShardStore` and `WriteBatch`.

- [ ] **Step 5: Update snapshot export/import**

Ensure new table state and secondary indexes export/import with existing generic table scanning. Add explicit regression test for plugin bindings surviving snapshot round trip.

- [ ] **Step 6: Write FSM command tests**

Test `EncodeBindPluginUserCommand` and `EncodeUnbindPluginUserCommand` decode/apply through state machine using UID hash slot.

- [ ] **Step 7: Implement FSM command IDs**

Use next available command IDs after existing commands:

```go
cmdTypeBindPluginUser uint8 = 42
cmdTypeUnbindPluginUser uint8 = 43
```

Do not reuse reserved/deprecated IDs. Add TLV tags for UID, plugin number, timestamps.

- [ ] **Step 8: Update `pkg/slot/FLOW.md`**

Update table count, table list, and FSM command list. Note UID is the routing key.

- [ ] **Step 9: Run slot meta/fsm tests**

Run: `go test ./pkg/slot/meta ./pkg/slot/fsm -run 'PluginBinding|Snapshot|StateMachine' -count=1`

Expected: PASS.

- [ ] **Step 10: Commit metadata table**

```bash
git add pkg/slot/meta pkg/slot/fsm pkg/slot/FLOW.md
git commit -m "feat: add plugin user binding metadata"
```

## Task 8: Slot Proxy Binding APIs And Plugin-Centric Listing

**Files:**
- Modify: `pkg/slot/proxy/store.go`
- Create: `pkg/slot/proxy/plugin_binding_rpc.go`
- Create: `pkg/slot/proxy/plugin_binding_codec.go`
- Create: `pkg/slot/proxy/plugin_binding_rpc_test.go`
- Create: `pkg/slot/proxy/plugin_binding_codec_test.go`
- Modify: `pkg/slot/FLOW.md`

- [ ] **Step 1: Write proxy binding tests**

Test APIs:

```go
BindPluginUser(ctx, uid, pluginNo)
UnbindPluginUser(ctx, uid, pluginNo)
ListPluginBindingsByUID(ctx, uid)
ListPluginBindingsByPluginNo(ctx, pluginNo, cursor, limit)
ExistPluginBindingByUID(ctx, uid)
```

Verify UID is used as `HashSlotForKey` key.

- [ ] **Step 2: Run proxy tests and verify failure**

Run: `go test ./pkg/slot/proxy -run PluginBinding -count=1`

Expected: FAIL because APIs are missing.

- [ ] **Step 3: Implement authoritative RPC codec**

Add request ops:

```text
bind
unbind
list_by_uid
scan_by_plugin_no
exists_by_uid
```

Use binary codec following nearby `cmd_conversation_state_codec.go` style, not JSON.

- [ ] **Step 4: Implement proxy methods**

Writes use `cluster.ProposeWithHashSlot` with UID key. UID queries use `callAuthoritativeRPC`. Plugin-centric listing fans out over authoritative slot leaders, scans each slot's `idx_plugin_no_uid`, and returns a deterministic merged page.

- [ ] **Step 5: Implement opaque cursor**

Cursor contains last scanned physical slot/hash slot plus `(plugin_no, uid)` tuple. Encode as base64 URL-safe JSON or existing binary cursor style. Tests must cover invalid cursor rejection and stable ordering.

- [ ] **Step 6: Register RPC handler**

Add a new service ID in the proxy range (next free after `cmdConversationStateRPCServiceID` unless already taken). Add a collision test that enumerates proxy service IDs and fails on duplicates. Update `proxy.New` registration and `pkg/slot/FLOW.md` RPC section.

- [ ] **Step 7: Run proxy tests**

Run: `go test ./pkg/slot/proxy -run PluginBinding -count=1`

Expected: PASS.

- [ ] **Step 8: Commit binding proxy**

```bash
git add pkg/slot/proxy pkg/slot/FLOW.md
git commit -m "feat: expose plugin binding slot proxy"
```

## Task 9: Usecase Binding Cache And Manager Binding Semantics

**Files:**
- Modify: `internal/usecase/plugin/binding.go`
- Create: `internal/usecase/plugin/binding_test.go`
- Create: `internal/usecase/plugin/cache.go`
- Create: `internal/usecase/plugin/cache_test.go`

- [ ] **Step 1: Write binding usecase tests**

Test bind/unbind call cluster-authoritative store, invalidate UID cache, `ListBindingsByUID`, `ListBindingsByPluginNo`, stale binding to absent local plugin is allowed with warning metadata.

- [ ] **Step 2: Implement binding store interface adapter**

Use `pkg/slot/proxy.Store` through narrow interface in `internal/usecase/plugin`:

```go
type BindingStore interface {
    BindPluginUser(ctx context.Context, uid, pluginNo string) error
    UnbindPluginUser(ctx context.Context, uid, pluginNo string) error
    ListPluginBindingsByUID(ctx context.Context, uid string) ([]Binding, error)
    ListPluginBindingsByPluginNo(ctx context.Context, pluginNo, cursor string, limit int) (BindingPage, error)
    ExistPluginBindingByUID(ctx context.Context, uid string) (bool, error)
}
```

- [ ] **Step 3: Implement short TTL cache**

Cache only UID binding results and selected highest-priority plugin; invalidate on bind/unbind. Keep cache bounded and optional; tests should not depend on wall-clock sleeps (inject clock).

- [ ] **Step 4: Run usecase binding tests**

Run: `go test ./internal/usecase/plugin -run 'Binding|Cache' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit binding usecase**

```bash
git add internal/usecase/plugin
git commit -m "feat: add plugin binding usecase"
```

## Task 10: Message Send Hook Contract And Plugin Send Hook

**Files:**
- Modify: `internal/usecase/message/command.go`
- Modify: `internal/usecase/message/deps.go`
- Modify: `internal/usecase/message/send.go`
- Modify: `internal/usecase/message/app.go`
- Modify: `internal/usecase/message/send_test.go`
- Modify: `internal/usecase/plugin/send_hook.go`
- Create: `internal/usecase/plugin/send_hook_test.go`

- [ ] **Step 1: Write message hook tests**

Add tests in `internal/usecase/message`:

- client send invokes hook after basic validation and permission checks.
- request-scoped `SyncOnce` send invokes hook exactly once.
- hook mutation changes payload before durable/realtime dispatch.
- hook reason rejection returns that reason.
- plugin-origin send increments depth and skips/rejects when max depth exceeded.

- [ ] **Step 2: Run message hook tests and verify failure**

Run: `go test ./internal/usecase/message -run 'SendHook|RequestScoped' -count=1`

Expected: FAIL because hook contract is missing and request-scoped currently branches early.

- [ ] **Step 3: Implement message hook contract**

Add to `command.go` / `deps.go`:

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

Add `Origin`, `HookDepth`, `SkipPluginHooks` to `SendCommand` with comments. Define `const DefaultPluginSendMaxHookDepth = 1` in the message or plugin usecase package and use it consistently in tests.

- [ ] **Step 4: Refactor send flow**

Invoke hook after normalization/permission and before NoPersist, SyncOnce, request-scoped, or durable append branches. Ensure request-scoped branch calls the common hook helper exactly once.

- [ ] **Step 5: Write plugin send hook tests**

In `internal/usecase/plugin`, fake registry/invoker to test ordered chain, payload mutation, reason rejection, timeout error fail-closed, and fail-open continuing original command.

- [ ] **Step 6: Implement plugin `SendHook`**

Map `message.SendCommand` to `pluginproto.SendPacket`, including `Conn` fields from sender session/device. Iterate running `Send` plugins in priority order. Convert returned payload and reason back to message command/result.

- [ ] **Step 7: Run hook tests**

Run: `go test ./internal/usecase/message ./internal/usecase/plugin -run 'SendHook|RequestScoped' -count=1`

Expected: PASS.

- [ ] **Step 8: Commit send hook**

```bash
git add internal/usecase/message internal/usecase/plugin
git commit -m "feat: add plugin send hook"
```

## Task 11: Plugin Host RPC Message And Channel Handlers

**Files:**
- Modify: `internal/access/plugin/handlers_message.go`
- Create: `internal/access/plugin/handlers_message_test.go`
- Modify: `internal/usecase/plugin/host_rpc.go`
- Modify: `internal/usecase/plugin/mapping.go`
- Create: `internal/usecase/plugin/host_rpc_message_test.go`
- Modify: `internal/usecase/message/deps.go` if reader interface needs extension

- [ ] **Step 1: Write `/message/send` adapter tests**

Test `pluginproto.SendReq` decodes, calls usecase with plugin origin under the host RPC timeout context, maps `Header.noPersist`, `Header.syncOnce`, from UID fallback, and returns `SendResp.messageId`.

- [ ] **Step 2: Write usecase host send tests**

Test plugin-origin send uses `message.SendOriginPlugin`, increments depth, uses configured system/default plugin sender when `fromUid` is empty, and never bypasses `message.App.Send`.

- [ ] **Step 3: Implement `/message/send`**

Map:

```text
SendReq.header.noPersist -> frame.Framer.NoPersist
SendReq.header.syncOnce -> frame.Framer.SyncOnce
SendReq.header.redDot -> frame.Setting.RedDot if supported by current frame model
SendReq.clientMsgNo -> SendCommand.ClientMsgNo
SendReq.fromUid -> SendCommand.FromUID or system/default plugin sender
```

- [ ] **Step 4: Write `/channel/messages` tests**

Test batch requests call the authoritative message reader under the host RPC timeout context and encode one response per request.

- [ ] **Step 5: Implement `/channel/messages`**

Reuse existing `message.ChannelMessageReader` pattern; remote owner routing belongs behind the reader interface, not in `access/plugin`.

- [ ] **Step 6: Run message host RPC tests**

Run: `go test ./internal/access/plugin ./internal/usecase/plugin -run 'MessageSend|ChannelMessages' -count=1`

Expected: PASS.

- [ ] **Step 7: Commit message host RPCs**

```bash
git add internal/access/plugin internal/usecase/plugin internal/usecase/message
git commit -m "feat: add plugin message host rpcs"
```

## Task 12: Cluster And Conversation Host RPC Compatibility

**Files:**
- Modify: `internal/access/plugin/handlers_cluster.go`
- Modify: `internal/access/plugin/handlers_conversation.go`
- Modify: `internal/usecase/plugin/host_rpc.go`
- Modify: `internal/usecase/plugin/mapping.go`
- Create: `internal/access/plugin/handlers_cluster_test.go`
- Create: `internal/access/plugin/handlers_conversation_test.go`
- Create: `internal/usecase/plugin/host_rpc_cluster_test.go`
- Create: `internal/usecase/plugin/host_rpc_conversation_test.go`

- [ ] **Step 1: Write cluster config tests**

Test `/cluster/config` uses the host RPC timeout context and returns nodes, slot IDs, leaders, terms, and replicas mapped to legacy `pluginproto.ClusterConfig` without local guesses.

- [ ] **Step 2: Implement cluster reader interface**

Define usecase deps for current cluster snapshot. Use existing `raftcluster.API` methods/observed state adapter from `internal/app`, not direct cluster imports in access.

- [ ] **Step 3: Write channel belong-node tests**

Test multiple channels group by owner node under the host RPC timeout context and return `ClusterChannelBelongNodeBatchResp` with stable ordering.

- [ ] **Step 4: Implement channel owner lookup**

Use channel metadata/routing reader. If ownership cannot be determined, return a clear error; do not guess local node.

- [ ] **Step 5: Write conversation channel tests**

Test `/conversation/channels` uses the host RPC timeout context and maps conversation usecase/store results to `pluginproto.Channel` list.

- [ ] **Step 6: Implement conversation host RPC**

Use a narrow conversation reader interface that preserves authoritative read semantics.

- [ ] **Step 7: Run host RPC tests**

Run: `go test ./internal/access/plugin ./internal/usecase/plugin -run 'Cluster|BelongNode|Conversation' -count=1`

Expected: PASS.

- [ ] **Step 8: Commit host RPC compatibility**

```bash
git add internal/access/plugin internal/usecase/plugin
git commit -m "feat: add plugin cluster conversation host rpcs"
```

## Task 13: Public Plugin Route And HTTP Forward

**Files:**
- Modify: `internal/access/api/options.go`
- Modify: `internal/access/api/routes.go`
- Create: `internal/access/api/plugin.go`
- Create: `internal/access/api/plugin_test.go`
- Modify: `internal/access/plugin/handlers_http.go`
- Create: `internal/access/plugin/handlers_http_test.go`
- Modify: `internal/usecase/plugin/host_rpc.go`
- Create: `internal/usecase/plugin/http_forward_test.go`
- Modify: `internal/access/node/options.go`
- Modify: `internal/access/node/service_ids.go`
- Create: `internal/access/node/plugin_http_forward_rpc.go`
- Create: `internal/access/node/plugin_http_forward_codec.go`
- Create: `internal/access/node/plugin_http_forward_rpc_test.go`

- [ ] **Step 1: Write public route tests**

Test `ANY /plugins/:plugin/*path` converts method/path/query/headers/body to `pluginproto.HttpRequest`, calls usecase Route, and writes status/header/body from `pluginproto.HttpResponse`.

- [ ] **Step 2: Implement public route**

Register route only when plugin usecase exists. Preserve documented open compatibility behavior; do not add auth in Phase 1.

- [ ] **Step 3: Write HTTP forward tests**

Test `toNodeId=0` calls local Route under the host RPC timeout context, positive remote node calls node RPC, `toNodeId=-1` returns explicit deferred/unimplemented, hop-by-hop headers are dropped, and body limit is enforced.

- [ ] **Step 4: Implement node RPC for remote HTTP forward**

Add node service ID after current `monitorMetricsRPCServiceID` range. Add/update a service-ID collision test for `internal/access/node/service_ids.go`. Encode request/response with binary codec and max body/header limits.

- [ ] **Step 5: Implement `/plugin/httpForward` host handler**

Decode `ForwardHttpReq`, fill missing `PluginNo` from caller plugin, and call usecase. Do not use raw HTTP client to API addresses from usecase; prefer node RPC for remote node-local semantics.

- [ ] **Step 6: Run HTTP route tests**

Run: `go test ./internal/access/api ./internal/access/plugin ./internal/access/node ./internal/usecase/plugin -run 'PluginRoute|HTTPForward|PluginHTTP' -count=1`

Expected: PASS.

- [ ] **Step 7: Commit public route and forward**

```bash
git add internal/access/api internal/access/plugin internal/access/node internal/usecase/plugin
git commit -m "feat: add plugin http route forwarding"
```

## Task 14: Manager Plugin APIs And Permission Catalog

**Files:**
- Modify: `internal/access/manager/options.go`
- Modify: `internal/access/manager/routes.go`
- Modify: `internal/access/manager/permissions.go`
- Create: `internal/access/manager/plugin.go`
- Create: `internal/access/manager/plugin_test.go`
- Modify: `internal/usecase/management/app.go`
- Create: `internal/usecase/management/plugin.go`
- Create: `internal/usecase/management/plugin_test.go`
- Modify: `internal/access/node/options.go`
- Modify: `internal/access/node/service_ids.go`
- Create: `internal/access/node/plugin_management_rpc.go`
- Create: `internal/access/node/plugin_management_codec.go`
- Create: `internal/access/node/plugin_management_rpc_test.go`

- [ ] **Step 1: Write permission catalog test**

Update expected permission snapshot to include:

```json
{"resource":"cluster.plugin","actions":["r","w"],"description":"Read and manage node-local plugins and cluster-wide plugin bindings."}
```

- [ ] **Step 2: Write manager route tests**

Test read permission for:

```text
GET /manager/nodes/:node_id/plugins
GET /manager/nodes/:node_id/plugins/:plugin_no
GET /manager/plugin-bindings?uid=...
GET /manager/plugin-bindings?plugin_no=...
```

Test write permission for:

```text
PUT /manager/nodes/:node_id/plugins/:plugin_no/config
POST /manager/nodes/:node_id/plugins/:plugin_no/restart
DELETE /manager/nodes/:node_id/plugins/:plugin_no
POST /manager/plugin-bindings
DELETE /manager/plugin-bindings
```

- [ ] **Step 3: Run manager tests and verify failure**

Run: `go test ./internal/access/manager -run 'Plugin|Permissions' -count=1`

Expected: FAIL because routes/catalog are missing.

- [ ] **Step 4: Implement management usecase methods**

Add `management.App` methods that depend on a narrow remote-plugin interface, not `internal/access/node` directly:

```go
type PluginNodeClient interface {
    ListNodePlugins(ctx context.Context, nodeID uint64) (pluginusecase.LocalPluginList, error)
    GetNodePlugin(ctx context.Context, nodeID uint64, pluginNo string) (pluginusecase.LocalPluginDetail, error)
    UpdateNodePluginConfig(ctx context.Context, nodeID uint64, pluginNo string, config json.RawMessage) (pluginusecase.LocalPluginDetail, error)
    RestartNodePlugin(ctx context.Context, nodeID uint64, pluginNo string) (pluginusecase.LocalPluginDetail, error)
    UninstallNodePlugin(ctx context.Context, nodeID uint64, pluginNo string) error
}
```

`internal/app` adapts `access/node.Client` to this interface when constructing `management.App`. This preserves the import boundary: `internal/usecase/management` must not import `internal/access/node`. Return 501 for remote unsupported and 503 for unavailable. Binding methods call cluster-authoritative plugin binding usecase/store, not node-local runtime.

- [ ] **Step 5: Implement node-scoped plugin management RPC**

Add node RPC ops for local inventory/detail/config/restart/uninstall. Use binary codec, bounded response sizes, and explicit unsupported status for older peers.

- [ ] **Step 6: Implement manager HTTP handlers**

Return JSON DTOs with redacted secret config fields. Ensure node-scoped APIs include target `node_id` in response.

- [ ] **Step 7: Run manager/node tests**

Run: `go test ./internal/access/manager ./internal/access/node ./internal/usecase/management -run 'Plugin|Permissions' -count=1`

Expected: PASS.

- [ ] **Step 8: Commit manager APIs**

```bash
git add internal/access/manager internal/access/node internal/usecase/management
git commit -m "feat: add plugin manager APIs"
```

## Task 15: Owner-Routed PersistAfter Side Effect

**Files:**
- Modify: `internal/app/deliveryrouting.go`
- Modify: `internal/app/deliveryrouting_test.go`
- Create: `internal/app/plugin_committed.go`
- Create: `internal/app/plugin_committed_test.go`
- Modify: `internal/usecase/plugin/persist_after.go`
- Create: `internal/usecase/plugin/persist_after_test.go`

- [ ] **Step 1: Write usecase PersistAfter tests**

Test committed message maps to one-message `pluginproto.MessageBatch`, invokes running `PersistAfter` plugins in priority order, honors `PersistAfterSync`, and logs/returns non-fatal errors.

- [ ] **Step 2: Implement usecase PersistAfter**

Convert `messageevents.MessageCommitted` or `channel.Message` to `pluginproto.Message` preserving `MessageID`, `MessageSeq`, `ClientMsgNo`, `StreamNo`, `Timestamp`, `From`, `ChannelID`, `ChannelType`, `Topic`, `Payload`.

- [ ] **Step 3: Write owner-routing tests**

In `internal/app`, test a committed event submitted on non-owner API node forwards to owner before plugin invocation; local owner invokes once; committed replay does not invoke plugin side effects.

- [ ] **Step 4: Implement plugin committed subscriber/router**

Add a committed subscriber after/alongside delivery routing:

```go
type pluginCommittedRouter struct {
    localNodeID uint64
    channelLog ownerResolver
    nodeClient pluginCommittedSubmitter
    pluginUsecase pluginPersistAfterUsecase
}
```

Only owner node calls `pluginUsecase.PersistAfterCommitted`. Implement a dedicated plugin committed node RPC so non-owner nodes forward committed plugin side effects to the owner without relying on delivery-only RPC semantics. Register the plugin committed subscriber outside committed replay so replay never invokes plugin side effects.

- [ ] **Step 5: Add plugin committed node RPC**

Existing `DeliverySubmit` node RPC is delivery-runtime specific and must not be reused for plugin side effects. Add `pluginCommittedRPCServiceID` in `internal/access/node/service_ids.go`, extend the node service-ID collision test, implement binary request/response codec tests, and route the handler to the local plugin PersistAfter usecase only.

- [ ] **Step 6: Run PersistAfter tests**

Run: `go test ./internal/usecase/plugin ./internal/app -run 'PersistAfter|PluginCommitted|CommittedReplay' -count=1`

Expected: PASS.

- [ ] **Step 7: Commit PersistAfter**

```bash
git add internal/usecase/plugin internal/app internal/access/node
git commit -m "feat: add owner routed plugin persist after"
```

## Task 16: Offline Receive Observer

**Files:**
- Modify: `internal/runtime/delivery/types.go`
- Modify: `internal/runtime/delivery/manager.go`
- Modify: `internal/runtime/delivery/*test.go`
- Modify: `internal/app/build.go`
- Create: `internal/app/plugin_receive_observer.go`
- Create: `internal/app/plugin_receive_observer_test.go`
- Modify: `internal/usecase/plugin/receive.go`
- Create: `internal/usecase/plugin/receive_test.go`

- [ ] **Step 1: Write Receive eligibility tests**

Test matrix:

| Case | Expected |
| --- | --- |
| durable message, recipient offline, sender != recipient, sender not system UID, not SyncOnce | invoke |
| recipient online | skip |
| sender equals recipient | skip |
| sender is system UID | skip |
| SyncOnce | skip |
| NoPersist | skip |
| request-scoped/temp channel | skip |

- [ ] **Step 2: Write delivery observer test**

Test observer sees offline UIDs after presence expansion/classification, not pre-presence raw subscriber resolution.

- [ ] **Step 3: Implement offline UID observer contract**

Add a narrow delivery runtime observer method after online/offline classification, for example:

```go
type OfflineResolvedObserver interface {
    OfflineResolved(ctx context.Context, event OfflineResolvedEvent)
}
```

Include message, offline UID, delivery attempt/page metadata, and enough fields to detect request-scoped/NoPersist/SyncOnce.

- [ ] **Step 4: Implement plugin Receive usecase**

For each eligible offline UID, query bindings, select highest-priority running local plugin with `Receive`, invoke `pluginproto.RecvPacket`, and record TTL dedupe key `messageID + uid`.

- [ ] **Step 5: Implement dedupe tests**

Use injected clock/cache to prove duplicate delivery retry or paged resolution does not invoke Receive twice within TTL.

- [ ] **Step 6: Wire observer in app**

Compose plugin Receive observer into delivery resolver/runtime only when plugin subsystem enabled.

- [ ] **Step 7: Run Receive tests**

Run: `go test ./internal/runtime/delivery ./internal/usecase/plugin ./internal/app -run 'Receive|OfflineResolved|Dedupe' -count=1`

Expected: PASS.

- [ ] **Step 8: Commit Receive observer**

```bash
git add internal/runtime/delivery internal/usecase/plugin internal/app
git commit -m "feat: add plugin offline receive observer"
```

## Task 17: App Wiring And Plugin Lifecycle Integration

**Files:**
- Modify: `internal/app/app.go`
- Modify: `internal/app/build.go`
- Modify: `internal/app/lifecycle_components.go`
- Create: `internal/app/plugin.go`
- Create: `internal/app/plugin_test.go`
- Modify: `internal/access/api/options.go`
- Modify: `internal/access/manager/options.go`
- Modify: `internal/access/node/options.go`
- Modify: `internal/FLOW.md`

- [ ] **Step 1: Write app build disabled test**

Test default config does not construct plugin runtime/usecase/access and lifecycle list does not include plugin.

- [ ] **Step 2: Write app build enabled test**

Test `WK_PLUGIN_ENABLE=true` constructs runtime, usecase, access adapter, injects `message.SendHook`, and lifecycle order is after delivery/presence/channel meta and before gateway/API/manager.

- [ ] **Step 3: Implement app fields and lifecycle constants**

Add fields:

```go
pluginRuntime *runtimeplugin.Runtime
pluginApp *pluginusecase.App
pluginAccess *accessplugin.Server
pluginOn atomic.Bool
startPluginFn func(context.Context) error
stopPluginFn func(context.Context) error
```

Add lifecycle name `plugin_runtime` between channel retention/committed dispatcher area and gateway/API/manager according to spec.

- [ ] **Step 4: Wire dependencies in build**

When enabled:

1. create runtime store/registry/socket/process runtime.
2. create usecase with runtime, store, invoker, binding store, message sender/reader, cluster/conversation readers.
3. create `access/plugin` with `Timeout: cfg.Plugin.Timeout` and register host RPCs on runtime socket.
4. pass plugin route usecase to public API.
5. pass plugin management usecase to manager/management.
6. pass plugin node RPC provider to node adapter.
7. inject plugin `SendHook` into message options.
8. add PersistAfter and Receive observers.

- [ ] **Step 5: Update `internal/FLOW.md`**

Add plugin subsystem to component list, lifecycle flow, dependency boundaries, and route/API summary.

- [ ] **Step 6: Run app wiring tests**

Run: `go test ./internal/app -run 'Plugin|Lifecycle|Build' -count=1`

Expected: PASS.

- [ ] **Step 7: Run import boundary tests**

Run: `GOWORK=off go test ./internal -run TestInternalImportBoundaries -count=1`

Expected: PASS.

- [ ] **Step 8: Commit app wiring**

```bash
git add internal/app internal/access/api internal/access/manager internal/access/node internal/FLOW.md
git commit -m "feat: wire plugin subsystem"
```

## Task 18: Focused Integration Tests

**Files:**
- Create: `internal/app/plugin_integration_test.go`
- Create: `test/e2e/plugin/plugin_smoke_test.go` with `//go:build integration`
- Create: `test/e2e/plugin/testdata/echo_plugin/main.go`

- [ ] **Step 1: Write fast single-node cluster integration test**

Test with in-process fakes where possible:

- enabled plugin runtime starts.
- fake plugin `/plugin/start` handshake registers status.
- manager list returns plugin status.
- public route calls plugin `Route`.
- plugin-origin `/message/send` reaches `message.App.Send`.

- [ ] **Step 2: Run focused app integration and verify failure**

Run: `go test ./internal/app -run TestPluginSingleNodeClusterSmoke -count=1`

Expected: FAIL until wiring is complete.

- [ ] **Step 3: Implement test harness fixes only**

Adjust constructors/fakes; do not introduce production shortcuts that bypass cluster semantics.

- [ ] **Step 4: Write integration-tag real `.wkp` smoke**

Under `test/e2e/plugin/testdata/echo_plugin`, create a helper plugin source using `github.com/WuKongIM/go-pdk`. The integration test builds it as a `.wkp` binary in a temp dir, starts a single-node cluster with `WK_PLUGIN_ENABLE=true`, waits for handshake, calls `/plugins/<no>/...`, and verifies the response. If the machine cannot download/build `go-pdk`, the integration test must call `t.Skip` with the exact build error rather than fail normal development.

- [ ] **Step 5: Keep real process smoke integration-only**

Add build tag:

```go
//go:build integration
```

Do not run it in normal `go test ./...`.

- [ ] **Step 6: Run fast integration**

Run: `go test ./internal/app -run TestPluginSingleNodeClusterSmoke -count=1`

Expected: PASS.

- [ ] **Step 7: Run plugin e2e when validating integration-tag tests**

Run only when explicitly validating full process behavior:

```bash
go test -tags=integration ./test/e2e/plugin -run TestPluginWKPSmoke -count=1
```

Expected: PASS or documented skip if local PDK/build toolchain is unavailable.

- [ ] **Step 8: Commit integration tests**

```bash
git add internal/app test/e2e/plugin
git commit -m "test: add plugin migration smoke coverage"
```

## Task 19: Documentation, Knowledge, And Final Verification

**Files:**
- Modify: `docs/development/PROJECT_KNOWLEDGE.md`
- Modify: `internal/FLOW.md`
- Modify: `pkg/slot/FLOW.md`
- Create or modify: `docs/wiki/plugin-phase1.md` if the repo uses wiki docs for user-facing feature notes
- Modify: `wukongim.conf.example`

- [ ] **Step 1: Update project knowledge**

Add concise bullets only:

```markdown
- Plugin runtime is node-local and disabled by default; plugin-user bindings are Slot Raft metadata keyed by UID.
- Phase 1 supports `.wkp`/go-pdk core methods and host RPCs, but stream RPCs return explicit unimplemented errors.
- Plugin sends must go through `message.App.Send`; PersistAfter runs only on the channel owner node.
```

Preserve unrelated existing dirty changes in `docs/development/PROJECT_KNOWLEDGE.md`; edit carefully.

- [ ] **Step 2: Document Phase 1 support**

Add a short wiki/dev doc describing:

- config keys and defaults.
- plugin directory/socket/sandbox/state layout.
- supported PDK methods and host RPCs.
- unsupported streams.
- node-scoped manager APIs vs cluster-wide bindings.
- fail-open/fail-closed Send hook behavior.

- [ ] **Step 3: Run package tests**

Run:

```bash
go test ./internal/runtime/plugin ./internal/usecase/plugin ./internal/access/plugin ./internal/access/api ./internal/access/manager ./internal/access/node ./internal/usecase/message ./internal/app ./pkg/slot/meta ./pkg/slot/fsm ./pkg/slot/proxy -count=1
```

Expected: PASS.

- [ ] **Step 4: Run architecture boundary tests**

Run:

```bash
GOWORK=off go test ./internal -run TestInternalImportBoundaries -count=1
```

Expected: PASS.

- [ ] **Step 5: Run full unit suite**

Run:

```bash
go test ./...
```

Expected: PASS. If unrelated dirty worktree changes cause failures, capture exact failing packages/tests, rerun the focused plugin package list to prove plugin work is healthy, and do not modify unrelated files.

- [ ] **Step 6: Inspect git status**

Run: `git status --short`

Expected: only plugin migration files are changed by this work, plus pre-existing unrelated dirty files the user asked to ignore.

- [ ] **Step 7: Final commit**

```bash
git add docs/development/PROJECT_KNOWLEDGE.md internal/FLOW.md pkg/slot/FLOW.md docs/wiki/plugin-phase1.md wukongim.conf.example
git commit -m "docs: document plugin phase one migration"
```

## Final Acceptance Checklist

- [ ] Existing `.wkp` plugins can connect to the host socket and complete `/plugin/start`.
- [ ] `Send`, `PersistAfter`, `Receive`, `Route`, `ConfigUpdate`, and `Stop` are implemented.
- [ ] `/stream/open`, `/stream/write`, and `/stream/close` return stable unimplemented errors.
- [ ] Plugin-origin sends route through `message.App.Send` and cluster append path.
- [ ] Send hooks run before request-scoped, NoPersist, SyncOnce, and durable branches, exactly once per request-scoped send.
- [ ] PersistAfter runs only on the channel owner node and not from committed replay.
- [ ] Receive runs only for eligible offline recipients and is deduped by `messageID + uid`.
- [ ] Plugin-user bindings are replicated through Slot Raft and keyed by UID.
- [ ] Manager node-scoped plugin APIs and cluster-wide binding APIs require `cluster.plugin` permissions.
- [ ] Public route `ANY /plugins/:plugin/*path` remains node-local and open for Phase 1 compatibility.
- [ ] `wkrpc` imports exist only in `internal/runtime/plugin` and `internal/access/plugin`.
- [ ] `go test` focused package list passes.
- [ ] `GOWORK=off go test ./internal -run TestInternalImportBoundaries -count=1` passes.
