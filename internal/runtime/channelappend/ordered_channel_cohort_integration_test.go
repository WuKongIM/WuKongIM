//go:build integration

package channelappend

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/channelappend"
)

type cohortResolverFunc func(context.Context, ChannelID) (AuthorityTarget, error)

func (f cohortResolverFunc) ResolveAppendAuthority(ctx context.Context, key ChannelID) (AuthorityTarget, error) {
	return f(ctx, key)
}

type cohortRemoteFunc func(context.Context, AuthorityTarget, []SendBatchItem) []SendBatchItemResult

func (f cohortRemoteFunc) ForwardSendBatch(ctx context.Context, target AuthorityTarget, items []SendBatchItem) []SendBatchItemResult {
	return f(ctx, target, items)
}

// cohortRouter observes actual Router port calls, not scheduler internals.
func cohortRouter(calls chan<- []string, gate <-chan struct{}, resolves *atomic.Int32) *Router {
	return NewRouter(RouterOptions{LocalNodeID: 1,
		Resolver: cohortResolverFunc(func(_ context.Context, key ChannelID) (AuthorityTarget, error) {
			resolves.Add(1)
			return routerTarget(key.ID, key.Type, 2), nil
		}),
		Remote: cohortRemoteFunc(func(_ context.Context, _ AuthorityTarget, items []SendBatchItem) []SendBatchItemResult {
			names := make([]string, len(items))
			results := make([]SendBatchItemResult, len(items))
			for i, item := range items {
				names[i] = item.Command.ClientMsgNo
				results[i].Result = SendResult{MessageID: item.Command.ClientSeq + 1, Reason: ReasonSuccess}
			}
			calls <- names
			if names[0] == "a0" {
				<-gate
			}
			return results
		}),
	})
}

func TestOrderedChannelCohortUsesOnePhysicalForwardWithinTargets(t *testing.T) {
	for _, tc := range []struct {
		name           string
		records, bytes int
		want           [][]string
	}{
		{"records", 3, 10, [][]string{{"a1", "a2", "a3"}}},
		{"bytes", 10, 2, [][]string{{"a1", "a2"}, {"a3"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := make(chan []string, 8)
			gate := make(chan struct{})
			var once sync.Once
			var resolves atomic.Int32
			s, err := NewOrderedSubmitter(OrderedSubmitterOptions{Workers: 1, Capacity: 8, PayloadCapacity: 20,
				SameChannelMaxRecords: tc.records, SameChannelMaxBytes: tc.bytes}, cohortRouter(calls, gate, &resolves))
			if err != nil {
				t.Fatal(err)
			}
			defer orderedClose(t, s)
			defer once.Do(func() { close(gate) })
			done := make(chan string, 8)
			for i, name := range []string{"a0", "a1", "a2", "a3"} {
				item := orderedItem("a", name)
				item.Command.ClientSeq = uint64(i)
				if err := s.Submit([]SendBatchItem{item}, func(results []SendBatchItemResult) {
					if len(results) != 1 || results[0].Err != nil || results[0].Result.MessageID != uint64(i+1) {
						t.Errorf("unaligned original callback %s: %+v", name, results)
					}
					done <- name
				}); err != nil {
					t.Fatal(err)
				}
				if i == 0 {
					orderedWait(t, calls)
				}
			}
			once.Do(func() { close(gate) })
			for _, want := range tc.want {
				if got := orderedWait(t, calls); !reflect.DeepEqual(got, want) {
					t.Errorf("physical Forward batch = %v, want sealed prefix %v", got, want)
				}
			}
			for _, want := range []string{"a0", "a1", "a2", "a3"} {
				if got := orderedWait(t, done); got != want {
					t.Errorf("callback %s before %s", got, want)
				}
			}
			orderedClose(t, s)
			if got := resolves.Load(); got != int32(1+len(tc.want)) {
				t.Errorf("authority resolves = %d, want %d", got, 1+len(tc.want))
			}
		})
	}
}

func TestOrderedChannelCohortRetainsCallbacksCreditsAndOutsideFence(t *testing.T) {
	calls := make(chan []string, 16)
	gate, callbackGate := make(chan struct{}), make(chan struct{})
	callbackEntered := make(chan struct{})
	var release, releaseCallback sync.Once
	var resolves atomic.Int32
	s, err := NewOrderedSubmitter(OrderedSubmitterOptions{Workers: 2, Capacity: 4, PayloadCapacity: 4,
		SameChannelMaxRecords: 2, SameChannelMaxBytes: 2}, cohortRouter(calls, gate, &resolves))
	if err != nil {
		t.Fatal(err)
	}
	defer orderedClose(t, s)
	defer release.Do(func() { close(gate) })
	defer releaseCallback.Do(func() { close(callbackGate) })
	done := make(chan string, 16)
	submit := func(key, name string) {
		t.Helper()
		if err := s.Submit([]SendBatchItem{orderedItem(key, name)}, func(results []SendBatchItemResult) {
			if name == "a1" {
				close(callbackEntered)
				<-callbackGate
			}
			done <- name
		}); err != nil {
			t.Fatal(err)
		}
	}
	submit("a", "a0")
	orderedWait(t, calls)
	submit("a", "a1")
	submit("a", "a2")
	submit("a", "a3")
	release.Do(func() { close(gate) })
	if got := orderedWait(t, calls); !reflect.DeepEqual(got, []string{"a1", "a2"}) {
		t.Errorf("cohort = %v, want a1/a2", got)
	}
	orderedWait(t, callbackEntered)
	if got := orderedWait(t, done); got != "a0" {
		t.Fatal(got)
	}
	if err := s.Submit([]SendBatchItem{orderedItem("b", "b1"), orderedItem("b", "b2")}, func([]SendBatchItemResult) {}); !errors.Is(err, ErrBackpressured) {
		t.Errorf("original callbacks must retain three credits, got %v", err)
	}
	submit("c", "c")
	if got := orderedWait(t, calls); !reflect.DeepEqual(got, []string{"c"}) {
		t.Fatalf("outside successor crossed callback: %v", got)
	}
	if got := orderedWait(t, done); got != "c" {
		t.Fatalf("cohort callback overtook predecessor: %s", got)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if err := s.Pause(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Pause lost callback ownership: %v", err)
	}
	if err := s.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Close lost callback ownership: %v", err)
	}
	releaseCallback.Do(func() { close(callbackGate) })
	if got := orderedWait(t, calls); !reflect.DeepEqual(got, []string{"a3"}) {
		t.Fatal(got)
	}
	for _, want := range []string{"a1", "a2", "a3"} {
		if got := orderedWait(t, done); got != want {
			t.Errorf("callback %s before %s", got, want)
		}
	}
	orderedClose(t, s)
}

func TestOrderedChannelCohortDoesNotCrossMixedKeyCallbackFence(t *testing.T) {
	calls := make(chan []string, 16)
	gate, callbackGate := make(chan struct{}), make(chan struct{})
	callbackEntered := make(chan struct{})
	var release, releaseCallback sync.Once
	var resolves atomic.Int32
	s, err := NewOrderedSubmitter(OrderedSubmitterOptions{Workers: 3, Capacity: 12, PayloadCapacity: 20,
		SameChannelMaxRecords: 8, SameChannelMaxBytes: 8}, cohortRouter(calls, gate, &resolves))
	if err != nil {
		t.Fatal(err)
	}
	defer orderedClose(t, s)
	defer release.Do(func() { close(gate) })
	defer releaseCallback.Do(func() { close(callbackGate) })
	done := make(chan string, 10)
	submit := func(name string, items ...SendBatchItem) {
		t.Helper()
		if err := s.Submit(items, func(results []SendBatchItemResult) {
			if len(results) != len(items) {
				t.Error("split original mixed-key job")
			}
			if name == "ab" {
				close(callbackEntered)
				<-callbackGate
			}
			done <- name
		}); err != nil {
			t.Fatal(err)
		}
	}
	submit("a0", orderedItem("a", "a0"))
	orderedWait(t, calls)
	submit("ab", orderedItem("a", "ab-a"), orderedItem("b", "ab-b"))
	submit("a3", orderedItem("a", "a3"))
	submit("b3", orderedItem("b", "b3"))
	release.Do(func() { close(gate) })
	seen := map[string]bool{}
	for range 2 {
		got := orderedWait(t, calls)
		if len(got) != 1 {
			t.Fatalf("mixed-key job crossed cohort: %v", got)
		}
		seen[got[0]] = true
	}
	if !seen["ab-a"] || !seen["ab-b"] {
		t.Fatalf("successor overtook mixed-key job: %v", seen)
	}
	orderedWait(t, callbackEntered)
	if got := orderedWait(t, done); got != "a0" {
		t.Fatal(got)
	}
	submit("c", orderedItem("c", "c"))
	if got := orderedWait(t, calls); !reflect.DeepEqual(got, []string{"c"}) {
		t.Fatal(got)
	}
	if got := orderedWait(t, done); got != "c" {
		t.Fatal(got)
	}
	releaseCallback.Do(func() { close(callbackGate) })
	seen = map[string]bool{}
	for range 2 {
		got := orderedWait(t, calls)
		if len(got) != 1 {
			t.Fatal(got)
		}
		seen[got[0]] = true
	}
	if !seen["a3"] || !seen["b3"] {
		t.Fatal(seen)
	}
	for range 3 {
		orderedWait(t, done)
	}
}

func TestOrderedChannelCohortAdmissionGuardRemainsSeparate(t *testing.T) {
	calls := make(chan []string, 16)
	gate := make(chan struct{})
	var release sync.Once
	var resolves, guards atomic.Int32
	s, err := NewOrderedSubmitter(OrderedSubmitterOptions{Workers: 2, Capacity: 8, PayloadCapacity: 10,
		SameChannelMaxRecords: 8, SameChannelMaxBytes: 8}, cohortRouter(calls, gate, &resolves))
	if err != nil {
		t.Fatal(err)
	}
	defer orderedClose(t, s)
	defer release.Do(func() { close(gate) })
	done := make(chan struct{}, 8)
	for i, name := range []string{"a0", "a1", "a2", "a3"} {
		item := orderedItem("a", name)
		if name == "a1" {
			item.AppendAdmission = func(context.Context) error { guards.Add(1); return nil }
		}
		if err := s.Submit([]SendBatchItem{item}, func([]SendBatchItemResult) { done <- struct{}{} }); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			orderedWait(t, calls)
		}
	}
	release.Do(func() { close(gate) })
	for _, want := range [][]string{{"a1"}, {"a2", "a3"}} {
		if got := orderedWait(t, calls); !reflect.DeepEqual(got, want) {
			t.Errorf("guard boundary got %v want %v", got, want)
		}
	}
	for range 4 {
		orderedWait(t, done)
	}
	if guards.Load() != 1 {
		t.Errorf("guard count %d", guards.Load())
	}
}

func TestOrderedChannelCohortRejectsInvalidTargets(t *testing.T) {
	for _, targets := range [][2]int{{-1, 1}, {1, -1}, {0, 1}, {1, 0}} {
		s, err := NewOrderedSubmitter(OrderedSubmitterOptions{Workers: 1, Capacity: 2, PayloadCapacity: 2,
			SameChannelMaxRecords: targets[0], SameChannelMaxBytes: targets[1]}, orderedSenderFunc(func(items []SendBatchItem) []SendBatchItemResult { return make([]SendBatchItemResult, len(items)) }))
		if err == nil {
			orderedClose(t, s)
			t.Errorf("accepted invalid targets %v", targets)
		}
	}
}

func TestOrderedChannelCohortTimeoutQueueIntervalsRemainOriginalJobAligned(t *testing.T) {
	entered, gate := make(chan struct{}), make(chan struct{})
	var release sync.Once
	sender := orderedSenderFunc(func(items []SendBatchItem) []SendBatchItemResult {
		if items[0].Command.ClientMsgNo == "a0" {
			close(entered)
			<-gate
		}
		results := make([]SendBatchItemResult, len(items))
		for i := range results {
			results[i].Err = context.DeadlineExceeded
		}
		return results
	})
	s, err := NewOrderedSubmitter(OrderedSubmitterOptions{Workers: 2, Capacity: 8, PayloadCapacity: 10,
		SameChannelMaxRecords: 3, SameChannelMaxBytes: 3}, sender)
	if err != nil {
		t.Fatal(err)
	}
	defer orderedClose(t, s)
	defer release.Do(func() { close(gate) })
	type completed struct {
		name   string
		timing contract.SubmissionFailureDiagnostics
	}
	done := make(chan completed, 4)
	for i, name := range []string{"a0", "a1", "a2", "a3"} {
		if err := s.Submit([]SendBatchItem{orderedItem("a", name)}, func(results []SendBatchItemResult) {
			d, ok := contract.SubmissionFailureDiagnosticsFromError(results[0].Err)
			if !ok || !errors.Is(results[0].Err, context.DeadlineExceeded) {
				t.Error("lost timeout diagnostic or cause")
			}
			done <- completed{name, d}
		}); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			orderedWait(t, entered)
		}
	}
	release.Do(func() { close(gate) })
	for range 4 {
		x := orderedWait(t, done)
		d := x.timing
		if d.Dependency < 0 || d.Ready < 0 || d.Dependency+d.Ready != d.Queue {
			t.Errorf("invalid intervals for %s: %+v", x.name, d)
		}
		if x.name == "a2" || x.name == "a3" {
			if d.Ready != 0 || d.Dependency != d.Queue {
				t.Errorf("cohort takeover recorded as independent ready wait for %s: %+v", x.name, d)
			}
		}
	}
}
