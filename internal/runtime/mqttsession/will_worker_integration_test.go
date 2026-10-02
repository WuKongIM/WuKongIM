//go:build integration

package mqttsession

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	gr "github.com/WuKongIM/WuKongIM/pkg/goroutine"
	"github.com/stretchr/testify/require"
)

func TestWillWorkerBoundedCohortJoinedStopAndFreshRestart(t *testing.T) {
	s := &deadlineSource{slots: []meta.HashSlot{0}}
	s.read = func(_ uint16, q meta.MQTTRead) (meta.MQTTReadResult, error) {
		var rows []meta.MQTTWill
		for i := range 12 {
			r := deadlineWill(fmt.Sprintf("%02d", i), meta.MQTTWillReady, 5000)
			if compareDeadlineCursor(q.Kind, q.After, willCursor(r)) < 0 {
				rows = append(rows, r)
			}
		}
		if len(rows) == 0 {
			return meta.MQTTReadResult{After: q.After, Done: true}, nil
		}
		return meta.MQTTReadResult{Wills: rows, After: willCursor(rows[len(rows)-1]), Done: true}, nil
	}
	entered, cancelled := make(chan meta.MQTTWillKey, 16), make(chan struct{}, 16)
	release := make(chan struct{})
	var calls atomic.Int32
	executor := willWorkFunc(func(ctx context.Context, k meta.MQTTWillKey) error {
		calls.Add(1)
		entered <- k
		select {
		case <-ctx.Done():
			cancelled <- struct{}{}
			<-release
			return ctx.Err()
		case <-release:
			return nil
		}
	})
	registry := gr.New()
	w, err := NewWillWorker(WillWorkerOptions{Source: s, Executor: executor, Registry: registry, Interval: 10 * time.Millisecond})
	require.NoError(t, err)
	start, cancelStart := context.WithCancel(context.Background())
	defer cancelStart()
	require.NoError(t, w.Start(start))
	require.NoError(t, w.Start(start))
	seen := map[meta.MQTTWillKey]bool{}
	for range 4 {
		select {
		case k := <-entered:
			require.False(t, seen[k])
			seen[k] = true
		case <-time.After(2 * time.Second):
			t.Fatal("cohort did not start")
		}
	}
	cancelStart()
	select {
	case <-cancelled:
		t.Fatal("startup context became runtime lifetime")
	default:
	}
	stop, cancelStop := context.WithCancel(context.Background())
	cancelStop()
	require.ErrorIs(t, w.Stop(stop), context.Canceled)
	for range 4 {
		select {
		case <-cancelled:
		case <-time.After(time.Second):
			t.Fatal("work was not cancelled")
		}
	}
	require.ErrorIs(t, w.Start(context.Background()), ErrWillWorkerStopping)
	require.EqualValues(t, 4, calls.Load(), "queue or discovery exceeded the fixed cohort")
	close(release)
	joined, done := context.WithTimeout(context.Background(), 3*time.Second)
	defer done()
	require.NoError(t, w.Stop(joined))
	require.NoError(t, registry.Group(gr.ModuleMQTT).Wait(joined))
	before := len(s.queries)
	require.NoError(t, w.Start(context.Background()))
	select {
	case <-entered:
	case <-joined.Done():
		t.Fatal("restart did not execute")
	}
	require.NoError(t, w.Stop(joined))
	require.NoError(t, registry.Group(gr.ModuleMQTT).Wait(joined))
	require.Equal(t, meta.MQTTReadCursor{}, s.queries[before].After)
	require.Zero(t, registry.Snapshot().ManagedTotal)
	t.Log("Will scheduling: max_admitted=4 stop_joined=true no_overlap=true restart_cursor_reset=true")
}
