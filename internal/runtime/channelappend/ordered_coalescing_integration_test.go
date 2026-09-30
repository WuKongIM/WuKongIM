//go:build integration

package channelappend

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestOrderedSubmitterCoalescesOnlyReadyJobsWithinTargets(t *testing.T) {
	for _, tc := range []struct {
		name           string
		records, bytes int
		want           [][]string
	}{
		{"records", 3, 100, [][]string{{"1", "2", "3"}, {"4", "5"}}},
		{"bytes", 10, 2, [][]string{{"1", "2"}, {"3", "4"}, {"5"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entered := make(chan []string, 8)
			release := make(chan struct{})
			callbackEntered := make(chan struct{})
			releaseCallback := make(chan struct{})
			done := make(chan string, 8)
			var once, callbackOnce sync.Once
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			deadline := time.Now().Add(time.Minute)
			s, err := NewOrderedSubmitter(OrderedSubmitterOptions{Workers: 1, Capacity: 8, PayloadCapacity: 100, BatchMaxRecords: tc.records, BatchMaxBytes: tc.bytes}, orderedSenderFunc(func(items []SendBatchItem) []SendBatchItemResult {
				ids := make([]string, len(items))
				results := make([]SendBatchItemResult, len(items))
				for i, item := range items {
					ids[i] = item.Command.ClientMsgNo
					if item.Context != ctx || !item.Deadline.Equal(deadline) {
						t.Error("lost per-item cancellation/deadline")
					}
					results[i].Result.MessageID = uint64(item.Command.ClientSeq)
				}
				entered <- ids
				if ids[0] == "first" {
					<-release
				}
				return results
			}))
			if err != nil {
				t.Fatal(err)
			}
			defer orderedClose(t, s)
			defer once.Do(func() { close(release) })
			defer callbackOnce.Do(func() { close(releaseCallback) })
			submit := func(channel, id string, seq uint64) {
				t.Helper()
				item := orderedItem(channel, id)
				item.Command.ClientSeq = seq
				item.Context = ctx
				item.Deadline = deadline
				if err := s.Submit([]SendBatchItem{item}, func(results []SendBatchItemResult) {
					if len(results) != 1 || results[0].Result.MessageID != seq {
						t.Errorf("unaligned callback %s: %#v", id, results)
					}
					if id == "1" {
						close(callbackEntered)
						<-releaseCallback
					}
					done <- id
				}); err != nil {
					t.Fatal(err)
				}
			}
			submit("first", "first", 10)
			orderedWait(t, entered)
			for i := 1; i <= 4; i++ {
				submit(fmt.Sprint(i), fmt.Sprint(i), uint64(i))
			}
			// This successor is ready only after job 1's completion returns.
			submit("1", "5", 5)
			once.Do(func() { close(release) })
			if got := orderedWait(t, entered); !reflect.DeepEqual(got, tc.want[0]) {
				t.Fatalf("queued jobs not coalesced: got %v want %v", got, tc.want[0])
			}
			orderedWait(t, callbackEntered)
			stats := s.poolStats()
			if stats.BusyTasks != 1 || stats.QueueDepth != int64(5-len(tc.want[0])) {
				t.Fatalf("worker/queued pressure counts jobs incorrectly: %+v", stats)
			}
			callbackOnce.Do(func() { close(releaseCallback) })
			for _, want := range tc.want[1:] {
				if got := orderedWait(t, entered); !reflect.DeepEqual(got, want) {
					t.Fatalf("dependency/target order got %v want %v", got, want)
				}
			}
			for range 6 {
				orderedWait(t, done)
			}
		})
	}
}

func TestOrderedSubmitterCoalescedCallbackKeepsCapacity(t *testing.T) {
	entered := make(chan []SendBatchItem, 4)
	release := make(chan struct{})
	inCallback := make(chan struct{})
	finishCallback := make(chan struct{})
	var firstOnce, callbackOnce sync.Once
	s, err := NewOrderedSubmitter(OrderedSubmitterOptions{Workers: 1, Capacity: 3, PayloadCapacity: 10, BatchMaxRecords: 2, BatchMaxBytes: 1}, orderedSenderFunc(func(items []SendBatchItem) []SendBatchItemResult {
		entered <- items
		if items[0].Command.ClientMsgNo == "first" {
			<-release
		}
		return make([]SendBatchItemResult, len(items))
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer orderedClose(t, s)
	defer firstOnce.Do(func() { close(release) })
	defer callbackOnce.Do(func() { close(finishCallback) })
	if err = s.Submit([]SendBatchItem{orderedItem("f", "first")}, func([]SendBatchItemResult) {}); err != nil {
		t.Fatal(err)
	}
	orderedWait(t, entered)
	// An admitted multi-record job over a merge target must still execute intact.
	if err = s.Submit([]SendBatchItem{orderedItem("a", "a"), orderedItem("b", "b")}, func(results []SendBatchItemResult) {
		if len(results) != 2 {
			t.Error("split original job")
		}
		close(inCallback)
		<-finishCallback
	}); err != nil {
		t.Fatal(err)
	}
	firstOnce.Do(func() { close(release) })
	if got := orderedWait(t, entered); len(got) != 2 {
		t.Fatal("split an admitted job")
	}
	orderedWait(t, inCallback)
	if err = s.Submit([]SendBatchItem{orderedItem("c", "c"), orderedItem("d", "d")}, func([]SendBatchItemResult) {}); !errors.Is(err, ErrBackpressured) {
		t.Fatalf("callback lost its original reservation: %v", err)
	}
	callbackOnce.Do(func() { close(finishCallback) })
}

func TestOrderedSubmitterRejectsNegativeBatchTargets(t *testing.T) {
	for _, opts := range []OrderedSubmitterOptions{
		{Workers: 1, Capacity: 1, PayloadCapacity: 1, BatchMaxRecords: -1, BatchMaxBytes: 1},
		{Workers: 1, Capacity: 1, PayloadCapacity: 1, BatchMaxRecords: 1, BatchMaxBytes: -1},
	} {
		s, err := NewOrderedSubmitter(opts, orderedSenderFunc(func(items []SendBatchItem) []SendBatchItemResult { return nil }))
		if err == nil {
			orderedClose(t, s)
			t.Fatal("negative batch target accepted")
		}
	}
}
