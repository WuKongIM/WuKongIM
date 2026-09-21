package workload

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/metrics"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/require"
)

type stageDelayClient struct{ *recordingPersonClient }

func (c stageDelayClient) Send(ctx context.Context, p *frame.SendPacket) error {
	time.Sleep(3 * time.Millisecond)
	return c.recordingPersonClient.Send(ctx, p)
}
func (c stageDelayClient) ReadFrame(ctx context.Context) (frame.Frame, error) {
	time.Sleep(17 * time.Millisecond)
	return c.recordingPersonClient.ReadFrame(ctx)
}

func TestSendLatencySeparatesSubmitAndSendackWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		raw := newRecordingPersonClient()
		raw.autoSendack = true
		reg := metrics.NewRegistry()
		w, err := NewPersonWorkload(PersonConfig{RunID: "run", ProfileName: "p", TrafficName: "t", SenderUID: "sender", RecipientUID: "recipient", Metrics: reg}, map[string]PersonClient{"sender": stageDelayClient{raw}, "recipient": newRecordingPersonClient()})
		require.NoError(t, err)
		require.NoError(t, w.SendOne(context.Background(), 1))
		h := reg.Collect().Histograms
		suffix := "{channel_type=person,phase=run,profile=p,traffic=t}"
		for name, want := range map[string]float64{"workload_send_submit_seconds": .003, "workload_sendack_wait_seconds": .017, "workload_operation_seconds": .020, "person_send_latency_seconds": .020} {
			got := h[name+suffix]
			require.Equal(t, uint64(1), got.Count, name)
			require.InDelta(t, want, got.SumSeconds, 1e-9, name)
		}
	})
}

func TestDispatchLagIncludesWaitingForSameSender(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		reg := metrics.NewRegistry()
		labels := metrics.Labels{"phase": "run"}
		stats := newScheduledMessageStats(reg, labels)
		err := runScheduledMessagesByKeyUntilWithStats(context.Background(), 3, 10*time.Millisecond, 2, time.Now().Add(time.Second), func(int) string { return "one-session" }, func(context.Context, int) error { time.Sleep(30 * time.Millisecond); return nil }, stats)
		require.NoError(t, err)
		h := reg.Collect().Histograms["workload_dispatch_lag_seconds{phase=run}"]
		require.Equal(t, uint64(3), h.Count)
		require.InDelta(t, .060, h.SumSeconds, 1e-9)
		require.InDelta(t, .040, h.MaxSeconds, 1e-9)
		require.Equal(t, uint64(3), stats.Dispatched)
	})
}

func TestSchedulerRechecksDeadlineBetweenAdmissions(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := scheduledMessageScheduler{maxConcurrency: 3, stopAt: time.Now().Add(5 * time.Millisecond), startAt: time.Now(), send: func(context.Context, int) error { return nil }, stats: &scheduledMessageStats{dispatchLag: func(time.Duration) { time.Sleep(10 * time.Millisecond) }}}
		for i := 0; i < 3; i++ {
			s.enqueueTask(scheduledMessageTask{offset: i})
		}
		done := make(chan scheduledMessageResult, 3)
		s.dispatch(context.Background(), done)
		require.Equal(t, uint64(1), s.stats.Dispatched, "a slow admission must not admit the remaining batch after the deadline")
		require.Equal(t, 2, s.pendingCount)
		synctest.Wait()
	})
}

func TestClosedSchedulerWindowWaitsForCompletionWithoutExpiredTimer(t *testing.T) {
	s := scheduledMessageScheduler{windowClosed: true, stopAt: time.Now().Add(-time.Second), active: 1}
	timer, ch := s.nextTimer(time.Now())
	defer stopTimer(timer)
	require.Nil(t, timer, "an expired window must not keep allocating immediately-expired timers while draining")
	require.Nil(t, ch)
}
