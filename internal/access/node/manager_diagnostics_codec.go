package node

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/internal/observability/diagnostics"
)

var (
	managerDiagnosticsRequestMagicV1  = [...]byte{'W', 'K', 'V', 'D', 'Q', 1}
	managerDiagnosticsRequestMagic    = [...]byte{'W', 'K', 'V', 'D', 'Q', 2}
	managerDiagnosticsResponseMagicV1 = [...]byte{'W', 'K', 'V', 'D', 'R', 1}
	managerDiagnosticsResponseMagic   = [...]byte{'W', 'K', 'V', 'D', 'R', 2}
)

const (
	managerDiagnosticsOpQuery  = "query"
	managerDiagnosticsOpAdd    = "add"
	managerDiagnosticsOpList   = "list"
	managerDiagnosticsOpDelete = "delete"

	managerDiagnosticsOpQueryID byte = iota + 1
	managerDiagnosticsOpAddID
	managerDiagnosticsOpListID
	managerDiagnosticsOpDeleteID
)

type managerDiagnosticsRPCRequest struct {
	Op     string
	Query  diagnostics.Query
	Rule   diagnostics.TrackingRuleInput
	RuleID string
}

type managerDiagnosticsRPCResponse struct {
	Status string
	Error  string
	Result diagnostics.QueryResult
	Rule   diagnostics.TrackingRule
	Rules  []diagnostics.TrackingRule
}

func encodeManagerDiagnosticsRequest(req managerDiagnosticsRPCRequest) ([]byte, error) {
	opID, err := managerDiagnosticsOpID(req.Op)
	if err != nil {
		return nil, err
	}
	dst := make([]byte, 0, len(managerDiagnosticsRequestMagic)+256)
	dst = append(dst, managerDiagnosticsRequestMagic[:]...)
	dst = append(dst, opID)
	dst = appendDiagnosticsQuery(dst, req.Query)
	dst = appendDiagnosticsTrackingRuleInput(dst, req.Rule)
	dst = appendString(dst, req.RuleID)
	return dst, nil
}

func decodeManagerDiagnosticsRequest(body []byte) (managerDiagnosticsRPCRequest, error) {
	if len(body) > maxDiagnosticsBodyBytes {
		return managerDiagnosticsRPCRequest{}, fmt.Errorf("internal/access/node: manager diagnostics request body too large")
	}
	version := managerDiagnosticsRequestCodecVersion(body)
	if version == 0 {
		return managerDiagnosticsRPCRequest{}, fmt.Errorf("internal/access/node: invalid manager diagnostics request codec")
	}
	offset := len(managerDiagnosticsRequestMagic)
	opID, next, err := readByte(body, offset, "manager diagnostics op")
	if err != nil {
		return managerDiagnosticsRPCRequest{}, err
	}
	offset = next
	op, err := managerDiagnosticsOpFromID(opID)
	if err != nil {
		return managerDiagnosticsRPCRequest{}, err
	}
	query, next, err := readDiagnosticsQuery(body, offset, version >= 2)
	if err != nil {
		return managerDiagnosticsRPCRequest{}, err
	}
	offset = next
	rule, next, err := readDiagnosticsTrackingRuleInput(body, offset)
	if err != nil {
		return managerDiagnosticsRPCRequest{}, err
	}
	offset = next
	ruleID, next, err := readDiagnosticsString(body, offset)
	if err != nil {
		return managerDiagnosticsRPCRequest{}, err
	}
	if next != len(body) {
		return managerDiagnosticsRPCRequest{}, fmt.Errorf("internal/access/node: trailing manager diagnostics request bytes")
	}
	return managerDiagnosticsRPCRequest{Op: op, Query: query, Rule: rule, RuleID: ruleID}, nil
}

func encodeManagerDiagnosticsResponse(resp managerDiagnosticsRPCResponse) ([]byte, error) {
	dst := make([]byte, 0, len(managerDiagnosticsResponseMagic)+256+estimateDiagnosticsEventsBinarySize(resp.Result.Events))
	dst = append(dst, managerDiagnosticsResponseMagic[:]...)
	dst = appendString(dst, resp.Status)
	dst = appendString(dst, resp.Error)
	dst = appendDiagnosticsQueryResult(dst, resp.Result)
	dst = appendDiagnosticsTrackingRule(dst, resp.Rule)
	dst = appendDiagnosticsTrackingRules(dst, resp.Rules)
	return dst, nil
}

func decodeManagerDiagnosticsResponse(body []byte) (managerDiagnosticsRPCResponse, error) {
	if len(body) > maxDiagnosticsBodyBytes {
		return managerDiagnosticsRPCResponse{}, fmt.Errorf("internal/access/node: manager diagnostics response body too large")
	}
	version := managerDiagnosticsResponseCodecVersion(body)
	if version == 0 {
		return managerDiagnosticsRPCResponse{}, fmt.Errorf("internal/access/node: invalid manager diagnostics response codec")
	}
	offset := len(managerDiagnosticsResponseMagic)
	var resp managerDiagnosticsRPCResponse
	var err error
	if resp.Status, offset, err = readString(body, offset); err != nil {
		return managerDiagnosticsRPCResponse{}, err
	}
	if resp.Error, offset, err = readString(body, offset); err != nil {
		return managerDiagnosticsRPCResponse{}, err
	}
	eventCodecVersion := 3
	if version >= 2 {
		eventCodecVersion = 4
	}
	if resp.Result, offset, err = readDiagnosticsQueryResult(body, offset, version >= 2, eventCodecVersion); err != nil {
		return managerDiagnosticsRPCResponse{}, err
	}
	if resp.Rule, offset, err = readDiagnosticsTrackingRule(body, offset); err != nil {
		return managerDiagnosticsRPCResponse{}, err
	}
	if resp.Rules, offset, err = readDiagnosticsTrackingRules(body, offset); err != nil {
		return managerDiagnosticsRPCResponse{}, err
	}
	if offset != len(body) {
		return managerDiagnosticsRPCResponse{}, fmt.Errorf("internal/access/node: trailing manager diagnostics response bytes")
	}
	return resp, nil
}

func managerDiagnosticsRequestCodecVersion(body []byte) int {
	switch {
	case hasMagic(body, managerDiagnosticsRequestMagic[:]):
		return 2
	case hasMagic(body, managerDiagnosticsRequestMagicV1[:]):
		return 1
	default:
		return 0
	}
}

func managerDiagnosticsResponseCodecVersion(body []byte) int {
	switch {
	case hasMagic(body, managerDiagnosticsResponseMagic[:]):
		return 2
	case hasMagic(body, managerDiagnosticsResponseMagicV1[:]):
		return 1
	default:
		return 0
	}
}

func managerDiagnosticsOpID(op string) (byte, error) {
	switch op {
	case managerDiagnosticsOpQuery:
		return managerDiagnosticsOpQueryID, nil
	case managerDiagnosticsOpAdd:
		return managerDiagnosticsOpAddID, nil
	case managerDiagnosticsOpList:
		return managerDiagnosticsOpListID, nil
	case managerDiagnosticsOpDelete:
		return managerDiagnosticsOpDeleteID, nil
	default:
		return 0, fmt.Errorf("internal/access/node: unknown manager diagnostics op %q", op)
	}
}

func managerDiagnosticsOpFromID(id byte) (string, error) {
	switch id {
	case managerDiagnosticsOpQueryID:
		return managerDiagnosticsOpQuery, nil
	case managerDiagnosticsOpAddID:
		return managerDiagnosticsOpAdd, nil
	case managerDiagnosticsOpListID:
		return managerDiagnosticsOpList, nil
	case managerDiagnosticsOpDeleteID:
		return managerDiagnosticsOpDelete, nil
	default:
		return "", fmt.Errorf("internal/access/node: unknown manager diagnostics op id %d", id)
	}
}
