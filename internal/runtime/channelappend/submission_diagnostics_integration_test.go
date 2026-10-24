//go:build integration

package channelappend

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/channelappend"
)

func TestOrderedSubmitterTimeoutTimingsRemainPerJobThroughCallback(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		blockerEntered, releaseBlocker := make(chan struct{}), make(chan struct{})
		firstCallback, releaseCallback := make(chan struct{}), make(chan struct{})
		cause := fmt.Errorf("authority wait: %w", context.DeadlineExceeded)
		owner, err := NewOrderedSubmitter(OrderedSubmitterOptions{
			Workers: 1, Capacity: 3, PayloadCapacity: 3, BatchMaxRecords: 2, BatchMaxBytes: 2,
		}, orderedSenderFunc(func(items []SendBatchItem) []SendBatchItemResult {
			if items[0].Command.ClientMsgNo == "blocker" {
				close(blockerEntered)
				<-releaseBlocker
				return []SendBatchItemResult{{}}
			}
			if len(items) != 2 || items[0].Command.ClientMsgNo != "first" || items[1].Command.ClientMsgNo != "second" {
				t.Error("ready original jobs were not coalesced in order")
			}
			time.Sleep(7 * time.Millisecond)
			return []SendBatchItemResult{{Result: SendResult{MessageID: 11}, Err: cause}, {Result: SendResult{MessageID: 22}, Err: cause}}
		}))
		if err != nil {
			t.Fatal(err)
		}
		if err := owner.Submit([]SendBatchItem{orderedItem("blocker", "blocker")}, func([]SendBatchItemResult) {}); err != nil {
			t.Fatal(err)
		}
		<-blockerEntered
		var first, second SendBatchItemResult
		if err := owner.Submit([]SendBatchItem{orderedItem("a", "first")}, func(results []SendBatchItemResult) {
			first = results[0]
			close(firstCallback)
			<-releaseCallback
		}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond)
		if err := owner.Submit([]SendBatchItem{orderedItem("b", "second")}, func(results []SendBatchItemResult) { second = results[0] }); err != nil {
			t.Fatal(err)
		}
		time.Sleep(3 * time.Millisecond)
		close(releaseBlocker)
		<-firstCallback
		firstBefore, present := contract.SubmissionFailureDiagnosticsFromError(first.Err)
		if !present || firstBefore != (contract.SubmissionFailureDiagnostics{Queue: 5 * time.Millisecond, Ready: 5 * time.Millisecond, Route: 7 * time.Millisecond}) {
			t.Fatalf("first job timings = %+v, present %v", firstBefore, present)
		}
		if err := owner.Submit([]SendBatchItem{orderedItem("c", "c"), orderedItem("d", "d")}, func([]SendBatchItemResult) { t.Error("rejected work completed") }); !errors.Is(err, ErrBackpressured) {
			t.Fatalf("callback did not retain ownership: %v", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
		if err := owner.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Close discarded active callback: %v", err)
		}
		cancel()
		time.Sleep(10 * time.Millisecond)
		close(releaseCallback)
		if err := owner.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
		firstAfter, present := contract.SubmissionFailureDiagnosticsFromError(first.Err)
		if !present || firstAfter != firstBefore {
			t.Fatalf("later callback changed first snapshot: before %+v, after %+v", firstBefore, firstAfter)
		}
		secondTiming, present := contract.SubmissionFailureDiagnosticsFromError(second.Err)
		if !present || secondTiming != (contract.SubmissionFailureDiagnostics{Queue: 3 * time.Millisecond, Ready: 3 * time.Millisecond, Route: 7 * time.Millisecond, PreCallback: 11 * time.Millisecond}) {
			t.Fatalf("second job timings = %+v, present %v", secondTiming, present)
		}
		for i, result := range []SendBatchItemResult{first, second} {
			if result.Result.MessageID != uint64((i+1)*11) || !errors.Is(result.Err, context.DeadlineExceeded) || result.Err.Error() != cause.Error() {
				t.Errorf("result %d changed: %+v", i, result)
			}
		}
	})
}

func TestOrderedSubmitterDiagnosticsDoNotInventTimeouts(t *testing.T) {
	other := errors.New("not a context deadline")
	for _, tc := range []struct {
		name    string
		results []SendBatchItemResult
		want    error
	}{
		{"success", []SendBatchItemResult{{}}, nil},
		{"canceled", []SendBatchItemResult{{Err: context.Canceled}}, context.Canceled},
		{"busy", []SendBatchItemResult{{Err: ErrBackpressured}}, ErrBackpressured},
		{"other", []SendBatchItemResult{{Err: other}}, other},
		{"missing", nil, ErrAppendResultMissing},
		{"excess", []SendBatchItemResult{{Err: context.DeadlineExceeded}, {}}, ErrAppendResultMissing},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				owner, err := NewOrderedSubmitter(OrderedSubmitterOptions{Workers: 1, Capacity: 1, PayloadCapacity: 1}, orderedSenderFunc(func([]SendBatchItem) []SendBatchItemResult { return tc.results }))
				if err != nil {
					t.Fatal(err)
				}
				var result SendBatchItemResult
				if err := owner.Submit([]SendBatchItem{orderedItem("a", tc.name)}, func(results []SendBatchItemResult) {
					if len(results) != 1 {
						t.Error("result cardinality changed")
						return
					}
					result = results[0]
				}); err != nil {
					t.Fatal(err)
				}
				if err := owner.Close(context.Background()); err != nil {
					t.Fatal(err)
				}
				if result.Err != tc.want {
					t.Fatalf("error identity changed: %v, want %v", result.Err, tc.want)
				}
				if _, present := contract.SubmissionFailureDiagnosticsFromError(result.Err); present {
					t.Fatal("invented timeout timings")
				}
			})
		})
	}
}

// One admitted job can first wait for its Channel predecessor, then for a
// worker. These intervals are observed through the original public completion.
func TestOrderedSubmitterTimeoutSeparatesDependencyAndReadyWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		entered, release := make(chan struct{}), make(chan struct{})
		callback, releaseCallback := make(chan struct{}), make(chan struct{})
		owner, err := NewOrderedSubmitter(OrderedSubmitterOptions{Workers: 1, Capacity: 3, PayloadCapacity: 3}, orderedSenderFunc(func(items []SendBatchItem) []SendBatchItemResult {
			switch items[0].Command.ClientMsgNo {
			case "first":
				close(entered)
				<-release
				return []SendBatchItemResult{{}}
			case "independent":
				time.Sleep(7 * time.Millisecond)
			case "successor":
				time.Sleep(11 * time.Millisecond)
			default:
				t.Error("unexpected original job")
			}
			return []SendBatchItemResult{{Err: context.DeadlineExceeded}}
		}))
		if err != nil {
			t.Fatal(err)
		}
		if err := owner.Submit([]SendBatchItem{orderedItem("a", "first")}, func([]SendBatchItemResult) { close(callback); <-releaseCallback }); err != nil {
			t.Fatal(err)
		}
		<-entered
		var successor, independent contract.SubmissionFailureDiagnostics
		observe := func(dst *contract.SubmissionFailureDiagnostics) func([]SendBatchItemResult) {
			return func(results []SendBatchItemResult) {
				var present bool
				*dst, present = contract.SubmissionFailureDiagnosticsFromError(results[0].Err)
				if !present || !errors.Is(results[0].Err, context.DeadlineExceeded) {
					t.Error("missing immutable timeout diagnostics")
				}
			}
		}
		if err := owner.Submit([]SendBatchItem{orderedItem("a", "successor")}, observe(&successor)); err != nil {
			t.Fatal(err)
		}
		if err := owner.Submit([]SendBatchItem{orderedItem("b", "independent")}, observe(&independent)); err != nil {
			t.Fatal(err)
		}
		time.Sleep(3 * time.Millisecond)
		close(release)
		<-callback
		time.Sleep(5 * time.Millisecond)
		close(releaseCallback)
		if err := owner.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
		wantIndependent := contract.SubmissionFailureDiagnostics{Queue: 8 * time.Millisecond, Ready: 8 * time.Millisecond, Route: 7 * time.Millisecond}
		wantSuccessor := contract.SubmissionFailureDiagnostics{Queue: 15 * time.Millisecond, Dependency: 8 * time.Millisecond, Ready: 7 * time.Millisecond, Route: 11 * time.Millisecond}
		if independent != wantIndependent {
			t.Errorf("independent phases = %+v, want %+v", independent, wantIndependent)
		}
		if successor != wantSuccessor {
			t.Errorf("successor phases = %+v, want %+v", successor, wantSuccessor)
		}
	})
}
