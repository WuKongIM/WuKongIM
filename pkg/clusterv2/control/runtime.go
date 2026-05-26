package control

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	cv2command "github.com/WuKongIM/WuKongIM/pkg/controllerv2/command"
	cv2fsm "github.com/WuKongIM/WuKongIM/pkg/controllerv2/fsm"
	cv2raft "github.com/WuKongIM/WuKongIM/pkg/controllerv2/raft"
	cv2server "github.com/WuKongIM/WuKongIM/pkg/controllerv2/server"
	cv2state "github.com/WuKongIM/WuKongIM/pkg/controllerv2/state"
	cv2statefile "github.com/WuKongIM/WuKongIM/pkg/controllerv2/statefile"
	cv2sync "github.com/WuKongIM/WuKongIM/pkg/controllerv2/sync"
	"go.etcd.io/raft/v3/raftpb"
)

// RuntimeRole declares how the local control runtime participates in ControllerV2.
type RuntimeRole string

const (
	// RuntimeRoleVoter runs ControllerV2 Raft and serves authoritative state.
	RuntimeRoleVoter RuntimeRole = "voter"
	// RuntimeRoleMirror mirrors ControllerV2 state from Controller voters.
	RuntimeRoleMirror RuntimeRole = "mirror"
)

// RuntimeVoter identifies one ControllerV2 voter endpoint.
type RuntimeVoter struct {
	// NodeID is the stable non-zero node identity of the Controller voter.
	NodeID uint64
	// Addr is the cluster RPC address used to reach this Controller voter.
	Addr string
}

// RuntimeConfig wires a ControllerV2-backed control runtime.
type RuntimeConfig struct {
	// NodeID is the local node identity.
	NodeID uint64
	// Addr is the local cluster RPC address.
	Addr string
	// StateDir stores ControllerV2 state and Raft files.
	StateDir string
	// ClusterID is the stable cluster identity.
	ClusterID string
	// Role declares voter or mirror behavior.
	Role RuntimeRole
	// Voters lists ControllerV2 Raft voters.
	Voters []RuntimeVoter
	// AllowBootstrap permits this node to initialize a new ControllerV2 Raft log.
	AllowBootstrap bool
	// InitialSlotCount is the number of physical slots created during bootstrap.
	InitialSlotCount uint32
	// HashSlotCount is the number of logical hash slots in the initial table.
	HashSlotCount uint16
	// ReplicaCount is the desired replica count for each physical slot.
	ReplicaCount uint16
	// TickInterval controls ControllerV2 Raft ticking.
	TickInterval time.Duration
	// RaftTransport sends ControllerV2 Raft messages.
	RaftTransport cv2raft.Transport
	// SyncClient mirrors ControllerV2 state for non-voter nodes.
	SyncClient *cv2sync.Client
	// Now returns timestamps used for ControllerV2 commands.
	Now func() time.Time
}

// Runtime adapts ControllerV2 state, Raft, planning, and sync to control.Controller.
type Runtime struct {
	cfg RuntimeConfig

	mu       sync.RWMutex
	snapshot Snapshot
	watch    chan SnapshotEvent

	store  *cv2statefile.Store
	sm     *cv2fsm.StateMachine
	raft   *cv2raft.Service
	server *cv2server.Server
}

// NewRuntime creates a ControllerV2-backed control runtime.
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
		return nil, fmt.Errorf("control runtime: invalid config")
	}
	return &Runtime{cfg: cfg, watch: make(chan SnapshotEvent, 16)}, nil
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
	r.store = cv2statefile.New(filepath.Join(r.cfg.StateDir, "cluster-state.json"))
	switch r.cfg.Role {
	case RuntimeRoleVoter:
		return r.startVoter(ctx)
	case RuntimeRoleMirror:
		return r.startMirror(ctx)
	default:
		return fmt.Errorf("control runtime: invalid role %q", r.cfg.Role)
	}
}

func (r *Runtime) startVoter(ctx context.Context) error {
	sm, err := cv2fsm.New(r.store)
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
	if err := service.Start(ctx); err != nil {
		return err
	}
	r.sm, r.raft = sm, service
	srv, err := cv2server.New(cv2server.Config{StateSource: sm, Proposer: service, Now: r.cfg.Now})
	if err != nil {
		_ = service.Stop()
		return err
	}
	r.server = srv
	if err := r.bootstrapIfNeeded(ctx); err != nil {
		_ = service.Stop()
		return err
	}
	return r.publishFromState(ctx)
}

func (r *Runtime) startMirror(ctx context.Context) error {
	return errors.New("control runtime: mirror mode not implemented")
}

func (r *Runtime) bootstrapIfNeeded(ctx context.Context) error {
	st := r.sm.Snapshot(ctx)
	if st.Revision != 0 {
		return r.runBootstrapPlanner(ctx)
	}
	if !r.cfg.AllowBootstrap {
		return errors.New("control runtime: empty state and bootstrap disabled")
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

func (r *Runtime) waitLocalLeader(ctx context.Context) error {
	ticker := time.NewTicker(r.cfg.TickInterval)
	defer ticker.Stop()
	for {
		if r.raft.LeaderID() == r.cfg.NodeID || r.raft.Status().Role == cv2raft.RoleLeader {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (r *Runtime) initCommand() cv2command.Command {
	controllers := make([]cv2state.ControllerVoter, 0, len(r.cfg.Voters))
	nodes := make([]cv2state.Node, 0, len(r.cfg.Voters))
	for _, voter := range r.cfg.Voters {
		controllers = append(controllers, cv2state.ControllerVoter{NodeID: voter.NodeID, Addr: voter.Addr, Role: cv2state.ControllerRoleVoter})
		nodes = append(nodes, cv2state.Node{
			NodeID:         voter.NodeID,
			Addr:           voter.Addr,
			Roles:          []cv2state.NodeRole{cv2state.NodeRoleControllerVoter, cv2state.NodeRoleData},
			JoinState:      cv2state.NodeJoinStateActive,
			Status:         cv2state.NodeStatusAlive,
			CapacityWeight: 1,
		})
	}
	return cv2command.Command{
		Kind:     cv2command.KindInitClusterState,
		IssuedAt: r.cfg.Now().UTC(),
		Init: &cv2command.InitClusterState{
			ClusterID: r.cfg.ClusterID,
			Config: cv2state.ClusterConfig{
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
	if r.raft != nil {
		return r.raft.Stop()
	}
	return nil
}

// LocalSnapshot returns the latest adapted ControllerV2 control snapshot.
func (r *Runtime) LocalSnapshot(ctx context.Context) (Snapshot, error) {
	if err := ctxErr(ctx); err != nil {
		return Snapshot{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.snapshot.Clone(), nil
}

// LeaderID returns the best-known ControllerV2 leader ID.
func (r *Runtime) LeaderID() uint64 {
	if r.raft != nil {
		return r.raft.LeaderID()
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.snapshot.ControllerID
}

// Step applies an inbound ControllerV2 Raft message to the local Raft service.
func (r *Runtime) Step(ctx context.Context, msg raftpb.Message) error {
	if r.raft == nil {
		return nil
	}
	return r.raft.Step(ctx, msg)
}

// Watch returns snapshot update events.
func (r *Runtime) Watch() <-chan SnapshotEvent { return r.watch }

// ReportNode is currently a best-effort no-op until ControllerV2 exposes report commands.
func (r *Runtime) ReportNode(ctx context.Context, report NodeReport) error {
	return ctxErr(ctx)
}

// ReportSlots is currently a best-effort no-op until ControllerV2 exposes report commands.
func (r *Runtime) ReportSlots(ctx context.Context, report SlotRuntimeReport) error {
	return ctxErr(ctx)
}

func (r *Runtime) publishFromState(ctx context.Context) error {
	st := r.sm.Snapshot(ctx)
	snap, err := SnapshotFromControllerV2(st)
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.snapshot = snap.Clone()
	r.mu.Unlock()
	select {
	case r.watch <- SnapshotEvent{Snapshot: snap.Clone()}:
	default:
	}
	return nil
}

type noopRaftTransport struct{}

func (noopRaftTransport) Send([]raftpb.Message) {}

var _ Controller = (*Runtime)(nil)
