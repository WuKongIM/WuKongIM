package channelmeta

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
)

func TestSlotRefreshSchedulerRerunsWhenDirty(t *testing.T) {
	var scheduler SlotRefreshScheduler
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	block := make(chan struct{})
	var calls int32
	key := channel.ChannelKey("1:dirty")

	scheduler.Schedule(ctx, &wg, 3, func(multiraft.SlotID) []channel.ChannelKey {
		return []channel.ChannelKey{key}
	}, func(context.Context, channel.ChannelKey) {
		atomic.AddInt32(&calls, 1)
		<-block
	}, 5*time.Second)

	require.Eventually(t, func() bool { return atomic.LoadInt32(&calls) == 1 }, time.Second, time.Millisecond)
	scheduler.Schedule(ctx, &wg, 3, func(multiraft.SlotID) []channel.ChannelKey {
		return []channel.ChannelKey{key}
	}, func(context.Context, channel.ChannelKey) {
		atomic.AddInt32(&calls, 1)
	}, 5*time.Second)
	close(block)

	require.Eventually(t, func() bool { return atomic.LoadInt32(&calls) >= 2 }, time.Second, time.Millisecond)
	cancel()
	wg.Wait()
}

func TestSlotRefreshSchedulerStopsOnContextCancel(t *testing.T) {
	var scheduler SlotRefreshScheduler
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	started := make(chan struct{})

	scheduler.Schedule(ctx, &wg, 9, func(multiraft.SlotID) []channel.ChannelKey {
		return []channel.ChannelKey{"1:stop"}
	}, func(ctx context.Context, _ channel.ChannelKey) {
		close(started)
		<-ctx.Done()
	}, 5*time.Second)

	<-started
	cancel()
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		require.Fail(t, "slot refresh did not stop after context cancellation")
	}
}

func TestObserveLocalReplicaStateChangeSchedulesLeaderDriftRefresh(t *testing.T) {
	key := channel.ChannelKey("1:drift")
	meta := channel.Meta{Key: key, Epoch: 4, Leader: 3, ISR: []channel.NodeID{2, 3}, Status: channel.StatusActive}
	runtime := localRuntimeFake{channels: map[channel.ChannelKey]ChannelObserver{
		key: channelObserverFake{meta: meta, state: channel.ReplicaState{ChannelKey: key, Epoch: 4, Leader: 2, Role: channel.ReplicaRoleLeader}},
	}}
	var tracked bool
	var scheduled multiraft.SlotID

	ObserveLocalReplicaStateChange(LocalReplicaStateChange{
		Key:     key,
		Runtime: runtime,
		Track:   func(channel.ChannelKey) { tracked = true },
		Untrack: func(channel.ChannelKey) {},
		SlotForKey: func(channel.ChannelKey) (multiraft.SlotID, bool) {
			return 12, true
		},
		ScheduleSlotRefresh: func(slotID multiraft.SlotID) { scheduled = slotID },
	})

	require.True(t, tracked)
	require.Equal(t, multiraft.SlotID(12), scheduled)
}

func TestObserveLocalReplicaStateChangeUntracksTombstonedReplica(t *testing.T) {
	key := channel.ChannelKey("1:tombstone")
	runtime := localRuntimeFake{channels: map[channel.ChannelKey]ChannelObserver{
		key: channelObserverFake{state: channel.ReplicaState{ChannelKey: key, Role: channel.ReplicaRoleTombstoned}},
	}}
	var untracked bool

	ObserveLocalReplicaStateChange(LocalReplicaStateChange{
		Key:     key,
		Runtime: runtime,
		Track:   func(channel.ChannelKey) {},
		Untrack: func(channel.ChannelKey) { untracked = true },
	})

	require.True(t, untracked)
}

func TestWatchLocalReplicaStateChangesObservesEnqueuedKeys(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	changes := make(chan channel.ChannelKey, 1)
	observed := make(chan channel.ChannelKey, 1)

	go WatchLocalReplicaStateChanges(ctx, changes, func(key channel.ChannelKey) {
		observed <- key
	})
	EnqueueLocalReplicaStateChange(changes, "1:watch")

	select {
	case got := <-observed:
		require.Equal(t, channel.ChannelKey("1:watch"), got)
	case <-time.After(time.Second):
		require.Fail(t, "state change was not observed")
	}
}

type localRuntimeFake struct {
	channels map[channel.ChannelKey]ChannelObserver
}

func (f localRuntimeFake) Channel(key channel.ChannelKey) (ChannelObserver, bool) {
	ch, ok := f.channels[key]
	return ch, ok
}

type channelObserverFake struct {
	meta  channel.Meta
	state channel.ReplicaState
}

func (f channelObserverFake) Meta() channel.Meta           { return f.meta }
func (f channelObserverFake) Status() channel.ReplicaState { return f.state }

func TestSlotRefreshSchedulerSkipsSlotsWithoutActiveKeys(t *testing.T) {
	var scheduler SlotRefreshScheduler
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	var calls int32

	scheduler.Schedule(ctx, &wg, 4, func(multiraft.SlotID) []channel.ChannelKey {
		return nil
	}, func(context.Context, channel.ChannelKey) {
		atomic.AddInt32(&calls, 1)
	}, 5*time.Second)

	wg.Wait()
	require.Equal(t, int32(0), atomic.LoadInt32(&calls))
}
