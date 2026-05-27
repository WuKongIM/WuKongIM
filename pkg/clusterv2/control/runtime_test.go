package control

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	clusternet "github.com/WuKongIM/WuKongIM/pkg/clusterv2/net"
	cv2 "github.com/WuKongIM/WuKongIM/pkg/controllerv2"
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

func TestRuntimeProbeProposeSingleVoter(t *testing.T) {
	runtime, err := NewRuntime(RuntimeConfig{
		NodeID:           1,
		Addr:             "127.0.0.1:10001",
		StateDir:         t.TempDir(),
		ClusterID:        "cluster-probe-single",
		Role:             RuntimeRoleVoter,
		Voters:           []RuntimeVoter{{NodeID: 1, Addr: "127.0.0.1:10001"}},
		AllowBootstrap:   true,
		InitialSlotCount: 1,
		HashSlotCount:    4,
		ReplicaCount:     1,
		TickInterval:     5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := runtime.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Stop(context.Background()) })

	before, err := runtime.LocalSnapshot(context.Background())
	if err != nil {
		t.Fatalf("LocalSnapshot(before) error = %v", err)
	}
	if err := runtime.ProbePropose(ctx); err != nil {
		t.Fatalf("ProbePropose() error = %v", err)
	}
	after, err := runtime.LocalSnapshot(context.Background())
	if err != nil {
		t.Fatalf("LocalSnapshot(after) error = %v", err)
	}
	if before.Revision != after.Revision || len(before.Slots) != len(after.Slots) {
		t.Fatalf("ProbePropose mutated local snapshot: before=%#v after=%#v", before, after)
	}
}

func TestRuntimeProbeProposeWithoutRaftReturnsNotStarted(t *testing.T) {
	var runtime Runtime
	if err := runtime.ProbePropose(context.Background()); !errors.Is(err, cv2.ErrNotStarted) {
		t.Fatalf("ProbePropose() error = %v, want ErrNotStarted", err)
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
	syncServer := cv2.NewStateSyncServer(cv2.StateSyncServerConfig{
		NodeID:    1,
		ClusterID: leaderState.ClusterID,
		LeaderID:  func() uint64 { return 1 },
		Ready:     func() bool { return true },
		Snapshot:  func(context.Context) (cv2.ClusterState, error) { return leaderState, nil },
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

func TestRuntimeThreeVotersConverge(t *testing.T) {
	network := clusternet.NewLocalNetwork()
	voters := []RuntimeVoter{{NodeID: 1, Addr: "n1"}, {NodeID: 2, Addr: "n2"}, {NodeID: 3, Addr: "n3"}}
	runtimes := make([]*Runtime, 0, len(voters))
	for _, voter := range voters {
		rt, err := NewRuntime(RuntimeConfig{
			NodeID:           voter.NodeID,
			Addr:             voter.Addr,
			StateDir:         t.TempDir(),
			ClusterID:        "cluster-three",
			Role:             RuntimeRoleVoter,
			Voters:           voters,
			AllowBootstrap:   true,
			InitialSlotCount: 1,
			HashSlotCount:    4,
			ReplicaCount:     3,
			TickInterval:     10 * time.Millisecond,
			RaftTransport:    NewRaftTransport(network),
		})
		if err != nil {
			t.Fatalf("NewRuntime(%d) error = %v", voter.NodeID, err)
		}
		network.Register(voter.NodeID, clusternet.RPCControlRaft, NewRaftHandler(rt))
		runtimes = append(runtimes, rt)
	}
	startCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	startErrs := make(chan error, len(runtimes))
	for _, rt := range runtimes {
		rt := rt
		go func() { startErrs <- rt.Start(startCtx) }()
		t.Cleanup(func() { _ = rt.Stop(context.Background()) })
	}
	for range runtimes {
		if err := <-startErrs; err != nil {
			t.Fatalf("Start() error = %v", err)
		}
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ready := true
		var revision uint64
		for _, rt := range runtimes {
			snap, err := rt.LocalSnapshot(context.Background())
			if err != nil || snap.Revision == 0 || len(snap.Slots) != 1 || snap.ControllerID == 0 {
				ready = false
				break
			}
			if revision == 0 {
				revision = snap.Revision
			}
			if snap.Revision != revision {
				ready = false
				break
			}
		}
		if ready {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, rt := range runtimes {
		snap, _ := rt.LocalSnapshot(context.Background())
		t.Logf("runtime %d snapshot: %#v", rt.cfg.NodeID, snap)
	}
	t.Fatal("runtimes did not converge")
}
