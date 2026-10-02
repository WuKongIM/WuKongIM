//go:build integration

package mqttsession

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	gr "github.com/WuKongIM/WuKongIM/pkg/goroutine"
	"github.com/stretchr/testify/require"
)

func TestDeadlineWorkerJoinedStopAndFreshRestart(t *testing.T) {
	s := &deadlineSource{slots: []meta.HashSlot{0}}
	s.read = func(_ uint16, q meta.MQTTRead) (meta.MQTTReadResult, error) {
		if q.Kind == meta.MQTTReadWillRecovery {
			return meta.MQTTReadResult{After: q.After, Done: true}, nil
		}
		r := deadlineRow("a", 1)
		return meta.MQTTReadResult{Sessions: []meta.MQTTSession{r}, After: sessionScanCursor(r), Done: false}, nil
	}
	entered, cancelled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var visits atomic.Int32
	h := deadlineHandler(func(ctx context.Context, _ contract.Owner) error {
		if visits.Add(1) == 1 {
			close(entered)
			<-ctx.Done()
			close(cancelled)
			<-release
			return ctx.Err()
		}
		return nil
	})
	r := gr.New()
	w, e := NewDeadlineWorker(DeadlineWorkerOptions{Source: s, Reconciler: h, Registry: r, Interval: time.Hour / 60, PagesPerTurn: 1, ItemTimeout: time.Second})
	require.NoError(t, e)
	start, cancelStart := context.WithCancel(context.Background())
	require.NoError(t, w.Start(start))
	require.NoError(t, w.Start(start))
	<-entered
	cancelStart()
	select {
	case <-cancelled:
		t.Fatal("startup context became the worker lifetime")
	default:
	}
	stop, cancelStop := context.WithCancel(context.Background())
	cancelStop()
	require.ErrorIs(t, w.Stop(stop), context.Canceled)
	<-cancelled
	require.ErrorIs(t, w.Start(context.Background()), ErrDeadlineWorkerStopping)
	close(release)
	joined, done := context.WithTimeout(context.Background(), time.Second)
	defer done()
	require.NoError(t, w.Stop(joined))
	require.NoError(t, r.Group(gr.ModuleMQTT).Wait(joined))
	require.Equal(t, int32(1), visits.Load())
	// The next run's first page starts at zero, even when the old run had a
	// partial cursor and stopped while a dependency ignored cancellation.
	restarted := make(chan struct{}, 1)
	w.opts.Observe = func(DeadlineObservation) {
		select {
		case restarted <- struct{}{}:
		default:
		}
	}
	require.NoError(t, w.Start(context.Background()))
	<-restarted
	require.NoError(t, w.Stop(joined))
	require.NoError(t, r.Group(gr.ModuleMQTT).Wait(joined))
	require.Equal(t, int32(2), visits.Load())
	require.Equal(t, meta.MQTTReadCursor{}, s.queries[len(s.queries)-1].After)
	snap := r.Snapshot()
	require.Zero(t, snap.ManagedTotal)
	require.Equal(t, int64(2), snap.TotalStarted)
	t.Log("MQTT deadline worker: managed_runs=2 overlapping_runs=0 cancelled_work_joined=true restart_cursor_reset=true")
}

type lateDeadlineSource struct{ stage string }

func (s lateDeadlineSource) LocalLeaderHashSlots(ctx context.Context) ([]meta.HashSlot, error) {
	if s.stage == "list" {
		<-ctx.Done()
	}
	return []meta.HashSlot{0}, nil
}
func (s lateDeadlineSource) ReadMQTTRecovery(ctx context.Context, _ uint16, q meta.MQTTRead) (meta.MQTTReadResult, error) {
	if s.stage == "page" {
		<-ctx.Done()
	}
	r := deadlineRow("a", 1)
	return meta.MQTTReadResult{Sessions: []meta.MQTTSession{r}, After: sessionScanCursor(r), Done: true}, nil
}

func TestDeadlineWorkerRejectsLateSuccessAfterCallDeadline(t *testing.T) {
	for _, stage := range []string{"list", "page", "handler"} {
		t.Run(stage, func(t *testing.T) {
			attempts := 0
			h := deadlineHandler(func(ctx context.Context, _ contract.Owner) error {
				attempts++
				if stage == "handler" {
					<-ctx.Done()
				}
				return nil
			})
			w, e := NewDeadlineWorker(DeadlineWorkerOptions{Source: lateDeadlineSource{stage: stage}, Reconciler: h, PagesPerTurn: 1, ItemTimeout: time.Millisecond})
			require.NoError(t, e)
			var state deadlineScanState
			o := w.sweep(context.Background(), &state)
			require.Equal(t, 1, o.Failures, "late nil result cannot count as a successful call")
			if stage == "handler" {
				require.Equal(t, 1, attempts)
			} else {
				require.Zero(t, attempts, "expired discovery cannot authorize a new attempt")
			}
		})
	}
}
