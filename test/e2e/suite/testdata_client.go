//go:build e2e

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
