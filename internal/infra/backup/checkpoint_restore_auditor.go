package backup

import (
	"context"
	"fmt"

	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

const maxCheckpointRestoreAuditDepth = 10_000

// CheckpointRestoreGraphAuditor verifies every immutable object reachable from
// one selected checkpoint in both repository failure domains before planning.
type CheckpointRestoreGraphAuditor struct {
	segments *backupartifact.ReplicatedSegmentStore
}

// NewCheckpointRestoreGraphAuditor creates an exact-checkpoint preflight.
func NewCheckpointRestoreGraphAuditor(
	segments *backupartifact.ReplicatedSegmentStore,
) (*CheckpointRestoreGraphAuditor, error) {
	if segments == nil {
		return nil, fmt.Errorf(
			"backup checkpoint restore graph auditor: segments are required",
		)
	}
	return &CheckpointRestoreGraphAuditor{segments: segments}, nil
}

// Audit verifies materialized baselines, ordered segment chains, and complete
// message cursor chains without repairing either repository copy.
func (a *CheckpointRestoreGraphAuditor) Audit(
	ctx context.Context,
	checkpoint backupartifact.Checkpoint,
) error {
	if a == nil || a.segments == nil ||
		len(checkpoint.Slots) != int(checkpoint.HashSlotCount) {
		return backupartifact.ErrInvalidObject
	}
	a.segments.BeginPartitionAuditCycle(
		"restore-admission-" + checkpoint.ID,
	)
	for _, slot := range checkpoint.Slots {
		if err := ctx.Err(); err != nil {
			return err
		}
		if slot.Baseline != nil {
			if err := a.auditBaseline(
				ctx, checkpoint, slot,
			); err != nil {
				return fmt.Errorf(
					"backup checkpoint restore graph auditor: Slot %d baseline: %w",
					slot.HashSlot, err,
				)
			}
		}
		for _, stream := range []struct {
			kind backupartifact.SegmentStream
			head backupartifact.CheckpointStream
		}{
			{backupartifact.SegmentStreamMetadata, slot.Metadata},
			{backupartifact.SegmentStreamMessages, slot.Messages},
		} {
			if err := a.auditSegmentChain(
				ctx, checkpoint, slot, stream.kind, stream.head,
			); err != nil {
				return fmt.Errorf(
					"backup checkpoint restore graph auditor: Slot %d %s: %w",
					slot.HashSlot, stream.kind, err,
				)
			}
		}
		if err := a.auditMessageCursorChain(
			ctx, checkpoint, slot,
		); err != nil {
			return fmt.Errorf(
				"backup checkpoint restore graph auditor: Slot %d cursor: %w",
				slot.HashSlot, err,
			)
		}
	}
	return nil
}

func (a *CheckpointRestoreGraphAuditor) auditBaseline(
	ctx context.Context,
	checkpoint backupartifact.Checkpoint,
	slot backupartifact.CheckpointSlot,
) error {
	reference := slot.Baseline.Partition
	report, err := a.segments.InspectPartitionArtifactEnvelopeCopies(
		ctx, reference, -1,
	)
	if err != nil {
		return err
	}
	if !healthyCheckpointRestoreAuditCopies(report.Copies) ||
		report.Navigation.HashSlot != slot.HashSlot ||
		report.Navigation.ObjectCount != reference.ObjectCount ||
		report.Navigation.Base != nil {
		return backupartifact.ErrRepositoryIncomplete
	}
	for objectIndex := uint64(0); objectIndex < report.Navigation.ObjectCount; objectIndex++ {
		report, err := a.segments.InspectPartitionArtifactEnvelopeCopies(
			ctx, reference, int(objectIndex),
		)
		if err != nil {
			return err
		}
		if !healthyCheckpointRestoreAuditCopies(report.Copies) {
			return backupartifact.ErrRepositoryIncomplete
		}
	}
	header, err := a.segments.VerifyEnvelopeCopies(
		ctx, slot.Baseline.MessageCursor,
	)
	if err != nil {
		return err
	}
	if header.Logical.RepositoryID != checkpoint.RepositoryID ||
		header.Logical.SourceClusterID != checkpoint.SourceClusterID ||
		header.Logical.SourceGeneration != checkpoint.SourceGeneration ||
		header.Logical.HashSlot != slot.HashSlot ||
		header.Logical.Generation != slot.Generation ||
		header.Logical.Stream !=
			backupartifact.SegmentStreamMessageBaselineCursor ||
		header.Logical.Sequence != 1 ||
		!header.Checkpoint || header.Previous != nil ||
		header.SourceHighWatermark != report.Navigation.SourceHighWatermark ||
		header.WatermarkAtUnixMillis !=
			report.Navigation.WatermarkAtUnixMillis {
		return backupartifact.ErrObjectCorrupt
	}
	return nil
}

func (a *CheckpointRestoreGraphAuditor) auditSegmentChain(
	ctx context.Context,
	checkpoint backupartifact.Checkpoint,
	slot backupartifact.CheckpointSlot,
	stream backupartifact.SegmentStream,
	cut backupartifact.CheckpointStream,
) error {
	if cut.Sequence == 0 {
		if cut.Head != nil {
			return backupartifact.ErrObjectCorrupt
		}
		return nil
	}
	if cut.Head == nil ||
		cut.Sequence > maxCheckpointRestoreAuditDepth {
		return backupartifact.ErrInvalidObject
	}
	current := *cut.Head
	for sequence := cut.Sequence; sequence > 0; sequence-- {
		header, err := a.segments.VerifyEnvelopeCopies(ctx, current)
		if err != nil {
			return err
		}
		if header.Logical.RepositoryID != checkpoint.RepositoryID ||
			header.Logical.SourceClusterID != checkpoint.SourceClusterID ||
			header.Logical.SourceGeneration != checkpoint.SourceGeneration ||
			header.Logical.HashSlot != slot.HashSlot ||
			header.Logical.Generation != slot.Generation ||
			header.Logical.Stream != stream ||
			header.Logical.Sequence != sequence ||
			header.Checkpoint {
			return backupartifact.ErrObjectCorrupt
		}
		if sequence == cut.Sequence &&
			(header.SourceHighWatermark != cut.SourceHighWatermark ||
				header.WatermarkAtUnixMillis != cut.WatermarkAtUnixMillis) {
			return backupartifact.ErrObjectCorrupt
		}
		if sequence == 1 {
			if header.Previous != nil {
				return backupartifact.ErrObjectCorrupt
			}
			break
		}
		if header.Previous == nil {
			return backupartifact.ErrObjectCorrupt
		}
		current = *header.Previous
	}
	return nil
}

func (a *CheckpointRestoreGraphAuditor) auditMessageCursorChain(
	ctx context.Context,
	checkpoint backupartifact.Checkpoint,
	slot backupartifact.CheckpointSlot,
) error {
	cut := slot.Messages
	if cut.Sequence == 0 {
		if cut.CursorHead != nil {
			return backupartifact.ErrObjectCorrupt
		}
		return nil
	}
	if cut.CursorHead == nil ||
		cut.Sequence > maxCheckpointRestoreAuditDepth {
		return backupartifact.ErrInvalidObject
	}
	current := *cut.CursorHead
	for sequence := cut.Sequence; sequence > 0; sequence-- {
		header, err := a.segments.VerifyEnvelopeCopies(ctx, current)
		if err != nil {
			return err
		}
		if header.Logical.RepositoryID != checkpoint.RepositoryID ||
			header.Logical.SourceClusterID != checkpoint.SourceClusterID ||
			header.Logical.SourceGeneration != checkpoint.SourceGeneration ||
			header.Logical.HashSlot != slot.HashSlot ||
			header.Logical.Generation != slot.Generation ||
			header.Logical.Stream !=
				backupartifact.SegmentStreamMessageCursor ||
			header.Logical.Sequence != sequence {
			return backupartifact.ErrObjectCorrupt
		}
		if sequence == cut.Sequence &&
			(header.SourceHighWatermark != cut.SourceHighWatermark ||
				header.WatermarkAtUnixMillis != cut.WatermarkAtUnixMillis) {
			return backupartifact.ErrObjectCorrupt
		}
		if header.Checkpoint {
			if header.Previous != nil {
				return backupartifact.ErrObjectCorrupt
			}
			return nil
		}
		if sequence == 1 {
			if header.Previous != nil {
				return backupartifact.ErrObjectCorrupt
			}
			return nil
		}
		if header.Previous == nil {
			return backupartifact.ErrObjectCorrupt
		}
		current = *header.Previous
	}
	return nil
}

func checkpointRestoreCursorRecordCount(
	boundaries []backupartifact.ChannelBoundary,
) uint64 {
	if len(boundaries) == 0 {
		return 1
	}
	return uint64(len(boundaries))
}

func healthyCheckpointRestoreAuditCopies(
	copies []backupartifact.SegmentAuditCopy,
) bool {
	if len(copies) != 2 ||
		copies[0].Repository == "" ||
		copies[1].Repository == "" ||
		copies[0].Repository == copies[1].Repository {
		return false
	}
	for _, copy := range copies {
		if !copy.Healthy || copy.Category != "" {
			return false
		}
	}
	return true
}
