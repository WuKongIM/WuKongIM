package node

import (
	"fmt"
	"math"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/observability/diagnostics"
)

const maxDiagnosticsTrackingRules = 256

var (
	diagnosticsTrackingRequestMagic  = [...]byte{'W', 'K', 'D', 'T', 'Q', 1}
	diagnosticsTrackingResponseMagic = [...]byte{'W', 'K', 'D', 'T', 'R', 1}
)

type diagnosticsTrackingOp string

const (
	diagnosticsTrackingOpAdd    diagnosticsTrackingOp = "add"
	diagnosticsTrackingOpList   diagnosticsTrackingOp = "list"
	diagnosticsTrackingOpDelete diagnosticsTrackingOp = "delete"
)

type diagnosticsTrackingRequest struct {
	Op     diagnosticsTrackingOp
	Rule   diagnostics.TrackingRuleInput
	RuleID string
}

type diagnosticsTrackingResponse struct {
	Status string
	Error  string
	Rule   diagnostics.TrackingRule
	Rules  []diagnostics.TrackingRule
}

func encodeDiagnosticsTrackingRequest(req diagnosticsTrackingRequest) ([]byte, error) {
	dst := make([]byte, 0, len(diagnosticsTrackingRequestMagic)+128)
	dst = append(dst, diagnosticsTrackingRequestMagic[:]...)
	dst = appendString(dst, string(req.Op))
	dst = appendDiagnosticsTrackingRuleInput(dst, req.Rule)
	dst = appendString(dst, req.RuleID)
	return dst, nil
}

func decodeDiagnosticsTrackingRequest(body []byte) (diagnosticsTrackingRequest, error) {
	if len(body) > maxDiagnosticsBodyBytes {
		return diagnosticsTrackingRequest{}, fmt.Errorf("internalv2/access/node: diagnostics tracking request body too large")
	}
	if !hasMagic(body, diagnosticsTrackingRequestMagic[:]) {
		return diagnosticsTrackingRequest{}, fmt.Errorf("internalv2/access/node: invalid diagnostics tracking request codec")
	}
	offset := len(diagnosticsTrackingRequestMagic)
	op, next, err := readDiagnosticsString(body, offset)
	if err != nil {
		return diagnosticsTrackingRequest{}, err
	}
	offset = next
	rule, next, err := readDiagnosticsTrackingRuleInput(body, offset)
	if err != nil {
		return diagnosticsTrackingRequest{}, err
	}
	offset = next
	ruleID, next, err := readDiagnosticsString(body, offset)
	if err != nil {
		return diagnosticsTrackingRequest{}, err
	}
	if next != len(body) {
		return diagnosticsTrackingRequest{}, fmt.Errorf("internalv2/access/node: trailing diagnostics tracking request bytes")
	}
	return diagnosticsTrackingRequest{Op: diagnosticsTrackingOp(op), Rule: rule, RuleID: ruleID}, nil
}

func encodeDiagnosticsTrackingResponse(resp diagnosticsTrackingResponse) ([]byte, error) {
	dst := make([]byte, 0, len(diagnosticsTrackingResponseMagic)+128)
	dst = append(dst, diagnosticsTrackingResponseMagic[:]...)
	dst = appendString(dst, resp.Status)
	dst = appendString(dst, resp.Error)
	dst = appendDiagnosticsTrackingRule(dst, resp.Rule)
	dst = appendDiagnosticsTrackingRules(dst, resp.Rules)
	return dst, nil
}

func decodeDiagnosticsTrackingResponse(body []byte) (diagnosticsTrackingResponse, error) {
	if len(body) > maxDiagnosticsBodyBytes {
		return diagnosticsTrackingResponse{}, fmt.Errorf("internalv2/access/node: diagnostics tracking response body too large")
	}
	if !hasMagic(body, diagnosticsTrackingResponseMagic[:]) {
		return diagnosticsTrackingResponse{}, fmt.Errorf("internalv2/access/node: invalid diagnostics tracking response codec")
	}
	offset := len(diagnosticsTrackingResponseMagic)
	var resp diagnosticsTrackingResponse
	var err error
	if resp.Status, offset, err = readDiagnosticsString(body, offset); err != nil {
		return diagnosticsTrackingResponse{}, err
	}
	if resp.Error, offset, err = readDiagnosticsString(body, offset); err != nil {
		return diagnosticsTrackingResponse{}, err
	}
	if resp.Rule, offset, err = readDiagnosticsTrackingRule(body, offset); err != nil {
		return diagnosticsTrackingResponse{}, err
	}
	if resp.Rules, offset, err = readDiagnosticsTrackingRules(body, offset); err != nil {
		return diagnosticsTrackingResponse{}, err
	}
	if offset != len(body) {
		return diagnosticsTrackingResponse{}, fmt.Errorf("internalv2/access/node: trailing diagnostics tracking response bytes")
	}
	return resp, nil
}

func appendDiagnosticsTrackingRuleInput(dst []byte, rule diagnostics.TrackingRuleInput) []byte {
	dst = appendString(dst, rule.ID)
	dst = appendString(dst, string(rule.Target))
	dst = appendString(dst, rule.UID)
	dst = appendString(dst, rule.ChannelKey)
	dst = appendNodeVarint(dst, int64(rule.TTL))
	return appendUvarint(dst, math.Float64bits(rule.SampleRate))
}

func readDiagnosticsTrackingRuleInput(body []byte, offset int) (diagnostics.TrackingRuleInput, int, error) {
	var rule diagnostics.TrackingRuleInput
	var target string
	var ttl int64
	var rateBits uint64
	var err error
	if rule.ID, offset, err = readDiagnosticsString(body, offset); err != nil {
		return diagnostics.TrackingRuleInput{}, offset, err
	}
	if target, offset, err = readDiagnosticsString(body, offset); err != nil {
		return diagnostics.TrackingRuleInput{}, offset, err
	}
	if rule.UID, offset, err = readDiagnosticsString(body, offset); err != nil {
		return diagnostics.TrackingRuleInput{}, offset, err
	}
	if rule.ChannelKey, offset, err = readDiagnosticsString(body, offset); err != nil {
		return diagnostics.TrackingRuleInput{}, offset, err
	}
	if ttl, offset, err = readNodeVarint(body, offset); err != nil {
		return diagnostics.TrackingRuleInput{}, offset, err
	}
	if rateBits, offset, err = readUvarint(body, offset); err != nil {
		return diagnostics.TrackingRuleInput{}, offset, err
	}
	rule.Target = diagnostics.TrackingTarget(target)
	rule.TTL = time.Duration(ttl)
	rule.SampleRate = math.Float64frombits(rateBits)
	return rule, offset, nil
}

func appendDiagnosticsTrackingRule(dst []byte, rule diagnostics.TrackingRule) []byte {
	dst = appendString(dst, rule.ID)
	dst = appendString(dst, string(rule.Target))
	dst = appendString(dst, rule.UID)
	dst = appendString(dst, rule.ChannelKey)
	dst = appendUvarint(dst, math.Float64bits(rule.SampleRate))
	dst = appendDiagnosticsTime(dst, rule.CreatedAt)
	return appendDiagnosticsTime(dst, rule.ExpiresAt)
}

func readDiagnosticsTrackingRule(body []byte, offset int) (diagnostics.TrackingRule, int, error) {
	var rule diagnostics.TrackingRule
	var target string
	var rateBits uint64
	var err error
	if rule.ID, offset, err = readDiagnosticsString(body, offset); err != nil {
		return diagnostics.TrackingRule{}, offset, err
	}
	if target, offset, err = readDiagnosticsString(body, offset); err != nil {
		return diagnostics.TrackingRule{}, offset, err
	}
	if rule.UID, offset, err = readDiagnosticsString(body, offset); err != nil {
		return diagnostics.TrackingRule{}, offset, err
	}
	if rule.ChannelKey, offset, err = readDiagnosticsString(body, offset); err != nil {
		return diagnostics.TrackingRule{}, offset, err
	}
	if rateBits, offset, err = readUvarint(body, offset); err != nil {
		return diagnostics.TrackingRule{}, offset, err
	}
	if rule.CreatedAt, offset, err = readDiagnosticsTime(body, offset); err != nil {
		return diagnostics.TrackingRule{}, offset, err
	}
	if rule.ExpiresAt, offset, err = readDiagnosticsTime(body, offset); err != nil {
		return diagnostics.TrackingRule{}, offset, err
	}
	rule.Target = diagnostics.TrackingTarget(target)
	rule.SampleRate = math.Float64frombits(rateBits)
	return rule, offset, nil
}

func appendDiagnosticsTrackingRules(dst []byte, rules []diagnostics.TrackingRule) []byte {
	dst = appendUvarint(dst, uint64(len(rules)))
	for _, rule := range rules {
		dst = appendDiagnosticsTrackingRule(dst, rule)
	}
	return dst
}

func readDiagnosticsTrackingRules(body []byte, offset int) ([]diagnostics.TrackingRule, int, error) {
	count, next, err := readUvarint(body, offset)
	if err != nil {
		return nil, offset, err
	}
	if count > maxDiagnosticsTrackingRules {
		return nil, offset, fmt.Errorf("internalv2/access/node: diagnostics tracking rules exceeds limit")
	}
	offset = next
	rulesLen, err := readCollectionLen(count, len(body)-offset, "diagnostics tracking rules")
	if err != nil {
		return nil, offset, err
	}
	rules := make([]diagnostics.TrackingRule, rulesLen)
	for i := range rules {
		if rules[i], offset, err = readDiagnosticsTrackingRule(body, offset); err != nil {
			return nil, offset, err
		}
	}
	return rules, offset, nil
}
