# Internalv2 Plugin Observability And Manager Readonly Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add internalv2 plugin PersistAfter metrics and read-only manager plugin inventory/detail APIs with cross-node manager RPC.

**Architecture:** Keep plugin state node-local in the existing plugin registry. Add entry-agnostic read methods in the v2 plugin and management usecases, adapt them through manager HTTP and manager node RPC, and wire metrics through the app's existing Prometheus registry.

**Tech Stack:** Go, gin, clusterv2 node RPC, prometheus/client_golang, testify, `go test` benchmarks.

---

### Task 1: Plugin Metrics Domain

**Files:**
- Create: `pkg/metrics/plugin.go`
- Modify: `pkg/metrics/registry.go`
- Test: `pkg/metrics/registry_test.go`

- [ ] **Step 1: Write failing metrics test**

Add a test that creates `metrics.New(1, "node-1")`, calls plugin hook observe methods, gathers metric families, and asserts:
`wukongim_plugin_hook_enqueue_total`,
`wukongim_plugin_hook_enqueue_wait_seconds`,
`wukongim_plugin_hook_invoke_total`, and
`wukongim_plugin_hook_invoke_duration_seconds`.

- [ ] **Step 2: Verify red**

Run:

```bash
GOWORK=off go test ./pkg/metrics -run TestPluginMetrics -count=1
```

Expected: fail because `Registry.Plugin` or observe methods do not exist.

- [ ] **Step 3: Implement minimal metrics**

Create `PluginMetrics` with:

```go
type PluginMetrics struct {
	enqueueTotal    *prometheus.CounterVec
	enqueueWait     *prometheus.HistogramVec
	invokeTotal     *prometheus.CounterVec
	invokeDuration  *prometheus.HistogramVec
}

func (m *PluginMetrics) ObserveHookEnqueue(method, result string, wait time.Duration)
func (m *PluginMetrics) ObserveHookInvoke(method, result string, d time.Duration)
```

Register it from `Registry.New` as `Plugin *PluginMetrics`.

- [ ] **Step 4: Verify green**

Run:

```bash
GOWORK=off go test ./pkg/metrics -run TestPluginMetrics -count=1
```

Expected: pass.

### Task 2: Plugin Usecase Read Methods

**Files:**
- Modify: `internalv2/usecase/plugin/types.go`
- Modify: `internalv2/usecase/plugin/app.go`
- Create: `internalv2/usecase/plugin/list.go`
- Test: `internalv2/usecase/plugin/list_test.go`
- Benchmark: `internalv2/usecase/plugin/benchmark_test.go`

- [ ] **Step 1: Write failing list/detail tests**

Add tests for:
- `ListPlugins` returns cloned method slices.
- `GetPlugin` returns one plugin and clones methods.
- `GetPlugin` returns `ErrPluginNotFound` for missing plugin.

- [ ] **Step 2: Verify red**

Run:

```bash
GOWORK=off go test ./internalv2/usecase/plugin -run 'TestListPlugins|TestGetPlugin' -count=1
```

Expected: fail because methods and error do not exist.

- [ ] **Step 3: Implement read methods**

Add:

```go
var ErrPluginNotFound = errors.New("plugin not found")

func (a *App) ListPlugins(ctx context.Context) ([]ObservedPlugin, error)
func (a *App) GetPlugin(ctx context.Context, pluginNo string) (ObservedPlugin, error)
```

Use `runtime.List()` and clone method slices before returning. Validate
non-empty plugin numbers with the existing plugin number error.

- [ ] **Step 4: Add benchmark**

Add `BenchmarkListPlugins` with sizes 1, 16, 256, and 1024 using a fake runtime.

- [ ] **Step 5: Verify green**

Run:

```bash
GOWORK=off go test ./internalv2/usecase/plugin -run 'TestListPlugins|TestGetPlugin' -count=1
GOWORK=off go test ./internalv2/usecase/plugin -run '^$' -bench 'BenchmarkListPlugins' -benchmem -count=5
```

Expected: tests and benchmark pass.

### Task 3: Management Plugin Read Model

**Files:**
- Modify: `internalv2/usecase/management/nodes.go`
- Create: `internalv2/usecase/management/plugins.go`
- Test: `internalv2/usecase/management/plugins_test.go`

- [ ] **Step 1: Write failing management tests**

Add tests for:
- local list uses `PluginReader`.
- remote list uses `RemotePluginReader`.
- local detail maps missing plugin to not found.
- zero node id is invalid for plugin routes.

- [ ] **Step 2: Verify red**

Run:

```bash
GOWORK=off go test ./internalv2/usecase/management -run 'TestListNodePlugins|TestGetNodePlugin' -count=1
```

Expected: fail because plugin ports and methods do not exist.

- [ ] **Step 3: Implement management types and methods**

Add:

```go
var ErrPluginReaderUnavailable = errors.New("internalv2/usecase/management: plugin reader unavailable")

type PluginReader interface {
	ListPlugins(context.Context) ([]pluginusecase.ObservedPlugin, error)
	GetPlugin(context.Context, string) (pluginusecase.ObservedPlugin, error)
}

type RemotePluginReader interface {
	NodePlugins(context.Context, uint64) ([]Plugin, error)
	NodePlugin(context.Context, uint64, string) (Plugin, error)
}
```

Define `Plugin`, `NodePluginList`, `ListNodePlugins`, and `GetNodePlugin` with
node id validation and local-vs-remote routing.

- [ ] **Step 4: Verify green**

Run:

```bash
GOWORK=off go test ./internalv2/usecase/management -run 'TestListNodePlugins|TestGetNodePlugin' -count=1
```

Expected: pass.

### Task 4: Manager Plugin Node RPC

**Files:**
- Modify: `pkg/clusterv2/net/ids.go`
- Modify: `pkg/clusterv2/net/ids_test.go`
- Modify: `internalv2/access/node/presence_rpc.go`
- Create: `internalv2/access/node/manager_plugins_codec.go`
- Create: `internalv2/access/node/manager_plugins_rpc.go`
- Test: `internalv2/access/node/manager_plugins_rpc_test.go`
- Create: `internalv2/infra/cluster/management_plugins.go`
- Test: `internalv2/infra/cluster/management_plugins_test.go`

- [ ] **Step 1: Write failing RPC tests**

Add codec/client/server tests for list and detail. Assert the client targets
the new service id and preserves plugin fields.

- [ ] **Step 2: Verify red**

Run:

```bash
GOWORK=off go test ./internalv2/access/node ./internalv2/infra/cluster ./pkg/clusterv2/net -run 'TestManagerPlugin|TestTransportServiceAlias' -count=1
```

Expected: fail because service id and RPC methods do not exist.

- [ ] **Step 3: Implement service id, codec, RPC, infra reader**

Add `RPCManagerPlugins` and alias `"manager plugins"`. Add adapter option
`ManagerPlugins ManagerPluginReader`, client methods
`ListManagerPlugins` and `GetManagerPlugin`, plus infra reader methods
`NodePlugins` and `NodePlugin`.

- [ ] **Step 4: Verify green**

Run:

```bash
GOWORK=off go test ./internalv2/access/node ./internalv2/infra/cluster ./pkg/clusterv2/net -run 'TestManagerPlugin|TestTransportServiceAlias' -count=1
```

Expected: pass.

### Task 5: Manager HTTP Routes

**Files:**
- Modify: `internalv2/access/manager/server.go`
- Create: `internalv2/access/manager/plugins.go`
- Test: `internalv2/access/manager/plugins_test.go`
- Modify: `internalv2/access/manager/FLOW.md`
- Modify: `internalv2/usecase/management/FLOW.md`
- Modify: `internalv2/infra/cluster/FLOW.md`
- Modify: `internalv2/access/node/FLOW.md`

- [ ] **Step 1: Write failing HTTP tests**

Add tests for:
- `GET /manager/nodes/:node_id/plugins` returns legacy-compatible JSON.
- `GET /manager/nodes/:node_id/plugins/:plugin_no` returns one plugin.
- invalid node id returns 400.
- missing plugin returns 404.
- unavailable plugin reader returns 503.

- [ ] **Step 2: Verify red**

Run:

```bash
GOWORK=off go test ./internalv2/access/manager -run 'TestManagerPlugin' -count=1
```

Expected: fail because routes do not exist.

- [ ] **Step 3: Implement routes and DTOs**

Register read routes under resource `cluster.plugin:r`, map management DTOs to
legacy-compatible fields, and reuse the manager JSON error style.

- [ ] **Step 4: Update FLOW docs**

Document the new read-only routes and remote RPC flow. Explicitly state that
write routes and plugin bindings remain unmigrated.

- [ ] **Step 5: Verify green**

Run:

```bash
GOWORK=off go test ./internalv2/access/manager -run 'TestManagerPlugin' -count=1
```

Expected: pass.

### Task 6: App Wiring And Metrics Observer

**Files:**
- Modify: `internalv2/app/plugin.go`
- Modify: `internalv2/app/observability.go`
- Modify: `internalv2/app/wiring.go`
- Test: `internalv2/app/plugin_test.go`
- Test: `internalv2/app/observability_test.go`
- Modify: `internalv2/app/FLOW.md`

- [ ] **Step 1: Write failing app tests**

Add tests that:
- pluginhook worker gets a metrics observer when metrics are enabled.
- `newManagerManagement` wires local plugin reader and remote plugin reader.
- gathered metrics include plugin hook observations.

- [ ] **Step 2: Verify red**

Run:

```bash
GOWORK=off go test ./internalv2/app -run 'TestPlugin.*Metrics|Test.*Plugin.*Management' -count=1
```

Expected: fail because wiring and metrics observer do not exist.

- [ ] **Step 3: Implement app wiring**

Add a `pluginHookObserver()` method returning an observer that writes to
`metrics.Plugin`. Pass it into `pluginhook.NewWorker`. Register manager plugin
RPC when node RPC is available, and pass local/remote plugin readers into
`management.Options`.

- [ ] **Step 4: Update app FLOW**

Document metrics wiring, manager plugin RPC registration, and manager route
attachment.

- [ ] **Step 5: Verify green**

Run:

```bash
GOWORK=off go test ./internalv2/app -run 'TestPlugin.*Metrics|Test.*Plugin.*Management' -count=1
```

Expected: pass.

### Task 7: Focused Verification

**Files:**
- All touched Go files and FLOW docs.

- [ ] **Step 1: Run focused package tests**

```bash
GOWORK=off GOCACHE=/private/tmp/wukongim-go-build-cache go test ./pkg/metrics ./internalv2/usecase/plugin ./internalv2/usecase/management ./internalv2/access/node ./internalv2/infra/cluster ./internalv2/access/manager ./internalv2/app -count=1
```

- [ ] **Step 2: Run focused benchmarks**

```bash
GOWORK=off GOCACHE=/private/tmp/wukongim-go-build-cache go test ./internalv2/usecase/plugin ./internalv2/runtime/pluginhook -run '^$' -bench 'Benchmark(ListPlugins|PluginHook)' -benchmem -count=5
```

- [ ] **Step 3: Run diff check**

```bash
git diff --check
```

Expected: all checks pass.
