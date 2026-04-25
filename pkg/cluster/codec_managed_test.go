package cluster

import (
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

func TestManagedSlotCodecRoundTripRequest(t *testing.T) {
	body, err := encodeManagedSlotRequest(managedSlotRPCRequest{
		Kind:       managedSlotRPCChangeConfig,
		SlotID:     7,
		TargetNode: 8,
		ChangeType: multiraft.PromoteLearner,
		NodeID:     9,
	})
	if err != nil {
		t.Fatalf("encodeManagedSlotRequest() error = %v", err)
	}

	req, err := decodeManagedSlotRequest(body)
	if err != nil {
		t.Fatalf("decodeManagedSlotRequest() error = %v", err)
	}
	if req.Kind != managedSlotRPCChangeConfig || req.SlotID != 7 || req.TargetNode != 8 || req.ChangeType != multiraft.PromoteLearner || req.NodeID != 9 {
		t.Fatalf("managed slot request round trip = %+v", req)
	}
}

func TestManagedSlotCodecRoundTripResponse(t *testing.T) {
	body, err := encodeManagedSlotResponse(managedSlotRPCResponse{
		LeaderID:     3,
		CommitIndex:  11,
		AppliedIndex: 10,
	})
	if err != nil {
		t.Fatalf("encodeManagedSlotResponse() error = %v", err)
	}

	resp, err := decodeManagedSlotResponse(body)
	if err != nil {
		t.Fatalf("decodeManagedSlotResponse() error = %v", err)
	}
	if resp.LeaderID != 3 || resp.CommitIndex != 11 || resp.AppliedIndex != 10 {
		t.Fatalf("managed slot response round trip = %+v", resp)
	}
}

func TestManagedSlotResponseDecodeMapsErrorFlags(t *testing.T) {
	body, err := encodeManagedSlotResponse(managedSlotRPCResponse{NotLeader: true})
	if err != nil {
		t.Fatalf("encodeManagedSlotResponse() error = %v", err)
	}

	_, err = decodeManagedSlotResponse(body)
	if !errors.Is(err, ErrNotLeader) {
		t.Fatalf("decodeManagedSlotResponse() error = %v, want %v", err, ErrNotLeader)
	}
}
