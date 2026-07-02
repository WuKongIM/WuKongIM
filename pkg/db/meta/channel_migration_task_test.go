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

func TestChannelMigrationTaskListActiveUsesActiveIndexLimit(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	shard := store.db.HashSlot(8)
	ctx := context.Background()
	activeA := testChannelMigrationTask("task-active-a", "channel-active-a")
	activeB := testChannelMigrationTask("task-active-b", "channel-active-b")
	activeC := testChannelMigrationTask("task-active-c", "channel-active-c")
	terminal := testChannelMigrationTask("task-terminal", "channel-active-terminal")

	for _, task := range []ChannelMigrationTask{activeA, terminal, activeB, activeC} {
		if err := shard.CreateChannelMigrationTask(ctx, task); err != nil {
			t.Fatalf("CreateChannelMigrationTask(%s): %v", task.TaskID, err)
		}
	}
	if err := shard.AdvanceChannelMigrationTask(ctx, ChannelMigrationTaskAdvance{
		Guard:         channelMigrationTaskGuard(terminal),
		Status:        ChannelMigrationStatusCompleted,
		Phase:         ChannelMigrationPhaseClearFence,
		UpdatedAtMS:   terminal.UpdatedAtMS + 1,
		CompletedAtMS: terminal.UpdatedAtMS + 2,
	}); err != nil {
		t.Fatalf("AdvanceChannelMigrationTask(terminal): %v", err)
	}

	list, err := shard.ListActiveChannelMigrationTasks(ctx, 2)
	if err != nil {
		t.Fatalf("ListActiveChannelMigrationTasks(): %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("active list len = %d, want 2: %+v", len(list), list)
	}
	if list[0].TaskID != activeA.TaskID || list[1].TaskID != activeB.TaskID {
		t.Fatalf("active list = %+v, want first two active tasks", list)
	}
	for _, task := range list {
		if !task.IsActive() {
			t.Fatalf("active list contains terminal task: %+v", task)
		}
	}

	empty, err := shard.ListActiveChannelMigrationTasks(ctx, 0)
	if err != nil {
		t.Fatalf("ListActiveChannelMigrationTasks(limit=0): %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("limit=0 active list = %+v, want empty", empty)
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
		NowMS:             1750000001000,
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

func TestChannelMigrationTaskClaimRequiresSameOwnerOrExpiredLease(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	shard := store.db.HashSlot(8)
	ctx := context.Background()
	task := testChannelMigrationTask("task-lease", "channel-lease")
	if err := shard.CreateChannelMigrationTask(ctx, task); err != nil {
		t.Fatalf("CreateChannelMigrationTask(): %v", err)
	}

	firstClaim := ChannelMigrationTaskClaim{
		Guard:             channelMigrationTaskGuard(task),
		Status:            ChannelMigrationStatusRunning,
		Phase:             ChannelMigrationPhaseValidate,
		OwnerNodeID:       1,
		OwnerLeaseUntilMS: 1750000005000,
		NowMS:             1750000001000,
		UpdatedAtMS:       1750000001000,
	}
	if err := shard.ClaimChannelMigrationTask(ctx, firstClaim); err != nil {
		t.Fatalf("ClaimChannelMigrationTask(first): %v", err)
	}
	claimed, ok, err := shard.GetChannelMigrationTask(ctx, task.ChannelID, task.ChannelType, task.TaskID)
	if err != nil || !ok {
		t.Fatalf("GetChannelMigrationTask(claimed) ok=%v err=%v", ok, err)
	}

	steal := ChannelMigrationTaskClaim{
		Guard:             channelMigrationTaskGuard(claimed),
		Status:            ChannelMigrationStatusRunning,
		Phase:             claimed.Phase,
		OwnerNodeID:       2,
		OwnerLeaseUntilMS: 1750000006000,
		NowMS:             1750000002000,
		UpdatedAtMS:       1750000002000,
	}
	if err := shard.ClaimChannelMigrationTask(ctx, steal); !errors.Is(err, dberrors.ErrConflict) {
		t.Fatalf("ClaimChannelMigrationTask(steal) err = %v, want conflict", err)
	}

	renew := steal
	renew.OwnerNodeID = 1
	renew.OwnerLeaseUntilMS = 1750000007000
	if err := shard.ClaimChannelMigrationTask(ctx, renew); err != nil {
		t.Fatalf("ClaimChannelMigrationTask(renew): %v", err)
	}
	renewed, ok, err := shard.GetChannelMigrationTask(ctx, task.ChannelID, task.ChannelType, task.TaskID)
	if err != nil || !ok {
		t.Fatalf("GetChannelMigrationTask(renewed) ok=%v err=%v", ok, err)
	}

	takeover := ChannelMigrationTaskClaim{
		Guard:             channelMigrationTaskGuard(renewed),
		Status:            ChannelMigrationStatusRunning,
		Phase:             renewed.Phase,
		OwnerNodeID:       2,
		OwnerLeaseUntilMS: 1750000009000,
		NowMS:             renewed.OwnerLeaseUntilMS,
		UpdatedAtMS:       renewed.UpdatedAtMS + 1,
	}
	if err := shard.ClaimChannelMigrationTask(ctx, takeover); err != nil {
		t.Fatalf("ClaimChannelMigrationTask(expired takeover): %v", err)
	}
	taken, ok, err := shard.GetChannelMigrationTask(ctx, task.ChannelID, task.ChannelType, task.TaskID)
	if err != nil || !ok {
		t.Fatalf("GetChannelMigrationTask(taken) ok=%v err=%v", ok, err)
	}
	if taken.OwnerNodeID != 2 {
		t.Fatalf("taken owner = %d, want 2", taken.OwnerNodeID)
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
