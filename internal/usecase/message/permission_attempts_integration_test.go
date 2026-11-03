//go:build integration

package message

import (
	"context"
	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
	"testing"
	"time"
)

type permissionAttemptProbe struct{ *recordingPermissionBatchStore }

func (p *permissionAttemptProbe) ReadPermissionsBatch(ctx context.Context, q []PermissionRead) []PermissionReadResult {
	var set sendtrace.PermissionAttempts
	set.AddContext(ctx)
	set.RecordRPC(sendtrace.RPCReference{State: "bound", TraceID: "00112233445566778899aabbccddeeff/1"})
	return p.recordingPermissionBatchStore.ReadPermissionsBatch(ctx, q)
}

type messagePermissionAttemptSink struct {
	preparationAttemptSink
	refs map[sendtrace.AttemptToken]int
}

func (s *messagePermissionAttemptSink) RecordAttemptPermissionRPC(t sendtrace.AttemptToken, _ sendtrace.RPCReference) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refs[t]++
}

// F5: shared-deadline policy coalescing must retain every original invocation.
func TestPermissionAttemptMessageBatchKeepsEveryOriginalToken(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		sink := &messagePermissionAttemptSink{preparationAttemptSink: preparationAttemptSink{enabled: true, phases: make(map[sendtrace.AttemptToken][]sendtrace.AttemptPhase)}, refs: make(map[sendtrace.AttemptToken]int)}
		restore := sendtrace.SetSink(sink)
		facts := &permissionAttemptProbe{&recordingPermissionBatchStore{base: newFakePermissionStore()}}
		app := New(Options{PermissionStore: facts, PermissionBatchStore: facts, Submitter: benchmarkMessageSubmitter{}})
		deadline := time.Now().Add(time.Hour)
		items := asyncMessageItems(2)
		var tokens [2]sendtrace.AttemptToken
		var cancels []context.CancelFunc
		for i := range items {
			items[i].Deadline = deadline
			if mixed {
				items[i].Deadline = deadline.Add(time.Duration(i) * time.Minute)
			}
			ctx := context.Background()
			if mixed {
				var cancel context.CancelFunc
				ctx, cancel = context.WithDeadline(ctx, items[i].Deadline)
				cancels = append(cancels, cancel)
			}
			tokens[i] = sendtrace.AttemptToken{Epoch: [16]byte{4}, Ordinal: uint64(i + 1)}
			items[i].Context = sendtrace.WithGatewayAttempt(ctx, tokens[i])
			items[i].Command.RequestScoped = true
			items[i].Command.MessageScopedUIDs = []string{"target"}
		}
		results := app.SendBatch(items)
		for _, r := range results {
			if r.Err != nil || r.Result.Reason != ReasonSuccess {
				t.Errorf("business result %+v", r)
			}
		}
		wantCalls := int64(1)
		if mixed {
			wantCalls = 2
		}
		if facts.batchCalls.Load() != wantCalls {
			t.Fatalf("fixture did not exercise expected deadline cohorts %d want%d", facts.batchCalls.Load(), wantCalls)
		}
		for _, cancel := range cancels {
			cancel()
		}
		for _, token := range tokens {
			if sink.refs[token] != 1 {
				t.Errorf("mixed%t original%d refs=%d want1", mixed, token.Ordinal, sink.refs[token])
			}
		}
		restore()
	}
}
