package cluster

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	slotcontroller "github.com/WuKongIM/WuKongIM/pkg/controller/plane"
	controllerraft "github.com/WuKongIM/WuKongIM/pkg/controller/raft"
	raftstorage "github.com/WuKongIM/WuKongIM/pkg/raftlog"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
)

type controllerHost struct {
	meta         *controllermeta.Store
	raftDB       *raftstorage.DB
	sm           *slotcontroller.StateMachine
	service      *controllerraft.Service
	obs          ObserverHooks
	observations *observationCache
	// syncState tracks leader-local observation revisions and delta-ready snapshots.
	syncState *observationSyncState
	// hintClient sends best-effort observation wakeups to followers.
	hintClient      *transport.Client
	hintPeers       []multiraft.NodeID
	healthScheduler *nodeHealthScheduler
	localNode       multiraft.NodeID

	leaderTerm   atomic.Uint64
	plannerDirty atomic.Bool

	hashSlotMu              sync.RWMutex
	hashSlotTable           *HashSlotTable
	hashSlotReloadMu        sync.Mutex
	hashSlotReloadPending   bool
	hashSlotReloadScheduled bool
	hashSlotReloadSeq       uint64

	warmupHooksMu       sync.RWMutex
	loadHashSlotTableFn func(context.Context) (*HashSlotTable, error)
	loadNodeMirrorFn    func(context.Context) ([]controllermeta.ClusterNode, error)
	// metadataSnapshotState caches controller meta (nodes/assignments/tasks) as a leader-local fast path.
	metadataSnapshotState controllerMetadataSnapshotState
	// bgCtx is canceled on Stop() to stop best-effort background warmups.
	bgCtx    context.Context
	bgCancel context.CancelFunc
	// metadataReloadTimeout bounds Pebble-backed metadata snapshot reload I/O in background workers.
	metadataReloadTimeout time.Duration

	warmupMu         sync.RWMutex
	warmupLeaderID   multiraft.NodeID
	warmupGeneration uint64
	warmupReady      bool
	warmupFullSyncs  map[uint64]struct{}

	plannerWakeMu       sync.Mutex
	plannerWakeTimer    *time.Timer
	plannerWakeDebounce time.Duration
	// plannerWakeCh carries debounced best-effort planner wake signals to the cluster loop.
	plannerWakeCh chan struct{}
}

func newControllerHost(cfg Config, layer *transportLayer) (*controllerHost, error) {
	meta, err := controllermeta.Open(cfg.ControllerMetaPath)
	if err != nil {
		return nil, fmt.Errorf("open controller meta: %w", err)
	}
	logDB, err := raftstorage.Open(cfg.ControllerRaftPath)
	if err != nil {
		_ = meta.Close()
		return nil, fmt.Errorf("open controller raft: %w", err)
	}

	peers := cfg.DerivedControllerNodes()
	controllerPeers := make([]controllerraft.Peer, 0, len(peers))
	for _, peer := range peers {
		controllerPeers = append(controllerPeers, controllerraft.Peer{
			NodeID: uint64(peer.NodeID),
			Addr:   peer.Addr,
		})
	}

	sm := slotcontroller.NewStateMachine(meta, slotcontroller.StateMachineConfig{})
	host := &controllerHost{
		meta:            meta,
		raftDB:          logDB,
		sm:              sm,
		obs:             cfg.Observer,
		observations:    newObservationCache(),
		syncState:       newObservationSyncState(),
		hintClient:      layer.fwdClient,
		localNode:       cfg.NodeID,
		warmupFullSyncs: make(map[uint64]struct{}),
		plannerWakeCh:   make(chan struct{}, 1),
	}
	host.hintPeers = make([]multiraft.NodeID, 0, len(cfg.Nodes))
	for _, node := range cfg.Nodes {
		if node.NodeID == 0 || node.NodeID == cfg.NodeID {
			continue
		}
		host.hintPeers = append(host.hintPeers, node.NodeID)
	}
	timeouts := cfg.Timeouts
	timeouts.applyDefaults()
	host.metadataReloadTimeout = timeouts.ControllerRequest
	host.plannerWakeDebounce = timeouts.PlannerWakeDebounce
	host.bgCtx, host.bgCancel = context.WithCancel(context.Background())
	host.loadHashSlotTableFn = func(ctx context.Context) (*HashSlotTable, error) { return host.meta.LoadHashSlotTable(ctx) }
	host.loadNodeMirrorFn = func(ctx context.Context) ([]controllermeta.ClusterNode, error) { return host.meta.ListNodes(ctx) }
	host.metadataSnapshotState.onLoaded = func(snapshot controllerMetadataSnapshot) {
		if host.syncState != nil {
			before := host.syncState.currentRevisions()
			host.syncState.replaceMetadataSnapshot(snapshot)
			host.emitObservationHintSince(before)
		}
	}
	host.observations.runtimeViewTTL = 3 * timeouts.ObservationRuntimeFullSyncInterval
	host.healthScheduler = newNodeHealthScheduler(nodeHealthSchedulerConfig{
		suspectTimeout: 3 * time.Second,
		deadTimeout:    10 * time.Second,
		loadNodes:      host.meta.ListNodes,
		loadNode:       host.meta.GetNode,
	})
	service := controllerraft.NewService(controllerraft.Config{
		NodeID:         uint64(cfg.NodeID),
		Peers:          controllerPeers,
		AllowBootstrap: true,
		LogDB:          logDB,
		StateMachine:   sm,
		Server:         layer.server,
		RPCMux:         layer.rpcMux,
		Pool:           layer.raftPool,
		Logger:         defaultLogger(cfg.Logger).Named("controller"),
		OnLeaderChange: func(from, to uint64) {
			host.handleLeaderChange(multiraft.NodeID(from), multiraft.NodeID(to))
		},
		OnCommittedCommand: func(cmd slotcontroller.Command) {
			host.handleCommittedCommand(cmd)
		},
	})
	host.service = service
	host.healthScheduler.cfg.propose = func(ctx context.Context, cmd slotcontroller.Command) error {
		return host.service.Propose(ctx, cmd)
	}
	return host, nil
}

func (h *controllerHost) Start(ctx context.Context) error {
	if h == nil || h.service == nil {
		return nil
	}
	return h.service.Start(ctx)
}

func (h *controllerHost) Stop() {
	if h == nil {
		return
	}
	if h.bgCancel != nil {
		h.bgCancel()
	}
	h.plannerWakeMu.Lock()
	if h.plannerWakeTimer != nil {
		h.plannerWakeTimer.Stop()
		h.plannerWakeTimer = nil
	}
	h.plannerWakeMu.Unlock()
	if h.healthScheduler != nil {
		h.healthScheduler.reset()
	}
	if h.service != nil {
		_ = h.service.Stop()
	}
	if h.raftDB != nil {
		_ = h.raftDB.Close()
	}
	if h.meta != nil {
		_ = h.meta.Close()
	}
}

func (h *controllerHost) IsLeader(local multiraft.NodeID) bool {
	return h != nil && h.LeaderID() == local
}

func (h *controllerHost) LeaderID() multiraft.NodeID {
	if h == nil || h.service == nil {
		return 0
	}
	return multiraft.NodeID(h.service.LeaderID())
}

func (h *controllerHost) applyObservation(report slotcontroller.AgentReport) {
	if h == nil || h.observations == nil {
		return
	}
	h.syncLeaderWarmupState()
	h.observations.applyNodeReport(report)
	if h.healthScheduler != nil {
		h.healthScheduler.observe(nodeObservation{
			NodeID:               report.NodeID,
			Addr:                 report.Addr,
			ObservedAt:           report.ObservedAt,
			CapacityWeight:       report.CapacityWeight,
			HashSlotTableVersion: report.HashSlotTableVersion,
		})
	}
}

func (h *controllerHost) applyRuntimeReport(report runtimeObservationReport) {
	if h == nil || h.observations == nil {
		return
	}
	h.syncLeaderWarmupState()
	h.observations.applyRuntimeReport(report)
	if h.syncState != nil {
		before := h.syncState.currentRevisions()
		h.syncState.replaceRuntimeViews(h.observations.snapshotRuntimeViews())
		h.emitObservationHintSince(before)
	}
	if report.FullSync {
		h.markWarmupFullSync(report.NodeID)
		h.refreshWarmupReady()
	}
	h.markPlannerDirty()
}

func (h *controllerHost) snapshotObservations() observationSnapshot {
	if h == nil || h.observations == nil {
		return observationSnapshot{}
	}
	return h.observations.snapshot()
}

// leaderGeneration returns the current local leader generation, if this host is the leader.
func (h *controllerHost) leaderGeneration() uint64 {
	if h == nil {
		return 0
	}

	h.warmupMu.RLock()
	defer h.warmupMu.RUnlock()

	if h.warmupLeaderID != h.localNode {
		return 0
	}
	return h.warmupGeneration
}

// buildObservationDelta serves a revision-aware observation snapshot for the current leader generation.
func (h *controllerHost) buildObservationDelta(req observationDeltaRequest) observationDeltaResponse {
	if h == nil || h.syncState == nil {
		return observationDeltaResponse{}
	}

	currentLeaderID := uint64(h.LeaderID())
	currentGeneration := h.leaderGeneration()
	if req.LeaderID != currentLeaderID || req.LeaderGeneration != currentGeneration {
		req.ForceFullSync = true
	}

	resp := h.syncState.buildDelta(req)
	resp.LeaderID = currentLeaderID
	resp.LeaderGeneration = currentGeneration
	return resp
}

func (h *controllerHost) emitObservationHintSince(before observationRevisions) {
	if h == nil || h.syncState == nil || h.hintClient == nil {
		return
	}

	currentLeaderID := h.LeaderID()
	currentGeneration := h.leaderGeneration()
	if currentLeaderID != h.localNode || currentGeneration == 0 {
		return
	}
	after := h.syncState.currentRevisions()
	if after == before {
		return
	}

	delta := h.syncState.buildDelta(observationDeltaRequest{Revisions: before})
	h.emitObservationHint(hintFromObservationDelta(uint64(h.localNode), currentGeneration, delta))
}

func (h *controllerHost) emitObservationHint(hint observationHint) {
	if h == nil || h.hintClient == nil || len(h.hintPeers) == 0 {
		return
	}

	body := encodeObservationHint(hint)
	for _, peerID := range h.hintPeers {
		if peerID == 0 || peerID == h.localNode {
			continue
		}
		_ = h.hintClient.Send(uint64(peerID), 0, msgTypeObservationHint, body)
	}
}

func (h *controllerHost) hashSlotTableSnapshot() (*HashSlotTable, bool) {
	if h == nil {
		return nil, false
	}

	h.hashSlotMu.RLock()
	defer h.hashSlotMu.RUnlock()

	if h.hashSlotTable == nil {
		return nil, false
	}
	return h.hashSlotTable, true
}

func (h *controllerHost) storeHashSlotTableSnapshot(table *HashSlotTable) {
	if h == nil {
		return
	}

	h.hashSlotMu.Lock()
	defer h.hashSlotMu.Unlock()

	if table == nil {
		h.hashSlotTable = nil
		return
	}
	h.hashSlotTable = table.Clone()
}

func (h *controllerHost) clearHashSlotTableSnapshot() {
	if h == nil {
		return
	}

	h.hashSlotMu.Lock()
	defer h.hashSlotMu.Unlock()
	h.hashSlotTable = nil
}

func (h *controllerHost) loadHashSlotTable(ctx context.Context) (*HashSlotTable, error) {
	if h == nil {
		return nil, nil
	}
	h.warmupHooksMu.RLock()
	fn := h.loadHashSlotTableFn
	h.warmupHooksMu.RUnlock()
	if fn == nil {
		return nil, nil
	}
	return fn(ctx)
}

func (h *controllerHost) loadNodeMirror(ctx context.Context) ([]controllermeta.ClusterNode, error) {
	if h == nil {
		return nil, nil
	}
	h.warmupHooksMu.RLock()
	fn := h.loadNodeMirrorFn
	h.warmupHooksMu.RUnlock()
	if fn == nil {
		return nil, nil
	}
	return fn(ctx)
}

func (h *controllerHost) reloadHashSlotTableSnapshot(ctx context.Context) error {
	if h == nil || h.meta == nil {
		return nil
	}
	table, err := h.meta.LoadHashSlotTable(ctx)
	if err != nil {
		h.clearHashSlotTableSnapshot()
		return err
	}
	h.storeHashSlotTableSnapshot(table)
	return nil
}

func (h *controllerHost) metadataSnapshot() (controllerMetadataSnapshot, bool) {
	if h == nil {
		return controllerMetadataSnapshot{}, false
	}
	return h.metadataSnapshotState.snapshotIfReadyClean()
}

func (h *controllerHost) reloadMetadataSnapshot(ctx context.Context) error {
	if h == nil {
		return nil
	}
	return h.metadataSnapshotState.reloadIfLeader(ctx, h.meta, h.localNode)
}

func (h *controllerHost) warmupComplete() bool {
	if h == nil {
		return false
	}
	h.syncLeaderWarmupState()

	h.warmupMu.RLock()
	defer h.warmupMu.RUnlock()
	return h.warmupLeaderID == h.localNode && h.warmupReady
}

func (h *controllerHost) plannerSnapshot() (observationSnapshot, bool) {
	if h == nil {
		return observationSnapshot{}, false
	}
	if !h.warmupComplete() {
		return observationSnapshot{}, false
	}
	return h.snapshotObservations(), true
}

func (h *controllerHost) syncLeaderWarmupState() {
	if h == nil {
		return
	}

	leaderID := h.LeaderID()

	h.warmupMu.Lock()
	defer h.warmupMu.Unlock()

	if leaderID != h.warmupLeaderID {
		h.warmupLeaderID = leaderID
		h.warmupReady = false
		h.warmupFullSyncs = make(map[uint64]struct{})
		if leaderID == h.localNode {
			h.warmupGeneration++
		}
		return
	}
	if leaderID != h.localNode {
		h.warmupReady = false
		h.warmupFullSyncs = make(map[uint64]struct{})
	}
}

func (h *controllerHost) markWarmupFullSync(nodeID uint64) {
	if h == nil || nodeID == 0 {
		return
	}

	h.warmupMu.Lock()
	defer h.warmupMu.Unlock()

	if h.warmupLeaderID != h.localNode {
		return
	}
	if h.warmupFullSyncs == nil {
		h.warmupFullSyncs = make(map[uint64]struct{})
	}
	h.warmupFullSyncs[nodeID] = struct{}{}
}

func (h *controllerHost) refreshWarmupReady() {
	if h == nil || h.meta == nil {
		return
	}

	h.syncLeaderWarmupState()

	nodes, err := h.meta.ListNodes(context.Background())
	if err != nil {
		return
	}

	h.warmupMu.Lock()
	defer h.warmupMu.Unlock()

	if h.warmupLeaderID != h.localNode {
		h.warmupReady = false
		return
	}

	aliveCount := 0
	for _, node := range nodes {
		if node.Status != controllermeta.NodeStatusAlive {
			continue
		}
		aliveCount++
		if _, ok := h.warmupFullSyncs[node.NodeID]; !ok {
			h.warmupReady = false
			return
		}
	}
	h.warmupReady = aliveCount > 0
}

func (h *controllerHost) handleLeaderChange(_, to multiraft.NodeID) {
	if h == nil {
		return
	}

	term := h.leaderTerm.Add(1)

	h.warmupMu.Lock()
	h.warmupLeaderID = to
	h.warmupReady = false
	h.warmupFullSyncs = make(map[uint64]struct{})
	if to == h.localNode {
		h.warmupGeneration++
	}
	h.warmupMu.Unlock()

	h.hashSlotReloadMu.Lock()
	h.hashSlotReloadPending = false
	h.hashSlotReloadScheduled = false
	h.hashSlotReloadSeq++
	h.hashSlotReloadMu.Unlock()

	// Clear on every leader change so no stale snapshot can be served during async warmups.
	h.clearHashSlotTableSnapshot()

	if h.observations != nil {
		h.observations.reset()
	}
	if h.syncState != nil {
		h.syncState.reset()
	}
	if h.healthScheduler != nil {
		h.healthScheduler.reset()
	}
	if to == h.localNode {
		h.metadataSnapshotState.onLocalLeaderAcquiredAsync(h.bgCtx, h.meta, h.localNode, h.metadataReloadTimeout)
		go h.warmupNodeMirror(term)
		h.enqueueHashSlotTableReload(term)
	} else {
		h.metadataSnapshotState.invalidate(to)
	}
	h.markPlannerDirty()
}

func (h *controllerHost) handleCommittedCommand(cmd slotcontroller.Command) {
	if h == nil {
		return
	}
	nodeStatusChanges := h.committedNodeStatusChanges(cmd)
	if shouldRefreshHashSlotSnapshot(cmd) {
		// Invalidate immediately; leader reads must fall back until reload completes.
		h.clearHashSlotTableSnapshot()
		term := h.leaderTerm.Load()
		h.enqueueHashSlotTableReload(term)
	}
	if shouldMarkMetadataSnapshotDirty(cmd) {
		if shouldEnqueueMetadataSnapshotReload(cmd) {
			h.metadataSnapshotState.markDirtyAndEnqueueReload(h.bgCtx, h.meta, h.localNode, h.metadataReloadTimeout)
		} else {
			h.metadataSnapshotState.markDirtyOnly()
		}
	}
	if h.healthScheduler != nil {
		h.healthScheduler.handleCommittedCommand(cmd)
	}
	h.emitCommittedNodeStatusChanges(nodeStatusChanges)
	switch cmd.Kind {
	case slotcontroller.CommandKindNodeStatusUpdate, slotcontroller.CommandKindOperatorRequest:
		h.refreshWarmupReady()
	}
	h.markPlannerDirty()
}

type committedNodeStatusChange struct {
	nodeID uint64
	from   controllermeta.NodeStatus
}

func (h *controllerHost) committedNodeStatusChanges(cmd slotcontroller.Command) []committedNodeStatusChange {
	if h == nil || h.obs.OnNodeStatusChange == nil || h.LeaderID() != h.localNode {
		return nil
	}
	switch cmd.Kind {
	case slotcontroller.CommandKindNodeStatusUpdate:
		if cmd.NodeStatusUpdate == nil {
			return nil
		}
		changes := make([]committedNodeStatusChange, 0, len(cmd.NodeStatusUpdate.Transitions))
		for _, transition := range cmd.NodeStatusUpdate.Transitions {
			changes = append(changes, committedNodeStatusChange{
				nodeID: transition.NodeID,
				from:   h.nodeStatusBeforeCommit(transition.NodeID, transition.ExpectedStatus),
			})
		}
		return changes
	case slotcontroller.CommandKindOperatorRequest:
		if cmd.Op == nil || cmd.Op.NodeID == 0 {
			return nil
		}
		return []committedNodeStatusChange{{
			nodeID: cmd.Op.NodeID,
			from:   h.nodeStatusBeforeCommit(cmd.Op.NodeID, nil),
		}}
	default:
		return nil
	}
}

func (h *controllerHost) nodeStatusBeforeCommit(nodeID uint64, fallback *controllermeta.NodeStatus) controllermeta.NodeStatus {
	if h == nil || nodeID == 0 {
		return controllermeta.NodeStatusUnknown
	}
	if h.healthScheduler != nil {
		if mirrored, ok := h.healthScheduler.mirroredNode(nodeID); ok {
			return mirrored.Status
		}
	}
	if fallback != nil {
		return *fallback
	}
	return controllermeta.NodeStatusUnknown
}

func (h *controllerHost) nodeStatusAfterCommit(nodeID uint64) (controllermeta.NodeStatus, bool) {
	if h == nil || nodeID == 0 {
		return controllermeta.NodeStatusUnknown, false
	}
	if h.healthScheduler != nil {
		if mirrored, ok := h.healthScheduler.mirroredNode(nodeID); ok {
			return mirrored.Status, true
		}
	}
	if h.meta == nil {
		return controllermeta.NodeStatusUnknown, false
	}
	node, err := h.meta.GetNode(context.Background(), nodeID)
	if err != nil {
		return controllermeta.NodeStatusUnknown, false
	}
	return node.Status, true
}

func (h *controllerHost) emitCommittedNodeStatusChanges(changes []committedNodeStatusChange) {
	if h == nil || len(changes) == 0 || h.obs.OnNodeStatusChange == nil || h.LeaderID() != h.localNode {
		return
	}
	for _, change := range changes {
		to, ok := h.nodeStatusAfterCommit(change.nodeID)
		if !ok {
			continue
		}
		if change.from == to && change.from != controllermeta.NodeStatusUnknown {
			continue
		}
		h.obs.OnNodeStatusChange(change.nodeID, change.from, to)
	}
}

func (h *controllerHost) plannerWakeChannel() <-chan struct{} {
	if h == nil {
		return nil
	}
	return h.plannerWakeCh
}

func (h *controllerHost) consumePlannerDirty() bool {
	if h == nil {
		return false
	}
	return h.plannerDirty.CompareAndSwap(true, false)
}

func (h *controllerHost) markPlannerDirty() {
	if h == nil {
		return
	}
	h.plannerDirty.Store(true)
	debounce := h.plannerWakeDebounce
	if debounce <= 0 {
		h.enqueuePlannerWake()
		return
	}
	h.plannerWakeMu.Lock()
	if h.plannerWakeTimer != nil {
		h.plannerWakeMu.Unlock()
		return
	}
	h.plannerWakeTimer = time.AfterFunc(debounce, func() {
		h.enqueuePlannerWake()
		h.plannerWakeMu.Lock()
		h.plannerWakeTimer = nil
		h.plannerWakeMu.Unlock()
	})
	h.plannerWakeMu.Unlock()
}

func (h *controllerHost) enqueuePlannerWake() {
	if h == nil || h.plannerWakeCh == nil {
		return
	}
	select {
	case h.plannerWakeCh <- struct{}{}:
	default:
	}
}

func (h *controllerHost) isLocalLeaderTerm(term uint64) bool {
	if h == nil {
		return false
	}
	if h.leaderTerm.Load() != term {
		return false
	}
	h.warmupMu.RLock()
	defer h.warmupMu.RUnlock()
	return h.warmupLeaderID == h.localNode && h.warmupGeneration > 0
}

func (h *controllerHost) isTerm(term uint64) bool {
	return h != nil && h.leaderTerm.Load() == term
}

func (h *controllerHost) withWarmupTimeout() (context.Context, context.CancelFunc) {
	ctx := h.bgCtx
	timeout := h.metadataReloadTimeout
	if timeout <= 0 {
		timeout = time.Second
	}
	if ctx == nil {
		return context.WithTimeout(context.Background(), timeout)
	}
	return context.WithTimeout(ctx, timeout)
}

func (h *controllerHost) warmupNodeMirror(term uint64) {
	if h == nil || h.healthScheduler == nil {
		return
	}
	if !h.isLocalLeaderTerm(term) {
		return
	}
	ctx, cancel := h.withWarmupTimeout()
	nodes, err := h.loadNodeMirror(ctx)
	cancel()
	if err != nil {
		return
	}
	s := h.healthScheduler
	if !h.isLocalLeaderTerm(term) {
		return
	}
	if !h.isTerm(term) {
		return
	}
	s.primeFromNodes(nodes)
}

func (h *controllerHost) enqueueHashSlotTableReload(term uint64) {
	if h == nil {
		return
	}
	if !h.isLocalLeaderTerm(term) {
		return
	}
	startWorker := false
	h.hashSlotReloadMu.Lock()
	h.hashSlotReloadSeq++
	h.hashSlotReloadPending = true
	if !h.hashSlotReloadScheduled {
		h.hashSlotReloadScheduled = true
		startWorker = true
	}
	h.hashSlotReloadMu.Unlock()
	if startWorker {
		go h.hashSlotTableReloadWorker(term)
	}
}

func (h *controllerHost) hashSlotTableReloadWorker(term uint64) {
	if h == nil {
		return
	}
	for {
		var seq uint64
		h.hashSlotReloadMu.Lock()
		if !h.hashSlotReloadPending {
			h.hashSlotReloadScheduled = false
			h.hashSlotReloadMu.Unlock()
			return
		}
		h.hashSlotReloadPending = false
		seq = h.hashSlotReloadSeq
		h.hashSlotReloadMu.Unlock()

		if !h.isLocalLeaderTerm(term) {
			h.hashSlotReloadMu.Lock()
			h.hashSlotReloadPending = false
			h.hashSlotReloadScheduled = false
			h.hashSlotReloadMu.Unlock()
			return
		}

		ctx, cancel := h.withWarmupTimeout()
		table, err := h.loadHashSlotTable(ctx)
		cancel()
		if err != nil || table == nil {
			continue
		}

		// Apply only if we're still in the same leader term and no newer reload was requested.
		h.hashSlotReloadMu.Lock()
		if h.hashSlotReloadSeq != seq {
			h.hashSlotReloadMu.Unlock()
			continue
		}
		if !h.isLocalLeaderTerm(term) {
			h.hashSlotReloadMu.Unlock()
			return
		}
		h.hashSlotMu.Lock()
		if h.hashSlotReloadSeq != seq || !h.isTerm(term) {
			h.hashSlotMu.Unlock()
			h.hashSlotReloadMu.Unlock()
			return
		}
		h.hashSlotTable = table.Clone()
		h.hashSlotMu.Unlock()
		h.hashSlotReloadMu.Unlock()
	}
}

func shouldRefreshHashSlotSnapshot(cmd slotcontroller.Command) bool {
	switch cmd.Kind {
	case slotcontroller.CommandKindStartMigration,
		slotcontroller.CommandKindAdvanceMigration,
		slotcontroller.CommandKindFinalizeMigration,
		slotcontroller.CommandKindAbortMigration,
		slotcontroller.CommandKindAddSlot,
		slotcontroller.CommandKindRemoveSlot:
		return true
	default:
		return false
	}
}

func shouldMarkMetadataSnapshotDirty(cmd slotcontroller.Command) bool {
	switch cmd.Kind {
	case slotcontroller.CommandKindNodeHeartbeat,
		slotcontroller.CommandKindOperatorRequest,
		slotcontroller.CommandKindEvaluateTimeouts,
		slotcontroller.CommandKindTaskResult,
		slotcontroller.CommandKindAssignmentTaskUpdate,
		slotcontroller.CommandKindStartMigration,
		slotcontroller.CommandKindAdvanceMigration,
		slotcontroller.CommandKindFinalizeMigration,
		slotcontroller.CommandKindAbortMigration,
		slotcontroller.CommandKindAddSlot,
		slotcontroller.CommandKindRemoveSlot,
		slotcontroller.CommandKindNodeStatusUpdate:
		return true
	default:
		return false
	}
}

func shouldEnqueueMetadataSnapshotReload(cmd slotcontroller.Command) bool {
	switch cmd.Kind {
	case slotcontroller.CommandKindNodeHeartbeat, slotcontroller.CommandKindEvaluateTimeouts:
		return false
	default:
		return shouldMarkMetadataSnapshotDirty(cmd)
	}
}
