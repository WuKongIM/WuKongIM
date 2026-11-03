//go:build integration

package message

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
)

// These are shared preparation intervals, not exclusive per-message CPU time.
// The spellings intentionally compile against the pre-observation baseline.
const preparationPermissionPhase = sendtrace.Stage("message.permission_batch")
const preparationPreAppendPhase = sendtrace.Stage("message.pre_append")

type preparationAttemptSink struct {
	mu      sync.Mutex
	enabled bool
	phases  map[sendtrace.AttemptToken][]sendtrace.AttemptPhase
}

func (s *preparationAttemptSink) RecordSendTrace(sendtrace.Event) {}
func (s *preparationAttemptSink) AttemptLinkEnabled() bool        { return s.enabled }
func (s *preparationAttemptSink) BeginSendAttempt(sendtrace.AttemptKey, time.Time) sendtrace.AttemptToken {
	return sendtrace.AttemptToken{}
}
func (s *preparationAttemptSink) BindAttemptTrace(sendtrace.AttemptToken, string) {}
func (s *preparationAttemptSink) RecordAttemptRPC(sendtrace.AttemptToken, sendtrace.RPCReference) {
}
func (s *preparationAttemptSink) RecordAttemptPhase(token sendtrace.AttemptToken, phase sendtrace.AttemptPhase) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.phases[token] = append(s.phases[token], phase)
}

func (s *preparationAttemptSink) check(t *testing.T, token sendtrace.AttemptToken, observer *recordingSendBatchStageObserver, expectPreAppend bool) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, check := range []struct {
		stage    sendtrace.Stage
		metric   string
		boundary string
		want     bool
	}{
		{preparationPermissionPhase, "permission", "shared_permission_batch", true},
		{preparationPreAppendPhase, "pre_append", "shared_pre_append_until_submit", expectPreAppend},
	} {
		var found []sendtrace.AttemptPhase
		for _, phase := range s.phases[token] {
			if phase.Stage == check.stage {
				found = append(found, phase)
			}
		}
		if !check.want {
			if len(found) != 0 {
				t.Fatalf("unmeasured preparation fabricated %s", check.stage)
			}
			continue
		}
		if len(found) != 1 {
			t.Fatalf("original token %d has %d %s facts, want one existing-clock fact", token.Ordinal, len(found), check.stage)
		}
		phase := found[0]
		if phase.StartedAt.IsZero() || phase.Duration < 0 || !phase.EndDerived || phase.Boundary != check.boundary {
			t.Fatalf("unqualified preparation interval: %+v", phase)
		}
		matched := false
		for _, metric := range observer.events {
			if metric.Stage == check.metric && metric.Duration == phase.Duration && metric.Result == string(phase.Result) {
				matched = true
			}
		}
		if !matched {
			t.Fatalf("%s did not borrow the already measured metric duration/result", check.stage)
		}
	}
}

func TestAttemptPreparationUsesOriginalContextAndExistingClocks(t *testing.T) {
	for _, async := range []bool{false, true} {
		for _, count := range []int{1, 2} {
			t.Run(fmt.Sprintf("async_%t_items_%d", async, count), func(t *testing.T) {
				sink := &preparationAttemptSink{enabled: true, phases: make(map[sendtrace.AttemptToken][]sendtrace.AttemptPhase)}
				restore := sendtrace.SetSink(sink)
				defer restore()
				observer := &recordingSendBatchStageObserver{}
				admission := &messageAdmissionProbe{}
				app := New(Options{Submitter: benchmarkMessageSubmitter{}, BatchAdmission: admission, SendBatchObserver: observer})
				items := asyncMessageItems(count)
				tokens := make([]sendtrace.AttemptToken, count)
				for i := range items {
					tokens[i] = sendtrace.AttemptToken{Epoch: [16]byte{8}, Ordinal: uint64(i + 1)}
					items[i].Context = sendtrace.WithGatewayAttempt(context.Background(), tokens[i])
					items[i].Command.ClientMsgNo = "same-logical-number"
				}
				var published []int
				completed := false
				defer func() {
					// An assertion failure still joins every fake accepted owner.
					if async && !completed {
						for _, job := range admission.jobs {
							job.succeed()
						}
					}
				}()
				emit := func(i int, result SendBatchItemResult) error {
					if result.Err != nil || result.Result.Reason != ReasonSuccess {
						t.Errorf("observation changed original result: %+v", result)
					}
					published = append(published, i)
					return result.Err
				}
				if async {
					if err := app.SubmitBatchEach(items, emit, func(err error) {
						if err != nil {
							t.Errorf("completion changed: %v", err)
						}
						completed = true
					}); err != nil {
						t.Fatal(err)
					}
					if completed || len(published) != 0 {
						t.Fatal("preparation observations completed accepted durable work")
					}
				} else if err := app.SendBatchEach(items, emit); err != nil {
					t.Fatal(err)
				}
				for _, token := range tokens {
					sink.check(t, token, observer, true)
				}
				if async {
					for i := len(admission.jobs) - 1; i >= 0; i-- {
						admission.jobs[i].succeed()
					}
					if !completed {
						t.Fatal("accepted owner failed to join")
					}
				}
				if len(published) != count {
					t.Fatalf("publication count %d want %d", len(published), count)
				}
				for i, index := range published {
					if index != i {
						t.Fatal("same-session publication order changed")
					}
				}
			})
		}
	}
}

func TestAttemptPreparationDisabledAndNoTokenStaySilent(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		sink := &preparationAttemptSink{enabled: enabled, phases: make(map[sendtrace.AttemptToken][]sendtrace.AttemptPhase)}
		restore := sendtrace.SetSink(sink)
		items := asyncMessageItems(1)
		if !enabled {
			items[0].Context = sendtrace.WithGatewayAttempt(context.Background(), sendtrace.AttemptToken{Epoch: [16]byte{8}, Ordinal: 1})
		}
		result := New(Options{Submitter: benchmarkMessageSubmitter{}}).SendBatch(items)
		restore()
		if len(result) != 1 || result[0].Err != nil || len(sink.phases) != 0 {
			t.Fatal("disabled/no-token observation changed result or retained facts")
		}
	}
}

func TestAttemptPreparationUnmeasuredDirectoryFailureStaysUnknown(t *testing.T) {
	sink := &preparationAttemptSink{enabled: true, phases: make(map[sendtrace.AttemptToken][]sendtrace.AttemptPhase)}
	restore := sendtrace.SetSink(sink)
	defer restore()
	observer := &recordingSendBatchStageObserver{}
	want := errors.New("directory-failure")
	app := New(Options{Submitter: benchmarkMessageSubmitter{}, PersonDirectory: &recordingPersonDirectoryEnsurer{err: want}, SendBatchObserver: observer})
	token := sendtrace.AttemptToken{Epoch: [16]byte{8}, Ordinal: 1}
	item := SendBatchItem{Context: sendtrace.WithGatewayAttempt(context.Background(), token), Command: SendCommand{FromUID: "u1", ChannelID: "u1@u2", ChannelType: channelTypePerson}}
	results := app.SendBatch([]SendBatchItem{item})
	if len(results) != 1 || !errors.Is(results[0].Err, want) {
		t.Fatal("original directory failure changed")
	}
	sink.check(t, token, observer, false)
}

func TestAttemptPreparationHookDenialKeepsExistingOutcomeAndClock(t *testing.T) {
	sink := &preparationAttemptSink{enabled: true, phases: make(map[sendtrace.AttemptToken][]sendtrace.AttemptPhase)}
	restore := sendtrace.SetSink(sink)
	defer restore()
	observer := &recordingSendBatchStageObserver{}
	var calls []string
	app := New(Options{Submitter: benchmarkMessageSubmitter{}, SendHook: prefixHook{order: &calls, reject: "reject"}, SendBatchObserver: observer})
	token := sendtrace.AttemptToken{Epoch: [16]byte{8}, Ordinal: 1}
	item := SendBatchItem{Context: sendtrace.WithGatewayAttempt(context.Background(), token), Command: SendCommand{FromUID: "u1", ChannelID: "g1", ChannelType: channelTypeGroup, ClientMsgNo: "reject"}}
	results := app.SendBatch([]SendBatchItem{item})
	if len(results) != 1 || results[0].Err != nil || results[0].Result.Reason != ReasonSendBan || len(calls) != 1 {
		t.Fatal("original hook denial or callback count changed")
	}
	sink.check(t, token, observer, true)
}
