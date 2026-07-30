package fsm

import (
	"errors"
	"reflect"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
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
		{name: "unknown_field", payload: `{"TaskID":"task-json","UnknownField":true}`},
		{name: "duplicate_key", payload: `{"TaskID":"task-json","TaskID":"task-json-2"}`},
		{name: "trailing_value", payload: `{"TaskID":"task-json"} {}`},
		{name: "case_insensitive_duplicate_key", payload: `{"TaskID":"task-json","taskid":"task-json-2"}`},
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
