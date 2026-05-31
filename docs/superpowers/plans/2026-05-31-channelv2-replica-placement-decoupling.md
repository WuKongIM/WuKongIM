# ChannelV2 Replica Placement Decoupling Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Decouple initial ChannelV2 replica selection from Slot metadata peers while keeping current three-node behavior operationally compatible.

**Architecture:** Slot routing remains the metadata ownership path. A new data-node placement view feeds `SlotPlacementResolver`, which selects ChannelV2 replicas from alive data nodes using deterministic rendezvous ranking instead of `routing.Route.Peers`.

**Tech Stack:** Go, `pkg/clusterv2`, `pkg/clusterv2/channels`, `pkg/clusterv2/control`, `pkg/clusterv2/routing`, `pkg/channelv2`, `go test`, local `wkbench` three-node script.

---

## File Structure

- Modify `pkg/clusterv2/config.go`: add `ChannelConfig.ReplicaCount`, default it from `Slots.ReplicaCount`, validate it.
- Modify `pkg/clusterv2/node.go`: add a small data-node provider field to `Node`.
- Modify `pkg/clusterv2/node_snapshot.go`: derive alive data nodes from control snapshots and update the provider during snapshot apply.
- Modify `pkg/clusterv2/node_defaults.go`: pass the data-node provider and channel replica count into default ChannelV2 placement.
- Modify `pkg/clusterv2/channels/meta.go`: add the `DataNodeProvider` interface and expand resolver construction surface.
- Modify `pkg/clusterv2/channels/placement.go`: rank data-node candidates and choose ChannelV2 replicas independently of Slot peers.
- Modify `pkg/clusterv2/channels/channels_test.go`: replace Slot-peer placement tests with data-node placement tests.
- Modify `pkg/clusterv2/node_defaults_test.go`: cover default replica count and data-node view wiring.
- Modify `pkg/clusterv2/FLOW.md`: document that Slot peers are metadata peers and Channel replicas are data-plane placement.
- Modify `docs/development/PROJECT_KNOWLEDGE.md`: add one concise ChannelV2 placement rule.

## Task 1: Channel Replica Count Configuration

**Files:**
- Modify: `pkg/clusterv2/config.go`
- Test: `pkg/clusterv2/node_defaults_test.go`

- [ ] **Step 1: Write the failing tests**

Add this test near `TestDefaultChannelMinISRUsesSlotReplicaMajority` in `pkg/clusterv2/node_defaults_test.go`:

```go
func TestChannelReplicaCountDefaultsToSlotReplicaCount(t *testing.T) {
	cfg := Config{}
	cfg.Control.Voters = []ControlVoter{
		{NodeID: 1, Addr: "127.0.0.1:1001"},
		{NodeID: 2, Addr: "127.0.0.1:1002"},
		{NodeID: 3, Addr: "127.0.0.1:1003"},
	}
	cfg.Slots.ReplicaCount = 3
	cfg.applyDefaults()
	if cfg.Channel.ReplicaCount != 3 {
		t.Fatalf("Channel.ReplicaCount = %d, want slot replica count 3", cfg.Channel.ReplicaCount)
	}
}

func TestChannelReplicaCountPreservesExplicitValue(t *testing.T) {
	cfg := Config{}
	cfg.Control.Voters = []ControlVoter{
		{NodeID: 1, Addr: "127.0.0.1:1001"},
		{NodeID: 2, Addr: "127.0.0.1:1002"},
		{NodeID: 3, Addr: "127.0.0.1:1003"},
	}
	cfg.Slots.ReplicaCount = 3
	cfg.Channel.ReplicaCount = 2
	cfg.applyDefaults()
	if cfg.Channel.ReplicaCount != 2 {
		t.Fatalf("Channel.ReplicaCount = %d, want explicit value 2", cfg.Channel.ReplicaCount)
	}
}
```

- [ ] **Step 2: Run the tests and verify they fail**

Run:

```bash
GOWORK=off go test -count=1 ./pkg/clusterv2 -run 'TestChannelReplicaCount' 
```

Expected: compile failure because `ChannelConfig.ReplicaCount` does not exist.

- [ ] **Step 3: Add the configuration field and defaults**

In `pkg/clusterv2/config.go`, update `ChannelConfig`:

```go
// ReplicaCount is the desired ChannelV2 data replica count for newly created channels.
// Zero defaults to Slots.ReplicaCount.
ReplicaCount uint16
```

In `applyDefaults`, after `c.applySlotDefaults()` or immediately after it is called, set the default:

```go
if c.Channel.ReplicaCount == 0 {
	c.Channel.ReplicaCount = c.Slots.ReplicaCount
}
```

Keep `Slots.ReplicaCount` defaulting before this assignment.

- [ ] **Step 4: Validate the new field**

In `Config.validate`, add:

```go
if c.Channel.ReplicaCount == 0 {
	return ErrInvalidConfig
}
```

Do not reject values greater than node count in config validation; placement will validate against the current control snapshot.

- [ ] **Step 5: Run the focused tests**

Run:

```bash
GOWORK=off go test -count=1 ./pkg/clusterv2 -run 'TestChannelReplicaCount'
```

Expected: tests pass.

- [ ] **Step 6: Commit**

```bash
git add pkg/clusterv2/config.go pkg/clusterv2/node_defaults_test.go
git commit -m "feat(clusterv2): configure channel replica count"
```

## Task 2: Data Node Snapshot Provider

**Files:**
- Modify: `pkg/clusterv2/node.go`
- Add: `pkg/clusterv2/channel_data_nodes.go`
- Modify: `pkg/clusterv2/node_snapshot.go`
- Test: `pkg/clusterv2/node_snapshot_test.go`

- [ ] **Step 1: Write the failing test**

Add this test to `pkg/clusterv2/node_snapshot_test.go`:

```go
func TestNodeAppliesAliveDataNodesForChannelPlacement(t *testing.T) {
	controller := control.NewStaticController(nodeControlSnapshot())
	node, err := New(validNodeConfig(t), withController(controller), withSlotReconciler(&recordingReconciler{}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = node.Stop(context.Background()) })

	got := node.channelDataNodes.DataNodes()
	want := []uint64{1, 2, 3}
	if !equalUint64s(got, want) {
		t.Fatalf("DataNodes() = %v, want %v", got, want)
	}

	next := nodeControlSnapshot()
	next.Revision = 2
	next.Nodes = append(next.Nodes,
		control.Node{NodeID: 4, Addr: "127.0.0.1:1004", Roles: []control.Role{control.RoleData}, Status: control.NodeAlive},
		control.Node{NodeID: 5, Addr: "127.0.0.1:1005", Roles: []control.Role{control.RoleData}, Status: control.NodeDown},
	)
	if err := controller.Publish(next); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	waitUntil(t, func() bool {
		got = node.channelDataNodes.DataNodes()
		return equalUint64s(got, []uint64{1, 2, 3, 4})
	})
}

func equalUint64s(a []uint64, b []uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
```

If `equalUint64s` already exists in the package, reuse it instead of duplicating it.

- [ ] **Step 2: Run the test and verify it fails**

Run:

```bash
GOWORK=off go test -count=1 ./pkg/clusterv2 -run TestNodeAppliesAliveDataNodesForChannelPlacement
```

Expected: compile failure because `node.channelDataNodes` does not exist.

- [ ] **Step 3: Add a small provider type**

In `pkg/clusterv2/node.go`, add a field to `Node`:

```go
channelDataNodes dataNodeView
```

Create `pkg/clusterv2/channel_data_nodes.go` with package `clusterv2` and a `sync` import:

```go
type dataNodeView struct {
	mu    sync.RWMutex
	nodes []uint64
}

func (v *dataNodeView) Update(nodes []uint64) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.nodes = append([]uint64(nil), nodes...)
}

func (v *dataNodeView) DataNodes() []uint64 {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return append([]uint64(nil), v.nodes...)
}
```

- [ ] **Step 4: Populate the provider from snapshots**

In `pkg/clusterv2/node_snapshot.go`, add:

```go
func aliveDataNodeIDs(nodes []control.Node) []uint64 {
	out := make([]uint64, 0, len(nodes))
	for _, node := range nodes {
		if node.Status != control.NodeAlive || !hasControlRole(node.Roles, control.RoleData) {
			continue
		}
		out = append(out, node.NodeID)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func hasControlRole(roles []control.Role, role control.Role) bool {
	for _, item := range roles {
		if item == role {
			return true
		}
	}
	return false
}
```

Add `sort` to imports.

In `applySnapshot`, after routing/discovery updates and before storing `n.controlSnapshot`, call:

```go
if firstSnapshot || changes.nodes {
	n.channelDataNodes.Update(aliveDataNodeIDs(snapshot.Nodes))
}
```

- [ ] **Step 5: Run the focused test**

Run:

```bash
GOWORK=off go test -count=1 ./pkg/clusterv2 -run TestNodeAppliesAliveDataNodesForChannelPlacement
```

Expected: test passes.

- [ ] **Step 6: Commit**

```bash
git add pkg/clusterv2/node.go pkg/clusterv2/node_snapshot.go pkg/clusterv2/node_snapshot_test.go
git commit -m "feat(clusterv2): track data nodes for channel placement"
```

## Task 3: Channel Placement Resolver Uses Data Nodes

**Files:**
- Modify: `pkg/clusterv2/channels/meta.go`
- Modify: `pkg/clusterv2/channels/placement.go`
- Test: `pkg/clusterv2/channels/channels_test.go`

- [ ] **Step 1: Write failing tests**

Replace `TestSlotPlacementResolverUsesSlotRouteLeaderAndPeers` with:

```go
func TestSlotPlacementResolverUsesDataNodesInsteadOfSlotPeers(t *testing.T) {
	id := ch.ChannelID{ID: "route-placement", Type: 1}
	resolver := NewSlotPlacementResolver(
		fakePlacementRouter{route: routing.Route{Leader: 2, Peers: []uint64{1, 2, 3}}},
		fakeDataNodeProvider{nodes: []uint64{4, 5, 6}},
		3,
	)

	placement, err := resolver.ResolveChannelPlacement(context.Background(), id)
	if err != nil {
		t.Fatalf("ResolveChannelPlacement() error = %v", err)
	}
	if placement.MinISR != 2 {
		t.Fatalf("MinISR = %d, want quorum 2", placement.MinISR)
	}
	if len(placement.Replicas) != 3 {
		t.Fatalf("Replicas = %v, want 3 data replicas", placement.Replicas)
	}
	for _, replica := range placement.Replicas {
		if replica < 4 || replica > 6 {
			t.Fatalf("Replicas = %v, want only data nodes 4,5,6", placement.Replicas)
		}
	}
}
```

Replace `TestSlotPlacementResolverPrefersRoutePreferredLeader` with:

```go
func TestSlotPlacementResolverPrefersRoutePreferredLeaderOnlyWhenSelected(t *testing.T) {
	id := ch.ChannelID{ID: "route-placement-preferred", Type: 1}
	resolver := NewSlotPlacementResolver(
		fakePlacementRouter{route: routing.Route{Leader: 3, PreferredLeader: 4, Peers: []uint64{1, 2, 3}}},
		fakeDataNodeProvider{nodes: []uint64{4, 5, 6}},
		3,
	)

	placement, err := resolver.ResolveChannelPlacement(context.Background(), id)
	if err != nil {
		t.Fatalf("ResolveChannelPlacement() error = %v", err)
	}
	if placement.Leader != 4 {
		t.Fatalf("Leader = %d, want selected preferred leader 4", placement.Leader)
	}

	withoutPreferred := NewSlotPlacementResolver(
		fakePlacementRouter{route: routing.Route{Leader: 3, PreferredLeader: 9, Peers: []uint64{1, 2, 3}}},
		fakeDataNodeProvider{nodes: []uint64{4, 5, 6}},
		3,
	)
	next, err := withoutPreferred.ResolveChannelPlacement(context.Background(), id)
	if err != nil {
		t.Fatalf("ResolveChannelPlacement(without preferred) error = %v", err)
	}
	if next.Leader == 9 {
		t.Fatalf("Leader = %d, preferred leader outside replicas must not be used", next.Leader)
	}
	if !nodeIDIn(next.Replicas, next.Leader) {
		t.Fatalf("Leader = %d, replicas=%v, want leader in replicas", next.Leader, next.Replicas)
	}
}
```

Add helpers near existing fakes:

```go
type fakeDataNodeProvider struct {
	nodes []uint64
}

func (p fakeDataNodeProvider) DataNodes() []uint64 {
	return append([]uint64(nil), p.nodes...)
}

func nodeIDIn(nodes []ch.NodeID, node ch.NodeID) bool {
	for _, item := range nodes {
		if item == node {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Run tests and verify failure**

Run:

```bash
GOWORK=off go test -count=1 ./pkg/clusterv2/channels -run 'TestSlotPlacementResolver'
```

Expected: compile failure because `NewSlotPlacementResolver` still accepts the old arguments.

- [ ] **Step 3: Add the provider interface and new constructor**

In `pkg/clusterv2/channels/meta.go`, add:

```go
// DataNodeProvider returns alive data-node candidates for initial ChannelV2 placement.
type DataNodeProvider interface {
	DataNodes() []uint64
}
```

In `pkg/clusterv2/channels/placement.go`, change the resolver:

```go
type SlotPlacementResolver struct {
	router       PlacementRouter
	dataNodes    DataNodeProvider
	replicaCount int
}

func NewSlotPlacementResolver(router PlacementRouter, dataNodes DataNodeProvider, replicaCount int) *SlotPlacementResolver {
	return &SlotPlacementResolver{router: router, dataNodes: dataNodes, replicaCount: replicaCount}
}
```

- [ ] **Step 4: Implement deterministic data-node selection**

In `pkg/clusterv2/channels/placement.go`, import `hash/fnv` and `sort`.

Use this implementation shape:

```go
type scoredNode struct {
	node  uint64
	score uint64
}

func selectChannelReplicas(channelID string, candidates []uint64, replicaCount int) ([]uint64, error) {
	if replicaCount <= 0 {
		return nil, fmt.Errorf("%w: channel replica count must be positive", ch.ErrInvalidConfig)
	}
	uniq := uniqueSortedUint64(candidates)
	if len(uniq) < replicaCount {
		return nil, fmt.Errorf("%w: channel replica candidates %d below replica count %d", ch.ErrInvalidConfig, len(uniq), replicaCount)
	}
	scored := make([]scoredNode, 0, len(uniq))
	for _, node := range uniq {
		scored = append(scored, scoredNode{node: node, score: rendezvousScore(channelID, node)})
	}
	sort.Slice(scored, func(i, j int) bool {
		if scored[i].score == scored[j].score {
			return scored[i].node < scored[j].node
		}
		return scored[i].score > scored[j].score
	})
	out := make([]uint64, 0, replicaCount)
	for i := 0; i < replicaCount; i++ {
		out = append(out, scored[i].node)
	}
	return out, nil
}

func rendezvousScore(channelID string, node uint64) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(channelID))
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], node)
	_, _ = h.Write(buf[:])
	return h.Sum64()
}
```

Use `encoding/binary` for the node ID buffer. Keep helper functions unexported.

- [ ] **Step 5: Update `ResolveChannelPlacement`**

Replace replica derivation from `route.Peers` with:

```go
candidates := r.dataNodes.DataNodes()
selected, err := selectChannelReplicas(id.ID, candidates, r.replicaCount)
if err != nil {
	return ChannelPlacement{}, err
}
replicas := make([]ch.NodeID, 0, len(selected))
for _, node := range selected {
	replicas = append(replicas, ch.NodeID(node))
}
leader := ch.NodeID(replicas[0])
if route.PreferredLeader != 0 && uint64NodeIn(selected, route.PreferredLeader) {
	leader = ch.NodeID(route.PreferredLeader)
}
return ChannelPlacement{Leader: leader, Replicas: replicas, MinISR: len(replicas)/2 + 1}, nil
```

Keep the `RouteKey` call so route readiness and Slot leader readiness still gate metadata creation.

- [ ] **Step 6: Run focused tests**

Run:

```bash
GOWORK=off go test -count=1 ./pkg/clusterv2/channels -run 'TestSlotPlacementResolver|TestSlotMetaSourceCreatesMissingRuntimeMeta'
```

Expected: tests pass.

- [ ] **Step 7: Commit**

```bash
git add pkg/clusterv2/channels/meta.go pkg/clusterv2/channels/placement.go pkg/clusterv2/channels/channels_test.go
git commit -m "feat(channelv2): choose replicas from data nodes"
```

## Task 4: Wire Data Placement Into Default Node Channels

**Files:**
- Modify: `pkg/clusterv2/node_defaults.go`
- Modify: `pkg/clusterv2/node_defaults_test.go`
- Modify: `pkg/clusterv2/channels/channels_test.go` if constructor call sites need updates

- [ ] **Step 1: Update constructor call sites to fail compile**

Run:

```bash
GOWORK=off go test -count=1 ./pkg/clusterv2/... 
```

Expected: compile failures at old `NewSlotPlacementResolver` call sites, including `node_defaults.go`.

- [ ] **Step 2: Update default channel meta source wiring**

In `pkg/clusterv2/node_defaults.go`, change:

```go
Placement: channels.NewSlotPlacementResolver(n.router, n.defaultChannelMinISR()),
```

to:

```go
Placement: channels.NewSlotPlacementResolver(n.router, &n.channelDataNodes, int(n.cfg.Channel.ReplicaCount)),
```

Leave observer wiring unchanged.

- [ ] **Step 3: Rename minISR test to match new source**

Replace `TestDefaultChannelMinISRUsesSlotReplicaMajority` with:

```go
func TestChannelReplicaCountDrivesDefaultMinISR(t *testing.T) {
	tests := []struct {
		name     string
		replicas uint16
		want     int
	}{
		{name: "single replica", replicas: 1, want: 1},
		{name: "two replicas", replicas: 2, want: 2},
		{name: "three replicas", replicas: 3, want: 2},
		{name: "four replicas", replicas: 4, want: 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := &Node{cfg: Config{Channel: ChannelConfig{ReplicaCount: tt.replicas}}}
			if got := node.defaultChannelMinISR(); got != tt.want {
				t.Fatalf("defaultChannelMinISR() = %d, want %d", got, tt.want)
			}
		})
	}
}
```

Then update `defaultChannelMinISR` in `node_defaults.go`:

```go
func (n *Node) defaultChannelMinISR() int {
	replicas := 1
	if n != nil && n.cfg.Channel.ReplicaCount > 0 {
		replicas = int(n.cfg.Channel.ReplicaCount)
	}
	return replicas/2 + 1
}
```

- [ ] **Step 4: Run package tests**

Run:

```bash
GOWORK=off go test -count=1 ./pkg/clusterv2/channels ./pkg/clusterv2
```

Expected: tests pass.

- [ ] **Step 5: Commit**

```bash
git add pkg/clusterv2/node_defaults.go pkg/clusterv2/node_defaults_test.go pkg/clusterv2/channels/channels_test.go
git commit -m "feat(clusterv2): wire channel data placement"
```

## Task 5: Documentation And Full Verification

**Files:**
- Modify: `pkg/clusterv2/FLOW.md`
- Modify: `docs/development/PROJECT_KNOWLEDGE.md`
- Modify: `docs/development/WKSIM_STRESS_FINDINGS.md`

- [ ] **Step 1: Update flow documentation**

In `pkg/clusterv2/FLOW.md`, leave the PullHint paragraph unchanged. Add this paragraph after the `SlotMetaSource` paragraph in the ChannelV2 flow section:

```markdown
Initial ChannelV2 placement is data-plane placement, not Slot metadata
placement. Slot routing identifies the authoritative metadata Slot and its
leader/peers; the default ChannelV2 placement resolver chooses replicas from
alive data nodes using deterministic rendezvous ranking and uses the route
preferred leader only when that node is selected as a ChannelV2 replica.
Existing `ChannelRuntimeMeta` rows remain authoritative for established
channels.
```

- [ ] **Step 2: Add project knowledge**

Add this concise line under the Channel Runtime section in `docs/development/PROJECT_KNOWLEDGE.md`:

```markdown
- ChannelV2 data replicas are selected by Channel placement, not by Slot metadata peers; Slot route peers describe metadata ownership only.
```

- [ ] **Step 3: Run focused verification**

Run:

```bash
GOWORK=off go test -count=1 ./pkg/clusterv2/routing ./pkg/clusterv2/channels ./pkg/clusterv2
```

Expected: all packages pass.

- [ ] **Step 4: Run diff check**

Run:

```bash
git diff --check
```

Expected: no output, exit 0.

- [ ] **Step 5: Run target 10k benchmark**

Run:

```bash
GOWORK=off scripts/bench-wukongimv2-three-nodes-10kch.sh
```

Expected:

- status passed,
- activation success `10000`,
- activation errors `0`,
- active leader node count `3`,
- PullHint receive error counters remain `0`.

- [ ] **Step 6: Record findings**

Append one concise evidence bullet to `docs/development/WKSIM_STRESS_FINDINGS.md` under the existing 2026-05-30/31 three-node 10k activation section. The bullet must name the exact evidence directory printed by the script and include activated channel count, error count, backlog count, active leader node count, and PullHint receive error result.

- [ ] **Step 7: Commit**

```bash
git add pkg/clusterv2/FLOW.md docs/development/PROJECT_KNOWLEDGE.md docs/development/WKSIM_STRESS_FINDINGS.md
git commit -m "docs(perf): record channel placement decoupling run"
```

## Self-Review Checklist

- Spec coverage: Tasks cover configuration, alive data-node provider, resolver behavior, node wiring, docs, unit tests, and 10k benchmark validation.
- Deliberate exclusions: remote Slot metadata facade, ForwardAppend metadata envelopes, and PullHint MetaRef are not included.
- TDD shape: Each code task starts with a failing test or compile failure, then implementation, verification, and commit.
- Type consistency: `DataNodeProvider.DataNodes() []uint64`, `ChannelConfig.ReplicaCount uint16`, and `NewSlotPlacementResolver(router, dataNodes, replicaCount)` are used consistently.
