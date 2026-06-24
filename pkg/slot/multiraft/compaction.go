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
	// Compacted reports whether a new snapshot was created and local entries were compacted.
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

func (c *logCompactor) shouldCompact(applied uint64) bool {
	if c == nil || !c.cfg.Enabled || applied == 0 {
		return false
	}
	if applied < c.lastSnapshotIdx || applied-c.lastSnapshotIdx < c.cfg.TriggerEntries {
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

func (g *slot) compactLog(ctx context.Context, applied uint64) error {
	if g == nil || applied == 0 {
		return nil
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
	if err := g.storage.Save(ctx, PersistentState{Snapshot: &snap}); err != nil {
		return err
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
	snapshotIndex, _, err := storageSnapshotBoundary(ctx, g.storage)
	if err != nil {
		return result, err
	}
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
	if applied <= result.BeforeSnapshotIndex {
		result.SkippedReason = LogCompactionSkippedUpToDate
		return result, nil
	}
	if err := g.compactLog(ctx, applied); err != nil {
		return result, err
	}
	result.AfterSnapshotIndex = applied
	result.Compacted = true
	g.compactor.recordSnapshot(applied)
	return result, nil
}

func storageSnapshotBoundary(ctx context.Context, storage Storage) (uint64, uint64, error) {
	first, err := storage.FirstIndex(ctx)
	if err != nil {
		return 0, 0, err
	}
	if first <= 1 {
		return 0, 0, nil
	}
	snapshotIndex := first - 1
	snapshotTerm, err := storage.Term(ctx, snapshotIndex)
	if err != nil {
		return 0, 0, err
	}
	return snapshotIndex, snapshotTerm, nil
}
