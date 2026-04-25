# Manager Slot Leader Transfer Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `POST /manager/slots/:slot_id/leader/transfer` so the backend manager can transfer one slot leader through a JWT-protected manager API and immediately receive the latest slot detail.

**Architecture:** Keep the HTTP write endpoint in `internal/access/manager` and the orchestration in `internal/usecase/management`. Reuse the existing strict-read `GetSlot` path for both pre-check and post-write reload so every node returns controller-leader-consistent slot detail, while target-node validation stays in the usecase layer instead of leaking raw cluster errors to HTTP.

**Tech Stack:** Go, `gin`, `testing`, `testify`, `internal/access/manager`, `internal/usecase/management`, `pkg/cluster`, Markdown specs/plans.

---

## References

- Spec: `docs/superpowers/specs/2026-04-22-manager-slot-leader-transfer-design.md`
- Existing slot detail read path: `internal/usecase/management/slot_detail.go`
- Existing slot DTO helpers: `internal/access/manager/slots.go`
- Existing slot operator primitive: `pkg/cluster/operator.go`
- Existing node operator pattern: `internal/usecase/management/node_operator.go`, `internal/access/manager/node_operator.go`
- Follow `@superpowers:test-driven-development` for every code change.
- Run `@superpowers:verification-before-completion` before claiming execution is done.
- Before editing a package, re-check whether it has a `FLOW.md`; if behavior changes, update it.
- Add English comments on new exported methods and DTOs to satisfy `AGENTS.md`.

## File Structure

- Modify: `internal/usecase/management/app.go` — extend the local cluster dependency with slot leader transfer support.
- Create: `internal/usecase/management/slot_operator.go` — slot write usecase built on `GetSlot`.
- Modify: `internal/usecase/management/slots_test.go` — extend the shared fake cluster reader with transfer hooks if reuse keeps tests smaller.
- Create: `internal/usecase/management/slot_operator_test.go` — write-path tests for pre-check, target validation, idempotency, and reload.
- Modify: `internal/access/manager/server.go` — extend the manager dependency interface with the slot transfer method.
- Modify: `internal/access/manager/routes.go` — add `cluster.slot:w` POST route for leader transfer.
- Create: `internal/access/manager/slot_operator.go` — handler that parses `slot_id` and JSON body, then reuses `SlotDetailDTO`.
- Modify: `internal/access/manager/server_test.go` — HTTP contract tests and management stub support for slot transfer.

### Task 1: Add slot leader transfer usecase in `internal/usecase/management`

**Files:**
- Modify: `internal/usecase/management/app.go`
- Create: `internal/usecase/management/slot_operator.go`
- Modify: `internal/usecase/management/slots_test.go`
- Create: `internal/usecase/management/slot_operator_test.go`

- [ ] **Step 1: Write the failing usecase tests**

Create focused tests before production code changes, for example:

```go
func TestTransferSlotLeaderReturnsNotFoundWithoutCallingOperator(t *testing.T) {
    cluster := fakeClusterReader{}
    app := New(Options{LocalNodeID: 1, ControllerPeerIDs: []uint64{1}, Cluster: cluster})

    _, err := app.TransferSlotLeader(context.Background(), 2, 3)

    require.ErrorIs(t, err, controllermeta.ErrNotFound)
    require.Zero(t, cluster.transferSlotLeaderCalls)
}

func TestTransferSlotLeaderRejectsTargetOutsideDesiredPeers(t *testing.T) { /* want ErrTargetNodeNotAssigned */ }
func TestTransferSlotLeaderReturnsCurrentDetailWhenLeaderAlreadyMatches(t *testing.T) { /* no operator call */ }
func TestTransferSlotLeaderCallsOperatorAndReloadsDetail(t *testing.T) { /* pre-read -> operator -> post-read */ }
func TestTransferSlotLeaderPropagatesStrictReadAndOperatorErrors(t *testing.T) { /* context deadline / ErrNoLeader */ }
```

Extend the fake cluster reader with:

```go
transferSlotLeaderCalls   int
transferSlotLeaderSlotID  uint32
transferSlotLeaderNodeID  multiraft.NodeID
transferSlotLeaderErr     error
```

- [ ] **Step 2: Run the focused usecase tests to verify they fail**

Run:

```bash
go test ./internal/usecase/management -run 'TestTransferSlotLeader' -count=1
```

Expected: FAIL because the cluster dependency and usecase method do not exist yet.

- [ ] **Step 3: Implement the minimal usecase changes**

Extend the local cluster dependency in `internal/usecase/management/app.go`:

```go
TransferSlotLeader(ctx context.Context, slotID uint32, nodeID multiraft.NodeID) error
```

Then implement `internal/usecase/management/slot_operator.go` with:

```go
var ErrTargetNodeNotAssigned = errors.New("management: target node is not assigned to slot")

func (a *App) TransferSlotLeader(ctx context.Context, slotID uint32, targetNodeID uint64) (SlotDetail, error)
```

Rules:
- First call `GetSlot(ctx, slotID)` for strict-read existence validation.
- If the slot does not exist, return `controllermeta.ErrNotFound` directly.
- Validate that `targetNodeID` is included in `detail.Assignment.DesiredPeers`; otherwise return `ErrTargetNodeNotAssigned`.
- If `detail.Runtime.LeaderID == targetNodeID`, return the current detail and do not call the operator.
- Otherwise call `a.cluster.TransferSlotLeader(ctx, slotID, multiraft.NodeID(targetNodeID))`.
- After a successful operator call, re-run `GetSlot(ctx, slotID)` and return the fresh detail.

- [ ] **Step 4: Re-run the focused usecase tests to verify they pass**

Run:

```bash
go test ./internal/usecase/management -run 'TestTransferSlotLeader' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit the usecase slice**

```bash
git add internal/usecase/management/app.go internal/usecase/management/slot_operator.go internal/usecase/management/slots_test.go internal/usecase/management/slot_operator_test.go
git commit -m "feat: add manager slot transfer usecase"
```

### Task 2: Add manager HTTP route and handler for slot leader transfer

**Files:**
- Modify: `internal/access/manager/server.go`
- Modify: `internal/access/manager/routes.go`
- Create: `internal/access/manager/slot_operator.go`
- Modify: `internal/access/manager/server_test.go`

- [ ] **Step 1: Write the failing HTTP contract tests**

Add focused handler tests such as:

```go
func TestManagerSlotLeaderTransferRejectsMissingToken(t *testing.T) { /* expect 401 */ }
func TestManagerSlotLeaderTransferRejectsInsufficientPermission(t *testing.T) { /* expect 403 for cluster.slot:w */ }
func TestManagerSlotLeaderTransferRejectsInvalidSlotID(t *testing.T) { /* expect 400 */ }
func TestManagerSlotLeaderTransferRejectsInvalidBody(t *testing.T) { /* expect 400 */ }
func TestManagerSlotLeaderTransferRejectsInvalidTargetNodeID(t *testing.T) { /* expect 400 */ }
func TestManagerSlotLeaderTransferReturnsNotFound(t *testing.T) { /* expect 404 */ }
func TestManagerSlotLeaderTransferRejectsUnassignedTarget(t *testing.T) { /* expect 400 */ }
func TestManagerSlotLeaderTransferReturnsServiceUnavailableWhenLeaderUnavailable(t *testing.T) { /* expect 503 */ }
func TestManagerSlotLeaderTransferReturnsUpdatedSlotDetail(t *testing.T) { /* expect SlotDetailDTO */ }
```

Extend the manager stub with:

```go
slotLeaderTransfer    managementusecase.SlotDetail
slotLeaderTransferErr error
```

and method:

```go
TransferSlotLeader(context.Context, uint32, uint64) (managementusecase.SlotDetail, error)
```

- [ ] **Step 2: Run the focused HTTP tests to verify they fail**

Run:

```bash
go test ./internal/access/manager -run 'TestManagerSlotLeaderTransfer' -count=1
```

Expected: FAIL because the management interface, route, and handler do not exist yet.

- [ ] **Step 3: Implement the minimal HTTP write path**

Update `internal/access/manager/server.go`:

```go
TransferSlotLeader(ctx context.Context, slotID uint32, targetNodeID uint64) (managementusecase.SlotDetail, error)
```

Update `internal/access/manager/routes.go` with a separate write-permission group:

```go
slotWrites := s.engine.Group("/manager")
if s.auth.enabled() {
    slotWrites.Use(s.requirePermission("cluster.slot", "w"))
}
slotWrites.POST("/slots/:slot_id/leader/transfer", s.handleSlotLeaderTransfer)
```

Implement `internal/access/manager/slot_operator.go`:
- parse `slot_id` with the existing `parseSlotIDParam`
- parse a JSON body containing `target_node_id`
- call `s.management.TransferSlotLeader(...)`
- map `controllermeta.ErrNotFound` to `404`
- map `managementusecase.ErrTargetNodeNotAssigned` to `400`
- map strict-read/operator unavailable errors to `503`
- reuse `slotDetailDTO(...)` for the success body

- [ ] **Step 4: Re-run the focused HTTP tests to verify they pass**

Run:

```bash
go test ./internal/access/manager -run 'TestManagerSlotLeaderTransfer' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit the access slice**

```bash
git add internal/access/manager/server.go internal/access/manager/routes.go internal/access/manager/slot_operator.go internal/access/manager/server_test.go
git commit -m "feat: add manager slot transfer endpoint"
```

### Task 3: Run focused verification on the finished slice

**Files:**
- No source changes expected unless verification exposes a real issue.

- [ ] **Step 1: Run the full focused manager verification**

Run:

```bash
go test ./internal/usecase/management ./internal/access/manager -count=1
```

Expected: PASS.

- [ ] **Step 2: Run targeted app coverage for manager wiring**

Run:

```bash
go test ./internal/app -run 'Test(NewBuildsOptionalManagerServerWhenConfigured|StartStartsManagerAfterAPIWhenEnabled|StartRollsBackAPIAndClusterWhenManagerStartFails|StopStopsManagerBeforeAPIGatewayAndClusterClose)' -count=1
```

Expected: PASS.

- [ ] **Step 3: If verification exposes failures, fix only the proven issue and re-run the same commands**

Do not bundle unrelated cleanup. Keep fixes scoped to what the failing output proves.
