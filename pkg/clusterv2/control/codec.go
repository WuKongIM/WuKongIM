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
	controlKindTaskRequest
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

// TaskAction selects which task write the remote Controller should apply.
type TaskAction string

const (
	// TaskActionComplete submits a global task completion.
	TaskActionComplete TaskAction = "complete"
	// TaskActionFail submits a global task failure.
	TaskActionFail TaskAction = "fail"
	// TaskActionProgress submits one participant progress report.
	TaskActionProgress TaskAction = "progress"
)

// TaskRequest carries one ControllerV2 task result or progress write.
type TaskRequest struct {
	// Action selects which payload should be applied.
	Action TaskAction `json:"action"`
	// Result carries complete/fail task result payloads.
	Result cv2.TaskResult `json:"result,omitempty"`
	// Progress carries participant progress payloads.
	Progress cv2.TaskProgress `json:"progress,omitempty"`
}

// EncodeTaskRequest encodes one task write request.
func EncodeTaskRequest(req TaskRequest) ([]byte, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	out := clusternet.PutHeader(nil, controlRPCVersion, controlKindTaskRequest)
	return append(out, payload...), nil
}

// DecodeTaskRequest decodes one task write request.
func DecodeTaskRequest(data []byte) (TaskRequest, error) {
	payload, err := clusternet.CheckHeader(data, controlRPCVersion, controlKindTaskRequest)
	if err != nil {
		return TaskRequest{}, err
	}
	var req TaskRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return TaskRequest{}, err
	}
	return req, nil
}
