//go:build integration

package message

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
)

// These literals keep the pre-implementation RED about missing evidence,
// rather than a missing exported stage constant.
const (
	appendLocksStage   = rpctrace.Stage("store_append_locks")
	appendPrepareStage = rpctrace.Stage("store_append_prepare")
)

func TestStoreAppendBatchTypedOwnersObserveCanceledCanonicalLockWait(t *testing.T) {
	for _, busyLock := range []string{"append", "checkpoint"} {
		t.Run(busyLock, func(t *testing.T) {
			eng := openCompatEngine(t)
			first, second, items := leaderBatchLockItems(t, eng)
			defer first.Close()
			defer second.Close()
			synctest.Test(t, func(t *testing.T) {
				owners := appendWaitObservationOwners(t, items)
				release := holdAppendObservationLock(second, busyLock)
				defer release()
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				completed := make(chan []AppendBatchResult, 1)
				go func() {
					completed <- StoreAppendBatch(ctx, items)
				}()
				// The append goroutine must be durably blocked at the retry timer
				// before cancellation; announcing goroutine start is insufficient.
				synctest.Wait()
				earlierFree := leaderBatchEarlierLocksAvailable(first.log.channelEntry)
				cancel()
				var results []AppendBatchResult
				returnedWhileBusy := false
				select {
				case results = <-completed:
					returnedWhileBusy = true
				case <-time.After(2 * time.Second):
				}
				// Release and join before asserting even when the cancellation fails.
				release()
				if !returnedWhileBusy {
					results = joinAppendObservation(t, completed)
				}
				if !returnedWhileBusy || !earlierFree {
					t.Errorf("pre-submission cancellation: returned while busy=%v, earlier locks free=%v", returnedWhileBusy, earlierFree)
				}
				if len(results) != len(items) {
					t.Fatalf("result count=%d, want %d", len(results), len(items))
				}
				for index, result := range results {
					if !errors.Is(result.Err, context.Canceled) || result.Outcome != quorumlog.AppendOutcomeDefinitelyNotWritten || result.BaseOffset != 0 || result.LastOffset != 0 {
						t.Errorf("canceled result[%d]=%+v", index, result)
					}
				}
				for _, store := range []*ChannelStore{first, second} {
					if !leaderBatchEarlierLocksAvailable(store.log.channelEntry) {
						t.Error("canceled owner retained a canonical lock")
					}
					if leo, err := store.LEOWithError(); err != nil || leo != 0 {
						t.Errorf("canceled LEO=%d, %v", leo, err)
					}
					if rows, err := store.Read(0, 1<<20); err != nil || len(rows) != 0 {
						t.Errorf("canceled rows=%d, %v", len(rows), err)
					}
				}
				if pins := eng.db.registry.snapshot().backgroundPins; pins != 0 {
					t.Errorf("canceled background pins=%d", pins)
				}
				for index, owner := range owners {
					sample := owner.Finish(rpctrace.ResultCanceled)
					assertAppendObservationStages(t, sample, rpctrace.ResultCanceled, false)
					assertNoAppendObservationCommit(t, sample)
					t.Logf("owner[%d]: canceled lock wait observed without preparation or submission", index)
				}
			})
		})
	}
}

func TestStoreAppendBatchTypedOwnersSeparateReleasedLocksAndPreparation(t *testing.T) {
	for _, busyLock := range []string{"append", "checkpoint"} {
		t.Run(busyLock, func(t *testing.T) {
			eng := openCompatEngine(t)
			first, second, items := leaderBatchLockItems(t, eng)
			defer first.Close()
			defer second.Close()
			owners := appendWaitObservationOwners(t, items)
			release := holdAppendObservationLock(second, busyLock)
			defer release()
			completed := make(chan []AppendBatchResult, 1)
			// No caller Trace or cancellation is attached to storage execution.
			go func() { completed <- StoreAppendBatch(context.Background(), items) }()
			// This case checks completed ordering/durability, not wait duration.
			// The canceled case above deterministically covers the blocked path.
			release()
			results := joinAppendObservation(t, completed)
			if len(results) != len(items) {
				t.Fatalf("result count=%d, want %d", len(results), len(items))
			}
			for index, result := range results {
				want := quorumlog.AppendOutcomeDurable
				if index == 2 {
					want = quorumlog.AppendOutcomeAlreadyDurable
				}
				if result.Err != nil || result.Outcome != want || result.BaseOffset != 0 || result.LastOffset != 1 {
					t.Errorf("released result[%d]=%+v, want %v at [0,1]", index, result, want)
				}
			}
			for index, store := range []*ChannelStore{second, first} {
				rows, err := store.Read(0, 1<<20)
				if err != nil || len(rows) != 1 || rows[0].ID != items[index].Records[0].ID {
					t.Errorf("aligned durable rows[%d]=%+v, %v", index, rows, err)
				}
				if checkpoint, err := store.LoadCheckpoint(); err != nil || checkpoint.HW != 1 {
					t.Errorf("checkpoint[%d]=%+v, %v", index, checkpoint, err)
				}
				proposal, found, err := store.LoadDurableProposal(context.Background(), items[index].Proposal.CommandID, 16, 1<<20)
				if err != nil || !found || proposal.Manifest != items[index].Proposal {
					t.Errorf("proposal[%d]=%+v, %v, %v", index, proposal, found, err)
				}
			}
			var cohort rpctrace.CommitCohortRef
			for index, owner := range owners {
				sample := owner.Finish(rpctrace.ResultOK)
				prepare := assertAppendObservationStages(t, sample, rpctrace.ResultOK, true)
				if sample.CommitCohorts == nil || len(sample.CommitCohorts.Links) != 1 || sample.CommitCohorts.Dropped != 0 || sample.CommitCohorts.OwnersDropped != 0 {
					t.Fatalf("owner[%d] physical ownership=%+v, want one complete link", index, sample.CommitCohorts)
				}
				link := sample.CommitCohorts.Links[0]
				if !link.PhysicalWindow.Observed || !link.PhysicalWindow.Complete || prepare.Offset+prepare.Duration > link.PhysicalWindow.Offset {
					t.Errorf("preparation includes physical submission: prepare=%+v, physical=%+v", prepare, link.PhysicalWindow)
				}
				if index == 0 {
					cohort = link.CohortRef
				} else if link.CohortRef != cohort {
					t.Errorf("owner[%d] cohort=%+v, want shared %+v", index, link.CohortRef, cohort)
				}
			}
			// Independent exact retries retain preparation, but no physical commit.
			retryOwners := appendWaitObservationOwners(t, items)
			for index, result := range StoreAppendBatch(context.Background(), items) {
				if result.Err != nil || result.Outcome != quorumlog.AppendOutcomeAlreadyDurable || result.BaseOffset != 0 || result.LastOffset != 1 {
					t.Errorf("exact retry[%d]=%+v", index, result)
				}
			}
			for _, owner := range retryOwners {
				sample := owner.Finish(rpctrace.ResultOK)
				assertAppendObservationStages(t, sample, rpctrace.ResultOK, true)
				assertNoAppendObservationCommit(t, sample)
			}
		})
	}
}

// The common owner appears in every item, including the staged exact replay.
// It must receive one pair of group stages, not one pair for every item.
func appendWaitObservationOwners(t *testing.T, items []AppendBatchItem) []*rpctrace.Trace {
	t.Helper()
	recorder, err := rpctrace.NewRecorder(len(items))
	if err != nil {
		t.Fatal(err)
	}
	owners := make([]*rpctrace.Trace, len(items))
	for index := range owners {
		owners[index] = recorder.Start(context.Background(), rpctrace.SideServer, 13)
		if owners[index] == nil {
			t.Fatal("fixture owner was not admitted")
		}
	}
	for index := range items {
		items[index].CommitObservation = rpctrace.NewCommitObservation(owners[0], owners[index])
	}
	return owners
}

func holdAppendObservationLock(store *ChannelStore, name string) func() {
	busy := &store.log.appendMu
	if name == "checkpoint" {
		busy = &store.log.checkpointMu
	}
	busy.Lock()
	var once sync.Once
	return func() { once.Do(busy.Unlock) }
}

func joinAppendObservation(t *testing.T, completed <-chan []AppendBatchResult) []AppendBatchResult {
	t.Helper()
	select {
	case results := <-completed:
		return results
	case <-time.After(2 * time.Second):
		t.Fatal("original append owner did not join after releasing the fixture lock")
		return nil
	}
}

func assertAppendObservationStages(t *testing.T, sample *rpctrace.Sample, result rpctrace.Result, prepared bool) rpctrace.SpanSample {
	t.Helper()
	if sample == nil || sample.DroppedSpans != 0 {
		t.Fatalf("missing or dropped owner sample: %+v", sample)
	}
	var locks, prepare []rpctrace.SpanSample
	for _, span := range sample.Spans {
		if span.Stage == appendLocksStage {
			locks = append(locks, span)
		}
		if span.Stage == appendPrepareStage {
			prepare = append(prepare, span)
		}
	}
	wantPrepare := 0
	if prepared {
		wantPrepare = 1
	}
	if len(locks) != 1 || len(prepare) != wantPrepare {
		t.Fatalf("group stages=%+v, want one lock span and %d preparation span", sample.Spans, wantPrepare)
	}
	for _, span := range append(locks, prepare...) {
		if span.Incomplete || span.Result != result || span.Offset < 0 || span.Duration < 0 || span.Offset+span.Duration > sample.Duration {
			t.Errorf("owner stage is not complete within its sample: %+v, sample duration=%v", span, sample.Duration)
		}
	}
	if prepared {
		if locks[0].Offset+locks[0].Duration > prepare[0].Offset {
			t.Errorf("canonical lock wait overlaps preparation: lock=%+v, prepare=%+v", locks[0], prepare[0])
		}
		return prepare[0]
	}
	return rpctrace.SpanSample{}
}

func assertNoAppendObservationCommit(t *testing.T, sample *rpctrace.Sample) {
	t.Helper()
	if sample.CommitCohorts != nil {
		t.Errorf("work without physical submission inherited a cohort: %+v", sample.CommitCohorts)
	}
	for _, span := range sample.Spans {
		if strings.HasPrefix(string(span.Stage), "commit_") {
			t.Errorf("work without physical submission invented commit phase: %+v", span)
		}
	}
}
