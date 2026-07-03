package node

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRuntimeSummaryRPCReturnsLocalSummary(t *testing.T) {
	provider := &stubRuntimeSummaryProvider{summary: RuntimeSummary{
		NodeID:               2,
		ActiveOnline:         3,
		ClosingOnline:        1,
		TotalOnline:          4,
		GatewaySessions:      5,
		SessionsByListener:   map[string]int{"tcp": 3, "ws": 2},
		AcceptingNewSessions: true,
		Draining:             true,
	}}
	adapter := New(Options{RuntimeSummary: provider})
	reqBody, err := encodeRuntimeSummaryRequestBinary(runtimeSummaryRequest{NodeID: 2})
	require.NoError(t, err)

	body, err := adapter.handleRuntimeSummaryRPC(context.Background(), reqBody)

	require.NoError(t, err)
	resp, err := decodeRuntimeSummaryResponse(body)
	require.NoError(t, err)
	require.Equal(t, rpcStatusOK, resp.Status)
	require.Equal(t, provider.summary, resp.Summary)
	require.Equal(t, 1, provider.calls)
}

func TestRuntimeSummaryClientCallsRemoteNode(t *testing.T) {
	network := newFakeClusterNetwork(map[uint64][]uint64{}, map[uint64]uint64{})
	node1 := network.cluster(1)
	node2 := network.cluster(2)
	want := RuntimeSummary{NodeID: 2, ActiveOnline: 7, SessionsByListener: map[string]int{"tcp": 7}, AcceptingNewSessions: true}
	New(Options{Cluster: node2, RuntimeSummary: &stubRuntimeSummaryProvider{summary: want}})

	got, err := NewClient(node1).RuntimeSummary(context.Background(), 2)

	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestRuntimeSummaryBinaryCodecRoundTrip(t *testing.T) {
	req := runtimeSummaryRequest{NodeID: 3}
	reqBody, err := encodeRuntimeSummaryRequestBinary(req)
	require.NoError(t, err)
	require.True(t, isRuntimeSummaryRequestBinary(reqBody))

	gotReq, err := decodeRuntimeSummaryRequest(reqBody)
	require.NoError(t, err)
	require.Equal(t, req, gotReq)

	resp := runtimeSummaryResponse{
		Status: rpcStatusOK,
		Summary: RuntimeSummary{
			NodeID:               3,
			ActiveOnline:         11,
			ClosingOnline:        2,
			TotalOnline:          13,
			GatewaySessions:      17,
			SessionsByListener:   map[string]int{"tcp": 7, "ws": 6},
			AcceptingNewSessions: true,
			Draining:             true,
			Unknown:              true,
		},
	}
	respBody, err := encodeRuntimeSummaryResponse(resp)
	require.NoError(t, err)
	require.True(t, isRuntimeSummaryResponseBinary(respBody))

	gotResp, err := decodeRuntimeSummaryResponse(respBody)
	require.NoError(t, err)
	require.Equal(t, resp, gotResp)
}

func TestRuntimeSummaryRPCRejectsJSONPayload(t *testing.T) {
	adapter := New(Options{RuntimeSummary: &stubRuntimeSummaryProvider{}})

	_, err := adapter.handleRuntimeSummaryRPC(context.Background(), []byte(`{"node_id":2}`))
	require.Error(t, err)
}

type stubRuntimeSummaryProvider struct {
	summary RuntimeSummary
	err     error
	calls   int
}

func (p *stubRuntimeSummaryProvider) LocalRuntimeSummary(context.Context) (RuntimeSummary, error) {
	p.calls++
	return p.summary, p.err
}
