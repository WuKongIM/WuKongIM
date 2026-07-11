package cluster

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

func TestNodeProbeWriteReadyRejectsMissingUnprobedPhysicalSlotStatus(t *testing.T) {
	proposer := &statusRecordingProposer{}
	node := writeProbeNodeForTest(t, proposer, []routing.SlotStatus{
		{SlotID: 1, Leader: 2},
		{SlotID: 2, Leader: 2},
		{SlotID: 3, Leader: 2},
		{SlotID: 4, Leader: 2},
		{SlotID: 5, Leader: 2},
		{SlotID: 6, Leader: 2},
	})
	caller := &recordingWriteProbeStatusCaller{statusesByNode: map[uint64][]routing.SlotStatus{
		2: {
			{SlotID: 1, Leader: 2},
			{SlotID: 2, Leader: 2},
			{SlotID: 3, Leader: 2},
			{SlotID: 4, Leader: 2},
			// Slot 5 is deliberately missing even though it is beyond the four noop probes.
			{SlotID: 6, Leader: 2},
		},
	}}
	node.slotStatusCaller = caller

	err := node.ProbeWriteReady(context.Background())
	if !errors.Is(err, ErrRouteNotReady) {
		t.Fatalf("ProbeWriteReady() error = %v, want ErrRouteNotReady for missing slot status", err)
	}
	if proposer.calls != 0 {
		t.Fatalf("proposer calls=%d, want validation failure before noop proposals", proposer.calls)
	}
	if caller.callsByNode[2] != 1 {
		t.Fatalf("slot status calls to node 2=%d, want one batch", caller.callsByNode[2])
	}
	wantSlotIDs := []uint32{1, 2, 3, 4, 5, 6}
	if !reflect.DeepEqual(caller.requestedByNode[2], wantSlotIDs) {
		t.Fatalf("slot status request=%v, want all physical slots %v", caller.requestedByNode[2], wantSlotIDs)
	}
}

func TestNodeProbeWriteReadyRejectsRemoteLeaderMismatch(t *testing.T) {
	proposer := &statusRecordingProposer{}
	node := writeProbeNodeForTest(t, proposer, []routing.SlotStatus{{SlotID: 1, Leader: 2}})
	node.slotStatusCaller = &recordingWriteProbeStatusCaller{statusesByNode: map[uint64][]routing.SlotStatus{
		2: {{SlotID: 1, Leader: 3}},
	}}

	err := node.ProbeWriteReady(context.Background())
	if !errors.Is(err, ErrRouteNotReady) {
		t.Fatalf("ProbeWriteReady() error = %v, want ErrRouteNotReady for leader mismatch", err)
	}
	if proposer.calls != 0 {
		t.Fatalf("proposer calls=%d, want validation failure before noop proposals", proposer.calls)
	}
}

func TestNodeProbeWriteReadyRejectsClosedLocalFollower(t *testing.T) {
	proposer := &statusRecordingProposer{
		errs: map[multiraft.SlotID]error{1: multiraft.ErrSlotNotFound},
	}
	node := writeProbeNodeForTest(t, proposer, []routing.SlotStatus{{SlotID: 1, Leader: 2}})
	caller := &recordingWriteProbeStatusCaller{statusesByNode: map[uint64][]routing.SlotStatus{
		2: {{SlotID: 1, Leader: 2}},
	}}
	node.slotStatusCaller = caller

	err := node.ProbeWriteReady(context.Background())
	if !errors.Is(err, ErrRouteNotReady) || !errors.Is(err, ErrSlotNotFound) {
		t.Fatalf("ProbeWriteReady() error = %v, want ErrRouteNotReady and ErrSlotNotFound for closed local follower", err)
	}
	if proposer.calls != 0 {
		t.Fatalf("proposer calls=%d, want local follower validation failure before noop proposals", proposer.calls)
	}
}

func TestNodeProbeWriteReadyRejectsRevisionChangeDuringStatusValidation(t *testing.T) {
	proposer := &statusRecordingProposer{}
	node := writeProbeNodeForTest(t, proposer, []routing.SlotStatus{{SlotID: 1, Leader: 2}})
	caller := &recordingWriteProbeStatusCaller{statusesByNode: map[uint64][]routing.SlotStatus{
		2: {{SlotID: 1, Leader: 2}},
	}}
	caller.onCall = func(_ uint64, _ []uint32) {
		node.mu.Lock()
		node.controlSnapshot.Revision = 2
		node.snapshot.StateRevision = 2
		node.mu.Unlock()
	}
	node.slotStatusCaller = caller

	err := node.ProbeWriteReady(context.Background())
	if !errors.Is(err, ErrRouteNotReady) {
		t.Fatalf("ProbeWriteReady() error = %v, want ErrRouteNotReady after revision change", err)
	}
	if proposer.calls != 0 {
		t.Fatalf("proposer calls=%d, want revision recheck before noop proposals", proposer.calls)
	}
}

func TestNodeProbeWriteReadyRejectsLocalLeaderTermMismatch(t *testing.T) {
	proposer := &statusRecordingProposer{statuses: map[multiraft.SlotID]multiraft.Status{
		1: {SlotID: 1, LeaderID: 1, Term: 11},
	}}
	node := writeProbeNodeForTest(t, proposer, []routing.SlotStatus{{SlotID: 1, Leader: 1, LeaderTerm: 10}})

	err := node.ProbeWriteReady(context.Background())
	if !errors.Is(err, ErrRouteNotReady) {
		t.Fatalf("ProbeWriteReady() error = %v, want ErrRouteNotReady for local term mismatch", err)
	}
	if proposer.calls != 0 {
		t.Fatalf("proposer calls=%d, want term validation failure before noop proposals", proposer.calls)
	}
}

func TestNodeProbeWriteReadyRejectsRemoteLeaderTermMismatch(t *testing.T) {
	proposer := &statusRecordingProposer{}
	node := writeProbeNodeForTest(t, proposer, []routing.SlotStatus{{SlotID: 1, Leader: 2, LeaderTerm: 10}})
	node.slotStatusCaller = &recordingWriteProbeStatusCaller{statusesByNode: map[uint64][]routing.SlotStatus{
		2: {{SlotID: 1, Leader: 2, LeaderTerm: 11}},
	}}

	err := node.ProbeWriteReady(context.Background())
	if !errors.Is(err, ErrRouteNotReady) {
		t.Fatalf("ProbeWriteReady() error = %v, want ErrRouteNotReady for remote term mismatch", err)
	}
	if proposer.calls != 0 {
		t.Fatalf("proposer calls=%d, want term validation failure before noop proposals", proposer.calls)
	}
}

func TestNodeProbeWriteReadyValidatesLocalLeaderDirectly(t *testing.T) {
	proposer := &statusRecordingProposer{}
	node := writeProbeNodeForTest(t, proposer, []routing.SlotStatus{{SlotID: 1, Leader: 1}})

	if err := node.ProbeWriteReady(context.Background()); err != nil {
		t.Fatalf("ProbeWriteReady() error = %v", err)
	}
	if proposer.calls != 1 {
		t.Fatalf("proposer calls=%d, want one bounded noop after local status validation", proposer.calls)
	}
}

func TestNodeProbeWriteReadyActiveMirrorUsesRemoteLeaderStatus(t *testing.T) {
	proposer := &statusRecordingProposer{
		errs: map[multiraft.SlotID]error{1: multiraft.ErrSlotNotFound},
	}
	node := writeProbeNodeForTest(t, proposer, []routing.SlotStatus{{SlotID: 1, Leader: 2}})
	node.mu.Lock()
	node.controlSnapshot.Slots[0].DesiredPeers = []uint64{2, 3}
	node.mu.Unlock()
	node.slotStatusCaller = &recordingWriteProbeStatusCaller{statusesByNode: map[uint64][]routing.SlotStatus{
		2: {{SlotID: 1, Leader: 2}},
	}}

	if err := node.ProbeWriteReady(context.Background()); err != nil {
		t.Fatalf("ProbeWriteReady() error = %v", err)
	}
	if proposer.calls != 1 {
		t.Fatalf("proposer calls=%d, want one bounded noop after remote status validation", proposer.calls)
	}
}

func TestValidateRemoteWriteProbeStatusesRequiresExactResponse(t *testing.T) {
	tests := []struct {
		name     string
		statuses []routing.SlotStatus
	}{
		{name: "duplicate", statuses: []routing.SlotStatus{{SlotID: 1, Leader: 2}, {SlotID: 1, Leader: 2}}},
		{name: "unexpected", statuses: []routing.SlotStatus{{SlotID: 1, Leader: 2}, {SlotID: 3, Leader: 2}}},
		{name: "zero leader", statuses: []routing.SlotStatus{{SlotID: 1, Leader: 2}, {SlotID: 2}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateRemoteWriteProbeStatuses(2, []uint32{1, 2}, map[uint32]uint64{1: 0, 2: 0}, tt.statuses); !errors.Is(err, ErrRouteNotReady) {
				t.Fatalf("validateRemoteWriteProbeStatuses() error = %v, want ErrRouteNotReady", err)
			}
		})
	}
}

func writeProbeNodeForTest(t *testing.T, proposer *statusRecordingProposer, statuses []routing.SlotStatus) *Node {
	t.Helper()
	sort.Slice(statuses, func(i, j int) bool { return statuses[i].SlotID < statuses[j].SlotID })
	slots := make([]control.SlotAssignment, 0, len(statuses))
	ranges := make([]control.HashSlotRange, 0, len(statuses))
	for i, status := range statuses {
		slots = append(slots, control.SlotAssignment{
			SlotID:          status.SlotID,
			DesiredPeers:    []uint64{1, 2, 3},
			ConfigEpoch:     1,
			PreferredLeader: status.Leader,
		})
		ranges = append(ranges, control.HashSlotRange{From: uint16(i), To: uint16(i), SlotID: status.SlotID})
	}
	node, err := New(validNodeConfig(t), WithProposer(proposer), withSlotStatusRuntime(proposer))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	snapshot := control.Snapshot{
		Revision:     1,
		ControllerID: 1,
		Nodes: []control.Node{
			{NodeID: 1, Addr: "127.0.0.1:1001", Roles: []control.Role{control.RoleData}, Status: control.NodeAlive},
			{NodeID: 2, Addr: "127.0.0.1:1002", Roles: []control.Role{control.RoleData}, Status: control.NodeAlive},
			{NodeID: 3, Addr: "127.0.0.1:1003", Roles: []control.Role{control.RoleData}, Status: control.NodeAlive},
		},
		Slots: slots,
		HashSlots: control.HashSlotTable{
			Revision: 1,
			Count:    uint16(len(statuses)),
			Ranges:   ranges,
		},
	}
	if err := node.router.UpdateControlSnapshot(snapshot); err != nil {
		t.Fatalf("UpdateControlSnapshot() error = %v", err)
	}
	node.router.UpdateSlotLeaders(statuses)
	node.controlSnapshot = snapshot.Clone()
	node.snapshot = Snapshot{
		NodeID:        1,
		StateRevision: 1,
		RoutesReady:   true,
		SlotsReady:    true,
		ChannelsReady: true,
		SlotCount:     uint32(len(statuses)),
		HashSlotCount: uint16(len(statuses)),
	}
	node.channelDataNodes.Update([]uint64{1})
	node.started.Store(true)
	if proposer.statuses == nil {
		proposer.statuses = make(map[multiraft.SlotID]multiraft.Status, len(statuses))
	}
	for _, status := range statuses {
		slotID := multiraft.SlotID(status.SlotID)
		if _, ok := proposer.statuses[slotID]; ok {
			continue
		}
		proposer.statuses[slotID] = multiraft.Status{
			SlotID:   slotID,
			LeaderID: multiraft.NodeID(status.Leader),
			Term:     status.LeaderTerm,
		}
	}
	return node
}

func TestWithProposerDoesNotImplicitlyEnableSlotStatusProof(t *testing.T) {
	proposer := &statusRecordingProposer{}
	node, err := New(validNodeConfig(t), WithProposer(proposer))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if node.slotStatusRuntime != nil {
		t.Fatalf("slotStatusRuntime = %T, want nil without explicit internal wiring", node.slotStatusRuntime)
	}
}

type statusRecordingProposer struct {
	recordingProposer
	statuses map[multiraft.SlotID]multiraft.Status
	errs     map[multiraft.SlotID]error
}

func (p *statusRecordingProposer) Status(slotID multiraft.SlotID) (multiraft.Status, error) {
	if err := p.errs[slotID]; err != nil {
		return multiraft.Status{}, err
	}
	status, ok := p.statuses[slotID]
	if !ok {
		return multiraft.Status{}, multiraft.ErrSlotNotFound
	}
	return status, nil
}

type recordingWriteProbeStatusCaller struct {
	statusesByNode  map[uint64][]routing.SlotStatus
	errsByNode      map[uint64]error
	callsByNode     map[uint64]int
	requestedByNode map[uint64][]uint32
	onCall          func(nodeID uint64, slotIDs []uint32)
}

func (c *recordingWriteProbeStatusCaller) Call(_ context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error) {
	if serviceID != clusternet.RPCSlotStatus {
		return nil, fmt.Errorf("unexpected service id %d", serviceID)
	}
	if c.callsByNode == nil {
		c.callsByNode = make(map[uint64]int)
	}
	if c.requestedByNode == nil {
		c.requestedByNode = make(map[uint64][]uint32)
	}
	slotIDs, err := decodeSlotStatusRequest(payload)
	if err != nil {
		return nil, err
	}
	c.callsByNode[nodeID]++
	c.requestedByNode[nodeID] = append([]uint32(nil), slotIDs...)
	if c.onCall != nil {
		c.onCall(nodeID, append([]uint32(nil), slotIDs...))
	}
	if err := c.errsByNode[nodeID]; err != nil {
		return nil, err
	}
	return encodeSlotStatusResponse(c.statusesByNode[nodeID])
}
