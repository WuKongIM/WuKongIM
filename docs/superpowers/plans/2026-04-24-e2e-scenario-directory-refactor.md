# E2E Scenario Directory Refactor Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Refactor `test/e2e` from one growing top-level test file into a domain-first, scenario-directory layout where future black-box e2e cases are easy to add, easy to find, and still share one stable `test/e2e/suite` harness.

**Architecture:** Move binary-path resolution behind `test/e2e/suite` so scenario subpackages can call `suite.New(t)` without a root `TestMain`. Migrate the existing message scenarios into `test/e2e/message/<scenario>/` packages, add root/domain/scenario `README.md` files as the discoverability layer, and then remove `test/e2e/e2e_test.go` so the root directory becomes documentation plus shared harness only.

**Tech Stack:** Go 1.23, `testing`, `testify/require`, `os/exec`, `pkg/protocol/frame`, existing `test/e2e/suite` helpers, `//go:build e2e`, Markdown docs.

---

## References

- Spec: `docs/superpowers/specs/2026-04-24-e2e-scenario-directory-architecture-design.md`
- Existing e2e foundation plan: `docs/superpowers/plans/2026-04-23-process-e2e-foundation.md`
- Existing three-node e2e plan: `docs/superpowers/plans/2026-04-23-three-node-process-e2e.md`
- Layer rules: `test/e2e/AGENTS.md`
- Current suite entrypoints: `test/e2e/suite/binary.go`, `test/e2e/suite/runtime.go`, `test/e2e/suite/manager_client.go`
- Current central scenarios: `test/e2e/e2e_test.go`
- Follow `@superpowers:test-driven-development` for each behavior-bearing slice.
- Run `@superpowers:verification-before-completion` before claiming execution is done.
- In Codex CLI, only use subagents if the user explicitly asks for delegation; otherwise execute this plan locally with `@superpowers:executing-plans`.
- Re-check `FLOW.md` before editing each touched package; as of plan writing, there is no `FLOW.md` under `test/e2e` or `test/e2e/suite`.

## Critical Review Notes Before Starting

- Keep `test/e2e` process-level black-box. Do not import `internal/app` or add gray-box shortcuts while migrating scenarios.
- Preserve the current no-fixed-sleep rule. Scenario packages must continue to rely on `suite` readiness probes and manager polling.
- Do not move scenario-specific business assertions into `test/e2e/suite`; `suite` stays for shared harness only.
- Scenario directories must use `snake_case`, and every scenario directory must contain both a `README.md` and one primary `*_test.go` file.
- Root `test/e2e/README.md` must become the suite catalog; per-domain and per-scenario READMEs must stay short and path-oriented.
- The worktree already contains unrelated changes in `internal/` and existing e2e helpers. Do not revert them; build on top of them carefully.
- Every new Go file under `test/e2e` must keep `//go:build e2e`.
- Add English comments for new important structs, fields, and non-obvious helpers to satisfy repository rules.

## File Structure

- Modify: `test/e2e/AGENTS.md` - document the domain/scenario layout rules alongside the existing black-box guidance.
- Modify: `test/e2e/README.md` - rewrite as the global entrypoint and scenario catalog.
- Delete: `test/e2e/e2e_test.go` - remove the central scenario file after all migrated packages are green.
- Modify: `test/e2e/suite/binary.go` - hide binary build/cache resolution behind package-private helpers.
- Modify: `test/e2e/suite/binary_test.go` - lock the package-private cache semantics needed by `suite.New(t)`.
- Modify: `test/e2e/suite/runtime.go` - change `suite.New` to resolve its own binary path and keep workspace assembly package-independent.
- Modify: `test/e2e/suite/runtime_test.go` - add focused tests for the new `suite.New(t)` contract.
- Create: `test/e2e/message/README.md` - document the message domain and list each migrated scenario.
- Create: `test/e2e/message/single_node_send_message/README.md` - describe the single-node-cluster send flow and direct run command.
- Create: `test/e2e/message/single_node_send_message/single_node_send_message_test.go` - own the migrated single-node send scenario.
- Create: `test/e2e/message/cross_node_closure/README.md` - describe the three-node cross-node closure scenario.
- Create: `test/e2e/message/cross_node_closure/cross_node_closure_test.go` - own the migrated three-node cross-node closure scenario.
- Create: `test/e2e/message/slot_leader_failover/README.md` - describe the leader failover scenario and diagnostics.
- Create: `test/e2e/message/slot_leader_failover/slot_leader_failover_test.go` - own the migrated failover scenario.

### Task 1: Make `suite.New(t)` package-independent

**Files:**
- Modify: `test/e2e/suite/binary.go`
- Modify: `test/e2e/suite/binary_test.go`
- Modify: `test/e2e/suite/runtime.go`
- Modify: `test/e2e/suite/runtime_test.go`

- [ ] **Step 1: Write the failing suite-construction tests**

Add focused tests that lock the new subpackage-safe contract:

- `TestNewResolvesBinaryPathWithoutCallerSuppliedPath`
- `TestResolveBinaryPathBuildsOncePerProcess`
- `TestSuiteConstructorPreservesWorkspaceLayout`

Use an injectable builder instead of running a real `go build` in the unit test. A representative assertion shape is:

```go
func TestResolveBinaryPathBuildsOncePerProcess(t *testing.T) {
    t.Setenv("WK_E2E_BINARY", "")

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

Also add one constructor test that proves the suite can be created with only `t`:

```go
func TestNewResolvesBinaryPathWithoutCallerSuppliedPath(t *testing.T) {
    fakeBinary := filepath.Join(t.TempDir(), "wukongim-e2e")
    require.NoError(t, os.WriteFile(fakeBinary, []byte("fake"), 0o755))
    t.Setenv("WK_E2E_BINARY", fakeBinary)

    s := New(t)
    require.Equal(t, fakeBinary, s.binaryPath)
    require.DirExists(t, s.workspace.RootDir)
}
```

- [ ] **Step 2: Run the focused suite tests and confirm they fail**

Run:

```bash
go test -tags=e2e ./test/e2e/suite -run 'Test(NewResolvesBinaryPathWithoutCallerSuppliedPath|ResolveBinaryPathBuildsOncePerProcess|SuiteConstructorPreservesWorkspaceLayout)$' -count=1
```

Expected: FAIL because `suite.New` still requires `binaryPath string`, and the shared binary resolver does not exist yet.

- [ ] **Step 3: Implement the minimal package-independent constructor path**

Make the smallest focused changes needed for scenario subpackages:

- keep `BinaryCache` but add a package-private default cache for scenario packages
- teach `suite.New(t)` to resolve a binary path internally instead of accepting one from callers
- optionally support `WK_E2E_BINARY` as a test-only override so focused unit tests can avoid real builds
- keep all binary-build details hidden inside `test/e2e/suite`
- preserve the existing workspace temp-dir behavior

Target constructor shape:

```go
func New(t *testing.T) *Suite {
    t.Helper()

    return &Suite{
        t:          t,
        binaryPath: mustBinaryPath(t),
        workspace:  NewWorkspace(t),
    }
}
```

- [ ] **Step 4: Re-run the focused suite tests and confirm they pass**

Run:

```bash
go test -tags=e2e ./test/e2e/suite -run 'Test(NewResolvesBinaryPathWithoutCallerSuppliedPath|ResolveBinaryPathBuildsOncePerProcess|SuiteConstructorPreservesWorkspaceLayout)$' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit the constructor refactor slice**

Run:

```bash
git add test/e2e/suite/binary.go test/e2e/suite/binary_test.go test/e2e/suite/runtime.go test/e2e/suite/runtime_test.go
git commit -m "test: decouple e2e suite from root TestMain"
```

### Task 2: Establish the domain-first catalog and migrate the single-node message scenario

**Files:**
- Modify: `test/e2e/AGENTS.md`
- Modify: `test/e2e/README.md`
- Create: `test/e2e/message/README.md`
- Create: `test/e2e/message/single_node_send_message/README.md`
- Create: `test/e2e/message/single_node_send_message/single_node_send_message_test.go`

- [ ] **Step 1: Define the new directory contract with a failing package/doc check**

Before adding files, lock the intended structure with one focused scenario package path and one documentation lookup.

The new scenario test should contain the migrated single-node closure from `test/e2e/e2e_test.go`, for example:

```go
func TestSingleNodeSendMessage(t *testing.T) {
    s := suite.New(t)
    node := s.StartSingleNodeCluster()

    sender, err := suite.NewWKProtoClient()
    require.NoError(t, err)
    defer func() { _ = sender.Close() }()

    recipient, err := suite.NewWKProtoClient()
    require.NoError(t, err)
    defer func() { _ = recipient.Close() }()

    require.NoError(t, sender.Connect(node.GatewayAddr(), "u1", "u1-device"))
    require.NoError(t, recipient.Connect(node.GatewayAddr(), "u2", "u2-device"))

    // send -> sendack -> recv -> recvack assertions
}
```

- [ ] **Step 2: Run the focused scenario/doc checks and confirm they fail**

Run:

```bash
go test -tags=e2e ./test/e2e/message/single_node_send_message -count=1
rg -n 'single_node_send_message|test/e2e/message' test/e2e/README.md test/e2e/AGENTS.md
```

Expected:

- `go test ...` FAIL because the scenario package does not exist yet.
- `rg ...` returns no catalog entry because the new layout is not documented yet.

- [ ] **Step 3: Implement the message-domain skeleton and migrate the single-node scenario**

Create the initial domain-first structure:

- update `test/e2e/AGENTS.md` with a short rule that root contains `suite/` plus domain directories, and domain directories contain scenario subdirectories
- rewrite `test/e2e/README.md` into the global catalog with a table containing domain, scenario path, purpose, and single-package run command
- add `test/e2e/message/README.md` with the message-domain scenario list
- add the `single_node_send_message` scenario directory with `README.md` and the migrated test file
- switch the migrated test to `suite.New(t)` with no local `TestMain`

Keep the scenario README short and concrete, for example:

```md
# single_node_send_message

## Purpose

Prove one fresh `单节点集群` can complete a real WKProto send -> sendack -> recv -> recvack closure.

## Run

go test -tags=e2e ./test/e2e/message/single_node_send_message -count=1
```

- [ ] **Step 4: Re-run the focused scenario/doc checks and confirm they pass**

Run:

```bash
go test -tags=e2e ./test/e2e/message/single_node_send_message -count=1
rg -n 'single_node_send_message|test/e2e/message' test/e2e/README.md test/e2e/AGENTS.md test/e2e/message/README.md
```

Expected: PASS, and the README files now expose the new message-domain layout.

- [ ] **Step 5: Commit the first migrated scenario slice**

Run:

```bash
git add test/e2e/AGENTS.md test/e2e/README.md test/e2e/message/README.md test/e2e/message/single_node_send_message/README.md test/e2e/message/single_node_send_message/single_node_send_message_test.go
git commit -m "test: add message e2e scenario catalog"
```

### Task 3: Migrate the three-node cross-node closure scenario

**Files:**
- Modify: `test/e2e/README.md`
- Modify: `test/e2e/message/README.md`
- Create: `test/e2e/message/cross_node_closure/README.md`
- Create: `test/e2e/message/cross_node_closure/cross_node_closure_test.go`

- [ ] **Step 1: Capture the three-node scenario as its own package-first test**

Move the existing cross-node closure body into a dedicated package under `test/e2e/message/cross_node_closure`.

Keep the test self-contained inside the scenario package. Do not move the message-specific send/recv assertions into `suite`; if a tiny helper improves readability, keep it local to this scenario package.

Representative test body:

```go
func TestCrossNodeClosure(t *testing.T) {
    s := suite.New(t)
    cluster := s.StartThreeNodeCluster()

    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()

    require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())

    topology, err := cluster.ResolveSlotTopology(ctx, 1)
    require.NoError(t, err, cluster.DumpDiagnostics())
    require.Len(t, topology.FollowerNodeIDs, 2)

    // connect sender and recipient to two follower nodes
    // assert sendack and recv closure over real WKProto
}
```

- [ ] **Step 2: Run the direct package check and confirm it fails before creation**

Run:

```bash
go test -tags=e2e ./test/e2e/message/cross_node_closure -count=1
```

Expected: FAIL because the scenario package does not exist yet.

- [ ] **Step 3: Implement the cross-node scenario package and catalog entry**

Create:

- `test/e2e/message/cross_node_closure/README.md`
- `test/e2e/message/cross_node_closure/cross_node_closure_test.go`

Update:

- `test/e2e/message/README.md` with the new scenario
- `test/e2e/README.md` with the root-level catalog row and run command

Document the key external steps in the scenario README:

- wait for three-node cluster readiness
- resolve slot 1 topology via `/manager/slots/1`
- connect two users to different follower nodes
- assert `Send -> SendAck -> Recv -> RecvAck`

- [ ] **Step 4: Re-run the direct package check and confirm it passes**

Run:

```bash
go test -tags=e2e ./test/e2e/message/cross_node_closure -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit the cross-node scenario slice**

Run:

```bash
git add test/e2e/README.md test/e2e/message/README.md test/e2e/message/cross_node_closure/README.md test/e2e/message/cross_node_closure/cross_node_closure_test.go
git commit -m "test: migrate cross-node message e2e"
```

### Task 4: Migrate the slot-leader failover scenario and remove the central scenario file

**Files:**
- Modify: `test/e2e/README.md`
- Modify: `test/e2e/message/README.md`
- Create: `test/e2e/message/slot_leader_failover/README.md`
- Create: `test/e2e/message/slot_leader_failover/slot_leader_failover_test.go`
- Delete: `test/e2e/e2e_test.go`

- [ ] **Step 1: Capture the failover scenario in its final package location**

Move the existing failover scenario out of `test/e2e/e2e_test.go` into `test/e2e/message/slot_leader_failover/slot_leader_failover_test.go`.

Keep the scenario package self-contained:

- start a three-node cluster
- resolve the current slot leader
- connect `u1` and `u2` on follower nodes
- stop the current leader process
- wait for `WaitForSlotLeaderChange`
- re-confirm the users are still externally observable via `/manager/connections`
- assert another cross-node send/recv closure after failover

If a small helper is needed to keep the test readable, keep it local to `slot_leader_failover_test.go` or `helpers_test.go` in the same scenario directory.

- [ ] **Step 2: Run the direct package check and confirm it fails before creation**

Run:

```bash
go test -tags=e2e ./test/e2e/message/slot_leader_failover -count=1
```

Expected: FAIL because the scenario package does not exist yet.

- [ ] **Step 3: Implement the failover scenario package, update docs, and delete the root scenario file**

Create:

- `test/e2e/message/slot_leader_failover/README.md`
- `test/e2e/message/slot_leader_failover/slot_leader_failover_test.go`

Update:

- `test/e2e/message/README.md`
- `test/e2e/README.md`

Delete:

- `test/e2e/e2e_test.go`

The scenario README should call out the main failure diagnostics explicitly:

- node-scoped stdout/stderr
- node `logs/app.log` and `logs/error.log` tails
- last observed manager slot body
- manager connection observations before and after failover

- [ ] **Step 4: Re-run direct packages plus the full e2e inventory and confirm they pass**

Run:

```bash
go test -tags=e2e ./test/e2e/message/single_node_send_message -count=1
go test -tags=e2e ./test/e2e/message/cross_node_closure -count=1
go test -tags=e2e ./test/e2e/message/slot_leader_failover -count=1
go test -tags=e2e ./test/e2e/... -count=1
```

Expected: PASS. The root `test/e2e` tree should now run through the scenario subpackages without depending on a root `TestMain`.

- [ ] **Step 5: Commit the final migration slice**

Run:

```bash
git add test/e2e/README.md test/e2e/message/README.md test/e2e/message/slot_leader_failover/README.md test/e2e/message/slot_leader_failover/slot_leader_failover_test.go test/e2e/e2e_test.go
git commit -m "test: migrate failover e2e scenarios"
```

### Task 5: Final verification and follow-up hygiene

**Files:**
- Review only: `test/e2e/README.md`
- Review only: `test/e2e/AGENTS.md`
- Review only: `test/e2e/message/README.md`
- Review only: `test/e2e/message/*/README.md`

- [ ] **Step 1: Re-read the catalog docs for drift or missing paths**

Check that every documented scenario path actually exists and every scenario directory appears in both the domain README and root README.

Use a simple verification pass:

```bash
find test/e2e/message -maxdepth 2 -type f | sort
```

- [ ] **Step 2: Run the focused suite tests that protect the constructor refactor**

Run:

```bash
go test -tags=e2e ./test/e2e/suite -run 'Test(NewResolvesBinaryPathWithoutCallerSuppliedPath|ResolveBinaryPathBuildsOncePerProcess|SuiteConstructorPreservesWorkspaceLayout)$' -count=1
```

Expected: PASS.

- [ ] **Step 3: Run the final full e2e pass one more time from a clean shell command**

Run:

```bash
go test -tags=e2e ./test/e2e/... -count=1
```

Expected: PASS.

- [ ] **Step 4: Capture the final status for handoff**

Record in the final execution note:

- which scenario packages now exist
- that `suite.New(t)` no longer needs `TestMain`
- which verification commands passed
- whether the spec and plan docs should be committed separately or together with the code changes

- [ ] **Step 5: Commit any final doc-only touch-ups if needed**

Run only if README wording changed during final verification:

```bash
git add test/e2e/README.md test/e2e/AGENTS.md test/e2e/message/README.md test/e2e/message/*/README.md
git commit -m "docs: polish e2e scenario catalog"
```
