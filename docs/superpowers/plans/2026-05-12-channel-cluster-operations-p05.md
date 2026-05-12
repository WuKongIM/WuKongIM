# Channel Cluster Operations P0.5 Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add safe manager channel-cluster operations: truthful replica/status detail and policy-driven leader repair.

**Architecture:** Keep manager HTTP as an adapter and route all business behavior through `internal/usecase/management`. Add narrow usecase ports for runtime status and safe leader repair, wire concrete implementations only from `internal/app`, and keep the frontend as a thin API consumer. Do not expose explicit target leader transfer or infer follower lag without proof.

**Tech Stack:** Go 1.23, Gin manager API, existing `internal/runtime/channelmeta` leader repairer, React 19, TypeScript, React Router DOM 7, react-intl, Vitest, Bun/Vite.

---

## References

- Design spec: `docs/superpowers/specs/2026-05-12-channel-cluster-operations-p05-design.md`
- Existing P0 usecase: `internal/usecase/management/channel_cluster.go`
- Existing channel meta DTOs: `internal/usecase/management/channel_runtime_meta.go`
- Existing manager handlers: `internal/access/manager/channel_cluster.go`, `internal/access/manager/channel_runtime_meta.go`, `internal/access/manager/slot_operator.go`
- Existing app wiring: `internal/app/build.go`
- Existing leader repair runtime: `internal/runtime/channelmeta/repair.go`, `internal/runtime/channelmeta/repair_types.go`, `internal/runtime/channelmeta/interfaces.go`
- Existing node repair RPCs: `internal/access/node/channel_leader_repair_rpc.go`, `internal/access/node/channel_leader_evaluate_rpc.go`
- Existing frontend P0 page: `web/src/pages/channel-cluster/unhealthy/page.tsx`
- Follow `@superpowers:test-driven-development` for every implementation slice.
- Run `@superpowers:verification-before-completion` before claiming completion.
- Re-check `FLOW.md` before editing packages. At plan time relevant files existed at `pkg/channel/FLOW.md`, `pkg/channel/replica/FLOW.md`, and `internal/runtime/channelmeta/FLOW.md`.

## File Structure

Backend:

- Create: `internal/usecase/management/channel_cluster_operations.go` — replica detail DTOs, repair DTOs, operation ports, and usecase methods.
- Create: `internal/usecase/management/channel_cluster_operations_test.go` — usecase tests for detail and repair behavior.
- Modify: `internal/usecase/management/app.go` — add operation ports to `Options` and `App`.
- Modify: `internal/access/manager/server.go` — extend `Management` interface with two operations.
- Modify: `internal/access/manager/routes.go` — register read and write channel-cluster operation routes.
- Create: `internal/access/manager/channel_cluster_operations.go` — HTTP DTOs, handlers, conversion, and error mapping.
- Modify or create tests under `internal/access/manager/` — endpoint JSON, permissions, and error mapping tests.
- Create: `internal/app/manager_channel_cluster_operations.go` — app-level adapters for runtime status and leader repair.
- Modify: `internal/app/build.go` — wire adapters into `managementusecase.Options`.
- Modify or create `internal/app/*test.go` — wiring/adapter tests.

Frontend:

- Modify: `web/src/lib/manager-api.types.ts` — add replica detail and repair DTOs.
- Modify: `web/src/lib/manager-api.ts` — add API wrappers.
- Modify: `web/src/lib/manager-api.test.ts` — URL/body/error tests.
- Modify: `web/src/pages/channel-cluster/unhealthy/page.test.tsx` — inspect detail and repair behavior tests.
- Modify: `web/src/pages/channel-cluster/unhealthy/page.tsx` — detail panel and repair action.
- Modify: `web/src/i18n/messages/en.ts` — English copy.
- Modify: `web/src/i18n/messages/zh-CN.ts` — Chinese copy.

Docs:

- Modify: `docs/raw/web-admin-restructure.md` — mark P0.5 safe repair/detail done and keep explicit transfer pending.
- Modify: `web/README.md` — update page/API matrix.

## Task 1: Add Management Usecase Operation Ports And DTOs

**Files:**
- Create: `internal/usecase/management/channel_cluster_operations.go`
- Create: `internal/usecase/management/channel_cluster_operations_test.go`
- Modify: `internal/usecase/management/app.go`

- [ ] **Step 1: Re-read package flow docs**

Run:

```bash
cat internal/runtime/channelmeta/FLOW.md
cat pkg/channel/FLOW.md
cat pkg/channel/replica/FLOW.md
```

Expected: understand repair and runtime status semantics before editing.

- [ ] **Step 2: Write a failing replica detail test**

In `internal/usecase/management/channel_cluster_operations_test.go`, add `TestGetChannelClusterReplicaDetailReportsOnlyProvenRuntimeStatus`.

Use fakes:

- `fakeChannelRuntimeMetaReader` with one meta:
  - channel ID `room-1`, type `2`, slot `9`, leader `1`, replicas `[1,2,3]`, ISR `[1,2]`, MinISR `2`, status active, max seq through fake message reader `42`.
- fake `ChannelReplicaStatusReader` returning `channel.ChannelRuntimeStatus{Leader: 1, HW: 42, CommittedSeq: 42, MinAvailableSeq: 1, RetentionThroughSeq: 0}`.

Assert:

- `Channel.HashSlot` is populated from fake cluster.
- `RuntimeReported == true`.
- `CommitSeq`, `MinAvailableSeq`, and `RetentionThroughSeq` are non-nil and match runtime status.
- replica row for node `1` is `Reported=true`, `IsLeader=true`, `Role="leader"`, `CommitSeq=42`, `Lag=0`.
- replica rows for nodes `2` and `3` are `Reported=false`, role `follower`, unknown numeric pointers nil.

- [ ] **Step 3: Run the usecase test to verify RED**

Run:

```bash
GOWORK=off go test ./internal/usecase/management -run TestGetChannelClusterReplicaDetailReportsOnlyProvenRuntimeStatus -count=1
```

Expected: FAIL because DTOs and method do not exist.

- [ ] **Step 4: Add ports to `app.go`**

In `internal/usecase/management/app.go`, add documented interfaces:

```go
type ChannelReplicaStatusReader interface {
    // ChannelRuntimeStatus returns the best proven runtime status for one channel.
    ChannelRuntimeStatus(ctx context.Context, id channel.ChannelID) (channel.ChannelRuntimeStatus, error)
}

type ChannelLeaderRepairOperator interface {
    // RepairChannelLeader repairs or validates authoritative channel leader metadata.
    RepairChannelLeader(ctx context.Context, req RepairChannelClusterLeaderRequest) (RepairChannelClusterLeaderResult, error)
}
```

Add fields to `Options` and `App`:

```go
ChannelReplicaStatus ChannelReplicaStatusReader
ChannelLeaderRepair ChannelLeaderRepairOperator
```

Use names that avoid colliding with existing `ChannelRuntimeMeta`.

- [ ] **Step 5: Add replica detail DTOs and method**

In `channel_cluster_operations.go`, add documented DTOs:

```go
type ChannelClusterReplicaStatus struct {
    // NodeID is the replica node ID.
    NodeID uint64
    // Role is "leader" or "follower" from authoritative metadata.
    Role string
    // IsLeader reports whether this replica is the authoritative leader.
    IsLeader bool
    // InISR reports whether this replica is in the authoritative ISR set.
    InISR bool
    // Reported reports whether live runtime status was proven for this replica.
    Reported bool
    // CommitSeq is the proven committed sequence for this replica, when known.
    CommitSeq *uint64
    // LEO is the proven log end offset for this replica, when known.
    LEO *uint64
    // CheckpointHW is the proven durable checkpoint high watermark, when known.
    CheckpointHW *uint64
    // Lag is leader commit minus replica commit, when both are known.
    Lag *uint64
}

type ChannelClusterReplicaDetail struct {
    // Channel is authoritative manager channel metadata.
    Channel ChannelRuntimeMetaDetail
    // RuntimeReported reports whether a runtime status source proved a live status.
    RuntimeReported bool
    // CommitSeq is the proven leader/local committed sequence, when known.
    CommitSeq *uint64
    // MinAvailableSeq is the proven minimum readable sequence, when known.
    MinAvailableSeq *uint64
    // RetentionThroughSeq is the proven retention boundary, when known.
    RetentionThroughSeq *uint64
    // Replicas contains one row for each authoritative replica.
    Replicas []ChannelClusterReplicaStatus
}
```

Implement:

```go
func (a *App) GetChannelClusterReplicaDetail(ctx context.Context, channelID string, channelType int64) (ChannelClusterReplicaDetail, error)
```

Use `a.GetChannelRuntimeMeta` for authoritative detail. If `a.channelReplicaStatus` is nil, return rows with `RuntimeReported=false` and no error. Treat `channel.ErrNotReady`, `channel.ErrStaleMeta`, and `channel.ErrInvalidConfig` from status reader as unreported status, not as endpoint failure.

- [ ] **Step 6: Re-run replica detail test**

Run:

```bash
GOWORK=off go test ./internal/usecase/management -run TestGetChannelClusterReplicaDetailReportsOnlyProvenRuntimeStatus -count=1
```

Expected: PASS.

## Task 2: Add Safe Leader Repair Usecase

**Files:**
- Modify: `internal/usecase/management/channel_cluster_operations.go`
- Modify: `internal/usecase/management/channel_cluster_operations_test.go`

- [ ] **Step 1: Write failing repair tests**

Add tests:

1. `TestRepairChannelClusterLeaderMapsNoLeaderToSafeRepair`
   - Fake authoritative meta has `Leader=0`, replicas `[1,2]`, ISR `[2]`.
   - Fake repair operator records request and returns changed meta with `Leader=2`, `LeaderEpoch+1`.
   - Assert response `Changed=true`, returned channel leader `2`, and operator received reason `no_leader` or the runtime-compatible mapped reason documented in implementation.

2. `TestRepairChannelClusterLeaderSkipsISRInsufficientOnly`
   - Request reason `isr_insufficient` for a channel that has a leader.
   - Expected: return `metadb.ErrInvalidArgument` or a package error such as `ErrUnsupportedChannelClusterRepairReason`.

3. `TestRepairChannelClusterLeaderReturnsNoSafeCandidate`
   - Fake operator returns `channel.ErrNoSafeChannelLeader`.
   - Assert error is preserved.

- [ ] **Step 2: Run repair tests to verify RED**

Run:

```bash
GOWORK=off go test ./internal/usecase/management -run 'TestRepairChannelClusterLeader' -count=1
```

Expected: FAIL because method/DTOs are missing.

- [ ] **Step 3: Implement repair DTOs and validation**

Add DTOs:

```go
type RepairChannelClusterLeaderRequest struct {
    // ChannelID identifies the channel to repair.
    ChannelID string
    // ChannelType identifies the channel type to repair.
    ChannelType int64
    // Reason is the manager-facing reason that requested repair.
    Reason string
}

type RepairChannelClusterLeaderResult struct {
    // Changed reports whether authoritative metadata changed.
    Changed bool
    // Meta is authoritative metadata after repair or validation.
    Meta metadb.ChannelRuntimeMeta
}

type RepairChannelClusterLeaderResponse struct {
    // Changed reports whether authoritative metadata changed.
    Changed bool
    // Channel is authoritative manager metadata after repair or validation.
    Channel ChannelRuntimeMetaDetail
}
```

Add package error:

```go
var ErrUnsupportedChannelClusterRepairReason = errors.New("management: unsupported channel cluster repair reason")
```

Implement:

```go
func (a *App) RepairChannelClusterLeader(ctx context.Context, req RepairChannelClusterLeaderRequest) (RepairChannelClusterLeaderResponse, error)
```

Rules:

- Validate non-empty `ChannelID` and `ChannelType > 0`.
- Load current detail/meta before repair.
- Allow repair when reason is empty and current `Leader==0`, or reason is `ChannelClusterUnhealthyReasonNoLeader`.
- Reject `isr_insufficient` and `status_not_active` by themselves with `ErrUnsupportedChannelClusterRepairReason`.
- If operator is nil, return `channel.ErrInvalidConfig`.
- Call operator with mapped request.
- Convert returned authoritative meta to `ChannelRuntimeMetaDetail` using same helper path as `GetChannelRuntimeMeta`. If needed, add a small helper to avoid duplicate conversion.

- [ ] **Step 4: Re-run repair tests**

Run:

```bash
GOWORK=off go test ./internal/usecase/management -run 'Test(GetChannelClusterReplicaDetail|RepairChannelClusterLeader)' -count=1
```

Expected: PASS.

- [ ] **Step 5: Run focused management tests**

Run:

```bash
GOWORK=off go test ./internal/usecase/management -run 'Test(GetChannelCluster|ListChannelCluster|RepairChannelCluster|GetChannelClusterReplica|ListChannelRuntimeMeta)' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit usecase operations slice**

Run:

```bash
git add internal/usecase/management/app.go internal/usecase/management/channel_cluster_operations.go internal/usecase/management/channel_cluster_operations_test.go
git commit -m "feat: add channel cluster operation usecases"
```

## Task 3: Expose Manager HTTP Operation Routes

**Files:**
- Modify: `internal/access/manager/server.go`
- Modify: `internal/access/manager/routes.go`
- Create: `internal/access/manager/channel_cluster_operations.go`
- Test: `internal/access/manager/channel_cluster_operations_test.go` or `internal/access/manager/server_test.go`

- [ ] **Step 1: Write failing HTTP tests**

Add tests for:

1. `GET /manager/channel-cluster/2/room-1/replicas`
   - Returns `channel`, `runtime_reported`, `commit_seq`, and per-replica `reported` fields.
2. `POST /manager/channel-cluster/2/room-1/repair` with `{"reason":"no_leader"}`
   - Returns `changed` and updated `channel.leader`.
3. read route requires `cluster.channel` `r` when auth enabled.
4. repair route requires `cluster.channel` `w` when auth enabled.
5. invalid channel type returns 400; not found returns 404; no safe candidate returns 409; leader unavailable returns 503.

Extend `managementStub` with:

```go
channelClusterReplicaDetail managementusecase.ChannelClusterReplicaDetail
channelClusterReplicaDetailErr error
repairChannelClusterLeader managementusecase.RepairChannelClusterLeaderResponse
repairChannelClusterLeaderErr error
```

- [ ] **Step 2: Run HTTP tests to verify RED**

Run:

```bash
GOWORK=off go test ./internal/access/manager -run 'TestManagerChannelCluster(Operation|Replica|Repair)' -count=1
```

Expected: FAIL because routes/handlers are missing.

- [ ] **Step 3: Extend `Management` interface**

In `internal/access/manager/server.go`, add English comments and methods:

```go
GetChannelClusterReplicaDetail(ctx context.Context, channelID string, channelType int64) (managementusecase.ChannelClusterReplicaDetail, error)
RepairChannelClusterLeader(ctx context.Context, req managementusecase.RepairChannelClusterLeaderRequest) (managementusecase.RepairChannelClusterLeaderResponse, error)
```

- [ ] **Step 4: Add handlers and DTOs**

Create `internal/access/manager/channel_cluster_operations.go`.

DTOs should use snake_case JSON:

```go
type ChannelClusterReplicaDetailResponse struct { ... }
type ChannelClusterReplicaStatusDTO struct { ... }
type RepairChannelClusterLeaderRequestDTO struct { Reason string `json:"reason"` }
type RepairChannelClusterLeaderResponseDTO struct { Changed bool `json:"changed"`; Channel ChannelRuntimeMetaDTO/Detail DTO }
```

Pointer numeric fields should encode as `null` when unknown.

Error mapping:

- `metadb.ErrInvalidArgument` -> 400.
- `metadb.ErrNotFound` -> 404.
- `managementusecase.ErrUnsupportedChannelClusterRepairReason` -> 400.
- `channel.ErrNoSafeChannelLeader` -> 409.
- `slotLeaderAuthoritativeReadUnavailable` or `leaderConsistentReadUnavailable` -> 503.
- default -> 500.

- [ ] **Step 5: Register routes**

In `routes.go`, add read route to the existing channel cluster read group or a new group:

```go
channelCluster.GET("/channel-cluster/:channel_type/:channel_id/replicas", s.handleChannelClusterReplicas)
```

Add write group:

```go
channelClusterWrites := s.engine.Group("/manager")
if s.auth.enabled() {
    channelClusterWrites.Use(s.requirePermission("cluster.channel", "w"))
}
channelClusterWrites.POST("/channel-cluster/:channel_type/:channel_id/repair", s.handleChannelClusterRepair)
```

Do not register leader transfer in this plan.

- [ ] **Step 6: Re-run HTTP tests**

Run:

```bash
GOWORK=off go test ./internal/access/manager -run 'TestManagerChannelCluster(Operation|Replica|Repair)' -count=1
```

Expected: PASS.

- [ ] **Step 7: Run backend focused tests**

Run:

```bash
GOWORK=off go test ./internal/usecase/management ./internal/access/manager -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit manager HTTP slice**

Run:

```bash
git add internal/access/manager/server.go internal/access/manager/routes.go internal/access/manager/channel_cluster_operations.go internal/access/manager/*channel_cluster*test.go internal/access/manager/server_test.go
git commit -m "feat: expose channel cluster operation APIs"
```

## Task 4: Wire Operations From `internal/app`

**Files:**
- Create: `internal/app/manager_channel_cluster_operations.go`
- Modify: `internal/app/build.go`
- Test: `internal/app/manager_channel_cluster_operations_test.go` or existing build tests

- [ ] **Step 1: Write failing adapter tests**

Add tests for:

1. status adapter returns `app.channelLog.Status` result as `channel.ChannelRuntimeStatus`.
2. status adapter maps `channel.ErrNotReady` through without wrapping.
3. repair adapter calls existing `runtime/channelmeta.AuthoritativeRepairer`/repairer and returns changed meta.

Use small fakes; do not start a full app unless existing helpers make that simpler.

- [ ] **Step 2: Run app adapter tests to verify RED**

Run:

```bash
GOWORK=off go test ./internal/app -run 'TestManagerChannelCluster(Operation|Status|Repair)' -count=1
```

Expected: FAIL because adapters do not exist.

- [ ] **Step 3: Implement adapters**

Create `internal/app/manager_channel_cluster_operations.go`.

Suggested types:

```go
type managerChannelReplicaStatusReader struct {
    channelLog interface {
        Status(channel.ChannelID) (channel.ChannelRuntimeStatus, error)
    }
}

func (r managerChannelReplicaStatusReader) ChannelRuntimeStatus(ctx context.Context, id channel.ChannelID) (channel.ChannelRuntimeStatus, error) {
    if r.channelLog == nil { return channel.ChannelRuntimeStatus{}, channel.ErrInvalidConfig }
    return r.channelLog.Status(id)
}
```

For repair:

```go
type managerChannelLeaderRepairOperator struct {
    repairer runtimechannelmeta.Repairer or AuthoritativeRepairer-compatible interface
}
```

If calling `RepairIfNeeded`, the usecase can pass meta and reason directly instead of the request-only shape. Adjust the usecase port if this makes the boundary cleaner; keep concrete wiring in app.

- [ ] **Step 4: Wire `managementusecase.Options`**

In `internal/app/build.go`, when creating `managementusecase.New`, add:

```go
ChannelReplicaStatus: managerChannelReplicaStatusReader{channelLog: app.channelLog},
ChannelLeaderRepair: managerChannelLeaderRepairOperator{repairer: channelLeaderRepairer},
```

Do not create new global services.

- [ ] **Step 5: Re-run app tests**

Run:

```bash
GOWORK=off go test ./internal/app -run 'TestManagerChannelCluster(Operation|Status|Repair)|TestBuild' -count=1
```

Expected: PASS.

- [ ] **Step 6: Run focused backend tests**

Run:

```bash
GOWORK=off go test ./internal/usecase/management ./internal/access/manager ./internal/app -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit app wiring slice**

Run:

```bash
git add internal/app/manager_channel_cluster_operations.go internal/app/build.go internal/app/*channel_cluster*test.go
git commit -m "feat: wire channel cluster operations"
```

## Task 5: Add Frontend API Contracts

**Files:**
- Modify: `web/src/lib/manager-api.types.ts`
- Modify: `web/src/lib/manager-api.ts`
- Modify: `web/src/lib/manager-api.test.ts`

- [ ] **Step 1: Write failing API client tests**

Add tests:

```ts
await getChannelClusterReplicas(2, "room-1")
// fetch called with "/manager/channel-cluster/2/room-1/replicas"

await repairChannelClusterLeader(2, "room-1", { reason: "no_leader" })
// fetch called with method POST, JSON body { reason: "no_leader" }
```

Also assert `ManagerApiError` maps 409 from repair.

- [ ] **Step 2: Run client tests to verify RED**

Run:

```bash
cd web && bun run test src/lib/manager-api.test.ts
```

Expected: FAIL because wrappers/types do not exist.

- [ ] **Step 3: Add DTO types**

In `manager-api.types.ts`, add:

```ts
export type ManagerChannelClusterReplicaStatus = {
  node_id: number
  role: string
  is_leader: boolean
  in_isr: boolean
  reported: boolean
  commit_seq?: number | null
  leo?: number | null
  checkpoint_hw?: number | null
  lag?: number | null
}

export type ManagerChannelClusterReplicaDetailResponse = {
  channel: ManagerChannelRuntimeMetaDetailResponse
  runtime_reported: boolean
  commit_seq?: number | null
  min_available_seq?: number | null
  retention_through_seq?: number | null
  replicas: ManagerChannelClusterReplicaStatus[]
}

export type RepairChannelClusterLeaderInput = {
  reason?: string
}

export type ManagerChannelClusterRepairResponse = {
  changed: boolean
  channel: ManagerChannelRuntimeMetaDetailResponse
}
```

- [ ] **Step 4: Add wrappers**

In `manager-api.ts`, add:

```ts
export function getChannelClusterReplicas(channelType: number, channelId: string) {
  return jsonManagerFetch<ManagerChannelClusterReplicaDetailResponse>(
    `/manager/channel-cluster/${channelType}/${encodeURIComponent(channelId)}/replicas`,
  )
}

export function repairChannelClusterLeader(
  channelType: number,
  channelId: string,
  input: RepairChannelClusterLeaderInput = {},
) {
  return jsonManagerFetch<ManagerChannelClusterRepairResponse>(
    `/manager/channel-cluster/${channelType}/${encodeURIComponent(channelId)}/repair`,
    { method: "POST", body: JSON.stringify(input) },
  )
}
```

- [ ] **Step 5: Re-run client tests**

Run:

```bash
cd web && bun run test src/lib/manager-api.test.ts
```

Expected: PASS.

- [ ] **Step 6: Commit frontend API slice**

Run:

```bash
git add web/src/lib/manager-api.types.ts web/src/lib/manager-api.ts web/src/lib/manager-api.test.ts
git commit -m "feat: add channel cluster operation client"
```

## Task 6: Update Unhealthy Page With Detail And Repair Actions

**Files:**
- Modify: `web/src/pages/channel-cluster/unhealthy/page.test.tsx`
- Modify: `web/src/pages/channel-cluster/unhealthy/page.tsx`
- Modify: `web/src/i18n/messages/en.ts`
- Modify: `web/src/i18n/messages/zh-CN.ts`

- [ ] **Step 1: Write failing page tests**

Update mocks to include `getChannelClusterReplicas` and `repairChannelClusterLeader`.

Add tests:

1. clicking `Inspect replicas` loads and shows detail:
   - `Runtime reported`
   - `Node 1`, `leader`, `Reported`, `commit 42`, `lag 0`
   - `Node 2`, `follower`, `Not reported`, `-`
2. row with `no_leader` shows `Repair leader`; row with only `isr_insufficient` does not.
3. successful repair calls API with `{ reason: "no_leader" }`, shows changed/leader copy, and reloads first page.
4. repair 409 shows conflict copy such as `No safe leader candidate` and keeps row visible.
5. detail 503 maps to unavailable state in the detail panel.

- [ ] **Step 2: Run page tests to verify RED**

Run:

```bash
cd web && bun run test src/pages/channel-cluster/unhealthy/page.test.tsx
```

Expected: FAIL because actions/detail are missing.

- [ ] **Step 3: Implement detail state**

In `page.tsx`, add local state for selected channel detail:

```ts
type DetailState = {
  channel: ManagerChannelClusterUnhealthyItem | null
  detail: ManagerChannelClusterReplicaDetailResponse | null
  loading: boolean
  error: Error | null
}
```

Use an inline `SectionCard` below the table or an existing `DetailSheet` if preferred. Keep UI simple.

- [ ] **Step 4: Add inspect action**

For each row, add a button:

```tsx
<Button onClick={() => void loadReplicaDetail(channel)} size="sm" variant="outline">
  {intl.formatMessage({ id: "channelCluster.unhealthy.inspectReplicas" })}
</Button>
```

Render replica table with unknown values as `-`.

- [ ] **Step 5: Add repair action**

Add a helper:

```ts
function canRepairLeader(channel: ManagerChannelClusterUnhealthyItem) {
  return channel.reasons.includes("no_leader")
}
```

On click:

- call `repairChannelClusterLeader(channel.channel_type, channel.channel_id, { reason: "no_leader" })`
- show result copy using returned `changed` and `channel.leader`
- call `loadFirstPage(true)` to refresh first page

Do not show repair for `isr_insufficient` only.

- [ ] **Step 6: Add i18n copy**

Add keys:

- `channelCluster.unhealthy.inspectReplicas`
- `channelCluster.unhealthy.repairLeader`
- `channelCluster.unhealthy.repairSuccessChanged`
- `channelCluster.unhealthy.repairSuccessUnchanged`
- `channelCluster.unhealthy.repairConflict`
- `channelCluster.replicaDetail.title`
- `channelCluster.replicaDetail.runtimeReported`
- `channelCluster.replicaDetail.runtimeUnreported`
- `channelCluster.replicaDetail.reported`
- `channelCluster.replicaDetail.notReported`
- `channelCluster.replicaDetail.commitSeq`
- `channelCluster.replicaDetail.lag`

- [ ] **Step 7: Re-run page tests**

Run:

```bash
cd web && bun run test src/pages/channel-cluster/unhealthy/page.test.tsx
```

Expected: PASS.

- [ ] **Step 8: Commit page slice**

Run:

```bash
git add web/src/pages/channel-cluster/unhealthy/page.tsx web/src/pages/channel-cluster/unhealthy/page.test.tsx web/src/i18n/messages/en.ts web/src/i18n/messages/zh-CN.ts
git commit -m "feat: add channel cluster repair actions"
```

## Task 7: Documentation And Verification

**Files:**
- Modify: `docs/raw/web-admin-restructure.md`
- Modify: `web/README.md`

- [ ] **Step 1: Update docs**

In `docs/raw/web-admin-restructure.md`:

- Mark `GET /manager/channel-cluster/:type/:id/replicas` as complete for best-effort/proven status.
- Mark `POST /manager/channel-cluster/:type/:id/repair` as complete for policy-driven repair.
- Keep explicit target transfer and batch leader drain as pending.

In `web/README.md`, update matrix for `/channel-cluster/unhealthy` to include replicas and repair endpoints.

- [ ] **Step 2: Run focused backend verification**

Run:

```bash
GOWORK=off go test ./internal/usecase/management ./internal/access/manager ./internal/app -count=1
```

Expected: PASS.

- [ ] **Step 3: Run focused frontend verification**

Run:

```bash
cd web && bun run test src/lib/manager-api.test.ts src/pages/channel-cluster/unhealthy/page.test.tsx src/pages/channel-cluster/page.test.tsx src/pages/dashboard/page.test.tsx
```

Expected: PASS.

- [ ] **Step 4: Run frontend build**

Run:

```bash
cd web && bun run build
```

Expected: PASS. Do not commit `web/dist` hash churn unless repository convention requires it; restore generated index hash if necessary.

- [ ] **Step 5: Run broader backend tests**

Run:

```bash
GOWORK=off go test ./internal/... ./pkg/... -count=1
```

Expected: PASS, or record unrelated/pre-existing flaky failures clearly with targeted rerun evidence.

- [ ] **Step 6: Commit docs slice**

Run:

```bash
git add docs/raw/web-admin-restructure.md web/README.md
git commit -m "docs: update channel cluster operations status"
```

## Final Acceptance Criteria

- `GET /manager/channel-cluster/:type/:id/replicas` returns truthful reported/unreported replica detail and is covered by usecase and HTTP tests.
- `POST /manager/channel-cluster/:type/:id/repair` invokes safe policy-driven leader repair and is covered by usecase and HTTP tests.
- Repair route is protected by `cluster.channel` write permission.
- Unhealthy page can inspect replica detail and repair `no_leader` rows.
- UI never shows explicit target transfer or made-up follower lag.
- Focused Go tests, focused web tests, frontend build, and broader backend tests have been run with results recorded.
