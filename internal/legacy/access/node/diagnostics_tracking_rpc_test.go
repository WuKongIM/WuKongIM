package node

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/legacy/observability/diagnostics"
	"github.com/stretchr/testify/require"
)

func TestDiagnosticsTrackingCodecRoundTrip(t *testing.T) {
	req := diagnosticsTrackingRequest{
		Op: diagnosticsTrackingOpAdd,
		Rule: diagnostics.TrackingRuleInput{
			ID:         "rule-1",
			Target:     diagnostics.TrackingTargetSenderUID,
			UID:        "u1",
			TTL:        time.Hour,
			SampleRate: 1,
		},
	}

	body, err := encodeDiagnosticsTrackingRequest(req)
	require.NoError(t, err)
	got, err := decodeDiagnosticsTrackingRequest(body)
	require.NoError(t, err)

	require.Equal(t, req.Op, got.Op)
	require.Equal(t, req.Rule.ID, got.Rule.ID)
	require.Equal(t, req.Rule.Target, got.Rule.Target)
	require.Equal(t, req.Rule.UID, got.Rule.UID)
	require.Equal(t, req.Rule.TTL, got.Rule.TTL)
	require.Equal(t, req.Rule.SampleRate, got.Rule.SampleRate)
}

func TestDiagnosticsTrackingAdapterAddListDelete(t *testing.T) {
	provider := &diagnosticsTrackingProviderStub{}
	adapter := New(Options{DiagnosticsTracking: provider})

	addBody, err := encodeDiagnosticsTrackingRequest(diagnosticsTrackingRequest{
		Op: diagnosticsTrackingOpAdd,
		Rule: diagnostics.TrackingRuleInput{
			ID:         "rule-1",
			Target:     diagnostics.TrackingTargetChannel,
			ChannelKey: "channel/2/ZzE",
			TTL:        time.Minute,
			SampleRate: 1,
		},
	})
	require.NoError(t, err)
	addRespBody, err := adapter.handleDiagnosticsTrackingRPC(context.Background(), addBody)
	require.NoError(t, err)
	addResp, err := decodeDiagnosticsTrackingResponse(addRespBody)
	require.NoError(t, err)
	require.Equal(t, rpcStatusOK, addResp.Status)
	require.Equal(t, "rule-1", provider.added.ID)

	listBody, err := encodeDiagnosticsTrackingRequest(diagnosticsTrackingRequest{Op: diagnosticsTrackingOpList})
	require.NoError(t, err)
	_, err = adapter.handleDiagnosticsTrackingRPC(context.Background(), listBody)
	require.NoError(t, err)
	require.True(t, provider.listed)

	deleteBody, err := encodeDiagnosticsTrackingRequest(diagnosticsTrackingRequest{Op: diagnosticsTrackingOpDelete, RuleID: "rule-1"})
	require.NoError(t, err)
	_, err = adapter.handleDiagnosticsTrackingRPC(context.Background(), deleteBody)
	require.NoError(t, err)
	require.Equal(t, "rule-1", provider.deleted)
}

func TestDiagnosticsTrackingClientCallsRemoteNode(t *testing.T) {
	network := newFakeClusterNetwork(map[uint64][]uint64{}, map[uint64]uint64{})
	node1 := network.cluster(1)
	node2 := network.cluster(2)
	New(Options{Cluster: node2, DiagnosticsTracking: &diagnosticsTrackingProviderStub{
		addRule: diagnostics.TrackingRule{ID: "rule-remote", Target: diagnostics.TrackingTargetSenderUID, UID: "u1"},
	}})

	got, err := NewClient(node1).AddDiagnosticsTrackingRule(context.Background(), 2, diagnostics.TrackingRuleInput{
		ID: "rule-remote", Target: diagnostics.TrackingTargetSenderUID, UID: "u1", TTL: time.Minute, SampleRate: 1,
	})

	require.NoError(t, err)
	require.Equal(t, "rule-remote", got.ID)
}

type diagnosticsTrackingProviderStub struct {
	added   diagnostics.TrackingRuleInput
	addRule diagnostics.TrackingRule
	listed  bool
	deleted string
}

func (s *diagnosticsTrackingProviderStub) AddDiagnosticsTrackingRule(_ context.Context, input diagnostics.TrackingRuleInput) (diagnostics.TrackingRule, error) {
	s.added = input
	if s.addRule.ID != "" {
		return s.addRule, nil
	}
	return diagnostics.TrackingRule{ID: input.ID, Target: input.Target, UID: input.UID, ChannelKey: input.ChannelKey, SampleRate: input.SampleRate}, nil
}

func (s *diagnosticsTrackingProviderStub) ListDiagnosticsTrackingRules(context.Context) ([]diagnostics.TrackingRule, error) {
	s.listed = true
	return []diagnostics.TrackingRule{{ID: "rule-1"}}, nil
}

func (s *diagnosticsTrackingProviderStub) DeleteDiagnosticsTrackingRule(_ context.Context, ruleID string) error {
	s.deleted = ruleID
	return nil
}
