package meta

import (
	"context"
	"errors"
	"testing"
)

func TestChannelLeaderMigrationPersistsFencedCutoverAndIdempotentCompletion(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	const hashSlot uint16 = 40
	shard := db.ForHashSlot(hashSlot)

	if err := shard.UpsertChannelRuntimeMeta(ctx, testRuntimeMeta("leader-move", 1)); err != nil {
		t.Fatalf("UpsertChannelRuntimeMeta(): %v", err)
	}
	meta, err := shard.GetChannelRuntimeMeta(ctx, "leader-move", 1)
	if err != nil {
		t.Fatalf("GetChannelRuntimeMeta(): %v", err)
	}
	task := ChannelMigrationTask{
		TaskID: "leader-move-1", Kind: ChannelMigrationKindLeaderTransfer,
		Status: ChannelMigrationStatusPending, Phase: ChannelMigrationPhaseValidate,
		ChannelID: meta.ChannelID, ChannelType: meta.ChannelType,
		SourceNode: meta.Leader, TargetNode: 2, DesiredLeader: 2,
		BaseChannelEpoch: meta.ChannelEpoch, BaseLeaderEpoch: meta.LeaderEpoch,
		CreatedAtMS: 1000, UpdatedAtMS: 1000,
	}
	create := db.NewWriteBatch()
	defer create.Close()
	if err := create.CreateChannelMigrationTaskWithRuntimeGuard(hashSlot, ChannelMigrationTaskCreate{
		Task: task, RuntimeGuard: channelMigrationRuntimeGuard(meta),
	}); err != nil {
		t.Fatalf("CreateChannelMigrationTaskWithRuntimeGuard(): %v", err)
	}
	if err := create.Commit(); err != nil {
		t.Fatalf("Commit(create): %v", err)
	}
	active, ok, err := shard.GetActiveChannelMigrationTask(ctx, task.ChannelID, task.ChannelType)
	if err != nil || !ok || active.TaskID != task.TaskID {
		t.Fatalf("GetActiveChannelMigrationTask() = (%+v, %v, %v)", active, ok, err)
	}
	activeTasks, err := shard.ListActiveChannelMigrationTasks(ctx, 10)
	if err != nil || len(activeTasks) != 1 || activeTasks[0].TaskID != task.TaskID {
		t.Fatalf("ListActiveChannelMigrationTasks() = (%+v, %v)", activeTasks, err)
	}

	claim := db.NewWriteBatch()
	defer claim.Close()
	if err := claim.ClaimChannelMigrationTask(hashSlot, ChannelMigrationTaskClaim{
		Guard:  channelMigrationTaskGuard(task),
		Status: ChannelMigrationStatusRunning, Phase: ChannelMigrationPhaseWriteFence,
		OwnerNodeID: 9, OwnerLeaseUntilMS: 4000, NowMS: 1100, UpdatedAtMS: 1100,
	}); err != nil {
		t.Fatalf("ClaimChannelMigrationTask(): %v", err)
	}
	if err := claim.Commit(); err != nil {
		t.Fatalf("Commit(claim): %v", err)
	}
	task, err = shard.GetChannelMigrationTask(ctx, task.ChannelID, task.ChannelType, task.TaskID)
	if err != nil || task.OwnerNodeID != 9 || task.Phase != ChannelMigrationPhaseWriteFence {
		t.Fatalf("GetChannelMigrationTask(claimed) = (%+v, %v)", task, err)
	}

	fence := db.NewWriteBatch()
	defer fence.Close()
	if err := fence.SetChannelWriteFence(hashSlot, ChannelMigrationFenceRequest{
		Guard: channelMigrationTaskGuard(task), RuntimeGuard: channelMigrationRuntimeGuard(meta),
		Status: ChannelMigrationStatusRunning, Phase: ChannelMigrationPhaseDrainLeader,
		FenceReason: 1, FenceUntilMS: 3000, UpdatedAtMS: 1200,
	}); err != nil {
		t.Fatalf("SetChannelWriteFence(): %v", err)
	}
	if err := fence.Commit(); err != nil {
		t.Fatalf("Commit(fence): %v", err)
	}
	task, err = shard.GetChannelMigrationTask(ctx, task.ChannelID, task.ChannelType, task.TaskID)
	if err != nil {
		t.Fatalf("GetChannelMigrationTask(fenced): %v", err)
	}
	meta, err = shard.GetChannelRuntimeMeta(ctx, meta.ChannelID, meta.ChannelType)
	if err != nil {
		t.Fatalf("GetChannelRuntimeMeta(fenced): %v", err)
	}
	if task.FenceToken != task.TaskID || task.FenceVersion == 0 || meta.WriteFenceToken != task.TaskID || meta.WriteFenceVersion != task.FenceVersion {
		t.Fatalf("persisted fence task=%+v meta=%+v", task, meta)
	}

	drain := db.NewWriteBatch()
	defer drain.Close()
	proof := ChannelMigrationCutoverProof{
		CutoverLEO: 100, CutoverHW: 100,
		DrainedLeaderNode: meta.Leader, DrainedRuntimeGeneration: meta.RouteGeneration,
		DrainedChannelEpoch: meta.ChannelEpoch, DrainedLeaderEpoch: meta.LeaderEpoch,
		DrainedFenceVersion: meta.WriteFenceVersion,
	}
	if err := drain.AdvanceChannelMigrationTask(hashSlot, ChannelMigrationTaskAdvance{
		Guard:  channelMigrationTaskGuard(task),
		Status: ChannelMigrationStatusRunning, Phase: ChannelMigrationPhaseCommitLeaderMeta,
		Attempt: 1, UpdatedAtMS: 1300, CutoverProof: proof,
		Progress: ChannelMigrationProgress{LeaderLEO: 100, LeaderHW: 100, TargetLEO: 100, TargetCheckpointHW: 100},
	}); err != nil {
		t.Fatalf("AdvanceChannelMigrationTask(drain proof): %v", err)
	}
	if err := drain.Commit(); err != nil {
		t.Fatalf("Commit(drain proof): %v", err)
	}
	task, err = shard.GetChannelMigrationTask(ctx, task.ChannelID, task.ChannelType, task.TaskID)
	if err != nil || task.CutoverHW != 100 || task.DrainedRuntimeGeneration != meta.RouteGeneration {
		t.Fatalf("GetChannelMigrationTask(with proof) = (%+v, %v)", task, err)
	}

	transfer := db.NewWriteBatch()
	defer transfer.Close()
	if err := transfer.CommitChannelLeaderTransfer(hashSlot, ChannelMigrationLeaderTransferRequest{
		Guard: channelMigrationTaskGuard(task), RuntimeGuard: channelMigrationRuntimeGuard(meta),
		Status: ChannelMigrationStatusRunning, Phase: ChannelMigrationPhaseVerifyNewLeader,
		DesiredLeader: 2, NextLeaderEpoch: meta.LeaderEpoch + 1,
		LeaseUntilMS: 5000, NowMS: 2000, UpdatedAtMS: 1400,
	}); err != nil {
		t.Fatalf("CommitChannelLeaderTransfer(): %v", err)
	}
	if err := transfer.Commit(); err != nil {
		t.Fatalf("Commit(leader transfer): %v", err)
	}
	task, err = shard.GetChannelMigrationTask(ctx, task.ChannelID, task.ChannelType, task.TaskID)
	if err != nil || task.Phase != ChannelMigrationPhaseVerifyNewLeader {
		t.Fatalf("GetChannelMigrationTask(transferred) = (%+v, %v)", task, err)
	}
	meta, err = shard.GetChannelRuntimeMeta(ctx, meta.ChannelID, meta.ChannelType)
	if err != nil || meta.Leader != 2 || meta.LeaderEpoch != 3 || meta.WriteFenceToken != task.TaskID {
		t.Fatalf("GetChannelRuntimeMeta(transferred) = (%+v, %v)", meta, err)
	}

	clearReq := ChannelMigrationClearFenceRequest{
		Guard: channelMigrationTaskGuard(task), RuntimeGuard: channelMigrationRuntimeGuard(meta),
		Status: ChannelMigrationStatusCompleted, Phase: ChannelMigrationPhaseClearFence,
		UpdatedAtMS: 1500, CompletedAtMS: 1500,
	}
	clear := db.NewWriteBatch()
	defer clear.Close()
	if err := clear.ClearChannelWriteFence(hashSlot, clearReq); err != nil {
		t.Fatalf("ClearChannelWriteFence(): %v", err)
	}
	if err := clear.Commit(); err != nil {
		t.Fatalf("Commit(clear): %v", err)
	}
	completed, err := shard.GetChannelMigrationTask(ctx, task.ChannelID, task.ChannelType, task.TaskID)
	if err != nil || !completed.IsTerminal() || completed.FenceToken != "" || completed.CutoverLEO != 0 {
		t.Fatalf("GetChannelMigrationTask(completed) = (%+v, %v)", completed, err)
	}
	completedMeta, err := shard.GetChannelRuntimeMeta(ctx, meta.ChannelID, meta.ChannelType)
	if err != nil || completedMeta.Leader != 2 || completedMeta.WriteFenceToken != "" || completedMeta.WriteFenceVersion != meta.WriteFenceVersion+1 {
		t.Fatalf("GetChannelRuntimeMeta(completed) = (%+v, %v)", completedMeta, err)
	}
	if _, ok, err := shard.GetActiveChannelMigrationTask(ctx, task.ChannelID, task.ChannelType); err != nil || ok {
		t.Fatalf("GetActiveChannelMigrationTask(after completion) = (ok %v, err %v), want none", ok, err)
	}

	replay := db.NewWriteBatch()
	defer replay.Close()
	if err := replay.ClearChannelWriteFence(hashSlot, clearReq); err != nil {
		t.Fatalf("ClearChannelWriteFence(replay): %v", err)
	}
	if err := replay.Commit(); err != nil {
		t.Fatalf("Commit(clear replay): %v", err)
	}
	replayedMeta, err := shard.GetChannelRuntimeMeta(ctx, meta.ChannelID, meta.ChannelType)
	if err != nil || !channelRuntimeMetaEqual(replayedMeta, completedMeta) {
		t.Fatalf("idempotent clear changed runtime metadata: before=%+v after=%+v err=%v", completedMeta, replayedMeta, err)
	}

	plan, err := shard.PlanTerminalChannelMigrationTaskGC(ctx, 1600, 10)
	if err != nil || plan.TaskCount != 1 || plan.EntryCount != 1 {
		t.Fatalf("PlanTerminalChannelMigrationTaskGC() = (%+v, %v)", plan, err)
	}
	deleted, err := shard.DeleteTerminalChannelMigrationTasksBefore(ctx, 1600, 10)
	if err != nil || deleted != 1 {
		t.Fatalf("DeleteTerminalChannelMigrationTasksBefore() = (%d, %v)", deleted, err)
	}
	if _, err := shard.GetChannelMigrationTask(ctx, task.ChannelID, task.ChannelType, task.TaskID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetChannelMigrationTask(after GC) error = %v, want not found", err)
	}
}

func TestExpiredMigrationFenceResetsTaskAndRuntimeToPreCutover(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	const hashSlot uint16 = 41
	shard := db.ForHashSlot(hashSlot)

	if err := shard.UpsertChannelRuntimeMeta(ctx, testRuntimeMeta("reset-fence", 1)); err != nil {
		t.Fatalf("UpsertChannelRuntimeMeta(): %v", err)
	}
	meta, err := shard.GetChannelRuntimeMeta(ctx, "reset-fence", 1)
	if err != nil {
		t.Fatalf("GetChannelRuntimeMeta(): %v", err)
	}
	task := ChannelMigrationTask{
		TaskID: "reset-fence-1", Kind: ChannelMigrationKindLeaderTransfer,
		Status: ChannelMigrationStatusRunning, Phase: ChannelMigrationPhaseWriteFence,
		ChannelID: meta.ChannelID, ChannelType: meta.ChannelType,
		SourceNode: 1, TargetNode: 2, DesiredLeader: 2,
		BaseChannelEpoch: meta.ChannelEpoch, BaseLeaderEpoch: meta.LeaderEpoch,
		OwnerNodeID: 9, OwnerLeaseUntilMS: 6000,
		CreatedAtMS: 1000, UpdatedAtMS: 1000,
	}
	if err := shard.CreateChannelMigrationTask(ctx, task); err != nil {
		t.Fatalf("CreateChannelMigrationTask(): %v", err)
	}
	fence := db.NewWriteBatch()
	defer fence.Close()
	if err := fence.SetChannelWriteFence(hashSlot, ChannelMigrationFenceRequest{
		Guard: channelMigrationTaskGuard(task), RuntimeGuard: channelMigrationRuntimeGuard(meta),
		Status: ChannelMigrationStatusRunning, Phase: ChannelMigrationPhaseDrainLeader,
		FenceReason: 2, FenceUntilMS: 2000, UpdatedAtMS: 1100,
	}); err != nil {
		t.Fatalf("SetChannelWriteFence(): %v", err)
	}
	if err := fence.Commit(); err != nil {
		t.Fatalf("Commit(fence): %v", err)
	}
	task, err = shard.GetChannelMigrationTask(ctx, task.ChannelID, task.ChannelType, task.TaskID)
	if err != nil {
		t.Fatalf("GetChannelMigrationTask(fenced): %v", err)
	}
	meta, err = shard.GetChannelRuntimeMeta(ctx, meta.ChannelID, meta.ChannelType)
	if err != nil {
		t.Fatalf("GetChannelRuntimeMeta(fenced): %v", err)
	}

	reset := db.NewWriteBatch()
	defer reset.Close()
	if err := reset.ResetChannelWriteFenceToPreCutover(hashSlot, ChannelMigrationResetFenceRequest{
		Guard: channelMigrationTaskGuard(task), RuntimeGuard: channelMigrationRuntimeGuard(meta),
		Status: ChannelMigrationStatusRunning, Phase: ChannelMigrationPhaseProbeTarget,
		NowMS: 2001, UpdatedAtMS: 1200,
	}); err != nil {
		t.Fatalf("ResetChannelWriteFenceToPreCutover(): %v", err)
	}
	if err := reset.Commit(); err != nil {
		t.Fatalf("Commit(reset): %v", err)
	}
	task, err = shard.GetChannelMigrationTask(ctx, task.ChannelID, task.ChannelType, task.TaskID)
	if err != nil || task.Phase != ChannelMigrationPhaseProbeTarget || task.FenceToken != "" || task.CutoverLEO != 0 {
		t.Fatalf("GetChannelMigrationTask(reset) = (%+v, %v)", task, err)
	}
	resetMeta, err := shard.GetChannelRuntimeMeta(ctx, meta.ChannelID, meta.ChannelType)
	if err != nil || resetMeta.WriteFenceToken != "" || resetMeta.WriteFenceVersion != meta.WriteFenceVersion+1 {
		t.Fatalf("GetChannelRuntimeMeta(reset) = (%+v, %v)", resetMeta, err)
	}
}

func TestAbortReplicaMigrationRemovesOnlyUnpromotedLearner(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	const hashSlot uint16 = 42
	shard := db.ForHashSlot(hashSlot)

	runtime := testRuntimeMeta("abort-learner", 1)
	runtime.Replicas = []uint64{1, 2, 3, 4}
	runtime.ISR = []uint64{1, 2}
	if err := shard.UpsertChannelRuntimeMeta(ctx, runtime); err != nil {
		t.Fatalf("UpsertChannelRuntimeMeta(): %v", err)
	}
	meta, err := shard.GetChannelRuntimeMeta(ctx, runtime.ChannelID, runtime.ChannelType)
	if err != nil {
		t.Fatalf("GetChannelRuntimeMeta(): %v", err)
	}
	task := ChannelMigrationTask{
		TaskID: "abort-learner-1", Kind: ChannelMigrationKindReplicaReplace,
		Status: ChannelMigrationStatusRunning, Phase: ChannelMigrationPhaseBootstrapTarget,
		ChannelID: meta.ChannelID, ChannelType: meta.ChannelType,
		SourceNode: 3, TargetNode: 4,
		BaseChannelEpoch: meta.ChannelEpoch, BaseLeaderEpoch: meta.LeaderEpoch,
		OwnerNodeID: 9, OwnerLeaseUntilMS: 5000,
		CreatedAtMS: 1000, UpdatedAtMS: 1000,
	}
	if err := shard.CreateChannelMigrationTask(ctx, task); err != nil {
		t.Fatalf("CreateChannelMigrationTask(): %v", err)
	}
	abort := db.NewWriteBatch()
	defer abort.Close()
	if err := abort.AbortChannelMigration(hashSlot, ChannelMigrationAbortRequest{
		Guard: channelMigrationTaskGuard(task), RuntimeGuard: channelMigrationRuntimeGuard(meta),
		Status: ChannelMigrationStatusAborted, Phase: ChannelMigrationPhaseClearFence,
		UpdatedAtMS: 1100, CompletedAtMS: 1100, LastError: "operator canceled",
	}); err != nil {
		t.Fatalf("AbortChannelMigration(): %v", err)
	}
	if err := abort.Commit(); err != nil {
		t.Fatalf("Commit(abort): %v", err)
	}
	aborted, err := shard.GetChannelMigrationTask(ctx, task.ChannelID, task.ChannelType, task.TaskID)
	if err != nil || aborted.Status != ChannelMigrationStatusAborted || aborted.LastError != "operator canceled" {
		t.Fatalf("GetChannelMigrationTask(aborted) = (%+v, %v)", aborted, err)
	}
	abortedMeta, err := shard.GetChannelRuntimeMeta(ctx, meta.ChannelID, meta.ChannelType)
	if err != nil {
		t.Fatalf("GetChannelRuntimeMeta(aborted): %v", err)
	}
	if containsUint64(abortedMeta.Replicas, 4) || !containsUint64(abortedMeta.Replicas, 3) || !containsUint64(abortedMeta.ISR, 1) || !containsUint64(abortedMeta.ISR, 2) {
		t.Fatalf("runtime membership after abort = replicas %v ISR %v", abortedMeta.Replicas, abortedMeta.ISR)
	}
}

func TestCompatibilityShardMigrationAdmissionHonorsRuntimeGuardAndReplay(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	const hashSlot uint16 = 43
	shard := db.ForHashSlot(hashSlot)

	if err := shard.UpsertChannelRuntimeMeta(ctx, testRuntimeMeta("admitted", 1)); err != nil {
		t.Fatalf("UpsertChannelRuntimeMeta(): %v", err)
	}
	meta, err := shard.GetChannelRuntimeMeta(ctx, "admitted", 1)
	if err != nil {
		t.Fatalf("GetChannelRuntimeMeta(): %v", err)
	}
	task := ChannelMigrationTask{
		TaskID: "admitted-1", Kind: ChannelMigrationKindLeaderTransfer,
		Status: ChannelMigrationStatusPending, Phase: ChannelMigrationPhaseValidate,
		ChannelID: meta.ChannelID, ChannelType: meta.ChannelType,
		SourceNode: meta.Leader, TargetNode: 2, DesiredLeader: 2,
		BaseChannelEpoch: meta.ChannelEpoch, BaseLeaderEpoch: meta.LeaderEpoch,
		CreatedAtMS: 1000, UpdatedAtMS: 1000,
	}
	if err := shard.CreateChannelMigrationTaskWithRuntimeGuard(ctx, ChannelMigrationTaskCreate{
		Task: task, RuntimeGuard: channelMigrationRuntimeGuard(meta),
	}); err != nil {
		t.Fatalf("CreateChannelMigrationTaskWithRuntimeGuard(): %v", err)
	}
	tasks, err := shard.ListChannelMigrationTasks(ctx)
	if err != nil || len(tasks) != 1 || tasks[0].TaskID != task.TaskID {
		t.Fatalf("ListChannelMigrationTasks() = (%+v, %v)", tasks, err)
	}
	claim := ChannelMigrationTaskClaim{
		Guard:  channelMigrationTaskGuard(task),
		Status: ChannelMigrationStatusRunning, Phase: ChannelMigrationPhaseProbeTarget,
		OwnerNodeID: 7, OwnerLeaseUntilMS: 5000, NowMS: 1100, UpdatedAtMS: 1100,
	}
	if err := shard.ClaimChannelMigrationTask(ctx, claim); err != nil {
		t.Fatalf("ClaimChannelMigrationTask(): %v", err)
	}
	claimed, err := shard.GetChannelMigrationTask(ctx, task.ChannelID, task.ChannelType, task.TaskID)
	if err != nil {
		t.Fatalf("GetChannelMigrationTask(claimed): %v", err)
	}
	if err := shard.AdvanceChannelMigrationTask(ctx, ChannelMigrationTaskAdvance{
		Guard:  channelMigrationTaskGuard(claimed),
		Status: ChannelMigrationStatusRunning, Phase: ChannelMigrationPhaseWriteFence,
		Attempt: 1, UpdatedAtMS: 1200,
	}); err != nil {
		t.Fatalf("AdvanceChannelMigrationTask(): %v", err)
	}
	advanced, err := shard.GetChannelMigrationTask(ctx, task.ChannelID, task.ChannelType, task.TaskID)
	if err != nil || advanced.Phase != ChannelMigrationPhaseWriteFence || advanced.Attempt != 1 {
		t.Fatalf("GetChannelMigrationTask(advanced) = (%+v, %v)", advanced, err)
	}

	if err := shard.UpsertChannelRuntimeMeta(ctx, testRuntimeMeta("stale-admission", 1)); err != nil {
		t.Fatalf("UpsertChannelRuntimeMeta(stale admission): %v", err)
	}
	staleMeta, err := shard.GetChannelRuntimeMeta(ctx, "stale-admission", 1)
	if err != nil {
		t.Fatalf("GetChannelRuntimeMeta(stale admission): %v", err)
	}
	changedMeta := staleMeta
	changedMeta.LeaderEpoch++
	if err := shard.UpsertChannelRuntimeMeta(ctx, changedMeta); err != nil {
		t.Fatalf("UpsertChannelRuntimeMeta(change): %v", err)
	}
	staleTask := task
	staleTask.TaskID = "stale-admission-1"
	staleTask.ChannelID = staleMeta.ChannelID
	staleTask.BaseChannelEpoch = staleMeta.ChannelEpoch
	staleTask.BaseLeaderEpoch = staleMeta.LeaderEpoch
	if err := shard.CreateChannelMigrationTaskWithRuntimeGuard(ctx, ChannelMigrationTaskCreate{
		Task: staleTask, RuntimeGuard: channelMigrationRuntimeGuard(staleMeta),
	}); !errors.Is(err, ErrStaleMeta) {
		t.Fatalf("CreateChannelMigrationTaskWithRuntimeGuard(stale) error = %v, want stale metadata", err)
	}
	if _, err := shard.GetChannelMigrationTask(ctx, staleTask.ChannelID, staleTask.ChannelType, staleTask.TaskID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetChannelMigrationTask(rejected admission) error = %v, want not found", err)
	}

	replayTask := task
	replayTask.TaskID = "batch-replay-1"
	replayTask.ChannelID = "batch-replay"
	replay := db.NewWriteBatch()
	defer replay.Close()
	if err := replay.CreateChannelMigrationTask(hashSlot, replayTask); err != nil {
		t.Fatalf("CreateChannelMigrationTask(first): %v", err)
	}
	if err := replay.CreateChannelMigrationTask(hashSlot, replayTask); err != nil {
		t.Fatalf("CreateChannelMigrationTask(idempotent replay): %v", err)
	}
	conflicting := replayTask
	conflicting.TargetNode = 3
	conflicting.DesiredLeader = 3
	if err := replay.CreateChannelMigrationTask(hashSlot, conflicting); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("CreateChannelMigrationTask(conflicting replay) error = %v, want already exists", err)
	}
	if err := replay.Commit(); err != nil {
		t.Fatalf("Commit(replay): %v", err)
	}
	gotReplay, err := shard.GetChannelMigrationTask(ctx, replayTask.ChannelID, replayTask.ChannelType, replayTask.TaskID)
	if err != nil || gotReplay != replayTask {
		t.Fatalf("GetChannelMigrationTask(replay) = (%+v, %v), want %+v", gotReplay, err, replayTask)
	}
}
