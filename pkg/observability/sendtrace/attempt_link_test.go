package sendtrace

import (
	"context"
	"encoding/hex"
	"reflect"
	"testing"
	"time"
)

// Pre-code failures F05/F06/F08: a logical key is confused with an attempt,
// generic tracing activates new state, or a shared Context replaces a token.
func TestAttemptKeySeparatesLogicalMessageFromWireAttempt(t *testing.T) {
	logical := "attempt-link-private-msg"
	expected, err := hex.DecodeString("e9023f13ad1423884b1783a44e5f1cb2")
	if err != nil {
		t.Fatal(err)
	}
	got := MessageFingerprint(logical)
	var first16 [16]byte
	copy(first16[:], expected[:16])
	if got != first16 {
		t.Fatalf("message identity changed: got=%x want=%x", got, first16)
	}
	a := AttemptKey{MessageFingerprint: got, ClientSeq: 1, ConnectFingerprint: [16]byte{1}}
	b := a
	b.ClientSeq = 2
	c := a
	c.ConnectFingerprint[0] = 2
	if !a.Valid() || a == b || a == c {
		t.Fatal("attempt key lost sequence or actual CONNECT scope")
	}
	for _, bad := range []AttemptKey{{}, {MessageFingerprint: got, ClientSeq: 1}, {ClientSeq: 1, ConnectFingerprint: [16]byte{1}}, {MessageFingerprint: got, ConnectFingerprint: [16]byte{1}}} {
		if bad.Valid() {
			t.Fatalf("incomplete key accepted: %+v", bad)
		}
	}
}

type attemptGenericSink struct{ calls int }

func (s *attemptGenericSink) RecordSendTrace(Event) { s.calls++ }
func TestAttemptCapabilityIsIndependentFromGenericTracing(t *testing.T) {
	restore := SetSink(&attemptGenericSink{})
	defer restore()
	if GatewayAttemptEnabled() {
		t.Fatal("generic tracing silently enabled attempt summaries")
	}
	key := AttemptKey{MessageFingerprint: [16]byte{1}, ClientSeq: 1, ConnectFingerprint: [16]byte{2}}
	if token := BeginGatewayAttempt(key, time.Unix(1, 0)); token.Valid() {
		t.Fatal("uncomposed capability minted token")
	}
	if allocs := testing.AllocsPerRun(100, func() { _ = BeginGatewayAttempt(key, time.Time{}) }); allocs != 0 {
		t.Fatalf("disabled begin allocates: %v", allocs)
	}
}
func TestAttemptContextIsValueOnlyAndPerOriginalItem(t *testing.T) {
	parent := context.WithValue(context.Background(), struct{}{}, "business-context")
	first := AttemptToken{Epoch: [16]byte{3}, Ordinal: 1}
	second := first
	second.Ordinal = 2
	a := WithGatewayAttempt(parent, first)
	b := WithGatewayAttempt(parent, second)
	if GatewayAttemptFromContext(a) != first || GatewayAttemptFromContext(b) != second || GatewayAttemptFromContext(parent).Valid() {
		t.Fatal("shared context borrowed another invocation")
	}
	if !reflect.DeepEqual(a.Value(struct{}{}), parent.Value(struct{}{})) {
		t.Fatal("diagnostic context replaced business values")
	}
}
