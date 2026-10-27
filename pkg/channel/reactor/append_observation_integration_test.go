//go:build integration

package reactor

import (
	"context"
	"sync"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/replication"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
	"github.com/stretchr/testify/require"
)

type reactorObservationKey struct{}
type reactorAppendObservation struct {
	mu    sync.Mutex
	calls map[rpctrace.Stage]int
}

func (o *reactorAppendObservation) BeginAppendPhase(stage rpctrace.Stage) rpctrace.AppendPhaseEnd {
	o.mu.Lock()
	o.calls[stage]++
	o.mu.Unlock()
	return func(rpctrace.Result, int, func(int) rpctrace.Result) {}
}

type observedReactorQuorumLog struct {
	reactorCaptureQuorumLog
	started chan context.Context
	release chan struct{}
	once    sync.Once
}

func (l *observedReactorQuorumLog) unblock() { l.once.Do(func() { close(l.release) }) }
func (l *observedReactorQuorumLog) Commit(ctx context.Context, proposal replication.Proposal) (replication.Receipt, error) {
	l.started <- ctx
	<-l.release
	rpctrace.BeginAppendPhase(ctx, rpctrace.StageChannelRuntimeAppend).End(rpctrace.ResultOK)
	return l.reactorCaptureQuorumLog.Commit(ctx, proposal)
}

func TestAppendObservationSurvivesAdmittedQuorumBackgroundBoundary(t *testing.T) {
	log := &observedReactorQuorumLog{started: make(chan context.Context, 1), release: make(chan struct{})}
	group, err := NewGroup(Config{LocalNode: 1, ReactorCount: 1, MailboxSize: 16, Store: store.NewMemoryFactory(), QuorumLog: log, AppendBatchMaxRecords: 1})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, group.Close()) })
	t.Cleanup(log.unblock)
	meta := testMeta("append-observation-detach", 1, 1)
	meta.RouteGeneration = 7
	meta.Replicas, meta.ISR, meta.MinISR = []ch.NodeID{1, 2, 3}, []ch.NodeID{1, 2, 3}, 2
	require.NoError(t, awaitSubmit(group, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))
	caller, cancel := context.WithCancel(context.WithValue(context.Background(), reactorObservationKey{}, "caller-only"))
	defer cancel()
	observer := &reactorAppendObservation{calls: make(map[rpctrace.Stage]int)}
	event := appendEvent(meta, 11, "observed")
	event.Context = rpctrace.WithAppendPhaseObserver(caller, observer)
	event.Append.CommitMode = ch.CommitModeQuorum
	future, err := group.Submit(caller, meta.Key, event)
	require.NoError(t, err)
	var owned context.Context
	select {
	case owned = <-log.started:
	case <-time.After(time.Second):
		t.Fatal("quorum worker did not start")
	}
	cancel()
	require.NoError(t, owned.Err(), "caller cancellation cannot cancel an admitted mutation")
	require.Nil(t, owned.Value(reactorObservationKey{}))
	log.unblock()
	_ = awaitFutureResult(t, future)
	require.NoError(t, group.Close())
	observer.mu.Lock()
	calls := observer.calls[rpctrace.StageChannelRuntimeAppend]
	observer.mu.Unlock()
	require.Equal(t, 1, calls, "shared append observation must survive the independently owned quorum context")
}
