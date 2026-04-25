# Manager Overview Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a JWT-protected `GET /manager/overview` endpoint that returns controller-leader-authoritative node, slot, and task summary cards plus capped slot/task anomaly samples for the manager homepage.

**Architecture:** Keep the overview read path on the existing controller-leader strict-read boundary. Add one new overview aggregation usecase in `internal/usecase/management`, reuse the existing slot/task derivation helpers for status mapping, and expose the result through a dedicated manager HTTP handler protected by a new `cluster.overview:r` permission. Avoid any new bottom-layer cluster or channel-runtime-meta reads; this endpoint should only aggregate `nodes / slots / tasks` in one pass and fail closed on any strict-read error.

**Tech Stack:** Go, `gin`, `testing`, `testify`, `internal/usecase/management`, `internal/access/manager`, `internal/app`, `pkg/cluster`.

---

## References

- Spec: `docs/superpowers/specs/2026-04-21-manager-overview-design.md`
- Foundation spec: `docs/superpowers/specs/2026-04-21-manager-api-foundation-design.md`
- Follow `@superpowers:test-driven-development` for every code change.
- Run `@superpowers:verification-before-completion` before claiming execution is done.
- Before editing a package, re-check whether it has a `FLOW.md`; update it only if implementation changes the documented flow.
- `internal/usecase/management`, `internal/access/manager`, and `internal/app` currently have no `FLOW.md`.
- Add English comments on new exported DTOs, response bodies, and non-obvious helpers to satisfy `AGENTS.md`.

## File Structure

- Modify: `internal/usecase/management/app.go` — add a deterministic clock hook for overview aggregation tests if needed.
- Create: `internal/usecase/management/overview.go` — overview DTOs, anomaly DTOs, and aggregation logic.
- Create: `internal/usecase/management/overview_test.go` — overview aggregation tests.
- Modify: `internal/usecase/management/nodes_test.go` — extend the shared fake cluster reader with strict-read error injection reused by overview tests.
- Modify: `internal/access/manager/server.go` — expand the HTTP dependency interface with the overview usecase.
- Modify: `internal/access/manager/routes.go` — register `GET /manager/overview` under the new `cluster.overview:r` permission.
- Create: `internal/access/manager/overview.go` — overview response DTOs and handler.
- Modify: `internal/access/manager/server_test.go` — add overview auth/contract tests and extend `managementStub`.
- Modify: `internal/app/build.go` — no logic change expected; touch only if interface growth requires compile-time alignment.
- Modify: `docs/superpowers/specs/2026-04-21-manager-overview-design.md` — only if implementation reveals spec drift.

### Task 1: Add the management overview aggregation usecase

**Files:**
- Modify: `internal/usecase/management/app.go`
- Create: `internal/usecase/management/overview.go`
- Create: `internal/usecase/management/overview_test.go`
- Modify: `internal/usecase/management/nodes_test.go`

- [ ] **Step 1: Write the failing overview usecase tests**

Add focused tests that lock the approved homepage contract, for example:

```go
func TestGetOverviewAggregatesCountsAndAnomalies(t *testing.T) {
    now := time.Unix(1713736200, 0).UTC()
    app := New(Options{
        Cluster: fakeClusterReader{
            controllerLeaderID: 1,
            slotIDs: []multiraft.SlotID{1, 2, 3, 4},
            nodes: []controllermeta.ClusterNode{
                {NodeID: 1, Status: controllermeta.NodeStatusAlive},
                {NodeID: 2, Status: controllermeta.NodeStatusDraining},
            },
            assignments: []controllermeta.SlotAssignment{
                {SlotID: 1, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 8},
                {SlotID: 2, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 8},
                {SlotID: 3, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 8},
            },
            views: []controllermeta.SlotRuntimeView{
                {SlotID: 1, CurrentPeers: []uint64{1, 2, 3}, LeaderID: 1, HasQuorum: true, ObservedConfigEpoch: 8},
                {SlotID: 2, CurrentPeers: []uint64{1, 2, 3}, LeaderID: 0, HasQuorum: false, ObservedConfigEpoch: 8},
                {SlotID: 3, CurrentPeers: []uint64{1, 2}, LeaderID: 2, HasQuorum: true, ObservedConfigEpoch: 8},
            },
            tasks: []controllermeta.ReconcileTask{
                {SlotID: 3, Status: controllermeta.TaskStatusRetrying},
                {SlotID: 4, Status: controllermeta.TaskStatusFailed},
            },
        },
        Now: func() time.Time { return now },
    })

    got, err := app.GetOverview(context.Background())
    require.NoError(t, err)
    require.Equal(t, now, got.GeneratedAt)
    require.Equal(t, 4, got.Slots.Total)
    require.Equal(t, 1, got.Slots.QuorumLost)
    require.Equal(t, 1, got.Slots.LeaderMissing)
    require.Equal(t, 1, got.Slots.Unreported)
    require.Equal(t, 1, got.Anomalies.Tasks.Failed.Count)
}
```

Also add tests for:
- `slot_ids` driving `slots.total`, even when assignment/runtime data is missing,
- `sync_mismatch.count` combining `peer_mismatch + epoch_lag` while preserving each item’s real `sync` value,
- per-group anomaly items sorted by `slot_id` and capped at `5`,
- strict-read errors (`ListNodesStrict`, `ListSlotAssignmentsStrict`, `ListObservedRuntimeViewsStrict`, `ListTasksStrict`) propagating immediately.

- [ ] **Step 2: Run the focused overview usecase tests to verify they fail**

Run:

```bash
go test ./internal/usecase/management -run 'TestGetOverview' -count=1
```

Expected: FAIL because the overview DTOs and aggregation method do not exist yet.

- [ ] **Step 3: Implement the minimal overview usecase**

Add overview DTOs and aggregation helpers, for example:

```go
type Overview struct {
    GeneratedAt time.Time
    Cluster     OverviewCluster
    Nodes       OverviewNodes
    Slots       OverviewSlots
    Tasks       OverviewTasks
    Anomalies   OverviewAnomalies
}

func (a *App) GetOverview(ctx context.Context) (Overview, error)
```

Implementation rules:
- If deterministic time is needed for tests, add `Now func() time.Time` to `management.Options` and store a `now func() time.Time` on `App`, defaulting to `time.Now`.
- Call `ControllerLeaderID()`, `SlotIDs()`, `ListNodesStrict(ctx)`, `ListSlotAssignmentsStrict(ctx)`, `ListObservedRuntimeViewsStrict(ctx)`, and `ListTasksStrict(ctx)` once each.
- Reuse existing helpers where possible: `slotFromAssignmentView`, `slotFromRuntimeView`, `slotWithoutObservation`, and `managerTask`.
- Treat `cluster.SlotIDs()` as the physical-slot universe for `slots.total`; for slot IDs missing both assignment and runtime data, still emit a synthetic slot summary so `unreported` and totals remain stable.
- Count slot stats independently: `ready`, `quorum_lost`, `leader_missing`, `unreported`, `peer_mismatch`, `epoch_lag`.
- Build only the approved anomaly groups:
  - slots: `quorum_lost`, `leader_missing`, `sync_mismatch`
  - tasks: `failed`, `retrying`
- Sort anomaly items by `slot_id ASC` and keep at most `5` samples per group while preserving the full `count`.
- Add English comments on all new exported DTOs and anomaly summary structs.

- [ ] **Step 4: Re-run the focused overview usecase tests to verify they pass**

Run:

```bash
go test ./internal/usecase/management -run 'TestGetOverview' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit the usecase slice**

```bash
git add internal/usecase/management/app.go internal/usecase/management/overview.go internal/usecase/management/overview_test.go internal/usecase/management/nodes_test.go
git commit -m "feat: add manager overview usecase"
```

### Task 2: Add the manager overview HTTP endpoint

**Files:**
- Modify: `internal/access/manager/server.go`
- Modify: `internal/access/manager/routes.go`
- Create: `internal/access/manager/overview.go`
- Modify: `internal/access/manager/server_test.go`

- [ ] **Step 1: Write the failing manager overview HTTP tests**

Add focused route tests that follow the existing manager server pattern, for example:

```go
func TestManagerOverviewRejectsMissingToken(t *testing.T) { /* expect 401 */ }
func TestManagerOverviewRejectsInsufficientPermission(t *testing.T) { /* expect 403 */ }
func TestManagerOverviewReturnsServiceUnavailableWhenLeaderConsistentReadUnavailable(t *testing.T) { /* expect 503 */ }
func TestManagerOverviewReturnsAggregatedObject(t *testing.T) { /* exact JSON body */ }
```

Use `managementStub` in `internal/access/manager/server_test.go` and extend it with one overview method plus one overview payload field. The success test should assert:
- `generated_at`, `controller_leader_id`, `nodes`, `slots`, `tasks`, and `anomalies` appear in the response,
- the response is a single object, not a `total/items` wrapper,
- no pagination fields are present.

- [ ] **Step 2: Run the focused overview HTTP tests to verify they fail**

Run:

```bash
go test ./internal/access/manager -run 'TestManagerOverview' -count=1
```

Expected: FAIL because the route, dependency method, and handler do not exist yet.

- [ ] **Step 3: Implement the minimal overview HTTP endpoint**

Add the new access-layer handler and route:

```go
overview := s.engine.Group("/manager")
if s.auth.enabled() {
    overview.Use(s.requirePermission("cluster.overview", "r"))
}
overview.GET("/overview", s.handleOverview)
```

Handler rules:
- Do not accept query parameters; just call `s.management.GetOverview(ctx)`.
- Reuse `leaderConsistentReadUnavailable(err)` and map such failures to `503 service_unavailable` with the same controller-leader-unavailable message style used by nodes/slots/tasks.
- Return a single JSON object with access-layer DTOs mirroring the approved spec shape.
- Keep response DTOs local to `internal/access/manager/overview.go` rather than exposing usecase structs directly as JSON.
- Add English comments on the new exported response structs.

- [ ] **Step 4: Re-run the focused overview HTTP tests to verify they pass**

Run:

```bash
go test ./internal/access/manager -run 'TestManagerOverview' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit the HTTP slice**

```bash
git add internal/access/manager/server.go internal/access/manager/routes.go internal/access/manager/overview.go internal/access/manager/server_test.go
git commit -m "feat: add manager overview endpoint"
```

### Task 3: Verify app wiring and guard against spec drift

**Files:**
- Modify: `internal/app/build.go` (only if compile-time alignment is needed)
- Modify: `docs/superpowers/specs/2026-04-21-manager-overview-design.md` (only if implementation reveals drift)

- [ ] **Step 1: Confirm manager app wiring still compiles cleanly**

The current `internal/app/build.go` manager wiring should keep working because `managementusecase.New(...)` already constructs one `*management.App`, and `accessmanager.Options{Management: app.managementApp}` should satisfy the expanded interface after Task 2. Do not edit `build.go` unless compilation proves it is necessary.

- [ ] **Step 2: Run focused verification**

Run:

```bash
go test ./internal/usecase/management -run 'TestGetOverview' -count=1
go test ./internal/access/manager -run 'TestManagerOverview' -count=1
go test -p 1 ./internal/usecase/management ./internal/access/manager ./internal/app -count=1
```

Expected: PASS.

- [ ] **Step 3: Review the overview spec for drift**

Only update `docs/superpowers/specs/2026-04-21-manager-overview-design.md` if the final implementation differs from the approved route, permission, response shape, or error semantics.

- [ ] **Step 4: Commit the verification slice if any files changed**

If `internal/app/build.go` or the overview spec changed, commit them; otherwise skip this commit and record that verification passed with no wiring diff.

```bash
git add internal/app/build.go docs/superpowers/specs/2026-04-21-manager-overview-design.md
git commit -m "chore: align manager overview wiring"
```

## Verification Checklist

- `go test ./internal/usecase/management -run 'TestGetOverview' -count=1`
- `go test ./internal/access/manager -run 'TestManagerOverview' -count=1`
- `go test -p 1 ./internal/usecase/management ./internal/access/manager ./internal/app -count=1`
- Optional broader sanity check if needed: `go test ./internal/... -count=1`

## Expected Outcome

After these tasks, the repo should expose a new manager homepage endpoint:

- `GET /manager/overview`

with controller-leader-authoritative `nodes / slots / tasks` summary cards, capped `slots / tasks` anomaly samples, `cluster.overview:r` permission enforcement, and fail-closed error behavior consistent with the rest of the manager cluster read API.
