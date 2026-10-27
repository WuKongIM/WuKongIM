package rpctrace_test

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
)

type appendCopyKey struct{}

func TestCopySharedAppendObserverPreservesDestinationOwnership(t *testing.T) {
	source, cancel := context.WithCancel(context.WithValue(context.Background(), appendCopyKey{}, "caller-only"))
	observer := &testAppendObserver{end: func(rpctrace.Result, int, func(int) rpctrace.Result) {}}
	source = rpctrace.WithAppendPhaseObserver(source, observer)
	destination, stop := context.WithCancel(context.Background())
	defer stop()
	copied := rpctrace.Copy(destination, source)
	cancel()
	if copied.Err() != nil || copied.Value(appendCopyKey{}) != nil || rpctrace.From(copied) != nil {
		t.Fatal("observation copy inherited caller ownership or became a single Trace")
	}
	rpctrace.BeginAppendPhase(copied, rpctrace.StageChannelRuntimeAppend).End(rpctrace.ResultOK)
	if observer.calls != 1 {
		t.Fatal("shared append observer disappeared at the detached boundary")
	}
	stop()
	if copied.Err() != context.Canceled {
		t.Fatal("destination cancellation was not preserved")
	}
}

func TestSharedAppendLowerStagesRemainClosed(t *testing.T) {
	observer := &testAppendObserver{end: func(rpctrace.Result, int, func(int) rpctrace.Result) {}}
	ctx := rpctrace.WithAppendPhaseObserver(context.Background(), observer)
	stages := []rpctrace.Stage{"channel_local_queue", "channel_local_sync", "channel_peer_wait", "channel_quorum_decision", "channel_reactor_complete"}
	for _, stage := range stages {
		rpctrace.BeginAppendPhase(ctx, stage).End(rpctrace.ResultOK)
	}
	if observer.calls != len(stages) {
		t.Fatal("the fixed lower append vocabulary is unavailable")
	}
	rpctrace.BeginAppendPhase(ctx, rpctrace.StageHandler).End(rpctrace.ResultOK)
	rpctrace.BeginAppendPhase(ctx, "arbitrary-stage").End(rpctrace.ResultOK)
	if observer.calls != len(stages) {
		t.Fatal("the lower append port accepted an unrestricted stage")
	}
	if rpctrace.MaxSpans != 32 {
		t.Fatal("observability widened the original fixed trace bound")
	}
}
