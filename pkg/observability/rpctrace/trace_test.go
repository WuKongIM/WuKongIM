package rpctrace_test

import (
	"context"
	"sync"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
)

func TestRecorderOwnsBoundedActiveSamplesUntilExactlyOneFinish(t *testing.T) {
	r, err := rpctrace.NewRecorder(1)
	if err != nil {
		t.Fatal(err)
	}
	x := r.Start(context.Background(), rpctrace.SideClient, 13)
	if x == nil || !x.Finish(rpctrace.ResultOK).Unbound {
		t.Fatal("pre-connection failure must remain unbound")
	}
	x = r.Start(context.Background(), rpctrace.SideClient, 13)
	if r.Start(context.Background(), rpctrace.SideClient, 13) != nil || r.Active() != 1 || r.Dropped() != 1 {
		t.Fatal("active trace limit did not reject observation only")
	}
	if x.Finish(rpctrace.ResultTimeout) == nil || x.Finish(rpctrace.ResultOK) != nil || r.Active() != 0 {
		t.Fatal("finish must return one snapshot and release once")
	}
	if y := r.Start(context.Background(), rpctrace.SideClient, 13); y == nil {
		t.Fatal("released credit unavailable")
	} else {
		y.Finish(rpctrace.ResultOK)
	}
}

func TestConnectionIdentityAndParentAreIndependentOfCancellation(t *testing.T) {
	r, err := rpctrace.NewRecorder(4)
	if err != nil {
		t.Fatal(err)
	}
	p := r.Start(context.Background(), rpctrace.SideServer, 13)
	token := [16]byte{1, 2, 3, 4}
	if !p.Bind(token, 7) {
		t.Fatal("valid transport identity rejected")
	}
	ctx, cancel := context.WithCancel(rpctrace.With(context.Background(), p))
	ctx = context.WithValue(ctx, unrelatedKey{}, "must not propagate")
	dst := rpctrace.Copy(context.Background(), ctx)
	cancel()
	if dst.Err() != nil || dst.Value(unrelatedKey{}) != nil || rpctrace.From(dst) != p {
		t.Fatal("copy changed independent execution context or lost trace")
	}
	child := r.Start(dst, rpctrace.SideClient, 14)
	if !child.Bind(token, 8) {
		t.Fatal("child binding failed")
	}
	s := child.Finish(rpctrace.ResultOK)
	if s.ParentTraceID != p.ID() || s.TraceID == p.ID() || s.Unbound {
		t.Fatalf("bad parent association: %+v", s)
	}
	p.Finish(rpctrace.ResultOK)
}

type unrelatedKey struct{}

func TestSampleKeepsIncompletePhasesAndDoesNotChangeAfterCompletion(t *testing.T) {
	r, _ := rpctrace.NewRecorder(1)
	x := r.Start(context.Background(), rpctrace.SideClient, 13)
	span := x.Begin(rpctrace.StageRequestWrite)
	s := x.Finish(rpctrace.ResultTimeout)
	if len(s.Spans) != 1 || !s.Spans[0].Incomplete || s.Spans[0].Result != rpctrace.ResultUnfinished {
		t.Fatalf("unfinished write hidden: %+v", s)
	}
	before := s.Clone()
	span.End(rpctrace.ResultOK)
	if s.Spans[0] != before.Spans[0] {
		t.Fatal("late writer changed published snapshot")
	}
	clone := s.Clone()
	clone.Spans[0].Result = rpctrace.ResultError
	if s.Spans[0].Result != rpctrace.ResultUnfinished {
		t.Fatal("clone aliases snapshot spans")
	}
}

func TestCompletedPhasesKeepLocalOffsetsAndSingleCompletion(t *testing.T) {
	r, _ := rpctrace.NewRecorder(1)
	x := r.Start(context.Background(), rpctrace.SideServer, 14)
	ctx := rpctrace.With(context.Background(), x)
	s := rpctrace.Begin(ctx, rpctrace.StageHandler)
	s.End(rpctrace.ResultOK)
	s.End(rpctrace.ResultError)
	out := x.Finish(rpctrace.ResultOK)
	if len(out.Spans) != 1 || out.Spans[0].Incomplete || out.Spans[0].Result != rpctrace.ResultOK {
		t.Fatalf("phase not completed exactly once: %+v", out)
	}
	if out.Spans[0].Offset < 0 || out.Spans[0].Duration < 0 || out.Spans[0].Offset+out.Spans[0].Duration > out.Duration {
		t.Fatal("local phase escapes enclosing RPC duration")
	}
}

func TestConcurrentPhaseOverflowIsExplicitAndBounded(t *testing.T) {
	r, _ := rpctrace.NewRecorder(1)
	x := r.Start(context.Background(), rpctrace.SideServer, 13)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); s := x.Begin(rpctrace.StageRecoveryOwner); s.End(rpctrace.ResultOK) }()
	}
	wg.Wait()
	out := x.Finish(rpctrace.ResultOK)
	if len(out.Spans) != 32 || out.DroppedSpans != 68 || r.Active() != 0 {
		t.Fatalf("unbounded or hidden loss: %+v", out)
	}
}

func TestDisabledAndInvalidObservationCannotAffectBusinessContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if rpctrace.With(ctx, nil) != ctx || rpctrace.Copy(ctx, context.Background()) != ctx {
		t.Fatal("disabled observation replaced context")
	}
	s := rpctrace.Begin(ctx, rpctrace.StageHandler)
	s.End(rpctrace.ResultOK)
	for _, limit := range []int{-1, 0, 4097} {
		if _, err := rpctrace.NewRecorder(limit); err == nil {
			t.Fatalf("invalid cap %d accepted", limit)
		}
	}
	r, _ := rpctrace.NewRecorder(1)
	x := r.Start(ctx, rpctrace.SideClient, 13)
	if x.Bind([16]byte{}, 7) || x.Bind([16]byte{1}, 0) {
		t.Fatal("invalid wire identity accepted")
	}
	x.Begin(rpctrace.Stage("arbitrary-user-input")).End(rpctrace.ResultOK)
	out := x.Finish(rpctrace.ResultOK)
	if len(out.Spans) != 0 || out.DroppedSpans != 1 {
		t.Fatal("unknown stage retained or hidden")
	}
}
