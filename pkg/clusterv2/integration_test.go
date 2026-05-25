package clusterv2

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channelv2"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/channels"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/clusterv2/net"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/propose"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/routing"
)

func TestClusterV2ThreeNodeSlotPropose(t *testing.T) {
	h := newThreeNodeHarness(t)
	h.Start(t)
	t.Cleanup(func() { h.Stop(t) })

	h.WaitSlotLeader(t, 1)
	nonLeader := h.NonLeaderForSlot(t, 1)
	err := nonLeader.Propose(context.Background(), ProposeRequest{Key: "user-a", Command: []byte("cmd")})
	if err != nil {
		t.Fatalf("Propose() error = %v", err)
	}
	h.RequireCommandApplied(t, 1, []byte("cmd"))
}

func TestClusterV2ThreeNodeChannelAppendQuorum(t *testing.T) {
	channelID := channelv2.ChannelID{ID: "room-a", Type: 1}
	h := newThreeNodeHarness(t)
	h.WithStaticChannelMeta(channelv2.Meta{
		Key:         channelv2.ChannelKeyForID(channelID),
		ID:          channelID,
		Epoch:       1,
		LeaderEpoch: 1,
		Leader:      1,
		Replicas:    []channelv2.NodeID{1, 2, 3},
		ISR:         []channelv2.NodeID{1, 2, 3},
		MinISR:      2,
		Status:      channelv2.StatusActive,
	})
	h.Start(t)
	t.Cleanup(func() { h.Stop(t) })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	res, err := h.Node(1).AppendChannel(ctx, channelv2.AppendRequest{
		ChannelID:            channelID,
		CommitMode:           channelv2.CommitModeQuorum,
		Message:              channelv2.Message{MessageID: 1001, Payload: []byte("hello")},
		ExpectedChannelEpoch: 1,
		ExpectedLeaderEpoch:  1,
	})
	if err != nil {
		t.Fatalf("AppendChannel() error = %v", err)
	}
	if res.MessageSeq == 0 {
		t.Fatal("AppendChannel() MessageSeq = 0, want committed sequence")
	}

	h.WaitChannelCommitted(t, 2, channelID, res.MessageSeq)
	fetched, err := h.Node(2).FetchChannel(context.Background(), channelv2.FetchRequest{ChannelID: channelID, FromSeq: 1, Limit: 10})
	if err != nil {
		t.Fatalf("FetchChannel() error = %v", err)
	}
	if len(fetched.Messages) != 1 || !bytes.Equal(fetched.Messages[0].Payload, []byte("hello")) {
		t.Fatalf("FetchChannel() messages = %#v, want hello", fetched.Messages)
	}
}

type threeNodeHarness struct {
	network  *clusternet.LocalNetwork
	nodes    map[uint64]*Node
	slots    map[uint64]*smokeSlotRuntime
	channels map[uint64]*channels.Service
	metas    []channelv2.Meta
	applied  *smokeAppliedLog

	tickCancel context.CancelFunc
	tickWG     sync.WaitGroup
}

func newThreeNodeHarness(t testing.TB) *threeNodeHarness {
	t.Helper()
	return &threeNodeHarness{
		network:  clusternet.NewLocalNetwork(),
		nodes:    make(map[uint64]*Node),
		slots:    make(map[uint64]*smokeSlotRuntime),
		channels: make(map[uint64]*channels.Service),
		applied:  newSmokeAppliedLog(),
	}
}

func (h *threeNodeHarness) WithStaticChannelMeta(meta channelv2.Meta) {
	h.metas = append(h.metas, meta)
}

func (h *threeNodeHarness) Start(t testing.TB) {
	t.Helper()
	snapshot := h.controlSnapshot()
	for _, nodeID := range []uint64{1, 2, 3} {
		slotRuntime := &smokeSlotRuntime{localNode: nodeID, leaders: map[uint32]uint64{1: 1}, applied: h.applied}
		node, err := New(Config{NodeID: nodeID, ListenAddr: fmt.Sprintf("127.0.0.1:%d", 10000+nodeID), DataDir: t.TempDir()}, withController(control.NewStaticController(snapshot)), withSlotReconciler(&recordingReconciler{}))
		if err != nil {
			t.Fatalf("New(node=%d) error = %v", nodeID, err)
		}
		node.proposer = propose.NewService(propose.Config{LocalNode: nodeID, Router: node.router, Slots: slotRuntime, Forward: propose.NewNetworkForwardClient(h.network)})
		h.network.Register(nodeID, clusternet.RPCSlotForwardPropose, propose.NewForwardHandler(slotRuntime))
		if len(h.metas) > 0 {
			service, err := channels.NewService(channels.Config{
				LocalNode:  channelv2.NodeID(nodeID),
				Store:      channelstore.NewMemoryFactory(),
				Transport:  channels.NewTransportClient(h.network),
				MetaSource: channels.NewStaticMetaSource(h.metas),
			})
			if err != nil {
				t.Fatalf("channels.NewService(node=%d) error = %v", nodeID, err)
			}
			node.channels = service
			h.channels[nodeID] = service
			channels.RegisterHandlers(h.network, nodeID, service.Server())
		}
		h.nodes[nodeID] = node
		h.slots[nodeID] = slotRuntime
	}
	for _, nodeID := range []uint64{1, 2, 3} {
		if err := h.nodes[nodeID].Start(context.Background()); err != nil {
			t.Fatalf("Start(node=%d) error = %v", nodeID, err)
		}
		h.nodes[nodeID].router.UpdateSlotLeaders([]routing.SlotStatus{{SlotID: 1, Leader: 1}})
	}
	h.startChannelTicks()
}

func (h *threeNodeHarness) Stop(t testing.TB) {
	t.Helper()
	if h.tickCancel != nil {
		h.tickCancel()
		h.tickWG.Wait()
	}
	for _, nodeID := range []uint64{1, 2, 3} {
		if node := h.nodes[nodeID]; node != nil {
			if err := node.Stop(context.Background()); err != nil {
				t.Fatalf("Stop(node=%d) error = %v", nodeID, err)
			}
		}
	}
	for _, service := range h.channels {
		_ = service.Close()
	}
}

func (h *threeNodeHarness) Node(nodeID uint64) *Node { return h.nodes[nodeID] }

func (h *threeNodeHarness) WaitSlotLeader(t testing.TB, slotID uint32) {
	t.Helper()
	waitUntil(t.(*testing.T), func() bool {
		for _, node := range h.nodes {
			route, err := node.RouteHashSlot(0)
			if err != nil || route.SlotID != slotID || route.Leader == 0 {
				return false
			}
		}
		return true
	})
}

func (h *threeNodeHarness) NonLeaderForSlot(t testing.TB, slotID uint32) *Node {
	t.Helper()
	leader := h.slots[1].leaders[slotID]
	for _, nodeID := range []uint64{1, 2, 3} {
		if nodeID != leader {
			return h.nodes[nodeID]
		}
	}
	t.Fatalf("missing non-leader for slot %d", slotID)
	return nil
}

func (h *threeNodeHarness) RequireCommandApplied(t testing.TB, slotID uint32, command []byte) {
	t.Helper()
	waitUntil(t.(*testing.T), func() bool {
		return h.applied.contains(slotID, command)
	})
}

func (h *threeNodeHarness) WaitChannelCommitted(t testing.TB, nodeID uint64, id channelv2.ChannelID, seq uint64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		res, err := h.nodes[nodeID].FetchChannel(context.Background(), channelv2.FetchRequest{ChannelID: id, FromSeq: seq, Limit: 1, MaxBytes: 1024})
		if err == nil && res.CommittedSeq >= seq && len(res.Messages) > 0 {
			return
		}
		h.tickChannels(context.Background())
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("node %d did not commit channel %v seq %d", nodeID, id, seq)
}

func (h *threeNodeHarness) controlSnapshot() control.Snapshot {
	return control.Snapshot{
		Revision:     1,
		ControllerID: 1,
		Nodes: []control.Node{
			{NodeID: 1, Addr: "127.0.0.1:10001", Roles: []control.Role{control.RoleData}, Status: control.NodeAlive},
			{NodeID: 2, Addr: "127.0.0.1:10002", Roles: []control.Role{control.RoleData}, Status: control.NodeAlive},
			{NodeID: 3, Addr: "127.0.0.1:10003", Roles: []control.Role{control.RoleData}, Status: control.NodeAlive},
		},
		Slots:     []control.SlotAssignment{{SlotID: 1, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 1, PreferredLeader: 1}},
		HashSlots: control.HashSlotTable{Revision: 1, Count: 4, Ranges: []control.HashSlotRange{{From: 0, To: 3, SlotID: 1}}},
	}
}

func (h *threeNodeHarness) startChannelTicks() {
	if len(h.channels) == 0 {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.tickCancel = cancel
	h.tickWG.Add(1)
	go func() {
		defer h.tickWG.Done()
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				h.tickChannels(ctx)
			}
		}
	}()
}

func (h *threeNodeHarness) tickChannels(ctx context.Context) {
	for _, nodeID := range []uint64{1, 2, 3} {
		if service := h.channels[nodeID]; service != nil {
			_ = service.Tick(ctx)
		}
	}
}

type smokeSlotRuntime struct {
	localNode uint64
	leaders   map[uint32]uint64
	applied   *smokeAppliedLog
}

func (s *smokeSlotRuntime) IsLocalLeader(slotID uint32) bool {
	return s.leaders[slotID] == s.localNode
}

func (s *smokeSlotRuntime) Propose(ctx context.Context, slotID uint32, payload []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !s.IsLocalLeader(slotID) {
		return propose.ErrNotLeader
	}
	_, command, err := propose.DecodePayload(payload)
	if err != nil {
		return err
	}
	s.applied.add(slotID, command)
	return nil
}

type smokeAppliedLog struct {
	mu       sync.Mutex
	commands map[uint32][][]byte
}

func newSmokeAppliedLog() *smokeAppliedLog {
	return &smokeAppliedLog{commands: make(map[uint32][][]byte)}
}

func (l *smokeAppliedLog) add(slotID uint32, command []byte) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.commands[slotID] = append(l.commands[slotID], append([]byte(nil), command...))
}

func (l *smokeAppliedLog) contains(slotID uint32, command []byte) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, item := range l.commands[slotID] {
		if bytes.Equal(item, command) {
			return true
		}
	}
	return false
}
