package node

import (
	"encoding/json"
	"fmt"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
)

var (
	managerDBInspectRequestMagic  = [...]byte{'W', 'K', 'V', 'I', 1}
	managerDBInspectResponseMagic = [...]byte{'W', 'K', 'V', 'i', 1}
)

type managerDBInspectRPCRequest struct {
	NodeID uint64 `json:"node_id"`
	Query  string `json:"query"`
}

type managerDBInspectRPCResponse struct {
	Status string                                   `json:"status"`
	Page   managementusecase.DBInspectQueryResponse `json:"page"`
}

func encodeManagerDBInspectRequest(req managerDBInspectRPCRequest) ([]byte, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	dst := make([]byte, 0, len(managerDBInspectRequestMagic)+len(payload))
	dst = append(dst, managerDBInspectRequestMagic[:]...)
	return append(dst, payload...), nil
}

func decodeManagerDBInspectRequest(body []byte) (managerDBInspectRPCRequest, error) {
	if !hasMagic(body, managerDBInspectRequestMagic[:]) {
		return managerDBInspectRPCRequest{}, fmt.Errorf("internal/access/node: invalid manager db inspect request codec")
	}
	var req managerDBInspectRPCRequest
	if err := json.Unmarshal(body[len(managerDBInspectRequestMagic):], &req); err != nil {
		return managerDBInspectRPCRequest{}, err
	}
	return req, nil
}

func encodeManagerDBInspectResponse(resp managerDBInspectRPCResponse) ([]byte, error) {
	payload, err := json.Marshal(resp)
	if err != nil {
		return nil, err
	}
	dst := make([]byte, 0, len(managerDBInspectResponseMagic)+len(payload))
	dst = append(dst, managerDBInspectResponseMagic[:]...)
	return append(dst, payload...), nil
}

func decodeManagerDBInspectResponse(body []byte) (managerDBInspectRPCResponse, error) {
	if !hasMagic(body, managerDBInspectResponseMagic[:]) {
		return managerDBInspectRPCResponse{}, fmt.Errorf("internal/access/node: invalid manager db inspect response codec")
	}
	var resp managerDBInspectRPCResponse
	if err := json.Unmarshal(body[len(managerDBInspectResponseMagic):], &resp); err != nil {
		return managerDBInspectRPCResponse{}, err
	}
	return resp, nil
}
