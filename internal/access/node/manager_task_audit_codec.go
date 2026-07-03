package node

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
)

var (
	managerTaskAuditRequestMagic  = [...]byte{'W', 'K', 'V', 'U', 1}
	managerTaskAuditResponseMagic = [...]byte{'W', 'K', 'V', 'u', 1}
)

const (
	managerTaskAuditOpList   = "list"
	managerTaskAuditOpEvents = "events"
)

type managerTaskAuditRPCRequest struct {
	Op     string                                           `json:"op"`
	List   managementusecase.ControllerTaskAuditListRequest `json:"list,omitempty"`
	TaskID string                                           `json:"task_id,omitempty"`
}

type managerTaskAuditRPCResponse struct {
	Status string                                              `json:"status"`
	List   managementusecase.ControllerTaskAuditListResponse   `json:"list,omitempty"`
	Events managementusecase.ControllerTaskAuditEventsResponse `json:"events,omitempty"`
}

func encodeManagerTaskAuditRequest(req managerTaskAuditRPCRequest) ([]byte, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	dst := make([]byte, 0, len(managerTaskAuditRequestMagic)+len(payload))
	dst = append(dst, managerTaskAuditRequestMagic[:]...)
	return append(dst, payload...), nil
}

func decodeManagerTaskAuditRequest(body []byte) (managerTaskAuditRPCRequest, error) {
	if !hasMagic(body, managerTaskAuditRequestMagic[:]) {
		return managerTaskAuditRPCRequest{}, fmt.Errorf("internalv2/access/node: invalid manager task audit request codec")
	}
	var req managerTaskAuditRPCRequest
	if err := decodeManagerTaskAuditJSON(body[len(managerTaskAuditRequestMagic):], &req); err != nil {
		return managerTaskAuditRPCRequest{}, err
	}
	return req, nil
}

func encodeManagerTaskAuditResponse(resp managerTaskAuditRPCResponse) ([]byte, error) {
	payload, err := json.Marshal(resp)
	if err != nil {
		return nil, err
	}
	dst := make([]byte, 0, len(managerTaskAuditResponseMagic)+len(payload))
	dst = append(dst, managerTaskAuditResponseMagic[:]...)
	return append(dst, payload...), nil
}

func decodeManagerTaskAuditResponse(body []byte) (managerTaskAuditRPCResponse, error) {
	if !hasMagic(body, managerTaskAuditResponseMagic[:]) {
		return managerTaskAuditRPCResponse{}, fmt.Errorf("internalv2/access/node: invalid manager task audit response codec")
	}
	var resp managerTaskAuditRPCResponse
	if err := decodeManagerTaskAuditJSON(body[len(managerTaskAuditResponseMagic):], &resp); err != nil {
		return managerTaskAuditRPCResponse{}, err
	}
	return resp, nil
}

func decodeManagerTaskAuditJSON(payload []byte, dst any) error {
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	var extra struct{}
	if err := dec.Decode(&extra); err != io.EOF {
		return fmt.Errorf("internalv2/access/node: trailing manager task audit json")
	}
	return nil
}
