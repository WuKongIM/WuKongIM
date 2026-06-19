package node

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/internalv2/observability/diagnostics"
)

var (
	managerDiagnosticsRequestMagic  = [...]byte{'W', 'K', 'V', 'D', 'Q', 1}
	managerDiagnosticsResponseMagic = [...]byte{'W', 'K', 'V', 'D', 'R', 1}
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
		return managerDiagnosticsRPCRequest{}, fmt.Errorf("internalv2/access/node: manager diagnostics request body too large")
	}
	if !hasMagic(body, managerDiagnosticsRequestMagic[:]) {
		return managerDiagnosticsRPCRequest{}, fmt.Errorf("internalv2/access/node: invalid manager diagnostics request codec")
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
	query, next, err := readDiagnosticsQuery(body, offset)
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
		return managerDiagnosticsRPCRequest{}, fmt.Errorf("internalv2/access/node: trailing manager diagnostics request bytes")
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
		return managerDiagnosticsRPCResponse{}, fmt.Errorf("internalv2/access/node: manager diagnostics response body too large")
	}
	if !hasMagic(body, managerDiagnosticsResponseMagic[:]) {
		return managerDiagnosticsRPCResponse{}, fmt.Errorf("internalv2/access/node: invalid manager diagnostics response codec")
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
	if resp.Result, offset, err = readDiagnosticsQueryResult(body, offset, 3); err != nil {
		return managerDiagnosticsRPCResponse{}, err
	}
	if resp.Rule, offset, err = readDiagnosticsTrackingRule(body, offset); err != nil {
		return managerDiagnosticsRPCResponse{}, err
	}
	if resp.Rules, offset, err = readDiagnosticsTrackingRules(body, offset); err != nil {
		return managerDiagnosticsRPCResponse{}, err
	}
	if offset != len(body) {
		return managerDiagnosticsRPCResponse{}, fmt.Errorf("internalv2/access/node: trailing manager diagnostics response bytes")
	}
	return resp, nil
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
		return 0, fmt.Errorf("internalv2/access/node: unknown manager diagnostics op %q", op)
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
		return "", fmt.Errorf("internalv2/access/node: unknown manager diagnostics op id %d", id)
	}
}
