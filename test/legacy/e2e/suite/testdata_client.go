//go:build e2e && legacy_e2e

package suite

import (
	"context"
	"encoding/json"
)

// SlotSnapshotUsersRequest describes one e2e Slot snapshot seed dataset request.
type SlotSnapshotUsersRequest struct {
	Prefix       string `json:"prefix"`
	Count        int    `json:"count"`
	PayloadBytes int    `json:"payload_bytes"`
	Seed         string `json:"seed,omitempty"`
	DeviceFlag   uint8  `json:"device_flag,omitempty"`
	DeviceLevel  uint8  `json:"device_level,omitempty"`
}

// SlotSnapshotUsersResponse describes generated Slot snapshot seed data.
type SlotSnapshotUsersResponse struct {
	Dataset      string `json:"dataset"`
	Prefix       string `json:"prefix"`
	Count        int    `json:"count"`
	PayloadBytes int    `json:"payload_bytes"`
	FirstUID     string `json:"first_uid"`
	LastUID      string `json:"last_uid"`
}

// ControllerSnapshotJobsRequest describes one e2e Controller snapshot seed dataset request.
type ControllerSnapshotJobsRequest struct {
	Prefix       string `json:"prefix"`
	TargetNodeID uint64 `json:"target_node_id"`
	Count        int    `json:"count"`
	PayloadBytes int    `json:"payload_bytes"`
	Seed         string `json:"seed,omitempty"`
}

// ControllerSnapshotJobsResponse describes generated Controller snapshot seed data.
type ControllerSnapshotJobsResponse struct {
	Dataset      string `json:"dataset"`
	Prefix       string `json:"prefix"`
	TargetNodeID uint64 `json:"target_node_id"`
	Count        int    `json:"count"`
	PayloadBytes int    `json:"payload_bytes"`
	FirstJobID   string `json:"first_job_id"`
	LastJobID    string `json:"last_job_id"`
}

// GenerateSlotSnapshotUsers calls the e2e test-data API on a started node.
func GenerateSlotSnapshotUsers(ctx context.Context, node StartedNode, req SlotSnapshotUsersRequest) (SlotSnapshotUsersResponse, []byte, error) {
	body, err := postHTTPJSONBody(ctx, node.Spec.APIAddr, "/testdata/e2e/cluster/slot-snapshot-users", req)
	if err != nil {
		return SlotSnapshotUsersResponse{}, nil, err
	}
	resp, err := decodeSlotSnapshotUsersResponse(body)
	if err != nil {
		return SlotSnapshotUsersResponse{}, body, err
	}
	return resp, body, nil
}

func decodeSlotSnapshotUsersResponse(body []byte) (SlotSnapshotUsersResponse, error) {
	var resp SlotSnapshotUsersResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return SlotSnapshotUsersResponse{}, err
	}
	return resp, nil
}

// GenerateControllerSnapshotJobs calls the e2e Controller snapshot test-data API on a started node.
func GenerateControllerSnapshotJobs(ctx context.Context, node StartedNode, req ControllerSnapshotJobsRequest) (ControllerSnapshotJobsResponse, []byte, error) {
	body, err := postHTTPJSONBody(ctx, node.Spec.APIAddr, "/testdata/e2e/cluster/controller-snapshot-jobs", req)
	if err != nil {
		return ControllerSnapshotJobsResponse{}, nil, err
	}
	resp, err := decodeControllerSnapshotJobsResponse(body)
	if err != nil {
		return ControllerSnapshotJobsResponse{}, body, err
	}
	return resp, body, nil
}

func decodeControllerSnapshotJobsResponse(body []byte) (ControllerSnapshotJobsResponse, error) {
	var resp ControllerSnapshotJobsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return ControllerSnapshotJobsResponse{}, err
	}
	return resp, nil
}
