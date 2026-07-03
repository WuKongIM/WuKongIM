package app

import (
	"context"
	"sync"
	"testing"
	"time"

	accessnode "github.com/WuKongIM/WuKongIM/internalv2/access/node"
	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	cv2 "github.com/WuKongIM/WuKongIM/pkg/controller"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

var _ accessnode.ControllerVoterReadinessProvider = (*App)(nil)
var _ accessnode.ControllerVoterPreparer = (*App)(nil)

func TestAppWiresSeedJoinLoopWhenJoinConfigPresent(t *testing.T) {
	cluster := &fakeManagerCluster{
		nodeID: 4,
		snapshot: control.Snapshot{
			Nodes: []control.Node{
				{NodeID: 2, Addr: "10.0.0.2:11110", Roles: []control.Role{control.RoleData}, JoinState: control.NodeJoinStateActive},
				{NodeID: 1, Addr: "10.0.0.1:11110", Roles: []control.Role{control.RoleData}, JoinState: control.NodeJoinStateActive},
				{NodeID: 4, Addr: "10.0.0.4:11110", Roles: []control.Role{control.RoleData}, JoinState: control.NodeJoinStateJoining},
			},
		},
	}
	app, err := newTestApp(t, Config{
		NodeID: 4,
		Cluster: clusterpkg.Config{
			NodeID: 4,
			Control: clusterpkg.ControlConfig{
				ClusterID: "cluster-a",
			},
			Join: clusterpkg.JoinConfig{
				Seeds:         []string{"10.0.0.2:11110", "10.0.0.1:11110"},
				AdvertiseAddr: "10.0.0.4:11110",
				Token:         "join-secret",
			},
		},
	}, WithCluster(cluster), WithGateway(nil))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	loop, ok := app.seedJoinLoop.(*seedJoinLoop)
	if !ok || loop == nil {
		t.Fatalf("seedJoinLoop = %#v, want concrete loop", app.seedJoinLoop)
	}
	if loop.cfg.NodeID != 4 || loop.cfg.AdvertiseAddr != "10.0.0.4:11110" ||
		loop.cfg.ClusterID != "cluster-a" || loop.cfg.JoinToken != "join-secret" || loop.cfg.CapacityWeight != 1 {
		t.Fatalf("seed join config = %#v, want node 4 cluster-a token and default capacity 1", loop.cfg)
	}
	if len(loop.cfg.Seeds) != 2 || loop.cfg.Seeds[0] != 1 || loop.cfg.Seeds[1] != 2 {
		t.Fatalf("seed ids = %#v, want stable ids [1 2]", loop.cfg.Seeds)
	}
}

func TestAppSkipsSeedJoinLoopWithoutJoinConfig(t *testing.T) {
	app, err := newTestApp(t, Config{NodeID: 4}, WithCluster(&fakeManagerCluster{nodeID: 4}), WithGateway(nil))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if app.seedJoinLoop != nil {
		t.Fatalf("seedJoinLoop = %#v, want nil without seed-join config", app.seedJoinLoop)
	}
}

func TestAppNodeReadinessReportsMirrorClusterAndRuntimeGates(t *testing.T) {
	cluster := &fakeReadinessCluster{
		fakeManagerCluster: fakeManagerCluster{
			nodeID: 4,
			snapshot: control.Snapshot{
				ClusterID: "cluster-a",
				Revision:  22,
				Nodes: []control.Node{{
					NodeID:    4,
					Addr:      "10.0.0.4:11110",
					Roles:     []control.Role{control.RoleData},
					JoinState: control.NodeJoinStateJoining,
				}},
			},
		},
		snapshot: clusterpkg.Snapshot{
			RoutesReady:   true,
			SlotsReady:    true,
			ChannelsReady: true,
			HashSlotCount: 2,
		},
		routes: map[uint16]clusterpkg.Route{
			0: {HashSlot: 0, SlotID: 1, Leader: 4},
			1: {HashSlot: 1, SlotID: 1, Leader: 4},
		},
	}
	app, err := newTestApp(t, Config{
		NodeID: 4,
		Cluster: clusterpkg.Config{
			NodeID:  4,
			Control: clusterpkg.ControlConfig{ClusterID: "cluster-a"},
		},
	}, WithCluster(cluster), WithGateway(nil))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	app.lifecycleMu.Lock()
	app.clusterStarted = true
	app.gatewayStarted = true
	app.lifecycleMu.Unlock()

	got, err := app.NodeReadiness(context.Background(), accessnode.NodeReadinessRequest{NodeID: 4, ClusterID: "cluster-a"})
	if err != nil {
		t.Fatalf("NodeReadiness() error = %v", err)
	}
	if !got.Ready || got.MirrorClusterID != "cluster-a" || got.MirrorRevision != 22 {
		t.Fatalf("NodeReadiness() = %#v, want ready mirror cluster-a revision 22", got)
	}
	if !got.TransportReady || !got.ControlReady || !got.RuntimeReady || got.ExpectedClusterID != "cluster-a" {
		t.Fatalf("NodeReadiness() = %#v, want transport/control/runtime ready and expected cluster-a", got)
	}
}

func TestAppControllerVoterReadinessMapsNodeReadiness(t *testing.T) {
	cluster := &fakeReadinessCluster{
		fakeManagerCluster: fakeManagerCluster{
			nodeID: 4,
			snapshot: control.Snapshot{
				ClusterID: "cluster-a",
				Revision:  22,
				Nodes: []control.Node{{
					NodeID:    4,
					Addr:      "10.0.0.4:11110",
					Roles:     []control.Role{control.RoleData},
					JoinState: control.NodeJoinStateJoining,
				}},
			},
			controllerRaftStatus: clusterpkg.ControllerRaftStatus{
				NodeID:       4,
				Role:         "follower",
				LeaderID:     1,
				AppliedIndex: 77,
				Voters:       []uint64{1, 2, 4},
			},
		},
		snapshot: clusterpkg.Snapshot{
			RoutesReady:   true,
			SlotsReady:    true,
			ChannelsReady: true,
			HashSlotCount: 1,
		},
		routes: map[uint16]clusterpkg.Route{
			0: {HashSlot: 0, SlotID: 1, Leader: 4},
		},
	}
	app, err := newTestApp(t, Config{
		NodeID: 4,
		Cluster: clusterpkg.Config{
			NodeID:  4,
			Control: clusterpkg.ControlConfig{ClusterID: "cluster-a"},
		},
	}, WithCluster(cluster), WithGateway(nil))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	app.lifecycleMu.Lock()
	app.clusterStarted = true
	app.gatewayStarted = true
	app.lifecycleMu.Unlock()

	got, err := app.ControllerVoterReadiness(context.Background(), accessnode.ControllerVoterReadinessRequest{NodeID: 4, ClusterID: "cluster-a"})
	if err != nil {
		t.Fatalf("ControllerVoterReadiness() error = %v", err)
	}
	if got.NodeID != 4 || got.ClusterID != "cluster-a" || !got.CanPrepare || got.MirrorRevision != 22 {
		t.Fatalf("ControllerVoterReadiness() = %#v, want node 4 cluster-a ready revision 22", got)
	}
	if !got.IsVoter || got.ControlLeaderID != 1 || got.ConfigIndex != 77 || !sameSeedJoinUint64s(got.Voters, []uint64{1, 2, 4}) {
		t.Fatalf("ControllerVoterReadiness() = %#v, want Controller Raft proof fields", got)
	}
	if !got.Reachable || !got.TransportReady || !got.ControlReady || !got.RuntimeReady || got.Unknown || got.LastError != "" {
		t.Fatalf("ControllerVoterReadiness() = %#v, want all readiness gates true without error", got)
	}
}

func TestAppControllerVoterReadinessAllowsMirrorBeforePrepare(t *testing.T) {
	cluster := &fakeReadinessCluster{
		fakeManagerCluster: fakeManagerCluster{
			nodeID: 4,
			snapshot: control.Snapshot{
				ClusterID: "cluster-a",
				Revision:  22,
				Nodes: []control.Node{{
					NodeID:    4,
					Addr:      "10.0.0.4:11110",
					Roles:     []control.Role{control.RoleData},
					JoinState: control.NodeJoinStateActive,
				}},
			},
			controllerRaftStatusErr: cv2.ErrNotStarted,
		},
		snapshot: clusterpkg.Snapshot{
			RoutesReady:   true,
			SlotsReady:    true,
			ChannelsReady: true,
			HashSlotCount: 1,
		},
		routes: map[uint16]clusterpkg.Route{
			0: {HashSlot: 0, SlotID: 1, Leader: 4},
		},
	}
	app, err := newTestApp(t, Config{
		NodeID: 4,
		Cluster: clusterpkg.Config{
			NodeID:  4,
			Control: clusterpkg.ControlConfig{ClusterID: "cluster-a"},
		},
	}, WithCluster(cluster), WithGateway(nil))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	app.lifecycleMu.Lock()
	app.clusterStarted = true
	app.gatewayStarted = true
	app.lifecycleMu.Unlock()

	got, err := app.ControllerVoterReadiness(context.Background(), accessnode.ControllerVoterReadinessRequest{NodeID: 4, ClusterID: "cluster-a"})
	if err != nil {
		t.Fatalf("ControllerVoterReadiness() error = %v", err)
	}
	if got.Unknown || got.LastError != "" || !got.CanPrepare {
		t.Fatalf("ControllerVoterReadiness() = %#v, want mirror node ready for prepare without pre-existing Raft status", got)
	}
	if !got.Reachable || !got.TransportReady || !got.ControlReady || !got.RuntimeReady || got.MirrorRevision != 22 {
		t.Fatalf("ControllerVoterReadiness() = %#v, want node readiness fields preserved", got)
	}
	if got.IsVoter || got.ConfigIndex != 0 || len(got.Voters) != 0 {
		t.Fatalf("ControllerVoterReadiness() = %#v, want no Raft proof before prepare", got)
	}
}

func TestAppPrepareControllerVoterReturnsControllerRaftProof(t *testing.T) {
	cluster := &fakeControllerVoterPreparationCluster{
		fakeManagerCluster: fakeManagerCluster{
			nodeID: 4,
			controllerRaftStatus: clusterpkg.ControllerRaftStatus{
				AppliedIndex: 77,
				Voters:       []uint64{1, 2, 4},
			},
		},
		prepareResult: cv2.PrepareControllerVoterResult{
			Prepared:      true,
			StateRevision: 22,
		},
	}
	app, err := newTestApp(t, Config{
		NodeID: 4,
		Cluster: clusterpkg.Config{
			NodeID:  4,
			Control: clusterpkg.ControlConfig{ClusterID: "cluster-a"},
		},
	}, WithCluster(cluster), WithGateway(nil))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	got, err := app.PrepareControllerVoter(context.Background(), accessnode.PrepareControllerVoterRequest{
		NodeID:           4,
		ClusterID:        "cluster-a",
		ExpectedRevision: 22,
		NextVoters: []accessnode.ControllerVoter{
			{NodeID: 1, Addr: "10.0.0.1:11110"},
			{NodeID: 2, Addr: "10.0.0.2:11110"},
			{NodeID: 4, Addr: "10.0.0.4:11110"},
		},
	})
	if err != nil {
		t.Fatalf("PrepareControllerVoter() error = %v", err)
	}

	if got.NodeID != 4 || !got.Prepared || got.StateRevision != 22 ||
		got.ObservedConfigIndex != 77 || !sameSeedJoinUint64s(got.ObservedVoters, []uint64{1, 2, 4}) {
		t.Fatalf("PrepareControllerVoter() = %#v, want status proof fields", got)
	}
	if cluster.prepareRequest.NodeID != 4 || cluster.prepareRequest.ClusterID != "cluster-a" ||
		cluster.prepareRequest.ExpectedRevision != 22 ||
		!sameSeedJoinControllerVoters(cluster.prepareRequest.NextVoters, []cv2.Voter{
			{NodeID: 1, Addr: "10.0.0.1:11110"},
			{NodeID: 2, Addr: "10.0.0.2:11110"},
			{NodeID: 4, Addr: "10.0.0.4:11110"},
		}) {
		t.Fatalf("prepare request = %#v, want converted ControllerV2 voters", cluster.prepareRequest)
	}
	got.ObservedVoters[0] = 99
	if cluster.controllerRaftStatus.Voters[0] != 1 {
		t.Fatalf("ObservedVoters was not cloned from Controller Raft status")
	}
}

func TestAppPrepareControllerVoterAllowsPendingControllerRaftProof(t *testing.T) {
	cluster := &fakeControllerVoterPreparationCluster{
		fakeManagerCluster: fakeManagerCluster{
			nodeID: 4,
			controllerRaftStatuses: []clusterpkg.ControllerRaftStatus{
				{NodeID: 4, Role: "follower", LeaderID: 1},
			},
		},
		prepareResult: cv2.PrepareControllerVoterResult{
			Prepared:      true,
			StateRevision: 22,
		},
	}
	app, err := newTestApp(t, Config{}, WithCluster(cluster), WithGateway(nil))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	got, err := app.PrepareControllerVoter(context.Background(), accessnode.PrepareControllerVoterRequest{
		NodeID:           4,
		ClusterID:        "cluster-a",
		ExpectedRevision: 22,
		NextVoters: []accessnode.ControllerVoter{
			{NodeID: 1, Addr: "10.0.0.1:11110"},
			{NodeID: 2, Addr: "10.0.0.2:11110"},
			{NodeID: 4, Addr: "10.0.0.4:11110"},
		},
	})
	if err != nil {
		t.Fatalf("PrepareControllerVoter() error = %v", err)
	}
	if !got.Prepared || got.StateRevision != 22 || got.ObservedConfigIndex != 0 || len(got.ObservedVoters) != 0 {
		t.Fatalf("PrepareControllerVoter() = %#v, want prepared response without waiting for final Raft proof", got)
	}
	if cluster.controllerRaftStatusCalls != 1 {
		t.Fatalf("LocalControllerRaftStatus calls = %d, want 1", cluster.controllerRaftStatusCalls)
	}
}

func TestSeedJoinNodeReadinessUsesGatewayRuntimeBeforeSlotRoutes(t *testing.T) {
	cluster := &fakeReadinessCluster{
		fakeManagerCluster: fakeManagerCluster{
			nodeID: 4,
			snapshot: control.Snapshot{
				ClusterID: "cluster-a",
				Revision:  22,
				Nodes: []control.Node{{
					NodeID:    4,
					Addr:      "10.0.0.4:11110",
					Roles:     []control.Role{control.RoleData},
					JoinState: control.NodeJoinStateJoining,
				}},
			},
		},
		snapshot: clusterpkg.Snapshot{
			RoutesReady:   true,
			SlotsReady:    true,
			ChannelsReady: true,
			HashSlotCount: 1,
		},
	}
	app, err := newTestApp(t, Config{
		NodeID: 4,
		Cluster: clusterpkg.Config{
			NodeID:  4,
			Control: clusterpkg.ControlConfig{ClusterID: "cluster-a"},
			Join: clusterpkg.JoinConfig{
				Seeds:         []string{"10.0.0.1:11110"},
				AdvertiseAddr: "10.0.0.4:11110",
				Token:         "join-secret",
			},
		},
	}, WithCluster(cluster), WithGateway(nil))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	app.lifecycleMu.Lock()
	app.clusterStarted = true
	app.gatewayStarted = true
	app.lifecycleMu.Unlock()

	got, err := app.NodeReadiness(context.Background(), accessnode.NodeReadinessRequest{NodeID: 4, ClusterID: "cluster-a"})
	if err != nil {
		t.Fatalf("NodeReadiness() error = %v", err)
	}
	if !got.Ready || !got.RuntimeReady {
		t.Fatalf("NodeReadiness() = %#v, want seed-join runtime ready without slot leaders", got)
	}
}

func TestSeedJoinActiveNodeReadinessRequiresSlotRoutes(t *testing.T) {
	cluster := &fakeReadinessCluster{
		fakeManagerCluster: fakeManagerCluster{
			nodeID: 4,
			snapshot: control.Snapshot{
				ClusterID: "cluster-a",
				Revision:  22,
				Nodes: []control.Node{{
					NodeID:    4,
					Addr:      "10.0.0.4:11110",
					Roles:     []control.Role{control.RoleData},
					JoinState: control.NodeJoinStateActive,
				}},
			},
		},
		snapshot: clusterpkg.Snapshot{
			RoutesReady:   true,
			SlotsReady:    true,
			ChannelsReady: true,
			HashSlotCount: 1,
		},
	}
	app, err := newTestApp(t, Config{
		NodeID: 4,
		Cluster: clusterpkg.Config{
			NodeID:  4,
			Control: clusterpkg.ControlConfig{ClusterID: "cluster-a"},
			Join: clusterpkg.JoinConfig{
				Seeds:         []string{"10.0.0.1:11110"},
				AdvertiseAddr: "10.0.0.4:11110",
				Token:         "join-secret",
			},
		},
	}, WithCluster(cluster), WithGateway(nil))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	app.lifecycleMu.Lock()
	app.clusterStarted = true
	app.gatewayStarted = true
	app.lifecycleMu.Unlock()

	got, err := app.NodeReadiness(context.Background(), accessnode.NodeReadinessRequest{NodeID: 4, ClusterID: "cluster-a"})
	if err != nil {
		t.Fatalf("NodeReadiness() error = %v", err)
	}
	if got.Ready || got.RuntimeReady {
		t.Fatalf("NodeReadiness() = %#v, want active seed-join to require slot routes", got)
	}
}

func TestSeedJoinReadyzWaitsForGatewayRuntime(t *testing.T) {
	app, err := newTestApp(t, Config{
		NodeID: 4,
		Cluster: clusterpkg.Config{
			NodeID:  4,
			Control: clusterpkg.ControlConfig{ClusterID: "cluster-a"},
			Join: clusterpkg.JoinConfig{
				Seeds:         []string{"10.0.0.1:11110"},
				AdvertiseAddr: "10.0.0.4:11110",
				Token:         "join-secret",
			},
		},
	}, WithCluster(&fakeReadinessCluster{
		fakeManagerCluster: fakeManagerCluster{
			nodeID: 4,
			snapshot: control.Snapshot{
				Nodes: []control.Node{{
					NodeID:    4,
					Addr:      "10.0.0.4:11110",
					JoinState: control.NodeJoinStateJoining,
				}},
			},
		},
	}), WithGateway(nil))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	app.lifecycleMu.Lock()
	app.clusterStarted = true
	app.gatewayStarted = false
	app.lifecycleMu.Unlock()

	ready, body := app.readyzReport(context.Background())
	if ready {
		t.Fatalf("readyz ready=true body=%#v, want not ready before gateway starts", body)
	}

	app.lifecycleMu.Lock()
	app.gatewayStarted = true
	app.lifecycleMu.Unlock()
	ready, body = app.readyzReport(context.Background())
	if !ready {
		t.Fatalf("readyz ready=false body=%#v, want ready after gateway starts", body)
	}
}

func TestSeedJoinActiveReadyzRequiresWriteRoutes(t *testing.T) {
	app, err := newTestApp(t, Config{
		NodeID: 4,
		Cluster: clusterpkg.Config{
			NodeID:  4,
			Control: clusterpkg.ControlConfig{ClusterID: "cluster-a"},
			Join: clusterpkg.JoinConfig{
				Seeds:         []string{"10.0.0.1:11110"},
				AdvertiseAddr: "10.0.0.4:11110",
				Token:         "join-secret",
			},
		},
	}, WithCluster(&fakeReadinessCluster{
		fakeManagerCluster: fakeManagerCluster{
			nodeID: 4,
			snapshot: control.Snapshot{
				Nodes: []control.Node{{
					NodeID:    4,
					Addr:      "10.0.0.4:11110",
					JoinState: control.NodeJoinStateActive,
				}},
			},
		},
	}), WithGateway(nil))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	app.lifecycleMu.Lock()
	app.clusterStarted = true
	app.gatewayStarted = true
	app.lifecycleMu.Unlock()

	ready, body := app.readyzReport(context.Background())
	if ready {
		t.Fatalf("readyz ready=true body=%#v, want active seed join to require write routes", body)
	}
}

func TestSeedJoinLoopStopsAfterMirrorSeesJoiningNode(t *testing.T) {
	snapshots := &fakeSeedJoinSnapshotReader{}
	client := &fakeSeedJoinClient{
		joinResponse: managementusecase.JoinNodeResponse{
			Created:   true,
			NodeID:    4,
			Addr:      "10.0.0.4:11110",
			JoinState: string(control.NodeJoinStateJoining),
			Revision:  9,
		},
		afterJoin: func() {
			snapshots.set(control.Snapshot{
				Revision: 9,
				Nodes: []control.Node{{
					NodeID:    4,
					Addr:      "10.0.0.4:11110",
					JoinState: control.NodeJoinStateJoining,
				}},
			})
		},
	}
	loop := newSeedJoinLoop(seedJoinLoopConfig{
		NodeID:         4,
		AdvertiseAddr:  "10.0.0.4:11110",
		ClusterID:      "cluster-a",
		JoinToken:      "join-secret",
		Seeds:          []uint64{2, 1},
		CapacityWeight: 7,
		Interval:       5 * time.Millisecond,
	}, client, snapshots, wklog.NewNop())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := loop.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() {
		if err := loop.Stop(context.Background()); err != nil {
			t.Fatalf("Stop() error = %v", err)
		}
	}()

	first := client.waitCall(t)
	if first.seedNodeID != 1 {
		t.Fatalf("first seed node = %d, want stable sorted seed 1", first.seedNodeID)
	}
	wantReq := accessnode.NodeJoinRequest{
		NodeID:         4,
		AdvertiseAddr:  "10.0.0.4:11110",
		ClusterID:      "cluster-a",
		JoinToken:      "join-secret",
		CapacityWeight: 7,
	}
	if first.req != wantReq {
		t.Fatalf("join request = %#v, want %#v", first.req, wantReq)
	}
	select {
	case call := <-client.calls:
		t.Fatalf("unexpected extra join call after mirror saw joining node: %#v", call)
	case <-time.After(40 * time.Millisecond):
	}
}

func TestSeedJoinLoopIgnoresSameNodeIDWithDifferentAddr(t *testing.T) {
	snapshots := &fakeSeedJoinSnapshotReader{}
	snapshots.set(control.Snapshot{
		Revision: 8,
		Nodes: []control.Node{{
			NodeID:    4,
			Addr:      "10.0.0.99:11110",
			JoinState: control.NodeJoinStateJoining,
		}},
	})
	loop := newSeedJoinLoop(seedJoinLoopConfig{
		NodeID:        4,
		AdvertiseAddr: "10.0.0.4:11110",
		ClusterID:     "cluster-a",
		JoinToken:     "join-secret",
		Seeds:         []uint64{1},
		Interval:      5 * time.Millisecond,
	}, &fakeSeedJoinClient{}, snapshots, wklog.NewNop())

	if loop.joinObserved(context.Background()) {
		t.Fatal("joinObserved = true for same node id with different addr, want false")
	}
}

type fakeSeedJoinSnapshotReader struct {
	mu       sync.Mutex
	snapshot control.Snapshot
}

func (f *fakeSeedJoinSnapshotReader) LocalControlSnapshot(context.Context) (control.Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.snapshot.Clone(), nil
}

func (f *fakeSeedJoinSnapshotReader) set(snapshot control.Snapshot) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.snapshot = snapshot.Clone()
}

type seedJoinCall struct {
	seedNodeID uint64
	req        accessnode.NodeJoinRequest
}

type fakeSeedJoinClient struct {
	calls        chan seedJoinCall
	joinResponse managementusecase.JoinNodeResponse
	afterJoin    func()
}

func (f *fakeSeedJoinClient) JoinNode(_ context.Context, seedNodeID uint64, req accessnode.NodeJoinRequest) (managementusecase.JoinNodeResponse, error) {
	if f.calls == nil {
		f.calls = make(chan seedJoinCall, 16)
	}
	f.calls <- seedJoinCall{seedNodeID: seedNodeID, req: req}
	if f.afterJoin != nil {
		f.afterJoin()
	}
	return f.joinResponse, nil
}

func (f *fakeSeedJoinClient) waitCall(t *testing.T) seedJoinCall {
	t.Helper()
	if f.calls == nil {
		f.calls = make(chan seedJoinCall, 16)
	}
	select {
	case call := <-f.calls:
		return call
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for seed join call")
		return seedJoinCall{}
	}
}

type fakeReadinessCluster struct {
	fakeManagerCluster
	snapshot clusterpkg.Snapshot
	routes   map[uint16]clusterpkg.Route
}

func (f *fakeReadinessCluster) Snapshot() clusterpkg.Snapshot {
	return f.snapshot
}

func (f *fakeReadinessCluster) RouteHashSlot(hashSlot uint16) (clusterpkg.Route, error) {
	route, ok := f.routes[hashSlot]
	if !ok {
		return clusterpkg.Route{}, clusterpkg.ErrRouteNotReady
	}
	return route, nil
}

type fakeControllerVoterPreparationCluster struct {
	fakeManagerCluster
	prepareRequest cv2.PrepareControllerVoterRequest
	prepareResult  cv2.PrepareControllerVoterResult
	prepareErr     error
}

func (f *fakeControllerVoterPreparationCluster) PrepareControllerVoter(_ context.Context, req cv2.PrepareControllerVoterRequest) (cv2.PrepareControllerVoterResult, error) {
	f.prepareRequest = req
	return f.prepareResult, f.prepareErr
}

func sameSeedJoinControllerVoters(left, right []cv2.Voter) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func sameSeedJoinUint64s(left, right []uint64) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
