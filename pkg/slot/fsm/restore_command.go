package fsm

import (
	"context"
	"fmt"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// IsRestorePortableCommand validates one committed Slot command and reports
// whether it belongs in a successor cluster's semantic metadata projection.
// Source topology, migration workflow, runtime retention, and no-op commands
// remain authenticated capture evidence but are not applied to the target.
func IsRestorePortableCommand(data []byte) (bool, error) {
	if _, err := decodeCommand(data); err != nil {
		return false, err
	}
	if len(data) < headerSize || data[0] != commandVersion {
		return false, fmt.Errorf(
			"%w: restore command header is invalid",
			metadb.ErrInvalidArgument,
		)
	}
	switch data[1] {
	case cmdTypeUpsertChannelRuntimeMeta,
		cmdTypeDeleteChannelRuntimeMeta,
		cmdTypeAdvanceChannelRetention,
		cmdTypeNoop,
		cmdTypeApplyDelta,
		cmdTypeEnterFence,
		cmdTypeAckMigrationOutbox,
		cmdTypeCleanupMigrationOutbox,
		cmdTypeCreateChannelMigrationTask,
		cmdTypeClaimChannelMigrationTask,
		cmdTypeAdvanceChannelMigrationTask,
		cmdTypeSetChannelWriteFence,
		cmdTypeResetChannelWriteFence,
		cmdTypeCommitChannelLeaderTransfer,
		cmdTypeAddChannelLearner,
		cmdTypePromoteLearnerAndRemoveReplica,
		cmdTypeClearChannelWriteFence,
		cmdTypeAbortChannelMigration,
		cmdTypeGarbageCollectMigrationTasks,
		cmdTypeCreateChannelMigrationGuarded:
		return false, nil
	default:
		return true, nil
	}
}

// ApplyRestorePortableCommand applies only the requested logical Hash Slot
// projection from one authenticated source command. A physical Slot command
// may batch rows for several Hash Slots; restore must never replay those
// unrelated rows merely because the source Raft entry covered the requested
// Hash Slot.
func ApplyRestorePortableCommand(
	ctx context.Context,
	db *metadb.DB,
	hashSlot uint16,
	data []byte,
) (bool, error) {
	if db == nil {
		return false, fmt.Errorf(
			"%w: restore database is required",
			metadb.ErrInvalidArgument,
		)
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	portable, err := IsRestorePortableCommand(data)
	if err != nil || !portable {
		return false, err
	}
	decoded, err := decodeCommand(data)
	if err != nil {
		return false, err
	}
	applyHashSlots := commandApplyHashSlots(decoded, hashSlot)
	applies := false
	for _, candidate := range applyHashSlots {
		if candidate == hashSlot {
			applies = true
			break
		}
	}
	if !applies {
		return false, fmt.Errorf(
			"%w: restore command does not cover hash slot %d",
			metadb.ErrInvalidArgument, hashSlot,
		)
	}

	wb := db.NewWriteBatch()
	defer wb.Close()
	if filtered, ok := decoded.(hashSlotFilteredCommand); ok {
		err = filtered.applyForHashSlot(wb, hashSlot)
	} else {
		for _, candidate := range applyHashSlots {
			if candidate != hashSlot {
				return false, fmt.Errorf(
					"%w: restore command spans unfilterable hash slots",
					metadb.ErrInvalidArgument,
				)
			}
		}
		err = decoded.apply(wb, hashSlot)
	}
	if err != nil {
		if isStaleMetaResult(decoded, err) {
			return true, nil
		}
		return false, err
	}
	if err := wb.Commit(); err != nil {
		if isStaleMetaCommitError(err) {
			return true, nil
		}
		return false, err
	}
	return true, nil
}
