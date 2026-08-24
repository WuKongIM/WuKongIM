package fsm

import (
	"bytes"
	"context"
	"reflect"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	runtimechannelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

func TestCreateChannelRuntimeMetaBatchAdmitsPersonDirectoryTask(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm, err := NewStateMachineWithHashSlots(db, 11, []uint16{99, 100})
	if err != nil {
		t.Fatalf("NewStateMachineWithHashSlots(): %v", err)
	}

	personChannelID := runtimechannelid.EncodePersonChannel("u1", "u2")
	command, err := EncodeCreateChannelRuntimeMetaBatchCommandChecked([]CreateChannelRuntimeMetaBatchItem{
		{HashSlot: 99, Meta: metadb.ChannelRuntimeMeta{
			ChannelID: personChannelID, ChannelType: 1, ChannelEpoch: 1, LeaderEpoch: 1,
			Replicas: []uint64{1, 2, 3}, ISR: []uint64{1, 2, 3}, Leader: 1, MinISR: 2, Status: 2,
		}},
		{HashSlot: 100, Meta: metadb.ChannelRuntimeMeta{
			ChannelID: "group-1", ChannelType: 2, ChannelEpoch: 1, LeaderEpoch: 1,
			Replicas: []uint64{1, 2, 3}, ISR: []uint64{1, 2, 3}, Leader: 1, MinISR: 2, Status: 2,
		}},
	})
	if err != nil {
		t.Fatalf("EncodeCreateChannelRuntimeMetaBatchCommandChecked(): %v", err)
	}
	if _, err := sm.Apply(ctx, multiraft.Command{SlotID: 11, HashSlot: 99, Index: 1, Term: 1, Data: command}); err != nil {
		t.Fatalf("Apply(batch create): %v", err)
	}

	task, ok, err := db.ForHashSlot(99).GetPersonDirectoryTask(ctx, personChannelID, 1)
	if err != nil || !ok {
		t.Fatalf("GetPersonDirectoryTask() = (%+v, %v, %v), want durable task", task, ok, err)
	}
	if task.CommittedTail != 0 || task.CreatedAt != 0 || task.Generation != 1 {
		t.Fatalf("person directory task = %+v, want initial tail/time and generation 1", task)
	}
	channel, err := db.ForHashSlot(99).GetChannel(ctx, personChannelID, 1)
	if err != nil {
		t.Fatalf("GetChannel(): %v", err)
	}
	if channel.DirectoryProjectionState != metadb.DirectoryProjectionPending || channel.DirectoryProjectionGeneration != task.Generation {
		t.Fatalf("person channel directory state = (%v, %d), want pending generation %d", channel.DirectoryProjectionState, channel.DirectoryProjectionGeneration, task.Generation)
	}

	groupTasks, _, _, err := db.ForHashSlot(100).ListPersonDirectoryTaskPage(ctx, metadb.PersonDirectoryTaskCursor{}, 10)
	if err != nil {
		t.Fatalf("ListPersonDirectoryTaskPage(group slot): %v", err)
	}
	if len(groupTasks) != 0 {
		t.Fatalf("group runtime created person directory tasks: %+v", groupTasks)
	}
}

func TestCreateChannelRuntimeMetaBatchCommandCanonicalizesAndApplies(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm, err := NewStateMachineWithHashSlots(db, 11, []uint16{99, 100})
	if err != nil {
		t.Fatalf("NewStateMachineWithHashSlots(): %v", err)
	}

	first := CreateChannelRuntimeMetaBatchItem{
		HashSlot: 100,
		Meta: metadb.ChannelRuntimeMeta{
			ChannelID: "runtime-b", ChannelType: 2, ChannelEpoch: 1, LeaderEpoch: 1,
			Replicas: []uint64{3, 1, 2}, ISR: []uint64{2, 1}, Leader: 1, MinISR: 2,
			Status: 2, LeaseUntilMS: 1700000000000,
		},
	}
	second := CreateChannelRuntimeMetaBatchItem{
		HashSlot: 99,
		Meta: metadb.ChannelRuntimeMeta{
			ChannelID: "runtime-a", ChannelType: 1, ChannelEpoch: 1, LeaderEpoch: 1,
			Replicas: []uint64{2, 1, 3}, ISR: []uint64{1, 2}, Leader: 1, MinISR: 2,
			Status: 2, LeaseUntilMS: 1700000000000,
		},
	}
	command, err := EncodeCreateChannelRuntimeMetaBatchCommandChecked([]CreateChannelRuntimeMetaBatchItem{first, second})
	if err != nil {
		t.Fatalf("EncodeCreateChannelRuntimeMetaBatchCommandChecked(): %v", err)
	}
	reordered, err := EncodeCreateChannelRuntimeMetaBatchCommandChecked([]CreateChannelRuntimeMetaBatchItem{second, first})
	if err != nil {
		t.Fatalf("EncodeCreateChannelRuntimeMetaBatchCommandChecked(reordered): %v", err)
	}
	if !bytes.Equal(command, reordered) {
		t.Fatal("equivalent runtime-meta batches encoded different proposal bytes")
	}

	encodedResult, err := sm.Apply(ctx, multiraft.Command{
		SlotID: 11, HashSlot: 99, Index: 1, Term: 1, Data: command,
	})
	if err != nil {
		t.Fatalf("Apply(batch create): %v", err)
	}
	results, err := DecodeCreateChannelRuntimeMetaBatchResult(encodedResult)
	if err != nil {
		t.Fatalf("DecodeCreateChannelRuntimeMetaBatchResult(): %v", err)
	}
	wantResults := []CreateChannelRuntimeMetaBatchResult{
		{HashSlot: 99, ChannelID: "runtime-a", ChannelType: 1, Created: true},
		{HashSlot: 100, ChannelID: "runtime-b", ChannelType: 2, Created: true},
	}
	if !reflect.DeepEqual(results, wantResults) {
		t.Fatalf("batch results = %#v, want %#v", results, wantResults)
	}
	for _, item := range []CreateChannelRuntimeMetaBatchItem{second, first} {
		got, err := db.ForHashSlot(item.HashSlot).GetChannelRuntimeMeta(ctx, item.Meta.ChannelID, item.Meta.ChannelType)
		if err != nil {
			t.Fatalf("GetChannelRuntimeMeta(%s): %v", item.Meta.ChannelID, err)
		}
		if want := metadb.NormalizeChannelRuntimeMeta(item.Meta); !reflect.DeepEqual(got, want) {
			t.Fatalf("runtime meta %s = %#v, want %#v", item.Meta.ChannelID, got, want)
		}
	}
}

func TestCreateChannelRuntimeMetaCommandReportsCreatedAndPreservesOriginal(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm, err := NewStateMachineWithHashSlots(db, 11, []uint16{99})
	if err != nil {
		t.Fatalf("NewStateMachineWithHashSlots(): %v", err)
	}

	original := metadb.ChannelRuntimeMeta{
		ChannelID: "runtime-create", ChannelType: 2, ChannelEpoch: 3, LeaderEpoch: 2,
		Replicas: []uint64{3, 1, 2}, ISR: []uint64{2, 1}, Leader: 1, MinISR: 2,
		Status: 2, Features: 1, LeaseUntilMS: 1700000000000,
	}
	createCommand, err := EncodeCreateChannelRuntimeMetaBatchCommandChecked([]CreateChannelRuntimeMetaBatchItem{{HashSlot: 99, Meta: original}})
	if err != nil {
		t.Fatalf("EncodeCreateChannelRuntimeMetaBatchCommandChecked(): %v", err)
	}
	result, err := sm.Apply(ctx, multiraft.Command{
		SlotID: 11, HashSlot: 99, Index: 1, Term: 1,
		Data: createCommand,
	})
	if err != nil {
		t.Fatalf("Apply(first create): %v", err)
	}
	decoded, err := DecodeCreateChannelRuntimeMetaBatchResult(result)
	if err != nil || len(decoded) != 1 || !decoded[0].Created {
		t.Fatalf("first create result = %+v, err=%v; want created", decoded, err)
	}

	result, err = sm.Apply(ctx, multiraft.Command{
		SlotID: 11, HashSlot: 99, Index: 2, Term: 1,
		Data: createCommand,
	})
	if err != nil {
		t.Fatalf("Apply(second create): %v", err)
	}
	decoded, err = DecodeCreateChannelRuntimeMetaBatchResult(result)
	if err != nil || len(decoded) != 1 || decoded[0].Created {
		t.Fatalf("second create result = %+v, err=%v; want already existing", decoded, err)
	}

	replacement := original
	replacement.ChannelEpoch++
	replacement.LeaderEpoch++
	replacement.Leader = 2
	result, err = sm.Apply(ctx, multiraft.Command{
		SlotID: 11, HashSlot: 99, Index: 3, Term: 1,
		Data: mustEncodeCreateChannelRuntimeMetaBatch(t, 99, replacement),
	})
	if err != nil {
		t.Fatalf("Apply(replacement create): %v", err)
	}
	decoded, err = DecodeCreateChannelRuntimeMetaBatchResult(result)
	if err != nil || len(decoded) != 1 || decoded[0].Created {
		t.Fatalf("replacement create result = %+v, err=%v; want already existing", decoded, err)
	}

	got, err := db.ForHashSlot(99).GetChannelRuntimeMeta(ctx, original.ChannelID, original.ChannelType)
	if err != nil {
		t.Fatalf("GetChannelRuntimeMeta(): %v", err)
	}
	want := metadb.NormalizeChannelRuntimeMeta(original)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("runtime meta after duplicate = %#v, want original %#v", got, want)
	}
}

func TestCommandInspectionIncludesCreateChannelRuntimeMeta(t *testing.T) {
	command := mustEncodeCreateChannelRuntimeMetaBatch(t, 99, metadb.ChannelRuntimeMeta{
		ChannelID: "runtime-inspect", ChannelType: 2, ChannelEpoch: 3, LeaderEpoch: 2,
		Replicas: []uint64{2, 1}, ISR: []uint64{1, 2}, Leader: 1, MinISR: 2,
		Status: 2, Features: 1, LeaseUntilMS: 1700000000000,
	})
	inspection, err := DecodeCommandInspection(command)
	if err != nil {
		t.Fatalf("DecodeCommandInspection(): %v", err)
	}
	if inspection.Type != "create_channel_runtime_meta_batch" {
		t.Fatalf("inspection type = %q, want create_channel_runtime_meta_batch", inspection.Type)
	}
	items, ok := inspection.Payload["items"].([]map[string]any)
	if !ok || len(items) != 1 {
		t.Fatalf("inspection items = %#v, want one item", inspection.Payload["items"])
	}
	if got := items[0]["channel_id"]; got != "runtime-inspect" {
		t.Fatalf("inspection channel_id = %#v, want runtime-inspect", got)
	}
}

func mustEncodeCreateChannelRuntimeMetaBatch(t *testing.T, hashSlot uint16, meta metadb.ChannelRuntimeMeta) []byte {
	t.Helper()
	command, err := EncodeCreateChannelRuntimeMetaBatchCommandChecked([]CreateChannelRuntimeMetaBatchItem{{HashSlot: hashSlot, Meta: meta}})
	if err != nil {
		t.Fatalf("EncodeCreateChannelRuntimeMetaBatchCommandChecked(): %v", err)
	}
	return command
}
