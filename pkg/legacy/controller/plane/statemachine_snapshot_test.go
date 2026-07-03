package plane

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/hashslot"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/legacy/controller/meta"
	"github.com/stretchr/testify/require"
)

func TestStateMachineSnapshotRestoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	source, err := controllermeta.Open(filepath.Join(t.TempDir(), "source"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, source.Close()) })

	now := time.Unix(100, 0)
	require.NoError(t, source.UpsertNode(ctx, controllermeta.ClusterNode{NodeID: 1, Addr: "127.0.0.1:7000", Status: controllermeta.NodeStatusAlive, JoinedAt: now, LastHeartbeatAt: now, CapacityWeight: 1}))
	require.NoError(t, source.UpsertAssignmentTask(ctx,
		controllermeta.SlotAssignment{SlotID: 1, DesiredPeers: []uint64{1}, ConfigEpoch: 2, PreferredLeader: 1},
		controllermeta.ReconcileTask{SlotID: 1, Kind: controllermeta.TaskKindBootstrap, Step: controllermeta.TaskStepAddLearner, TargetNode: 1, Status: controllermeta.TaskStatusPending},
	))
	require.NoError(t, source.UpsertOnboardingJob(ctx, controllermeta.NodeOnboardingJob{
		JobID:            "job-1",
		TargetNodeID:     2,
		Status:           controllermeta.OnboardingJobStatusPlanned,
		CreatedAt:        now,
		UpdatedAt:        now,
		PlanVersion:      1,
		Plan:             controllermeta.NodeOnboardingPlan{TargetNodeID: 2},
		CurrentMoveIndex: -1,
	}))
	require.NoError(t, source.SaveHashSlotTable(ctx, hashslot.NewHashSlotTable(8, 1)))

	snap, err := NewStateMachine(source, StateMachineConfig{}).Snapshot(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, snap)

	target, err := controllermeta.Open(filepath.Join(t.TempDir(), "target"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, target.Close()) })
	require.NoError(t, NewStateMachine(target, StateMachineConfig{}).Restore(ctx, snap))

	node, err := target.GetNode(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, uint64(1), node.NodeID)
	assignment, err := target.GetAssignment(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, []uint64{1}, assignment.DesiredPeers)
	task, err := target.GetTask(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, controllermeta.TaskKindBootstrap, task.Kind)
	job, err := target.GetOnboardingJob(ctx, "job-1")
	require.NoError(t, err)
	require.Equal(t, uint64(2), job.TargetNodeID)
	table, err := target.LoadHashSlotTable(ctx)
	require.NoError(t, err)
	require.Equal(t, uint16(8), table.HashSlotCount())
}

func TestStateMachineRestoreRejectsCorruptSnapshot(t *testing.T) {
	store, err := controllermeta.Open(filepath.Join(t.TempDir(), "store"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })

	err = NewStateMachine(store, StateMachineConfig{}).Restore(context.Background(), []byte("not-a-controller-snapshot"))
	require.Error(t, err)
}
