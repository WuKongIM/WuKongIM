package worker

import (
	"context"
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
}

func (s *captureSink) Complete(result Result) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.results = append(s.results, result)
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
