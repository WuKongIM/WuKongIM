# internalv2 Plugin PersistAfter Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a real `.wkp` plugin `PersistAfter` path to `internalv2` with bounded side-effect behavior and benchmark coverage.

**Architecture:** Keep `internalv2/runtime/channelappend` as the durable commit source. Add a v2 plugin usecase and a bounded `pluginhook` worker that receives cloned committed events after successful append, then invokes PDK-compatible `.wkp` plugins through the existing node-local plugin runtime and wire protocol. Do not import the legacy `internal/usecase/plugin` business usecase into `internalv2`.

**Tech Stack:** Go, `internalv2` app/usecase/runtime layering, existing `internal/runtime/plugin` process/socket runtime, existing `internal/usecase/plugin/pluginproto` wire compatibility, `wkrpc`, focused Go unit tests, `go test -benchmem`.

---

## Scope Boundary

This plan implements Phase 1 from `docs/superpowers/specs/2026-06-22-internalv2-plugin-persist-after-design.md`.

Included:

- v2 plugin config and `WK_` parsing.
- Minimal v2 plugin lifecycle usecase for observed plugin start/close and `PersistAfter`.
- Minimal v2 plugin host RPC adapter for `/plugin/start` and `/close`.
- Bounded `internalv2/runtime/pluginhook` worker.
- `channelappend` post-commit `PersistAfter` enqueue port.
- `internalv2/app` wiring and lifecycle ordering.
- Unit tests, focused benchmarks, and real `.wkp` process compatibility coverage.

Excluded:

- `Send` hook chaining.
- `Receive`.
- plugin HTTP `Route`.
- plugin manager APIs.
- plugin-origin `/message/send`.
- plugin user bindings.
- stream APIs.
- legacy plugin committed owner RPC.

Do not commit during execution unless the user explicitly asks for commits. The working tree already has unrelated or prior changes; preserve them.

## File Structure

Create:

- `internalv2/contracts/pluginevents/types.go`
  - Defines `PersistAfterCommitted` and clone helpers.
- `internalv2/contracts/pluginevents/types_test.go`
  - Verifies clone semantics.
- `internalv2/usecase/plugin/types.go`
  - Defines v2 plugin observed types, methods, ports, and errors.
- `internalv2/usecase/plugin/app.go`
  - Owns v2 plugin usecase construction and host start/close methods.
- `internalv2/usecase/plugin/mapping.go`
  - Maps v2 committed events to PDK `pluginproto.MessageBatch`.
- `internalv2/usecase/plugin/invocation.go`
  - Invokes sync and async `PersistAfter`.
- `internalv2/usecase/plugin/persist_after.go`
  - Selects candidates and calls `PersistAfter`.
- `internalv2/usecase/plugin/*_test.go`
  - Unit tests and benchmarks for mapping, selection, and invocation.
- `internalv2/access/plugin/server.go`
  - Minimal v2 host RPC adapter.
- `internalv2/access/plugin/codec.go`
  - Proto decode/encode and body limit helpers.
- `internalv2/access/plugin/handlers_lifecycle.go`
  - `/plugin/start` and `/close` route handlers.
- `internalv2/access/plugin/*_test.go`
  - Host RPC adapter tests.
- `internalv2/runtime/pluginhook/worker.go`
  - Bounded async worker and observer interface.
- `internalv2/runtime/pluginhook/worker_test.go`
  - Worker behavior tests.
- `internalv2/runtime/pluginhook/benchmark_test.go`
  - Enqueue, queue-full, and worker benchmarks.
- `internalv2/app/plugin.go`
  - v2 plugin subsystem wiring and runtime adapter.
- `internalv2/app/plugin_test.go`
  - App wiring and lifecycle tests.
- `test/e2ev2/plugin/persist_after_plugin_test.go`
  - Real `.wkp` compatibility test, guarded with build constraints if it needs external process build time.
- `test/e2ev2/plugin/testdata/persistafter/main.go`
  - Minimal process-style `.wkp` test plugin that speaks the existing wkrpc/pluginproto path.

Modify:

- `internalv2/app/config.go`
  - Add `PluginConfig`, defaults, and validation.
- `cmd/wukongimv2/config.go`
  - Parse `WK_PLUGIN_*` keys for v2.
- `cmd/wukongimv2/config_test.go`
  - Cover config file and environment overrides.
- `wukongim.conf.example`
  - Align plugin config comments and keys.
- `internalv2/app/app.go`
  - Add plugin fields.
- `internalv2/app/wiring.go`
  - Wire plugin subsystem before channelappend wiring.
- `internalv2/app/lifecycle.go`
  - Start plugin runtime/worker before channelappend; stop after channelappend drains.
- `internalv2/app/FLOW.md`
  - Document plugin wiring and lifecycle.
- `internalv2/runtime/channelappend/options.go`
  - Add `PersistAfterEnqueuer` port.
- `internalv2/runtime/channelappend/commit.go`
  - Invoke plugin enqueue as post-commit side effect independent of recipient dispatch.
- `internalv2/runtime/channelappend/writer.go`
  - Ensure committed events enter post-commit backlog when only plugin hook is enabled.
- `internalv2/runtime/channelappend/commit_test.go`
  - Add plugin enqueue tests.
- `internalv2/runtime/channelappend/benchmark_test.go`
  - Add disabled/enabled plugin overhead benchmarks.
- `internalv2/runtime/channelappend/FLOW.md`
  - Document plugin `PersistAfter` post-commit behavior.

## Task 1: Add v2 Plugin Config

**Files:**

- Modify: `internalv2/app/config.go`
- Modify: `cmd/wukongimv2/config.go`
- Modify: `cmd/wukongimv2/config_test.go`
- Modify: `wukongim.conf.example`

- [ ] **Step 1: Add failing app config tests**

Add tests in `internalv2/app/config_test.go`:

```go
func TestPluginConfigDefaultsDisabledAndDerivesPaths(t *testing.T) {
	cfg := Config{DataDir: t.TempDir(), Plugin: PluginConfig{Enable: true}}
	app := &App{cfg: cfg}
	require.NoError(t, app.applyConfigDefaults())

	require.True(t, app.cfg.Plugin.Enable)
	require.Equal(t, filepath.Join(cfg.DataDir, "plugins"), app.cfg.Plugin.Dir)
	require.Equal(t, filepath.Join(cfg.DataDir, "run", "plugin.sock"), app.cfg.Plugin.SocketPath)
	require.Equal(t, filepath.Join(cfg.DataDir, "plugin-sandbox"), app.cfg.Plugin.SandboxDir)
	require.Equal(t, filepath.Join(cfg.DataDir, "plugin-state"), app.cfg.Plugin.StateDir)
	require.Equal(t, 5*time.Second, app.cfg.Plugin.Timeout)
	require.True(t, app.cfg.Plugin.HotReload)
	require.Equal(t, 1024, app.cfg.Plugin.PersistAfterQueueSize)
	require.Equal(t, 16, app.cfg.Plugin.PersistAfterWorkers)
}

func TestPluginConfigDefaultsKeepPluginsDisabled(t *testing.T) {
	app := &App{cfg: Config{DataDir: t.TempDir()}}
	require.NoError(t, app.applyConfigDefaults())
	require.False(t, app.cfg.Plugin.Enable)
}

func TestPluginConfigValidationRejectsInvalidBounds(t *testing.T) {
	cases := []PluginConfig{
		{Enable: true, Timeout: -time.Second},
		{Enable: true, PersistAfterQueueSize: -1},
		{Enable: true, PersistAfterWorkers: -1},
	}
	for _, cfg := range cases {
		app := &App{cfg: Config{DataDir: t.TempDir(), Plugin: cfg}}
		require.Error(t, app.applyConfigDefaults())
	}
}
```

Run:

```sh
GOWORK=off GOCACHE=/private/tmp/wukongim-go-build-cache go test ./internalv2/app -run 'TestPluginConfig' -count=1
```

Expected: fail because `Config.Plugin` and `PluginConfig` do not exist.

- [ ] **Step 2: Implement `PluginConfig` defaults and validation**

Add to `internalv2/app/config.go`:

```go
// PluginConfig controls node-local PDK-compatible plugin runtime integration.
type PluginConfig struct {
	// Enable starts node-local .wkp plugin processes and PersistAfter hooks.
	Enable bool
	// Dir stores executable .wkp plugin files for this node.
	Dir string
	// SocketPath is the Unix socket used for plugin host RPC traffic.
	SocketPath string
	// SandboxDir is the root directory for per-plugin writable sandbox data.
	SandboxDir string
	// StateDir stores node-local desired plugin state files.
	StateDir string
	// Timeout bounds plugin host RPCs and graceful process shutdown.
	Timeout time.Duration
	// HotReload watches Dir for plugin binary changes when enabled.
	HotReload bool
	// FailOpen is retained for future Send hooks; PersistAfter is always fail-open.
	FailOpen bool
	// PersistAfterQueueSize bounds queued PersistAfter events retained in memory.
	PersistAfterQueueSize int
	// PersistAfterWorkers bounds concurrent PersistAfter hook invocations.
	PersistAfterWorkers int
}
```

Add `Plugin PluginConfig` to `Config`, call `defaultPluginConfig` and
`validatePluginConfig` from `applyConfigDefaults`, and use these helpers:

```go
func defaultPluginConfig(dataDir string, cfg PluginConfig) PluginConfig {
	if cfg.Enable {
		if cfg.Dir == "" {
			cfg.Dir = filepath.Join(dataDir, "plugins")
		}
		if cfg.SocketPath == "" {
			cfg.SocketPath = filepath.Join(dataDir, "run", "plugin.sock")
		}
		if cfg.SandboxDir == "" {
			cfg.SandboxDir = filepath.Join(dataDir, "plugin-sandbox")
		}
		if cfg.StateDir == "" {
			cfg.StateDir = filepath.Join(dataDir, "plugin-state")
		}
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 5 * time.Second
	}
	if cfg.PersistAfterQueueSize == 0 {
		cfg.PersistAfterQueueSize = 1024
	}
	if cfg.PersistAfterWorkers == 0 {
		cfg.PersistAfterWorkers = 16
	}
	cfg.HotReload = true
	return cfg
}

func validatePluginConfig(cfg PluginConfig) error {
	if cfg.Timeout < 0 {
		return fmt.Errorf("%w: plugin timeout must be >= 0", ErrInvalidConfig)
	}
	if cfg.PersistAfterQueueSize < 0 {
		return fmt.Errorf("%w: plugin persist-after queue size must be >= 0", ErrInvalidConfig)
	}
	if cfg.PersistAfterWorkers < 0 {
		return fmt.Errorf("%w: plugin persist-after workers must be >= 0", ErrInvalidConfig)
	}
	return nil
}
```

Keep `HotReload` explicit-flag handling if existing config parser needs a false
override; otherwise add a private `hotReloadSet` flag like `LogConfig` uses.

- [ ] **Step 3: Add v2 executable config parsing tests**

Extend `cmd/wukongimv2/config_test.go` file and env override tests with:

```go
"WK_PLUGIN_ENABLE=true",
"WK_PLUGIN_DIR=/tmp/wk-plugins",
"WK_PLUGIN_SOCKET_PATH=/tmp/wk-plugin.sock",
"WK_PLUGIN_SANDBOX_DIR=/tmp/wk-plugin-sandbox",
"WK_PLUGIN_STATE_DIR=/tmp/wk-plugin-state",
"WK_PLUGIN_TIMEOUT=3s",
"WK_PLUGIN_HOT_RELOAD=false",
"WK_PLUGIN_FAIL_OPEN=true",
"WK_PLUGIN_PERSIST_AFTER_QUEUE_SIZE=2048",
"WK_PLUGIN_PERSIST_AFTER_WORKERS=24",
```

Assert:

```go
if !cfg.Plugin.Enable ||
	cfg.Plugin.Dir != "/tmp/wk-plugins" ||
	cfg.Plugin.SocketPath != "/tmp/wk-plugin.sock" ||
	cfg.Plugin.SandboxDir != "/tmp/wk-plugin-sandbox" ||
	cfg.Plugin.StateDir != "/tmp/wk-plugin-state" ||
	cfg.Plugin.Timeout != 3*time.Second ||
	cfg.Plugin.HotReload ||
	!cfg.Plugin.FailOpen ||
	cfg.Plugin.PersistAfterQueueSize != 2048 ||
	cfg.Plugin.PersistAfterWorkers != 24 {
	t.Fatalf("Plugin config = %#v", cfg.Plugin)
}
```

Run:

```sh
GOWORK=off GOCACHE=/private/tmp/wukongim-go-build-cache go test ./cmd/wukongimv2 -run 'TestLoadConfig' -count=1
```

Expected: fail until parser is implemented.

- [ ] **Step 4: Implement config parsing and example keys**

Add plugin keys to the known config key list in `cmd/wukongimv2/config.go`.
Parse each key using the existing `parseBool`, `parseDuration`, and `parseInt`
helpers. Reject negative timeout, queue size, and worker count with errors that
include the exact key name.

Add to `wukongim.conf.example`:

```text
# Plugins execute node-local .wkp binaries and are disabled by default for safety.
# WK_PLUGIN_DIR defaults to ${WK_NODE_DATA_DIR}/plugins when enabled.
# WK_PLUGIN_SOCKET_PATH defaults to ${WK_NODE_DATA_DIR}/run/plugin.sock.
# PersistAfter is always fail-open in internalv2 Phase 1.
WK_PLUGIN_ENABLE=false
WK_PLUGIN_DIR=
WK_PLUGIN_SOCKET_PATH=
WK_PLUGIN_SANDBOX_DIR=
WK_PLUGIN_STATE_DIR=
WK_PLUGIN_TIMEOUT=5s
WK_PLUGIN_HOT_RELOAD=true
WK_PLUGIN_FAIL_OPEN=false
WK_PLUGIN_PERSIST_AFTER_QUEUE_SIZE=1024
WK_PLUGIN_PERSIST_AFTER_WORKERS=16
```

- [ ] **Step 5: Verify config task**

Run:

```sh
GOWORK=off GOCACHE=/private/tmp/wukongim-go-build-cache go test ./internalv2/app ./cmd/wukongimv2 -run 'TestPluginConfig|TestLoadConfig' -count=1
```

Expected: pass.

## Task 2: Add v2 Plugin Event Contract And Mapping Benchmarks

**Files:**

- Create: `internalv2/contracts/pluginevents/types.go`
- Create: `internalv2/contracts/pluginevents/types_test.go`
- Create: `internalv2/contracts/pluginevents/benchmark_test.go`
- Create: `internalv2/usecase/plugin/mapping.go`
- Create: `internalv2/usecase/plugin/mapping_test.go`
- Create: `internalv2/usecase/plugin/benchmark_test.go`

- [ ] **Step 1: Add failing clone and mapping tests**

Add `internalv2/contracts/pluginevents/types_test.go`:

```go
func TestPersistAfterCommittedCloneCopiesMutableFields(t *testing.T) {
	event := PersistAfterCommitted{
		MessageID:         10,
		MessageSeq:        3,
		ChannelID:         "room",
		ChannelType:       2,
		FromUID:           "u1",
		ClientMsgNo:        "c1",
		ServerTimestampMS:  1713859200123,
		Payload:           []byte("hello"),
		MessageScopedUIDs: []string{"u2"},
	}
	clone := event.Clone()
	event.Payload[0] = 'H'
	event.MessageScopedUIDs[0] = "changed"

	require.Equal(t, []byte("hello"), clone.Payload)
	require.Equal(t, []string{"u2"}, clone.MessageScopedUIDs)
}
```

Add `internalv2/usecase/plugin/mapping_test.go`:

```go
func TestMessageBatchFromPersistAfterCommitted(t *testing.T) {
	event := pluginevents.PersistAfterCommitted{
		MessageID:        11,
		MessageSeq:       5,
		ChannelID:        "room",
		ChannelType:      2,
		FromUID:          "sender",
		ClientMsgNo:       "client-1",
		ServerTimestampMS: 1713859200123,
		Payload:          []byte("payload"),
	}
	batch := messageBatchFromPersistAfter(event)
	require.Len(t, batch.Messages, 1)
	msg := batch.Messages[0]
	require.Equal(t, int64(11), msg.MessageId)
	require.Equal(t, uint64(5), msg.MessageSeq)
	require.Equal(t, "client-1", msg.ClientMsgNo)
	require.Equal(t, uint32(1713859200), msg.Timestamp)
	require.Equal(t, "sender", msg.From)
	require.Equal(t, "room", msg.ChannelId)
	require.Equal(t, uint32(2), msg.ChannelType)
	require.Equal(t, []byte("payload"), msg.Payload)

	event.Payload[0] = 'P'
	require.Equal(t, []byte("payload"), msg.Payload)
}
```

Run:

```sh
GOWORK=off GOCACHE=/private/tmp/wukongim-go-build-cache go test ./internalv2/contracts/pluginevents ./internalv2/usecase/plugin -run 'TestPersistAfterCommittedClone|TestMessageBatchFromPersistAfter' -count=1
```

Expected: fail because packages do not exist.

- [ ] **Step 2: Implement event and mapper**

In `internalv2/contracts/pluginevents/types.go`:

```go
package pluginevents

// PersistAfterCommitted carries one durable committed message into plugin hooks.
type PersistAfterCommitted struct {
	MessageID         uint64
	MessageSeq        uint64
	ChannelID         string
	ChannelType       uint8
	FromUID           string
	SenderNodeID      uint64
	SenderSessionID   uint64
	ClientMsgNo       string
	ServerTimestampMS int64
	Payload           []byte
	RedDot            bool
	SyncOnce          bool
	MessageScopedUIDs []string
}

// Clone returns an independent event copy safe for asynchronous plugin workers.
func (e PersistAfterCommitted) Clone() PersistAfterCommitted {
	e.Payload = append([]byte(nil), e.Payload...)
	e.MessageScopedUIDs = append([]string(nil), e.MessageScopedUIDs...)
	return e
}
```

In `internalv2/usecase/plugin/mapping.go`:

```go
package plugin

import (
	"math"

	pluginevents "github.com/WuKongIM/WuKongIM/internalv2/contracts/pluginevents"
	"github.com/WuKongIM/WuKongIM/internal/usecase/plugin/pluginproto"
)

func messageBatchFromPersistAfter(event pluginevents.PersistAfterCommitted) *pluginproto.MessageBatch {
	return &pluginproto.MessageBatch{Messages: []*pluginproto.Message{{
		MessageId:   messageIDToInt64(event.MessageID),
		MessageSeq:  event.MessageSeq,
		ClientMsgNo: event.ClientMsgNo,
		Timestamp:   timestampSeconds(event.ServerTimestampMS),
		From:        event.FromUID,
		ChannelId:   event.ChannelID,
		ChannelType: uint32(event.ChannelType),
		Payload:     append([]byte(nil), event.Payload...),
	}}}
}

func messageIDToInt64(id uint64) int64 {
	if id > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(id)
}

func timestampSeconds(ms int64) uint32 {
	if ms <= 0 {
		return 0
	}
	sec := ms / 1000
	if sec > math.MaxUint32 {
		return math.MaxUint32
	}
	return uint32(sec)
}
```

- [ ] **Step 3: Add mapping and clone benchmarks**

Add to `internalv2/usecase/plugin/benchmark_test.go`:

```go
func BenchmarkPersistAfterMessageBatchMapping(b *testing.B) {
	payloadSizes := []int{128, 1024, 16 * 1024}
	for _, payloadSize := range payloadSizes {
		b.Run(fmt.Sprintf("payload_%d", payloadSize), func(b *testing.B) {
			event := benchmarkPersistAfterEvent(payloadSize)
			b.ReportAllocs()
			b.SetBytes(int64(payloadSize))
			for i := 0; i < b.N; i++ {
				batch := messageBatchFromPersistAfter(event)
				if len(batch.Messages) != 1 {
					b.Fatal("empty batch")
				}
			}
		})
	}
}
```

Include a local `benchmarkPersistAfterEvent(payloadSize int)` helper that fills
`Payload`. Mapping does not read scoped UIDs because the PDK message batch has
no scoped UID field.

Add to `internalv2/contracts/pluginevents/benchmark_test.go`:

```go
func BenchmarkPersistAfterCommittedClone(b *testing.B) {
	payloadSizes := []int{128, 1024, 16 * 1024}
	scopedCounts := []int{0, 10, 1000}
	for _, payloadSize := range payloadSizes {
		for _, scopedCount := range scopedCounts {
			b.Run(fmt.Sprintf("payload_%d/scoped_%d", payloadSize, scopedCount), func(b *testing.B) {
				event := benchmarkPersistAfterCommitted(payloadSize, scopedCount)
				b.ReportAllocs()
				b.SetBytes(int64(payloadSize))
				for i := 0; i < b.N; i++ {
					clone := event.Clone()
					if len(clone.Payload) != payloadSize || len(clone.MessageScopedUIDs) != scopedCount {
						b.Fatal("bad clone")
					}
				}
			})
		}
	}
}
```

Include a local `benchmarkPersistAfterCommitted(payloadSize, scopedCount int)`
helper that fills `Payload` and `MessageScopedUIDs`. This benchmark measures
the real scoped UID copy cost at the asynchronous plugin-worker ownership
boundary.

- [ ] **Step 4: Verify contract task**

Run:

```sh
GOWORK=off GOCACHE=/private/tmp/wukongim-go-build-cache go test ./internalv2/contracts/pluginevents ./internalv2/usecase/plugin -count=1
GOWORK=off GOCACHE=/private/tmp/wukongim-go-build-cache go test ./internalv2/usecase/plugin ./internalv2/contracts/pluginevents -run '^$' -bench 'BenchmarkPersistAfter' -benchmem -count=5
```

Expected: tests pass; benchmark output includes `ns/op`, `B/op`, and `allocs/op`.

## Task 3: Add v2 Plugin Usecase For Start, Close, Selection, And Invocation

**Files:**

- Create: `internalv2/usecase/plugin/types.go`
- Create: `internalv2/usecase/plugin/app.go`
- Create: `internalv2/usecase/plugin/invocation.go`
- Create: `internalv2/usecase/plugin/persist_after.go`
- Create: `internalv2/usecase/plugin/persist_after_test.go`
- Modify: `internalv2/usecase/plugin/benchmark_test.go`

- [ ] **Step 1: Add failing usecase tests**

Add tests for candidate selection and invocation:

```go
func TestPersistAfterCommittedInvokesRunningCandidatesByPriority(t *testing.T) {
	runtime := &recordingRuntime{plugins: []ObservedPlugin{
		{No: "low", Methods: []Method{MethodPersistAfter}, Priority: 1, Status: StatusRunning, Enabled: true},
		{No: "send-only", Methods: []Method{MethodSend}, Priority: 100, Status: StatusRunning, Enabled: true},
		{No: "offline", Methods: []Method{MethodPersistAfter}, Priority: 99, Status: StatusOffline, Enabled: true},
		{No: "high", Methods: []Method{MethodPersistAfter}, Priority: 9, Status: StatusRunning, Enabled: true},
	}}
	invoker := &recordingInvoker{}
	app, err := NewApp(Options{Runtime: runtime, Invoker: invoker})
	require.NoError(t, err)

	err = app.PersistAfterCommitted(context.Background(), pluginevents.PersistAfterCommitted{
		MessageID: 1, MessageSeq: 2, ChannelID: "room", ChannelType: 2, Payload: []byte("hello"),
	})
	require.NoError(t, err)
	require.Equal(t, []string{"high", "low"}, invoker.asyncPluginNos())
}

func TestInvokePersistAfterUsesSyncRequestWhenPluginRequiresIt(t *testing.T) {
	invoker := &recordingInvoker{}
	app, err := NewApp(Options{Runtime: &recordingRuntime{}, Invoker: invoker})
	require.NoError(t, err)

	err = app.InvokePersistAfter(context.Background(), ObservedPlugin{
		No: "sync", PersistAfterSync: true, Status: StatusRunning, Enabled: true,
	}, &pluginproto.MessageBatch{})
	require.NoError(t, err)
	require.Equal(t, []string{"sync:/plugin/persist_after"}, invoker.requests)
	require.Empty(t, invoker.sends)
}
```

Run:

```sh
GOWORK=off GOCACHE=/private/tmp/wukongim-go-build-cache go test ./internalv2/usecase/plugin -run 'TestPersistAfterCommitted|TestInvokePersistAfter' -count=1
```

Expected: fail because usecase types do not exist.

- [ ] **Step 2: Implement usecase types and constructor**

Define in `types.go`:

```go
type Method string

const (
	MethodSend         Method = "Send"
	MethodPersistAfter Method = "PersistAfter"
)

type Status string

const (
	StatusRunning Status = "running"
	StatusOffline Status = "offline"
	StatusDisabled Status = "disabled"
)

type ObservedPlugin struct {
	No               string
	Name             string
	Version          string
	Methods          []Method
	Priority         int
	PersistAfterSync bool
	Status           Status
	Enabled          bool
	LastError        string
}

type Runtime interface {
	RegisterObserved(context.Context, ObservedPlugin) error
	MarkClosed(context.Context, string) error
	List() []ObservedPlugin
}

type Invoker interface {
	RequestPlugin(context.Context, string, string, []byte) ([]byte, error)
	SendPlugin(string, uint32, []byte) error
}

type Options struct {
	Runtime Runtime
	Invoker Invoker
	Logger  wklog.Logger
}

type App struct {
	runtime Runtime
	invoker Invoker
	logger  wklog.Logger
}
```

`NewApp` must require `Runtime` and `Invoker` because Phase 1 is a real plugin path.

- [ ] **Step 3: Implement lifecycle handshake methods**

Implement `StartPlugin(ctx, info, callerUID)` and `ClosePlugin(ctx, pluginNo, callerUID)`:

```go
func (a *App) StartPlugin(ctx context.Context, info *pluginproto.PluginInfo, callerUID string) (*pluginproto.StartupResp, error) {
	if info == nil || strings.TrimSpace(info.GetNo()) == "" {
		return nil, ErrPluginNoRequired
	}
	no := strings.TrimSpace(info.GetNo())
	if callerUID != "" && callerUID != no {
		return nil, ErrPluginCallerMismatch
	}
	observed := observedFromPluginInfo(info)
	observed.Status = StatusRunning
	observed.Enabled = true
	if err := a.runtime.RegisterObserved(ctx, observed); err != nil {
		return nil, err
	}
	return &pluginproto.StartupResp{}, nil
}

func (a *App) ClosePlugin(ctx context.Context, pluginNo string, callerUID string) error {
	no := strings.TrimSpace(pluginNo)
	if no == "" {
		return ErrPluginNoRequired
	}
	if callerUID != "" && callerUID != no {
		return ErrPluginCallerMismatch
	}
	return a.runtime.MarkClosed(ctx, no)
}
```

Map pluginproto methods into v2 `Method` values. Ignore unknown method strings.

- [ ] **Step 4: Implement PersistAfter selection and invocation**

Use constants:

```go
const (
	PathPersistAfter = "/plugin/persist_after"
	MsgTypePersistAfter uint32 = 2
)
```

Selection rules:

- `Enabled == true`
- `Status == StatusRunning`
- method list contains `MethodPersistAfter`
- sort priority descending, tie by plugin number ascending for determinism

Invocation rules:

- marshal `pluginproto.MessageBatch`
- if `PersistAfterSync`, call `RequestPlugin(ctx, no, PathPersistAfter, data)`
- otherwise call `SendPlugin(no, MsgTypePersistAfter, data)`

- [ ] **Step 5: Add candidate benchmarks**

Add:

```go
func BenchmarkPersistAfterCandidates(b *testing.B) {
	for _, count := range []int{1, 16, 128, 1024} {
		b.Run(fmt.Sprintf("plugins_%d", count), func(b *testing.B) {
			app, err := NewApp(Options{Runtime: &recordingRuntime{plugins: benchmarkPlugins(count)}, Invoker: &recordingInvoker{}})
			require.NoError(b, err)
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				candidates, err := app.PersistAfterPluginCandidates(context.Background())
				if err != nil {
					b.Fatal(err)
				}
				if len(candidates) == 0 {
					b.Fatal("empty candidates")
				}
			}
		})
	}
}
```

- [ ] **Step 6: Verify usecase task**

Run:

```sh
GOWORK=off GOCACHE=/private/tmp/wukongim-go-build-cache go test ./internalv2/usecase/plugin -count=1
GOWORK=off GOCACHE=/private/tmp/wukongim-go-build-cache go test ./internalv2/usecase/plugin -run '^$' -bench 'BenchmarkPersistAfter' -benchmem -count=5
```

Expected: tests pass; benchmark output includes selection and mapping lines.

## Task 4: Add Minimal v2 Plugin Host RPC Adapter

**Files:**

- Create: `internalv2/access/plugin/server.go`
- Create: `internalv2/access/plugin/codec.go`
- Create: `internalv2/access/plugin/handlers_lifecycle.go`
- Create: `internalv2/access/plugin/server_test.go`

- [ ] **Step 1: Add failing adapter tests**

Add tests:

```go
func TestServerRegistersLifecycleRoutes(t *testing.T) {
	routes := &recordingRoutes{}
	_, err := NewServer(Options{Routes: routes, Usecase: &recordingUsecase{}, Timeout: time.Second})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"/plugin/start", "/close"}, routes.paths)
}

func TestHandlePluginStartDecodesAndWritesStartupResponse(t *testing.T) {
	usecase := &recordingUsecase{}
	server, err := NewServer(Options{Usecase: usecase, Timeout: time.Second})
	require.NoError(t, err)
	ctx := newTestRPCContext("wk.echo", mustMarshalProto(t, &pluginproto.PluginInfo{
		No: "wk.echo",
		Methods: []string{"PersistAfter"},
	}))

	server.handlePath("/plugin/start", ctx)

	require.NoError(t, ctx.err)
	require.Equal(t, "wk.echo", usecase.started.No)
	require.NotEmpty(t, ctx.body)
}
```

Run:

```sh
GOWORK=off GOCACHE=/private/tmp/wukongim-go-build-cache go test ./internalv2/access/plugin -count=1
```

Expected: fail because package does not exist.

- [ ] **Step 2: Implement minimal server**

Implement only these routes:

```go
var routePaths = []string{"/plugin/start", "/close"}
```

`Usecase` interface:

```go
type Usecase interface {
	StartPlugin(context.Context, *pluginproto.PluginInfo, string) (*pluginproto.StartupResp, error)
	ClosePlugin(context.Context, string, string) error
}
```

Use body limit `10 << 20`, per-route timeout, and proto marshal/unmarshal using the same `pluginproto` package as the old adapter.

- [ ] **Step 3: Verify adapter task**

Run:

```sh
GOWORK=off GOCACHE=/private/tmp/wukongim-go-build-cache go test ./internalv2/access/plugin -count=1
```

Expected: pass.

## Task 5: Add Bounded Pluginhook Worker And Benchmarks

**Files:**

- Create: `internalv2/runtime/pluginhook/worker.go`
- Create: `internalv2/runtime/pluginhook/worker_test.go`
- Create: `internalv2/runtime/pluginhook/benchmark_test.go`

- [ ] **Step 1: Add failing worker tests**

Add tests:

```go
func TestWorkerEnqueueDetachesCallerCancellation(t *testing.T) {
	usecase := &recordingPersistAfterUsecase{done: make(chan struct{})}
	worker := NewWorker(Options{Usecase: usecase, QueueSize: 1, Workers: 1, Timeout: time.Second})
	require.NoError(t, worker.Start(context.Background()))
	defer worker.Stop(context.Background())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	worker.EnqueuePersistAfter(ctx, pluginevents.PersistAfterCommitted{MessageID: 1, Payload: []byte("hello")})

	select {
	case <-usecase.done:
	case <-time.After(time.Second):
		t.Fatal("PersistAfter was not invoked")
	}
}

func TestWorkerQueueFullDropsWithoutBlockingCaller(t *testing.T) {
	usecase := newBlockingPersistAfterUsecase()
	observer := &recordingObserver{}
	worker := NewWorker(Options{Usecase: usecase, QueueSize: 1, Workers: 1, Timeout: time.Second, Observer: observer})
	require.NoError(t, worker.Start(context.Background()))
	defer worker.Stop(context.Background())

	worker.EnqueuePersistAfter(context.Background(), pluginevents.PersistAfterCommitted{MessageID: 1})
	usecase.waitStarted(t)
	worker.EnqueuePersistAfter(context.Background(), pluginevents.PersistAfterCommitted{MessageID: 2})
	worker.EnqueuePersistAfter(context.Background(), pluginevents.PersistAfterCommitted{MessageID: 3})

	require.Eventually(t, func() bool { return observer.drops() > 0 }, time.Second, time.Millisecond)
	usecase.release()
}
```

Run:

```sh
GOWORK=off GOCACHE=/private/tmp/wukongim-go-build-cache go test ./internalv2/runtime/pluginhook -count=1
```

Expected: fail because package does not exist.

- [ ] **Step 2: Implement worker**

Use this public shape:

```go
type PersistAfterUsecase interface {
	PersistAfterCommitted(context.Context, pluginevents.PersistAfterCommitted) error
}

type Observer interface {
	ObservePersistAfterEnqueue(result string, wait time.Duration)
	ObservePersistAfterInvoke(result string, d time.Duration)
}

type Options struct {
	Usecase PersistAfterUsecase
	QueueSize int
	Workers int
	Timeout time.Duration
	Observer Observer
	Logger wklog.Logger
}

type Worker struct {
	usecase PersistAfterUsecase
	queue chan pluginevents.PersistAfterCommitted
	workers int
	timeout time.Duration
	observer Observer
	logger wklog.Logger
	ctx context.Context
	cancel context.CancelFunc
	wg sync.WaitGroup
	started atomic.Bool
}
```

`EnqueuePersistAfter` must clone the event, try to enqueue, wait at most a small bounded duration such as `2 * time.Millisecond`, then record a `full` drop and return without error. It must not return errors to `channelappend`.

- [ ] **Step 3: Add benchmarks**

Add:

```go
func BenchmarkPluginHookEnqueue(b *testing.B) {
	worker := NewWorker(Options{Usecase: &recordingPersistAfterUsecase{}, QueueSize: 1 << 20, Workers: 1, Timeout: time.Second})
	require.NoError(b, worker.Start(context.Background()))
	defer worker.Stop(context.Background())
	event := pluginevents.PersistAfterCommitted{MessageID: 1, Payload: bytes.Repeat([]byte("a"), 1024)}
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			worker.EnqueuePersistAfter(context.Background(), event)
		}
	})
}

func BenchmarkPluginHookQueueFull(b *testing.B) {
	worker := NewWorker(Options{Usecase: newBlockingPersistAfterUsecase(), QueueSize: 1, Workers: 1, Timeout: time.Second})
	require.NoError(b, worker.Start(context.Background()))
	defer worker.Stop(context.Background())
	event := pluginevents.PersistAfterCommitted{MessageID: 1, Payload: []byte("x")}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		worker.EnqueuePersistAfter(context.Background(), event)
	}
}
```

- [ ] **Step 4: Verify worker task**

Run:

```sh
GOWORK=off GOCACHE=/private/tmp/wukongim-go-build-cache go test ./internalv2/runtime/pluginhook -count=1
GOWORK=off GOCACHE=/private/tmp/wukongim-go-build-cache go test ./internalv2/runtime/pluginhook -run '^$' -bench 'BenchmarkPluginHook' -benchmem -count=5
```

Expected: tests pass and benchmark output includes enqueue and queue-full lines.

## Task 6: Connect channelappend Post-Commit To Pluginhook

**Files:**

- Modify: `internalv2/runtime/channelappend/options.go`
- Modify: `internalv2/runtime/channelappend/commit.go`
- Modify: `internalv2/runtime/channelappend/writer.go`
- Modify: `internalv2/runtime/channelappend/commit_test.go`
- Modify: `internalv2/runtime/channelappend/benchmark_test.go`
- Modify: `internalv2/runtime/channelappend/FLOW.md`

- [ ] **Step 1: Add failing channelappend tests**

Add tests:

```go
func TestCommitEffectEnqueuesPersistAfterWithoutRecipientWork(t *testing.T) {
	enqueuer := &recordingPersistAfterEnqueuer{}
	effect := commitEffect{
		key: "room",
		seq: 1,
		events: []CommittedEnvelope{{
			MessageID: 10, MessageSeq: 4, ChannelID: "room", ChannelType: 2, Payload: []byte("hello"),
		}},
	}
	completion := effect.run(context.Background(), commitPorts{persistAfter: enqueuer})
	require.Len(t, completion.items, 1)
	require.Equal(t, []uint64{10}, enqueuer.messageIDs())
}

func TestCommitEffectDoesNotRequirePersistAfterForNoPostCommitWork(t *testing.T) {
	effect := commitEffect{events: []CommittedEnvelope{{MessageID: 10}}}
	completion := effect.run(context.Background(), commitPorts{})
	require.Len(t, completion.items, 1)
}
```

Add a no-persist regression test in the existing NoPersist test area:

```go
func TestNoPersistRealtimeDoesNotEnqueuePersistAfter(t *testing.T) {
	persistAfter := &recordingPersistAfterEnqueuer{}
	delivery := &scriptedRecipientDeliveryEnqueuerForCommitTest{}
	group := newStartedGroupForCommitTest(t, Options{
		LocalNodeID: 1,
		Appender: &recordingAppenderForCommitTest{},
		MessageID: sequentialMessageIDForCommitTest{next: 100},
		RecipientDeliveryEnqueuer: delivery,
		PersistAfterEnqueuer: persistAfter,
	})

	_, err := group.SubmitLocal(context.Background(), authorityTargetForCommitTest("cmd", 2), []SendBatchItem{{
		Context: context.Background(),
		Command: SendCommand{FromUID: "u1", ChannelID: "cmd", ChannelType: 2, NoPersist: true, SyncOnce: true, MessageScopedUIDs: []string{"u2"}, Payload: []byte("x")},
	}})
	require.NoError(t, err)
	require.Eventually(t, func() bool { return delivery.callCount() == 1 }, time.Second, time.Millisecond)
	require.Zero(t, persistAfter.callCount())
}
```

Run:

```sh
GOWORK=off GOCACHE=/private/tmp/wukongim-go-build-cache go test ./internalv2/runtime/channelappend -run 'TestCommitEffectEnqueuesPersistAfter|TestNoPersistRealtimeDoesNotEnqueuePersistAfter' -count=1
```

Expected: fail until the port exists.

- [ ] **Step 2: Add `PersistAfterEnqueuer` port**

In `options.go`:

```go
// PersistAfterEnqueuer accepts durable committed messages for plugin PersistAfter hooks.
type PersistAfterEnqueuer interface {
	// EnqueuePersistAfter queues one committed message for plugin side effects.
	EnqueuePersistAfter(context.Context, CommittedEnvelope)
}
```

Add `PersistAfterEnqueuer PersistAfterEnqueuer` to `Options`, add `persistAfter PersistAfterEnqueuer` to `commitPorts`, and wire it in `commitPortsFromOptions`.

- [ ] **Step 3: Update commit effect semantics**

Adjust `commitPorts.hasPostCommitWork`:

```go
func (p commitPorts) hasPostCommitWork() bool {
	return p.persistAfter != nil || p.activeAdmitter != nil || effectiveRecipientDeliveryEnqueuer(p) != nil
}

func (p commitPorts) hasRecipientWork() bool {
	return p.activeAdmitter != nil || effectiveRecipientDeliveryEnqueuer(p) != nil
}
```

At the start of each event loop in `commitEffect.run`, enqueue plugin work:

```go
if ports.persistAfter != nil {
	ports.persistAfter.EnqueuePersistAfter(runtimeCtx, event)
}
if !ports.hasRecipientWork() {
	completion.items = append(completion.items, commitCompletedItem{event: event, checkpointSeq: event.MessageSeq})
	continue
}
```

Do not convert plugin queue drops into `ErrCommitEffectFailed`; `pluginhook` owns drop logging and metrics.

- [ ] **Step 4: Add channelappend benchmarks**

Add:

```go
func BenchmarkChannelAppendPostCommitPlugin(b *testing.B) {
	event := CommittedEnvelope{MessageID: 1, MessageSeq: 1, ChannelID: "room", ChannelType: 2, Payload: []byte("payload")}
	b.Run("disabled", func(b *testing.B) {
		effect := commitEffect{events: []CommittedEnvelope{event}}
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = effect.run(context.Background(), commitPorts{})
		}
	})
	b.Run("enabled_enqueue", func(b *testing.B) {
		effect := commitEffect{events: []CommittedEnvelope{event}}
		enqueuer := &recordingPersistAfterEnqueuer{}
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = effect.run(context.Background(), commitPorts{persistAfter: enqueuer})
		}
	})
}
```

- [ ] **Step 5: Update FLOW**

Add one paragraph to `internalv2/runtime/channelappend/FLOW.md`:

```text
When a PersistAfter enqueuer is configured, each successful durable committed
envelope is cloned into the plugin side-effect worker from the same
authority-local post-commit point. PersistAfter enqueue is best-effort and does
not affect SENDACK, append success, recipient delivery, or conversation active
projection. Transient NoPersist realtime sends skip PersistAfter because they
do not create durable committed envelopes.
```

- [ ] **Step 6: Verify channelappend task**

Run:

```sh
GOWORK=off GOCACHE=/private/tmp/wukongim-go-build-cache go test ./internalv2/runtime/channelappend -run 'TestCommitEffectEnqueuesPersistAfter|TestNoPersistRealtimeDoesNotEnqueuePersistAfter|Test.*Commit' -count=1
GOWORK=off GOCACHE=/private/tmp/wukongim-go-build-cache go test ./internalv2/runtime/channelappend -run '^$' -bench 'BenchmarkChannelAppendPostCommitPlugin' -benchmem -count=5
```

Expected: tests pass; benchmark output includes disabled and enabled lines.

## Task 7: Wire Plugin Runtime, Host RPC, Usecase, And Worker In internalv2 App

**Files:**

- Create: `internalv2/app/plugin.go`
- Modify: `internalv2/app/app.go`
- Modify: `internalv2/app/wiring.go`
- Modify: `internalv2/app/lifecycle.go`
- Modify: `internalv2/app/plugin_test.go`
- Modify: `internalv2/app/FLOW.md`

- [ ] **Step 1: Add failing app wiring tests**

Add tests:

```go
func TestNewSkipsPluginSubsystemWhenDisabled(t *testing.T) {
	app, err := newTestApp(t, Config{DataDir: t.TempDir()}, WithCluster(&fakeCluster{}))
	require.NoError(t, err)
	require.Nil(t, app.pluginRuntime)
	require.Nil(t, app.pluginHook)
}

func TestNewWiresPluginSubsystemWhenEnabled(t *testing.T) {
	app, err := newTestApp(t, Config{
		DataDir: t.TempDir(),
		Cluster: clusterv2.Config{NodeID: 1},
		Plugin: PluginConfig{Enable: true, HotReload: false},
	}, WithCluster(&fakeCluster{}))
	require.NoError(t, err)
	require.NotNil(t, app.pluginRuntime)
	require.NotNil(t, app.plugins)
	require.NotNil(t, app.pluginHook)
}
```

Add lifecycle order test:

```go
func TestPluginLifecycleStartsBeforeChannelAppendAndStopsAfter(t *testing.T) {
	var calls []string
	app := &App{
		cluster: &recordingClusterRuntime{calls: &calls},
		pluginRuntime: &recordingWorkerRuntime{name: "plugin_runtime", calls: &calls},
		pluginHook: &recordingWorkerRuntime{name: "plugin_hook", calls: &calls},
		channelAppends: newNoopStartedChannelAppendGroupForLifecycleTest(),
	}
	require.NoError(t, app.Start(context.Background()))
	require.NoError(t, app.Stop(context.Background()))
	requireLifecycleOrder(t, calls, []string{
		"cluster.start",
		"plugin_runtime.start",
		"plugin_hook.start",
		"channel_append.start",
		"channel_append.stop",
		"plugin_hook.stop",
		"plugin_runtime.stop",
		"cluster.stop",
	})
}

func requireLifecycleOrder(t *testing.T, got []string, want []string) {
	t.Helper()
	pos := 0
	for _, call := range got {
		if pos < len(want) && call == want[pos] {
			pos++
		}
	}
	require.Equalf(t, len(want), pos, "calls = %v, want ordered subsequence %v", got, want)
}
```

Run:

```sh
GOWORK=off GOCACHE=/private/tmp/wukongim-go-build-cache go test ./internalv2/app -run 'TestNew.*Plugin|TestPluginLifecycle' -count=1
```

Expected: fail because app fields and wiring do not exist.

- [ ] **Step 2: Implement plugin runtime adapter**

In `internalv2/app/plugin.go`, build:

```go
func (a *App) wirePluginSubsystem(nodeID uint64) error {
	if !a.cfg.Plugin.Enable || a.pluginRuntime != nil {
		return nil
	}
	socket := runtimeplugin.NewSocketServer(a.cfg.Plugin.SocketPath)
	invoker := runtimeplugin.NewInvoker(socket, runtimeplugin.WithTimeout(a.cfg.Plugin.Timeout))
	store := runtimeplugin.NewStore(a.cfg.Plugin.StateDir)
	runtime := runtimeplugin.NewRuntime(runtimeplugin.RuntimeOptions{
		Enable:     a.cfg.Plugin.Enable,
		HotReload:  a.cfg.Plugin.HotReload,
		Dir:        a.cfg.Plugin.Dir,
		SocketPath: a.cfg.Plugin.SocketPath,
		SandboxDir: a.cfg.Plugin.SandboxDir,
		StateDir:   a.cfg.Plugin.StateDir,
		Timeout:    a.cfg.Plugin.Timeout,
		Store:      store,
		Socket:     socket,
		Invoker:    invoker,
	})
	plugins, err := pluginusecase.NewApp(pluginusecase.Options{
		Runtime: pluginRuntimeAdapter{runtime: runtime},
		Invoker: invoker,
		Logger:  a.logger.Named("plugin"),
	})
	if err != nil {
		return err
	}
	if _, err := accessplugin.NewServer(accessplugin.Options{
		Routes:  socket,
		Usecase: plugins,
		Timeout: a.cfg.Plugin.Timeout,
		Logger:  a.logger.Named("access.plugin"),
	}); err != nil {
		return err
	}
	hook := pluginhook.NewWorker(pluginhook.Options{
		Usecase: plugins,
		QueueSize: a.cfg.Plugin.PersistAfterQueueSize,
		Workers: a.cfg.Plugin.PersistAfterWorkers,
		Timeout: a.cfg.Plugin.Timeout,
		Logger: a.logger.Named("plugin.persist_after"),
	})
	a.pluginRuntime = runtime
	a.plugins = plugins
	a.pluginHook = hook
	return nil
}
```

Adapter methods map `runtimeplugin.ObservedPlugin` to v2 `pluginusecase.ObservedPlugin`.

- [ ] **Step 3: Wire channelappend enqueuer**

Call `wirePluginSubsystem(clusterCfg.NodeID)` before `wireChannelAppend`.
Inside `wireChannelAppend`, set:

```go
if a.pluginHook != nil {
	opts.PersistAfterEnqueuer = a.pluginHook
}
```

- [ ] **Step 4: Update lifecycle**

Add app fields:

```go
pluginRuntime WorkerRuntime
pluginHook WorkerRuntime
pluginRuntimeStarted bool
pluginHookStarted bool
plugins *pluginusecase.App
```

Start order after cluster readiness and before delivery/channelappend:

```go
if a.pluginRuntime != nil {
	if err := a.pluginRuntime.Start(ctx); err != nil { ... }
	a.pluginRuntimeStarted = true
}
if a.pluginHook != nil {
	if err := a.pluginHook.Start(ctx); err != nil { ... }
	a.pluginHookStarted = true
}
```

Stop order after channelappend stops and before cluster stops:

```go
if a.pluginHookStarted && a.pluginHook != nil { ... }
if a.pluginRuntimeStarted && a.pluginRuntime != nil { ... }
```

Rollback order must mirror stop order.

- [ ] **Step 5: Update FLOW**

Add to `internalv2/app/FLOW.md` construction and lifecycle descriptions:

```text
When Plugin.Enable=true, app wires the PDK-compatible node-local plugin runtime,
minimal lifecycle host RPC adapter, v2 plugin usecase, and bounded PersistAfter
worker before channelappend. The channelappend group receives only the
PersistAfter enqueue port. Plugin runtime and hook workers start before
channelappend and stop after channelappend drains, so accepted durable commits
can still enqueue plugin side effects during shutdown.
```

- [ ] **Step 6: Verify app wiring task**

Run:

```sh
GOWORK=off GOCACHE=/private/tmp/wukongim-go-build-cache go test ./internalv2/app -run 'TestNew.*Plugin|TestPluginLifecycle|TestChannelAppend' -count=1
```

Expected: pass.

## Task 8: Add Real `.wkp` Compatibility Test And Benchmark

**Files:**

- Create: `test/e2ev2/plugin/persist_after_plugin_test.go`
- Create: `test/e2ev2/plugin/testdata/persistafter/main.go`
- Modify: `docs/superpowers/specs/2026-06-22-internalv2-plugin-persist-after-design.md` only if the actual test command differs from the spec.

- [ ] **Step 1: Add the minimal test plugin process**

Create `test/e2ev2/plugin/testdata/persistafter/main.go`. It must:

- accept `--socket` and `--sandbox`
- connect to the Unix socket
- send wkrpc connect with UID derived from executable name `persistafter`
- call `/plugin/start` with `pluginproto.PluginInfo{No:"persistafter", Methods:[]string{"PersistAfter"}}`
- receive one-way `MsgTypePersistAfter` messages
- decode `pluginproto.MessageBatch`
- append a JSON line under the sandbox directory with message id, channel id, and payload string
- handle `/stop` request by writing OK and exiting

Use `internal/app/plugin_integration_test.go` as the wire-format reference, but keep this helper under `test/e2ev2/plugin/testdata/persistafter` so Phase 1 does not depend on legacy `internal` test helpers.

- [ ] **Step 2: Add failing process compatibility test**

In `test/e2ev2/plugin/persist_after_plugin_test.go`, compile the helper:

```go
func buildPersistAfterPlugin(t *testing.T, dir string) string {
	t.Helper()
	out := filepath.Join(dir, "persistafter.wkp")
	cmd := exec.Command("go", "build", "-o", out, "./test/e2ev2/plugin/testdata/persistafter")
	cmd.Dir = repoRootForTest(t)
	cmd.Env = append(os.Environ(), "GOWORK=off")
	data, err := cmd.CombinedOutput()
	require.NoError(t, err, string(data))
	return out
}
```

Then start an `internalv2/app` single-node cluster config with plugin enabled, copy the binary into `Plugin.Dir`, send one durable message through the existing v2 API or message usecase, and assert the sandbox JSON line appears.

Run:

```sh
GOWORK=off GOCACHE=/private/tmp/wukongim-go-build-cache go test -tags=e2e ./test/e2ev2/plugin -run TestPersistAfterPluginReceivesDurableCommittedMessage -count=1
```

Expected: fail until app wiring and helper are complete.

- [ ] **Step 3: Add real `.wkp` benchmark**

Add:

```go
func BenchmarkPersistAfterRealWKP(b *testing.B) {
	if testing.Short() {
		b.Skip("real process benchmark skipped in short mode")
	}
	h := newPersistAfterPluginHarness(b)
	defer h.Close()
	payload := bytes.Repeat([]byte("a"), 1024)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		h.SendDurableMessage(b, fmt.Sprintf("client-%d", i), payload)
		h.WaitPersistAfterCount(b, i+1)
	}
}
```

Run:

```sh
GOWORK=off GOCACHE=/private/tmp/wukongim-go-build-cache go test -tags=e2e ./test/e2ev2/plugin -run '^$' -bench 'BenchmarkPersistAfterRealWKP' -benchmem -count=3
```

Expected: benchmark completes and reports process/socket path cost. If this is too noisy for default CI, keep it as an explicit command in the implementation summary rather than running inside broad package tests.

## Task 9: Final Benchmark Pass And Regression Verification

**Files:**

- Modify only files needed to fix benchmark or test failures found by this task.

- [ ] **Step 1: Run focused unit tests**

Run:

```sh
GOWORK=off GOCACHE=/private/tmp/wukongim-go-build-cache go test ./internalv2/contracts/pluginevents ./internalv2/access/plugin ./internalv2/usecase/plugin ./internalv2/runtime/pluginhook ./internalv2/runtime/channelappend ./internalv2/app ./cmd/wukongimv2 -count=1
```

Expected: pass.

- [ ] **Step 2: Run focused benchmarks**

Run:

```sh
GOWORK=off GOCACHE=/private/tmp/wukongim-go-build-cache go test ./internalv2/usecase/plugin ./internalv2/runtime/pluginhook ./internalv2/runtime/channelappend -run '^$' -bench 'Benchmark(PersistAfter|PluginHook|ChannelAppend.*Plugin)' -benchmem -count=5
```

Expected: pass. Capture and include key `ns/op`, `B/op`, and `allocs/op` lines in the implementation summary.

- [ ] **Step 3: Run real plugin compatibility test and benchmark**

Run:

```sh
GOWORK=off GOCACHE=/private/tmp/wukongim-go-build-cache go test -tags=e2e ./test/e2ev2/plugin -run TestPersistAfterPluginReceivesDurableCommittedMessage -count=1
GOWORK=off GOCACHE=/private/tmp/wukongim-go-build-cache go test -tags=e2e ./test/e2ev2/plugin -run '^$' -bench 'BenchmarkPersistAfterRealWKP' -benchmem -count=3
```

Expected: pass. If the benchmark is noisy, report it separately as process/socket compatibility cost.

- [ ] **Step 4: Run full internalv2 suite**

Run:

```sh
GOWORK=off GOCACHE=/private/tmp/wukongim-go-build-cache go test ./internalv2/... -count=1
```

Expected: pass.

- [ ] **Step 5: Run diff hygiene check**

Run:

```sh
git diff --check
```

Expected: no output and exit code 0.

- [ ] **Step 6: Prepare implementation summary**

Summarize:

- plugin config and default behavior
- v2 usecase/access/runtime boundaries
- channelappend post-commit semantics
- NoPersist exclusion
- benchmark commands and important result lines
- tests run and any skipped/noisy benchmark notes

Do not claim completion without fresh command output from this task.
