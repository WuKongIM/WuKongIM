package control

import (
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

func TestControlCodecRejectsWrongKind(t *testing.T) {
	frame := []byte{controlRPCVersion, controlKindRaftBatch + 99}
	if _, err := DecodeRaftBatch(frame); err == nil {
		t.Fatal("DecodeRaftBatch() error = nil, want invalid frame")
	}
}
