package fsm

import (
	"context"
	"reflect"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

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
	createCommand := EncodeCreateChannelRuntimeMetaCommand(original)
	result, err := sm.Apply(ctx, multiraft.Command{
		SlotID: 11, HashSlot: 99, Index: 1, Term: 1,
		Data: createCommand,
	})
	if err != nil {
		t.Fatalf("Apply(first create): %v", err)
	}
	decoded, err := DecodeCreateChannelRuntimeMetaResult(result)
	if err != nil || !decoded.Created {
		t.Fatalf("first create result = %+v, err=%v; want created", decoded, err)
	}

	result, err = sm.Apply(ctx, multiraft.Command{
		SlotID: 11, HashSlot: 99, Index: 2, Term: 1,
		Data: createCommand,
	})
	if err != nil {
		t.Fatalf("Apply(second create): %v", err)
	}
	decoded, err = DecodeCreateChannelRuntimeMetaResult(result)
	if err != nil || decoded.Created {
		t.Fatalf("second create result = %+v, err=%v; want already existing", decoded, err)
	}

	replacement := original
	replacement.ChannelEpoch++
	replacement.LeaderEpoch++
	replacement.Leader = 2
	result, err = sm.Apply(ctx, multiraft.Command{
		SlotID: 11, HashSlot: 99, Index: 3, Term: 1,
		Data: EncodeCreateChannelRuntimeMetaCommand(replacement),
	})
	if err != nil {
		t.Fatalf("Apply(replacement create): %v", err)
	}
	decoded, err = DecodeCreateChannelRuntimeMetaResult(result)
	if err != nil || decoded.Created {
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
	inspection, err := DecodeCommandInspection(EncodeCreateChannelRuntimeMetaCommand(metadb.ChannelRuntimeMeta{
		ChannelID: "runtime-inspect", ChannelType: 2, ChannelEpoch: 3, LeaderEpoch: 2,
		Replicas: []uint64{2, 1}, ISR: []uint64{1, 2}, Leader: 1, MinISR: 2,
		Status: 2, Features: 1, LeaseUntilMS: 1700000000000,
	}))
	if err != nil {
		t.Fatalf("DecodeCommandInspection(): %v", err)
	}
	if inspection.Type != "create_channel_runtime_meta" {
		t.Fatalf("inspection type = %q, want create_channel_runtime_meta", inspection.Type)
	}
	if got := inspection.Payload["channel_id"]; got != "runtime-inspect" {
		t.Fatalf("inspection channel_id = %#v, want runtime-inspect", got)
	}
}
