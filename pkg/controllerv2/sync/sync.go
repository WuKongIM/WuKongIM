package sync

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/state"
	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/statefile"
)

var (
	// ErrClusterIDMismatch indicates that a peer returned state for another cluster.
	ErrClusterIDMismatch = errors.New("controllerv2/sync: cluster id mismatch")
	// ErrStalePayload indicates that a peer returned state older than the local snapshot.
	ErrStalePayload = errors.New("controllerv2/sync: stale payload")
	// ErrHeaderMismatch indicates that response metadata does not match the payload.
	ErrHeaderMismatch = errors.New("controllerv2/sync: response header mismatch")
	// ErrNoReachablePeer indicates that no endpoint could provide usable state.
	ErrNoReachablePeer = errors.New("controllerv2/sync: no reachable peer")
)

// GetStateRequest asks a Controller leader for a full cluster-state snapshot.
type GetStateRequest struct {
	ClusterID     string
	LocalRevision uint64
	LocalChecksum string
}

// GetStateResponse reports leader state availability or carries a full state file payload.
type GetStateResponse struct {
	NotLeader   bool
	NotReady    bool
	StaleLeader bool
	LeaderID    uint64
	NotModified bool
	Revision    uint64
	Checksum    string
	Payload     []byte
}

// Endpoint is an in-process or RPC adapter that serves ControllerV2 state.
type Endpoint interface {
	GetState(context.Context, GetStateRequest) (GetStateResponse, error)
}

// PeerPicker resolves Controller peer IDs to sync endpoints.
type PeerPicker interface {
	Endpoint(nodeID uint64) (Endpoint, bool)
	PeerIDs() []uint64
}

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

// GetState returns leader redirects, readiness signals, not-modified responses, or payloads.
func (s *Server) GetState(ctx context.Context, req GetStateRequest) (GetStateResponse, error) {
	if err := ctx.Err(); err != nil {
		return GetStateResponse{}, err
	}
	if s.clusterID != "" && req.ClusterID != "" && req.ClusterID != s.clusterID {
		return GetStateResponse{}, fmt.Errorf("%w: requested %q local %q", ErrClusterIDMismatch, req.ClusterID, s.clusterID)
	}
	leaderID := callUint64(s.leaderID)
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

// ClientConfig wires a full-file sync client for a non-controller node.
type ClientConfig struct {
	// ClusterID is the cluster identity accepted by this client.
	ClusterID string
	// Store is the local cluster-state file store.
	Store *statefile.Store
	// Peers resolves Controller voters to sync endpoints.
	Peers PeerPicker
	// LeaderID is the best known Controller leader ID.
	LeaderID uint64
	// InitialState seeds the in-memory snapshot when a caller already has one.
	InitialState *state.ClusterState
}

// Client installs validated leader cluster-state files into a local statefile.Store.
type Client struct {
	clusterID   string
	store       *statefile.Store
	peers       PeerPicker
	leaderID    uint64
	snapshot    state.ClusterState
	hasSnapshot bool
}

// NewClient creates a full-file sync client.
func NewClient(cfg ClientConfig) *Client {
	c := &Client{
		clusterID: cfg.ClusterID,
		store:     cfg.Store,
		peers:     cfg.Peers,
		leaderID:  cfg.LeaderID,
	}
	if cfg.InitialState != nil {
		c.snapshot = cfg.InitialState.Clone()
		c.hasSnapshot = true
	}
	return c
}

// LeaderID returns the best known Controller leader ID.
func (c *Client) LeaderID() uint64 {
	return c.leaderID
}

// LocalState returns the last validated state snapshot published by this client.
func (c *Client) LocalState() (state.ClusterState, bool) {
	if !c.hasSnapshot {
		return state.ClusterState{}, false
	}
	return c.snapshot.Clone(), true
}

// SyncOnce probes the known leader and peers, installing the first valid usable payload.
func (c *Client) SyncOnce(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.store == nil {
		return errors.New("controllerv2/sync: nil state store")
	}
	if c.peers == nil {
		return errors.New("controllerv2/sync: nil peer picker")
	}

	local, localValid, loadErr := c.loadLocal(ctx)
	reqRevision, reqChecksum := uint64(0), ""
	compareRevision := uint64(0)
	if localValid {
		reqRevision = local.Revision
		reqChecksum = local.Checksum
		compareRevision = local.Revision
	} else if c.hasSnapshot {
		compareRevision = c.snapshot.Revision
	}

	var lastErr error
	queue := c.candidateIDs()
	tried := make(map[uint64]bool, len(queue))
	for len(queue) > 0 {
		nodeID := queue[0]
		queue = queue[1:]
		if nodeID == 0 || tried[nodeID] {
			continue
		}
		tried[nodeID] = true
		ep, ok := c.peers.Endpoint(nodeID)
		if !ok || ep == nil {
			continue
		}
		resp, err := ep.GetState(ctx, GetStateRequest{
			ClusterID:     c.clusterID,
			LocalRevision: reqRevision,
			LocalChecksum: reqChecksum,
		})
		if err != nil {
			lastErr = err
			continue
		}
		if resp.NotLeader {
			if resp.LeaderID != 0 {
				c.leaderID = resp.LeaderID
				if !tried[resp.LeaderID] {
					queue = append([]uint64{resp.LeaderID}, queue...)
				}
			}
			continue
		}
		if resp.NotReady || resp.StaleLeader {
			if resp.LeaderID != 0 {
				c.leaderID = resp.LeaderID
			}
			continue
		}
		if resp.NotModified {
			if localValid {
				return nil
			}
			continue
		}
		if len(resp.Payload) == 0 {
			lastErr = errors.New("controllerv2/sync: empty payload")
			continue
		}
		if err := c.installPayload(ctx, resp, compareRevision); err != nil {
			return err
		}
		if resp.LeaderID != 0 {
			c.leaderID = resp.LeaderID
		} else {
			c.leaderID = nodeID
		}
		return nil
	}

	if loadErr != nil && !errors.Is(loadErr, os.ErrNotExist) {
		if lastErr != nil {
			return errors.Join(loadErr, lastErr)
		}
		return loadErr
	}
	if !localValid && !c.hasSnapshot {
		if lastErr != nil {
			return lastErr
		}
		return ErrNoReachablePeer
	}
	return nil
}

func (c *Client) loadLocal(ctx context.Context) (state.ClusterState, bool, error) {
	st, err := c.store.Load(ctx)
	if err != nil {
		return state.ClusterState{}, false, err
	}
	if c.clusterID != "" && st.ClusterID != c.clusterID {
		return state.ClusterState{}, false, fmt.Errorf("%w: local %q client %q", ErrClusterIDMismatch, st.ClusterID, c.clusterID)
	}
	c.snapshot = st.Clone()
	c.hasSnapshot = true
	return st, true, nil
}

func (c *Client) candidateIDs() []uint64 {
	ids := make([]uint64, 0, 1+len(c.peers.PeerIDs()))
	if c.leaderID != 0 {
		ids = append(ids, c.leaderID)
	}
	ids = append(ids, c.peers.PeerIDs()...)
	return ids
}

func (c *Client) installPayload(ctx context.Context, resp GetStateResponse, compareRevision uint64) error {
	decoded, err := state.Decode(resp.Payload)
	if err != nil {
		return err
	}
	if c.clusterID != "" && decoded.ClusterID != c.clusterID {
		return fmt.Errorf("%w: payload %q client %q", ErrClusterIDMismatch, decoded.ClusterID, c.clusterID)
	}
	if resp.Revision != 0 && resp.Revision != decoded.Revision {
		return fmt.Errorf("%w: revision header %d payload %d", ErrHeaderMismatch, resp.Revision, decoded.Revision)
	}
	if resp.Checksum != "" && resp.Checksum != decoded.Checksum {
		return fmt.Errorf("%w: checksum header %s payload %s", ErrHeaderMismatch, resp.Checksum, decoded.Checksum)
	}
	if decoded.Revision < compareRevision {
		return fmt.Errorf("%w: payload revision %d local revision %d", ErrStalePayload, decoded.Revision, compareRevision)
	}
	if err := c.store.Save(ctx, decoded); err != nil {
		return err
	}
	c.snapshot = decoded.Clone()
	c.hasSnapshot = true
	return nil
}
