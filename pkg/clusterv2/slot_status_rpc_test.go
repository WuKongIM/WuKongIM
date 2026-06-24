package clusterv2

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/routing"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

func TestSlotStatusRPCCodecRoundTrip(t *testing.T) {
	req, err := encodeSlotStatusRequest([]uint32{1, 2})
	if err != nil {
		t.Fatalf("encodeSlotStatusRequest() error = %v", err)
	}
	slotIDs, err := decodeSlotStatusRequest(req)
	if err != nil {
		t.Fatalf("decodeSlotStatusRequest() error = %v", err)
	}
	if len(slotIDs) != 2 || slotIDs[0] != 1 || slotIDs[1] != 2 {
		t.Fatalf("slotIDs = %#v, want [1 2]", slotIDs)
	}

	resp, err := encodeSlotStatusResponse([]routing.SlotStatus{{SlotID: 1, Leader: 2, LeaderTerm: 9}})
	if err != nil {
		t.Fatalf("encodeSlotStatusResponse() error = %v", err)
	}
	statuses, err := decodeSlotStatusResponse(resp)
	if err != nil {
		t.Fatalf("decodeSlotStatusResponse() error = %v", err)
	}
	if len(statuses) != 1 || statuses[0].SlotID != 1 || statuses[0].Leader != 2 || statuses[0].LeaderTerm != 9 {
		t.Fatalf("statuses = %#v, want slot 1 leader 2 term 9", statuses)
	}
}

func TestSlotStatusHandlerReturnsOnlyObservedLeaders(t *testing.T) {
	handler := slotStatusHandler{runtime: fakeSlotStatusRuntime{
		statuses: map[multiraft.SlotID]multiraft.Status{
			1: {SlotID: 1, LeaderID: 2, Term: 9},
			2: {SlotID: 2},
		},
	}}
	req, err := encodeSlotStatusRequest([]uint32{1, 2})
	if err != nil {
		t.Fatalf("encodeSlotStatusRequest() error = %v", err)
	}
	resp, err := handler.HandleRPC(context.Background(), req)
	if err != nil {
		t.Fatalf("HandleRPC() error = %v", err)
	}
	statuses, err := decodeSlotStatusResponse(resp)
	if err != nil {
		t.Fatalf("decodeSlotStatusResponse() error = %v", err)
	}
	if len(statuses) != 1 || statuses[0].SlotID != 1 || statuses[0].Leader != 2 || statuses[0].LeaderTerm != 9 {
		t.Fatalf("statuses = %#v, want only slot 1 leader 2 term 9", statuses)
	}
}

type fakeSlotStatusRuntime struct {
	statuses map[multiraft.SlotID]multiraft.Status
}

func (f fakeSlotStatusRuntime) Status(slotID multiraft.SlotID) (multiraft.Status, error) {
	return f.statuses[slotID], nil
}
