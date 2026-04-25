# Manager API Foundation Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a separate manager HTTP service with JWT login, resource-based read permissions, and a first `GET /manager/nodes` endpoint for backend management systems.

**Architecture:** Keep manager HTTP concerns in a new `internal/access/manager` package and move node aggregation into a thin `internal/usecase/management` package so the existing `access -> usecase -> pkg` direction stays intact. Extend config and app wiring just enough to support a second HTTP server, and add a small `pkg/cluster.API` read hook for controller leader lookup instead of leaking concrete cluster types into the usecase layer.

**Tech Stack:** Go, `gin`, `testing`, `testify`, `github.com/golang-jwt/jwt/v5`, `cmd/wukongim`, `internal/app`, `internal/access/manager`, `internal/usecase/management`, `pkg/cluster`, Markdown config/docs.

---

## References

- Spec: `docs/superpowers/specs/2026-04-21-manager-api-foundation-design.md`
- Follow `@superpowers:test-driven-development` for every code change.
- Run `@superpowers:verification-before-completion` before claiming execution is done.
- Before editing any touched package, re-check whether that package now has a `FLOW.md`; update it if the implementation changes the documented flow.
- Add English comments on new key structs, exported config fields, and non-obvious methods to satisfy `AGENTS.md`.

## File Structure

- Create: `internal/access/manager/server.go` — manager HTTP server/options/start-stop wrapper.
- Create: `internal/access/manager/routes.go` — route registration and middleware wiring.
- Create: `internal/access/manager/auth.go` — JWT claims, static-user auth helpers, permission checks.
- Create: `internal/access/manager/login.go` — `POST /manager/login` handler.
- Create: `internal/access/manager/nodes.go` — `GET /manager/nodes` handler.
- Create: `internal/access/manager/server_test.go` — manager HTTP contract tests.
- Create: `internal/usecase/management/app.go` — management usecase dependencies and constructor.
- Create: `internal/usecase/management/nodes.go` — node DTOs plus aggregation logic.
- Create: `internal/usecase/management/nodes_test.go` — node aggregation tests.
- Create: `pkg/cluster/controller_info.go` — `ControllerLeaderID()` implementation for `*cluster.Cluster`.
- Modify: `pkg/cluster/api.go` — expose the new read method on the cluster interface.
- Modify: `internal/app/config.go` — add manager config types and validation.
- Modify: `internal/app/config_test.go` — config validation tests for manager settings.
- Modify: `cmd/wukongim/config.go` — parse `WK_MANAGER_*` config keys.
- Modify: `cmd/wukongim/config_test.go` — config loader tests for manager settings.
- Modify: `internal/app/app.go` — hold the manager server and optional accessor.
- Modify: `internal/app/build.go` — construct the management usecase and manager server.
- Modify: `internal/app/lifecycle.go` — start/stop the manager server in app lifecycle order.
- Modify: `internal/app/build_test.go` — optional manager build coverage.
- Modify: `internal/app/lifecycle_test.go` — manager lifecycle coverage.
- Modify: `go.mod` — add `github.com/golang-jwt/jwt/v5`.
- Modify: `go.sum` — record the JWT dependency hashes.
- Modify: `wukongim.conf.example` — document manager config example.
- Modify: `AGENTS.md` — add the new `internal/access/manager` and `internal/usecase/management` directories to the directory structure section.

### Task 1: Add manager config types, parsing, and validation

**Files:**
- Modify: `internal/app/config.go`
- Modify: `internal/app/config_test.go`
- Modify: `cmd/wukongim/config.go`
- Modify: `cmd/wukongim/config_test.go`

- [ ] **Step 1: Write the failing manager config tests**

Add tests that lock the intended behavior before touching production code, for example:

```go
func TestLoadConfigParsesManagerSettings(t *testing.T) {
    configPath := writeConf(t, dir, "wukongim.conf",
        "WK_NODE_ID=1",
        "WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
        "WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
        "WK_CLUSTER_INITIAL_SLOT_COUNT=1",
        `WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
        `WK_GATEWAY_LISTENERS=[{"name":"tcp-wkproto","network":"tcp","address":"127.0.0.1:5100","transport":"stdnet","protocol":"wkproto"}]`,
        "WK_MANAGER_LISTEN_ADDR=127.0.0.1:5301",
        "WK_MANAGER_AUTH_ON=true",
        "WK_MANAGER_JWT_SECRET=test-secret",
        "WK_MANAGER_JWT_ISSUER=wukongim-manager",
        "WK_MANAGER_JWT_EXPIRE=24h",
        `WK_MANAGER_USERS=[{"username":"admin","password":"secret","permissions":[{"resource":"cluster.node","actions":["r"]}]}]`,
    )

    cfg, err := loadConfig(configPath)
    require.NoError(t, err)
    require.Equal(t, "127.0.0.1:5301", cfg.Manager.ListenAddr)
    require.True(t, cfg.Manager.AuthOn)
    require.Equal(t, "wukongim-manager", cfg.Manager.JWTIssuer)
    require.Len(t, cfg.Manager.Users, 1)
}
```

Also add validation tests for:
- missing `WK_MANAGER_JWT_SECRET` when auth is on,
- empty `WK_MANAGER_USERS` when auth is on,
- invalid permission action values,
- manager disabled when `WK_MANAGER_LISTEN_ADDR` is empty.

- [ ] **Step 2: Run the focused config tests to verify they fail**

Run: `go test ./cmd/wukongim ./internal/app -run 'TestLoadConfig.*Manager|TestConfig.*Manager' -count=1`
Expected: FAIL because manager config fields, parsing, and validation do not exist yet.

- [ ] **Step 3: Implement the minimal manager config model and parser**

Add manager config types with English comments on the exported fields, then parse and validate only the fields required by the spec:

```go
type ManagerConfig struct {
    ListenAddr string
    AuthOn     bool
    JWTSecret  string
    JWTIssuer  string
    JWTExpire  time.Duration
    Users      []ManagerUserConfig
}

type ManagerUserConfig struct {
    Username    string
    Password    string
    Permissions []ManagerPermissionConfig
}

type ManagerPermissionConfig struct {
    Resource string
    Actions  []string
}
```

Wire `WK_MANAGER_*` parsing in `cmd/wukongim/config.go`, keep manager disabled when `ListenAddr` is blank, and enforce validation only when the manager service is enabled.

- [ ] **Step 4: Re-run the focused config tests to verify they pass**

Run: `go test ./cmd/wukongim ./internal/app -run 'TestLoadConfig.*Manager|TestConfig.*Manager' -count=1`
Expected: PASS.

- [ ] **Step 5: Commit the config slice**

Run:

```bash
git add internal/app/config.go internal/app/config_test.go cmd/wukongim/config.go cmd/wukongim/config_test.go
git commit -m "feat: add manager config model"
```

### Task 2: Add the management usecase and cluster leader read hook

**Files:**
- Modify: `pkg/cluster/api.go`
- Create: `pkg/cluster/controller_info.go`
- Create: `internal/usecase/management/app.go`
- Create: `internal/usecase/management/nodes.go`
- Create: `internal/usecase/management/nodes_test.go`

- [ ] **Step 1: Write the failing node aggregation tests**

Create a fake cluster reader in `internal/usecase/management/nodes_test.go` and lock the DTO contract:

```go
func TestListNodesAggregatesControllerRoleAndSlotCounts(t *testing.T) {
    app := New(Options{
        LocalNodeID:      2,
        ControllerPeerIDs: []uint64{1, 2},
        Cluster: fakeClusterReader{
            controllerLeaderID: 1,
            nodes: []controllermeta.ClusterNode{
                {NodeID: 1, Addr: "127.0.0.1:7001", Status: controllermeta.NodeStatusAlive, CapacityWeight: 1},
                {NodeID: 2, Addr: "127.0.0.1:7002", Status: controllermeta.NodeStatusDraining, CapacityWeight: 2},
                {NodeID: 3, Addr: "127.0.0.1:7003", Status: controllermeta.NodeStatusAlive, CapacityWeight: 1},
            },
            views: []controllermeta.SlotRuntimeView{
                {SlotID: 1, CurrentPeers: []uint64{1, 2}, LeaderID: 1, HasQuorum: true},
                {SlotID: 2, CurrentPeers: []uint64{2, 3}, LeaderID: 2, HasQuorum: true},
            },
        },
    })

    got, err := app.ListNodes(context.Background())
    require.NoError(t, err)
    require.Equal(t, []Node{
        {NodeID: 1, ControllerRole: "leader", SlotCount: 1, LeaderSlotCount: 1},
        {NodeID: 2, ControllerRole: "follower", SlotCount: 2, LeaderSlotCount: 1, IsLocal: true},
        {NodeID: 3, ControllerRole: "none", SlotCount: 1, LeaderSlotCount: 0},
    }, summarize(got))
}
```

Also add tests for stable `node_id` sorting and status-to-string mapping.

- [ ] **Step 2: Run the focused usecase tests to verify they fail**

Run: `go test ./internal/usecase/management -run 'TestListNodes' -count=1`
Expected: FAIL because the management package and `ControllerLeaderID()` hook do not exist yet.

- [ ] **Step 3: Implement the minimal cluster/read-model changes**

Expose the smallest new cluster interface method and keep the usecase dependency concrete-free:

```go
// pkg/cluster/api.go
ControllerLeaderID() uint64
```

```go
// pkg/cluster/controller_info.go
func (c *Cluster) ControllerLeaderID() uint64 {
    if c == nil || c.controller == nil {
        return 0
    }
    return c.controller.LeaderID()
}
```

Then implement `internal/usecase/management` with:
- a constructor that takes `LocalNodeID`, `ControllerPeerIDs`, and a narrow cluster reader interface,
- DTO structs for the manager nodes response,
- `ListNodes(ctx)` that aggregates node basics, controller role, `slot_count`, and `leader_slot_count`.

- [ ] **Step 4: Re-run the focused usecase tests to verify they pass**

Run: `go test ./internal/usecase/management -run 'TestListNodes' -count=1`
Expected: PASS.

- [ ] **Step 5: Commit the usecase slice**

Run:

```bash
git add pkg/cluster/api.go pkg/cluster/controller_info.go internal/usecase/management/app.go internal/usecase/management/nodes.go internal/usecase/management/nodes_test.go
git commit -m "feat: add manager node aggregation usecase"
```

### Task 3: Add the manager HTTP server, JWT auth, and node routes

**Files:**
- Create: `internal/access/manager/server.go`
- Create: `internal/access/manager/routes.go`
- Create: `internal/access/manager/auth.go`
- Create: `internal/access/manager/login.go`
- Create: `internal/access/manager/nodes.go`
- Create: `internal/access/manager/server_test.go`
- Modify: `go.mod`
- Modify: `go.sum`

- [ ] **Step 1: Write the failing HTTP contract tests**

Add handler/server tests that cover the full first-version contract:

```go
func TestManagerLoginIssuesJWTForAuthorizedUser(t *testing.T) { /* expect 200 and Bearer token */ }
func TestManagerLoginRejectsInvalidCredentials(t *testing.T) { /* expect 401 */ }
func TestManagerNodesRejectsMissingToken(t *testing.T) { /* expect 401 */ }
func TestManagerNodesRejectsInsufficientPermission(t *testing.T) { /* expect 403 */ }
func TestManagerNodesReturnsAggregatedList(t *testing.T) { /* expect items DTO */ }
```

Use a stub management usecase so these tests only validate HTTP, JWT, and permission behavior.

- [ ] **Step 2: Run the focused manager HTTP tests to verify they fail**

Run: `go test ./internal/access/manager -run 'TestManager(Login|Nodes)' -count=1`
Expected: FAIL because the package, routes, and JWT dependency do not exist yet.

- [ ] **Step 3: Implement the minimal manager server**

Add the JWT dependency, then implement a small server with separate responsibilities:

```go
type Options struct {
    ListenAddr string
    Auth       AuthConfig
    Management ManagementUsecase
    Logger     wklog.Logger
}
```

Implementation notes:
- register `POST /manager/login` and `GET /manager/nodes`,
- use `jwt/v5` for signing and parsing,
- parse `Authorization: Bearer <token>`,
- re-check permissions from the configured users after JWT validation,
- return `401` for bad/expired tokens, `403` for missing `cluster.node:r`, and `500` for usecase errors.

- [ ] **Step 4: Re-run the focused manager HTTP tests to verify they pass**

Run: `go test ./internal/access/manager -run 'TestManager(Login|Nodes)' -count=1`
Expected: PASS.

- [ ] **Step 5: Commit the manager HTTP slice**

Run:

```bash
git add internal/access/manager/server.go internal/access/manager/routes.go internal/access/manager/auth.go internal/access/manager/login.go internal/access/manager/nodes.go internal/access/manager/server_test.go go.mod go.sum
git commit -m "feat: add manager http server"
```

### Task 4: Wire the manager server into app lifecycle and repo metadata

**Files:**
- Modify: `internal/app/app.go`
- Modify: `internal/app/build.go`
- Modify: `internal/app/lifecycle.go`
- Modify: `internal/app/build_test.go`
- Modify: `internal/app/lifecycle_test.go`
- Modify: `wukongim.conf.example`
- Modify: `AGENTS.md`

- [ ] **Step 1: Write the failing app wiring tests**

Add tests that prove manager is optional but fully wired when configured, for example:

```go
func TestNewBuildsOptionalManagerServerWhenConfigured(t *testing.T) {
    cfg := testConfig(t)
    cfg.Manager.ListenAddr = "127.0.0.1:0"
    cfg.Manager.AuthOn = true
    cfg.Manager.JWTSecret = "secret"
    cfg.Manager.JWTIssuer = "wukongim-manager"
    cfg.Manager.JWTExpire = 24 * time.Hour
    cfg.Manager.Users = []ManagerUserConfig{{
        Username: "admin",
        Password: "secret",
        Permissions: []ManagerPermissionConfig{{Resource: "cluster.node", Actions: []string{"r"}}},
    }}

    app, err := New(cfg)
    require.NoError(t, err)
    require.NotNil(t, app.Manager())
}
```

Also add a lifecycle test that starts the app and asserts the manager server binds an address when configured.

- [ ] **Step 2: Run the focused app tests to verify they fail**

Run: `go test ./internal/app -run 'TestNewBuildsOptionalManagerServerWhenConfigured|TestStart.*Manager' -count=1`
Expected: FAIL because `App` does not yet know about the manager service.

- [ ] **Step 3: Implement the minimal app wiring**

Add a manager field and lifecycle hooks beside the existing API server:

```go
// internal/app/app.go
manager *accessmanager.Server
```

Wire in `internal/app/build.go`:
- build the `internal/usecase/management` app,
- derive controller peer IDs from `cfg.Cluster.DerivedControllerNodes()`,
- construct `internal/access/manager` when `cfg.Manager.ListenAddr != ""`.

Wire in `internal/app/lifecycle.go`:
- start manager after gateway/API startup settles,
- stop manager during app shutdown,
- preserve the existing optional-server semantics.

- [ ] **Step 4: Update example and repo metadata**

Update `wukongim.conf.example` with a clearly commented manager section and production-secret warning.

Update `AGENTS.md` so the directory structure section includes:
- `internal/access/manager`
- `internal/usecase/management`

- [ ] **Step 5: Re-run the focused app tests to verify they pass**

Run: `go test ./internal/app -run 'TestNewBuildsOptionalManagerServerWhenConfigured|TestStart.*Manager' -count=1`
Expected: PASS.

- [ ] **Step 6: Commit the app wiring slice**

Run:

```bash
git add internal/app/app.go internal/app/build.go internal/app/lifecycle.go internal/app/build_test.go internal/app/lifecycle_test.go wukongim.conf.example AGENTS.md
git commit -m "feat: wire manager server into app"
```

### Task 5: Run the end-to-end verification suite and final review

**Files:**
- Review: `docs/superpowers/specs/2026-04-21-manager-api-foundation-design.md`
- Review: `docs/superpowers/plans/2026-04-21-manager-api-foundation.md`

- [ ] **Step 1: Run the focused verification suite**

Run: `go test ./cmd/wukongim ./internal/app ./internal/usecase/management ./internal/access/manager ./pkg/cluster -count=1`
Expected: PASS.

- [ ] **Step 2: Confirm the expected manager integration points exist and no accidental paths were skipped**

Run: `rg -n 'WK_MANAGER_|cluster\.node|ControllerLeaderID|/manager/login|/manager/nodes' cmd internal pkg wukongim.conf.example AGENTS.md`
Expected: matches only in the new manager config, auth, usecase, and docs paths.

- [ ] **Step 3: Review the recent commit/file scope for control**

Run: `git log --stat --oneline -5`
Expected: only the manager config, usecase, access server, app wiring, dependency, and docs commits/files appear in the recent history for this feature branch.

- [ ] **Step 4: If implementation forced a spec-level deviation, update the spec before handoff**

If the code required a necessary change from the approved design, reconcile `docs/superpowers/specs/2026-04-21-manager-api-foundation-design.md` before marking the work complete. Otherwise leave the spec untouched.

- [ ] **Step 5: Commit the final verified integration state**

Run:

```bash
git add docs/superpowers/specs/2026-04-21-manager-api-foundation-design.md
git commit -m "feat: add manager api foundation"
```

If the spec file did not change in Step 4, replace the `git add` command with the actual implementation files still left unstaged from the previous tasks.
