package fsm

import (
	"context"
	"errors"
	"reflect"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
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
			name: "create_with_runtime_guard",
			data: EncodeCreateChannelMigrationTaskWithRuntimeGuardCommand(metadb.ChannelMigrationTaskCreate{
				Task:         fsmTestChannelMigrationTask("task-create-guard", "channel-create-guard"),
				RuntimeGuard: fsmTestRuntimeGuard(fsmTestRuntimeMeta("channel-create-guard", 1)),
			}),
			want: &createChannelMigrationTaskWithRuntimeGuardCmd{req: metadb.ChannelMigrationTaskCreate{
				Task:         fsmTestChannelMigrationTask("task-create-guard", "channel-create-guard"),
				RuntimeGuard: fsmTestRuntimeGuard(fsmTestRuntimeMeta("channel-create-guard", 1)),
			}},
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
		{
			name: "garbage_collect",
			data: EncodeGarbageCollectTerminalChannelMigrationTasksCommand(metadb.ChannelMigrationTaskGCRequest{BeforeMS: 1750000010000, Limit: 10}),
			want: &garbageCollectMigrationTasksCmd{req: metadb.ChannelMigrationTaskGCRequest{BeforeMS: 1750000010000, Limit: 10}},
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

func TestChannelMigrationCommandRejectsDuplicatePayload(t *testing.T) {
	data := []byte{commandVersion, cmdTypeCreateChannelMigrationTask}
	data = appendBytesTLVField(data, tagChannelMigrationCommandPayload, []byte(`{}`))
	data = appendBytesTLVField(data, tagChannelMigrationCommandPayload, []byte(`{}`))

	_, err := decodeCommand(data)
	if err == nil {
		t.Fatal("decodeCommand(duplicate payload) error = nil, want error")
	}
	if !errors.Is(err, metadb.ErrCorruptValue) {
		t.Fatalf("decodeCommand(duplicate payload) error = %v, want ErrCorruptValue", err)
	}
}

func TestChannelMigrationCommandRejectsAmbiguousJSONPayload(t *testing.T) {
	tests := []struct {
		name    string
		payload string
	}{
		{
			name:    "unknown_field",
			payload: `{"TaskID":"task-json","UnknownField":true}`,
		},
		{
			name:    "duplicate_key",
			payload: `{"TaskID":"task-json","TaskID":"task-json-2"}`,
		},
		{
			name:    "trailing_value",
			payload: `{"TaskID":"task-json"} {}`,
		},
		{
			name:    "case_insensitive_duplicate_key",
			payload: `{"TaskID":"task-json","taskid":"task-json-2"}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data := []byte{commandVersion, cmdTypeCreateChannelMigrationTask}
			data = appendBytesTLVField(data, tagChannelMigrationCommandPayload, []byte(tc.payload))

			_, err := decodeCommand(data)
			if err == nil {
				t.Fatalf("decodeCommand(%s) error = nil, want error", tc.name)
			}
			if !errors.Is(err, metadb.ErrCorruptValue) {
				t.Fatalf("decodeCommand(%s) error = %v, want ErrCorruptValue", tc.name, err)
			}
		})
	}
}

func TestStateMachineStaleCommitErrorsAreDeterministicResults(t *testing.T) {
	for _, err := range []error{metadb.ErrStaleMeta, metadb.ErrNotFound, metadb.ErrAlreadyExists} {
		if !isStaleMetaCommitError(err) {
			t.Fatalf("isStaleMetaCommitError(%v) = false, want true", err)
		}
	}
	if isStaleMetaCommitError(metadb.ErrInvalidArgument) {
		t.Fatal("isStaleMetaCommitError(ErrInvalidArgument) = true, want false")
	}
}

func TestStateMachineChannelApplyBatchRuntimeMetaCreateThenAddLearner(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm := mustNewStateMachine(t, db, 11)
	task := fsmTestChannelMigrationTask("task-add-same-batch", "channel-add-same-batch")
	task.Status = metadb.ChannelMigrationStatusRunning
	task.Phase = metadb.ChannelMigrationPhaseAddLearner
	task.UpdatedAtMS = 1750000001000
	meta := fsmTestRuntimeMeta(task.ChannelID, task.ChannelType)
	meta.ChannelEpoch = task.BaseChannelEpoch
	meta.LeaderEpoch = task.BaseLeaderEpoch
	meta.Replicas = []uint64{1, 2}
	meta.ISR = []uint64{1, 2}

	batchSM := sm.(multiraft.BatchStateMachine)
	results, err := batchSM.ApplyBatch(ctx, []multiraft.Command{
		{SlotID: 11, Index: 1, Term: 1, Data: EncodeUpsertChannelRuntimeMetaCommand(meta)},
		{SlotID: 11, Index: 2, Term: 1, Data: EncodeCreateChannelMigrationTaskCommand(task)},
		{SlotID: 11, Index: 3, Term: 1, Data: EncodeAddChannelLearnerCommand(fsmTestAddLearnerRequest(task, meta, 1750000002000))},
	})
	if err != nil {
		t.Fatalf("ApplyBatch(runtime meta + create + add learner) error = %v", err)
	}
	if got := []string{string(results[0]), string(results[1]), string(results[2])}; !reflect.DeepEqual(got, []string{ApplyResultOK, ApplyResultOK, ApplyResultOK}) {
		t.Fatalf("runtime meta + create + add learner results = %#v", got)
	}

	gotTask, err := db.ForSlot(11).GetChannelMigrationTask(ctx, task.ChannelID, task.ChannelType, task.TaskID)
	if err != nil {
		t.Fatalf("GetChannelMigrationTask() error = %v", err)
	}
	if gotTask.Phase != metadb.ChannelMigrationPhaseBootstrapTarget {
		t.Fatalf("task phase after same-batch add learner = %v, want BootstrapTarget", gotTask.Phase)
	}
	gotMeta, err := db.ForSlot(11).GetChannelRuntimeMeta(ctx, meta.ChannelID, meta.ChannelType)
	if err != nil {
		t.Fatalf("GetChannelRuntimeMeta() error = %v", err)
	}
	if gotMeta.ChannelEpoch != meta.ChannelEpoch+1 || !reflect.DeepEqual(gotMeta.Replicas, []uint64{1, 2, 3}) {
		t.Fatalf("meta after same-batch add learner = %#v", gotMeta)
	}
}

func TestStateMachineCreateChannelMigrationTaskWithRuntimeGuardRejectsStaleMeta(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm := mustNewStateMachine(t, db, 11)
	task := fsmTestChannelMigrationTask("task-create-stale-runtime-guard", "channel-create-stale-runtime-guard")
	meta := fsmTestRuntimeMeta(task.ChannelID, task.ChannelType)
	fsmApplyOK(t, ctx, sm, 1, EncodeUpsertChannelRuntimeMetaCommand(meta))
	changed := meta
	changed.LeaderEpoch++
	fsmApplyOK(t, ctx, sm, 2, EncodeUpsertChannelRuntimeMetaCommand(changed))

	result, err := sm.Apply(ctx, multiraft.Command{
		SlotID: 11,
		Index:  3,
		Term:   1,
		Data: EncodeCreateChannelMigrationTaskWithRuntimeGuardCommand(metadb.ChannelMigrationTaskCreate{
			Task:         task,
			RuntimeGuard: fsmTestRuntimeGuard(meta),
		}),
	})
	if err != nil {
		t.Fatalf("Apply(create with runtime guard) error = %v", err)
	}
	if got := string(result); got != ApplyResultStaleMeta {
		t.Fatalf("create with stale runtime guard result = %q, want %q", got, ApplyResultStaleMeta)
	}
}

func TestStateMachineChannelSetFenceRejectsForeignRuntimeFence(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm := mustNewStateMachine(t, db, 11)
	task := fsmTestChannelMigrationTask("task-set-foreign-fence", "channel-set-foreign-fence")
	task.Status = metadb.ChannelMigrationStatusRunning
	task.Phase = metadb.ChannelMigrationPhaseWarmCatchUp
	task.UpdatedAtMS = 1750000001000
	meta := fsmTestFencedRuntimeMeta(task.ChannelID, task.ChannelType, "foreign-task", 7)
	meta.ChannelEpoch = task.BaseChannelEpoch
	meta.LeaderEpoch = task.BaseLeaderEpoch

	fsmApplyOK(t, ctx, sm, 1, EncodeUpsertChannelRuntimeMetaCommand(meta))
	fsmApplyOK(t, ctx, sm, 2, EncodeCreateChannelMigrationTaskCommand(task))
	result, err := sm.Apply(ctx, multiraft.Command{SlotID: 11, Index: 3, Term: 1, Data: EncodeSetChannelWriteFenceCommand(fsmTestSetFenceRequest(task, meta, 1750000009000, 1750000002000))})
	requireFSMStaleResult(t, result, err)
}

func TestStateMachineChannelSetFenceRenewalClearsDrainProof(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm := mustNewStateMachine(t, db, 11)
	task := fsmTestChannelMigrationTask("task-renew-proof", "channel-renew-proof")
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
	req := fsmTestSetFenceRequest(task, meta, 1750000010000, 1750000002000)
	req.Phase = task.Phase
	fsmApplyOK(t, ctx, sm, 3, EncodeSetChannelWriteFenceCommand(req))

	gotTask, err := db.ForSlot(11).GetChannelMigrationTask(ctx, task.ChannelID, task.ChannelType, task.TaskID)
	if err != nil {
		t.Fatalf("GetChannelMigrationTask() error = %v", err)
	}
	if gotTask.FenceVersion != 8 ||
		gotTask.CutoverLEO != 0 ||
		gotTask.CutoverHW != 0 ||
		gotTask.DrainedFenceVersion != 0 ||
		gotTask.DrainedRuntimeGeneration != 0 {
		t.Fatalf("task after fence renewal = %#v", gotTask)
	}
	gotMeta, err := db.ForSlot(11).GetChannelRuntimeMeta(ctx, meta.ChannelID, meta.ChannelType)
	if err != nil {
		t.Fatalf("GetChannelRuntimeMeta() error = %v", err)
	}
	if gotMeta.WriteFenceVersion != 8 || gotMeta.WriteFenceToken != task.TaskID {
		t.Fatalf("meta after fence renewal = %#v", gotMeta)
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

func TestStateMachineChannelAddLearnerRejectsInvalidSourceTargetRoles(t *testing.T) {
	tests := []struct {
		name     string
		replicas []uint64
		isr      []uint64
	}{
		{
			name:     "source_not_isr",
			replicas: []uint64{1, 2},
			isr:      []uint64{1},
		},
		{
			name:     "target_already_learner",
			replicas: []uint64{1, 2, 3},
			isr:      []uint64{1, 2},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			db := openTestDB(t)
			sm := mustNewStateMachine(t, db, 11)
			task := fsmTestChannelMigrationTask("task-add-invalid-"+tc.name, "channel-add-invalid-"+tc.name)
			task.Status = metadb.ChannelMigrationStatusRunning
			task.Phase = metadb.ChannelMigrationPhaseAddLearner
			task.UpdatedAtMS = 1750000001000
			meta := fsmTestRuntimeMeta(task.ChannelID, task.ChannelType)
			meta.ChannelEpoch = task.BaseChannelEpoch
			meta.LeaderEpoch = task.BaseLeaderEpoch
			meta.Replicas = tc.replicas
			meta.ISR = tc.isr

			fsmApplyOK(t, ctx, sm, 1, EncodeUpsertChannelRuntimeMetaCommand(meta))
			fsmApplyOK(t, ctx, sm, 2, EncodeCreateChannelMigrationTaskCommand(task))
			result, err := sm.Apply(ctx, multiraft.Command{SlotID: 11, Index: 3, Term: 1, Data: EncodeAddChannelLearnerCommand(fsmTestAddLearnerRequest(task, meta, 1750000002000))})
			requireFSMStaleResult(t, result, err)
		})
	}
}

func TestStateMachineChannelAddLearnerRejectsSourceStillLeader(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm := mustNewStateMachine(t, db, 11)
	task := fsmTestChannelMigrationTask("task-add-source-leader", "channel-add-source-leader")
	task.SourceNode = 1
	task.TargetNode = 3
	task.Status = metadb.ChannelMigrationStatusRunning
	task.Phase = metadb.ChannelMigrationPhaseAddLearner
	task.UpdatedAtMS = 1750000001000
	meta := fsmTestRuntimeMeta(task.ChannelID, task.ChannelType)
	meta.ChannelEpoch = task.BaseChannelEpoch
	meta.LeaderEpoch = task.BaseLeaderEpoch
	meta.Leader = task.SourceNode
	meta.Replicas = []uint64{1, 2}
	meta.ISR = []uint64{1, 2}

	fsmApplyOK(t, ctx, sm, 1, EncodeUpsertChannelRuntimeMetaCommand(meta))
	fsmApplyOK(t, ctx, sm, 2, EncodeCreateChannelMigrationTaskCommand(task))
	result, err := sm.Apply(ctx, multiraft.Command{SlotID: 11, Index: 3, Term: 1, Data: EncodeAddChannelLearnerCommand(fsmTestAddLearnerRequest(task, meta, 1750000002000))})
	requireFSMStaleResult(t, result, err)

	got, err := db.ForSlot(11).GetChannelRuntimeMeta(ctx, meta.ChannelID, meta.ChannelType)
	if err != nil {
		t.Fatalf("GetChannelRuntimeMeta() error = %v", err)
	}
	if got.Leader != meta.Leader || !reflect.DeepEqual(got.Replicas, meta.Replicas) || !reflect.DeepEqual(got.ISR, meta.ISR) {
		t.Fatalf("meta after stale add learner = %#v, want %#v", got, meta)
	}
}

func TestStateMachineChannelCommitLeaderRequiresCutoverProof(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm := mustNewStateMachine(t, db, 11)
	task := fsmTestLeaderTransferTask("task-commit-no-proof", "channel-commit-no-proof")
	task.Phase = metadb.ChannelMigrationPhaseCommitLeaderMeta
	task.FenceToken = task.TaskID
	task.FenceVersion = 7
	task.FenceUntilMS = 1750000009000
	meta := fsmTestFencedRuntimeMeta(task.ChannelID, task.ChannelType, task.TaskID, 7)
	meta.ChannelEpoch = task.BaseChannelEpoch
	meta.LeaderEpoch = task.BaseLeaderEpoch
	meta.Leader = task.SourceNode
	meta.Replicas = []uint64{1, 2, 3}
	meta.ISR = []uint64{1, 2, 3}

	fsmApplyOK(t, ctx, sm, 1, EncodeUpsertChannelRuntimeMetaCommand(meta))
	fsmApplyOK(t, ctx, sm, 2, EncodeCreateChannelMigrationTaskCommand(task))
	result, err := sm.Apply(ctx, multiraft.Command{SlotID: 11, Index: 3, Term: 1, Data: EncodeCommitChannelLeaderTransferCommand(fsmTestCommitLeaderRequest(task, meta, 1750000002000))})
	requireFSMStaleResult(t, result, err)
}

func TestStateMachineChannelCommitLeaderRejectsMismatchedCutoverProof(t *testing.T) {
	tests := []struct {
		name   string
		adjust func(*metadb.ChannelMigrationTask, *metadb.ChannelRuntimeMeta)
	}{
		{
			name: "proof_leader_epoch_mismatch",
			adjust: func(task *metadb.ChannelMigrationTask, _ *metadb.ChannelRuntimeMeta) {
				task.DrainedLeaderEpoch = task.BaseLeaderEpoch - 1
			},
		},
		{
			name: "renewed_fence_invalidates_proof",
			adjust: func(task *metadb.ChannelMigrationTask, meta *metadb.ChannelRuntimeMeta) {
				meta.WriteFenceVersion = task.FenceVersion + 1
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			db := openTestDB(t)
			sm := mustNewStateMachine(t, db, 11)
			task := fsmTestLeaderTransferTask("task-commit-"+tc.name, "channel-commit-"+tc.name)
			task.Phase = metadb.ChannelMigrationPhaseCommitLeaderMeta
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
			tc.adjust(&task, &meta)

			fsmApplyOK(t, ctx, sm, 1, EncodeUpsertChannelRuntimeMetaCommand(meta))
			fsmApplyOK(t, ctx, sm, 2, EncodeCreateChannelMigrationTaskCommand(task))
			result, err := sm.Apply(ctx, multiraft.Command{SlotID: 11, Index: 3, Term: 1, Data: EncodeCommitChannelLeaderTransferCommand(fsmTestCommitLeaderRequest(task, meta, 1750000002000))})
			requireFSMStaleResult(t, result, err)
		})
	}
}

func TestStateMachineChannelPromoteRequiresMatchingCutoverProof(t *testing.T) {
	tests := []struct {
		name   string
		adjust func(*metadb.ChannelMigrationTask, *metadb.ChannelRuntimeMeta)
	}{
		{
			name:   "missing_proof",
			adjust: func(_ *metadb.ChannelMigrationTask, _ *metadb.ChannelRuntimeMeta) {},
		},
		{
			name: "proof_leader_epoch_mismatch",
			adjust: func(task *metadb.ChannelMigrationTask, _ *metadb.ChannelRuntimeMeta) {
				setFSMTestDrainProof(task, 7)
				task.DrainedLeaderEpoch = task.BaseLeaderEpoch - 1
			},
		},
		{
			name: "renewed_fence_invalidates_proof",
			adjust: func(task *metadb.ChannelMigrationTask, meta *metadb.ChannelRuntimeMeta) {
				setFSMTestDrainProof(task, 7)
				meta.WriteFenceVersion = task.FenceVersion + 1
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			db := openTestDB(t)
			sm := mustNewStateMachine(t, db, 11)
			task := fsmTestChannelMigrationTask("task-promote-"+tc.name, "channel-promote-"+tc.name)
			task.Status = metadb.ChannelMigrationStatusRunning
			task.Phase = metadb.ChannelMigrationPhasePromoteAndRemove
			task.UpdatedAtMS = 1750000001000
			task.FenceToken = task.TaskID
			task.FenceVersion = 7
			task.FenceUntilMS = 1750000009000
			meta := fsmTestFencedRuntimeMeta(task.ChannelID, task.ChannelType, task.TaskID, 7)
			meta.ChannelEpoch = task.BaseChannelEpoch
			meta.LeaderEpoch = task.BaseLeaderEpoch
			meta.Replicas = []uint64{1, 2, 3}
			meta.ISR = []uint64{1, 2}
			tc.adjust(&task, &meta)

			fsmApplyOK(t, ctx, sm, 1, EncodeUpsertChannelRuntimeMetaCommand(meta))
			fsmApplyOK(t, ctx, sm, 2, EncodeCreateChannelMigrationTaskCommand(task))
			result, err := sm.Apply(ctx, multiraft.Command{SlotID: 11, Index: 3, Term: 1, Data: EncodePromoteLearnerAndRemoveReplicaCommand(fsmTestPromoteRequest(task, meta, 1750000002000))})
			requireFSMStaleResult(t, result, err)
		})
	}
}

func TestStateMachineChannelClearRejectsTaskRuntimeFenceMismatch(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm := mustNewStateMachine(t, db, 11)
	task := fsmTestChannelMigrationTask("task-clear-foreign-fence", "channel-clear-foreign-fence")
	task.Status = metadb.ChannelMigrationStatusRunning
	task.Phase = metadb.ChannelMigrationPhaseVerifyMembership
	task.UpdatedAtMS = 1750000001000
	task.FenceToken = task.TaskID
	task.FenceVersion = 7
	task.FenceUntilMS = 1750000009000
	meta := fsmTestFencedRuntimeMeta(task.ChannelID, task.ChannelType, "foreign-task", 7)
	meta.ChannelEpoch = task.BaseChannelEpoch
	meta.LeaderEpoch = task.BaseLeaderEpoch

	fsmApplyOK(t, ctx, sm, 1, EncodeUpsertChannelRuntimeMetaCommand(meta))
	fsmApplyOK(t, ctx, sm, 2, EncodeCreateChannelMigrationTaskCommand(task))
	result, err := sm.Apply(ctx, multiraft.Command{SlotID: 11, Index: 3, Term: 1, Data: EncodeClearChannelWriteFenceCommand(fsmTestClearFenceRequest(task, meta, 1750000002000))})
	requireFSMStaleResult(t, result, err)
}

func TestStateMachineChannelClearRejectsPrematureCompletion(t *testing.T) {
	tests := []struct {
		name     string
		task     metadb.ChannelMigrationTask
		meta     metadb.ChannelRuntimeMeta
		reqPhase metadb.ChannelMigrationPhase
	}{
		func() struct {
			name     string
			task     metadb.ChannelMigrationTask
			meta     metadb.ChannelRuntimeMeta
			reqPhase metadb.ChannelMigrationPhase
		} {
			task := fsmTestLeaderTransferTask("task-clear-lt-write-fence", "channel-clear-lt-write-fence")
			task.Phase = metadb.ChannelMigrationPhaseWriteFence
			task.FenceToken = task.TaskID
			task.FenceVersion = 7
			task.FenceUntilMS = 1750000009000
			meta := fsmTestFencedRuntimeMeta(task.ChannelID, task.ChannelType, task.TaskID, 7)
			meta.ChannelEpoch = task.BaseChannelEpoch
			meta.LeaderEpoch = task.BaseLeaderEpoch
			meta.Leader = task.SourceNode
			meta.Replicas = []uint64{1, 2, 3}
			meta.ISR = []uint64{1, 2, 3}
			return struct {
				name     string
				task     metadb.ChannelMigrationTask
				meta     metadb.ChannelRuntimeMeta
				reqPhase metadb.ChannelMigrationPhase
			}{name: "leader_transfer_write_fence", task: task, meta: meta}
		}(),
		func() struct {
			name     string
			task     metadb.ChannelMigrationTask
			meta     metadb.ChannelRuntimeMeta
			reqPhase metadb.ChannelMigrationPhase
		} {
			task := fsmTestLeaderTransferTask("task-clear-lt-commit", "channel-clear-lt-commit")
			task.Phase = metadb.ChannelMigrationPhaseCommitLeaderMeta
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
			return struct {
				name     string
				task     metadb.ChannelMigrationTask
				meta     metadb.ChannelRuntimeMeta
				reqPhase metadb.ChannelMigrationPhase
			}{name: "leader_transfer_commit_meta", task: task, meta: meta}
		}(),
		func() struct {
			name     string
			task     metadb.ChannelMigrationTask
			meta     metadb.ChannelRuntimeMeta
			reqPhase metadb.ChannelMigrationPhase
		} {
			task := fsmTestChannelMigrationTask("task-clear-rr-cutover", "channel-clear-rr-cutover")
			task.Status = metadb.ChannelMigrationStatusRunning
			task.Phase = metadb.ChannelMigrationPhaseCutoverFence
			task.UpdatedAtMS = 1750000001000
			task.FenceToken = task.TaskID
			task.FenceVersion = 7
			task.FenceUntilMS = 1750000009000
			meta := fsmTestFencedRuntimeMeta(task.ChannelID, task.ChannelType, task.TaskID, 7)
			meta.ChannelEpoch = task.BaseChannelEpoch
			meta.LeaderEpoch = task.BaseLeaderEpoch
			meta.Replicas = []uint64{1, 2, 3}
			meta.ISR = []uint64{1, 2}
			return struct {
				name     string
				task     metadb.ChannelMigrationTask
				meta     metadb.ChannelRuntimeMeta
				reqPhase metadb.ChannelMigrationPhase
			}{name: "replica_replace_cutover", task: task, meta: meta}
		}(),
		func() struct {
			name     string
			task     metadb.ChannelMigrationTask
			meta     metadb.ChannelRuntimeMeta
			reqPhase metadb.ChannelMigrationPhase
		} {
			task := fsmTestChannelMigrationTask("task-clear-rr-promote", "channel-clear-rr-promote")
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
			return struct {
				name     string
				task     metadb.ChannelMigrationTask
				meta     metadb.ChannelRuntimeMeta
				reqPhase metadb.ChannelMigrationPhase
			}{name: "replica_replace_promote", task: task, meta: meta}
		}(),
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			db := openTestDB(t)
			sm := mustNewStateMachine(t, db, 11)
			fsmApplyOK(t, ctx, sm, 1, EncodeUpsertChannelRuntimeMetaCommand(tc.meta))
			fsmApplyOK(t, ctx, sm, 2, EncodeCreateChannelMigrationTaskCommand(tc.task))
			result, err := sm.Apply(ctx, multiraft.Command{SlotID: 11, Index: 3, Term: 1, Data: EncodeClearChannelWriteFenceCommand(fsmTestClearFenceRequest(tc.task, tc.meta, 1750000002000))})
			requireFSMStaleResult(t, result, err)
		})
	}
}

func TestStateMachineChannelClearEmbeddedLeaderTransferAdvancesToAddLearner(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm := mustNewStateMachine(t, db, 11)
	task := fsmTestChannelMigrationTask("task-clear-embedded", "channel-clear-embedded")
	task.Status = metadb.ChannelMigrationStatusRunning
	task.Phase = metadb.ChannelMigrationPhaseVerifyNewLeader
	task.UpdatedAtMS = 1750000001000
	task.EmbeddedLeaderTransfer = true
	task.EmbeddedDesiredLeader = task.DesiredLeader
	task.FenceToken = task.TaskID
	task.FenceVersion = 7
	task.FenceUntilMS = 1750000009000
	setFSMTestDrainProof(&task, 7)
	meta := fsmTestFencedRuntimeMeta(task.ChannelID, task.ChannelType, task.TaskID, 7)
	meta.ChannelEpoch = task.BaseChannelEpoch
	meta.LeaderEpoch = task.BaseLeaderEpoch
	meta.Leader = task.EmbeddedDesiredLeader
	meta.Replicas = []uint64{1, 2}
	meta.ISR = []uint64{1, 2}

	fsmApplyOK(t, ctx, sm, 1, EncodeUpsertChannelRuntimeMetaCommand(meta))
	fsmApplyOK(t, ctx, sm, 2, EncodeCreateChannelMigrationTaskCommand(task))
	req := fsmTestClearFenceRequest(task, meta, 1750000002000)
	req.Status = metadb.ChannelMigrationStatusRunning
	req.Phase = metadb.ChannelMigrationPhaseAddLearner
	req.CompletedAtMS = 0
	fsmApplyOK(t, ctx, sm, 3, EncodeClearChannelWriteFenceCommand(req))

	gotTask, err := db.ForSlot(11).GetChannelMigrationTask(ctx, task.ChannelID, task.ChannelType, task.TaskID)
	if err != nil {
		t.Fatalf("GetChannelMigrationTask() error = %v", err)
	}
	if gotTask.Status != metadb.ChannelMigrationStatusRunning ||
		gotTask.Phase != metadb.ChannelMigrationPhaseAddLearner ||
		gotTask.FenceToken != "" ||
		gotTask.DrainedFenceVersion != 0 ||
		gotTask.EmbeddedLeaderTransfer ||
		gotTask.EmbeddedDesiredLeader != 0 ||
		gotTask.CompletedAtMS != 0 {
		t.Fatalf("task after embedded clear = %#v", gotTask)
	}
	gotMeta, err := db.ForSlot(11).GetChannelRuntimeMeta(ctx, meta.ChannelID, meta.ChannelType)
	if err != nil {
		t.Fatalf("GetChannelRuntimeMeta() error = %v", err)
	}
	if gotMeta.WriteFenceToken != "" || gotMeta.WriteFenceVersion != 8 {
		t.Fatalf("meta after embedded clear = %#v", gotMeta)
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

func TestStateMachineChannelSetFenceAfterResetClearMarker(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm := mustNewStateMachine(t, db, 11)
	task := fsmTestChannelMigrationTask("task-set-after-reset", "channel-set-after-reset")
	task.Status = metadb.ChannelMigrationStatusRunning
	task.Phase = metadb.ChannelMigrationPhasePromoteAndRemove
	task.UpdatedAtMS = 1750000001000
	task.FenceToken = task.TaskID
	task.FenceVersion = 7
	task.FenceUntilMS = 1750000002000
	setFSMTestDrainProof(&task, 7)
	meta := fsmTestFencedRuntimeMeta(task.ChannelID, task.ChannelType, task.TaskID, 7)
	meta.WriteFenceUntilMS = task.FenceUntilMS
	meta.ChannelEpoch = task.BaseChannelEpoch
	meta.LeaderEpoch = task.BaseLeaderEpoch
	meta.Replicas = []uint64{1, 2, 3}
	meta.ISR = []uint64{1, 2}

	fsmApplyOK(t, ctx, sm, 1, EncodeUpsertChannelRuntimeMetaCommand(meta))
	fsmApplyOK(t, ctx, sm, 2, EncodeCreateChannelMigrationTaskCommand(task))
	resetReq := fsmTestResetFenceRequest(task, meta, metadb.ChannelMigrationPhaseWarmCatchUp, 1750000003000)
	fsmApplyOK(t, ctx, sm, 3, EncodeResetChannelWriteFenceToPreCutoverCommand(resetReq))

	resetTask, err := db.ForSlot(11).GetChannelMigrationTask(ctx, task.ChannelID, task.ChannelType, task.TaskID)
	if err != nil {
		t.Fatalf("GetChannelMigrationTask(reset) error = %v", err)
	}
	resetMeta, err := db.ForSlot(11).GetChannelRuntimeMeta(ctx, meta.ChannelID, meta.ChannelType)
	if err != nil {
		t.Fatalf("GetChannelRuntimeMeta(reset) error = %v", err)
	}
	setReq := fsmTestSetFenceRequest(resetTask, resetMeta, 1750000010000, 1750000004000)
	fsmApplyOK(t, ctx, sm, 4, EncodeSetChannelWriteFenceCommand(setReq))

	gotTask, err := db.ForSlot(11).GetChannelMigrationTask(ctx, task.ChannelID, task.ChannelType, task.TaskID)
	if err != nil {
		t.Fatalf("GetChannelMigrationTask(set) error = %v", err)
	}
	if gotTask.Phase != metadb.ChannelMigrationPhaseCutoverFence || gotTask.FenceVersion != 9 {
		t.Fatalf("task after set on reset marker = %#v", gotTask)
	}
	gotMeta, err := db.ForSlot(11).GetChannelRuntimeMeta(ctx, meta.ChannelID, meta.ChannelType)
	if err != nil {
		t.Fatalf("GetChannelRuntimeMeta(set) error = %v", err)
	}
	if gotMeta.WriteFenceToken != task.TaskID || gotMeta.WriteFenceVersion != 9 {
		t.Fatalf("meta after set on reset marker = %#v", gotMeta)
	}
}

func TestStateMachineChannelResetRejectsLiveMatchingFence(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm := mustNewStateMachine(t, db, 11)
	task := fsmTestChannelMigrationTask("task-reset-live-fence", "channel-reset-live-fence")
	task.Status = metadb.ChannelMigrationStatusRunning
	task.Phase = metadb.ChannelMigrationPhasePromoteAndRemove
	task.UpdatedAtMS = 1750000001000
	task.FenceToken = task.TaskID
	task.FenceVersion = 7
	task.FenceUntilMS = 1750000009000
	setFSMTestDrainProof(&task, 7)
	meta := fsmTestFencedRuntimeMeta(task.ChannelID, task.ChannelType, task.TaskID, 7)
	meta.WriteFenceUntilMS = task.FenceUntilMS
	meta.ChannelEpoch = task.BaseChannelEpoch
	meta.LeaderEpoch = task.BaseLeaderEpoch

	fsmApplyOK(t, ctx, sm, 1, EncodeUpsertChannelRuntimeMetaCommand(meta))
	fsmApplyOK(t, ctx, sm, 2, EncodeCreateChannelMigrationTaskCommand(task))
	req := fsmTestResetFenceRequest(task, meta, metadb.ChannelMigrationPhaseWarmCatchUp, 1750000003000)
	req.NowMS = meta.WriteFenceUntilMS - 1
	result, err := sm.Apply(ctx, multiraft.Command{SlotID: 11, Index: 3, Term: 1, Data: EncodeResetChannelWriteFenceToPreCutoverCommand(req)})
	requireFSMStaleResult(t, result, err)
}

func TestStateMachineChannelSetAndClearFenceSameBatch(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm := mustNewStateMachine(t, db, 11)
	task := fsmTestChannelMigrationTask("task-set-clear-same-batch", "channel-set-clear-same-batch")
	task.Status = metadb.ChannelMigrationStatusRunning
	task.Phase = metadb.ChannelMigrationPhaseVerifyMembership
	task.UpdatedAtMS = 1750000001000
	task.FenceToken = task.TaskID
	task.FenceVersion = 7
	task.FenceUntilMS = 1750000009000
	setFSMTestDrainProof(&task, 7)
	meta := fsmTestFencedRuntimeMeta(task.ChannelID, task.ChannelType, task.TaskID, 7)
	meta.ChannelEpoch = task.BaseChannelEpoch
	meta.LeaderEpoch = task.BaseLeaderEpoch
	meta.Replicas = []uint64{1, 3}
	meta.ISR = []uint64{1, 3}

	fsmApplyOK(t, ctx, sm, 1, EncodeUpsertChannelRuntimeMetaCommand(meta))
	fsmApplyOK(t, ctx, sm, 2, EncodeCreateChannelMigrationTaskCommand(task))

	setReq := fsmTestSetFenceRequest(task, meta, 1750000009000, 1750000002000)
	setReq.Phase = task.Phase
	fencedTask := task
	fencedTask.Status = setReq.Status
	fencedTask.Phase = setReq.Phase
	fencedTask.FenceToken = task.TaskID
	fencedTask.FenceVersion = 8
	fencedTask.FenceUntilMS = setReq.FenceUntilMS
	fencedTask.UpdatedAtMS = setReq.UpdatedAtMS
	fencedTask.CutoverLEO = 0
	fencedTask.CutoverHW = 0
	fencedTask.DrainedLeaderNode = 0
	fencedTask.DrainedRuntimeGeneration = 0
	fencedTask.DrainedChannelEpoch = 0
	fencedTask.DrainedLeaderEpoch = 0
	fencedTask.DrainedFenceVersion = 0
	fencedMeta := meta
	fencedMeta.WriteFenceToken = task.TaskID
	fencedMeta.WriteFenceVersion = 8
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
	if gotMeta.WriteFenceToken != "" || gotMeta.WriteFenceVersion != 9 {
		t.Fatalf("meta after set+clear = %#v", gotMeta)
	}
}

func TestStateMachineChannelClearFenceDuplicateTerminalNoOp(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm := mustNewStateMachine(t, db, 11)
	task := fsmTestChannelMigrationTask("task-clear-duplicate", "channel-clear-duplicate")
	task.Status = metadb.ChannelMigrationStatusRunning
	task.Phase = metadb.ChannelMigrationPhaseVerifyMembership
	task.UpdatedAtMS = 1750000001000
	task.FenceToken = task.TaskID
	task.FenceVersion = 7
	task.FenceUntilMS = 1750000009000
	setFSMTestDrainProof(&task, 7)
	meta := fsmTestFencedRuntimeMeta(task.ChannelID, task.ChannelType, task.TaskID, 7)
	meta.ChannelEpoch = task.BaseChannelEpoch
	meta.LeaderEpoch = task.BaseLeaderEpoch
	meta.Replicas = []uint64{1, 3}
	meta.ISR = []uint64{1, 3}

	fsmApplyOK(t, ctx, sm, 1, EncodeUpsertChannelRuntimeMetaCommand(meta))
	fsmApplyOK(t, ctx, sm, 2, EncodeCreateChannelMigrationTaskCommand(task))
	req := fsmTestClearFenceRequest(task, meta, 1750000002000)
	fsmApplyOK(t, ctx, sm, 3, EncodeClearChannelWriteFenceCommand(req))
	fsmApplyOK(t, ctx, sm, 4, EncodeClearChannelWriteFenceCommand(req))

	gotTask, err := db.ForSlot(11).GetChannelMigrationTask(ctx, task.ChannelID, task.ChannelType, task.TaskID)
	if err != nil {
		t.Fatalf("GetChannelMigrationTask() error = %v", err)
	}
	if gotTask.Status != metadb.ChannelMigrationStatusCompleted ||
		gotTask.Phase != metadb.ChannelMigrationPhaseClearFence ||
		gotTask.FenceToken != "" ||
		gotTask.CompletedAtMS != req.CompletedAtMS {
		t.Fatalf("task after duplicate clear = %#v", gotTask)
	}
	gotMeta, err := db.ForSlot(11).GetChannelRuntimeMeta(ctx, meta.ChannelID, meta.ChannelType)
	if err != nil {
		t.Fatalf("GetChannelRuntimeMeta() error = %v", err)
	}
	if gotMeta.WriteFenceToken != "" || gotMeta.WriteFenceVersion != meta.WriteFenceVersion+1 {
		t.Fatalf("meta after duplicate clear = %#v", gotMeta)
	}
}

func TestStateMachineChannelSetFenceForNewTaskAfterTerminalClear(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm := mustNewStateMachine(t, db, 11)
	task := fsmTestChannelMigrationTask("task-clear-then-new", "channel-clear-then-new")
	task.Status = metadb.ChannelMigrationStatusRunning
	task.Phase = metadb.ChannelMigrationPhaseVerifyMembership
	task.UpdatedAtMS = 1750000001000
	task.FenceToken = task.TaskID
	task.FenceVersion = 7
	task.FenceUntilMS = 1750000009000
	setFSMTestDrainProof(&task, 7)
	meta := fsmTestFencedRuntimeMeta(task.ChannelID, task.ChannelType, task.TaskID, 7)
	meta.ChannelEpoch = task.BaseChannelEpoch
	meta.LeaderEpoch = task.BaseLeaderEpoch
	meta.Replicas = []uint64{1, 3}
	meta.ISR = []uint64{1, 3}

	fsmApplyOK(t, ctx, sm, 1, EncodeUpsertChannelRuntimeMetaCommand(meta))
	fsmApplyOK(t, ctx, sm, 2, EncodeCreateChannelMigrationTaskCommand(task))
	fsmApplyOK(t, ctx, sm, 3, EncodeClearChannelWriteFenceCommand(fsmTestClearFenceRequest(task, meta, 1750000002000)))

	clearedMeta, err := db.ForSlot(11).GetChannelRuntimeMeta(ctx, meta.ChannelID, meta.ChannelType)
	if err != nil {
		t.Fatalf("GetChannelRuntimeMeta(clear) error = %v", err)
	}
	nextTask := fsmTestChannelMigrationTask("task-after-clear-marker", task.ChannelID)
	nextTask.Status = metadb.ChannelMigrationStatusRunning
	nextTask.Phase = metadb.ChannelMigrationPhaseWarmCatchUp
	nextTask.UpdatedAtMS = 1750000003000
	nextTask.CreatedAtMS = 1750000003000
	nextTask.BaseChannelEpoch = clearedMeta.ChannelEpoch
	nextTask.BaseLeaderEpoch = clearedMeta.LeaderEpoch
	fsmApplyOK(t, ctx, sm, 4, EncodeCreateChannelMigrationTaskCommand(nextTask))
	fsmApplyOK(t, ctx, sm, 5, EncodeSetChannelWriteFenceCommand(fsmTestSetFenceRequest(nextTask, clearedMeta, 1750000010000, 1750000004000)))

	gotTask, err := db.ForSlot(11).GetChannelMigrationTask(ctx, nextTask.ChannelID, nextTask.ChannelType, nextTask.TaskID)
	if err != nil {
		t.Fatalf("GetChannelMigrationTask(next) error = %v", err)
	}
	if gotTask.FenceToken != nextTask.TaskID || gotTask.FenceVersion != clearedMeta.WriteFenceVersion+1 {
		t.Fatalf("new task after clear marker = %#v", gotTask)
	}
	gotMeta, err := db.ForSlot(11).GetChannelRuntimeMeta(ctx, clearedMeta.ChannelID, clearedMeta.ChannelType)
	if err != nil {
		t.Fatalf("GetChannelRuntimeMeta(next set) error = %v", err)
	}
	if gotMeta.WriteFenceToken != nextTask.TaskID || gotMeta.WriteFenceVersion != clearedMeta.WriteFenceVersion+1 {
		t.Fatalf("meta after new task set = %#v", gotMeta)
	}
}

func TestStateMachineChannelPromoteRejectsTargetAlreadyInISR(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm := mustNewStateMachine(t, db, 11)
	task := fsmTestChannelMigrationTask("task-promote-target-isr", "channel-promote-target-isr")
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
	meta.ISR = []uint64{1, 2, 3}

	fsmApplyOK(t, ctx, sm, 1, EncodeUpsertChannelRuntimeMetaCommand(meta))
	fsmApplyOK(t, ctx, sm, 2, EncodeCreateChannelMigrationTaskCommand(task))
	result, err := sm.Apply(ctx, multiraft.Command{SlotID: 11, Index: 3, Term: 1, Data: EncodePromoteLearnerAndRemoveReplicaCommand(fsmTestPromoteRequest(task, meta, 1750000002000))})
	requireFSMStaleResult(t, result, err)
}

func TestStateMachineChannelPromoteRejectsSourceStillLeader(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm := mustNewStateMachine(t, db, 11)
	task := fsmTestChannelMigrationTask("task-promote-source-leader", "channel-promote-source-leader")
	task.SourceNode = 1
	task.TargetNode = 3
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
	meta.Leader = task.SourceNode
	meta.Replicas = []uint64{1, 2, 3}
	meta.ISR = []uint64{1, 2}

	fsmApplyOK(t, ctx, sm, 1, EncodeUpsertChannelRuntimeMetaCommand(meta))
	fsmApplyOK(t, ctx, sm, 2, EncodeCreateChannelMigrationTaskCommand(task))
	result, err := sm.Apply(ctx, multiraft.Command{SlotID: 11, Index: 3, Term: 1, Data: EncodePromoteLearnerAndRemoveReplicaCommand(fsmTestPromoteRequest(task, meta, 1750000002000))})
	requireFSMStaleResult(t, result, err)

	got, err := db.ForSlot(11).GetChannelRuntimeMeta(ctx, meta.ChannelID, meta.ChannelType)
	if err != nil {
		t.Fatalf("GetChannelRuntimeMeta() error = %v", err)
	}
	if got.Leader != meta.Leader || !reflect.DeepEqual(got.Replicas, meta.Replicas) || !reflect.DeepEqual(got.ISR, meta.ISR) {
		t.Fatalf("meta after stale promote = %#v, want %#v", got, meta)
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

func TestStateMachineChannelAbortLeaderTransferDoesNotRemoveNonISRReplica(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm := mustNewStateMachine(t, db, 11)
	task := fsmTestLeaderTransferTask("task-abort-leader-transfer", "channel-abort-leader-transfer")
	task.TargetNode = 3
	task.DesiredLeader = 3
	task.Phase = metadb.ChannelMigrationPhaseProbeTarget
	task.UpdatedAtMS = 1750000001000
	meta := fsmTestRuntimeMeta(task.ChannelID, task.ChannelType)
	meta.ChannelEpoch = task.BaseChannelEpoch
	meta.LeaderEpoch = task.BaseLeaderEpoch
	meta.Replicas = []uint64{1, 2, 3}
	meta.ISR = []uint64{1, 2}

	fsmApplyOK(t, ctx, sm, 1, EncodeUpsertChannelRuntimeMetaCommand(meta))
	fsmApplyOK(t, ctx, sm, 2, EncodeCreateChannelMigrationTaskCommand(task))
	fsmApplyOK(t, ctx, sm, 3, EncodeAbortChannelMigrationCommand(fsmTestAbortRequest(task, meta, 1750000002000)))

	gotMeta, err := db.ForSlot(11).GetChannelRuntimeMeta(ctx, meta.ChannelID, meta.ChannelType)
	if err != nil {
		t.Fatalf("GetChannelRuntimeMeta() error = %v", err)
	}
	if gotMeta.ChannelEpoch != meta.ChannelEpoch || !reflect.DeepEqual(gotMeta.Replicas, []uint64{1, 2, 3}) {
		t.Fatalf("meta after leader-transfer abort = %#v", gotMeta)
	}
}

func TestStateMachineChannelAbortBeforeLearnerAddedKeepsExistingLearner(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm := mustNewStateMachine(t, db, 11)
	task := fsmTestChannelMigrationTask("task-abort-pre-add-learner", "channel-abort-pre-add-learner")
	task.Status = metadb.ChannelMigrationStatusRunning
	task.Phase = metadb.ChannelMigrationPhaseAddLearner
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
	if gotTask.Status != metadb.ChannelMigrationStatusAborted {
		t.Fatalf("task after pre-add abort = %#v", gotTask)
	}
	gotMeta, err := db.ForSlot(11).GetChannelRuntimeMeta(ctx, meta.ChannelID, meta.ChannelType)
	if err != nil {
		t.Fatalf("GetChannelRuntimeMeta() error = %v", err)
	}
	if gotMeta.ChannelEpoch != meta.ChannelEpoch || !reflect.DeepEqual(gotMeta.Replicas, []uint64{1, 2, 3}) {
		t.Fatalf("meta after pre-add abort = %#v", gotMeta)
	}
}

func TestStateMachineChannelAbortRejectsAfterIrreversiblePhases(t *testing.T) {
	tests := []struct {
		name  string
		setup func() (metadb.ChannelMigrationTask, metadb.ChannelRuntimeMeta)
	}{
		{
			name: "after_leader_commit",
			setup: func() (metadb.ChannelMigrationTask, metadb.ChannelRuntimeMeta) {
				task := fsmTestLeaderTransferTask("task-abort-after-leader", "channel-abort-after-leader")
				task.Phase = metadb.ChannelMigrationPhaseVerifyNewLeader
				task.FenceToken = task.TaskID
				task.FenceVersion = 7
				task.FenceUntilMS = 1750000009000
				setFSMTestDrainProof(&task, 7)
				meta := fsmTestFencedRuntimeMeta(task.ChannelID, task.ChannelType, task.TaskID, 7)
				meta.ChannelEpoch = task.BaseChannelEpoch
				meta.LeaderEpoch = task.BaseLeaderEpoch + 1
				meta.Leader = task.TargetNode
				meta.Replicas = []uint64{1, 2, 3}
				meta.ISR = []uint64{1, 2, 3}
				task.DrainedLeaderEpoch = meta.LeaderEpoch
				task.DrainedLeaderNode = meta.Leader
				return task, meta
			},
		},
		{
			name: "after_promote",
			setup: func() (metadb.ChannelMigrationTask, metadb.ChannelRuntimeMeta) {
				task := fsmTestChannelMigrationTask("task-abort-after-promote", "channel-abort-after-promote")
				task.Status = metadb.ChannelMigrationStatusRunning
				task.Phase = metadb.ChannelMigrationPhaseVerifyMembership
				task.UpdatedAtMS = 1750000001000
				task.FenceToken = task.TaskID
				task.FenceVersion = 7
				task.FenceUntilMS = 1750000009000
				setFSMTestDrainProof(&task, 7)
				meta := fsmTestFencedRuntimeMeta(task.ChannelID, task.ChannelType, task.TaskID, 7)
				meta.ChannelEpoch = task.BaseChannelEpoch
				meta.LeaderEpoch = task.BaseLeaderEpoch
				meta.Replicas = []uint64{1, 3}
				meta.ISR = []uint64{1, 3}
				return task, meta
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			db := openTestDB(t)
			sm := mustNewStateMachine(t, db, 11)
			task, meta := tc.setup()

			fsmApplyOK(t, ctx, sm, 1, EncodeUpsertChannelRuntimeMetaCommand(meta))
			fsmApplyOK(t, ctx, sm, 2, EncodeCreateChannelMigrationTaskCommand(task))
			result, err := sm.Apply(ctx, multiraft.Command{SlotID: 11, Index: 3, Term: 1, Data: EncodeAbortChannelMigrationCommand(fsmTestAbortRequest(task, meta, 1750000002000))})
			requireFSMStaleResult(t, result, err)
		})
	}
}

func TestStateMachineChannelCommandsRejectWrongKindOrPhase(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, ctx context.Context, sm multiraft.StateMachine) ([]byte, uint64)
	}{
		{
			name: "add_learner_rejects_leader_transfer_kind",
			setup: func(t *testing.T, ctx context.Context, sm multiraft.StateMachine) ([]byte, uint64) {
				task := fsmTestLeaderTransferTask("task-add-wrong-kind", "channel-add-wrong-kind")
				task.Phase = metadb.ChannelMigrationPhaseProbeTarget
				meta := fsmTestRuntimeMeta(task.ChannelID, task.ChannelType)
				meta.ChannelEpoch = task.BaseChannelEpoch
				meta.LeaderEpoch = task.BaseLeaderEpoch
				meta.Replicas = []uint64{1, 3}
				meta.ISR = []uint64{1}
				fsmApplyOK(t, ctx, sm, 1, EncodeUpsertChannelRuntimeMetaCommand(meta))
				fsmApplyOK(t, ctx, sm, 2, EncodeCreateChannelMigrationTaskCommand(task))
				req := fsmTestAddLearnerRequest(task, meta, 1750000002000)
				req.Phase = metadb.ChannelMigrationPhaseVerifyNewLeader
				return EncodeAddChannelLearnerCommand(req), 3
			},
		},
		{
			name: "add_learner_rejects_wrong_phase",
			setup: func(t *testing.T, ctx context.Context, sm multiraft.StateMachine) ([]byte, uint64) {
				task := fsmTestChannelMigrationTask("task-add-wrong-phase", "channel-add-wrong-phase")
				task.Status = metadb.ChannelMigrationStatusRunning
				task.Phase = metadb.ChannelMigrationPhaseWarmCatchUp
				task.UpdatedAtMS = 1750000001000
				meta := fsmTestRuntimeMeta(task.ChannelID, task.ChannelType)
				meta.ChannelEpoch = task.BaseChannelEpoch
				meta.LeaderEpoch = task.BaseLeaderEpoch
				fsmApplyOK(t, ctx, sm, 1, EncodeUpsertChannelRuntimeMetaCommand(meta))
				fsmApplyOK(t, ctx, sm, 2, EncodeCreateChannelMigrationTaskCommand(task))
				return EncodeAddChannelLearnerCommand(fsmTestAddLearnerRequest(task, meta, 1750000002000)), 3
			},
		},
		{
			name: "commit_leader_rejects_wrong_phase",
			setup: func(t *testing.T, ctx context.Context, sm multiraft.StateMachine) ([]byte, uint64) {
				task := fsmTestLeaderTransferTask("task-commit-wrong-phase", "channel-commit-wrong-phase")
				task.Phase = metadb.ChannelMigrationPhaseFinalTargetCatchUp
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
				return EncodeCommitChannelLeaderTransferCommand(fsmTestCommitLeaderRequest(task, meta, 1750000002000)), 3
			},
		},
		{
			name: "promote_rejects_leader_transfer_kind",
			setup: func(t *testing.T, ctx context.Context, sm multiraft.StateMachine) ([]byte, uint64) {
				task := fsmTestLeaderTransferTask("task-promote-wrong-kind", "channel-promote-wrong-kind")
				task.Phase = metadb.ChannelMigrationPhaseCommitLeaderMeta
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
				req.Phase = metadb.ChannelMigrationPhaseVerifyNewLeader
				return EncodePromoteLearnerAndRemoveReplicaCommand(req), 3
			},
		},
		{
			name: "promote_rejects_wrong_phase",
			setup: func(t *testing.T, ctx context.Context, sm multiraft.StateMachine) ([]byte, uint64) {
				task := fsmTestChannelMigrationTask("task-promote-wrong-phase", "channel-promote-wrong-phase")
				task.Status = metadb.ChannelMigrationStatusRunning
				task.Phase = metadb.ChannelMigrationPhaseFinalTargetCatchUp
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
				return EncodePromoteLearnerAndRemoveReplicaCommand(fsmTestPromoteRequest(task, meta, 1750000002000)), 3
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			db := openTestDB(t)
			sm := mustNewStateMachine(t, db, 11)
			data, index := tc.setup(t, ctx, sm)
			result, err := sm.Apply(ctx, multiraft.Command{SlotID: 11, Index: index, Term: 1, Data: data})
			requireFSMStaleResult(t, result, err)
		})
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

func TestStateMachineChannelCreateDuplicateReturnsStaleMeta(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm := mustNewStateMachine(t, db, 11)
	task := fsmTestChannelMigrationTask("task-create-dup", "channel-create-dup")

	fsmApplyOK(t, ctx, sm, 1, EncodeCreateChannelMigrationTaskCommand(task))
	fsmApplyOK(t, ctx, sm, 2, EncodeCreateChannelMigrationTaskCommand(task))

	competing := fsmTestChannelMigrationTask("task-create-competing", task.ChannelID)
	result, err := sm.Apply(ctx, multiraft.Command{SlotID: 11, Index: 3, Term: 1, Data: EncodeCreateChannelMigrationTaskCommand(competing)})
	requireFSMStaleResult(t, result, err)
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
	if want := metadb.NormalizeChannelRuntimeMeta(meta); !reflect.DeepEqual(gotMeta, want) {
		t.Fatalf("meta mutated by advance = %#v, want %#v", gotMeta, want)
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

func TestStateMachineChannelMigrationGarbageCollectDeletesTerminalTasks(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm := mustNewStateMachine(t, db, 11)
	oldTask := fsmTestChannelMigrationTask("task-gc-old", "channel-gc-old")
	oldTask.Status = metadb.ChannelMigrationStatusCompleted
	oldTask.Phase = metadb.ChannelMigrationPhaseVerifyMembership
	oldTask.UpdatedAtMS = 1750000010000
	oldTask.CompletedAtMS = 1750000010000
	newTask := fsmTestChannelMigrationTask("task-gc-new", "channel-gc-new")
	newTask.Status = metadb.ChannelMigrationStatusCompleted
	newTask.Phase = metadb.ChannelMigrationPhaseVerifyMembership
	newTask.UpdatedAtMS = 1750000030000
	newTask.CompletedAtMS = 1750000030000

	fsmApplyOK(t, ctx, sm, 1, EncodeCreateChannelMigrationTaskCommand(oldTask))
	fsmApplyOK(t, ctx, sm, 2, EncodeCreateChannelMigrationTaskCommand(newTask))
	result, err := sm.Apply(ctx, multiraft.Command{
		SlotID:   11,
		HashSlot: 11,
		Index:    3,
		Data: EncodeGarbageCollectTerminalChannelMigrationTasksCommand(metadb.ChannelMigrationTaskGCRequest{
			BeforeMS: 1750000020000,
			Limit:    10,
		}),
	})
	if err != nil {
		t.Fatalf("Apply(gc) error = %v", err)
	}
	deleted, ok, err := DecodeGarbageCollectTerminalChannelMigrationTasksResult(result)
	if err != nil {
		t.Fatalf("DecodeGarbageCollectTerminalChannelMigrationTasksResult() error = %v", err)
	}
	if !ok || deleted != 1 {
		t.Fatalf("gc result ok=%v deleted=%d, want ok=true deleted=1", ok, deleted)
	}

	_, err = db.ForSlot(11).GetChannelMigrationTask(ctx, oldTask.ChannelID, oldTask.ChannelType, oldTask.TaskID)
	if !errors.Is(err, metadb.ErrNotFound) {
		t.Fatalf("GetChannelMigrationTask(old) error = %v, want ErrNotFound", err)
	}

	gotNew, err := db.ForSlot(11).GetChannelMigrationTask(ctx, newTask.ChannelID, newTask.ChannelType, newTask.TaskID)
	if err != nil {
		t.Fatalf("GetChannelMigrationTask(new) error = %v", err)
	}
	if gotNew.TaskID != newTask.TaskID {
		t.Fatalf("new task id = %q, want %q", gotNew.TaskID, newTask.TaskID)
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

func fsmTestLeaderTransferTask(taskID, channelID string) metadb.ChannelMigrationTask {
	task := fsmTestChannelMigrationTask(taskID, channelID)
	task.Kind = metadb.ChannelMigrationKindLeaderTransfer
	task.Status = metadb.ChannelMigrationStatusRunning
	task.Phase = metadb.ChannelMigrationPhaseProbeTarget
	task.SourceNode = 1
	task.TargetNode = 2
	task.DesiredLeader = 2
	task.UpdatedAtMS = 1750000001000
	return task
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
		NowMS:        meta.WriteFenceUntilMS + 1,
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
