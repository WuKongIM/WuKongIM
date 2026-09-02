package message

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/commit"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	channel "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
)

var compatibilityErrorType = reflect.TypeOf((*error)(nil)).Elem()
var contextType = reflect.TypeOf((*context.Context)(nil)).Elem()

// Channel stores may outlive their owning engine during shutdown. The entire
// compatibility surface must reject such calls consistently and never touch a
// released lease.
func TestNilChannelStoreRejectsTheCompleteCompatibilitySurface(t *testing.T) {
	assertCompatibilityErrorSurface(t, (*ChannelStore)(nil), map[string]error{"Close": nil}, channel.ErrInvalidArgument)
	if got := (*ChannelStore)(nil).LEO(); got != 0 {
		t.Fatalf("LEO() = %d, want 0", got)
	}
}

func TestNilEngineDistinguishesInvalidIdentityFromClosedStorage(t *testing.T) {
	assertCompatibilityErrorSurface(t, (*Engine)(nil), map[string]error{
		"Close":           nil,
		"ForChannel":      channel.ErrInvalidArgument,
		"ListChannelKeys": channel.ErrInvalidArgument,
		"Read":            channel.ErrInvalidArgument,
		"ReadReverse":     channel.ErrInvalidArgument,
	}, channel.ErrClosed)

	var engine *Engine
	if got := engine.CommitCoordinatorConfig(); got.FlushWindow != defaultCommitCoordinatorFlushWindow || got.QueueSize != defaultCommitCoordinatorQueueSize || got.Shards != 1 {
		t.Fatalf("nil engine config = %+v", got)
	}
	engine.ConfigureCommitCoordinator(CommitCoordinatorConfig{QueueSize: 7})
}

func TestCommitCoordinatorCompatibilityPolicyAndObservability(t *testing.T) {
	observer := &recordingCommitObserver{}
	cfg := effectiveCommitCoordinatorConfig(CommitCoordinatorConfig{
		FlushWindow: 2 * time.Millisecond,
		QueueSize:   4,
		Shards:      3,
		MaxRequests: 5,
		MaxRecords:  6,
		MaxBytes:    7,
		Observer:    observer,
	})
	if cfg.FlushWindow != 2*time.Millisecond || cfg.QueueSize != 4 || cfg.Shards != 3 {
		t.Fatalf("effective config = %+v", cfg)
	}
	commitCfg := commitCoordinatorConfig(cfg)
	if commitCfg.FlushWindow != cfg.FlushWindow || commitCfg.QueueSize != 4 || commitCfg.Shards != 3 || commitCfg.MaxRequests != 5 || commitCfg.MaxRecords != 6 || commitCfg.MaxBytes != 7 || commitCfg.Observer == nil {
		t.Fatalf("commit config = %+v", commitCfg)
	}

	adapter := commitObserverAdapter{observer: observer, queueSize: 12}
	adapter.SetQueueDepth(3)
	if observer.queueDepth != 3 || observer.queueCapacity != 12 {
		t.Fatalf("queue observation = (%d,%d), want (3,12)", observer.queueDepth, observer.queueCapacity)
	}
	batchErr := errors.New("commit failed")
	adapter.ObserveBatch(commit.BatchEvent{
		Requests: 2, Records: 4, Bytes: 99,
		CollectDuration: time.Millisecond, BuildDuration: 2 * time.Millisecond,
		CommitDuration: 3 * time.Millisecond, PublishDuration: 4 * time.Millisecond,
		TotalDuration: 10 * time.Millisecond, Err: batchErr,
	})
	if observer.batch.Requests != 2 || observer.batch.Records != 4 || observer.batch.Bytes != 99 || !errors.Is(observer.batch.Err, batchErr) {
		t.Fatalf("batch observation = %+v", observer.batch)
	}

	requestCases := []struct {
		name string
		err  error
		want string
	}{
		{name: "ok", want: "ok"},
		{name: "timeout", err: context.DeadlineExceeded, want: "timeout"},
		{name: "canceled", err: context.Canceled, want: "canceled"},
		{name: "closed", err: commit.ErrClosed, want: "closed"},
		{name: "invalid", err: dberrors.ErrInvalidArgument, want: "invalid"},
		{name: "other", err: errors.New("other"), want: "err"},
	}
	for _, test := range requestCases {
		t.Run(test.name, func(t *testing.T) {
			adapter.ObserveRequest(commit.RequestEvent{Lane: commit.Lane{Name: "replica"}, Records: 2, Bytes: 8, Duration: time.Second, Err: test.err})
			if observer.request.Lane != "replica" || observer.request.Records != 2 || observer.request.Bytes != 8 || observer.request.Duration != time.Second || observer.request.Result != test.want {
				t.Fatalf("request observation = %+v, want result %q", observer.request, test.want)
			}
		})
	}
	if got := commitCoordinatorLaneName(commit.Lane{}); got != "default" {
		t.Fatalf("empty lane name = %q, want default", got)
	}

	legacy := &legacyCommitObserver{}
	legacyAdapter := commitObserverAdapter{observer: legacy}
	legacyAdapter.SetQueueDepth(9)
	legacyAdapter.ObserveRequest(commit.RequestEvent{})
	if legacy.depth != 9 {
		t.Fatalf("legacy queue depth = %d, want 9", legacy.depth)
	}
}

func TestAppendOutcomeClassificationPreservesDurabilityEvidence(t *testing.T) {
	if got := appendOutcomeForPreCommitError(channel.ErrCorruptState); got != quorumlog.AppendOutcomeConflict {
		t.Fatalf("corrupt-state precommit outcome = %v", got)
	}
	if got := appendOutcomeForPreCommitError(dberrors.ErrConflict); got != quorumlog.AppendOutcomeConflict {
		t.Fatalf("conflict precommit outcome = %v", got)
	}
	if got := appendOutcomeForPreCommitError(context.Canceled); got != quorumlog.AppendOutcomeDefinitelyNotWritten {
		t.Fatalf("canceled precommit outcome = %v", got)
	}
	tests := []struct {
		result commit.SubmitResult
		want   quorumlog.AppendOutcome
	}{
		{result: commit.SubmitResult{Outcome: commit.OutcomeCommitted}, want: quorumlog.AppendOutcomeDurable},
		{result: commit.SubmitResult{Outcome: commit.OutcomeDefinitelyNotCommitted, Err: dberrors.ErrConflict}, want: quorumlog.AppendOutcomeConflict},
		{result: commit.SubmitResult{Outcome: commit.OutcomeDefinitelyNotCommitted, Err: context.Canceled}, want: quorumlog.AppendOutcomeDefinitelyNotWritten},
		{result: commit.SubmitResult{Outcome: commit.OutcomeUnknown}, want: quorumlog.AppendOutcomeUnknown},
	}
	for _, test := range tests {
		if got := appendOutcomeForCommitResult(test.result); got != test.want {
			t.Fatalf("append outcome for %+v = %v, want %v", test.result, got, test.want)
		}
	}
}

func TestCompatibilityErrorMappingRetainsPublicErrorClasses(t *testing.T) {
	tests := []struct {
		input error
		want  error
	}{
		{input: nil, want: nil},
		{input: dberrors.ErrClosed, want: channel.ErrClosed},
		{input: dberrors.ErrInvalidArgument, want: channel.ErrInvalidArgument},
		{input: dberrors.ErrCorruptValue, want: channel.ErrCorruptValue},
		{input: dberrors.ErrChecksumMismatch, want: channel.ErrCorruptValue},
		{input: dberrors.ErrCorruptState, want: channel.ErrCorruptState},
		{input: dberrors.ErrConflict, want: channel.ErrCorruptState},
		{input: context.Canceled, want: context.Canceled},
	}
	for _, test := range tests {
		if got := toChannelError(test.input); !errors.Is(got, test.want) {
			t.Fatalf("toChannelError(%v) = %v, want class %v", test.input, got, test.want)
		}
	}
}

type recordingCommitObserver struct {
	queueDepth    int
	queueCapacity int
	batch         CommitCoordinatorBatchEvent
	request       CommitCoordinatorRequestEvent
}

func (o *recordingCommitObserver) SetCommitCoordinatorQueueDepth(depth int) {
	o.queueDepth = depth
}

func (o *recordingCommitObserver) SetCommitCoordinatorQueue(depth int, capacity int) {
	o.queueDepth = depth
	o.queueCapacity = capacity
}

func (o *recordingCommitObserver) ObserveCommitCoordinatorBatch(event CommitCoordinatorBatchEvent) {
	o.batch = event
}

func (o *recordingCommitObserver) ObserveCommitCoordinatorRequest(event CommitCoordinatorRequestEvent) {
	o.request = event
}

type legacyCommitObserver struct {
	depth int
}

func (o *legacyCommitObserver) SetCommitCoordinatorQueueDepth(depth int) {
	o.depth = depth
}

func (*legacyCommitObserver) ObserveCommitCoordinatorBatch(CommitCoordinatorBatchEvent) {}

func assertCompatibilityErrorSurface(t *testing.T, receiver any, exceptions map[string]error, want error) {
	t.Helper()
	typeOfReceiver := reflect.TypeOf(receiver)
	valueOfReceiver := reflect.ValueOf(receiver)
	for index := 0; index < typeOfReceiver.NumMethod(); index++ {
		method := typeOfReceiver.Method(index)
		bound := valueOfReceiver.Method(index)
		methodType := bound.Type()
		if methodType.NumOut() == 0 || !methodType.Out(methodType.NumOut()-1).Implements(compatibilityErrorType) {
			continue
		}
		t.Run(method.Name, func(t *testing.T) {
			arguments := make([]reflect.Value, methodType.NumIn())
			for argument := range arguments {
				argumentType := methodType.In(argument)
				if argumentType == contextType {
					arguments[argument] = reflect.ValueOf(context.Background())
				} else {
					arguments[argument] = reflect.Zero(argumentType)
				}
			}
			var results []reflect.Value
			if methodType.IsVariadic() {
				results = bound.CallSlice(arguments)
			} else {
				results = bound.Call(arguments)
			}
			last := results[len(results)-1]
			var got error
			if !last.IsNil() {
				got = last.Interface().(error)
			}
			if exception, ok := exceptions[method.Name]; ok {
				if !errors.Is(got, exception) {
					t.Fatalf("error = %v, want %v", got, exception)
				}
				return
			}
			if !errors.Is(got, want) {
				t.Fatalf("error = %v, want %v", got, want)
			}
		})
	}
}
