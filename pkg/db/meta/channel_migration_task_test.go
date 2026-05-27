package meta

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
)

func TestChannelMigrationTaskCreateGetActiveAndList(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	shard := store.db.HashSlot(8)
	ctx := context.Background()
	task := testChannelMigrationTask("task-create", "channel-create")

	if err := shard.CreateChannelMigrationTask(ctx, task); err != nil {
		t.Fatalf("CreateChannelMigrationTask(): %v", err)
	}
	got, ok, err := shard.GetChannelMigrationTask(ctx, task.ChannelID, task.ChannelType, task.TaskID)
	if err != nil || !ok {
		t.Fatalf("GetChannelMigrationTask() ok=%v err=%v", ok, err)
	}
	if got != task {
		t.Fatalf("task = %+v, want %+v", got, task)
	}
	active, ok, err := shard.GetActiveChannelMigrationTask(ctx, task.ChannelID, task.ChannelType)
	if err != nil || !ok || active.TaskID != task.TaskID {
		t.Fatalf("active task=%+v ok=%v err=%v, want %s", active, ok, err, task.TaskID)
	}
	list, err := shard.ListChannelMigrationTasks(ctx)
	if err != nil {
		t.Fatalf("ListChannelMigrationTasks(): %v", err)
	}
	if len(list) != 1 || list[0] != task {
		t.Fatalf("tasks = %+v, want [%+v]", list, task)
	}
}

func TestChannelMigrationTaskKeepsLegacyRowIndexLayouts(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	shard := store.db.HashSlot(8)
	ctx := context.Background()
	task := testChannelMigrationTask("task-layout", "channel-layout")

	if err := shard.CreateChannelMigrationTask(ctx, task); err != nil {
		t.Fatalf("CreateChannelMigrationTask(): %v", err)
	}

	legacyRowKey := encodeChannelMigrationTaskRowKey(8, task.ChannelID, task.ChannelType, task.TaskID, channelMigrationPrimaryFamilyID)
	runtimeRowKey, err := channelMigrationTable.primaryRowKey(8, channelMigrationTaskPrimaryKey(task.ChannelID, task.ChannelType, task.TaskID))
	if err != nil {
		t.Fatalf("runtime row key: %v", err)
	}
	if string(runtimeRowKey) != string(legacyRowKey) {
		t.Fatalf("runtime row key %x, want legacy %x", runtimeRowKey, legacyRowKey)
	}
	if _, ok, err := store.db.get(legacyRowKey); err != nil || !ok {
		t.Fatalf("legacy row ok=%v err=%v", ok, err)
	}

	activeValue, ok, err := store.db.get(encodeChannelMigrationActiveIndexKey(8, task.ChannelID, task.ChannelType))
	if err != nil || !ok || string(activeValue) != task.TaskID {
		t.Fatalf("active index value=%q ok=%v err=%v, want task id", string(activeValue), ok, err)
	}

	terminal := task
	terminal.Status = ChannelMigrationStatusCompleted
	terminal.CompletedAtMS = task.UpdatedAtMS + 1000
	if err := shard.AdvanceChannelMigrationTask(ctx, ChannelMigrationTaskAdvance{
		Guard:         channelMigrationTaskGuard(task),
		Status:        terminal.Status,
		Phase:         ChannelMigrationPhaseClearFence,
		UpdatedAtMS:   terminal.UpdatedAtMS + 1,
		CompletedAtMS: terminal.CompletedAtMS,
	}); err != nil {
		t.Fatalf("AdvanceChannelMigrationTask(): %v", err)
	}
	legacyTerminalKey := encodeChannelMigrationTerminalIndexKey(8, terminal.CompletedAtMS, terminal.ChannelID, terminal.ChannelType, terminal.TaskID)
	runtimeTerminalKey, err := channelMigrationTable.indexEntryKey(8, channelMigrationTerminalIndexSpec(), channelMigrationTerminalIndexParts(terminal), channelMigrationTaskPrimaryKey(terminal.ChannelID, terminal.ChannelType, terminal.TaskID))
	if err != nil {
		t.Fatalf("runtime terminal key: %v", err)
	}
	if string(runtimeTerminalKey) != string(legacyTerminalKey) {
		t.Fatalf("runtime terminal key %x, want legacy %x", runtimeTerminalKey, legacyTerminalKey)
	}
	if _, ok, err := store.db.get(legacyTerminalKey); err != nil || !ok {
		t.Fatalf("legacy terminal index ok=%v err=%v", ok, err)
	}
}

func TestChannelMigrationTaskRejectsSecondActiveTask(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	shard := store.db.HashSlot(8)
	ctx := context.Background()
	first := testChannelMigrationTask("task-active-1", "channel-active")
	second := testChannelMigrationTask("task-active-2", "channel-active")

	if err := shard.CreateChannelMigrationTask(ctx, first); err != nil {
		t.Fatalf("CreateChannelMigrationTask(first): %v", err)
	}
	err := shard.CreateChannelMigrationTask(ctx, second)
	if !errors.Is(err, dberrors.ErrAlreadyExists) {
		t.Fatalf("CreateChannelMigrationTask(second) err = %v, want already exists", err)
	}
}

func TestChannelMigrationTaskClaimAdvanceAndTerminalIndex(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	shard := store.db.HashSlot(8)
	ctx := context.Background()
	task := testChannelMigrationTask("task-claim", "channel-claim")
	if err := shard.CreateChannelMigrationTask(ctx, task); err != nil {
		t.Fatalf("CreateChannelMigrationTask(): %v", err)
	}

	claim := ChannelMigrationTaskClaim{
		Guard:             channelMigrationTaskGuard(task),
		Status:            ChannelMigrationStatusRunning,
		Phase:             ChannelMigrationPhaseWriteFence,
		OwnerNodeID:       3,
		OwnerLeaseUntilMS: 1750000005000,
		UpdatedAtMS:       1750000001000,
	}
	if err := shard.ClaimChannelMigrationTask(ctx, claim); err != nil {
		t.Fatalf("ClaimChannelMigrationTask(): %v", err)
	}
	got, ok, err := shard.GetChannelMigrationTask(ctx, task.ChannelID, task.ChannelType, task.TaskID)
	if err != nil || !ok {
		t.Fatalf("GetChannelMigrationTask(claimed) ok=%v err=%v", ok, err)
	}
	if got.OwnerNodeID != 3 || got.Status != ChannelMigrationStatusRunning || got.Phase != ChannelMigrationPhaseWriteFence {
		t.Fatalf("claimed task = %+v", got)
	}

	stale := claim
	stale.OwnerNodeID = 4
	err = shard.ClaimChannelMigrationTask(ctx, stale)
	if !errors.Is(err, dberrors.ErrConflict) {
		t.Fatalf("stale claim err = %v, want conflict", err)
	}

	advance := ChannelMigrationTaskAdvance{
		Guard:         channelMigrationTaskGuard(got),
		Status:        ChannelMigrationStatusCompleted,
		Phase:         ChannelMigrationPhaseClearFence,
		Attempt:       2,
		UpdatedAtMS:   1750000002000,
		CompletedAtMS: 1750000003000,
		LastError:     "done",
	}
	if err := shard.AdvanceChannelMigrationTask(ctx, advance); err != nil {
		t.Fatalf("AdvanceChannelMigrationTask(): %v", err)
	}
	active, ok, err := shard.GetActiveChannelMigrationTask(ctx, task.ChannelID, task.ChannelType)
	if err != nil || ok {
		t.Fatalf("active after terminal ok=%v err=%v task=%+v, want none", ok, err, active)
	}
	count, err := shard.CountTerminalChannelMigrationTasksBefore(ctx, 1750000004000, 10)
	if err != nil || count != 1 {
		t.Fatalf("CountTerminalChannelMigrationTasksBefore() = %d err %v, want 1", count, err)
	}
}

func TestMetaBatchCreateChannelMigrationTaskWithRuntimeGuard(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()
	shard := store.db.HashSlot(8)
	task := testChannelMigrationTask("task-batch-guard", "channel-batch-guard")
	meta := testRuntimeMeta(task.ChannelID, task.ChannelType)
	if _, err := shard.UpsertChannelRuntimeMeta(ctx, meta); err != nil {
		t.Fatalf("UpsertChannelRuntimeMeta(): %v", err)
	}

	batch := store.db.NewBatch()
	if err := batch.CreateChannelMigrationTaskWithRuntimeGuard(8, ChannelMigrationTaskCreate{
		Task:         task,
		RuntimeGuard: channelMigrationRuntimeGuard(meta),
	}); err != nil {
		t.Fatalf("CreateChannelMigrationTaskWithRuntimeGuard(stage): %v", err)
	}
	if err := batch.Commit(ctx); err != nil {
		t.Fatalf("Commit(): %v", err)
	}
	if _, ok, err := shard.GetChannelMigrationTask(ctx, task.ChannelID, task.ChannelType, task.TaskID); err != nil || !ok {
		t.Fatalf("GetChannelMigrationTask(after batch) ok=%v err=%v", ok, err)
	}

	first := store.db.NewBatch()
	second := store.db.NewBatch()
	raceA := testChannelMigrationTask("task-race-a", "channel-race")
	raceB := testChannelMigrationTask("task-race-b", "channel-race")
	if err := first.CreateChannelMigrationTask(8, raceA); err != nil {
		t.Fatalf("CreateChannelMigrationTask(first stage): %v", err)
	}
	if err := second.CreateChannelMigrationTask(8, raceB); err != nil {
		t.Fatalf("CreateChannelMigrationTask(second stage): %v", err)
	}
	if err := first.Commit(ctx); err != nil {
		t.Fatalf("first Commit(): %v", err)
	}
	err := second.Commit(ctx)
	if !errors.Is(err, dberrors.ErrAlreadyExists) {
		t.Fatalf("second Commit() err = %v, want already exists", err)
	}
}

func testChannelMigrationTask(taskID string, channelID string) ChannelMigrationTask {
	return ChannelMigrationTask{
		TaskID:           taskID,
		Kind:             ChannelMigrationKindReplicaReplace,
		Status:           ChannelMigrationStatusPending,
		Phase:            ChannelMigrationPhaseValidate,
		ChannelID:        channelID,
		ChannelType:      1,
		SourceNode:       1,
		TargetNode:       4,
		BaseChannelEpoch: 10,
		BaseLeaderEpoch:  20,
		CreatedAtMS:      1750000000000,
		UpdatedAtMS:      1750000000000,
	}
}

func channelMigrationTaskGuard(task ChannelMigrationTask) ChannelMigrationTaskGuard {
	return ChannelMigrationTaskGuard{
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

func channelMigrationRuntimeGuard(meta ChannelRuntimeMeta) ChannelMigrationRuntimeGuard {
	meta = normalizeChannelRuntimeMeta(meta)
	return ChannelMigrationRuntimeGuard{
		ChannelID:               meta.ChannelID,
		ChannelType:             meta.ChannelType,
		ExpectedChannelEpoch:    meta.ChannelEpoch,
		ExpectedLeaderEpoch:     meta.LeaderEpoch,
		ExpectedLeader:          meta.Leader,
		ExpectedFenceToken:      meta.WriteFenceToken,
		ExpectedFenceVersion:    meta.WriteFenceVersion,
		ExpectedRouteGeneration: meta.RouteGeneration,
	}
}
