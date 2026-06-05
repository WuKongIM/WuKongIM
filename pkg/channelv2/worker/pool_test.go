package worker

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/stretchr/testify/require"
)

func TestPoolRunsTaskAndReportsCompletion(t *testing.T) {
	sink := &captureSink{}
	pool, err := NewPool(PoolConfig{Name: "test", Workers: 1, QueueSize: 1}, Deps{}, sink)
	require.NoError(t, err)
	defer pool.Close()

	fence := ch.Fence{ChannelKey: ch.ChannelKey("1:a"), Generation: 1, Epoch: 1, LeaderEpoch: 1, OpID: 1}
	err = pool.Submit(context.Background(), Task{Kind: TaskFunc, Fence: fence, RunFunc: func(context.Context) Result { return Result{Fence: fence} }})
	require.NoError(t, err)
	require.Eventually(t, func() bool { return sink.Len() == 1 }, time.Second, time.Millisecond)
}

func TestPoolReturnsBackpressureWhenQueueFull(t *testing.T) {
	sink := &captureSink{}
	pool, err := NewPool(PoolConfig{Name: "test", Workers: 1, QueueSize: 1}, Deps{}, sink)
	require.NoError(t, err)
	defer pool.Close()

	block := make(chan struct{})
	started := make(chan struct{})
	defer close(block)
	fence := ch.Fence{ChannelKey: ch.ChannelKey("1:a"), Generation: 1, Epoch: 1, LeaderEpoch: 1, OpID: 1}
	require.NoError(t, pool.Submit(context.Background(), Task{Kind: TaskFunc, Fence: fence, RunFunc: func(context.Context) Result {
		close(started)
		<-block
		return Result{Fence: fence}
	}}))
	require.Eventually(t, func() bool {
		select {
		case <-started:
			return true
		default:
			return false
		}
	}, time.Second, time.Millisecond)
	require.NoError(t, pool.Submit(context.Background(), Task{Kind: TaskFunc, Fence: fence, RunFunc: func(context.Context) Result { return Result{Fence: fence} }}))
	err = pool.Submit(context.Background(), Task{Kind: TaskFunc, Fence: fence, RunFunc: func(context.Context) Result { return Result{Fence: fence} }})
	require.ErrorIs(t, err, ch.ErrBackpressured)
}

func TestPoolReportsInflightCurrentAndPeak(t *testing.T) {
	sink := &captureSink{}
	pool, err := NewPool(PoolConfig{Name: "test", Workers: 1, QueueSize: 1}, Deps{}, sink)
	require.NoError(t, err)
	defer pool.Close()

	obs := &captureWorkerObserver{}
	pool.SetQueueObserver(obs)

	block := make(chan struct{})
	release := sync.Once{}
	defer release.Do(func() { close(block) })
	started := make(chan struct{})
	fence := ch.Fence{ChannelKey: ch.ChannelKey("1:a"), Generation: 1, Epoch: 1, LeaderEpoch: 1, OpID: 1}
	require.NoError(t, pool.Submit(context.Background(), Task{Kind: TaskFunc, Fence: fence, RunFunc: func(context.Context) Result {
		close(started)
		<-block
		return Result{Fence: fence}
	}}))

	require.Eventually(t, func() bool {
		select {
		case <-started:
		default:
			return false
		}
		return obs.Inflight("test") == 1 && obs.InflightPeak("test") == 1
	}, time.Second, time.Millisecond)

	release.Do(func() { close(block) })
	require.Eventually(t, func() bool {
		return obs.Inflight("test") == 0 && obs.InflightPeak("test") == 1
	}, time.Second, time.Millisecond)
}

func TestPoolReportsCapacityWorkersAdmissionWaitAndTaskDuration(t *testing.T) {
	obs := &recordingPoolPressureObserver{}
	sink := &captureSink{ch: make(chan Result, 1)}
	pool, err := NewPool(PoolConfig{Name: "store_append", Workers: 1, QueueSize: 2}, Deps{}, sink)
	if err != nil {
		t.Fatalf("NewPool() error = %v", err)
	}
	defer pool.Close()
	pool.SetQueueObserver(obs)

	err = pool.Submit(context.Background(), Task{
		Kind: TaskFunc,
		RunFunc: func(context.Context) Result {
			time.Sleep(time.Millisecond)
			return Result{Kind: TaskFunc}
		},
	})
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	select {
	case <-sink.ch:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for worker result")
	}

	if got := obs.workers["store_append"]; got != 1 {
		t.Fatalf("workers = %d, want 1", got)
	}
	if got := obs.capacity["store_append"]; got != 2 {
		t.Fatalf("capacity = %d, want 2", got)
	}
	if got := obs.admissions["store_append:ok"]; got != 1 {
		t.Fatalf("ok admissions = %d, want 1", got)
	}
	if got := obs.waits["store_append:func"]; got == 0 {
		t.Fatal("queue wait was not observed")
	}
	if got := obs.tasks["store_append:func:ok"]; got == 0 {
		t.Fatal("task duration was not observed")
	}
}

func TestPoolReportsFullClosedAndCanceledAdmission(t *testing.T) {
	obs := &recordingPoolPressureObserver{}
	block := make(chan struct{})
	sink := &captureSink{ch: make(chan Result, 3)}
	pool, err := NewPool(PoolConfig{Name: "rpc", Workers: 1, QueueSize: 1}, Deps{}, sink)
	if err != nil {
		t.Fatalf("NewPool() error = %v", err)
	}
	pool.SetQueueObserver(obs)
	defer pool.Close()

	started := make(chan struct{})
	mustSubmit := func(onRun func()) {
		if err := pool.Submit(context.Background(), Task{
			Kind: TaskFunc,
			RunFunc: func(context.Context) Result {
				if onRun != nil {
					onRun()
				}
				<-block
				return Result{Kind: TaskFunc}
			},
		}); err != nil {
			t.Fatalf("Submit() error = %v", err)
		}
	}
	mustSubmit(func() { close(started) })
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for worker to start")
	}
	mustSubmit(nil)
	if err := pool.Submit(context.Background(), Task{Kind: TaskFunc, RunFunc: func(context.Context) Result { return Result{} }}); !errors.Is(err, ch.ErrBackpressured) {
		t.Fatalf("Submit() error = %v, want %v", err, ch.ErrBackpressured)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = pool.Submit(ctx, Task{Kind: TaskFunc, RunFunc: func(context.Context) Result { return Result{} }})
	close(block)
	_ = pool.Close()
	_ = pool.Submit(context.Background(), Task{Kind: TaskFunc, RunFunc: func(context.Context) Result { return Result{} }})

	if got := obs.admissions["rpc:full"]; got != 1 {
		t.Fatalf("full admissions = %d, want 1", got)
	}
	if got := obs.admissions["rpc:canceled"]; got != 1 {
		t.Fatalf("canceled admissions = %d, want 1", got)
	}
	if got := obs.admissions["rpc:closed"]; got != 1 {
		t.Fatalf("closed admissions = %d, want 1", got)
	}
}

func TestPoolSubmitRejectsCanceledContextWithQueueCapacity(t *testing.T) {
	obs := &recordingPoolPressureObserver{}
	block := make(chan struct{})
	sink := &captureSink{}
	pool, err := NewPool(PoolConfig{Name: "rpc", Workers: 1, QueueSize: 64}, Deps{}, sink)
	if err != nil {
		t.Fatalf("NewPool() error = %v", err)
	}
	defer func() {
		close(block)
		_ = pool.Close()
	}()
	pool.SetQueueObserver(obs)

	started := make(chan struct{})
	if err := pool.Submit(context.Background(), Task{
		Kind: TaskFunc,
		RunFunc: func(context.Context) Result {
			close(started)
			<-block
			return Result{Kind: TaskFunc}
		},
	}); err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for worker to start")
	}

	const attempts = 64
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for i := 0; i < attempts; i++ {
		err := pool.Submit(ctx, Task{Kind: TaskFunc, RunFunc: func(context.Context) Result { return Result{} }})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Submit() error = %v, want %v at attempt %d", err, context.Canceled, i)
		}
	}
	if got := pool.QueueDepth(); got != 0 {
		t.Fatalf("queue depth = %d, want 0", got)
	}
	if got := obs.admissions["rpc:ok"]; got != 1 {
		t.Fatalf("ok admissions = %d, want 1", got)
	}
	if got := obs.admissions["rpc:canceled"]; got != attempts {
		t.Fatalf("canceled admissions = %d, want %d", got, attempts)
	}
}

func TestPoolReportsAdmissionResultLabels(t *testing.T) {
	if got := workerAdmissionResult(nil); got != "ok" {
		t.Fatalf("nil result = %q, want ok", got)
	}
	if got := workerAdmissionResult(context.Canceled); got != "canceled" {
		t.Fatalf("canceled result = %q, want canceled", got)
	}
	if got := workerAdmissionResult(context.DeadlineExceeded); got != "timeout" {
		t.Fatalf("deadline result = %q, want timeout", got)
	}
	if got := workerAdmissionResult(errors.New("custom")); got != "other" {
		t.Fatalf("unknown result = %q, want other", got)
	}
}

func TestPoolCloseCancelsDequeuedTaskContext(t *testing.T) {
	sink := &captureSink{}
	pool, err := NewPool(PoolConfig{Name: "test", Workers: 1, QueueSize: 1}, Deps{}, sink)
	require.NoError(t, err)

	started := make(chan struct{})
	cancelled := make(chan struct{})
	fence := ch.Fence{ChannelKey: ch.ChannelKey("1:a"), Generation: 1, Epoch: 1, LeaderEpoch: 1, OpID: 1}
	require.NoError(t, pool.Submit(context.Background(), Task{Kind: TaskFunc, Fence: fence, RunFunc: func(ctx context.Context) Result {
		close(started)
		<-ctx.Done()
		close(cancelled)
		return Result{Fence: fence, Err: ctx.Err()}
	}}))
	require.Eventually(t, func() bool {
		select {
		case <-started:
			return true
		default:
			return false
		}
	}, time.Second, time.Millisecond)

	closed := make(chan struct{})
	go func() {
		_ = pool.Close()
		close(closed)
	}()

	select {
	case <-closed:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("pool Close did not cancel a dequeued task context")
	}
	select {
	case <-cancelled:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("task did not observe pool cancellation")
	}
}

func TestPoolsRouteTasksByKindAndReportDepth(t *testing.T) {
	sink := &captureSink{}
	pools, err := NewPools(PoolsConfig{
		StoreAppend: PoolConfig{Name: "store-append", Workers: 1, QueueSize: 1},
		StoreRead:   PoolConfig{Name: "store-read", Workers: 1, QueueSize: 1},
		StoreApply:  PoolConfig{Name: "store-apply", Workers: 1, QueueSize: 1},
		RPC:         PoolConfig{Name: "rpc", Workers: 1, QueueSize: 1},
	}, Deps{}, sink)
	require.NoError(t, err)
	defer pools.Close()

	require.Equal(t, "store-append", pools.StoreAppend.Name())
	require.Equal(t, "store-read", pools.StoreRead.Name())
	require.Equal(t, "store-apply", pools.StoreApply.Name())
	require.Equal(t, "rpc", pools.RPC.Name())

	block := make(chan struct{})
	started := make(chan struct{})
	defer close(block)
	fence := ch.Fence{ChannelKey: ch.ChannelKey("1:a"), Generation: 1, Epoch: 1, LeaderEpoch: 1, OpID: 1}
	require.NoError(t, pools.StoreRead.Submit(context.Background(), Task{Kind: TaskFunc, Fence: fence, RunFunc: func(context.Context) Result {
		close(started)
		<-block
		return Result{Fence: fence}
	}}))
	require.Eventually(t, func() bool {
		select {
		case <-started:
			return true
		default:
			return false
		}
	}, time.Second, time.Millisecond)

	require.NoError(t, pools.Submit(context.Background(), Task{Kind: TaskStoreReadLog, Fence: fence}))
	require.Equal(t, 1, pools.QueueDepth(TaskStoreReadLog))
	require.Equal(t, 0, pools.QueueDepth(TaskStoreAppend))
	require.ErrorIs(t, pools.Submit(context.Background(), Task{Kind: TaskFunc, Fence: fence}), ch.ErrInvalidConfig)
}

type captureSink struct {
	mu      sync.Mutex
	results []Result
	ch      chan Result
}

func (s *captureSink) Complete(result Result) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.results = append(s.results, result)
	if s.ch != nil {
		s.ch <- result
	}
}

func (s *captureSink) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.results)
}

type captureWorkerObserver struct {
	mu       sync.Mutex
	inflight map[string]int
	peak     map[string]int
}

func (o *captureWorkerObserver) SetWorkerQueueDepth(string, int) {}

func (o *captureWorkerObserver) SetWorkerInflight(pool string, inflight int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.inflight == nil {
		o.inflight = map[string]int{}
	}
	o.inflight[pool] = inflight
}

func (o *captureWorkerObserver) SetWorkerInflightPeak(pool string, peak int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.peak == nil {
		o.peak = map[string]int{}
	}
	o.peak[pool] = peak
}

func (o *captureWorkerObserver) Inflight(pool string) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.inflight[pool]
}

func (o *captureWorkerObserver) InflightPeak(pool string) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.peak[pool]
}

type recordingPoolPressureObserver struct {
	mu         sync.Mutex
	workers    map[string]int
	capacity   map[string]int
	admissions map[string]int
	waits      map[string]time.Duration
	tasks      map[string]time.Duration
}

func (o *recordingPoolPressureObserver) ensure() {
	if o.workers == nil {
		o.workers = make(map[string]int)
		o.capacity = make(map[string]int)
		o.admissions = make(map[string]int)
		o.waits = make(map[string]time.Duration)
		o.tasks = make(map[string]time.Duration)
	}
}

func (o *recordingPoolPressureObserver) SetWorkerQueueDepth(string, int) {}

func (o *recordingPoolPressureObserver) SetWorkerQueueCapacity(pool string, capacity int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.ensure()
	o.capacity[pool] = capacity
}

func (o *recordingPoolPressureObserver) SetWorkerWorkers(pool string, workers int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.ensure()
	o.workers[pool] = workers
}

func (o *recordingPoolPressureObserver) ObserveWorkerAdmission(pool string, result string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.ensure()
	o.admissions[pool+":"+result]++
}

func (o *recordingPoolPressureObserver) ObserveWorkerWait(pool string, kind TaskKind, d time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.ensure()
	o.waits[pool+":"+taskKindTestLabel(kind)] += d
}

func (o *recordingPoolPressureObserver) ObserveWorkerTask(pool string, kind TaskKind, err error, d time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.ensure()
	result := "ok"
	if err != nil {
		result = "err"
	}
	o.tasks[pool+":"+taskKindTestLabel(kind)+":"+result] += d
}

func taskKindTestLabel(kind TaskKind) string {
	if kind == TaskFunc {
		return "func"
	}
	return "other"
}
