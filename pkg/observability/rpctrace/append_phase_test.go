package rpctrace_test

import (
	"context"
	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
	"testing"
)

func TestAppendPhaseVocabulary(t *testing.T) {
	stages := []rpctrace.Stage{"channel_meta_resolve", "channel_meta_apply", "channel_runtime_append", "channel_runtime_reserve", "channel_runtime_submit", "channel_runtime_future_wait"}
	r, _ := rpctrace.NewRecorder(1)
	trace := r.Start(context.Background(), rpctrace.SideServer, 21)
	for _, stage := range stages {
		trace.Begin(stage).End(rpctrace.ResultOK)
	}
	trace.Begin("channel_unrecognized").End(rpctrace.ResultOK)
	sample := trace.Finish(rpctrace.ResultOK)
	if len(sample.Spans) != 6 || sample.DroppedSpans != 1 {
		t.Fatalf("six phases not accepted with unknown rejected: %+v", sample)
	}
	for i, stage := range stages {
		if sample.Spans[i].Stage != stage {
			t.Fatalf("phase %d: %+v", i, sample.Spans[i])
		}
	}
	if rpctrace.MaxSpans != 32 {
		t.Fatal("span cap changed")
	}
}

type testAppendObserver struct {
	calls int
	stage rpctrace.Stage
	end   rpctrace.AppendPhaseEnd
}

func (o *testAppendObserver) BeginAppendPhase(stage rpctrace.Stage) rpctrace.AppendPhaseEnd {
	o.calls++
	o.stage = stage
	return o.end
}

func TestAppendPhasePortIsIndependentAndSynchronous(t *testing.T) {
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	observer := &testAppendObserver{end: func(common rpctrace.Result, count int, at func(int) rpctrace.Result) {
		calls++
		if count == -1 {
			if common != rpctrace.ResultTimeout || at != nil {
				t.Fatal("common contract changed")
			}
			return
		}
		if count != 1 || at(0) != rpctrace.ResultBusy {
			t.Fatal("item contract changed")
		}
	}}
	ctx := rpctrace.WithAppendPhaseObserver(base, observer)
	if rpctrace.From(ctx) != nil {
		t.Fatal("observer became caller trace")
	}
	rpctrace.BeginAppendPhase(ctx, rpctrace.StageChannelRuntimeAppend).End(rpctrace.ResultTimeout)
	consumed := false
	rpctrace.BeginAppendPhase(ctx, rpctrace.StageChannelRuntimeFutureWait).EndItems(1, func(int) rpctrace.Result { consumed = true; return rpctrace.ResultBusy })
	if !consumed || calls != 2 || observer.calls != 2 {
		t.Fatal("callback not consumed synchronously")
	}
	rpctrace.BeginAppendPhase(ctx, rpctrace.StageHandler).End(rpctrace.ResultOK)
	rpctrace.BeginAppendPhase(ctx, "unknown").End(rpctrace.ResultOK)
	if observer.calls != 2 {
		t.Fatal("port accepted stages outside its fixed append vocabulary")
	}
	cancel()
	if ctx.Err() != context.Canceled {
		t.Fatal("runtime cancellation lost")
	}
}

func TestAppendPhaseDisabledDoesNotReadResultsOrChangeContext(t *testing.T) {
	ctx := context.Background()
	if rpctrace.WithAppendPhaseObserver(ctx, nil) != ctx {
		t.Fatal("nil observer changed context")
	}
	var end rpctrace.AppendPhaseEnd
	end.End(rpctrace.ResultOK)
	end.EndItems(1, func(int) rpctrace.Result { t.Fatal("nil read results"); return rpctrace.ResultOK })
	rpctrace.BeginAppendPhase(nil, rpctrace.StageChannelMetaResolve).End(rpctrace.ResultOK)
	r, _ := rpctrace.NewRecorder(1)
	trace := r.Start(ctx, rpctrace.SideServer, 21)
	end = rpctrace.BeginAppendPhase(rpctrace.With(ctx, trace), rpctrace.StageChannelMetaApply)
	if end != nil {
		t.Fatal("single trace enabled shared port")
	}
	trace.Finish(rpctrace.ResultOK)
}
