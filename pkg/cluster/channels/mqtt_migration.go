package channels

import (
	"context"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// migrationReplayReady requires explicit replica-local evidence at the exact
// probed HW. Missing capability is not native-only proof; current native stores
// return a covered no-anchor receipt. Waiting cannot make the task terminal.
func migrationReplayReady(probe ch.RuntimeProbeChannel, task metadb.ChannelMigrationTask) bool {
	return !probe.RecoveryRequired && probe.HW <= probe.LEO && probe.HW >= task.CutoverLEO &&
		writeFenceMatchesTask(probe.WriteFence, task) && probe.ReplayReadiness != nil &&
		probe.ReplayReadiness.ValidFor(probe.HW) && probe.ReplayReadiness.Covered
}

// recheckMigrationReplayTarget prevents the next asynchronous phase from reusing
// a catch-up receipt after restart or content loss. The existing Slot mutation
// still verifies the durable task/cutover/runtime guards before changing placement.
func (e *MigrationExecutor) recheckMigrationReplayTarget(ctx context.Context, task metadb.ChannelMigrationTask) (bool, error) {
	meta, id, err := e.readLeaderTransferMeta(ctx, task)
	if err != nil {
		return false, err
	}
	if meta.WriteFenceToken != task.TaskID || meta.WriteFenceVersion != task.FenceVersion {
		return false, nil
	}
	probe, err := e.runtime.ProbeChannel(ctx, task.TargetNode, id.ID, id.Type)
	if err != nil {
		return false, err
	}
	if err = ctxErr(ctx); err != nil {
		return false, err
	}
	if validateLeaderTransferRuntimeProbe(probe, meta, ch.RoleFollower) != nil {
		return false, nil
	}
	return migrationReplayReady(probe, task), nil
}
