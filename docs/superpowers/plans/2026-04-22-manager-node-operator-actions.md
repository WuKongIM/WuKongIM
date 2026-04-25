# Manager Node Operator Actions Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `POST /manager/nodes/:node_id/draining` and `POST /manager/nodes/:node_id/resume` so the backend manager can change node operator state through JWT-protected manager APIs and immediately receive the latest node detail.

**Architecture:** Keep the HTTP write endpoints in `internal/access/manager` and the write orchestration in `internal/usecase/management`. Reuse the existing strict-read `GetNode` path for both pre-check and post-write reload so the manager API never creates placeholder nodes implicitly and always returns controller-leader-consistent node detail.

**Tech Stack:** Go, `gin`, `testing`, `testify`, `internal/access/manager`, `internal/usecase/management`, `pkg/cluster`, Markdown specs/plans.

---

## References

- Spec: `docs/superpowers/specs/2026-04-22-manager-node-operator-actions-design.md`
- Existing node detail read path: `internal/usecase/management/node_detail.go`
- Existing node DTO helpers: `internal/access/manager/node_detail.go`
- Existing operator primitives: `pkg/cluster/operator.go`
- Follow `@superpowers:test-driven-development` for every code change.
- Run `@superpowers:verification-before-completion` before claiming execution is done.
- Before editing a package, re-check whether it has a `FLOW.md`; if behavior changes, update it.
- Add English comments on new exported methods and DTOs to satisfy `AGENTS.md`.

## File Structure

- Modify: `internal/usecase/management/app.go` — extend the local cluster dependency with node operator methods.
- Create: `internal/usecase/management/node_operator.go` — node write usecases built on `GetNode`.
- Modify: `internal/usecase/management/nodes_test.go` — extend the shared fake cluster reader with operator hooks if that is the least noisy reuse path.
- Create: `internal/usecase/management/node_operator_test.go` — write-path tests for draining/resume pre-check, idempotency, and reload.
- Modify: `internal/access/manager/server.go` — extend the manager dependency interface with node operator methods.
- Modify: `internal/access/manager/routes.go` — add `cluster.node:w` POST routes for draining/resume.
- Create: `internal/access/manager/node_operator.go` — handlers that parse `node_id`, call the usecase, and reuse `NodeDetailDTO`.
- Modify: `internal/access/manager/server_test.go` — HTTP contract tests and management stub support for node operator endpoints.

### Task 1: Add node operator usecases in `internal/usecase/management`

**Files:**
- Modify: `internal/usecase/management/app.go`
- Create: `internal/usecase/management/node_operator.go`
- Modify: `internal/usecase/management/nodes_test.go`
- Create: `internal/usecase/management/node_operator_test.go`

- [ ] **Step 1: Write the failing node operator usecase tests**

Create focused tests that lock the behavior before any production code changes, for example:

```go
func TestMarkNodeDrainingReturnsNotFoundWithoutCallingOperator(t *testing.T) {
    cluster := fakeClusterReader{
        nodes: []controllermeta.ClusterNode{{NodeID: 1, Status: controllermeta.NodeStatusAlive}},
    }
    app := New(Options{LocalNodeID: 1, ControllerPeerIDs: []uint64{1}, Cluster: cluster})

    _, err := app.MarkNodeDraining(context.Background(), 2)

    require.ErrorIs(t, err, controllermeta.ErrNotFound)
    require.Equal(t, uint64(0), cluster.markNodeDrainingCalledWith)
}

func TestMarkNodeDrainingReturnsCurrentDetailWhenAlreadyDraining(t *testing.T) { /* no operator call */ }
func TestMarkNodeDrainingCallsOperatorAndReloadsNode(t *testing.T) { /* pre-read -> operator -> post-read */ }
func TestResumeNodeReturnsCurrentDetailWhenAlreadyAlive(t *testing.T) { /* no operator call */ }
func TestResumeNodeCallsOperatorAndReloadsNode(t *testing.T) { /* pre-read -> operator -> post-read */ }
func TestNodeOperatorsPropagateStrictReadAndOperatorErrors(t *testing.T) { /* ErrNoLeader / ErrNotStarted / context deadline */ }
```

Make the fake cluster reader record:

```go
markNodeDrainingCalledWith uint64
resumeNodeCalledWith       uint64
markNodeDrainingErr        error
resumeNodeErr              error
```

- [ ] **Step 2: Run the focused usecase tests to verify they fail**

Run:

```bash
go test ./internal/usecase/management -run 'Test(MarkNodeDraining|ResumeNode)' -count=1
```

Expected: FAIL because the cluster dependency and node operator methods do not exist yet.

- [ ] **Step 3: Implement the minimal usecase changes**

Extend the local cluster dependency in `internal/usecase/management/app.go`:

```go
MarkNodeDraining(ctx context.Context, nodeID uint64) error
ResumeNode(ctx context.Context, nodeID uint64) error
```

Then implement `internal/usecase/management/node_operator.go` with:

```go
func (a *App) MarkNodeDraining(ctx context.Context, nodeID uint64) (NodeDetail, error)
func (a *App) ResumeNode(ctx context.Context, nodeID uint64) (NodeDetail, error)
```

Rules:
- First call `GetNode(ctx, nodeID)` for strict-read existence validation.
- If `GetNode` returns `controllermeta.ErrNotFound`, return it directly.
- If the current status already matches the target (`draining` or `alive`), return the current detail and do not call the operator.
- Otherwise call `a.cluster.MarkNodeDraining(...)` or `a.cluster.ResumeNode(...)`.
- After a successful operator call, re-run `GetNode(ctx, nodeID)` and return the fresh detail.
- Do not add any new response DTO; reuse `NodeDetail`.

- [ ] **Step 4: Re-run the focused usecase tests to verify they pass**

Run:

```bash
go test ./internal/usecase/management -run 'Test(MarkNodeDraining|ResumeNode)' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit the usecase slice**

```bash
git add internal/usecase/management/app.go internal/usecase/management/node_operator.go internal/usecase/management/nodes_test.go internal/usecase/management/node_operator_test.go
git commit -m "feat: add manager node operator usecases"
```

### Task 2: Add manager HTTP routes and handlers for node operators

**Files:**
- Modify: `internal/access/manager/server.go`
- Modify: `internal/access/manager/routes.go`
- Create: `internal/access/manager/node_operator.go`
- Modify: `internal/access/manager/server_test.go`

- [ ] **Step 1: Write the failing HTTP contract tests**

Extend `internal/access/manager/server_test.go` with at least:

```go
func TestManagerNodeDrainingRejectsMissingToken(t *testing.T) { /* expect 401 */ }
func TestManagerNodeDrainingRejectsInsufficientPermission(t *testing.T) { /* expect 403 for cluster.node:w */ }
func TestManagerNodeDrainingRejectsInvalidNodeID(t *testing.T) { /* expect 400 */ }
func TestManagerNodeDrainingReturnsNotFound(t *testing.T) { /* expect 404 */ }
func TestManagerNodeDrainingReturnsServiceUnavailableWhenLeaderUnavailable(t *testing.T) { /* expect 503 */ }
func TestManagerNodeDrainingReturnsUpdatedNodeDetail(t *testing.T) { /* expect NodeDetailDTO */ }

func TestManagerNodeResumeRejectsMissingToken(t *testing.T) { /* expect 401 */ }
func TestManagerNodeResumeRejectsInsufficientPermission(t *testing.T) { /* expect 403 for cluster.node:w */ }
func TestManagerNodeResumeRejectsInvalidNodeID(t *testing.T) { /* expect 400 */ }
func TestManagerNodeResumeReturnsNotFound(t *testing.T) { /* expect 404 */ }
func TestManagerNodeResumeReturnsServiceUnavailableWhenLeaderUnavailable(t *testing.T) { /* expect 503 */ }
func TestManagerNodeResumeReturnsUpdatedNodeDetail(t *testing.T) { /* expect NodeDetailDTO */ }
```

Extend the manager stub with:

```go
nodeDraining    managementusecase.NodeDetail
nodeDrainingErr error
nodeResume      managementusecase.NodeDetail
nodeResumeErr   error
```

and corresponding methods:

```go
MarkNodeDraining(context.Context, uint64) (managementusecase.NodeDetail, error)
ResumeNode(context.Context, uint64) (managementusecase.NodeDetail, error)
```

- [ ] **Step 2: Run the focused HTTP tests to verify they fail**

Run:

```bash
go test ./internal/access/manager -run 'TestManagerNode(Draining|Resume)' -count=1
```

Expected: FAIL because the management interface, routes, and handlers do not exist yet.

- [ ] **Step 3: Implement the minimal HTTP write path**

Update `internal/access/manager/server.go`:

```go
MarkNodeDraining(ctx context.Context, nodeID uint64) (managementusecase.NodeDetail, error)
ResumeNode(ctx context.Context, nodeID uint64) (managementusecase.NodeDetail, error)
```

Update `internal/access/manager/routes.go` with a separate write-permission group:

```go
nodeWrites := s.engine.Group("/manager")
if s.auth.enabled() {
    nodeWrites.Use(s.requirePermission("cluster.node", "w"))
}
nodeWrites.POST("/nodes/:node_id/draining", s.handleNodeDraining)
nodeWrites.POST("/nodes/:node_id/resume", s.handleNodeResume)
```

Implement `internal/access/manager/node_operator.go`:
- parse `node_id` with the existing `parseNodeIDParam`
- call `s.management.MarkNodeDraining(...)` or `s.management.ResumeNode(...)`
- map `controllermeta.ErrNotFound` to `404`
- map leader/operator unavailable errors to `503`
- reuse `nodeDetailDTO(...)` for the success body

Prefer a small helper to keep the two handlers parallel, for example:

```go
func writeNodeDetail(c *gin.Context, item managementusecase.NodeDetail)
func controllerLeaderUnavailable(err error) bool
```

- [ ] **Step 4: Re-run the focused HTTP tests to verify they pass**

Run:

```bash
go test ./internal/access/manager -run 'TestManagerNode(Draining|Resume)' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit the access slice**

```bash
git add internal/access/manager/server.go internal/access/manager/routes.go internal/access/manager/node_operator.go internal/access/manager/server_test.go
git commit -m "feat: add manager node operator endpoints"
```

### Task 3: Run cross-package verification on the finished slice

**Files:**
- No source changes expected unless verification exposes a real issue.

- [ ] **Step 1: Run the full focused manager verification**

Run:

```bash
go test ./internal/usecase/management ./internal/access/manager -count=1
```

Expected: PASS.

- [ ] **Step 2: Run the manager + app regression suite**

Run:

```bash
go test -p 1 ./internal/usecase/management ./internal/access/manager ./internal/app -count=1
```

Expected: PASS.

- [ ] **Step 3: If verification exposes failures, fix only the proven issue and re-run the same commands**

Do not bundle unrelated cleanup. Keep fixes scoped to what the failing output proves.

- [ ] **Step 4: Commit any verification-driven fix if one was required**

```bash
git add <exact files>
git commit -m "fix: address manager node operator verification issue"
```
