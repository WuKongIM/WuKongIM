package runtime

import (
	"sync/atomic"
	"testing"
	"time"

	core "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

func TestSchedulerPrefersHighPriorityWork(t *testing.T) {
	s := newScheduler()
	s.enqueue("channel/1/YQ==", PriorityLow)
	s.enqueue("channel/1/Yg==", PriorityHigh)

	first, ok := s.popReady()
	require.True(t, ok)
	require.Equal(t, PriorityHigh, first.priority)
	require.Equal(t, core.ChannelKey("channel/1/Yg=="), first.key)
}

func TestSchedulerDoesNotRunSameKeyConcurrently(t *testing.T) {
	s := newScheduler()
	key := core.ChannelKey("channel/1/YQ==")
	s.enqueue(key, PriorityNormal)
	first, ok := s.popReady()
	require.True(t, ok)
	require.Equal(t, key, first.key)

	s.enqueue(key, PriorityHigh)

	next, ok := s.popReady()
	require.False(t, ok)
	require.True(t, s.done(key))

	s.requeue(key)
	next, ok = s.popReady()
	require.True(t, ok)
	require.Equal(t, key, next.key)
	require.Equal(t, PriorityHigh, next.priority)
}

func TestSchedulerAutoRunSerializesSameKeyProcessing(t *testing.T) {
	env := newSessionTestEnvWithConfig(t, func(cfg *Config) {
		cfg.AutoRunScheduler = true
	})
	key := testChannelKey(90901)
	mustEnsureLocal(t, env.runtime, core.Meta{
		Key:      key,
		Epoch:    1,
		Leader:   1,
		Replicas: []core.NodeID{1, 2},
		ISR:      []core.NodeID{1, 2},
		MinISR:   1,
	})

	ch, ok := env.runtime.lookupChannel(key)
	require.True(t, ok)

	pausePop := make(chan struct{})
	popBlocked := make(chan struct{}, 1)
	var popCount atomic.Int32
	env.runtime.schedulerPopHook = func(popKey core.ChannelKey) {
		if popKey != key {
			return
		}
		if popCount.Add(1) != 1 {
			return
		}
		popBlocked <- struct{}{}
		<-pausePop
	}

	enter := make(chan struct{}, 2)
	releaseFirst := make(chan struct{})
	var calls atomic.Int32
	var active atomic.Int32
	var maxActive atomic.Int32
	ch.delegate = channelDelegateFunc{
		onReplication: func(core.ChannelKey) {
			current := active.Add(1)
			for {
				observed := maxActive.Load()
				if current <= observed || maxActive.CompareAndSwap(observed, current) {
					break
				}
			}
			call := calls.Add(1)
			enter <- struct{}{}
			if call == 1 {
				<-releaseFirst
			}
			active.Add(-1)
		},
	}

	ch.markReplication()
	env.runtime.enqueueScheduler(key, PriorityNormal)
	<-popBlocked

	env.runtime.enqueueScheduler(key, PriorityNormal)

	select {
	case <-enter:
		t.Fatal("same key entered processing before the first scheduled handler began")
	case <-time.After(30 * time.Millisecond):
	}

	close(pausePop)
	require.Eventually(t, func() bool {
		return calls.Load() >= 1
	}, time.Second, 10*time.Millisecond)

	ch.markReplication()
	env.runtime.enqueueScheduler(key, PriorityNormal)
	close(releaseFirst)

	require.Eventually(t, func() bool {
		return calls.Load() == 2
	}, time.Second, 10*time.Millisecond)
	require.Equal(t, int32(1), maxActive.Load())
}

type channelDelegateFunc struct {
	onReplication func(core.ChannelKey)
	onSnapshot    func(core.ChannelKey)
}

func (f channelDelegateFunc) OnReplication(key core.ChannelKey) {
	if f.onReplication != nil {
		f.onReplication(key)
	}
}

func (f channelDelegateFunc) OnSnapshot(key core.ChannelKey) {
	if f.onSnapshot != nil {
		f.onSnapshot(key)
	}
}
