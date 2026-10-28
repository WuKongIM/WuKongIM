//go:build integration

package diagnostics_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/observability/diagnostics"
	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
)

func durableIntegrationKey(seq uint32) sendtrace.AttemptKey {
	return sendtrace.AttemptKey{MessageFingerprint: sendtrace.MessageFingerprint("same-original-and-retry"), ClientSeq: seq, ConnectFingerprint: [16]byte{9}}
}

func durableIntegrationStore() *diagnostics.AttemptStore {
	return diagnostics.NewAttemptStore(diagnostics.AttemptStoreOptions{Enabled: true, NodeID: 1, Epoch: [16]byte{7}})
}

func durableIntegrationCandidate(t *testing.T, store *diagnostics.AttemptStore, key sendtrace.AttemptKey) diagnostics.AttemptCandidate {
	t.Helper()
	got := store.QuerySendAttempts(context.Background(), []sendtrace.AttemptKey{key})
	if got.Status != "ok" || len(got.Results) != 1 || got.Results[0].Status != "candidate" || len(got.Results[0].Candidates) != 1 {
		t.Fatalf("expected one retained candidate, got %+v", got)
	}
	return got.Results[0].Candidates[0]
}

func assertDurableDurationFact(t *testing.T, candidate diagnostics.AttemptCandidate, duration time.Duration, result sendtrace.Result) {
	t.Helper()
	phase := candidate.Phases[4]
	if phase.Stage != sendtrace.StageMessageSendDurable || !phase.Observed || phase.Duration != duration || phase.Result != result || phase.Boundary != "diagnostic_event_duration" || !phase.StartedAt.IsZero() || phase.EndDerived {
		t.Fatalf("durable event must remain a duration fact without a fabricated interval: got=%+v want duration=%v result=%q", phase, duration, result)
	}
}

func assertDurableUnknown(t *testing.T, candidate diagnostics.AttemptCandidate) {
	t.Helper()
	phase := candidate.Phases[4]
	if phase.Stage != sendtrace.StageMessageSendDurable || phase.Observed || phase.Duration != 0 || phase.Result != "" || phase.Boundary != "diagnostic_event_duration" || !phase.StartedAt.IsZero() || phase.EndDerived {
		t.Fatalf("unbound/invalid durable facts must remain unknown: %+v", phase)
	}
}

// The public Sink path must project the uniquely bound original, rather than
// matching a logical message number or retaining the global payload ring.
func TestAttemptDurablePhasePublicSinkKeepsOriginalRetryAndSurvivesGlobalOverwrite(t *testing.T) {
	at := time.Unix(10, 0)
	store := durableIntegrationStore()
	global := diagnostics.NewStore(diagnostics.StoreOptions{Capacity: 2, Now: func() time.Time { return at.Add(time.Second) }})
	sink := diagnostics.NewSendTraceSink(global, nil).WithAttemptStore(store)
	keys := []sendtrace.AttemptKey{durableIntegrationKey(1), durableIntegrationKey(2)}
	traces := []string{"00112233445566778899aabbccddeeff", "11112233445566778899aabbccddeeff"}
	durations := []time.Duration{80 * time.Millisecond, 11 * time.Millisecond}
	results := []sendtrace.Result{sendtrace.ResultOK, sendtrace.ResultTimeout}
	for i, key := range keys {
		token := sink.BeginSendAttempt(key, at)
		sink.BindAttemptTrace(token, traces[i])
		sink.RecordSendTrace(sendtrace.Event{TraceID: traces[i], Stage: sendtrace.StageMessageSendDurable, At: at.Add(time.Millisecond), Duration: durations[i], Result: results[i], ClientMsgNo: "same-original-and-retry", NodeID: 1})
		assertDurableDurationFact(t, durableIntegrationCandidate(t, store, key), durations[i], results[i])
	}
	for i := 0; i < 3; i++ {
		global.Record(diagnostics.Event{TraceID: "overwritten", Stage: "other.stage", At: at})
	}
	for i, key := range keys {
		if retained := global.Query(context.Background(), diagnostics.Query{TraceID: traces[i]}); len(retained.Events) != 0 {
			t.Fatalf("fixture must first lose original global payloads, got %+v", retained.Events)
		}
		candidate := durableIntegrationCandidate(t, store, key)
		if candidate.UpperTraceID != traces[i] || candidate.TraceAmbiguous {
			t.Fatalf("original and retry upper identities changed: %+v", candidate)
		}
		assertDurableDurationFact(t, candidate, durations[i], results[i])
	}
}

// Publication timestamps cannot recover a continuous interval when the native
// duration may be an accumulated retry-segment sum.
func TestAttemptDurablePhasePublicSinkHasNoInventedInterval(t *testing.T) {
	at := time.Unix(20, 0)
	store := durableIntegrationStore()
	sink := diagnostics.NewSendTraceSink(diagnostics.NewStore(diagnostics.StoreOptions{Capacity: 8, Now: func() time.Time { return at.Add(time.Second) }}), nil).WithAttemptStore(store)
	cases := []struct {
		name        string
		publication time.Time
	}{
		{name: "duration_exceeds_ingress_to_publication", publication: at.Add(time.Millisecond)},
		{name: "publication_missing", publication: time.Time{}},
		{name: "publication_before_ingress", publication: at.Add(-time.Second)},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key := durableIntegrationKey(uint32(101 + i))
			trace := fmt.Sprintf("%032x", i+101)
			token := sink.BeginSendAttempt(key, at)
			sink.BindAttemptTrace(token, trace)
			sink.RecordSendTrace(sendtrace.Event{TraceID: trace, Stage: sendtrace.StageMessageSendDurable, At: tc.publication, Duration: 2 * time.Second, Result: sendtrace.ResultOK})
			assertDurableDurationFact(t, durableIntegrationCandidate(t, store, key), 2*time.Second, sendtrace.ResultOK)
		})
	}
}

func TestAttemptDurablePhasePublicSinkRejectsMissingReusedAndUnboundUpperTrace(t *testing.T) {
	at := time.Unix(30, 0)
	store := durableIntegrationStore()
	sink := diagnostics.NewSendTraceSink(diagnostics.NewStore(diagnostics.StoreOptions{Capacity: 8, Now: func() time.Time { return at }}), nil).WithAttemptStore(store)
	missingKey := durableIntegrationKey(201)
	sink.BindAttemptTrace(sink.BeginSendAttempt(missingKey, at), "22222222222222222222222222222222")
	sink.RecordSendTrace(sendtrace.Event{TraceID: "33333333333333333333333333333333", Stage: sendtrace.StageMessageSendDurable, At: at, Duration: time.Second, Result: sendtrace.ResultOK, ClientMsgNo: "same-original-and-retry"})
	assertDurableUnknown(t, durableIntegrationCandidate(t, store, missingKey))

	reused := "44444444444444444444444444444444"
	for _, seq := range []uint32{202, 203} {
		sink.BindAttemptTrace(sink.BeginSendAttempt(durableIntegrationKey(seq), at), reused)
	}
	sink.RecordSendTrace(sendtrace.Event{TraceID: reused, Stage: sendtrace.StageMessageSendDurable, At: at, Duration: time.Second, Result: sendtrace.ResultOK})
	for _, seq := range []uint32{202, 203} {
		candidate := durableIntegrationCandidate(t, store, durableIntegrationKey(seq))
		if !candidate.TraceAmbiguous {
			t.Fatal("fixture must expose reused upper identity as ambiguous")
		}
		assertDurableUnknown(t, candidate)
	}

	invalid := []string{"fallback-trace", "ABCDEF0123456789abcdef0123456789", "00112233445566778899aabbccddeeff00"}
	for i, trace := range invalid {
		key := durableIntegrationKey(uint32(204 + i))
		sink.BindAttemptTrace(sink.BeginSendAttempt(key, at), trace)
		sink.RecordSendTrace(sendtrace.Event{TraceID: trace, Stage: sendtrace.StageMessageSendDurable, At: at, Duration: time.Second, Result: sendtrace.ResultOK})
		assertDurableUnknown(t, durableIntegrationCandidate(t, store, key))
	}

	remoteKey := durableIntegrationKey(207)
	_ = sink.BeginSendAttempt(remoteKey, at)
	sink.RecordSendTrace(sendtrace.Event{TraceID: "55555555555555555555555555555555", Stage: sendtrace.StageMessageSendDurable, At: at, Duration: time.Second, Result: sendtrace.ResultOK, NodeID: 2, ClientMsgNo: "same-original-and-retry"})
	assertDurableUnknown(t, durableIntegrationCandidate(t, store, remoteKey))
}

func TestAttemptDurablePhasePublicSinkSeparatesObservedZeroFromUnknown(t *testing.T) {
	at := time.Unix(40, 0)
	store := durableIntegrationStore()
	sink := diagnostics.NewSendTraceSink(diagnostics.NewStore(diagnostics.StoreOptions{Capacity: 8, Now: func() time.Time { return at }}), nil).WithAttemptStore(store)
	cases := []struct {
		name     string
		stage    sendtrace.Stage
		duration time.Duration
		result   sendtrace.Result
		observed bool
	}{
		{name: "observed_zero", stage: sendtrace.StageMessageSendDurable, result: sendtrace.ResultOK, observed: true},
		{name: "negative_duration", stage: sendtrace.StageMessageSendDurable, duration: -time.Nanosecond, result: sendtrace.ResultOK},
		{name: "invalid_result", stage: sendtrace.StageMessageSendDurable, duration: time.Second, result: "made-up"},
		{name: "unrelated_stage", stage: sendtrace.StageChannelAppendLocal, duration: time.Second, result: sendtrace.ResultOK},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key := durableIntegrationKey(uint32(301 + i))
			trace := fmt.Sprintf("%032x", i+301)
			sink.BindAttemptTrace(sink.BeginSendAttempt(key, at), trace)
			sink.RecordSendTrace(sendtrace.Event{TraceID: trace, Stage: tc.stage, At: at, Duration: tc.duration, Result: tc.result})
			candidate := durableIntegrationCandidate(t, store, key)
			if tc.observed {
				assertDurableDurationFact(t, candidate, 0, sendtrace.ResultOK)
				sink.RecordSendTrace(sendtrace.Event{TraceID: trace, Stage: tc.stage, At: at, Duration: time.Second, Result: sendtrace.ResultTimeout})
				assertDurableDurationFact(t, durableIntegrationCandidate(t, store, key), 0, sendtrace.ResultOK)
			} else {
				assertDurableUnknown(t, candidate)
			}
		})
	}
	unknownKey := durableIntegrationKey(305)
	_ = sink.BeginSendAttempt(unknownKey, at)
	assertDurableUnknown(t, durableIntegrationCandidate(t, store, unknownKey))
}

func TestAttemptDurablePhasePublicRecorderAlsoExposesOnlyDurationFact(t *testing.T) {
	at := time.Unix(50, 0)
	store := durableIntegrationStore()
	for i, start := range []time.Time{time.Time{}, at.Add(time.Millisecond), at.Add(-time.Second)} {
		key := durableIntegrationKey(uint32(401 + i))
		token := store.BeginSendAttempt(key, at)
		store.RecordAttemptPhase(token, sendtrace.AttemptPhase{Stage: sendtrace.StageMessageSendDurable, StartedAt: start, Duration: 3 * time.Second, Result: sendtrace.ResultTimeout, EndDerived: true})
		assertDurableDurationFact(t, durableIntegrationCandidate(t, store, key), 3*time.Second, sendtrace.ResultTimeout)
	}
	key := durableIntegrationKey(404)
	token := store.BeginSendAttempt(key, at)
	stages := []sendtrace.Stage{sendtrace.StageGatewayAsyncDispatchWait, sendtrace.StageGatewayMessagesSend, sendtrace.StageGatewayWriteSendack, sendtrace.StageGatewaySessionPublicationWait}
	for i, stage := range stages {
		start := at.Add(time.Duration(i+1) * time.Millisecond)
		store.RecordAttemptPhase(token, sendtrace.AttemptPhase{Stage: stage, StartedAt: start, Duration: time.Millisecond, Result: sendtrace.ResultOK})
	}
	candidate := durableIntegrationCandidate(t, store, key)
	for i, stage := range stages {
		phase := candidate.Phases[i]
		if !phase.Observed || phase.Stage != stage || !phase.StartedAt.Equal(at.Add(time.Duration(i+1)*time.Millisecond)) || !phase.EndDerived || phase.Duration != time.Millisecond {
			t.Fatalf("duration-only durable semantics changed existing Gateway interval %d: %+v", i, phase)
		}
	}
	assertDurableUnknown(t, candidate)
}

func TestAttemptDurablePhaseLateEventAfterRowOverwriteCannotBorrowNewOccupant(t *testing.T) {
	at := time.Unix(60, 0)
	store := durableIntegrationStore()
	sink := diagnostics.NewSendTraceSink(diagnostics.NewStore(diagnostics.StoreOptions{Capacity: 2, Now: func() time.Time { return at }}), nil).WithAttemptStore(store)
	oldKey := durableIntegrationKey(501)
	oldTrace := "66666666666666666666666666666666"
	sink.BindAttemptTrace(sink.BeginSendAttempt(oldKey, at), oldTrace)
	var last sendtrace.AttemptKey
	for i := uint32(1); i <= 65536; i++ {
		last = durableIntegrationKey(1000 + i)
		_ = sink.BeginSendAttempt(last, at)
	}
	sink.RecordSendTrace(sendtrace.Event{TraceID: oldTrace, Stage: sendtrace.StageMessageSendDurable, At: at, Duration: time.Second, Result: sendtrace.ResultOK})
	old := store.QuerySendAttempts(context.Background(), []sendtrace.AttemptKey{oldKey})
	if len(old.Results) != 1 || old.Results[0].Status != "not_retained" || len(old.Results[0].Candidates) != 0 {
		t.Fatalf("overwritten original was resurrected: %+v", old)
	}
	assertDurableUnknown(t, durableIntegrationCandidate(t, store, last))
}
