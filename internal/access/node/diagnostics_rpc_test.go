package node

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/observability/diagnostics"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
	"github.com/stretchr/testify/require"
)

func TestDiagnosticsRPCBinaryCodecRoundTrip(t *testing.T) {
	startedAt := time.Unix(100, 123).UTC()
	eventAt := time.Unix(101, 456).UTC()
	req := diagnosticsRequest{Query: diagnostics.Query{
		TraceID:     "tr-1",
		ClientMsgNo: "cm-1",
		ChannelKey:  "group:g1",
		MessageSeq:  42,
		Stage:       diagnostics.Stage("channel_append"),
		Result:      diagnostics.ResultError,
		Limit:       10,
	}}
	body, err := encodeDiagnosticsRequestBinary(req)
	require.NoError(t, err)
	require.True(t, isDiagnosticsRequestBinary(body))

	gotReq, err := decodeDiagnosticsRequest(body)
	require.NoError(t, err)
	require.Equal(t, req, gotReq)

	resp := diagnosticsResponse{Status: rpcStatusOK, Result: diagnostics.QueryResult{
		Scope:       "local_node",
		NodeID:      2,
		TraceID:     "tr-1",
		ClientMsgNo: "cm-1",
		ChannelKey:  "group:g1",
		MessageSeq:  42,
		Query:       req.Query,
		Status:      diagnostics.StatusError,
		StartedAt:   startedAt,
		DurationMS:  17,
		Summary: diagnostics.QuerySummary{
			SlowestStage:      "delivery_push",
			SlowestDurationMS: 11,
			ErrorStage:        "channel_append",
			ErrorCode:         string(diagnostics.ErrorCodeUnknown),
		},
		Events: []diagnostics.Event{{
			TraceID:      "tr-1",
			SpanID:       "sp-1",
			ParentSpanID: "sp-0",
			Stage:        diagnostics.Stage("channel_append"),
			At:           eventAt,
			Duration:     3 * time.Millisecond,
			NodeID:       2,
			PeerNodeID:   3,
			SlotID:       4,
			ChannelKey:   "group:g1",
			ClientMsgNo:  "cm-1",
			MessageSeq:   42,
			RangeStart:   40,
			RangeEnd:     45,
			Service:      "channel",
			Result:       diagnostics.ResultError,
			ErrorCode:    diagnostics.ErrorCodeUnknown,
			Error:        "append failed",
			Attempt:      2,
			QueueDepth:   8,
			ReplicaRole:  "leader",
			SampleReason: "sampled",
		}},
		Notes: []string{"first note", "second note"},
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

func TestDiagnosticsRPCDisabledStoreReturnsNotFound(t *testing.T) {
	query := diagnostics.Query{TraceID: "tr-disabled", ClientMsgNo: "cm-disabled", Limit: 3}
	adapter := New(Options{})
	body, err := encodeDiagnosticsRequestBinary(diagnosticsRequest{Query: query})
	require.NoError(t, err)

	respBody, err := adapter.handleDiagnosticsRPC(context.Background(), body)
	require.NoError(t, err)
	resp, err := decodeDiagnosticsResponse(respBody)
	require.NoError(t, err)

	require.Equal(t, rpcStatusOK, resp.Status)
	require.Equal(t, diagnostics.StatusNotFound, resp.Result.Status)
	require.Equal(t, query, resp.Result.Query)
	require.Empty(t, resp.Result.Events)
	require.Equal(t, []string{"diagnostics store is disabled on this node"}, resp.Result.Notes)
}

func TestDiagnosticsRPCClientRejectsNonOKStatus(t *testing.T) {
	respBody, err := encodeDiagnosticsResponse(diagnosticsResponse{Status: rpcStatusRejected})
	require.NoError(t, err)
	cluster := &stubDiagnosticsCluster{response: respBody}

	_, err = NewClient(cluster).QueryDiagnostics(context.Background(), 2, diagnostics.Query{TraceID: "tr-1"})

	require.Error(t, err)
	require.Contains(t, err.Error(), "unexpected diagnostics status")
}

func TestDiagnosticsRPCRejectsTruncatedBinaryPayloads(t *testing.T) {
	reqBody, err := encodeDiagnosticsRequestBinary(diagnosticsRequest{Query: diagnostics.Query{TraceID: "tr-1"}})
	require.NoError(t, err)
	_, err = decodeDiagnosticsRequest(reqBody[:len(reqBody)-1])
	require.Error(t, err)

	respBody, err := encodeDiagnosticsResponse(diagnosticsResponse{
		Status: rpcStatusOK,
		Result: diagnostics.QueryResult{
			Scope:  "local_node",
			Status: diagnostics.StatusOK,
			Events: []diagnostics.Event{{TraceID: "tr-1"}},
		},
	})
	require.NoError(t, err)
	_, err = decodeDiagnosticsResponse(respBody[:len(respBody)-1])
	require.Error(t, err)
}

func TestDiagnosticsRPCRejectsOversizedDecodedCollections(t *testing.T) {
	events := make([]diagnostics.Event, maxDiagnosticsEvents+1)
	respBody, err := encodeDiagnosticsResponse(diagnosticsResponse{Status: rpcStatusOK, Result: diagnostics.QueryResult{Events: events}})
	require.NoError(t, err)
	_, err = decodeDiagnosticsResponse(respBody)
	require.Error(t, err)

	notes := make([]string, maxDiagnosticsNotes+1)
	respBody, err = encodeDiagnosticsResponse(diagnosticsResponse{Status: rpcStatusOK, Result: diagnostics.QueryResult{Notes: notes}})
	require.NoError(t, err)
	_, err = decodeDiagnosticsResponse(respBody)
	require.Error(t, err)
}

func TestDiagnosticsRPCRejectsOversizedDecodedStringsAndBodies(t *testing.T) {
	largeTraceID := strings.Repeat("x", maxDiagnosticsStringBytes+1)
	reqBody, err := encodeDiagnosticsRequestBinary(diagnosticsRequest{Query: diagnostics.Query{TraceID: largeTraceID}})
	require.NoError(t, err)
	_, err = decodeDiagnosticsRequest(reqBody)
	require.Error(t, err)

	oversized := append([]byte(nil), diagnosticsRequestMagic[:]...)
	oversized = append(oversized, make([]byte, maxDiagnosticsBodyBytes+1)...)
	_, err = decodeDiagnosticsRequest(oversized)
	require.Error(t, err)
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

type stubDiagnosticsCluster struct {
	response []byte
}

func (c *stubDiagnosticsCluster) RPCMux() *transport.RPCMux { return transport.NewRPCMux() }

func (c *stubDiagnosticsCluster) LeaderOf(multiraft.SlotID) (multiraft.NodeID, error) { return 0, nil }

func (c *stubDiagnosticsCluster) IsLocal(multiraft.NodeID) bool { return false }

func (c *stubDiagnosticsCluster) SlotForKey(string) multiraft.SlotID { return 0 }

func (c *stubDiagnosticsCluster) RPCService(context.Context, multiraft.NodeID, multiraft.SlotID, uint8, []byte) ([]byte, error) {
	return c.response, nil
}

func (c *stubDiagnosticsCluster) PeersForSlot(multiraft.SlotID) []multiraft.NodeID { return nil }
