package control

import (
	"context"
	"fmt"
	"sync"
	"time"

	cv2 "github.com/WuKongIM/WuKongIM/pkg/controllerv2"
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
	RaftTransport cv2.Transport
	// SyncClient mirrors ControllerV2 state for non-voter nodes.
	SyncClient *cv2.SyncClient
	// SyncPeers resolves ControllerV2 state sync endpoints for mirror nodes.
	SyncPeers cv2.PeerPicker
	// Now returns timestamps used for ControllerV2 commands.
	Now func() time.Time
}

// Runtime adapts the root ControllerV2 runtime facade to control.Controller.
type Runtime struct {
	cfg     RuntimeConfig
	backend *cv2.Runtime

	mu       sync.RWMutex
	snapshot Snapshot
	watch    chan SnapshotEvent

	watchCancel context.CancelFunc
	watchWG     sync.WaitGroup
}

// NewRuntime creates a ControllerV2-backed control runtime.
func NewRuntime(cfg RuntimeConfig) (*Runtime, error) {
	backend, err := cv2.NewRuntime(cv2.RuntimeConfig{
		NodeID:           cfg.NodeID,
		Addr:             cfg.Addr,
		StateDir:         cfg.StateDir,
		ClusterID:        cfg.ClusterID,
		Role:             cv2.RuntimeRole(cfg.Role),
		Voters:           runtimeFacadeVoters(cfg.Voters),
		AllowBootstrap:   cfg.AllowBootstrap,
		InitialSlotCount: cfg.InitialSlotCount,
		HashSlotCount:    cfg.HashSlotCount,
		ReplicaCount:     cfg.ReplicaCount,
		TickInterval:     cfg.TickInterval,
		RaftTransport:    cfg.RaftTransport,
		SyncClient:       cfg.SyncClient,
		SyncPeers:        cfg.SyncPeers,
		Now:              cfg.Now,
	})
	if err != nil {
		return nil, err
	}
	return &Runtime{cfg: cfg, backend: backend, watch: make(chan SnapshotEvent, 16)}, nil
}

// Start starts the local ControllerV2 runtime.
func (r *Runtime) Start(ctx context.Context) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if r == nil || r.backend == nil {
		return fmt.Errorf("control runtime: controllerv2 runtime is required")
	}
	if err := r.backend.Start(ctx); err != nil {
		return err
	}
	st, err := r.backend.LocalState(ctx)
	if err != nil {
		_ = r.backend.Stop(context.Background())
		return err
	}
	if !emptyControllerV2State(st) {
		if err := r.publishState(st); err != nil {
			_ = r.backend.Stop(context.Background())
			return err
		}
	}
	r.startWatchLoop()
	return nil
}

// Stop stops local ControllerV2 resources.
func (r *Runtime) Stop(ctx context.Context) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if r.watchCancel != nil {
		r.watchCancel()
		r.watchWG.Wait()
		r.watchCancel = nil
	}
	if r == nil || r.backend == nil {
		return nil
	}
	return r.backend.Stop(ctx)
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
	if r != nil && r.backend != nil {
		return r.backend.LeaderID()
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.snapshot.ControllerID
}

// ProbePropose verifies the hosted ControllerV2 proposal path when this runtime is a voter.
func (r *Runtime) ProbePropose(ctx context.Context) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if r == nil || r.backend == nil {
		return cv2.ErrNotStarted
	}
	return r.backend.ProbePropose(ctx)
}

// Step applies an inbound ControllerV2 Raft message to the local Raft service.
func (r *Runtime) Step(ctx context.Context, msg raftpb.Message) error {
	if r == nil || r.backend == nil {
		return nil
	}
	return r.backend.Step(ctx, msg)
}

// GetState serves ControllerV2 state sync requests from the local voter state.
func (r *Runtime) GetState(ctx context.Context, req cv2.GetStateRequest) (cv2.GetStateResponse, error) {
	if r == nil || r.backend == nil {
		return cv2.GetStateResponse{NotReady: true}, nil
	}
	return r.backend.GetState(ctx, req)
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

func (r *Runtime) startWatchLoop() {
	if r.watchCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.watchCancel = cancel
	r.watchWG.Add(1)
	go func() {
		defer r.watchWG.Done()
		watch := r.backend.Watch()
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-watch:
				if !ok {
					return
				}
				_ = r.publishState(event.State)
			}
		}
	}()
}

func (r *Runtime) publishState(st cv2.ClusterState) error {
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

func runtimeFacadeVoters(voters []RuntimeVoter) []cv2.Voter {
	out := make([]cv2.Voter, 0, len(voters))
	for _, voter := range voters {
		out = append(out, cv2.Voter{NodeID: voter.NodeID, Addr: voter.Addr})
	}
	return out
}

func emptyControllerV2State(st cv2.ClusterState) bool {
	return st.SchemaVersion == 0 &&
		st.ClusterID == "" &&
		st.Revision == 0 &&
		st.AppliedRaftIndex == 0 &&
		len(st.Controllers) == 0 &&
		len(st.Nodes) == 0 &&
		len(st.Slots) == 0 &&
		st.HashSlots.SlotCount == 0 &&
		len(st.HashSlots.Ranges) == 0 &&
		len(st.Tasks) == 0
}

var _ Controller = (*Runtime)(nil)
