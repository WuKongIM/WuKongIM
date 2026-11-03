package diagnostics

import (
	"context"
	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
	"reflect"
	"testing"
	"time"
)

// F7/F8: permission identities cannot consume append credits or alias query data.
func TestAttemptPermissionRPCIndependentBoundsAndLateEpoch(t *testing.T) {
	s := attemptStoreFixture(t)
	key := attemptStoreKey(1)
	token := s.BeginSendAttempt(key, time.Unix(10, 0))
	for _, id := range []string{"1", "2", "3"} {
		ref := sendtrace.RPCReference{State: "bound", TraceID: "00112233445566778899aabbccddeeff/" + id}
		s.RecordAttemptPermissionRPC(token, ref)
		s.RecordAttemptRPC(token, ref)
	}
	q := s.QuerySendAttempts(context.Background(), []sendtrace.AttemptKey{key})
	got := q.Results[0].Candidates[0]
	if len(got.PermissionRPCReferences) != 2 || !got.PermissionRPCOverflow || len(got.RPCReferences) != 2 || !got.RPCOverflow {
		t.Fatalf("independent bounds %+v", got)
	}
	got.PermissionRPCReferences[0].TraceID = "private"
	next := s.QuerySendAttempts(context.Background(), []sendtrace.AttemptKey{key})
	if next.Results[0].Candidates[0].PermissionRPCReferences[0].TraceID == "private" {
		t.Fatal("query aliases row")
	}
	stale := token
	stale.Epoch[0]++
	s.RecordAttemptPermissionRPC(stale, sendtrace.RPCReference{State: "recorder_disabled"})
	s.RecordAttemptPermissionRPC(token, sendtrace.RPCReference{State: "bound", TraceID: "private"})
	if !reflect.DeepEqual(next, s.QuerySendAttempts(context.Background(), []sendtrace.AttemptKey{key})) {
		t.Fatal("invalid/stale identity changed row")
	}
	for i := uint32(2); i <= 65537; i++ {
		s.BeginSendAttempt(attemptStoreKey(i), time.Unix(10, 0))
	}
	before := s.QuerySendAttempts(context.Background(), []sendtrace.AttemptKey{attemptStoreKey(65537)})
	s.RecordAttemptPermissionRPC(token, sendtrace.RPCReference{State: "recorder_disabled"})
	if !reflect.DeepEqual(before, s.QuerySendAttempts(context.Background(), []sendtrace.AttemptKey{attemptStoreKey(65537)})) {
		t.Fatal("late permission update changed recycled row")
	}
}
