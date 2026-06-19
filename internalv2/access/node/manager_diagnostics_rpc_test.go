package node

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internalv2/observability/diagnostics"
)

func TestManagerDiagnosticsRPCQueriesLocalStore(t *testing.T) {
	service := &fakeManagerDiagnosticsService{
		result: diagnostics.QueryResult{
			Scope:  "local_node",
			NodeID: 2,
			Status: diagnostics.StatusOK,
			Events: []diagnostics.Event{{
				TraceID: "trace-1",
				Stage:   diagnostics.Stage("gateway_send"),
				At:      time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC),
				Result:  diagnostics.ResultOK,
			}},
		},
	}
	adapter := New(Options{ManagerDiagnostics: service})
	body, err := encodeManagerDiagnosticsRequest(managerDiagnosticsRPCRequest{
		Op:    managerDiagnosticsOpQuery,
		Query: diagnostics.Query{TraceID: "trace-1", Limit: 50},
	})
	if err != nil {
		t.Fatalf("encodeManagerDiagnosticsRequest() error = %v", err)
	}

	respBody, err := adapter.HandleManagerDiagnosticsRPC(context.Background(), body)
	if err != nil {
		t.Fatalf("HandleManagerDiagnosticsRPC() error = %v", err)
	}
	resp, err := decodeManagerDiagnosticsResponse(respBody)
	if err != nil {
		t.Fatalf("decodeManagerDiagnosticsResponse() error = %v", err)
	}

	if resp.Status != rpcStatusOK || resp.Result.NodeID != 2 || len(resp.Result.Events) != 1 {
		t.Fatalf("response = %#v, want ok diagnostics result", resp)
	}
	if service.query.TraceID != "trace-1" || service.query.Limit != 50 {
		t.Fatalf("query = %#v, want trace-1 limit 50", service.query)
	}
}

func TestManagerDiagnosticsRPCClientRoutesTrackingRules(t *testing.T) {
	service := &fakeManagerDiagnosticsService{
		addRule: diagnostics.TrackingRule{ID: "rule-1", Target: diagnostics.TrackingTargetSenderUID, UID: "u1", SampleRate: 1},
		rules:   []diagnostics.TrackingRule{{ID: "rule-1", Target: diagnostics.TrackingTargetSenderUID, UID: "u1", SampleRate: 1}},
	}
	adapter := New(Options{ManagerDiagnostics: service})
	node := &fakeManagerDiagnosticsRPCNode{handler: adapter.HandleManagerDiagnosticsRPC}
	client := NewClient(node)

	added, err := client.AddManagerDiagnosticsTrackingRule(context.Background(), 2, diagnostics.TrackingRuleInput{
		ID:         "rule-1",
		Target:     diagnostics.TrackingTargetSenderUID,
		UID:        "u1",
		TTL:        time.Minute,
		SampleRate: 1,
	})
	if err != nil {
		t.Fatalf("AddManagerDiagnosticsTrackingRule() error = %v", err)
	}
	listed, err := client.ListManagerDiagnosticsTrackingRules(context.Background(), 2)
	if err != nil {
		t.Fatalf("ListManagerDiagnosticsTrackingRules() error = %v", err)
	}
	if err := client.DeleteManagerDiagnosticsTrackingRule(context.Background(), 2, "rule-1"); err != nil {
		t.Fatalf("DeleteManagerDiagnosticsTrackingRule() error = %v", err)
	}

	if added.ID != "rule-1" || len(listed) != 1 || listed[0].ID != "rule-1" || service.deletedRuleID != "rule-1" {
		t.Fatalf("tracking results added=%#v listed=%#v deleted=%q", added, listed, service.deletedRuleID)
	}
	if node.nodeID != 2 || node.serviceID != ManagerDiagnosticsRPCServiceID {
		t.Fatalf("rpc target = node:%d service:%d, want node 2 service %d", node.nodeID, node.serviceID, ManagerDiagnosticsRPCServiceID)
	}
}

type fakeManagerDiagnosticsService struct {
	query         diagnostics.Query
	result        diagnostics.QueryResult
	addInput      diagnostics.TrackingRuleInput
	addRule       diagnostics.TrackingRule
	rules         []diagnostics.TrackingRule
	deletedRuleID string
}

func (f *fakeManagerDiagnosticsService) QueryDiagnostics(_ context.Context, query diagnostics.Query) diagnostics.QueryResult {
	f.query = query
	return f.result
}

func (f *fakeManagerDiagnosticsService) AddDiagnosticsTrackingRule(_ context.Context, input diagnostics.TrackingRuleInput) (diagnostics.TrackingRule, error) {
	f.addInput = input
	return f.addRule, nil
}

func (f *fakeManagerDiagnosticsService) ListDiagnosticsTrackingRules(context.Context) ([]diagnostics.TrackingRule, error) {
	return append([]diagnostics.TrackingRule(nil), f.rules...), nil
}

func (f *fakeManagerDiagnosticsService) DeleteDiagnosticsTrackingRule(_ context.Context, ruleID string) error {
	f.deletedRuleID = ruleID
	return nil
}

type fakeManagerDiagnosticsRPCNode struct {
	handler   func(context.Context, []byte) ([]byte, error)
	nodeID    uint64
	serviceID uint8
}

func (f *fakeManagerDiagnosticsRPCNode) CallRPC(ctx context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error) {
	f.nodeID = nodeID
	f.serviceID = serviceID
	return f.handler(ctx, payload)
}
