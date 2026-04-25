# Process E2E Foundation Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a new `test/e2e` black-box test layer that starts the real `cmd/wukongim` binary as a child process and proves one fresh `单节点集群` can complete a real `WKProto` send/recv/ack flow.

**Architecture:** Keep the new e2e layer fully external: build the production binary once in `TestMain`, generate temporary `wukongim.conf` files with official `WK_` keys, start child processes with node-scoped temp directories, and assert behavior only through real `WKProto` clients. Put harness concerns into a small `test/e2e/suite` package so later three-node process e2e can extend the same lifecycle without importing `internal/app`.

**Tech Stack:** Go 1.23, `testing`, `testify/require`, `os/exec`, temporary config files, `pkg/protocol/frame`, `pkg/protocol/codec`, `internal/gateway/testkit` client crypto helpers, `//go:build e2e`.

---

## References

- Spec: `docs/superpowers/specs/2026-04-23-e2e-test-architecture-design.md`
- Follow `@superpowers:test-driven-development` for each behavior-bearing slice: write the failing test first, run it red, implement the minimum code, run it green.
- Run `@superpowers:verification-before-completion` before claiming execution is done.
- In Codex CLI, only use subagents if the user explicitly asks for delegation; otherwise execute this plan locally with `@superpowers:executing-plans`.
- `cmd/wukongim`, `internal/app`, `internal/gateway`, and `pkg/protocol/frame` currently have no `FLOW.md`; re-check before editing in case one appears.

## Critical Review Notes Before Starting

- Keep `test/e2e` black-box. Do not import `internal/app` or add internal-state assertions just to make the first scenario easier.
- Every file under `test/e2e` must carry `//go:build e2e`; otherwise `go test ./...` will start compiling the new harness by default and blur the layer boundary.
- The first scenario is intentionally a `单节点集群`. Do not expand scope to three-node orchestration, leader change, or Docker Compose in this implementation.
- Use the real `wukongim.conf` format with `WK_` keys. Do not create a test-only config DSL.
- Adding `test/e2e` changes the top-level repository structure, so `AGENTS.md` must be updated.
- If the black-box scenario exposes a genuine production defect in first-send bootstrap or gateway startup, fix the production bug with the smallest possible change after reproducing it with the failing e2e test.

## File Structure

- Modify: `AGENTS.md` — add `test/` and `test/e2e` to the directory structure section.
- Create: `test/e2e/README.md` — explain the purpose, boundaries, run commands, and failure triage for the new layer.
- Create: `test/e2e/e2e_test.go` — own `TestMain` and the first `TestE2E_WKProtoSendMessage_SingleNodeCluster` scenario.
- Create: `test/e2e/suite/binary.go` — build and cache the `cmd/wukongim` binary once per test process.
- Create: `test/e2e/suite/binary_test.go` — lock build-cache semantics with injectable test hooks.
- Create: `test/e2e/suite/config.go` — render node specs and `wukongim.conf` files using official `WK_` keys.
- Create: `test/e2e/suite/config_test.go` — assert required keys and single-node cluster config rendering.
- Create: `test/e2e/suite/ports.go` — reserve distinct loopback addresses for cluster/gateway/API listeners.
- Create: `test/e2e/suite/ports_test.go` — assert distinct loopback addresses and stable formatting.
- Create: `test/e2e/suite/process.go` — start/stop child processes, capture stdout/stderr, and surface diagnostics.
- Create: `test/e2e/suite/process_test.go` — lock stop behavior and diagnostics formatting with helper processes.
- Create: `test/e2e/suite/readiness.go` — wait for TCP and protocol-level `WKProto` readiness.
- Create: `test/e2e/suite/readiness_test.go` — assert readiness rejects bare TCP and accepts a valid `Connack`.
- Create: `test/e2e/suite/runtime.go` — assemble a per-test workspace, node specs, and single-node suite startup.
- Create: `test/e2e/suite/runtime_test.go` — assert workspace layout, node-scoped paths, and retry-free single-node assembly.
- Create: `test/e2e/suite/wkproto_client.go` — black-box `WKProto` client wrapper for connect/send/recv/ack.

### Task 1: Scaffold the tagged `test/e2e` package and static harness primitives

**Files:**
- Modify: `AGENTS.md`
- Create: `test/e2e/README.md`
- Create: `test/e2e/suite/config.go`
- Create: `test/e2e/suite/config_test.go`
- Create: `test/e2e/suite/ports.go`
- Create: `test/e2e/suite/ports_test.go`
- Create: `test/e2e/suite/runtime.go`
- Create: `test/e2e/suite/runtime_test.go`

- [ ] **Step 1: Write the failing static harness tests and structure checks**

Add focused tests that lock the non-runtime foundation first:

- `TestRenderSingleNodeConfigUsesOfficialWKKeys`
- `TestReserveLoopbackPortsReturnsDistinctAddresses`
- `TestNewWorkspaceCreatesNodeScopedPaths`

Also add a simple structure check for `AGENTS.md` using a shell assertion in the task run step so the new top-level directory cannot be forgotten.

Use a config assertion shape like:

```go
func TestRenderSingleNodeConfigUsesOfficialWKKeys(t *testing.T) {
    spec := NodeSpec{
        ID:          1,
        Name:        "node-1",
        DataDir:     "/tmp/node-1/data",
        ConfigPath:  "/tmp/node-1/wukongim.conf",
        ClusterAddr: "127.0.0.1:17000",
        GatewayAddr: "127.0.0.1:15100",
    }

    cfg := RenderSingleNodeConfig(spec)
    require.Contains(t, cfg, "WK_NODE_ID=1")
    require.Contains(t, cfg, "WK_CLUSTER_SLOT_COUNT=1")
    require.Contains(t, cfg, `"transport":"stdnet"`)
}
```

- [ ] **Step 2: Run the focused static tests to verify they fail**

Run:

```bash
go test -tags=e2e ./test/e2e/suite -run 'Test(RenderSingleNodeConfigUsesOfficialWKKeys|ReserveLoopbackPortsReturnsDistinctAddresses|NewWorkspaceCreatesNodeScopedPaths)$' -count=1
rg -n "test/e2e" AGENTS.md
```

Expected:

- `go test ...` FAIL because the tagged package and helpers do not exist yet.
- `rg ...` returns no match because `AGENTS.md` has not been updated yet.

- [ ] **Step 3: Implement the minimal static harness layer**

Implement only the smallest code needed for the tests:

- add `//go:build e2e` to every new file in `test/e2e`
- create `README.md` with run/debug rules
- update `AGENTS.md` directory structure with `test/e2e`
- define `NodeSpec`, `PortSet`, and `Workspace`
- implement loopback port reservation and single-node config rendering

Keep the config renderer explicit and boring, for example:

```go
type NodeSpec struct {
    ID          uint64
    Name        string
    RootDir     string
    DataDir     string
    ConfigPath  string
    StdoutPath  string
    StderrPath  string
    ClusterAddr string
    GatewayAddr string
    APIAddr     string
}

func RenderSingleNodeConfig(spec NodeSpec) string {
    return strings.Join([]string{
        fmt.Sprintf("WK_NODE_ID=%d", spec.ID),
        fmt.Sprintf("WK_NODE_NAME=%s", spec.Name),
        fmt.Sprintf("WK_NODE_DATA_DIR=%s", spec.DataDir),
        fmt.Sprintf("WK_CLUSTER_LISTEN_ADDR=%s", spec.ClusterAddr),
        "WK_CLUSTER_SLOT_COUNT=1",
        fmt.Sprintf(`WK_CLUSTER_NODES=[{"id":%d,"addr":"%s"}]`, spec.ID, spec.ClusterAddr),
        fmt.Sprintf(`WK_GATEWAY_LISTENERS=[{"name":"tcp-wkproto","network":"tcp","address":"%s","transport":"stdnet","protocol":"wkproto"}]`, spec.GatewayAddr),
    }, "\n") + "\n"
}
```

- [ ] **Step 4: Re-run the focused static tests to verify they pass**

Run:

```bash
go test -tags=e2e ./test/e2e/suite -run 'Test(RenderSingleNodeConfigUsesOfficialWKKeys|ReserveLoopbackPortsReturnsDistinctAddresses|NewWorkspaceCreatesNodeScopedPaths)$' -count=1
rg -n "test/e2e" AGENTS.md
```

Expected: PASS, and `AGENTS.md` now includes the new directory entry.

- [ ] **Step 5: Commit the static harness slice**

Run:

```bash
git add AGENTS.md test/e2e/README.md test/e2e/suite/config.go test/e2e/suite/config_test.go test/e2e/suite/ports.go test/e2e/suite/ports_test.go test/e2e/suite/runtime.go test/e2e/suite/runtime_test.go
git commit -m "test: scaffold process e2e suite"
```

### Task 2: Add binary build caching and child-process lifecycle support

**Files:**
- Create: `test/e2e/suite/binary.go`
- Create: `test/e2e/suite/binary_test.go`
- Create: `test/e2e/suite/process.go`
- Create: `test/e2e/suite/process_test.go`
- Modify: `test/e2e/suite/runtime.go`

- [ ] **Step 1: Write the failing build-cache and process tests**

Add focused tests for:

- `TestBinaryCacheBuildsOncePerProcess`
- `TestNodeProcessStopPrefersSIGTERMBeforeKill`
- `TestNodeProcessDumpDiagnosticsIncludesConfigAndLogPaths`

For `BinaryCache`, inject a fake builder function so the test locks `sync.Once`
behavior without running a real `go build`:

```go
func TestBinaryCacheBuildsOncePerProcess(t *testing.T) {
    var calls int
    cache := BinaryCache{
        build: func(dst string) error {
            calls++
            return os.WriteFile(dst, []byte("fake"), 0o755)
        },
    }

    first, err := cache.Path(t.TempDir())
    require.NoError(t, err)
    second, err := cache.Path(t.TempDir())
    require.NoError(t, err)
    require.Equal(t, 1, calls)
    require.Equal(t, first, second)
}
```

Use the Go helper-process pattern in `process_test.go` so stop/exit behavior is
tested without introducing shell-specific assumptions.

- [ ] **Step 2: Run the focused lifecycle tests to verify they fail**

Run:

```bash
go test -tags=e2e ./test/e2e/suite -run 'Test(BinaryCacheBuildsOncePerProcess|NodeProcessStopPrefersSIGTERMBeforeKill|NodeProcessDumpDiagnosticsIncludesConfigAndLogPaths)$' -count=1
```

Expected: FAIL because neither the build cache nor the process wrapper exists yet.

- [ ] **Step 3: Implement the minimal build and process layer**

Implement:

- `BinaryCache` with `sync.Once`, a cached path, and an injectable `build` func
- real builder path that runs `go build -o <dst> ./cmd/wukongim`
- `NodeProcess.Start()`, `Stop()`, and `DumpDiagnostics()`
- stdout/stderr file capture
- `SIGTERM -> timeout -> Kill` shutdown flow

Keep the process wrapper small, for example:

```go
type NodeProcess struct {
    Spec      NodeSpec
    Cmd       *exec.Cmd
    StdoutLog *os.File
    StderrLog *os.File
}

func (p *NodeProcess) Start(binaryPath string) error {
    cmd := exec.Command(binaryPath, "-config", p.Spec.ConfigPath)
    cmd.Stdout = p.StdoutLog
    cmd.Stderr = p.StderrLog
    p.Cmd = cmd
    return cmd.Start()
}
```

- [ ] **Step 4: Re-run the focused lifecycle tests to verify they pass**

Run:

```bash
go test -tags=e2e ./test/e2e/suite -run 'Test(BinaryCacheBuildsOncePerProcess|NodeProcessStopPrefersSIGTERMBeforeKill|NodeProcessDumpDiagnosticsIncludesConfigAndLogPaths)$' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit the lifecycle slice**

Run:

```bash
git add test/e2e/suite/binary.go test/e2e/suite/binary_test.go test/e2e/suite/process.go test/e2e/suite/process_test.go test/e2e/suite/runtime.go
git commit -m "test: add e2e binary and process lifecycle helpers"
```

### Task 3: Add protocol readiness and the black-box `WKProto` client wrapper

**Files:**
- Create: `test/e2e/suite/readiness.go`
- Create: `test/e2e/suite/readiness_test.go`
- Create: `test/e2e/suite/wkproto_client.go`
- Modify: `test/e2e/suite/runtime.go`

- [ ] **Step 1: Write the failing readiness and client tests**

Add tests for:

- `TestWaitWKProtoReadyRejectsBareTCPListener`
- `TestWaitWKProtoReadyAcceptsSuccessfulConnack`
- `TestWKProtoClientDecryptsRecvPacketAfterConnack`

Use a tiny fake listener in `readiness_test.go`:

- one variant accepts TCP but never returns a valid `ConnackPacket`
- one variant sends a valid `ConnackPacket`
- one variant sends an encrypted `RecvPacket` after the client applies the connack

The client test should prove the wrapper uses `internal/gateway/testkit`
correctly rather than open-coding crypto in the scenario test.

- [ ] **Step 2: Run the focused protocol tests to verify they fail**

Run:

```bash
go test -tags=e2e ./test/e2e/suite -run 'Test(WaitWKProtoReadyRejectsBareTCPListener|WaitWKProtoReadyAcceptsSuccessfulConnack|WKProtoClientDecryptsRecvPacketAfterConnack)$' -count=1
```

Expected: FAIL because the readiness probe and client wrapper do not exist yet.

- [ ] **Step 3: Implement the minimal protocol layer**

Implement:

- `WaitWKProtoReady(ctx, addr)` that first dials TCP and then performs a real `ConnectPacket -> ConnackPacket` handshake
- `WKProtoClient` wrapper with `Connect`, `Send`, `ReadFrame`, `ReadSendAck`, `ReadRecv`, and `RecvAck`
- `Connack` application and send/recv encryption/decryption through `testkit.WKProtoClient`

Keep the wrapper intentionally small:

```go
type WKProtoClient struct {
    conn   net.Conn
    crypto *testkit.WKProtoClient
}

func (c *WKProtoClient) Connect(addr, uid, deviceID string) (*frame.ConnackPacket, error) {
    // dial, send ConnectPacket with client key, read ConnackPacket, apply keys
}
```

- [ ] **Step 4: Re-run the focused protocol tests to verify they pass**

Run:

```bash
go test -tags=e2e ./test/e2e/suite -run 'Test(WaitWKProtoReadyRejectsBareTCPListener|WaitWKProtoReadyAcceptsSuccessfulConnack|WKProtoClientDecryptsRecvPacketAfterConnack)$' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit the protocol slice**

Run:

```bash
git add test/e2e/suite/readiness.go test/e2e/suite/readiness_test.go test/e2e/suite/wkproto_client.go test/e2e/suite/runtime.go
git commit -m "test: add e2e wkproto readiness helpers"
```

### Task 4: Add the first real single-node send e2e scenario

**Files:**
- Create: `test/e2e/e2e_test.go`
- Modify: `test/e2e/suite/binary.go`
- Modify: `test/e2e/suite/runtime.go`
- Modify: `test/e2e/suite/process.go`
- Modify: `test/e2e/suite/readiness.go`
- Modify: `test/e2e/suite/wkproto_client.go`

- [ ] **Step 1: Write the failing first end-to-end scenario**

Add `TestE2E_WKProtoSendMessage_SingleNodeCluster` with this exact contract:

- start a fresh real `单节点集群`
- connect sender `u1`
- connect recipient `u2`
- send one person message from `u1` to `u2`
- assert `SendAckPacket.ReasonCode == frame.ReasonSuccess`
- assert `MessageID != 0` and `MessageSeq != 0`
- assert the recipient receives `RecvPacket`
- assert `RecvPacket.ChannelID == "u1"` and `ChannelType == frame.ChannelTypePerson`
- send `RecvackPacket`

Use a scenario skeleton like:

```go
func TestE2E_WKProtoSendMessage_SingleNodeCluster(t *testing.T) {
    s := suite.New(t, testBinaryPath)
    node := s.StartSingleNodeCluster()

    sender := s.NewWKProtoClient(t)
    recipient := s.NewWKProtoClient(t)

    require.NoError(t, sender.Connect(node.GatewayAddr(), "u1", "u1-device"))
    require.NoError(t, recipient.Connect(node.GatewayAddr(), "u2", "u2-device"))

    sendAck, err := sender.SendPersonMessage("u2", 1, "e2e-msg-1", []byte("hello from e2e"))
    require.NoError(t, err)
    require.Equal(t, frame.ReasonSuccess, sendAck.ReasonCode)
}
```

- [ ] **Step 2: Run the focused e2e test to verify it fails**

Run:

```bash
go test -tags=e2e ./test/e2e/... -run TestE2E_WKProtoSendMessage_SingleNodeCluster -count=1 -timeout 10m
```

Expected: FAIL because the end-to-end suite startup path is not fully wired yet, or because the first black-box scenario reveals a real startup/first-send bug.

- [ ] **Step 3: Implement the minimum real suite wiring needed to pass**

Complete:

- `TestMain` binary build + shared binary path handoff
- `suite.New(...)`
- `suite.StartSingleNodeCluster()`
- config write + child process start + readiness wait
- scenario-specific client helpers for send/read/ack

If the scenario reveals a real product issue, fix it with the smallest possible
production change after reproducing it with the red e2e test.

- [ ] **Step 4: Re-run the focused e2e scenario to verify it passes**

Run:

```bash
go test -tags=e2e ./test/e2e/... -run TestE2E_WKProtoSendMessage_SingleNodeCluster -count=1 -timeout 10m
```

Expected: PASS, with the sender receiving `SendAckPacket` and the recipient receiving `RecvPacket` followed by a successful `RecvackPacket` write.

- [ ] **Step 5: Commit the first scenario slice**

Run:

```bash
git add test/e2e/e2e_test.go test/e2e/suite/binary.go test/e2e/suite/runtime.go test/e2e/suite/process.go test/e2e/suite/readiness.go test/e2e/suite/wkproto_client.go
git commit -m "test: add single-node wkproto e2e"
```

### Task 5: Final verification and docs sync

**Files:**
- Modify: `test/e2e/README.md` if run/debug commands or failure guidance drifted
- Modify: `AGENTS.md` if the implemented directory layout differs from the plan

- [ ] **Step 1: Reconcile docs with the final file names and commands**

Make sure `README.md` and `AGENTS.md` match the actual implementation:

- final `go test -tags=e2e ./test/e2e/... -count=1` command
- exact directory names under `test/e2e`
- rule that the layer stays black-box

- [ ] **Step 2: Run the default unit suite to prove the new layer stays opt-in**

Run:

```bash
go test ./... -count=1
```

Expected: PASS, and the new `test/e2e` layer remains excluded because every file
under it is tagged with `//go:build e2e`.

- [ ] **Step 3: Run the dedicated e2e suite end-to-end**

Run:

```bash
go test -tags=e2e ./test/e2e/... -count=1 -timeout 10m
```

Expected: PASS.

- [ ] **Step 4: Confirm the tree is clean and only planned files changed**

Run:

```bash
git status --short
```

Expected: empty output if no post-verification edits were needed; otherwise only
the expected doc-sync files remain before the final commit.

- [ ] **Step 5: Commit any final doc-sync or follow-up fixes**

Run:

```bash
git add AGENTS.md test/e2e/README.md test/e2e
git commit -m "docs: finalize e2e test guidance"
```

If `git status --short` is already empty after step 4, record that no final
doc-sync commit was needed and leave the worktree clean.

## Verification Notes

Before marking the implementation done, confirm these points explicitly:

- `go test ./... -count=1` passes without pulling `test/e2e` into the default run
- `go test -tags=e2e ./test/e2e/... -count=1 -timeout 10m` passes
- the first scenario starts the real `cmd/wukongim` child process rather than any internal bootstrap path
- the first scenario never imports `internal/app` or reads internal DB/runtime state
- `AGENTS.md` reflects the new top-level `test/e2e` directory
