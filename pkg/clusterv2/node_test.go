package clusterv2

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channelv2"
	channeltransport "github.com/WuKongIM/WuKongIM/pkg/channelv2/transport"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/channels"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/internal/lifecycle"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/propose"
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

func TestNodeProposeDelegatesToService(t *testing.T) {
	proposer := &recordingProposer{}
	node, err := New(validNodeConfig(t), withProposer(proposer))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := node.Propose(context.Background(), ProposeRequest{Key: "u1", Command: []byte("cmd")}); err != nil {
		t.Fatalf("Propose() error = %v", err)
	}
	if proposer.calls != 1 || proposer.last.Key != "u1" {
		t.Fatalf("proposer calls=%d last=%#v, want key u1", proposer.calls, proposer.last)
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
	node, err := New(validNodeConfig(t), withChannels(svc))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	_, err = node.AppendChannel(context.Background(), channelv2.AppendRequest{ChannelID: channelv2.ChannelID{ID: "room", Type: 1}})
	if err != nil {
		t.Fatalf("AppendChannel() error = %v", err)
	}
	if runtime.appendCalls != 1 {
		t.Fatalf("append calls = %d, want 1", runtime.appendCalls)
	}
}

func TestNodeStopClosesChannelService(t *testing.T) {
	runtime := &nodeChannelRuntime{}
	svc, err := channels.NewService(channels.Config{Runtime: runtime})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	node, err := New(validNodeConfig(t), withChannels(svc))
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
func (r *nodeChannelRuntime) Fetch(context.Context, channelv2.FetchRequest) (channelv2.FetchResult, error) {
	return channelv2.FetchResult{}, nil
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
