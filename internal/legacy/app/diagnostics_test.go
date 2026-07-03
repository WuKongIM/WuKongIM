package app

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/legacy/observability/diagnostics"
	"github.com/stretchr/testify/require"
)

func TestManagementDiagnosticsReaderRoutesLocalNodeToApp(t *testing.T) {
	local := &managementDiagnosticsLocalFake{
		result: diagnostics.QueryResult{
			NodeID: 1,
			Status: diagnostics.StatusOK,
			Events: []diagnostics.Event{{TraceID: "tr-local"}},
		},
	}
	remote := &managementDiagnosticsRemoteFake{err: errors.New("remote should not be called")}
	reader := managementDiagnosticsReader{
		localNodeID: 1,
		local:       local,
		remote:      remote,
	}

	got, err := reader.QueryNodeDiagnostics(context.Background(), 1, diagnostics.Query{TraceID: "tr-local"})

	require.NoError(t, err)
	require.Equal(t, diagnostics.StatusOK, got.Status)
	require.Equal(t, "tr-local", got.Events[0].TraceID)
	require.Equal(t, []diagnostics.Query{{TraceID: "tr-local"}}, local.queries)
	require.Empty(t, remote.nodeIDs)
}

func TestManagementDiagnosticsReaderReturnsErrorWhenRemoteClientMissing(t *testing.T) {
	local := &managementDiagnosticsLocalFake{
		result: diagnostics.QueryResult{NodeID: 1, Status: diagnostics.StatusOK},
	}
	reader := managementDiagnosticsReader{
		localNodeID: 1,
		local:       local,
	}

	_, err := reader.QueryNodeDiagnostics(context.Background(), 2, diagnostics.Query{TraceID: "tr-remote"})

	require.Error(t, err)
	require.Contains(t, err.Error(), "node client not configured")
	require.Empty(t, local.queries)
}

func TestManagementDiagnosticsReaderRoutesRemoteNodeToNodeClient(t *testing.T) {
	local := &managementDiagnosticsLocalFake{
		result: diagnostics.QueryResult{NodeID: 1, Status: diagnostics.StatusOK},
	}
	remote := &managementDiagnosticsRemoteFake{
		result: diagnostics.QueryResult{
			NodeID: 2,
			Status: diagnostics.StatusOK,
			Events: []diagnostics.Event{{TraceID: "tr-remote", NodeID: 2}},
		},
	}
	reader := managementDiagnosticsReader{
		localNodeID: 1,
		local:       local,
		remote:      remote,
	}

	got, err := reader.QueryNodeDiagnostics(context.Background(), 2, diagnostics.Query{TraceID: "tr-remote"})

	require.NoError(t, err)
	require.Equal(t, uint64(2), got.NodeID)
	require.Equal(t, "tr-remote", got.Events[0].TraceID)
	require.Empty(t, local.queries)
	require.Equal(t, []uint64{2}, remote.nodeIDs)
	require.Equal(t, []diagnostics.Query{{TraceID: "tr-remote"}}, remote.queries)
}

func TestManagementDiagnosticsTrackingReaderRoutesLocalNodeToApp(t *testing.T) {
	local := &managementDiagnosticsTrackingLocalFake{
		addRule: diagnostics.TrackingRule{ID: "rule-local"},
		rules:   []diagnostics.TrackingRule{{ID: "rule-local"}},
	}
	remote := &managementDiagnosticsTrackingRemoteFake{err: errors.New("remote should not be called")}
	reader := managementDiagnosticsTrackingReader{localNodeID: 1, local: local, remote: remote}

	got, err := reader.AddNodeDiagnosticsTrackingRule(context.Background(), 1, diagnostics.TrackingRuleInput{ID: "rule-local"})
	require.NoError(t, err)
	require.Equal(t, "rule-local", got.ID)

	listed, err := reader.ListNodeDiagnosticsTrackingRules(context.Background(), 1)
	require.NoError(t, err)
	require.Len(t, listed, 1)

	require.NoError(t, reader.DeleteNodeDiagnosticsTrackingRule(context.Background(), 1, "rule-local"))
	require.Equal(t, "rule-local", local.deleted)
	require.Equal(t, uint64(0), remote.nodeID)
}

func TestManagementDiagnosticsTrackingReaderRoutesRemoteNodeToClient(t *testing.T) {
	local := &managementDiagnosticsTrackingLocalFake{err: errors.New("local should not be called")}
	remote := &managementDiagnosticsTrackingRemoteFake{addRule: diagnostics.TrackingRule{ID: "rule-remote"}}
	reader := managementDiagnosticsTrackingReader{localNodeID: 1, local: local, remote: remote}

	got, err := reader.AddNodeDiagnosticsTrackingRule(context.Background(), 2, diagnostics.TrackingRuleInput{ID: "rule-remote"})
	require.NoError(t, err)
	require.Equal(t, uint64(2), remote.nodeID)
	require.Equal(t, "rule-remote", got.ID)
}

type managementDiagnosticsLocalFake struct {
	queries []diagnostics.Query
	result  diagnostics.QueryResult
}

func (f *managementDiagnosticsLocalFake) QueryDiagnostics(_ context.Context, query diagnostics.Query) diagnostics.QueryResult {
	f.queries = append(f.queries, query)
	return f.result
}

type managementDiagnosticsRemoteFake struct {
	nodeIDs []uint64
	queries []diagnostics.Query
	result  diagnostics.QueryResult
	err     error
}

func (f *managementDiagnosticsRemoteFake) QueryDiagnostics(_ context.Context, nodeID uint64, query diagnostics.Query) (diagnostics.QueryResult, error) {
	f.nodeIDs = append(f.nodeIDs, nodeID)
	f.queries = append(f.queries, query)
	return f.result, f.err
}

type managementDiagnosticsTrackingLocalFake struct {
	addRule diagnostics.TrackingRule
	rules   []diagnostics.TrackingRule
	deleted string
	err     error
}

func (f *managementDiagnosticsTrackingLocalFake) AddDiagnosticsTrackingRule(context.Context, diagnostics.TrackingRuleInput) (diagnostics.TrackingRule, error) {
	return f.addRule, f.err
}

func (f *managementDiagnosticsTrackingLocalFake) ListDiagnosticsTrackingRules(context.Context) ([]diagnostics.TrackingRule, error) {
	return f.rules, f.err
}

func (f *managementDiagnosticsTrackingLocalFake) DeleteDiagnosticsTrackingRule(_ context.Context, ruleID string) error {
	f.deleted = ruleID
	return f.err
}

type managementDiagnosticsTrackingRemoteFake struct {
	nodeID  uint64
	addRule diagnostics.TrackingRule
	rules   []diagnostics.TrackingRule
	deleted string
	err     error
}

func (f *managementDiagnosticsTrackingRemoteFake) AddDiagnosticsTrackingRule(_ context.Context, nodeID uint64, input diagnostics.TrackingRuleInput) (diagnostics.TrackingRule, error) {
	f.nodeID = nodeID
	return f.addRule, f.err
}

func (f *managementDiagnosticsTrackingRemoteFake) ListDiagnosticsTrackingRules(_ context.Context, nodeID uint64) ([]diagnostics.TrackingRule, error) {
	f.nodeID = nodeID
	return f.rules, f.err
}

func (f *managementDiagnosticsTrackingRemoteFake) DeleteDiagnosticsTrackingRule(_ context.Context, nodeID uint64, ruleID string) error {
	f.nodeID = nodeID
	f.deleted = ruleID
	return f.err
}
