//go:build integration

package channelappend

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
)

type orderedSenderFunc func([]SendBatchItem) []SendBatchItemResult

func (f orderedSenderFunc) SendBatch(items []SendBatchItem) []SendBatchItemResult { return f(items) }
func orderedItem(channel, number string) SendBatchItem {
	item := routerItem("u", channel, 2)
	item.Command.ClientMsgNo = number
	item.Command.Payload = []byte("x")
	return item
}
func orderedWait[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(3 * time.Second):
		t.Fatal("ordered submitter did not make progress")
		var zero T
		return zero
	}
}
func orderedClose(t *testing.T, s *OrderedSubmitter) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestOrderedSubmitterIndependentProgressAndOverlappingFIFO(t *testing.T) {
	entered := make(chan string, 8)
	release := make(chan struct{})
	done := make(chan string, 8)
	sender := orderedSenderFunc(func(items []SendBatchItem) []SendBatchItemResult {
		name := items[0].Command.ClientMsgNo
		entered <- name
		if name == "a1" {
			<-release
		}
		results := make([]SendBatchItemResult, len(items))
		for i := range results {
			results[i].Result.MessageID = uint64(i + 10)
		}
		return results
	})
	s, err := NewOrderedSubmitter(OrderedSubmitterOptions{Workers: 2, Capacity: 8, PayloadCapacity: 8}, sender)
	if err != nil {
		t.Fatal(err)
	}
	defer orderedClose(t, s)
	var once sync.Once
	defer once.Do(func() { close(release) })
	submit := func(name string, items ...SendBatchItem) {
		t.Helper()
		if err := s.Submit(items, func(results []SendBatchItemResult) {
			if len(results) != len(items) {
				t.Errorf("result cardinality %d", len(results))
			}
			for i, r := range results {
				if r.Result.MessageID != uint64(i+10) {
					t.Errorf("result alignment %d", i)
				}
			}
			done <- name
		}); err != nil {
			t.Fatal(err)
		}
	}
	submit("a1", orderedItem("a", "a1"))
	if got := orderedWait(t, entered); got != "a1" {
		t.Fatal(got)
	}
	submit("ab2", orderedItem("a", "ab2"), orderedItem("b", "ab2"))
	submit("b3", orderedItem("b", "b3"))
	submit("c4", orderedItem("c", "c4"))
	if got := orderedWait(t, entered); got != "c4" {
		t.Fatalf("overlap overtook predecessor: %s", got)
	}
	if got := orderedWait(t, done); got != "c4" {
		t.Fatal(got)
	}
	once.Do(func() { close(release) })
	if got := orderedWait(t, entered); got != "ab2" {
		t.Fatal(got)
	}
	if got := orderedWait(t, entered); got != "b3" {
		t.Fatal(got)
	}
	for range 3 {
		orderedWait(t, done)
	}
}

func TestOrderedSubmitterRetainsRecordAndPayloadCapacityThroughCallback(t *testing.T) {
	for _, tc := range []struct {
		name           string
		records, bytes int
	}{{"records", 1, 10}, {"payload", 10, 1}} {
		t.Run(tc.name, func(t *testing.T) {
			inCallback := make(chan struct{})
			release := make(chan struct{})
			var callbacks atomic.Int32
			s, err := NewOrderedSubmitter(OrderedSubmitterOptions{Workers: 1, Capacity: tc.records, PayloadCapacity: tc.bytes}, orderedSenderFunc(func(items []SendBatchItem) []SendBatchItemResult { return make([]SendBatchItemResult, len(items)) }))
			if err != nil {
				t.Fatal(err)
			}
			defer orderedClose(t, s)
			var once sync.Once
			defer once.Do(func() { close(release) })
			if err := s.Submit([]SendBatchItem{orderedItem("a", "1")}, func([]SendBatchItemResult) { callbacks.Add(1); close(inCallback); <-release }); err != nil {
				t.Fatal(err)
			}
			orderedWait(t, inCallback)
			if err := s.Submit([]SendBatchItem{orderedItem("b", "2")}, func([]SendBatchItemResult) { callbacks.Add(1) }); !errors.Is(err, ErrBackpressured) {
				t.Fatalf("active callback did not retain budget: %v", err)
			}
			if callbacks.Load() != 1 {
				t.Fatal("rejected work completed")
			}
			once.Do(func() { close(release) })
		})
	}
}

func TestOrderedSubmitterCanonicalKeysAndDescriptorOwnership(t *testing.T) {
	entered := make(chan string, 8)
	release := make(chan struct{})
	done := make(chan struct{}, 8)
	s, err := NewOrderedSubmitter(OrderedSubmitterOptions{Workers: 2, Capacity: 8, PayloadCapacity: 8, CommandChannelSuffix: "_cmd"}, orderedSenderFunc(func(items []SendBatchItem) []SendBatchItemResult {
		name := items[0].Command.ClientMsgNo
		entered <- name
		if name == "first" {
			<-release
		}
		return make([]SendBatchItemResult, len(items))
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer orderedClose(t, s)
	var once sync.Once
	defer once.Do(func() { close(release) })
	first := orderedItem("b", "first")
	first.Command.FromUID = "a"
	first.Command.ChannelType = 1
	first.Command.NormalizePersonChannel = true
	second := orderedItem("a", "second")
	second.Command.FromUID = "b"
	second.Command.ChannelType = 1
	second.Command.NormalizePersonChannel = true
	submit := func(items []SendBatchItem) {
		t.Helper()
		if err := s.Submit(items, func([]SendBatchItemResult) { done <- struct{}{} }); err != nil {
			t.Fatal(err)
		}
	}
	submit([]SendBatchItem{first})
	if got := orderedWait(t, entered); got != "first" {
		t.Fatal(got)
	}
	items := []SendBatchItem{second, second}
	submit(items)
	items[0].Command.ClientMsgNo = "mutated"
	other := orderedItem("a@b", "different-type")
	other.Command.ChannelType = 2
	submit([]SendBatchItem{other})
	if got := orderedWait(t, entered); got != "different-type" {
		t.Fatalf("canonical pair not fenced: %s", got)
	}
	once.Do(func() { close(release) })
	if got := orderedWait(t, entered); got != "second" {
		t.Fatal(got)
	}
	for range 3 {
		orderedWait(t, done)
	}
}

func TestOrderedSubmitterCloseTimeoutPreservesAcceptedWorkAndFence(t *testing.T) {
	entered := make(chan string, 4)
	release := make(chan struct{})
	completed := make(chan string, 4)
	s, err := NewOrderedSubmitter(OrderedSubmitterOptions{Workers: 1, Capacity: 3, PayloadCapacity: 3}, orderedSenderFunc(func(items []SendBatchItem) []SendBatchItemResult {
		name := items[0].Command.ClientMsgNo
		entered <- name
		if name == "1" {
			<-release
		}
		return make([]SendBatchItemResult, len(items))
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer orderedClose(t, s)
	var once sync.Once
	defer once.Do(func() { close(release) })
	for _, name := range []string{"1", "2"} {
		name := name
		if err := s.Submit([]SendBatchItem{orderedItem("a", name)}, func([]SendBatchItemResult) { completed <- name }); err != nil {
			t.Fatal(err)
		}
	}
	if got := orderedWait(t, entered); got != "1" {
		t.Fatal(got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Close(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := s.Submit([]SendBatchItem{orderedItem("c", "3")}, func([]SendBatchItemResult) {}); !errors.Is(err, ErrRouteNotReady) {
		t.Fatalf("closed admission: %v", err)
	}
	once.Do(func() { close(release) })
	if got := orderedWait(t, entered); got != "2" {
		t.Fatal(got)
	}
	orderedClose(t, s)
	if got := orderedWait(t, completed); got != "1" {
		t.Fatal(got)
	}
	if got := orderedWait(t, completed); got != "2" {
		t.Fatal(got)
	}
}

func TestOrderedSubmitterRejectsInvalidAndOversizedAdmissionAtomically(t *testing.T) {
	sender := orderedSenderFunc(func(items []SendBatchItem) []SendBatchItemResult { return make([]SendBatchItemResult, len(items)) })
	for _, opts := range []OrderedSubmitterOptions{{}, {Workers: 1, Capacity: 1}, {Workers: 1, PayloadCapacity: 1}, {Capacity: 1, PayloadCapacity: 1}} {
		if s, err := NewOrderedSubmitter(opts, sender); err == nil {
			orderedClose(t, s)
			t.Fatal("invalid config accepted")
		}
	}
	if s, err := NewOrderedSubmitter(OrderedSubmitterOptions{Workers: 1, Capacity: 1, PayloadCapacity: 1}, nil); err == nil {
		orderedClose(t, s)
		t.Fatal("nil sender accepted")
	}
	s, err := NewOrderedSubmitter(OrderedSubmitterOptions{Workers: 1, Capacity: 2, PayloadCapacity: 2}, sender)
	if err != nil {
		t.Fatal(err)
	}
	defer orderedClose(t, s)
	var called atomic.Int32
	callback := func([]SendBatchItemResult) { called.Add(1) }
	if err := s.Submit(nil, callback); err == nil {
		t.Fatal("empty accepted")
	}
	if err := s.Submit([]SendBatchItem{orderedItem("a", "x")}, nil); err == nil {
		t.Fatal("nil callback accepted")
	}
	items := []SendBatchItem{orderedItem("a", "x"), orderedItem("b", "x"), orderedItem("c", "x")}
	if err := s.Submit(items, callback); !errors.Is(err, ErrBackpressured) {
		t.Fatal(err)
	}
	item := orderedItem("a", "x")
	item.Command.Payload = []byte("123")
	if err := s.Submit([]SendBatchItem{item}, callback); !errors.Is(err, ErrBackpressured) {
		t.Fatal(err)
	}
	if called.Load() != 0 {
		t.Fatal("rejected batch had partial completion")
	}
}

func TestOrderedSubmitterWorkerBoundAndQueuedCancellation(t *testing.T) {
	entered := make(chan string, 4)
	release := make(chan struct{})
	completed := make(chan struct{}, 4)
	s, err := NewOrderedSubmitter(OrderedSubmitterOptions{Workers: 2, Capacity: 4, PayloadCapacity: 4}, orderedSenderFunc(func(items []SendBatchItem) []SendBatchItemResult {
		entered <- items[0].Command.ClientMsgNo
		<-release
		results := make([]SendBatchItemResult, len(items))
		for i, item := range items {
			if item.Context != nil {
				results[i].Err = item.Context.Err()
			}
		}
		return results
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer orderedClose(t, s)
	var once sync.Once
	defer once.Do(func() { close(release) })
	for _, name := range []string{"a", "b"} {
		if err := s.Submit([]SendBatchItem{orderedItem(name, name)}, func([]SendBatchItemResult) { completed <- struct{}{} }); err != nil {
			t.Fatal(err)
		}
	}
	orderedWait(t, entered)
	orderedWait(t, entered)
	ctx, cancel := context.WithCancel(context.Background())
	item := orderedItem("c", "c")
	item.Context = ctx
	if err := s.Submit([]SendBatchItem{item}, func(results []SendBatchItemResult) {
		if !errors.Is(results[0].Err, context.Canceled) {
			t.Errorf("cancellation result %v", results[0].Err)
		}
		completed <- struct{}{}
	}); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case got := <-entered:
		t.Fatalf("third worker started: %s", got)
	case <-time.After(20 * time.Millisecond):
	}
	once.Do(func() { close(release) })
	if got := orderedWait(t, entered); got != "c" {
		t.Fatal(got)
	}
	for range 3 {
		orderedWait(t, completed)
	}
}

func TestOrderedSubmitterCommandAliasesShareLane(t *testing.T) {
	entered := make(chan string, 4)
	release := make(chan struct{})
	completed := make(chan struct{}, 4)
	s, err := NewOrderedSubmitter(OrderedSubmitterOptions{Workers: 2, Capacity: 4, PayloadCapacity: 4, CommandChannelSuffix: "_cmd"}, orderedSenderFunc(func(items []SendBatchItem) []SendBatchItemResult {
		name := items[0].Command.ClientMsgNo
		entered <- name
		if name == "first" {
			<-release
		}
		return make([]SendBatchItemResult, len(items))
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer orderedClose(t, s)
	var once sync.Once
	defer once.Do(func() { close(release) })
	first := orderedItem("g", "first")
	first.Command.SyncOnce = true
	for _, item := range []SendBatchItem{first, orderedItem("g_cmd", "second"), orderedItem("g", "ordinary")} {
		if err := s.Submit([]SendBatchItem{item}, func([]SendBatchItemResult) { completed <- struct{}{} }); err != nil {
			t.Fatal(err)
		}
		if item.Command.ClientMsgNo == "first" {
			if got := orderedWait(t, entered); got != "first" {
				t.Fatal(got)
			}
		}
	}
	if got := orderedWait(t, entered); got != "ordinary" {
		t.Fatalf("command alias overtook: %s", got)
	}
	once.Do(func() { close(release) })
	if got := orderedWait(t, entered); got != "second" {
		t.Fatal(got)
	}
	for range 3 {
		orderedWait(t, completed)
	}
}

func TestOrderedSubmitterPoolOwnershipAndRetiredPressure(t *testing.T) {
	registry := goruntimeregistry.New()
	inCallback := make(chan struct{})
	release := make(chan struct{})
	s, err := NewOrderedSubmitter(OrderedSubmitterOptions{Workers: 1, Capacity: 3, PayloadCapacity: 3, Goroutines: registry}, orderedSenderFunc(func(items []SendBatchItem) []SendBatchItemResult { return make([]SendBatchItemResult, len(items)) }))
	if err != nil {
		t.Fatal(err)
	}
	defer orderedClose(t, s)
	var once sync.Once
	defer once.Do(func() { close(release) })
	if err := s.Submit([]SendBatchItem{orderedItem("a", "1")}, func([]SendBatchItemResult) { close(inCallback); <-release }); err != nil {
		t.Fatal(err)
	}
	orderedWait(t, inCallback)
	if err := s.Submit([]SendBatchItem{orderedItem("a", "2")}, func([]SendBatchItemResult) {}); err != nil {
		t.Fatal(err)
	}
	if err := s.Submit([]SendBatchItem{orderedItem("b", "3"), orderedItem("c", "4")}, func([]SendBatchItemResult) {}); !errors.Is(err, ErrBackpressured) {
		t.Fatal(err)
	}
	snapshot := func() goruntimeregistry.TaskSnapshot {
		for _, module := range registry.Snapshot().Modules {
			for _, task := range module.Tasks {
				if task.Task == goruntimeregistry.TaskChannelAppendWorkerPool {
					return task
				}
			}
		}
		return goruntimeregistry.TaskSnapshot{}
	}
	task := snapshot()
	if task.Active != 1 || task.BusyTasks != 1 || task.PoolCapacity != 1 || task.QueueDepth != 1 || task.QueueCapacity != 3 || task.RejectedTotal != 1 {
		t.Fatalf("pool ownership: %+v", task)
	}
	once.Do(func() { close(release) })
	orderedClose(t, s)
	task = snapshot()
	if task.BusyTasks != 0 || task.PoolCapacity != 0 || task.QueueDepth != 0 || task.QueueCapacity != 0 || task.RejectedTotal != 1 {
		t.Fatalf("retired pool: %+v", task)
	}
}

func TestOrderedSubmitterMaintenanceWaitsAndResumesSameOwner(t *testing.T) {
	entered, release := make(chan struct{}, 1), make(chan struct{})
	s, err := NewOrderedSubmitter(OrderedSubmitterOptions{Workers: 1, Capacity: 2, PayloadCapacity: 2}, orderedSenderFunc(func(items []SendBatchItem) []SendBatchItemResult { return make([]SendBatchItemResult, len(items)) }))
	if err != nil {
		t.Fatal(err)
	}
	defer orderedClose(t, s)
	if err := s.Submit([]SendBatchItem{orderedItem("a", "1")}, func([]SendBatchItemResult) { entered <- struct{}{}; <-release }); err != nil {
		t.Fatal(err)
	}
	orderedWait(t, entered)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Pause(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := s.Submit([]SendBatchItem{orderedItem("b", "2")}, func([]SendBatchItemResult) { t.Error("paused work admitted") }); !errors.Is(err, ErrRouteNotReady) {
		t.Fatal(err)
	}
	if err := s.Resume(); err == nil {
		t.Fatal("resumed incomplete maintenance drain")
	}
	close(release)
	waitCtx, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	if err := s.Pause(waitCtx); err != nil {
		t.Fatal(err)
	}
	if err := s.Resume(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{}, 1)
	if err := s.Submit([]SendBatchItem{orderedItem("b", "2")}, func([]SendBatchItemResult) { done <- struct{}{} }); err != nil {
		t.Fatal(err)
	}
	orderedWait(t, done)
	orderedClose(t, s)
	if err := s.Resume(); !errors.Is(err, ErrRouteNotReady) {
		t.Fatal("terminal close reopened")
	}
}
