package sendtrace

import (
	"context"
	"sync"
	"testing"
	"time"
)

type permissionLinkSink struct {
	mu      sync.Mutex
	enabled bool
	refs    map[AttemptToken][]RPCReference
}

func (*permissionLinkSink) RecordSendTrace(Event)      {}
func (s *permissionLinkSink) AttemptLinkEnabled() bool { return s.enabled }
func (*permissionLinkSink) BeginSendAttempt(AttemptKey, time.Time) AttemptToken {
	return AttemptToken{}
}
func (*permissionLinkSink) BindAttemptTrace(AttemptToken, string)         {}
func (*permissionLinkSink) RecordAttemptRPC(AttemptToken, RPCReference)   {}
func (*permissionLinkSink) RecordAttemptPhase(AttemptToken, AttemptPhase) {}
func (s *permissionLinkSink) RecordAttemptPermissionRPC(t AttemptToken, r RPCReference) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refs[t] = append(s.refs[t], r)
}

// F3/F4: no disabled allocations, no borrowed parent cancellation, fixed64 values.
func TestPermissionAttemptsBoundedDetachedAndDefaultOff(t *testing.T) {
	ref := RPCReference{State: "bound", TraceID: "00112233445566778899aabbccddeeff/1"}
	for _, enabled := range []bool{false, true} {
		sink := &permissionLinkSink{enabled: enabled, refs: make(map[AttemptToken][]RPCReference)}
		restore := SetSink(sink)
		var set PermissionAttempts
		for i := uint64(1); i <= 70; i++ {
			set.AddContext(WithGatewayAttempt(context.Background(), AttemptToken{Epoch: [16]byte{1}, Ordinal: i}))
		}
		ctx, cancel := context.WithCancel(context.Background())
		pub := set.WithContext(ctx)
		set.AddContext(WithGatewayAttempt(context.Background(), AttemptToken{Epoch: [16]byte{2}, Ordinal: 100}))
		var child PermissionAttempts
		child.AddContext(pub)
		child.AddContext(pub)
		child.RecordRPC(ref)
		cancel()
		if pub.Err() != context.Canceled {
			t.Fatal("execution context cancellation changed")
		}
		want := 0
		if enabled {
			want = 64
		}
		if len(sink.refs) != want {
			t.Fatalf("bounded tokens=%d want%d", len(sink.refs), want)
		}
		for token, refs := range sink.refs {
			if token.Epoch != [16]byte{1} || token.Ordinal > 64 || len(refs) != 1 {
				t.Fatalf("alias/duplicate %v/%v", token, refs)
			}
		}
		restore()
	}
	restore := SetSink(&attemptGenericSink{})
	defer restore()
	if n := testing.AllocsPerRun(100, func() {
		var set PermissionAttempts
		set.AddContext(context.Background())
		ctx := set.WithContext(context.Background())
		var next PermissionAttempts
		next.AddContext(ctx)
		next.RecordRPC(ref)
	}); n != 0 {
		t.Fatalf("disabled/generic allocates %v", n)
	}
}
func TestPermissionAttemptsPreserveExecutionOwnerAndRejectInvalidReference(t *testing.T) {
	sink := &permissionLinkSink{enabled: true, refs: make(map[AttemptToken][]RPCReference)}
	restore := SetSink(sink)
	defer restore()
	member, cancel := context.WithCancel(context.Background())
	token := AttemptToken{Epoch: [16]byte{3}, Ordinal: 1}
	member = WithGatewayAttempt(member, token)
	var set PermissionAttempts
	set.AddContext(member)
	owner := context.WithValue(context.Background(), struct{}{}, "owner")
	published := set.WithContext(owner)
	cancel()
	if published.Err() != nil || published.Value(struct{}{}) != "owner" || GatewayAttemptFromContext(published).Valid() {
		t.Fatal("borrowed member cancellation/value/token as parent")
	}
	var copied PermissionAttempts
	copied.AddContext(published)
	copied.RecordRPC(RPCReference{State: "bound", TraceID: "private"})
	if len(sink.refs) != 0 {
		t.Fatal("invalid reference retained")
	}
	sink.enabled = false
	copied.RecordRPC(RPCReference{State: "recorder_disabled"})
	if len(sink.refs) != 0 {
		t.Fatal("disabled sink revived")
	}
}
