package runtime

import (
	"errors"
	"sync"
	"testing"
	"time"

	core "github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
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

func filterTraceEvents(events []sendtrace.Event, stage sendtrace.Stage) []sendtrace.Event {
	filtered := make([]sendtrace.Event, 0, len(events))
	for _, event := range events {
		if event.Stage == stage {
			filtered = append(filtered, event)
		}
	}
	return filtered
}

func TestReplicationRequestTraceRecordsSendFailure(t *testing.T) {
	env := newSessionTestEnv(t)
	mustEnsureLocal(t, env.runtime, testMetaLocal(27, 4, 1, []core.NodeID{1, 2}))

	replica := env.factory.replicas[0]
	replica.mu.Lock()
	replica.state.LEO = 6
	replica.state.Epoch = 4
	replica.state.OffsetEpoch = 4
	replica.mu.Unlock()

	env.sessions.session(2).setSendErr(errors.New("boom"))
	sink := &traceEventSink{}
	restore := sendtrace.SetSink(sink)
	defer restore()

	env.runtime.enqueueReplication(testChannelKey(27), 2)
	env.runtime.runScheduler()

	events := filterTraceEvents(sink.Snapshot(), sendtrace.StageRuntimeFetchRequestSend)
	require.NotEmpty(t, events)
	for _, event := range events {
		require.Equal(t, sendtrace.StageRuntimeFetchRequestSend, event.Stage)
		require.Equal(t, sendtrace.ResultError, event.Result)
		require.Equal(t, "unknown_error", event.ErrorCode)
		require.Equal(t, "boom", event.Error)
		require.Equal(t, "fetch_request", event.Service)
		require.Equal(t, uint64(1), event.NodeID)
		require.Equal(t, uint64(2), event.PeerNodeID)
		require.Equal(t, string(testChannelKey(27)), event.ChannelKey)
	}
}

func TestFollowerRetryScheduledTraceRecordsOKResult(t *testing.T) {
	fixedNow := time.Unix(1700000200, 0).UTC()
	env := newSessionTestEnvWithConfig(t, func(cfg *Config) {
		cfg.Now = func() time.Time { return fixedNow }
		cfg.FollowerReplicationRetryInterval = 15 * time.Millisecond
	})
	mustEnsureLocal(t, env.runtime, testMetaLocal(301, 4, 2, []core.NodeID{1, 2}))

	sink := &traceEventSink{}
	restore := sendtrace.SetSink(sink)
	defer restore()

	env.runtime.scheduleFollowerReplication(testChannelKey(301), 2)

	events := filterTraceEvents(sink.Snapshot(), sendtrace.StageRuntimeFollowerRetryScheduled)
	require.Len(t, events, 1)
	event := events[0]
	require.Equal(t, sendtrace.StageRuntimeFollowerRetryScheduled, event.Stage)
	require.Equal(t, sendtrace.ResultOK, event.Result)
	require.Empty(t, event.ErrorCode)
	require.Empty(t, event.Error)
	require.Equal(t, fixedNow, event.At)
	require.Equal(t, 15*time.Millisecond, event.Duration)
	require.Equal(t, uint64(1), event.NodeID)
	require.Equal(t, uint64(2), event.PeerNodeID)
	require.Equal(t, string(testChannelKey(301)), event.ChannelKey)
}
