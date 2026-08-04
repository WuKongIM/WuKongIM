package multiraft

import (
	"context"
	"errors"
	"sync"
	"testing"
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
	future.resolve(want, nil)
	future.resolve(Result{}, errors.New("duplicate"))
	if observer.calls != 1 || observer.result.Index != want.Index || observer.result.Term != want.Term || string(observer.result.Data) != string(want.Data) || observer.err != nil {
		t.Fatalf("observer = calls:%d result:%#v err:%v, want one %#v nil", observer.calls, observer.result, observer.err, want)
	}
}

func TestFutureCompletionObserverRegisteredAfterResolutionRunsOnce(t *testing.T) {
	future := newFuture(nil)
	wantErr := errors.New("apply failed")
	future.resolve(Result{Index: 9}, wantErr)
	observer := &recordingFutureCompletionObserver{}

	if !future.ObserveCompletion(observer) {
		t.Fatal("ObserveCompletion() = false, want registered")
	}
	if observer.calls != 1 || observer.result.Index != 9 || !errors.Is(observer.err, wantErr) {
		t.Fatalf("observer = calls:%d result:%#v err:%v, want one index 9 apply failure", observer.calls, observer.result, observer.err)
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
			future.resolve(Result{Index: index}, nil)
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
