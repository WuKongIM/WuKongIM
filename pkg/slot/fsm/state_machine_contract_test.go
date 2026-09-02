package fsm

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	runtimechannelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

func TestFSMBatchOwnershipFailureIsAtomicAndRerouteable(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm, err := NewStateMachineWithHashSlots(db, 11, []uint16{5})
	if err != nil {
		t.Fatalf("NewStateMachineWithHashSlots(): %v", err)
	}
	batchSM := sm.(multiraft.BatchStateMachine)
	durableSM := sm.(multiraft.DurableAppliedStateMachine)

	channelID := runtimechannelid.EncodePersonChannel("u5", "u7")
	membershipCommand, err := EncodeEnsureUserChannelMembershipBatchCommandChecked([]UserChannelMembershipBatchItem{
		{HashSlot: 5, Membership: metadb.UserChannelMembership{UID: "u5", ChannelID: channelID, ChannelType: 1, JoinSeq: 10, SourceVersion: 1, UpdatedAt: 100}},
		{HashSlot: 7, Membership: metadb.UserChannelMembership{UID: "u7", ChannelID: channelID, ChannelType: 1, JoinSeq: 10, SourceVersion: 1, UpdatedAt: 100}},
	})
	if err != nil {
		t.Fatalf("EncodeEnsureUserChannelMembershipBatchCommandChecked(): %v", err)
	}
	commands := []multiraft.Command{
		{SlotID: 11, HashSlot: 5, Index: 1, Term: 1, Data: EncodeUpsertUserCommand(metadb.User{UID: "staged-before-foreign-slot", Token: "must-rollback"})},
		{SlotID: 11, HashSlot: 5, Index: 2, Term: 1, Data: membershipCommand},
	}

	if _, err := batchSM.ApplyBatch(ctx, commands); !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("ApplyBatch(foreign member hash slot) error = %v, want ErrInvalidArgument", err)
	}
	if _, err := db.ForHashSlot(5).GetUser(ctx, "staged-before-foreign-slot"); !errors.Is(err, metadb.ErrNotFound) {
		t.Fatalf("GetUser(after rejected batch) error = %v, want ErrNotFound", err)
	}
	for _, uidAndSlot := range []struct {
		uid      string
		hashSlot uint16
	}{{"u5", 5}, {"u7", 7}} {
		if _, err := db.ForHashSlot(uidAndSlot.hashSlot).GetUserChannelMembership(ctx, uidAndSlot.uid, channelID, 1); !errors.Is(err, metadb.ErrNotFound) {
			t.Fatalf("GetUserChannelMembership(%s, after rejected batch) error = %v, want ErrNotFound", uidAndSlot.uid, err)
		}
	}
	if got, err := durableSM.DurableAppliedIndex(ctx); err != nil || got != 0 {
		t.Fatalf("DurableAppliedIndex(after rejected batch) = %d, %v; want 0, nil", got, err)
	}

	raw := sm.(*stateMachine)
	raw.UpdateOwnedHashSlots([]uint16{7, 5, 7})
	results, err := batchSM.ApplyBatch(ctx, commands)
	if err != nil {
		t.Fatalf("ApplyBatch(after ownership update): %v", err)
	}
	if len(results) != 2 || string(results[0]) != ApplyResultOK || string(results[1]) != ApplyResultOK {
		t.Fatalf("ApplyBatch(after ownership update) results = %q, want [OK OK]", results)
	}
	if got, err := durableSM.DurableAppliedIndex(ctx); err != nil || got != 2 {
		t.Fatalf("DurableAppliedIndex(after commit) = %d, %v; want 2, nil", got, err)
	}
	for _, uidAndSlot := range []struct {
		uid      string
		hashSlot uint16
	}{{"u5", 5}, {"u7", 7}} {
		got, err := db.ForHashSlot(uidAndSlot.hashSlot).GetUserChannelMembership(ctx, uidAndSlot.uid, channelID, 1)
		if err != nil {
			t.Fatalf("GetUserChannelMembership(%s, after commit): %v", uidAndSlot.uid, err)
		}
		if got.SourceVersion != 1 || got.JoinSeq != 10 {
			t.Fatalf("membership %s = %+v, want committed projection fence", uidAndSlot.uid, got)
		}
	}

	raw.UpdateOwnedHashSlots([]uint16{7})
	if _, err := sm.Apply(ctx, multiraft.Command{
		SlotID: 11, HashSlot: 5, Index: 3, Term: 1,
		Data: EncodeUpsertUserCommand(metadb.User{UID: "stale-owner", Token: "must-reject"}),
	}); !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("Apply(after ownership removal) error = %v, want ErrInvalidArgument", err)
	}
	if got, err := durableSM.DurableAppliedIndex(ctx); err != nil || got != 2 {
		t.Fatalf("DurableAppliedIndex(after rejected reroute) = %d, %v; want 2, nil", got, err)
	}
}

func TestFSMBoundedMultiHashCommandsAdmitCapsAndRejectOverflow(t *testing.T) {
	t.Run("runtime metadata", testFSMRuntimeMetadataBatchBound)
	t.Run("person membership", testFSMPersonMembershipBatchBound)
}

func testFSMRuntimeMetadataBatchBound(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm, err := NewStateMachineWithHashSlots(db, 11, []uint16{5, 7})
	if err != nil {
		t.Fatalf("NewStateMachineWithHashSlots(): %v", err)
	}
	durableSM := sm.(multiraft.DurableAppliedStateMachine)

	runtimeItems := make([]CreateChannelRuntimeMetaBatchItem, MaxCreateChannelRuntimeMetaBatchItems)
	for i := range runtimeItems {
		runtimeItems[i] = CreateChannelRuntimeMetaBatchItem{
			HashSlot: []uint16{5, 7}[i%2],
			Meta:     fsmContractRuntimeMeta(fmt.Sprintf("bounded-runtime-%03d", i), 2),
		}
	}
	runtimeCommand, err := EncodeCreateChannelRuntimeMetaBatchCommandChecked(runtimeItems)
	if err != nil {
		t.Fatalf("EncodeCreateChannelRuntimeMetaBatchCommandChecked(at cap): %v", err)
	}
	encodedResults, err := sm.Apply(ctx, multiraft.Command{
		SlotID: 11, HashSlot: 5, Index: 1, Term: 1, Data: runtimeCommand,
	})
	if err != nil {
		t.Fatalf("Apply(runtime metadata at cap): %v", err)
	}
	runtimeResults, err := DecodeCreateChannelRuntimeMetaBatchResult(encodedResults)
	if err != nil {
		t.Fatalf("DecodeCreateChannelRuntimeMetaBatchResult(): %v", err)
	}
	if len(runtimeResults) != MaxCreateChannelRuntimeMetaBatchItems {
		t.Fatalf("runtime result count = %d, want %d", len(runtimeResults), MaxCreateChannelRuntimeMetaBatchItems)
	}
	for i, result := range runtimeResults {
		if !result.Created {
			t.Fatalf("runtime result[%d] = %+v, want Created", i, result)
		}
	}
	for _, index := range []int{0, len(runtimeItems) - 1} {
		item := runtimeItems[index]
		if _, err := db.ForHashSlot(item.HashSlot).GetChannelRuntimeMeta(ctx, item.Meta.ChannelID, item.Meta.ChannelType); err != nil {
			t.Fatalf("GetChannelRuntimeMeta(%s, at cap): %v", item.Meta.ChannelID, err)
		}
	}

	overflowRuntime := CreateChannelRuntimeMetaBatchItem{HashSlot: 5, Meta: fsmContractRuntimeMeta("bounded-runtime-overflow", 2)}
	if _, err := EncodeCreateChannelRuntimeMetaBatchCommandChecked(append(append([]CreateChannelRuntimeMetaBatchItem(nil), runtimeItems...), overflowRuntime)); !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("EncodeCreateChannelRuntimeMetaBatchCommandChecked(over cap) error = %v, want ErrInvalidArgument", err)
	}
	forgedRuntimeCommand := append([]byte(nil), runtimeCommand...)
	forgedRuntimeEntry := make([]byte, 2)
	binary.BigEndian.PutUint16(forgedRuntimeEntry, overflowRuntime.HashSlot)
	forgedRuntimeEntry = append(forgedRuntimeEntry, EncodeUpsertChannelRuntimeMetaCommand(overflowRuntime.Meta)...)
	forgedRuntimeCommand = appendBytesTLVField(forgedRuntimeCommand, tagCreateChannelRuntimeMetaBatchEntry, forgedRuntimeEntry)
	if _, err := sm.Apply(ctx, multiraft.Command{SlotID: 11, HashSlot: 5, Index: 2, Term: 1, Data: forgedRuntimeCommand}); !errors.Is(err, metadb.ErrCorruptValue) {
		t.Fatalf("Apply(forged runtime batch over cap) error = %v, want ErrCorruptValue", err)
	}
	if _, err := db.ForHashSlot(overflowRuntime.HashSlot).GetChannelRuntimeMeta(ctx, overflowRuntime.Meta.ChannelID, overflowRuntime.Meta.ChannelType); !errors.Is(err, metadb.ErrNotFound) {
		t.Fatalf("GetChannelRuntimeMeta(overflow) error = %v, want ErrNotFound", err)
	}
	if got, err := durableSM.DurableAppliedIndex(ctx); err != nil || got != 1 {
		t.Fatalf("DurableAppliedIndex(after rejected runtime overflow) = %d, %v; want 1, nil", got, err)
	}
}

func testFSMPersonMembershipBatchBound(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm, err := NewStateMachineWithHashSlots(db, 11, []uint16{5, 7})
	if err != nil {
		t.Fatalf("NewStateMachineWithHashSlots(): %v", err)
	}
	durableSM := sm.(multiraft.DurableAppliedStateMachine)

	membershipItems := make([]UserChannelMembershipBatchItem, MaxPersonDirectoryBatchItems)
	for i := range membershipItems {
		uid := fmt.Sprintf("bounded-member-%03d", i)
		membershipItems[i] = UserChannelMembershipBatchItem{
			HashSlot: []uint16{5, 7}[i%2],
			Membership: metadb.UserChannelMembership{
				UID: uid, ChannelID: runtimechannelid.EncodePersonChannel(uid, "bounded-peer"), ChannelType: 1,
				JoinSeq: uint64(i + 1), SourceVersion: 1, UpdatedAt: int64(i + 1),
			},
		}
	}
	membershipCommand, err := EncodeEnsureUserChannelMembershipBatchCommandChecked(membershipItems)
	if err != nil {
		t.Fatalf("EncodeEnsureUserChannelMembershipBatchCommandChecked(at cap): %v", err)
	}
	if result, err := sm.Apply(ctx, multiraft.Command{SlotID: 11, HashSlot: 5, Index: 1, Term: 1, Data: membershipCommand}); err != nil || string(result) != ApplyResultOK {
		t.Fatalf("Apply(membership batch at cap) = %q, %v; want OK, nil", result, err)
	}
	for _, index := range []int{0, len(membershipItems) - 1} {
		item := membershipItems[index]
		if _, err := db.ForHashSlot(item.HashSlot).GetUserChannelMembership(ctx, item.Membership.UID, item.Membership.ChannelID, item.Membership.ChannelType); err != nil {
			t.Fatalf("GetUserChannelMembership(%s, at cap): %v", item.Membership.UID, err)
		}
	}

	overflowUID := "bounded-member-overflow"
	overflowMembership := UserChannelMembershipBatchItem{
		HashSlot: 5,
		Membership: metadb.UserChannelMembership{
			UID: overflowUID, ChannelID: runtimechannelid.EncodePersonChannel(overflowUID, "bounded-peer"), ChannelType: 1,
			JoinSeq: 129, SourceVersion: 1, UpdatedAt: 129,
		},
	}
	if _, err := EncodeEnsureUserChannelMembershipBatchCommandChecked(append(append([]UserChannelMembershipBatchItem(nil), membershipItems...), overflowMembership)); !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("EncodeEnsureUserChannelMembershipBatchCommandChecked(over cap) error = %v, want ErrInvalidArgument", err)
	}
	forgedMembershipCommand := append([]byte(nil), membershipCommand...)
	forgedMembershipEntry := make([]byte, 2)
	binary.BigEndian.PutUint16(forgedMembershipEntry, overflowMembership.HashSlot)
	forgedMembershipEntry = append(forgedMembershipEntry, encodeUserChannelMembershipEntry(overflowMembership.Membership, true)...)
	forgedMembershipCommand = appendBytesTLVField(forgedMembershipCommand, tagPersonDirectoryTaskBatchEntry, forgedMembershipEntry)
	if _, err := sm.Apply(ctx, multiraft.Command{SlotID: 11, HashSlot: 5, Index: 2, Term: 1, Data: forgedMembershipCommand}); !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("Apply(forged membership batch over cap) error = %v, want ErrInvalidArgument", err)
	}
	if _, err := db.ForHashSlot(overflowMembership.HashSlot).GetUserChannelMembership(ctx, overflowUID, overflowMembership.Membership.ChannelID, 1); !errors.Is(err, metadb.ErrNotFound) {
		t.Fatalf("GetUserChannelMembership(overflow) error = %v, want ErrNotFound", err)
	}
	if got, err := durableSM.DurableAppliedIndex(ctx); err != nil || got != 1 {
		t.Fatalf("DurableAppliedIndex(after rejected membership overflow) = %d, %v; want 1, nil", got, err)
	}
}

func TestFSMHashSlotFenceIsDurableAndMonotonicAcrossRestart(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm, err := NewStateMachineWithHashSlots(db, 11, []uint16{5})
	if err != nil {
		t.Fatalf("NewStateMachineWithHashSlots(): %v", err)
	}
	raw := sm.(*stateMachine)
	raw.UpdateOutgoingDeltaTargets(map[uint16]multiraft.SlotID{5: 22})

	type forwardedDelta struct {
		target multiraft.SlotID
		index  uint64
	}
	var forwarded []forwardedDelta
	raw.SetDeltaForwarder(func(_ context.Context, target multiraft.SlotID, command multiraft.Command) error {
		forwarded = append(forwarded, forwardedDelta{target: target, index: command.Index})
		return nil
	})

	apply := func(index uint64, data []byte) []byte {
		t.Helper()
		result, applyErr := sm.Apply(ctx, multiraft.Command{
			SlotID: 11, HashSlot: 5, Index: index, Term: 1, Data: data,
		})
		if applyErr != nil {
			t.Fatalf("Apply(index=%d): %v", index, applyErr)
		}
		return result
	}
	if result := apply(100, EncodeUpsertUserCommand(metadb.User{UID: "before-fence", Token: "committed"})); string(result) != ApplyResultOK {
		t.Fatalf("Apply(before fence) result = %q, want %q", result, ApplyResultOK)
	}
	if result := apply(101, EncodeEnterFenceCommand(5)); string(result) != ApplyResultOK {
		t.Fatalf("Apply(enter fence) result = %q, want %q", result, ApplyResultOK)
	}
	if result := apply(102, EncodeEnterFenceCommand(5)); string(result) != ApplyResultOK {
		t.Fatalf("Apply(duplicate enter fence) result = %q, want %q", result, ApplyResultOK)
	}

	state, err := db.LoadHashSlotMigrationState(ctx, 5)
	if err != nil {
		t.Fatalf("LoadHashSlotMigrationState(): %v", err)
	}
	if state.SourceSlot != 11 || state.TargetSlot != 22 || state.FenceIndex != 101 || state.LastOutboxIndex != 101 {
		t.Fatalf("migration state after duplicate fence = %+v, want immutable fence index 101", state)
	}
	rows, err := db.ListHashSlotMigrationOutbox(ctx, 5, 11, 22, 0, 10)
	if err != nil {
		t.Fatalf("ListHashSlotMigrationOutbox(): %v", err)
	}
	if len(rows) != 2 || rows[0].SourceIndex != 100 || rows[1].SourceIndex != 101 {
		t.Fatalf("migration outbox indexes = %+v, want [100 101] without duplicate fence row", rows)
	}
	if len(forwarded) != 2 || forwarded[0] != (forwardedDelta{target: 22, index: 100}) || forwarded[1] != (forwardedDelta{target: 22, index: 101}) {
		t.Fatalf("forwarded deltas = %+v, want committed indexes 100 and 101 once", forwarded)
	}

	restarted, err := NewStateMachineWithHashSlots(db, 11, []uint16{5})
	if err != nil {
		t.Fatalf("NewStateMachineWithHashSlots(restart): %v", err)
	}
	result, err := restarted.Apply(ctx, multiraft.Command{
		SlotID: 11, HashSlot: 5, Index: 103, Term: 1,
		Data: EncodeUpsertUserCommand(metadb.User{UID: "after-restart-fence", Token: "must-reject"}),
	})
	if err != nil {
		t.Fatalf("Apply(after restart fence): %v", err)
	}
	if string(result) != ApplyResultHashSlotFenced {
		t.Fatalf("Apply(after restart fence) result = %q, want %q", result, ApplyResultHashSlotFenced)
	}
	if _, err := db.ForHashSlot(5).GetUser(ctx, "after-restart-fence"); !errors.Is(err, metadb.ErrNotFound) {
		t.Fatalf("GetUser(after restart fence) error = %v, want ErrNotFound", err)
	}
	stateAfterRestart, err := db.LoadHashSlotMigrationState(ctx, 5)
	if err != nil {
		t.Fatalf("LoadHashSlotMigrationState(after restart): %v", err)
	}
	if stateAfterRestart.FenceIndex != 101 || stateAfterRestart.LastOutboxIndex != 101 {
		t.Fatalf("migration state after restart rejection = %+v, want fence index 101 unchanged", stateAfterRestart)
	}
}

func TestFSMChannelWriteFenceAdvancesMonotonicallyWithinAtomicBatch(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm := mustNewStateMachine(t, db, 11)
	task := fsmTestChannelMigrationTask("contract-set-clear-fence", "contract-set-clear-channel")
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

	applyFSMContractOK(t, ctx, sm, 1, EncodeUpsertChannelRuntimeMetaCommand(meta))
	applyFSMContractOK(t, ctx, sm, 2, EncodeCreateChannelMigrationTaskCommand(task))
	baselineRouteGeneration := metadb.NormalizeChannelRuntimeMeta(meta).RouteGeneration

	setRequest := fsmTestSetFenceRequest(task, meta, 1750000009000, 1750000002000)
	setRequest.Phase = task.Phase
	fencedTask := task
	fencedTask.Status = setRequest.Status
	fencedTask.Phase = setRequest.Phase
	fencedTask.FenceToken = task.TaskID
	fencedTask.FenceVersion = 8
	fencedTask.FenceUntilMS = setRequest.FenceUntilMS
	fencedTask.UpdatedAtMS = setRequest.UpdatedAtMS
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
	fencedMeta.WriteFenceReason = setRequest.FenceReason
	fencedMeta.WriteFenceUntilMS = setRequest.FenceUntilMS
	fencedMeta.RouteGeneration = baselineRouteGeneration + 1
	clearRequest := fsmTestClearFenceRequest(fencedTask, fencedMeta, 1750000003000)

	results, err := sm.(multiraft.BatchStateMachine).ApplyBatch(ctx, []multiraft.Command{
		{SlotID: 11, Index: 3, Term: 1, Data: EncodeSetChannelWriteFenceCommand(setRequest)},
		{SlotID: 11, Index: 4, Term: 1, Data: EncodeClearChannelWriteFenceCommand(clearRequest)},
	})
	if err != nil {
		t.Fatalf("ApplyBatch(set then clear fence): %v", err)
	}
	if len(results) != 2 || string(results[0]) != ApplyResultOK || string(results[1]) != ApplyResultOK {
		t.Fatalf("ApplyBatch(set then clear fence) results = %q, want [OK OK]", results)
	}

	gotTask, err := db.ForSlot(11).GetChannelMigrationTask(ctx, task.ChannelID, task.ChannelType, task.TaskID)
	if err != nil {
		t.Fatalf("GetChannelMigrationTask(): %v", err)
	}
	if gotTask.Status != metadb.ChannelMigrationStatusCompleted || gotTask.Phase != metadb.ChannelMigrationPhaseClearFence || gotTask.FenceToken != "" || gotTask.FenceVersion != 0 {
		t.Fatalf("task after atomic set/clear = %+v, want completed task with transient fence fields cleared", gotTask)
	}
	gotMeta, err := db.ForSlot(11).GetChannelRuntimeMeta(ctx, meta.ChannelID, meta.ChannelType)
	if err != nil {
		t.Fatalf("GetChannelRuntimeMeta(): %v", err)
	}
	if gotMeta.WriteFenceToken != "" || gotMeta.WriteFenceVersion != 9 || gotMeta.WriteFenceUntilMS != 0 {
		t.Fatalf("runtime metadata after atomic set/clear = %+v, want cleared fence version 9", gotMeta)
	}
	if gotMeta.RouteGeneration != baselineRouteGeneration+2 {
		t.Fatalf("route generation after atomic set/clear = %d, want %d", gotMeta.RouteGeneration, baselineRouteGeneration+2)
	}

	result, err := sm.Apply(ctx, multiraft.Command{
		SlotID: 11, Index: 5, Term: 1, Data: EncodeSetChannelWriteFenceCommand(setRequest),
	})
	if err != nil {
		t.Fatalf("Apply(stale set fence replay): %v", err)
	}
	if string(result) != ApplyResultStaleMeta {
		t.Fatalf("Apply(stale set fence replay) result = %q, want %q", result, ApplyResultStaleMeta)
	}
	metaAfterReplay, err := db.ForSlot(11).GetChannelRuntimeMeta(ctx, meta.ChannelID, meta.ChannelType)
	if err != nil {
		t.Fatalf("GetChannelRuntimeMeta(after stale replay): %v", err)
	}
	if metaAfterReplay.WriteFenceVersion != 9 || metaAfterReplay.WriteFenceToken != "" || metaAfterReplay.RouteGeneration != baselineRouteGeneration+2 {
		t.Fatalf("runtime metadata after stale fence replay = %+v, want version and route generation unchanged", metaAfterReplay)
	}
	if got, err := sm.(multiraft.DurableAppliedStateMachine).DurableAppliedIndex(ctx); err != nil || got != 5 {
		t.Fatalf("DurableAppliedIndex(after stale replay) = %d, %v; want 5, nil", got, err)
	}

	restarted := mustNewStateMachine(t, db, 11)
	if got, err := restarted.(multiraft.DurableAppliedStateMachine).DurableAppliedIndex(ctx); err != nil || got != 5 {
		t.Fatalf("DurableAppliedIndex(after restart) = %d, %v; want 5, nil", got, err)
	}
	replayedResult, err := restarted.Apply(ctx, multiraft.Command{
		SlotID: 11, Index: 5, Term: 1, Data: EncodeSetChannelWriteFenceCommand(setRequest),
	})
	if err != nil || string(replayedResult) != ApplyResultStaleMeta {
		t.Fatalf("Apply(repeated stale entry after restart) = %q, %v; want stale_meta, nil", replayedResult, err)
	}
	if got, err := restarted.(multiraft.DurableAppliedStateMachine).DurableAppliedIndex(ctx); err != nil || got != 5 {
		t.Fatalf("DurableAppliedIndex(after repeated stale entry) = %d, %v; want 5, nil", got, err)
	}

	fallbackResults, err := restarted.(multiraft.BatchStateMachine).ApplyBatch(ctx, []multiraft.Command{
		{SlotID: 11, Index: 6, Term: 1, Data: EncodeUpsertUserCommand(metadb.User{UID: "before-stale-fallback", Token: "committed"})},
		{SlotID: 11, Index: 7, Term: 1, Data: EncodeSetChannelWriteFenceCommand(setRequest)},
	})
	if err != nil {
		t.Fatalf("ApplyBatch(valid then commit-time stale fallback): %v", err)
	}
	if len(fallbackResults) != 2 || string(fallbackResults[0]) != ApplyResultOK || string(fallbackResults[1]) != ApplyResultStaleMeta {
		t.Fatalf("stale fallback results = %q, want [OK stale_meta]", fallbackResults)
	}
	if _, err := db.ForSlot(11).GetUser(ctx, "before-stale-fallback"); err != nil {
		t.Fatalf("GetUser(valid command before stale fallback): %v", err)
	}
	if got, err := restarted.(multiraft.DurableAppliedStateMachine).DurableAppliedIndex(ctx); err != nil || got != 7 {
		t.Fatalf("DurableAppliedIndex(after multi-command stale fallback) = %d, %v; want 7, nil", got, err)
	}
	metaAfterFallback, err := db.ForSlot(11).GetChannelRuntimeMeta(ctx, meta.ChannelID, meta.ChannelType)
	if err != nil {
		t.Fatalf("GetChannelRuntimeMeta(after multi-command fallback): %v", err)
	}
	if metaAfterFallback.WriteFenceVersion != 9 || metaAfterFallback.WriteFenceToken != "" || metaAfterFallback.RouteGeneration != baselineRouteGeneration+2 {
		t.Fatalf("runtime metadata after multi-command fallback = %+v, want fence marker unchanged", metaAfterFallback)
	}
}

func applyFSMContractOK(t *testing.T, ctx context.Context, sm multiraft.StateMachine, index uint64, data []byte) {
	t.Helper()
	result, err := sm.Apply(ctx, multiraft.Command{SlotID: 11, Index: index, Term: 1, Data: data})
	if err != nil {
		t.Fatalf("Apply(index=%d): %v", index, err)
	}
	if string(result) != ApplyResultOK {
		t.Fatalf("Apply(index=%d) result = %q, want %q", index, result, ApplyResultOK)
	}
}

func fsmContractRuntimeMeta(channelID string, channelType int64) metadb.ChannelRuntimeMeta {
	return metadb.ChannelRuntimeMeta{
		ChannelID: channelID, ChannelType: channelType, ChannelEpoch: 1, LeaderEpoch: 1,
		Replicas: []uint64{1}, ISR: []uint64{1}, Leader: 1, MinISR: 1, Status: 1,
	}
}
