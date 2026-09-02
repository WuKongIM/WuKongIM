package cluster

import (
	"context"
	"strings"
	"testing"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	"github.com/WuKongIM/WuKongIM/internal/observability/diagnostics"
)

func TestManagementDiagnosticsReaderRoutesExactLocalOperationsWithoutRPC(t *testing.T) {
	t.Parallel()

	local := &contractLocalDiagnostics{
		queryResult: diagnostics.QueryResult{NodeID: 3, TraceID: "trace-1"},
		addedRule:   diagnostics.TrackingRule{ID: "rule-1", UID: "u1"},
		listedRules: []diagnostics.TrackingRule{{ID: "rule-1", UID: "u1"}},
	}
	node := &contractManagementDiagnosticsNode{nodeID: 3}
	reader := NewManagementDiagnosticsReader(node, local)
	query := diagnostics.Query{TraceID: "trace-1", SlotID: 7, Limit: 20}

	result, err := reader.QueryNodeDiagnostics(context.Background(), 3, query)
	if err != nil || result.TraceID != "trace-1" || local.query != query {
		t.Fatalf("QueryNodeDiagnostics() = %#v query=%#v err=%v", result, local.query, err)
	}
	input := diagnostics.TrackingRuleInput{ID: "rule-1", Target: diagnostics.TrackingTargetSenderUID, UID: "u1", SampleRate: 1}
	rule, err := reader.AddNodeDiagnosticsTrackingRule(context.Background(), 3, input)
	if err != nil || rule.ID != "rule-1" || local.addInput != input {
		t.Fatalf("AddNodeDiagnosticsTrackingRule() = %#v input=%#v err=%v", rule, local.addInput, err)
	}
	rules, err := reader.ListNodeDiagnosticsTrackingRules(context.Background(), 0)
	if err != nil || len(rules) != 1 || rules[0].ID != "rule-1" {
		t.Fatalf("ListNodeDiagnosticsTrackingRules(local zero target) = %#v err=%v", rules, err)
	}
	if err := reader.DeleteNodeDiagnosticsTrackingRule(context.Background(), 3, "rule-1"); err != nil {
		t.Fatalf("DeleteNodeDiagnosticsTrackingRule() error = %v", err)
	}
	if local.deletedRuleID != "rule-1" || node.rpcCalls != 0 {
		t.Fatalf("delete id=%q remote calls=%d, want local-only route", local.deletedRuleID, node.rpcCalls)
	}
}

func TestManagementDiagnosticsReaderFailsClosedWhenSelectedPathIsUnwired(t *testing.T) {
	t.Parallel()

	reader := NewManagementDiagnosticsReader(&contractManagementDiagnosticsNode{nodeID: 3}, nil)
	if _, err := reader.QueryNodeDiagnostics(context.Background(), 3, diagnostics.Query{}); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("local unwired query error = %v", err)
	}
	var nilReader *ManagementDiagnosticsReader
	if err := nilReader.DeleteNodeDiagnosticsTrackingRule(context.Background(), 3, "rule-1"); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("nil reader delete error = %v", err)
	}
}

func TestManagementDiagnosticsReaderRoutesExactRemoteOperations(t *testing.T) {
	t.Parallel()

	remoteService := &contractLocalDiagnostics{
		queryResult: diagnostics.QueryResult{NodeID: 2, TraceID: "trace-2"},
		addedRule:   diagnostics.TrackingRule{ID: "rule-2", UID: "u2"},
		listedRules: []diagnostics.TrackingRule{{ID: "rule-2", UID: "u2"}},
	}
	adapter := accessnode.New(accessnode.Options{ManagerDiagnostics: remoteService})
	node := &contractManagementDiagnosticsNode{nodeID: 1, handler: adapter.HandleManagerDiagnosticsRPC}
	reader := NewManagementDiagnosticsReader(node, nil)

	query := diagnostics.Query{TraceID: "trace-2", Limit: 10}
	result, err := reader.QueryNodeDiagnostics(context.Background(), 2, query)
	if err != nil || result.NodeID != 2 || remoteService.query != query {
		t.Fatalf("remote QueryNodeDiagnostics() = %#v query=%#v err=%v", result, remoteService.query, err)
	}
	input := diagnostics.TrackingRuleInput{ID: "rule-2", Target: diagnostics.TrackingTargetSenderUID, UID: "u2", SampleRate: 1}
	rule, err := reader.AddNodeDiagnosticsTrackingRule(context.Background(), 2, input)
	if err != nil || rule.ID != "rule-2" || remoteService.addInput != input {
		t.Fatalf("remote AddNodeDiagnosticsTrackingRule() = %#v input=%#v err=%v", rule, remoteService.addInput, err)
	}
	rules, err := reader.ListNodeDiagnosticsTrackingRules(context.Background(), 2)
	if err != nil || len(rules) != 1 || rules[0].ID != "rule-2" {
		t.Fatalf("remote ListNodeDiagnosticsTrackingRules() = %#v err=%v", rules, err)
	}
	if err := reader.DeleteNodeDiagnosticsTrackingRule(context.Background(), 2, "rule-2"); err != nil {
		t.Fatalf("remote DeleteNodeDiagnosticsTrackingRule() error = %v", err)
	}
	if remoteService.deletedRuleID != "rule-2" || node.calledNodeID != 2 || node.calledServiceID != accessnode.ManagerDiagnosticsRPCServiceID || node.rpcCalls != 4 {
		t.Fatalf("remote route calls=%d target=%d/%d deleted=%q", node.rpcCalls, node.calledNodeID, node.calledServiceID, remoteService.deletedRuleID)
	}
}

type contractManagementDiagnosticsNode struct {
	nodeID          uint64
	rpcCalls        int
	calledNodeID    uint64
	calledServiceID uint8
	handler         func(context.Context, []byte) ([]byte, error)
}

func (n *contractManagementDiagnosticsNode) NodeID() uint64 { return n.nodeID }

func (n *contractManagementDiagnosticsNode) CallRPC(ctx context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error) {
	n.rpcCalls++
	n.calledNodeID, n.calledServiceID = nodeID, serviceID
	if n.handler == nil {
		return nil, nil
	}
	return n.handler(ctx, payload)
}

type contractLocalDiagnostics struct {
	query         diagnostics.Query
	queryResult   diagnostics.QueryResult
	addInput      diagnostics.TrackingRuleInput
	addedRule     diagnostics.TrackingRule
	listedRules   []diagnostics.TrackingRule
	deletedRuleID string
}

func (d *contractLocalDiagnostics) QueryDiagnostics(_ context.Context, query diagnostics.Query) diagnostics.QueryResult {
	d.query = query
	return d.queryResult
}

func (d *contractLocalDiagnostics) AddDiagnosticsTrackingRule(_ context.Context, input diagnostics.TrackingRuleInput) (diagnostics.TrackingRule, error) {
	d.addInput = input
	return d.addedRule, nil
}

func (d *contractLocalDiagnostics) ListDiagnosticsTrackingRules(context.Context) ([]diagnostics.TrackingRule, error) {
	return append([]diagnostics.TrackingRule(nil), d.listedRules...), nil
}

func (d *contractLocalDiagnostics) DeleteDiagnosticsTrackingRule(_ context.Context, ruleID string) error {
	d.deletedRuleID = ruleID
	return nil
}
