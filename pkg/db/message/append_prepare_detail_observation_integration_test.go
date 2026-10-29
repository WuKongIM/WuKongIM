//go:build integration

package message

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
)

// The child is fixed numeric group wall evidence, never individual request CPU.
// Literal wire keys keep the original semantic RED about missing observations.
type storePrepareDetailWire struct {
	knownMask     uint64
	frontierWall  time.Duration
	frontierCalls uint64
	itemWall      time.Duration
	itemCalls     uint64
}

func TestStorePrepareDetailFreshWarmAndReopenExactReplay(t *testing.T) {
	directory := t.TempDir()
	for _, reopened := range []bool{false, true} {
		func() {
			eng, err := Open(directory)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := eng.Close(); err != nil {
					t.Error(err)
				}
			}()
			first, second, items := leaderBatchLockItems(t, eng)
			defer first.Close()
			defer second.Close()
			for round := 0; round < 2; round++ {
				owners := appendWaitObservationOwners(t, items)
				results := StoreAppendBatch(context.Background(), items)
				want := []quorumlog.AppendOutcome{quorumlog.AppendOutcomeDurable, quorumlog.AppendOutcomeDurable, quorumlog.AppendOutcomeAlreadyDurable}
				if reopened || round > 0 {
					want[0], want[1] = quorumlog.AppendOutcomeAlreadyDurable, quorumlog.AppendOutcomeAlreadyDurable
				}
				assertStorePrepareResults(t, results, want)
				var common storePrepareDetailWire
				for index, owner := range owners {
					sample := owner.Finish(rpctrace.ResultOK)
					span := assertAppendObservationStages(t, sample, rpctrace.ResultOK, true)
					detail := requireStorePrepareDetail(t, span)
					if detail.knownMask != 3 || detail.frontierCalls != 2 || detail.itemCalls != 3 {
						t.Errorf("reopened=%v round=%d detail=%+v, want known3/frontier2/item3", reopened, round, detail)
					}
					if index == 0 {
						common = detail
					} else if detail != common {
						t.Error("shared group owners received different numeric preparation evidence")
					}
					if reopened || round > 0 {
						assertNoAppendObservationCommit(t, sample)
					}
				}
				for _, store := range []*ChannelStore{first, second} {
					rows, readErr := store.Read(0, 1<<20)
					if readErr != nil || len(rows) != 1 {
						t.Errorf("durable row count=%d error=%v, want1", len(rows), readErr)
					}
					if leo, leoErr := store.LEOWithError(); leoErr != nil || leo != 1 {
						t.Errorf("durable LEO=%d error=%v, want1", leo, leoErr)
					}
					if cp, cpErr := store.LoadCheckpoint(); cpErr != nil || cp.HW != 1 {
						t.Errorf("durable HW=%d error=%v, want1", cp.HW, cpErr)
					}
				}
			}
		}()
	}
}

func TestStorePrepareDetailMixedProposalErrorKeepsOriginalOutcomes(t *testing.T) {
	eng := openCompatEngine(t)
	first, second, items := leaderBatchLockItems(t, eng)
	defer first.Close()
	defer second.Close()
	// Item1 is not the common sampled owner; its malformed proposal must not
	// inherit the valid sibling's physical cohort while sharing group timing.
	items[1].Proposal.Digest[0] ^= 1
	owners := appendWaitObservationOwners(t, items)
	results := StoreAppendBatch(context.Background(), items)
	if len(results) != 3 {
		t.Fatalf("results=%d, want3", len(results))
	}
	if results[0].Err != nil || results[0].Outcome != quorumlog.AppendOutcomeDurable || results[2].Err != nil || results[2].Outcome != quorumlog.AppendOutcomeAlreadyDurable {
		t.Fatal("valid sibling and staged replay lost original durable outcomes")
	}
	if results[1].Err == nil || results[1].Outcome != quorumlog.AppendOutcomeConflict {
		t.Fatal("malformed proposal lost original conflict")
	}
	for index, owner := range owners {
		sample := owner.Finish(rpctrace.ResultError)
		span := assertMixedStorePrepareStages(t, sample)
		detail := requireStorePrepareDetail(t, span)
		if detail.knownMask != 3 || detail.frontierCalls != 2 || detail.itemCalls != 3 {
			t.Errorf("error group detail=%+v", detail)
		}
		if index == 1 {
			assertNoAppendObservationCommit(t, sample)
		}
	}
	if leo, err := first.LEOWithError(); err != nil || leo != 0 {
		t.Errorf("rejected sibling LEO=%d error=%v, want0", leo, err)
	}
	if leo, err := second.LEOWithError(); err != nil || leo != 1 {
		t.Errorf("valid sibling LEO=%d error=%v, want1", leo, err)
	}
}

// Mixed item validation happens after successful canonical lock acquisition.
// Its preparation result must not be projected backward onto the lock span.
func assertMixedStorePrepareStages(t *testing.T, sample *rpctrace.Sample) rpctrace.SpanSample {
	t.Helper()
	if sample == nil || sample.DroppedSpans != 0 {
		t.Fatal("mixed group lost an admitted owner sample")
	}
	var locks, prepare []rpctrace.SpanSample
	for _, span := range sample.Spans {
		switch span.Stage {
		case appendLocksStage:
			locks = append(locks, span)
		case appendPrepareStage:
			prepare = append(prepare, span)
		}
	}
	if len(locks) != 1 || len(prepare) != 1 {
		t.Fatal("mixed group needs one original lock span and one preparation span")
	}
	for index, span := range []rpctrace.SpanSample{locks[0], prepare[0]} {
		want := rpctrace.ResultOK
		if index == 1 {
			want = rpctrace.ResultError
		}
		if span.Incomplete || span.Result != want || span.Offset < 0 || span.Duration < 0 || span.Offset+span.Duration > sample.Duration {
			t.Errorf("mixed group stage lost original result or timing: %+v, want %s", span, want)
		}
	}
	if locks[0].Offset+locks[0].Duration > prepare[0].Offset {
		t.Error("mixed group canonical locks overlap preparation")
	}
	return prepare[0]
}

func TestStorePrepareDetailCanceledCanonicalLockHasNoPreparation(t *testing.T) {
	for _, lock := range []string{"append", "checkpoint"} {
		t.Run(lock, func(t *testing.T) {
			eng := openCompatEngine(t)
			first, second, items := leaderBatchLockItems(t, eng)
			defer first.Close()
			defer second.Close()
			synctest.Test(t, func(t *testing.T) {
				owners := appendWaitObservationOwners(t, items)
				release := holdAppendObservationLock(second, lock)
				defer release()
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				completed := make(chan []AppendBatchResult, 1)
				go func() { completed <- StoreAppendBatch(ctx, items) }()
				synctest.Wait()
				cancel()
				release()
				results := joinAppendObservation(t, completed)
				if len(results) != 3 {
					t.Fatalf("results=%d, want3", len(results))
				}
				for _, result := range results {
					if !errors.Is(result.Err, context.Canceled) || result.Outcome != quorumlog.AppendOutcomeDefinitelyNotWritten {
						t.Error("cancellation outcome changed")
					}
				}
				for _, owner := range owners {
					sample := owner.Finish(rpctrace.ResultCanceled)
					assertAppendObservationStages(t, sample, rpctrace.ResultCanceled, false)
					assertNoAppendObservationCommit(t, sample)
					assertNoStorePrepareDetail(t, sample)
				}
			})
		})
	}
}

func TestStorePrepareDetailNilFullAndFinishedOwnersStayInert(t *testing.T) {
	for _, mode := range []string{"nil", "full", "finished"} {
		t.Run(mode, func(t *testing.T) {
			eng := openCompatEngine(t)
			first, second, items := leaderBatchLockItems(t, eng)
			defer first.Close()
			defer second.Close()
			recorder, err := rpctrace.NewRecorder(1)
			if err != nil {
				t.Fatal(err)
			}
			owner := recorder.Start(context.Background(), rpctrace.SideServer, 13)
			if owner == nil {
				t.Fatal("fixture trace was not admitted")
			}
			var before []byte
			var finished *rpctrace.Sample
			if mode == "full" {
				for range rpctrace.MaxSpans {
					owner.Begin(rpctrace.StageHandler).End(rpctrace.ResultOK)
				}
			}
			if mode == "finished" {
				finished = owner.Finish(rpctrace.ResultOK)
				before, err = json.Marshal(finished)
				if err != nil {
					t.Fatal(err)
				}
			}
			if mode != "nil" {
				for index := range items {
					items[index].CommitObservation = rpctrace.NewCommitObservation(owner)
				}
			}
			assertStorePrepareResults(t, StoreAppendBatch(context.Background(), items), []quorumlog.AppendOutcome{quorumlog.AppendOutcomeDurable, quorumlog.AppendOutcomeDurable, quorumlog.AppendOutcomeAlreadyDurable})
			if mode == "finished" {
				after, marshalErr := json.Marshal(finished)
				if marshalErr != nil || !bytes.Equal(before, after) || owner.Finish(rpctrace.ResultOK) != nil {
					t.Error("finished sample was reopened or mutated")
				}
				assertNoStorePrepareDetail(t, finished)
				return
			}
			sample := owner.Finish(rpctrace.ResultOK)
			assertNoStorePrepareDetail(t, sample)
			if mode == "nil" && (len(sample.Spans) != 0 || sample.CommitCohorts != nil) {
				t.Error("unattached observer gained storage evidence")
			}
			if mode == "full" && len(sample.Spans) != rpctrace.MaxSpans {
				t.Error("full trace span cap changed")
			}
		})
	}
}

func assertStorePrepareResults(t *testing.T, got []AppendBatchResult, want []quorumlog.AppendOutcome) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("result count=%d, want%d", len(got), len(want))
	}
	for index, result := range got {
		if result.Err != nil || result.Outcome != want[index] || result.BaseOffset != 0 || result.LastOffset != 1 {
			t.Errorf("result[%d] lost original durable outcome: %+v", index, result)
		}
	}
}

func requireStorePrepareDetail(t *testing.T, span rpctrace.SpanSample) storePrepareDetailWire {
	t.Helper()
	encoded, err := json.Marshal(span)
	if err != nil {
		t.Fatal(err)
	}
	var parent map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &parent); err != nil {
		t.Fatal(err)
	}
	raw, ok := parent["store_prepare_detail"]
	if !ok || bytes.Equal(raw, []byte("null")) {
		t.Fatal("existing store_append_prepare span is missing fixed numeric store_prepare_detail")
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		t.Fatal("store preparation detail is not an object")
	}
	keys := []string{"known_mask", "frontier_wall", "frontier_calls", "item_prepare_wall", "item_prepare_calls"}
	if len(values) != len(keys) {
		t.Fatal("store preparation detail contains extra fields or omits its fixed numeric shape")
	}
	numbers := make([]int64, len(keys))
	for index, key := range keys {
		value, exists := values[key]
		if !exists || json.Unmarshal(value, &numbers[index]) != nil || numbers[index] < 0 {
			t.Fatalf("fixed numeric preparation field %s is missing, negative or nonnumeric", key)
		}
	}
	detail := storePrepareDetailWire{uint64(numbers[0]), time.Duration(numbers[1]), uint64(numbers[2]), time.Duration(numbers[3]), uint64(numbers[4])}
	if detail.knownMask & ^uint64(3) != 0 || detail.frontierWall > span.Duration || detail.itemWall > span.Duration-detail.frontierWall {
		t.Fatal("detail claims unknown bits or overlapping time outside its preparation parent")
	}
	return detail
}

func assertNoStorePrepareDetail(t *testing.T, sample *rpctrace.Sample) {
	t.Helper()
	if sample == nil {
		t.Fatal("missing observation fixture sample")
	}
	for _, span := range sample.Spans {
		body, err := json.Marshal(span)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(body, &fields); err != nil {
			t.Fatal(err)
		}
		if value, present := fields["store_prepare_detail"]; present && !bytes.Equal(value, []byte("null")) {
			t.Error("inert/full/finished owner gained a preparation child")
		}
	}
}
