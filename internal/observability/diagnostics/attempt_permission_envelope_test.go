package diagnostics

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
)

// The public selector exposes one maximum envelope, not a sum, a latest RPC or
// the RPC referenced by the independent two identity slots.
func TestPermissionOriginRPCSummaryKeepsLongestEnvelope(t *testing.T) {
	s := attemptStoreFixture(t)
	at := time.Unix(10, 0)
	key := attemptStoreKey(1)
	token := s.BeginSendAttempt(key, at)
	stage := sendtrace.Stage("message.permission_rpc")
	for _, fact := range []struct {
		start, duration time.Duration
		result          sendtrace.Result
	}{{time.Millisecond, 20 * time.Millisecond, sendtrace.ResultOK}, {5 * time.Millisecond, 30 * time.Millisecond, sendtrace.ResultTimeout}, {9 * time.Millisecond, 10 * time.Millisecond, sendtrace.ResultError}, {12 * time.Millisecond, 30 * time.Millisecond, sendtrace.ResultError}} {
		s.RecordAttemptPhase(token, sendtrace.AttemptPhase{Stage: stage, StartedAt: at.Add(fact.start), Duration: fact.duration, Result: fact.result})
	}
	// A large invalid interval or foreign epoch must not displace valid evidence.
	s.RecordAttemptPhase(token, sendtrace.AttemptPhase{Stage: stage, StartedAt: at.Add(-time.Second), Duration: time.Hour, Result: sendtrace.ResultOK})
	foreign := token
	foreign.Epoch = [16]byte{9}
	s.RecordAttemptPhase(foreign, sendtrace.AttemptPhase{Stage: stage, StartedAt: at.Add(time.Second), Duration: time.Hour, Result: sendtrace.ResultOK})
	phase := preparationStorePhase(t, s, key, stage)
	if !phase.Observed || !phase.StartedAt.Equal(at.Add(5*time.Millisecond)) || phase.Duration != 30*time.Millisecond || phase.Result != sendtrace.ResultTimeout || phase.Boundary != "longest_origin_rpc91" || !phase.EndDerived {
		t.Fatalf("longest exact envelope mismatch: %+v", phase)
	}
	query := s.QuerySendAttempts(context.Background(), []sendtrace.AttemptKey{key})
	if len(query.Results[0].Candidates[0].Phases) != 8 {
		t.Fatal("summary is not the fixed eight-phase envelope")
	}
	if raw, err := json.Marshal(query); err != nil || !json.Valid(raw) {
		t.Fatal("public summary JSON is invalid")
	}
	last := len(query.Results[0].Candidates[0].Phases) - 1
	query.Results[0].Candidates[0].Phases[last].Duration = time.Hour
	if again := preparationStorePhase(t, s, key, stage); again.Duration != 30*time.Millisecond {
		t.Fatal("returned envelope aliases mutable storage")
	}
	other := attemptStoreKey(2)
	s.BeginSendAttempt(other, at)
	if preparationStorePhase(t, s, other, stage).Observed {
		t.Fatal("another original borrowed the envelope")
	}
	// The existing shared preparation facts retain first-valid semantics.
	s.RecordAttemptPhase(token, sendtrace.AttemptPhase{Stage: sendtrace.StageMessagePermissionBatch, StartedAt: at, Duration: time.Millisecond, Result: sendtrace.ResultOK})
	s.RecordAttemptPhase(token, sendtrace.AttemptPhase{Stage: sendtrace.StageMessagePermissionBatch, StartedAt: at, Duration: time.Hour, Result: sendtrace.ResultError})
	if old := preparationStorePhase(t, s, key, sendtrace.StageMessagePermissionBatch); old.Duration != time.Millisecond || old.Result != sendtrace.ResultOK {
		t.Fatal("the new maximum rule changed an original phase")
	}
}
