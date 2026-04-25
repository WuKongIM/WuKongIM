# Manager Slots Strict Read Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a leader-consistent `GET /manager/slots` endpoint and tighten existing manager cluster reads so every node returns the same controller-leader view or fails with `503`.

**Architecture:** Extend `pkg/cluster.API` with explicit strict-read methods that never fall back to local metadata, then switch `internal/usecase/management` to depend on those strict methods for both node and slot aggregation. Keep HTTP-specific response shaping in `internal/access/manager`, including route-level permissions and `503 service_unavailable` mapping when the controller leader view is unavailable.

**Tech Stack:** Go, `gin`, `testing`, `testify`, `pkg/cluster`, `internal/usecase/management`, `internal/access/manager`, Markdown specs/plans.

---

## References

- Spec: `docs/superpowers/specs/2026-04-21-manager-api-foundation-design.md`
- Follow `@superpowers:test-driven-development` for every code change.
- Run `@superpowers:verification-before-completion` before claiming execution is done.
- Before editing a package, re-check whether it has a `FLOW.md`; if behavior changes, update it.
- Add English comments on new exported methods, DTOs, and non-obvious helpers to satisfy `AGENTS.md`.

## File Structure

- Modify: `pkg/cluster/api.go` — expose strict leader-consistent read methods for manager-facing cluster queries.
- Modify: `pkg/cluster/cluster.go` — implement strict read methods without local fallback.
- Modify: `pkg/cluster/cluster_test.go` — strict read behavior tests.
- Modify: `pkg/cluster/FLOW.md` — document strict manager read semantics.
- Modify: `internal/usecase/management/app.go` — narrow dependency to strict cluster reader methods.
- Modify: `internal/usecase/management/nodes.go` — switch node aggregation to strict reads.
- Create: `internal/usecase/management/slots.go` — slot DTOs and aggregation logic.
- Create: `internal/usecase/management/slots_test.go` — slot aggregation tests.
- Modify: `internal/access/manager/routes.go` — route-level permission wiring for `/manager/nodes` and `/manager/slots`.
- Modify: `internal/access/manager/server.go` — expand management dependency interface if needed.
- Modify: `internal/access/manager/nodes.go` — map strict-read unavailable errors to `503`.
- Create: `internal/access/manager/slots.go` — `/manager/slots` response DTOs and handler.
- Modify: `internal/access/manager/server_test.go` — HTTP contract tests for `/manager/slots` and `503` behavior.
- Modify: `docs/superpowers/specs/2026-04-21-manager-api-foundation-design.md` — keep examples and consistency rule aligned with implementation.

### Task 1: Add explicit strict read methods in `pkg/cluster`

**Files:**
- Modify: `pkg/cluster/api.go`
- Modify: `pkg/cluster/cluster.go`
- Modify: `pkg/cluster/cluster_test.go`
- Modify: `pkg/cluster/FLOW.md`

- [ ] **Step 1: Write the failing strict-read tests**

Add tests in `pkg/cluster/cluster_test.go` that prove the new methods never fall back to local metadata. Cover at least:

```go
func TestListNodesStrictReturnsLeaderDataWithoutLocalFallback(t *testing.T) { /* strict method returns controller client result */ }
func TestListNodesStrictReturnsErrorWhenLeaderReadUnavailable(t *testing.T) { /* strict method returns ErrNotLeader/ErrNoLeader/timeout path instead of local data */ }
func TestListObservedRuntimeViewsStrictReturnsLeaderSnapshotWithoutFallback(t *testing.T) { /* runtime views strict path */ }
func TestListSlotAssignmentsStrictReturnsLeaderAssignmentsWithoutFallback(t *testing.T) { /* assignments strict path */ }
```

Use a fake controller client and a different local `controllerMeta` payload so the test would clearly fail if fallback still happens.

- [ ] **Step 2: Run the focused cluster strict-read tests to verify they fail**

Run:

```bash
go test ./pkg/cluster -run 'TestList(Nodes|ObservedRuntimeViews|SlotAssignments)Strict' -count=1
```

Expected: FAIL because the strict methods do not exist yet.

- [ ] **Step 3: Implement the minimal strict cluster methods**

Add methods like these to `pkg/cluster.API` and `*Cluster`:

```go
ListNodesStrict(ctx context.Context) ([]controllermeta.ClusterNode, error)
ListSlotAssignmentsStrict(ctx context.Context) ([]controllermeta.SlotAssignment, error)
ListObservedRuntimeViewsStrict(ctx context.Context) ([]controllermeta.SlotRuntimeView, error)
```

Rules:
- If `controllerClient` is available, use it and return its result directly.
- Do not fall back to `controllerMeta` on `ErrNotLeader`, `ErrNoLeader`, or timeout.
- If no controller client is available at all, return `ErrNotStarted`.
- Keep existing non-strict methods unchanged for non-manager callers.

- [ ] **Step 4: Re-run the focused cluster strict-read tests to verify they pass**

Run:

```bash
go test ./pkg/cluster -run 'TestList(Nodes|ObservedRuntimeViews|SlotAssignments)Strict' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit the strict-read slice**

```bash
git add pkg/cluster/api.go pkg/cluster/cluster.go pkg/cluster/cluster_test.go pkg/cluster/FLOW.md
git commit -m "feat: add strict manager cluster reads"
```

### Task 2: Switch management usecases to strict reads and add slot aggregation

**Files:**
- Modify: `internal/usecase/management/app.go`
- Modify: `internal/usecase/management/nodes.go`
- Modify: `internal/usecase/management/nodes_test.go`
- Create: `internal/usecase/management/slots.go`
- Create: `internal/usecase/management/slots_test.go`

- [ ] **Step 1: Write the failing management usecase tests**

Add tests that lock both the new slot DTO contract and the strict-read dependency shape. Cover at least:

```go
func TestListNodesUsesStrictClusterReads(t *testing.T) { /* strict method stubs called, old methods absent */ }
func TestListSlotsAggregatesAssignmentRuntimeAndDerivedState(t *testing.T) { /* matched / ready */ }
func TestListSlotsMarksPeerMismatchEpochLagAndUnknownStates(t *testing.T) { /* peer_mismatch / epoch_lag / unreported / lost */ }
func TestListSlotsSortsBySlotID(t *testing.T) { /* stable ordering */ }
```

Define a fake cluster reader with only strict methods:

```go
type fakeClusterReader struct {
    nodes       []controllermeta.ClusterNode
    assignments []controllermeta.SlotAssignment
    views       []controllermeta.SlotRuntimeView
}
```

- [ ] **Step 2: Run the focused management tests to verify they fail**

Run:

```bash
go test ./internal/usecase/management -run 'TestList(NodesUsesStrictClusterReads|Slots)' -count=1
```

Expected: FAIL because the usecase still depends on the old methods and slot aggregation does not exist.

- [ ] **Step 3: Implement the minimal management changes**

Update the dependency interface in `internal/usecase/management/app.go` to use the strict methods, then implement `slots.go` with DTOs similar to:

```go
type Slot struct {
    SlotID      uint32
    State       SlotState
    Assignment  SlotAssignmentView
    Runtime     SlotRuntimeView
}
```

Derived state rules:
- `quorum=ready|lost|unknown`
- `sync=matched|peer_mismatch|epoch_lag|unreported`

Switch `ListNodes` to read through `ListNodesStrict` and `ListObservedRuntimeViewsStrict`.

- [ ] **Step 4: Re-run the focused management tests to verify they pass**

Run:

```bash
go test ./internal/usecase/management -run 'TestList(NodesUsesStrictClusterReads|Slots)' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit the management slice**

```bash
git add internal/usecase/management/app.go internal/usecase/management/nodes.go internal/usecase/management/nodes_test.go internal/usecase/management/slots.go internal/usecase/management/slots_test.go
git commit -m "feat: add manager slot aggregation usecase"
```

### Task 3: Add `/manager/slots` and map strict-read failures to `503`

**Files:**
- Modify: `internal/access/manager/routes.go`
- Modify: `internal/access/manager/server.go`
- Modify: `internal/access/manager/nodes.go`
- Create: `internal/access/manager/slots.go`
- Modify: `internal/access/manager/server_test.go`

- [ ] **Step 1: Write the failing HTTP tests**

Extend `internal/access/manager/server_test.go` with at least:

```go
func TestManagerNodesReturnsServiceUnavailableWhenStrictLeaderReadUnavailable(t *testing.T) { /* expect 503 */ }
func TestManagerSlotsRejectsMissingToken(t *testing.T) { /* expect 401 */ }
func TestManagerSlotsRejectsInsufficientPermission(t *testing.T) { /* expect 403 for cluster.slot:r */ }
func TestManagerSlotsReturnsAggregatedList(t *testing.T) { /* expect total + items + state/assignment/runtime */ }
func TestManagerSlotsReturnsServiceUnavailableWhenStrictLeaderReadUnavailable(t *testing.T) { /* expect 503 */ }
```

Use a stub management dependency that can return `ErrNotLeader`, `ErrNoLeader`, or `context.DeadlineExceeded`.

- [ ] **Step 2: Run the focused manager HTTP tests to verify they fail**

Run:

```bash
go test ./internal/access/manager -run 'TestManager(Nodes|Slots)' -count=1
```

Expected: FAIL because `/manager/slots` and `503` mapping do not exist yet.

- [ ] **Step 3: Implement the minimal HTTP changes**

Add route-specific permission wiring:

```go
nodes := s.engine.Group("/manager")
nodes.Use(s.requirePermission("cluster.node", "r"))
nodes.GET("/nodes", s.handleNodes)

slots := s.engine.Group("/manager")
slots.Use(s.requirePermission("cluster.slot", "r"))
slots.GET("/slots", s.handleSlots)
```

Then implement:
- `handleSlots`
- slot response DTOs
- `isLeaderConsistentReadUnavailable(err)` helper
- `503` mapping in both `handleNodes` and `handleSlots`

Use the existing error body shape:

```json
{"error":"service_unavailable","message":"controller leader consistent read unavailable"}
```

- [ ] **Step 4: Re-run the focused manager HTTP tests to verify they pass**

Run:

```bash
go test ./internal/access/manager -run 'TestManager(Nodes|Slots)' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit the manager HTTP slice**

```bash
git add internal/access/manager/routes.go internal/access/manager/server.go internal/access/manager/nodes.go internal/access/manager/slots.go internal/access/manager/server_test.go
git commit -m "feat: add manager slots endpoint"
```

### Task 4: Run focused verification and align docs

**Files:**
- Modify: `docs/superpowers/specs/2026-04-21-manager-api-foundation-design.md` (if implementation details shifted)

- [ ] **Step 1: Run the focused end-to-end verification suite**

Run:

```bash
go test ./cmd/wukongim ./internal/app ./internal/usecase/management ./internal/access/manager ./pkg/cluster -run 'Test(LoadConfig.*Manager|Config.*Manager|Manager(Nodes|Slots|Login)|List(Nodes|Slots)|NewBuildsOptionalManagerServerWhenConfigured|StartStartsManagerAfterAPIWhenEnabled|StartRollsBackAPIAndClusterWhenManagerStartFails|StopStopsManagerBeforeAPIGatewayAndClusterClose|AccessorsExposeBuiltRuntime|List(Nodes|ObservedRuntimeViews|SlotAssignments)Strict)' -count=1
```

Expected: PASS.

- [ ] **Step 2: Run a repo search for the new API contract and strict read methods**

Run:

```bash
rg -n 'cluster\.slot|/manager/slots|ListNodesStrict|ListSlotAssignmentsStrict|ListObservedRuntimeViewsStrict|service_unavailable' cmd internal pkg docs -S
```

Expected: the new route, permissions, strict-read methods, and spec text all appear in the right places.

- [ ] **Step 3: Commit any final doc alignment**

```bash
git add docs/superpowers/specs/2026-04-21-manager-api-foundation-design.md
git commit -m "docs: align manager slots strict read design"
```
