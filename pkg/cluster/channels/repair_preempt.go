package channels

import (
	"context"
	"errors"
	"slices"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
)

// AbortReplicaReplacementForFailover releases only an unpromoted replacement
// when the scanner has observed its leader unavailable. Existing Slot commands
// atomically fence the task version and metadata, remove the unpromoted learner,
// and clear only the task-owned write fence. Election uses the next fresh scan.
func (s *MigrationStore) AbortReplicaReplacementForFailover(ctx context.Context, id ch.ChannelID, leader, leaderEpoch uint64) (bool, error) {
	route, meta, err := s.readRuntimeMeta(ctx, id)
	if err != nil {
		return false, err
	}
	if leader == 0 || meta.Leader != leader || meta.LeaderEpoch != leaderEpoch {
		return false, nil
	}
	task, found, err := s.reader.GetActiveChannelMigrationTask(ctx, route.HashSlot, id.ID, int64(id.Type))
	if err != nil || !found {
		return false, err
	}
	if task.IsTerminal() || task.Kind != metadb.ChannelMigrationKindReplicaReplace || task.EmbeddedLeaderTransfer || slices.Contains(meta.ISR, task.TargetNode) {
		return false, nil
	}
	switch task.Phase {
	case metadb.ChannelMigrationPhaseValidate, metadb.ChannelMigrationPhaseAddLearner,
		metadb.ChannelMigrationPhaseBootstrapTarget, metadb.ChannelMigrationPhaseWarmCatchUp,
		metadb.ChannelMigrationPhaseCutoverFence, metadb.ChannelMigrationPhaseFinalTargetCatchUp,
		metadb.ChannelMigrationPhasePromoteAndRemove:
	default:
		return false, nil
	}
	err = s.abortAtRuntimeMeta(ctx, route, meta, task, "leader unavailable before replica promotion")
	if errors.Is(err, metadb.ErrStaleMeta) {
		return false, nil
	}
	return err == nil, err
}

// CompletePromotedReplicaReplacementForFailover completes a replacement that
// already committed its membership change (VerifyMembership) when the scanner
// observed its leader unavailable. The skipped runtime verification can only
// reach the dead leader; the following failover re-applies metadata under a
// new leader epoch. It reuses the ordinary ClearChannelWriteFence command, so
// Slot replicas of every version apply the same transition rules, and clears
// only the task-owned fence. Election uses the next fresh scan (Issue #1077).
func (s *MigrationStore) CompletePromotedReplicaReplacementForFailover(ctx context.Context, id ch.ChannelID, leader, leaderEpoch uint64) (bool, error) {
	route, meta, err := s.readRuntimeMeta(ctx, id)
	if err != nil {
		return false, err
	}
	if leader == 0 || meta.Leader != leader || meta.LeaderEpoch != leaderEpoch {
		return false, nil
	}
	task, found, err := s.reader.GetActiveChannelMigrationTask(ctx, route.HashSlot, id.ID, int64(id.Type))
	if err != nil || !found {
		return false, err
	}
	if task.IsTerminal() || task.Kind != metadb.ChannelMigrationKindReplicaReplace || task.EmbeddedLeaderTransfer ||
		task.Phase != metadb.ChannelMigrationPhaseVerifyMembership {
		return false, nil
	}
	// Same committed-membership proof that VerifyMembership itself requires.
	if slices.Contains(meta.Replicas, task.SourceNode) || slices.Contains(meta.ISR, task.SourceNode) ||
		!slices.Contains(meta.Replicas, task.TargetNode) || !slices.Contains(meta.ISR, task.TargetNode) ||
		int64(len(meta.ISR)) < meta.MinISR {
		return false, nil
	}
	if task.FenceVersion == 0 || meta.WriteFenceToken != task.TaskID || meta.WriteFenceVersion != task.FenceVersion {
		return false, nil
	}
	updatedAtMS := nextMigrationVersion(s.nowMS(), task.UpdatedAtMS)
	req := metadb.ChannelMigrationClearFenceRequest{
		Guard:         migrationTaskGuard(task, task.UpdatedAtMS),
		RuntimeGuard:  migrationRuntimeGuard(meta),
		Status:        metadb.ChannelMigrationStatusCompleted,
		Phase:         metadb.ChannelMigrationPhaseClearFence,
		UpdatedAtMS:   updatedAtMS,
		CompletedAtMS: updatedAtMS,
	}
	err = s.propose(ctx, route, metafsm.EncodeClearChannelWriteFenceCommand(req))
	if errors.Is(err, metadb.ErrStaleMeta) {
		return false, nil
	}
	return err == nil, err
}
