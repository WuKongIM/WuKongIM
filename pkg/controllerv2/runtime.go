package controllerv2

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/command"
	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/fsm"
	cv2raft "github.com/WuKongIM/WuKongIM/pkg/controllerv2/raft"
	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/server"
	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/state"
	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/statefile"
	cv2sync "github.com/WuKongIM/WuKongIM/pkg/controllerv2/sync"
	"go.etcd.io/raft/v3/raftpb"
)

// Runtime hosts ControllerV2 Raft or mirror sync behind the public facade.
type Runtime struct {
	cfg RuntimeConfig

	mu    sync.RWMutex
	state ClusterState
	watch chan StateEvent

	store  *statefile.Store
	sm     *fsm.StateMachine
	raft   *cv2raft.Service
	server *server.Server

	syncServer *cv2sync.Server

	refreshCancel context.CancelFunc
	refreshWG     sync.WaitGroup
}

// NewRuntime creates a ControllerV2 runtime facade.
func NewRuntime(cfg RuntimeConfig) (*Runtime, error) {
	if cfg.Role == "" {
		cfg.Role = RuntimeRoleVoter
	}
	if cfg.TickInterval == 0 {
		cfg.TickInterval = 20 * time.Millisecond
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.NodeID == 0 || cfg.StateDir == "" || cfg.ClusterID == "" || len(cfg.Voters) == 0 {
		return nil, fmt.Errorf("controllerv2: invalid runtime config")
	}
	return &Runtime{cfg: cfg, watch: make(chan StateEvent, 16)}, nil
}

// Start starts the local ControllerV2 runtime.
func (r *Runtime) Start(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(r.cfg.StateDir, 0o755); err != nil {
		return err
	}
	r.store = statefile.New(filepath.Join(r.cfg.StateDir, "cluster-state.json"))
	switch r.cfg.Role {
	case RuntimeRoleVoter:
		return r.startVoter(ctx)
	case RuntimeRoleMirror:
		return r.startMirror(ctx)
	default:
		return fmt.Errorf("controllerv2: invalid runtime role %q", r.cfg.Role)
	}
}

func (r *Runtime) startVoter(ctx context.Context) error {
	sm, err := fsm.New(r.store)
	if err != nil {
		return err
	}
	transport := r.cfg.RaftTransport
	if transport == nil {
		transport = noopRaftTransport{}
	}
	service, err := cv2raft.NewService(cv2raft.Config{
		NodeID:         r.cfg.NodeID,
		Peers:          r.raftPeers(),
		AllowBootstrap: r.cfg.AllowBootstrap,
		RaftDir:        filepath.Join(r.cfg.StateDir, "raft"),
		StateMachine:   sm,
		Transport:      transport,
		TickInterval:   r.cfg.TickInterval,
	})
	if err != nil {
		return err
	}
	r.sm, r.raft = sm, service
	if err := service.Start(ctx); err != nil {
		r.sm, r.raft = nil, nil
		return err
	}
	srv, err := server.New(server.Config{StateSource: sm, Proposer: service, Now: r.cfg.Now})
	if err != nil {
		_ = service.Stop()
		return err
	}
	r.server = srv
	r.syncServer = r.newStateSyncServer()
	if len(r.cfg.Voters) > 1 {
		if st := sm.Snapshot(ctx); st.Revision != 0 && len(st.Slots) >= int(r.cfg.InitialSlotCount) {
			if err := r.publishFromState(ctx); err != nil {
				_ = service.Stop()
				return err
			}
		}
		r.startRefreshLoop()
		return nil
	}
	if err := r.bootstrapIfNeeded(ctx); err != nil {
		_ = service.Stop()
		return err
	}
	if err := r.publishFromState(ctx); err != nil {
		_ = service.Stop()
		return err
	}
	r.startRefreshLoop()
	return nil
}

func (r *Runtime) startMirror(ctx context.Context) error {
	client := r.cfg.SyncClient
	if client == nil {
		if r.cfg.SyncPeers == nil {
			return errors.New("controllerv2: mirror sync peers required")
		}
		client = cv2sync.NewClient(cv2sync.ClientConfig{
			ClusterID: r.cfg.ClusterID,
			Store:     r.store,
			Peers:     r.cfg.SyncPeers,
		})
	}
	srv, err := server.New(server.Config{SyncClient: syncClientAdapter{client: client}})
	if err != nil {
		return err
	}
	r.server = srv
	if err := srv.SyncOnce(ctx); err != nil {
		return err
	}
	return r.publishState(srv.LocalState())
}

func (r *Runtime) bootstrapIfNeeded(ctx context.Context) error {
	st := r.sm.Snapshot(ctx)
	if st.Revision != 0 {
		return r.runBootstrapPlanner(ctx)
	}
	if !r.cfg.AllowBootstrap {
		return errors.New("controllerv2: empty state and bootstrap disabled")
	}
	if err := r.waitLocalLeader(ctx); err != nil {
		return err
	}
	if err := r.raft.Propose(ctx, r.initCommand()); err != nil {
		return err
	}
	return r.runBootstrapPlanner(ctx)
}

func (r *Runtime) runBootstrapPlanner(ctx context.Context) error {
	for range r.cfg.InitialSlotCount {
		before := r.sm.Snapshot(ctx)
		if len(before.Slots) >= int(r.cfg.InitialSlotCount) {
			return nil
		}
		if err := r.server.TickPlanner(ctx); err != nil {
			return err
		}
		after := r.sm.Snapshot(ctx)
		if after.Revision == before.Revision {
			return nil
		}
	}
	return nil
}

func (r *Runtime) startRefreshLoop() {
	if r.refreshCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.refreshCancel = cancel
	r.refreshWG.Add(1)
	go func() {
		defer r.refreshWG.Done()
		interval := r.cfg.TickInterval * 5
		if interval <= 0 {
			interval = 100 * time.Millisecond
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = r.controlTick(ctx)
			}
		}
	}()
}

func (r *Runtime) controlTick(ctx context.Context) error {
	if r.sm == nil || r.server == nil {
		return nil
	}
	st := r.sm.Snapshot(ctx)
	if st.Revision == 0 {
		if r.cfg.AllowBootstrap && r.isLocalLeader() {
			if err := r.raft.Propose(ctx, r.initCommand()); err != nil && !errors.Is(err, cv2raft.ErrNotLeader) {
				return err
			}
		}
		return nil
	}
	if len(st.Slots) < int(r.cfg.InitialSlotCount) {
		if r.isLocalLeader() {
			if err := r.server.TickPlanner(ctx); err != nil && !errors.Is(err, cv2raft.ErrNotLeader) {
				return err
			}
		}
		return nil
	}
	return r.publishIfChanged(ctx, st.Revision)
}

func (r *Runtime) publishIfChanged(ctx context.Context, revision uint64) error {
	r.mu.RLock()
	currentRevision := r.state.Revision
	r.mu.RUnlock()
	if currentRevision == revision {
		return nil
	}
	return r.publishFromState(ctx)
}

func (r *Runtime) isLocalLeader() bool {
	return r.raft != nil && (r.raft.LeaderID() == r.cfg.NodeID || r.raft.Status().Role == cv2raft.RoleLeader)
}

func (r *Runtime) waitLocalLeader(ctx context.Context) error {
	ticker := time.NewTicker(r.cfg.TickInterval)
	defer ticker.Stop()
	for {
		if r.isLocalLeader() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (r *Runtime) initCommand() command.Command {
	controllers := make([]state.ControllerVoter, 0, len(r.cfg.Voters))
	nodes := make([]state.Node, 0, len(r.cfg.Voters))
	for _, voter := range r.cfg.Voters {
		controllers = append(controllers, state.ControllerVoter{NodeID: voter.NodeID, Addr: voter.Addr, Role: state.ControllerRoleVoter})
		nodes = append(nodes, state.Node{
			NodeID:         voter.NodeID,
			Addr:           voter.Addr,
			Roles:          []state.NodeRole{state.NodeRoleControllerVoter, state.NodeRoleData},
			JoinState:      state.NodeJoinStateActive,
			Status:         state.NodeStatusAlive,
			CapacityWeight: 1,
		})
	}
	return command.Command{
		Kind:     command.KindInitClusterState,
		IssuedAt: r.cfg.Now().UTC(),
		Init: &command.InitClusterState{
			ClusterID: r.cfg.ClusterID,
			Config: state.ClusterConfig{
				SlotCount:             r.cfg.InitialSlotCount,
				HashSlotCount:         r.cfg.HashSlotCount,
				ReplicaCount:          r.cfg.ReplicaCount,
				DefaultCapacityWeight: 1,
			},
			Controllers: controllers,
			Nodes:       nodes,
		},
	}
}

func (r *Runtime) raftPeers() []cv2raft.Peer {
	peers := make([]cv2raft.Peer, 0, len(r.cfg.Voters))
	for _, voter := range r.cfg.Voters {
		peers = append(peers, cv2raft.Peer{NodeID: voter.NodeID, Addr: voter.Addr})
	}
	return peers
}

// Stop stops local ControllerV2 resources.
func (r *Runtime) Stop(ctx context.Context) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if r.refreshCancel != nil {
		r.refreshCancel()
		r.refreshWG.Wait()
		r.refreshCancel = nil
	}
	if r.raft != nil {
		return r.raft.Stop()
	}
	return nil
}

// LocalState returns a deep copy of the latest locally visible cluster state.
func (r *Runtime) LocalState(ctx context.Context) (ClusterState, error) {
	if err := ctxErr(ctx); err != nil {
		return ClusterState{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.state.Clone(), nil
}

// LeaderID returns the best-known ControllerV2 leader ID.
func (r *Runtime) LeaderID() uint64 {
	if r.raft != nil {
		return r.raft.LeaderID()
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.state.Controllers) > 0 {
		return r.state.Controllers[0].NodeID
	}
	return 0
}

// ProbePropose verifies the hosted ControllerV2 proposal path when this runtime is a voter.
func (r *Runtime) ProbePropose(ctx context.Context) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if r == nil || r.raft == nil {
		return ErrNotStarted
	}
	return r.raft.ProbePropose(ctx)
}

// Step applies an inbound ControllerV2 Raft message to the local Raft service.
func (r *Runtime) Step(ctx context.Context, msg raftpb.Message) error {
	if r == nil || r.raft == nil {
		return nil
	}
	return r.raft.Step(ctx, msg)
}

// GetState serves ControllerV2 state sync requests from local voter state.
func (r *Runtime) GetState(ctx context.Context, req GetStateRequest) (GetStateResponse, error) {
	if r == nil || r.syncServer == nil {
		return GetStateResponse{NotReady: true}, nil
	}
	return r.syncServer.GetState(ctx, req)
}

// newStateSyncServer wires the full-file sync endpoint for a voter runtime.
func (r *Runtime) newStateSyncServer() *cv2sync.Server {
	return cv2sync.NewServer(cv2sync.ServerConfig{
		NodeID:    r.cfg.NodeID,
		ClusterID: r.cfg.ClusterID,
		LeaderID:  r.LeaderID,
		Ready: func() bool {
			if r.sm == nil {
				return false
			}
			return r.sm.Snapshot(context.Background()).Revision != 0
		},
		Snapshot: func(ctx context.Context) (state.ClusterState, error) {
			if r.sm == nil {
				return state.ClusterState{}, ErrNotStarted
			}
			return r.sm.Snapshot(ctx), nil
		},
	})
}

// Watch returns state update events.
func (r *Runtime) Watch() <-chan StateEvent { return r.watch }

func (r *Runtime) publishFromState(ctx context.Context) error {
	st := r.sm.Snapshot(ctx)
	return r.publishState(st)
}

func (r *Runtime) publishState(st ClusterState) error {
	if err := st.Validate(); err != nil {
		return err
	}
	clone := st.Clone()
	r.mu.Lock()
	r.state = clone.Clone()
	r.mu.Unlock()
	select {
	case r.watch <- StateEvent{State: clone.Clone()}:
	default:
	}
	return nil
}

type noopRaftTransport struct{}

func (noopRaftTransport) Send([]raftpb.Message) {}

type syncClientAdapter struct {
	client *cv2sync.Client
}

func (a syncClientAdapter) SyncOnce(ctx context.Context) (state.ClusterState, error) {
	if a.client == nil {
		return state.ClusterState{}, errors.New("controllerv2: sync client is required")
	}
	if err := a.client.SyncOnce(ctx); err != nil {
		return state.ClusterState{}, err
	}
	st, ok := a.client.LocalState()
	if !ok {
		return state.ClusterState{}, errors.New("controllerv2: sync produced no state")
	}
	return st, nil
}

func ctxErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}
