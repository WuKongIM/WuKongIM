package channelappend

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestActiveAppendItemsPartitionsEveryOriginalExactlyOnce(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, test := range []struct {
		name         string
		inactive     []bool
		wantActive   []int
		wantInactive []int
	}{
		{name: "empty"},
		{name: "all_active", inactive: []bool{false, false, false}, wantActive: []int{0, 1, 2}},
		{name: "one_inactive", inactive: []bool{true}, wantInactive: []int{0}},
		{name: "first_inactive", inactive: []bool{true, false}, wantActive: []int{1}, wantInactive: []int{0}},
		{name: "two_leading_inactive", inactive: []bool{true, true, false}, wantActive: []int{2}, wantInactive: []int{0, 1}},
		{name: "two_all_inactive", inactive: []bool{true, true}, wantInactive: []int{0, 1}},
		{name: "three_all_inactive", inactive: []bool{true, true, true}, wantInactive: []int{0, 1, 2}},
		{name: "long_inactive_prefix", inactive: []bool{true, true, true, false}, wantActive: []int{3}, wantInactive: []int{0, 1, 2}},
		{name: "nonleading_inactive", inactive: []bool{false, true, true}, wantActive: []int{0}, wantInactive: []int{1, 2}},
		{name: "alternating_first_inactive", inactive: []bool{true, false, true, false}, wantActive: []int{1, 3}, wantInactive: []int{0, 2}},
		{name: "alternating_first_active", inactive: []bool{false, true, false, true}, wantActive: []int{0, 2}, wantInactive: []int{1, 3}},
	} {
		t.Run(test.name, func(t *testing.T) {
			items := make([]preparedSend, len(test.inactive))
			for i, inactive := range test.inactive {
				items[i] = preparedSend{Index: i, Context: context.Background()}
				if inactive {
					items[i].Context = canceled
				}
			}
			active, inactive := activeAppendItems(items)
			var activeIndices, inactiveIndices []int
			seen := make(map[int]int, len(items))
			for _, item := range active {
				activeIndices = append(activeIndices, item.Index)
				seen[item.Index]++
			}
			for _, completion := range inactive {
				inactiveIndices = append(inactiveIndices, completion.item.Index)
				seen[completion.item.Index]++
				if !errors.Is(completion.result.Err, context.Canceled) || !errors.Is(completion.traceErr, context.Canceled) {
					t.Errorf("inactive error = %v/%v, want canceled", completion.result.Err, completion.traceErr)
				}
			}
			if !reflect.DeepEqual(activeIndices, test.wantActive) || !reflect.DeepEqual(inactiveIndices, test.wantInactive) {
				t.Errorf("partition = %v / %v, want %v / %v", activeIndices, inactiveIndices, test.wantActive, test.wantInactive)
			}
			if len(active)+len(inactive) != len(items) {
				t.Errorf("partition cardinality = %d, want %d", len(active)+len(inactive), len(items))
			}
			for i := range items {
				if seen[i] != 1 {
					t.Errorf("original index %d appears %d times, want once", i, seen[i])
				}
				if items[i].Index != i {
					t.Errorf("input index %d mutated", i)
				}
			}
			if len(inactive) == 0 && len(items) > 0 && &active[0] != &items[0] {
				t.Error("all-active partition did not borrow original slice")
			}
		})
	}
}

func TestAppendEffectInactivePrefixPreservesCompletionCardinality(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, test := range []struct {
		name      string
		count     int
		validLast bool
	}{
		{"all_inactive", 2, false},
		{"two_inactive_then_active", 3, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			appender := newRecordingAppenderForAppendTest()
			items := make([]preparedSend, test.count)
			for i := range items {
				items[i] = preparedSend{Index: i, Context: canceled, Command: SendCommand{MessageID: uint64(i + 1), FromUID: "sender", ClientMsgNo: string(rune('a' + i)), Payload: []byte("payload")}}
			}
			items[1].Context = context.Background()
			items[1].Deadline = time.Unix(1, 0)
			if test.validLast {
				items[len(items)-1].Context = context.Background()
			}
			effect := appendEffect{target: localTargetForAppendTest("filtered"), key: "2:filtered", seq: 0, items: items}
			completion := effect.run(context.Background(), appendPorts{appender: appender})
			if len(completion.items) != len(items) {
				t.Errorf("completion cardinality = %d, want %d", len(completion.items), len(items))
			}
			seen := make(map[int]int, len(items))
			for _, item := range completion.items {
				seen[item.item.Index]++
			}
			for i := range items {
				if seen[i] != 1 {
					t.Errorf("original %d completed %d times, want once", i, seen[i])
				}
			}
			requests := appender.Requests()
			if !test.validLast {
				if len(requests) != 0 {
					t.Errorf("all inactive batch made %d storage calls", len(requests))
				}
			} else if len(requests) != 1 || len(requests[0].Messages) != 1 || requests[0].Messages[0].MessageID != uint64(len(items)) {
				t.Errorf("storage requests = %+v, want only final valid item", requests)
			}
		})
	}
}

// appendClaimObserverForFilterTest provides a deterministic pre-effect barrier.
// The held one-worker pool guarantees that publication cannot follow execution.
type appendClaimObserverForFilterTest struct {
	items   int
	claimed chan struct{}
	once    sync.Once
}

func (*appendClaimObserverForFilterTest) AppendFinished(string, error, time.Duration) {}
func (o *appendClaimObserverForFilterTest) SetChannelAppendWriterPressure(event WriterPressureObservation) {
	if event.AppendInflightItems == o.items {
		o.once.Do(func() { close(o.claimed) })
	}
}

func TestGroupCanceledAppendPrefixPreservesCompletionOwnership(t *testing.T) {
	for _, test := range []struct {
		name      string
		count     int
		validLast bool
	}{
		{"all_inactive", 2, false},
		{"two_inactive_then_active", 3, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			observer := &appendClaimObserverForFilterTest{items: test.count, claimed: make(chan struct{})}
			appender := newRecordingAppenderForAppendTest()
			persist := &recordingPersistAfterEnqueuerForCommitTest{}
			group := newStartedTestGroup(t, Options{LocalNodeID: 1, MessageID: newSequenceIDsForPrepare(10), Appender: appender, PersistAfterEnqueuer: persist, EffectPoolSize: 1, Observer: observer, InboxCoalesceWindow: -time.Nanosecond})
			poolEntered, releasePool := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(releasePool) }) }
			t.Cleanup(release)
			if err := group.appendPool.submit(func() { close(poolEntered); <-releasePool }); err != nil {
				t.Fatal(err)
			}
			<-poolEntered
			canceled, cancel := context.WithCancel(context.Background())
			defer cancel()
			items := make([]SendBatchItem, test.count)
			for i := range items {
				items[i] = appendSendItemForTest("sender", "filtered", string(rune('a'+i)))
				items[i].Context = canceled
			}
			if test.validLast {
				items[len(items)-1].Context = context.Background()
			}
			future, err := group.SubmitLocal(context.Background(), localTargetForAppendTest("filtered"), items)
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-observer.claimed:
			case <-time.After(time.Second):
				t.Fatal("accepted batch never claimed append ownership")
			}
			future.mu.Lock()
			originalOnDone := future.onDone
			future.mu.Unlock()
			var doneCalls atomic.Int64
			future.setOnDone(func() { doneCalls.Add(1); originalOnDone() })
			cancel()
			release()
			results := waitFutureForTest(t, future)
			for i, result := range results {
				if test.validLast && i == len(results)-1 {
					if result.Err != nil || result.Result.Reason != ReasonSuccess {
						t.Errorf("active result %d = %+v", i, result)
					}
				} else if !errors.Is(result.Err, context.Canceled) {
					t.Errorf("inactive result %d = %+v, want canceled", i, result)
				}
			}
			drainCtx, cancelDrain := context.WithTimeout(context.Background(), time.Second)
			defer cancelDrain()
			if err := group.WaitIdle(drainCtx); err != nil {
				t.Fatal(err)
			}
			if got := group.metrics.appendInflightItems.Load(); got != 0 {
				t.Errorf("aggregate append inflight = %d, want exact zero", got)
			}
			writer := group.writerForTest(localTargetForAppendTest("filtered").ChannelID)
			writer.mu.Lock()
			stateInflight, stateItems := writer.state.appendInflight, writer.state.appendInflightItems
			writer.mu.Unlock()
			if stateInflight != 0 || stateItems != 0 {
				t.Errorf("writer inflight = %d/%d, want zero", stateInflight, stateItems)
			}
			if got := doneCalls.Load(); got != 1 {
				t.Errorf("Future callback count = %d, want once", got)
			}
			if got := group.handoff.depth(); got != 0 {
				t.Errorf("handoff depth = %d, want zero", got)
			}
			if got := group.metrics.admissionDepth(); got != 0 {
				t.Errorf("admission depth = %d, want zero", got)
			}
			want := 0
			if test.validLast {
				want = 1
			}
			if got := persist.callCount(); got != want {
				t.Errorf("post-commit count = %d, want %d", got, want)
			}
			requests := appender.Requests()
			if len(requests) != want {
				t.Errorf("Appender calls = %d, want %d", len(requests), want)
			}
			if test.validLast && len(requests) == 1 && (len(requests[0].Messages) != 1 || requests[0].Messages[0].ClientMsgNo != items[len(items)-1].Command.ClientMsgNo) {
				t.Errorf("Appender request = %+v, want only final valid item", requests[0])
			}
		})
	}
}
