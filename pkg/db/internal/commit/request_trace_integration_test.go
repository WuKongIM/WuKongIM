//go:build integration

package commit_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/commit"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
)

func TestCoordinatorRequestTracePhysicalStages(t *testing.T) {
	db := openTestDB(t)
	synctest.Test(t, func(t *testing.T) {
		c := commit.NewCoordinator(db, commit.Config{QueueSize: 1, MaxRequests: 1})
		build, durable, publish := newCommitTraceLatch(), newCommitTraceLatch(), newCommitTraceLatch()
		defer func() { build.release(); durable.release(); publish.release(); c.Close() }()
		c.SetCommitFunc(func(*engine.Batch) error { durable.block(); return nil })
		trace, ctx := newCommitTrace(t)
		var finalized atomic.Int64
		result := make(chan commit.SubmitResult, 1)
		go func() {
			result <- c.SubmitWithOutcome(ctx, commit.Request{
				Build:    func(*engine.Batch) error { build.block(); return nil },
				Publish:  func() error { publish.block(); return nil },
				Finalize: func() { finalized.Add(1) },
			})
		}()
		<-build.entered
		time.Sleep(3 * time.Millisecond)
		build.release()
		<-durable.entered
		time.Sleep(5 * time.Millisecond)
		durable.release()
		<-publish.entered
		if finalized.Load() != 0 {
			t.Fatal("finalized before physical publish completed")
		}
		time.Sleep(7 * time.Millisecond)
		publish.release()
		got := <-result
		if got.Err != nil || got.Outcome != commit.OutcomeCommitted || finalized.Load() != 1 {
			t.Fatalf("result=%+v finalized=%d", got, finalized.Load())
		}
		sample := trace.Finish(rpctrace.ResultOK)
		assertCommitTraceNames(t, sample, "commit_admission", "commit_queue", "commit_collect", "commit_build", "commit_durable", "commit_publish")
		for stage, duration := range map[string]time.Duration{"commit_build": 3 * time.Millisecond, "commit_durable": 5 * time.Millisecond, "commit_publish": 7 * time.Millisecond} {
			span := commitTraceSpan(t, sample, stage, 0)
			if span.Duration != duration || span.Incomplete || span.Result != rpctrace.ResultOK {
				t.Fatalf("%s=%+v, want %s completed", stage, span, duration)
			}
		}
		assertCommitTraceWithinSample(t, sample)
	})
}

func TestCoordinatorRequestTraceAdmissionQueueCancellationAndLateCompletion(t *testing.T) {
	db := openTestDB(t)
	synctest.Test(t, func(t *testing.T) {
		c := commit.NewCoordinator(db, commit.Config{QueueSize: 1, MaxRequests: 1})
		first := newCommitTraceLatch()
		defer func() { first.release(); c.Close() }()
		c.SetCommitFunc(func(*engine.Batch) error { return nil })
		firstResult := make(chan commit.SubmitResult, 1)
		go func() {
			firstResult <- c.SubmitWithOutcome(context.Background(), commit.Request{Build: func(*engine.Batch) error { first.block(); return nil }})
		}()
		<-first.entered
		queuedTrace, queuedBase := newCommitTrace(t)
		queuedCtx, cancelQueued := context.WithCancel(queuedBase)
		defer cancelQueued()
		queuedResult := make(chan commit.SubmitResult, 1)
		var queuedBuilt, finalized atomic.Int64
		go func() {
			queuedResult <- c.SubmitWithOutcome(queuedCtx, commit.Request{Build: func(*engine.Batch) error { queuedBuilt.Add(1); return nil }, Finalize: func() { finalized.Add(1) }})
		}()
		synctest.Wait()
		admissionTrace, admissionBase := newCommitTrace(t)
		admissionCtx, cancelAdmission := context.WithCancel(admissionBase)
		defer cancelAdmission()
		admissionResult := make(chan commit.SubmitResult, 1)
		go func() {
			admissionResult <- c.SubmitWithOutcome(admissionCtx, commit.Request{Build: func(*engine.Batch) error { t.Error("pre-admission canceled build executed"); return nil }})
		}()
		synctest.Wait()
		time.Sleep(2 * time.Millisecond)
		cancelAdmission()
		got := <-admissionResult
		if got.Outcome != commit.OutcomeDefinitelyNotCommitted || !errors.Is(got.Err, context.Canceled) {
			t.Fatalf("pre-admission result=%+v", got)
		}
		admissionSample := admissionTrace.Finish(rpctrace.ResultCanceled)
		assertCommitTraceNames(t, admissionSample, "commit_admission")
		if span := commitTraceSpan(t, admissionSample, "commit_admission", 0); span.Duration != 2*time.Millisecond || span.Incomplete || span.Result != rpctrace.ResultCanceled {
			t.Fatalf("admission=%+v", span)
		}
		cancelQueued()
		got = <-queuedResult
		if got.Outcome != commit.OutcomeUnknown || !errors.Is(got.Err, context.Canceled) || finalized.Load() != 0 {
			t.Fatalf("admitted result=%+v finalized=%d", got, finalized.Load())
		}
		queuedSample := queuedTrace.Finish(rpctrace.ResultCanceled)
		assertCommitTraceNames(t, queuedSample, "commit_admission", "commit_queue")
		if span := commitTraceSpan(t, queuedSample, "commit_queue", 0); span.Duration != 2*time.Millisecond || !span.Incomplete {
			t.Fatalf("queue=%+v", span)
		}
		before := queuedSample.Clone()
		first.release()
		if got := <-firstResult; got.Err != nil {
			t.Fatalf("first=%+v", got)
		}
		synctest.Wait()
		if finalized.Load() != 1 || queuedBuilt.Load() != 1 {
			t.Fatalf("admitted mutation lost: built=%d final=%d", queuedBuilt.Load(), finalized.Load())
		}
		if !reflect.DeepEqual(before, *queuedSample) {
			t.Fatal("late physical completion mutated finished RPC sample")
		}
	})
}

func TestCoordinatorRequestTraceQueueIncludesOwnerBufferAndCloseIsNotBatchEntry(t *testing.T) {
	db := openTestDB(t)
	synctest.Test(t, func(t *testing.T) {
		observer := &recordingObserver{depthCh: make(chan int, 32)}
		c := commit.NewCoordinator(db, commit.Config{QueueSize: 2, MaxBytes: 3, FlushWindow: time.Hour, Observer: observer})
		first := newCommitTraceLatch()
		defer func() { first.release(); c.Close() }()
		c.SetCommitFunc(func(*engine.Batch) error { return nil })
		firstResult := make(chan commit.SubmitResult, 1)
		go func() {
			firstResult <- c.SubmitWithOutcome(context.Background(), commit.Request{Bytes: 2, Build: func(*engine.Batch) error { first.block(); return nil }})
		}()
		<-observer.depthCh
		trace, ctx := newCommitTrace(t)
		result := make(chan commit.SubmitResult, 1)
		var built, finalized atomic.Int64
		go func() {
			result <- c.SubmitWithOutcome(ctx, commit.Request{Bytes: 2, Build: func(*engine.Batch) error { built.Add(1); return nil }, Finalize: func() { finalized.Add(1) }})
		}()
		// The one-hour window can reach the first Build only after the second
		// request was drained and deferred by MaxBytes into the owner buffer.
		<-first.entered
		time.Sleep(11 * time.Millisecond)
		closed := make(chan struct{})
		go func() { c.Close(); close(closed) }()
		synctest.Wait()
		first.release()
		<-closed
		if got := <-firstResult; got.Err != nil {
			t.Fatalf("first=%+v", got)
		}
		got := <-result
		if got.Outcome != commit.OutcomeDefinitelyNotCommitted || !errors.Is(got.Err, commit.ErrClosed) || built.Load() != 0 || finalized.Load() != 1 {
			t.Fatalf("deferred=%+v built=%d finalized=%d", got, built.Load(), finalized.Load())
		}
		sample := trace.Finish(rpctrace.ResultStopped)
		assertCommitTraceNames(t, sample, "commit_admission", "commit_queue")
		span := commitTraceSpan(t, sample, "commit_queue", 0)
		if span.Duration != 11*time.Millisecond || span.Incomplete || span.Result != rpctrace.ResultStopped {
			t.Fatalf("owner-buffer queue=%+v", span)
		}
	})
}

func TestCoordinatorRequestTraceGroupStagesOverlapWithoutSplittingPhysicalCommit(t *testing.T) {
	db := openTestDB(t)
	synctest.Test(t, func(t *testing.T) {
		c := commit.NewCoordinator(db, commit.Config{QueueSize: 4, FlushWindow: 5 * time.Millisecond, MaxRequests: 4})
		buildA, buildB, durable, publishA, publishB := newCommitTraceLatch(), newCommitTraceLatch(), newCommitTraceLatch(), newCommitTraceLatch(), newCommitTraceLatch()
		defer func() {
			buildA.release()
			buildB.release()
			durable.release()
			publishA.release()
			publishB.release()
			c.Close()
		}()
		var commits atomic.Int64
		c.SetCommitFunc(func(*engine.Batch) error { commits.Add(1); durable.block(); return nil })
		traceA, ctxA := newCommitTrace(t)
		traceB, ctxB := newCommitTrace(t)
		resultA, resultB := make(chan commit.SubmitResult, 1), make(chan commit.SubmitResult, 1)
		go func() {
			resultA <- c.SubmitWithOutcome(ctxA, commit.Request{Build: func(*engine.Batch) error { buildA.block(); return nil }, Publish: func() error { publishA.block(); return nil }})
		}()
		synctest.Wait()
		time.Sleep(2 * time.Millisecond)
		go func() {
			resultB <- c.SubmitWithOutcome(ctxB, commit.Request{Build: func(*engine.Batch) error { buildB.block(); return nil }, Publish: func() error { publishB.block(); return nil }})
		}()
		synctest.Wait()
		time.Sleep(3 * time.Millisecond)
		<-buildA.entered
		time.Sleep(7 * time.Millisecond)
		buildA.release()
		<-buildB.entered
		time.Sleep(11 * time.Millisecond)
		buildB.release()
		<-durable.entered
		time.Sleep(13 * time.Millisecond)
		durable.release()
		<-publishA.entered
		time.Sleep(17 * time.Millisecond)
		publishA.release()
		<-publishB.entered
		time.Sleep(19 * time.Millisecond)
		publishB.release()
		for _, got := range []commit.SubmitResult{<-resultA, <-resultB} {
			if got.Err != nil || got.Outcome != commit.OutcomeCommitted {
				t.Fatalf("result=%+v", got)
			}
		}
		if commits.Load() != 1 {
			t.Fatalf("physical commits=%d, want 1", commits.Load())
		}
		a, b := traceA.Finish(rpctrace.ResultOK), traceB.Finish(rpctrace.ResultOK)
		for _, sample := range []*rpctrace.Sample{a, b} {
			assertCommitTraceNames(t, sample, "commit_admission", "commit_queue", "commit_collect", "commit_build", "commit_durable", "commit_publish")
			for stage, duration := range map[string]time.Duration{"commit_build": 18 * time.Millisecond, "commit_durable": 13 * time.Millisecond, "commit_publish": 36 * time.Millisecond} {
				if span := commitTraceSpan(t, sample, stage, 0); span.Duration != duration || span.Incomplete {
					t.Fatalf("%s=%+v, want shared physical duration %s", stage, span, duration)
				}
			}
		}
		if got := commitTraceSpan(t, a, "commit_collect", 0).Duration; got != 5*time.Millisecond {
			t.Fatalf("first collect=%s", got)
		}
		if got := commitTraceSpan(t, b, "commit_collect", 0).Duration; got != 3*time.Millisecond {
			t.Fatalf("late join collect=%s", got)
		}
		for _, stage := range []string{"commit_build", "commit_durable", "commit_publish"} {
			x, y := commitTraceSpan(t, a, stage, 0), commitTraceSpan(t, b, stage, 0)
			if !a.At.Add(x.Offset).Equal(b.At.Add(y.Offset)) {
				t.Fatalf("%s does not cover same physical group", stage)
			}
		}
	})
}

func TestCoordinatorRequestTraceFailureStagesAndShardedRouting(t *testing.T) {
	cause := errors.New("controlled request failure")
	for _, tc := range []struct {
		name                            string
		shards                          int
		buildErr, commitErr, publishErr error
		outcome                         commit.Outcome
		stages                          []string
	}{
		{"build", 1, cause, nil, nil, commit.OutcomeDefinitelyNotCommitted, []string{"commit_admission", "commit_queue", "commit_collect", "commit_build"}},
		{"durable", 1, nil, cause, nil, commit.OutcomeUnknown, []string{"commit_admission", "commit_queue", "commit_collect", "commit_build", "commit_durable"}},
		{"publish", 1, nil, nil, cause, commit.OutcomeCommitted, []string{"commit_admission", "commit_queue", "commit_collect", "commit_build", "commit_durable", "commit_publish"}},
		{"sharded", 2, nil, nil, nil, commit.OutcomeCommitted, []string{"commit_admission", "commit_queue", "commit_collect", "commit_build", "commit_durable", "commit_publish"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := openTestDB(t)
			c := commit.NewCoordinator(db, commit.Config{QueueSize: 1, MaxRequests: 1, Shards: tc.shards})
			defer c.Close()
			var committed, published, finalized atomic.Int64
			c.SetCommitFunc(func(*engine.Batch) error { committed.Add(1); return tc.commitErr })
			trace, ctx := newCommitTrace(t)
			got := c.SubmitWithOutcome(ctx, commit.Request{Partition: "partition", Build: func(*engine.Batch) error { return tc.buildErr }, Publish: func() error { published.Add(1); return tc.publishErr }, Finalize: func() { finalized.Add(1) }})
			wantErr := tc.buildErr
			if wantErr == nil {
				wantErr = tc.commitErr
			}
			if wantErr == nil {
				wantErr = tc.publishErr
			}
			if got.Outcome != tc.outcome || !errors.Is(got.Err, wantErr) || finalized.Load() != 1 {
				t.Fatalf("result=%+v finalized=%d", got, finalized.Load())
			}
			sample := trace.Finish(rpctrace.ResultFor(got.Err))
			assertCommitTraceNames(t, sample, tc.stages...)
			last := sample.Spans[len(sample.Spans)-1]
			if last.Incomplete || last.Result != rpctrace.ResultFor(got.Err) {
				t.Fatalf("terminal phase=%+v", last)
			}
			if tc.buildErr != nil && committed.Load() != 0 {
				t.Fatal("build failure issued physical commit")
			}
			if (tc.buildErr != nil || tc.commitErr != nil) && published.Load() != 0 {
				t.Fatal("failed physical path published")
			}
		})
	}
}

func TestCoordinatorRequestTraceGroupAbortRebuildIsOneBoundedAttempt(t *testing.T) {
	db := openTestDB(t)
	synctest.Test(t, func(t *testing.T) {
		c := commit.NewCoordinator(db, commit.Config{QueueSize: 2, FlushWindow: time.Hour, MaxRequests: 2})
		defer c.Close()
		var commits, builds, finalized atomic.Int64
		c.SetCommitFunc(func(*engine.Batch) error { commits.Add(1); return nil })
		goodTrace, goodCtx := newCommitTrace(t)
		badTrace, badCtx := newCommitTrace(t)
		good, bad := make(chan commit.SubmitResult, 1), make(chan commit.SubmitResult, 1)
		go func() {
			good <- c.SubmitWithOutcome(goodCtx, commit.Request{RebuildOnGroupAbort: true, Build: func(*engine.Batch) error { builds.Add(1); return nil }, Finalize: func() { finalized.Add(1) }})
		}()
		synctest.Wait()
		cause := errors.New("neighbor build rejected")
		go func() {
			bad <- c.SubmitWithOutcome(badCtx, commit.Request{RebuildOnGroupAbort: true, Build: func(*engine.Batch) error { return cause }, Finalize: func() { finalized.Add(1) }})
		}()
		if got := <-good; got.Outcome != commit.OutcomeCommitted || got.Err != nil {
			t.Fatalf("good=%+v", got)
		}
		if got := <-bad; got.Outcome != commit.OutcomeDefinitelyNotCommitted || !errors.Is(got.Err, cause) {
			t.Fatalf("bad=%+v", got)
		}
		if builds.Load() != 2 || commits.Load() != 1 || finalized.Load() != 2 {
			t.Fatalf("builds=%d commits=%d finalized=%d", builds.Load(), commits.Load(), finalized.Load())
		}
		sample := goodTrace.Finish(rpctrace.ResultOK)
		assertCommitTraceNames(t, sample, "commit_admission", "commit_queue", "commit_collect", "commit_build", "commit_build", "commit_durable", "commit_publish")
		if commitTraceSpan(t, sample, "commit_build", 0).Result != rpctrace.ResultError || commitTraceSpan(t, sample, "commit_build", 1).Result != rpctrace.ResultOK {
			t.Fatal("isolated reconstruction hid original failed group stage")
		}
		badSample := badTrace.Finish(rpctrace.ResultError)
		assertCommitTraceNames(t, badSample, "commit_admission", "commit_queue", "commit_collect", "commit_build")
	})
}

func TestCoordinatorRequestTraceDisabledInvalidAndOverflowRemainObservational(t *testing.T) {
	db := openTestDB(t)
	c := commit.NewCoordinator(db, commit.Config{QueueSize: 1, MaxRequests: 1})
	defer c.Close()
	c.SetCommitFunc(func(*engine.Batch) error { return nil })
	for _, ctx := range []context.Context{nil, context.Background()} {
		if got := c.SubmitWithOutcome(ctx, commit.Request{Build: func(*engine.Batch) error { return nil }}); got.Err != nil || got.Outcome != commit.OutcomeCommitted {
			t.Fatalf("disabled=%+v", got)
		}
	}
	trace, ctx := newCommitTrace(t)
	got := c.SubmitWithOutcome(ctx, commit.Request{})
	if got.Outcome != commit.OutcomeDefinitelyNotCommitted || !errors.Is(got.Err, dberrors.ErrInvalidArgument) {
		t.Fatalf("invalid=%+v", got)
	}
	sample := trace.Finish(rpctrace.ResultError)
	assertCommitTraceNames(t, sample, "commit_admission")
	trace, ctx = newCommitTrace(t)
	for i := 0; i < 31; i++ {
		span := trace.Begin(rpctrace.StageHandler)
		span.End(rpctrace.ResultOK)
	}
	if got := c.SubmitWithOutcome(ctx, commit.Request{Build: func(*engine.Batch) error { return nil }}); got.Err != nil || got.Outcome != commit.OutcomeCommitted {
		t.Fatalf("overflow=%+v", got)
	}
	sample = trace.Finish(rpctrace.ResultOK)
	if len(sample.Spans) != 32 || sample.DroppedSpans != 5 {
		t.Fatalf("span overflow bound=%d dropped=%d", len(sample.Spans), sample.DroppedSpans)
	}
}

func newCommitTrace(t *testing.T) (*rpctrace.Trace, context.Context) {
	t.Helper()
	recorder, err := rpctrace.NewRecorder(1)
	if err != nil {
		t.Fatal(err)
	}
	trace := recorder.Start(context.Background(), rpctrace.SideServer, 91)
	return trace, rpctrace.With(context.Background(), trace)
}

type commitTraceLatch struct {
	entered, allow chan struct{}
	once           sync.Once
}

func newCommitTraceLatch() *commitTraceLatch {
	return &commitTraceLatch{entered: make(chan struct{}), allow: make(chan struct{})}
}
func (l *commitTraceLatch) block()   { close(l.entered); <-l.allow }
func (l *commitTraceLatch) release() { l.once.Do(func() { close(l.allow) }) }

func assertCommitTraceNames(t *testing.T, sample *rpctrace.Sample, want ...string) {
	t.Helper()
	if sample == nil {
		t.Fatal("missing trace sample")
	}
	got := make([]string, len(sample.Spans))
	for i, span := range sample.Spans {
		got[i] = string(span.Stage)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stages=%v want=%v", got, want)
	}
}
func commitTraceSpan(t *testing.T, sample *rpctrace.Sample, stage string, index int) rpctrace.SpanSample {
	t.Helper()
	for _, span := range sample.Spans {
		if string(span.Stage) == stage {
			if index == 0 {
				return span
			}
			index--
		}
	}
	t.Fatalf("missing %s span", stage)
	return rpctrace.SpanSample{}
}
func assertCommitTraceWithinSample(t *testing.T, sample *rpctrace.Sample) {
	t.Helper()
	for _, span := range sample.Spans {
		if span.Offset < 0 || span.Duration < 0 || span.Offset+span.Duration > sample.Duration {
			t.Fatalf("span outside sample=%+v", span)
		}
	}
}
