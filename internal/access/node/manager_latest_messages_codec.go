package node

import (
	"encoding/json"
	"fmt"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
)

var (
	managerLatestMessagesRequestMagic  = [...]byte{'W', 'K', 'L', 'M', 1}
	managerLatestMessagesResponseMagic = [...]byte{'W', 'K', 'L', 'm', 1}
)

type managerLatestMessagesRPCRequest struct {
	BeforeMessageID uint64 `json:"before_message_id,omitempty"`
	Limit           int    `json:"limit"`
}

type managerLatestMessagesRPCResponse struct {
	Status string                                 `json:"status"`
	Page   managementusecase.ListMessagesResponse `json:"page"`
}

func encodeManagerLatestMessagesRequest(req managerLatestMessagesRPCRequest) ([]byte, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	return append(append([]byte(nil), managerLatestMessagesRequestMagic[:]...), payload...), nil
}

func decodeManagerLatestMessagesRequest(body []byte) (managerLatestMessagesRPCRequest, error) {
	if !hasMagic(body, managerLatestMessagesRequestMagic[:]) {
		return managerLatestMessagesRPCRequest{}, fmt.Errorf("internal/access/node: invalid manager latest messages request codec")
	}
	var req managerLatestMessagesRPCRequest
	if err := json.Unmarshal(body[len(managerLatestMessagesRequestMagic):], &req); err != nil {
		return managerLatestMessagesRPCRequest{}, err
	}
	return req, nil
}

func encodeManagerLatestMessagesResponse(resp managerLatestMessagesRPCResponse) ([]byte, error) {
	payload, err := json.Marshal(resp)
	if err != nil {
		return nil, err
	}
	return append(append([]byte(nil), managerLatestMessagesResponseMagic[:]...), payload...), nil
}

func decodeManagerLatestMessagesResponse(body []byte) (managerLatestMessagesRPCResponse, error) {
	if !hasMagic(body, managerLatestMessagesResponseMagic[:]) {
		return managerLatestMessagesRPCResponse{}, fmt.Errorf("internal/access/node: invalid manager latest messages response codec")
	}
	var resp managerLatestMessagesRPCResponse
	if err := json.Unmarshal(body[len(managerLatestMessagesResponseMagic):], &resp); err != nil {
		return managerLatestMessagesRPCResponse{}, err
	}
	return resp, nil
}
