package management

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internalv2/observability/diagnostics"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
)

func TestQueryDiagnosticsAggregatesControlSnapshotNodes(t *testing.T) {
	now := time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC)
	reader := &fakeDiagnosticsReader{
		results: map[uint64]diagnostics.QueryResult{
			1: diagnosticsResult(1, diagnostics.StatusOK,
				diagnosticsEvent(1, now.Add(2*time.Second), "channel_append", diagnostics.ResultOK),
			),
			2: diagnosticsResult(2, diagnostics.StatusOK,
				diagnosticsEvent(2, now.Add(time.Second), "gateway_send", diagnostics.ResultOK),
			),
		},
	}
	app := New(Options{
		Cluster: fakeNodeSnapshotReader{
			nodeID: 1,
			snapshot: control.Snapshot{Nodes: []control.Node{
				{NodeID: 1, Status: control.NodeAlive},
				{NodeID: 2, Status: control.NodeSuspect},
			}},
		},
		Diagnostics: reader,
		Now:         func() time.Time { return now },
	})

	got, err := app.QueryDiagnostics(context.Background(), DiagnosticsQueryRequest{Query: diagnostics.Query{TraceID: "trace-1"}})
	if err != nil {
		t.Fatalf("QueryDiagnostics() error = %v", err)
	}
	if got.Scope != "cluster" || got.Status != DiagnosticsStatusOK {
		t.Fatalf("status = %s scope = %s, want ok cluster; response=%#v", got.Status, got.Scope, got)
	}
	if len(got.Events) != 2 {
		t.Fatalf("events len = %d, want 2: %#v", len(got.Events), got.Events)
	}
	if got.Events[0].Stage != "gateway_send" || got.Events[1].Stage != "channel_append" {
		t.Fatalf("event order = %#v, want chronological stages", got.Events)
	}
	if got.Summary.EventCount != 2 || !sameUint64s(got.Summary.InvolvedNodes, []uint64{1, 2}) {
		t.Fatalf("summary = %#v, want two involved nodes", got.Summary)
	}
	for _, query := range reader.queries {
		if query.TraceID != "trace-1" || query.Limit != 100 {
			t.Fatalf("node query = %#v, want normalized trace lookup", query)
		}
	}
}

func TestQueryDiagnosticsSkipsDownNodesAndMarksPartial(t *testing.T) {
	now := time.Date(2026, 6, 19, 11, 0, 0, 0, time.UTC)
	reader := &fakeDiagnosticsReader{
		results: map[uint64]diagnostics.QueryResult{
			1: diagnosticsResult(1, diagnostics.StatusOK, diagnosticsEvent(1, now, "gateway_send", diagnostics.ResultOK)),
		},
	}
	app := New(Options{
		Cluster: fakeNodeSnapshotReader{
			nodeID: 1,
			snapshot: control.Snapshot{Nodes: []control.Node{
				{NodeID: 1, Status: control.NodeAlive},
				{NodeID: 2, Status: control.NodeDown},
			}},
		},
		Diagnostics: reader,
		Now:         func() time.Time { return now },
	})

	got, err := app.QueryDiagnostics(context.Background(), DiagnosticsQueryRequest{})
	if err != nil {
		t.Fatalf("QueryDiagnostics() error = %v", err)
	}
	if got.Status != DiagnosticsStatusPartial {
		t.Fatalf("status = %s, want partial: %#v", got.Status, got)
	}
	if diagnosticsNodeStatus(got.Nodes, 2) != "skipped" {
		t.Fatalf("nodes = %#v, want node 2 skipped", got.Nodes)
	}
	if _, ok := reader.queries[2]; ok {
		t.Fatalf("down node was queried: %#v", reader.queries)
	}
}

func TestQueryDiagnosticsFallsBackToLocalNodeWhenSnapshotUnavailable(t *testing.T) {
	reader := &fakeDiagnosticsReader{results: map[uint64]diagnostics.QueryResult{
		9: diagnosticsResult(9, diagnostics.StatusNotFound),
	}}
	app := New(Options{
		Cluster:     fakeNodeSnapshotReader{nodeID: 9, err: errors.New("control unavailable")},
		Diagnostics: reader,
		Now:         func() time.Time { return time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC) },
	})

	got, err := app.QueryDiagnostics(context.Background(), DiagnosticsQueryRequest{})
	if err != nil {
		t.Fatalf("QueryDiagnostics() error = %v", err)
	}
	if got.Scope != "local_node" || got.Status != DiagnosticsStatusPartial {
		t.Fatalf("scope/status = %s/%s, want local_node partial", got.Scope, got.Status)
	}
	if _, ok := reader.queries[9]; !ok {
		t.Fatalf("local fallback node was not queried: %#v", reader.queries)
	}
}

func TestCreateDiagnosticsTrackingRuleFansOutToControlSnapshotNodes(t *testing.T) {
	tracker := newDiagnosticsTrackingStub()
	app := New(Options{
		Cluster: fakeNodeSnapshotReader{
			nodeID: 1,
			snapshot: control.Snapshot{Nodes: []control.Node{
				{NodeID: 1, Status: control.NodeAlive},
				{NodeID: 2, Status: control.NodeSuspect},
				{NodeID: 3, Status: control.NodeDown},
			}},
		},
		DiagnosticsTracking: tracker,
	})

	resp, err := app.CreateDiagnosticsTrackingRule(context.Background(), DiagnosticsTrackingCreateRequest{
		Target: "sender_uid", UID: "u1", TTLSeconds: 60, SampleRate: 1,
	})
	if err != nil {
		t.Fatalf("CreateDiagnosticsTrackingRule() error = %v", err)
	}
	if resp.Status != DiagnosticsTrackingStatusPartial {
		t.Fatalf("status = %s, want partial because down node is skipped", resp.Status)
	}
	if tracker.addedRule(t, 1).UID != "u1" || tracker.addedRule(t, 2).UID != "u1" {
		t.Fatalf("tracking fanout = %#v, want rule on nodes 1 and 2", tracker.added)
	}
	if _, ok := tracker.added[3]; ok {
		t.Fatalf("down node received tracking rule: %#v", tracker.added)
	}
}

type fakeDiagnosticsReader struct {
	mu      sync.Mutex
	results map[uint64]diagnostics.QueryResult
	errors  map[uint64]error
	queries map[uint64]diagnostics.Query
}

func (f *fakeDiagnosticsReader) QueryNodeDiagnostics(_ context.Context, nodeID uint64, q diagnostics.Query) (diagnostics.QueryResult, error) {
	f.mu.Lock()
	if f.queries == nil {
		f.queries = map[uint64]diagnostics.Query{}
	}
	f.queries[nodeID] = q
	f.mu.Unlock()
	if err := f.errors[nodeID]; err != nil {
		return diagnostics.QueryResult{}, err
	}
	return f.results[nodeID], nil
}

type diagnosticsTrackingStub struct {
	mu        sync.Mutex
	added     map[uint64]diagnostics.TrackingRuleInput
	rules     map[uint64][]diagnostics.TrackingRule
	deleted   map[uint64]string
	failNodes map[uint64]error
}

func newDiagnosticsTrackingStub() *diagnosticsTrackingStub {
	return &diagnosticsTrackingStub{
		added:     map[uint64]diagnostics.TrackingRuleInput{},
		rules:     map[uint64][]diagnostics.TrackingRule{},
		deleted:   map[uint64]string{},
		failNodes: map[uint64]error{},
	}
}

func (s *diagnosticsTrackingStub) AddNodeDiagnosticsTrackingRule(_ context.Context, nodeID uint64, input diagnostics.TrackingRuleInput) (diagnostics.TrackingRule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.failNodes[nodeID]; err != nil {
		return diagnostics.TrackingRule{}, err
	}
	s.added[nodeID] = input
	rule := diagnostics.TrackingRule{ID: input.ID, Target: input.Target, UID: input.UID, ChannelKey: input.ChannelKey, SampleRate: input.SampleRate}
	s.rules[nodeID] = append(s.rules[nodeID], rule)
	return rule, nil
}

func (s *diagnosticsTrackingStub) ListNodeDiagnosticsTrackingRules(_ context.Context, nodeID uint64) ([]diagnostics.TrackingRule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.failNodes[nodeID]; err != nil {
		return nil, err
	}
	return append([]diagnostics.TrackingRule(nil), s.rules[nodeID]...), nil
}

func (s *diagnosticsTrackingStub) DeleteNodeDiagnosticsTrackingRule(_ context.Context, nodeID uint64, ruleID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.failNodes[nodeID]; err != nil {
		return err
	}
	s.deleted[nodeID] = ruleID
	return nil
}

func (s *diagnosticsTrackingStub) addedRule(t *testing.T, nodeID uint64) diagnostics.TrackingRuleInput {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	rule, ok := s.added[nodeID]
	if !ok {
		t.Fatalf("missing added tracking rule for node %d", nodeID)
	}
	return rule
}

func diagnosticsResult(nodeID uint64, status diagnostics.Status, events ...diagnostics.Event) diagnostics.QueryResult {
	return diagnostics.QueryResult{
		Scope:  "local_node",
		NodeID: nodeID,
		Status: status,
		Events: events,
	}
}

func diagnosticsEvent(nodeID uint64, at time.Time, stage string, result diagnostics.Result) diagnostics.Event {
	return diagnostics.Event{
		TraceID: "trace-1",
		NodeID:  nodeID,
		Stage:   diagnostics.Stage(stage),
		At:      at,
		Result:  result,
	}
}

func diagnosticsNodeStatus(nodes []DiagnosticsNodeResult, nodeID uint64) string {
	for _, node := range nodes {
		if node.NodeID == nodeID {
			return node.Status
		}
	}
	return ""
}

func sameUint64s(left, right []uint64) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
