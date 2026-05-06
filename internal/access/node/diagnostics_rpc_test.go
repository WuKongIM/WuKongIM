package node

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/observability/diagnostics"
	"github.com/stretchr/testify/require"
)

func TestDiagnosticsRPCBinaryCodecRoundTrip(t *testing.T) {
	req := diagnosticsRequest{Query: diagnostics.Query{TraceID: "tr-1", Result: diagnostics.ResultError, Limit: 10}}
	body, err := encodeDiagnosticsRequestBinary(req)
	require.NoError(t, err)
	require.True(t, isDiagnosticsRequestBinary(body))

	gotReq, err := decodeDiagnosticsRequest(body)
	require.NoError(t, err)
	require.Equal(t, req, gotReq)

	resp := diagnosticsResponse{Status: rpcStatusOK, Result: diagnostics.QueryResult{
		Scope:  "local_node",
		NodeID: 2,
		Status: diagnostics.StatusError,
		Events: []diagnostics.Event{{TraceID: "tr-1", Stage: diagnostics.Stage("channel_append"), Result: diagnostics.ResultError, Attempt: 2, QueueDepth: 8, ReplicaRole: "leader", SampleReason: "sampled"}},
	}}
	respBody, err := encodeDiagnosticsResponse(resp)
	require.NoError(t, err)
	require.True(t, isDiagnosticsResponseBinary(respBody))

	gotResp, err := decodeDiagnosticsResponse(respBody)
	require.NoError(t, err)
	require.Equal(t, resp, gotResp)
}

func TestDiagnosticsRPCRejectsJSONPayload(t *testing.T) {
	adapter := New(Options{Diagnostics: stubDiagnosticsProvider{}})
	_, err := adapter.handleDiagnosticsRPC(context.Background(), []byte(`{"query":{"trace_id":"tr-1"}}`))
	require.Error(t, err)
}

func TestDiagnosticsRPCReturnsLocalResult(t *testing.T) {
	want := diagnostics.QueryResult{Scope: "local_node", NodeID: 2, Status: diagnostics.StatusOK}
	adapter := New(Options{Diagnostics: stubDiagnosticsProvider{result: want}})

	body, err := encodeDiagnosticsRequestBinary(diagnosticsRequest{Query: diagnostics.Query{TraceID: "tr-1"}})
	require.NoError(t, err)
	respBody, err := adapter.handleDiagnosticsRPC(context.Background(), body)
	require.NoError(t, err)
	resp, err := decodeDiagnosticsResponse(respBody)
	require.NoError(t, err)

	require.Equal(t, rpcStatusOK, resp.Status)
	require.Equal(t, want, resp.Result)
}

func TestDiagnosticsRPCClientCallsRemoteNode(t *testing.T) {
	network := newFakeClusterNetwork(map[uint64][]uint64{}, map[uint64]uint64{})
	node1 := network.cluster(1)
	node2 := network.cluster(2)
	want := diagnostics.QueryResult{Scope: "local_node", NodeID: 2, Status: diagnostics.StatusOK}
	New(Options{Cluster: node2, Diagnostics: stubDiagnosticsProvider{result: want}})

	got, err := NewClient(node1).QueryDiagnostics(context.Background(), 2, diagnostics.Query{TraceID: "tr-1"})

	require.NoError(t, err)
	require.Equal(t, want, got)
}

type stubDiagnosticsProvider struct {
	result diagnostics.QueryResult
}

func (p stubDiagnosticsProvider) QueryDiagnostics(context.Context, diagnostics.Query) diagnostics.QueryResult {
	return p.result
}
