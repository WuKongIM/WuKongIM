package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	coregateway "github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/session"
	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

type attemptAccessSink struct {
	tokens []sendtrace.AttemptToken
	stages []sendtrace.Stage
	phases []sendtrace.AttemptPhase
	traces map[sendtrace.AttemptToken]string
}

func (s *attemptAccessSink) AttemptLinkEnabled() bool        { return true }
func (s *attemptAccessSink) RecordSendTrace(sendtrace.Event) {}
func (s *attemptAccessSink) BeginSendAttempt(sendtrace.AttemptKey, time.Time) sendtrace.AttemptToken {
	return sendtrace.AttemptToken{}
}
func (s *attemptAccessSink) RecordAttemptPhase(token sendtrace.AttemptToken, phase sendtrace.AttemptPhase) {
	s.tokens = append(s.tokens, token)
	s.stages = append(s.stages, phase.Stage)
	s.phases = append(s.phases, phase)
}
func (s *attemptAccessSink) BindAttemptTrace(token sendtrace.AttemptToken, trace string) {
	s.traces[token] = trace
}
func (s *attemptAccessSink) RecordAttemptRPC(sendtrace.AttemptToken, sendtrace.RPCReference) {}

// F08: reversed completions retain each original Context token even where one
// sender/session and one downstream execution cohort are shared.
func TestAttemptLinkGatewayBatchAlignsPerOriginalInvocation(t *testing.T) {
	sink := &attemptAccessSink{traces: make(map[sendtrace.AttemptToken]string)}
	restore := sendtrace.SetSink(sink)
	defer restore()
	var written []frame.Frame
	sess := newTestSession(t, &written)
	sess.SetValue(coregateway.SessionValueUID, "private-sender")
	n := 0
	handler := New(Options{Messages: reverseMessages{}, OwnerNodeID: 1, SendTimeout: time.Second, TraceIDGenerator: func() string {
		n++
		if n == 1 {
			return "00112233445566778899aabbccddeeff"
		}
		return "11112233445566778899aabbccddeeff"
	}})
	first := sendtrace.AttemptToken{Epoch: [16]byte{9}, Ordinal: 1}
	second := first
	second.Ordinal = 2
	items := []coregateway.SendBatchItem{
		{Context: coregateway.Context{Session: sess, RequestContext: sendtrace.WithGatewayAttempt(context.Background(), first)}, Frame: &frame.SendPacket{ClientSeq: 1, ClientMsgNo: "same-logical", ChannelID: "private-channel", ChannelType: 2}},
		{Context: coregateway.Context{Session: sess, RequestContext: sendtrace.WithGatewayAttempt(context.Background(), second)}, Frame: &frame.SendPacket{ClientSeq: 2, ClientMsgNo: "same-logical", ChannelID: "private-channel", ChannelType: 2}},
	}
	if err := handler.OnSendBatch(items); err != nil {
		t.Fatal(err)
	}
	if len(written) != 2 || requireSendack(t, written, 0).ClientSeq != 1 || requireSendack(t, written, 1).ClientSeq != 2 {
		t.Fatal("diagnostic changed original SENDACK order")
	}
	for _, token := range []sendtrace.AttemptToken{first, second} {
		counts := map[sendtrace.Stage]int{}
		for i, got := range sink.tokens {
			if got == token {
				counts[sink.stages[i]]++
			}
		}
		if counts[sendtrace.StageGatewayMessagesSend] != 1 || counts[sendtrace.StageGatewayWriteSendack] != 1 {
			t.Fatalf("per-item phases for %+v=%+v", token, counts)
		}
		if sink.traces[token] == "" {
			t.Fatal("original upper trace not bound")
		}
	}
	if sink.traces[first] == sink.traces[second] {
		t.Fatal("batch borrowed first trace")
	}
}
func TestAttemptLinkWriteBoundaryReportsCallbackOnly(t *testing.T) {
	sink := &attemptAccessSink{traces: make(map[sendtrace.AttemptToken]string)}
	restore := sendtrace.SetSink(sink)
	defer restore()
	callbacks := 0
	sess := session.New(session.Config{ID: 1, WriteFrameFn: func(frame.Frame, session.OutboundMeta) error { callbacks++; return nil }})
	sess.SetValue(coregateway.SessionValueUID, "private-sender")
	handler := New(Options{Messages: &recordingMessages{sendResult: message.SendResult{MessageID: 1, MessageSeq: 1, Reason: message.ReasonSuccess}}, OwnerNodeID: 1, TraceIDGenerator: fixedTraceIDGenerator("00112233445566778899aabbccddeeff")})
	token := sendtrace.AttemptToken{Epoch: [16]byte{1}, Ordinal: 1}
	if err := handler.OnFrame(coregateway.Context{Session: sess, RequestContext: sendtrace.WithGatewayAttempt(context.Background(), token)}, &frame.SendPacket{ClientSeq: 1, ClientMsgNo: "original", ChannelID: "private-channel", ChannelType: 2}); err != nil {
		t.Fatal(err)
	}
	if callbacks != 1 {
		t.Fatalf("ordinary session write callbacks=%d", callbacks)
	}
	found := false
	for _, phase := range sink.phases {
		if phase.Stage == sendtrace.StageGatewayWriteSendack {
			found = true
			if phase.Boundary != "write_frame_callback" || !phase.EndDerived {
				t.Fatalf("callback timing falsely claims another boundary: %+v", phase)
			}
		}
	}
	if !found {
		t.Fatal("actual WriteFrame callback did not retain phase")
	}
}
