//go:build integration

package diagnostics

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
)

// A validated RPC identity is opaque: request hex width remains part of the
// lookup key. Losing its leading zeros can select no sample or another call.
func TestAttemptSummaryPreservesOpaqueRPCRequestLeadingZeros(t *testing.T) {
	store := NewAttemptStore(AttemptStoreOptions{Enabled: true, NodeID: 1, Epoch: [16]byte{1}})
	key := sendtrace.AttemptKey{MessageFingerprint: [16]byte{1}, ClientSeq: 1, ConnectFingerprint: [16]byte{2}}
	token := store.BeginSendAttempt(key, time.Unix(10, 0))
	refs := []sendtrace.RPCReference{{State: "bound", TraceID: "0123456789abcdef0123456789abcdef/0001"}, {State: "bound", TraceID: "0123456789abcdef0123456789abcdef/1"}}
	for _, ref := range refs {
		if !ref.Valid() {
			t.Fatal("fixture must use legal distinct opaque identities")
		}
		store.RecordAttemptRPC(token, ref)
	}
	result := store.QuerySendAttempts(context.Background(), []sendtrace.AttemptKey{key})
	if len(result.Results) != 1 || len(result.Results[0].Candidates) != 1 {
		t.Fatalf("original summary absent: %+v", result)
	}
	got := result.Results[0].Candidates[0].RPCReferences
	if len(got) != 2 || got[0] != refs[0] || got[1] != refs[1] {
		t.Fatalf("opaque RPC reference bytes changed or distinct requests were deduplicated: got=%+v want=%+v", got, refs)
	}
}
