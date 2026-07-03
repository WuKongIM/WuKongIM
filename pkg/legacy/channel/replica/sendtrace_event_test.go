package replica

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
	"github.com/stretchr/testify/require"
)

type traceEventSink struct {
	mu     sync.Mutex
	events []sendtrace.Event
}

func (s *traceEventSink) RecordSendTrace(event sendtrace.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
}

func (s *traceEventSink) Snapshot() []sendtrace.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]sendtrace.Event(nil), s.events...)
}

func TestLeaderAppendCommittedTraceRecordsOKResults(t *testing.T) {
	r := newLeaderReplica(t)
	startedAt := time.Unix(1700000000, 0).UTC()
	durableStartedAt := startedAt.Add(10 * time.Millisecond)
	durableDoneAt := durableStartedAt.Add(5 * time.Millisecond)
	quorumDoneAt := durableDoneAt.Add(7 * time.Millisecond)
	r.now = func() time.Time { return quorumDoneAt }

	waiter := &appendWaiter{ch: make(chan appendCompletion, 1), enqueuedAt: startedAt}
	req := &appendRequest{
		requestID:  1,
		ctx:        channel.WithCommitMode(context.Background(), channel.CommitModeQuorum),
		batch:      []channel.Record{{Payload: []byte("a"), SizeBytes: 1}},
		commitMode: channel.CommitModeQuorum,
		waiter:     waiter,
		stage:      appendRequestDurable,
	}
	waiter.request = req

	r.mu.Lock()
	r.appendRequests = map[uint64]*appendRequest{req.requestID: req}
	r.appendInFlightIDs = []uint64{req.requestID}
	r.appendInFlightEffectID = 10
	r.mu.Unlock()

	sink := &traceEventSink{}
	restore := sendtrace.SetSink(sink)
	defer restore()

	st := r.Status()
	result := r.applyLoopEvent(machineLeaderAppendCommittedEvent{
		EffectID:         10,
		ChannelKey:       st.ChannelKey,
		Epoch:            st.Epoch,
		LeaderEpoch:      r.meta.LeaderEpoch,
		RoleGeneration:   r.roleGeneration,
		RequestIDs:       []uint64{req.requestID},
		BaseOffset:       0,
		DurableStartedAt: durableStartedAt,
		DoneAt:           durableDoneAt,
	})
	require.NoError(t, result.Err)

	events := sink.Snapshot()
	require.Len(t, events, 3)
	require.Equal(t, sendtrace.StageReplicaLeaderQueueWait, events[0].Stage)
	require.Equal(t, sendtrace.StageReplicaLeaderLocalDurable, events[1].Stage)
	require.Equal(t, sendtrace.StageReplicaLeaderQuorumWait, events[2].Stage)
	for _, event := range events {
		require.Equal(t, sendtrace.ResultOK, event.Result)
		require.Empty(t, event.ErrorCode)
		require.Empty(t, event.Error)
		require.Equal(t, uint64(r.localNode), event.NodeID)
		require.Equal(t, string(st.ChannelKey), event.ChannelKey)
		require.Equal(t, uint64(1), event.RangeStart)
		require.Equal(t, uint64(1), event.RangeEnd)
	}
}

func TestFollowerApplyTraceRecordsErrorDetails(t *testing.T) {
	env := newFollowerEnv(t)
	startedAt := time.Unix(1700000100, 0).UTC()
	finishedAt := startedAt.Add(5 * time.Millisecond)
	nowCalls := 0
	env.replica.now = func() time.Time {
		nowCalls++
		if nowCalls == 1 {
			return startedAt
		}
		return finishedAt
	}
	spy := &spyDurableStore{applyErr: errors.New("boom")}
	env.replica.durable = spy

	sink := &traceEventSink{}
	restore := sendtrace.SetSink(sink)
	defer restore()

	err := env.replica.ApplyFetch(context.Background(), channel.ReplicaApplyFetchRequest{
		ChannelKey: "group-10",
		Epoch:      7,
		Leader:     1,
		Records:    []channel.Record{{Index: 1, Payload: []byte("a"), SizeBytes: 1}},
		LeaderHW:   1,
	})
	require.EqualError(t, err, "boom")

	events := sink.Snapshot()
	require.Len(t, events, 1)
	event := events[0]
	require.Equal(t, sendtrace.StageReplicaFollowerApplyDurable, event.Stage)
	require.Equal(t, sendtrace.ResultError, event.Result)
	require.Equal(t, "unknown_error", event.ErrorCode)
	require.Equal(t, "boom", event.Error)
	require.Equal(t, uint64(env.replica.localNode), event.NodeID)
	require.Equal(t, uint64(1), event.PeerNodeID)
	require.Equal(t, "group-10", event.ChannelKey)
	require.Equal(t, uint64(1), event.RangeStart)
	require.Equal(t, uint64(1), event.RangeEnd)
	require.Equal(t, startedAt, event.At)
	require.Equal(t, finishedAt.Sub(startedAt), event.Duration)
}

func TestLeaderAppendEffectTraceSplitsDurableMutexAndStore(t *testing.T) {
	env := newTestEnv(t)
	r := newReplicaFromEnv(t, env)
	r.mu.Lock()
	r.state.ChannelKey = "group-trace"
	r.state.Epoch = 1
	r.state.Role = channel.ReplicaRoleLeader
	r.state.CommitReady = true
	r.state.LEO = 0
	r.meta = channel.Meta{Key: "group-trace", Epoch: 1, LeaderEpoch: 1, Leader: r.localNode, LeaseUntil: time.Now().Add(time.Hour), ISR: []channel.NodeID{r.localNode}, MinISR: 1}
	r.roleGeneration = 1
	r.appendInFlightEffectID = 11
	r.appendInFlightIDs = []uint64{1}
	r.mu.Unlock()

	sink := &traceEventSink{}
	restore := sendtrace.SetSink(sink)
	defer restore()

	r.runAppendEffect(context.Background(), appendLeaderBatchEffect{
		EffectID:       11,
		RequestIDs:     []uint64{1},
		ChannelKey:     "group-trace",
		Epoch:          1,
		LeaderEpoch:    1,
		RoleGeneration: 1,
		LeaseUntil:     r.meta.LeaseUntil,
		Records: []channel.Record{
			{Index: 1, Payload: []byte("abc"), SizeBytes: 3},
			{Index: 2, Payload: []byte("defg"), SizeBytes: 4},
		},
		StartedAt: time.Now(),
	})

	events := sink.Snapshot()
	mutexWait := findReplicaTraceEvent(events, sendtrace.StageReplicaLeaderDurableMuWait)
	require.NotNil(t, mutexWait, "missing durable mutex wait event in %#v", events)
	require.Equal(t, sendtrace.ResultOK, mutexWait.Result)
	require.Equal(t, 2, mutexWait.RecordCount)
	require.Equal(t, 7, mutexWait.ByteCount)

	appendStore := findReplicaTraceEvent(events, sendtrace.StageReplicaLeaderDurableAppend)
	require.NotNil(t, appendStore, "missing durable append store event in %#v", events)
	require.Equal(t, sendtrace.ResultOK, appendStore.Result)
	require.Equal(t, "group-trace", appendStore.ChannelKey)
	require.Equal(t, 2, appendStore.RecordCount)
	require.Equal(t, 7, appendStore.ByteCount)
}

func findReplicaTraceEvent(events []sendtrace.Event, stage sendtrace.Stage) *sendtrace.Event {
	for i := range events {
		if events[i].Stage == stage {
			return &events[i]
		}
	}
	return nil
}
