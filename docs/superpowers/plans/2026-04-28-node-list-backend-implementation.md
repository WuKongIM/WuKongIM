# Node List Backend Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extend the manager node inventory API and web Nodes page so node membership, health, Controller, Slot, runtime, and action hints are explicit while keeping list/detail reads lightweight.

**Architecture:** Add grouped node inventory DTOs in `internal/usecase/management`, map them through `internal/access/manager`, and update `web` to render the grouped shape while preserving legacy fields. Remove the hidden distributed-log status scan from default list/detail reads; leave expensive log-health diagnostics for a future endpoint.

**Tech Stack:** Go manager usecases and Gin access layer, controller metadata DTOs, React 19, TypeScript, Vitest, Bun.

---

## File Structure

- Modify `internal/usecase/management/nodes.go`
  - Owns node list DTOs, role/state mapping helpers, node snapshot aggregation, slot summary counts, runtime summary attachment, and action hints.
  - Remove default distributed-log aggregation from the inventory snapshot path.
- Modify `internal/usecase/management/node_detail.go`
  - Reuse the list snapshot and return grouped detail with hosted/leader Slot IDs.
- Modify `internal/usecase/management/node_operator.go`
  - No semantic change expected, but compile against any `NodeDetail` shape updates.
- Modify `internal/usecase/management/nodes_test.go`
  - Add TDD coverage for membership, join state, schedulable, Controller roles, Slot counts, runtime unknown fallback, and no log status scan.
- Modify `internal/access/manager/server.go`
  - Change `Management.ListNodes` signature if the usecase returns a top-level `NodeList` response.
- Modify `internal/access/manager/nodes.go`
  - Add top-level response metadata and grouped JSON DTOs.
  - Keep legacy `status`, `last_heartbeat_at`, `controller.role`, and `slot_stats` fields.
- Modify `internal/access/manager/node_detail.go`
  - Add grouped detail DTO fields and keep legacy shape.
- Modify `internal/access/manager/server_test.go`
  - Assert grouped JSON fields and legacy compatibility fields.
- Modify `web/src/lib/manager-api.types.ts`
  - Add grouped node inventory TypeScript types while preserving existing fields.
- Modify `web/src/pages/nodes/page.tsx`
  - Render membership, health, Controller, Slot, runtime, and action hints as separate concepts.
- Modify `web/src/pages/nodes/page.test.tsx`
  - Cover grouped rendering and action hint behavior.
- Modify `web/src/i18n/messages/en.ts`
  - Add English copy for the new grouped labels.
- Modify `web/src/i18n/messages/zh-CN.ts`
  - Add Chinese copy for the new grouped labels.
- Modify `web/src/lib/manager-api.test.ts` only if the response type checks or fixtures need updates.

## Task 1: Add Usecase Node Inventory Model

**Files:**
- Modify: `internal/usecase/management/nodes.go`
- Modify: `internal/usecase/management/node_detail.go`
- Test: `internal/usecase/management/nodes_test.go`

- [ ] **Step 1: Write failing tests for grouped node inventory mapping**

Add tests that build a `management.App` with fake controller data and assert the grouped fields. Include at least:

```go
func TestListNodesReturnsLayeredInventoryFields(t *testing.T) {
	now := time.Unix(1714298400, 0)
	app := New(Options{
		LocalNodeID:       1,
		ControllerPeerIDs: []uint64{1, 2},
		SlotReplicaN:      3,
		Now:               func() time.Time { return now },
		Cluster: fakeClusterReader{
			controllerLeaderID: 1,
			nodes: []controllermeta.ClusterNode{{
				NodeID:          1,
				Name:            "node-1",
				Addr:            "10.0.0.1:7000",
				Role:            controllermeta.NodeRoleData,
				JoinState:       controllermeta.NodeJoinStateActive,
				Status:          controllermeta.NodeStatusAlive,
				LastHeartbeatAt: now.Add(-2 * time.Second),
				CapacityWeight:  2,
			}},
			views: []controllermeta.SlotRuntimeView{{
				SlotID:       7,
				CurrentPeers: []uint64{1, 2, 3},
				LeaderID:     1,
				HasQuorum:    true,
			}},
		},
		RuntimeSummary: scaleInRuntimeSummaryReader{summary: NodeRuntimeSummary{
			NodeID:               1,
			ActiveOnline:         4,
			GatewaySessions:      5,
			AcceptingNewSessions: true,
		}},
	})

	got, err := app.ListNodes(context.Background())
	require.NoError(t, err)
	require.Equal(t, uint64(1), got.ControllerLeaderID)
	require.Len(t, got.Items, 1)
	node := got.Items[0]
	require.Equal(t, "node-1", node.Name)
	require.Equal(t, "data", node.Membership.Role)
	require.Equal(t, "active", node.Membership.JoinState)
	require.True(t, node.Membership.Schedulable)
	require.Equal(t, "alive", node.Health.Status)
	require.Equal(t, "leader", node.Controller.Role)
	require.True(t, node.Controller.Voter)
	require.Equal(t, 1, node.Slots.ReplicaCount)
	require.Equal(t, 1, node.Slots.LeaderCount)
	require.Equal(t, 0, node.Slots.FollowerCount)
	require.Equal(t, 4, node.Runtime.ActiveOnline)
	require.True(t, node.Runtime.AcceptingNewSessions)
	require.True(t, node.Actions.CanDrain)
}
```

Expected compile failure: `ListNodes` still returns `[]Node`, and grouped fields such as `Membership`, `Health`, `Controller`, `Slots`, `Runtime`, and `Actions` do not exist.

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
go test ./internal/usecase/management -run 'TestListNodesReturnsLayeredInventoryFields' -count=1
```

Expected: FAIL at compile time or assertion time because grouped inventory does not exist.

- [ ] **Step 3: Implement usecase model structs**

In `internal/usecase/management/nodes.go`, add grouped structs near `Node`:

```go
// NodeList is the manager-facing node inventory snapshot.
type NodeList struct {
	// GeneratedAt records when this inventory snapshot was built.
	GeneratedAt time.Time
	// ControllerLeaderID is the Controller Raft leader known to this node.
	ControllerLeaderID uint64
	// Items contains ordered node inventory rows.
	Items []Node
}

// NodeMembership describes durable cluster membership for one node.
type NodeMembership struct {
	// Role is the durable cluster membership role, such as data or controller_voter.
	Role string
	// JoinState is the durable membership lifecycle state.
	JoinState string
	// Schedulable reports whether the planner may place data replicas on this node.
	Schedulable bool
}

// NodeHealth describes observed node health and operator state.
type NodeHealth struct {
	// Status is the manager-facing health or operator state.
	Status string
	// LastHeartbeatAt is the latest controller heartbeat timestamp.
	LastHeartbeatAt time.Time
}

// NodeController describes this node's Controller Raft perspective.
type NodeController struct {
	// Role is leader, follower, or none.
	Role string
	// Voter reports whether this node is a configured Controller voter.
	Voter bool
	// LeaderID is the current Controller Raft leader known locally.
	LeaderID uint64
}

// NodeSlotSummary contains lightweight Slot placement counts for one node.
type NodeSlotSummary struct {
	ReplicaCount    int
	LeaderCount     int
	FollowerCount   int
	QuorumLostCount int
	UnreportedCount int
}

// NodeActions contains backend business capability hints for UI actions.
type NodeActions struct {
	CanDrain   bool
	CanResume  bool
	CanScaleIn bool
	CanOnboard bool
}
```

Extend `Node` while preserving legacy fields:

```go
type Node struct {
	NodeID uint64
	Name string
	Addr string
	Status string
	LastHeartbeatAt time.Time
	ControllerRole string
	SlotCount int
	LeaderSlotCount int
	IsLocal bool
	CapacityWeight int
	Membership NodeMembership
	Health NodeHealth
	Controller NodeController
	Slots NodeSlotSummary
	Runtime NodeRuntimeSummary
	Actions NodeActions
}
```

Remove `DistributedLog` from `Node` unless a future diagnostics endpoint is implemented in the same change.

- [ ] **Step 4: Implement mapping helpers**

Add helpers in `internal/usecase/management/nodes.go`:

```go
func managerNodeRole(role controllermeta.NodeRole) string {
	switch role {
	case controllermeta.NodeRoleData:
		return "data"
	case controllermeta.NodeRoleControllerVoter:
		return "controller_voter"
	default:
		return "unknown"
	}
}

func managerNodeJoinState(state controllermeta.NodeJoinState) string {
	switch state {
	case controllermeta.NodeJoinStateJoining:
		return "joining"
	case controllermeta.NodeJoinStateActive:
		return "active"
	case controllermeta.NodeJoinStateRejected:
		return "rejected"
	default:
		return "unknown"
	}
}

func managerNodeSchedulable(node controllermeta.ClusterNode) bool {
	return node.Role == controllermeta.NodeRoleData &&
		node.JoinState == controllermeta.NodeJoinStateActive &&
		node.Status == controllermeta.NodeStatusAlive
}
```

- [ ] **Step 5: Update `ListNodes` return shape**

Change `ListNodes(ctx)` to return `NodeList`:

```go
func (a *App) ListNodes(ctx context.Context) (NodeList, error) {
	clusterNodes, slotSummary, controllerLeaderID, err := a.loadNodeSnapshot(ctx)
	if err != nil {
		return NodeList{}, err
	}
	nodes := make([]Node, 0, len(clusterNodes))
	for _, clusterNode := range clusterNodes {
		nodes = append(nodes, a.managerNode(ctx, clusterNode, controllerLeaderID, slotSummary))
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].NodeID < nodes[j].NodeID })
	return NodeList{GeneratedAt: a.now(), ControllerLeaderID: controllerLeaderID, Items: nodes}, nil
}
```

If passing `ctx` into `managerNode` makes tests awkward, use a helper `runtimeSummaryForNode(ctx, nodeID)` inside `managerNode`.

- [ ] **Step 6: Run targeted usecase tests**

Run:

```bash
go test ./internal/usecase/management -run 'TestListNodesReturnsLayeredInventoryFields|TestListNodesSortsByNodeIDAndDefaultsCountsToZero|TestGetNodeReturnsNodeWithHostedAndLeaderSlots' -count=1
```

Expected: PASS after implementation.

- [ ] **Step 7: Commit usecase model changes**

```bash
git add internal/usecase/management/nodes.go internal/usecase/management/node_detail.go internal/usecase/management/nodes_test.go
git commit -m "Add layered node inventory usecase model"
```

## Task 2: Keep Node Inventory Lightweight

**Files:**
- Modify: `internal/usecase/management/nodes.go`
- Test: `internal/usecase/management/nodes_test.go`

- [ ] **Step 1: Write failing test proving list/detail do not scan Slot log status**

Add a fake cluster that implements `SlotLogStatusOnNode` and counts calls:

```go
type fakeNodeInventoryCluster struct {
	fakeClusterReader
	logStatusCalls int
}

func (f *fakeNodeInventoryCluster) SlotLogStatusOnNode(context.Context, uint64, uint32) (raftcluster.SlotLogStatus, error) {
	f.logStatusCalls++
	return raftcluster.SlotLogStatus{}, nil
}

func TestListNodesDoesNotReadDistributedLogStatus(t *testing.T) {
	cluster := &fakeNodeInventoryCluster{fakeClusterReader: fakeClusterReader{
		controllerLeaderID: 1,
		nodes: []controllermeta.ClusterNode{{
			NodeID: 1, Addr: "127.0.0.1:7001", Role: controllermeta.NodeRoleData,
			JoinState: controllermeta.NodeJoinStateActive, Status: controllermeta.NodeStatusAlive,
			CapacityWeight: 1,
		}},
		views: []controllermeta.SlotRuntimeView{{SlotID: 1, CurrentPeers: []uint64{1}, LeaderID: 1, HasQuorum: true}},
	}}
	app := New(Options{Cluster: cluster, ControllerPeerIDs: []uint64{1}, Now: time.Now})

	_, err := app.ListNodes(context.Background())
	require.NoError(t, err)
	require.Zero(t, cluster.logStatusCalls)
}
```

Expected current failure: `logStatusCalls` is greater than zero because `loadNodeSnapshot` calls `summarizeDistributedLog`.

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/usecase/management -run TestListNodesDoesNotReadDistributedLogStatus -count=1
```

Expected: FAIL before the snapshot path is changed.

- [ ] **Step 3: Remove distributed-log aggregation from `loadNodeSnapshot`**

Change `nodeSlotSummary` to hold only lightweight inventory maps and counts:

```go
type nodeSlotSummary struct {
	slotCountByNode       map[uint64]int
	leaderSlotCountByNode map[uint64]int
	followerCountByNode   map[uint64]int
	quorumLostByNode      map[uint64]int
	unreportedByNode      map[uint64]int
	hostedSlotIDsByNode   map[uint64][]uint32
	leaderSlotIDsByNode   map[uint64][]uint32
}
```

Remove this line from `loadNodeSnapshot`:

```go
summary.distributedLogByNode = a.summarizeDistributedLog(ctx, views, controllerLeaderID)
```

If no diagnostics endpoint is created in this implementation, remove the now-unused distributed log structs and helpers from `nodes.go`:

- `NodeDistributedLog`
- `NodeControllerLog`
- `NodeSlotLogHealth`
- `NodeSlotLogSample`
- `slotLogStatusReader`
- `summarizeDistributedLog`
- `loadSlotLogStatuses`
- `buildNodeSlotLogSample`
- `sortNodeSlotLogSamples`
- `nodeSlotLogStatusRank`
- `subtractUint64` if it becomes unused

Then remove the `raftcluster` import from `nodes.go` if it is no longer needed.

- [ ] **Step 4: Compute lightweight Slot counts**

Update `summarizeNodeSlots` so each hosted peer receives counts:

```go
for _, view := range views {
	for _, peer := range view.CurrentPeers {
		summary.slotCountByNode[peer]++
		summary.hostedSlotIDsByNode[peer] = append(summary.hostedSlotIDsByNode[peer], view.SlotID)
		if !view.HasQuorum {
			summary.quorumLostByNode[peer]++
		}
		if view.LeaderID != peer {
			summary.followerCountByNode[peer]++
		}
	}
	if view.LeaderID != 0 {
		summary.leaderSlotCountByNode[view.LeaderID]++
		summary.leaderSlotIDsByNode[view.LeaderID] = append(summary.leaderSlotIDsByNode[view.LeaderID], view.SlotID)
	}
}
```

Keep `FollowerCount` clamped in `managerNode` as a safety net:

```go
followers := slotSummary.slotCountByNode[nodeID] - slotSummary.leaderSlotCountByNode[nodeID]
if followers < 0 {
	followers = 0
}
```

- [ ] **Step 5: Run targeted usecase tests**

```bash
go test ./internal/usecase/management -run 'TestListNodesDoesNotReadDistributedLogStatus|TestListNodesReturnsLayeredInventoryFields|TestGetNodeReturnsNodeWithHostedAndLeaderSlots' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit lightweight snapshot change**

```bash
git add internal/usecase/management/nodes.go internal/usecase/management/nodes_test.go
git commit -m "Keep node inventory reads lightweight"
```

## Task 3: Map Grouped Inventory Through Manager HTTP API

**Files:**
- Modify: `internal/access/manager/server.go`
- Modify: `internal/access/manager/nodes.go`
- Modify: `internal/access/manager/node_detail.go`
- Test: `internal/access/manager/server_test.go`

- [ ] **Step 1: Write failing API JSON tests**

Update or add `TestManagerNodesReturnsAggregatedInventory` to assert grouped fields and legacy compatibility fields:

```go
require.JSONEq(t, `{
  "generated_at":"2026-04-28T10:00:00Z",
  "controller_leader_id":1,
  "total":1,
  "items":[{
    "node_id":2,
    "name":"node-2",
    "addr":"127.0.0.1:7002",
    "status":"alive",
    "last_heartbeat_at":"2026-04-28T09:59:58Z",
    "is_local":false,
    "capacity_weight":2,
    "membership":{"role":"data","join_state":"active","schedulable":true},
    "health":{"status":"alive","last_heartbeat_at":"2026-04-28T09:59:58Z"},
    "controller":{"role":"follower","voter":true,"leader_id":1},
    "slot_stats":{"count":3,"leader_count":1},
    "slots":{"replica_count":3,"leader_count":1,"follower_count":2,"quorum_lost_count":0,"unreported_count":0},
    "runtime":{"node_id":2,"active_online":4,"closing_online":0,"total_online":4,"gateway_sessions":5,"sessions_by_listener":{},"accepting_new_sessions":true,"draining":false,"unknown":false},
    "actions":{"can_drain":true,"can_resume":false,"can_scale_in":true,"can_onboard":false}
  }]
}`, rec.Body.String())
```

Expected failure: DTOs do not include top-level or grouped fields.

- [ ] **Step 2: Run failing access test**

```bash
go test ./internal/access/manager -run TestManagerNodesReturnsAggregatedInventory -count=1
```

Expected: FAIL before DTOs and interface are updated.

- [ ] **Step 3: Update access interface and response DTOs**

Change `Management.ListNodes` in `internal/access/manager/server.go`:

```go
ListNodes(ctx context.Context) (managementusecase.NodeList, error)
```

Update `NodesResponse`:

```go
type NodesResponse struct {
	GeneratedAt time.Time `json:"generated_at"`
	ControllerLeaderID uint64 `json:"controller_leader_id"`
	Total int `json:"total"`
	Items []NodeDTO `json:"items"`
}
```

Add DTO groups in `internal/access/manager/nodes.go`:

```go
type NodeMembershipDTO struct {
	Role string `json:"role"`
	JoinState string `json:"join_state"`
	Schedulable bool `json:"schedulable"`
}

type NodeHealthDTO struct {
	Status string `json:"status"`
	LastHeartbeatAt time.Time `json:"last_heartbeat_at"`
}

type NodeControllerDTO struct {
	Role string `json:"role"`
	Voter bool `json:"voter"`
	LeaderID uint64 `json:"leader_id"`
}

type NodeSlotsSummaryDTO struct {
	ReplicaCount int `json:"replica_count"`
	LeaderCount int `json:"leader_count"`
	FollowerCount int `json:"follower_count"`
	QuorumLostCount int `json:"quorum_lost_count"`
	UnreportedCount int `json:"unreported_count"`
}

type NodeActionsDTO struct {
	CanDrain bool `json:"can_drain"`
	CanResume bool `json:"can_resume"`
	CanScaleIn bool `json:"can_scale_in"`
	CanOnboard bool `json:"can_onboard"`
}
```

Use the existing `managementusecase.NodeRuntimeSummary` shape directly or add a thin DTO if JSON field names need explicit control.

- [ ] **Step 4: Update `handleNodes` mapping**

```go
list, err := s.management.ListNodes(c.Request.Context())
...
c.JSON(http.StatusOK, NodesResponse{
	GeneratedAt: list.GeneratedAt,
	ControllerLeaderID: list.ControllerLeaderID,
	Total: len(list.Items),
	Items: nodeDTOs(list.Items),
})
```

Update `nodeDTO` to map both compatibility and grouped fields. Keep `controller.role` compatible while adding `controller.voter` and `controller.leader_id`.

- [ ] **Step 5: Update detail DTO mapping**

Extend `NodeSlotsDTO` with summary fields:

```go
type NodeSlotsDTO struct {
	HostedIDs []uint32 `json:"hosted_ids"`
	LeaderIDs []uint32 `json:"leader_ids"`
	ReplicaCount int `json:"replica_count"`
	LeaderCount int `json:"leader_count"`
	FollowerCount int `json:"follower_count"`
	QuorumLostCount int `json:"quorum_lost_count"`
	UnreportedCount int `json:"unreported_count"`
}
```

Map from `item.Node.Slots` when building `nodeDetailDTO`.

- [ ] **Step 6: Update test stubs**

In `internal/access/manager/server_test.go`, change `managementStub.ListNodes` to return `managementusecase.NodeList`. Update all fixtures from `nodes: []managementusecase.Node` to either:

```go
nodes: managementusecase.NodeList{GeneratedAt: now, ControllerLeaderID: 1, Items: []managementusecase.Node{...}}
```

or provide a helper:

```go
func nodeListAt(t time.Time, leader uint64, items ...managementusecase.Node) managementusecase.NodeList {
	return managementusecase.NodeList{GeneratedAt: t, ControllerLeaderID: leader, Items: items}
}
```

- [ ] **Step 7: Run access tests**

```bash
go test ./internal/access/manager -run 'TestManagerNodes|TestManagerNodeDetail|TestManagerNodeDraining|TestManagerNodeResume' -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit HTTP DTO changes**

```bash
git add internal/access/manager/server.go internal/access/manager/nodes.go internal/access/manager/node_detail.go internal/access/manager/server_test.go
git commit -m "Expose layered node inventory API"
```

## Task 4: Update Web Types and Nodes Page Rendering

**Files:**
- Modify: `web/src/lib/manager-api.types.ts`
- Modify: `web/src/pages/nodes/page.tsx`
- Modify: `web/src/pages/nodes/page.test.tsx`
- Modify: `web/src/i18n/messages/en.ts`
- Modify: `web/src/i18n/messages/zh-CN.ts`
- Optional modify: `web/src/lib/manager-api.test.ts`

- [ ] **Step 1: Write failing web component test**

Update `nodeRow` in `web/src/pages/nodes/page.test.tsx` to include grouped fields and assert separate labels. Example expectations:

```tsx
expect(await screen.findByText("127.0.0.1:7000")).toBeInTheDocument()
expect(screen.getByText("data")).toBeInTheDocument()
expect(screen.getByText("active")).toBeInTheDocument()
expect(screen.getByText("schedulable")).toBeInTheDocument()
expect(screen.getByText("controller voter")).toBeInTheDocument()
expect(screen.getByText("replicas 3 / leaders 2 / followers 1")).toBeInTheDocument()
expect(screen.getByText("sessions 5 / online 4")).toBeInTheDocument()
```

Also add a test that backend action hints disable a write button even when the user has permission:

```tsx
const blockedNode = { ...nodeRow, actions: { ...nodeRow.actions, can_drain: false } }
getNodesMock.mockResolvedValueOnce({ generated_at: now, controller_leader_id: 1, total: 1, items: [blockedNode] })
...
expect(screen.getByRole("button", { name: "Drain" })).toBeDisabled()
```

Expected failure: TypeScript types and UI do not know grouped fields.

- [ ] **Step 2: Run failing web test**

```bash
cd web
bun run test src/pages/nodes/page.test.tsx
```

Expected: FAIL before types and rendering are updated.

- [ ] **Step 3: Add TypeScript grouped types**

In `web/src/lib/manager-api.types.ts`:

```ts
export type ManagerNodeMembership = {
  role: string
  join_state: string
  schedulable: boolean
}

export type ManagerNodeHealth = {
  status: string
  last_heartbeat_at: string
}

export type ManagerNodeController = {
  role: string
  voter: boolean
  leader_id: number
}

export type ManagerNodeSlotsSummary = {
  replica_count: number
  leader_count: number
  follower_count: number
  quorum_lost_count: number
  unreported_count: number
}

export type ManagerNodeRuntime = {
  node_id: number
  active_online: number
  closing_online: number
  total_online: number
  gateway_sessions: number
  sessions_by_listener: Record<string, number>
  accepting_new_sessions: boolean
  draining: boolean
  unknown: boolean
}

export type ManagerNodeActions = {
  can_drain: boolean
  can_resume: boolean
  can_scale_in: boolean
  can_onboard: boolean
}
```

Extend `ManagerNode`:

```ts
name?: string
membership: ManagerNodeMembership
health: ManagerNodeHealth
controller: ManagerNodeController
slots: ManagerNodeSlotsSummary
runtime: ManagerNodeRuntime
actions: ManagerNodeActions
```

Extend `ManagerNodesResponse`:

```ts
generated_at: string
controller_leader_id: number
total: number
items: ManagerNode[]
```

Extend `ManagerNodeDetailResponse.slots` with summary count fields while keeping `hosted_ids` and `leader_ids`.

- [ ] **Step 4: Add i18n messages**

Add English and Chinese keys for:

- `nodes.table.membership`
- `nodes.table.health`
- `nodes.membershipSummary`
- `nodes.controllerVoter`
- `nodes.controllerNonVoter`
- `nodes.schedulable`
- `nodes.notSchedulable`
- `nodes.runtimeSummary`
- `nodes.runtimeUnknown`
- `nodes.slotLayerSummary`

Use concise copy. Keep existing keys intact.

- [ ] **Step 5: Update Nodes page rendering**

In `web/src/pages/nodes/page.tsx`:

- Add helper functions with fallback to compatibility fields where possible:

```ts
function nodeHealthStatus(node: ManagerNode) {
  return node.health?.status ?? node.status
}

function canDrainNode(node: ManagerNode, canWriteNodes: boolean) {
  return canWriteNodes && Boolean(node.actions?.can_drain)
}

function canResumeNode(node: ManagerNode, canWriteNodes: boolean) {
  return canWriteNodes && Boolean(node.actions?.can_resume)
}
```

- Replace the single Controller cell with Controller role plus voter badge.
- Add membership cell showing role, join state, and schedulable state.
- Show Slot summary from `node.slots` and fall back to `slot_stats` if the grouped field is absent during rollout.
- Show runtime summary only when `node.runtime` exists and `unknown` is false; otherwise show the runtime unknown label.
- Keep `Inspect`, `Drain/Resume`, and `Scale-in` permission checks, but additionally respect backend `actions` hints.

- [ ] **Step 6: Update detail sheet**

In the detail sheet content, add grouped key/value rows:

- Membership role
- Join state
- Schedulable
- Controller role
- Controller voter
- Slot summary
- Runtime admission/session summary

Keep existing Hosted IDs and Leader IDs rows.

- [ ] **Step 7: Run web tests**

```bash
cd web
bun run test src/pages/nodes/page.test.tsx src/lib/manager-api.test.ts
```

Expected: PASS.

- [ ] **Step 8: Commit web updates**

```bash
git add web/src/lib/manager-api.types.ts web/src/pages/nodes/page.tsx web/src/pages/nodes/page.test.tsx web/src/i18n/messages/en.ts web/src/i18n/messages/zh-CN.ts web/src/lib/manager-api.test.ts
git commit -m "Render layered node inventory fields"
```

## Task 5: Full Verification and Cleanup

**Files:**
- Potential modify: `docs/development/PROJECT_KNOWLEDGE.md` only if a new lasting project rule is discovered.
- Potential modify: `docs/superpowers/specs/2026-04-28-node-list-backend-design.md` only if implementation changes the accepted contract.
- Potential modify: `docs/superpowers/plans/2026-04-28-node-list-backend-implementation.md` if tracking execution status in-place.

- [ ] **Step 1: Run focused Go tests**

```bash
go test ./internal/usecase/management ./internal/access/manager -count=1
```

Expected: PASS.

- [ ] **Step 2: Run focused web tests**

```bash
cd web
bun run test src/pages/nodes/page.test.tsx src/lib/manager-api.test.ts
```

Expected: PASS.

- [ ] **Step 3: Run broader manager-related tests**

```bash
go test ./cmd/wukongim ./internal/app ./internal/usecase/management ./internal/access/manager -run 'Test(LoadConfig.*Manager|Config.*Manager|Manager(Login|Nodes|Slots|SlotDetail|Tasks|TaskDetail|Overview)|List(Nodes|Slots|Tasks)|Get(Task|Slot|Node)|NewBuildsOptionalManagerServerWhenConfigured|StartStartsManagerAfterAPIWhenEnabled|StartRollsBackAPIAndClusterWhenManagerStartFails|StopStopsManagerBeforeAPIGatewayAndClusterClose)' -count=1
```

Expected: PASS. If this is too narrow or misses renamed tests, run the package-level command from Step 1 as the authoritative focused check.

- [ ] **Step 4: Run web build**

```bash
cd web
bun run build
```

Expected: PASS and Vite emits a production build.

- [ ] **Step 5: Check formatting and diff hygiene**

```bash
gofmt -w internal/usecase/management/nodes.go internal/usecase/management/node_detail.go internal/usecase/management/node_operator.go internal/access/manager/server.go internal/access/manager/nodes.go internal/access/manager/node_detail.go internal/access/manager/server_test.go internal/usecase/management/nodes_test.go
git diff --check
git status --short
```

Expected: `git diff --check` returns no output. `git status --short` shows only intended files before the final commit.

- [ ] **Step 6: Update docs only if needed**

If implementation discovers a durable project rule, add one concise bullet to `docs/development/PROJECT_KNOWLEDGE.md`. Otherwise do not modify docs.

- [ ] **Step 7: Final commit**

If any cleanup or docs changed after prior commits:

```bash
git add docs/development/PROJECT_KNOWLEDGE.md docs/superpowers/specs/2026-04-28-node-list-backend-design.md docs/superpowers/plans/2026-04-28-node-list-backend-implementation.md
git commit -m "Document layered node inventory rollout"
```

Skip this commit if there are no remaining changes.

- [ ] **Step 8: Final verification evidence**

Before reporting completion, paste the exact commands run and their pass/fail status into the final response. Do not claim completion unless the fresh verification commands passed.
