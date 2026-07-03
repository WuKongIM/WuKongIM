package management

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/observability/diagnostics"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/legacy/controller/meta"
	"github.com/stretchr/testify/require"
)

func TestCreateDiagnosticsTrackingRuleForSenderUID(t *testing.T) {
	tracker := newDiagnosticsTrackingStub()
	app := New(Options{
		LocalNodeID: 1,
		Cluster: fakeClusterReader{nodes: []controllermeta.ClusterNode{
			{NodeID: 1, Status: controllermeta.NodeStatusAlive},
			{NodeID: 2, Status: controllermeta.NodeStatusAlive},
		}},
		DiagnosticsTracking: tracker,
		Now:                 func() time.Time { return time.Unix(100, 0) },
	})

	resp, err := app.CreateDiagnosticsTrackingRule(context.Background(), DiagnosticsTrackingCreateRequest{
		Target: "sender_uid", UID: "u1", TTLSeconds: 3600, SampleRate: 1,
	})

	require.NoError(t, err)
	require.Equal(t, DiagnosticsTrackingStatusOK, resp.Status)
	require.NotEmpty(t, resp.Rule.ID)
	require.Equal(t, string(diagnostics.TrackingTargetSenderUID), resp.Rule.Target)
	require.Equal(t, "u1", resp.Rule.UID)
	require.Len(t, resp.Nodes, 2)
	require.Equal(t, resp.Rule.ID, tracker.addedRule(t, 1).ID)
	require.Equal(t, resp.Rule.ID, tracker.addedRule(t, 2).ID)
	require.Equal(t, time.Hour, tracker.addedRule(t, 1).TTL)
}

func TestCreateDiagnosticsTrackingRuleConvertsChannelKey(t *testing.T) {
	tracker := newDiagnosticsTrackingStub()
	app := New(Options{
		LocalNodeID:         1,
		Cluster:             fakeClusterReader{nodes: []controllermeta.ClusterNode{{NodeID: 1, Status: controllermeta.NodeStatusAlive}}},
		DiagnosticsTracking: tracker,
	})

	resp, err := app.CreateDiagnosticsTrackingRule(context.Background(), DiagnosticsTrackingCreateRequest{
		Target: "channel", ChannelID: "g1", ChannelType: 2, TTLSeconds: 60, SampleRate: 1,
	})

	require.NoError(t, err)
	require.Equal(t, "channel/2/ZzE", resp.Rule.ChannelKey)
	require.Equal(t, "channel/2/ZzE", tracker.addedRule(t, 1).ChannelKey)
}

func TestCreateDiagnosticsTrackingRuleReturnsPartialWhenNodeFails(t *testing.T) {
	tracker := newDiagnosticsTrackingStub()
	tracker.failNodes[2] = errors.New("rpc timeout")
	app := New(Options{
		LocalNodeID: 1,
		Cluster: fakeClusterReader{nodes: []controllermeta.ClusterNode{
			{NodeID: 1, Status: controllermeta.NodeStatusAlive},
			{NodeID: 2, Status: controllermeta.NodeStatusAlive},
		}},
		DiagnosticsTracking: tracker,
	})

	resp, err := app.CreateDiagnosticsTrackingRule(context.Background(), DiagnosticsTrackingCreateRequest{Target: "sender_uid", UID: "u1", TTLSeconds: 60, SampleRate: 1})

	require.NoError(t, err)
	require.Equal(t, DiagnosticsTrackingStatusPartial, resp.Status)
	require.Len(t, resp.Nodes, 2)
	require.Equal(t, "unavailable", diagnosticsTrackingNodeStatus(resp.Nodes, 2))
}

func TestCreateDiagnosticsTrackingRuleRejectsInvalidInput(t *testing.T) {
	app := New(Options{})

	_, err := app.CreateDiagnosticsTrackingRule(context.Background(), DiagnosticsTrackingCreateRequest{Target: "sender_uid", UID: "", TTLSeconds: 60, SampleRate: 1})
	require.Error(t, err)
	_, err = app.CreateDiagnosticsTrackingRule(context.Background(), DiagnosticsTrackingCreateRequest{Target: "channel", ChannelID: "g1", ChannelType: 0, TTLSeconds: 60, SampleRate: 1})
	require.Error(t, err)
	_, err = app.CreateDiagnosticsTrackingRule(context.Background(), DiagnosticsTrackingCreateRequest{Target: "sender_uid", UID: "u1", TTLSeconds: 0, SampleRate: 1})
	require.Error(t, err)
	_, err = app.CreateDiagnosticsTrackingRule(context.Background(), DiagnosticsTrackingCreateRequest{Target: "sender_uid", UID: "u1", TTLSeconds: 60, SampleRate: 1.1})
	require.Error(t, err)
}

func TestListDiagnosticsTrackingRulesDeduplicatesRulesAcrossNodes(t *testing.T) {
	now := time.Unix(100, 0)
	tracker := newDiagnosticsTrackingStub()
	tracker.rules[1] = []diagnostics.TrackingRule{{ID: "rule-1", Target: diagnostics.TrackingTargetSenderUID, UID: "u1", SampleRate: 1, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}}
	tracker.rules[2] = []diagnostics.TrackingRule{{ID: "rule-1", Target: diagnostics.TrackingTargetSenderUID, UID: "u1", SampleRate: 1, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}}
	app := New(Options{
		LocalNodeID: 1,
		Cluster: fakeClusterReader{nodes: []controllermeta.ClusterNode{
			{NodeID: 1, Status: controllermeta.NodeStatusAlive},
			{NodeID: 2, Status: controllermeta.NodeStatusAlive},
		}},
		DiagnosticsTracking: tracker,
	})

	resp, err := app.ListDiagnosticsTrackingRules(context.Background())

	require.NoError(t, err)
	require.Equal(t, DiagnosticsTrackingStatusOK, resp.Status)
	require.Len(t, resp.Rules, 1)
	require.Equal(t, "rule-1", resp.Rules[0].ID)
	require.Len(t, resp.Nodes, 2)
}

func TestDeleteDiagnosticsTrackingRuleFansOut(t *testing.T) {
	tracker := newDiagnosticsTrackingStub()
	app := New(Options{
		LocalNodeID: 1,
		Cluster: fakeClusterReader{nodes: []controllermeta.ClusterNode{
			{NodeID: 1, Status: controllermeta.NodeStatusAlive},
			{NodeID: 2, Status: controllermeta.NodeStatusAlive},
		}},
		DiagnosticsTracking: tracker,
	})

	resp, err := app.DeleteDiagnosticsTrackingRule(context.Background(), "rule-1")

	require.NoError(t, err)
	require.Equal(t, DiagnosticsTrackingStatusOK, resp.Status)
	require.Equal(t, "rule-1", resp.RuleID)
	require.Equal(t, "rule-1", tracker.deletedRule(t, 1))
	require.Equal(t, "rule-1", tracker.deletedRule(t, 2))
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
	require.True(t, ok)
	return rule
}

func (s *diagnosticsTrackingStub) deletedRule(t *testing.T, nodeID uint64) string {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	ruleID, ok := s.deleted[nodeID]
	require.True(t, ok)
	return ruleID
}

func diagnosticsTrackingNodeStatus(nodes []DiagnosticsTrackingNodeResult, nodeID uint64) string {
	for _, node := range nodes {
		if node.NodeID == nodeID {
			return node.Status
		}
	}
	return ""
}
