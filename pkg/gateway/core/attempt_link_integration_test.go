//go:build integration

package core

import (
	"sync"
	"testing"
	"time"

	gatewaytypes "github.com/WuKongIM/WuKongIM/pkg/gateway/types"
	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

type attemptCoreSink struct {
	mu     sync.Mutex
	next   uint64
	keys   map[sendtrace.AttemptToken]sendtrace.AttemptKey
	phases map[sendtrace.AttemptToken][]sendtrace.AttemptPhase
}

func (s *attemptCoreSink) AttemptLinkEnabled() bool        { return true }
func (s *attemptCoreSink) RecordSendTrace(sendtrace.Event) {}
func (s *attemptCoreSink) BeginSendAttempt(key sendtrace.AttemptKey, _ time.Time) sendtrace.AttemptToken {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.next++
	token := sendtrace.AttemptToken{Epoch: [16]byte{1}, Ordinal: s.next}
	s.keys[token] = key
	return token
}
func (s *attemptCoreSink) RecordAttemptPhase(token sendtrace.AttemptToken, p sendtrace.AttemptPhase) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.phases[token] = append(s.phases[token], p)
}
func (s *attemptCoreSink) BindAttemptTrace(sendtrace.AttemptToken, string)                 {}
func (s *attemptCoreSink) RecordAttemptRPC(sendtrace.AttemptToken, sendtrace.RPCReference) {}

// F08/F09: submission and deferred publication execute the real core owners;
// a reused ClientMsgNo does not replace the token of its first actual frame.
func TestAttemptLinkCoreAllocatesOneInvocationPerActualAcceptedFrame(t *testing.T) {
	sink := &attemptCoreSink{keys: make(map[sendtrace.AttemptToken]sendtrace.AttemptKey), phases: make(map[sendtrace.AttemptToken][]sendtrace.AttemptPhase)}
	restore := sendtrace.SetSink(sink)
	defer restore()
	handler := &deferredProbeHandler{calls: make(chan deferredProbeCall, 4)}
	executor, server := deferredTestExecutor(t, 3, handler)
	state := deferredTestState(server, 1)
	state.session.SetValue(gatewaytypes.SessionValueConnectFingerprint, [16]byte{7})
	var calls []deferredProbeCall
	for _, seq := range []uint64{1, 2, 1} {
		if !executor.submit(state, "", &frame.SendPacket{ClientMsgNo: "same-logical-message", ClientSeq: seq}) {
			t.Fatal("unexpected admission rejection")
		}
		calls = append(calls, deferredNext(t, handler))
	}
	if executor.submit(state, "", &frame.SendPacket{ClientMsgNo: "rejected-at-capacity", ClientSeq: 99}) {
		t.Fatal("fixture did not enforce original admitted capacity")
	}
	seen := make(map[sendtrace.AttemptToken]bool)
	for i, c := range calls {
		if len(c.items) != 1 {
			t.Fatal("unexpected fixture batch")
		}
		item := c.items[0]
		token := sendtrace.GatewayAttemptFromContext(item.Context.RequestContext)
		if !token.Valid() || seen[token] {
			t.Fatalf("duplicate/missing actual ingress token: %+v", token)
		}
		seen[token] = true
		sink.mu.Lock()
		key := sink.keys[token]
		sink.mu.Unlock()
		if key.ClientSeq != uint32(item.Frame.ClientSeq) || key.MessageFingerprint != sendtrace.MessageFingerprint(item.Frame.ClientMsgNo) || key.ConnectFingerprint != [16]byte{7} {
			t.Fatalf("wrong per-item identity %d: %+v", i, key)
		}
	}
	for _, c := range calls {
		if err := c.publish(0, func() error { return nil }); err != nil {
			t.Fatal(err)
		}
		c.complete(nil)
	}
	deferredDrain(t, executor)
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.keys) != 3 {
		t.Fatalf("actual invocation count=%d", len(sink.keys))
	}
	for token := range seen {
		found := false
		for _, p := range sink.phases[token] {
			if p.Stage == sendtrace.StageGatewayAsyncDispatchWait {
				found = true
				if p.StartedAt.IsZero() || p.Duration < 0 {
					t.Fatal("missing existing enqueue/read boundary")
				}
			}
		}
		if !found {
			t.Fatal("actual invocation lost dispatch phase")
		}
	}
}
