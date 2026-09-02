package meta

import (
	"errors"
	"reflect"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
)

func TestChannelMigrationRequestValidationProtectsDurableTransitions(t *testing.T) {
	guard := validMigrationTaskGuard()
	runtimeGuard := validMigrationRuntimeGuard()

	valid := []struct {
		name     string
		validate func() error
	}{
		{"set fence", func() error {
			return validateChannelMigrationFenceRequest(ChannelMigrationFenceRequest{Guard: guard, RuntimeGuard: runtimeGuard, Status: ChannelMigrationStatusRunning, Phase: ChannelMigrationPhaseDrainLeader, FenceReason: 1, FenceUntilMS: 200, UpdatedAtMS: 101})
		}},
		{"reset fence", func() error {
			return validateChannelMigrationResetFenceRequest(ChannelMigrationResetFenceRequest{Guard: guard, RuntimeGuard: runtimeGuard, Status: ChannelMigrationStatusRunning, Phase: ChannelMigrationPhaseProbeTarget, NowMS: 200, UpdatedAtMS: 101})
		}},
		{"leader transfer", func() error {
			return validateChannelMigrationLeaderTransferRequest(ChannelMigrationLeaderTransferRequest{Guard: guard, RuntimeGuard: runtimeGuard, Status: ChannelMigrationStatusRunning, Phase: ChannelMigrationPhaseVerifyNewLeader, DesiredLeader: 2, NextLeaderEpoch: 4, LeaseUntilMS: 300, NowMS: 200, UpdatedAtMS: 101})
		}},
		{"add learner", func() error {
			return validateChannelMigrationAddLearnerRequest(ChannelMigrationAddLearnerRequest{Guard: guard, RuntimeGuard: runtimeGuard, Status: ChannelMigrationStatusRunning, Phase: ChannelMigrationPhaseBootstrapTarget, TargetNode: 3, UpdatedAtMS: 101})
		}},
		{"promote learner", func() error {
			return validateChannelMigrationPromoteLearnerRequest(ChannelMigrationPromoteLearnerRequest{Guard: guard, RuntimeGuard: runtimeGuard, Status: ChannelMigrationStatusRunning, Phase: ChannelMigrationPhaseVerifyMembership, SourceNode: 1, TargetNode: 3, NowMS: 200, UpdatedAtMS: 101})
		}},
		{"clear fence", func() error {
			return validateChannelMigrationClearFenceRequest(ChannelMigrationClearFenceRequest{Guard: guard, RuntimeGuard: runtimeGuard, Status: ChannelMigrationStatusCompleted, Phase: ChannelMigrationPhaseClearFence, UpdatedAtMS: 101, CompletedAtMS: 101})
		}},
		{"abort", func() error {
			return validateChannelMigrationAbortRequest(ChannelMigrationAbortRequest{Guard: guard, RuntimeGuard: runtimeGuard, Status: ChannelMigrationStatusAborted, Phase: ChannelMigrationPhaseClearFence, UpdatedAtMS: 101, CompletedAtMS: 101})
		}},
		{"gc", func() error {
			return validateChannelMigrationTaskGCRequest(ChannelMigrationTaskGCRequest{BeforeMS: 100, Limit: 1})
		}},
	}
	for _, tc := range valid {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.validate(); err != nil {
				t.Fatalf("valid request rejected: %v", err)
			}
		})
	}

	invalid := []struct {
		name     string
		validate func() error
	}{
		{"empty task guard", func() error {
			return validateChannelMigrationTaskRuntimeTransition(ChannelMigrationTaskGuard{}, runtimeGuard, ChannelMigrationStatusRunning, ChannelMigrationPhaseValidate, 101, 0)
		}},
		{"empty runtime guard", func() error {
			return validateChannelMigrationTaskRuntimeTransition(guard, ChannelMigrationRuntimeGuard{}, ChannelMigrationStatusRunning, ChannelMigrationPhaseValidate, 101, 0)
		}},
		{"unknown status", func() error {
			return validateChannelMigrationTaskRuntimeTransition(guard, runtimeGuard, 99, ChannelMigrationPhaseValidate, 101, 0)
		}},
		{"unknown phase", func() error {
			return validateChannelMigrationTaskRuntimeTransition(guard, runtimeGuard, ChannelMigrationStatusRunning, 99, 101, 0)
		}},
		{"non-monotonic update", func() error {
			return validateChannelMigrationTaskRuntimeTransition(guard, runtimeGuard, ChannelMigrationStatusRunning, ChannelMigrationPhaseValidate, guard.ExpectedUpdatedAtMS, 0)
		}},
		{"terminal without completion", func() error {
			return validateChannelMigrationTaskRuntimeTransition(guard, runtimeGuard, ChannelMigrationStatusCompleted, ChannelMigrationPhaseClearFence, 101, 0)
		}},
		{"fence without reason", func() error {
			return validateChannelMigrationFenceRequest(ChannelMigrationFenceRequest{Guard: guard, RuntimeGuard: runtimeGuard, Status: ChannelMigrationStatusRunning, Phase: ChannelMigrationPhaseDrainLeader, FenceUntilMS: 200, UpdatedAtMS: 101})
		}},
		{"reset without clock", func() error {
			return validateChannelMigrationResetFenceRequest(ChannelMigrationResetFenceRequest{Guard: guard, RuntimeGuard: runtimeGuard, Status: ChannelMigrationStatusRunning, Phase: ChannelMigrationPhaseProbeTarget, UpdatedAtMS: 101})
		}},
		{"leader transfer without leader", func() error {
			return validateChannelMigrationLeaderTransferRequest(ChannelMigrationLeaderTransferRequest{Guard: guard, RuntimeGuard: runtimeGuard, Status: ChannelMigrationStatusRunning, Phase: ChannelMigrationPhaseVerifyNewLeader, NextLeaderEpoch: 4, LeaseUntilMS: 300, NowMS: 200, UpdatedAtMS: 101})
		}},
		{"add learner without target", func() error {
			return validateChannelMigrationAddLearnerRequest(ChannelMigrationAddLearnerRequest{Guard: guard, RuntimeGuard: runtimeGuard, Status: ChannelMigrationStatusRunning, Phase: ChannelMigrationPhaseBootstrapTarget, UpdatedAtMS: 101})
		}},
		{"promote same node", func() error {
			return validateChannelMigrationPromoteLearnerRequest(ChannelMigrationPromoteLearnerRequest{Guard: guard, RuntimeGuard: runtimeGuard, Status: ChannelMigrationStatusRunning, Phase: ChannelMigrationPhaseVerifyMembership, SourceNode: 2, TargetNode: 2, NowMS: 200, UpdatedAtMS: 101})
		}},
		{"abort with non-aborted status", func() error {
			return validateChannelMigrationAbortRequest(ChannelMigrationAbortRequest{Guard: guard, RuntimeGuard: runtimeGuard, Status: ChannelMigrationStatusFailed, Phase: ChannelMigrationPhaseClearFence, UpdatedAtMS: 101, CompletedAtMS: 101})
		}},
		{"gc without limit", func() error {
			return validateChannelMigrationTaskGCRequest(ChannelMigrationTaskGCRequest{BeforeMS: 100})
		}},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.validate(); !errors.Is(err, dberrors.ErrInvalidArgument) {
				t.Fatalf("error = %v, want ErrInvalidArgument", err)
			}
		})
	}

	task := validMigrationTask(ChannelMigrationKindLeaderTransfer, ChannelMigrationPhaseValidate)
	if err := validateChannelMigrationTaskCreate(ChannelMigrationTaskCreate{Task: task, RuntimeGuard: runtimeGuard}); err != nil {
		t.Fatalf("valid create rejected: %v", err)
	}
	mismatch := runtimeGuard
	mismatch.ChannelType++
	if err := validateChannelMigrationTaskCreate(ChannelMigrationTaskCreate{Task: task, RuntimeGuard: mismatch}); !errors.Is(err, dberrors.ErrInvalidArgument) {
		t.Fatalf("mismatched create error = %v, want ErrInvalidArgument", err)
	}
}

func TestChannelMigrationTransitionMatrix(t *testing.T) {
	tests := []struct {
		name string
		task ChannelMigrationTask
		run  func(ChannelMigrationTask) error
		ok   bool
	}{
		{"leader fence advances from write fence", validMigrationTask(ChannelMigrationKindLeaderTransfer, ChannelMigrationPhaseWriteFence), func(task ChannelMigrationTask) error {
			return requireChannelMigrationSetFenceTransition(task, ChannelMigrationFenceRequest{Status: ChannelMigrationStatusRunning, Phase: ChannelMigrationPhaseDrainLeader})
		}, true},
		{"leader fence rejects skipped phase", validMigrationTask(ChannelMigrationKindLeaderTransfer, ChannelMigrationPhaseWriteFence), func(task ChannelMigrationTask) error {
			return requireChannelMigrationSetFenceTransition(task, ChannelMigrationFenceRequest{Status: ChannelMigrationStatusRunning, Phase: ChannelMigrationPhaseCommitLeaderMeta})
		}, false},
		{"replica fence advances from warm catchup", validMigrationTask(ChannelMigrationKindReplicaReplace, ChannelMigrationPhaseWarmCatchUp), func(task ChannelMigrationTask) error {
			return requireChannelMigrationSetFenceTransition(task, ChannelMigrationFenceRequest{Status: ChannelMigrationStatusRunning, Phase: ChannelMigrationPhaseCutoverFence})
		}, true},
		{"fence rejects blocked status", validMigrationTask(ChannelMigrationKindReplicaReplace, ChannelMigrationPhaseWarmCatchUp), func(task ChannelMigrationTask) error {
			return requireChannelMigrationSetFenceTransition(task, ChannelMigrationFenceRequest{Status: ChannelMigrationStatusBlocked, Phase: ChannelMigrationPhaseCutoverFence})
		}, false},
		{"leader reset returns to probe", validMigrationTask(ChannelMigrationKindLeaderTransfer, ChannelMigrationPhaseDrainLeader), func(task ChannelMigrationTask) error {
			return requireChannelMigrationResetFenceTransition(task, ChannelMigrationResetFenceRequest{Status: ChannelMigrationStatusRunning, Phase: ChannelMigrationPhaseProbeTarget})
		}, true},
		{"replica reset returns to warm catchup", validMigrationTask(ChannelMigrationKindReplicaReplace, ChannelMigrationPhaseCutoverFence), func(task ChannelMigrationTask) error {
			return requireChannelMigrationResetFenceTransition(task, ChannelMigrationResetFenceRequest{Status: ChannelMigrationStatusRunning, Phase: ChannelMigrationPhaseWarmCatchUp})
		}, true},
		{"reset rejects unfenced phase", validMigrationTask(ChannelMigrationKindLeaderTransfer, ChannelMigrationPhaseValidate), func(task ChannelMigrationTask) error {
			return requireChannelMigrationResetFenceTransition(task, ChannelMigrationResetFenceRequest{Status: ChannelMigrationStatusRunning, Phase: ChannelMigrationPhaseProbeTarget})
		}, false},
		{"leader metadata commit", validMigrationTask(ChannelMigrationKindLeaderTransfer, ChannelMigrationPhaseCommitLeaderMeta), func(task ChannelMigrationTask) error {
			return requireChannelMigrationLeaderTransferTransition(task, ChannelMigrationLeaderTransferRequest{Status: ChannelMigrationStatusRunning, Phase: ChannelMigrationPhaseVerifyNewLeader})
		}, true},
		{"embedded leader metadata commit", embeddedLeaderTransferTask(ChannelMigrationPhaseCommitLeaderMeta), func(task ChannelMigrationTask) error {
			return requireChannelMigrationLeaderTransferTransition(task, ChannelMigrationLeaderTransferRequest{Status: ChannelMigrationStatusRunning, Phase: ChannelMigrationPhaseVerifyNewLeader})
		}, true},
		{"plain replica cannot commit leader", validMigrationTask(ChannelMigrationKindReplicaReplace, ChannelMigrationPhaseCommitLeaderMeta), func(task ChannelMigrationTask) error {
			return requireChannelMigrationLeaderTransferTransition(task, ChannelMigrationLeaderTransferRequest{Status: ChannelMigrationStatusRunning, Phase: ChannelMigrationPhaseVerifyNewLeader})
		}, false},
		{"add learner transition", validMigrationTask(ChannelMigrationKindReplicaReplace, ChannelMigrationPhaseAddLearner), func(task ChannelMigrationTask) error {
			return requireChannelMigrationAddLearnerTransition(task, ChannelMigrationAddLearnerRequest{Status: ChannelMigrationStatusRunning, Phase: ChannelMigrationPhaseBootstrapTarget})
		}, true},
		{"add learner rejects wrong kind", validMigrationTask(ChannelMigrationKindLeaderTransfer, ChannelMigrationPhaseAddLearner), func(task ChannelMigrationTask) error {
			return requireChannelMigrationAddLearnerTransition(task, ChannelMigrationAddLearnerRequest{Status: ChannelMigrationStatusRunning, Phase: ChannelMigrationPhaseBootstrapTarget})
		}, false},
		{"promote learner transition", validMigrationTask(ChannelMigrationKindReplicaReplace, ChannelMigrationPhasePromoteAndRemove), func(task ChannelMigrationTask) error {
			return requireChannelMigrationPromoteLearnerTransition(task, ChannelMigrationPromoteLearnerRequest{Status: ChannelMigrationStatusRunning, Phase: ChannelMigrationPhaseVerifyMembership})
		}, true},
		{"promote learner rejects wrong phase", validMigrationTask(ChannelMigrationKindReplicaReplace, ChannelMigrationPhaseWarmCatchUp), func(task ChannelMigrationTask) error {
			return requireChannelMigrationPromoteLearnerTransition(task, ChannelMigrationPromoteLearnerRequest{Status: ChannelMigrationStatusRunning, Phase: ChannelMigrationPhaseVerifyMembership})
		}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run(tc.task)
			if tc.ok && err != nil {
				t.Fatalf("valid transition rejected: %v", err)
			}
			if !tc.ok && !errors.Is(err, dberrors.ErrConflict) {
				t.Fatalf("invalid transition error = %v, want ErrConflict", err)
			}
		})
	}
}

func TestChannelMigrationClearAndAbortTransitions(t *testing.T) {
	complete := ChannelMigrationClearFenceRequest{Status: ChannelMigrationStatusCompleted, Phase: ChannelMigrationPhaseClearFence, CompletedAtMS: 200}
	for _, tc := range []struct {
		name string
		task ChannelMigrationTask
		ok   bool
	}{
		{"leader after verification", validMigrationTask(ChannelMigrationKindLeaderTransfer, ChannelMigrationPhaseVerifyNewLeader), true},
		{"failover after verification", validMigrationTask(ChannelMigrationKindLeaderFailover, ChannelMigrationPhaseVerifyNewLeader), true},
		{"replica after membership verification", validMigrationTask(ChannelMigrationKindReplicaReplace, ChannelMigrationPhaseVerifyMembership), true},
		{"leader before verification", validMigrationTask(ChannelMigrationKindLeaderTransfer, ChannelMigrationPhaseCommitLeaderMeta), false},
		{"replica before verification", validMigrationTask(ChannelMigrationKindReplicaReplace, ChannelMigrationPhasePromoteAndRemove), false},
	} {
		t.Run("clear "+tc.name, func(t *testing.T) {
			err := requireChannelMigrationClearFenceTransition(tc.task, complete)
			if tc.ok && err != nil {
				t.Fatalf("valid clear rejected: %v", err)
			}
			if !tc.ok && !errors.Is(err, dberrors.ErrConflict) {
				t.Fatalf("invalid clear error = %v, want ErrConflict", err)
			}
		})
	}

	embedded := embeddedLeaderTransferTask(ChannelMigrationPhaseVerifyNewLeader)
	resumeReplica := ChannelMigrationClearFenceRequest{Status: ChannelMigrationStatusRunning, Phase: ChannelMigrationPhaseAddLearner}
	if err := requireChannelMigrationClearFenceTransition(embedded, resumeReplica); err != nil {
		t.Fatalf("embedded transfer should resume replica replacement: %v", err)
	}

	for _, tc := range []struct {
		name string
		task ChannelMigrationTask
		ok   bool
	}{
		{"leader before commit", validMigrationTask(ChannelMigrationKindLeaderTransfer, ChannelMigrationPhaseDrainLeader), true},
		{"leader after commit", validMigrationTask(ChannelMigrationKindLeaderTransfer, ChannelMigrationPhaseVerifyNewLeader), false},
		{"replica before promotion", validMigrationTask(ChannelMigrationKindReplicaReplace, ChannelMigrationPhaseWarmCatchUp), true},
		{"replica after verification", validMigrationTask(ChannelMigrationKindReplicaReplace, ChannelMigrationPhaseVerifyMembership), false},
		{"embedded leader before commit", embeddedLeaderTransferTask(ChannelMigrationPhaseDrainLeader), true},
		{"embedded leader after commit", embeddedLeaderTransferTask(ChannelMigrationPhaseVerifyNewLeader), false},
	} {
		t.Run("abort "+tc.name, func(t *testing.T) {
			err := requireChannelMigrationAbortTransition(tc.task)
			if tc.ok && err != nil {
				t.Fatalf("valid abort rejected: %v", err)
			}
			if !tc.ok && !errors.Is(err, dberrors.ErrConflict) {
				t.Fatalf("invalid abort error = %v, want ErrConflict", err)
			}
		})
	}
}

func TestChannelMigrationFenceAndCutoverProofContracts(t *testing.T) {
	meta := validMigrationRuntimeMeta()
	task := validMigrationTask(ChannelMigrationKindLeaderTransfer, ChannelMigrationPhaseDrainLeader)
	task.FenceToken = task.TaskID
	task.FenceVersion = meta.WriteFenceVersion
	task.FenceUntilMS = meta.WriteFenceUntilMS
	task.CutoverLEO = 100
	task.CutoverHW = 90
	task.DrainedLeaderNode = meta.Leader
	task.DrainedRuntimeGeneration = meta.RouteGeneration
	task.DrainedChannelEpoch = meta.ChannelEpoch
	task.DrainedLeaderEpoch = meta.LeaderEpoch
	task.DrainedFenceVersion = meta.WriteFenceVersion

	if err := requireMatchingFence(meta, task.TaskID, task.FenceVersion, meta.WriteFenceUntilMS, false); err != nil {
		t.Fatalf("matching fence rejected: %v", err)
	}
	if err := requireMatchingFence(meta, task.TaskID, task.FenceVersion, meta.WriteFenceUntilMS+1, false); !errors.Is(err, dberrors.ErrConflict) {
		t.Fatalf("expired fence error = %v, want ErrConflict", err)
	}
	if err := requireMatchingFence(meta, task.TaskID, task.FenceVersion, meta.WriteFenceUntilMS+1, true); err != nil {
		t.Fatalf("explicitly allowed expired fence rejected: %v", err)
	}
	if err := requireNoForeignChannelMigrationFence(task, meta); err != nil {
		t.Fatalf("owned fence rejected: %v", err)
	}
	if err := requireActiveChannelMigrationTaskFence(task, meta, meta.WriteFenceVersion); err != nil {
		t.Fatalf("active task fence rejected: %v", err)
	}
	if err := requireChannelMigrationCutoverProof(task, meta, meta.WriteFenceVersion); err != nil {
		t.Fatalf("complete cutover proof rejected: %v", err)
	}

	for _, mutate := range []func(*ChannelMigrationTask, *ChannelRuntimeMeta, *uint64){
		func(task *ChannelMigrationTask, _ *ChannelRuntimeMeta, _ *uint64) { task.DrainedChannelEpoch++ },
		func(_ *ChannelMigrationTask, meta *ChannelRuntimeMeta, _ *uint64) { meta.WriteFenceVersion++ },
		func(task *ChannelMigrationTask, _ *ChannelRuntimeMeta, _ *uint64) { task.DrainedLeaderNode = 0 },
		func(task *ChannelMigrationTask, _ *ChannelRuntimeMeta, _ *uint64) {
			task.CutoverHW = task.CutoverLEO + 1
		},
		func(_ *ChannelMigrationTask, _ *ChannelRuntimeMeta, expected *uint64) { *expected = 0 },
	} {
		badTask, badMeta, expected := task, meta, meta.WriteFenceVersion
		mutate(&badTask, &badMeta, &expected)
		if err := requireChannelMigrationCutoverProof(badTask, badMeta, expected); !errors.Is(err, dberrors.ErrConflict) {
			t.Fatalf("invalid cutover proof error = %v, want ErrConflict", err)
		}
	}

	noFenceTask := validMigrationTask(ChannelMigrationKindLeaderTransfer, ChannelMigrationPhaseValidate)
	noFenceMeta := validMigrationRuntimeMeta()
	noFenceMeta.WriteFenceToken, noFenceMeta.WriteFenceVersion, noFenceMeta.WriteFenceUntilMS = "", 0, 0
	if err := requireNoForeignChannelMigrationFence(noFenceTask, noFenceMeta); err != nil {
		t.Fatalf("absence of fences rejected: %v", err)
	}
	noFenceMeta.WriteFenceToken = "foreign"
	if err := requireNoForeignChannelMigrationFence(noFenceTask, noFenceMeta); !errors.Is(err, dberrors.ErrConflict) {
		t.Fatalf("foreign fence error = %v, want ErrConflict", err)
	}
}

func TestChannelMigrationClearFenceIsIdempotentOnlyForExactDurableResult(t *testing.T) {
	req := ChannelMigrationClearFenceRequest{
		RuntimeGuard:  validMigrationRuntimeGuard(),
		Status:        ChannelMigrationStatusCompleted,
		Phase:         ChannelMigrationPhaseClearFence,
		UpdatedAtMS:   200,
		CompletedAtMS: 200,
	}
	task := validMigrationTask(ChannelMigrationKindLeaderTransfer, ChannelMigrationPhaseClearFence)
	task.Status, task.UpdatedAtMS, task.CompletedAtMS = req.Status, req.UpdatedAtMS, req.CompletedAtMS
	meta := validMigrationRuntimeMeta()
	meta.ChannelID = req.RuntimeGuard.ChannelID
	meta.ChannelType = req.RuntimeGuard.ChannelType
	meta.ChannelEpoch = req.RuntimeGuard.ExpectedChannelEpoch
	meta.LeaderEpoch = req.RuntimeGuard.ExpectedLeaderEpoch
	meta.Leader = req.RuntimeGuard.ExpectedLeader
	meta.WriteFenceToken = ""
	meta.WriteFenceVersion = req.RuntimeGuard.ExpectedFenceVersion + 1
	meta.WriteFenceReason = 0
	meta.WriteFenceUntilMS = 0
	if !isChannelMigrationClearFenceIdempotent(task, meta, req) {
		t.Fatal("exact persisted clear-fence result was not recognized as idempotent")
	}

	badTask := task
	badTask.FenceToken = "still-owned"
	if isChannelMigrationClearFenceIdempotent(badTask, meta, req) {
		t.Fatal("task retaining fence state must not be idempotent")
	}
	badMeta := meta
	badMeta.WriteFenceVersion++
	if isChannelMigrationClearFenceIdempotent(task, badMeta, req) {
		t.Fatal("runtime metadata with a different fence generation must not be idempotent")
	}
}

func TestChannelMigrationCleanupHelpersDoNotRetainFenceOrProof(t *testing.T) {
	meta := validMigrationRuntimeMeta()
	clearedMeta := clearChannelRuntimeMetaFence(meta)
	if clearedMeta.WriteFenceToken != "" || clearedMeta.WriteFenceReason != 0 || clearedMeta.WriteFenceUntilMS != 0 || clearedMeta.WriteFenceVersion != meta.WriteFenceVersion+1 {
		t.Fatalf("cleared runtime fence = %+v", clearedMeta)
	}

	task := validMigrationTask(ChannelMigrationKindReplicaReplace, ChannelMigrationPhaseCutoverFence)
	task.FenceToken, task.FenceVersion, task.FenceUntilMS = task.TaskID, 8, 900
	task.CutoverLEO, task.CutoverHW = 100, 90
	task.DrainedLeaderNode, task.DrainedRuntimeGeneration = 1, 8
	task.DrainedChannelEpoch, task.DrainedLeaderEpoch, task.DrainedFenceVersion = 2, 3, 8
	clearedTask := clearChannelMigrationTaskFenceAndProof(task)
	if clearedTask.FenceToken != "" || clearedTask.FenceVersion != 0 || clearedTask.FenceUntilMS != 0 || (ChannelMigrationCutoverProof{
		CutoverLEO: clearedTask.CutoverLEO, CutoverHW: clearedTask.CutoverHW,
		DrainedLeaderNode: clearedTask.DrainedLeaderNode, DrainedRuntimeGeneration: clearedTask.DrainedRuntimeGeneration,
		DrainedChannelEpoch: clearedTask.DrainedChannelEpoch, DrainedLeaderEpoch: clearedTask.DrainedLeaderEpoch,
		DrainedFenceVersion: clearedTask.DrainedFenceVersion,
	}).hasAny() {
		t.Fatalf("cleared task retained fence or proof: %+v", clearedTask)
	}

	if got := replaceUint64Member([]uint64{3, 1, 3, 2}, 3, 2); !reflect.DeepEqual(got, []uint64{1, 2}) {
		t.Fatalf("replaceUint64Member = %v, want [1 2]", got)
	}
	if got := removeUint64Member([]uint64{3, 1, 3, 2}, 3); !reflect.DeepEqual(got, []uint64{1, 2}) {
		t.Fatalf("removeUint64Member = %v, want [1 2]", got)
	}
	if channelMigrationTaskDesiredLeader(ChannelMigrationTask{DesiredLeader: 2, EmbeddedLeaderTransfer: true, EmbeddedDesiredLeader: 3}) != 3 {
		t.Fatal("embedded desired leader did not take precedence")
	}
}

func validMigrationTaskGuard() ChannelMigrationTaskGuard {
	return ChannelMigrationTaskGuard{
		ChannelID: "channel-a", ChannelType: 1, TaskID: "task-a",
		ExpectedStatus: ChannelMigrationStatusRunning, ExpectedPhase: ChannelMigrationPhaseWriteFence,
		ExpectedOwnerNodeID: 1, ExpectedOwnerLeaseUntilMS: 200, ExpectedUpdatedAtMS: 100,
	}
}

func validMigrationRuntimeGuard() ChannelMigrationRuntimeGuard {
	return ChannelMigrationRuntimeGuard{
		ChannelID: "channel-a", ChannelType: 1, ExpectedChannelEpoch: 2,
		ExpectedLeaderEpoch: 3, ExpectedLeader: 1, ExpectedFenceToken: "task-a",
		ExpectedFenceVersion: 8, ExpectedRouteGeneration: 9,
	}
}

func validMigrationTask(kind ChannelMigrationKind, phase ChannelMigrationPhase) ChannelMigrationTask {
	return ChannelMigrationTask{
		TaskID: "task-a", Kind: kind, Status: ChannelMigrationStatusRunning, Phase: phase,
		ChannelID: "channel-a", ChannelType: 1, SourceNode: 1, TargetNode: 2,
		DesiredLeader: 2, BaseChannelEpoch: 2, BaseLeaderEpoch: 3,
		OwnerNodeID: 1, OwnerLeaseUntilMS: 200, CreatedAtMS: 1, UpdatedAtMS: 100,
	}
}

func embeddedLeaderTransferTask(phase ChannelMigrationPhase) ChannelMigrationTask {
	task := validMigrationTask(ChannelMigrationKindReplicaReplace, phase)
	task.EmbeddedLeaderTransfer = true
	task.EmbeddedDesiredLeader = 2
	return task
}

func validMigrationRuntimeMeta() ChannelRuntimeMeta {
	return ChannelRuntimeMeta{
		ChannelID: "channel-a", ChannelType: 1, ChannelEpoch: 2, LeaderEpoch: 3,
		RouteGeneration: 9, Replicas: []uint64{1, 2}, ISR: []uint64{1, 2}, Leader: 1, MinISR: 1,
		WriteFenceToken: "task-a", WriteFenceVersion: 8, WriteFenceReason: 1, WriteFenceUntilMS: 900,
	}
}
