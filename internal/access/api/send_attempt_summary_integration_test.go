//go:build integration

package api

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/observability/diagnostics"
	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
)

// F02/F05/F12: publishing a new field is insufficient. This actual HTTP query
// must retrieve the original record after its old global event has disappeared,
// while a newer retry of the same logical message remains separately retained.
func TestAttemptSelectorRetrievesOriginalAfterGlobalRingOverwrite(t *testing.T) {
	at := time.Unix(1700000000, 0)
	global := diagnostics.NewStore(diagnostics.StoreOptions{Capacity: 2})
	summaries := diagnostics.NewAttemptStore(diagnostics.AttemptStoreOptions{Enabled: true, NodeID: 1, Epoch: [16]byte{1}})
	first := sendtrace.AttemptKey{MessageFingerprint: sendtrace.MessageFingerprint("private-logical-message"), ClientSeq: 1, ConnectFingerprint: [16]byte{7}}
	retry := first
	retry.ClientSeq = 2
	originalTrace := "00112233445566778899aabbccddeeff"
	retryTrace := "11112233445566778899aabbccddeeff"
	originalToken := summaries.BeginSendAttempt(first, at)
	retryToken := summaries.BeginSendAttempt(retry, at)
	summaries.BindAttemptTrace(originalToken, originalTrace)
	summaries.BindAttemptTrace(retryToken, retryTrace)
	originalRef := sendtrace.RPCReference{State: "bound", TraceID: "0123456789abcdef0123456789abcdef/8000000000000001"}
	summaries.RecordAttemptRPC(originalToken, originalRef)
	summaries.RecordAttemptRPC(retryToken, sendtrace.RPCReference{State: "bound", TraceID: "0123456789abcdef0123456789abcdef/8000000000000002"})
	for _, stage := range []sendtrace.Stage{sendtrace.StageGatewayAsyncDispatchWait, sendtrace.StageGatewayMessagesSend, sendtrace.StageGatewayWriteSendack} {
		summaries.RecordAttemptPhase(originalToken, sendtrace.AttemptPhase{Stage: stage, StartedAt: at, Duration: 500 * time.Millisecond, Result: sendtrace.ResultOK, EndDerived: true})
	}
	global.Record(diagnostics.Event{TraceID: originalTrace, Stage: diagnostics.Stage(sendtrace.StageGatewayMessagesSend), ClientMsgNo: "private-logical-message", At: at})
	for i := 0; i < 3; i++ {
		global.Record(diagnostics.Event{TraceID: "unrelated-latest", Stage: diagnostics.Stage(sendtrace.StageGatewayMessagesSend), At: at})
	}
	if got := global.Query(context.Background(), diagnostics.Query{TraceID: originalTrace}); len(got.Events) != 0 {
		t.Fatal("fixture did not actually overwrite old original event")
	}
	server := httptest.NewServer(New(Options{DebugAPIEnabled: true, BenchToken: "attempt-token", Diagnostics: attemptStoreReader{global}, SendAttempts: summaries}).Handler())
	defer server.Close()
	requestBody, err := json.Marshal(map[string]any{"version": 1, "keys": []map[string]any{{"message_fingerprint": hex.EncodeToString(first.MessageFingerprint[:]), "client_seq": 1, "connect_packet_fingerprint": hex.EncodeToString(first.ConnectFingerprint[:])}}})
	if err != nil {
		t.Fatal(err)
	}
	status, raw := attemptPOST(t, server, string(requestBody), "attempt-token")
	if status != http.StatusOK || len(raw) > 64<<10 {
		t.Fatalf("summary query status=%d bytes=%d", status, len(raw))
	}
	var response struct {
		Version uint64 `json:"version"`
		Results []struct {
			Status     string `json:"status"`
			Candidates []struct {
				UpperTraceID  string                   `json:"upper_trace_id"`
				RPCReferences []sendtrace.RPCReference `json:"rpc_references"`
				Phases        json.RawMessage          `json:"phases"`
			} `json:"candidates"`
		} `json:"results"`
	}
	if err = json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	if response.Version != 1 || len(response.Results) != 1 || response.Results[0].Status == "exact" || len(response.Results[0].Candidates) != 1 {
		t.Fatalf("missing/overstated original candidate: %s", raw)
	}
	row := response.Results[0].Candidates[0]
	if row.UpperTraceID != originalTrace || len(row.RPCReferences) != 1 || row.RPCReferences[0] != originalRef || len(row.Phases) == 0 {
		t.Fatalf("fields exist but true original record is unavailable/wrong: %s", raw)
	}
	var envelope map[string]json.RawMessage
	if err = json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope["rpc_results"]) > 0 && string(envelope["rpc_results"]) != "null" && string(envelope["rpc_results"]) != "[]" {
		t.Fatal("summary mode unexpectedly executed RPC lookup")
	}
}
