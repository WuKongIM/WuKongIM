package multiraft

import (
	"context"
	"errors"
	"time"

	raft "go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
)

const (
	// LogCompactionSkippedDisabled reports that local Slot Raft compaction is disabled.
	LogCompactionSkippedDisabled = "disabled"
	// LogCompactionSkippedNoAppliedIndex reports that no local applied entry can be snapshotted yet.
	LogCompactionSkippedNoAppliedIndex = "no_applied_index"
	// LogCompactionSkippedUpToDate reports that the latest local snapshot already covers applied entries.
	LogCompactionSkippedUpToDate = "up_to_date"
)

// LogCompactionResult describes one manual Slot Raft log compaction attempt.
type LogCompactionResult struct {
	// NodeID is the local Slot Raft node ID that handled the attempt.
	NodeID NodeID
	// SlotID is the local Raft group that handled the attempt.
	SlotID SlotID
	// AppliedIndex is the applied index used as the manual compaction target.
	AppliedIndex uint64
	// BeforeSnapshotIndex is the persisted snapshot index before the attempt.
	BeforeSnapshotIndex uint64
	// AfterSnapshotIndex is the persisted snapshot index after the attempt.
	AfterSnapshotIndex uint64
	// Compacted reports whether the recovery snapshot advanced.
	Compacted bool
	// SkippedReason explains why no new snapshot was created when Compacted is false.
	SkippedReason string
}

type logCompactor struct {
	cfg             LogCompactionConfig
	lastCheck       time.Time
	lastSnapshotIdx uint64
	now             func() time.Time
}

func newLogCompactor(cfg LogCompactionConfig, lastSnapshotIdx uint64) *logCompactor {
	return &logCompactor{
		cfg:             cfg,
		lastSnapshotIdx: lastSnapshotIdx,
		now:             time.Now,
	}
}

func (c *logCompactor) shouldCompact(target uint64) bool {
	if c == nil || !c.cfg.Enabled || target == 0 {
		return false
	}
	if target < c.lastSnapshotIdx || target-c.lastSnapshotIdx < c.cfg.TriggerEntries {
		return false
	}
	now := c.now()
	if !c.lastCheck.IsZero() && now.Sub(c.lastCheck) < c.cfg.CheckInterval {
		return false
	}
	c.lastCheck = now
	return true
}

func (c *logCompactor) shouldRefreshAfterConfigChange(applied uint64) bool {
	if c == nil || applied == 0 {
		return false
	}
	return c.lastSnapshotIdx > 0 && applied > c.lastSnapshotIdx
}

func (c *logCompactor) recordSnapshot(index uint64) {
	if c == nil {
		return
	}
	c.lastSnapshotIdx = index
}

func (g *slot) compactLog(ctx context.Context, applied uint64) (bool, error) {
	if g == nil || applied == 0 {
		return false, nil
	}
	err := g.compactLogAt(ctx, applied, false)
	return err == nil, err
}

func (g *slot) compactLogAt(
	ctx context.Context,
	applied uint64,
	replaceExternal bool,
) error {
	if g == nil || applied == 0 {
		return nil
	}
	// DurableAppliedStateMachine makes its business state authoritative at the
	// apply boundary. Mirror that watermark into Raft storage only when a
	// snapshot is about to validate and compact the durable log.
	if _, ok := g.stateMachine.(DurableAppliedStateMachine); ok {
		if err := g.storage.MarkApplied(ctx, applied); err != nil {
			return err
		}
	}
	stateSnap, err := g.stateMachine.Snapshot(ctx)
	if err != nil {
		return err
	}
	term, err := g.storageView.memory.Term(applied)
	if err != nil {
		return err
	}
	confState := cloneConfState(g.storageView.memory.confState)
	snapshotData := encodeSlotSnapshotData(stateSnap.Data, g.configAppliedIndexForSnapshot(applied))
	snap := raftpb.Snapshot{
		Data: snapshotData,
		Metadata: raftpb.SnapshotMetadata{
			Index:     applied,
			Term:      term,
			ConfState: confState,
		},
	}
	var persistErr error
	if replacer, ok := g.storage.(ExternalSnapshotStorage); replaceExternal && ok {
		persistErr = replacer.ReplaceSnapshot(ctx, snap)
	} else {
		persistErr = g.storage.Save(ctx, PersistentState{Snapshot: &snap})
	}
	if persistErr != nil {
		return persistErr
	}
	if _, err := g.storageView.memory.CreateSnapshot(applied, &snap.Metadata.ConfState, snap.Data); err != nil && !errors.Is(err, raft.ErrSnapOutOfDate) {
		return err
	}
	g.storageView.memory.confState = cloneConfState(snap.Metadata.ConfState)
	if err := g.storageView.memory.Compact(applied); err != nil && !errors.Is(err, raft.ErrCompacted) {
		return err
	}
	return nil
}

func (g *slot) compactLogManually(ctx context.Context, applied uint64) (LogCompactionResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	result := LogCompactionResult{
		NodeID:       g.nodeID(),
		SlotID:       g.id,
		AppliedIndex: applied,
	}
	snapshotIndex := g.compactor.lastSnapshotIdx
	if snapshotIndex > 0 {
		result.BeforeSnapshotIndex = snapshotIndex
		result.AfterSnapshotIndex = snapshotIndex
	}
	if g.compactor == nil || !g.compactor.cfg.Enabled {
		result.SkippedReason = LogCompactionSkippedDisabled
		return result, nil
	}
	if applied == 0 {
		result.SkippedReason = LogCompactionSkippedNoAppliedIndex
		return result, nil
	}
	firstIndex, err := g.storage.FirstIndex(ctx)
	if err != nil {
		return result, err
	}
	snapshotUpToDate := applied <= result.BeforeSnapshotIndex
	logUpToDate := firstIndex > applied
	if snapshotUpToDate && logUpToDate {
		result.SkippedReason = LogCompactionSkippedUpToDate
		return result, nil
	}
	if err := g.compactLogAt(ctx, applied, false); err != nil {
		return result, err
	}
	result.AfterSnapshotIndex = applied
	result.Compacted = true
	g.compactor.recordSnapshot(applied)
	return result, nil
}
