package app

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/observability/diagnostics"
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
