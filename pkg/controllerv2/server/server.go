package server

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/command"
	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/planner"
	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/state"
)

// ErrStateSourceRequired indicates that a command-producing planner tick lacks authoritative state.
var ErrStateSourceRequired = errors.New("controllerv2/server: state source is required for planner command decisions")

// Proposer appends ControllerV2 commands to the current Controller Raft leader.
type Proposer interface {
	// Propose submits cmd to Controller Raft and waits for local apply semantics.
	Propose(context.Context, command.Command) error
	// LeaderID returns the best known Controller Raft leader ID.
	LeaderID() uint64
}

// StateSource provides authoritative ControllerV2 snapshots after local apply.
type StateSource interface {
	// Snapshot returns the latest locally visible ControllerV2 cluster state.
	Snapshot(context.Context) state.ClusterState
}

// SyncClient is a facade adapter contract that returns the state installed by sync.
//
// Real sync clients that expose SyncOnce(ctx) error can be wrapped by calling
// SyncOnce and then returning their LocalState snapshot.
type SyncClient interface {
	// SyncOnce fetches one leader snapshot and returns the state that was installed locally.
	SyncOnce(context.Context) (state.ClusterState, error)
}

// Config wires the thin ControllerV2 server facade.
type Config struct {
	// InitialState seeds the facade's local in-memory state snapshot.
	InitialState state.ClusterState
	// Planner produces durable commands from the local state snapshot; nil uses the bootstrap planner.
	Planner planner.Planner
	// Proposer submits planner commands to Controller Raft.
	Proposer Proposer
	// StateSource reads authoritative local state, for example from the ControllerV2 FSM.
	StateSource StateSource
	// SyncClient fetches and persists leader snapshots for non-controller nodes.
	SyncClient SyncClient
	// Now returns the timestamp used for planner views; nil uses time.Now.
	Now func() time.Time
}

// Server is a thin facade that connects local state, planning, Raft proposal, and sync.
type Server struct {
	tickMu sync.Mutex

	mu         sync.RWMutex
	localState state.ClusterState

	planner     planner.Planner
	proposer    Proposer
	stateSource StateSource
	syncClient  SyncClient
	now         func() time.Time
}

// New creates a ControllerV2 server facade from cfg.
func New(cfg Config) (*Server, error) {
	pl := cfg.Planner
	if pl == nil {
		pl = planner.NewBootstrapPlanner()
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Server{
		localState:  cfg.InitialState.Clone(),
		planner:     pl,
		proposer:    cfg.Proposer,
		stateSource: cfg.StateSource,
		syncClient:  cfg.SyncClient,
		now:         now,
	}, nil
}

// TickPlanner runs one planner decision against LocalState and proposes command decisions.
func (s *Server) TickPlanner(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	s.tickMu.Lock()
	defer s.tickMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	decision, err := s.planner.Next(ctx, planner.View{
		State: s.localStateSnapshot(ctx),
		Now:   s.now().UTC(),
	})
	if err != nil {
		return err
	}
	if decision.Kind != planner.DecisionKindCommand {
		return nil
	}
	if s.stateSource == nil {
		return ErrStateSourceRequired
	}
	if s.proposer == nil {
		return errors.New("controllerv2/server: proposer is required")
	}
	if err := s.proposer.Propose(ctx, decision.Command); err != nil {
		return err
	}
	s.refreshFromStateSource(ctx)
	return nil
}

// LocalState returns a deep copy of the latest local ControllerV2 state snapshot.
func (s *Server) LocalState() state.ClusterState {
	return s.localStateSnapshot(context.Background())
}

func (s *Server) localStateSnapshot(ctx context.Context) state.ClusterState {
	if s.stateSource != nil {
		return s.refreshFromStateSource(ctx)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.localState.Clone()
}

func (s *Server) refreshFromStateSource(ctx context.Context) state.ClusterState {
	if s.stateSource == nil {
		return s.localStateSnapshot(ctx)
	}
	st := s.stateSource.Snapshot(ctx).Clone()
	s.mu.Lock()
	s.localState = st.Clone()
	s.mu.Unlock()
	return st
}

// SyncOnce delegates to the configured sync client and publishes the returned state locally.
func (s *Server) SyncOnce(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.syncClient == nil {
		return errors.New("controllerv2/server: sync client is required")
	}
	st, err := s.syncClient.SyncOnce(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.localState = st.Clone()
	s.mu.Unlock()
	return nil
}
