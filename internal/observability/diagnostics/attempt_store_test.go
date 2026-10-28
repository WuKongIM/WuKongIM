package diagnostics

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
)

func attemptStoreFixture(t *testing.T) *AttemptStore {
	t.Helper()
	return NewAttemptStore(AttemptStoreOptions{Enabled: true, NodeID: 1, Epoch: [16]byte{1}})
}
func attemptStoreKey(seq uint32) sendtrace.AttemptKey {
	return sendtrace.AttemptKey{MessageFingerprint: [16]byte{1}, ClientSeq: seq, ConnectFingerprint: [16]byte{2}}
}
func TestAttemptStoreDefaultOffAndPhysicalBound(t *testing.T) {
	if NewAttemptStore(AttemptStoreOptions{NodeID: 1}) != nil {
		t.Fatal("disabled capability allocated a ring")
	}
	if got := unsafe.Sizeof(attemptSummaryRow{}); got > 256 {
		t.Fatalf("summary row=%d bytes want<=256", got)
	}
	s := attemptStoreFixture(t)
	if len(s.rows) != 65536 || len(s.index) != 131072 || len(s.traceIndex) != 131072 {
		t.Fatalf("unfrozen row/index capacity %d/%d", len(s.rows), len(s.index))
	}
	// Index size is separately reported; a 16MiB row bound is not a heap/RSS bound.
	t.Logf("retained arrays: row_size=%d row_capacity=%d index_entry_size=%d index_capacity=%d key_index_bytes=%d trace_index_entry=%d trace_index_bytes=%d raw_total_bytes=%d", unsafe.Sizeof(attemptSummaryRow{}), len(s.rows), unsafe.Sizeof(attemptIndexEntry{}), len(s.index), uintptr(len(s.index))*unsafe.Sizeof(attemptIndexEntry{}), unsafe.Sizeof(attemptTraceIndexEntry{}), uintptr(len(s.traceIndex))*unsafe.Sizeof(attemptTraceIndexEntry{}), uintptr(len(s.rows))*unsafe.Sizeof(attemptSummaryRow{})+uintptr(len(s.index))*unsafe.Sizeof(attemptIndexEntry{})+uintptr(len(s.traceIndex))*unsafe.Sizeof(attemptTraceIndexEntry{}))
	key := attemptStoreKey(1)
	started := time.Unix(10, 0)
	if allocs := testing.AllocsPerRun(128, func() {
		token := s.BeginSendAttempt(key, started)
		s.RecordAttemptPhase(token, sendtrace.AttemptPhase{Stage: sendtrace.StageGatewayAsyncDispatchWait, StartedAt: started, Duration: time.Millisecond, Result: sendtrace.ResultOK})
	}); allocs != 0 {
		t.Fatalf("fixed begin/phase allocates: %v", allocs)
	}
}
func TestAttemptStoreRepeatedKeyIsAmbiguousAndSequenceScopesStaySeparate(t *testing.T) {
	s := attemptStoreFixture(t)
	at := time.Unix(10, 0)
	a := attemptStoreKey(1)
	b := attemptStoreKey(2)
	first := s.BeginSendAttempt(a, at)
	retry := s.BeginSendAttempt(b, at)
	if !first.Valid() || !retry.Valid() || first == retry {
		t.Fatal("two real admissions shared a token")
	}
	s.BindAttemptTrace(first, "00112233445566778899aabbccddeeff")
	s.BindAttemptTrace(retry, "11112233445566778899aabbccddeeff")
	q := s.QuerySendAttempts(context.Background(), []sendtrace.AttemptKey{a, b})
	if len(q.Results) != 2 || len(q.Results[0].Candidates) != 1 || len(q.Results[1].Candidates) != 1 || q.Results[0].Candidates[0].Token != first || q.Results[1].Candidates[0].Token != retry {
		t.Fatalf("selector borrowed retry/latest: %+v", q)
	}
	if q.Results[0].Status == "exact" {
		t.Fatal("one retained candidate is not generic uniqueness proof")
	}
	s.BeginSendAttempt(a, at)
	q = s.QuerySendAttempts(context.Background(), []sendtrace.AttemptKey{a})
	if q.Results[0].Status != "ambiguous" || len(q.Results[0].Candidates) != 2 {
		t.Fatalf("repeat packet/tuple falsely unique: %+v", q)
	}
	s.BeginSendAttempt(a, at)
	q = s.QuerySendAttempts(context.Background(), []sendtrace.AttemptKey{a})
	if q.Results[0].Status != "ambiguous" || len(q.Results[0].Candidates) > 2 {
		t.Fatal("candidate cap or ambiguity lost")
	}
}
func TestAttemptStoreWrapLateUpdatesAndMissingKeysRemainUnknown(t *testing.T) {
	s := attemptStoreFixture(t)
	at := time.Unix(10, 0)
	key := attemptStoreKey(1)
	old := s.BeginSendAttempt(key, at)
	for i := uint32(2); i <= 65537; i++ {
		s.BeginSendAttempt(attemptStoreKey(i), at)
	}
	replacement := s.QuerySendAttempts(context.Background(), []sendtrace.AttemptKey{attemptStoreKey(65537)})
	s.BindAttemptTrace(old, "00112233445566778899aabbccddeeff")
	s.RecordAttemptPhase(old, sendtrace.AttemptPhase{Stage: sendtrace.StageGatewayWriteSendack, StartedAt: at, Duration: time.Second, Result: sendtrace.ResultOK})
	after := s.QuerySendAttempts(context.Background(), []sendtrace.AttemptKey{attemptStoreKey(65537)})
	if !reflect.DeepEqual(replacement, after) {
		t.Fatal("late update changed a recycled ring slot")
	}
	q := s.QuerySendAttempts(context.Background(), []sendtrace.AttemptKey{key})
	if q.Results[0].Status != "not_retained" || len(q.Results[0].Candidates) != 0 {
		t.Fatalf("overwritten key borrowed latest: %+v", q)
	}
}
func TestAttemptStoreReferencesOverflowUnknownAndNoRawIdentityRetained(t *testing.T) {
	s := attemptStoreFixture(t)
	at := time.Unix(10, 0)
	key := attemptStoreKey(1)
	token := s.BeginSendAttempt(key, at)
	s.BindAttemptTrace(token, strings.Repeat("raw-private-id-", 1024))
	for _, id := range []string{"1", "2", "3"} {
		s.RecordAttemptRPC(token, sendtrace.RPCReference{State: "bound", TraceID: "00112233445566778899aabbccddeeff/" + id})
	}
	q := s.QuerySendAttempts(context.Background(), []sendtrace.AttemptKey{key})
	row := q.Results[0].Candidates[0]
	if len(row.RPCReferences) > 2 || !row.RPCOverflow || row.UpperTraceID != "" {
		t.Fatalf("unbounded or misleading ref/trace: %+v", row)
	}
	body, err := json.Marshal(q)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"raw-private-id", "client_msg_no", "uid", "channel_id", "payload", "client_key", "token_secret"} {
		if strings.Contains(string(body), raw) {
			t.Fatalf("private identity leaked: %q", raw)
		}
	}
}
func TestAttemptStoreIndependentSummarySurvivesGlobalStageRingOverwrite(t *testing.T) {
	s := attemptStoreFixture(t)
	global := NewStore(StoreOptions{NodeID: 1, Capacity: 2, Now: func() time.Time { return time.Unix(10, 0) }})
	at := time.Unix(10, 0)
	key := attemptStoreKey(1)
	token := s.BeginSendAttempt(key, at)
	s.BindAttemptTrace(token, "00112233445566778899aabbccddeeff")
	s.RecordAttemptPhase(token, sendtrace.AttemptPhase{Stage: sendtrace.StageGatewayMessagesSend, StartedAt: at, Duration: 500 * time.Millisecond, Result: sendtrace.ResultOK})
	global.Record(Event{TraceID: "00112233445566778899aabbccddeeff", ClientMsgNo: "private-original", Stage: Stage(sendtrace.StageGatewayMessagesSend), At: at})
	for i := 0; i < 3; i++ {
		global.Record(Event{TraceID: "other", Stage: Stage(sendtrace.StageGatewayMessagesSend), At: at})
	}
	if got := global.Query(context.Background(), Query{TraceID: "00112233445566778899aabbccddeeff"}); len(got.Events) != 0 {
		t.Fatal("fixture did not overwrite original global sample")
	}
	q := s.QuerySendAttempts(context.Background(), []sendtrace.AttemptKey{key})
	if len(q.Results) != 1 || len(q.Results[0].Candidates) != 1 || q.Results[0].Candidates[0].Token != token {
		t.Fatalf("retention design still depends on global payload ring: %+v", q)
	}
}

func TestAttemptStoreInvalidUpperTraceAndOrdinalExhaustionStayUnknown(t *testing.T) {
	s := attemptStoreFixture(t)
	at := time.Unix(10, 0)
	for i, trace := range []string{"", "custom-nonhex-trace", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", strings.Repeat("a", 31), strings.Repeat("a", 33), "fallback/opaque"} {
		key := attemptStoreKey(uint32(i + 1))
		token := s.BeginSendAttempt(key, at)
		s.BindAttemptTrace(token, trace)
		row := s.QuerySendAttempts(context.Background(), []sendtrace.AttemptKey{key}).Results[0].Candidates[0]
		if row.UpperTraceID != "" || len(row.RPCReferences) != 0 {
			t.Fatalf("invalid/fallback TraceID became an authoritative lookup key: %q %+v", trace, row)
		}
	}
	s.nextOrdinal = math.MaxUint64
	if token := s.BeginSendAttempt(attemptStoreKey(100), at); token.Valid() {
		t.Fatal("exhausted invocation identity wrapped or silently reused a token")
	}
}
func fillAttemptBudgetNumbers(value reflect.Value) {
	if value.Type() == reflect.TypeOf(time.Time{}) {
		return
	}
	switch value.Kind() {
	case reflect.Struct:
		for i := 0; i < value.NumField(); i++ {
			if value.Field(i).CanSet() {
				fillAttemptBudgetNumbers(value.Field(i))
			}
		}
	case reflect.Array, reflect.Slice:
		for i := 0; i < value.Len(); i++ {
			fillAttemptBudgetNumbers(value.Index(i))
		}
	case reflect.Pointer:
		if !value.IsNil() {
			fillAttemptBudgetNumbers(value.Elem())
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		value.SetUint(^uint64(0) >> (64 - value.Type().Bits()))
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		value.SetInt(int64(^uint64(0) >> (65 - value.Type().Bits())))
	case reflect.Bool:
		value.SetBool(true)
	}
}
func TestAttemptStoreMaximumSummaryOnlyEncodingStaysWithin64KiB(t *testing.T) {
	s := attemptStoreFixture(t)
	at := time.Unix(1700000000, 0)
	keys := make([]sendtrace.AttemptKey, 16)
	for i := range keys {
		keys[i] = attemptStoreKey(uint32(i + 1))
		for j := 0; j < 2; j++ {
			token := s.BeginSendAttempt(keys[i], at)
			s.BindAttemptTrace(token, fmt.Sprintf("%032x", 1+i*2+j))
			for _, stage := range []sendtrace.Stage{sendtrace.StageGatewayAsyncDispatchWait, sendtrace.StageGatewayMessagesSend, sendtrace.StageGatewayWriteSendack, sendtrace.StageGatewaySessionPublicationWait, sendtrace.StageMessageSendDurable} {
				s.RecordAttemptPhase(token, sendtrace.AttemptPhase{Stage: stage, StartedAt: at, Duration: time.Second, Result: sendtrace.ResultTimeout})
			}
			for _, id := range []string{"ffffffffffffffff", "eeeeeeeeeeeeeeee"} {
				s.RecordAttemptRPC(token, sendtrace.RPCReference{State: "bound", TraceID: strings.Repeat("f", 32) + "/" + id})
			}
		}
	}
	result := s.QuerySendAttempts(context.Background(), keys)
	for _, row := range result.Results {
		if len(row.Candidates) != 2 {
			t.Fatal("maximum fixture did not retain both ambiguous candidates")
		}
	}
	// Max-width numeric encoding is a size-only shape, not a causal claim.
	fillAttemptBudgetNumbers(reflect.ValueOf(&result).Elem())
	body, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if len(body)+1 > 64<<10 {
		t.Fatalf("shrink optional summary rather than expand 64KiB cap: %d", len(body)+1)
	}
	t.Logf("maximum-width summary-only response=%d bytes", len(body)+1)
}
