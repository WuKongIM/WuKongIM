package cluster

import (
	"encoding/binary"
	"errors"
	"reflect"
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

func TestManagedSlotStatusResponseRoundTripCurrentVoters(t *testing.T) {
	body, err := encodeManagedSlotResponse(managedSlotRPCResponse{
		LeaderID:      2,
		CurrentVoters: []uint64{1, 2, 3},
		CommitIndex:   10,
		AppliedIndex:  9,
	})
	if err != nil {
		t.Fatalf("encodeManagedSlotResponse() error = %v", err)
	}

	resp, err := decodeManagedSlotResponse(body)
	if err != nil {
		t.Fatalf("decodeManagedSlotResponse() error = %v", err)
	}
	if got, want := resp.CurrentVoters, []uint64{1, 2, 3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("CurrentVoters = %v, want %v", got, want)
	}
}

func TestManagedSlotStatusResponseDecodeOldPayloadDefaultsCurrentVoters(t *testing.T) {
	body := []byte{managedSlotCodecVersion, 0}
	var fixed [24]byte
	binary.BigEndian.PutUint64(fixed[0:8], 2)
	binary.BigEndian.PutUint64(fixed[8:16], 10)
	binary.BigEndian.PutUint64(fixed[16:24], 9)
	body = append(body, fixed[:]...)
	body = binary.AppendUvarint(body, 0)

	resp, err := decodeManagedSlotResponse(body)
	if err != nil {
		t.Fatalf("decodeManagedSlotResponse() error = %v", err)
	}
	if resp.CurrentVoters != nil {
		t.Fatalf("CurrentVoters = %v, want nil", resp.CurrentVoters)
	}
}

func TestManagedSlotLogsCodecRoundTrip(t *testing.T) {
	reqBody, err := encodeManagedSlotRequest(managedSlotRPCRequest{
		Kind:   managedSlotRPCLogs,
		SlotID: 9,
		Limit:  2,
		Cursor: 5,
	})
	if err != nil {
		t.Fatalf("encodeManagedSlotRequest() error = %v", err)
	}
	req, err := decodeManagedSlotRequest(reqBody)
	if err != nil {
		t.Fatalf("decodeManagedSlotRequest() error = %v", err)
	}
	if req.Kind != managedSlotRPCLogs || req.SlotID != 9 || req.Limit != 2 || req.Cursor != 5 {
		t.Fatalf("managed slot logs request round trip = %+v", req)
	}

	respBody, err := encodeManagedSlotResponse(managedSlotRPCResponse{
		FirstIndex: 1,
		LastIndex:  4,
		NextCursor: 3,
		LogEntries: []managedSlotLogEntry{{
			Index:    4,
			Term:     2,
			Type:     "normal",
			DataSize: 12,
		}},
	})
	if err != nil {
		t.Fatalf("encodeManagedSlotResponse() error = %v", err)
	}
	resp, err := decodeManagedSlotResponse(respBody)
	if err != nil {
		t.Fatalf("decodeManagedSlotResponse() error = %v", err)
	}
	wantEntries := []managedSlotLogEntry{{Index: 4, Term: 2, Type: "normal", DataSize: 12}}
	if resp.FirstIndex != 1 || resp.LastIndex != 4 || resp.NextCursor != 3 || !reflect.DeepEqual(resp.LogEntries, wantEntries) {
		t.Fatalf("managed slot logs response round trip = %+v", resp)
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
