package control

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	clusternet "github.com/WuKongIM/WuKongIM/pkg/clusterv2/net"
	cv2state "github.com/WuKongIM/WuKongIM/pkg/controllerv2/state"
	cv2sync "github.com/WuKongIM/WuKongIM/pkg/controllerv2/sync"
)

func TestRuntimeSingleVoterBootstrapsSnapshot(t *testing.T) {
	cfg := RuntimeConfig{
		NodeID:           1,
		Addr:             "127.0.0.1:10001",
		StateDir:         t.TempDir(),
		ClusterID:        "cluster-single",
		Role:             RuntimeRoleVoter,
		Voters:           []RuntimeVoter{{NodeID: 1, Addr: "127.0.0.1:10001"}},
		AllowBootstrap:   true,
		InitialSlotCount: 1,
		HashSlotCount:    4,
		ReplicaCount:     1,
		TickInterval:     5 * time.Millisecond,
	}
	runtime, err := NewRuntime(cfg)
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := runtime.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Stop(context.Background()) })

	snap, err := runtime.LocalSnapshot(context.Background())
	if err != nil {
		t.Fatalf("LocalSnapshot() error = %v", err)
	}
	if snap.Revision == 0 || snap.ControllerID != 1 || len(snap.Slots) != 1 || snap.HashSlots.Count != 4 {
		t.Fatalf("snapshot = %#v, want bootstrapped control state", snap)
	}
	if runtime.LeaderID() != 1 {
		t.Fatalf("LeaderID() = %d, want 1", runtime.LeaderID())
	}
	if _, err := os.Stat(filepath.Join(cfg.StateDir, "cluster-state.json")); err != nil {
		t.Fatalf("cluster-state.json missing: %v", err)
	}
}

func TestRuntimeRestartReusesExistingState(t *testing.T) {
	dir := t.TempDir()
	cfg := RuntimeConfig{
		NodeID:           1,
		Addr:             "n1",
		StateDir:         dir,
		ClusterID:        "cluster-restart",
		Role:             RuntimeRoleVoter,
		Voters:           []RuntimeVoter{{NodeID: 1, Addr: "n1"}},
		AllowBootstrap:   true,
		InitialSlotCount: 1,
		HashSlotCount:    4,
		ReplicaCount:     1,
		TickInterval:     5 * time.Millisecond,
	}
	first, err := NewRuntime(cfg)
	if err != nil {
		t.Fatalf("NewRuntime(first) error = %v", err)
	}
	if err := first.Start(context.Background()); err != nil {
		t.Fatalf("Start(first) error = %v", err)
	}
	firstSnap, err := first.LocalSnapshot(context.Background())
	if err != nil {
		t.Fatalf("LocalSnapshot(first) error = %v", err)
	}
	if err := first.Stop(context.Background()); err != nil {
		t.Fatalf("Stop(first) error = %v", err)
	}

	second, err := NewRuntime(cfg)
	if err != nil {
		t.Fatalf("NewRuntime(second) error = %v", err)
	}
	if err := second.Start(context.Background()); err != nil {
		t.Fatalf("Start(second) error = %v", err)
	}
	t.Cleanup(func() { _ = second.Stop(context.Background()) })
	secondSnap, err := second.LocalSnapshot(context.Background())
	if err != nil {
		t.Fatalf("LocalSnapshot(second) error = %v", err)
	}

	if secondSnap.Revision != firstSnap.Revision || len(secondSnap.Slots) != len(firstSnap.Slots) {
		t.Fatalf("restart snapshot = %#v, first %#v", secondSnap, firstSnap)
	}
}

func TestRuntimeMirrorSyncsLeaderState(t *testing.T) {
	network := clusternet.NewLocalNetwork()
	leaderState := controllerV2State()
	syncServer := cv2sync.NewServer(cv2sync.ServerConfig{
		NodeID:    1,
		ClusterID: leaderState.ClusterID,
		LeaderID:  func() uint64 { return 1 },
		Ready:     func() bool { return true },
		Snapshot:  func(context.Context) (cv2state.ClusterState, error) { return leaderState, nil },
	})
	network.Register(1, clusternet.RPCControlStateSync, NewStateSyncHandler(syncServer))

	runtime, err := NewRuntime(RuntimeConfig{
		NodeID:    4,
		Addr:      "n4",
		StateDir:  t.TempDir(),
		ClusterID: leaderState.ClusterID,
		Role:      RuntimeRoleMirror,
		Voters:    []RuntimeVoter{{NodeID: 1, Addr: "n1"}},
		SyncPeers: NewStaticPeerPicker(network, []RuntimeVoter{{NodeID: 1, Addr: "n1"}}),
	})
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Stop(context.Background()) })

	snap, err := runtime.LocalSnapshot(context.Background())
	if err != nil {
		t.Fatalf("LocalSnapshot() error = %v", err)
	}
	if snap.Revision != leaderState.Revision || len(snap.Nodes) != len(leaderState.Nodes) {
		t.Fatalf("mirror snapshot = %#v, want revision %d", snap, leaderState.Revision)
	}
}
