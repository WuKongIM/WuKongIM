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

// Proposer appends ControllerV2 commands to the current Controller Raft leader.
type Proposer interface {
	// Propose submits cmd to Controller Raft and waits for local apply semantics.
	Propose(context.Context, command.Command) error
	// LeaderID returns the best known Controller Raft leader ID.
	LeaderID() uint64
}

// SyncClient refreshes local ControllerV2 state from a Controller leader.
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
	// SyncClient fetches and persists leader snapshots for non-controller nodes.
	SyncClient SyncClient
	// Now returns the timestamp used for planner views; nil uses time.Now.
	Now func() time.Time
}

// Server is a thin facade that connects local state, planning, Raft proposal, and sync.
type Server struct {
	mu         sync.RWMutex
	localState state.ClusterState

	planner    planner.Planner
	proposer   Proposer
	syncClient SyncClient
	now        func() time.Time
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
		localState: cfg.InitialState.Clone(),
		planner:    pl,
		proposer:   cfg.Proposer,
		syncClient: cfg.SyncClient,
		now:        now,
	}, nil
}

// TickPlanner runs one planner decision against LocalState and proposes command decisions.
func (s *Server) TickPlanner(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	decision, err := s.planner.Next(ctx, planner.View{
		State: s.LocalState(),
		Now:   s.now().UTC(),
	})
	if err != nil {
		return err
	}
	if decision.Kind != planner.DecisionKindCommand {
		return nil
	}
	if s.proposer == nil {
		return errors.New("controllerv2/server: proposer is required")
	}
	return s.proposer.Propose(ctx, decision.Command)
}

// LocalState returns a deep copy of the latest local ControllerV2 state snapshot.
func (s *Server) LocalState() state.ClusterState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.localState.Clone()
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
