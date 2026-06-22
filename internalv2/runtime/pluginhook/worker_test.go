package pluginhook

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/pluginevents"
	"github.com/stretchr/testify/require"
)

func TestWorkerEnqueueDetachesCallerCancellation(t *testing.T) {
	usecase := &recordingPersistAfterUsecase{done: make(chan struct{})}
	worker := NewWorker(Options{Usecase: usecase, QueueSize: 1, Workers: 1, Timeout: time.Second})
	require.NoError(t, worker.Start(context.Background()))
	defer worker.Stop(context.Background())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	worker.EnqueuePersistAfter(ctx, pluginevents.PersistAfterCommitted{MessageID: 1, Payload: []byte("hello")})

	select {
	case <-usecase.done:
	case <-time.After(time.Second):
		t.Fatal("PersistAfter was not invoked")
	}
}

func TestWorkerStartCallerContextCancellationDoesNotStopRun(t *testing.T) {
	usecase := &recordingPersistAfterUsecase{done: make(chan struct{})}
	worker := NewWorker(Options{Usecase: usecase, QueueSize: 1, Workers: 1, Timeout: time.Second})

	startCtx, cancel := context.WithCancel(context.Background())
	require.NoError(t, worker.Start(startCtx))
	cancel()
	defer worker.Stop(context.Background())

	worker.EnqueuePersistAfter(context.Background(), pluginevents.PersistAfterCommitted{MessageID: 1})
	select {
	case <-usecase.done:
	case <-time.After(time.Second):
		t.Fatal("PersistAfter was not invoked after start caller context cancellation")
	}
}

func TestWorkerStartRequiresUsecase(t *testing.T) {
	worker := NewWorker(Options{})
	require.Error(t, worker.Start(context.Background()))
}

func TestWorkerEnqueueClonesPayload(t *testing.T) {
	usecase := &recordingPersistAfterUsecase{done: make(chan struct{})}
	worker := NewWorker(Options{Usecase: usecase, QueueSize: 1, Workers: 1, Timeout: time.Second})
	require.NoError(t, worker.Start(context.Background()))
	defer worker.Stop(context.Background())

	payload := []byte("hello")
	scopedUIDs := []string{"u1", "u2"}
	worker.EnqueuePersistAfter(context.Background(), pluginevents.PersistAfterCommitted{
		MessageID:         1,
		Payload:           payload,
		MessageScopedUIDs: scopedUIDs,
	})
	payload[0] = 'x'
	scopedUIDs[0] = "changed"

	select {
	case <-usecase.done:
	case <-time.After(time.Second):
		t.Fatal("PersistAfter was not invoked")
	}

	usecase.mu.Lock()
	defer usecase.mu.Unlock()
	require.Len(t, usecase.calls, 1)
	require.Equal(t, []byte("hello"), usecase.calls[0].Payload)
	require.Equal(t, []string{"u1", "u2"}, usecase.calls[0].MessageScopedUIDs)
}

func TestWorkerEnqueueReceiveInvokesReceiveUsecaseAndClonesPayload(t *testing.T) {
	usecase := &recordingHookUsecase{receiveDone: make(chan struct{})}
	observer := &recordingObserver{}
	worker := NewWorker(Options{Usecase: usecase, QueueSize: 1, Workers: 1, Timeout: time.Second, Observer: observer})
	require.NoError(t, worker.Start(context.Background()))
	defer worker.Stop(context.Background())

	payload := []byte("hello")
	scopedUIDs := []string{"u2"}
	worker.EnqueueReceive(context.Background(), pluginevents.ReceiveOffline{
		MessageID:         1,
		UID:               "u2",
		Payload:           payload,
		MessageScopedUIDs: scopedUIDs,
	})
	payload[0] = 'x'
	scopedUIDs[0] = "changed"

	select {
	case <-usecase.receiveDone:
	case <-time.After(time.Second):
		t.Fatal("Receive was not invoked")
	}

	usecase.mu.Lock()
	defer usecase.mu.Unlock()
	require.Len(t, usecase.receiveCalls, 1)
	require.Equal(t, []byte("hello"), usecase.receiveCalls[0].Payload)
	require.Equal(t, []string{"u2"}, usecase.receiveCalls[0].MessageScopedUIDs)
	require.Eventually(t, func() bool { return observer.receiveEnqueueCount(enqueueResultAccepted) == 1 }, time.Second, time.Millisecond)
	require.Eventually(t, func() bool { return observer.receiveInvokeCount(invokeResultOK) == 1 }, time.Second, time.Millisecond)
}

func TestWorkerQueueFullDropsWithoutBlockingCaller(t *testing.T) {
	usecase := newBlockingPersistAfterUsecase()
	observer := &recordingObserver{}
	worker := NewWorker(Options{Usecase: usecase, QueueSize: 1, Workers: 1, Timeout: time.Second, Observer: observer})
	require.NoError(t, worker.Start(context.Background()))
	defer worker.Stop(context.Background())

	worker.EnqueuePersistAfter(context.Background(), pluginevents.PersistAfterCommitted{MessageID: 1})
	usecase.waitStarted(t)
	worker.EnqueuePersistAfter(context.Background(), pluginevents.PersistAfterCommitted{MessageID: 2})
	worker.EnqueuePersistAfter(context.Background(), pluginevents.PersistAfterCommitted{MessageID: 3})

	require.Eventually(t, func() bool { return observer.enqueueCount(enqueueResultFull) > 0 }, time.Second, time.Millisecond)
	usecase.release()
}

func TestWorkerSlowEnqueueAcceptsMultipleWaitersAfterCapacityFrees(t *testing.T) {
	usecase := newTwoPhaseBlockingPersistAfterUsecase()
	observer := &recordingObserver{}
	worker := NewWorker(Options{Usecase: usecase, QueueSize: 2, Workers: 2, Timeout: time.Second, Observer: observer})
	require.NoError(t, worker.Start(context.Background()))
	defer worker.Stop(context.Background())

	for i := 1; i <= 4; i++ {
		worker.EnqueuePersistAfter(context.Background(), pluginevents.PersistAfterCommitted{MessageID: uint64(i)})
	}
	usecase.waitStartedCalls(t, 2)
	require.Eventually(t, func() bool { return observer.enqueueCount(enqueueResultAccepted) >= 4 }, time.Second, time.Millisecond)

	waiterDone := make(chan struct{}, 2)
	for i := 5; i <= 6; i++ {
		go func(id uint64) {
			worker.EnqueuePersistAfter(context.Background(), pluginevents.PersistAfterCommitted{MessageID: id})
			waiterDone <- struct{}{}
		}(uint64(i))
	}

	usecase.releasePhaseOne()
	for i := 0; i < 2; i++ {
		select {
		case <-waiterDone:
		case <-time.After(time.Second):
			t.Fatal("waiting enqueue did not complete after capacity freed")
		}
	}

	require.Equal(t, 0, observer.enqueueCount(enqueueResultFull))
	require.Eventually(t, func() bool { return observer.enqueueCount(enqueueResultAccepted) >= 6 }, time.Second, time.Millisecond)
	usecase.releasePhaseTwo()
}

func TestWorkerEnqueueAfterStopObservesClosedWithoutInvoke(t *testing.T) {
	usecase := &recordingPersistAfterUsecase{}
	observer := &recordingObserver{}
	worker := NewWorker(Options{Usecase: usecase, QueueSize: 1, Workers: 1, Timeout: time.Second, Observer: observer})
	require.NoError(t, worker.Start(context.Background()))
	require.NoError(t, worker.Stop(context.Background()))

	worker.EnqueuePersistAfter(context.Background(), pluginevents.PersistAfterCommitted{MessageID: 1})

	require.Eventually(t, func() bool { return observer.enqueueCount(enqueueResultClosed) == 1 }, time.Second, time.Millisecond)
	require.Never(t, func() bool { return usecase.callCount() > 0 }, 50*time.Millisecond, time.Millisecond)
}

func TestWorkerStopTimeoutKeepsClosingUntilFinalizedAndDropsStaleQueue(t *testing.T) {
	usecase := newStubbornPersistAfterUsecase()
	observer := &recordingObserver{}
	worker := NewWorker(Options{Usecase: usecase, QueueSize: 2, Workers: 1, Timeout: time.Second, Observer: observer})
	require.NoError(t, worker.Start(context.Background()))

	worker.EnqueuePersistAfter(context.Background(), pluginevents.PersistAfterCommitted{MessageID: 1})
	usecase.waitStarted(t)
	worker.EnqueuePersistAfter(context.Background(), pluginevents.PersistAfterCommitted{MessageID: 2})

	stopCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, worker.Stop(stopCtx), context.DeadlineExceeded)

	require.ErrorIs(t, worker.Start(context.Background()), ErrWorkerClosing)
	worker.EnqueuePersistAfter(context.Background(), pluginevents.PersistAfterCommitted{MessageID: 3})
	require.Eventually(t, func() bool { return observer.enqueueCount(enqueueResultClosed) > 0 }, time.Second, time.Millisecond)

	usecase.release()
	require.NoError(t, worker.Stop(context.Background()))
	require.Eventually(t, func() bool { return usecase.callCount() == 1 }, time.Second, time.Millisecond)

	require.NoError(t, worker.Start(context.Background()))
	worker.EnqueuePersistAfter(context.Background(), pluginevents.PersistAfterCommitted{MessageID: 4})
	require.Eventually(t, func() bool { return usecase.callCount() == 2 }, time.Second, time.Millisecond)
	require.NoError(t, worker.Stop(context.Background()))
}

func TestWorkerInvokeClassificationError(t *testing.T) {
	observer := &recordingObserver{}
	worker := NewWorker(Options{
		Usecase:   persistAfterUsecaseFunc(func(context.Context, pluginevents.PersistAfterCommitted) error { return errors.New("boom") }),
		QueueSize: 1,
		Workers:   1,
		Timeout:   time.Second,
		Observer:  observer,
	})
	require.NoError(t, worker.Start(context.Background()))
	defer worker.Stop(context.Background())

	worker.EnqueuePersistAfter(context.Background(), pluginevents.PersistAfterCommitted{MessageID: 1})
	require.Eventually(t, func() bool { return observer.invokeCount(invokeResultError) == 1 }, time.Second, time.Millisecond)
}

func TestWorkerInvokeClassificationTimeout(t *testing.T) {
	observer := &recordingObserver{}
	worker := NewWorker(Options{
		Usecase: persistAfterUsecaseFunc(func(ctx context.Context, _ pluginevents.PersistAfterCommitted) error {
			<-ctx.Done()
			return ctx.Err()
		}),
		QueueSize: 1,
		Workers:   1,
		Timeout:   20 * time.Millisecond,
		Observer:  observer,
	})
	require.NoError(t, worker.Start(context.Background()))
	defer worker.Stop(context.Background())

	worker.EnqueuePersistAfter(context.Background(), pluginevents.PersistAfterCommitted{MessageID: 1})
	require.Eventually(t, func() bool { return observer.invokeCount(invokeResultTimeout) == 1 }, time.Second, time.Millisecond)
}

func TestWorkerInvokeClassificationPanic(t *testing.T) {
	observer := &recordingObserver{}
	worker := NewWorker(Options{
		Usecase:   persistAfterUsecaseFunc(func(context.Context, pluginevents.PersistAfterCommitted) error { panic("boom") }),
		QueueSize: 1,
		Workers:   1,
		Timeout:   time.Second,
		Observer:  observer,
	})
	require.NoError(t, worker.Start(context.Background()))
	defer worker.Stop(context.Background())

	worker.EnqueuePersistAfter(context.Background(), pluginevents.PersistAfterCommitted{MessageID: 1})
	require.Eventually(t, func() bool { return observer.invokeCount(invokeResultPanic) == 1 }, time.Second, time.Millisecond)
}

func TestWorkerPanicContinuesNextEvent(t *testing.T) {
	usecase := &panicThenRecordUsecase{done: make(chan struct{})}
	observer := &recordingObserver{}
	worker := NewWorker(Options{Usecase: usecase, QueueSize: 2, Workers: 1, Timeout: time.Second, Observer: observer})
	require.NoError(t, worker.Start(context.Background()))
	defer worker.Stop(context.Background())

	worker.EnqueuePersistAfter(context.Background(), pluginevents.PersistAfterCommitted{MessageID: 1})
	worker.EnqueuePersistAfter(context.Background(), pluginevents.PersistAfterCommitted{MessageID: 2})

	require.Eventually(t, func() bool { return observer.invokeCount(invokeResultPanic) == 1 }, time.Second, time.Millisecond)
	select {
	case <-usecase.done:
	case <-time.After(time.Second):
		t.Fatal("worker did not continue after panic")
	}
	require.Equal(t, 2, usecase.callCount())
}

type persistAfterUsecaseFunc func(context.Context, pluginevents.PersistAfterCommitted) error

func (f persistAfterUsecaseFunc) PersistAfterCommitted(ctx context.Context, event pluginevents.PersistAfterCommitted) error {
	return f(ctx, event)
}

type recordingHookUsecase struct {
	receiveDone chan struct{}

	mu                sync.Mutex
	persistAfterCalls []pluginevents.PersistAfterCommitted
	receiveCalls      []pluginevents.ReceiveOffline
}

func (r *recordingHookUsecase) PersistAfterCommitted(_ context.Context, event pluginevents.PersistAfterCommitted) error {
	r.mu.Lock()
	r.persistAfterCalls = append(r.persistAfterCalls, event)
	r.mu.Unlock()
	return nil
}

func (r *recordingHookUsecase) ReceiveOffline(_ context.Context, event pluginevents.ReceiveOffline) error {
	r.mu.Lock()
	r.receiveCalls = append(r.receiveCalls, event)
	r.mu.Unlock()
	if r.receiveDone != nil {
		select {
		case <-r.receiveDone:
		default:
			close(r.receiveDone)
		}
	}
	return nil
}

type recordingPersistAfterUsecase struct {
	done chan struct{}

	mu    sync.Mutex
	calls []pluginevents.PersistAfterCommitted
}

func (r *recordingPersistAfterUsecase) PersistAfterCommitted(_ context.Context, event pluginevents.PersistAfterCommitted) error {
	r.mu.Lock()
	r.calls = append(r.calls, event)
	r.mu.Unlock()

	if r.done != nil {
		select {
		case <-r.done:
		default:
			close(r.done)
		}
	}
	return nil
}

func (r *recordingPersistAfterUsecase) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

type panicThenRecordUsecase struct {
	done chan struct{}

	mu    sync.Mutex
	calls int
}

func (p *panicThenRecordUsecase) PersistAfterCommitted(context.Context, pluginevents.PersistAfterCommitted) error {
	p.mu.Lock()
	p.calls++
	call := p.calls
	p.mu.Unlock()
	if call == 1 {
		panic("boom")
	}
	if p.done != nil {
		select {
		case <-p.done:
		default:
			close(p.done)
		}
	}
	return nil
}

func (p *panicThenRecordUsecase) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

type blockingPersistAfterUsecase struct {
	started chan struct{}
	blocked chan struct{}
	once    sync.Once

	mu    sync.Mutex
	calls int
}

func newBlockingPersistAfterUsecase() *blockingPersistAfterUsecase {
	return &blockingPersistAfterUsecase{
		started: make(chan struct{}),
		blocked: make(chan struct{}),
	}
}

func (b *blockingPersistAfterUsecase) PersistAfterCommitted(ctx context.Context, _ pluginevents.PersistAfterCommitted) error {
	b.mu.Lock()
	b.calls++
	b.mu.Unlock()
	b.once.Do(func() { close(b.started) })
	select {
	case <-b.blocked:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (b *blockingPersistAfterUsecase) waitStarted(t *testing.T) {
	t.Helper()
	select {
	case <-b.started:
	case <-time.After(time.Second):
		t.Fatal("PersistAfterCommitted did not start")
	}
}

func (b *blockingPersistAfterUsecase) release() {
	select {
	case <-b.blocked:
	default:
		close(b.blocked)
	}
}

func (b *blockingPersistAfterUsecase) callCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.calls
}

type twoPhaseBlockingPersistAfterUsecase struct {
	started       chan struct{}
	phaseOneBlock chan struct{}
	phaseTwoBlock chan struct{}

	mu           sync.Mutex
	calls        int
	startedCalls int
}

func newTwoPhaseBlockingPersistAfterUsecase() *twoPhaseBlockingPersistAfterUsecase {
	return &twoPhaseBlockingPersistAfterUsecase{
		started:       make(chan struct{}, 8),
		phaseOneBlock: make(chan struct{}),
		phaseTwoBlock: make(chan struct{}),
	}
}

func (u *twoPhaseBlockingPersistAfterUsecase) PersistAfterCommitted(context.Context, pluginevents.PersistAfterCommitted) error {
	u.mu.Lock()
	u.calls++
	call := u.calls
	u.startedCalls++
	u.mu.Unlock()

	select {
	case u.started <- struct{}{}:
	default:
	}

	if call <= 2 {
		<-u.phaseOneBlock
		return nil
	}
	<-u.phaseTwoBlock
	return nil
}

func (u *twoPhaseBlockingPersistAfterUsecase) waitStartedCalls(t *testing.T, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		select {
		case <-u.started:
		case <-time.After(time.Second):
			t.Fatal("PersistAfterCommitted did not start enough calls")
		}
	}
}

func (u *twoPhaseBlockingPersistAfterUsecase) releasePhaseOne() {
	select {
	case <-u.phaseOneBlock:
	default:
		close(u.phaseOneBlock)
	}
}

func (u *twoPhaseBlockingPersistAfterUsecase) releasePhaseTwo() {
	select {
	case <-u.phaseTwoBlock:
	default:
		close(u.phaseTwoBlock)
	}
}

type stubbornPersistAfterUsecase struct {
	started chan struct{}
	blocked chan struct{}
	once    sync.Once

	mu    sync.Mutex
	calls int
}

func newStubbornPersistAfterUsecase() *stubbornPersistAfterUsecase {
	return &stubbornPersistAfterUsecase{
		started: make(chan struct{}),
		blocked: make(chan struct{}),
	}
}

func (s *stubbornPersistAfterUsecase) PersistAfterCommitted(context.Context, pluginevents.PersistAfterCommitted) error {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	s.once.Do(func() { close(s.started) })
	<-s.blocked
	return nil
}

func (s *stubbornPersistAfterUsecase) waitStarted(t *testing.T) {
	t.Helper()
	select {
	case <-s.started:
	case <-time.After(time.Second):
		t.Fatal("PersistAfterCommitted did not start")
	}
}

func (s *stubbornPersistAfterUsecase) release() {
	select {
	case <-s.blocked:
	default:
		close(s.blocked)
	}
}

func (s *stubbornPersistAfterUsecase) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

type recordingObserver struct {
	mu                   sync.Mutex
	enqueueCounts        map[string]int
	invokeCounts         map[string]int
	receiveEnqueueCounts map[string]int
	receiveInvokeCounts  map[string]int
}

func (r *recordingObserver) ObservePersistAfterEnqueue(result string, _ time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.enqueueCounts == nil {
		r.enqueueCounts = make(map[string]int)
	}
	r.enqueueCounts[result]++
}

func (r *recordingObserver) ObservePersistAfterInvoke(result string, _ time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.invokeCounts == nil {
		r.invokeCounts = make(map[string]int)
	}
	r.invokeCounts[result]++
}

func (r *recordingObserver) ObserveReceiveEnqueue(result string, _ time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.receiveEnqueueCounts == nil {
		r.receiveEnqueueCounts = make(map[string]int)
	}
	r.receiveEnqueueCounts[result]++
}

func (r *recordingObserver) ObserveReceiveInvoke(result string, _ time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.receiveInvokeCounts == nil {
		r.receiveInvokeCounts = make(map[string]int)
	}
	r.receiveInvokeCounts[result]++
}

func (r *recordingObserver) enqueueCount(result string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.enqueueCounts[result]
}

func (r *recordingObserver) invokeCount(result string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.invokeCounts[result]
}

func (r *recordingObserver) receiveEnqueueCount(result string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.receiveEnqueueCounts[result]
}

func (r *recordingObserver) receiveInvokeCount(result string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.receiveInvokeCounts[result]
}
