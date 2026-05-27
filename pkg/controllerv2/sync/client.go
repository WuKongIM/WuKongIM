package sync

import (
	"context"
	"errors"
	"fmt"
	"os"
	stdsync "sync"

	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/state"
	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/statefile"
)

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
	opMu        stdsync.Mutex
	mu          stdsync.RWMutex
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
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.leaderID
}

// LocalState returns the last validated state snapshot published by this client.
func (c *Client) LocalState() (state.ClusterState, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if !c.hasSnapshot {
		return state.ClusterState{}, false
	}
	return c.snapshot.Clone(), true
}

// SyncOnce prefers the known leader, follows redirects, and installs the first validated non-stale state file.
func (c *Client) SyncOnce(ctx context.Context) error {
	c.opMu.Lock()
	defer c.opMu.Unlock()

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
	if errors.Is(loadErr, ErrClusterIDMismatch) {
		return loadErr
	}
	reqRevision, reqChecksum := uint64(0), ""
	compareRevision, compareChecksum := uint64(0), ""
	if localValid {
		reqRevision = local.Revision
		reqChecksum = local.Checksum
		compareRevision = local.Revision
		compareChecksum = local.Checksum
	} else if snapshot, ok := c.LocalState(); ok {
		compareRevision = snapshot.Revision
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
				c.setLeaderID(resp.LeaderID)
				if !tried[resp.LeaderID] {
					queue = append([]uint64{resp.LeaderID}, queue...)
				}
			}
			continue
		}
		if resp.NotReady || resp.StaleLeader {
			if resp.LeaderID != 0 {
				c.setLeaderID(resp.LeaderID)
			}
			continue
		}
		if resp.NotModified {
			// Accept NotModified only when the local file metadata exactly matches
			// the leader header; otherwise continue looking for a full payload.
			if localValid && resp.Revision == local.Revision && resp.Checksum == local.Checksum {
				return nil
			}
			lastErr = fmt.Errorf("%w: invalid not-modified header", ErrHeaderMismatch)
			continue
		}
		if len(resp.Payload) == 0 {
			lastErr = errors.New("controllerv2/sync: empty payload")
			continue
		}
		if err := c.installPayload(ctx, resp, compareRevision, compareChecksum); err != nil {
			if errors.Is(err, ErrStalePayload) {
				lastErr = err
				continue
			}
			return err
		}
		if resp.LeaderID != 0 {
			c.setLeaderID(resp.LeaderID)
		} else {
			c.setLeaderID(nodeID)
		}
		return nil
	}

	if loadErr != nil && !errors.Is(loadErr, os.ErrNotExist) {
		if lastErr != nil {
			return errors.Join(loadErr, lastErr)
		}
		return loadErr
	}
	if !localValid && !c.hasLocalSnapshot() {
		if lastErr != nil {
			return lastErr
		}
		return ErrNoReachablePeer
	}
	if lastErr != nil {
		return lastErr
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
	c.publishSnapshot(st)
	return st, true, nil
}

func (c *Client) candidateIDs() []uint64 {
	ids := make([]uint64, 0, 1+len(c.peers.PeerIDs()))
	if leaderID := c.LeaderID(); leaderID != 0 {
		ids = append(ids, leaderID)
	}
	ids = append(ids, c.peers.PeerIDs()...)
	return ids
}

func (c *Client) installPayload(ctx context.Context, resp GetStateResponse, compareRevision uint64, compareChecksum string) error {
	decoded, err := state.Decode(resp.Payload)
	if err != nil {
		return err
	}
	if c.clusterID != "" && decoded.ClusterID != c.clusterID {
		return fmt.Errorf("%w: payload %q client %q", ErrClusterIDMismatch, decoded.ClusterID, c.clusterID)
	}
	if resp.Revision != decoded.Revision {
		return fmt.Errorf("%w: revision header %d payload %d", ErrHeaderMismatch, resp.Revision, decoded.Revision)
	}
	if resp.Checksum != decoded.Checksum {
		return fmt.Errorf("%w: checksum header %s payload %s", ErrHeaderMismatch, resp.Checksum, decoded.Checksum)
	}
	if decoded.Revision < compareRevision {
		return fmt.Errorf("%w: payload revision %d local revision %d", ErrStalePayload, decoded.Revision, compareRevision)
	}
	if decoded.Revision == compareRevision && compareChecksum != "" && decoded.Checksum == compareChecksum {
		return nil
	}
	if err := c.store.Save(ctx, decoded); err != nil {
		return err
	}
	c.publishSnapshot(decoded)
	return nil
}

func (c *Client) publishSnapshot(st state.ClusterState) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.snapshot = st.Clone()
	c.hasSnapshot = true
}

func (c *Client) setLeaderID(leaderID uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.leaderID = leaderID
}

func (c *Client) hasLocalSnapshot() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.hasSnapshot
}
