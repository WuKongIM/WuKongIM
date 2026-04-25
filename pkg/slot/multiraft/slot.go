package multiraft

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	raft "go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
)

type slot struct {
	mu                 sync.Mutex
	id                 SlotID
	storage            Storage
	stateMachine       StateMachine
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
}

type trackedFuture struct {
	future *future
	term   uint64
}

type futureResolution struct {
	kind   controlKind
	index  uint64
	term   uint64
	result Result
	err    error
}

type controlKind uint8

const (
	controlPropose controlKind = iota + 1
	controlCampaign
	controlConfigChange
	controlTransferLeader
)

type controlAction struct {
	kind   controlKind
	data   []byte
	future *future
	target NodeID
	change ConfigChange
}

func newSlot(ctx context.Context, nodeID NodeID, logger wklog.Logger, raftOpts RaftOptions, opts SlotOptions) (*slot, error) {
	state, snapshot, memory, err := newStorageAdapter(opts.Storage).load(ctx)
	if err != nil {
		return nil, err
	}

	rawNode, err := raft.NewRawNode(&raft.Config{
		ID:              uint64(nodeID),
		ElectionTick:    raftOpts.ElectionTick,
		HeartbeatTick:   raftOpts.HeartbeatTick,
		Storage:         memory,
		Applied:         state.AppliedIndex,
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
		status: Status{
			SlotID:       opts.ID,
			NodeID:       nodeID,
			LeaderID:     NodeID(state.HardState.Vote),
			CommitIndex:  state.HardState.Commit,
			AppliedIndex: state.AppliedIndex,
		},
		storageView: newStorageAdapter(opts.Storage),
		rawNode:     rawNode,
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

func (g *slot) processRequests() {
	requests := g.takeRequestBatch()
	defer g.releaseRequestBatch(requests)

	for _, msg := range requests {
		_ = g.rawNode.Step(msg)
	}
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

func (g *slot) processControls() {
	controls := g.takeControlBatch()
	defer g.releaseControlBatch(controls)

	for _, action := range controls {
		switch action.kind {
		case controlPropose:
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
		}
	}
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

func (g *slot) processTick() {
	g.mu.Lock()
	defer g.mu.Unlock()

	if !g.tickPending {
		return
	}
	g.tickPending = false
	g.tickCount++
	g.rawNode.Tick()
}

func (g *slot) processReady(ctx context.Context, transport Transport) bool {
	if !g.rawNode.HasReady() {
		return false
	}

	ready := g.rawNode.Ready()
	if err := g.storageView.persistReady(ctx, ready); err != nil {
		g.failPending(err)
		return false
	}
	proposalCount, configCount := countTrackedReadyEntries(ready.Entries)
	g.ensurePendingProposalCapacity(proposalCount)
	g.ensurePendingConfigCapacity(configCount)
	g.trackReadyEntries(ready.Entries)

	if len(ready.Messages) > 0 {
		g.transportBuf = wrapMessagesInto(g.transportBuf[:0], g.id, ready.Messages)
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
			return false
		}
		lastApplied = ready.Snapshot.Metadata.Index
	}

	batchSM, canBatch := g.stateMachine.(BatchStateMachine)
	resolutions = g.applyCommittedEntries(ctx, ready.CommittedEntries, &lastApplied, resolutions, batchSM, canBatch)
	if g.fatalErr != nil {
		return false
	}

	if lastApplied > appliedBeforeReady {
		if err := g.storage.MarkApplied(ctx, lastApplied); err != nil {
			g.fail(err)
			return false
		}
	}

	g.rawNode.Advance(ready)
	g.refreshStatus()
	g.completeResolutions(resolutions)
	return g.rawNode.HasReady()
}

func (g *slot) applyCommittedEntries(
	ctx context.Context,
	entries []raftpb.Entry,
	lastApplied *uint64,
	resolutions []futureResolution,
	batchSM BatchStateMachine,
	canBatch bool,
) []futureResolution {
	// Collect contiguous normal entries for batched apply.
	var batchEntries []raftpb.Entry

	flushBatch := func() bool {
		if len(batchEntries) == 0 {
			return true
		}
		defer func() { batchEntries = batchEntries[:0] }()

		if !canBatch || len(batchEntries) == 1 {
			// Fall back to one-by-one Apply.
			for _, entry := range batchEntries {
				hashSlot, data, err := decodeProposalPayload(entry.Data)
				if err != nil {
					g.resolveProposal(entry.Index, entry.Term, Result{
						Index: entry.Index,
						Term:  entry.Term,
					}, err)
					g.fail(err)
					return false
				}
				result, err := g.stateMachine.Apply(ctx, Command{
					SlotID:   g.id,
					HashSlot: hashSlot,
					Index:    entry.Index,
					Term:     entry.Term,
					Data:     append([]byte(nil), data...),
				})
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
					kind:  controlPropose,
					index: entry.Index,
					term:  entry.Term,
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
		results, err := batchSM.ApplyBatch(ctx, cmds)
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
				kind:  controlPropose,
				index: entry.Index,
				term:  entry.Term,
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
				return resolutions
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
			g.rawNode.ApplyConfChange(cc)
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
	return resolutions
}

func decodeProposalPayload(data []byte) (uint16, []byte, error) {
	if len(data) < 2 {
		return 0, nil, fmt.Errorf("proposal payload too short: %d", len(data))
	}
	return binary.BigEndian.Uint16(data[:2]), data[2:], nil
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

func (g *slot) refreshStatus() {
	st := g.rawNode.Status()
	g.mu.Lock()
	defer g.mu.Unlock()
	prevRole := g.status.Role
	g.status.LeaderID = NodeID(st.Lead)
	g.status.Term = st.Term
	g.status.CommitIndex = st.Commit
	g.status.AppliedIndex = st.Applied
	g.status.Role = mapRole(st.RaftState)
	if prevRole == RoleLeader && g.status.Role != RoleLeader {
		g.failLeadershipDependentLocked(ErrNotLeader)
	}
}

func (g *slot) appliedIndex() uint64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.status.AppliedIndex
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
	return g.status, nil
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
		g.status.LeaderID = NodeID(msg.From)
	default:
		g.status.LeaderID = 0
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
	defer g.mu.Unlock()

	pending, ok := g.pendingProposals[index]
	if !ok || pending.term != term {
		return
	}
	delete(g.pendingProposals, index)
	pending.future.resolve(result, err)
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

func wrapMessages(slotID SlotID, messages []raftpb.Message) []Envelope {
	return wrapMessagesInto(nil, slotID, messages)
}

func wrapMessagesInto(dst []Envelope, slotID SlotID, messages []raftpb.Message) []Envelope {
	out := dst[:0]
	for _, msg := range messages {
		out = append(out, Envelope{
			SlotID:  slotID,
			Message: cloneMessage(msg),
		})
	}
	return out
}

func cloneMessage(msg raftpb.Message) raftpb.Message {
	cloned := msg
	if len(msg.Context) > 0 {
		cloned.Context = append([]byte(nil), msg.Context...)
	}
	if len(msg.Entries) > 0 {
		cloned.Entries = make([]raftpb.Entry, len(msg.Entries))
		for i, entry := range msg.Entries {
			cloned.Entries[i] = cloneEntry(entry)
		}
	}
	if msg.Snapshot != nil {
		snap := cloneSnapshot(*msg.Snapshot)
		cloned.Snapshot = &snap
	}
	if len(msg.Responses) > 0 {
		cloned.Responses = make([]raftpb.Message, len(msg.Responses))
		for i, response := range msg.Responses {
			cloned.Responses[i] = cloneMessage(response)
		}
	}
	return cloned
}

func cloneEntry(entry raftpb.Entry) raftpb.Entry {
	cloned := entry
	if len(entry.Data) > 0 {
		cloned.Data = append([]byte(nil), entry.Data...)
	}
	return cloned
}

func cloneSnapshot(snapshot raftpb.Snapshot) raftpb.Snapshot {
	cloned := snapshot
	if len(snapshot.Data) > 0 {
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
