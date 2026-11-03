//go:build integration

package commit_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/commit"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
)

// Failure cases: a background owner context can lose the typed owners' waits;
// a shared request can observe only one owner; cancellation can invent a cohort
// or cancel admitted work; late completion can mutate an already finished trace;
// and a context trace repeated in the explicit owner set can duplicate stages.
// These tests use the existing Submit/Trace seam and controlled owner latches.
func TestCoordinatorTypedOwnerWaitsWithoutTraceContext(t *testing.T) {
	db := openTestDB(t)
	synctest.Test(t, func(t *testing.T) {
		c := commit.NewCoordinator(db, commit.Config{QueueSize: 1, MaxRecords: 2, FlushWindow: 7 * time.Millisecond})
		first := newCommitTraceLatch()
		defer func() { first.release(); c.Close() }()
		c.SetCommitFunc(func(*engine.Batch) error { return nil })
		firstResult := make(chan commit.SubmitResult, 1)
		go func() {
			firstResult <- c.SubmitWithOutcome(context.Background(), commit.Request{
				Records: 2,
				Build:   func(*engine.Batch) error { first.block(); return nil },
			})
		}()
		<-first.entered
		a, _ := newCommitTrace(t)
		b, _ := newCommitTrace(t)
		result := make(chan commit.SubmitResult, 1)
		var builds, finalizes atomic.Int64
		go func() {
			result <- c.SubmitWithOutcome(context.Background(), commit.Request{
				Records:           1,
				CommitObservation: rpctrace.NewCommitObservation(a, b),
				Build:             func(*engine.Batch) error { builds.Add(1); return nil },
				Finalize:          func() { finalizes.Add(1) },
			})
		}()
		synctest.Wait()
		time.Sleep(5 * time.Millisecond)
		first.release()
		if got := <-firstResult; got.Err != nil || got.Outcome != commit.OutcomeCommitted {
			t.Fatalf("preceding request=%+v", got)
		}
		if got := <-result; got.Err != nil || got.Outcome != commit.OutcomeCommitted || builds.Load() != 1 || finalizes.Load() != 1 {
			t.Fatalf("typed request=%+v builds=%d finalizes=%d", got, builds.Load(), finalizes.Load())
		}
		as, bs := a.Finish(rpctrace.ResultOK), b.Finish(rpctrace.ResultOK)
		al, bl := oneCohortLink(t, as), oneCohortLink(t, bs)
		if al.CohortRef != bl.CohortRef || al.RequestOrdinal != bl.RequestOrdinal {
			t.Fatalf("two owners lost original request membership: a=%+v b=%+v", al, bl)
		}
		for _, sample := range []*rpctrace.Sample{as, bs} {
			assertTypedOwnerWait(t, sample, "commit_admission", 0, rpctrace.ResultOK, false)
			assertTypedOwnerWait(t, sample, "commit_queue", 5*time.Millisecond, rpctrace.ResultOK, false)
			assertTypedOwnerWait(t, sample, "commit_collect", 7*time.Millisecond, rpctrace.ResultOK, false)
			assertCommitTraceWithinSample(t, sample)
		}
	})
}

func TestCoordinatorTypedOwnerAdmissionCancellationHasNoCohort(t *testing.T) {
	db := openTestDB(t)
	synctest.Test(t, func(t *testing.T) {
		c := commit.NewCoordinator(db, commit.Config{QueueSize: 1, MaxRequests: 1})
		first := newCommitTraceLatch()
		defer func() { first.release(); c.Close() }()
		c.SetCommitFunc(func(*engine.Batch) error { return nil })
		firstResult, queuedResult := make(chan commit.SubmitResult, 1), make(chan commit.SubmitResult, 1)
		go func() {
			firstResult <- c.SubmitWithOutcome(context.Background(), commit.Request{Build: func(*engine.Batch) error { first.block(); return nil }})
		}()
		<-first.entered
		go func() {
			queuedResult <- c.SubmitWithOutcome(context.Background(), commit.Request{Build: func(*engine.Batch) error { return nil }})
		}()
		synctest.Wait()
		trace, _ := newCommitTrace(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		result := make(chan commit.SubmitResult, 1)
		var builds, finalizes atomic.Int64
		go func() {
			result <- c.SubmitWithOutcome(ctx, commit.Request{
				CommitObservation: rpctrace.NewCommitObservation(trace),
				Build:             func(*engine.Batch) error { builds.Add(1); return nil },
				Finalize:          func() { finalizes.Add(1) },
			})
		}()
		synctest.Wait()
		time.Sleep(2 * time.Millisecond)
		cancel()
		got := <-result
		if !errors.Is(got.Err, context.Canceled) || got.Outcome != commit.OutcomeDefinitelyNotCommitted || builds.Load() != 0 || finalizes.Load() != 1 {
			t.Fatalf("rejected admission=%+v builds=%d finalizes=%d", got, builds.Load(), finalizes.Load())
		}
		sample := trace.Finish(rpctrace.ResultCanceled)
		first.release()
		for _, got := range []commit.SubmitResult{<-firstResult, <-queuedResult} {
			if got.Err != nil || got.Outcome != commit.OutcomeCommitted {
				t.Fatalf("accepted fixture request=%+v", got)
			}
		}
		if coverage := readCohortCoverage(t, sample); coverage != nil && len(coverage.Links) != 0 {
			t.Fatalf("canceled admission invented physical work: %+v", coverage)
		}
		assertCommitTraceNames(t, sample, "commit_admission")
		assertTypedOwnerWait(t, sample, "commit_admission", 2*time.Millisecond, rpctrace.ResultCanceled, false)
	})
}

func TestCoordinatorTypedOwnerAcceptedCancellationKeepsLateCompletionImmutable(t *testing.T) {
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
		early, _ := newCommitTrace(t)
		remaining, _ := newCommitTrace(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		result := make(chan commit.SubmitResult, 1)
		var builds, finalizes atomic.Int64
		go func() {
			result <- c.SubmitWithOutcome(ctx, commit.Request{
				CommitObservation: rpctrace.NewCommitObservation(early, remaining),
				Build:             func(*engine.Batch) error { builds.Add(1); return nil },
				Finalize:          func() { finalizes.Add(1) },
			})
		}()
		synctest.Wait()
		time.Sleep(3 * time.Millisecond)
		cancel()
		got := <-result
		if !errors.Is(got.Err, context.Canceled) || got.Outcome != commit.OutcomeUnknown || builds.Load() != 0 || finalizes.Load() != 0 {
			t.Fatalf("accepted caller return=%+v builds=%d finalizes=%d", got, builds.Load(), finalizes.Load())
		}
		earlySample := early.Finish(rpctrace.ResultCanceled)
		// Compare the published representation itself. Sample.Clone normalizes
		// an empty Spans slice to nil, which is not a late-completion mutation.
		before, err := json.Marshal(earlySample)
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(4 * time.Millisecond)
		first.release()
		if got := <-firstResult; got.Err != nil {
			t.Fatalf("preceding request=%+v", got)
		}
		synctest.Wait()
		if builds.Load() != 1 || finalizes.Load() != 1 {
			t.Fatalf("cancellation changed admitted work: builds=%d finalizes=%d", builds.Load(), finalizes.Load())
		}
		after, err := json.Marshal(earlySample)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before, after) {
			t.Fatal("late owner completion mutated finished trace")
		}
		if coverage := readCohortCoverage(t, earlySample); coverage != nil && len(coverage.Links) != 0 {
			t.Fatalf("early queue snapshot invented a later cohort: %+v", coverage)
		}
		remainingSample := remaining.Finish(rpctrace.ResultOK)
		link := oneCohortLink(t, remainingSample)
		if !link.PhysicalWindow.Complete || !link.SubmitWindow.Complete || link.SubmitWindow.Duration != 3*time.Millisecond || link.Outcome != "committed" {
			t.Fatalf("remaining owner lost physical completion or original caller window: %+v", link)
		}
		assertCommitTraceNames(t, earlySample, "commit_admission", "commit_queue")
		assertTypedOwnerWait(t, earlySample, "commit_admission", 0, rpctrace.ResultOK, false)
		assertTypedOwnerWait(t, earlySample, "commit_queue", 3*time.Millisecond, rpctrace.ResultUnfinished, true)
		assertTypedOwnerWait(t, remainingSample, "commit_admission", 0, rpctrace.ResultOK, false)
		assertTypedOwnerWait(t, remainingSample, "commit_queue", 7*time.Millisecond, rpctrace.ResultOK, false)
		assertTypedOwnerWait(t, remainingSample, "commit_collect", 0, rpctrace.ResultOK, false)
	})
}

func TestCoordinatorTypedOwnerContextTraceIsNotDuplicated(t *testing.T) {
	db := openTestDB(t)
	synctest.Test(t, func(t *testing.T) {
		c := commit.NewCoordinator(db, commit.Config{QueueSize: 1, MaxRequests: 1})
		defer c.Close()
		c.SetCommitFunc(func(*engine.Batch) error { return nil })
		trace, ctx := newCommitTrace(t)
		got := c.SubmitWithOutcome(ctx, commit.Request{
			CommitObservation: rpctrace.NewCommitObservation(trace),
			Build:             func(*engine.Batch) error { return nil },
		})
		if got.Err != nil || got.Outcome != commit.OutcomeCommitted {
			t.Fatalf("context plus typed owner result=%+v", got)
		}
		sample := trace.Finish(rpctrace.ResultOK)
		assertCommitTraceNames(t, sample, "commit_admission", "commit_queue", "commit_collect", "commit_build", "commit_durable", "commit_publish")
		oneCohortLink(t, sample)
		if sample.DroppedSpans != 0 {
			t.Fatalf("context plus typed owner exhausted spans: %d", sample.DroppedSpans)
		}
	})
}

func assertTypedOwnerWait(t *testing.T, sample *rpctrace.Sample, stage string, duration time.Duration, result rpctrace.Result, incomplete bool) {
	t.Helper()
	count := 0
	for _, span := range sample.Spans {
		if string(span.Stage) == stage {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("typed owner %s count=%d, want exactly one", stage, count)
	}
	span := commitTraceSpan(t, sample, stage, 0)
	if span.Duration != duration || span.Result != result || span.Incomplete != incomplete {
		t.Fatalf("typed owner %s=%+v, want duration=%s result=%s incomplete=%t", stage, span, duration, result, incomplete)
	}
}
