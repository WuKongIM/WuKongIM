# internalv2 Dynamic Node Lifecycle Stage 5B Channel Drain Inventory Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add bounded ChannelRuntimeMeta inventory so scale-in status can prove a leaving node is not a Channel leader, replica, or ISR member.

**Architecture:** This sub-stage adds a management usecase that scans authoritative `ChannelRuntimeMeta` by physical Slot using the existing `ChannelRuntimeMetaReader`. The result is merged into `NodeScaleInStatusResponse` as fail-closed Channel blockers, but final `safe_to_remove` remains false until Stage 5C/5D adds gateway drain and remove gating.

**Tech Stack:** Go, `internalv2/usecase/management`, `metadb.ChannelRuntimeMetaCursor`, clusterv2 control snapshots, bounded paged scans.

---

## Scope

Implements only Stage 5B from:

- Stage 5 index: `docs/superpowers/plans/2026-06-24-internalv2-dynamic-node-stage5.md`
- Previous sub-stage: `docs/superpowers/plans/2026-06-24-internalv2-dynamic-node-stage5a-removed-lifecycle.md`

This sub-stage must not add gateway drain mode, safe remove route, or `MarkNodeRemoved` usecase wiring in `internalv2`.

## Entry Gate

- [ ] Stage 5A is merged into local `main`.
- [ ] Stage 4 scale-in status tests still pass:

```bash
GOWORK=off go test ./internalv2/usecase/management -run 'TestScaleInStatus|TestAdvanceNodeScaleIn' -count=1
```

Expected: PASS.

## File Structure

- Create `internalv2/usecase/management/channel_drain.go`
  - Implements target-node Channel drain inventory.
- Create `internalv2/usecase/management/channel_drain_test.go`
  - Verifies paging, leader/replica/ISR counting, missing readers, and scan errors.
- Modify `internalv2/usecase/management/scale_in.go`
  - Adds Channel blocker fields to scale-in status and merges inventory.
- Modify `internalv2/usecase/management/scale_in_test.go`
  - Verifies status fails closed when Channel inventory is unknown or target appears in Channel metadata.

## Task 1: Add Channel Drain Inventory Usecase

**Files:**
- Create: `internalv2/usecase/management/channel_drain.go`
- Create: `internalv2/usecase/management/channel_drain_test.go`

- [ ] **Step 1: Write failing inventory tests**

Create `internalv2/usecase/management/channel_drain_test.go`:

```go
package management

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestNodeChannelDrainInventoryCountsTargetRolesAcrossSlots(t *testing.T) {
	reader := newChannelDrainMetaReader()
	reader.pages[1] = map[metadb.ChannelRuntimeMetaCursor]channelDrainMetaPage{
		{}: {
			items: []metadb.ChannelRuntimeMeta{
				{ChannelID: "leader", ChannelType: 1, Leader: 4, Replicas: []uint64{2, 3, 4}, ISR: []uint64{2, 3, 4}},
				{ChannelID: "replica", ChannelType: 1, Leader: 2, Replicas: []uint64{2, 3, 4}, ISR: []uint64{2, 3}},
			},
			done: true,
		},
	}
	reader.pages[2] = map[metadb.ChannelRuntimeMetaCursor]channelDrainMetaPage{
		{}: {
			items: []metadb.ChannelRuntimeMeta{
				{ChannelID: "isr", ChannelType: 1, Leader: 2, Replicas: []uint64{2, 3, 5}, ISR: []uint64{3, 4}},
			},
			done: true,
		},
	}
	app := New(Options{
		Cluster: fakeNodeSnapshotReader{snapshot: control.Snapshot{
			Slots: []control.SlotAssignment{{SlotID: 2}, {SlotID: 1}},
		}},
		ChannelRuntimeMeta: reader,
	})

	inv, err := app.NodeChannelDrainInventory(context.Background(), NodeChannelDrainInventoryRequest{NodeID: 4, PageLimit: 2})
	if err != nil {
		t.Fatalf("NodeChannelDrainInventory() error = %v", err)
	}
	if inv.Unknown || inv.Safe || inv.LeaderCount != 1 || inv.ReplicaCount != 2 || inv.ISRCount != 2 || inv.ScannedSlotCount != 2 {
		t.Fatalf("inventory = %#v, want counted unsafe target roles", inv)
	}
}

func TestNodeChannelDrainInventoryPaginatesOneSlot(t *testing.T) {
	reader := newChannelDrainMetaReader()
	reader.pages[1] = map[metadb.ChannelRuntimeMetaCursor]channelDrainMetaPage{
		{}: {
			items:  []metadb.ChannelRuntimeMeta{{ChannelID: "a", ChannelType: 1, Replicas: []uint64{4}}},
			cursor: metadb.ChannelRuntimeMetaCursor{ChannelID: "a", ChannelType: 1},
			done:   false,
		},
		{ChannelID: "a", ChannelType: 1}: {
			items: []metadb.ChannelRuntimeMeta{{ChannelID: "b", ChannelType: 1, ISR: []uint64{4}}},
			done:  true,
		},
	}
	app := New(Options{
		Cluster:            fakeNodeSnapshotReader{snapshot: control.Snapshot{Slots: []control.SlotAssignment{{SlotID: 1}}}},
		ChannelRuntimeMeta: reader,
	})

	inv, err := app.NodeChannelDrainInventory(context.Background(), NodeChannelDrainInventoryRequest{NodeID: 4, PageLimit: 1})
	if err != nil {
		t.Fatalf("NodeChannelDrainInventory() error = %v", err)
	}
	if inv.ReplicaCount != 1 || inv.ISRCount != 1 || reader.calls != 2 {
		t.Fatalf("inventory = %#v calls=%d, want paged replica/isr counts", inv, reader.calls)
	}
}

func TestNodeChannelDrainInventoryFailsClosedWhenReaderMissingOrScanFails(t *testing.T) {
	missing := New(Options{Cluster: fakeNodeSnapshotReader{snapshot: control.Snapshot{Slots: []control.SlotAssignment{{SlotID: 1}}}}})
	inv, err := missing.NodeChannelDrainInventory(context.Background(), NodeChannelDrainInventoryRequest{NodeID: 4})
	if err != nil {
		t.Fatalf("missing reader inventory error = %v", err)
	}
	if !inv.Unknown || inv.Safe {
		t.Fatalf("missing reader inventory = %#v, want unknown unsafe", inv)
	}

	reader := newChannelDrainMetaReader()
	reader.err = errors.New("scan failed")
	app := New(Options{
		Cluster:            fakeNodeSnapshotReader{snapshot: control.Snapshot{Slots: []control.SlotAssignment{{SlotID: 1}}}},
		ChannelRuntimeMeta: reader,
	})
	inv, err = app.NodeChannelDrainInventory(context.Background(), NodeChannelDrainInventoryRequest{NodeID: 4})
	if err != nil {
		t.Fatalf("scan error inventory error = %v", err)
	}
	if !inv.Unknown || inv.Safe || inv.LastError == "" {
		t.Fatalf("scan error inventory = %#v, want unknown unsafe with error", inv)
	}
}
```

Add test fakes in the same file:

```go
type channelDrainMetaReader struct {
	pages map[uint32]map[metadb.ChannelRuntimeMetaCursor]channelDrainMetaPage
	err   error
	calls int
}

type channelDrainMetaPage struct {
	items  []metadb.ChannelRuntimeMeta
	cursor metadb.ChannelRuntimeMetaCursor
	done   bool
}

func newChannelDrainMetaReader() *channelDrainMetaReader {
	return &channelDrainMetaReader{pages: map[uint32]map[metadb.ChannelRuntimeMetaCursor]channelDrainMetaPage{}}
}

func (r *channelDrainMetaReader) ScanChannelRuntimeMetaSlotPage(_ context.Context, slotID uint32, after metadb.ChannelRuntimeMetaCursor, _ int) ([]metadb.ChannelRuntimeMeta, metadb.ChannelRuntimeMetaCursor, bool, error) {
	r.calls++
	if r.err != nil {
		return nil, metadb.ChannelRuntimeMetaCursor{}, false, r.err
	}
	page := r.pages[slotID][after]
	return append([]metadb.ChannelRuntimeMeta(nil), page.items...), page.cursor, page.done, nil
}
```

- [ ] **Step 2: Verify RED**

```bash
GOWORK=off go test ./internalv2/usecase/management -run 'TestNodeChannelDrainInventory' -count=1
```

Expected: FAIL because `NodeChannelDrainInventory` types and method do not exist.

- [ ] **Step 3: Implement inventory usecase**

Create `internalv2/usecase/management/channel_drain.go`:

```go
package management

import (
	"context"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const (
	DefaultChannelDrainScanLimit = 256
	MaxChannelDrainScanLimit     = 1024
)

// NodeChannelDrainInventoryRequest configures a target-node Channel drain scan.
type NodeChannelDrainInventoryRequest struct {
	// NodeID is the leaving node being checked.
	NodeID uint64
	// PageLimit bounds each physical Slot metadata scan page.
	PageLimit int
}

// NodeChannelDrainInventoryResponse reports Channel blockers for target-node removal.
type NodeChannelDrainInventoryResponse struct {
	NodeID           uint64
	Safe             bool
	Unknown          bool
	ScannedSlotCount int
	LeaderCount      int
	ReplicaCount     int
	ISRCount         int
	LastError         string
}
```

Implement `NodeChannelDrainInventory` with these rules:

- invalid `NodeID` returns `metadb.ErrInvalidArgument`;
- missing app, cluster, or `ChannelRuntimeMetaReader` returns `Unknown=true`, `Safe=false`;
- `LocalControlSnapshot` errors are returned to caller;
- scan Slot IDs in `sortedSnapshotSlotIDs(snapshot.Slots)` order;
- page with `normalizeChannelDrainLimit(req.PageLimit)`;
- scan errors return `Unknown=true`, `Safe=false`, and `LastError=err.Error()`;
- count target as leader, replica, and ISR independently;
- `Safe=true` only when inventory is known and all counts are zero.

Add helpers:

```go
func normalizeChannelDrainLimit(limit int) int {
	if limit <= 0 {
		return DefaultChannelDrainScanLimit
	}
	if limit > MaxChannelDrainScanLimit {
		return MaxChannelDrainScanLimit
	}
	return limit
}

func countChannelDrainMeta(resp *NodeChannelDrainInventoryResponse, targetNode uint64, meta metadb.ChannelRuntimeMeta) {
	if meta.Leader == targetNode {
		resp.LeaderCount++
	}
	if containsUint64(meta.Replicas, targetNode) {
		resp.ReplicaCount++
	}
	if containsUint64(meta.ISR, targetNode) {
		resp.ISRCount++
	}
}
```

- [ ] **Step 4: Verify inventory GREEN**

```bash
GOWORK=off go test ./internalv2/usecase/management -run 'TestNodeChannelDrainInventory' -count=1
```

Expected: PASS.

## Task 2: Merge Channel Blockers Into Scale-In Status

**Files:**
- Modify: `internalv2/usecase/management/scale_in.go`
- Modify: `internalv2/usecase/management/scale_in_test.go`

- [ ] **Step 1: Add failing status tests**

Add to `internalv2/usecase/management/scale_in_test.go`:

```go
func TestScaleInStatusBlocksWhenChannelInventoryReferencesTarget(t *testing.T) {
	reader := newChannelDrainMetaReader()
	reader.pages[1] = map[metadb.ChannelRuntimeMetaCursor]channelDrainMetaPage{
		{}: {
			items: []metadb.ChannelRuntimeMeta{{ChannelID: "ch", ChannelType: 1, Leader: 4, Replicas: []uint64{2, 4}, ISR: []uint64{4}}},
			done:  true,
		},
	}
	snap := scaleInReadyNoSlotReplicaSnapshot()
	app := New(Options{
		Cluster:            fakeNodeSnapshotReader{snapshot: snap},
		RuntimeSummary:     fakeNodeRuntimeSummaryReader{summaries: scaleInRuntimeSummariesFor(snap.Revision, scaleInSnapshotNodeIDs(snap)...)},
		SlotRuntimeStatus:  scaleInSafeSlotRuntimeReader{},
		ChannelRuntimeMeta: reader,
	})

	status, err := app.NodeScaleInStatus(context.Background(), NodeScaleInStatusRequest{NodeID: 4})
	if err != nil {
		t.Fatalf("NodeScaleInStatus() error = %v", err)
	}
	if status.SafeToProceed || !status.BlockedByChannels || status.ChannelLeaderCount != 1 || status.ChannelReplicaCount != 1 || status.ChannelISRCount != 1 {
		t.Fatalf("status = %#v, want channel blockers", status)
	}
}

func TestScaleInStatusBlocksWhenChannelInventoryUnknown(t *testing.T) {
	snap := scaleInReadyNoSlotReplicaSnapshot()
	app := New(Options{
		Cluster:           fakeNodeSnapshotReader{snapshot: snap},
		RuntimeSummary:    fakeNodeRuntimeSummaryReader{summaries: scaleInRuntimeSummariesFor(snap.Revision, scaleInSnapshotNodeIDs(snap)...)},
		SlotRuntimeStatus: scaleInSafeSlotRuntimeReader{},
	})

	status, err := app.NodeScaleInStatus(context.Background(), NodeScaleInStatusRequest{NodeID: 4})
	if err != nil {
		t.Fatalf("NodeScaleInStatus() error = %v", err)
	}
	if status.SafeToProceed || !status.BlockedByChannels || !status.UnknownChannelInventory {
		t.Fatalf("status = %#v, want unknown channel inventory blocker", status)
	}
}
```

Add these helper definitions to `scale_in_test.go` when adding the tests:

```go
func scaleInReadyNoSlotReplicaSnapshot() control.Snapshot {
	return control.Snapshot{
		Revision: 11,
		Nodes: []control.Node{
			{ID: 1, Role: control.NodeRoleController, JoinState: control.NodeJoinStateActive, Online: true},
			{ID: 2, Role: control.NodeRoleData, JoinState: control.NodeJoinStateActive, Online: true},
			{ID: 3, Role: control.NodeRoleData, JoinState: control.NodeJoinStateActive, Online: true},
			{ID: 4, Role: control.NodeRoleData, JoinState: control.NodeJoinStateLeaving, Online: true},
		},
		Slots: []control.SlotAssignment{
			{SlotID: 1, DesiredPeers: []uint64{2, 3}, PreferredLeaderID: 2, ConfigEpoch: 7},
		},
	}
}

type scaleInSafeSlotRuntimeReader struct{}

func (scaleInSafeSlotRuntimeReader) ReadSlotRuntimeStatus(_ context.Context, slotID uint32) (SlotRuntimeStatus, error) {
	return SlotRuntimeStatus{SlotID: slotID, LeaderID: 2, ReplicaIDs: []uint64{2, 3}, Ready: true}, nil
}
```

- [ ] **Step 2: Extend status response**

Add fields to `NodeScaleInStatusResponse`:

```go
	// BlockedByChannels reports Channel leader, replica, ISR, or unknown inventory blockers.
	BlockedByChannels bool
	// UnknownChannelInventory reports that Channel inventory could not be proven.
	UnknownChannelInventory bool
	// ChannelLeaderCount counts Channels led by the target node.
	ChannelLeaderCount int
	// ChannelReplicaCount counts Channels where the target is a configured replica.
	ChannelReplicaCount int
	// ChannelISRCount counts Channels where the target is in ISR.
	ChannelISRCount int
```

Merge `NodeChannelDrainInventory` inside `nodeScaleInStatusFromSnapshot` after Stage 4 Slot/task blockers. Missing inventory must be fail-closed.

- [ ] **Step 3: Update `SafeToProceed` calculation**

`SafeToProceed` must also require:

```go
!response.BlockedByChannels &&
!response.UnknownChannelInventory &&
response.ChannelLeaderCount == 0 &&
response.ChannelReplicaCount == 0 &&
response.ChannelISRCount == 0
```

Do not add final `SafeToRemove` yet. Stage 5C adds runtime drain blockers first.

- [ ] **Step 4: Run usecase tests**

```bash
GOWORK=off go test ./internalv2/usecase/management -run 'TestNodeChannelDrainInventory|TestScaleInStatus' -count=1
git diff --check
```

Expected: PASS.

- [ ] **Step 5: Commit Stage 5B**

```bash
git add internalv2/usecase/management/channel_drain.go internalv2/usecase/management/channel_drain_test.go internalv2/usecase/management/scale_in.go internalv2/usecase/management/scale_in_test.go
git commit -m "feat: add channel drain inventory"
```

## Exit Gate

```bash
GOWORK=off go test ./internalv2/usecase/management -run 'TestNodeChannelDrainInventory|TestScaleInStatus|TestAdvanceNodeScaleIn' -count=1
git diff --check
rg -n "NodeChannelDrainInventory|BlockedByChannels|UnknownChannelInventory|ChannelLeaderCount|ChannelReplicaCount|ChannelISRCount" internalv2/usecase/management
```

Expected: all commands pass. `SafeToProceed` remains false whenever Channel inventory is unknown or target Channel membership exists.
