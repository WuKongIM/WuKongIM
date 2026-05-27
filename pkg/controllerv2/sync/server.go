package sync

import (
	"context"
	"errors"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/state"
)

// ServerConfig wires a full-file sync server to local Controller state.
type ServerConfig struct {
	// NodeID is the local node identity.
	NodeID uint64
	// ClusterID is the cluster identity this server is allowed to serve.
	ClusterID string
	// LeaderID returns the current Controller leader ID.
	LeaderID func() uint64
	// Ready reports whether the leader has a serviceable state snapshot.
	Ready func() bool
	// Snapshot returns the local Controller state snapshot.
	Snapshot func(context.Context) (state.ClusterState, error)
}

// Server serves full cluster-state files from the current ready Controller leader.
type Server struct {
	nodeID    uint64
	clusterID string
	leaderID  func() uint64
	ready     func() bool
	snapshot  func(context.Context) (state.ClusterState, error)
}

// NewServer creates a full-file sync endpoint.
func NewServer(cfg ServerConfig) *Server {
	return &Server{
		nodeID:    cfg.NodeID,
		clusterID: cfg.ClusterID,
		leaderID:  cfg.LeaderID,
		ready:     cfg.Ready,
		snapshot:  cfg.Snapshot,
	}
}

// GetState serves a canonical full-file payload only when this node is the ready Controller leader.
func (s *Server) GetState(ctx context.Context, req GetStateRequest) (GetStateResponse, error) {
	if err := ctx.Err(); err != nil {
		return GetStateResponse{}, err
	}
	if s.clusterID != "" && req.ClusterID != "" && req.ClusterID != s.clusterID {
		return GetStateResponse{}, fmt.Errorf("%w: requested %q local %q", ErrClusterIDMismatch, req.ClusterID, s.clusterID)
	}
	leaderID := callUint64(s.leaderID)
	if leaderID == 0 {
		return GetStateResponse{NotReady: true}, nil
	}
	if leaderID != 0 && leaderID != s.nodeID {
		return GetStateResponse{NotLeader: true, LeaderID: leaderID}, nil
	}
	if s.ready != nil && !s.ready() {
		return GetStateResponse{NotReady: true, LeaderID: leaderID}, nil
	}
	if s.snapshot == nil {
		return GetStateResponse{}, errors.New("controllerv2/sync: nil snapshot function")
	}
	st, err := s.snapshot(ctx)
	if err != nil {
		return GetStateResponse{}, err
	}
	payload, err := state.Encode(st)
	if err != nil {
		return GetStateResponse{}, err
	}
	decoded, err := state.Decode(payload)
	if err != nil {
		return GetStateResponse{}, err
	}
	if req.LocalRevision > decoded.Revision {
		return GetStateResponse{
			StaleLeader: true,
			LeaderID:    leaderID,
			Revision:    decoded.Revision,
			Checksum:    decoded.Checksum,
		}, nil
	}
	resp := GetStateResponse{
		LeaderID: leaderID,
		Revision: decoded.Revision,
		Checksum: decoded.Checksum,
	}
	if req.LocalRevision == decoded.Revision && req.LocalChecksum == decoded.Checksum {
		resp.NotModified = true
		return resp, nil
	}
	resp.Payload = payload
	return resp, nil
}

func callUint64(fn func() uint64) uint64 {
	if fn == nil {
		return 0
	}
	return fn()
}
