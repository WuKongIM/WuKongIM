package clusterv2

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channelv2"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
	channeltransport "github.com/WuKongIM/WuKongIM/pkg/channelv2/transport"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/channels"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/internal/lifecycle"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/propose"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/routing"
)

func TestNodeStartStartsResourcesInOrder(t *testing.T) {
	var calls []string
	node, err := New(validNodeConfig(t), withResources(
		namedTestResource("net", &recordingResource{name: "net", calls: &calls}),
		namedTestResource("control", &recordingResource{name: "control", calls: &calls}),
	))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = node.Stop(context.Background()) })
	want := []string{"start:net", "start:control"}
	if !equalStrings(calls, want) {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}
}

func TestNodeStopStopsResourcesInReverseOrder(t *testing.T) {
	var calls []string
	node, err := New(validNodeConfig(t), withResources(
		namedTestResource("net", &recordingResource{name: "net", calls: &calls}),
		namedTestResource("control", &recordingResource{name: "control", calls: &calls}),
	))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	calls = nil
	if err := node.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	want := []string{"stop:control", "stop:net"}
	if !equalStrings(calls, want) {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}
}

func TestNodeStartStopsStartedResourcesOnFailure(t *testing.T) {
	var calls []string
	boom := errors.New("boom")
	node, err := New(validNodeConfig(t), withResources(
		namedTestResource("net", &recordingResource{name: "net", calls: &calls}),
		namedTestResource("control", &recordingResource{name: "control", calls: &calls, startErr: boom}),
	))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := node.Start(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("Start() error = %v, want boom", err)
	}
	want := []string{"start:net", "start:control", "stop:net"}
	if !equalStrings(calls, want) {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}
}

func TestNodeRejectsInvalidConfig(t *testing.T) {
	if _, err := New(Config{}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("New() error = %v, want ErrInvalidConfig", err)
	}
}

func TestNodeRejectsInvalidChannelTickInterval(t *testing.T) {
	cfg := validNodeConfig(t)
	cfg.Channel.TickInterval = -time.Millisecond
	if _, err := New(cfg); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("New() error = %v, want ErrInvalidConfig", err)
	}
}

func TestStoppedNodeRejectsForegroundWithErrStopping(t *testing.T) {
	node, err := New(validNodeConfig(t))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := node.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if _, err := node.RouteKey("u1"); !errors.Is(err, ErrStopping) {
		t.Fatalf("RouteKey() error = %v, want ErrStopping", err)
	}
}

func namedTestResource(name string, resource lifecycle.Resource) lifecycle.NamedResource {
	return lifecycle.NamedResource{Name: name, Resource: resource}
}

func validNodeConfig(t *testing.T) Config {
	t.Helper()
	return Config{NodeID: 1, ListenAddr: "127.0.0.1:0", DataDir: t.TempDir()}
}

func freeTCPAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

type recordingResource struct {
	name     string
	calls    *[]string
	startErr error
	stopErr  error
}

func (r *recordingResource) Start(context.Context) error {
	*r.calls = append(*r.calls, "start:"+r.name)
	return r.startErr
}

func (r *recordingResource) Stop(context.Context) error {
	*r.calls = append(*r.calls, "stop:"+r.name)
	return r.stopErr
}

func equalStrings(a, b []string) bool {
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

func TestNodeStartAppliesControlSnapshot(t *testing.T) {
	controller := control.NewStaticController(nodeControlSnapshot())
	reconciler := &recordingReconciler{}
	node, err := New(validNodeConfig(t), withController(controller), withSlotReconciler(reconciler))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = node.Stop(context.Background()) })
	if _, err := node.RouteHashSlot(0); !errors.Is(err, ErrNoSlotLeader) {
		t.Fatalf("RouteHashSlot() error = %v, want ErrNoSlotLeader before status observation", err)
	}
	if reconciler.calls != 1 || reconciler.last.Revision != 1 {
		t.Fatalf("reconciler calls=%d revision=%d, want one call revision 1", reconciler.calls, reconciler.last.Revision)
	}
	if snap := node.Snapshot(); !snap.RoutesReady || !snap.SlotsReady || snap.StateRevision != 1 {
		t.Fatalf("Snapshot() = %#v, want ready revision 1", snap)
	}
}

func TestNodeControlWatchUpdatesRouteRevision(t *testing.T) {
	controller := control.NewStaticController(nodeControlSnapshot())
	node, err := New(validNodeConfig(t), withController(controller), withSlotReconciler(&recordingReconciler{}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = node.Stop(context.Background()) })
	next := nodeControlSnapshot()
	next.Revision = 2
	next.HashSlots.Revision = 2
	if err := controller.Publish(next); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	waitUntil(t, func() bool {
		return node.Snapshot().StateRevision == 2
	})
}

func TestNodeControlWatchNodeOnlyChangeSkipsSlotReconcile(t *testing.T) {
	controller := control.NewStaticController(nodeControlSnapshot())
	reconciler := &recordingReconciler{}
	node, err := New(validNodeConfig(t), withController(controller), withSlotReconciler(reconciler))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = node.Stop(context.Background()) })
	if reconciler.calls != 1 {
		t.Fatalf("initial reconciler calls = %d, want 1", reconciler.calls)
	}

	next := nodeControlSnapshot()
	next.Revision = 2
	next.Nodes[1].Status = control.NodeDown
	if err := controller.Publish(next); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	waitUntil(t, func() bool {
		return node.Snapshot().StateRevision == 2
	})
	if reconciler.calls != 1 {
		t.Fatalf("reconciler calls = %d, want node-only change to skip slot reconcile", reconciler.calls)
	}
}

func TestNodeControlWatchSlotChangeReconcilesSlots(t *testing.T) {
	controller := control.NewStaticController(nodeControlSnapshot())
	reconciler := &recordingReconciler{}
	node, err := New(validNodeConfig(t), withController(controller), withSlotReconciler(reconciler))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = node.Stop(context.Background()) })
	if reconciler.calls != 1 {
		t.Fatalf("initial reconciler calls = %d, want 1", reconciler.calls)
	}

	next := nodeControlSnapshot()
	next.Revision = 2
	next.Slots[0].ConfigEpoch = 2
	next.Slots[0].PreferredLeader = 2
	if err := controller.Publish(next); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	waitUntil(t, func() bool {
		return reconciler.calls == 2
	})
	if reconciler.last.Slots[0].ConfigEpoch != 2 || reconciler.last.Slots[0].PreferredLeader != 2 {
		t.Fatalf("last reconciled snapshot = %#v, want slot epoch 2 preferred leader 2", reconciler.last)
	}
}

func TestControlSnapshotChangesDetectTasks(t *testing.T) {
	previous := nodeControlSnapshot()
	next := previous.Clone()
	next.Revision = 2
	next.Tasks = []control.ReconcileTask{{
		TaskID:      "bootstrap-1",
		SlotID:      1,
		Kind:        control.TaskKindBootstrap,
		TargetNode:  1,
		TargetPeers: []uint64{1, 2, 3},
		ConfigEpoch: 1,
	}}

	changes := snapshotChanges(previous, next)
	if !changes.tasks || changes.nodes || changes.slots || changes.hashSlots {
		t.Fatalf("snapshotChanges() = %#v, want only tasks changed", changes)
	}
}

func TestControlSnapshotChangesDetectHashSlots(t *testing.T) {
	previous := nodeControlSnapshot()
	next := previous.Clone()
	next.Revision = 2
	next.HashSlots.Revision = 2

	changes := snapshotChanges(previous, next)
	if !changes.hashSlots || changes.nodes || changes.slots || changes.tasks {
		t.Fatalf("snapshotChanges() = %#v, want only hash slots changed", changes)
	}
}

func TestNodeProposeDelegatesToService(t *testing.T) {
	proposer := &recordingProposer{}
	node, err := New(validNodeConfig(t), WithProposer(proposer))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = node.Stop(context.Background()) })
	if err := node.Propose(context.Background(), ProposeRequest{Key: "u1", Command: []byte("cmd")}); err != nil {
		t.Fatalf("Propose() error = %v", err)
	}
	if proposer.calls != 1 || proposer.last.Key != "u1" {
		t.Fatalf("proposer calls=%d last=%#v, want key u1", proposer.calls, proposer.last)
	}
}

func TestNodeInitializesDefaultProposerWhenOptionMissing(t *testing.T) {
	node, err := New(validNodeConfig(t), withController(control.NewStaticController(nodeControlSnapshot())))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = node.Stop(context.Background()) })
	node.router.UpdateSlotLeaders([]routing.SlotStatus{{SlotID: 1, Leader: 1}})

	err = node.Propose(context.Background(), ProposeRequest{Key: "u1", Command: []byte("cmd")})
	if errors.Is(err, ErrNotStarted) {
		t.Fatalf("Propose() error = %v, want default proposer initialized", err)
	}
}

func TestNodeInitializesDefaultChannelsWhenOptionMissing(t *testing.T) {
	node, err := New(validNodeConfig(t))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = node.Stop(context.Background()) })
	_, err = node.AppendChannel(context.Background(), channelv2.AppendRequest{ChannelID: channelv2.ChannelID{ID: "missing", Type: 1}})
	if errors.Is(err, ErrNotStarted) {
		t.Fatalf("AppendChannel() error = %v, want default channel service initialized", err)
	}
}

func TestNodeInitializesDefaultControllerV2WhenOptionMissing(t *testing.T) {
	cfg := validNodeConfig(t)
	cfg.Channel.TickInterval = time.Millisecond
	cfg.Control.ClusterID = "node-default-control"
	cfg.Slots.InitialSlotCount = 1
	cfg.Slots.HashSlotCount = 4
	cfg.Slots.ReplicaCount = 1

	node, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := node.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = node.Stop(context.Background()) })

	snap := node.Snapshot()
	if !snap.RoutesReady || snap.StateRevision == 0 || snap.SlotCount != 1 || snap.HashSlotCount != 4 {
		t.Fatalf("Snapshot() = %#v, want ControllerV2-backed ready routes", snap)
	}
	route, err := node.RouteHashSlot(0)
	if err != nil && !errors.Is(err, ErrNoSlotLeader) {
		t.Fatalf("RouteHashSlot() error = %v, want route or no slot leader", err)
	}
	if err == nil && route.SlotID != 1 {
		t.Fatalf("RouteHashSlot() = %#v, want slot 1", route)
	}
}

func TestNodeDefaultControllerV2ThreeVotersConvergeOverTransport(t *testing.T) {
	addrs := []string{freeTCPAddr(t), freeTCPAddr(t), freeTCPAddr(t)}
	voters := []ControlVoter{
		{NodeID: 1, Addr: addrs[0]},
		{NodeID: 2, Addr: addrs[1]},
		{NodeID: 3, Addr: addrs[2]},
	}
	nodes := make([]*Node, 0, len(voters))
	for _, voter := range voters {
		cfg := Config{NodeID: voter.NodeID, ListenAddr: voter.Addr, DataDir: t.TempDir()}
		cfg.Control.ClusterID = "node-default-control-three"
		cfg.Control.Voters = voters
		cfg.Control.AllowBootstrap = true
		cfg.Slots.InitialSlotCount = 1
		cfg.Slots.HashSlotCount = 4
		cfg.Slots.ReplicaCount = 3
		node, err := New(cfg)
		if err != nil {
			t.Fatalf("New(node=%d) error = %v", voter.NodeID, err)
		}
		nodes = append(nodes, node)
	}

	startCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	startErrs := make(chan error, len(nodes))
	for _, node := range nodes {
		node := node
		go func() { startErrs <- node.Start(startCtx) }()
		t.Cleanup(func() { _ = node.Stop(context.Background()) })
	}
	for range nodes {
		if err := <-startErrs; err != nil {
			t.Fatalf("Start() error = %v", err)
		}
	}

	readyCtx, readyCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer readyCancel()
	if err := WaitClusterReady(readyCtx, nodes...); err != nil {
		t.Fatalf("WaitClusterReady() error = %v", err)
	}
}

func TestNodeDefaultChannelsUseDurableMessageDBStore(t *testing.T) {
	cfg := validNodeConfig(t)
	node, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	channelID := channelv2.ChannelID{ID: "durable", Type: 1}
	applyDefaultChannelMeta(t, node, channelID)
	if _, err := node.AppendChannel(context.Background(), channelv2.AppendRequest{
		ChannelID: channelID,
		Message:   channelv2.Message{MessageID: 100, Payload: []byte("persisted")},
	}); err != nil {
		t.Fatalf("AppendChannel() error = %v", err)
	}
	if err := node.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("restart Start() error = %v", err)
	}
	t.Cleanup(func() { _ = node.Stop(context.Background()) })
	applyDefaultChannelMeta(t, node, channelID)
	second, err := node.AppendChannel(context.Background(), channelv2.AppendRequest{
		ChannelID: channelID,
		Message:   channelv2.Message{MessageID: 101, Payload: []byte("after-restart")},
	})
	if err != nil {
		t.Fatalf("restart AppendChannel() error = %v", err)
	}
	if second.MessageSeq != 2 {
		t.Fatalf("restart AppendChannel() MessageSeq = %d, want 2 from durable message DB LEO", second.MessageSeq)
	}
}

func TestNodeStartMarksDefaultChannelsReadyWithoutController(t *testing.T) {
	node, err := New(validNodeConfig(t))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = node.Stop(context.Background()) })
	if snap := node.Snapshot(); !snap.ChannelsReady {
		t.Fatalf("Snapshot() ChannelsReady = false, want true")
	}
}

func TestNodeStopDiscardsDefaultChannelsForRestart(t *testing.T) {
	node, err := New(validNodeConfig(t))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := node.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if node.channels != nil {
		t.Fatal("default channels retained after Stop, want discarded")
	}
	if node.defaultChannelStore != nil {
		t.Fatal("default channel store retained after Stop, want discarded")
	}
}

func TestNodeStartFailureDiscardsDefaultChannels(t *testing.T) {
	boom := errors.New("boom")
	var calls []string
	node, err := New(validNodeConfig(t), withResources(namedTestResource("boom", &recordingResource{calls: &calls, startErr: boom})))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := node.Start(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("Start() error = %v, want boom", err)
	}
	if node.channels != nil {
		t.Fatal("default channels retained after failed Start, want discarded")
	}
	if node.defaultChannelStore != nil {
		t.Fatal("default channel store retained after failed Start, want discarded")
	}
}

func TestNodeWithChannelsOptionOverridesDefault(t *testing.T) {
	channelID := channelv2.ChannelID{ID: "room", Type: 1}
	svc, err := channels.NewService(channels.Config{
		LocalNode: 1,
		Store:     channelstore.NewMemoryFactory(),
		MetaSource: channels.NewStaticMetaSource([]channelv2.Meta{{
			ID:          channelID,
			Epoch:       1,
			LeaderEpoch: 1,
			Leader:      1,
			Replicas:    []channelv2.NodeID{1},
			ISR:         []channelv2.NodeID{1},
			MinISR:      1,
			Status:      channelv2.StatusActive,
		}}),
	})
	if err != nil {
		t.Fatalf("channels.NewService() error = %v", err)
	}
	node, err := New(validNodeConfig(t), WithChannels(svc))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = node.Stop(context.Background()) })

	res, err := node.AppendChannel(context.Background(), channelv2.AppendRequest{
		ChannelID:            channelID,
		CommitMode:           channelv2.CommitModeLocal,
		ExpectedChannelEpoch: 1,
		ExpectedLeaderEpoch:  1,
		Message:              channelv2.Message{MessageID: 100, Payload: []byte("hello")},
	})
	if err != nil {
		t.Fatalf("AppendChannel() error = %v", err)
	}
	if res.MessageSeq == 0 {
		t.Fatal("AppendChannel() MessageSeq = 0, want committed sequence")
	}
}

type recordingReconciler struct {
	calls int
	last  control.Snapshot
}

func (r *recordingReconciler) Reconcile(_ context.Context, snap control.Snapshot) error {
	r.calls++
	r.last = snap.Clone()
	return nil
}

type recordingProposer struct {
	calls int
	last  propose.Request
}

func (p *recordingProposer) Propose(_ context.Context, req propose.Request) error {
	p.calls++
	p.last = req
	return nil
}

func nodeControlSnapshot() control.Snapshot {
	return control.Snapshot{
		Revision:     1,
		ControllerID: 1,
		Nodes: []control.Node{
			{NodeID: 1, Addr: "127.0.0.1:1001", Roles: []control.Role{control.RoleData}, Status: control.NodeAlive},
			{NodeID: 2, Addr: "127.0.0.1:1002", Roles: []control.Role{control.RoleData}, Status: control.NodeAlive},
			{NodeID: 3, Addr: "127.0.0.1:1003", Roles: []control.Role{control.RoleData}, Status: control.NodeAlive},
		},
		Slots:     []control.SlotAssignment{{SlotID: 1, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 1, PreferredLeader: 1}},
		HashSlots: control.HashSlotTable{Revision: 1, Count: 4, Ranges: []control.HashSlotRange{{From: 0, To: 3, SlotID: 1}}},
	}
}

func waitUntil(t *testing.T, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met before deadline")
}

func TestNodeAppendChannelDelegatesToService(t *testing.T) {
	runtime := &nodeChannelRuntime{}
	svc, err := channels.NewService(channels.Config{Runtime: runtime})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	node, err := New(validNodeConfig(t), WithChannels(svc))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = node.Stop(context.Background()) })
	_, err = node.AppendChannel(context.Background(), channelv2.AppendRequest{ChannelID: channelv2.ChannelID{ID: "room", Type: 1}})
	if err != nil {
		t.Fatalf("AppendChannel() error = %v", err)
	}
	if runtime.appendCalls != 1 {
		t.Fatalf("append calls = %d, want 1", runtime.appendCalls)
	}
}

func applyDefaultChannelMeta(t *testing.T, node *Node, channelID channelv2.ChannelID) {
	t.Helper()
	svc, ok := node.channels.(*channels.Service)
	if !ok {
		t.Fatalf("default channels type = %T, want *channels.Service", node.channels)
	}
	if err := svc.ApplyMeta(channelv2.Meta{
		Key:         channelv2.ChannelKeyForID(channelID),
		ID:          channelID,
		Epoch:       1,
		LeaderEpoch: 1,
		Leader:      1,
		Replicas:    []channelv2.NodeID{1},
		ISR:         []channelv2.NodeID{1},
		MinISR:      1,
		Status:      channelv2.StatusActive,
	}); err != nil {
		t.Fatalf("ApplyMeta() error = %v", err)
	}
}

func TestNodeStopClosesChannelService(t *testing.T) {
	runtime := &nodeChannelRuntime{}
	svc, err := channels.NewService(channels.Config{Runtime: runtime})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	node, err := New(validNodeConfig(t), WithChannels(svc))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := node.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if runtime.closeCalls != 1 {
		t.Fatalf("close calls = %d, want 1", runtime.closeCalls)
	}
}

type nodeChannelRuntime struct {
	appendCalls int
	closeCalls  int
}

func (r *nodeChannelRuntime) ApplyMeta(channelv2.Meta) error { return nil }
func (r *nodeChannelRuntime) Append(context.Context, channelv2.AppendRequest) (channelv2.AppendResult, error) {
	r.appendCalls++
	return channelv2.AppendResult{}, nil
}
func (r *nodeChannelRuntime) AppendBatch(context.Context, channelv2.AppendBatchRequest) (channelv2.AppendBatchResult, error) {
	return channelv2.AppendBatchResult{}, nil
}
func (r *nodeChannelRuntime) Tick(context.Context) error { return nil }
func (r *nodeChannelRuntime) Close() error {
	r.closeCalls++
	return nil
}
func (r *nodeChannelRuntime) HandlePull(context.Context, channeltransport.PullRequest) (channeltransport.PullResponse, error) {
	return channeltransport.PullResponse{}, nil
}
func (r *nodeChannelRuntime) HandleAck(context.Context, channeltransport.AckRequest) error {
	return nil
}
func (r *nodeChannelRuntime) HandlePullHint(context.Context, channeltransport.PullHintRequest) error {
	return nil
}
func (r *nodeChannelRuntime) HandleNotify(context.Context, channeltransport.NotifyRequest) error {
	return nil
}
