package worker

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/transport"
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

func TestPoolReportsAntsPoolUsage(t *testing.T) {
	obs := &recordingPoolPressureObserver{}
	sink := &captureSink{ch: make(chan Result, 1)}
	pool, err := NewPool(PoolConfig{Name: "store_append", Workers: 1, QueueSize: 2}, Deps{}, sink)
	if err != nil {
		t.Fatalf("NewPool() error = %v", err)
	}
	block := make(chan struct{})
	release := sync.Once{}
	defer func() {
		release.Do(func() { close(block) })
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
		t.Fatal("timed out waiting for worker task to start")
	}

	require.Eventually(t, func() bool {
		usage := obs.AntsUsage("store_append")
		return usage.running == 1 && usage.capacity == 1 && usage.waiting == 0
	}, time.Second, time.Millisecond)

	release.Do(func() { close(block) })
	select {
	case <-sink.ch:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for worker result")
	}
	require.Eventually(t, func() bool {
		usage := obs.AntsUsage("store_append")
		return usage.running == 0 && usage.capacity == 1 && usage.waiting == 0
	}, time.Second, time.Millisecond)
}

func TestPoolSetQueueObserverConcurrentWithExecution(t *testing.T) {
	sink := &captureSink{ch: make(chan Result, 64)}
	pool, err := NewPool(PoolConfig{Name: "race", Workers: 2, QueueSize: 64}, Deps{}, sink)
	require.NoError(t, err)
	defer pool.Close()

	stopObservers := make(chan struct{})
	observerDone := make(chan struct{})
	stopObserverLoop := sync.Once{}
	cleanupObserverLoop := func() {
		stopObserverLoop.Do(func() {
			close(stopObservers)
			select {
			case <-observerDone:
			case <-time.After(time.Second):
				t.Fatal("observer setter did not stop")
			}
		})
	}
	t.Cleanup(cleanupObserverLoop)

	go func() {
		defer close(observerDone)
		for {
			select {
			case <-stopObservers:
				return
			default:
				pool.SetQueueObserver(&recordingPoolPressureObserver{})
			}
		}
	}()

	for i := 0; i < 32; i++ {
		fence := ch.Fence{ChannelKey: ch.ChannelKey("1:race"), OpID: ch.OpID(i + 1)}
		require.NoError(t, pool.Submit(context.Background(), Task{
			Kind:  TaskFunc,
			Fence: fence,
			RunFunc: func(context.Context) Result {
				time.Sleep(100 * time.Microsecond)
				return Result{Kind: TaskFunc, Fence: fence}
			},
		}))
	}
	require.Eventually(t, func() bool { return sink.Len() == 32 }, time.Second, time.Millisecond)
	cleanupObserverLoop()
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

func TestPoolCloseCompletesQueuedTaskAsClosed(t *testing.T) {
	sink := &captureSink{ch: make(chan Result, 2)}
	pool, err := NewPool(PoolConfig{Name: "test", Workers: 1, QueueSize: 2}, Deps{}, sink)
	require.NoError(t, err)

	block := make(chan struct{})
	release := sync.Once{}
	closed := make(chan error, 1)
	closeStarted := make(chan struct{})
	waitClose := sync.Once{}
	var closeErr error
	waitForClose := func() error {
		release.Do(func() { close(block) })
		waitClose.Do(func() {
			select {
			case <-closeStarted:
				select {
				case closeErr = <-closed:
				case <-time.After(time.Second):
					closeErr = errors.New("pool Close did not return")
				}
			default:
				closeErr = pool.Close()
			}
		})
		return closeErr
	}
	t.Cleanup(func() { require.NoError(t, waitForClose()) })

	blockingStarted := make(chan struct{})
	blockingFence := ch.Fence{ChannelKey: ch.ChannelKey("1:a"), Generation: 1, Epoch: 1, LeaderEpoch: 1, OpID: 1}
	queuedFence := ch.Fence{ChannelKey: ch.ChannelKey("1:b"), Generation: 1, Epoch: 1, LeaderEpoch: 1, OpID: 2}

	require.NoError(t, pool.Submit(context.Background(), Task{
		Kind:  TaskFunc,
		Fence: blockingFence,
		RunFunc: func(context.Context) Result {
			close(blockingStarted)
			<-block
			return Result{Kind: TaskFunc, Fence: blockingFence}
		},
	}))
	require.Eventually(t, func() bool {
		select {
		case <-blockingStarted:
			return true
		default:
			return false
		}
	}, time.Second, time.Millisecond)

	require.NoError(t, pool.Submit(context.Background(), Task{
		Kind:  TaskFunc,
		Fence: queuedFence,
		RunFunc: func(context.Context) Result {
			return Result{Kind: TaskFunc, Fence: queuedFence, Err: errors.New("queued task ran")}
		},
	}))

	go func() {
		close(closeStarted)
		closed <- pool.Close()
	}()

	deadline := time.After(100 * time.Millisecond)
queuedResult:
	for {
		if resultContainsOpID(sink.Results(), queuedFence.OpID) {
			require.ErrorIs(t, resultByOpID(t, sink.Results(), queuedFence.OpID).Err, ch.ErrClosed)
			break queuedResult
		}
		select {
		case result := <-sink.ch:
			if result.Fence.OpID == queuedFence.OpID {
				require.ErrorIs(t, result.Err, ch.ErrClosed)
				break queuedResult
			}
		case <-deadline:
			t.Fatal("queued task was not completed while running task was blocked")
		}
	}

	require.NoError(t, waitForClose())
}

func TestPoolCompletesAcceptedTaskAfterExecutorSaturation(t *testing.T) {
	sink := &captureSink{ch: make(chan Result, 2)}
	pool, err := NewPool(PoolConfig{Name: "test", Workers: 1, QueueSize: 4}, Deps{}, sink)
	require.NoError(t, err)
	defer pool.Close()

	block := make(chan struct{})
	started := make(chan struct{})
	release := sync.Once{}
	defer release.Do(func() { close(block) })

	firstFence := ch.Fence{ChannelKey: ch.ChannelKey("1:a"), Generation: 1, Epoch: 1, LeaderEpoch: 1, OpID: 1}
	secondFence := ch.Fence{ChannelKey: ch.ChannelKey("1:b"), Generation: 1, Epoch: 1, LeaderEpoch: 1, OpID: 2}
	require.NoError(t, pool.Submit(context.Background(), Task{
		Kind:  TaskFunc,
		Fence: firstFence,
		RunFunc: func(context.Context) Result {
			close(started)
			<-block
			return Result{Kind: TaskFunc, Fence: firstFence}
		},
	}))
	require.Eventually(t, func() bool {
		select {
		case <-started:
			return true
		default:
			return false
		}
	}, time.Second, time.Millisecond)

	require.NoError(t, pool.Submit(context.Background(), Task{
		Kind:  TaskFunc,
		Fence: secondFence,
		RunFunc: func(context.Context) Result {
			return Result{Kind: TaskFunc, Fence: secondFence}
		},
	}))

	require.Never(t, func() bool {
		return sink.Len() > 0 && resultContainsOpID(sink.Results(), 2)
	}, 20*time.Millisecond, time.Millisecond)

	release.Do(func() { close(block) })
	require.Eventually(t, func() bool { return sink.Len() == 2 }, time.Second, time.Millisecond)
	require.ElementsMatch(t, []ch.OpID{1, 2}, resultOpIDs(sink.Results()))
}

func TestPoolBatchesRPCPullTasksByNode(t *testing.T) {
	sink := &captureSink{ch: make(chan Result, 2)}
	tr := &batchWorkerTransport{}
	obs := &recordingPoolPressureObserver{}
	pool, err := NewPool(PoolConfig{Name: "rpc", Workers: 1, QueueSize: 8}, Deps{Transport: tr}, sink)
	require.NoError(t, err)
	defer pool.Close()
	pool.SetQueueObserver(obs)

	first := Task{
		Kind:    TaskRPCPull,
		Fence:   ch.Fence{ChannelKey: "1:a", OpID: 1},
		RPCPull: &RPCPullTask{Node: 2, Request: transport.PullRequest{ChannelKey: "1:a", NextOffset: 1}},
	}
	second := Task{
		Kind:    TaskRPCPull,
		Fence:   ch.Fence{ChannelKey: "1:b", OpID: 2},
		RPCPull: &RPCPullTask{Node: 2, Request: transport.PullRequest{ChannelKey: "1:b", NextOffset: 3}},
	}
	require.NoError(t, pool.Submit(context.Background(), first))
	require.NoError(t, pool.Submit(context.Background(), second))

	require.Eventually(t, func() bool { return sink.Len() == 2 }, time.Second, time.Millisecond)
	require.Equal(t, 1, tr.PullBatchCalls())
	require.Equal(t, 0, tr.PullCalls())
	require.Equal(t, 1, obs.batchCalls["rpc:rpc_pull:ok"])
	require.Equal(t, 2, obs.batchItems["rpc:rpc_pull:ok"])
	require.ElementsMatch(t, []ch.OpID{1, 2}, resultOpIDs(sink.Results()))
}

func TestPoolRPCPullTimeoutStartsWhenWorkerExecutes(t *testing.T) {
	sink := &captureSink{ch: make(chan Result, 2)}
	tr := &batchWorkerTransport{}
	pool, err := NewPool(PoolConfig{Name: "rpc", Workers: 1, QueueSize: 4}, Deps{Transport: tr}, sink)
	require.NoError(t, err)
	defer pool.Close()

	block := make(chan struct{})
	started := make(chan struct{})
	blockFence := ch.Fence{ChannelKey: "1:block", OpID: 1}
	require.NoError(t, pool.Submit(context.Background(), Task{
		Kind:  TaskFunc,
		Fence: blockFence,
		RunFunc: func(context.Context) Result {
			close(started)
			<-block
			return Result{Kind: TaskFunc, Fence: blockFence}
		},
	}))
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for blocking task to start")
	}

	pullFence := ch.Fence{ChannelKey: "1:a", OpID: 2}
	require.NoError(t, pool.Submit(context.Background(), Task{
		Kind:  TaskRPCPull,
		Fence: pullFence,
		RPCPull: &RPCPullTask{
			Node:    2,
			Request: transport.PullRequest{ChannelKey: "1:a", NextOffset: 1},
			Timeout: time.Millisecond,
		},
	}))
	time.Sleep(5 * time.Millisecond)
	close(block)

	require.Eventually(t, func() bool { return sink.Len() == 2 }, time.Second, time.Millisecond)
	result := resultByOpID(t, sink.Results(), 2)
	require.NoError(t, result.Err)
	require.Equal(t, 1, tr.PullCalls())
}

func TestPoolRPCPullBatchTimeoutStartsWhenWorkerExecutes(t *testing.T) {
	sink := &captureSink{ch: make(chan Result, 3)}
	tr := &batchWorkerTransport{}
	pool, err := NewPool(PoolConfig{Name: "rpc", Workers: 1, QueueSize: 8}, Deps{Transport: tr}, sink)
	require.NoError(t, err)
	defer pool.Close()

	block := make(chan struct{})
	started := make(chan struct{})
	blockFence := ch.Fence{ChannelKey: "1:block", OpID: 1}
	require.NoError(t, pool.Submit(context.Background(), Task{
		Kind:  TaskFunc,
		Fence: blockFence,
		RunFunc: func(context.Context) Result {
			close(started)
			<-block
			return Result{Kind: TaskFunc, Fence: blockFence}
		},
	}))
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for blocking task to start")
	}

	first := Task{
		Kind:  TaskRPCPull,
		Fence: ch.Fence{ChannelKey: "1:a", OpID: 2},
		RPCPull: &RPCPullTask{
			Node:    2,
			Request: transport.PullRequest{ChannelKey: "1:a", NextOffset: 1},
			Timeout: time.Millisecond,
		},
	}
	second := Task{
		Kind:  TaskRPCPull,
		Fence: ch.Fence{ChannelKey: "1:b", OpID: 3},
		RPCPull: &RPCPullTask{
			Node:    2,
			Request: transport.PullRequest{ChannelKey: "1:b", NextOffset: 3},
			Timeout: time.Millisecond,
		},
	}
	require.NoError(t, pool.Submit(context.Background(), first))
	require.NoError(t, pool.Submit(context.Background(), second))
	time.Sleep(5 * time.Millisecond)
	close(block)

	require.Eventually(t, func() bool { return sink.Len() == 3 }, time.Second, time.Millisecond)
	firstResult := resultByOpID(t, sink.Results(), 2)
	secondResult := resultByOpID(t, sink.Results(), 3)
	require.NoError(t, firstResult.Err)
	require.NoError(t, secondResult.Err)
	require.Equal(t, 1, tr.PullBatchCalls())
	require.Equal(t, 0, tr.PullCalls())
}

func TestPoolRPCPullTimeoutAppliesDuringTransportCall(t *testing.T) {
	sink := &captureSink{ch: make(chan Result, 1)}
	tr := newBlockingPullTransport(2)
	pool, err := NewPool(PoolConfig{Name: "rpc", Workers: 1, QueueSize: 2}, Deps{Transport: tr}, sink)
	require.NoError(t, err)
	defer pool.Close()

	require.NoError(t, pool.Submit(context.Background(), Task{
		Kind:  TaskRPCPull,
		Fence: ch.Fence{ChannelKey: "1:a", OpID: 1},
		RPCPull: &RPCPullTask{
			Node:    2,
			Request: transport.PullRequest{ChannelKey: "1:a", NextOffset: 1},
			Timeout: time.Millisecond,
		},
	}))
	select {
	case <-tr.Started():
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for blocking pull to start")
	}

	require.Eventually(t, func() bool { return sink.Len() == 1 }, time.Second, time.Millisecond)
	result := resultByOpID(t, sink.Results(), 1)
	require.ErrorIs(t, result.Err, context.DeadlineExceeded)
}

func TestPoolRunsCollectedRPCSubgroupsSerially(t *testing.T) {
	sink := &captureSink{ch: make(chan Result, 2)}
	tr := newBlockingPullTransport(1)
	exec, err := newExecutor(2)
	require.NoError(t, err)
	defer exec.close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool := &Pool{
		cfg:    PoolConfig{Name: "rpc", Workers: 2, QueueSize: 2},
		deps:   Deps{Transport: tr},
		sink:   sink,
		queue:  make(chan queuedTask, 2),
		slots:  make(chan struct{}, 2),
		stop:   make(chan struct{}),
		ctx:    ctx,
		cancel: cancel,
		exec:   exec,
		obs:    noopQueueObserver{},
	}
	defer func() {
		tr.Release()
		close(pool.stop)
		pool.dispatchWG.Wait()
	}()

	first := queuedTask{enqueuedAt: time.Now(), task: Task{
		Kind:    TaskRPCPull,
		Fence:   ch.Fence{ChannelKey: "1:a", OpID: 1},
		RPCPull: &RPCPullTask{Node: 1, Request: transport.PullRequest{ChannelKey: "1:a", NextOffset: 1}},
	}}
	second := queuedTask{enqueuedAt: time.Now(), task: Task{
		Kind:    TaskRPCPull,
		Fence:   ch.Fence{ChannelKey: "1:b", OpID: 2},
		RPCPull: &RPCPullTask{Node: 2, Request: transport.PullRequest{ChannelKey: "1:b", NextOffset: 2}},
	}}
	pool.slots <- struct{}{}
	pool.slots <- struct{}{}
	pool.queue <- first
	pool.queue <- second

	pool.dispatchWG.Add(1)
	go pool.dispatch()

	select {
	case <-tr.Started():
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first RPC subgroup to start")
	}
	select {
	case result := <-sink.ch:
		t.Fatalf("second RPC subgroup ran before first completed: %+v", result)
	case <-time.After(20 * time.Millisecond):
	}

	tr.Release()
	require.Eventually(t, func() bool { return sink.Len() == 2 }, time.Second, time.Millisecond)
	require.Equal(t, []ch.OpID{1, 2}, resultOpIDs(sink.Results()))
}

func TestPoolBatchesRPCPullHintTasksByNode(t *testing.T) {
	sink := &captureSink{ch: make(chan Result, 2)}
	tr := &batchWorkerTransport{}
	obs := &recordingPoolPressureObserver{}
	pool, err := NewPool(PoolConfig{Name: "rpc", Workers: 1, QueueSize: 8}, Deps{Transport: tr}, sink)
	require.NoError(t, err)
	defer pool.Close()
	pool.SetQueueObserver(obs)

	first := Task{
		Kind:        TaskRPCPullHint,
		Fence:       ch.Fence{ChannelKey: "1:a", OpID: 1},
		RPCPullHint: &RPCPullHintTask{Node: 2, Request: transport.PullHintRequest{ChannelKey: "1:a", LeaderLEO: 1}},
	}
	second := Task{
		Kind:        TaskRPCPullHint,
		Fence:       ch.Fence{ChannelKey: "1:b", OpID: 2},
		RPCPullHint: &RPCPullHintTask{Node: 2, Request: transport.PullHintRequest{ChannelKey: "1:b", LeaderLEO: 2}},
	}
	require.NoError(t, pool.Submit(context.Background(), first))
	require.NoError(t, pool.Submit(context.Background(), second))

	require.Eventually(t, func() bool { return sink.Len() == 2 }, time.Second, time.Millisecond)
	require.Equal(t, 1, tr.PullHintBatchCalls())
	require.Equal(t, 0, tr.PullHintCalls())
	require.Equal(t, 1, obs.batchCalls["rpc:rpc_pull_hint:ok"])
	require.Equal(t, 2, obs.batchItems["rpc:rpc_pull_hint:ok"])
	require.ElementsMatch(t, []ch.OpID{1, 2}, resultOpIDs(sink.Results()))
}

func TestPoolBatchesStoreApplyTasksWhenFactorySupportsBatch(t *testing.T) {
	sink := &captureSink{ch: make(chan Result, 2)}
	stores := &batchApplyStoreFactory{}
	obs := &recordingPoolPressureObserver{}
	pool, err := NewPool(PoolConfig{Name: "store-apply", Workers: 1, QueueSize: 8}, Deps{Stores: stores}, sink)
	require.NoError(t, err)
	defer pool.Close()
	pool.SetQueueObserver(obs)

	first := Task{
		Kind:       TaskStoreApply,
		Fence:      ch.Fence{ChannelKey: "1:a", OpID: 1},
		StoreApply: &StoreApplyTask{ChannelID: ch.ChannelID{ID: "a", Type: 1}, Records: []ch.Record{{ID: 1, Index: 1, Payload: []byte("a")}}},
	}
	second := Task{
		Kind:       TaskStoreApply,
		Fence:      ch.Fence{ChannelKey: "1:b", OpID: 2},
		StoreApply: &StoreApplyTask{ChannelID: ch.ChannelID{ID: "b", Type: 1}, Records: []ch.Record{{ID: 2, Index: 3, Payload: []byte("b")}}},
	}
	require.NoError(t, pool.Submit(context.Background(), first))
	require.NoError(t, pool.Submit(context.Background(), second))

	require.Eventually(t, func() bool { return sink.Len() == 2 }, time.Second, time.Millisecond)
	require.Equal(t, 1, stores.BatchCalls())
	require.Equal(t, 0, stores.SingleApplyCalls())
	require.Equal(t, 1, obs.batchCalls["store-apply:store_apply:ok"])
	require.Equal(t, 2, obs.batchItems["store-apply:store_apply:ok"])
	require.ElementsMatch(t, []ch.OpID{1, 2}, resultOpIDs(sink.Results()))
	require.ElementsMatch(t, []uint64{1, 3}, resultStoreApplyLEOs(sink.Results()))
}

func TestPoolBatchesStoreAppendTasksWhenFactorySupportsBatch(t *testing.T) {
	sink := &captureSink{ch: make(chan Result, 2)}
	stores := &batchAppendStoreFactory{}
	obs := &recordingPoolPressureObserver{}
	pool, err := NewPool(PoolConfig{Name: "store-append", Workers: 1, QueueSize: 8}, Deps{Stores: stores}, sink)
	require.NoError(t, err)
	defer pool.Close()
	pool.SetQueueObserver(obs)

	first := Task{
		Kind:        TaskStoreAppend,
		Fence:       ch.Fence{ChannelKey: "1:a", OpID: 1},
		StoreAppend: &StoreAppendTask{ChannelID: ch.ChannelID{ID: "a", Type: 1}, Records: []ch.Record{{ID: 1, Payload: []byte("a")}}},
	}
	second := Task{
		Kind:        TaskStoreAppend,
		Fence:       ch.Fence{ChannelKey: "1:b", OpID: 2},
		StoreAppend: &StoreAppendTask{ChannelID: ch.ChannelID{ID: "b", Type: 1}, Records: []ch.Record{{ID: 2, Payload: []byte("b")}}},
	}
	require.NoError(t, pool.Submit(context.Background(), first))
	require.NoError(t, pool.Submit(context.Background(), second))

	require.Eventually(t, func() bool { return sink.Len() == 2 }, time.Second, time.Millisecond)
	require.Equal(t, 1, stores.BatchCalls())
	require.Equal(t, 0, stores.SingleAppendCalls())
	require.Equal(t, 1, obs.batchCalls["store-append:store_append:ok"])
	require.Equal(t, 2, obs.batchItems["store-append:store_append:ok"])
	require.ElementsMatch(t, []ch.OpID{1, 2}, resultOpIDs(sink.Results()))
	require.ElementsMatch(t, []uint64{1, 1}, resultStoreAppendBaseOffsets(sink.Results()))
	require.ElementsMatch(t, []uint64{1, 1}, resultStoreAppendLastOffsets(sink.Results()))
}

func TestPoolUsesConfiguredStoreAppendBatchMaxWait(t *testing.T) {
	pool := &Pool{
		cfg:   PoolConfig{Name: "store-append", BatchMaxWait: time.Hour},
		deps:  Deps{Stores: &batchAppendStoreFactory{}},
		queue: make(chan queuedTask, 2),
		stop:  make(chan struct{}),
	}
	first := queuedStoreAppendTask("a", 1)
	go func() {
		time.Sleep(2 * time.Millisecond)
		pool.queue <- queuedStoreAppendTask("b", 2)
	}()

	groups := pool.taskGroups(first)

	require.Len(t, groups, 1)
	require.Len(t, groups[0], 2)
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

func queuedStoreAppendTask(channel string, opID ch.OpID) queuedTask {
	return queuedTask{enqueuedAt: time.Now(), task: Task{
		Kind:        TaskStoreAppend,
		Fence:       ch.Fence{ChannelKey: ch.ChannelKey("1:" + channel), OpID: opID},
		StoreAppend: &StoreAppendTask{ChannelID: ch.ChannelID{ID: channel, Type: 1}, Records: []ch.Record{{ID: uint64(opID), Payload: []byte(channel)}}},
	}}
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

func (s *captureSink) Results() []Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Result, len(s.results))
	copy(out, s.results)
	return out
}

func resultOpIDs(results []Result) []ch.OpID {
	out := make([]ch.OpID, 0, len(results))
	for _, result := range results {
		out = append(out, result.Fence.OpID)
	}
	return out
}

func resultByOpID(t *testing.T, results []Result, opID ch.OpID) Result {
	t.Helper()
	for _, result := range results {
		if result.Fence.OpID == opID {
			return result
		}
	}
	t.Fatalf("missing result for op id %d in %#v", opID, results)
	return Result{}
}

func resultContainsOpID(results []Result, opID ch.OpID) bool {
	for _, result := range results {
		if result.Fence.OpID == opID {
			return true
		}
	}
	return false
}

func resultStoreApplyLEOs(results []Result) []uint64 {
	out := make([]uint64, 0, len(results))
	for _, result := range results {
		if result.StoreApply == nil {
			continue
		}
		out = append(out, result.StoreApply.LEO)
	}
	return out
}

func resultStoreAppendBaseOffsets(results []Result) []uint64 {
	out := make([]uint64, 0, len(results))
	for _, result := range results {
		if result.StoreAppend == nil {
			continue
		}
		out = append(out, result.StoreAppend.BaseOffset)
	}
	return out
}

func resultStoreAppendLastOffsets(results []Result) []uint64 {
	out := make([]uint64, 0, len(results))
	for _, result := range results {
		if result.StoreAppend == nil {
			continue
		}
		out = append(out, result.StoreAppend.LastOffset)
	}
	return out
}

type batchWorkerTransport struct {
	mu                 sync.Mutex
	pullCalls          int
	pullBatchCalls     int
	pullHintCalls      int
	pullHintBatchCalls int
}

func (t *batchWorkerTransport) Pull(_ context.Context, _ ch.NodeID, req transport.PullRequest) (transport.PullResponse, error) {
	t.mu.Lock()
	t.pullCalls++
	t.mu.Unlock()
	return transport.PullResponse{ChannelKey: req.ChannelKey, LeaderHW: req.NextOffset, LeaderLEO: req.NextOffset}, nil
}

func (t *batchWorkerTransport) PullBatch(_ context.Context, _ ch.NodeID, req transport.PullBatchRequest) (transport.PullBatchResponse, error) {
	t.mu.Lock()
	t.pullBatchCalls++
	t.mu.Unlock()
	resp := transport.PullBatchResponse{Items: make([]transport.PullBatchItemResult, len(req.Items))}
	for i, item := range req.Items {
		resp.Items[i].Response = transport.PullResponse{ChannelKey: item.ChannelKey, LeaderHW: item.NextOffset, LeaderLEO: item.NextOffset}
	}
	return resp, nil
}

func (t *batchWorkerTransport) Ack(context.Context, ch.NodeID, transport.AckRequest) error {
	return nil
}

func (t *batchWorkerTransport) PullHint(context.Context, ch.NodeID, transport.PullHintRequest) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pullHintCalls++
	return nil
}

func (t *batchWorkerTransport) PullHintBatch(_ context.Context, _ ch.NodeID, req transport.PullHintBatchRequest) (transport.PullHintBatchResponse, error) {
	t.mu.Lock()
	t.pullHintBatchCalls++
	t.mu.Unlock()
	return transport.PullHintBatchResponse{Items: make([]transport.PullHintBatchItemResult, len(req.Items))}, nil
}

func (t *batchWorkerTransport) Notify(context.Context, ch.NodeID, transport.NotifyRequest) error {
	return nil
}

func (t *batchWorkerTransport) PullCalls() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.pullCalls
}

func (t *batchWorkerTransport) PullBatchCalls() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.pullBatchCalls
}

func (t *batchWorkerTransport) PullHintCalls() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.pullHintCalls
}

func (t *batchWorkerTransport) PullHintBatchCalls() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.pullHintBatchCalls
}

type blockingPullTransport struct {
	batchWorkerTransport
	blockedNode ch.NodeID
	started     chan struct{}
	release     chan struct{}
	startOnce   sync.Once
	releaseOnce sync.Once
}

func newBlockingPullTransport(blockedNode ch.NodeID) *blockingPullTransport {
	return &blockingPullTransport{
		blockedNode: blockedNode,
		started:     make(chan struct{}),
		release:     make(chan struct{}),
	}
}

func (t *blockingPullTransport) Pull(ctx context.Context, node ch.NodeID, req transport.PullRequest) (transport.PullResponse, error) {
	if node == t.blockedNode {
		t.startOnce.Do(func() { close(t.started) })
		select {
		case <-t.release:
		case <-ctx.Done():
			return transport.PullResponse{}, ctx.Err()
		}
	}
	return t.batchWorkerTransport.Pull(ctx, node, req)
}

func (t *blockingPullTransport) Started() <-chan struct{} {
	return t.started
}

func (t *blockingPullTransport) Release() {
	t.releaseOnce.Do(func() { close(t.release) })
}

type batchApplyStoreFactory struct {
	mu               sync.Mutex
	batchCalls       int
	singleApplyCalls int
}

func (f *batchApplyStoreFactory) ChannelStore(ch.ChannelKey, ch.ChannelID) (store.ChannelStore, error) {
	return &batchApplySingleStore{factory: f}, nil
}

func (f *batchApplyStoreFactory) ApplyFollowerBatch(_ context.Context, items []store.ApplyFollowerBatchItem) []store.ApplyFollowerBatchResult {
	f.mu.Lock()
	f.batchCalls++
	f.mu.Unlock()
	results := make([]store.ApplyFollowerBatchResult, len(items))
	for i, item := range items {
		if len(item.Request.Records) > 0 {
			results[i].LEO = item.Request.Records[len(item.Request.Records)-1].Index
		}
	}
	return results
}

func (f *batchApplyStoreFactory) BatchCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.batchCalls
}

func (f *batchApplyStoreFactory) SingleApplyCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.singleApplyCalls
}

type batchApplySingleStore struct {
	factory *batchApplyStoreFactory
}

func (s *batchApplySingleStore) Load(context.Context) (store.InitialState, error) {
	return store.InitialState{}, nil
}

func (s *batchApplySingleStore) AppendLeader(context.Context, store.AppendLeaderRequest) (store.AppendLeaderResult, error) {
	return store.AppendLeaderResult{}, nil
}

func (s *batchApplySingleStore) ApplyFollower(_ context.Context, req store.ApplyFollowerRequest) (store.ApplyFollowerResult, error) {
	s.factory.mu.Lock()
	s.factory.singleApplyCalls++
	s.factory.mu.Unlock()
	if len(req.Records) == 0 {
		return store.ApplyFollowerResult{}, nil
	}
	return store.ApplyFollowerResult{LEO: req.Records[len(req.Records)-1].Index}, nil
}

func (s *batchApplySingleStore) ReadCommitted(context.Context, store.ReadCommittedRequest) (store.ReadCommittedResult, error) {
	return store.ReadCommittedResult{}, nil
}

func (s *batchApplySingleStore) ReadLog(context.Context, store.ReadLogRequest) (store.ReadLogResult, error) {
	return store.ReadLogResult{}, nil
}

func (s *batchApplySingleStore) StoreCheckpoint(context.Context, ch.Checkpoint) error { return nil }

func (s *batchApplySingleStore) Close() error { return nil }

type batchAppendStoreFactory struct {
	mu                sync.Mutex
	batchCalls        int
	singleAppendCalls int
}

func (f *batchAppendStoreFactory) ChannelStore(ch.ChannelKey, ch.ChannelID) (store.ChannelStore, error) {
	return &batchAppendSingleStore{factory: f}, nil
}

func (f *batchAppendStoreFactory) AppendLeaderBatch(_ context.Context, items []store.AppendLeaderBatchItem) []store.AppendLeaderBatchResult {
	f.mu.Lock()
	f.batchCalls++
	f.mu.Unlock()
	results := make([]store.AppendLeaderBatchResult, len(items))
	for i, item := range items {
		if len(item.Request.Records) == 0 {
			continue
		}
		results[i].BaseOffset = 1
		results[i].LastOffset = uint64(len(item.Request.Records))
	}
	return results
}

func (f *batchAppendStoreFactory) BatchCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.batchCalls
}

func (f *batchAppendStoreFactory) SingleAppendCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.singleAppendCalls
}

type batchAppendSingleStore struct {
	factory *batchAppendStoreFactory
}

func (s *batchAppendSingleStore) Load(context.Context) (store.InitialState, error) {
	return store.InitialState{}, nil
}

func (s *batchAppendSingleStore) AppendLeader(_ context.Context, req store.AppendLeaderRequest) (store.AppendLeaderResult, error) {
	s.factory.mu.Lock()
	s.factory.singleAppendCalls++
	s.factory.mu.Unlock()
	if len(req.Records) == 0 {
		return store.AppendLeaderResult{}, nil
	}
	return store.AppendLeaderResult{BaseOffset: 1, LastOffset: uint64(len(req.Records))}, nil
}

func (s *batchAppendSingleStore) ApplyFollower(context.Context, store.ApplyFollowerRequest) (store.ApplyFollowerResult, error) {
	return store.ApplyFollowerResult{}, nil
}

func (s *batchAppendSingleStore) ReadCommitted(context.Context, store.ReadCommittedRequest) (store.ReadCommittedResult, error) {
	return store.ReadCommittedResult{}, nil
}

func (s *batchAppendSingleStore) ReadLog(context.Context, store.ReadLogRequest) (store.ReadLogResult, error) {
	return store.ReadLogResult{}, nil
}

func (s *batchAppendSingleStore) StoreCheckpoint(context.Context, ch.Checkpoint) error {
	return nil
}

func (s *batchAppendSingleStore) Close() error { return nil }

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
	batchCalls map[string]int
	batchItems map[string]int
	antsUsage  map[string]recordedAntsUsage
}

type recordedAntsUsage struct {
	running  int
	capacity int
	waiting  int
}

func (o *recordingPoolPressureObserver) ensure() {
	if o.workers == nil {
		o.workers = make(map[string]int)
		o.capacity = make(map[string]int)
		o.admissions = make(map[string]int)
		o.waits = make(map[string]time.Duration)
		o.tasks = make(map[string]time.Duration)
		o.batchCalls = make(map[string]int)
		o.batchItems = make(map[string]int)
		o.antsUsage = make(map[string]recordedAntsUsage)
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

func (o *recordingPoolPressureObserver) SetWorkerAntsPoolUsage(pool string, running int, capacity int, waiting int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.ensure()
	o.antsUsage[pool] = recordedAntsUsage{running: running, capacity: capacity, waiting: waiting}
}

func (o *recordingPoolPressureObserver) AntsUsage(pool string) recordedAntsUsage {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.antsUsage[pool]
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

func (o *recordingPoolPressureObserver) ObserveWorkerBatch(pool string, kind TaskKind, items int, err error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.ensure()
	result := "ok"
	if err != nil {
		result = "err"
	}
	key := pool + ":" + taskKindTestLabel(kind) + ":" + result
	o.batchCalls[key]++
	o.batchItems[key] += items
}

func taskKindTestLabel(kind TaskKind) string {
	switch kind {
	case TaskFunc:
		return "func"
	case TaskStoreAppend:
		return "store_append"
	case TaskRPCPull:
		return "rpc_pull"
	case TaskRPCPullHint:
		return "rpc_pull_hint"
	case TaskStoreApply:
		return "store_apply"
	default:
		return "other"
	}
}
