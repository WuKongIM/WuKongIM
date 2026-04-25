package cluster

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/slotmigration"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	slotcontroller "github.com/WuKongIM/WuKongIM/pkg/controller/plane"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
	"go.etcd.io/raft/v3/raftpb"
)

func TestObserverHooksOnForwardPropose(t *testing.T) {
	var (
		gotSlot     uint32
		gotAttempts int
		gotErr      error
		calls       int
	)
	cluster := newObserverTestCluster(t, ObserverHooks{
		OnForwardPropose: func(slotID uint32, attempts int, dur time.Duration, err error) {
			calls++
			gotSlot = slotID
			gotAttempts = attempts
			gotErr = err
			if dur <= 0 {
				t.Fatalf("OnForwardPropose() duration = %v, want > 0", dur)
			}
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := cluster.ensureManagedSlotLocal(ctx, 1, []uint64{1}, false, true); err != nil {
		t.Fatalf("ensureManagedSlotLocal() error = %v", err)
	}
	if err := cluster.waitForManagedSlotLeader(ctx, 1); err != nil {
		t.Fatalf("waitForManagedSlotLeader() error = %v", err)
	}
	if err := cluster.Propose(ctx, 1, []byte("observer-propose")); err != nil {
		t.Fatalf("Propose() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("OnForwardPropose() calls = %d, want 1", calls)
	}
	if gotSlot != 1 {
		t.Fatalf("OnForwardPropose() slotID = %d, want 1", gotSlot)
	}
	if gotAttempts < 1 {
		t.Fatalf("OnForwardPropose() attempts = %d, want >= 1", gotAttempts)
	}
	if gotErr != nil {
		t.Fatalf("OnForwardPropose() err = %v, want nil", gotErr)
	}
}

func TestObserverHooksOnControllerCall(t *testing.T) {
	server := transport.NewServer()
	mux := transport.NewRPCMux()
	mux.Handle(rpcServiceController, func(_ context.Context, body []byte) ([]byte, error) {
		req, err := decodeControllerRequest(body)
		if err != nil {
			t.Fatalf("decodeControllerRequest() error = %v", err)
		}
		if req.Kind != controllerRPCListNodes {
			t.Fatalf("request kind = %q, want %q", req.Kind, controllerRPCListNodes)
		}
		return encodeControllerResponse(req.Kind, controllerRPCResponse{
			Nodes: []controllermeta.ClusterNode{{NodeID: 2, Addr: "n2"}},
		})
	})
	server.HandleRPCMux(mux)
	if err := server.Start("127.0.0.1:0"); err != nil {
		t.Fatalf("server.Start() error = %v", err)
	}
	defer server.Stop()

	discovery := &controllerClientTestDiscovery{
		addrs: map[uint64]string{2: server.Listener().Addr().String()},
	}
	pool := transport.NewPool(discovery, 1, 50*time.Millisecond)
	defer pool.Close()

	client := transport.NewClient(pool)
	defer client.Stop()

	var (
		gotKind string
		gotErr  error
		calls   int
	)
	cluster := newObserverTestCluster(t, ObserverHooks{
		OnControllerCall: func(kind string, dur time.Duration, err error) {
			calls++
			gotKind = kind
			gotErr = err
			if dur <= 0 {
				t.Fatalf("OnControllerCall() duration = %v, want > 0", dur)
			}
		},
	})
	cluster.fwdClient = client

	controllerClient := newControllerClient(cluster, []NodeConfig{{NodeID: 2}}, nil)
	nodes, err := controllerClient.ListNodes(context.Background())
	if err != nil {
		t.Fatalf("ListNodes() error = %v", err)
	}
	if len(nodes) != 1 || nodes[0].NodeID != 2 {
		t.Fatalf("ListNodes() = %+v", nodes)
	}
	if calls != 1 {
		t.Fatalf("OnControllerCall() calls = %d, want 1", calls)
	}
	if gotKind != controllerRPCListNodes {
		t.Fatalf("OnControllerCall() kind = %q, want %q", gotKind, controllerRPCListNodes)
	}
	if gotErr != nil {
		t.Fatalf("OnControllerCall() err = %v, want nil", gotErr)
	}
}

func TestObserverHooksOnControllerDecision(t *testing.T) {
	cfg := validTestConfig()
	cfg.ControllerReplicaN = 1
	cfg.SlotReplicaN = 1
	cfg.Nodes = []NodeConfig{{NodeID: cfg.NodeID, Addr: "127.0.0.1:0"}}
	cfg.ControllerMetaPath = filepath.Join(t.TempDir(), "controller-meta")
	cfg.ControllerRaftPath = filepath.Join(t.TempDir(), "controller-raft")

	discovery := NewStaticDiscovery(cfg.Nodes)
	layer := newTransportLayer(cfg, discovery, nil)
	requireNoErr(t, layer.Start(
		"127.0.0.1:0",
		func([]byte) {},
		func(context.Context, []byte) ([]byte, error) { return nil, nil },
		func(context.Context, []byte) ([]byte, error) { return nil, nil },
		func(context.Context, []byte) ([]byte, error) { return nil, nil },
	))
	t.Cleanup(layer.Stop)

	cfg.Nodes[0].Addr = layer.server.Listener().Addr().String()
	host, err := newControllerHost(cfg, layer)
	if err != nil {
		t.Fatalf("newControllerHost() error = %v", err)
	}
	requireNoErr(t, host.Start(context.Background()))
	t.Cleanup(host.Stop)

	deadline := time.Now().Add(2 * time.Second)
	for host.LeaderID() != cfg.NodeID && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if host.LeaderID() != cfg.NodeID {
		t.Fatalf("controllerHost.LeaderID() = %d, want %d", host.LeaderID(), cfg.NodeID)
	}

	requireNoErr(t, host.meta.UpsertNode(context.Background(), controllermeta.ClusterNode{
		NodeID:          uint64(cfg.NodeID),
		Addr:            cfg.Nodes[0].Addr,
		Status:          controllermeta.NodeStatusAlive,
		LastHeartbeatAt: time.Now(),
		CapacityWeight:  1,
	}))

	var (
		gotSlot uint32
		gotKind string
		calls   int
	)
	cluster := &Cluster{
		cfg: cfg,
		controllerResources: controllerResources{
			controllerMeta: host.meta,
			controller:     host.service,
		},
		obs: ObserverHooks{
			OnControllerDecision: func(slotID uint32, kind string, dur time.Duration) {
				calls++
				gotSlot = slotID
				gotKind = kind
				if dur <= 0 {
					t.Fatalf("OnControllerDecision() duration = %v, want > 0", dur)
				}
			},
		},
	}

	cluster.controllerTickOnce(context.Background())

	if calls != 1 {
		t.Fatalf("OnControllerDecision() calls = %d, want 1", calls)
	}
	if gotSlot != 1 {
		t.Fatalf("OnControllerDecision() slotID = %d, want 1", gotSlot)
	}
	if gotKind != "bootstrap" {
		t.Fatalf("OnControllerDecision() kind = %q, want %q", gotKind, "bootstrap")
	}
}

func TestControllerLeaderHandleCommittedCommandInvokesOnNodeStatusChange(t *testing.T) {
	cfg := validTestConfig()
	cfg.ControllerReplicaN = 1
	cfg.SlotReplicaN = 1
	cfg.Nodes = []NodeConfig{{NodeID: cfg.NodeID, Addr: "127.0.0.1:0"}}
	cfg.ControllerMetaPath = filepath.Join(t.TempDir(), "controller-meta")
	cfg.ControllerRaftPath = filepath.Join(t.TempDir(), "controller-raft")

	var (
		gotNode uint64
		gotFrom controllermeta.NodeStatus
		gotTo   controllermeta.NodeStatus
		calls   int
	)
	cfg.Observer = ObserverHooks{
		OnNodeStatusChange: func(nodeID uint64, from, to controllermeta.NodeStatus) {
			calls++
			gotNode = nodeID
			gotFrom = from
			gotTo = to
		},
	}

	discovery := NewStaticDiscovery(cfg.Nodes)
	layer := newTransportLayer(cfg, discovery, nil)
	requireNoErr(t, layer.Start(
		"127.0.0.1:0",
		func([]byte) {},
		func(context.Context, []byte) ([]byte, error) { return nil, nil },
		func(context.Context, []byte) ([]byte, error) { return nil, nil },
		func(context.Context, []byte) ([]byte, error) { return nil, nil },
	))
	t.Cleanup(layer.Stop)

	cfg.Nodes[0].Addr = layer.server.Listener().Addr().String()
	host, err := newControllerHost(cfg, layer)
	if err != nil {
		t.Fatalf("newControllerHost() error = %v", err)
	}
	requireNoErr(t, host.Start(context.Background()))
	t.Cleanup(host.Stop)

	deadline := time.Now().Add(2 * time.Second)
	for host.LeaderID() != cfg.NodeID && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if host.LeaderID() != cfg.NodeID {
		t.Fatalf("controllerHost.LeaderID() = %d, want %d", host.LeaderID(), cfg.NodeID)
	}

	host.healthScheduler.mirrorNode(controllermeta.ClusterNode{
		NodeID: 7,
		Addr:   "127.0.0.1:7007",
		Status: controllermeta.NodeStatusAlive,
	})
	requireNoErr(t, host.meta.UpsertNode(context.Background(), controllermeta.ClusterNode{
		NodeID:          7,
		Addr:            "127.0.0.1:7007",
		Status:          controllermeta.NodeStatusDead,
		LastHeartbeatAt: time.Now(),
		CapacityWeight:  1,
	}))

	alive := controllermeta.NodeStatusAlive
	host.handleCommittedCommand(slotcontroller.Command{
		Kind: slotcontroller.CommandKindNodeStatusUpdate,
		NodeStatusUpdate: &slotcontroller.NodeStatusUpdate{
			Transitions: []slotcontroller.NodeStatusTransition{{
				NodeID:         7,
				NewStatus:      controllermeta.NodeStatusDead,
				ExpectedStatus: &alive,
				EvaluatedAt:    time.Now(),
				Addr:           "127.0.0.1:7007",
				CapacityWeight: 1,
			}},
		},
	})

	if calls != 1 {
		t.Fatalf("OnNodeStatusChange() calls = %d, want 1", calls)
	}
	if gotNode != 7 {
		t.Fatalf("OnNodeStatusChange() nodeID = %d, want 7", gotNode)
	}
	if gotFrom != controllermeta.NodeStatusAlive || gotTo != controllermeta.NodeStatusDead {
		t.Fatalf("OnNodeStatusChange() from=%v to=%v, want %v->%v", gotFrom, gotTo, controllermeta.NodeStatusAlive, controllermeta.NodeStatusDead)
	}
}

func TestObserverHooksOnReconcileStep(t *testing.T) {
	sentinel := errors.New("reconcile step failed")
	var (
		gotSlot uint32
		gotStep string
		gotErr  error
		calls   int
	)
	cluster := newObserverTestCluster(t, ObserverHooks{
		OnReconcileStep: func(slotID uint32, step string, dur time.Duration, err error) {
			calls++
			gotSlot = slotID
			gotStep = step
			gotErr = err
			if dur <= 0 {
				t.Fatalf("OnReconcileStep() duration = %v, want > 0", dur)
			}
		},
	})
	restore := cluster.SetManagedSlotExecutionTestHook(func(slotID uint32, task controllermeta.ReconcileTask) error {
		if slotID != 1 || task.Step != controllermeta.TaskStepAddLearner {
			t.Fatalf("execution hook got slot=%d step=%d", slotID, task.Step)
		}
		return sentinel
	})
	defer restore()

	err := cluster.executeReconcileTask(context.Background(), assignmentTaskState{
		assignment: controllermeta.SlotAssignment{SlotID: 1, DesiredPeers: []uint64{1}},
		task: controllermeta.ReconcileTask{
			SlotID: 1,
			Kind:   controllermeta.TaskKindRepair,
			Step:   controllermeta.TaskStepAddLearner,
		},
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("executeReconcileTask() error = %v, want %v", err, sentinel)
	}
	if calls != 1 {
		t.Fatalf("OnReconcileStep() calls = %d, want 1", calls)
	}
	if gotSlot != 1 {
		t.Fatalf("OnReconcileStep() slotID = %d, want 1", gotSlot)
	}
	if gotStep != "add_learner" {
		t.Fatalf("OnReconcileStep() step = %q, want %q", gotStep, "add_learner")
	}
	if !errors.Is(gotErr, sentinel) {
		t.Fatalf("OnReconcileStep() err = %v, want %v", gotErr, sentinel)
	}
}

func TestObserverHooksOnTaskResult(t *testing.T) {
	var (
		gotSlot   uint32
		gotKind   string
		gotResult string
		calls     int
	)
	cluster := &Cluster{
		obs: ObserverHooks{
			OnTaskResult: func(slotID uint32, kind string, result string) {
				calls++
				gotSlot = slotID
				gotKind = kind
				gotResult = result
			},
		},
	}
	agent := &slotAgent{
		cluster: cluster,
		client: fakeControllerClient{
			reportTaskResultFn: func(context.Context, controllermeta.ReconcileTask, error) error {
				return nil
			},
		},
	}

	err := agent.reportTaskResult(context.Background(), controllermeta.ReconcileTask{
		SlotID: 1,
		Kind:   controllermeta.TaskKindRepair,
	}, context.DeadlineExceeded)
	if err != nil {
		t.Fatalf("reportTaskResult() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("OnTaskResult() calls = %d, want 1", calls)
	}
	if gotSlot != 1 {
		t.Fatalf("OnTaskResult() slotID = %d, want 1", gotSlot)
	}
	if gotKind != "repair" {
		t.Fatalf("OnTaskResult() kind = %q, want %q", gotKind, "repair")
	}
	if gotResult != "timeout" {
		t.Fatalf("OnTaskResult() result = %q, want %q", gotResult, "timeout")
	}
}

func TestObserverHooksOnHashSlotMigrationComplete(t *testing.T) {
	table := NewHashSlotTable(8, 2)
	table.StartMigration(3, 1, 2)

	var (
		gotHashSlot uint16
		gotSource   multiraft.SlotID
		gotTarget   multiraft.SlotID
		gotResult   string
		calls       int
	)
	cluster := &Cluster{
		cfg:             Config{NodeID: 1},
		router:          NewRouter(table, 1, nil),
		migrationWorker: &fakeHashSlotMigrationWorker{transitionsByTick: [][]slotmigration.Transition{{{HashSlot: 3, Source: 1, Target: 2, To: slotmigration.PhaseDone}}}},
		controllerResources: controllerResources{
			controllerClient: fakeControllerClient{
				finalizeMigrationFn: func(_ context.Context, req slotcontroller.MigrationRequest) error {
					if req.HashSlot != 3 || req.Source != 1 || req.Target != 2 {
						t.Fatalf("finalize migration req = %#v", req)
					}
					return nil
				},
			},
		},
		obs: ObserverHooks{
			OnHashSlotMigration: func(hashSlot uint16, source, target multiraft.SlotID, result string) {
				calls++
				gotHashSlot = hashSlot
				gotSource = source
				gotTarget = target
				gotResult = result
			},
		},
	}

	restore := cluster.setManagedSlotLeaderTestHook(func(_ *Cluster, slotID multiraft.SlotID) (multiraft.NodeID, error, bool) {
		if slotID != 1 {
			return 0, nil, false
		}
		return 1, nil, true
	})
	defer restore()

	if err := cluster.observeHashSlotMigrations(context.Background()); err != nil {
		t.Fatalf("observeHashSlotMigrations() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("OnHashSlotMigration() calls = %d, want 1", calls)
	}
	if gotHashSlot != 3 {
		t.Fatalf("OnHashSlotMigration() hashSlot = %d, want 3", gotHashSlot)
	}
	if gotSource != 1 || gotTarget != 2 {
		t.Fatalf("OnHashSlotMigration() source=%d target=%d, want 1->2", gotSource, gotTarget)
	}
	if gotResult != "ok" {
		t.Fatalf("OnHashSlotMigration() result = %q, want %q", gotResult, "ok")
	}
}

func TestObserverHooksOnHashSlotMigrationAbort(t *testing.T) {
	table := NewHashSlotTable(8, 2)
	table.StartMigration(3, 1, 2)
	table.AdvanceMigration(3, PhaseDelta)

	var (
		gotHashSlot uint16
		gotSource   multiraft.SlotID
		gotTarget   multiraft.SlotID
		gotResult   string
		calls       int
	)
	cluster := &Cluster{
		cfg:    Config{NodeID: 1},
		router: NewRouter(table, 1, nil),
		migrationWorker: &fakeHashSlotMigrationWorker{
			active: []slotmigration.Migration{{HashSlot: 3, Source: 1, Target: 2, Phase: slotmigration.PhaseDelta}},
			transitionsByTick: [][]slotmigration.Transition{
				{{HashSlot: 3, Source: 1, Target: 2, TimedOut: true}},
			},
		},
		controllerResources: controllerResources{
			controllerClient: fakeControllerClient{
				abortMigrationFn: func(_ context.Context, req slotcontroller.MigrationRequest) error {
					if req.HashSlot != 3 || req.Source != 1 || req.Target != 2 {
						t.Fatalf("abort migration req = %#v", req)
					}
					return nil
				},
			},
		},
		obs: ObserverHooks{
			OnHashSlotMigration: func(hashSlot uint16, source, target multiraft.SlotID, result string) {
				calls++
				gotHashSlot = hashSlot
				gotSource = source
				gotTarget = target
				gotResult = result
			},
		},
	}

	restore := cluster.setManagedSlotLeaderTestHook(func(_ *Cluster, slotID multiraft.SlotID) (multiraft.NodeID, error, bool) {
		if slotID != 1 {
			return 0, nil, false
		}
		return 1, nil, true
	})
	defer restore()

	if err := cluster.observeHashSlotMigrations(context.Background()); err != nil {
		t.Fatalf("observeHashSlotMigrations() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("OnHashSlotMigration() calls = %d, want 1", calls)
	}
	if gotHashSlot != 3 {
		t.Fatalf("OnHashSlotMigration() hashSlot = %d, want 3", gotHashSlot)
	}
	if gotSource != 1 || gotTarget != 2 {
		t.Fatalf("OnHashSlotMigration() source=%d target=%d, want 1->2", gotSource, gotTarget)
	}
	if gotResult != "abort" {
		t.Fatalf("OnHashSlotMigration() result = %q, want %q", gotResult, "abort")
	}
}

func TestObserverHooksOnSlotEnsure(t *testing.T) {
	var (
		gotSlot   uint32
		gotAction string
		gotErr    error
		calls     int
	)
	cluster := newObserverTestCluster(t, ObserverHooks{
		OnSlotEnsure: func(slotID uint32, action string, err error) {
			calls++
			gotSlot = slotID
			gotAction = action
			gotErr = err
		},
	})

	if err := cluster.ensureManagedSlotLocal(context.Background(), 1, []uint64{1}, false, true); err != nil {
		t.Fatalf("ensureManagedSlotLocal() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("OnSlotEnsure() calls = %d, want 1", calls)
	}
	if gotSlot != 1 {
		t.Fatalf("OnSlotEnsure() slotID = %d, want 1", gotSlot)
	}
	if gotAction != "bootstrap" {
		t.Fatalf("OnSlotEnsure() action = %q, want %q", gotAction, "bootstrap")
	}
	if gotErr != nil {
		t.Fatalf("OnSlotEnsure() err = %v, want nil", gotErr)
	}
}

func TestObserverHooksOnSlotOpen(t *testing.T) {
	var (
		gotSlot   uint32
		gotAction string
		gotErr    error
		calls     int
	)
	cluster := newObserverTestCluster(t, ObserverHooks{
		OnSlotEnsure: func(slotID uint32, action string, err error) {
			calls++
			gotSlot = slotID
			gotAction = action
			gotErr = err
		},
	})
	cluster.cfg.NewStorage = func(multiraft.SlotID) (multiraft.Storage, error) {
		return &observerTestStorage{
			state: multiraft.BootstrapState{
				HardState: raftpb.HardState{Term: 1},
				ConfState: raftpb.ConfState{Voters: []uint64{1}},
			},
		}, nil
	}

	if err := cluster.openOrBootstrapSlot(context.Background(), SlotConfig{SlotID: 1, Peers: []multiraft.NodeID{1}}); err != nil {
		t.Fatalf("openOrBootstrapSlot() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("OnSlotEnsure() calls = %d, want 1", calls)
	}
	if gotSlot != 1 {
		t.Fatalf("OnSlotEnsure() slotID = %d, want 1", gotSlot)
	}
	if gotAction != "open" {
		t.Fatalf("OnSlotEnsure() action = %q, want %q", gotAction, "open")
	}
	if gotErr != nil {
		t.Fatalf("OnSlotEnsure() err = %v, want nil", gotErr)
	}
}

func TestObserverHooksOnSlotClose(t *testing.T) {
	var (
		gotSlot   uint32
		gotAction string
		gotErr    error
		calls     int
	)
	cluster := newObserverTestCluster(t, ObserverHooks{
		OnSlotEnsure: func(slotID uint32, action string, err error) {
			calls++
			gotSlot = slotID
			gotAction = action
			gotErr = err
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := cluster.ensureManagedSlotLocal(ctx, 1, []uint64{1}, false, true); err != nil {
		t.Fatalf("ensureManagedSlotLocal() error = %v", err)
	}
	if err := cluster.waitForManagedSlotLeader(ctx, 1); err != nil {
		t.Fatalf("waitForManagedSlotLeader() error = %v", err)
	}

	cluster.assignments.SetAssignments([]controllermeta.SlotAssignment{
		{SlotID: 1, DesiredPeers: []uint64{2}},
	})
	agent := &slotAgent{
		cluster: cluster,
		client:  fakeControllerClient{},
		cache:   cluster.assignments,
	}
	calls = 0

	if err := agent.ApplyAssignments(ctx); err != nil {
		t.Fatalf("ApplyAssignments() error = %v", err)
	}
	if _, err := cluster.runtime.Status(1); !errors.Is(err, multiraft.ErrSlotNotFound) {
		t.Fatalf("runtime.Status() error = %v, want %v", err, multiraft.ErrSlotNotFound)
	}
	if calls != 1 {
		t.Fatalf("OnSlotEnsure() calls = %d, want 1", calls)
	}
	if gotSlot != 1 {
		t.Fatalf("OnSlotEnsure() slotID = %d, want 1", gotSlot)
	}
	if gotAction != "close" {
		t.Fatalf("OnSlotEnsure() action = %q, want %q", gotAction, "close")
	}
	if gotErr != nil {
		t.Fatalf("OnSlotEnsure() err = %v, want nil", gotErr)
	}
}

func TestObserverHooksOnLeaderChange(t *testing.T) {
	var (
		gotSlot uint32
		gotFrom multiraft.NodeID
		gotTo   multiraft.NodeID
		calls   int
	)
	cluster := newObserverTestCluster(t, ObserverHooks{
		OnLeaderChange: func(slotID uint32, from, to multiraft.NodeID) {
			calls++
			gotSlot = slotID
			gotFrom = from
			gotTo = to
		},
	})

	attempts := 0
	restore := cluster.setManagedSlotLeaderTestHook(func(_ *Cluster, slotID multiraft.SlotID) (multiraft.NodeID, error, bool) {
		if slotID != 1 {
			return 0, nil, false
		}
		attempts++
		if attempts < 2 {
			return 0, nil, true
		}
		return 2, nil, true
	})
	defer restore()

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()

	if err := cluster.waitForManagedSlotLeader(ctx, 1); err != nil {
		t.Fatalf("waitForManagedSlotLeader() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("OnLeaderChange() calls = %d, want 1", calls)
	}
	if gotSlot != 1 {
		t.Fatalf("OnLeaderChange() slotID = %d, want 1", gotSlot)
	}
	if gotFrom != 0 || gotTo != 2 {
		t.Fatalf("OnLeaderChange() from=%d to=%d, want 0->2", gotFrom, gotTo)
	}
}

func TestObserverHooksOnLeaderMoveTransition(t *testing.T) {
	var (
		gotSlot uint32
		gotFrom multiraft.NodeID
		gotTo   multiraft.NodeID
		calls   int
	)
	hooks := ObserverHooks{
		OnLeaderChange: func(slotID uint32, from, to multiraft.NodeID) {
			calls++
			gotSlot = slotID
			gotFrom = from
			gotTo = to
		},
	}
	cluster := &Cluster{
		cfg: Config{
			Observer: hooks,
			Timeouts: Timeouts{
				ManagedSlotLeaderMove: 10 * time.Millisecond,
			},
		},
		obs: hooks,
	}

	restore := cluster.setManagedSlotLeaderTestHook(func(_ *Cluster, slotID multiraft.SlotID) (multiraft.NodeID, error, bool) {
		if slotID != 1 {
			return 0, nil, false
		}
		return 2, nil, true
	})
	defer restore()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	if err := cluster.ensureLeaderMovedOffSource(ctx, 1, 1, 2); err != nil {
		t.Fatalf("ensureLeaderMovedOffSource() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("OnLeaderChange() calls = %d, want 1", calls)
	}
	if gotSlot != 1 {
		t.Fatalf("OnLeaderChange() slotID = %d, want 1", gotSlot)
	}
	if gotFrom != 1 || gotTo != 2 {
		t.Fatalf("OnLeaderChange() from=%d to=%d, want 1->2", gotFrom, gotTo)
	}
}

func TestObserverHooksOnLeaderMoveSkipsUnknownLeader(t *testing.T) {
	var (
		gotSlot uint32
		gotFrom multiraft.NodeID
		gotTo   multiraft.NodeID
		calls   int
	)
	hooks := ObserverHooks{
		OnLeaderChange: func(slotID uint32, from, to multiraft.NodeID) {
			calls++
			gotSlot = slotID
			gotFrom = from
			gotTo = to
		},
	}
	cluster := &Cluster{
		cfg: Config{
			Observer: hooks,
			Timeouts: Timeouts{
				ManagedSlotLeaderMove: 20 * time.Millisecond,
			},
		},
		obs: hooks,
	}

	attempts := 0
	restore := cluster.setManagedSlotLeaderTestHook(func(_ *Cluster, slotID multiraft.SlotID) (multiraft.NodeID, error, bool) {
		if slotID != 1 {
			return 0, nil, false
		}
		attempts++
		if attempts == 1 {
			return 0, nil, true
		}
		return 2, nil, true
	})
	defer restore()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	if err := cluster.ensureLeaderMovedOffSource(ctx, 1, 1, 2); err != nil {
		t.Fatalf("ensureLeaderMovedOffSource() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("OnLeaderChange() calls = %d, want 1", calls)
	}
	if gotSlot != 1 {
		t.Fatalf("OnLeaderChange() slotID = %d, want 1", gotSlot)
	}
	if gotFrom != 1 || gotTo != 2 {
		t.Fatalf("OnLeaderChange() from=%d to=%d, want 1->2", gotFrom, gotTo)
	}
}

type observerTestTransport struct{}

func (observerTestTransport) Send(context.Context, []multiraft.Envelope) error {
	return nil
}

type observerTestStorage struct {
	state   multiraft.BootstrapState
	applied uint64
}

func (s *observerTestStorage) InitialState(context.Context) (multiraft.BootstrapState, error) {
	return s.state, nil
}

func (s *observerTestStorage) Entries(context.Context, uint64, uint64, uint64) ([]raftpb.Entry, error) {
	return nil, nil
}

func (s *observerTestStorage) Term(context.Context, uint64) (uint64, error) {
	return 0, nil
}

func (s *observerTestStorage) FirstIndex(context.Context) (uint64, error) {
	return 0, nil
}

func (s *observerTestStorage) LastIndex(context.Context) (uint64, error) {
	return 0, nil
}

func (s *observerTestStorage) Snapshot(context.Context) (raftpb.Snapshot, error) {
	return raftpb.Snapshot{}, nil
}

func (s *observerTestStorage) Save(_ context.Context, st multiraft.PersistentState) error {
	if st.HardState != nil {
		s.state.HardState = *st.HardState
	}
	if st.Snapshot != nil {
		s.state.HardState.Commit = st.Snapshot.Metadata.Index
	}
	return nil
}

func (s *observerTestStorage) MarkApplied(_ context.Context, index uint64) error {
	s.applied = index
	s.state.AppliedIndex = index
	return nil
}

type observerTestStateMachine struct{}

func (observerTestStateMachine) Apply(_ context.Context, cmd multiraft.Command) ([]byte, error) {
	return append([]byte(nil), cmd.Data...), nil
}

func (observerTestStateMachine) Restore(context.Context, multiraft.Snapshot) error {
	return nil
}

func (observerTestStateMachine) Snapshot(context.Context) (multiraft.Snapshot, error) {
	return multiraft.Snapshot{}, nil
}

func newObserverTestCluster(t *testing.T, hooks ObserverHooks) *Cluster {
	t.Helper()

	cluster, err := NewCluster(Config{
		NodeID:       1,
		ListenAddr:   "127.0.0.1:0",
		SlotCount:    1,
		SlotReplicaN: 1,
		Nodes: []NodeConfig{
			{NodeID: 1, Addr: "127.0.0.1:0"},
		},
		Observer: hooks,
		NewStorage: func(multiraft.SlotID) (multiraft.Storage, error) {
			return &observerTestStorage{}, nil
		},
		NewStateMachine: func(multiraft.SlotID) (multiraft.StateMachine, error) {
			return observerTestStateMachine{}, nil
		},
		Timeouts: Timeouts{
			ManagedSlotLeaderWait: 200 * time.Millisecond,
		},
	})
	if err != nil {
		t.Fatalf("NewCluster() error = %v", err)
	}

	rt, err := multiraft.New(multiraft.Options{
		NodeID:       1,
		TickInterval: 10 * time.Millisecond,
		Workers:      1,
		Transport:    observerTestTransport{},
		Raft: multiraft.RaftOptions{
			ElectionTick:  3,
			HeartbeatTick: 1,
		},
	})
	if err != nil {
		t.Fatalf("multiraft.New() error = %v", err)
	}
	t.Cleanup(func() {
		_ = rt.Close()
	})

	cluster.runtime = rt
	cluster.router = NewRouter(
		NewHashSlotTable(cluster.cfg.effectiveHashSlotCount(), int(cluster.cfg.effectiveInitialSlotCount())),
		cluster.cfg.NodeID,
		rt,
	)
	return cluster
}
