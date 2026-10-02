package controller

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/controller/fsm"
	controllerraft "github.com/WuKongIM/WuKongIM/pkg/controller/raft"
	"github.com/WuKongIM/WuKongIM/pkg/controller/server"
	"github.com/WuKongIM/WuKongIM/pkg/controller/statefile"
	cv2sync "github.com/WuKongIM/WuKongIM/pkg/controller/sync"
	"go.etcd.io/raft/v3/raftpb"
)

// Runtime hosts Controller Raft or mirror sync behind the public facade.
type Runtime struct {
	cfg RuntimeConfig

	// mu protects visible state and resource publication to startup ingress.
	mu    sync.RWMutex
	state ClusterState
	watch chan StateEvent

	store *statefile.Store
	sm    *fsm.StateMachine
	// raft is published/captured under mu for inbound Step calls that can arrive
	// during startup. Other lifecycle operations retain serialized ownership.
	raft   *controllerraft.Service
	server *server.Server

	syncServer *cv2sync.Server
	syncClient *cv2sync.Client

	refreshCancel context.CancelFunc
	refreshWG     sync.WaitGroup
}

// NewRuntime creates a Controller runtime facade.
func NewRuntime(cfg RuntimeConfig) (*Runtime, error) {
	if cfg.Role == "" {
		cfg.Role = RuntimeRoleVoter
	}
	if cfg.TickInterval == 0 {
		cfg.TickInterval = controllerraft.DefaultTickInterval
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.NodeID == 0 || cfg.StateDir == "" || cfg.ClusterID == "" || len(cfg.Voters) == 0 {
		return nil, fmt.Errorf("controller: invalid runtime config")
	}
	return &Runtime{cfg: cfg, watch: make(chan StateEvent, 16)}, nil
}

// Start starts the local Controller runtime.
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
		return fmt.Errorf("controller: invalid runtime role %q", r.cfg.Role)
	}
}

// Stop stops local Controller resources.
func (r *Runtime) Stop(ctx context.Context) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	r.stopRefreshLoop()
	if service := r.raftService(); service != nil {
		return service.Stop()
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

// LeaderID returns the best-known Controller leader ID.
func (r *Runtime) LeaderID() uint64 {
	r.mu.RLock()
	service, client := r.raft, r.syncClient
	r.mu.RUnlock()
	if service != nil {
		return service.LeaderID()
	}
	if client != nil {
		if leaderID := client.LeaderID(); leaderID != 0 {
			return leaderID
		}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.state.Controllers) > 0 {
		return r.state.Controllers[0].NodeID
	}
	return 0
}

// ProbePropose verifies the hosted Controller proposal path when this runtime is a voter.
func (r *Runtime) ProbePropose(ctx context.Context) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	service := r.raftService()
	if service == nil {
		return ErrNotStarted
	}
	return service.ProbePropose(ctx)
}

// ControllerRaftStatus returns the local Controller Raft status snapshot.
func (r *Runtime) ControllerRaftStatus(ctx context.Context) (RaftStatus, error) {
	if err := ctxErr(ctx); err != nil {
		return RaftStatus{}, err
	}
	service := r.raftService()
	if service == nil {
		return RaftStatus{}, ErrNotStarted
	}
	return service.Status(), nil
}

// CompactControllerRaftLog forces local Controller Raft log compaction.
func (r *Runtime) CompactControllerRaftLog(ctx context.Context) (LogCompactionResult, error) {
	if err := ctxErr(ctx); err != nil {
		return LogCompactionResult{}, err
	}
	service := r.raftService()
	if service == nil {
		return LogCompactionResult{}, ErrNotStarted
	}
	return service.CompactLog(ctx)
}

// Step applies an inbound Controller Raft message to the local Raft service.
func (r *Runtime) Step(ctx context.Context, msg raftpb.Message) error {
	if r == nil {
		return nil
	}
	// Transport starts before the Controller. Snapshot its published service
	// without holding the state lock while the bounded Step queue waits.
	service := r.raftService()
	if service == nil {
		return nil
	}
	return service.Step(ctx, msg)
}

// GetState serves Controller state sync requests from local voter state.
func (r *Runtime) GetState(ctx context.Context, req GetStateRequest) (GetStateResponse, error) {
	if r == nil {
		return GetStateResponse{NotReady: true}, nil
	}
	r.mu.RLock()
	syncServer := r.syncServer
	r.mu.RUnlock()
	if syncServer == nil {
		return GetStateResponse{NotReady: true}, nil
	}
	return syncServer.GetState(ctx, req)
}

// Watch returns state update events.
func (r *Runtime) Watch() <-chan StateEvent { return r.watch }

func (r *Runtime) stopRefreshLoop() {
	if r.refreshCancel == nil {
		return
	}
	r.refreshCancel()
	r.refreshWG.Wait()
	r.refreshCancel = nil
}

func ctxErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

// raftService snapshots the published service before any protocol or proposal
// work. Each caller retains one generation and releases the lock before waiting.
func (r *Runtime) raftService() *controllerraft.Service {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.raft
}
