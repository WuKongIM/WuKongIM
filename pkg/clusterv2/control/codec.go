package control

import (
	"encoding/json"

	clusternet "github.com/WuKongIM/WuKongIM/pkg/clusterv2/net"
	cv2 "github.com/WuKongIM/WuKongIM/pkg/controllerv2"
	"go.etcd.io/raft/v3/raftpb"
)

const (
	controlRPCVersion uint8 = 1

	controlKindRaftBatch uint8 = 1 + iota
	controlKindStateSyncRequest
	controlKindStateSyncResponse
)

// EncodeRaftBatch encodes ControllerV2 Raft messages for clusterv2 RPC.
func EncodeRaftBatch(messages []raftpb.Message) ([]byte, error) {
	payload, err := json.Marshal(messages)
	if err != nil {
		return nil, err
	}
	out := clusternet.PutHeader(nil, controlRPCVersion, controlKindRaftBatch)
	return append(out, payload...), nil
}

// DecodeRaftBatch decodes ControllerV2 Raft messages from clusterv2 RPC.
func DecodeRaftBatch(data []byte) ([]raftpb.Message, error) {
	payload, err := clusternet.CheckHeader(data, controlRPCVersion, controlKindRaftBatch)
	if err != nil {
		return nil, err
	}
	var messages []raftpb.Message
	if err := json.Unmarshal(payload, &messages); err != nil {
		return nil, err
	}
	return messages, nil
}

// EncodeStateSyncRequest encodes a ControllerV2 full-file sync request.
func EncodeStateSyncRequest(req cv2.GetStateRequest) ([]byte, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	out := clusternet.PutHeader(nil, controlRPCVersion, controlKindStateSyncRequest)
	return append(out, payload...), nil
}

// DecodeStateSyncRequest decodes a ControllerV2 full-file sync request.
func DecodeStateSyncRequest(data []byte) (cv2.GetStateRequest, error) {
	payload, err := clusternet.CheckHeader(data, controlRPCVersion, controlKindStateSyncRequest)
	if err != nil {
		return cv2.GetStateRequest{}, err
	}
	var req cv2.GetStateRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return cv2.GetStateRequest{}, err
	}
	return req, nil
}

// EncodeStateSyncResponse encodes a ControllerV2 full-file sync response.
func EncodeStateSyncResponse(resp cv2.GetStateResponse) ([]byte, error) {
	payload, err := json.Marshal(resp)
	if err != nil {
		return nil, err
	}
	out := clusternet.PutHeader(nil, controlRPCVersion, controlKindStateSyncResponse)
	return append(out, payload...), nil
}

// DecodeStateSyncResponse decodes a ControllerV2 full-file sync response.
func DecodeStateSyncResponse(data []byte) (cv2.GetStateResponse, error) {
	payload, err := clusternet.CheckHeader(data, controlRPCVersion, controlKindStateSyncResponse)
	if err != nil {
		return cv2.GetStateResponse{}, err
	}
	var resp cv2.GetStateResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return cv2.GetStateResponse{}, err
	}
	return resp, nil
}
