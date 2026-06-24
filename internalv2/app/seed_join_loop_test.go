package app

import (
	"context"
	"sync"
	"testing"
	"time"

	accessnode "github.com/WuKongIM/WuKongIM/internalv2/access/node"
	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

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
		Cluster: clusterv2.Config{
			NodeID: 4,
			Control: clusterv2.ControlConfig{
				ClusterID: "cluster-a",
			},
			Join: clusterv2.JoinConfig{
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
