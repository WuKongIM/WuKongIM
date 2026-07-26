package fsm

import (
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
