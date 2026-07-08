package node

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
)

var (
	managerNodeConfigRequestMagic  = [...]byte{'W', 'K', 'V', 'C', 1}
	managerNodeConfigResponseMagic = [...]byte{'W', 'K', 'V', 'c', 1}
)

type managerNodeConfigRPCRequest struct {
	NodeID uint64 `json:"node_id"`
}

type managerNodeConfigRPCResponse struct {
	Status   string                               `json:"status"`
	Snapshot managementusecase.NodeConfigSnapshot `json:"snapshot"`
}

func encodeManagerNodeConfigRequest(req managerNodeConfigRPCRequest) ([]byte, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	dst := make([]byte, 0, len(managerNodeConfigRequestMagic)+len(payload))
	dst = append(dst, managerNodeConfigRequestMagic[:]...)
	return append(dst, payload...), nil
}

func decodeManagerNodeConfigRequest(body []byte) (managerNodeConfigRPCRequest, error) {
	if !hasMagic(body, managerNodeConfigRequestMagic[:]) {
		return managerNodeConfigRPCRequest{}, fmt.Errorf("internal/access/node: invalid manager node config request codec")
	}
	var req managerNodeConfigRPCRequest
	if err := decodeManagerNodeConfigJSON(body[len(managerNodeConfigRequestMagic):], &req); err != nil {
		return managerNodeConfigRPCRequest{}, err
	}
	return req, nil
}

func encodeManagerNodeConfigResponse(resp managerNodeConfigRPCResponse) ([]byte, error) {
	payload, err := json.Marshal(resp)
	if err != nil {
		return nil, err
	}
	dst := make([]byte, 0, len(managerNodeConfigResponseMagic)+len(payload))
	dst = append(dst, managerNodeConfigResponseMagic[:]...)
	return append(dst, payload...), nil
}

func decodeManagerNodeConfigResponse(body []byte) (managerNodeConfigRPCResponse, error) {
	if !hasMagic(body, managerNodeConfigResponseMagic[:]) {
		return managerNodeConfigRPCResponse{}, fmt.Errorf("internal/access/node: invalid manager node config response codec")
	}
	var resp managerNodeConfigRPCResponse
	if err := decodeManagerNodeConfigJSON(body[len(managerNodeConfigResponseMagic):], &resp); err != nil {
		return managerNodeConfigRPCResponse{}, err
	}
	return resp, nil
}

func decodeManagerNodeConfigJSON(payload []byte, dst any) error {
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	var extra struct{}
	if err := dec.Decode(&extra); err != io.EOF {
		return fmt.Errorf("internal/access/node: trailing manager node config json")
	}
	return nil
}
