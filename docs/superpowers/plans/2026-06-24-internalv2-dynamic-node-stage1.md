# internalv2 Dynamic Node Lifecycle Stage 1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the Stage 1 foundation for internalv2 dynamic node lifecycle: seed-join config safety, durable lifecycle read models, active-only placement candidates, and manager lifecycle projection.

**Architecture:** This plan does not implement `JoinNode`, `ActivateNode`, Slot replica movement, or scale-in mutations. It makes the existing runtime safe for those later stages by separating static Controller voter bootstrap from seed discovery, carrying node lifecycle state through ControllerV2 -> clusterv2 -> manager, and excluding non-active nodes from new placement candidate views. Health freshness remains display-only in Stage 1; placement uses durable role and lifecycle only until later stages add fresh runtime/control-revision gates.

**Tech Stack:** Go, `testing`, ControllerV2 state model, clusterv2 control snapshots, cmd/wukongimv2 KEY=value config loader, internalv2 manager usecase tests.

---

## Scope

This plan implements only the "Stage 1: Read Model And Config Foundation" section of:

- `docs/superpowers/specs/2026-06-24-internalv2-dynamic-node-lifecycle-design.md`
- Master index: `docs/superpowers/plans/2026-06-24-internalv2-dynamic-node-lifecycle.md`
- Previous stage: none
- Next stage: `docs/superpowers/plans/2026-06-24-internalv2-dynamic-node-stage2.md`

It intentionally does not add manager join routes, control write RPCs, Slot learner movement, gateway drain mode, or node removal operations. Those require separate plans after this foundation passes.

## File Structure

- Modify `pkg/controllerv2/state/types.go`
  - Adds the durable `removed` lifecycle constant.
- Modify `pkg/controllerv2/state/validate.go`
  - Accepts `removed` as a node lifecycle state.
  - Keeps Slot `DesiredPeers` restricted to active data nodes.
  - Requires Controller voters to reference active controller-voter nodes.
- Modify `pkg/controllerv2/state/state_test.go`
  - Adds validation coverage for `removed` tombstones and controller-voter safety.
- Modify `pkg/clusterv2/control/snapshot.go`
  - Adds clusterv2 lifecycle/capacity fields to `control.Node`.
- Modify `pkg/clusterv2/control/controllerv2.go`
  - Maps ControllerV2 `JoinState` and `CapacityWeight` into clusterv2 snapshots.
- Modify `pkg/clusterv2/control/snapshot_validate.go`
  - Validates known lifecycle values.
  - Treats empty lifecycle in injected test snapshots as active for compatibility.
  - Restricts Slot peers to active data nodes.
- Modify `pkg/clusterv2/control/controllerv2_test.go`
  - Verifies lifecycle and capacity mapping.
- Modify `pkg/clusterv2/node_snapshot.go`
  - Renames the candidate helper from "alive" semantics to active schedulable semantics.
  - Excludes `joining`, `leaving`, and `removed` nodes from ChannelV2 placement candidates.
  - Keeps `NodeStatus` out of placement because Stage 1 does not yet persist fresh health reports.
- Modify `pkg/clusterv2/node_snapshot_test.go`
  - Verifies candidate filtering.
- Modify `pkg/clusterv2/config.go`
  - Adds seed-join config fields.
  - Prevents seed-join mode from implicit Controller voter bootstrap.
- Modify `pkg/clusterv2/config_test.go`
  - Verifies seed-join mode is mirror-only and non-bootstrap.
- Modify `cmd/wukongimv2/config.go`
  - Parses `WK_CLUSTER_SEEDS`, `WK_CLUSTER_ADVERTISE_ADDR`, and `WK_CLUSTER_JOIN_TOKEN`.
  - Rejects ambiguous seed-join plus static `WK_CLUSTER_NODES`.
- Modify `cmd/wukongimv2/config_test.go`
  - Verifies seed config parsing, env overlay, and invalid seed values.
- Modify `cmd/wukongimv2/*.conf.example` and `scripts/wukongimv2/*.conf`
  - Documents static bootstrap versus seed-join usage with English config comments.
- Modify `internalv2/usecase/management/nodes.go`
  - Projects real lifecycle state and capacity weight into node DTOs.
- Modify `internalv2/usecase/management/nodes_test.go`
  - Verifies joining/leaving/removed nodes are visible but not schedulable.

## Task 1: Add Durable Removed State And Control Snapshot Lifecycle Fields

**Files:**
- Modify: `pkg/controllerv2/state/types.go`
- Modify: `pkg/controllerv2/state/validate.go`
- Modify: `pkg/controllerv2/state/state_test.go`
- Modify: `pkg/clusterv2/control/snapshot.go`
- Modify: `pkg/clusterv2/control/controllerv2.go`
- Modify: `pkg/clusterv2/control/snapshot_validate.go`
- Modify: `pkg/clusterv2/control/controllerv2_test.go`

- [ ] **Step 1: Write failing ControllerV2 validation tests**

Append these tests near the existing state validation tests in `pkg/controllerv2/state/state_test.go`:

```go
func TestValidateAllowsRemovedDataNodeTombstone(t *testing.T) {
	st := testState()
	st.Nodes = append(st.Nodes, Node{
		NodeID:         4,
		Name:           "n4",
		Addr:           "n4",
		Roles:          []NodeRole{NodeRoleData},
		JoinState:      NodeJoinStateRemoved,
		Status:         NodeStatusDown,
		CapacityWeight: 1,
	})

	require.NoError(t, st.Validate())
}

func TestValidateRejectsRemovedControllerVoter(t *testing.T) {
	st := testState()
	st.Nodes[0].JoinState = NodeJoinStateRemoved

	require.ErrorIs(t, st.Validate(), ErrInvalidState)
}

func TestValidateRejectsRemovedSlotPeer(t *testing.T) {
	st := testState()
	st.Nodes[2].JoinState = NodeJoinStateRemoved

	require.ErrorIs(t, st.Validate(), ErrInvalidState)
}
```

- [ ] **Step 2: Run the ControllerV2 validation tests and verify RED**

Run:

```bash
go test ./pkg/controllerv2/state -run 'TestValidate(AllowsRemovedDataNodeTombstone|RejectsRemovedControllerVoter|RejectsRemovedSlotPeer)' -count=1
```

Expected: FAIL because `NodeJoinStateRemoved` is not defined.

- [ ] **Step 3: Add the durable removed lifecycle constant**

In `pkg/controllerv2/state/types.go`, extend the `NodeJoinState` constants:

```go
	// NodeJoinStateRemoved means the node identity is retained as a tombstone and must not receive assignments.
	NodeJoinStateRemoved NodeJoinState = "removed"
```

- [ ] **Step 4: Update ControllerV2 validation**

In `pkg/controllerv2/state/validate.go`, replace the join-state check with:

```go
			if node.JoinState != NodeJoinStateActive &&
				node.JoinState != NodeJoinStateJoining &&
				node.JoinState != NodeJoinStateLeaving &&
				node.JoinState != NodeJoinStateRemoved {
				return nil, invalid("unknown node join_state")
			}
```

In `validateControllers`, replace the controller node check with:

```go
			node, ok := nodes[controller.NodeID]
			if !ok || !node.HasRole(NodeRoleControllerVoter) || node.JoinState != NodeJoinStateActive {
				return invalid("controller voter must reference active controller_voter node")
			}
```

Do not change the existing Slot peer validation that requires `node.JoinState == NodeJoinStateActive`; that is the desired safety gate.

- [ ] **Step 5: Run the ControllerV2 validation tests and verify GREEN**

Run:

```bash
go test ./pkg/controllerv2/state -run 'TestValidate(AllowsRemovedDataNodeTombstone|RejectsRemovedControllerVoter|RejectsRemovedSlotPeer)' -count=1
```

Expected: PASS.

- [ ] **Step 6: Write failing clusterv2 control snapshot mapping tests**

In `pkg/clusterv2/control/controllerv2_test.go`, extend `TestControllerV2SnapshotMapping` after the existing node count assertion:

```go
	if snap.Nodes[0].JoinState != NodeJoinStateActive || snap.Nodes[0].CapacityWeight != 1 {
		t.Fatalf("first node lifecycle = %q capacity=%d, want active capacity 1", snap.Nodes[0].JoinState, snap.Nodes[0].CapacityWeight)
	}
```

Add a new test:

```go
func TestControllerV2SnapshotMappingPreservesNonActiveLifecycle(t *testing.T) {
	st := controllerV2State()
	st.Nodes[1].JoinState = cv2.NodeJoinStateJoining
	st.Nodes[1].CapacityWeight = 7
	st.Nodes[2].JoinState = cv2.NodeJoinStateLeaving

	snap, err := SnapshotFromControllerV2(st)
	if err != nil {
		t.Fatalf("SnapshotFromControllerV2() error = %v", err)
	}
	if snap.Nodes[1].JoinState != NodeJoinStateJoining || snap.Nodes[1].CapacityWeight != 7 {
		t.Fatalf("node 2 lifecycle = %q capacity=%d, want joining capacity 7", snap.Nodes[1].JoinState, snap.Nodes[1].CapacityWeight)
	}
	if snap.Nodes[2].JoinState != NodeJoinStateLeaving {
		t.Fatalf("node 3 lifecycle = %q, want leaving", snap.Nodes[2].JoinState)
	}
}
```

Add this control snapshot validation test to `pkg/clusterv2/control/control_test.go`:

```go
func TestSnapshotValidateRejectsSlotPeerThatIsNotActive(t *testing.T) {
	snap := validSnapshot()
	snap.Nodes[1].JoinState = NodeJoinStateJoining
	if err := snap.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want joining desired peer rejection")
	}
}
```

- [ ] **Step 7: Run clusterv2 control tests and verify RED**

Run:

```bash
go test ./pkg/clusterv2/control -run 'TestControllerV2SnapshotMapping|TestControllerV2SnapshotMappingPreservesNonActiveLifecycle|TestSnapshotValidateRejectsSlotPeerThatIsNotActive' -count=1
```

Expected: FAIL because `control.Node` does not expose `JoinState` or `CapacityWeight`.

- [ ] **Step 8: Add clusterv2 lifecycle read-model fields**

In `pkg/clusterv2/control/snapshot.go`, add:

```go
// NodeJoinState describes durable node lifecycle in the clusterv2 control read model.
type NodeJoinState string

const (
	// NodeJoinStateActive means the node may receive new placement.
	NodeJoinStateActive NodeJoinState = "active"
	// NodeJoinStateJoining means the node is visible but not assignment-ready.
	NodeJoinStateJoining NodeJoinState = "joining"
	// NodeJoinStateLeaving means the node is draining and must not receive new placement.
	NodeJoinStateLeaving NodeJoinState = "leaving"
	// NodeJoinStateRemoved means the node identity is retained as a tombstone.
	NodeJoinStateRemoved NodeJoinState = "removed"
)
```

Extend `control.Node`:

```go
	// JoinState is the durable membership lifecycle state.
	JoinState NodeJoinState
	// CapacityWeight is the relative planner capacity for future placement decisions.
	CapacityWeight uint32
```

- [ ] **Step 9: Map ControllerV2 lifecycle and capacity into clusterv2 snapshots**

In `pkg/clusterv2/control/controllerv2.go`, change node mapping to include lifecycle and capacity:

```go
		for _, node := range st.Nodes {
			snap.Nodes = append(snap.Nodes, Node{
				NodeID:         node.NodeID,
				Addr:           node.Addr,
				Roles:          mapControllerV2Roles(node.Roles),
				Status:         mapControllerV2Status(node.Status),
				JoinState:      mapControllerV2JoinState(node.JoinState),
				CapacityWeight: node.CapacityWeight,
			})
		}
```

Add:

```go
func mapControllerV2JoinState(state cv2.NodeJoinState) NodeJoinState {
	switch state {
	case cv2.NodeJoinStateActive:
		return NodeJoinStateActive
	case cv2.NodeJoinStateJoining:
		return NodeJoinStateJoining
	case cv2.NodeJoinStateLeaving:
		return NodeJoinStateLeaving
	case cv2.NodeJoinStateRemoved:
		return NodeJoinStateRemoved
	default:
		return NodeJoinStateRemoved
	}
}
```

- [ ] **Step 10: Make snapshot validation lifecycle-aware**

In `pkg/clusterv2/control/snapshot_validate.go`, add:

```go
func effectiveJoinState(state NodeJoinState) NodeJoinState {
	if state == "" {
		return NodeJoinStateActive
	}
	return state
}

func validJoinState(state NodeJoinState) bool {
	switch effectiveJoinState(state) {
	case NodeJoinStateActive, NodeJoinStateJoining, NodeJoinStateLeaving, NodeJoinStateRemoved:
		return true
	default:
		return false
	}
}
```

Then update the node loop:

```go
			if !validJoinState(node.JoinState) {
				return fmt.Errorf("control snapshot: unknown node join_state")
			}
			if hasRole(node.Roles, RoleData) && effectiveJoinState(node.JoinState) == NodeJoinStateActive {
				dataNodes[node.NodeID] = struct{}{}
			}
```

This keeps older injected test snapshots with empty join state behaving as active while real ControllerV2 snapshots carry explicit lifecycle state.

- [ ] **Step 11: Run clusterv2 control tests and verify GREEN**

Run:

```bash
go test ./pkg/clusterv2/control -run 'TestControllerV2SnapshotMapping|TestControllerV2SnapshotMappingPreservesNonActiveLifecycle|TestSnapshotValidateRejectsSlotPeerThatIsNotActive' -count=1
```

Expected: PASS.

- [ ] **Step 12: Run broader state/control tests**

Run:

```bash
go test ./pkg/controllerv2/state ./pkg/clusterv2/control -count=1
```

Expected: PASS.

- [ ] **Step 13: Commit Task 1**

```bash
git add pkg/controllerv2/state/types.go pkg/controllerv2/state/validate.go pkg/controllerv2/state/state_test.go pkg/clusterv2/control/snapshot.go pkg/clusterv2/control/controllerv2.go pkg/clusterv2/control/snapshot_validate.go pkg/clusterv2/control/controllerv2_test.go pkg/clusterv2/control/control_test.go
git commit -m "feat: carry node lifecycle in control snapshots"
```

## Task 2: Add Seed-Join Config Foundation Without Bootstrap Bypass

**Files:**
- Modify: `pkg/clusterv2/config.go`
- Modify: `pkg/clusterv2/config_test.go`
- Modify: `cmd/wukongimv2/config.go`
- Modify: `cmd/wukongimv2/config_test.go`

- [ ] **Step 1: Write failing clusterv2 seed-join config tests**

Append to `pkg/clusterv2/config_test.go`:

```go
func TestConfigSeedJoinModeUsesMirrorAndDisablesBootstrap(t *testing.T) {
	cfg := Config{
		NodeID:     4,
		ListenAddr: "127.0.0.1:7014",
		DataDir:    t.TempDir(),
		Control: ControlConfig{
			ClusterID: "dev-three",
		},
		Join: JoinConfig{
			Seeds:         []string{"127.0.0.1:7011", "127.0.0.1:7012"},
			AdvertiseAddr: "127.0.0.1:7014",
			Token:         "join-secret",
		},
		Slots: SlotConfig{ReplicaCount: 3},
	}
	cfg.applyDefaults()

	if cfg.Control.Role != ControlRoleMirror {
		t.Fatalf("Control.Role = %q, want mirror", cfg.Control.Role)
	}
	if cfg.Control.AllowBootstrap {
		t.Fatal("Control.AllowBootstrap = true, want false for seed-join mode")
	}
	if len(cfg.Control.Voters) != 0 {
		t.Fatalf("Control.Voters = %#v, want no implicit voters in seed-join mode", cfg.Control.Voters)
	}
	if err := cfg.validate(); err != nil {
		t.Fatalf("validate() error = %v", err)
	}
}

func TestConfigRejectsSeedJoinMissingAdvertiseAddr(t *testing.T) {
	cfg := Config{
		NodeID:     4,
		ListenAddr: "127.0.0.1:7014",
		DataDir:    t.TempDir(),
		Control:    ControlConfig{ClusterID: "dev-three"},
		Join:       JoinConfig{Seeds: []string{"127.0.0.1:7011"}, Token: "join-secret"},
	}
	cfg.applyDefaults()
	if err := cfg.validate(); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("validate() error = %v, want ErrInvalidConfig", err)
	}
}
```

- [ ] **Step 2: Run clusterv2 config tests and verify RED**

Run:

```bash
go test ./pkg/clusterv2 -run 'TestConfigSeedJoinModeUsesMirrorAndDisablesBootstrap|TestConfigRejectsSeedJoinMissingAdvertiseAddr' -count=1
```

Expected: FAIL because `JoinConfig` does not exist.

- [ ] **Step 3: Add join config fields and seed-join mode helpers**

In `pkg/clusterv2/config.go`, add this type near `ControlConfig`:

```go
// JoinConfig contains dynamic data-node join bootstrap settings.
type JoinConfig struct {
	// Seeds lists reachable existing node addresses used before membership discovery is available.
	Seeds []string
	// AdvertiseAddr is the stable RPC address this node asks the cluster to store for membership.
	AdvertiseAddr string
	// Token authenticates the join request before the node becomes a durable member.
	Token string
}
```

Add this field to `Config`:

```go
	// Join contains dynamic data-node join bootstrap settings.
	Join JoinConfig
```

Add helpers:

```go
func (c Config) seedJoinMode() bool {
	return len(c.Join.Seeds) > 0
}
```

- [ ] **Step 4: Disable implicit bootstrap in seed-join mode**

In `applyControlDefaults`, place this block after `StateDir` defaulting and before the default voter role:

```go
	if c.seedJoinMode() {
		if c.Control.Role == "" {
			c.Control.Role = ControlRoleMirror
		}
		c.Control.AllowBootstrap = false
		return
	}
```

- [ ] **Step 5: Validate seed-join mode separately from static Controller voters**

At the top of `validateControl`, after the state-dir/cluster-id check, add:

```go
	if c.seedJoinMode() {
		if c.Control.Role != ControlRoleMirror || c.Control.AllowBootstrap || len(c.Control.Voters) != 0 {
			return ErrInvalidConfig
		}
		if c.Join.AdvertiseAddr == "" || c.Join.Token == "" {
			return ErrInvalidConfig
		}
		for _, seed := range c.Join.Seeds {
			if seed == "" {
				return ErrInvalidConfig
			}
		}
		return nil
	}
```

Leave the existing static voter validation path unchanged.

- [ ] **Step 6: Decouple seed-join replica validation from Controller voters**

In `validateSlots`, replace:

```go
	if int(c.Slots.ReplicaCount) > len(c.Control.Voters) {
		return ErrInvalidConfig
	}
```

with:

```go
	if !c.seedJoinMode() && int(c.Slots.ReplicaCount) > len(c.Control.Voters) {
		return ErrInvalidConfig
	}
```

Static bootstrap still checks Controller voter capacity. Seed-join nodes defer
active data-node capacity validation to the control snapshot they sync from the
existing cluster.

- [ ] **Step 7: Run clusterv2 config tests and verify GREEN**

Run:

```bash
go test ./pkg/clusterv2 -run 'TestConfigSeedJoinModeUsesMirrorAndDisablesBootstrap|TestConfigRejectsSeedJoinMissingAdvertiseAddr|TestConfigDefaultsSingleNodeControl|TestConfigRejectsVoterRoleMissingLocalNode' -count=1
```

Expected: PASS.

- [ ] **Step 8: Write failing cmd/wukongimv2 seed config tests**

Add to `cmd/wukongimv2/config_test.go` near the static cluster config tests:

```go
func TestLoadConfigSeedJoinMode(t *testing.T) {
	unsetLoadConfigEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "wukongim.conf")
	writeConf(t, path,
		"WK_NODE_ID=4",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-4"),
		"WK_CLUSTER_LISTEN_ADDR=0.0.0.0:7014",
		"WK_CLUSTER_ID=dev-three",
		`WK_CLUSTER_SEEDS=["127.0.0.1:7011","127.0.0.1:7012"]`,
		"WK_CLUSTER_ADVERTISE_ADDR=127.0.0.1:7014",
		"WK_CLUSTER_JOIN_TOKEN=join-secret",
		"WK_CLUSTER_SLOT_REPLICA_N=3",
	)

	cfg, err := loadConfig([]string{"-config", path})
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}
	if cfg.Cluster.Control.Role != clusterv2.ControlRoleMirror {
		t.Fatalf("Control.Role = %q, want mirror", cfg.Cluster.Control.Role)
	}
	if cfg.Cluster.Control.AllowBootstrap {
		t.Fatal("Control.AllowBootstrap = true, want false")
	}
	if len(cfg.Cluster.Control.Voters) != 0 {
		t.Fatalf("Control.Voters = %#v, want none", cfg.Cluster.Control.Voters)
	}
	if got := cfg.Cluster.Join.Seeds; len(got) != 2 || got[0] != "127.0.0.1:7011" || got[1] != "127.0.0.1:7012" {
		t.Fatalf("Join.Seeds = %#v", got)
	}
	if cfg.Cluster.Join.AdvertiseAddr != "127.0.0.1:7014" || cfg.Cluster.Join.Token != "join-secret" {
		t.Fatalf("Join = %#v", cfg.Cluster.Join)
	}
}

func TestLoadConfigRejectsSeedJoinWithStaticNodes(t *testing.T) {
	unsetLoadConfigEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "wukongim.conf")
	writeConf(t, path,
		"WK_NODE_ID=4",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-4"),
		"WK_CLUSTER_LISTEN_ADDR=0.0.0.0:7014",
		"WK_CLUSTER_ID=dev-three",
		`WK_CLUSTER_SEEDS=["127.0.0.1:7011"]`,
		"WK_CLUSTER_ADVERTISE_ADDR=127.0.0.1:7014",
		"WK_CLUSTER_JOIN_TOKEN=join-secret",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7011"}]`,
	)

	_, err := loadConfig([]string{"-config", path})
	if err == nil || !strings.Contains(err.Error(), "WK_CLUSTER_SEEDS") {
		t.Fatalf("loadConfig() error = %v, want WK_CLUSTER_SEEDS conflict", err)
	}
}
```

Also add invalid seed parsing to the table that already checks parse errors:

```go
{name: "cluster seeds json", line: "WK_CLUSTER_SEEDS=not-json", wantKey: "WK_CLUSTER_SEEDS"},
```

- [ ] **Step 9: Run cmd/wukongimv2 seed config tests and verify RED**

Run:

```bash
go test ./cmd/wukongimv2 -run 'TestLoadConfigSeedJoinMode|TestLoadConfigRejectsSeedJoinWithStaticNodes|TestLoadConfigRejectsInvalidValues' -count=1
```

Expected: FAIL because the new keys are unsupported and not parsed.

- [ ] **Step 10: Parse the new config keys in cmd/wukongimv2**

In `cmd/wukongimv2/config.go`, add these keys to `supportedConfigKeys`:

```go
	"WK_CLUSTER_SEEDS",
	"WK_CLUSTER_ADVERTISE_ADDR",
	"WK_CLUSTER_JOIN_TOKEN",
```

Add a helper near `parseClusterNodes`:

```go
func parseClusterSeeds(raw string) ([]string, error) {
	var seeds []string
	if err := json.Unmarshal([]byte(raw), &seeds); err != nil {
		return nil, fmt.Errorf("parse WK_CLUSTER_SEEDS as JSON: %w", err)
	}
	for i, seed := range seeds {
		seeds[i] = strings.TrimSpace(seed)
		if seeds[i] == "" {
			return nil, fmt.Errorf("parse WK_CLUSTER_SEEDS: seed address must not be empty")
		}
	}
	return seeds, nil
}
```

In `buildConfig`, before the `WK_CLUSTER_NODES` block, parse seeds:

```go
	if raw := configValue(values, "WK_CLUSTER_SEEDS"); raw != "" {
		seeds, err := parseClusterSeeds(raw)
		if err != nil {
			return app.Config{}, err
		}
		cfg.Cluster.Join.Seeds = seeds
		cfg.Cluster.Join.AdvertiseAddr = configValue(values, "WK_CLUSTER_ADVERTISE_ADDR")
		cfg.Cluster.Join.Token = configValue(values, "WK_CLUSTER_JOIN_TOKEN")
		cfg.Cluster.Control.Role = clusterv2.ControlRoleMirror
		cfg.Cluster.Control.AllowBootstrap = false
		if configValue(values, "WK_CLUSTER_NODES") != "" {
			return app.Config{}, fmt.Errorf("load config: WK_CLUSTER_SEEDS cannot be combined with WK_CLUSTER_NODES")
		}
	}
```

Keep the existing `WK_CLUSTER_NODES` block for static bootstrap only.

- [ ] **Step 11: Run cmd/wukongimv2 config tests and verify GREEN**

Run:

```bash
go test ./cmd/wukongimv2 -run 'TestLoadConfigSeedJoinMode|TestLoadConfigRejectsSeedJoinWithStaticNodes|TestLoadConfigRejectsInvalidValues|TestLoadConfigStaticMultiNodeCluster|TestLoadConfigDerivesStaticMultiNodeClusterID' -count=1
```

Expected: PASS.

- [ ] **Step 12: Run broader config tests**

Run:

```bash
go test ./pkg/clusterv2 ./cmd/wukongimv2 -run 'Config|LoadConfig' -count=1
```

Expected: PASS.

- [ ] **Step 13: Commit Task 2**

```bash
git add pkg/clusterv2/config.go pkg/clusterv2/config_test.go cmd/wukongimv2/config.go cmd/wukongimv2/config_test.go
git commit -m "feat: add seed join config foundation"
```

## Task 3: Exclude Non-Active Nodes From Placement Candidate Views

**Files:**
- Modify: `pkg/clusterv2/node_snapshot.go`
- Modify: `pkg/clusterv2/node_snapshot_test.go`
- Modify: `internalv2/usecase/management/slot_leader_transfer.go`
- Modify: `internalv2/usecase/management/slot_leader_transfer_batch.go`
- Modify: existing related tests if candidate helpers are made lifecycle-aware.

- [ ] **Step 1: Write failing ChannelV2 candidate test**

In `pkg/clusterv2/node_snapshot_test.go`, update `TestNodeAppliesAliveDataNodesForChannelPlacement`. Keep the initial expectation at `{1,2,3}`, then replace the appended nodes with:

```go
	next.Nodes = append(next.Nodes,
		control.Node{NodeID: 4, Addr: "127.0.0.1:1004", Roles: []control.Role{control.RoleData}, Status: control.NodeAlive, JoinState: control.NodeJoinStateActive},
		control.Node{NodeID: 5, Addr: "127.0.0.1:1005", Roles: []control.Role{control.RoleData}, Status: control.NodeAlive, JoinState: control.NodeJoinStateJoining},
		control.Node{NodeID: 6, Addr: "127.0.0.1:1006", Roles: []control.Role{control.RoleData}, Status: control.NodeAlive, JoinState: control.NodeJoinStateLeaving},
		control.Node{NodeID: 7, Addr: "127.0.0.1:1007", Roles: []control.Role{control.RoleData}, Status: control.NodeAlive, JoinState: control.NodeJoinStateRemoved},
		control.Node{NodeID: 8, Addr: "127.0.0.1:1008", Roles: []control.Role{control.RoleData}, Status: control.NodeSuspect, JoinState: control.NodeJoinStateActive},
	)
```

Keep the final expected data nodes:

```go
return equalUint64s(got, []uint64{1, 2, 3, 4, 8})
```

- [ ] **Step 2: Run candidate test and verify RED**

Run:

```bash
go test ./pkg/clusterv2 -run TestNodeAppliesAliveDataNodesForChannelPlacement -count=1
```

Expected: FAIL because joining/leaving/removed data nodes are still included.

- [ ] **Step 3: Rename and update the candidate helper**

In `pkg/clusterv2/node_snapshot.go`, rename the helper call:

```go
			n.channelDataNodes.Update(activeDataNodeIDs(snapshot.Nodes))
```

Replace `aliveDataNodeIDs` with:

```go
func activeDataNodeIDs(nodes []control.Node) []uint64 {
	out := make([]uint64, 0, len(nodes))
	for _, node := range nodes {
		if !hasControlRole(node.Roles, control.RoleData) {
			continue
		}
		if controlNodeJoinState(node.JoinState) != control.NodeJoinStateActive {
			continue
		}
		out = append(out, node.NodeID)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func controlNodeJoinState(state control.NodeJoinState) control.NodeJoinState {
	if state == "" {
		return control.NodeJoinStateActive
	}
	return state
}
```

Update the comment in `pkg/clusterv2/node.go` from "alive data-role nodes" to "active data-role nodes". Add a comment above `activeDataNodeIDs`:

```go
// activeDataNodeIDs intentionally ignores NodeStatus until health reports have freshness.
```

- [ ] **Step 4: Run candidate tests and verify GREEN**

Run:

```bash
go test ./pkg/clusterv2 -run 'TestNodeAppliesAliveDataNodesForChannelPlacement|TestNodeControlWatchNodeOnlyChangeSkipsSlotReconcile' -count=1
```

Expected: PASS.

- [ ] **Step 5: Write failing manager transfer candidate tests**

Find existing leader-transfer tests that mark node status suspect. Add one case in `internalv2/usecase/management/slot_leader_transfer_test.go` where the target has the data role but `JoinState: control.NodeJoinStateJoining`, and assert the request is rejected:

```go
func TestRequestSlotLeaderTransferRejectsJoiningTarget(t *testing.T) {
	app, writer := newSlotLeaderTransferTestApp()
	app.cluster = fakeSlotTransferSnapshotReader{snapshot: slotLeaderTransferSnapshot(func(snapshot *control.Snapshot) {
		snapshot.Nodes[1].JoinState = control.NodeJoinStateJoining
	})}

	_, err := app.RequestSlotLeaderTransfer(context.Background(), SlotLeaderTransferInput{
		SlotID:        1,
		TargetNodeID: 2,
	})
	if err == nil {
		t.Fatal("RequestSlotLeaderTransfer() error = nil, want joining target rejection")
	}
	if len(writer.requests) != 0 {
		t.Fatalf("writer requests = %#v, want none", writer.requests)
	}
}
```

If helper names differ, use the existing helper names in that file and keep the same assertion.

- [ ] **Step 6: Run manager transfer tests and verify RED**

Run:

```bash
go test ./internalv2/usecase/management -run 'TestRequestSlotLeaderTransferRejectsJoiningTarget|TestRequestSlotLeaderTransferRejectsUnavailableTarget' -count=1
```

Expected: FAIL because transfer target filtering only checks data role and ignores lifecycle.

- [ ] **Step 7: Add a shared active-data predicate in management**

In `internalv2/usecase/management/slot_leader_transfer.go`, add:

```go
func isActiveDataNode(node control.Node) bool {
	return hasRole(node.Roles, control.RoleData) &&
		managerControlJoinState(node.JoinState) == control.NodeJoinStateActive
}

func managerControlJoinState(state control.NodeJoinState) control.NodeJoinState {
	if state == "" {
		return control.NodeJoinStateActive
	}
	return state
}
```

Replace local checks shaped like:

```go
hasRole(node.Roles, control.RoleData)
```

with:

```go
isActiveDataNode(node)
```

Apply the same predicate in `internalv2/usecase/management/slot_leader_transfer_batch.go`.

- [ ] **Step 8: Run manager transfer tests and verify GREEN**

Run:

```bash
go test ./internalv2/usecase/management -run 'SlotLeaderTransfer' -count=1
```

Expected: PASS.

- [ ] **Step 9: Run broader placement-related tests**

Run:

```bash
go test ./pkg/clusterv2 ./internalv2/usecase/management -run 'Node|SlotLeaderTransfer' -count=1
```

Expected: PASS.

- [ ] **Step 10: Commit Task 3**

```bash
git add pkg/clusterv2/node.go pkg/clusterv2/node_snapshot.go pkg/clusterv2/node_snapshot_test.go internalv2/usecase/management/slot_leader_transfer.go internalv2/usecase/management/slot_leader_transfer_batch.go internalv2/usecase/management/slot_leader_transfer_test.go
git commit -m "feat: filter placement candidates by node lifecycle"
```

## Task 4: Project Real Lifecycle And Capacity In Manager Node List

**Files:**
- Modify: `internalv2/usecase/management/nodes.go`
- Modify: `internalv2/usecase/management/nodes_test.go`

- [ ] **Step 1: Write failing manager node lifecycle projection test**

Add to `internalv2/usecase/management/nodes_test.go`:

```go
func TestListNodesReportsLifecycleAndCapacity(t *testing.T) {
	app := New(Options{
		Cluster: fakeNodeSnapshotReader{
			nodeID: 1,
			snapshot: control.Snapshot{
				ControllerID: 1,
				Nodes: []control.Node{
					{NodeID: 1, Addr: "127.0.0.1:7011", Roles: []control.Role{control.RoleController, control.RoleData}, Status: control.NodeAlive, JoinState: control.NodeJoinStateActive, CapacityWeight: 3},
					{NodeID: 2, Addr: "127.0.0.1:7012", Roles: []control.Role{control.RoleData}, Status: control.NodeAlive, JoinState: control.NodeJoinStateJoining, CapacityWeight: 2},
					{NodeID: 3, Addr: "127.0.0.1:7013", Roles: []control.Role{control.RoleData}, Status: control.NodeAlive, JoinState: control.NodeJoinStateLeaving, CapacityWeight: 1},
					{NodeID: 4, Addr: "127.0.0.1:7014", Roles: []control.Role{control.RoleData}, Status: control.NodeDown, JoinState: control.NodeJoinStateRemoved, CapacityWeight: 1},
				},
			},
		},
	})

	got, err := app.ListNodes(context.Background())
	if err != nil {
		t.Fatalf("ListNodes() error = %v", err)
	}
	if len(got.Items) != 4 {
		t.Fatalf("Items len = %d, want 4", len(got.Items))
	}
	want := map[uint64]struct {
		joinState   string
		schedulable bool
		capacity    int
	}{
		1: {joinState: "active", schedulable: true, capacity: 3},
		2: {joinState: "joining", schedulable: false, capacity: 2},
		3: {joinState: "leaving", schedulable: false, capacity: 1},
		4: {joinState: "removed", schedulable: false, capacity: 1},
	}
	for _, item := range got.Items {
		expect := want[item.NodeID]
		if item.Membership.JoinState != expect.joinState || item.Membership.Schedulable != expect.schedulable || item.CapacityWeight != expect.capacity {
			t.Fatalf("node %d membership=%#v capacity=%d, want %#v", item.NodeID, item.Membership, item.CapacityWeight, expect)
		}
	}
}
```

- [ ] **Step 2: Run manager node lifecycle test and verify RED**

Run:

```bash
go test ./internalv2/usecase/management -run TestListNodesReportsLifecycleAndCapacity -count=1
```

Expected: FAIL because `buildNode` hard-codes join state to active and capacity to 1.

- [ ] **Step 3: Add manager lifecycle helpers**

In `internalv2/usecase/management/nodes.go`, add:

```go
func managerNodeJoinState(state control.NodeJoinState) string {
	switch managerControlJoinState(state) {
	case control.NodeJoinStateActive:
		return "active"
	case control.NodeJoinStateJoining:
		return "joining"
	case control.NodeJoinStateLeaving:
		return "leaving"
	case control.NodeJoinStateRemoved:
		return "removed"
	default:
		return "unknown"
	}
}

func managerNodeCapacityWeight(weight uint32) int {
	if weight == 0 {
		return 1
	}
	return int(weight)
}
```

- [ ] **Step 4: Use real lifecycle fields in `buildNode`**

In `buildNode`, compute lifecycle values before returning:

```go
	joinState := managerNodeJoinState(opts.node.JoinState)
	schedulable := role == "data" && joinState == "active"
```

Then replace:

```go
			CapacityWeight:  1,
```

with:

```go
			CapacityWeight:  managerNodeCapacityWeight(opts.node.CapacityWeight),
```

and replace the membership block with:

```go
			Membership: NodeMembership{
				Role:        role,
				JoinState:   joinState,
				Schedulable: schedulable,
			},
```

- [ ] **Step 5: Run manager node tests and verify GREEN**

Run:

```bash
go test ./internalv2/usecase/management -run 'TestListNodesBuildsReadOnlyNodeInventory|TestListNodesReportsLifecycleAndCapacity|TestListNodesAttachesRuntimeSummary' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit Task 4**

```bash
git add internalv2/usecase/management/nodes.go internalv2/usecase/management/nodes_test.go
git commit -m "feat: expose node lifecycle in manager inventory"
```

## Task 5: Update v2 Config Examples And Run Focused Verification

**Files:**
- Modify: `cmd/wukongimv2/wukongimv2.conf.example`
- Modify: `cmd/wukongimv2/wukongimv2-node1.conf.example`
- Modify: `cmd/wukongimv2/wukongimv2-node2.conf.example`
- Modify: `cmd/wukongimv2/wukongimv2-node3.conf.example`
- Modify: `scripts/wukongimv2/wukongimv2.conf`
- Modify: `scripts/wukongimv2/wukongimv2-node1.conf`
- Modify: `scripts/wukongimv2/wukongimv2-node2.conf`
- Modify: `scripts/wukongimv2/wukongimv2-node3.conf`
- Modify: `cmd/wukongimv2/README.md`

- [ ] **Step 1: Update static example comments**

In each static v2 example file, keep `WK_CLUSTER_NODES` and add this comment above it:

```conf
# Static Controller voter bootstrap. Existing bootstrap nodes keep this full list.
# New dynamic data nodes must use WK_CLUSTER_SEEDS instead of WK_CLUSTER_NODES.
```

Do not remove static three-node examples; Stage 1 preserves static bootstrap.

- [ ] **Step 2: Create a seed-join example file**

Create `cmd/wukongimv2/wukongimv2-join-node.conf.example` with:

```conf
# WuKongIM v2 dynamic data-node join example.
#
# Start the existing static Controller voter cluster first. This joining node
# uses seed discovery and must not set WK_CLUSTER_NODES.

# Stable non-zero node identity. It must not collide with existing members.
WK_NODE_ID=4

# Root data directory for this joining node.
WK_NODE_DATA_DIR=./data/wukongimv2-node-4

# Local node-to-node clusterv2 RPC listen address.
WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7014

# Stable cluster identity shared with the existing cluster.
WK_CLUSTER_ID=wukongimv2-dev-three

# Existing reachable nodes used only for startup discovery before membership is synced.
WK_CLUSTER_SEEDS=["127.0.0.1:7011","127.0.0.1:7012"]

# Stable address that other nodes should use to call this joining node.
WK_CLUSTER_ADVERTISE_ADDR=127.0.0.1:7014

# Shared join secret used by the future JoinNode request path.
WK_CLUSTER_JOIN_TOKEN=change-me

# The joining node must use the same hash-slot count as the existing cluster.
WK_CLUSTER_HASH_SLOT_COUNT=16

# Replica count is read for local validation and must not exceed existing active data capacity.
WK_CLUSTER_SLOT_REPLICA_N=3

# Optional API and manager listeners for local inspection.
WK_API_LISTEN_ADDR=127.0.0.1:5014
WK_MANAGER_LISTEN_ADDR=127.0.0.1:5314
```

- [ ] **Step 3: Update README with the seed/static distinction**

In `cmd/wukongimv2/README.md`, add:

```md
Static bootstrap nodes use `WK_CLUSTER_NODES` to form the initial Controller
voter set. A future dynamic data node uses `WK_CLUSTER_SEEDS`,
`WK_CLUSTER_ADVERTISE_ADDR`, and `WK_CLUSTER_JOIN_TOKEN` instead; it must not set
`WK_CLUSTER_NODES`, because seed-join mode runs as a Controller mirror and does
not bootstrap a new voter set.
```

- [ ] **Step 4: Run focused package tests**

Run:

```bash
go test ./pkg/controllerv2/state ./pkg/clusterv2/control ./pkg/clusterv2 ./internalv2/usecase/management ./cmd/wukongimv2 -count=1
```

Expected: PASS.

- [ ] **Step 5: Run config/example grep checks**

Run:

```bash
rg -n 'WK_CLUSTER_SEEDS|WK_CLUSTER_ADVERTISE_ADDR|WK_CLUSTER_JOIN_TOKEN|WK_CLUSTER_NODES' cmd/wukongimv2 scripts/wukongimv2
```

Expected:
- Static node examples still include `WK_CLUSTER_NODES`.
- The join-node example includes `WK_CLUSTER_SEEDS`, `WK_CLUSTER_ADVERTISE_ADDR`, and `WK_CLUSTER_JOIN_TOKEN`.
- The join-node example does not include an uncommented `WK_CLUSTER_NODES=`.

- [ ] **Step 6: Run formatting and diff checks**

Run:

```bash
gofmt -w pkg/controllerv2/state/types.go pkg/controllerv2/state/validate.go pkg/controllerv2/state/state_test.go pkg/clusterv2/control/snapshot.go pkg/clusterv2/control/controllerv2.go pkg/clusterv2/control/snapshot_validate.go pkg/clusterv2/control/controllerv2_test.go pkg/clusterv2/control/control_test.go pkg/clusterv2/config.go pkg/clusterv2/config_test.go pkg/clusterv2/node.go pkg/clusterv2/node_snapshot.go pkg/clusterv2/node_snapshot_test.go internalv2/usecase/management/nodes.go internalv2/usecase/management/nodes_test.go internalv2/usecase/management/slot_leader_transfer.go internalv2/usecase/management/slot_leader_transfer_batch.go internalv2/usecase/management/slot_leader_transfer_test.go cmd/wukongimv2/config.go cmd/wukongimv2/config_test.go
git diff --check
```

Expected: `gofmt` exits 0 and `git diff --check` prints no output.

- [ ] **Step 7: Commit Task 5**

```bash
git add cmd/wukongimv2/wukongimv2.conf.example cmd/wukongimv2/wukongimv2-node1.conf.example cmd/wukongimv2/wukongimv2-node2.conf.example cmd/wukongimv2/wukongimv2-node3.conf.example cmd/wukongimv2/wukongimv2-join-node.conf.example scripts/wukongimv2/wukongimv2.conf scripts/wukongimv2/wukongimv2-node1.conf scripts/wukongimv2/wukongimv2-node2.conf scripts/wukongimv2/wukongimv2-node3.conf cmd/wukongimv2/README.md
git commit -m "docs: document v2 seed join config"
```

## Final Verification

- [ ] **Step 1: Run focused Stage 1 tests**

```bash
go test ./pkg/controllerv2/state ./pkg/clusterv2/control ./pkg/clusterv2 ./internalv2/usecase/management ./cmd/wukongimv2 -count=1
```

Expected: PASS.

- [ ] **Step 2: Run repository hygiene checks for touched files**

```bash
git diff --check
rg -n 'T[B]D|T[O]DO|F[I]XME|待[定]|后续再[说]' docs/superpowers/plans/2026-06-24-internalv2-dynamic-node-stage1.md docs/superpowers/specs/2026-06-24-internalv2-dynamic-node-lifecycle-design.md
```

Expected: `git diff --check` prints no output. The `rg` command prints no output.

- [ ] **Step 3: Inspect commits and branch status**

```bash
git log --oneline --decorate -6
git status --short
```

Expected: Stage 1 commits are visible, and only intentional local changes remain.

## Handoff To Stage 2

After Stage 1 lands, continue with:

- `docs/superpowers/plans/2026-06-24-internalv2-dynamic-node-stage2.md`

Do not begin Stage 2 implementation from this plan. Stage 2 has its own entry gate and verification commands.
