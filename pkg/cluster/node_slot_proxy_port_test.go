package cluster

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

func TestNodeSlotProxyPortRoutesFromControlSnapshot(t *testing.T) {
	node := newStartedSlotProxyPortNode(t, &recordingProposer{})
	key := keyForNodeHashSlot(t, 4, 3)

	if got := node.HashSlotForKey(key); got != 3 {
		t.Fatalf("HashSlotForKey() = %d, want 3", got)
	}
	if got := node.SlotForKey(key); got != 2 {
		t.Fatalf("SlotForKey() = %d, want 2", got)
	}
	if got := node.HashSlotsOf(1); !reflect.DeepEqual(got, []uint16{0, 1}) {
		t.Fatalf("HashSlotsOf(1) = %#v, want [0 1]", got)
	}
	if got := node.HashSlotTableVersion(); got != 9 {
		t.Fatalf("HashSlotTableVersion() = %d, want 9", got)
	}
	leader, err := node.LeaderOf(2)
	if err != nil {
		t.Fatalf("LeaderOf(2) error = %v", err)
	}
	if leader != 2 {
		t.Fatalf("LeaderOf(2) = %d, want 2", leader)
	}
	if !node.IsLocal(1) || node.IsLocal(2) {
		t.Fatalf("IsLocal mismatch for local=1 remote=2")
	}
	if got := node.PeersForSlot(2); !reflect.DeepEqual(got, []multiraft.NodeID{1, 2}) {
		t.Fatalf("PeersForSlot(2) = %#v, want [1 2]", got)
	}
}

func TestNodeSlotProxyPortProposesExplicitHashSlotTarget(t *testing.T) {
	proposer := &recordingProposer{}
	node := newStartedSlotProxyPortNode(t, proposer)

	if err := node.ProposeWithHashSlot(context.Background(), 2, 3, []byte("cmd")); err != nil {
		t.Fatalf("ProposeWithHashSlot() error = %v", err)
	}
	if proposer.calls != 1 {
		t.Fatalf("proposer calls = %d, want 1", proposer.calls)
	}
	target := proposer.last.Target
	if !target.HasSlotID || target.SlotID != 2 || !target.HasHashSlot || target.HashSlot != 3 {
		t.Fatalf("propose target = %#v, want slot 2 hash slot 3", target)
	}
}

func TestNodeSlotProxyPortLocalProposeRejectsRemoteLeader(t *testing.T) {
	proposer := &recordingProposer{}
	node := newStartedSlotProxyPortNode(t, proposer)

	if err := node.ProposeLocalWithHashSlot(context.Background(), 2, 3, []byte("cmd")); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("ProposeLocalWithHashSlot(remote) error = %v, want ErrNotLeader", err)
	}
	if proposer.calls != 0 {
		t.Fatalf("proposer calls = %d, want 0 when remote leader is rejected", proposer.calls)
	}
	if err := node.ProposeLocalWithHashSlot(context.Background(), 1, 0, []byte("cmd")); err != nil {
		t.Fatalf("ProposeLocalWithHashSlot(local) error = %v", err)
	}
	if proposer.calls != 1 {
		t.Fatalf("proposer calls = %d, want 1 after local proposal", proposer.calls)
	}
}

func newStartedSlotProxyPortNode(t *testing.T, proposer *recordingProposer) *Node {
	t.Helper()
	node, err := New(validNodeConfig(t), WithProposer(proposer))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	snapshot := control.Snapshot{
		Revision:     7,
		ControllerID: 1,
		Nodes: []control.Node{
			{NodeID: 1, Addr: "127.0.0.1:1001", Roles: []control.Role{control.RoleData}, Status: control.NodeAlive},
			{NodeID: 2, Addr: "127.0.0.1:1002", Roles: []control.Role{control.RoleData}, Status: control.NodeAlive},
		},
		Slots: []control.SlotAssignment{
			{SlotID: 1, DesiredPeers: []uint64{1, 2}, ConfigEpoch: 1, PreferredLeader: 1},
			{SlotID: 2, DesiredPeers: []uint64{1, 2}, ConfigEpoch: 1, PreferredLeader: 2},
		},
		HashSlots: control.HashSlotTable{Revision: 9, Count: 4, Ranges: []control.HashSlotRange{
			{From: 0, To: 1, SlotID: 1},
			{From: 2, To: 3, SlotID: 2},
		}},
	}
	if err := node.router.UpdateControlSnapshot(snapshot); err != nil {
		t.Fatalf("UpdateControlSnapshot() error = %v", err)
	}
	node.router.UpdateSlotLeaders([]routing.SlotStatus{{SlotID: 1, Leader: 1}, {SlotID: 2, Leader: 2}})
	node.controlSnapshot = snapshot
	node.snapshot = Snapshot{NodeID: 1, StateRevision: snapshot.Revision, RoutesReady: true, SlotsReady: true, ChannelsReady: true, SlotCount: 2, HashSlotCount: 4}
	node.started.Store(true)
	return node
}

func keyForNodeHashSlot(t testing.TB, count, want uint16) string {
	t.Helper()
	for i := 0; i < 100_000; i++ {
		key := fmt.Sprintf("slot-proxy-key-%d", i)
		if routing.HashSlotForKey(key, count) == want {
			return key
		}
	}
	t.Fatalf("no key found for hash slot %d", want)
	return ""
}
