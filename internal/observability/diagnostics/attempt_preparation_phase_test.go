package diagnostics

import (
	"context"
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
)

func preparationStorePhase(t *testing.T, store *AttemptStore, key sendtrace.AttemptKey, stage sendtrace.Stage) sendtrace.AttemptPhase {
	t.Helper()
	result := store.QuerySendAttempts(context.Background(), []sendtrace.AttemptKey{key})
	if len(result.Results) != 1 || len(result.Results[0].Candidates) != 1 {
		t.Fatalf("original candidate missing: %+v", result)
	}
	for _, phase := range result.Results[0].Candidates[0].Phases {
		if phase.Stage == stage {
			return phase
		}
	}
	t.Fatalf("fixed summary omitted preparation stage %s", stage)
	return sendtrace.AttemptPhase{}
}

func TestAttemptPreparationStoreFixedClockAndDetachedOriginals(t *testing.T) {
	s := attemptStoreFixture(t)
	at := time.Unix(10, 0)
	for i, name := range []string{"message.permission_batch", "message.pre_append"} {
		stage := sendtrace.Stage(name)
		key := attemptStoreKey(uint32(i + 1))
		token := s.BeginSendAttempt(key, at)
		phase := sendtrace.AttemptPhase{Stage: stage, StartedAt: at.Add(time.Millisecond), Duration: time.Duration(i) * time.Millisecond, Result: sendtrace.ResultOK}
		s.RecordAttemptPhase(token, phase)
		// The first valid observation is immutable and belongs to this token.
		s.RecordAttemptPhase(token, sendtrace.AttemptPhase{Stage: stage, StartedAt: at.Add(time.Second), Duration: time.Hour, Result: sendtrace.ResultError})
		got := preparationStorePhase(t, s, key, stage)
		if !got.Observed || !got.StartedAt.Equal(phase.StartedAt) || got.Duration != phase.Duration || got.Result != phase.Result || !got.EndDerived {
			t.Fatalf("shared preparation fact lost existing clock/result: %+v", got)
		}
		query := s.QuerySendAttempts(context.Background(), []sendtrace.AttemptKey{key})
		if len(query.Results[0].Candidates[0].Phases) != 7 || query.Results[0].Candidates[0].Phases[4].Stage != sendtrace.StageMessageSendDurable {
			t.Fatal("new bounded phases displaced the five original phase identities")
		}
		encoded, err := json.Marshal(query)
		if err != nil || !json.Valid(encoded) {
			t.Fatal("typed phase serialization failed")
		}
		for index, retained := range query.Results[0].Candidates[0].Phases {
			if retained.Stage == stage {
				query.Results[0].Candidates[0].Phases[index].Duration = time.Hour
			}
		}
		if again := preparationStorePhase(t, s, key, stage); again.Duration != phase.Duration {
			t.Fatal("returned preparation aliases mutable storage")
		}
	}
	// Another invocation of the same logical message cannot borrow either fact.
	key := attemptStoreKey(3)
	s.BeginSendAttempt(key, at)
	if preparationStorePhase(t, s, key, "message.permission_batch").Observed || preparationStorePhase(t, s, key, "message.pre_append").Observed {
		t.Fatal("another original borrowed preparation")
	}
}

func TestAttemptPreparationStoreRejectsMissingInvalidOrStaleFacts(t *testing.T) {
	for _, stage := range []sendtrace.Stage{"message.permission_batch", "message.pre_append"} {
		for _, name := range []string{"missing_start", "negative", "before_ingress", "overflow", "bad_result", "stale_epoch"} {
			t.Run(string(stage)+"/"+name, func(t *testing.T) {
				s := attemptStoreFixture(t)
				at := time.Unix(10, 0)
				key := attemptStoreKey(1)
				token := s.BeginSendAttempt(key, at)
				phase := sendtrace.AttemptPhase{Stage: stage, StartedAt: at.Add(time.Millisecond), Duration: time.Second, Result: sendtrace.ResultOK}
				switch name {
				case "missing_start":
					phase.StartedAt = time.Time{}
				case "negative":
					phase.Duration = -1
				case "before_ingress":
					phase.StartedAt = at.Add(-time.Nanosecond)
				case "overflow":
					phase.Duration = time.Duration(math.MaxInt64)
				case "bad_result":
					phase.Result = "private-cause"
				case "stale_epoch":
					token.Epoch[0]++
				}
				s.RecordAttemptPhase(token, phase)
				if got := preparationStorePhase(t, s, key, stage); got.Observed || got.Duration != 0 || !got.StartedAt.IsZero() || got.Result != "" {
					t.Fatalf("invalid preparation became a fact: %+v", got)
				}
			})
		}
	}
}
