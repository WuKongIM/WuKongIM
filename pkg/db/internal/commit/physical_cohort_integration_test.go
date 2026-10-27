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

type cohortProbeRef struct {
	Instance string `json:"instance"`
	Attempt  uint64 `json:"attempt"`
}

type cohortProbeInterval struct {
	Observed bool          `json:"observed"`
	Complete bool          `json:"complete"`
	Offset   time.Duration `json:"offset"`
	Duration time.Duration `json:"duration"`
}

type cohortProbeNative struct {
	Available                   bool          `json:"available"`
	TotalDuration               time.Duration `json:"total_duration"`
	SemaphoreWaitDuration       time.Duration `json:"semaphore_wait_duration"`
	WALQueueWaitDuration        time.Duration `json:"wal_queue_wait_duration"`
	MemTableWriteStallDuration  time.Duration `json:"mem_table_write_stall_duration"`
	L0ReadAmpWriteStallDuration time.Duration `json:"l0_read_amp_write_stall_duration"`
	WALRotationDuration         time.Duration `json:"wal_rotation_duration"`
	CommitWaitDuration          time.Duration `json:"commit_wait_duration"`
}

type cohortProbeLink struct {
	CohortRef      cohortProbeRef      `json:"cohort_ref"`
	RequestOrdinal uint32              `json:"request_ordinal"`
	RebuildOf      *cohortProbeRef     `json:"rebuild_of,omitempty"`
	PhysicalWindow cohortProbeInterval `json:"physical_window"`
	SubmitWindow   cohortProbeInterval `json:"submit_window"`
	TimedSum       time.Duration       `json:"timed_sum"`
	Build          cohortProbeInterval `json:"build"`
	Durable        cohortProbeInterval `json:"durable"`
	Publish        cohortProbeInterval `json:"publish"`
	TerminalWait   cohortProbeInterval `json:"terminal_wait"`
	Finalize       cohortProbeInterval `json:"finalize"`
	Native         cohortProbeNative   `json:"native"`
	Outcome        string              `json:"outcome"`
}

type cohortProbeCoverage struct {
	Links   []cohortProbeLink `json:"links"`
	Dropped uint64            `json:"dropped"`
}

func readCohortCoverage(t *testing.T, sample *rpctrace.Sample) *cohortProbeCoverage {
	t.Helper()
	if sample == nil {
		t.Fatal("missing RPC sample")
	}
	b, err := json.Marshal(sample)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Cohorts *cohortProbeCoverage `json:"commit_cohorts"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Cohorts
}

func oneCohortLink(t *testing.T, sample *rpctrace.Sample) cohortProbeLink {
	t.Helper()
	coverage := readCohortCoverage(t, sample)
	if coverage == nil || len(coverage.Links) != 1 || coverage.Dropped != 0 {
		t.Fatalf("coverage=%+v, want one complete identity slot", coverage)
	}
	link := coverage.Links[0]
	if link.CohortRef.Instance == "" || link.CohortRef.Attempt == 0 {
		t.Fatalf("invalid physical reference=%+v", link.CohortRef)
	}
	return link
}

func TestPhysicalCohortGroupedRequestsShareOnlyActualBuildIdentity(t *testing.T) {
	db := openTestDB(t)
	synctest.Test(t, func(t *testing.T) {
		c := commit.NewCoordinator(db, commit.Config{QueueSize: 2, FlushWindow: time.Hour, MaxRequests: 2})
		defer c.Close()
		var commits, builds, publishes, finalizes atomic.Int64
		c.SetCommitFunc(func(*engine.Batch) error { commits.Add(1); return nil })
		a, ac := newCommitTrace(t)
		b, bc := newCommitTrace(t)
		results := make(chan commit.SubmitResult, 2)
		req := func() commit.Request {
			return commit.Request{Build: func(*engine.Batch) error { builds.Add(1); return nil }, Publish: func() error { publishes.Add(1); return nil }, Finalize: func() { finalizes.Add(1) }}
		}
		go func() { results <- c.SubmitWithOutcome(ac, req()) }()
		synctest.Wait()
		go func() { results <- c.SubmitWithOutcome(bc, req()) }()
		for range 2 {
			if got := <-results; got.Err != nil || got.Outcome != commit.OutcomeCommitted {
				t.Fatalf("result=%+v", got)
			}
		}
		if commits.Load() != 1 || builds.Load() != 2 || publishes.Load() != 2 || finalizes.Load() != 2 {
			t.Fatalf("commit/build/publish/finalize=%d/%d/%d/%d", commits.Load(), builds.Load(), publishes.Load(), finalizes.Load())
		}
		la, lb := oneCohortLink(t, a.Finish(rpctrace.ResultOK)), oneCohortLink(t, b.Finish(rpctrace.ResultOK))
		if la.CohortRef != lb.CohortRef || la.RequestOrdinal != 0 || lb.RequestOrdinal != 1 {
			t.Fatalf("shared physical membership a=%+v b=%+v", la, lb)
		}
		if !la.PhysicalWindow.Complete || !lb.Finalize.Complete || la.Native.Available || lb.Native.Available {
			t.Fatalf("fake commit must remain explicitly non-native: a=%+v b=%+v", la, lb)
		}
	})
}

type cohortGateObserver struct{ entered, release chan struct{} }

func (*cohortGateObserver) SetQueueDepth(int)                  {}
func (*cohortGateObserver) ObserveRequest(commit.RequestEvent) {}
func (o *cohortGateObserver) ObserveBatch(commit.BatchEvent)   { close(o.entered); <-o.release }

func TestPhysicalCohortContinuousWindowExcludesObserverAndFinalize(t *testing.T) {
	db := openTestDB(t)
	synctest.Test(t, func(t *testing.T) {
		observer := &cohortGateObserver{entered: make(chan struct{}), release: make(chan struct{})}
		c := commit.NewCoordinator(db, commit.Config{QueueSize: 1, MaxRequests: 1, Observer: observer})
		defer c.Close()
		c.SetCommitFunc(func(*engine.Batch) error { time.Sleep(7 * time.Millisecond); return nil })
		trace, ctx := newCommitTrace(t)
		done := make(chan commit.SubmitResult, 1)
		go func() {
			done <- c.SubmitWithOutcome(ctx, commit.Request{Build: func(*engine.Batch) error { time.Sleep(5 * time.Millisecond); return nil }, Publish: func() error { time.Sleep(11 * time.Millisecond); return nil }, Finalize: func() { time.Sleep(17 * time.Millisecond) }})
		}()
		<-observer.entered
		time.Sleep(13 * time.Millisecond)
		close(observer.release)
		if got := <-done; got.Err != nil {
			t.Fatal(got.Err)
		}
		link := oneCohortLink(t, trace.Finish(rpctrace.ResultOK))
		if !link.PhysicalWindow.Observed || !link.PhysicalWindow.Complete || link.PhysicalWindow.Duration != 23*time.Millisecond || link.TimedSum != 23*time.Millisecond {
			t.Fatalf("physical window/sum=%+v, want separately measured 23ms windows", link)
		}
		if !link.TerminalWait.Complete || link.TerminalWait.Duration != 13*time.Millisecond || !link.Finalize.Complete || link.Finalize.Duration != 17*time.Millisecond {
			t.Fatalf("observer wait/finalize=%+v, want separate 13ms/17ms", link)
		}
	})
}

func TestPhysicalCohortRebuildRetainsDiscardedBuildWithoutInventingSync(t *testing.T) {
	db := openTestDB(t)
	synctest.Test(t, func(t *testing.T) {
		c := commit.NewCoordinator(db, commit.Config{QueueSize: 2, FlushWindow: time.Hour, MaxRequests: 2})
		defer c.Close()
		var commits atomic.Int64
		c.SetCommitFunc(func(*engine.Batch) error { commits.Add(1); return nil })
		goodTrace, goodCtx := newCommitTrace(t)
		badTrace, badCtx := newCommitTrace(t)
		good, bad := make(chan commit.SubmitResult, 1), make(chan commit.SubmitResult, 1)
		cause := errors.New("controlled group build rejection")
		go func() {
			good <- c.SubmitWithOutcome(goodCtx, commit.Request{RebuildOnGroupAbort: true, Build: func(*engine.Batch) error { return nil }})
		}()
		synctest.Wait()
		go func() {
			bad <- c.SubmitWithOutcome(badCtx, commit.Request{Build: func(*engine.Batch) error { return cause }})
		}()
		if got := <-good; got.Err != nil || got.Outcome != commit.OutcomeCommitted {
			t.Fatalf("good=%+v", got)
		}
		if got := <-bad; !errors.Is(got.Err, cause) || got.Outcome != commit.OutcomeDefinitelyNotCommitted {
			t.Fatalf("bad=%+v", got)
		}
		coverage := readCohortCoverage(t, goodTrace.Finish(rpctrace.ResultOK))
		badLink := oneCohortLink(t, badTrace.Finish(rpctrace.ResultError))
		if coverage == nil || len(coverage.Links) != 2 || coverage.Dropped != 0 || commits.Load() != 1 {
			t.Fatalf("coverage=%+v commit calls=%d", coverage, commits.Load())
		}
		first, second := coverage.Links[0], coverage.Links[1]
		if first.CohortRef != badLink.CohortRef || first.CohortRef == second.CohortRef || second.RebuildOf == nil || *second.RebuildOf != first.CohortRef {
			t.Fatalf("rebuild references=%+v bad=%+v", coverage, badLink)
		}
		if first.Durable.Observed || badLink.Durable.Observed || first.Native.Available || !second.Durable.Complete {
			t.Fatalf("discarded build claimed physical sync: %+v bad=%+v", coverage, badLink)
		}
	})
}

func TestPhysicalCohortCanceledCallerCannotReopenFinishedTrace(t *testing.T) {
	db := openTestDB(t)
	synctest.Test(t, func(t *testing.T) {
		c := commit.NewCoordinator(db, commit.Config{QueueSize: 1, MaxRequests: 1})
		gate := newCommitTraceLatch()
		defer func() { gate.release(); c.Close() }()
		c.SetCommitFunc(func(*engine.Batch) error { return nil })
		trace, base := newCommitTrace(t)
		ctx, cancel := context.WithCancel(base)
		defer cancel()
		var finalized atomic.Int64
		done := make(chan commit.SubmitResult, 1)
		go func() {
			done <- c.SubmitWithOutcome(ctx, commit.Request{Build: func(*engine.Batch) error { gate.block(); return nil }, Finalize: func() { finalized.Add(1) }})
		}()
		<-gate.entered
		cancel()
		if got := <-done; got.Outcome != commit.OutcomeUnknown || !errors.Is(got.Err, context.Canceled) {
			t.Fatalf("canceled=%+v", got)
		}
		sample := trace.Finish(rpctrace.ResultCanceled)
		link := oneCohortLink(t, sample)
		if !link.PhysicalWindow.Observed || link.PhysicalWindow.Complete || link.Finalize.Observed || link.Outcome != "unknown" {
			t.Fatalf("caller-clipped physical state=%+v", link)
		}
		before, err := json.Marshal(sample)
		if err != nil {
			t.Fatal(err)
		}
		gate.release()
		c.Close()
		if finalized.Load() != 1 {
			t.Fatalf("finalize count=%d", finalized.Load())
		}
		after, err := json.Marshal(sample)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before, after) || trace.Finish(rpctrace.ResultOK) != nil {
			t.Fatal("late physical completion reopened a published observation")
		}
	})
}

func TestPhysicalCohortWholeLinkCapacityDoesNotChangeCommits(t *testing.T) {
	c := commit.NewCoordinator(openTestDB(t), commit.Config{QueueSize: 1, MaxRequests: 1})
	defer c.Close()
	var commits atomic.Int64
	c.SetCommitFunc(func(*engine.Batch) error { commits.Add(1); return nil })
	trace, ctx := newCommitTrace(t)
	for range 3 {
		if got := c.SubmitWithOutcome(ctx, commit.Request{Build: func(*engine.Batch) error { return nil }, Finalize: func() {}}); got.Err != nil || got.Outcome != commit.OutcomeCommitted {
			t.Fatalf("submit=%+v", got)
		}
	}
	coverage := readCohortCoverage(t, trace.Finish(rpctrace.ResultOK))
	if coverage == nil || len(coverage.Links) != 2 || coverage.Dropped != 1 || commits.Load() != 3 {
		t.Fatalf("bounded whole links=%+v commits=%d", coverage, commits.Load())
	}
	if coverage.Links[0].CohortRef == coverage.Links[1].CohortRef {
		t.Fatal("independent physical builds reused an identity")
	}
	for _, link := range coverage.Links {
		if !link.Finalize.Complete || !link.PhysicalWindow.Complete {
			t.Fatalf("partial retained link after terminal completion: %+v", link)
		}
	}
}

func TestPhysicalCohortNativeFactSurvivesUnknownAndPublishErrors(t *testing.T) {
	cause := errors.New("controlled physical terminal error")
	for _, tc := range []struct {
		name                                                 string
		native, empty, buildError, commitError, publishError bool
		outcome                                              commit.Outcome
	}{
		{name: "native", native: true, outcome: commit.OutcomeCommitted},
		{name: "native then ambiguous wrapper", native: true, commitError: true, outcome: commit.OutcomeUnknown},
		{name: "native then publish error", native: true, publishError: true, outcome: commit.OutcomeCommitted},
		{name: "empty native", native: true, empty: true, outcome: commit.OutcomeCommitted},
		{name: "fake commit", outcome: commit.OutcomeCommitted},
		{name: "build rejected", native: true, buildError: true, outcome: commit.OutcomeDefinitelyNotCommitted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := commit.NewCoordinator(openTestDB(t), commit.Config{QueueSize: 1, MaxRequests: 1})
			defer c.Close()
			calls := 0
			var captured engine.NativeCommitStats
			c.SetCommitFunc(func(batch *engine.Batch) error {
				calls++
				if tc.native {
					if err := batch.Commit(true); err != nil {
						return err
					}
					captured = batch.CommitStats()
				}
				if tc.commitError {
					return cause
				}
				return nil
			})
			trace, ctx := newCommitTrace(t)
			got := c.SubmitWithOutcome(ctx, commit.Request{Build: func(batch *engine.Batch) error {
				if tc.buildError {
					return cause
				}
				if tc.empty {
					return nil
				}
				return batch.Set([]byte("cohort-key"), []byte("value"))
			}, Publish: func() error {
				if tc.publishError {
					return cause
				}
				return nil
			}, Finalize: func() {}})
			if got.Outcome != tc.outcome || (got.Err != nil) != (tc.buildError || tc.commitError || tc.publishError) {
				t.Fatalf("result=%+v", got)
			}
			link := oneCohortLink(t, trace.Finish(rpctrace.ResultFor(got.Err)))
			if link.Native.Available != (tc.native && !tc.empty && !tc.buildError) {
				t.Fatalf("native availability=%+v", link)
			}
			wantNative := cohortProbeNative{Available: captured.Available, TotalDuration: captured.TotalDuration, SemaphoreWaitDuration: captured.SemaphoreWaitDuration, WALQueueWaitDuration: captured.WALQueueWaitDuration, MemTableWriteStallDuration: captured.MemTableWriteStallDuration, L0ReadAmpWriteStallDuration: captured.L0ReadAmpWriteStallDuration, WALRotationDuration: captured.WALRotationDuration, CommitWaitDuration: captured.CommitWaitDuration}
			if link.Native != wantNative {
				t.Fatalf("sidecar native facts=%+v, actual completed batch=%+v", link.Native, captured)
			}
			wantCalls := 1
			if tc.buildError {
				wantCalls = 0
			}
			if calls != wantCalls {
				t.Fatalf("physical invocation calls=%d,want%d", calls, wantCalls)
			}
			if !link.Finalize.Complete {
				t.Fatalf("terminal cleanup not observed: %+v", link)
			}
		})
	}
}

func TestPhysicalCohortDisabledAndRejectedWorkHasNoReference(t *testing.T) {
	observer := &recordingObserver{}
	c := commit.NewCoordinator(openTestDB(t), commit.Config{QueueSize: 1, MaxRequests: 1, Observer: observer})
	c.SetCommitFunc(func(*engine.Batch) error { return nil })
	if got := c.SubmitWithOutcome(context.Background(), commit.Request{Build: func(*engine.Batch) error { return nil }}); got.Err != nil {
		t.Fatal(got.Err)
	}
	event := observer.onlyBatch(t)
	b, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	var batch struct {
		Ref *cohortProbeRef `json:"cohort_ref"`
	}
	if err := json.Unmarshal(b, &batch); err != nil {
		t.Fatal(err)
	}
	if batch.Ref != nil {
		t.Fatal("disabled observation generated a physical identity")
	}
	c.Close()
	trace, ctx := newCommitTrace(t)
	if got := c.SubmitWithOutcome(ctx, commit.Request{Build: func(*engine.Batch) error { t.Error("closed build executed"); return nil }}); got.Outcome != commit.OutcomeDefinitelyNotCommitted || !errors.Is(got.Err, commit.ErrClosed) {
		t.Fatalf("closed=%+v", got)
	}
	if coverage := readCohortCoverage(t, trace.Finish(rpctrace.ResultStopped)); coverage != nil {
		t.Fatalf("rejection invented physical membership: %+v", coverage)
	}
}

type cohortRequestObserver struct {
	entered, release chan struct{}
	duration         time.Duration
}

func (*cohortRequestObserver) SetQueueDepth(int)              {}
func (*cohortRequestObserver) ObserveBatch(commit.BatchEvent) {}
func (o *cohortRequestObserver) ObserveRequest(event commit.RequestEvent) {
	o.duration = event.Duration
	close(o.entered)
	<-o.release
}

func TestPhysicalCohortSubmitWindowIncludesRequestObserverOutsideTimedSum(t *testing.T) {
	db := openTestDB(t)
	synctest.Test(t, func(t *testing.T) {
		observer := &cohortRequestObserver{entered: make(chan struct{}), release: make(chan struct{})}
		c := commit.NewCoordinator(db, commit.Config{QueueSize: 1, MaxRequests: 1, Observer: observer})
		defer c.Close()
		c.SetCommitFunc(func(*engine.Batch) error { return nil })
		trace, ctx := newCommitTrace(t)
		done := make(chan commit.SubmitResult, 1)
		go func() {
			done <- c.SubmitWithOutcome(ctx, commit.Request{Build: func(*engine.Batch) error { return nil }, Finalize: func() {}})
		}()
		<-observer.entered
		time.Sleep(19 * time.Millisecond)
		close(observer.release)
		if got := <-done; got.Err != nil {
			t.Fatal(got.Err)
		}
		link := oneCohortLink(t, trace.Finish(rpctrace.ResultOK))
		if !link.SubmitWindow.Observed || !link.SubmitWindow.Complete || link.SubmitWindow.Duration != 19*time.Millisecond {
			t.Fatalf("outer Submit window omitted synchronous request observer: %+v", link)
		}
		if link.TimedSum != 0 || link.PhysicalWindow.Duration != 0 || observer.duration != 0 {
			t.Fatalf("timed subphases or pre-observer RequestEvent were relabeled as full wall time: link=%+v legacyRequest=%s", link, observer.duration)
		}
	})
}

func TestPhysicalCohortLaterRequestWaitsForEarlierFinalizeWithoutCallingItPublish(t *testing.T) {
	db := openTestDB(t)
	synctest.Test(t, func(t *testing.T) {
		c := commit.NewCoordinator(db, commit.Config{QueueSize: 2, MaxRequests: 2, FlushWindow: time.Hour})
		defer c.Close()
		c.SetCommitFunc(func(*engine.Batch) error { return nil })
		a, ac := newCommitTrace(t)
		b, bc := newCommitTrace(t)
		done := make(chan commit.SubmitResult, 2)
		go func() {
			done <- c.SubmitWithOutcome(ac, commit.Request{Build: func(*engine.Batch) error { return nil }, Finalize: func() { time.Sleep(17 * time.Millisecond) }})
		}()
		synctest.Wait()
		go func() {
			done <- c.SubmitWithOutcome(bc, commit.Request{Build: func(*engine.Batch) error { return nil }, Finalize: func() { time.Sleep(3 * time.Millisecond) }})
		}()
		for range 2 {
			if got := <-done; got.Err != nil {
				t.Fatal(got.Err)
			}
		}
		la, lb := oneCohortLink(t, a.Finish(rpctrace.ResultOK)), oneCohortLink(t, b.Finish(rpctrace.ResultOK))
		if la.CohortRef != lb.CohortRef || lb.RequestOrdinal != 1 || lb.TerminalWait.Duration != 17*time.Millisecond || lb.Finalize.Duration != 3*time.Millisecond {
			t.Fatalf("request-aligned terminal waits a=%+v b=%+v", la, lb)
		}
		if la.Publish.Duration != 0 || lb.Publish.Duration != 0 || la.PhysicalWindow.Duration != 0 || lb.PhysicalWindow.Duration != 0 {
			t.Fatalf("Finalize leaked into physical publication: a=%+v b=%+v", la, lb)
		}
	})
}

func TestPhysicalCohortNamespaceSurvivesRPCBindAndChangesWithRecorderEpoch(t *testing.T) {
	c := commit.NewCoordinator(openTestDB(t), commit.Config{QueueSize: 1, MaxRequests: 1})
	defer c.Close()
	c.SetCommitFunc(func(*engine.Batch) error { return nil })
	recorder, err := rpctrace.NewRecorder(1)
	if err != nil {
		t.Fatal(err)
	}
	var refs []cohortProbeRef
	for i := range 3 {
		if i == 2 {
			recorder, err = rpctrace.NewRecorder(1)
			if err != nil {
				t.Fatal(err)
			}
		}
		trace := recorder.Start(context.Background(), rpctrace.SideServer, 21)
		if trace == nil || !trace.Bind([16]byte{byte(i + 1)}, uint64(101+i)) {
			t.Fatal("binding fixture failed")
		}
		got := c.SubmitWithOutcome(rpctrace.With(context.Background(), trace), commit.Request{Build: func(*engine.Batch) error { return nil }})
		if got.Err != nil {
			t.Fatal(got.Err)
		}
		refs = append(refs, oneCohortLink(t, trace.Finish(rpctrace.ResultOK)).CohortRef)
	}
	if refs[0].Instance != refs[1].Instance || refs[0].Attempt == refs[1].Attempt {
		t.Fatalf("RPC rebind replaced stable observation namespace or reused attempt: %+v", refs)
	}
	if refs[1].Instance == refs[2].Instance {
		t.Fatalf("new recorder epoch reused prior namespace: %+v", refs)
	}
}

func TestPhysicalCohortCloseOfQueuedRequestDoesNotInventBuildIdentity(t *testing.T) {
	db := openTestDB(t)
	synctest.Test(t, func(t *testing.T) {
		c := commit.NewCoordinator(db, commit.Config{QueueSize: 2, MaxRequests: 1})
		gate := newCommitTraceLatch()
		defer func() { gate.release(); c.Close() }()
		c.SetCommitFunc(func(*engine.Batch) error { return nil })
		first := make(chan commit.SubmitResult, 1)
		go func() {
			first <- c.SubmitWithOutcome(context.Background(), commit.Request{Build: func(*engine.Batch) error { gate.block(); return nil }})
		}()
		<-gate.entered
		trace, ctx := newCommitTrace(t)
		queued := make(chan commit.SubmitResult, 1)
		go func() {
			queued <- c.SubmitWithOutcome(ctx, commit.Request{Build: func(*engine.Batch) error { t.Error("queued request built after Close"); return nil }})
		}()
		synctest.Wait()
		closed := make(chan struct{})
		go func() { c.Close(); close(closed) }()
		synctest.Wait()
		gate.release()
		if got := <-first; got.Err != nil {
			t.Fatal(got.Err)
		}
		if got := <-queued; got.Outcome != commit.OutcomeDefinitelyNotCommitted || !errors.Is(got.Err, commit.ErrClosed) {
			t.Fatalf("queued close=%+v", got)
		}
		<-closed
		if coverage := readCohortCoverage(t, trace.Finish(rpctrace.ResultStopped)); coverage != nil {
			t.Fatalf("queue shutdown invented physical membership: %+v", coverage)
		}
	})
}

func TestPhysicalCohortConcurrentFinishPublishesAnImmutableWholeSnapshot(t *testing.T) {
	c := commit.NewCoordinator(openTestDB(t), commit.Config{QueueSize: 1, MaxRequests: 1})
	defer c.Close()
	c.SetCommitFunc(func(*engine.Batch) error { return nil })
	for range 8 {
		gate := newCommitTraceLatch()
		trace, ctx := newCommitTrace(t)
		var finalized atomic.Int64
		done := make(chan commit.SubmitResult, 1)
		go func() {
			done <- c.SubmitWithOutcome(ctx, commit.Request{Build: func(*engine.Batch) error { return nil }, Publish: func() error { gate.block(); return nil }, Finalize: func() { finalized.Add(1) }})
		}()
		<-gate.entered
		finished := make(chan *rpctrace.Sample, 1)
		go func() { finished <- trace.Finish(rpctrace.ResultOK) }()
		gate.release()
		sample := <-finished
		oneCohortLink(t, sample)
		before, err := json.Marshal(sample)
		if err != nil {
			t.Fatal(err)
		}
		if got := <-done; got.Err != nil || got.Outcome != commit.OutcomeCommitted || finalized.Load() != 1 {
			t.Fatalf("terminal business result=%+v finalized=%d", got, finalized.Load())
		}
		after, err := json.Marshal(sample)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before, after) {
			t.Fatal("concurrent physical completion mutated published snapshot")
		}
	}
}
