package node

import (
	"reflect"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/observability/diagnostics"
)

func TestDiagnosticsCodecRoundTripsPhysicalSlotReconciliationEvidence(t *testing.T) {
	query := diagnostics.Query{
		TraceID: "trace-slot-42",
		SlotID:  42,
		Stage:   diagnostics.Stage("slot.preferred_leader_reconcile"),
		Limit:   20,
	}
	requestBody, err := encodeDiagnosticsRequestBinary(diagnosticsRequest{Query: query})
	if err != nil {
		t.Fatalf("encodeDiagnosticsRequestBinary() error = %v", err)
	}
	if !hasMagic(requestBody, diagnosticsRequestMagic[:]) {
		t.Fatalf("request magic = %v, want current diagnostics request codec", requestBody[:len(diagnosticsRequestMagic)])
	}
	request, err := decodeDiagnosticsRequest(requestBody)
	if err != nil {
		t.Fatalf("decodeDiagnosticsRequest() error = %v", err)
	}
	if !reflect.DeepEqual(request.Query, query) {
		t.Fatalf("request query = %#v, want %#v", request.Query, query)
	}

	event := diagnostics.Event{
		Stage:             diagnostics.Stage("slot.preferred_leader_reconcile"),
		At:                time.Date(2026, 7, 21, 3, 0, 0, 0, time.UTC),
		Duration:          350 * time.Millisecond,
		NodeID:            2,
		SlotID:            42,
		Service:           "cluster.preferred_leader",
		Result:            diagnostics.ResultSkipped,
		Decision:          "preferred_lagging",
		ActualLeaderID:    3,
		PreferredLeaderID: 2,
		RaftTerm:          19,
		ConfigEpoch:       27,
	}
	responseBody, err := encodeDiagnosticsResponse(diagnosticsResponse{
		Status: rpcStatusOK,
		Result: diagnostics.QueryResult{
			Scope:  "local_node",
			NodeID: 2,
			Query:  query,
			Status: diagnostics.StatusOK,
			Events: []diagnostics.Event{event},
		},
	})
	if err != nil {
		t.Fatalf("encodeDiagnosticsResponse() error = %v", err)
	}
	if !hasMagic(responseBody, diagnosticsResponseMagic[:]) {
		t.Fatalf("response magic = %v, want current diagnostics response codec", responseBody[:len(diagnosticsResponseMagic)])
	}
	response, err := decodeDiagnosticsResponse(responseBody)
	if err != nil {
		t.Fatalf("decodeDiagnosticsResponse() error = %v", err)
	}
	if !reflect.DeepEqual(response.Result.Query, query) {
		t.Fatalf("response query = %#v, want %#v", response.Result.Query, query)
	}
	if len(response.Result.Events) != 1 || !reflect.DeepEqual(response.Result.Events[0], event) {
		t.Fatalf("response events = %#v, want %#v", response.Result.Events, []diagnostics.Event{event})
	}
}

func TestDiagnosticsCodecReadsPreviousRequestAndResponseVersions(t *testing.T) {
	legacyQuery := diagnostics.Query{TraceID: "legacy-trace", Limit: 10}
	requestBody := append([]byte(nil), diagnosticsRequestMagicV2[:]...)
	requestBody = appendDiagnosticsQueryV2ForTest(requestBody, legacyQuery)

	request, err := decodeDiagnosticsRequest(requestBody)
	if err != nil {
		t.Fatalf("decodeDiagnosticsRequest(version 2) error = %v", err)
	}
	if request.Query.TraceID != legacyQuery.TraceID || request.Query.Limit != legacyQuery.Limit || request.Query.SlotID != 0 {
		t.Fatalf("legacy request query = %#v, want trace/limit with zero SlotID", request.Query)
	}

	legacyEvent := diagnostics.Event{
		TraceID:      "legacy-trace",
		Stage:        diagnostics.Stage("gateway_send"),
		At:           time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC),
		NodeID:       2,
		SlotID:       42,
		Result:       diagnostics.ResultOK,
		RequestCount: 3,
		RecordCount:  2,
		ByteCount:    128,
	}
	legacyResult := diagnostics.QueryResult{
		Scope:  "local_node",
		NodeID: 2,
		Query:  legacyQuery,
		Status: diagnostics.StatusOK,
		Events: []diagnostics.Event{legacyEvent},
	}
	responseBody := append([]byte(nil), diagnosticsResponseMagicV3[:]...)
	responseBody = appendString(responseBody, rpcStatusOK)
	responseBody = appendDiagnosticsQueryResultV3ForTest(responseBody, legacyResult)

	response, err := decodeDiagnosticsResponse(responseBody)
	if err != nil {
		t.Fatalf("decodeDiagnosticsResponse(version 3) error = %v", err)
	}
	if response.Result.Query.SlotID != 0 {
		t.Fatalf("legacy response query SlotID = %d, want 0", response.Result.Query.SlotID)
	}
	if len(response.Result.Events) != 1 {
		t.Fatalf("legacy response events = %#v, want one", response.Result.Events)
	}
	got := response.Result.Events[0]
	if got.RequestCount != 3 || got.RecordCount != 2 || got.ByteCount != 128 {
		t.Fatalf("legacy event batch counts = %d/%d/%d, want 3/2/128", got.RequestCount, got.RecordCount, got.ByteCount)
	}
	if got.Decision != "" || got.ActualLeaderID != 0 || got.PreferredLeaderID != 0 || got.RaftTerm != 0 || got.ConfigEpoch != 0 {
		t.Fatalf("legacy event reconciliation fields = %#v, want zero values", got)
	}
}

func TestDiagnosticsCodecRejectsPhysicalSlotIDOverflow(t *testing.T) {
	body := append([]byte(nil), diagnosticsRequestMagic[:]...)
	body = appendDiagnosticsQueryV2ForTest(body, diagnostics.Query{Limit: 10})
	body = appendUvarint(body, uint64(^uint32(0))+1)

	if _, err := decodeDiagnosticsRequest(body); err == nil {
		t.Fatal("decodeDiagnosticsRequest() error = nil, want physical Slot ID overflow error")
	}
}

func appendDiagnosticsQueryV2ForTest(dst []byte, query diagnostics.Query) []byte {
	dst = appendString(dst, query.TraceID)
	dst = appendString(dst, query.ClientMsgNo)
	dst = appendString(dst, query.ChannelKey)
	dst = appendString(dst, query.UID)
	dst = appendUvarint(dst, query.MessageSeq)
	dst = appendString(dst, string(query.Stage))
	dst = appendString(dst, string(query.Result))
	dst = appendNodeInt(dst, query.Limit)
	return dst
}

func appendDiagnosticsQueryResultV3ForTest(dst []byte, result diagnostics.QueryResult) []byte {
	dst = appendString(dst, result.Scope)
	dst = appendUvarint(dst, result.NodeID)
	dst = appendString(dst, result.TraceID)
	dst = appendString(dst, result.ClientMsgNo)
	dst = appendString(dst, result.ChannelKey)
	dst = appendString(dst, result.UID)
	dst = appendUvarint(dst, result.MessageSeq)
	dst = appendDiagnosticsQueryV2ForTest(dst, result.Query)
	dst = appendString(dst, string(result.Status))
	dst = appendDiagnosticsTime(dst, result.StartedAt)
	dst = appendNodeVarint(dst, result.DurationMS)
	dst = appendDiagnosticsSummary(dst, result.Summary)
	dst = appendDiagnosticsEventsV3ForTest(dst, result.Events)
	dst = appendDiagnosticsStringSlice(dst, result.Notes)
	return dst
}

func appendDiagnosticsEventsV3ForTest(dst []byte, events []diagnostics.Event) []byte {
	if events == nil {
		return append(dst, 0)
	}
	dst = append(dst, 1)
	dst = appendUvarint(dst, uint64(len(events)))
	for _, event := range events {
		dst = appendDiagnosticsEventV3ForTest(dst, event)
	}
	return dst
}

func appendDiagnosticsEventV3ForTest(dst []byte, event diagnostics.Event) []byte {
	dst = appendString(dst, event.TraceID)
	dst = appendString(dst, event.SpanID)
	dst = appendString(dst, event.ParentSpanID)
	dst = appendString(dst, string(event.Stage))
	dst = appendDiagnosticsTime(dst, event.At)
	dst = appendNodeVarint(dst, int64(event.Duration))
	dst = appendUvarint(dst, event.NodeID)
	dst = appendUvarint(dst, event.PeerNodeID)
	dst = appendUvarint(dst, uint64(event.SlotID))
	dst = appendString(dst, event.ChannelKey)
	dst = appendString(dst, event.ClientMsgNo)
	dst = appendUvarint(dst, event.MessageSeq)
	dst = appendUvarint(dst, event.RangeStart)
	dst = appendUvarint(dst, event.RangeEnd)
	dst = appendString(dst, event.Service)
	dst = appendString(dst, string(event.Result))
	dst = appendString(dst, string(event.ErrorCode))
	dst = appendString(dst, event.Error)
	dst = appendNodeInt(dst, event.Attempt)
	dst = appendNodeInt(dst, event.RequestCount)
	dst = appendNodeInt(dst, event.RecordCount)
	dst = appendNodeInt(dst, event.ByteCount)
	dst = appendNodeInt(dst, event.QueueDepth)
	dst = appendString(dst, event.ReplicaRole)
	dst = appendString(dst, event.SampleReason)
	return dst
}
