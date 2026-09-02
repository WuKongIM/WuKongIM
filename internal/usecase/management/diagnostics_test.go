package management

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/observability/diagnostics"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
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

func TestManagerDiagnosticsEventPreservesPreferredLeaderDecisionContext(t *testing.T) {
	got := managerDiagnosticsEvent(diagnostics.Event{
		Stage:             diagnostics.StageSlotPreferredLeaderReconcile,
		SlotID:            7,
		Decision:          "preferred_lagging",
		ActualLeaderID:    1,
		PreferredLeaderID: 2,
		RaftTerm:          11,
		ConfigEpoch:       4,
	})

	if got.SlotID != 7 || got.Decision != "preferred_lagging" || got.ActualLeaderID != 1 ||
		got.PreferredLeaderID != 2 || got.RaftTerm != 11 || got.ConfigEpoch != 4 {
		t.Fatalf("manager event = %#v, want explicit preferred-leader decision context", got)
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

func TestCreateDiagnosticsTrackingRuleTargetsOneRequestedNode(t *testing.T) {
	tracker := newDiagnosticsTrackingStub()
	app := New(Options{
		Cluster: fakeNodeSnapshotReader{
			nodeID: 1,
			snapshot: control.Snapshot{Nodes: []control.Node{
				{NodeID: 1, Status: control.NodeAlive},
				{NodeID: 2, Status: control.NodeAlive},
			}},
		},
		DiagnosticsTracking: tracker,
	})

	resp, err := app.CreateDiagnosticsTrackingRule(context.Background(), DiagnosticsTrackingCreateRequest{
		NodeID: 2, Target: "sender_uid", UID: "u1", TTLSeconds: 60, SampleRate: 1,
	})
	if err != nil {
		t.Fatalf("CreateDiagnosticsTrackingRule() error = %v", err)
	}
	if resp.Status != DiagnosticsTrackingStatusOK || len(resp.Nodes) != 1 || resp.Nodes[0].NodeID != 2 {
		t.Fatalf("response = %#v, want only node 2", resp)
	}
	if tracker.addedRule(t, 2).UID != "u1" {
		t.Fatalf("tracking target = %#v, want node 2", tracker.added)
	}
	if _, ok := tracker.added[1]; ok {
		t.Fatalf("unexpected rule on node 1: %#v", tracker.added)
	}
}

func TestCreateDiagnosticsChannelTrackingRuleUsesCanonicalChannelIdentity(t *testing.T) {
	tracker := newDiagnosticsTrackingStub()
	app := New(Options{DiagnosticsTracking: tracker})

	resp, err := app.CreateDiagnosticsTrackingRule(context.Background(), DiagnosticsTrackingCreateRequest{
		NodeID: 7, Target: " channel ", ChannelID: " group/alpha ", ChannelType: 2,
		TTLSeconds: 90, SampleRate: 0.25,
	})
	if err != nil {
		t.Fatalf("CreateDiagnosticsTrackingRule() error = %v", err)
	}
	input := tracker.addedRule(t, 7)
	wantKey := sendtrace.ChannelKeyFromID("group/alpha", 2)
	if input.Target != diagnostics.TrackingTargetChannel || input.ChannelKey != wantKey || input.TTL != 90*time.Second || input.SampleRate != 0.25 {
		t.Fatalf("installed channel rule = %#v", input)
	}
	if resp.Status != DiagnosticsTrackingStatusOK || resp.Rule.ChannelKey != wantKey || resp.Rule.ChannelID != "group/alpha" || resp.Rule.ChannelType != 2 {
		t.Fatalf("manager channel rule = %#v", resp)
	}
}

func TestDiagnosticsTrackingListMergesReplicasAndPreservesPartialEvidence(t *testing.T) {
	tracker := newDiagnosticsTrackingStub()
	createdEarly := time.Date(2026, 6, 19, 9, 0, 0, 0, time.UTC)
	createdLate := createdEarly.Add(time.Minute)
	expiresEarly := createdEarly.Add(30 * time.Minute)
	expiresLate := createdEarly.Add(time.Hour)
	channelKey := sendtrace.ChannelKeyFromID("group/alpha", 2)
	tracker.rules[1] = []diagnostics.TrackingRule{
		{ID: "rule-b", Target: diagnostics.TrackingTargetChannel, ChannelKey: channelKey, SampleRate: 0.5, CreatedAt: createdLate, ExpiresAt: expiresEarly},
		{ID: "rule-a", Target: diagnostics.TrackingTargetSenderUID, UID: "u1", SampleRate: 1, CreatedAt: createdLate, ExpiresAt: expiresEarly},
	}
	tracker.rules[2] = []diagnostics.TrackingRule{
		{ID: "rule-b", Target: diagnostics.TrackingTargetChannel, ChannelKey: channelKey, SampleRate: 0.5, CreatedAt: createdEarly, ExpiresAt: expiresLate},
	}
	app := New(Options{
		Cluster: fakeNodeSnapshotReader{snapshot: control.Snapshot{Nodes: []control.Node{
			{NodeID: 3, Status: control.NodeDown},
			{NodeID: 2, Status: control.NodeSuspect},
			{NodeID: 1, Status: control.NodeAlive},
		}}},
		DiagnosticsTracking: tracker,
	})

	resp, err := app.ListDiagnosticsTrackingRules(context.Background())
	if err != nil {
		t.Fatalf("ListDiagnosticsTrackingRules() error = %v", err)
	}
	if resp.Status != DiagnosticsTrackingStatusPartial {
		t.Fatalf("status = %s, want partial", resp.Status)
	}
	if len(resp.Rules) != 2 || resp.Rules[0].ID != "rule-a" || resp.Rules[1].ID != "rule-b" {
		t.Fatalf("sorted rules = %#v", resp.Rules)
	}
	merged := resp.Rules[1]
	if !merged.CreatedAt.Equal(createdEarly) || !merged.ExpiresAt.Equal(expiresLate) || merged.ChannelID != "group/alpha" || merged.ChannelType != 2 {
		t.Fatalf("merged replicated rule = %#v", merged)
	}
	if len(resp.Nodes) != 3 || resp.Nodes[0].NodeID != 1 || resp.Nodes[1].NodeID != 2 || resp.Nodes[2].NodeID != 3 || resp.Nodes[2].Status != "skipped" {
		t.Fatalf("node evidence = %#v", resp.Nodes)
	}
}

func TestDiagnosticsTrackingDeleteReportsPerNodeFailureAndExactRuleIdentity(t *testing.T) {
	tracker := newDiagnosticsTrackingStub()
	tracker.failNodes[2] = errors.New("node 2 diagnostics unavailable")
	app := New(Options{
		Cluster: fakeNodeSnapshotReader{snapshot: control.Snapshot{Nodes: []control.Node{
			{NodeID: 2, Status: control.NodeAlive},
			{NodeID: 1, Status: control.NodeAlive},
			{NodeID: 3, Status: control.NodeDown},
		}}},
		DiagnosticsTracking: tracker,
	})

	resp, err := app.DeleteDiagnosticsTrackingRule(context.Background(), " rule-7 ")
	if err != nil {
		t.Fatalf("DeleteDiagnosticsTrackingRule() error = %v", err)
	}
	if resp.Status != DiagnosticsTrackingStatusPartial || resp.RuleID != "rule-7" {
		t.Fatalf("delete response = %#v", resp)
	}
	if tracker.deleted[1] != "rule-7" {
		t.Fatalf("node 1 deleted rule = %q", tracker.deleted[1])
	}
	if _, ok := tracker.deleted[2]; ok {
		t.Fatalf("failed node recorded deletion: %#v", tracker.deleted)
	}
	if len(resp.Nodes) != 3 || resp.Nodes[0].Status != "ok" || resp.Nodes[1].Status != "unavailable" || resp.Nodes[2].Status != "skipped" {
		t.Fatalf("delete node evidence = %#v", resp.Nodes)
	}
	if len(resp.Nodes[1].Notes) != 1 || !strings.Contains(resp.Nodes[1].Notes[0], "node 2 diagnostics unavailable") {
		t.Fatalf("node failure notes = %#v", resp.Nodes[1].Notes)
	}
}

func TestDiagnosticsTrackingRejectsInvalidRulesBeforeFanout(t *testing.T) {
	tracker := newDiagnosticsTrackingStub()
	app := New(Options{DiagnosticsTracking: tracker})
	tests := []DiagnosticsTrackingCreateRequest{
		{NodeID: 1, Target: "sender_uid", UID: "", TTLSeconds: 60, SampleRate: 1},
		{NodeID: 1, Target: "channel", ChannelID: "g1", ChannelType: 0, TTLSeconds: 60, SampleRate: 1},
		{NodeID: 1, Target: "sender_uid", UID: "u1", TTLSeconds: 0, SampleRate: 1},
		{NodeID: 1, Target: "sender_uid", UID: "u1", TTLSeconds: 60, SampleRate: 1.01},
		{NodeID: 1, Target: "unknown", UID: "u1", TTLSeconds: 60, SampleRate: 1},
	}
	for _, req := range tests {
		if _, err := app.CreateDiagnosticsTrackingRule(context.Background(), req); !errors.Is(err, diagnostics.ErrInvalidTrackingRule) {
			t.Fatalf("request %#v error = %v", req, err)
		}
	}
	if len(tracker.added) != 0 {
		t.Fatalf("invalid rules reached nodes: %#v", tracker.added)
	}
	if _, err := app.DeleteDiagnosticsTrackingRule(context.Background(), "  "); !errors.Is(err, diagnostics.ErrInvalidTrackingRule) {
		t.Fatalf("empty delete error = %v", err)
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
