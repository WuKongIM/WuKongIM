package cluster

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
)

func TestUpsertChannelLatestBatchGroupsByPhysicalSlotInStableOrder(t *testing.T) {
	proposer := &recordingProposer{}
	node := newStartedSlotProxyPortNode(t, proposer)
	rows := make([]metadb.ChannelLatest, 0, 4)
	for hashSlot := uint16(0); hashSlot < 4; hashSlot++ {
		channelID := distinctChannelIDsForHashSlot(t, 4, hashSlot, 1)[0]
		rows = append(rows, metadb.ChannelLatest{
			ChannelID: channelID, ChannelType: 2,
			LastMessageID: uint64(100 + hashSlot), LastMessageSeq: uint64(10 + hashSlot),
			UpdatedAt: int64(1000 + hashSlot),
		})
	}
	// Deliberately reverse input so proposal order must come from physical Slot
	// identity rather than map iteration or caller ordering.
	for left, right := 0, len(rows)-1; left < right; left, right = left+1, right-1 {
		rows[left], rows[right] = rows[right], rows[left]
	}
	if err := node.UpsertChannelLatestBatch(context.Background(), rows); err != nil {
		t.Fatalf("UpsertChannelLatestBatch() error = %v", err)
	}
	if len(proposer.requests) != 2 {
		t.Fatalf("proposal count = %d, want one per physical Slot", len(proposer.requests))
	}
	for index, wantSlotID := range []uint32{1, 2} {
		request := proposer.requests[index]
		if !request.Target.HasSlotID || request.Target.SlotID != wantSlotID || !request.Target.HasHashSlot {
			t.Fatalf("proposal[%d] target = %#v, want explicit Slot %d and hash slot", index, request.Target, wantSlotID)
		}
		hashSlots, err := metafsm.DecodeCommandHashSlots(request.Command, request.Target.HashSlot)
		if err != nil {
			t.Fatalf("DecodeCommandHashSlots(proposal=%d) error = %v", index, err)
		}
		wantHashSlots := []uint16{uint16(index * 2), uint16(index*2 + 1)}
		if !reflect.DeepEqual(hashSlots, wantHashSlots) {
			t.Fatalf("proposal[%d] hash slots = %#v, want %#v", index, hashSlots, wantHashSlots)
		}
	}
}

func TestUpsertChannelLatestBatchBoundsOnePhysicalSlotCommand(t *testing.T) {
	proposer := &recordingProposer{}
	node := newStartedSlotProxyPortNode(t, proposer)
	rows := make([]metadb.ChannelLatest, 0, maxChannelLatestBatchItems+1)
	for index := 0; len(rows) < maxChannelLatestBatchItems+1; index++ {
		channelID := fmt.Sprintf("latest-bounded-%d", index)
		if routing.HashSlotForKey(channelID, 4) != 0 {
			continue
		}
		rows = append(rows, metadb.ChannelLatest{
			ChannelID: channelID, ChannelType: 2,
			LastMessageID: uint64(index + 1), LastMessageSeq: uint64(index + 1), UpdatedAt: int64(index + 1),
		})
	}
	if err := node.UpsertChannelLatestBatch(context.Background(), rows); err != nil {
		t.Fatalf("UpsertChannelLatestBatch() error = %v", err)
	}
	if len(proposer.requests) != 2 {
		t.Fatalf("proposal count = %d, want bounded chunks of %d and 1", len(proposer.requests), maxChannelLatestBatchItems)
	}
	for index, request := range proposer.requests {
		if request.Target.SlotID != 1 || request.Target.HashSlot != 0 || !request.Target.HasSlotID || !request.Target.HasHashSlot {
			t.Fatalf("proposal[%d] target = %#v, want exact Slot 1/hash slot 0", index, request.Target)
		}
		hashSlots, err := metafsm.DecodeCommandHashSlots(request.Command, request.Target.HashSlot)
		if err != nil || !reflect.DeepEqual(hashSlots, []uint16{0}) {
			t.Fatalf("proposal[%d] hash slots = %#v err=%v, want [0]", index, hashSlots, err)
		}
	}
}

func TestUpsertChannelLatestBatchRejectsInvalidRowsBeforeProposal(t *testing.T) {
	proposer := &recordingProposer{}
	node := newStartedSlotProxyPortNode(t, proposer)
	if err := node.UpsertChannelLatestBatch(context.Background(), nil); err != nil {
		t.Fatalf("UpsertChannelLatestBatch(empty) error = %v", err)
	}
	if proposer.calls != 0 {
		t.Fatalf("empty batch proposals = %d, want 0", proposer.calls)
	}
	if err := node.UpsertChannelLatestBatch(context.Background(), []metadb.ChannelLatest{{ChannelType: 2}}); !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("UpsertChannelLatestBatch(invalid row) error = %v, want ErrInvalidArgument", err)
	}
	if proposer.calls != 0 {
		t.Fatalf("invalid batch proposals = %d, want fail before proposal", proposer.calls)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := node.UpsertChannelLatestBatch(canceled, []metadb.ChannelLatest{{ChannelID: "room", ChannelType: 2}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("UpsertChannelLatestBatch(canceled) error = %v, want context.Canceled", err)
	}
}
