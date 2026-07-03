package controller

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/controller/fsm"
	cv2raft "github.com/WuKongIM/WuKongIM/pkg/controller/raft"
	"github.com/WuKongIM/WuKongIM/pkg/controller/server"
	"github.com/WuKongIM/WuKongIM/pkg/controller/statefile"
	cv2sync "github.com/WuKongIM/WuKongIM/pkg/controller/sync"
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
	syncClient *cv2sync.Client

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
		return nil, fmt.Errorf("controller: invalid runtime config")
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
		return fmt.Errorf("controller: invalid runtime role %q", r.cfg.Role)
	}
}

// Stop stops local ControllerV2 resources.
func (r *Runtime) Stop(ctx context.Context) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	r.stopRefreshLoop()
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
	if r.syncClient != nil {
		if leaderID := r.syncClient.LeaderID(); leaderID != 0 {
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

// ControllerRaftStatus returns the local ControllerV2 Raft status snapshot.
func (r *Runtime) ControllerRaftStatus(ctx context.Context) (RaftStatus, error) {
	if err := ctxErr(ctx); err != nil {
		return RaftStatus{}, err
	}
	if r == nil || r.raft == nil {
		return RaftStatus{}, ErrNotStarted
	}
	return r.raft.Status(), nil
}

// CompactControllerRaftLog forces local ControllerV2 Raft log compaction.
func (r *Runtime) CompactControllerRaftLog(ctx context.Context) (LogCompactionResult, error) {
	if err := ctxErr(ctx); err != nil {
		return LogCompactionResult{}, err
	}
	if r == nil || r.raft == nil {
		return LogCompactionResult{}, ErrNotStarted
	}
	return r.raft.CompactLog(ctx)
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
