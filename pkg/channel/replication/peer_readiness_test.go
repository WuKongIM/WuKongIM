package replication

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

func readyTestBatcher(t *testing.T, executor *manualPeerExecutor, link PeerLink) *peerBatcher {
	t.Helper()
	b, err := newPeerBatcher(peerBatcherConfig{Link: link, Executor: executor, OwnerContext: context.Background(), ExchangeTimeout: time.Minute, MaxBatchItems: 4, MaxBatchBytes: 4096, MaxTargetFlight: 2, MaxQueuedItems: 16, MaxQueuedBytes: 16384, MaxTargetQueuedItems: 8, MaxTargetQueuedBytes: 8192})
	require.NoError(t, err)
	return b
}

// The executor is deliberately stepped, so an empty drain's self-rescheduling
// can be detected without a timing window or letting it consume a CPU.
func TestPeerReadinessStopsEmptyRescheduleAndAllowsIndependentChannel(t *testing.T) {
	executor := &manualPeerExecutor{}
	link := &priorityPeerLink{blockKey: "1:hot", started: make(chan struct{}), release: make(chan struct{})}
	b := readyTestBatcher(t, executor, link)
	completed := make(chan string, 4)
	submit := func(label string, key ch.ChannelKey) {
		t.Helper()
		req := testReplicateRequest(t, key, string(key), 1, []byte(label))
		require.NoError(t, b.submit(context.Background(), 2, req, func(result ReplicateResult, err error) {
			if err != nil || result.Status != ReplicateDurable {
				t.Errorf("%s completion = %+v, %v", label, result, err)
			}
			completed <- label
		}))
	}
	submit("hot-first", "1:hot")
	joined := make(chan struct{})
	go func() { executor.RunNext(); close(joined) }()
	<-link.started
	var once sync.Once
	release := func() { once.Do(func() { close(link.release) }) }
	t.Cleanup(func() {
		release()
		<-joined
		for i := 0; i < 16 && executor.Len() > 0; i++ {
			executor.RunNext()
		}
	})
	submit("hot-second", "1:hot")
	if executor.Len() > 0 {
		executor.RunNext()
	}
	require.Zero(t, executor.Len(), "an empty drain must not reschedule itself")
	submit("independent", "1:other")
	require.Equal(t, 1, executor.Len(), "independent channel must not wait for the hot channel")
	executor.RunNext()
	require.Equal(t, "independent", <-completed)
	require.Zero(t, executor.Len(), "independent completion must not spin on blocked hot work")
	release()
	<-joined
	for i := 0; i < 16 && executor.Len() > 0; i++ {
		executor.RunNext()
	}
	require.Equal(t, "hot-first", <-completed)
	require.Equal(t, "hot-second", <-completed)
	require.Zero(t, b.ownedItems)
	require.Zero(t, b.ownedBytes)
}

type readinessPeerLinkFunc func(context.Context, ch.NodeID, ExchangeBatch) (ExchangeBatchResult, error)

func (f readinessPeerLinkFunc) Exchange(ctx context.Context, node ch.NodeID, batch ExchangeBatch) (ExchangeBatchResult, error) {
	return f(ctx, node, batch)
}

func TestPeerReadinessReleaseWakesBlockedBackground(t *testing.T) {
	executor := &manualPeerExecutor{}
	var b *peerBatcher
	calls, completed := 0, 0
	request := testReplicateRequest(t, "1:same", "same", 1, []byte("payload"))
	link := readinessPeerLinkFunc(func(_ context.Context, _ ch.NodeID, batch ExchangeBatch) (ExchangeBatchResult, error) {
		calls++
		if calls == 1 {
			require.NoError(t, b.submitDeferred(context.Background(), 2, request, func(ReplicateResult, error) { completed++ }))
			require.NoError(t, b.flushDeferred())
			require.Zero(t, executor.Len(), "background exchange is blocked by the active foreground channel")
		}
		out := ExchangeBatchResult{Version: ExchangeVersion}
		for _, item := range batch.Items {
			out.Items = append(out.Items, durableExchangeResult(item))
		}
		return out, nil
	})
	b = readyTestBatcher(t, executor, link)
	require.NoError(t, b.submit(context.Background(), 2, request, func(ReplicateResult, error) {
		completed++
		require.Equal(t, 1, executor.Len(), "release must wake background before foreground owner finishes")
	}))
	executor.RunNext()
	require.Equal(t, 1, executor.Len())
	executor.RunNext()
	require.Equal(t, 2, completed)
	require.Equal(t, 2, calls)
	require.Zero(t, executor.Len())
	require.Zero(t, b.ownedItems)
	require.Zero(t, b.ownedBytes)
}

func TestPeerReadinessPreservesMixedKindBarrier(t *testing.T) {
	executor := &manualPeerExecutor{}
	b := readyTestBatcher(t, executor, &recordingPeerLink{})
	a := testReplicateRequest(t, "1:a", "a", 1, []byte("a"))
	other := testReplicateRequest(t, "1:b", "b", 2, []byte("b"))
	c := testReplicateRequest(t, "1:c", "c", 3, []byte("c"))
	queue := []queuedPeerItem{
		{kind: ExchangeReplicate, replicate: a},
		{kind: ExchangeProbe, probe: ProbeRequest{ChannelKey: other.ChannelKey}},
		{kind: ExchangeReplicate, replicate: other},
	}
	target := &peerTargetQueue{urgent: queue, urgentWorkers: 1, inflight: map[ch.ChannelKey]struct{}{a.ChannelKey: {}}}
	b.targets[2] = target
	require.NoError(t, b.ensureTargetWorkersLocked(2, target))
	require.Zero(t, executor.Len(), "replicate B cannot overtake the earlier probe B")
	target.urgent = append(target.urgent, queuedPeerItem{kind: ExchangeReplicate, replicate: c})
	require.NoError(t, b.ensureTargetWorkersLocked(2, target))
	require.Equal(t, 1, executor.Len(), "independent replicate C remains eligible")
	got := b.takeBatch(2, peerWorkUrgent)
	require.Len(t, got, 1)
	require.Equal(t, c.ChannelKey, got[0].channelKey())
	require.True(t, reflect.DeepEqual(queue, target.urgent), "blocked queue order changed")
}

func TestPeerReadinessEmptyBatchDoesNotAllocate(t *testing.T) {
	b := readyTestBatcher(t, &manualPeerExecutor{}, &recordingPeerLink{})
	request := testReplicateRequest(t, "1:hot", "hot", 1, []byte("payload"))
	b.targets[2] = &peerTargetQueue{urgent: []queuedPeerItem{{kind: ExchangeReplicate, replicate: request}}, inflight: map[ch.ChannelKey]struct{}{request.ChannelKey: {}}}
	allocations := testing.AllocsPerRun(100, func() {
		if len(b.takeBatch(2, peerWorkUrgent)) != 0 {
			t.Fatal("inflight channel entered a new batch")
		}
	})
	require.Zero(t, allocations, "no executable work must not allocate a batch")
}
