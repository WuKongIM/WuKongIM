package diagnostics

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
)

// F08/F12: an original upper trace maps an existing forward event to its own
// summary. One shared RPC may be linked to two originals without selecting a
// physical parent or copying one caller's token into the other.
func TestAttemptSinkLinksExistingForwardEventToEveryExactOriginal(t *testing.T) {
	summaries := attemptStoreFixture(t)
	global := NewStore(StoreOptions{Capacity: 2})
	sink := NewSendTraceSink(global, nil).WithAttemptStore(summaries)
	restore := sendtrace.SetSink(sink)
	defer restore()
	at := time.Unix(10, 0)
	keys := []sendtrace.AttemptKey{attemptStoreKey(1), attemptStoreKey(2)}
	traces := []string{"00112233445566778899aabbccddeeff", "11112233445566778899aabbccddeeff"}
	ref := sendtrace.RPCReference{State: "bound", TraceID: "0123456789abcdef0123456789abcdef/8000000000000001"}
	for i, key := range keys {
		token := sendtrace.BeginGatewayAttempt(key, at)
		sendtrace.BindGatewayAttemptTrace(token, traces[i])
		sink.RecordSendTrace(sendtrace.Event{TraceID: traces[i], Stage: sendtrace.StageChannelAppendForward, At: at, Duration: time.Second, Result: sendtrace.ResultOK, RPCReference: &ref})
	}
	// Existing global payload retention is deliberately lost before lookup.
	for i := 0; i < 3; i++ {
		global.Record(Event{TraceID: "overwritten", Stage: "message.send_durable", At: at})
	}
	result := summaries.QuerySendAttempts(context.Background(), keys)
	for i, row := range result.Results {
		if len(row.Candidates) != 1 || row.Candidates[0].UpperTraceID != traces[i] || len(row.Candidates[0].RPCReferences) != 1 || row.Candidates[0].RPCReferences[0] != ref {
			t.Fatalf("original %d borrowed/lost reference: %+v", i, row)
		}
	}
	ref.TraceID = "caller-mutated"
	again := summaries.QuerySendAttempts(context.Background(), keys)
	if again.Results[0].Candidates[0].RPCReferences[0].TraceID == "caller-mutated" {
		t.Fatal("summary retained mutable reference backing")
	}
}
func TestAttemptSinkDoesNotInventTraceLinkForMissingOrReusedUpperIdentity(t *testing.T) {
	summaries := attemptStoreFixture(t)
	sink := NewSendTraceSink(NewStore(StoreOptions{Capacity: 8}), nil).WithAttemptStore(summaries)
	at := time.Unix(10, 0)
	token := summaries.BeginSendAttempt(attemptStoreKey(1), at)
	summaries.BindAttemptTrace(token, "00112233445566778899aabbccddeeff")
	ref := sendtrace.RPCReference{State: "bound", TraceID: "0123456789abcdef0123456789abcdef/1"}
	sink.RecordSendTrace(sendtrace.Event{TraceID: "11112233445566778899aabbccddeeff", Stage: sendtrace.StageChannelAppendForward, At: at, RPCReference: &ref})
	if got := summaries.QuerySendAttempts(context.Background(), []sendtrace.AttemptKey{attemptStoreKey(1)}); len(got.Results[0].Candidates[0].RPCReferences) != 0 {
		t.Fatal("unmapped forward borrowed recent invocation")
	}
	other := summaries.BeginSendAttempt(attemptStoreKey(2), at)
	summaries.BindAttemptTrace(other, "00112233445566778899aabbccddeeff")
	sink.RecordSendTrace(sendtrace.Event{TraceID: "00112233445566778899aabbccddeeff", Stage: sendtrace.StageChannelAppendForward, At: at, RPCReference: &ref})
	for _, row := range summaries.QuerySendAttempts(context.Background(), []sendtrace.AttemptKey{attemptStoreKey(1), attemptStoreKey(2)}).Results {
		for _, candidate := range row.Candidates {
			if len(candidate.RPCReferences) > 0 && !candidate.TraceAmbiguous {
				t.Fatal("reused trace identity hid ambiguous binding")
			}
		}
	}
}
