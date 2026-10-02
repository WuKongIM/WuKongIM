//go:build integration

package mqttsession

import (
	"context"
	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	gr "github.com/WuKongIM/WuKongIM/pkg/goroutine"
	"github.com/stretchr/testify/require"
	"sync/atomic"
	"testing"
	"time"
)

func TestReplayWorkerJoinedStopAndFreshRestart(t *testing.T) {
	s := &deadlineSource{slots: []meta.HashSlot{0}}
	s.read = func(_ uint16, q meta.MQTTRead) (meta.MQTTReadResult, error) {
		return replayPage(q, []meta.MQTTBindingOwner{replayOwner(0)}, true), nil
	}
	entered, cancelled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	h := replayHandler(func(ctx context.Context, _ meta.MQTTBindingOwner, c contract.ReplayCursor) (contract.ReplayStepResult, error) {
		if calls.Add(1) == 1 {
			close(entered)
			<-ctx.Done()
			close(cancelled)
			<-release
			return contract.ReplayStepResult{}, ctx.Err()
		}
		require.Empty(t, c.Targets)
		require.Zero(t, c.Pass)
		return contract.ReplayStepResult{}, nil
	})
	r := gr.New()
	w := replayWorkerFixture(t, s, h, func(o *ReplayWorkerOptions) { o.Registry = r; o.Interval = time.Minute })
	start, cancelStart := context.WithCancel(context.Background())
	require.NoError(t, w.Start(start))
	require.NoError(t, w.Start(start))
	<-entered
	cancelStart()
	select {
	case <-cancelled:
		t.Fatal("startup context owns run")
	default:
	}
	stop, cancelStop := context.WithCancel(context.Background())
	cancelStop()
	require.ErrorIs(t, w.Stop(stop), context.Canceled)
	<-cancelled
	require.ErrorIs(t, w.Start(context.Background()), ErrReplayWorkerStopping)
	close(release)
	joined, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, w.Stop(joined))
	require.NoError(t, r.Group(gr.ModuleMQTT).Wait(joined))
	restarted := make(chan struct{}, 1)
	w.opts.Observe = func(ReplayObservation) {
		select {
		case restarted <- struct{}{}:
		default:
		}
	}
	require.NoError(t, w.Start(context.Background()))
	<-restarted
	require.NoError(t, w.Stop(joined))
	require.NoError(t, r.Group(gr.ModuleMQTT).Wait(joined))
	require.Equal(t, int32(2), calls.Load())
	require.Zero(t, r.Snapshot().ManagedTotal)
	require.Equal(t, int64(2), r.Snapshot().TotalStarted)
	t.Log("mqtt_replay_worker_lifecycle: managed_runs=2 overlapping_runs=0 cancelled_work_joined=true restart_hints_reset=true")
}

type lateReplaySource struct{ stage string }

func (s lateReplaySource) LocalLeaderHashSlots(ctx context.Context) ([]meta.HashSlot, error) {
	if s.stage == "list" {
		<-ctx.Done()
	}
	return []meta.HashSlot{0}, nil
}
func (s lateReplaySource) ReadMQTTRecovery(ctx context.Context, _ uint16, q meta.MQTTRead) (meta.MQTTReadResult, error) {
	if s.stage == "page" {
		<-ctx.Done()
	}
	return replayPage(q, []meta.MQTTBindingOwner{replayOwner(0)}, true), nil
}
func TestReplayWorkerRejectsLateSuccess(t *testing.T) {
	for _, stage := range []string{"list", "page", "step"} {
		t.Run(stage, func(t *testing.T) {
			calls := 0
			w := replayWorkerFixture(t, lateReplaySource{stage}, func(ctx context.Context, _ meta.MQTTBindingOwner, _ contract.ReplayCursor) (contract.ReplayStepResult, error) {
				calls++
				<-ctx.Done()
				return contract.ReplayStepResult{}, nil
			}, func(o *ReplayWorkerOptions) { o.ReadTimeout = time.Millisecond; o.StepTimeout = time.Millisecond })
			var state replayScanState
			out := w.sweep(context.Background(), &state)
			require.Equal(t, 1, out.Failures)
			if stage == "step" {
				require.Equal(t, 1, calls)
			} else {
				require.Zero(t, calls)
			}
		})
	}
}
