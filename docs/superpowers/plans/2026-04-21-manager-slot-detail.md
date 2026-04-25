# Manager Slot Detail Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a leader-consistent `GET /manager/slots/:slot_id` endpoint that returns one slot detail plus an optional current reconcile-task summary.

**Architecture:** Reuse the existing strict manager reads in `internal/usecase/management`: slot detail comes from strict assignment/runtime reads, and the optional task summary comes from `GetReconcileTaskStrict` with `ErrNotFound` treated as `task=null`. Keep path parsing, permission checks, and `400/404/503` HTTP mapping inside `internal/access/manager`.

**Tech Stack:** Go, `gin`, `testing`, `testify`, `pkg/controller/meta`, `internal/usecase/management`, `internal/access/manager`, Markdown specs/plans.

---

## References

- Spec: `docs/superpowers/specs/2026-04-21-manager-api-foundation-design.md`
- Follow `@superpowers:test-driven-development` for every code change.
- Run `@superpowers:verification-before-completion` before claiming execution is done.
- Before editing a package, re-check whether it has a `FLOW.md`; if behavior changes, update it.
- Add English comments on new exported methods, DTOs, and non-obvious helpers to satisfy `AGENTS.md`.

## File Structure

- Modify: `internal/usecase/management/slots.go` — factor reusable slot-detail aggregation helpers when needed.
- Create: `internal/usecase/management/slot_detail.go` — slot detail DTOs and slot-detail aggregation.
- Create: `internal/usecase/management/slot_detail_test.go` — slot detail aggregation tests.
- Modify: `internal/access/manager/server.go` — expand the management dependency interface with slot detail reads.
- Modify: `internal/access/manager/routes.go` — add `/manager/slots/:slot_id` with existing `cluster.slot:r` permission wiring.
- Modify: `internal/access/manager/slots.go` — slot detail response DTOs and handlers in addition to list support.
- Modify: `internal/access/manager/server_test.go` — HTTP contract tests for slot detail, `400`, `404`, nullable `task`, and `503` behavior.
- Modify: `docs/superpowers/specs/2026-04-21-manager-api-foundation-design.md` — keep the slot-detail contract aligned with implementation.

### Task 1: Add manager slot-detail aggregation

**Files:**
- Modify: `internal/usecase/management/slots.go`
- Create: `internal/usecase/management/slot_detail.go`
- Create: `internal/usecase/management/slot_detail_test.go`
- Modify: `internal/usecase/management/nodes_test.go` (reuse or extend fake strict reader only if needed)

- [ ] **Step 1: Write the failing slot-detail usecase tests**

Add tests that lock the slot-detail DTO contract. Cover at least:

```go
func TestGetSlotReturnsDetailWithTaskSummary(t *testing.T) { /* slot body + task summary */ }
func TestGetSlotReturnsDetailWithNilTaskWhenTaskNotFound(t *testing.T) { /* GetReconcileTaskStrict ErrNotFound => task=nil */ }
func TestGetSlotReturnsRuntimeOnlyDetailWhenAssignmentMissing(t *testing.T) { /* runtime view still makes slot resolvable */ }
func TestGetSlotReturnsNotFoundWhenAssignmentAndRuntimeMissing(t *testing.T) { /* slot detail 404 source */ }
```

Use the existing fake strict reader pattern. Keep the task summary optional and do not require a new cluster API method.

- [ ] **Step 2: Run the focused slot-detail usecase tests to verify they fail**

Run:

```bash
go test ./internal/usecase/management -run 'TestGetSlot' -count=1
```

Expected: FAIL because the slot detail usecase does not exist yet.

- [ ] **Step 3: Implement the minimal slot-detail usecase**

Add DTOs similar to:

```go
type SlotDetail struct {
    Slot
    Task *Task
}
```

Rules:
- Reuse the existing slot state derivation helpers from `slots.go`.
- Read assignments and runtime views with strict methods.
- If either assignment or runtime exists for the `slot_id`, treat the slot as found.
- If `GetReconcileTaskStrict` returns `controllermeta.ErrNotFound`, set `Task=nil` and still return the slot detail.
- If neither assignment nor runtime exists, return `controllermeta.ErrNotFound`.

- [ ] **Step 4: Re-run the focused slot-detail usecase tests to verify they pass**

Run:

```bash
go test ./internal/usecase/management -run 'TestGetSlot' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit the usecase slice**

```bash
git add internal/usecase/management/slots.go internal/usecase/management/slot_detail.go internal/usecase/management/slot_detail_test.go internal/usecase/management/nodes_test.go
git commit -m "feat: add manager slot detail usecase"
```

### Task 2: Add `GET /manager/slots/:slot_id`

**Files:**
- Modify: `internal/access/manager/server.go`
- Modify: `internal/access/manager/routes.go`
- Modify: `internal/access/manager/slots.go`
- Modify: `internal/access/manager/server_test.go`

- [ ] **Step 1: Write the failing HTTP slot-detail tests**

Extend `internal/access/manager/server_test.go` with at least:

```go
func TestManagerSlotDetailRejectsMissingToken(t *testing.T) { /* expect 401 */ }
func TestManagerSlotDetailRejectsInvalidSlotID(t *testing.T) { /* expect 400 */ }
func TestManagerSlotDetailRejectsInsufficientPermission(t *testing.T) { /* expect 403 for cluster.slot:r */ }
func TestManagerSlotDetailReturnsDetailWithTaskSummary(t *testing.T) { /* expect slot + task */ }
func TestManagerSlotDetailReturnsDetailWithNullTask(t *testing.T) { /* expect task: null */ }
func TestManagerSlotDetailReturnsNotFound(t *testing.T) { /* expect 404 */ }
func TestManagerSlotDetailReturnsServiceUnavailableWhenLeaderConsistentReadUnavailable(t *testing.T) { /* expect 503 */ }
```

- [ ] **Step 2: Run the focused manager slot-detail tests to verify they fail**

Run:

```bash
go test ./internal/access/manager -run 'TestManagerSlotDetail' -count=1
```

Expected: FAIL because the route and handler do not exist yet.

- [ ] **Step 3: Implement the minimal HTTP slot-detail endpoint**

Update the management dependency interface in `internal/access/manager/server.go`:

```go
GetSlot(ctx context.Context, slotID uint32) (managementusecase.SlotDetail, error)
```

Add route wiring in `internal/access/manager/routes.go` under the existing `cluster.slot:r` group:

```go
slots.GET("/slots/:slot_id", s.handleSlot)
```

Then implement in `internal/access/manager/slots.go`:
- `handleSlot`
- slot detail response DTOs
- nested nullable `task` summary DTO (no repeated `slot_id` inside the nested object)
- `400` mapping for invalid `slot_id`
- `404` mapping for `controllermeta.ErrNotFound`
- `503` mapping for leader-consistent read unavailability

- [ ] **Step 4: Re-run the focused manager slot-detail tests to verify they pass**

Run:

```bash
go test ./internal/access/manager -run 'TestManagerSlotDetail' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit the manager HTTP slice**

```bash
git add internal/access/manager/server.go internal/access/manager/routes.go internal/access/manager/slots.go internal/access/manager/server_test.go
git commit -m "feat: add manager slot detail endpoint"
```

### Task 3: Run focused verification and align docs

**Files:**
- Modify: `docs/superpowers/specs/2026-04-21-manager-api-foundation-design.md` (only if implementation details drift)

- [ ] **Step 1: Run the focused manager verification suite**

Run:

```bash
go test ./cmd/wukongim ./internal/app ./internal/usecase/management ./internal/access/manager ./pkg/cluster -run 'Test(LoadConfig.*Manager|Config.*Manager|Manager(Login|Nodes|Slots|SlotDetail|Tasks|TaskDetail)|List(Nodes|Slots|Tasks)|Get(Task|Slot)|NewBuildsOptionalManagerServerWhenConfigured|StartStartsManagerAfterAPIWhenEnabled|StartRollsBackAPIAndClusterWhenManagerStartFails|StopStopsManagerBeforeAPIGatewayAndClusterClose|AccessorsExposeBuiltRuntime|List(Nodes|ObservedRuntimeViews|SlotAssignments|Tasks)Strict|GetReconcileTaskStrict)' -count=1
```

Expected: PASS.

- [ ] **Step 2: Run a grep-based contract check**

Run:

```bash
rg -n '/manager/slots/:slot_id|GetSlot\(|cluster\.slot|task": null|invalid slot_id|slot not found' cmd internal pkg docs -S
```

Expected: the new route, DTO, error semantics, and slot permission references appear in the intended files.

- [ ] **Step 3: Commit any remaining spec alignment**

If Task 3 required doc-only changes beyond the usecase / HTTP commits:

```bash
git add docs/superpowers/specs/2026-04-21-manager-api-foundation-design.md
git commit -m "docs: align manager slot detail contract"
```

If no further files changed, skip this commit.
