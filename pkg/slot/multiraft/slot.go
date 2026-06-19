package multiraft

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	raft "go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
)

type slot struct {
	mu                 sync.Mutex
	id                 SlotID
	logger             wklog.Logger
	storage            Storage
	stateMachine       StateMachine
	observer           SchedulerObserver
	status             Status
	storageView        *storageAdapter
	closed             bool
	fatalErr           error
	cond               *sync.Cond
	processing         bool
	rawNode            *raft.RawNode
	requests           []raftpb.Message
	requestWorkBuf     []raftpb.Message
	requestCount       int
	controls           []controlAction
	controlWorkBuf     []controlAction
	submittedProposals []*future
	submittedConfigs   []*future
	pendingProposals   map[uint64]trackedFuture
	pendingConfigs     map[uint64]trackedFuture
	pendingProposalCap int
	pendingConfigCap   int
	resolutionBuf      []futureResolution
	transportBuf       []Envelope
	tickPending        bool
	tickCount          int
	// votersInitialized reports whether CurrentVoters has been populated from a full Raft status.
	votersInitialized bool
	// votersDirty requests a full status refresh after applying a config change.
	votersDirty bool
	// compactor creates local snapshots and trims applied log entries for this Slot.
	compactor *logCompactor
	// basicStatusRefreshCount counts allocation-light status refreshes for regression tests.
	basicStatusRefreshCount int
	// fullStatusRefreshCount counts full RawNode.Status refreshes for regression tests.
	fullStatusRefreshCount int
}

type trackedFuture struct {
	future *future
	term   uint64
}

type futureResolution struct {
	kind   controlKind
	index  uint64
	term   uint64
	future *future
	result Result
	err    error
}

type controlKind uint8

const (
	controlPropose controlKind = iota + 1
	controlCampaign
	controlConfigChange
	controlTransferLeader
	controlCompactLog
)

type controlAction struct {
	kind    controlKind
	data    []byte
	future  *future
	target  NodeID
	change  ConfigChange
	compact *logCompactionRequest
}

type logCompactionRequest struct {
	ctx  context.Context
	resp chan logCompactionResponse
}

type logCompactionResponse struct {
	result LogCompactionResult
	err    error
}

func newSlot(ctx context.Context, nodeID NodeID, logger wklog.Logger, raftOpts RaftOptions, opts SlotOptions, observer SchedulerObserver) (*slot, error) {
	state, snapshot, memory, err := newStorageAdapter(opts.Storage).load(ctx)
	if err != nil {
		return nil, err
	}

	appliedIndex := state.AppliedIndex
	if !raft.IsEmptySnap(snapshot) {
		appliedIndex = snapshot.Metadata.Index
	}
	rawNode, err := raft.NewRawNode(&raft.Config{
		ID:              uint64(nodeID),
		ElectionTick:    raftOpts.ElectionTick,
		HeartbeatTick:   raftOpts.HeartbeatTick,
		Storage:         memory,
		Applied:         appliedIndex,
		MaxSizePerMsg:   maxSizePerMsg(raftOpts.MaxSizePerMsg),
		MaxInflightMsgs: maxInflight(raftOpts.MaxInflight),
		CheckQuorum:     raftOpts.CheckQuorum,
		PreVote:         raftOpts.PreVote,
		Logger:          newEtcdRaftLogger(logger, nodeID, opts.ID),
	})
	if err != nil {
		return nil, err
	}

	g := &slot{
		id:           opts.ID,
		storage:      opts.Storage,
		stateMachine: opts.StateMachine,
		observer:     observer,
		status: Status{
			SlotID:       opts.ID,
			NodeID:       nodeID,
			LeaderID:     NodeID(state.HardState.Vote),
			CommitIndex:  state.HardState.Commit,
			AppliedIndex: appliedIndex,
		},
		logger:      logger,
		storageView: newStorageAdapter(opts.Storage),
		rawNode:     rawNode,
		compactor:   newLogCompactor(raftOpts.LogCompaction, snapshot.Metadata.Index),
	}
	g.cond = sync.NewCond(&g.mu)
	g.storageView.memory = memory
	if !raft.IsEmptySnap(snapshot) {
		if err := g.stateMachine.Restore(ctx, Snapshot{
			Index: snapshot.Metadata.Index,
			Term:  snapshot.Metadata.Term,
			Data:  append([]byte(nil), snapshot.Data...),
		}); err != nil {
			return nil, err
		}
	}
	g.refreshStatus()
	return g, nil
}

func (g *slot) enqueueRequest(msg raftpb.Message) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.admissionErrLocked(); err != nil {
		return err
	}
	g.observeQueuedMessageLocked(msg)
	g.requests = append(g.requests, msg)
	return nil
}

func (g *slot) processRequests() bool {
	requests := g.takeRequestBatch()
	defer g.releaseRequestBatch(requests)

	for _, msg := range requests {
		_ = g.rawNode.Step(msg)
	}
	return len(requests) > 0
}

func (g *slot) enqueueControl(action controlAction) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.admissionErrLocked(); err != nil {
		return err
	}
	switch action.kind {
	case controlPropose, controlConfigChange:
		if g.status.Role != RoleLeader {
			return ErrNotLeader
		}
		if action.kind == controlConfigChange && g.hasPendingConfigChangeLocked() {
			return ErrConfigChangePending
		}
	}
	g.controls = append(g.controls, action)
	return nil
}

func (g *slot) processControls(ctx context.Context) bool {
	controls := g.takeControlBatch()
	defer g.releaseControlBatch(controls)

	for _, action := range controls {
		switch action.kind {
		case controlPropose:
			action.future.observeStageSince("meta_create_slot_control_wait", nil, action.future.createdAt)
			if err := g.rawNode.Propose(action.data); err != nil {
				action.future.resolve(Result{}, err)
				continue
			}
			g.mu.Lock()
			g.submittedProposals = append(g.submittedProposals, action.future)
			g.mu.Unlock()
		case controlConfigChange:
			cc, err := toRaftConfChange(action.change)
			if err != nil {
				action.future.resolve(Result{}, err)
				continue
			}
			if err := g.rawNode.ProposeConfChange(cc); err != nil {
				action.future.resolve(Result{}, err)
				continue
			}
			g.mu.Lock()
			g.submittedConfigs = append(g.submittedConfigs, action.future)
			g.mu.Unlock()
		case controlCampaign:
			_ = g.rawNode.Campaign()
		case controlTransferLeader:
			g.rawNode.TransferLeader(uint64(action.target))
		case controlCompactLog:
			if action.compact == nil {
				continue
			}
			if err := action.compact.ctx.Err(); err != nil {
				action.compact.resp <- logCompactionResponse{err: err}
				continue
			}
			applied := g.rawNode.BasicStatus().Applied
			result, err := g.compactLogManually(action.compact.ctx, applied)
			action.compact.resp <- logCompactionResponse{result: result, err: err}
		}
	}
	return len(controls) > 0
}

func (g *slot) takeRequestBatch() []raftpb.Message {
	g.mu.Lock()
	defer g.mu.Unlock()

	batch := g.requests
	g.requestCount += len(batch)
	g.requests = g.requestWorkBuf[:0]
	g.requestWorkBuf = nil
	return batch
}

func (g *slot) releaseRequestBatch(batch []raftpb.Message) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.requestWorkBuf = batch[:0]
}

func (g *slot) takeControlBatch() []controlAction {
	g.mu.Lock()
	defer g.mu.Unlock()

	batch := g.controls
	g.controls = g.controlWorkBuf[:0]
	g.controlWorkBuf = nil
	return batch
}

func (g *slot) releaseControlBatch(batch []controlAction) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.controlWorkBuf = batch[:0]
}

func (g *slot) hasPendingConfigChangeLocked() bool {
	if len(g.submittedConfigs) > 0 || len(g.pendingConfigs) > 0 {
		return true
	}
	for _, action := range g.controls {
		if action.kind == controlConfigChange {
			return true
		}
	}
	return false
}

func (g *slot) takeResolutionBuffer() []futureResolution {
	g.mu.Lock()
	defer g.mu.Unlock()

	buf := g.resolutionBuf[:0]
	g.resolutionBuf = nil
	return buf
}

func (g *slot) releaseResolutionBuffer(buf []futureResolution) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.resolutionBuf = buf[:0]
}

func (g *slot) markTickPending() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.tickPending = true
}

func (g *slot) processTick() bool {
	g.mu.Lock()
	defer g.mu.Unlock()

	if !g.tickPending {
		return false
	}
	g.tickPending = false
	g.tickCount++
	g.rawNode.Tick()
	return true
}

func (g *slot) processReady(ctx context.Context, transport Transport) (bool, bool) {
	if !g.rawNode.HasReady() {
		return false, false
	}

	ready := g.rawNode.Ready()
	if err := g.storageView.persistReady(ctx, ready); err != nil {
		g.failPending(err)
		return true, false
	}
	proposalCount, configCount := countTrackedReadyEntries(ready.Entries)
	g.ensurePendingProposalCapacity(proposalCount)
	g.ensurePendingConfigCapacity(configCount)
	g.trackReadyEntries(ready.Entries)

	if len(ready.Messages) > 0 {
		g.transportBuf = wrapMessagesIntoForTransport(g.transportBuf[:0], g.id, ready.Messages, transport)
		_ = transport.Send(ctx, g.transportBuf)
	}

	lastApplied := g.appliedIndex()
	appliedBeforeReady := lastApplied
	resolutions := g.takeResolutionBuffer()
	defer func() {
		g.releaseResolutionBuffer(resolutions)
	}()
	if !raft.IsEmptySnap(ready.Snapshot) {
		if err := g.stateMachine.Restore(ctx, Snapshot{
			Index: ready.Snapshot.Metadata.Index,
			Term:  ready.Snapshot.Metadata.Term,
			Data:  append([]byte(nil), ready.Snapshot.Data...),
		}); err != nil {
			g.fail(err)
			return true, false
		}
		lastApplied = ready.Snapshot.Metadata.Index
	}

	batchSM, canBatch := g.stateMachine.(BatchStateMachine)
	var configChanged bool
	resolutions, configChanged = g.applyCommittedEntries(ctx, ready.CommittedEntries, &lastApplied, resolutions, batchSM, canBatch)
	if g.fatalErr != nil {
		return true, false
	}

	if lastApplied > appliedBeforeReady {
		started := time.Now()
		err := g.storage.MarkApplied(ctx, lastApplied)
		g.observeResolutionFutures(resolutions, "meta_create_slot_mark_applied", err, time.Since(started))
		if err != nil {
			g.fail(err)
			return true, false
		}
	}

	g.rawNode.Advance(ready)
	g.refreshStatus()
	g.completeResolutions(resolutions)
	// Refresh snapshots after membership changes so future learners can restore
	// a snapshot whose ConfState includes the latest peer set.
	if g.compactor.shouldCompact(lastApplied) || (configChanged && g.compactor.shouldRefreshAfterConfigChange(lastApplied)) {
		if err := g.compactLog(ctx, lastApplied); err != nil {
			g.logCompactionWarning(err, lastApplied)
		} else {
			g.compactor.recordSnapshot(lastApplied)
		}
	}
	return true, g.rawNode.HasReady()
}

func (g *slot) logCompactionWarning(err error, applied uint64) {
	if g == nil || g.logger == nil || err == nil {
		return
	}
	g.logger.Warn("slot raft log compaction failed",
		wklog.SlotID(uint64(g.id)),
		wklog.Uint64("appliedIndex", applied),
		wklog.Error(err),
	)
}

func (g *slot) applyCommittedEntries(
	ctx context.Context,
	entries []raftpb.Entry,
	lastApplied *uint64,
	resolutions []futureResolution,
	batchSM BatchStateMachine,
	canBatch bool,
) ([]futureResolution, bool) {
	// Collect contiguous normal entries for batched apply.
	var batchEntries []raftpb.Entry
	var configChanged bool

	flushBatch := func() bool {
		if len(batchEntries) == 0 {
			return true
		}
		defer func() { batchEntries = batchEntries[:0] }()

		if !canBatch || len(batchEntries) == 1 {
			// Fall back to one-by-one Apply.
			for _, entry := range batchEntries {
				fut := g.proposalFuture(entry.Index, entry.Term)
				var trackedAt time.Time
				if fut != nil {
					trackedAt = fut.trackedAt
				}
				fut.observeStageSince("meta_create_slot_raft_commit_wait", nil, trackedAt)
				hashSlot, data, err := decodeProposalPayload(entry.Data)
				if err != nil {
					g.resolveProposal(entry.Index, entry.Term, Result{
						Index: entry.Index,
						Term:  entry.Term,
					}, err)
					g.fail(err)
					return false
				}
				applyCtx := withProposalStageObservers(ctx, proposalStageObserversFromFutures([]*future{fut}))
				started := time.Now()
				result, err := g.stateMachine.Apply(applyCtx, Command{
					SlotID:   g.id,
					HashSlot: hashSlot,
					Index:    entry.Index,
					Term:     entry.Term,
					Data:     append([]byte(nil), data...),
				})
				fut.observeStage("meta_create_slot_fsm_apply", err, time.Since(started))
				if err != nil {
					g.resolveProposal(entry.Index, entry.Term, Result{
						Index: entry.Index,
						Term:  entry.Term,
						Data:  result,
					}, err)
					g.fail(err)
					return false
				}
				resolutions = append(resolutions, futureResolution{
					kind:   controlPropose,
					index:  entry.Index,
					term:   entry.Term,
					future: fut,
					result: Result{
						Index: entry.Index,
						Term:  entry.Term,
						Data:  result,
					},
				})
			}
			return true
		}

		// Batched apply.
		cmds := make([]Command, len(batchEntries))
		futures := g.proposalFutures(batchEntries)
		for i, entry := range batchEntries {
			hashSlot, data, err := decodeProposalPayload(entry.Data)
			if err != nil {
				g.resolveProposal(entry.Index, entry.Term, Result{
					Index: entry.Index,
					Term:  entry.Term,
				}, err)
				g.fail(err)
				return false
			}
			cmds[i] = Command{
				SlotID:   g.id,
				HashSlot: hashSlot,
				Index:    entry.Index,
				Term:     entry.Term,
				Data:     append([]byte(nil), data...),
			}
		}
		observeFuturesSince(futures, "meta_create_slot_raft_commit_wait", nil, func(f *future) time.Time {
			if f == nil {
				return time.Time{}
			}
			return f.trackedAt
		})
		applyCtx := withProposalStageObservers(ctx, proposalStageObserversFromFutures(futures))
		started := time.Now()
		results, err := batchSM.ApplyBatch(applyCtx, cmds)
		observeFutures(futures, "meta_create_slot_fsm_apply", err, time.Since(started))
		if err != nil {
			// Resolve the last entry and fail the slot.
			last := batchEntries[len(batchEntries)-1]
			g.resolveProposal(last.Index, last.Term, Result{
				Index: last.Index,
				Term:  last.Term,
			}, err)
			g.fail(err)
			return false
		}
		for i, entry := range batchEntries {
			var data []byte
			if i < len(results) {
				data = results[i]
			}
			resolutions = append(resolutions, futureResolution{
				kind:   controlPropose,
				index:  entry.Index,
				term:   entry.Term,
				future: futures[i],
				result: Result{
					Index: entry.Index,
					Term:  entry.Term,
					Data:  data,
				},
			})
		}
		return true
	}

	for _, entry := range entries {
		*lastApplied = entry.Index
		switch entry.Type {
		case raftpb.EntryNormal:
			if len(entry.Data) == 0 {
				continue
			}
			batchEntries = append(batchEntries, entry)
		case raftpb.EntryConfChange:
			// Flush pending normal entries before processing conf change.
			if !flushBatch() {
				return resolutions, configChanged
			}
			var cc raftpb.ConfChange
			if err := cc.Unmarshal(entry.Data); err != nil {
				resolutions = append(resolutions, futureResolution{
					kind:  controlConfigChange,
					index: entry.Index,
					term:  entry.Term,
					err:   err,
				})
				continue
			}
			latest := g.rawNode.ApplyConfChange(cc)
			g.storageView.memory.confState = cloneConfState(*latest)
			configChanged = true
			g.markVotersDirty()
			resolutions = append(resolutions, futureResolution{
				kind:  controlConfigChange,
				index: entry.Index,
				term:  entry.Term,
				result: Result{
					Index: entry.Index,
					Term:  entry.Term,
				},
			})
		}
	}

	// Flush any remaining normal entries.
	flushBatch()
	return resolutions, configChanged
}

// proposalEnvelopeSize is [hashSlot:2][createdAtMS:8] before the Slot FSM command.
const proposalEnvelopeSize = 10

func decodeProposalPayload(data []byte) (uint16, []byte, error) {
	if len(data) < proposalEnvelopeSize {
		return 0, nil, fmt.Errorf("proposal payload too short: %d", len(data))
	}
	return binary.BigEndian.Uint16(data[:2]), data[proposalEnvelopeSize:], nil
}

func (g *slot) completeResolutions(resolutions []futureResolution) {
	for _, resolution := range resolutions {
		switch resolution.kind {
		case controlPropose:
			g.resolveProposal(resolution.index, resolution.term, resolution.result, resolution.err)
		case controlConfigChange:
			g.resolveConfig(resolution.index, resolution.term, resolution.result, resolution.err)
		}
	}
}

func (g *slot) proposalFuture(index, term uint64) *future {
	g.mu.Lock()
	defer g.mu.Unlock()

	pending, ok := g.pendingProposals[index]
	if !ok || pending.term != term {
		return nil
	}
	return pending.future
}

func (g *slot) proposalFutures(entries []raftpb.Entry) []*future {
	futures := make([]*future, len(entries))
	g.mu.Lock()
	defer g.mu.Unlock()

	for i, entry := range entries {
		pending, ok := g.pendingProposals[entry.Index]
		if ok && pending.term == entry.Term {
			futures[i] = pending.future
		}
	}
	return futures
}

func (g *slot) observeResolutionFutures(resolutions []futureResolution, stage string, err error, d time.Duration) {
	for _, resolution := range resolutions {
		if resolution.kind == controlPropose && resolution.future != nil {
			resolution.future.observeStage(stage, err, d)
		}
	}
}

func observeFutures(futures []*future, stage string, err error, d time.Duration) {
	for _, future := range futures {
		if future != nil {
			future.observeStage(stage, err, d)
		}
	}
}

func observeFuturesSince(futures []*future, stage string, err error, started func(*future) time.Time) {
	for _, future := range futures {
		if future == nil {
			continue
		}
		future.observeStageSince(stage, err, started(future))
	}
}

func proposalStageObserversFromFutures(futures []*future) []ProposalStageObserver {
	var observers []ProposalStageObserver
	for _, future := range futures {
		if future == nil || len(future.observers) == 0 {
			continue
		}
		observers = append(observers, future.observers...)
	}
	return observers
}

func (g *slot) refreshStatus() {
	if g.needsFullStatusRefresh() {
		g.refreshFullStatus()
		return
	}
	g.refreshBasicStatus()
}

func (g *slot) needsFullStatusRefresh() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return !g.votersInitialized || g.votersDirty
}

// refreshBasicStatus updates volatile Raft status without cloning tracker progress.
func (g *slot) refreshBasicStatus() {
	st := g.rawNode.BasicStatus()
	g.mu.Lock()
	defer g.mu.Unlock()
	g.basicStatusRefreshCount++
	g.applyBasicStatusLocked(st)
}

// refreshFullStatus updates voter membership and basic status from RawNode.Status.
func (g *slot) refreshFullStatus() {
	st := g.rawNode.Status()
	g.mu.Lock()
	defer g.mu.Unlock()
	g.fullStatusRefreshCount++
	g.votersInitialized = true
	g.votersDirty = false
	g.status.CurrentVoters = currentVotersFromRaftStatus(st)
	g.applyBasicStatusLocked(st.BasicStatus)
}

func (g *slot) applyBasicStatusLocked(st raft.BasicStatus) {
	prevRole := g.status.Role
	nextRole := mapRole(st.RaftState)
	nextLeader := NodeID(st.Lead)
	if nextLeader == 0 && nextRole == RoleLeader {
		nextLeader = g.status.NodeID
	}
	g.setLeaderIDLocked(nextLeader)
	g.status.Term = st.Term
	g.status.CommitIndex = st.Commit
	g.status.AppliedIndex = st.Applied
	g.status.Role = nextRole
	if observer, ok := g.observer.(ApplyStateObserver); ok && observer != nil {
		observer.SetSlotApplyState(g.id, st.Commit, st.Applied)
	}
	if prevRole == RoleLeader && g.status.Role != RoleLeader {
		g.failLeadershipDependentLocked(ErrNotLeader)
	}
}

func (g *slot) markVotersDirty() {
	g.mu.Lock()
	g.votersDirty = true
	g.mu.Unlock()
}

func currentVotersFromRaftStatus(st raft.Status) []NodeID {
	ids := st.Config.Voters.IDs()
	if len(ids) == 0 {
		return nil
	}
	voters := make([]NodeID, 0, len(ids))
	for id := range ids {
		voters = append(voters, NodeID(id))
	}
	sort.Slice(voters, func(i, j int) bool {
		return voters[i] < voters[j]
	})
	return voters
}

func (g *slot) appliedIndex() uint64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.status.AppliedIndex
}

func (g *slot) nodeID() NodeID {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.status.NodeID
}

func (g *slot) statusSnapshot() (Status, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return Status{}, ErrSlotClosed
	}
	if g.fatalErr != nil {
		return Status{}, g.fatalErr
	}
	status := g.status
	status.CurrentVoters = append([]NodeID(nil), g.status.CurrentVoters...)
	return status, nil
}

func (g *slot) admissionErrLocked() error {
	if g.closed {
		return ErrSlotClosed
	}
	if g.fatalErr != nil {
		return g.fatalErr
	}
	return nil
}

func (g *slot) observeQueuedMessageLocked(msg raftpb.Message) {
	if msg.Term <= g.status.Term {
		return
	}

	g.status.Term = msg.Term
	g.status.Role = RoleFollower

	switch msg.Type {
	case raftpb.MsgApp, raftpb.MsgHeartbeat, raftpb.MsgSnap:
		g.setLeaderIDLocked(NodeID(msg.From))
	default:
		g.setLeaderIDLocked(0)
	}
}

func (g *slot) setLeaderIDLocked(next NodeID) {
	prev := g.status.LeaderID
	g.status.LeaderID = next
	if observer, ok := g.observer.(LeaderChangeObserver); ok && observer != nil && next != 0 && prev != next {
		observer.ObserveSlotLeaderChange(g.id, prev, next)
	}
}

func (g *slot) shouldProcess() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.admissionErrLocked() == nil
}

func (g *slot) beginProcessing() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.admissionErrLocked() != nil {
		return false
	}
	if g.processing {
		return false
	}
	g.processing = true
	return true
}

func (g *slot) finishProcessing() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.processing = false
	if g.cond != nil {
		g.cond.Broadcast()
	}
}

func (g *slot) fail(err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err == nil || g.closed || g.fatalErr != nil {
		return
	}
	g.fatalErr = err
	g.failPendingLocked(err)
}

func countTrackedReadyEntries(entries []raftpb.Entry) (proposalCount, configCount int) {
	for _, entry := range entries {
		switch entry.Type {
		case raftpb.EntryNormal:
			if len(entry.Data) > 0 {
				proposalCount++
			}
		case raftpb.EntryConfChange:
			configCount++
		}
	}
	return proposalCount, configCount
}

func (g *slot) ensurePendingProposalCapacity(additional int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.pendingProposals, g.pendingProposalCap = ensureTrackedFutureMapCapacity(
		g.pendingProposals,
		g.pendingProposalCap,
		additional,
	)
}

func (g *slot) ensurePendingConfigCapacity(additional int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.pendingConfigs, g.pendingConfigCap = ensureTrackedFutureMapCapacity(
		g.pendingConfigs,
		g.pendingConfigCap,
		additional,
	)
}

func ensureTrackedFutureMapCapacity(
	current map[uint64]trackedFuture,
	currentCap int,
	additional int,
) (map[uint64]trackedFuture, int) {
	if additional <= 0 {
		if current == nil {
			return nil, 0
		}
		if currentCap < len(current) {
			currentCap = len(current)
		}
		return current, currentCap
	}

	required := len(current) + additional
	if current == nil {
		return make(map[uint64]trackedFuture, required), required
	}
	if currentCap >= required {
		return current, currentCap
	}

	nextCap := currentCap * 2
	if nextCap < required {
		nextCap = required
	}
	resized := make(map[uint64]trackedFuture, nextCap)
	for index, pending := range current {
		resized[index] = pending
	}
	return resized, nextCap
}

func (g *slot) trackReadyEntries(entries []raftpb.Entry) {
	g.mu.Lock()
	defer g.mu.Unlock()

	for _, entry := range entries {
		switch entry.Type {
		case raftpb.EntryNormal:
			if len(entry.Data) == 0 || len(g.submittedProposals) == 0 {
				continue
			}
			if g.pendingProposals == nil {
				g.pendingProposals = make(map[uint64]trackedFuture)
			}
			g.submittedProposals[0].trackedAt = time.Now()
			g.pendingProposals[entry.Index] = trackedFuture{
				future: g.submittedProposals[0],
				term:   entry.Term,
			}
			g.submittedProposals = g.submittedProposals[1:]
		case raftpb.EntryConfChange:
			if len(g.submittedConfigs) == 0 {
				continue
			}
			if g.pendingConfigs == nil {
				g.pendingConfigs = make(map[uint64]trackedFuture)
			}
			g.pendingConfigs[entry.Index] = trackedFuture{
				future: g.submittedConfigs[0],
				term:   entry.Term,
			}
			g.submittedConfigs = g.submittedConfigs[1:]
		}
	}
}

func (g *slot) resolveProposal(index, term uint64, result Result, err error) {
	g.mu.Lock()

	pending, ok := g.pendingProposals[index]
	if !ok || pending.term != term {
		g.mu.Unlock()
		return
	}
	delete(g.pendingProposals, index)
	fut := pending.future
	if fut != nil {
		fut.resolve(result, err)
	}
	g.mu.Unlock()
	g.observeSlotProposal(fut)
}

func (g *slot) failPending(err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.failPendingLocked(err)
}

func (g *slot) failPendingLocked(err error) {
	for _, fut := range g.submittedProposals {
		fut.resolve(Result{}, err)
	}
	for _, fut := range g.submittedConfigs {
		fut.resolve(Result{}, err)
	}
	for index, pending := range g.pendingProposals {
		pending.future.resolve(Result{}, err)
		delete(g.pendingProposals, index)
	}
	for index, pending := range g.pendingConfigs {
		pending.future.resolve(Result{}, err)
		delete(g.pendingConfigs, index)
	}
	g.submittedProposals = nil
	g.submittedConfigs = nil
}

func (g *slot) failLeadershipDependentLocked(err error) {
	if err == nil {
		return
	}
	for _, fut := range g.submittedProposals {
		fut.resolve(Result{}, err)
	}
	for _, fut := range g.submittedConfigs {
		fut.resolve(Result{}, err)
	}
	for index, pending := range g.pendingProposals {
		pending.future.resolve(Result{}, err)
		delete(g.pendingProposals, index)
	}
	for index, pending := range g.pendingConfigs {
		pending.future.resolve(Result{}, err)
		delete(g.pendingConfigs, index)
	}
	g.submittedProposals = nil
	g.submittedConfigs = nil
}

func (g *slot) resolveConfig(index, term uint64, result Result, err error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	pending, ok := g.pendingConfigs[index]
	if !ok || pending.term != term {
		return
	}
	delete(g.pendingConfigs, index)
	pending.future.resolve(result, err)
}

func (g *slot) observeSlotProposal(fut *future) {
	if g == nil || fut == nil || fut.createdAt.IsZero() {
		return
	}
	observer, ok := g.observer.(ProposalObserver)
	if !ok || observer == nil {
		return
	}
	observer.ObserveSlotProposal(g.id, time.Since(fut.createdAt))
}

func wrapMessages(slotID SlotID, messages []raftpb.Message) []Envelope {
	return wrapMessagesInto(nil, slotID, messages)
}

func wrapMessagesInto(dst []Envelope, slotID SlotID, messages []raftpb.Message) []Envelope {
	return wrapMessagesIntoWithPayloadMode(dst, slotID, messages, true)
}

func wrapMessagesIntoForTransport(dst []Envelope, slotID SlotID, messages []raftpb.Message, transport Transport) []Envelope {
	return wrapMessagesIntoWithPayloadMode(dst, slotID, messages, !transportOwnsReadyMessagePayloads(transport))
}

func transportOwnsReadyMessagePayloads(transport Transport) bool {
	owner, ok := transport.(ReadyMessagePayloadOwner)
	return ok && owner.OwnsReadyMessagePayloads()
}

func wrapMessagesIntoWithPayloadMode(dst []Envelope, slotID SlotID, messages []raftpb.Message, clonePayloads bool) []Envelope {
	out := dst[:0]
	for _, msg := range messages {
		out = append(out, Envelope{
			SlotID:  slotID,
			Message: cloneMessage(msg, clonePayloads),
		})
	}
	return out
}

func cloneMessage(msg raftpb.Message, clonePayloads bool) raftpb.Message {
	cloned := msg
	if len(msg.Context) > 0 {
		cloned.Context = append([]byte(nil), msg.Context...)
	}
	if len(msg.Entries) > 0 {
		cloned.Entries = make([]raftpb.Entry, len(msg.Entries))
		for i, entry := range msg.Entries {
			cloned.Entries[i] = cloneEntry(entry, clonePayloads)
		}
	}
	if msg.Snapshot != nil {
		snap := cloneSnapshot(*msg.Snapshot, clonePayloads)
		cloned.Snapshot = &snap
	}
	if len(msg.Responses) > 0 {
		cloned.Responses = make([]raftpb.Message, len(msg.Responses))
		for i, response := range msg.Responses {
			cloned.Responses[i] = cloneMessage(response, clonePayloads)
		}
	}
	return cloned
}

func cloneEntry(entry raftpb.Entry, clonePayloads bool) raftpb.Entry {
	cloned := entry
	if clonePayloads && len(entry.Data) > 0 {
		cloned.Data = append([]byte(nil), entry.Data...)
	}
	return cloned
}

func cloneSnapshot(snapshot raftpb.Snapshot, clonePayloads bool) raftpb.Snapshot {
	cloned := snapshot
	if clonePayloads && len(snapshot.Data) > 0 {
		cloned.Data = append([]byte(nil), snapshot.Data...)
	}
	cloned.Metadata.ConfState = cloneConfState(snapshot.Metadata.ConfState)
	return cloned
}

func cloneConfState(state raftpb.ConfState) raftpb.ConfState {
	cloned := state
	if len(state.Voters) > 0 {
		cloned.Voters = append([]uint64(nil), state.Voters...)
	}
	if len(state.Learners) > 0 {
		cloned.Learners = append([]uint64(nil), state.Learners...)
	}
	if len(state.VotersOutgoing) > 0 {
		cloned.VotersOutgoing = append([]uint64(nil), state.VotersOutgoing...)
	}
	if len(state.LearnersNext) > 0 {
		cloned.LearnersNext = append([]uint64(nil), state.LearnersNext...)
	}
	return cloned
}

func mapRole(state raft.StateType) Role {
	switch state {
	case raft.StateLeader:
		return RoleLeader
	case raft.StateCandidate:
		return RoleCandidate
	default:
		return RoleFollower
	}
}

func maxSizePerMsg(v uint64) uint64 {
	if v == 0 {
		return math.MaxUint64
	}
	return v
}

func maxInflight(v int) int {
	if v <= 0 {
		return 256
	}
	return v
}

func toRaftConfChange(change ConfigChange) (raftpb.ConfChange, error) {
	cc := raftpb.ConfChange{
		NodeID:  uint64(change.NodeID),
		Context: append([]byte(nil), change.Context...),
	}

	switch change.Type {
	case AddVoter:
		cc.Type = raftpb.ConfChangeAddNode
	case RemoveVoter:
		cc.Type = raftpb.ConfChangeRemoveNode
	case AddLearner:
		cc.Type = raftpb.ConfChangeAddLearnerNode
	case PromoteLearner:
		cc.Type = raftpb.ConfChangeAddNode
	default:
		return raftpb.ConfChange{}, errNotImplemented
	}
	return cc, nil
}
