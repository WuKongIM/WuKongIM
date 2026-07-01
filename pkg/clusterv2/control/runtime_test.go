package control

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

func TestRuntimeReportNodePersistsHealth(t *testing.T) {
	runtime, err := NewRuntime(RuntimeConfig{
		NodeID:           1,
		Addr:             "127.0.0.1:10001",
		StateDir:         t.TempDir(),
		ClusterID:        "cluster-report-node-health",
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

	if err := runtime.ReportNode(context.Background(), NodeReport{
		NodeID:                  1,
		Status:                  NodeAlive,
		RuntimeReady:            true,
		ObservedControlRevision: 1,
		ObservedSlotRevision:    2,
		ReportSeq:               7,
	}); err != nil {
		t.Fatalf("ReportNode() error = %v", err)
	}
	snap, err := runtime.LocalSnapshot(context.Background())
	if err != nil {
		t.Fatalf("LocalSnapshot() error = %v", err)
	}
	if len(snap.Nodes) != 1 || snap.Nodes[0].Health.Freshness != NodeHealthFresh || snap.Nodes[0].Health.ReportSeq != 7 {
		t.Fatalf("snapshot nodes = %#v, want fresh report seq 7", snap.Nodes)
	}
}

func TestRuntimePassesTaskTransitionObserver(t *testing.T) {
	observed := make(chan []cv2.TaskTransition, 1)
	observer := cv2.TaskTransitionObserverFunc(func(items []cv2.TaskTransition) {
		if len(items) == 0 {
			return
		}
		select {
		case observed <- items:
		default:
		}
	})
	runtime, err := NewRuntime(RuntimeConfig{
		NodeID:                 1,
		Addr:                   "127.0.0.1:10001",
		StateDir:               t.TempDir(),
		ClusterID:              "cluster-task-transition-observer",
		Role:                   RuntimeRoleVoter,
		Voters:                 []RuntimeVoter{{NodeID: 1, Addr: "127.0.0.1:10001"}},
		AllowBootstrap:         true,
		InitialSlotCount:       1,
		HashSlotCount:          4,
		ReplicaCount:           1,
		TickInterval:           5 * time.Millisecond,
		TaskTransitionObserver: observer,
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

	select {
	case items := <-observed:
		if len(items) == 0 || !items[0].AfterValid || items[0].After.Kind != cv2.TaskKindBootstrap {
			t.Fatalf("observed task transitions = %#v, want bootstrap creation", items)
		}
	case <-ctx.Done():
		t.Fatalf("observer did not receive task transition: %v", ctx.Err())
	}
}

func TestRuntimeLocalSnapshotRefreshesFromBackendWhenWatchMissesLifecycleWrite(t *testing.T) {
	runtime, err := NewRuntime(RuntimeConfig{
		NodeID:           1,
		Addr:             "127.0.0.1:10001",
		StateDir:         t.TempDir(),
		ClusterID:        "cluster-local-snapshot-refresh",
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

	if _, err := runtime.JoinNode(ctx, JoinNodeRequest{NodeID: 4, Addr: "n4", Roles: []Role{RoleData}, CapacityWeight: 1}); err != nil {
		t.Fatalf("JoinNode() error = %v", err)
	}
	if _, err := runtime.ActivateNode(ctx, ActivateNodeRequest{NodeID: 4}); err != nil {
		t.Fatalf("ActivateNode() error = %v", err)
	}
	activeState, err := runtime.backend.LocalState(ctx)
	if err != nil {
		t.Fatalf("backend LocalState(active) error = %v", err)
	}
	activeSnapshot, err := SnapshotFromControllerV2(activeState)
	if err != nil {
		t.Fatalf("SnapshotFromControllerV2(active) error = %v", err)
	}
	if got := snapshotJoinState(activeSnapshot, 4); got != NodeJoinStateActive {
		t.Fatalf("backend node 4 join_state = %q, want active", got)
	}
	runtime.mu.Lock()
	runtime.snapshot = activeSnapshot.Clone()
	runtime.mu.Unlock()
	if runtime.watchCancel != nil {
		runtime.watchCancel()
		runtime.watchWG.Wait()
		runtime.watchCancel = nil
	}

	if _, err := runtime.MarkNodeLeaving(ctx, MarkNodeLeavingRequest{NodeID: 4}); err != nil {
		t.Fatalf("MarkNodeLeaving() error = %v", err)
	}
	got, err := runtime.LocalSnapshot(context.Background())
	if err != nil {
		t.Fatalf("LocalSnapshot() error = %v", err)
	}
	if state := snapshotJoinState(got, 4); state != NodeJoinStateLeaving {
		t.Fatalf("LocalSnapshot node 4 join_state = %q, want leaving after backend write", state)
	}
}

func TestRuntimeProbeProposeWithoutRaftReturnsNotStarted(t *testing.T) {
	var runtime Runtime
	if err := runtime.ProbePropose(context.Background()); !errors.Is(err, cv2.ErrNotStarted) {
		t.Fatalf("ProbePropose() error = %v, want ErrNotStarted", err)
	}
}

func TestRuntimeTaskWritersWithoutBackendReturnNotStarted(t *testing.T) {
	var runtime Runtime
	if err := runtime.ReportTaskProgress(context.Background(), TaskProgress{TaskID: "bootstrap-1"}); !errors.Is(err, cv2.ErrNotStarted) {
		t.Fatalf("ReportTaskProgress() error = %v, want ErrNotStarted", err)
	}
	if err := runtime.CompleteTask(context.Background(), TaskResult{TaskID: "bootstrap-1"}); !errors.Is(err, cv2.ErrNotStarted) {
		t.Fatalf("CompleteTask() error = %v, want ErrNotStarted", err)
	}
	if err := runtime.FailTask(context.Background(), TaskResult{TaskID: "bootstrap-1"}); !errors.Is(err, cv2.ErrNotStarted) {
		t.Fatalf("FailTask() error = %v, want ErrNotStarted", err)
	}
	if _, err := runtime.RequestSlotLeaderTransfer(context.Background(), SlotLeaderTransferRequest{SlotID: 1}); !errors.Is(err, cv2.ErrNotStarted) {
		t.Fatalf("RequestSlotLeaderTransfer() error = %v, want ErrNotStarted", err)
	}
}

func TestRuntimeLifecycleWritesNotStartedWithoutForwardPreserveNotStarted(t *testing.T) {
	runtime, err := NewRuntime(RuntimeConfig{
		NodeID:           1,
		Addr:             "n1",
		StateDir:         t.TempDir(),
		ClusterID:        "cluster-lifecycle-not-started",
		Role:             RuntimeRoleVoter,
		Voters:           []RuntimeVoter{{NodeID: 1, Addr: "n1"}},
		AllowBootstrap:   true,
		InitialSlotCount: 1,
		HashSlotCount:    4,
		ReplicaCount:     1,
	})
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	if _, err := runtime.JoinNode(context.Background(), JoinNodeRequest{NodeID: 2, Addr: "n2"}); !errors.Is(err, cv2.ErrNotStarted) {
		t.Fatalf("JoinNode() error = %v, want ErrNotStarted", err)
	}
	if _, err := runtime.ActivateNode(context.Background(), ActivateNodeRequest{NodeID: 2}); !errors.Is(err, cv2.ErrNotStarted) {
		t.Fatalf("ActivateNode() error = %v, want ErrNotStarted", err)
	}
	if _, err := runtime.MarkNodeLeaving(context.Background(), MarkNodeLeavingRequest{NodeID: 2}); !errors.Is(err, cv2.ErrNotStarted) {
		t.Fatalf("MarkNodeLeaving() error = %v, want ErrNotStarted", err)
	}
	if _, err := runtime.MarkNodeRemoved(context.Background(), MarkNodeRemovedRequest{NodeID: 2}); !errors.Is(err, cv2.ErrNotStarted) {
		t.Fatalf("MarkNodeRemoved() error = %v, want ErrNotStarted", err)
	}
}

func TestRuntimeRequestSlotLeaderTransferReturnsTaskAfterForward(t *testing.T) {
	network := clusternet.NewLocalNetwork()
	taskClient := NewTaskClient(network)
	voters := []RuntimeVoter{{NodeID: 1, Addr: "n1"}, {NodeID: 2, Addr: "n2"}, {NodeID: 3, Addr: "n3"}}
	runtimes := make([]*Runtime, 0, len(voters))
	for _, voter := range voters {
		rt, err := NewRuntime(RuntimeConfig{
			NodeID:           voter.NodeID,
			Addr:             voter.Addr,
			StateDir:         t.TempDir(),
			ClusterID:        "cluster-forward-transfer",
			Role:             RuntimeRoleVoter,
			Voters:           voters,
			AllowBootstrap:   true,
			InitialSlotCount: 1,
			HashSlotCount:    4,
			ReplicaCount:     3,
			TickInterval:     10 * time.Millisecond,
			RaftTransport:    NewRaftTransport(network),
			TaskClient:       taskClient,
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

	var leaderID uint64
	var follower *Runtime
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, rt := range runtimes {
			leaderID = rt.LeaderID()
			if leaderID == 0 {
				continue
			}
			if rt.cfg.NodeID != leaderID {
				follower = rt
				break
			}
		}
		if follower != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if follower == nil {
		t.Fatal("timeout waiting for follower runtime")
	}

	applier := &recordingTaskApplier{}
	network.Register(leaderID, clusternet.RPCControlTaskResult, NewTaskHandler(applier))
	req := SlotLeaderTransferRequest{
		SlotID:        1,
		SourceNode:    1,
		TargetNode:    2,
		TargetPeers:   []uint64{1, 2, 3},
		ConfigEpoch:   7,
		StateRevision: 9,
	}
	result, err := follower.RequestSlotLeaderTransfer(context.Background(), req)
	if err != nil {
		t.Fatalf("RequestSlotLeaderTransfer() error = %v", err)
	}
	if len(applier.leaderTransfers) != 1 || applier.leaderTransfers[0].TargetNode != 2 {
		t.Fatalf("leaderTransfers = %#v, want one forwarded transfer", applier.leaderTransfers)
	}
	if !result.Created || result.Task == nil || result.Task.TaskID != "slot-1-leader-transfer-7-r9" {
		t.Fatalf("RequestSlotLeaderTransfer() = %#v, want deterministic forwarded task", result)
	}
}

func TestRuntimeJoinNodeReturnsControlWriteAfterForward(t *testing.T) {
	network := clusternet.NewLocalNetwork()
	taskClient := NewTaskClient(network)
	controlWriteClient := NewControlWriteClient(network)
	voters := []RuntimeVoter{{NodeID: 1, Addr: "n1"}, {NodeID: 2, Addr: "n2"}, {NodeID: 3, Addr: "n3"}}
	runtimes := make([]*Runtime, 0, len(voters))
	for _, voter := range voters {
		rt, err := NewRuntime(RuntimeConfig{
			NodeID:             voter.NodeID,
			Addr:               voter.Addr,
			StateDir:           t.TempDir(),
			ClusterID:          "cluster-forward-join-node",
			Role:               RuntimeRoleVoter,
			Voters:             voters,
			AllowBootstrap:     true,
			InitialSlotCount:   1,
			HashSlotCount:      4,
			ReplicaCount:       3,
			TickInterval:       10 * time.Millisecond,
			RaftTransport:      NewRaftTransport(network),
			TaskClient:         taskClient,
			ControlWriteClient: controlWriteClient,
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

	var leaderID uint64
	var leader *Runtime
	var follower *Runtime
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, rt := range runtimes {
			leaderID = rt.LeaderID()
			if leaderID == 0 {
				continue
			}
			for _, candidate := range runtimes {
				if candidate.cfg.NodeID == leaderID {
					leader = candidate
				} else {
					follower = candidate
				}
			}
			if leader != nil {
				snap, err := leader.LocalSnapshot(context.Background())
				if err != nil || snap.Revision == 0 {
					leader = nil
					follower = nil
					continue
				}
			}
			if leader != nil && follower != nil {
				break
			}
		}
		if leader != nil && follower != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if follower == nil || leader == nil {
		t.Fatal("timeout waiting for leader and follower runtime")
	}

	network.Register(leaderID, clusternet.RPCControlWrite, NewControlWriteHandler(leader))
	req := JoinNodeRequest{
		NodeID:         4,
		Name:           "node-4",
		Addr:           "n4",
		Roles:          []Role{RoleData},
		CapacityWeight: 2,
	}
	result, err := follower.JoinNode(context.Background(), req)
	if err != nil {
		t.Fatalf("JoinNode() error = %v", err)
	}
	if !result.Created || result.Node.NodeID != 4 || result.Node.JoinState != NodeJoinStateJoining {
		t.Fatalf("JoinNode() = %#v, want forwarded joining node creation", result)
	}
}

func TestRuntimeActivateNodeReturnsControlWriteAfterForward(t *testing.T) {
	network := clusternet.NewLocalNetwork()
	controlWriteClient := NewControlWriteClient(network)
	voters := []RuntimeVoter{{NodeID: 1, Addr: "n1"}, {NodeID: 2, Addr: "n2"}, {NodeID: 3, Addr: "n3"}}
	runtimes := make([]*Runtime, 0, len(voters))
	for _, voter := range voters {
		rt, err := NewRuntime(RuntimeConfig{
			NodeID:             voter.NodeID,
			Addr:               voter.Addr,
			StateDir:           t.TempDir(),
			ClusterID:          "cluster-forward-activate-node",
			Role:               RuntimeRoleVoter,
			Voters:             voters,
			AllowBootstrap:     true,
			InitialSlotCount:   1,
			HashSlotCount:      4,
			ReplicaCount:       3,
			TickInterval:       10 * time.Millisecond,
			RaftTransport:      NewRaftTransport(network),
			ControlWriteClient: controlWriteClient,
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

	var leaderID uint64
	var leader *Runtime
	var follower *Runtime
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, rt := range runtimes {
			leaderID = rt.LeaderID()
			if leaderID == 0 {
				continue
			}
			for _, candidate := range runtimes {
				if candidate.cfg.NodeID == leaderID {
					leader = candidate
				} else {
					follower = candidate
				}
			}
			if leader != nil {
				snap, err := leader.LocalSnapshot(context.Background())
				if err != nil || snap.Revision == 0 {
					leader = nil
					follower = nil
					continue
				}
			}
			if leader != nil && follower != nil {
				break
			}
		}
		if leader != nil && follower != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if follower == nil || leader == nil {
		t.Fatal("timeout waiting for leader and follower runtime")
	}

	network.Register(leaderID, clusternet.RPCControlWrite, NewControlWriteHandler(leader))
	if _, err := leader.JoinNode(context.Background(), JoinNodeRequest{NodeID: 4, Addr: "n4", Roles: []Role{RoleData}, CapacityWeight: 1}); err != nil {
		t.Fatalf("leader JoinNode() error = %v", err)
	}
	observedJoining := false
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !observedJoining {
		snap, err := follower.LocalSnapshot(context.Background())
		if err == nil {
			for _, node := range snap.Nodes {
				if node.NodeID == 4 && node.JoinState == NodeJoinStateJoining {
					observedJoining = true
					break
				}
			}
		}
		if !observedJoining {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if !observedJoining {
		t.Fatal("timeout waiting for follower to observe joining node")
	}
	result, err := follower.ActivateNode(context.Background(), ActivateNodeRequest{NodeID: 4})
	if err != nil {
		t.Fatalf("ActivateNode() error = %v", err)
	}
	if !result.Changed || result.Node.NodeID != 4 || result.Node.JoinState != NodeJoinStateActive {
		t.Fatalf("ActivateNode() = %#v, want forwarded active node change", result)
	}
}

func TestRuntimeMarkNodeLeavingReturnsControlWriteAfterForward(t *testing.T) {
	network := clusternet.NewLocalNetwork()
	controlWriteClient := NewControlWriteClient(network)
	voters := []RuntimeVoter{{NodeID: 1, Addr: "n1"}, {NodeID: 2, Addr: "n2"}, {NodeID: 3, Addr: "n3"}}
	runtimes := make([]*Runtime, 0, len(voters))
	for _, voter := range voters {
		rt, err := NewRuntime(RuntimeConfig{
			NodeID:             voter.NodeID,
			Addr:               voter.Addr,
			StateDir:           t.TempDir(),
			ClusterID:          "cluster-forward-mark-node-leaving",
			Role:               RuntimeRoleVoter,
			Voters:             voters,
			AllowBootstrap:     true,
			InitialSlotCount:   1,
			HashSlotCount:      4,
			ReplicaCount:       3,
			TickInterval:       10 * time.Millisecond,
			RaftTransport:      NewRaftTransport(network),
			ControlWriteClient: controlWriteClient,
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

	var leaderID uint64
	var leader *Runtime
	var follower *Runtime
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, rt := range runtimes {
			leaderID = rt.LeaderID()
			if leaderID == 0 {
				continue
			}
			for _, candidate := range runtimes {
				if candidate.cfg.NodeID == leaderID {
					leader = candidate
				} else {
					follower = candidate
				}
			}
			if leader != nil {
				snap, err := leader.LocalSnapshot(context.Background())
				if err != nil || snap.Revision == 0 {
					leader = nil
					follower = nil
					continue
				}
			}
			if leader != nil && follower != nil {
				break
			}
		}
		if leader != nil && follower != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if follower == nil || leader == nil {
		t.Fatal("timeout waiting for leader and follower runtime")
	}

	network.Register(leaderID, clusternet.RPCControlWrite, NewControlWriteHandler(leader))
	if _, err := leader.JoinNode(context.Background(), JoinNodeRequest{NodeID: 4, Addr: "n4", Roles: []Role{RoleData}, CapacityWeight: 1}); err != nil {
		t.Fatalf("leader JoinNode() error = %v", err)
	}
	if _, err := leader.ActivateNode(context.Background(), ActivateNodeRequest{NodeID: 4}); err != nil {
		t.Fatalf("leader ActivateNode() error = %v", err)
	}
	observedActive := false
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !observedActive {
		snap, err := follower.LocalSnapshot(context.Background())
		if err == nil {
			for _, node := range snap.Nodes {
				if node.NodeID == 4 && node.JoinState == NodeJoinStateActive {
					observedActive = true
					break
				}
			}
		}
		if !observedActive {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if !observedActive {
		t.Fatal("timeout waiting for follower to observe active node")
	}
	result, err := follower.MarkNodeLeaving(context.Background(), MarkNodeLeavingRequest{NodeID: 4})
	if err != nil {
		t.Fatalf("MarkNodeLeaving() error = %v", err)
	}
	if !result.Changed || result.Node.NodeID != 4 || result.Node.JoinState != NodeJoinStateLeaving {
		t.Fatalf("MarkNodeLeaving() = %#v, want forwarded leaving node change", result)
	}
}

func TestRuntimeMarkNodeRemovedReturnsControlWriteAfterForward(t *testing.T) {
	network := clusternet.NewLocalNetwork()
	controlWriteClient := NewControlWriteClient(network)
	voters := []RuntimeVoter{{NodeID: 1, Addr: "n1"}, {NodeID: 2, Addr: "n2"}, {NodeID: 3, Addr: "n3"}}
	runtimes := make([]*Runtime, 0, len(voters))
	for _, voter := range voters {
		rt, err := NewRuntime(RuntimeConfig{
			NodeID:             voter.NodeID,
			Addr:               voter.Addr,
			StateDir:           t.TempDir(),
			ClusterID:          "cluster-forward-mark-node-removed",
			Role:               RuntimeRoleVoter,
			Voters:             voters,
			AllowBootstrap:     true,
			InitialSlotCount:   1,
			HashSlotCount:      4,
			ReplicaCount:       3,
			TickInterval:       10 * time.Millisecond,
			RaftTransport:      NewRaftTransport(network),
			ControlWriteClient: controlWriteClient,
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

	var leaderID uint64
	var leader *Runtime
	var follower *Runtime
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, rt := range runtimes {
			leaderID = rt.LeaderID()
			if leaderID == 0 {
				continue
			}
			for _, candidate := range runtimes {
				if candidate.cfg.NodeID == leaderID {
					leader = candidate
				} else {
					follower = candidate
				}
			}
			if leader != nil {
				snap, err := leader.LocalSnapshot(context.Background())
				if err != nil || snap.Revision == 0 {
					leader = nil
					follower = nil
					continue
				}
			}
			if leader != nil && follower != nil {
				break
			}
		}
		if leader != nil && follower != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if follower == nil || leader == nil {
		t.Fatal("timeout waiting for leader and follower runtime")
	}

	network.Register(leaderID, clusternet.RPCControlWrite, NewControlWriteHandler(leader))
	if _, err := leader.JoinNode(context.Background(), JoinNodeRequest{NodeID: 4, Addr: "n4", Roles: []Role{RoleData}, CapacityWeight: 1}); err != nil {
		t.Fatalf("leader JoinNode() error = %v", err)
	}
	if _, err := leader.ActivateNode(context.Background(), ActivateNodeRequest{NodeID: 4}); err != nil {
		t.Fatalf("leader ActivateNode() error = %v", err)
	}
	if _, err := leader.MarkNodeLeaving(context.Background(), MarkNodeLeavingRequest{NodeID: 4}); err != nil {
		t.Fatalf("leader MarkNodeLeaving() error = %v", err)
	}
	observedLeaving := false
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !observedLeaving {
		snap, err := follower.LocalSnapshot(context.Background())
		if err == nil {
			for _, node := range snap.Nodes {
				if node.NodeID == 4 && node.JoinState == NodeJoinStateLeaving {
					observedLeaving = true
					break
				}
			}
		}
		if !observedLeaving {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if !observedLeaving {
		t.Fatal("timeout waiting for follower to observe leaving node")
	}
	result, err := follower.MarkNodeRemoved(context.Background(), MarkNodeRemovedRequest{NodeID: 4})
	if err != nil {
		t.Fatalf("MarkNodeRemoved() error = %v", err)
	}
	if !result.Changed || result.Node.NodeID != 4 || result.Node.JoinState != NodeJoinStateRemoved || result.Node.Status != NodeDown {
		t.Fatalf("MarkNodeRemoved() = %#v, want forwarded removed down node change", result)
	}
}

func TestRuntimeRequestSlotReplicaMoveReturnsControlWriteAfterForward(t *testing.T) {
	network := clusternet.NewLocalNetwork()
	controlWriteClient := NewControlWriteClient(network)
	voters := []RuntimeVoter{{NodeID: 1, Addr: "n1"}, {NodeID: 2, Addr: "n2"}, {NodeID: 3, Addr: "n3"}}
	runtimes := make([]*Runtime, 0, len(voters))
	for _, voter := range voters {
		rt, err := NewRuntime(RuntimeConfig{
			NodeID:             voter.NodeID,
			Addr:               voter.Addr,
			StateDir:           t.TempDir(),
			ClusterID:          "cluster-forward-slot-replica-move",
			Role:               RuntimeRoleVoter,
			Voters:             voters,
			AllowBootstrap:     true,
			InitialSlotCount:   1,
			HashSlotCount:      4,
			ReplicaCount:       3,
			TickInterval:       10 * time.Millisecond,
			RaftTransport:      NewRaftTransport(network),
			ControlWriteClient: controlWriteClient,
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

	var leaderID uint64
	var follower *Runtime
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, rt := range runtimes {
			leaderID = rt.LeaderID()
			if leaderID == 0 {
				continue
			}
			if rt.cfg.NodeID != leaderID {
				follower = rt
				break
			}
		}
		if follower != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if follower == nil {
		t.Fatal("timeout waiting for follower runtime")
	}

	wantTask := ReconcileTask{
		TaskID:           "slot-1-replica-move-1-to-4-r9",
		SlotID:           1,
		Kind:             TaskKindSlotReplicaMove,
		Step:             TaskStepOpenLearner,
		SourceNode:       1,
		TargetNode:       4,
		TargetPeers:      []uint64{4, 2, 3},
		CompletionPolicy: TaskCompletionPolicySingleObserver,
		ConfigEpoch:      7,
		Status:           TaskStatusPending,
	}
	applier := &recordingControlWriteApplier{
		slotReplicaMoveResult: SlotReplicaMoveResult{Created: true, Task: &wantTask},
	}
	network.Register(leaderID, clusternet.RPCControlWrite, NewControlWriteHandler(applier))
	req := SlotReplicaMoveRequest{
		SlotID:        1,
		SourceNode:    1,
		TargetNode:    4,
		TargetPeers:   []uint64{4, 2, 3},
		ConfigEpoch:   7,
		StateRevision: 9,
	}
	result, err := follower.RequestSlotReplicaMove(context.Background(), req)
	if err != nil {
		t.Fatalf("RequestSlotReplicaMove() error = %v", err)
	}
	if len(applier.slotReplicaMoves) != 1 || applier.slotReplicaMoves[0].TargetNode != 4 {
		t.Fatalf("slotReplicaMoves = %#v, want one forwarded move", applier.slotReplicaMoves)
	}
	if !result.Created || result.Task == nil || result.Task.TaskID != wantTask.TaskID {
		t.Fatalf("RequestSlotReplicaMove() = %#v, want forwarded task", result)
	}
}

func TestRuntimePromoteControllerVoterCommitsLocalStateAfterProof(t *testing.T) {
	runtime, err := NewRuntime(RuntimeConfig{
		NodeID:           1,
		Addr:             "n1",
		StateDir:         t.TempDir(),
		ClusterID:        "cluster-promote-controller-voter-local",
		Role:             RuntimeRoleVoter,
		Voters:           []RuntimeVoter{{NodeID: 1, Addr: "n1"}},
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
	if _, err := runtime.JoinNode(context.Background(), JoinNodeRequest{NodeID: 4, Addr: "n4", Roles: []Role{RoleData}, CapacityWeight: 1}); err != nil {
		t.Fatalf("JoinNode() error = %v", err)
	}
	if _, err := runtime.ActivateNode(context.Background(), ActivateNodeRequest{NodeID: 4}); err != nil {
		t.Fatalf("ActivateNode() error = %v", err)
	}
	before, err := runtime.LocalSnapshot(context.Background())
	if err != nil {
		t.Fatalf("LocalSnapshot(before) error = %v", err)
	}

	result, err := runtime.PromoteControllerVoter(context.Background(), PromoteControllerVoterRequest{
		NodeID:              4,
		ExpectedRevision:    before.Revision,
		ExpectedVoters:      []uint64{1},
		ObservedConfigIndex: 11,
		ObservedVoters:      []uint64{1, 4},
	})
	if err != nil {
		t.Fatalf("PromoteControllerVoter() error = %v", err)
	}
	if !result.Changed || result.Node.NodeID != 4 || !controlNodeHasRole(result.Node, RoleController) || result.Revision <= before.Revision {
		t.Fatalf("PromoteControllerVoter() = %#v, want changed controller node revision > %d", result, before.Revision)
	}
	if !sameUint64Set(result.PreviousVoters, []uint64{1}) || !sameUint64Set(result.NextVoters, []uint64{1, 4}) {
		t.Fatalf("voters previous=%v next=%v, want [1] -> [1 4]", result.PreviousVoters, result.NextVoters)
	}
	if len(result.Warnings) != 1 || result.Warnings[0] != "controller_voter_count_even" {
		t.Fatalf("warnings = %#v, want even controller voter count warning", result.Warnings)
	}
	after, err := runtime.LocalSnapshot(context.Background())
	if err != nil {
		t.Fatalf("LocalSnapshot(after) error = %v", err)
	}
	node, ok := controlNodeByID(after.Nodes, 4)
	if !ok || !controlNodeHasRole(node, RoleController) {
		t.Fatalf("node 4 after promotion = %#v ok=%v, want controller role", node, ok)
	}
}

func TestRuntimePromoteControllerVoterRejectsExplicitEmptyExpectedVoters(t *testing.T) {
	runtime, err := NewRuntime(RuntimeConfig{
		NodeID:           1,
		Addr:             "n1",
		StateDir:         t.TempDir(),
		ClusterID:        "cluster-promote-controller-voter-empty-fence",
		Role:             RuntimeRoleVoter,
		Voters:           []RuntimeVoter{{NodeID: 1, Addr: "n1"}},
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
	if _, err := runtime.JoinNode(context.Background(), JoinNodeRequest{NodeID: 4, Addr: "n4", Roles: []Role{RoleData}, CapacityWeight: 1}); err != nil {
		t.Fatalf("JoinNode() error = %v", err)
	}
	if _, err := runtime.ActivateNode(context.Background(), ActivateNodeRequest{NodeID: 4}); err != nil {
		t.Fatalf("ActivateNode() error = %v", err)
	}
	before, err := runtime.LocalSnapshot(context.Background())
	if err != nil {
		t.Fatalf("LocalSnapshot(before) error = %v", err)
	}

	_, err = runtime.PromoteControllerVoter(context.Background(), PromoteControllerVoterRequest{
		NodeID:              4,
		ExpectedRevision:    before.Revision,
		ExpectedVoters:      []uint64{},
		ObservedConfigIndex: 11,
		ObservedVoters:      []uint64{1, 4},
	})
	if !errors.Is(err, cv2.ErrProposalRejected) || !strings.Contains(err.Error(), "controller_voter_set_mismatch") {
		t.Fatalf("PromoteControllerVoter(explicit empty fence) error = %v, want proposal rejected controller_voter_set_mismatch", err)
	}
	after, err := runtime.LocalSnapshot(context.Background())
	if err != nil {
		t.Fatalf("LocalSnapshot(after) error = %v", err)
	}
	if after.Revision != before.Revision {
		t.Fatalf("revision after rejected promotion = %d, want unchanged %d", after.Revision, before.Revision)
	}
	node, ok := controlNodeByID(after.Nodes, 4)
	if !ok || controlNodeHasRole(node, RoleController) {
		t.Fatalf("node 4 after rejected promotion = %#v ok=%v, want active data node without controller role", node, ok)
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

func TestRuntimeMirrorRefreshesLeaderStateAfterStart(t *testing.T) {
	network := clusternet.NewLocalNetwork()
	leaderState := controllerV2State()
	syncServer := cv2.NewStateSyncServer(cv2.StateSyncServerConfig{
		NodeID:    1,
		ClusterID: leaderState.ClusterID,
		LeaderID:  func() uint64 { return 1 },
		Ready:     func() bool { return true },
		Snapshot:  func(context.Context) (cv2.ClusterState, error) { return leaderState.Clone(), nil },
	})
	network.Register(1, clusternet.RPCControlStateSync, NewStateSyncHandler(syncServer))

	runtime, err := NewRuntime(RuntimeConfig{
		NodeID:       4,
		Addr:         "n4",
		StateDir:     t.TempDir(),
		ClusterID:    leaderState.ClusterID,
		Role:         RuntimeRoleMirror,
		Voters:       []RuntimeVoter{{NodeID: 1, Addr: "n1"}},
		TickInterval: 10 * time.Millisecond,
		SyncPeers:    NewStaticPeerPicker(network, []RuntimeVoter{{NodeID: 1, Addr: "n1"}}),
	})
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Stop(context.Background()) })

	leaderState.Revision++
	leaderState.Nodes = append(leaderState.Nodes, cv2.Node{
		NodeID:         4,
		Addr:           "n4",
		Roles:          []cv2.NodeRole{cv2.NodeRoleData},
		JoinState:      cv2.NodeJoinStateJoining,
		Status:         cv2.NodeStatusAlive,
		CapacityWeight: 1,
	})
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		snap, err := runtime.LocalSnapshot(context.Background())
		if err != nil {
			t.Fatalf("LocalSnapshot() error = %v", err)
		}
		if snap.Revision == leaderState.Revision && len(snap.Nodes) == len(leaderState.Nodes) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	snap, _ := runtime.LocalSnapshot(context.Background())
	t.Fatalf("mirror snapshot revision = %d nodes=%d, want revision %d nodes=%d", snap.Revision, len(snap.Nodes), leaderState.Revision, len(leaderState.Nodes))
}

func TestRuntimeMirrorForwardsControlWriteToSyncClientLeader(t *testing.T) {
	network := clusternet.NewLocalNetwork()
	leaderState := controllerV2State()
	leaderState.Controllers = append(leaderState.Controllers,
		cv2.ControllerVoter{NodeID: 2, Addr: "127.0.0.1:1002", Role: cv2.ControllerRoleVoter})
	for i := range leaderState.Nodes {
		if leaderState.Nodes[i].NodeID == 2 {
			leaderState.Nodes[i].Roles = []cv2.NodeRole{cv2.NodeRoleControllerVoter, cv2.NodeRoleData}
		}
	}
	syncServer := cv2.NewStateSyncServer(cv2.StateSyncServerConfig{
		NodeID:    2,
		ClusterID: leaderState.ClusterID,
		LeaderID:  func() uint64 { return 2 },
		Ready:     func() bool { return true },
		Snapshot:  func(context.Context) (cv2.ClusterState, error) { return leaderState, nil },
	})
	network.Register(1, clusternet.RPCControlStateSync, NewStateSyncHandler(syncServer))
	applier := &recordingControlWriteApplier{
		activateResult: ActivateNodeResult{
			Changed: true,
			Node: Node{
				NodeID:         4,
				Addr:           "n4",
				Roles:          []Role{RoleData},
				JoinState:      NodeJoinStateActive,
				Status:         NodeAlive,
				CapacityWeight: 1,
			},
			Revision: leaderState.Revision + 1,
		},
	}
	network.Register(2, clusternet.RPCControlWrite, NewControlWriteHandler(applier))

	runtime, err := NewRuntime(RuntimeConfig{
		NodeID:             4,
		Addr:               "n4",
		StateDir:           t.TempDir(),
		ClusterID:          leaderState.ClusterID,
		Role:               RuntimeRoleMirror,
		Voters:             []RuntimeVoter{{NodeID: 1, Addr: "n1"}, {NodeID: 2, Addr: "n2"}},
		SyncPeers:          NewStaticPeerPicker(network, []RuntimeVoter{{NodeID: 1, Addr: "n1"}}),
		ControlWriteClient: NewControlWriteClient(network),
	})
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Stop(context.Background()) })

	result, err := runtime.ActivateNode(context.Background(), ActivateNodeRequest{NodeID: 4})
	if err != nil {
		t.Fatalf("ActivateNode() error = %v", err)
	}
	if runtime.LeaderID() != 2 {
		t.Fatalf("LeaderID() = %d, want sync client leader 2", runtime.LeaderID())
	}
	if applier.activateCalls != 1 || !result.Changed || result.Node.NodeID != 4 {
		t.Fatalf("ActivateNode() = %#v, activateCalls=%d, want forwarded result from leader 2", result, applier.activateCalls)
	}
}

func TestRuntimeMirrorForwardsMarkNodeRemovedToSyncClientLeader(t *testing.T) {
	network := clusternet.NewLocalNetwork()
	leaderState := controllerV2State()
	leaderState.Controllers = append(leaderState.Controllers,
		cv2.ControllerVoter{NodeID: 2, Addr: "127.0.0.1:1002", Role: cv2.ControllerRoleVoter})
	for i := range leaderState.Nodes {
		if leaderState.Nodes[i].NodeID == 2 {
			leaderState.Nodes[i].Roles = []cv2.NodeRole{cv2.NodeRoleControllerVoter, cv2.NodeRoleData}
		}
	}
	syncServer := cv2.NewStateSyncServer(cv2.StateSyncServerConfig{
		NodeID:    2,
		ClusterID: leaderState.ClusterID,
		LeaderID:  func() uint64 { return 2 },
		Ready:     func() bool { return true },
		Snapshot:  func(context.Context) (cv2.ClusterState, error) { return leaderState, nil },
	})
	network.Register(1, clusternet.RPCControlStateSync, NewStateSyncHandler(syncServer))
	applier := &recordingControlWriteApplier{
		markNodeRemovedResult: MarkNodeRemovedResult{
			Changed: true,
			Node: Node{
				NodeID:         4,
				Addr:           "n4",
				Roles:          []Role{RoleData},
				JoinState:      NodeJoinStateRemoved,
				Status:         NodeDown,
				CapacityWeight: 1,
			},
			Revision: leaderState.Revision + 1,
		},
	}
	network.Register(2, clusternet.RPCControlWrite, NewControlWriteHandler(applier))

	runtime, err := NewRuntime(RuntimeConfig{
		NodeID:             4,
		Addr:               "n4",
		StateDir:           t.TempDir(),
		ClusterID:          leaderState.ClusterID,
		Role:               RuntimeRoleMirror,
		Voters:             []RuntimeVoter{{NodeID: 1, Addr: "n1"}, {NodeID: 2, Addr: "n2"}},
		SyncPeers:          NewStaticPeerPicker(network, []RuntimeVoter{{NodeID: 1, Addr: "n1"}}),
		ControlWriteClient: NewControlWriteClient(network),
	})
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Stop(context.Background()) })

	result, err := runtime.MarkNodeRemoved(context.Background(), MarkNodeRemovedRequest{NodeID: 4})
	if err != nil {
		t.Fatalf("MarkNodeRemoved() error = %v", err)
	}
	if runtime.LeaderID() != 2 {
		t.Fatalf("LeaderID() = %d, want sync client leader 2", runtime.LeaderID())
	}
	if len(applier.markNodeRemoved) != 1 || applier.markNodeRemoved[0].NodeID != 4 || !result.Changed || result.Node.JoinState != NodeJoinStateRemoved {
		t.Fatalf("MarkNodeRemoved() = %#v removed=%#v, want forwarded removed result from leader 2", result, applier.markNodeRemoved)
	}
}

func TestRuntimeMirrorForwardsPromoteControllerVoterToSyncClientLeader(t *testing.T) {
	network := clusternet.NewLocalNetwork()
	leaderState := controllerV2State()
	leaderState.Controllers = append(leaderState.Controllers,
		cv2.ControllerVoter{NodeID: 2, Addr: "127.0.0.1:1002", Role: cv2.ControllerRoleVoter})
	for i := range leaderState.Nodes {
		if leaderState.Nodes[i].NodeID == 2 {
			leaderState.Nodes[i].Roles = []cv2.NodeRole{cv2.NodeRoleControllerVoter, cv2.NodeRoleData}
		}
	}
	syncServer := cv2.NewStateSyncServer(cv2.StateSyncServerConfig{
		NodeID:    2,
		ClusterID: leaderState.ClusterID,
		LeaderID:  func() uint64 { return 2 },
		Ready:     func() bool { return true },
		Snapshot:  func(context.Context) (cv2.ClusterState, error) { return leaderState, nil },
	})
	network.Register(1, clusternet.RPCControlStateSync, NewStateSyncHandler(syncServer))
	applier := &recordingControlWriteApplier{
		promoteControllerVoterResult: PromoteControllerVoterResult{
			Changed: true,
			Node: Node{
				NodeID:         3,
				Addr:           "127.0.0.1:1003",
				Roles:          []Role{RoleData, RoleController},
				JoinState:      NodeJoinStateActive,
				Status:         NodeAlive,
				CapacityWeight: 1,
			},
			Revision:       leaderState.Revision + 1,
			PreviousVoters: []uint64{1, 2},
			NextVoters:     []uint64{1, 2, 3},
		},
	}
	network.Register(2, clusternet.RPCControlWrite, NewControlWriteHandler(applier))

	runtime, err := NewRuntime(RuntimeConfig{
		NodeID:             3,
		Addr:               "127.0.0.1:1003",
		StateDir:           t.TempDir(),
		ClusterID:          leaderState.ClusterID,
		Role:               RuntimeRoleMirror,
		Voters:             []RuntimeVoter{{NodeID: 1, Addr: "n1"}, {NodeID: 2, Addr: "n2"}},
		SyncPeers:          NewStaticPeerPicker(network, []RuntimeVoter{{NodeID: 1, Addr: "n1"}}),
		ControlWriteClient: NewControlWriteClient(network),
	})
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Stop(context.Background()) })

	req := PromoteControllerVoterRequest{
		NodeID:              3,
		ExpectedRevision:    leaderState.Revision,
		ExpectedVoters:      []uint64{1, 2},
		ObservedConfigIndex: 11,
		ObservedVoters:      []uint64{1, 2, 3},
	}
	result, err := runtime.PromoteControllerVoter(context.Background(), req)
	if err != nil {
		t.Fatalf("PromoteControllerVoter() error = %v", err)
	}
	if runtime.LeaderID() != 2 {
		t.Fatalf("LeaderID() = %d, want sync client leader 2", runtime.LeaderID())
	}
	if len(applier.promoteControllerVoters) != 1 || !reflect.DeepEqual(applier.promoteControllerVoters[0], req) || !result.Changed || result.Node.NodeID != 3 {
		t.Fatalf("PromoteControllerVoter() = %#v requests=%#v, want forwarded promotion result from leader 2", result, applier.promoteControllerVoters)
	}
}

func TestRuntimePromoteControllerVoterPreservesForwardedSemanticError(t *testing.T) {
	network := clusternet.NewLocalNetwork()
	leaderState := controllerV2State()
	leaderState.Controllers = append(leaderState.Controllers,
		cv2.ControllerVoter{NodeID: 2, Addr: "127.0.0.1:1002", Role: cv2.ControllerRoleVoter})
	for i := range leaderState.Nodes {
		if leaderState.Nodes[i].NodeID == 2 {
			leaderState.Nodes[i].Roles = []cv2.NodeRole{cv2.NodeRoleControllerVoter, cv2.NodeRoleData}
		}
	}
	syncServer := cv2.NewStateSyncServer(cv2.StateSyncServerConfig{
		NodeID:    2,
		ClusterID: leaderState.ClusterID,
		LeaderID:  func() uint64 { return 2 },
		Ready:     func() bool { return true },
		Snapshot:  func(context.Context) (cv2.ClusterState, error) { return leaderState, nil },
	})
	network.Register(1, clusternet.RPCControlStateSync, NewStateSyncHandler(syncServer))
	applier := &recordingControlWriteApplier{promoteControllerVoterErr: cv2.ErrExpectedRevisionMismatch}
	network.Register(2, clusternet.RPCControlWrite, NewControlWriteHandler(applier))

	runtime, err := NewRuntime(RuntimeConfig{
		NodeID:             3,
		Addr:               "127.0.0.1:1003",
		StateDir:           t.TempDir(),
		ClusterID:          leaderState.ClusterID,
		Role:               RuntimeRoleMirror,
		Voters:             []RuntimeVoter{{NodeID: 1, Addr: "n1"}, {NodeID: 2, Addr: "n2"}},
		SyncPeers:          NewStaticPeerPicker(network, []RuntimeVoter{{NodeID: 1, Addr: "n1"}}),
		ControlWriteClient: NewControlWriteClient(network),
	})
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Stop(context.Background()) })

	_, err = runtime.PromoteControllerVoter(context.Background(), PromoteControllerVoterRequest{
		NodeID:           3,
		ExpectedRevision: leaderState.Revision + 1,
	})
	if !errors.Is(err, cv2.ErrExpectedRevisionMismatch) {
		t.Fatalf("PromoteControllerVoter() error = %v, want errors.Is(ErrExpectedRevisionMismatch)", err)
	}
}

func TestPromoteControllerVoterResultFromCV2AddsEvenVoterWarning(t *testing.T) {
	previous := []uint64{1, 2, 3}
	next := []uint64{1, 2, 3, 4}
	result := promoteControllerVoterResultFromCV2(cv2.PromoteControllerVoterResult{
		Changed: true,
		Node: cv2.Node{
			NodeID:         4,
			Addr:           "n4",
			Roles:          []cv2.NodeRole{cv2.NodeRoleData, cv2.NodeRoleControllerVoter},
			JoinState:      cv2.NodeJoinStateActive,
			Status:         cv2.NodeStatusAlive,
			CapacityWeight: 1,
		},
		Revision:       10,
		PreviousVoters: previous,
		NextVoters:     next,
	})

	if !result.Changed || result.Node.NodeID != 4 || result.Node.Roles[1] != RoleController || result.Revision != 10 {
		t.Fatalf("promoteControllerVoterResultFromCV2() = %#v, want mapped controller node", result)
	}
	if len(result.Warnings) != 1 || result.Warnings[0] != "controller_voter_count_even" {
		t.Fatalf("warnings = %#v, want controller_voter_count_even", result.Warnings)
	}
	result.PreviousVoters[0] = 99
	result.NextVoters[0] = 99
	if previous[0] != 1 || next[0] != 1 {
		t.Fatalf("promoteControllerVoterResultFromCV2 did not copy voter slices: previous=%v next=%v", previous, next)
	}
}

func TestRuntimeActivateNodeForwardsBeforeFollowerLocalValidation(t *testing.T) {
	network := clusternet.NewLocalNetwork()
	controlWriteClient := NewControlWriteClient(network)
	voters := []RuntimeVoter{{NodeID: 1, Addr: "n1"}, {NodeID: 2, Addr: "n2"}, {NodeID: 3, Addr: "n3"}}
	runtimes := make([]*Runtime, 0, len(voters))
	for _, voter := range voters {
		rt, err := NewRuntime(RuntimeConfig{
			NodeID:             voter.NodeID,
			Addr:               voter.Addr,
			StateDir:           t.TempDir(),
			ClusterID:          "cluster-forward-activate-before-local-validation",
			Role:               RuntimeRoleVoter,
			Voters:             voters,
			AllowBootstrap:     true,
			InitialSlotCount:   1,
			HashSlotCount:      4,
			ReplicaCount:       3,
			TickInterval:       10 * time.Millisecond,
			RaftTransport:      NewRaftTransport(network),
			ControlWriteClient: controlWriteClient,
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

	var leaderID uint64
	var leader *Runtime
	var follower *Runtime
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, rt := range runtimes {
			leaderID = rt.LeaderID()
			if leaderID == 0 {
				continue
			}
			for _, candidate := range runtimes {
				if candidate.cfg.NodeID == leaderID {
					leader = candidate
				} else {
					follower = candidate
				}
			}
			if leader != nil {
				snap, err := leader.LocalSnapshot(context.Background())
				if err != nil || snap.Revision == 0 {
					leader = nil
					follower = nil
					continue
				}
			}
			if leader != nil && follower != nil {
				break
			}
		}
		if leader != nil && follower != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if follower == nil || leader == nil {
		t.Fatal("timeout waiting for leader and follower runtime")
	}
	localSnap, err := follower.LocalSnapshot(context.Background())
	if err != nil {
		t.Fatalf("follower LocalSnapshot() error = %v", err)
	}
	for _, node := range localSnap.Nodes {
		if node.NodeID == 4 {
			t.Fatalf("follower already has node 4 in local state: %#v", localSnap.Nodes)
		}
	}

	applier := &recordingControlWriteApplier{
		activateResult: ActivateNodeResult{
			Changed: true,
			Node: Node{
				NodeID:         4,
				Addr:           "n4",
				Roles:          []Role{RoleData},
				JoinState:      NodeJoinStateActive,
				Status:         NodeAlive,
				CapacityWeight: 1,
			},
			Revision: localSnap.Revision + 1,
		},
	}
	network.Register(leaderID, clusternet.RPCControlWrite, NewControlWriteHandler(applier))
	result, err := follower.ActivateNode(context.Background(), ActivateNodeRequest{NodeID: 4})
	if err != nil {
		t.Fatalf("ActivateNode() error = %v", err)
	}
	if applier.activateCalls != 1 || !result.Changed || result.Node.NodeID != 4 {
		t.Fatalf("ActivateNode() = %#v, activateCalls=%d, want forwarded result before local validation", result, applier.activateCalls)
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

func snapshotJoinState(snapshot Snapshot, nodeID uint64) NodeJoinState {
	for _, node := range snapshot.Nodes {
		if node.NodeID == nodeID {
			return node.JoinState
		}
	}
	return ""
}

func controlNodeByID(nodes []Node, nodeID uint64) (Node, bool) {
	for _, node := range nodes {
		if node.NodeID == nodeID {
			return node, true
		}
	}
	return Node{}, false
}

func controlNodeHasRole(node Node, role Role) bool {
	for _, candidate := range node.Roles {
		if candidate == role {
			return true
		}
	}
	return false
}

func sameUint64Set(left, right []uint64) bool {
	if len(left) != len(right) {
		return false
	}
	seen := make(map[uint64]int, len(left))
	for _, value := range left {
		seen[value]++
	}
	for _, value := range right {
		if seen[value] == 0 {
			return false
		}
		seen[value]--
	}
	return true
}
