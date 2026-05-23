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
	pool, err := NewPool(PoolConfig{Name: "test", Workers: 1, QueueSize: 1}, sink)
	require.NoError(t, err)
	defer pool.Close()

	fence := ch.Fence{ChannelKey: ch.ChannelKey("1:a"), Generation: 1, Epoch: 1, LeaderEpoch: 1, OpID: 1}
	err = pool.Submit(context.Background(), Task{Kind: TaskFunc, Fence: fence, RunFunc: func(context.Context) Result { return Result{Fence: fence} }})
	require.NoError(t, err)
	require.Eventually(t, func() bool { return sink.Len() == 1 }, time.Second, time.Millisecond)
}

func TestPoolReturnsBackpressureWhenQueueFull(t *testing.T) {
	sink := &captureSink{}
	pool, err := NewPool(PoolConfig{Name: "test", Workers: 1, QueueSize: 1}, sink)
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
