package node

import (
	"encoding/json"
	"fmt"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
)

var (
	managerSlotRaftRequestMagic  = [...]byte{'W', 'K', 'V', 'S', 1}
	managerSlotRaftResponseMagic = [...]byte{'W', 'K', 'V', 's', 1}
)

const (
	managerSlotRaftOpStatus  = "status"
	managerSlotRaftOpCompact = "compact"
)

type managerSlotRaftRPCRequest struct {
	Op     string `json:"op"`
	NodeID uint64 `json:"node_id"`
	SlotID uint32 `json:"slot_id"`
}

type managerSlotRaftRPCResponse struct {
	Status     string                                     `json:"status"`
	Raft       managementusecase.SlotNodeLogStatus        `json:"raft"`
	Compaction managementusecase.SlotRaftCompactionResult `json:"compaction"`
}

func encodeManagerSlotRaftRequest(req managerSlotRaftRPCRequest) ([]byte, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	dst := make([]byte, 0, len(managerSlotRaftRequestMagic)+len(payload))
	dst = append(dst, managerSlotRaftRequestMagic[:]...)
	return append(dst, payload...), nil
}

func decodeManagerSlotRaftRequest(body []byte) (managerSlotRaftRPCRequest, error) {
	if !hasMagic(body, managerSlotRaftRequestMagic[:]) {
		return managerSlotRaftRPCRequest{}, fmt.Errorf("internal/access/node: invalid manager slot raft request codec")
	}
	var req managerSlotRaftRPCRequest
	if err := json.Unmarshal(body[len(managerSlotRaftRequestMagic):], &req); err != nil {
		return managerSlotRaftRPCRequest{}, err
	}
	return req, nil
}

func encodeManagerSlotRaftResponse(resp managerSlotRaftRPCResponse) ([]byte, error) {
	payload, err := json.Marshal(resp)
	if err != nil {
		return nil, err
	}
	dst := make([]byte, 0, len(managerSlotRaftResponseMagic)+len(payload))
	dst = append(dst, managerSlotRaftResponseMagic[:]...)
	return append(dst, payload...), nil
}

func decodeManagerSlotRaftResponse(body []byte) (managerSlotRaftRPCResponse, error) {
	if !hasMagic(body, managerSlotRaftResponseMagic[:]) {
		return managerSlotRaftRPCResponse{}, fmt.Errorf("internal/access/node: invalid manager slot raft response codec")
	}
	var resp managerSlotRaftRPCResponse
	if err := json.Unmarshal(body[len(managerSlotRaftResponseMagic):], &resp); err != nil {
		return managerSlotRaftRPCResponse{}, err
	}
	return resp, nil
}
