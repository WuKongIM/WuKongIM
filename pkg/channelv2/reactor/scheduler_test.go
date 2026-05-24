package reactor

import (
	"strconv"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/worker"
	"github.com/stretchr/testify/require"
)

func TestDueSchedulerPopsDueItemsInOrder(t *testing.T) {
	var s dueScheduler
	now := time.Unix(10, 0)
	s.push(dueItem{key: ch.ChannelKey("b"), kind: dueReplication, due: now.Add(2 * time.Second), version: 1})
	s.push(dueItem{key: ch.ChannelKey("a"), kind: dueAppendFlush, due: now.Add(time.Second), version: 1})

	item, ok := s.popDue(now.Add(time.Second))
	require.True(t, ok)
	require.Equal(t, ch.ChannelKey("a"), item.key)
	_, ok = s.popDue(now.Add(time.Second))
	require.False(t, ok)
}

func TestDueSchedulerNextWait(t *testing.T) {
	var s dueScheduler
	now := time.Unix(10, 0)
	s.push(dueItem{key: ch.ChannelKey("a"), kind: dueAppendFlush, due: now.Add(2 * time.Second), version: 1})
	require.Equal(t, 2*time.Second, s.nextWait(now))
	require.Equal(t, time.Duration(0), s.nextWait(now.Add(3*time.Second)))
}

func TestDueSchedulerLifecycleTickDoesNotScanLoadedIdleLeaders(t *testing.T) {
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 64)}
	pools := newDirectTestPools(t, factory, sink)
	defer pools.Close()

	now := time.Unix(100, 0)
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16, IdleEvictAfter: time.Second, IdleEvictCheckInterval: time.Hour})
	const loadedLeaders = 32
	var dueKey ch.ChannelKey
	for i := 0; i < loadedLeaders; i++ {
		meta := testMeta("idle-leader-"+strconv.Itoa(i), 1, 1)
		require.NoError(t, applyMetaDirect(t, r, meta))
		rc := r.channels[meta.Key]
		require.NotNil(t, rc)
		rc.lifecycle.LastAppendAt = now.Add(-2 * time.Second)
		if i == loadedLeaders/2 {
			dueKey = meta.Key
			rc.lifecycleDueVersion++
			r.due.push(dueItem{key: meta.Key, kind: dueLifecycle, due: now, version: rc.lifecycleDueVersion})
		}
	}

	r.processDue(now)
	checkpoint := sink.awaitResultKind(t, worker.TaskStoreCheckpoint)
	require.Equal(t, dueKey, checkpoint.Fence.ChannelKey)
	requireNoWorkerResultKind(t, sink.results, worker.TaskStoreCheckpoint)
}
