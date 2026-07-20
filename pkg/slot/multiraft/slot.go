package multiraft

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	raft "go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
)

type slot struct {
	mu           sync.Mutex
	id           SlotID
	logger       wklog.Logger
	storage      Storage
	stateMachine StateMachine
	observer     SchedulerObserver
	status       Status
	storageView  *storageAdapter
	apply        *applyPipeline
	closed       bool
	fatalErr     error
	cond         *sync.Cond
	processing   bool
	// applying counts async apply tasks that have been accepted for this Slot.
	applying                    int
	rawNode                     *raft.RawNode
	requests                    []raftpb.Message
	requestWorkBuf              []raftpb.Message
	requestCount                int
	controls                    []controlAction
	controlWorkBuf              []controlAction
	submittedProposals          []*future
	submittedConfigs            []*future
	pendingProposals            map[uint64]trackedFuture
	pendingConfigs              map[uint64]trackedFuture
	pendingProposalCap          int
	pendingConfigCap            int
	maxQueuedRequests           int
	maxQueuedControls           int
	maxQueuedBackgroundControls int
	queuedBackgroundControls    int
	maxApplyingTasks            int
	resolutionBuf               []futureResolution
	transportBuf                []Envelope
	tickPending                 bool
	tickCount                   int
	lastKnownLeaderID           NodeID
	// pendingLeaderTransferTarget records an expected transfer target until the next non-zero leader change is observed.
	pendingLeaderTransferTarget NodeID
	// durableAppliedIndex is the highest index that completed FSM apply and Storage.MarkApplied.
	durableAppliedIndex uint64
	// durableConfigAppliedIndex is the latest applied membership entry persisted to storage metadata.
	durableConfigAppliedIndex uint64
	// campaignAfterReady requests a local campaign after bootstrap Ready applies membership.
	campaignAfterReady bool
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
	kind           controlKind
	data           []byte
	proposalClass  ProposalClass
	future         *future
	target         NodeID
	change         ConfigChange
	compact        *logCompactionRequest
	strictTransfer *strictLeaderTransferRequest
}

// strictLeaderTransferRequest carries one exact, timeout-bounded placement
// intent from the cluster reconciler to the owning Slot worker.
type strictLeaderTransferRequest struct {
	// ctx cancels the request before it crosses the nonblocking issue point.
	ctx context.Context
	// expectedLeader fences the actual leader observed by the reconciler.
	expectedLeader NodeID
	// expectedTerm fences the Raft term observed by the reconciler.
	expectedTerm uint64
	// expectedVoters fences the stable voter set observed by the reconciler.
	expectedVoters []NodeID
	// target is the exact preferred voter; strict placement never falls back.
	target NodeID
	// guard linearizes Controller-intent invalidation with the final issue point.
	guard PreferredLeaderTransferGuard
	// resp carries the single terminal decision or error to the caller.
	resp chan strictLeaderTransferResponse
	// state prevents cancellation, execution, and completion from winning twice.
	state atomic.Uint32
}

// strictLeaderTransferResponse is the single terminal result returned by the
// Slot worker for a strict preferred-leader request.
type strictLeaderTransferResponse struct {
	// decision is a bounded no-op or transfer-started classification.
	decision PreferredLeaderTransferDecision
	// err reports cancellation or terminal Slot/runtime failure.
	err error
}

const (
	strictLeaderTransferPending uint32 = iota
	strictLeaderTransferProcessing
	strictLeaderTransferExecuting
	strictLeaderTransferDone
)

func (r *strictLeaderTransferRequest) claim() bool {
	return r != nil && r.state.CompareAndSwap(strictLeaderTransferPending, strictLeaderTransferProcessing)
}

func (r *strictLeaderTransferRequest) finish(response strictLeaderTransferResponse) {
	if r == nil {
		return
	}
	for {
		state := r.state.Load()
		if state != strictLeaderTransferProcessing && state != strictLeaderTransferExecuting {
			return
		}
		if r.state.CompareAndSwap(state, strictLeaderTransferDone) {
			r.resp <- response
			return
		}
	}
}

func (r *strictLeaderTransferRequest) cancel(err error) bool {
	if r == nil {
		return false
	}
	for {
		state := r.state.Load()
		if state == strictLeaderTransferExecuting || state == strictLeaderTransferDone {
			return false
		}
		if r.state.CompareAndSwap(state, strictLeaderTransferDone) {
			r.resp <- strictLeaderTransferResponse{err: err}
			return true
		}
	}
}

func (r *strictLeaderTransferRequest) beginExecution() bool {
	return r != nil && r.state.CompareAndSwap(strictLeaderTransferProcessing, strictLeaderTransferExecuting)
}

type logCompactionRequest struct {
	ctx  context.Context
	resp chan logCompactionResponse
}

type logCompactionResponse struct {
	result LogCompactionResult
	err    error
}

type applyStateEvent struct {
	observer     ApplyStateObserver
	slotID       SlotID
	commitIndex  uint64
	appliedIndex uint64
}

func (e applyStateEvent) emit() {
	if e.observer != nil {
		e.observer.SetSlotApplyState(e.slotID, e.commitIndex, e.appliedIndex)
	}
}

type leaderChangeEvent struct {
	observer      LeaderChangeObserver
	causeObserver LeaderChangeCauseObserver
	slotID        SlotID
	from          NodeID
	to            NodeID
	cause         LeaderChangeCause
}

func (e leaderChangeEvent) emit() {
	if e.from == 0 || e.to == 0 || e.from == e.to {
		return
	}
	if e.cause == "" {
		e.cause = LeaderChangeCauseElection
	}
	if e.causeObserver != nil {
		e.causeObserver.ObserveSlotLeaderChangeWithCause(e.slotID, e.from, e.to, e.cause)
		return
	}
	if e.observer != nil {
		e.observer.ObserveSlotLeaderChange(e.slotID, e.from, e.to)
	}
}

type proposalAdmissionEvent struct {
	observer ProposalAdmissionObserver
	slotID   SlotID
	class    ProposalClass
	result   string
}

func (e proposalAdmissionEvent) emit() {
	if e.observer != nil {
		e.observer.ObserveSlotProposalAdmission(e.slotID, e.class, e.result)
	}
}

func newSlot(ctx context.Context, nodeID NodeID, logger wklog.Logger, raftOpts RaftOptions, opts SlotOptions, observer SchedulerObserver, apply *applyPipeline) (*slot, error) {
	raftOpts = NormalizeRaftOptions(raftOpts)
	state, snapshot, memory, err := newStorageAdapter(opts.Storage).load(ctx)
	if err != nil {
		return nil, err
	}

	appliedIndex := state.AppliedIndex
	if !raft.IsEmptySnap(snapshot) {
		appliedIndex = snapshot.Metadata.Index
	}
	snapshotData, snapshotConfigAppliedIndex, err := decodeSlotSnapshotData(snapshot.Data)
	if err != nil {
		return nil, err
	}
	configAppliedIndex, err := restoreConfigAppliedIndex(memory, appliedIndex, state.ConfigAppliedIndex, snapshotConfigAppliedIndex)
	if err != nil {
		return nil, err
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
			// ConfigAppliedIndex is restored from durable metadata or already-loaded applied config entries.
			ConfigAppliedIndex: configAppliedIndex,
		},
		logger:                      logger,
		storageView:                 newStorageAdapter(opts.Storage),
		apply:                       apply,
		rawNode:                     rawNode,
		durableAppliedIndex:         appliedIndex,
		durableConfigAppliedIndex:   configAppliedIndex,
		compactor:                   newLogCompactor(raftOpts.LogCompaction, snapshot.Metadata.Index),
		maxQueuedRequests:           raftOpts.MaxQueuedRequests,
		maxQueuedControls:           raftOpts.MaxQueuedControls,
		maxQueuedBackgroundControls: raftOpts.MaxQueuedBackgroundControls,
		maxApplyingTasks:            raftOpts.MaxApplyingTasks,
	}
	g.cond = sync.NewCond(&g.mu)
	g.storageView.memory = memory
	if !raft.IsEmptySnap(snapshot) {
		if err := g.stateMachine.Restore(ctx, Snapshot{
			Index: snapshot.Metadata.Index,
			Term:  snapshot.Metadata.Term,
			Data:  snapshotData,
		}); err != nil {
			return nil, err
		}
	}
	g.refreshStatus()
	return g, nil
}

func (g *slot) enqueueRequest(msg raftpb.Message) error {
	g.mu.Lock()
	if err := g.admissionErrLocked(); err != nil {
		g.mu.Unlock()
		return err
	}
	if queueLimitReached(g.maxQueuedRequests, len(g.requests)) {
		g.mu.Unlock()
		return ErrSlotBusy
	}
	leaderEvent := g.observeQueuedMessageLocked(msg)
	g.requests = append(g.requests, msg)
	g.mu.Unlock()
	leaderEvent.emit()
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
	if err := g.admissionErrLocked(); err != nil {
		g.mu.Unlock()
		return err
	}
	action.proposalClass = normalizeProposalClass(action.proposalClass)
	switch action.kind {
	case controlPropose, controlConfigChange:
		if g.status.Role != RoleLeader {
			event := g.proposalAdmissionEventLocked(action, "not_leader")
			g.mu.Unlock()
			event.emit()
			return ErrNotLeader
		}
		if action.kind == controlConfigChange && g.hasPendingConfigChangeLocked() {
			g.mu.Unlock()
			return ErrConfigChangePending
		}
	}
	if queueLimitReached(g.maxQueuedControls, len(g.controls)) {
		event := g.proposalAdmissionEventLocked(action, "busy")
		g.mu.Unlock()
		event.emit()
		if action.kind == controlPropose {
			return fmt.Errorf("%w: %w", ErrProposalBackpressure, ErrSlotBusy)
		}
		return ErrSlotBusy
	}
	if action.kind == controlPropose &&
		action.proposalClass == ProposalClassBackground &&
		queueLimitReached(g.maxQueuedBackgroundControls, g.queuedBackgroundControls) {
		event := g.proposalAdmissionEventLocked(action, "throttled")
		g.mu.Unlock()
		event.emit()
		return fmt.Errorf("%w: %w", ErrBackgroundProposalThrottled, ErrProposalBackpressure)
	}
	if action.kind == controlPropose && action.proposalClass == ProposalClassBackground {
		g.queuedBackgroundControls++
	}
	g.controls = append(g.controls, action)
	event := g.proposalAdmissionEventLocked(action, "ok")
	g.mu.Unlock()
	event.emit()
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
			if action.strictTransfer != nil {
				request := action.strictTransfer
				if !request.claim() {
					continue
				}
				if err := request.ctx.Err(); err != nil {
					request.finish(strictLeaderTransferResponse{err: err})
					continue
				}
				target, decision := selectStrictLeaderTransferTarget(
					g.rawNode.Status(),
					request.expectedLeader,
					request.expectedTerm,
					request.expectedVoters,
					request.target,
				)
				if target == 0 {
					request.finish(strictLeaderTransferResponse{decision: decision})
					continue
				}
				if err := request.ctx.Err(); err != nil {
					request.finish(strictLeaderTransferResponse{err: err})
					continue
				}
				g.executeStrictLeaderTransfer(request, target)
				continue
			}
			target := selectLeaderTransferTransferee(g.rawNode.Status(), action.target)
			if target != 0 {
				g.expectLeaderTransfer(target)
				g.rawNode.TransferLeader(uint64(target))
			}
		case controlCompactLog:
			if action.compact == nil {
				continue
			}
			if err := action.compact.ctx.Err(); err != nil {
				action.compact.resp <- logCompactionResponse{err: err}
				continue
			}
			if err := g.waitApplyIdle(action.compact.ctx); err != nil {
				action.compact.resp <- logCompactionResponse{err: err}
				continue
			}
			if err := g.currentErr(); err != nil {
				action.compact.resp <- logCompactionResponse{err: err}
				continue
			}
			applied := g.appliedIndex()
			result, err := g.compactLogManually(action.compact.ctx, applied)
			action.compact.resp <- logCompactionResponse{result: result, err: err}
		}
	}
	return len(controls) > 0
}

func (g *slot) executeStrictLeaderTransfer(request *strictLeaderTransferRequest, target NodeID) {
	if g == nil || request == nil || target == 0 {
		return
	}
	started := false
	var terminalErr error
	current := request.guard != nil && request.guard.ExecuteIfCurrent(func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		if err := g.admissionErrLocked(); err != nil {
			terminalErr = err
			return
		}
		if request.ctx.Err() != nil || !request.beginExecution() {
			return
		}
		g.expectLeaderTransferLocked(target)
		g.rawNode.TransferLeader(uint64(target))
		started = true
	})
	if !current {
		request.finish(strictLeaderTransferResponse{decision: PreferredLeaderTransferStaleIntent})
		return
	}
	if terminalErr != nil {
		request.finish(strictLeaderTransferResponse{err: terminalErr})
		return
	}
	if started {
		request.finish(strictLeaderTransferResponse{decision: PreferredLeaderTransferStarted})
		return
	}
	if err := request.ctx.Err(); err != nil {
		request.finish(strictLeaderTransferResponse{err: err})
		return
	}
	request.finish(strictLeaderTransferResponse{decision: PreferredLeaderTransferStaleIntent})
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
	clear(batch)
	g.requestWorkBuf = batch[:0]
}

func (g *slot) takeControlBatch() []controlAction {
	g.mu.Lock()
	defer g.mu.Unlock()

	batch := g.controls
	g.queuedBackgroundControls = 0
	g.controls = g.controlWorkBuf[:0]
	g.controlWorkBuf = nil
	return batch
}

func (g *slot) releaseControlBatch(batch []controlAction) {
	g.mu.Lock()
	defer g.mu.Unlock()
	clear(batch)
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
	clear(buf)
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
	persist, err := g.storageView.persistReadyDurable(ctx, ready)
	if err != nil {
		g.failPending(err)
		return true, false
	}
	requiresSyncApply := readyRequiresSynchronousApply(ready)
	if requiresSyncApply {
		if err := g.waitApplyIdle(ctx); err != nil {
			return true, false
		}
		if err := g.currentErr(); err != nil {
			return true, false
		}
	}
	if err := g.storageView.applyReadyToMemory(persist); err != nil {
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
		clear(g.transportBuf)
		g.transportBuf = g.transportBuf[:0]
	}

	if requiresSyncApply {
		return g.processReadySynchronously(ctx, ready)
	}
	return g.processReadyAsyncNormal(ctx, ready)
}

func (g *slot) processReadySynchronously(ctx context.Context, ready raft.Ready) (bool, bool) {
	if err := g.waitApplyIdle(ctx); err != nil {
		return true, false
	}
	if !g.shouldProcess() {
		return true, false
	}

	lastApplied := g.appliedIndex()
	appliedBeforeReady := lastApplied
	resolutions := g.takeResolutionBuffer()
	defer func() {
		g.releaseResolutionBuffer(resolutions)
	}()
	if !raft.IsEmptySnap(ready.Snapshot) {
		snapshotData, snapshotConfigAppliedIndex, err := decodeSlotSnapshotData(ready.Snapshot.Data)
		if err != nil {
			g.fail(err)
			return true, false
		}
		if err := g.stateMachine.Restore(ctx, Snapshot{
			Index: ready.Snapshot.Metadata.Index,
			Term:  ready.Snapshot.Metadata.Term,
			Data:  snapshotData,
		}); err != nil {
			g.fail(err)
			return true, false
		}
		g.recordConfigChangeApplied(snapshotConfigAppliedIndex)
		lastApplied = ready.Snapshot.Metadata.Index
	}

	batchSM, canBatch := g.stateMachine.(BatchStateMachine)
	var configChanged bool
	resolutions, configChanged = g.applyCommittedEntries(ctx, ready.CommittedEntries, &lastApplied, resolutions, batchSM, canBatch)
	if g.hasFatalErr() {
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
		if err := g.persistConfigAppliedIndex(ctx, lastApplied); err != nil {
			g.fail(err)
			return true, false
		}
		g.setDurableAppliedIndex(lastApplied)
	}

	g.rawNode.Advance(ready)
	g.refreshStatus()
	g.completeResolutions(resolutions)
	requeue := g.rawNode.HasReady()
	if g.takeCampaignAfterReady() {
		_ = g.rawNode.Campaign()
		requeue = true
	}
	// Refresh snapshots after membership changes so future learners can restore
	// a snapshot whose ConfState includes the latest peer set.
	if g.compactor.shouldCompact(lastApplied) || (configChanged && g.compactor.shouldRefreshAfterConfigChange(lastApplied)) {
		if err := g.compactLog(ctx, lastApplied); err != nil {
			g.logCompactionWarning(err, lastApplied)
		} else {
			g.compactor.recordSnapshot(lastApplied)
		}
	}
	return true, requeue || g.rawNode.HasReady()
}

func (g *slot) processReadyAsyncNormal(ctx context.Context, ready raft.Ready) (bool, bool) {
	if len(ready.CommittedEntries) == 0 {
		g.rawNode.Advance(ready)
		g.refreshStatus()
		return true, g.rawNode.HasReady()
	}
	if g.apply == nil {
		return g.processReadySynchronously(ctx, ready)
	}

	task := applyTask{
		slot:          g,
		entries:       cloneEntries(ready.CommittedEntries),
		appliedBefore: g.appliedIndex(),
	}
	if err := g.apply.enqueue(task); err != nil {
		if errors.Is(err, ErrSlotBusy) {
			return g.processReadySynchronously(ctx, ready)
		}
		g.fail(err)
		return true, false
	}
	g.rawNode.Advance(ready)
	g.refreshStatus()
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
					Data:     data,
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
				Data:     data,
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
			g.recordConfigChangeApplied(entry.Index)
			resolutions = append(resolutions, futureResolution{
				kind:  controlConfigChange,
				index: entry.Index,
				term:  entry.Term,
				result: Result{
					Index: entry.Index,
					Term:  entry.Term,
				},
			})
		case raftpb.EntryConfChangeV2:
			// Flush pending normal entries before processing conf change.
			if !flushBatch() {
				return resolutions, configChanged
			}
			var cc raftpb.ConfChangeV2
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
			g.recordConfigChangeApplied(entry.Index)
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
	return !g.votersInitialized || g.votersDirty || g.needsLearnerProgressRefreshLocked()
}

// refreshBasicStatus updates volatile Raft status without cloning tracker progress.
func (g *slot) refreshBasicStatus() {
	st := g.rawNode.BasicStatus()
	g.mu.Lock()
	g.basicStatusRefreshCount++
	leaderEvent, applyEvent := g.applyBasicStatusLocked(st)
	g.mu.Unlock()
	leaderEvent.emit()
	applyEvent.emit()
}

// refreshFullStatus updates voter membership and basic status from RawNode.Status.
func (g *slot) refreshFullStatus() {
	st := g.rawNode.Status()
	g.mu.Lock()
	g.fullStatusRefreshCount++
	g.votersInitialized = true
	g.votersDirty = false
	g.status.CurrentVoters = currentVotersFromRaftStatus(st)
	g.status.CurrentLearners = currentLearnersFromRaftStatus(st)
	g.status.ConfState = confStateFromRaftStatus(st)
	g.status.Progress = progressFromRaftStatus(st)
	leaderEvent, applyEvent := g.applyBasicStatusLocked(st.BasicStatus)
	g.mu.Unlock()
	leaderEvent.emit()
	applyEvent.emit()
}

func (g *slot) refreshDurableAppliedStatus() {
	g.mu.Lock()
	g.status.AppliedIndex = g.durableAppliedIndex
	applyEvent := g.applyStateEventLocked(g.status.CommitIndex, g.durableAppliedIndex)
	g.mu.Unlock()
	applyEvent.emit()
}

func (g *slot) applyBasicStatusLocked(st raft.BasicStatus) (leaderChangeEvent, applyStateEvent) {
	prevRole := g.status.Role
	nextRole := mapRole(st.RaftState)
	nextLeader := NodeID(st.Lead)
	if nextLeader == 0 && nextRole == RoleLeader {
		nextLeader = g.status.NodeID
	}
	leaderEvent := g.setLeaderIDLocked(nextLeader)
	g.status.Term = st.Term
	g.status.CommitIndex = st.Commit
	g.status.AppliedIndex = g.durableAppliedIndex
	g.status.Role = nextRole
	applyEvent := g.applyStateEventLocked(st.Commit, g.durableAppliedIndex)
	if prevRole == RoleLeader && g.status.Role != RoleLeader {
		g.failLeadershipDependentLocked(ErrNotLeader)
	}
	return leaderEvent, applyEvent
}

func (g *slot) needsLearnerProgressRefreshLocked() bool {
	return g.status.Role == RoleLeader && len(g.status.CurrentLearners) > 0
}

func (g *slot) recordConfigChangeApplied(index uint64) {
	if index == 0 {
		return
	}
	g.mu.Lock()
	if index > g.status.ConfigAppliedIndex {
		g.status.ConfigAppliedIndex = index
	}
	g.votersDirty = true
	g.mu.Unlock()
}

func (g *slot) requestCampaignAfterReady() {
	g.mu.Lock()
	g.campaignAfterReady = true
	g.mu.Unlock()
}

func (g *slot) takeCampaignAfterReady() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.campaignAfterReady {
		return false
	}
	g.campaignAfterReady = false
	return true
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

func currentLearnersFromRaftStatus(st raft.Status) []NodeID {
	if len(st.Config.Learners) == 0 {
		return nil
	}
	learners := make([]NodeID, 0, len(st.Config.Learners))
	for id := range st.Config.Learners {
		learners = append(learners, NodeID(id))
	}
	sort.Slice(learners, func(i, j int) bool {
		return learners[i] < learners[j]
	})
	return learners
}

func confStateFromRaftStatus(st raft.Status) raftpb.ConfState {
	return raftpb.ConfState{
		Voters:         sortedRaftIDs(st.Config.Voters[0]),
		VotersOutgoing: sortedRaftIDs(st.Config.Voters[1]),
		Learners:       sortedRaftIDs(st.Config.Learners),
		LearnersNext:   sortedRaftIDs(st.Config.LearnersNext),
		AutoLeave:      st.Config.AutoLeave,
	}
}

func sortedRaftIDs(ids map[uint64]struct{}) []uint64 {
	if len(ids) == 0 {
		return nil
	}
	out := make([]uint64, 0, len(ids))
	for id := range ids {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i] < out[j]
	})
	return out
}

func progressFromRaftStatus(st raft.Status) map[NodeID]PeerProgress {
	if len(st.Progress) == 0 {
		return nil
	}
	progress := make(map[NodeID]PeerProgress, len(st.Progress))
	for id, peer := range st.Progress {
		progress[NodeID(id)] = PeerProgress{
			Match: peer.Match,
			Next:  peer.Next,
			State: peer.State.String(),
		}
	}
	return progress
}

func restoreConfigAppliedIndex(memory *loadedMemoryStorage, appliedIndex uint64, durableConfigAppliedIndex uint64, snapshotConfigAppliedIndex uint64) (uint64, error) {
	if appliedIndex == 0 {
		return 0, nil
	}
	first, err := memory.FirstIndex()
	if err != nil {
		return 0, err
	}
	last, err := memory.LastIndex()
	if err != nil {
		return 0, err
	}
	if last > appliedIndex {
		last = appliedIndex
	}
	if last >= first {
		entries, err := memory.Entries(first, last+1, maxSizePerMsg(0))
		if err != nil {
			if !errors.Is(err, raft.ErrCompacted) && !errors.Is(err, raft.ErrUnavailable) {
				return 0, err
			}
		} else {
			for i := len(entries) - 1; i >= 0; i-- {
				if isConfigChangeEntry(entries[i]) {
					return entries[i].Index, nil
				}
			}
		}
	}
	return latestValidConfigAppliedIndex(appliedIndex, durableConfigAppliedIndex, snapshotConfigAppliedIndex), nil
}

func isConfigChangeEntry(entry raftpb.Entry) bool {
	return entry.Type == raftpb.EntryConfChange || entry.Type == raftpb.EntryConfChangeV2
}

func latestValidConfigAppliedIndex(appliedIndex uint64, indexes ...uint64) uint64 {
	var latest uint64
	for _, index := range indexes {
		if index == 0 || index > appliedIndex || index <= latest {
			continue
		}
		latest = index
	}
	return latest
}

const (
	slotSnapshotDataMagic           = "WKSLOTSN"
	slotSnapshotDataVersion    byte = 1
	slotSnapshotDataHeaderSize      = len(slotSnapshotDataMagic) + 1 + 8
)

func encodeSlotSnapshotData(payload []byte, configAppliedIndex uint64) []byte {
	out := make([]byte, 0, slotSnapshotDataHeaderSize+len(payload))
	out = append(out, slotSnapshotDataMagic...)
	out = append(out, slotSnapshotDataVersion)
	out = binary.BigEndian.AppendUint64(out, configAppliedIndex)
	out = append(out, payload...)
	return out
}

func decodeSlotSnapshotData(data []byte) ([]byte, uint64, error) {
	if !bytes.HasPrefix(data, []byte(slotSnapshotDataMagic)) {
		return append([]byte(nil), data...), 0, nil
	}
	if len(data) < slotSnapshotDataHeaderSize {
		return nil, 0, fmt.Errorf("slot snapshot data envelope too short: %d", len(data))
	}
	version := data[len(slotSnapshotDataMagic)]
	if version != slotSnapshotDataVersion {
		return nil, 0, fmt.Errorf("slot snapshot data envelope version %d unsupported", version)
	}
	configAppliedIndex := binary.BigEndian.Uint64(data[len(slotSnapshotDataMagic)+1 : slotSnapshotDataHeaderSize])
	return append([]byte(nil), data[slotSnapshotDataHeaderSize:]...), configAppliedIndex, nil
}

func selectLeaderTransferTransferee(st raft.Status, preferred NodeID) NodeID {
	voters := st.Config.Voters.IDs()
	if len(voters) == 0 {
		return 0
	}
	lead := st.Lead
	requiredMatch := st.Commit
	isEligible := func(id uint64) bool {
		if id == 0 || id == lead {
			return false
		}
		if _, ok := voters[id]; !ok {
			return false
		}
		progress, ok := st.Progress[id]
		return ok && progress.Match >= requiredMatch
	}
	if isEligible(uint64(preferred)) {
		return preferred
	}

	var selected uint64
	var selectedMatch uint64
	for id := range voters {
		if !isEligible(id) {
			continue
		}
		match := st.Progress[id].Match
		if selected == 0 || match > selectedMatch || (match == selectedMatch && id < selected) {
			selected = id
			selectedMatch = match
		}
	}
	return NodeID(selected)
}

// selectStrictLeaderTransferTarget returns preferred only when the caller's
// leader fence still matches and preferred is a caught-up voter. Unlike the
// operator-facing advisory transfer path, it never falls back to another
// voter; steady-state placement reconciliation must retain the valid current
// leader when the preferred voter cannot safely lead yet.
func selectStrictLeaderTransferTarget(st raft.Status, expectedLeader NodeID, expectedTerm uint64, expectedVoters []NodeID, preferred NodeID) (NodeID, PreferredLeaderTransferDecision) {
	if expectedLeader == 0 || preferred == 0 || preferred == expectedLeader ||
		st.ID != uint64(expectedLeader) || st.Lead != uint64(expectedLeader) ||
		st.Term != expectedTerm || st.RaftState != raft.StateLeader {
		return 0, PreferredLeaderTransferStaleIntent
	}
	if st.LeadTransferee != 0 {
		return 0, PreferredLeaderTransferInProgress
	}
	if len(st.Config.Voters[1]) != 0 {
		return 0, PreferredLeaderTransferJointConfig
	}
	currentVoters := st.Config.Voters[0]
	if len(currentVoters) != len(expectedVoters) {
		return 0, PreferredLeaderTransferVoterMismatch
	}
	for i, voter := range expectedVoters {
		if voter == 0 {
			return 0, PreferredLeaderTransferVoterMismatch
		}
		for _, previous := range expectedVoters[:i] {
			if previous == voter {
				return 0, PreferredLeaderTransferVoterMismatch
			}
		}
		if _, ok := currentVoters[uint64(voter)]; !ok {
			return 0, PreferredLeaderTransferVoterMismatch
		}
	}
	if _, ok := st.Config.Voters.IDs()[uint64(preferred)]; !ok {
		return 0, PreferredLeaderTransferVoterMismatch
	}
	progress, ok := st.Progress[uint64(preferred)]
	if !ok || !progress.RecentActive {
		return 0, PreferredLeaderTransferPreferredInactive
	}
	if progress.Match < st.Commit {
		return 0, PreferredLeaderTransferPreferredLagging
	}
	return preferred, PreferredLeaderTransferStarted
}

func (g *slot) appliedIndex() uint64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.durableAppliedIndex
}

func (g *slot) setDurableAppliedIndex(index uint64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if index > g.durableAppliedIndex {
		g.durableAppliedIndex = index
	}
	g.status.AppliedIndex = g.durableAppliedIndex
}

func (g *slot) pendingConfigAppliedIndex(lastApplied uint64) uint64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	index := g.status.ConfigAppliedIndex
	if index == 0 || index > lastApplied || index <= g.durableConfigAppliedIndex {
		return 0
	}
	return index
}

func (g *slot) configAppliedIndexForSnapshot(applied uint64) uint64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	index := g.status.ConfigAppliedIndex
	if index == 0 || index > applied {
		return 0
	}
	return index
}

func (g *slot) setDurableConfigAppliedIndex(index uint64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if index > g.durableConfigAppliedIndex {
		g.durableConfigAppliedIndex = index
	}
}

func (g *slot) persistConfigAppliedIndex(ctx context.Context, lastApplied uint64) error {
	index := g.pendingConfigAppliedIndex(lastApplied)
	if index == 0 {
		return nil
	}
	if storage, ok := g.storage.(ConfigAppliedIndexStorage); ok {
		if err := storage.MarkConfigApplied(ctx, index); err != nil {
			return err
		}
	}
	g.setDurableConfigAppliedIndex(index)
	return nil
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
	status.CurrentLearners = append([]NodeID(nil), g.status.CurrentLearners...)
	status.ConfState = cloneConfState(g.status.ConfState)
	if len(g.status.Progress) > 0 {
		status.Progress = make(map[NodeID]PeerProgress, len(g.status.Progress))
		for id, progress := range g.status.Progress {
			status.Progress[id] = progress
		}
	}
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

func (g *slot) observeQueuedMessageLocked(msg raftpb.Message) leaderChangeEvent {
	if msg.Term <= g.status.Term {
		return leaderChangeEvent{}
	}

	g.status.Term = msg.Term
	g.status.Role = RoleFollower

	switch msg.Type {
	case raftpb.MsgApp, raftpb.MsgHeartbeat, raftpb.MsgSnap:
		return g.setLeaderIDLocked(NodeID(msg.From))
	default:
		return g.setLeaderIDLocked(0)
	}
}

func (g *slot) setLeaderIDLocked(next NodeID) leaderChangeEvent {
	prev := g.status.LeaderID
	g.status.LeaderID = next
	eventFrom := prev
	if next != 0 {
		if eventFrom == 0 {
			eventFrom = g.lastKnownLeaderID
		}
		g.lastKnownLeaderID = next
	}
	cause := g.leaderChangeCauseLocked(eventFrom, next)
	observer, _ := g.observer.(LeaderChangeObserver)
	causeObserver, _ := g.observer.(LeaderChangeCauseObserver)
	return leaderChangeEvent{
		observer:      observer,
		causeObserver: causeObserver,
		slotID:        g.id,
		from:          eventFrom,
		to:            next,
		cause:         cause,
	}
}

func (g *slot) expectLeaderTransfer(target NodeID) {
	if target == 0 {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.expectLeaderTransferLocked(target)
}

func (g *slot) expectLeaderTransferLocked(target NodeID) {
	if target == 0 {
		return
	}
	g.pendingLeaderTransferTarget = target
}

func (g *slot) leaderChangeCauseLocked(from, to NodeID) LeaderChangeCause {
	cause := LeaderChangeCauseElection
	if to == 0 || from == to {
		return cause
	}
	if g.pendingLeaderTransferTarget == to {
		if from != 0 {
			cause = LeaderChangeCausePlannedTransfer
		}
		g.pendingLeaderTransferTarget = 0
		return cause
	}
	if from != 0 {
		g.pendingLeaderTransferTarget = 0
	}
	return cause
}

func (g *slot) applyStateEventLocked(commitIndex, appliedIndex uint64) applyStateEvent {
	observer, _ := g.observer.(ApplyStateObserver)
	return applyStateEvent{
		observer:     observer,
		slotID:       g.id,
		commitIndex:  commitIndex,
		appliedIndex: appliedIndex,
	}
}

func (g *slot) proposalAdmissionEventLocked(action controlAction, result string) proposalAdmissionEvent {
	if action.kind != controlPropose {
		return proposalAdmissionEvent{}
	}
	observer, _ := g.observer.(ProposalAdmissionObserver)
	return proposalAdmissionEvent{
		observer: observer,
		slotID:   g.id,
		class:    action.proposalClass,
		result:   result,
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

func (g *slot) beginApply() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.admissionErrLocked(); err != nil {
		return err
	}
	if queueLimitReached(g.maxApplyingTasks, g.applying) {
		return ErrSlotBusy
	}
	g.applying++
	return nil
}

func (g *slot) finishApply() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.applying > 0 {
		g.applying--
	}
	if g.cond != nil {
		g.cond.Broadcast()
	}
}

func (g *slot) waitIdleLocked() {
	for g.processing || g.applying > 0 {
		g.cond.Wait()
	}
}

func (g *slot) waitApplyIdle(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	var stop chan struct{}
	if done := ctx.Done(); done != nil {
		stop = make(chan struct{})
		go func() {
			select {
			case <-done:
				g.mu.Lock()
				if g.cond != nil {
					g.cond.Broadcast()
				}
				g.mu.Unlock()
			case <-stop:
			}
		}()
		defer close(stop)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for g.applying > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		g.cond.Wait()
	}
	return ctx.Err()
}

func (g *slot) currentErr() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.admissionErrLocked()
}

func (g *slot) hasFatalErr() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.fatalErr != nil
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
		case raftpb.EntryConfChangeV2:
			configCount++
		}
	}
	return proposalCount, configCount
}

func readyRequiresSynchronousApply(ready raft.Ready) bool {
	if !raft.IsEmptySnap(ready.Snapshot) {
		return true
	}
	for _, entry := range ready.CommittedEntries {
		if entry.Type == raftpb.EntryConfChange || entry.Type == raftpb.EntryConfChangeV2 {
			return true
		}
	}
	return false
}

func cloneEntries(entries []raftpb.Entry) []raftpb.Entry {
	if len(entries) == 0 {
		return nil
	}
	out := make([]raftpb.Entry, len(entries))
	for i, entry := range entries {
		out[i] = cloneEntry(entry, true)
	}
	return out
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
			g.submittedProposals[0] = nil
			g.submittedProposals = g.submittedProposals[1:]
		case raftpb.EntryConfChange, raftpb.EntryConfChangeV2:
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
			g.submittedConfigs[0] = nil
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
	g.mu.Unlock()
	g.observeSlotProposal(fut)
	if fut != nil {
		fut.resolve(result, err)
	}
}

func (g *slot) failPending(err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.failPendingLocked(err)
}

func (g *slot) failPendingLocked(err error) {
	for i := range g.controls {
		if g.controls[i].strictTransfer != nil {
			g.controls[i].strictTransfer.cancel(err)
		}
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

func queueLimitReached(limit, current int) bool {
	return limit > 0 && current >= limit
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
