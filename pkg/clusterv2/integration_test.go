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
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
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
}

func TestClusterV2ThreeNodeFirstChannelAppendEnsuresMeta(t *testing.T) {
	channelID := channelv2.ChannelID{ID: "room-first-append", Type: 1}
	h := newThreeNodeHarness(t)
	h.WithEnsuredChannelMeta()
	h.Start(t)
	t.Cleanup(func() { h.Stop(t) })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	res, err := h.Node(2).AppendChannel(ctx, channelv2.AppendRequest{
		ChannelID:  channelID,
		CommitMode: channelv2.CommitModeQuorum,
		Message:    channelv2.Message{MessageID: 1003, Payload: []byte("first")},
	})
	if err != nil {
		t.Fatalf("AppendChannel(first append) error = %v", err)
	}
	if res.MessageSeq == 0 {
		t.Fatal("AppendChannel(first append) MessageSeq = 0, want committed sequence")
	}
	h.WaitChannelCommitted(t, 3, channelID, res.MessageSeq)
	meta, ok := h.ChannelMeta(channelID)
	if !ok {
		t.Fatalf("ensured meta missing for %v", channelID)
	}
	if meta.Leader != 1 || !equalChannelNodeIDs(meta.Replicas, []channelv2.NodeID{1, 2, 3}) || meta.MinISR != 2 {
		t.Fatalf("ensured meta = %#v, want slot leader channel meta", meta)
	}
}

func TestClusterV2ThreeNodeChannelAppendReplicatesToAllNodes(t *testing.T) {
	channelID := channelv2.ChannelID{ID: "room-replicate-all", Type: 1}
	payload := []byte("replicated")
	h := newThreeNodeHarness(t)
	h.WithEnsuredChannelMeta()
	h.Start(t)
	t.Cleanup(func() { h.Stop(t) })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	res, err := h.Node(2).AppendChannel(ctx, channelv2.AppendRequest{
		ChannelID:  channelID,
		CommitMode: channelv2.CommitModeQuorum,
		Message:    channelv2.Message{MessageID: 1004, Payload: payload},
	})
	if err != nil {
		t.Fatalf("AppendChannel(replicate all) error = %v", err)
	}
	if res.MessageSeq != 1 {
		t.Fatalf("AppendChannel(replicate all) MessageSeq = %d, want 1", res.MessageSeq)
	}

	for _, nodeID := range []uint64{1, 2, 3} {
		h.RequireChannelMessage(t, nodeID, channelID, res.MessageSeq, 1004, payload)
	}
}

func TestClusterV2ThreeNodeChannelAppendForwardsFromNonLeader(t *testing.T) {
	channelID := channelv2.ChannelID{ID: "room-forward", Type: 1}
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
	res, err := h.Node(2).AppendChannel(ctx, channelv2.AppendRequest{
		ChannelID:            channelID,
		CommitMode:           channelv2.CommitModeQuorum,
		Message:              channelv2.Message{MessageID: 1002, Payload: []byte("forwarded")},
		ExpectedChannelEpoch: 1,
		ExpectedLeaderEpoch:  1,
	})
	if err != nil {
		t.Fatalf("AppendChannel(non-leader) error = %v", err)
	}
	if res.MessageSeq == 0 {
		t.Fatal("AppendChannel(non-leader) MessageSeq = 0, want committed sequence")
	}
	h.WaitChannelCommitted(t, 2, channelID, res.MessageSeq)
}

type threeNodeHarness struct {
	network    *clusternet.LocalNetwork
	nodes      map[uint64]*Node
	stores     map[uint64]*channelstore.MemoryFactory
	slots      map[uint64]*smokeSlotRuntime
	metas      []channelv2.Meta
	ensureMeta bool
	metaSource channels.ChannelMetaSource
	applied    *smokeAppliedLog
}

func newThreeNodeHarness(t testing.TB) *threeNodeHarness {
	t.Helper()
	return &threeNodeHarness{
		network: clusternet.NewLocalNetwork(),
		nodes:   make(map[uint64]*Node),
		stores:  make(map[uint64]*channelstore.MemoryFactory),
		slots:   make(map[uint64]*smokeSlotRuntime),
		applied: newSmokeAppliedLog(),
	}
}

func (h *threeNodeHarness) WithStaticChannelMeta(meta channelv2.Meta) {
	h.metas = append(h.metas, meta)
}

func (h *threeNodeHarness) WithEnsuredChannelMeta() {
	h.ensureMeta = true
}

func (h *threeNodeHarness) ChannelMeta(id channelv2.ChannelID) (channelv2.Meta, bool) {
	if h.metaSource == nil {
		return channelv2.Meta{}, false
	}
	meta, err := h.metaSource.ResolveChannelMeta(context.Background(), id)
	return meta, err == nil
}

func (h *threeNodeHarness) Start(t testing.TB) {
	t.Helper()
	snapshot := h.controlSnapshot()
	metaSource := h.channelMetaSource()
	for _, nodeID := range []uint64{1, 2, 3} {
		slotRuntime := &smokeSlotRuntime{localNode: nodeID, leaders: map[uint32]uint64{1: 1}, applied: h.applied}
		cfg := Config{NodeID: nodeID, ListenAddr: fmt.Sprintf("127.0.0.1:%d", 10000+nodeID), DataDir: t.TempDir()}
		cfg.Channel.TickInterval = time.Millisecond
		var opts []Option
		if metaSource != nil {
			storeFactory := channelstore.NewMemoryFactory()
			service, err := channels.NewService(channels.Config{
				LocalNode:  channelv2.NodeID(nodeID),
				Store:      storeFactory,
				Transport:  channels.NewTransportClient(h.network),
				MetaSource: metaSource,
			})
			if err != nil {
				t.Fatalf("channels.NewService(node=%d) error = %v", nodeID, err)
			}
			opts = append(opts, WithChannels(service))
			channels.RegisterServiceHandlers(h.network, nodeID, service)
			h.stores[nodeID] = storeFactory
		}
		opts = append(opts, withController(control.NewStaticController(snapshot)), withSlotReconciler(&recordingReconciler{}))
		node, err := New(cfg, opts...)
		if err != nil {
			t.Fatalf("New(node=%d) error = %v", nodeID, err)
		}
		WithProposer(propose.NewService(propose.Config{LocalNode: nodeID, Router: node.router, Slots: slotRuntime, Forward: propose.NewNetworkForwardClient(h.network)}))(node)
		h.network.Register(nodeID, clusternet.RPCSlotForwardPropose, propose.NewForwardHandler(slotRuntime))
		h.nodes[nodeID] = node
		h.slots[nodeID] = slotRuntime
	}
	for _, nodeID := range []uint64{1, 2, 3} {
		if err := h.nodes[nodeID].Start(context.Background()); err != nil {
			t.Fatalf("Start(node=%d) error = %v", nodeID, err)
		}
		h.nodes[nodeID].router.UpdateSlotLeaders([]routing.SlotStatus{{SlotID: 1, Leader: 1}})
	}
}

func (h *threeNodeHarness) channelMetaSource() channels.ChannelMetaSource {
	if h.ensureMeta {
		if h.metaSource == nil {
			store := newSmokeRuntimeMetaStore()
			h.metaSource = channels.NewSlotMetaSource(store, channels.SlotMetaSourceOptions{
				Placement: smokePlacementResolver{leader: 1, peers: []channelv2.NodeID{1, 2, 3}, minISR: 2},
				Writer:    store,
			})
		}
		return h.metaSource
	}
	if len(h.metas) > 0 {
		return channels.NewStaticMetaSource(h.metas)
	}
	return nil
}

func (h *threeNodeHarness) Stop(t testing.TB) {
	t.Helper()
	for _, nodeID := range []uint64{1, 2, 3} {
		if node := h.nodes[nodeID]; node != nil {
			if err := node.Stop(context.Background()); err != nil {
				t.Fatalf("Stop(node=%d) error = %v", nodeID, err)
			}
		}
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
		messages, err := h.readChannelMessages(nodeID, id, seq)
		if err == nil && len(messages) > 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("node %d did not commit channel %v seq %d", nodeID, id, seq)
}

func (h *threeNodeHarness) RequireChannelMessage(t testing.TB, nodeID uint64, id channelv2.ChannelID, seq uint64, messageID uint64, payload []byte) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var lastErr error
	var lastMessages []channelv2.Message
	for time.Now().Before(deadline) {
		messages, err := h.readChannelMessages(nodeID, id, seq)
		if err == nil && len(messages) > 0 {
			msg := messages[0]
			if msg.MessageSeq == seq && msg.MessageID == messageID && bytes.Equal(msg.Payload, payload) {
				return
			}
			t.Fatalf("node %d fetched message = %#v, want seq=%d messageID=%d payload=%q", nodeID, msg, seq, messageID, payload)
		}
		lastErr = err
		if err == nil {
			lastMessages = append([]channelv2.Message(nil), messages...)
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("node %d did not replicate channel %v seq %d; lastErr=%v lastMessages=%#v", nodeID, id, seq, lastErr, lastMessages)
}

func (h *threeNodeHarness) readChannelMessages(nodeID uint64, id channelv2.ChannelID, seq uint64) ([]channelv2.Message, error) {
	factory := h.stores[nodeID]
	if factory == nil {
		return nil, channelv2.ErrChannelNotFound
	}
	cs, err := factory.ChannelStore(channelv2.ChannelKeyForID(id), id)
	if err != nil {
		return nil, err
	}
	read, err := cs.ReadCommitted(context.Background(), channelstore.ReadCommittedRequest{FromSeq: seq, MaxSeq: seq, Limit: 1, MaxBytes: 1024})
	if err != nil {
		return nil, err
	}
	return read.Messages, nil
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

type smokeRuntimeMetaStore struct {
	mu    sync.Mutex
	metas map[smokeRuntimeMetaKey]metadb.ChannelRuntimeMeta
}

type smokeRuntimeMetaKey struct {
	id  string
	typ int64
}

func newSmokeRuntimeMetaStore() *smokeRuntimeMetaStore {
	return &smokeRuntimeMetaStore{metas: make(map[smokeRuntimeMetaKey]metadb.ChannelRuntimeMeta)}
}

func (s *smokeRuntimeMetaStore) GetChannelRuntimeMeta(_ context.Context, channelID string, channelType int64) (metadb.ChannelRuntimeMeta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	meta, ok := s.metas[smokeRuntimeMetaKey{id: channelID, typ: channelType}]
	if !ok {
		return metadb.ChannelRuntimeMeta{}, metadb.ErrNotFound
	}
	return cloneRuntimeMeta(meta), nil
}

func (s *smokeRuntimeMetaStore) UpsertChannelRuntimeMeta(_ context.Context, meta metadb.ChannelRuntimeMeta) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	meta = metadb.NormalizeChannelRuntimeMeta(meta)
	s.metas[smokeRuntimeMetaKey{id: meta.ChannelID, typ: meta.ChannelType}] = cloneRuntimeMeta(meta)
	return nil
}

func cloneRuntimeMeta(meta metadb.ChannelRuntimeMeta) metadb.ChannelRuntimeMeta {
	meta.Replicas = append([]uint64(nil), meta.Replicas...)
	meta.ISR = append([]uint64(nil), meta.ISR...)
	return meta
}

type smokePlacementResolver struct {
	leader channelv2.NodeID
	peers  []channelv2.NodeID
	minISR int
}

func (r smokePlacementResolver) ResolveChannelPlacement(context.Context, channelv2.ChannelID) (channels.ChannelPlacement, error) {
	return channels.ChannelPlacement{Leader: r.leader, Replicas: append([]channelv2.NodeID(nil), r.peers...), MinISR: r.minISR}, nil
}

func equalChannelNodeIDs(a, b []channelv2.NodeID) bool {
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
