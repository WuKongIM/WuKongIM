package reactor

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/machine"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/stretchr/testify/require"
)

func TestMailboxDrainsHighBeforeNormal(t *testing.T) {
	mailbox := NewMailbox(MailboxConfig{HighSize: 2, NormalSize: 2, LowSize: 2})
	require.NoError(t, mailbox.Submit(PriorityNormal, Event{Kind: EventAppend, Key: ch.ChannelKey("normal")}))
	require.NoError(t, mailbox.Submit(PriorityHigh, Event{Kind: EventApplyMeta, Key: ch.ChannelKey("high")}))
	events := mailbox.Drain(2)
	require.Equal(t, ch.ChannelKey("high"), events[0].Key)
	require.Equal(t, ch.ChannelKey("normal"), events[1].Key)
}

func TestMailboxNormalBackpressure(t *testing.T) {
	mailbox := NewMailbox(MailboxConfig{HighSize: 1, NormalSize: 1, LowSize: 1})
	require.NoError(t, mailbox.Submit(PriorityNormal, Event{Kind: EventAppend, Key: ch.ChannelKey("a")}))
	err := mailbox.Submit(PriorityNormal, Event{Kind: EventAppend, Key: ch.ChannelKey("b")})
	require.ErrorIs(t, err, ch.ErrBackpressured)
}

func TestMailboxDrainIntoReusesProvidedBuffer(t *testing.T) {
	mailbox := NewMailbox(MailboxConfig{HighSize: 2, NormalSize: 2, LowSize: 2})
	backing := make([]Event, 2)
	buf := backing[:0]

	require.NoError(t, mailbox.Submit(PriorityHigh, Event{Kind: EventApplyMeta, Key: ch.ChannelKey("high")}))
	require.NoError(t, mailbox.Submit(PriorityNormal, Event{Kind: EventAppend, Key: ch.ChannelKey("normal")}))

	events := mailbox.DrainInto(buf, 2)
	require.Len(t, events, 2)
	require.Equal(t, &backing[0], &events[0])
	require.Equal(t, ch.ChannelKey("high"), events[0].Key)
	require.Equal(t, ch.ChannelKey("normal"), events[1].Key)
}

func TestMailboxBoundsConsecutiveHighBeforeNormal(t *testing.T) {
	mailbox := NewMailbox(MailboxConfig{HighSize: maxConsecutiveHigh + 1, NormalSize: 1, LowSize: 1})
	for index := 0; index < maxConsecutiveHigh+1; index++ {
		require.NoError(t, mailbox.Submit(PriorityHigh, Event{Kind: EventWorkerResult, Key: ch.ChannelKey("high-" + strconv.Itoa(index))}))
	}
	require.NoError(t, mailbox.Submit(PriorityNormal, Event{Kind: EventAppend, Key: ch.ChannelKey("normal")}))

	events := mailbox.Drain(maxConsecutiveHigh + 1)
	require.Len(t, events, maxConsecutiveHigh+1)
	require.Equal(t, ch.ChannelKey("normal"), events[maxConsecutiveHigh].Key)
}

func TestMailboxFairnessCarriesAcrossSingleItemDrains(t *testing.T) {
	mailbox := NewMailbox(MailboxConfig{HighSize: maxConsecutiveHigh + 1, NormalSize: 1, LowSize: 1})
	for index := 0; index < maxConsecutiveHigh+1; index++ {
		require.NoError(t, mailbox.Submit(PriorityHigh, Event{Kind: EventWorkerResult, Key: ch.ChannelKey("high-" + strconv.Itoa(index))}))
	}
	require.NoError(t, mailbox.Submit(PriorityNormal, Event{Kind: EventAppend, Key: ch.ChannelKey("normal")}))

	for index := 0; index < maxConsecutiveHigh; index++ {
		events := mailbox.Drain(1)
		require.Len(t, events, 1)
		require.NotEqual(t, ch.ChannelKey("normal"), events[0].Key)
	}
	events := mailbox.Drain(1)
	require.Len(t, events, 1)
	require.Equal(t, ch.ChannelKey("normal"), events[0].Key)
}

func TestMailboxWaitOneWakesOnSubmit(t *testing.T) {
	mailbox := NewMailbox(MailboxConfig{HighSize: 1, NormalSize: 1, LowSize: 1})
	stop := make(chan struct{})
	got := make(chan Event, 1)

	go func() {
		event, ok := mailbox.WaitOne(stop, nil)
		if ok {
			got <- event
		}
	}()

	require.NoError(t, mailbox.Submit(PriorityNormal, Event{Kind: EventAppend, Key: ch.ChannelKey("wake")}))
	select {
	case event := <-got:
		require.Equal(t, ch.ChannelKey("wake"), event.Key)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("mailbox wait did not wake after submit")
	}
	close(stop)
}

func TestReactorReportsMailboxCapacityAdmissionAndDepth(t *testing.T) {
	obs := &recordingMailboxPressureObserver{}
	r := NewReactor(ReactorConfig{ID: 3, LocalNode: 1, Store: store.NewMemoryFactory(), MailboxSize: 1, Observer: obs})

	err := r.Submit(PriorityNormal, Event{Kind: EventTick})
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	err = r.Submit(PriorityNormal, Event{Kind: EventTick})
	if !errors.Is(err, ch.ErrBackpressured) {
		t.Fatalf("Submit() error = %v, want %v", err, ch.ErrBackpressured)
	}
	err = r.Submit(PriorityLow, Event{Kind: EventTick})
	if err != nil {
		t.Fatalf("low priority fill Submit() error = %v", err)
	}
	coalescedFuture := NewFuture()
	err = r.Submit(PriorityLow, Event{Kind: EventTick, Future: coalescedFuture})
	if err != nil {
		t.Fatalf("low priority Submit() error = %v", err)
	}
	err = r.SubmitCompletion(Event{Kind: EventWorkerResult})
	if err != nil {
		t.Fatalf("SubmitCompletion() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err = coalescedFuture.Await(ctx)
	require.NoError(t, err)

	if got := obs.capacity["3:normal"]; got != 1 {
		t.Fatalf("normal capacity = %d, want 1", got)
	}
	if got := obs.depth["3:high"]; got != 1 {
		t.Fatalf("high depth = %d, want 1", got)
	}
	if got := obs.admission["3:normal:ok"]; got != 1 {
		t.Fatalf("normal ok admission = %d, want 1", got)
	}
	if got := obs.admission["3:normal:full"]; got != 1 {
		t.Fatalf("normal full admission = %d, want 1", got)
	}
	if got := obs.admission["3:low:coalesced"]; got != 1 {
		t.Fatalf("low coalesced admission = %d, want 1", got)
	}
	if got := obs.admission["3:high:ok"]; got != 1 {
		t.Fatalf("high ok admission = %d, want 1", got)
	}
}

func TestLeaderEvictReadyReportsMailboxAdmissionAndDepth(t *testing.T) {
	obs := &recordingMailboxPressureObserver{}
	meta := testMeta("leader-evict-ready-admission", 1, 1)
	r := NewReactor(ReactorConfig{ID: 4, LocalNode: 1, Store: store.NewMemoryFactory(), MailboxSize: 1, Observer: obs})
	state := machine.NewChannelState(meta.Key, 1, 1)
	state.ID = meta.ID
	rc := &runtimeChannel{state: state}

	r.submitLeaderEvictReady(rc, time.Now(), 12)

	if got := obs.admission["4:normal:ok"]; got != 1 {
		t.Fatalf("normal ok admission = %d, want 1", got)
	}
	if got := obs.depth["4:normal"]; got != 1 {
		t.Fatalf("normal depth = %d, want 1", got)
	}
}

func TestDefaultObserverDoesNotImplementMailboxPressureObserver(t *testing.T) {
	_, ok := defaultObserver(nil).(MailboxPressureObserver)
	require.False(t, ok)
}

type recordingMailboxPressureObserver struct {
	captureObserver
	mu        sync.Mutex
	capacity  map[string]int
	depth     map[string]int
	admission map[string]int
}

func (o *recordingMailboxPressureObserver) ensure() {
	if o.capacity == nil {
		o.capacity = make(map[string]int)
		o.depth = make(map[string]int)
		o.admission = make(map[string]int)
	}
}

func (o *recordingMailboxPressureObserver) SetReactorMailboxDepth(reactorID int, priority string, depth int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.ensure()
	o.depth[strconv.Itoa(reactorID)+":"+priority] = depth
}

func (o *recordingMailboxPressureObserver) SetReactorMailboxCapacity(reactorID int, priority string, capacity int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.ensure()
	o.capacity[strconv.Itoa(reactorID)+":"+priority] = capacity
}

func (o *recordingMailboxPressureObserver) ObserveReactorMailboxAdmission(reactorID int, priority string, result string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.ensure()
	o.admission[strconv.Itoa(reactorID)+":"+priority+":"+result]++
}
