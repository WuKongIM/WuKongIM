package multiraft

import (
	"context"
	"errors"
	"sync"
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
	// LogCompactionSkippedPinned reports that backup temporarily retains source entries.
	LogCompactionSkippedPinned = "backup_pinned"
	archiveTrimRetryInitial    = 10 * time.Millisecond
	archiveTrimRetryMaximum    = time.Second
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
	// Compacted reports whether the recovery snapshot advanced or retained
	// backup-log entries were deleted.
	Compacted bool
	// SkippedReason explains why no new snapshot was created when Compacted is false.
	SkippedReason string
}

type logCompactor struct {
	cfg             LogCompactionConfig
	lastCheck       time.Time
	lastSnapshotIdx uint64
	now             func() time.Time
	// pinOperation serializes pin mutations through any resulting archive trim.
	// Readers never take this gate, so storage latency cannot block Slot apply.
	pinOperation chan struct{}
	pinMu        sync.RWMutex
	// archiveTrimDirty retries cleanup after a pin mutation outlives a failed
	// storage trim. It is protected by pinMu and changed only under pinOperation.
	archiveTrimDirty        bool
	archiveTrimRetryRunning bool
	// pins maps each consumer to the greatest persisted log index that may be
	// deleted. Entries strictly after the minimum floor remain readable even
	// when the recovery snapshot advances beyond that floor.
	pins map[string]uint64
}

func newLogCompactor(cfg LogCompactionConfig, lastSnapshotIdx uint64) *logCompactor {
	compactor := &logCompactor{
		cfg:             cfg,
		lastSnapshotIdx: lastSnapshotIdx,
		now:             time.Now,
		pinOperation:    make(chan struct{}, 1),
		pins:            make(map[string]uint64),
	}
	compactor.pinOperation <- struct{}{}
	return compactor
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

func (c *logCompactor) setPin(pinID string, retainAfter uint64, held bool) {
	if c == nil || pinID == "" {
		return
	}
	c.pinMu.Lock()
	defer c.pinMu.Unlock()
	if held {
		c.pins[pinID] = retainAfter
		return
	}
	delete(c.pins, pinID)
}

func (c *logCompactor) retentionFloor(applied uint64) uint64 {
	if c == nil {
		return 0
	}
	c.pinMu.RLock()
	defer c.pinMu.RUnlock()
	target := applied
	for _, retainAfter := range c.pins {
		if retainAfter < target {
			target = retainAfter
		}
	}
	return target
}

func (c *logCompactor) lockPinOperation(ctx context.Context) error {
	if c == nil || c.pinOperation == nil {
		return ErrInvalidOptions
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.pinOperation:
		if err := ctx.Err(); err != nil {
			c.pinOperation <- struct{}{}
			return err
		}
		return nil
	}
}

func (c *logCompactor) unlockPinOperation() {
	if c != nil && c.pinOperation != nil {
		c.pinOperation <- struct{}{}
	}
}

func (c *logCompactor) tryLockPinOperation() bool {
	if c == nil || c.pinOperation == nil {
		return false
	}
	select {
	case <-c.pinOperation:
		return true
	default:
		return false
	}
}

func (c *logCompactor) trimDirty() bool {
	if c == nil {
		return false
	}
	c.pinMu.RLock()
	defer c.pinMu.RUnlock()
	return c.archiveTrimDirty
}

func (c *logCompactor) setTrimDirty(dirty bool) {
	if c == nil {
		return
	}
	c.pinMu.Lock()
	c.archiveTrimDirty = dirty
	c.pinMu.Unlock()
}

func (c *logCompactor) claimTrimRetry() bool {
	if c == nil {
		return false
	}
	c.pinMu.Lock()
	defer c.pinMu.Unlock()
	if !c.archiveTrimDirty || c.archiveTrimRetryRunning {
		return false
	}
	c.archiveTrimRetryRunning = true
	return true
}

func (c *logCompactor) finishTrimRetry() bool {
	if c == nil {
		return false
	}
	c.pinMu.Lock()
	c.archiveTrimRetryRunning = false
	dirty := c.archiveTrimDirty
	c.pinMu.Unlock()
	return dirty
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
	// Automatic compaction runs after apply. Never wait behind backup pin I/O
	// on the Slot worker; the next periodic check can retry safely.
	if !g.compactor.tryLockPinOperation() {
		return false, nil
	}
	defer g.compactor.unlockPinOperation()
	err := g.compactLogAtRetentionFloor(
		ctx, applied, g.compactor.retentionFloor(applied),
	)
	if err == nil {
		g.compactor.setTrimDirty(false)
	}
	return err == nil, err
}

func (g *slot) compactLogAtRetentionFloor(
	ctx context.Context,
	applied uint64,
	retainAfter uint64,
) error {
	if g == nil || applied == 0 || retainAfter > applied {
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
	if err := g.storage.Save(ctx, PersistentState{
		Snapshot: &snap, RetainLogAfter: &retainAfter,
	}); err != nil {
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

func (g *slot) startArchiveTrimRetry() {
	if g == nil || g.compactor == nil || !g.compactor.claimTrimRetry() {
		return
	}
	g.mu.Lock()
	if g.closed || g.pinCleanupCtx == nil {
		g.mu.Unlock()
		g.compactor.finishTrimRetry()
		return
	}
	g.pinOperations++
	ctx := g.pinCleanupCtx
	g.mu.Unlock()
	go g.retryArchiveTrim(ctx)
}

func (g *slot) retryArchiveTrim(ctx context.Context) {
	defer func() {
		retry := g.compactor.finishTrimRetry()
		if retry && ctx.Err() == nil {
			g.startArchiveTrimRetry()
		}
		g.finishPinOperation()
	}()
	delay := archiveTrimRetryInitial
	for {
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		case <-timer.C:
		}
		if err := g.compactor.lockPinOperation(ctx); err != nil {
			return
		}
		if !g.compactor.trimDirty() {
			g.compactor.unlockPinOperation()
			return
		}
		trimmer, ok := g.storage.(RetainedLogStorage)
		if !ok {
			g.compactor.setTrimDirty(false)
			g.compactor.unlockPinOperation()
			return
		}
		retainAfter := g.compactor.retentionFloor(g.appliedIndex())
		err := trimmer.TrimRetainedLog(ctx, retainAfter)
		if err == nil {
			g.compactor.setTrimDirty(false)
			g.compactor.unlockPinOperation()
			return
		}
		g.compactor.unlockPinOperation()
		if delay < archiveTrimRetryMaximum {
			delay *= 2
			if delay > archiveTrimRetryMaximum {
				delay = archiveTrimRetryMaximum
			}
		}
	}
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
	// Manual compaction is also dispatched by the Slot worker. Treat an active
	// pin mutation/trim as a safe skip instead of blocking proposal progress.
	if !g.compactor.tryLockPinOperation() {
		result.SkippedReason = LogCompactionSkippedPinned
		return result, nil
	}
	defer g.compactor.unlockPinOperation()
	retainAfter := g.compactor.retentionFloor(applied)
	firstIndex, err := g.storage.FirstIndex(ctx)
	if err != nil {
		return result, err
	}
	snapshotUpToDate := applied <= result.BeforeSnapshotIndex
	retentionUpToDate := firstIndex > retainAfter
	if snapshotUpToDate && retentionUpToDate {
		g.compactor.setTrimDirty(false)
		result.SkippedReason = LogCompactionSkippedUpToDate
		return result, nil
	}
	if snapshotUpToDate {
		trimmer, ok := g.storage.(RetainedLogStorage)
		if !ok {
			result.SkippedReason = LogCompactionSkippedPinned
			return result, nil
		}
		if err := trimmer.TrimRetainedLog(ctx, retainAfter); err != nil {
			return result, err
		}
		g.compactor.setTrimDirty(false)
		result.Compacted = true
		return result, nil
	}
	if err := g.compactLogAtRetentionFloor(ctx, applied, retainAfter); err != nil {
		return result, err
	}
	result.AfterSnapshotIndex = applied
	result.Compacted = true
	g.compactor.recordSnapshot(applied)
	g.compactor.setTrimDirty(false)
	return result, nil
}
