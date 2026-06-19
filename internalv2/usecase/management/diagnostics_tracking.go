package management

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internalv2/observability/diagnostics"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	channelhandler "github.com/WuKongIM/WuKongIM/pkg/channel/handler"
)

// DiagnosticsTrackingStatus summarizes the fanout result for runtime tracking rules.
type DiagnosticsTrackingStatus string

const (
	// DiagnosticsTrackingStatusOK means all targeted nodes completed the operation.
	DiagnosticsTrackingStatusOK DiagnosticsTrackingStatus = "ok"
	// DiagnosticsTrackingStatusPartial means at least one node completed and another did not.
	DiagnosticsTrackingStatusPartial DiagnosticsTrackingStatus = "partial"
	// DiagnosticsTrackingStatusError means no targeted node completed the operation.
	DiagnosticsTrackingStatusError DiagnosticsTrackingStatus = "error"
)

// DiagnosticsTrackingCreateRequest describes a manager request to retain future diagnostics events.
type DiagnosticsTrackingCreateRequest struct {
	// Target selects sender UID or channel tracking.
	Target string
	// UID is required when Target is sender_uid and matches the message sender.
	UID string
	// ChannelID is required when Target is channel.
	ChannelID string
	// ChannelType is required when Target is channel.
	ChannelType uint8
	// TTLSeconds controls how long the rule is retained on each node.
	TTLSeconds int
	// SampleRate is the keep probability for matching events.
	SampleRate float64
}

// DiagnosticsTrackingRule is the manager-facing runtime tracking rule DTO.
type DiagnosticsTrackingRule struct {
	// ID identifies the rule across nodes.
	ID string
	// Target is sender_uid or channel.
	Target string
	// UID is populated for sender UID rules.
	UID string
	// ChannelKey is populated for channel rules.
	ChannelKey string
	// ChannelID is decoded from ChannelKey when available.
	ChannelID string
	// ChannelType is decoded from ChannelKey when available.
	ChannelType uint8
	// SampleRate is the keep probability for matching events.
	SampleRate float64
	// CreatedAt records when a node installed the rule.
	CreatedAt time.Time
	// ExpiresAt records when a node stops applying the rule.
	ExpiresAt time.Time
}

// DiagnosticsTrackingNodeResult reports one node's tracking operation outcome.
type DiagnosticsTrackingNodeResult struct {
	// NodeID identifies the target node.
	NodeID uint64
	// Status is ok, unavailable, or skipped.
	Status string
	// Notes contains node-local caveats or error messages.
	Notes []string
}

// DiagnosticsTrackingMutationResponse describes a create fanout result.
type DiagnosticsTrackingMutationResponse struct {
	Status DiagnosticsTrackingStatus
	Rule   DiagnosticsTrackingRule
	Nodes  []DiagnosticsTrackingNodeResult
	Notes  []string
}

// DiagnosticsTrackingListResponse describes a list fanout result.
type DiagnosticsTrackingListResponse struct {
	Status DiagnosticsTrackingStatus
	Rules  []DiagnosticsTrackingRule
	Nodes  []DiagnosticsTrackingNodeResult
	Notes  []string
}

// DiagnosticsTrackingDeleteResponse describes a delete fanout result.
type DiagnosticsTrackingDeleteResponse struct {
	Status DiagnosticsTrackingStatus
	RuleID string
	Nodes  []DiagnosticsTrackingNodeResult
	Notes  []string
}

// CreateDiagnosticsTrackingRule installs one runtime tracking rule on eligible cluster nodes.
func (a *App) CreateDiagnosticsTrackingRule(ctx context.Context, req DiagnosticsTrackingCreateRequest) (DiagnosticsTrackingMutationResponse, error) {
	input, rule, err := a.buildDiagnosticsTrackingInput(req)
	if err != nil {
		return DiagnosticsTrackingMutationResponse{}, err
	}
	if a == nil || a.diagnosticsTracking == nil {
		return DiagnosticsTrackingMutationResponse{}, fmt.Errorf("management: diagnostics tracking operator is unavailable")
	}
	_, targets, notes := a.diagnosticsTargets(ctx, 0)
	nodes := make([]DiagnosticsTrackingNodeResult, 0, len(targets))
	for _, target := range targets {
		if target.skipped {
			nodes = append(nodes, DiagnosticsTrackingNodeResult{NodeID: target.nodeID, Status: "skipped", Notes: append([]string(nil), target.notes...)})
			continue
		}
		nodeCtx, cancel := context.WithTimeout(ctx, managerDiagnosticsNodeTimeout)
		installed, err := a.diagnosticsTracking.AddNodeDiagnosticsTrackingRule(nodeCtx, target.nodeID, input)
		cancel()
		if err != nil {
			nodes = append(nodes, diagnosticsTrackingUnavailableNode(target.nodeID, "diagnostics tracking create unavailable: "+err.Error()))
			continue
		}
		if rule.CreatedAt.IsZero() {
			rule = managerDiagnosticsTrackingRule(installed)
		} else {
			rule = mergeDiagnosticsTrackingRule(rule, managerDiagnosticsTrackingRule(installed))
		}
		nodes = append(nodes, DiagnosticsTrackingNodeResult{NodeID: target.nodeID, Status: "ok", Notes: []string{}})
	}
	sortDiagnosticsTrackingNodes(nodes)
	return DiagnosticsTrackingMutationResponse{
		Status: diagnosticsTrackingStatus(nodes, notes),
		Rule:   rule,
		Nodes:  nodes,
		Notes:  append([]string(nil), notes...),
	}, nil
}

// ListDiagnosticsTrackingRules returns active runtime tracking rules from eligible cluster nodes.
func (a *App) ListDiagnosticsTrackingRules(ctx context.Context) (DiagnosticsTrackingListResponse, error) {
	if a == nil || a.diagnosticsTracking == nil {
		return DiagnosticsTrackingListResponse{}, fmt.Errorf("management: diagnostics tracking operator is unavailable")
	}
	_, targets, notes := a.diagnosticsTargets(ctx, 0)
	nodes := make([]DiagnosticsTrackingNodeResult, 0, len(targets))
	ruleByID := map[string]DiagnosticsTrackingRule{}
	for _, target := range targets {
		if target.skipped {
			nodes = append(nodes, DiagnosticsTrackingNodeResult{NodeID: target.nodeID, Status: "skipped", Notes: append([]string(nil), target.notes...)})
			continue
		}
		nodeCtx, cancel := context.WithTimeout(ctx, managerDiagnosticsNodeTimeout)
		rules, err := a.diagnosticsTracking.ListNodeDiagnosticsTrackingRules(nodeCtx, target.nodeID)
		cancel()
		if err != nil {
			nodes = append(nodes, diagnosticsTrackingUnavailableNode(target.nodeID, "diagnostics tracking list unavailable: "+err.Error()))
			continue
		}
		for _, rule := range rules {
			managerRule := managerDiagnosticsTrackingRule(rule)
			if existing, ok := ruleByID[managerRule.ID]; ok {
				ruleByID[managerRule.ID] = mergeDiagnosticsTrackingRule(existing, managerRule)
				continue
			}
			ruleByID[managerRule.ID] = managerRule
		}
		nodes = append(nodes, DiagnosticsTrackingNodeResult{NodeID: target.nodeID, Status: "ok", Notes: []string{}})
	}
	rules := make([]DiagnosticsTrackingRule, 0, len(ruleByID))
	for _, rule := range ruleByID {
		rules = append(rules, rule)
	}
	sort.Slice(rules, func(i, j int) bool { return rules[i].ID < rules[j].ID })
	sortDiagnosticsTrackingNodes(nodes)
	return DiagnosticsTrackingListResponse{
		Status: diagnosticsTrackingStatus(nodes, notes),
		Rules:  rules,
		Nodes:  nodes,
		Notes:  append([]string(nil), notes...),
	}, nil
}

// DeleteDiagnosticsTrackingRule removes one runtime tracking rule from eligible cluster nodes.
func (a *App) DeleteDiagnosticsTrackingRule(ctx context.Context, ruleID string) (DiagnosticsTrackingDeleteResponse, error) {
	ruleID = strings.TrimSpace(ruleID)
	if ruleID == "" {
		return DiagnosticsTrackingDeleteResponse{}, diagnostics.ErrInvalidTrackingRule
	}
	if a == nil || a.diagnosticsTracking == nil {
		return DiagnosticsTrackingDeleteResponse{}, fmt.Errorf("management: diagnostics tracking operator is unavailable")
	}
	_, targets, notes := a.diagnosticsTargets(ctx, 0)
	nodes := make([]DiagnosticsTrackingNodeResult, 0, len(targets))
	for _, target := range targets {
		if target.skipped {
			nodes = append(nodes, DiagnosticsTrackingNodeResult{NodeID: target.nodeID, Status: "skipped", Notes: append([]string(nil), target.notes...)})
			continue
		}
		nodeCtx, cancel := context.WithTimeout(ctx, managerDiagnosticsNodeTimeout)
		err := a.diagnosticsTracking.DeleteNodeDiagnosticsTrackingRule(nodeCtx, target.nodeID, ruleID)
		cancel()
		if err != nil {
			nodes = append(nodes, diagnosticsTrackingUnavailableNode(target.nodeID, "diagnostics tracking delete unavailable: "+err.Error()))
			continue
		}
		nodes = append(nodes, DiagnosticsTrackingNodeResult{NodeID: target.nodeID, Status: "ok", Notes: []string{}})
	}
	sortDiagnosticsTrackingNodes(nodes)
	return DiagnosticsTrackingDeleteResponse{
		Status: diagnosticsTrackingStatus(nodes, notes),
		RuleID: ruleID,
		Nodes:  nodes,
		Notes:  append([]string(nil), notes...),
	}, nil
}

func (a *App) buildDiagnosticsTrackingInput(req DiagnosticsTrackingCreateRequest) (diagnostics.TrackingRuleInput, DiagnosticsTrackingRule, error) {
	if req.TTLSeconds <= 0 || time.Duration(req.TTLSeconds)*time.Second > diagnostics.DefaultMaxTrackingTTL || req.SampleRate < 0 || req.SampleRate > 1 {
		return diagnostics.TrackingRuleInput{}, DiagnosticsTrackingRule{}, diagnostics.ErrInvalidTrackingRule
	}
	ruleID, err := newDiagnosticsTrackingRuleID()
	if err != nil {
		return diagnostics.TrackingRuleInput{}, DiagnosticsTrackingRule{}, err
	}
	input := diagnostics.TrackingRuleInput{
		ID:         ruleID,
		TTL:        time.Duration(req.TTLSeconds) * time.Second,
		SampleRate: req.SampleRate,
	}
	rule := DiagnosticsTrackingRule{ID: ruleID, SampleRate: req.SampleRate}
	switch diagnostics.TrackingTarget(strings.TrimSpace(req.Target)) {
	case diagnostics.TrackingTargetSenderUID:
		uid := strings.TrimSpace(req.UID)
		if uid == "" {
			return diagnostics.TrackingRuleInput{}, DiagnosticsTrackingRule{}, diagnostics.ErrInvalidTrackingRule
		}
		input.Target = diagnostics.TrackingTargetSenderUID
		input.UID = uid
		rule.Target = string(diagnostics.TrackingTargetSenderUID)
		rule.UID = uid
	case diagnostics.TrackingTargetChannel:
		channelID := strings.TrimSpace(req.ChannelID)
		if channelID == "" || req.ChannelType == 0 {
			return diagnostics.TrackingRuleInput{}, DiagnosticsTrackingRule{}, diagnostics.ErrInvalidTrackingRule
		}
		channelKey := string(channelhandler.KeyFromChannelID(channel.ChannelID{ID: channelID, Type: req.ChannelType}))
		input.Target = diagnostics.TrackingTargetChannel
		input.ChannelKey = channelKey
		rule.Target = string(diagnostics.TrackingTargetChannel)
		rule.ChannelID = channelID
		rule.ChannelType = req.ChannelType
		rule.ChannelKey = channelKey
	default:
		return diagnostics.TrackingRuleInput{}, DiagnosticsTrackingRule{}, diagnostics.ErrInvalidTrackingRule
	}
	return input, rule, nil
}

func newDiagnosticsTrackingRuleID() (string, error) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("management: generate diagnostics tracking rule id: %w", err)
	}
	return "diagtrack-" + hex.EncodeToString(raw[:]), nil
}

func managerDiagnosticsTrackingRule(rule diagnostics.TrackingRule) DiagnosticsTrackingRule {
	out := DiagnosticsTrackingRule{
		ID:         rule.ID,
		Target:     string(rule.Target),
		UID:        rule.UID,
		ChannelKey: rule.ChannelKey,
		SampleRate: rule.SampleRate,
		CreatedAt:  rule.CreatedAt,
		ExpiresAt:  rule.ExpiresAt,
	}
	if rule.ChannelKey != "" {
		id, err := channelhandler.ParseChannelKey(channel.ChannelKey(rule.ChannelKey))
		if err == nil {
			out.ChannelID = id.ID
			out.ChannelType = id.Type
		}
	}
	return out
}

func mergeDiagnosticsTrackingRule(left, right DiagnosticsTrackingRule) DiagnosticsTrackingRule {
	if left.ID == "" {
		return right
	}
	if right.ID == "" {
		return left
	}
	if left.CreatedAt.IsZero() || (!right.CreatedAt.IsZero() && right.CreatedAt.Before(left.CreatedAt)) {
		left.CreatedAt = right.CreatedAt
	}
	if right.ExpiresAt.After(left.ExpiresAt) {
		left.ExpiresAt = right.ExpiresAt
	}
	return left
}

func diagnosticsTrackingUnavailableNode(nodeID uint64, note string) DiagnosticsTrackingNodeResult {
	return DiagnosticsTrackingNodeResult{NodeID: nodeID, Status: "unavailable", Notes: []string{note}}
}

func sortDiagnosticsTrackingNodes(nodes []DiagnosticsTrackingNodeResult) {
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].NodeID < nodes[j].NodeID })
}

func diagnosticsTrackingStatus(nodes []DiagnosticsTrackingNodeResult, notes []string) DiagnosticsTrackingStatus {
	okCount := 0
	nonOKCount := 0
	for _, node := range nodes {
		if node.Status == "ok" {
			okCount++
			continue
		}
		nonOKCount++
	}
	if okCount == 0 {
		return DiagnosticsTrackingStatusError
	}
	if nonOKCount > 0 || managerDiagnosticsSnapshotUnavailable(notes) {
		return DiagnosticsTrackingStatusPartial
	}
	return DiagnosticsTrackingStatusOK
}
