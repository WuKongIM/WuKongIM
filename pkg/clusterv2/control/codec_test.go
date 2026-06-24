package control

import (
	"errors"
	"reflect"
	"testing"

	cv2 "github.com/WuKongIM/WuKongIM/pkg/controllerv2"
	"go.etcd.io/raft/v3/raftpb"
)

func TestControlRaftBatchCodecRoundTrip(t *testing.T) {
	input := []raftpb.Message{{From: 1, To: 2, Type: raftpb.MsgHeartbeat, Term: 3}}
	payload, err := EncodeRaftBatch(input)
	if err != nil {
		t.Fatalf("EncodeRaftBatch() error = %v", err)
	}
	got, err := DecodeRaftBatch(payload)
	if err != nil {
		t.Fatalf("DecodeRaftBatch() error = %v", err)
	}
	if len(got) != 1 || got[0].From != 1 || got[0].To != 2 || got[0].Type != raftpb.MsgHeartbeat || got[0].Term != 3 {
		t.Fatalf("DecodeRaftBatch() = %#v", got)
	}
}

func TestControlSyncCodecRoundTrip(t *testing.T) {
	req := cv2.GetStateRequest{ClusterID: "cluster-a", LocalRevision: 7, LocalChecksum: "crc32c:abcd"}
	payload, err := EncodeStateSyncRequest(req)
	if err != nil {
		t.Fatalf("EncodeStateSyncRequest() error = %v", err)
	}
	got, err := DecodeStateSyncRequest(payload)
	if err != nil {
		t.Fatalf("DecodeStateSyncRequest() error = %v", err)
	}
	if got != req {
		t.Fatalf("DecodeStateSyncRequest() = %#v, want %#v", got, req)
	}

	resp := cv2.GetStateResponse{LeaderID: 1, Revision: 8, Checksum: "crc32c:1234", Payload: []byte(`{"revision":8}`)}
	encoded, err := EncodeStateSyncResponse(resp)
	if err != nil {
		t.Fatalf("EncodeStateSyncResponse() error = %v", err)
	}
	decoded, err := DecodeStateSyncResponse(encoded)
	if err != nil {
		t.Fatalf("DecodeStateSyncResponse() error = %v", err)
	}
	if decoded.LeaderID != resp.LeaderID || decoded.Revision != resp.Revision || decoded.Checksum != resp.Checksum || string(decoded.Payload) != string(resp.Payload) {
		t.Fatalf("DecodeStateSyncResponse() = %#v, want %#v", decoded, resp)
	}
}

func TestControlTaskRequestCodecRoundTrip(t *testing.T) {
	requests := []TaskRequest{
		{
			Action: TaskActionProgress,
			Progress: cv2.TaskProgress{
				TaskID:             "slot-1-bootstrap-1",
				SlotID:             1,
				TaskKind:           cv2.TaskKindBootstrap,
				ConfigEpoch:        1,
				TaskAttempt:        0,
				ParticipantNodeID:  2,
				ParticipantAttempt: 0,
				Status:             cv2.TaskParticipantStatusDone,
			},
		},
		{
			Action: TaskActionLeaderTransfer,
			LeaderTransfer: SlotLeaderTransferRequest{
				SlotID:        1,
				SourceNode:    1,
				TargetNode:    2,
				TargetPeers:   []uint64{1, 2, 3},
				ConfigEpoch:   7,
				StateRevision: 9,
			},
		},
	}
	for _, req := range requests {
		payload, err := EncodeTaskRequest(req)
		if err != nil {
			t.Fatalf("EncodeTaskRequest() error = %v", err)
		}
		got, err := DecodeTaskRequest(payload)
		if err != nil {
			t.Fatalf("DecodeTaskRequest() error = %v", err)
		}
		if !reflect.DeepEqual(got, req) {
			t.Fatalf("DecodeTaskRequest() = %#v, want %#v", got, req)
		}
	}
}

func TestControlWriteRequestNodeLifecycleCodecRoundTrip(t *testing.T) {
	req := ControlWriteRequest{
		Action: ControlWriteActionJoinNode,
		JoinNode: JoinNodeRequest{
			NodeID:         4,
			Name:           "node-4",
			Addr:           "127.0.0.1:10004",
			Roles:          []Role{RoleData},
			CapacityWeight: 2,
		},
	}
	payload, err := EncodeControlWriteRequest(req)
	if err != nil {
		t.Fatalf("EncodeControlWriteRequest() error = %v", err)
	}
	got, err := DecodeControlWriteRequest(payload)
	if err != nil {
		t.Fatalf("DecodeControlWriteRequest() error = %v", err)
	}
	if !reflect.DeepEqual(got, req) {
		t.Fatalf("DecodeControlWriteRequest() = %#v, want %#v", got, req)
	}
}

func TestControlWriteResponsePreservesSemanticErrorIdentity(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want error
	}{
		{name: "not leader", err: cv2.ErrNotLeader, want: cv2.ErrNotLeader},
		{name: "not started", err: cv2.ErrNotStarted, want: cv2.ErrNotStarted},
		{name: "proposal rejected", err: cv2.ErrProposalRejected, want: cv2.ErrProposalRejected},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload, err := encodeControlWriteErrorResponse(tt.err)
			if err != nil {
				t.Fatalf("encodeControlWriteErrorResponse() error = %v", err)
			}
			_, err = DecodeControlWriteResponse(payload)
			if !errors.Is(err, tt.want) {
				t.Fatalf("DecodeControlWriteResponse() error = %v, want errors.Is(%v)", err, tt.want)
			}
		})
	}
}

func TestControlCodecRejectsWrongKind(t *testing.T) {
	frame := []byte{controlRPCVersion, controlKindRaftBatch + 99}
	if _, err := DecodeRaftBatch(frame); err == nil {
		t.Fatal("DecodeRaftBatch() error = nil, want invalid frame")
	}
}
