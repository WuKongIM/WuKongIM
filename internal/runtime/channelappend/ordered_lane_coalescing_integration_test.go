//go:build integration

package channelappend

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

// Failure modes: a hot lane stays serialized into singleton routing calls;
// merging exceeds a target, splits an original job, loses per-item deadlines,
// crosses a multi-Channel fence, publishes twice, or releases callback capacity.
func TestOrderedSubmitterCoalescesAdmittedHotLaneWithinTargets(t *testing.T) {
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
			done := make(chan string, 8)
			release := make(chan struct{})
			var once sync.Once
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			deadline := time.Now().Add(time.Minute)
			s, err := NewOrderedSubmitter(OrderedSubmitterOptions{Workers: 1, Capacity: 8, PayloadCapacity: 100, BatchMaxRecords: tc.records, BatchMaxBytes: tc.bytes}, orderedSenderFunc(func(items []SendBatchItem) []SendBatchItemResult {
				ids := make([]string, len(items))
				results := make([]SendBatchItemResult, len(items))
				for i, item := range items {
					ids[i] = item.Command.ClientMsgNo
					if item.Context != ctx || !item.Deadline.Equal(deadline) {
						t.Error("lost per-item context/deadline")
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
			for i, id := range []string{"first", "1", "2", "3", "4", "5"} {
				item := orderedItem("hot", id)
				item.Context, item.Deadline, item.Command.ClientSeq = ctx, deadline, uint64(i+1)
				seq := uint64(i + 1)
				if err := s.Submit([]SendBatchItem{item}, func(results []SendBatchItemResult) {
					if len(results) != 1 || results[0].Result.MessageID != seq {
						t.Errorf("unaligned %s: %#v", id, results)
					}
					done <- id
				}); err != nil {
					t.Fatal(err)
				}
				if i == 0 {
					orderedWait(t, entered)
				}
			}
			once.Do(func() { close(release) })
			for _, want := range tc.want {
				if got := orderedWait(t, entered); !reflect.DeepEqual(got, want) {
					t.Fatalf("hot lane got %v want %v", got, want)
				}
			}
			for _, want := range []string{"first", "1", "2", "3", "4", "5"} {
				if got := orderedWait(t, done); got != want {
					t.Fatalf("callback order %s want %s", got, want)
				}
			}
		})
	}
}

func TestOrderedSubmitterHotLaneStopsAtMultiChannelFence(t *testing.T) {
	entered := make(chan []string, 8)
	release := make(chan struct{})
	var once sync.Once
	s, err := NewOrderedSubmitter(OrderedSubmitterOptions{Workers: 1, Capacity: 8, PayloadCapacity: 100, BatchMaxRecords: 8, BatchMaxBytes: 100}, orderedSenderFunc(func(items []SendBatchItem) []SendBatchItemResult {
		ids := make([]string, len(items))
		for i, item := range items {
			ids[i] = item.Command.ClientMsgNo
		}
		entered <- ids
		if ids[0] == "first" {
			<-release
		}
		return make([]SendBatchItemResult, len(items))
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer orderedClose(t, s)
	defer once.Do(func() { close(release) })
	for i, items := range [][]SendBatchItem{
		{orderedItem("a", "first")}, {orderedItem("a", "a1")}, {orderedItem("a", "a2")},
		{orderedItem("a", "ab"), orderedItem("b", "ab")}, {orderedItem("a", "a3")}, {orderedItem("b", "b1")},
	} {
		if err := s.Submit(items, func([]SendBatchItemResult) {}); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			orderedWait(t, entered)
		}
	}
	once.Do(func() { close(release) })
	for _, want := range [][]string{{"a1", "a2"}, {"ab", "ab"}, {"a3", "b1"}} {
		got := orderedWait(t, entered)
		// Independent successors have no required relative order.
		if len(got) == 2 && got[0] == "b1" {
			got[0], got[1] = got[1], got[0]
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("crossed Channel fence: got %v want %v", got, want)
		}
	}
}

func TestOrderedSubmitterHotLaneCallbacksKeepFenceAndCapacity(t *testing.T) {
	entered := make(chan []string, 8)
	release := make(chan struct{})
	callback := make(chan struct{})
	finishCallback := make(chan struct{})
	var once, callbackOnce sync.Once
	s, err := NewOrderedSubmitter(OrderedSubmitterOptions{Workers: 2, Capacity: 4, PayloadCapacity: 4, BatchMaxRecords: 2, BatchMaxBytes: 2}, orderedSenderFunc(func(items []SendBatchItem) []SendBatchItemResult {
		ids := make([]string, len(items))
		for i, item := range items {
			ids[i] = item.Command.ClientMsgNo
		}
		entered <- ids
		if ids[0] == "first" {
			<-release
		}
		return make([]SendBatchItemResult, len(items))
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer orderedClose(t, s)
	defer once.Do(func() { close(release) })
	defer callbackOnce.Do(func() { close(finishCallback) })
	for i, id := range []string{"first", "1", "2", "3"} {
		if err := s.Submit([]SendBatchItem{orderedItem("hot", id)}, func([]SendBatchItemResult) {
			if id == "1" {
				close(callback)
				<-finishCallback
			}
		}); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			orderedWait(t, entered)
		}
	}
	once.Do(func() { close(release) })
	if got := orderedWait(t, entered); !reflect.DeepEqual(got, []string{"1", "2"}) {
		t.Fatalf("hot lane batch %v", got)
	}
	orderedWait(t, callback)
	if err := s.Submit([]SendBatchItem{orderedItem("other", "x"), orderedItem("other", "y")}, func([]SendBatchItemResult) {}); !errors.Is(err, ErrBackpressured) {
		t.Fatalf("released callback capacity: %v", err)
	}
	if stats := s.poolStats(); stats.BusyTasks != 1 || stats.QueueDepth != 1 {
		t.Fatalf("bad ownership: %+v", stats)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := s.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close failed to join callback: %v", err)
	}
	select {
	case got := <-entered:
		t.Fatalf("successor overtook callback: %v", got)
	default:
	}
	callbackOnce.Do(func() { close(finishCallback) })
	if got := orderedWait(t, entered); !reflect.DeepEqual(got, []string{"3"}) {
		t.Fatalf("duplicate dispatch or lost successor: %v", got)
	}
	orderedClose(t, s)
	select {
	case got := <-entered:
		t.Fatalf("duplicate routing: %v", got)
	default:
	}
}
