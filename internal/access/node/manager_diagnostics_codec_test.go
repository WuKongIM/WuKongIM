package node

import (
	"reflect"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/observability/diagnostics"
)

func TestManagerDiagnosticsCodecRoundTripsPhysicalSlotReconciliationEvidence(t *testing.T) {
	query := diagnostics.Query{
		SlotID: 42,
		Stage:  diagnostics.Stage("slot.preferred_leader_reconcile"),
		Limit:  25,
	}
	requestBody, err := encodeManagerDiagnosticsRequest(managerDiagnosticsRPCRequest{
		Op:    managerDiagnosticsOpQuery,
		Query: query,
	})
	if err != nil {
		t.Fatalf("encodeManagerDiagnosticsRequest() error = %v", err)
	}
	if !hasMagic(requestBody, managerDiagnosticsRequestMagic[:]) {
		t.Fatalf("request magic = %v, want current manager diagnostics request codec", requestBody[:len(managerDiagnosticsRequestMagic)])
	}
	request, err := decodeManagerDiagnosticsRequest(requestBody)
	if err != nil {
		t.Fatalf("decodeManagerDiagnosticsRequest() error = %v", err)
	}
	if !reflect.DeepEqual(request.Query, query) {
		t.Fatalf("request query = %#v, want %#v", request.Query, query)
	}

	event := diagnostics.Event{
		Stage:             diagnostics.Stage("slot.preferred_leader_reconcile"),
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
	responseBody, err := encodeManagerDiagnosticsResponse(managerDiagnosticsRPCResponse{
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
		t.Fatalf("encodeManagerDiagnosticsResponse() error = %v", err)
	}
	if !hasMagic(responseBody, managerDiagnosticsResponseMagic[:]) {
		t.Fatalf("response magic = %v, want current manager diagnostics response codec", responseBody[:len(managerDiagnosticsResponseMagic)])
	}
	response, err := decodeManagerDiagnosticsResponse(responseBody)
	if err != nil {
		t.Fatalf("decodeManagerDiagnosticsResponse() error = %v", err)
	}
	if !reflect.DeepEqual(response.Result.Query, query) {
		t.Fatalf("response query = %#v, want %#v", response.Result.Query, query)
	}
	if len(response.Result.Events) != 1 || !reflect.DeepEqual(response.Result.Events[0], event) {
		t.Fatalf("response events = %#v, want %#v", response.Result.Events, []diagnostics.Event{event})
	}
}

func TestManagerDiagnosticsCodecReadsVersionOneRequestAndResponse(t *testing.T) {
	legacyQuery := diagnostics.Query{TraceID: "legacy-trace", Limit: 10}
	requestBody := append([]byte(nil), managerDiagnosticsRequestMagicV1[:]...)
	requestBody = append(requestBody, managerDiagnosticsOpQueryID)
	requestBody = appendDiagnosticsQueryV2ForTest(requestBody, legacyQuery)
	requestBody = appendDiagnosticsTrackingRuleInput(requestBody, diagnostics.TrackingRuleInput{})
	requestBody = appendString(requestBody, "")

	request, err := decodeManagerDiagnosticsRequest(requestBody)
	if err != nil {
		t.Fatalf("decodeManagerDiagnosticsRequest(version 1) error = %v", err)
	}
	if request.Query.TraceID != legacyQuery.TraceID || request.Query.Limit != legacyQuery.Limit || request.Query.SlotID != 0 {
		t.Fatalf("legacy manager query = %#v, want trace/limit with zero SlotID", request.Query)
	}

	legacyEvent := diagnostics.Event{
		TraceID:      "legacy-trace",
		Stage:        diagnostics.Stage("gateway_send"),
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
	responseBody := append([]byte(nil), managerDiagnosticsResponseMagicV1[:]...)
	responseBody = appendString(responseBody, rpcStatusOK)
	responseBody = appendString(responseBody, "")
	responseBody = appendDiagnosticsQueryResultV3ForTest(responseBody, legacyResult)
	responseBody = appendDiagnosticsTrackingRule(responseBody, diagnostics.TrackingRule{})
	responseBody = appendDiagnosticsTrackingRules(responseBody, nil)

	response, err := decodeManagerDiagnosticsResponse(responseBody)
	if err != nil {
		t.Fatalf("decodeManagerDiagnosticsResponse(version 1) error = %v", err)
	}
	if response.Result.Query.SlotID != 0 || len(response.Result.Events) != 1 {
		t.Fatalf("legacy manager response = %#v, want one event and zero query SlotID", response.Result)
	}
	got := response.Result.Events[0]
	if got.RequestCount != 3 || got.RecordCount != 2 || got.ByteCount != 128 {
		t.Fatalf("legacy manager event batch counts = %d/%d/%d, want 3/2/128", got.RequestCount, got.RecordCount, got.ByteCount)
	}
	if got.Decision != "" || got.ActualLeaderID != 0 || got.PreferredLeaderID != 0 || got.RaftTerm != 0 || got.ConfigEpoch != 0 {
		t.Fatalf("legacy manager event reconciliation fields = %#v, want zero values", got)
	}
}
