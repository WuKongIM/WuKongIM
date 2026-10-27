package replication

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
)

type localObservationKey struct{}
type localObservation struct{ calls int }

func (o *localObservation) BeginAppendPhase(rpctrace.Stage) rpctrace.AppendPhaseEnd {
	o.calls++
	return func(rpctrace.Result, int, func(int) rpctrace.Result) {}
}

type localObservationSubmitter struct{ ctx context.Context }

func (l *localObservationSubmitter) submitLocal(ctx context.Context, _ durableProposal, done func(durabilityCompletion)) error {
	l.ctx = ctx
	rpctrace.BeginAppendPhase(ctx, rpctrace.StageChannelRuntimeAppend).End(rpctrace.ResultOK)
	done(durabilityCompletion{})
	return nil
}

func TestLocalDurabilityObservationDoesNotReviveCallerCancellation(t *testing.T) {
	caller, cancel := context.WithCancel(context.WithValue(context.Background(), localObservationKey{}, "caller-only"))
	observer := &localObservation{}
	caller = rpctrace.WithAppendPhaseObserver(caller, observer)
	cancel()
	owner, stop := context.WithCancel(context.Background())
	defer stop()
	local := &localObservationSubmitter{}
	dispatcher := &batchingDurabilityDispatcher{ownerContext: owner, local: local}
	completed := 0
	if err := dispatcher.submitLocal(caller, durableProposal{}, func(durabilityCompletion) { completed++ }); err != nil {
		t.Fatal(err)
	}
	if completed != 1 || local.ctx == nil || local.ctx.Err() != nil || local.ctx.Value(localObservationKey{}) != nil {
		t.Fatal("admitted local work inherited the caller's context instead of its owner")
	}
	if observer.calls != 1 {
		t.Fatal("the owner context replacement discarded the exact append observer")
	}
	stop()
	if local.ctx.Err() != context.Canceled {
		t.Fatal("local runtime ownership was lost")
	}
}
