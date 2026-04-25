# App Bootstrap Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a cluster-first process bootstrap path that assembles `wkdb`, `raftstore`, `wkcluster`, `wkstore`, `internal/service`, and `internal/gateway` into a runnable node through `internal/app` and `cmd/wukongim`.

**Architecture:** The implementation is split into four layers. First add a narrow `internal/app` config and error boundary. Then extend `internal/service` options so `app` can inject store- and cluster-facing collaborators without leaking bootstrap logic downward. Next build the `App` runtime builder plus lifecycle orchestration with strict startup and rollback order. Finally add a thin `cmd/wukongim` JSON-config entrypoint and one end-to-end integration test that proves a node starts, accepts a `wkproto` connection, and shuts down cleanly.

**Tech Stack:** Go 1.23, standard library `flag`/`encoding/json`/`os/signal`, `internal/gateway`, `internal/service`, `pkg/wkcluster`, `pkg/wkstore`, `pkg/wkdb`, `pkg/raftstore`, `testing`, `net`

**Spec:** `docs/superpowers/specs/2026-04-01-app-bootstrap-design.md`

---

## File Map

| Path | Responsibility |
|------|----------------|
| `internal/app/errors.go` | App-scoped sentinel errors for invalid config, build failure, and lifecycle misuse |
| `internal/app/config.go` | `app.Config` and nested config structs, validation, defaults, and derivation of storage paths |
| `internal/app/app.go` | `App` struct, constructor, runtime accessors |
| `internal/app/build.go` | Runtime dependency construction for `wkdb`, `raftstore`, `wkcluster`, `wkstore`, `service`, and `gateway` |
| `internal/app/lifecycle.go` | `Start`, `Stop`, rollback helpers, idempotence guards |
| `internal/app/config_test.go` | Config validation, defaults, and derived-path tests |
| `internal/app/lifecycle_test.go` | Startup order, rollback order, and stop idempotence tests |
| `internal/app/integration_test.go` | Real process-assembly smoke test over gateway + cluster + storage |
| `internal/service/options.go` | Add store- and cluster-facing option ports that `app` can inject |
| `internal/service/service.go` | Persist new collaborators on `Service` while preserving current behavior |
| `internal/service/handler_test.go` | Cover default collaborator wiring if constructor shape changes |
| `cmd/wukongim/main.go` | Thin process entrypoint: parse flags, load JSON config, create app, start, wait for signal, stop |
| `cmd/wukongim/config.go` | Wire-level JSON config loader and translation into `app.Config` |
| `cmd/wukongim/config_test.go` | Config-file decode and translation tests |

## Task 1: Add the `internal/app` config boundary

**Files:**
- Create: `internal/app/errors.go`
- Create: `internal/app/config.go`
- Create: `internal/app/config_test.go`

- [ ] **Step 1: Write the failing config tests**

Add tests for:
- `TestConfigValidateRequiresNodeAndClusterIdentity`
- `TestConfigApplyDefaultsDerivesStoragePathsFromDataDir`
- `TestConfigValidateRejectsMismatchedGroupCount`
- `TestConfigGatewayDefaultsSessionOptions`

Use a minimal config shape that matches the spec:

```go
func TestConfigApplyDefaultsDerivesStoragePathsFromDataDir(t *testing.T) {
	cfg := Config{
		Node: NodeConfig{
			ID:      1,
			DataDir: "/tmp/wukong-node-1",
		},
		Cluster: ClusterConfig{
			ListenAddr: "127.0.0.1:7000",
			Nodes:      []NodeConfigRef{{ID: 1, Addr: "127.0.0.1:7000"}},
			Groups:     []GroupConfig{{ID: 1, Peers: []uint64{1}}},
		},
		Gateway: GatewayConfig{
			Listeners: []gateway.ListenerOptions{
				binding.TCPWKProto("tcp-wkproto", "127.0.0.1:5100"),
			},
		},
	}

	require.NoError(t, cfg.ApplyDefaultsAndValidate())
	require.Equal(t, "/tmp/wukong-node-1/data", cfg.Storage.DBPath)
	require.Equal(t, "/tmp/wukong-node-1/raft", cfg.Storage.RaftPath)
}
```

- [ ] **Step 2: Run the targeted tests to verify they fail**

Run: `go test ./internal/app -run 'TestConfig' -v`

Expected: FAIL because `internal/app` does not exist yet.

- [ ] **Step 3: Implement config structs, defaults, and validation**

Create `internal/app/config.go` with:

```go
type Config struct {
	Node    NodeConfig
	Storage StorageConfig
	Cluster ClusterConfig
	Gateway GatewayConfig
}

type NodeConfig struct {
	ID      uint64
	Name    string
	DataDir string
}

type StorageConfig struct {
	DBPath   string
	RaftPath string
}

type ClusterConfig struct {
	ListenAddr     string
	GroupCount     uint32
	Nodes          []NodeConfigRef
	Groups         []GroupConfig
	ForwardTimeout time.Duration
	PoolSize       int
	TickInterval   time.Duration
	RaftWorkers    int
	ElectionTick   int
	HeartbeatTick  int
	DialTimeout    time.Duration
}

type GatewayConfig struct {
	TokenAuthOn    bool
	DefaultSession gateway.SessionOptions
	Listeners      []gateway.ListenerOptions
}
```

Implementation notes:
- Add `ApplyDefaultsAndValidate() error` on `*Config`
- Derive `Storage.DBPath` and `Storage.RaftPath` from `Node.DataDir` when omitted
- Default `Cluster.GroupCount` to `len(Groups)` when unset, but reject zero after defaults
- Normalize `Gateway.DefaultSession` through `gateway.DefaultSessionOptions()`
- Reject empty node ID, empty data dir, empty cluster listen addr, zero nodes, zero groups, and empty gateway listeners
- Validate that every configured peer ID exists in `Cluster.Nodes`
- Keep error variables in `internal/app/errors.go`, for example:

```go
var (
	ErrInvalidConfig = errors.New("app: invalid config")
	ErrNotBuilt      = errors.New("app: runtime not built")
	ErrAlreadyStarted = errors.New("app: already started")
)
```

- [ ] **Step 4: Run the targeted tests to verify they pass**

Run: `go test ./internal/app -run 'TestConfig' -v`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/app/errors.go internal/app/config.go internal/app/config_test.go
git commit -m "feat(app): add bootstrap config boundary"
```

## Task 2: Extend `internal/service` for app-owned dependency injection

**Files:**
- Modify: `internal/service/options.go`
- Modify: `internal/service/service.go`
- Modify: `internal/service/handler_test.go`

- [ ] **Step 1: Write the failing service-constructor tests**

Add tests for:
- `TestNewServicePreservesInjectedBusinessPorts`
- `TestNewServiceKeepsDefaultsWhenPortsUnset`

Sketch:

```go
func TestNewServicePreservesInjectedBusinessPorts(t *testing.T) {
	users := &fakeIdentityStore{}
	channels := &fakeChannelStore{}
	cluster := &fakeClusterPort{}

	svc := New(Options{
		IdentityStore: users,
		ChannelStore:  channels,
		ClusterPort:   cluster,
	})

	require.Same(t, users, svc.users)
	require.Same(t, channels, svc.channels)
	require.Same(t, cluster, svc.cluster)
}
```

- [ ] **Step 2: Run the targeted tests to verify they fail**

Run: `go test ./internal/service -run 'TestNewService(PreservesInjectedBusinessPorts|KeepsDefaultsWhenPortsUnset)' -v`

Expected: FAIL because `Options` does not yet expose these collaborators.

- [ ] **Step 3: Add narrow business ports and preserve current behavior**

Update `internal/service/options.go` to add ports owned by the service layer:

```go
type IdentityStore interface{}
type ChannelStore interface{}
type ClusterPort interface{}

type Options struct {
	Now               func() time.Time
	Registry          SessionRegistry
	SequenceAllocator SequenceAllocator
	DeliveryPort      DeliveryPort
	IdentityStore     IdentityStore
	ChannelStore      ChannelStore
	ClusterPort       ClusterPort
}
```

Update `internal/service/service.go`:

```go
type Service struct {
	registry  SessionRegistry
	sequencer SequenceAllocator
	delivery  DeliveryPort
	users     IdentityStore
	channels  ChannelStore
	cluster   ClusterPort
	opts      Options
}
```

Implementation notes:
- Keep the new interfaces intentionally narrow or empty for this step; the goal is to create the injection seam, not new behavior
- Do not change existing send-path behavior in this task
- Preserve current defaults for registry, sequencer, delivery, and `Now`

- [ ] **Step 4: Run the targeted tests to verify they pass**

Run: `go test ./internal/service -run 'TestNewService(PreservesInjectedBusinessPorts|KeepsDefaultsWhenPortsUnset)' -v`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/service/options.go internal/service/service.go internal/service/handler_test.go
git commit -m "refactor(service): add app injection seams"
```

## Task 3: Build the `App` runtime and accessors

**Files:**
- Create: `internal/app/app.go`
- Create: `internal/app/build.go`
- Modify: `internal/app/config.go`
- Create: `internal/app/lifecycle_test.go`

- [ ] **Step 1: Write the failing runtime-construction tests**

Add tests for:
- `TestNewBuildsDBClusterStoreServiceAndGateway`
- `TestNewReturnsConfigErrorsBeforeOpeningResources`
- `TestAccessorsExposeBuiltRuntime`

Use temp dirs and a single-node cluster config to keep the test deterministic.

```go
func TestNewBuildsDBClusterStoreServiceAndGateway(t *testing.T) {
	cfg := testConfig(t)

	app, err := New(cfg)
	require.NoError(t, err)
	require.NotNil(t, app.DB())
	require.NotNil(t, app.Cluster())
	require.NotNil(t, app.Store())
	require.NotNil(t, app.Service())
	require.NotNil(t, app.Gateway())
}
```

- [ ] **Step 2: Run the targeted tests to verify they fail**

Run: `go test ./internal/app -run 'Test(NewBuilds|Accessors|NewReturnsConfigErrors)' -v`

Expected: FAIL because `App` and `New` do not exist yet.

- [ ] **Step 3: Implement `App`, `New`, and dependency construction**

Create `internal/app/app.go`:

```go
type App struct {
	cfg Config

	db      *wkdb.DB
	raftDB  *raftstore.DB
	cluster *wkcluster.Cluster
	store   *wkstore.Store
	service *service.Service
	gateway *gateway.Gateway

	startOnce sync.Once
	stopOnce  sync.Once
	started   atomic.Bool
}

func New(cfg Config) (*App, error)
```

Create `internal/app/build.go` to:
- call `cfg.ApplyDefaultsAndValidate()`
- open `wkdb.Open(cfg.Storage.DBPath)`
- open `raftstore.Open(cfg.Storage.RaftPath)`
- derive `wkcluster.Config`
- create `wkcluster.NewCluster(...)`
- create `wkstore.New(cluster, db)`
- create `service.New(service.Options{IdentityStore: store, ChannelStore: store})`
- create `gateway.New(gateway.Options{Handler: svc, ...})`

Important implementation details:
- On build failure, close already-opened `wkdb` and `raftstore` before returning
- Keep `cluster.Start()` and `gateway.Start()` out of `New`
- Translate cluster config IDs into `multiraft.NodeID` and `multiraft.GroupID` inside `app`, not in `cmd`
- Reuse `wkstore.NewStateMachineFactory(db)` when building `wkcluster.Config.NewStateMachine`

- [ ] **Step 4: Run the targeted tests to verify they pass**

Run: `go test ./internal/app -run 'Test(NewBuilds|Accessors|NewReturnsConfigErrors)' -v`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/app/app.go internal/app/build.go internal/app/config.go internal/app/lifecycle_test.go
git commit -m "feat(app): build runtime dependencies"
```

## Task 4: Implement startup, rollback, and stop sequencing

**Files:**
- Create: `internal/app/lifecycle.go`
- Modify: `internal/app/lifecycle_test.go`
- Create: `internal/app/integration_test.go`

- [ ] **Step 1: Write the failing lifecycle tests**

Add tests for:
- `TestStartStartsClusterBeforeGateway`
- `TestStartRollsBackClusterWhenGatewayStartFails`
- `TestStopStopsGatewayBeforeClosingStorage`
- `TestStopIsIdempotent`

Also add an app-level integration test:
- `TestAppStartAcceptsWKProtoConnectionAndStopsCleanly`

The integration test should:
- build a temp-dir single-node cluster config
- call `New` then `Start`
- dial `Gateway().ListenerAddr("tcp-wkproto")`
- send a `ConnectPacket`
- assert a successful `ConnackPacket`
- call `Stop` and assert no error

- [ ] **Step 2: Run the targeted tests to verify they fail**

Run: `go test ./internal/app -run 'Test(Start|Stop|AppStart)' -v`

Expected: FAIL because `Start` and `Stop` do not exist yet.

- [ ] **Step 3: Implement lifecycle sequencing and rollback**

Create `internal/app/lifecycle.go`:

```go
func (a *App) Start() error {
	if a == nil || a.cluster == nil || a.gateway == nil {
		return ErrNotBuilt
	}
	if a.started.Load() {
		return ErrAlreadyStarted
	}
	if err := a.cluster.Start(); err != nil {
		return err
	}
	if err := a.gateway.Start(); err != nil {
		a.cluster.Stop()
		return err
	}
	a.started.Store(true)
	return nil
}

func (a *App) Stop() error
```

Implementation notes:
- Stop order must be `gateway -> cluster -> raftDB -> wkdb`
- `Stop` should always attempt all cleanup steps and join errors with `errors.Join`
- `Stop` should be safe after partial start, failed start, or double invocation
- If `gateway.Start()` fails, stop cluster immediately before returning
- Prefer explicit helper methods such as `stopGateway()`, `stopCluster()`, `closeRaftDB()`, `closeWKDB()` to keep order readable in tests

- [ ] **Step 4: Run the targeted tests to verify they pass**

Run: `go test ./internal/app -run 'Test(Start|Stop|AppStart)' -v`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/app/lifecycle.go internal/app/lifecycle_test.go internal/app/integration_test.go
git commit -m "feat(app): add lifecycle orchestration"
```

## Task 5: Add the `cmd/wukongim` entrypoint

**Files:**
- Create: `cmd/wukongim/config.go`
- Create: `cmd/wukongim/config_test.go`
- Create: `cmd/wukongim/main.go`

- [ ] **Step 1: Write the failing command config tests**

Add tests for:
- `TestLoadConfigParsesJSONFileIntoAppConfig`
- `TestLoadConfigRejectsMissingConfigPath`

Use a minimal JSON payload to avoid new dependencies:

```go
{
  "node": { "id": 1, "dataDir": "/tmp/wukong-node-1" },
  "cluster": {
    "listenAddr": "127.0.0.1:7000",
    "nodes": [{ "id": 1, "addr": "127.0.0.1:7000" }],
    "groups": [{ "id": 1, "peers": [1] }]
  },
  "gateway": {
    "listeners": [
      { "name": "tcp-wkproto", "network": "tcp", "address": "127.0.0.1:5100", "transport": "stdnet", "protocol": "wkproto" }
    ]
  }
}
```

- [ ] **Step 2: Run the targeted tests to verify they fail**

Run: `go test ./cmd/wukongim -run 'TestLoadConfig' -v`

Expected: FAIL because `cmd/wukongim` does not exist yet.

- [ ] **Step 3: Implement config loading and process main**

Create `cmd/wukongim/config.go`:
- implement `loadConfig(path string) (app.Config, error)`
- load startup config from `wukongim.conf` and environment variables
- translate known `WK_*` values into `app.Config`

Create `cmd/wukongim/main.go`:

```go
func main() {
	configPath := flag.String("config", "", "path to wukongim.conf file")
	flag.Parse()

	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatal(err)
	}

	application, err := app.New(cfg)
	if err != nil {
		log.Fatal(err)
	}
	if err := application.Start(); err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := application.Stop(); err != nil {
			log.Printf("stop app: %v", err)
		}
	}()

	waitForSignal()
}
```

Implementation notes:
- Use `signal.NotifyContext` for `SIGINT` and `SIGTERM`
- Keep main package free of runtime assembly details
- Do not add YAML/TOML dependencies in v1; JSON keeps the entrypoint dependency-free and easy to test

- [ ] **Step 4: Run the targeted tests to verify they pass**

Run: `go test ./cmd/wukongim -run 'TestLoadConfig' -v`

Expected: PASS

- [ ] **Step 5: Run focused package verification**

Run: `go test ./internal/app ./internal/service ./cmd/wukongim -v`

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add cmd/wukongim/config.go cmd/wukongim/config_test.go cmd/wukongim/main.go
git commit -m "feat(cmd): add app bootstrap entrypoint"
```

## Task 6: Final verification and integration sweep

**Files:**
- Modify: `internal/app/integration_test.go` if needed
- Modify: `cmd/wukongim/config_test.go` if needed

- [ ] **Step 1: Run the app integration test alone**

Run: `go test ./internal/app -run 'TestAppStartAcceptsWKProtoConnectionAndStopsCleanly' -v`

Expected: PASS

- [ ] **Step 2: Run the main bootstrap package set**

Run: `go test ./internal/app ./internal/service ./internal/gateway ./pkg/wkcluster ./pkg/wkstore ./pkg/wkdb ./pkg/raftstore ./cmd/wukongim -v`

Expected: PASS

- [ ] **Step 3: Run full repository verification**

Run: `go test ./...`

Expected: PASS

- [ ] **Step 4: Commit final polish if any fixes were needed**

```bash
git add internal/app internal/service cmd/wukongim
git commit -m "test: verify app bootstrap integration"
```

## Notes for Execution

- Keep `internal/app` thin. If a helper starts looking like business logic, move it back down to the correct package.
- Do not let `cmd` create `wkcluster.Config.NewStorage` or `wkcluster.Config.NewStateMachine`. That translation belongs in `internal/app`.
- Do not widen `internal/service` behavior in this plan beyond adding the dependency seam needed by `app`.
- Prefer one deterministic single-node cluster integration test first. Multi-node process assembly can come in a follow-up after the bootstrap path is stable.
