package fsm

import (
	"context"
	"errors"
	"reflect"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

func TestChannelMigrationCommandCodecRoundTrip(t *testing.T) {
	task := fsmTestChannelMigrationTask("task-codec", "channel-codec")
	task.Status = metadb.ChannelMigrationStatusRunning
	task.Phase = metadb.ChannelMigrationPhaseWarmCatchUp
	task.OwnerNodeID = 1
	task.OwnerLeaseUntilMS = 1750000005000
	task.UpdatedAtMS = 1750000001000
	advance := fsmTestChannelMigrationAdvance(task, metadb.ChannelMigrationStatusRunning, metadb.ChannelMigrationPhaseFinalTargetCatchUp, 1750000002000)
	advance.CutoverProof = metadb.ChannelMigrationCutoverProof{
		CutoverLEO:               100,
		CutoverHW:                99,
		DrainedLeaderNode:        1,
		DrainedRuntimeGeneration: 2,
		DrainedChannelEpoch:      3,
		DrainedLeaderEpoch:       4,
		DrainedFenceVersion:      7,
	}

	cases := []struct {
		name string
		data []byte
		want command
	}{
		{
			name: "create",
			data: EncodeCreateChannelMigrationTaskCommand(fsmTestChannelMigrationTask("task-create", "channel-create")),
			want: &createChannelMigrationTaskCmd{task: fsmTestChannelMigrationTask("task-create", "channel-create")},
		},
		{
			name: "claim",
			data: EncodeClaimChannelMigrationTaskCommand(fsmTestChannelMigrationClaim(task, 2, 1750000006000, 1750000002000)),
			want: &claimChannelMigrationTaskCmd{req: fsmTestChannelMigrationClaim(task, 2, 1750000006000, 1750000002000)},
		},
		{
			name: "advance",
			data: EncodeAdvanceChannelMigrationTaskCommand(advance),
			want: &advanceChannelMigrationTaskCmd{req: advance},
		},
		{
			name: "set_fence",
			data: EncodeSetChannelWriteFenceCommand(fsmTestSetFenceRequest(task, fsmTestRuntimeMeta(task.ChannelID, task.ChannelType), 1750000007000, 1750000002000)),
			want: &setChannelWriteFenceCmd{req: fsmTestSetFenceRequest(task, fsmTestRuntimeMeta(task.ChannelID, task.ChannelType), 1750000007000, 1750000002000)},
		},
		{
			name: "reset_fence",
			data: EncodeResetChannelWriteFenceToPreCutoverCommand(fsmTestResetFenceRequest(task, fsmTestFencedRuntimeMeta(task.ChannelID, task.ChannelType, task.TaskID, 7), metadb.ChannelMigrationPhaseWarmCatchUp, 1750000003000)),
			want: &resetChannelWriteFenceToPreCutoverCmd{req: fsmTestResetFenceRequest(task, fsmTestFencedRuntimeMeta(task.ChannelID, task.ChannelType, task.TaskID, 7), metadb.ChannelMigrationPhaseWarmCatchUp, 1750000003000)},
		},
		{
			name: "commit_leader",
			data: EncodeCommitChannelLeaderTransferCommand(fsmTestCommitLeaderRequest(task, fsmTestFencedRuntimeMeta(task.ChannelID, task.ChannelType, task.TaskID, 7), 1750000003000)),
			want: &commitChannelLeaderTransferCmd{req: fsmTestCommitLeaderRequest(task, fsmTestFencedRuntimeMeta(task.ChannelID, task.ChannelType, task.TaskID, 7), 1750000003000)},
		},
		{
			name: "add_learner",
			data: EncodeAddChannelLearnerCommand(fsmTestAddLearnerRequest(task, fsmTestRuntimeMeta(task.ChannelID, task.ChannelType), 1750000003000)),
			want: &addChannelLearnerCmd{req: fsmTestAddLearnerRequest(task, fsmTestRuntimeMeta(task.ChannelID, task.ChannelType), 1750000003000)},
		},
		{
			name: "promote",
			data: EncodePromoteLearnerAndRemoveReplicaCommand(fsmTestPromoteRequest(task, fsmTestFencedRuntimeMeta(task.ChannelID, task.ChannelType, task.TaskID, 7), 1750000003000)),
			want: &promoteLearnerAndRemoveReplicaCmd{req: fsmTestPromoteRequest(task, fsmTestFencedRuntimeMeta(task.ChannelID, task.ChannelType, task.TaskID, 7), 1750000003000)},
		},
		{
			name: "clear_fence",
			data: EncodeClearChannelWriteFenceCommand(fsmTestClearFenceRequest(task, fsmTestFencedRuntimeMeta(task.ChannelID, task.ChannelType, task.TaskID, 7), 1750000003000)),
			want: &clearChannelWriteFenceCmd{req: fsmTestClearFenceRequest(task, fsmTestFencedRuntimeMeta(task.ChannelID, task.ChannelType, task.TaskID, 7), 1750000003000)},
		},
		{
			name: "abort",
			data: EncodeAbortChannelMigrationCommand(fsmTestAbortRequest(task, fsmTestRuntimeMeta(task.ChannelID, task.ChannelType), 1750000003000)),
			want: &abortChannelMigrationCmd{req: fsmTestAbortRequest(task, fsmTestRuntimeMeta(task.ChannelID, task.ChannelType), 1750000003000)},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := decodeCommand(tc.data)
			if err != nil {
				t.Fatalf("decodeCommand(%s) error = %v", tc.name, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("decodeCommand(%s) = %#v, want %#v", tc.name, got, tc.want)
			}
		})
	}
}

func TestStateMachineAddChannelLearnerUpdatesTaskAndMetaAtomically(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm := mustNewStateMachine(t, db, 11)
	task := fsmTestChannelMigrationTask("task-add-learner", "channel-add-learner")
	task.Status = metadb.ChannelMigrationStatusRunning
	task.Phase = metadb.ChannelMigrationPhaseAddLearner
	task.UpdatedAtMS = 1750000001000
	meta := fsmTestRuntimeMeta(task.ChannelID, task.ChannelType)
	meta.ChannelEpoch = task.BaseChannelEpoch
	meta.LeaderEpoch = task.BaseLeaderEpoch
	meta.Replicas = []uint64{1, 2}
	meta.ISR = []uint64{1, 2}

	fsmApplyOK(t, ctx, sm, 1, EncodeUpsertChannelRuntimeMetaCommand(meta))
	fsmApplyOK(t, ctx, sm, 2, EncodeCreateChannelMigrationTaskCommand(task))
	fsmApplyOK(t, ctx, sm, 3, EncodeAddChannelLearnerCommand(fsmTestAddLearnerRequest(task, meta, 1750000002000)))

	gotTask, err := db.ForSlot(11).GetChannelMigrationTask(ctx, task.ChannelID, task.ChannelType, task.TaskID)
	if err != nil {
		t.Fatalf("GetChannelMigrationTask() error = %v", err)
	}
	if gotTask.Phase != metadb.ChannelMigrationPhaseBootstrapTarget || gotTask.UpdatedAtMS != 1750000002000 {
		t.Fatalf("task after add learner = %#v", gotTask)
	}
	gotMeta, err := db.ForSlot(11).GetChannelRuntimeMeta(ctx, meta.ChannelID, meta.ChannelType)
	if err != nil {
		t.Fatalf("GetChannelRuntimeMeta() error = %v", err)
	}
	if gotMeta.ChannelEpoch != meta.ChannelEpoch+1 || !reflect.DeepEqual(gotMeta.Replicas, []uint64{1, 2, 3}) || !reflect.DeepEqual(gotMeta.ISR, []uint64{1, 2}) {
		t.Fatalf("meta after add learner = %#v", gotMeta)
	}
}

func TestStateMachineChannelPromoteRejectsExpiredFence(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm := mustNewStateMachine(t, db, 11)
	task := fsmTestChannelMigrationTask("task-promote-expired", "channel-promote-expired")
	task.Status = metadb.ChannelMigrationStatusRunning
	task.Phase = metadb.ChannelMigrationPhasePromoteAndRemove
	task.UpdatedAtMS = 1750000001000
	task.FenceToken = task.TaskID
	task.FenceVersion = 7
	task.FenceUntilMS = 1750000002000
	setFSMTestDrainProof(&task, 7)
	meta := fsmTestFencedRuntimeMeta(task.ChannelID, task.ChannelType, task.TaskID, 7)
	meta.WriteFenceUntilMS = 1750000002000
	meta.ChannelEpoch = task.BaseChannelEpoch
	meta.LeaderEpoch = task.BaseLeaderEpoch
	meta.Replicas = []uint64{1, 2, 3}
	meta.ISR = []uint64{1, 2}

	fsmApplyOK(t, ctx, sm, 1, EncodeUpsertChannelRuntimeMetaCommand(meta))
	fsmApplyOK(t, ctx, sm, 2, EncodeCreateChannelMigrationTaskCommand(task))
	req := fsmTestPromoteRequest(task, meta, 1750000003000)
	req.NowMS = meta.WriteFenceUntilMS + 1
	result, err := sm.Apply(ctx, multiraft.Command{
		SlotID: 11,
		Index:  3,
		Term:   1,
		Data:   EncodePromoteLearnerAndRemoveReplicaCommand(req),
	})
	if err != nil {
		t.Fatalf("Apply(promote expired fence) error = %v", err)
	}
	if got := string(result); got != ApplyResultStaleMeta {
		t.Fatalf("promote expired fence result = %q, want %q", got, ApplyResultStaleMeta)
	}
}

func TestStateMachineChannelResetAcceptsExpiredMatchingFenceAndBumpsVersion(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm := mustNewStateMachine(t, db, 11)
	task := fsmTestChannelMigrationTask("task-reset-fence", "channel-reset-fence")
	task.Status = metadb.ChannelMigrationStatusRunning
	task.Phase = metadb.ChannelMigrationPhasePromoteAndRemove
	task.UpdatedAtMS = 1750000001000
	task.FenceToken = task.TaskID
	task.FenceVersion = 7
	task.FenceUntilMS = 1750000002000
	setFSMTestDrainProof(&task, 7)
	meta := fsmTestFencedRuntimeMeta(task.ChannelID, task.ChannelType, task.TaskID, 7)
	meta.WriteFenceUntilMS = 1750000002000
	meta.ChannelEpoch = task.BaseChannelEpoch
	meta.LeaderEpoch = task.BaseLeaderEpoch

	fsmApplyOK(t, ctx, sm, 1, EncodeUpsertChannelRuntimeMetaCommand(meta))
	fsmApplyOK(t, ctx, sm, 2, EncodeCreateChannelMigrationTaskCommand(task))
	fsmApplyOK(t, ctx, sm, 3, EncodeResetChannelWriteFenceToPreCutoverCommand(fsmTestResetFenceRequest(task, meta, metadb.ChannelMigrationPhaseWarmCatchUp, 1750000003000)))

	gotTask, err := db.ForSlot(11).GetChannelMigrationTask(ctx, task.ChannelID, task.ChannelType, task.TaskID)
	if err != nil {
		t.Fatalf("GetChannelMigrationTask() error = %v", err)
	}
	if gotTask.Phase != metadb.ChannelMigrationPhaseWarmCatchUp || gotTask.FenceToken != "" || gotTask.DrainedFenceVersion != 0 {
		t.Fatalf("task after reset = %#v", gotTask)
	}
	gotMeta, err := db.ForSlot(11).GetChannelRuntimeMeta(ctx, meta.ChannelID, meta.ChannelType)
	if err != nil {
		t.Fatalf("GetChannelRuntimeMeta() error = %v", err)
	}
	if gotMeta.WriteFenceToken != "" || gotMeta.WriteFenceVersion != 8 || gotMeta.WriteFenceUntilMS != 0 {
		t.Fatalf("meta after reset = %#v", gotMeta)
	}
}

func TestStateMachineChannelSetAndClearFenceSameBatch(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm := mustNewStateMachine(t, db, 11)
	task := fsmTestChannelMigrationTask("task-set-clear-same-batch", "channel-set-clear-same-batch")
	task.Status = metadb.ChannelMigrationStatusRunning
	task.Phase = metadb.ChannelMigrationPhaseWarmCatchUp
	task.UpdatedAtMS = 1750000001000
	meta := fsmTestRuntimeMeta(task.ChannelID, task.ChannelType)
	meta.ChannelEpoch = task.BaseChannelEpoch
	meta.LeaderEpoch = task.BaseLeaderEpoch

	fsmApplyOK(t, ctx, sm, 1, EncodeUpsertChannelRuntimeMetaCommand(meta))
	fsmApplyOK(t, ctx, sm, 2, EncodeCreateChannelMigrationTaskCommand(task))

	setReq := fsmTestSetFenceRequest(task, meta, 1750000009000, 1750000002000)
	fencedTask := task
	fencedTask.Status = setReq.Status
	fencedTask.Phase = setReq.Phase
	fencedTask.FenceToken = task.TaskID
	fencedTask.FenceVersion = 1
	fencedTask.FenceUntilMS = setReq.FenceUntilMS
	fencedTask.UpdatedAtMS = setReq.UpdatedAtMS
	fencedMeta := meta
	fencedMeta.WriteFenceToken = task.TaskID
	fencedMeta.WriteFenceVersion = 1
	fencedMeta.WriteFenceReason = setReq.FenceReason
	fencedMeta.WriteFenceUntilMS = setReq.FenceUntilMS
	clearReq := fsmTestClearFenceRequest(fencedTask, fencedMeta, 1750000003000)

	batchSM := sm.(multiraft.BatchStateMachine)
	results, err := batchSM.ApplyBatch(ctx, []multiraft.Command{
		{SlotID: 11, Index: 3, Term: 1, Data: EncodeSetChannelWriteFenceCommand(setReq)},
		{SlotID: 11, Index: 4, Term: 1, Data: EncodeClearChannelWriteFenceCommand(clearReq)},
	})
	if err != nil {
		t.Fatalf("ApplyBatch(set+clear fence) error = %v", err)
	}
	if got := []string{string(results[0]), string(results[1])}; !reflect.DeepEqual(got, []string{ApplyResultOK, ApplyResultOK}) {
		t.Fatalf("set+clear results = %#v", got)
	}

	gotTask, err := db.ForSlot(11).GetChannelMigrationTask(ctx, task.ChannelID, task.ChannelType, task.TaskID)
	if err != nil {
		t.Fatalf("GetChannelMigrationTask() error = %v", err)
	}
	if gotTask.Status != metadb.ChannelMigrationStatusCompleted || gotTask.FenceToken != "" {
		t.Fatalf("task after set+clear = %#v", gotTask)
	}
	gotMeta, err := db.ForSlot(11).GetChannelRuntimeMeta(ctx, meta.ChannelID, meta.ChannelType)
	if err != nil {
		t.Fatalf("GetChannelRuntimeMeta() error = %v", err)
	}
	if gotMeta.WriteFenceToken != "" || gotMeta.WriteFenceVersion != 2 {
		t.Fatalf("meta after set+clear = %#v", gotMeta)
	}
}

func TestStateMachineChannelAbortAfterLearnerIncrementsChannelEpoch(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm := mustNewStateMachine(t, db, 11)
	task := fsmTestChannelMigrationTask("task-abort-learner", "channel-abort-learner")
	task.Status = metadb.ChannelMigrationStatusRunning
	task.Phase = metadb.ChannelMigrationPhaseBootstrapTarget
	task.UpdatedAtMS = 1750000001000
	meta := fsmTestRuntimeMeta(task.ChannelID, task.ChannelType)
	meta.ChannelEpoch = task.BaseChannelEpoch
	meta.LeaderEpoch = task.BaseLeaderEpoch
	meta.Replicas = []uint64{1, 2, 3}
	meta.ISR = []uint64{1, 2}

	fsmApplyOK(t, ctx, sm, 1, EncodeUpsertChannelRuntimeMetaCommand(meta))
	fsmApplyOK(t, ctx, sm, 2, EncodeCreateChannelMigrationTaskCommand(task))
	fsmApplyOK(t, ctx, sm, 3, EncodeAbortChannelMigrationCommand(fsmTestAbortRequest(task, meta, 1750000002000)))

	gotTask, err := db.ForSlot(11).GetChannelMigrationTask(ctx, task.ChannelID, task.ChannelType, task.TaskID)
	if err != nil {
		t.Fatalf("GetChannelMigrationTask() error = %v", err)
	}
	if gotTask.Status != metadb.ChannelMigrationStatusAborted || gotTask.CompletedAtMS != 1750000002000 {
		t.Fatalf("task after abort = %#v", gotTask)
	}
	gotMeta, err := db.ForSlot(11).GetChannelRuntimeMeta(ctx, meta.ChannelID, meta.ChannelType)
	if err != nil {
		t.Fatalf("GetChannelRuntimeMeta() error = %v", err)
	}
	if gotMeta.ChannelEpoch != meta.ChannelEpoch+1 || !reflect.DeepEqual(gotMeta.Replicas, []uint64{1, 2}) {
		t.Fatalf("meta after abort = %#v", gotMeta)
	}
}

func TestStateMachineChannelCommandsRejectTaskNodeMismatch(t *testing.T) {
	t.Run("commit_leader", func(t *testing.T) {
		ctx := context.Background()
		db := openTestDB(t)
		sm := mustNewStateMachine(t, db, 11)
		task := fsmTestChannelMigrationTask("task-commit-mismatch", "channel-commit-mismatch")
		task.Kind = metadb.ChannelMigrationKindLeaderTransfer
		task.SourceNode = 1
		task.TargetNode = 2
		task.DesiredLeader = 2
		task.Status = metadb.ChannelMigrationStatusRunning
		task.Phase = metadb.ChannelMigrationPhaseCommitLeaderMeta
		task.UpdatedAtMS = 1750000001000
		task.FenceToken = task.TaskID
		task.FenceVersion = 7
		task.FenceUntilMS = 1750000009000
		setFSMTestDrainProof(&task, 7)
		meta := fsmTestFencedRuntimeMeta(task.ChannelID, task.ChannelType, task.TaskID, 7)
		meta.ChannelEpoch = task.BaseChannelEpoch
		meta.LeaderEpoch = task.BaseLeaderEpoch
		meta.Leader = task.SourceNode
		meta.Replicas = []uint64{1, 2, 3}
		meta.ISR = []uint64{1, 2, 3}
		fsmApplyOK(t, ctx, sm, 1, EncodeUpsertChannelRuntimeMetaCommand(meta))
		fsmApplyOK(t, ctx, sm, 2, EncodeCreateChannelMigrationTaskCommand(task))
		req := fsmTestCommitLeaderRequest(task, meta, 1750000002000)
		req.DesiredLeader = 3
		result, err := sm.Apply(ctx, multiraft.Command{SlotID: 11, Index: 3, Term: 1, Data: EncodeCommitChannelLeaderTransferCommand(req)})
		if err != nil {
			t.Fatalf("Apply(commit leader mismatch) error = %v", err)
		}
		if got := string(result); got != ApplyResultStaleMeta {
			t.Fatalf("commit leader mismatch result = %q, want %q", got, ApplyResultStaleMeta)
		}
	})

	t.Run("add_learner", func(t *testing.T) {
		ctx := context.Background()
		db := openTestDB(t)
		sm := mustNewStateMachine(t, db, 11)
		task := fsmTestChannelMigrationTask("task-add-mismatch", "channel-add-mismatch")
		task.Status = metadb.ChannelMigrationStatusRunning
		task.Phase = metadb.ChannelMigrationPhaseAddLearner
		task.UpdatedAtMS = 1750000001000
		meta := fsmTestRuntimeMeta(task.ChannelID, task.ChannelType)
		meta.ChannelEpoch = task.BaseChannelEpoch
		meta.LeaderEpoch = task.BaseLeaderEpoch
		fsmApplyOK(t, ctx, sm, 1, EncodeUpsertChannelRuntimeMetaCommand(meta))
		fsmApplyOK(t, ctx, sm, 2, EncodeCreateChannelMigrationTaskCommand(task))
		req := fsmTestAddLearnerRequest(task, meta, 1750000002000)
		req.TargetNode = 4
		result, err := sm.Apply(ctx, multiraft.Command{SlotID: 11, Index: 3, Term: 1, Data: EncodeAddChannelLearnerCommand(req)})
		if err != nil {
			t.Fatalf("Apply(add learner mismatch) error = %v", err)
		}
		if got := string(result); got != ApplyResultStaleMeta {
			t.Fatalf("add learner mismatch result = %q, want %q", got, ApplyResultStaleMeta)
		}
	})

	t.Run("promote", func(t *testing.T) {
		ctx := context.Background()
		db := openTestDB(t)
		sm := mustNewStateMachine(t, db, 11)
		task := fsmTestChannelMigrationTask("task-promote-mismatch", "channel-promote-mismatch")
		task.Status = metadb.ChannelMigrationStatusRunning
		task.Phase = metadb.ChannelMigrationPhasePromoteAndRemove
		task.UpdatedAtMS = 1750000001000
		task.FenceToken = task.TaskID
		task.FenceVersion = 7
		task.FenceUntilMS = 1750000009000
		setFSMTestDrainProof(&task, 7)
		meta := fsmTestFencedRuntimeMeta(task.ChannelID, task.ChannelType, task.TaskID, 7)
		meta.ChannelEpoch = task.BaseChannelEpoch
		meta.LeaderEpoch = task.BaseLeaderEpoch
		meta.Replicas = []uint64{1, 2, 3}
		meta.ISR = []uint64{1, 2}
		fsmApplyOK(t, ctx, sm, 1, EncodeUpsertChannelRuntimeMetaCommand(meta))
		fsmApplyOK(t, ctx, sm, 2, EncodeCreateChannelMigrationTaskCommand(task))
		req := fsmTestPromoteRequest(task, meta, 1750000002000)
		req.SourceNode = 1
		result, err := sm.Apply(ctx, multiraft.Command{SlotID: 11, Index: 3, Term: 1, Data: EncodePromoteLearnerAndRemoveReplicaCommand(req)})
		if err != nil {
			t.Fatalf("Apply(promote mismatch) error = %v", err)
		}
		if got := string(result); got != ApplyResultStaleMeta {
			t.Fatalf("promote mismatch result = %q, want %q", got, ApplyResultStaleMeta)
		}
	})
}

func TestStateMachineChannelAbortRejectsNonTerminalStatus(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm := mustNewStateMachine(t, db, 11)
	task := fsmTestChannelMigrationTask("task-abort-non-terminal", "channel-abort-non-terminal")
	task.Status = metadb.ChannelMigrationStatusRunning
	task.Phase = metadb.ChannelMigrationPhaseWarmCatchUp
	task.UpdatedAtMS = 1750000001000
	meta := fsmTestRuntimeMeta(task.ChannelID, task.ChannelType)
	meta.ChannelEpoch = task.BaseChannelEpoch
	meta.LeaderEpoch = task.BaseLeaderEpoch

	fsmApplyOK(t, ctx, sm, 1, EncodeUpsertChannelRuntimeMetaCommand(meta))
	fsmApplyOK(t, ctx, sm, 2, EncodeCreateChannelMigrationTaskCommand(task))
	req := fsmTestAbortRequest(task, meta, 1750000002000)
	req.Status = metadb.ChannelMigrationStatusRunning
	req.CompletedAtMS = 0

	_, err := sm.Apply(ctx, multiraft.Command{SlotID: 11, Index: 3, Term: 1, Data: EncodeAbortChannelMigrationCommand(req)})
	if err == nil {
		t.Fatal("Apply(abort non-terminal) error = nil, want error")
	}
	if !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("Apply(abort non-terminal) error = %v, want ErrInvalidArgument", err)
	}
}

func TestStateMachineChannelAdvancePersistsProgressWithoutMetaMutation(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm := mustNewStateMachine(t, db, 11)
	task := fsmTestChannelMigrationTask("task-advance-only", "channel-advance-only")
	meta := fsmTestRuntimeMeta(task.ChannelID, task.ChannelType)
	meta.ChannelEpoch = task.BaseChannelEpoch
	meta.LeaderEpoch = task.BaseLeaderEpoch
	progress := metadb.ChannelMigrationProgress{LeaderLEO: 20, TargetLEO: 18, LagRecords: 2}
	advanced := fsmTestChannelMigrationAdvance(task, metadb.ChannelMigrationStatusRunning, metadb.ChannelMigrationPhaseWarmCatchUp, 1750000001000)
	advanced.Attempt = 2
	advanced.NextRunAtMS = 1750000005000
	advanced.Progress = progress

	fsmApplyOK(t, ctx, sm, 1, EncodeUpsertChannelRuntimeMetaCommand(meta))
	fsmApplyOK(t, ctx, sm, 2, EncodeCreateChannelMigrationTaskCommand(task))
	fsmApplyOK(t, ctx, sm, 3, EncodeAdvanceChannelMigrationTaskCommand(advanced))

	gotTask, err := db.ForSlot(11).GetChannelMigrationTask(ctx, task.ChannelID, task.ChannelType, task.TaskID)
	if err != nil {
		t.Fatalf("GetChannelMigrationTask() error = %v", err)
	}
	if gotTask.Phase != metadb.ChannelMigrationPhaseWarmCatchUp || gotTask.Attempt != 2 || !reflect.DeepEqual(gotTask.Progress, progress) {
		t.Fatalf("task after advance = %#v", gotTask)
	}
	gotMeta, err := db.ForSlot(11).GetChannelRuntimeMeta(ctx, meta.ChannelID, meta.ChannelType)
	if err != nil {
		t.Fatalf("GetChannelRuntimeMeta() error = %v", err)
	}
	if !reflect.DeepEqual(gotMeta, meta) {
		t.Fatalf("meta mutated by advance = %#v, want %#v", gotMeta, meta)
	}
}

func TestStateMachineChannelAdvanceRejectsStalePhase(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm := mustNewStateMachine(t, db, 11)
	task := fsmTestChannelMigrationTask("task-stale-advance", "channel-stale-advance")
	fsmApplyOK(t, ctx, sm, 1, EncodeCreateChannelMigrationTaskCommand(task))

	stale := fsmTestChannelMigrationAdvance(task, metadb.ChannelMigrationStatusRunning, metadb.ChannelMigrationPhaseWarmCatchUp, 1750000001000)
	stale.Guard.ExpectedPhase = metadb.ChannelMigrationPhaseWarmCatchUp
	result, err := sm.Apply(ctx, multiraft.Command{SlotID: 11, Index: 2, Term: 1, Data: EncodeAdvanceChannelMigrationTaskCommand(stale)})
	if err != nil {
		t.Fatalf("Apply(stale advance) error = %v", err)
	}
	if got := string(result); got != ApplyResultStaleMeta {
		t.Fatalf("stale advance result = %q, want %q", got, ApplyResultStaleMeta)
	}
}

func fsmApplyOK(t *testing.T, ctx context.Context, sm multiraft.StateMachine, index uint64, data []byte) {
	t.Helper()
	result, err := sm.Apply(ctx, multiraft.Command{SlotID: 11, Index: index, Term: 1, Data: data})
	if err != nil {
		t.Fatalf("Apply(index=%d) error = %v", index, err)
	}
	if got := string(result); got != ApplyResultOK {
		t.Fatalf("Apply(index=%d) result = %q, want %q", index, got, ApplyResultOK)
	}
}

func fsmTestChannelMigrationTask(taskID, channelID string) metadb.ChannelMigrationTask {
	return metadb.ChannelMigrationTask{
		TaskID:           taskID,
		Kind:             metadb.ChannelMigrationKindReplicaReplace,
		Status:           metadb.ChannelMigrationStatusPending,
		Phase:            metadb.ChannelMigrationPhaseValidate,
		ChannelID:        channelID,
		ChannelType:      1,
		SourceNode:       2,
		TargetNode:       3,
		DesiredLeader:    1,
		BaseChannelEpoch: 10,
		BaseLeaderEpoch:  20,
		CreatedAtMS:      1750000000000,
		UpdatedAtMS:      1750000000000,
	}
}

func fsmTestRuntimeMeta(channelID string, channelType int64) metadb.ChannelRuntimeMeta {
	return metadb.ChannelRuntimeMeta{
		ChannelID:    channelID,
		ChannelType:  channelType,
		ChannelEpoch: 10,
		LeaderEpoch:  20,
		Replicas:     []uint64{1, 2},
		ISR:          []uint64{1, 2},
		Leader:       1,
		MinISR:       2,
		Status:       1,
		Features:     1,
		LeaseUntilMS: 1750000010000,
	}
}

func fsmTestFencedRuntimeMeta(channelID string, channelType int64, taskID string, version uint64) metadb.ChannelRuntimeMeta {
	meta := fsmTestRuntimeMeta(channelID, channelType)
	meta.WriteFenceToken = taskID
	meta.WriteFenceVersion = version
	meta.WriteFenceReason = 1
	meta.WriteFenceUntilMS = 1750000010000
	return meta
}

func fsmTestTaskGuard(task metadb.ChannelMigrationTask) metadb.ChannelMigrationTaskGuard {
	return metadb.ChannelMigrationTaskGuard{
		ChannelID:                 task.ChannelID,
		ChannelType:               task.ChannelType,
		TaskID:                    task.TaskID,
		ExpectedStatus:            task.Status,
		ExpectedPhase:             task.Phase,
		ExpectedOwnerNodeID:       task.OwnerNodeID,
		ExpectedOwnerLeaseUntilMS: task.OwnerLeaseUntilMS,
		ExpectedUpdatedAtMS:       task.UpdatedAtMS,
	}
}

func fsmTestRuntimeGuard(meta metadb.ChannelRuntimeMeta) metadb.ChannelMigrationRuntimeGuard {
	return metadb.ChannelMigrationRuntimeGuard{
		ChannelID:            meta.ChannelID,
		ChannelType:          meta.ChannelType,
		ExpectedChannelEpoch: meta.ChannelEpoch,
		ExpectedLeaderEpoch:  meta.LeaderEpoch,
		ExpectedLeader:       meta.Leader,
		ExpectedFenceToken:   meta.WriteFenceToken,
		ExpectedFenceVersion: meta.WriteFenceVersion,
	}
}

func fsmTestChannelMigrationClaim(task metadb.ChannelMigrationTask, owner uint64, leaseUntilMS, updatedAtMS int64) metadb.ChannelMigrationTaskClaim {
	return metadb.ChannelMigrationTaskClaim{
		Guard:             fsmTestTaskGuard(task),
		Status:            metadb.ChannelMigrationStatusRunning,
		Phase:             task.Phase,
		OwnerNodeID:       owner,
		OwnerLeaseUntilMS: leaseUntilMS,
		UpdatedAtMS:       updatedAtMS,
	}
}

func fsmTestChannelMigrationAdvance(task metadb.ChannelMigrationTask, status metadb.ChannelMigrationStatus, phase metadb.ChannelMigrationPhase, updatedAtMS int64) metadb.ChannelMigrationTaskAdvance {
	return metadb.ChannelMigrationTaskAdvance{
		Guard:       fsmTestTaskGuard(task),
		Status:      status,
		Phase:       phase,
		UpdatedAtMS: updatedAtMS,
	}
}

func fsmTestSetFenceRequest(task metadb.ChannelMigrationTask, meta metadb.ChannelRuntimeMeta, fenceUntilMS, updatedAtMS int64) metadb.ChannelMigrationFenceRequest {
	return metadb.ChannelMigrationFenceRequest{
		Guard:        fsmTestTaskGuard(task),
		RuntimeGuard: fsmTestRuntimeGuard(meta),
		Status:       metadb.ChannelMigrationStatusRunning,
		Phase:        metadb.ChannelMigrationPhaseCutoverFence,
		FenceReason:  1,
		FenceUntilMS: fenceUntilMS,
		UpdatedAtMS:  updatedAtMS,
	}
}

func fsmTestResetFenceRequest(task metadb.ChannelMigrationTask, meta metadb.ChannelRuntimeMeta, phase metadb.ChannelMigrationPhase, updatedAtMS int64) metadb.ChannelMigrationResetFenceRequest {
	return metadb.ChannelMigrationResetFenceRequest{
		Guard:        fsmTestTaskGuard(task),
		RuntimeGuard: fsmTestRuntimeGuard(meta),
		Status:       metadb.ChannelMigrationStatusRunning,
		Phase:        phase,
		UpdatedAtMS:  updatedAtMS,
	}
}

func fsmTestCommitLeaderRequest(task metadb.ChannelMigrationTask, meta metadb.ChannelRuntimeMeta, updatedAtMS int64) metadb.ChannelMigrationLeaderTransferRequest {
	return metadb.ChannelMigrationLeaderTransferRequest{
		Guard:           fsmTestTaskGuard(task),
		RuntimeGuard:    fsmTestRuntimeGuard(meta),
		Status:          metadb.ChannelMigrationStatusRunning,
		Phase:           metadb.ChannelMigrationPhaseVerifyNewLeader,
		DesiredLeader:   task.TargetNode,
		NextLeaderEpoch: meta.LeaderEpoch + 1,
		LeaseUntilMS:    meta.LeaseUntilMS + 1000,
		NowMS:           meta.WriteFenceUntilMS - 1,
		UpdatedAtMS:     updatedAtMS,
	}
}

func fsmTestAddLearnerRequest(task metadb.ChannelMigrationTask, meta metadb.ChannelRuntimeMeta, updatedAtMS int64) metadb.ChannelMigrationAddLearnerRequest {
	return metadb.ChannelMigrationAddLearnerRequest{
		Guard:        fsmTestTaskGuard(task),
		RuntimeGuard: fsmTestRuntimeGuard(meta),
		Status:       metadb.ChannelMigrationStatusRunning,
		Phase:        metadb.ChannelMigrationPhaseBootstrapTarget,
		TargetNode:   task.TargetNode,
		UpdatedAtMS:  updatedAtMS,
	}
}

func fsmTestPromoteRequest(task metadb.ChannelMigrationTask, meta metadb.ChannelRuntimeMeta, updatedAtMS int64) metadb.ChannelMigrationPromoteLearnerRequest {
	return metadb.ChannelMigrationPromoteLearnerRequest{
		Guard:        fsmTestTaskGuard(task),
		RuntimeGuard: fsmTestRuntimeGuard(meta),
		Status:       metadb.ChannelMigrationStatusRunning,
		Phase:        metadb.ChannelMigrationPhaseVerifyMembership,
		SourceNode:   task.SourceNode,
		TargetNode:   task.TargetNode,
		NowMS:        meta.WriteFenceUntilMS - 1,
		UpdatedAtMS:  updatedAtMS,
	}
}

func fsmTestClearFenceRequest(task metadb.ChannelMigrationTask, meta metadb.ChannelRuntimeMeta, updatedAtMS int64) metadb.ChannelMigrationClearFenceRequest {
	return metadb.ChannelMigrationClearFenceRequest{
		Guard:         fsmTestTaskGuard(task),
		RuntimeGuard:  fsmTestRuntimeGuard(meta),
		Status:        metadb.ChannelMigrationStatusCompleted,
		Phase:         metadb.ChannelMigrationPhaseClearFence,
		UpdatedAtMS:   updatedAtMS,
		CompletedAtMS: updatedAtMS,
	}
}

func fsmTestAbortRequest(task metadb.ChannelMigrationTask, meta metadb.ChannelRuntimeMeta, updatedAtMS int64) metadb.ChannelMigrationAbortRequest {
	return metadb.ChannelMigrationAbortRequest{
		Guard:         fsmTestTaskGuard(task),
		RuntimeGuard:  fsmTestRuntimeGuard(meta),
		Status:        metadb.ChannelMigrationStatusAborted,
		Phase:         task.Phase,
		UpdatedAtMS:   updatedAtMS,
		CompletedAtMS: updatedAtMS,
		LastError:     "operator aborted",
	}
}

func setFSMTestDrainProof(task *metadb.ChannelMigrationTask, fenceVersion uint64) {
	task.CutoverLEO = 100
	task.CutoverHW = 99
	task.DrainedLeaderNode = 1
	task.DrainedRuntimeGeneration = 2
	task.DrainedChannelEpoch = task.BaseChannelEpoch
	task.DrainedLeaderEpoch = task.BaseLeaderEpoch
	task.DrainedFenceVersion = fenceVersion
}

func requireFSMStaleResult(t *testing.T, result []byte, err error) {
	t.Helper()
	if err != nil && !errors.Is(err, metadb.ErrStaleMeta) {
		t.Fatalf("unexpected stale result error = %v", err)
	}
	if got := string(result); got != ApplyResultStaleMeta {
		t.Fatalf("stale result = %q, want %q", got, ApplyResultStaleMeta)
	}
}
