package app

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	accessnode "github.com/WuKongIM/WuKongIM/internalv2/access/node"
	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

const (
	defaultSeedJoinLoopInterval = time.Second
	maxSeedJoinLoopInterval     = 5 * time.Second
)

type seedJoinLoopConfig struct {
	// NodeID is the stable identity of the joining node.
	NodeID uint64
	// AdvertiseAddr is the stable cluster RPC address stored in membership.
	AdvertiseAddr string
	// ClusterID is the cluster identity sent to seed nodes.
	ClusterID string
	// JoinToken authenticates the pre-membership join request.
	JoinToken string
	// Seeds lists seed node IDs in any order; the loop calls them in stable order.
	Seeds []uint64
	// SeedAddrs lists configured seed addresses used to resolve IDs from the control mirror.
	SeedAddrs []string
	// CapacityWeight is the planner placement weight requested by this node.
	CapacityWeight uint32
	// Interval controls the base retry delay; zero uses the production default.
	Interval time.Duration
}

type seedJoinClient interface {
	JoinNode(context.Context, uint64, accessnode.NodeJoinRequest) (managementusecase.JoinNodeResponse, error)
}

type seedJoinSnapshotReader interface {
	LocalControlSnapshot(context.Context) (control.Snapshot, error)
}

type seedJoinLoop struct {
	cfg       seedJoinLoopConfig
	client    seedJoinClient
	snapshots seedJoinSnapshotReader
	logger    wklog.Logger

	mu     sync.Mutex
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func newSeedJoinLoop(cfg seedJoinLoopConfig, client seedJoinClient, snapshots seedJoinSnapshotReader, logger wklog.Logger) *seedJoinLoop {
	seeds := append([]uint64(nil), cfg.Seeds...)
	sort.Slice(seeds, func(i, j int) bool { return seeds[i] < seeds[j] })
	cfg.Seeds = seeds
	if cfg.Interval <= 0 {
		cfg.Interval = defaultSeedJoinLoopInterval
	}
	if logger == nil {
		logger = wklog.NewNop()
	}
	return &seedJoinLoop{
		cfg:       cfg,
		client:    client,
		snapshots: snapshots,
		logger:    logger,
	}
}

// Start launches the background seed join retry loop.
func (l *seedJoinLoop) Start(ctx context.Context) error {
	if l == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	l.mu.Lock()
	if l.cancel != nil {
		l.mu.Unlock()
		return nil
	}
	runCtx, cancel := context.WithCancel(ctx)
	l.cancel = cancel
	l.wg.Add(1)
	go l.run(runCtx)
	l.mu.Unlock()
	return nil
}

// Stop cancels the seed join retry loop and waits for it to exit.
func (l *seedJoinLoop) Stop(context.Context) error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	cancel := l.cancel
	l.cancel = nil
	l.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	l.wg.Wait()
	return nil
}

func (l *seedJoinLoop) run(ctx context.Context) {
	defer l.wg.Done()
	delay := l.cfg.Interval
	for {
		if l.joinObserved(ctx) {
			return
		}
		hadError := false
		attempted := false
		for _, seedNodeID := range l.seedIDs(ctx) {
			if seedNodeID == 0 || seedNodeID == l.cfg.NodeID {
				continue
			}
			if l.joinObserved(ctx) {
				return
			}
			attempted = true
			if l.client == nil {
				return
			}
			if _, err := l.client.JoinNode(ctx, seedNodeID, l.joinRequest()); err != nil {
				hadError = true
				l.logger.Warn("seed join attempt failed",
					wklog.Event("internalv2.app.seed_join_failed"),
					wklog.Uint64("seedNodeID", seedNodeID),
					wklog.Uint64("nodeID", l.cfg.NodeID),
					wklog.Error(err),
				)
			}
			if l.joinObserved(ctx) {
				return
			}
		}
		if !attempted {
			if !waitSeedJoinDelay(ctx, delay) {
				return
			}
			continue
		}
		if !waitSeedJoinDelay(ctx, delay) {
			return
		}
		if hadError && delay < maxSeedJoinLoopInterval {
			delay *= 2
			if delay > maxSeedJoinLoopInterval {
				delay = maxSeedJoinLoopInterval
			}
		} else if !hadError {
			delay = l.cfg.Interval
		}
	}
}

func (l *seedJoinLoop) seedIDs(ctx context.Context) []uint64 {
	if len(l.cfg.Seeds) > 0 {
		return append([]uint64(nil), l.cfg.Seeds...)
	}
	if l.snapshots == nil || len(l.cfg.SeedAddrs) == 0 {
		return nil
	}
	snapshot, err := l.snapshots.LocalControlSnapshot(ctx)
	if err != nil {
		return nil
	}
	return seedJoinSeedIDs(snapshot, l.cfg.SeedAddrs, l.cfg.NodeID)
}

func (l *seedJoinLoop) joinRequest() accessnode.NodeJoinRequest {
	return accessnode.NodeJoinRequest{
		NodeID:         l.cfg.NodeID,
		AdvertiseAddr:  l.cfg.AdvertiseAddr,
		ClusterID:      l.cfg.ClusterID,
		JoinToken:      l.cfg.JoinToken,
		CapacityWeight: l.cfg.CapacityWeight,
	}
}

func (l *seedJoinLoop) joinObserved(ctx context.Context) bool {
	if l == nil || l.snapshots == nil || l.cfg.NodeID == 0 {
		return false
	}
	snapshot, err := l.snapshots.LocalControlSnapshot(ctx)
	if err != nil {
		l.logger.Debug("seed join snapshot read failed",
			wklog.Event("internalv2.app.seed_join_snapshot_failed"),
			wklog.Uint64("nodeID", l.cfg.NodeID),
			wklog.Error(err),
		)
		return false
	}
	for _, node := range snapshot.Nodes {
		if node.NodeID != l.cfg.NodeID {
			continue
		}
		switch node.JoinState {
		case "", control.NodeJoinStateJoining, control.NodeJoinStateActive:
			return true
		default:
			return false
		}
	}
	return false
}

func seedJoinSeedIDs(snapshot control.Snapshot, seedAddrs []string, localNodeID uint64) []uint64 {
	allowed := make(map[string]struct{}, len(seedAddrs))
	for _, addr := range seedAddrs {
		if trimmed := strings.TrimSpace(addr); trimmed != "" {
			allowed[trimmed] = struct{}{}
		}
	}
	if len(allowed) == 0 {
		return nil
	}
	ids := make([]uint64, 0, len(seedAddrs))
	for _, node := range snapshot.Nodes {
		if node.NodeID == 0 || node.NodeID == localNodeID {
			continue
		}
		if _, ok := allowed[strings.TrimSpace(node.Addr)]; !ok {
			continue
		}
		switch node.JoinState {
		case "", control.NodeJoinStateActive:
			ids = append(ids, node.NodeID)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// NodeReadiness reports coarse local startup readiness for seed lifecycle RPC probes.
func (a *App) NodeReadiness(ctx context.Context, req accessnode.NodeReadinessRequest) (accessnode.NodeReadinessResponse, error) {
	if a == nil {
		return accessnode.NodeReadinessResponse{NodeID: req.NodeID, ClusterID: req.ClusterID, LastError: "app not configured"}, nil
	}
	nodeID := a.cfg.Cluster.NodeID
	if nodeID == 0 {
		nodeID = a.cfg.NodeID
	}
	clusterID := strings.TrimSpace(a.cfg.Cluster.Control.ClusterID)
	resp := accessnode.NodeReadinessResponse{
		NodeID:          nodeID,
		ClusterID:       clusterID,
		Reachable:       true,
		MirrorClusterID: clusterID,
	}
	a.lifecycleMu.Lock()
	resp.TransportReady = a.clusterStarted
	resp.RuntimeReady = a.started
	a.lifecycleMu.Unlock()
	if snapshots, ok := a.cluster.(seedJoinSnapshotReader); ok {
		snapshot, err := snapshots.LocalControlSnapshot(ctx)
		if err != nil {
			resp.LastError = err.Error()
		} else {
			resp.MirrorRevision = snapshot.Revision
			resp.ControlReady = len(snapshot.Nodes) > 0
		}
	}
	resp.Ready = resp.Reachable && resp.TransportReady && resp.ControlReady && resp.RuntimeReady
	return resp, nil
}

func waitSeedJoinDelay(ctx context.Context, delay time.Duration) bool {
	if delay <= 0 {
		delay = defaultSeedJoinLoopInterval
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
