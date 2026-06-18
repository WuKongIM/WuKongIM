package node

import (
	"encoding/json"
	"fmt"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
)

var (
	managerControllerRaftRequestMagic  = [...]byte{'W', 'K', 'V', 'R', 1}
	managerControllerRaftResponseMagic = [...]byte{'W', 'K', 'V', 'r', 1}
)

const (
	managerControllerRaftOpStatus  = "status"
	managerControllerRaftOpCompact = "compact"
)

type managerControllerRaftRPCRequest struct {
	Op     string `json:"op"`
	NodeID uint64 `json:"node_id"`
}

type managerControllerRaftRPCResponse struct {
	Status     string                                           `json:"status"`
	RaftStatus managementusecase.ControllerRaftStatus           `json:"raft_status"`
	Compaction managementusecase.ControllerRaftCompactionResult `json:"compaction"`
}

func encodeManagerControllerRaftRequest(req managerControllerRaftRPCRequest) ([]byte, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	dst := make([]byte, 0, len(managerControllerRaftRequestMagic)+len(payload))
	dst = append(dst, managerControllerRaftRequestMagic[:]...)
	return append(dst, payload...), nil
}

func decodeManagerControllerRaftRequest(body []byte) (managerControllerRaftRPCRequest, error) {
	if !hasMagic(body, managerControllerRaftRequestMagic[:]) {
		return managerControllerRaftRPCRequest{}, fmt.Errorf("internalv2/access/node: invalid manager controller raft request codec")
	}
	var req managerControllerRaftRPCRequest
	if err := json.Unmarshal(body[len(managerControllerRaftRequestMagic):], &req); err != nil {
		return managerControllerRaftRPCRequest{}, err
	}
	return req, nil
}

func encodeManagerControllerRaftResponse(resp managerControllerRaftRPCResponse) ([]byte, error) {
	payload, err := json.Marshal(resp)
	if err != nil {
		return nil, err
	}
	dst := make([]byte, 0, len(managerControllerRaftResponseMagic)+len(payload))
	dst = append(dst, managerControllerRaftResponseMagic[:]...)
	return append(dst, payload...), nil
}

func decodeManagerControllerRaftResponse(body []byte) (managerControllerRaftRPCResponse, error) {
	if !hasMagic(body, managerControllerRaftResponseMagic[:]) {
		return managerControllerRaftRPCResponse{}, fmt.Errorf("internalv2/access/node: invalid manager controller raft response codec")
	}
	var resp managerControllerRaftRPCResponse
	if err := json.Unmarshal(body[len(managerControllerRaftResponseMagic):], &resp); err != nil {
		return managerControllerRaftRPCResponse{}, err
	}
	return resp, nil
}
