package multiraft

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestFutureCompletionObserverFollowsRuntimeResolutionAfterCanceledWait(t *testing.T) {
	future := newFuture(nil)
	observer := &recordingFutureCompletionObserver{}
	if !future.ObserveCompletion(observer) {
		t.Fatal("ObserveCompletion() = false, want registered")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := future.Wait(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait() error = %v, want context canceled", err)
	}
	if observer.calls != 0 {
		t.Fatalf("observer calls at cancellation = %d, want 0", observer.calls)
	}

	want := Result{Index: 7, Term: 3, Data: []byte("created")}
	future.resolveAndDispatch(want, nil)
	future.resolveAndDispatch(Result{}, errors.New("duplicate"))
	if future.completionObserver != nil {
		t.Fatal("resolved future retained completion observer")
	}
	if observer.calls != 1 || observer.result.Index != want.Index || observer.result.Term != want.Term || string(observer.result.Data) != string(want.Data) || observer.err != nil {
		t.Fatalf("observer = calls:%d result:%#v err:%v, want one %#v nil", observer.calls, observer.result, observer.err, want)
	}
}

func TestFutureCompletionObserverRegisteredAfterResolutionRunsOnce(t *testing.T) {
	future := newFuture(nil)
	wantErr := errors.New("apply failed")
	future.resolveAndDispatch(Result{Index: 9}, wantErr)
	observer := &recordingFutureCompletionObserver{}

	if !future.ObserveCompletion(observer) {
		t.Fatal("ObserveCompletion() = false, want registered")
	}
	if observer.calls != 1 || observer.result.Index != 9 || !errors.Is(observer.err, wantErr) {
		t.Fatalf("observer = calls:%d result:%#v err:%v, want one index 9 apply failure", observer.calls, observer.result, observer.err)
	}
	if future.completionObserver != nil {
		t.Fatal("late registration retained completion observer on resolved future")
	}
	if future.ObserveCompletion(&recordingFutureCompletionObserver{}) {
		t.Fatal("second ObserveCompletion() = true, want bounded single observer")
	}
}

func TestFutureCompletionObserverRegistrationRacesResolution(t *testing.T) {
	for i := 0; i < 200; i++ {
		future := newFuture(nil)
		observer := &recordingFutureCompletionObserver{}
		start := make(chan struct{})
		registered := make(chan bool, 1)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			registered <- future.ObserveCompletion(observer)
		}()
		go func(index uint64) {
			defer wg.Done()
			<-start
			future.resolveAndDispatch(Result{Index: index}, nil)
		}(uint64(i + 1))
		close(start)
		wg.Wait()

		if !<-registered {
			t.Fatalf("iteration %d ObserveCompletion() = false, want registered", i)
		}
		if observer.calls != 1 || observer.result.Index != uint64(i+1) || observer.err != nil {
			t.Fatalf("iteration %d observer = calls:%d result:%#v err:%v, want one matching result", i, observer.calls, observer.result, observer.err)
		}
	}
}

func TestLateFutureCompletionObserverAlwaysStartsAfterWaitIsReady(t *testing.T) {
	for i := 0; i < 10000; i++ {
		future := newFuture(nil)
		completion := future.resolve(Result{Index: uint64(i + 1)}, nil)
		observer := &waitReadyFutureCompletionObserver{future: future}
		start := make(chan struct{})
		registered := make(chan bool, 1)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			completion.dispatch()
		}()
		go func() {
			defer wg.Done()
			<-start
			registered <- future.ObserveCompletion(observer)
		}()
		close(start)
		wg.Wait()

		if !<-registered {
			t.Fatalf("iteration %d ObserveCompletion() = false, want registered", i)
		}
		if !observer.waitReady {
			t.Fatalf("iteration %d completion observer started before Future.Wait was ready", i)
		}
	}
}

func TestFutureCompletionObserverRegisteredBeforeDispatchWaitsForDispatchSafety(t *testing.T) {
	runtime := &Runtime{slots: make(map[SlotID]*slot)}
	future := newFuture(nil)
	runtime.mu.Lock()
	completion := future.resolve(Result{Index: 11}, nil)
	observer := &gatedReentrantRuntimeStatusObserver{
		runtime: runtime,
		slotID:  91,
		entered: make(chan struct{}, 2),
		result:  make(chan error, 2),
	}
	registered := make(chan bool, 1)
	go func() {
		registered <- future.ObserveCompletion(observer)
	}()

	select {
	case ok := <-registered:
		if !ok {
			runtime.mu.Unlock()
			completion.dispatch()
			t.Fatal("ObserveCompletion() = false, want registered")
		}
	case <-observer.entered:
		runtime.mu.Unlock()
		completion.dispatch()
		<-registered
		t.Fatal("completion observer entered before outer lock release and dispatch")
	case <-time.After(250 * time.Millisecond):
		runtime.mu.Unlock()
		completion.dispatch()
		t.Fatal("ObserveCompletion() did not register during terminal pre-dispatch state")
	}
	select {
	case <-observer.entered:
		runtime.mu.Unlock()
		completion.dispatch()
		t.Fatal("completion observer entered before dispatch")
	default:
	}

	runtime.mu.Unlock()
	completion.dispatch()
	select {
	case err := <-observer.result:
		if !errors.Is(err, ErrSlotNotFound) {
			t.Fatalf("observer Status() error = %v, want slot not found", err)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("completion observer did not re-enter Runtime.Status after dispatch")
	}
	if observer.calls != 1 {
		t.Fatalf("observer calls = %d, want exactly one", observer.calls)
	}
	result, err := future.Wait(context.Background())
	if err != nil || result.Index != 11 {
		t.Fatalf("Wait() = (%#v, %v), want terminal index 11", result, err)
	}
}

func TestRuntimeCloseDispatchesFutureCompletionObserverAfterUnlock(t *testing.T) {
	runtime, err := New(Options{
		NodeID:       1,
		TickInterval: time.Hour,
		Workers:      1,
		Transport:    &internalFakeTransport{},
		Raft: RaftOptions{
			ElectionTick:  10,
			HeartbeatTick: 1,
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	const slotID SlotID = 91
	if err := runtime.OpenSlot(context.Background(), newInternalSlotOptions(slotID)); err != nil {
		t.Fatalf("OpenSlot() error = %v", err)
	}

	future := newFuture(nil)
	observer := &reentrantRuntimeStatusObserver{
		runtime: runtime,
		slotID:  slotID,
		result:  make(chan error, 1),
	}
	if !future.ObserveCompletion(observer) {
		t.Fatal("ObserveCompletion() = false, want registered")
	}
	runtime.mu.RLock()
	slot := runtime.slots[slotID]
	runtime.mu.RUnlock()
	slot.mu.Lock()
	slot.submittedProposals = append(slot.submittedProposals, future)
	slot.mu.Unlock()

	closed := make(chan error, 1)
	go func() {
		closed <- runtime.Close()
	}()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("Close() deadlocked while completion observer re-entered Runtime.Status")
	}
	select {
	case err := <-observer.result:
		if !errors.Is(err, ErrRuntimeClosed) {
			t.Fatalf("observer Status() error = %v, want runtime closed", err)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("completion observer did not run")
	}
}

type recordingFutureCompletionObserver struct {
	calls  int
	result Result
	err    error
}

func (o *recordingFutureCompletionObserver) ObserveFutureCompletion(result Result, err error) {
	o.calls++
	o.result = result
	o.err = err
}

type reentrantRuntimeStatusObserver struct {
	runtime *Runtime
	slotID  SlotID
	result  chan error
}

func (o *reentrantRuntimeStatusObserver) ObserveFutureCompletion(Result, error) {
	_, err := o.runtime.Status(o.slotID)
	o.result <- err
}

type gatedReentrantRuntimeStatusObserver struct {
	runtime *Runtime
	slotID  SlotID
	entered chan struct{}
	result  chan error
	calls   int
}

func (o *gatedReentrantRuntimeStatusObserver) ObserveFutureCompletion(Result, error) {
	o.calls++
	o.entered <- struct{}{}
	_, err := o.runtime.Status(o.slotID)
	o.result <- err
}

type waitReadyFutureCompletionObserver struct {
	future    *future
	waitReady bool
}

func (o *waitReadyFutureCompletionObserver) ObserveFutureCompletion(Result, error) {
	select {
	case <-o.future.done:
		o.waitReady = true
	default:
	}
}
