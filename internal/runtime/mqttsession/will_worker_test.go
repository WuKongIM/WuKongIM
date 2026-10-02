package mqttsession

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

type willWorkFunc func(context.Context, meta.MQTTWillKey) error

func (f willWorkFunc) ExecuteWill(ctx context.Context, k meta.MQTTWillKey) error { return f(ctx, k) }

func willWorkerFixture(t *testing.T, s DeadlineSource) *WillWorker {
	t.Helper()
	w, err := NewWillWorker(WillWorkerOptions{Source: s, Executor: willWorkFunc(func(context.Context, meta.MQTTWillKey) error { return nil }), Now: func() time.Time { return time.UnixMilli(100000) }})
	require.NoError(t, err)
	return w
}
func willCursor(w meta.MQTTWill) meta.MQTTReadCursor {
	at := w.DueAtMS
	if w.Stage == meta.MQTTWillExecuting {
		at = w.LeaseUntilMS
	}
	return meta.MQTTReadCursor{Will: meta.MQTTWillRecoveryCursor{RecoveryAtMS: at, Key: w.Key}}
}

func TestWillWorkerRotatesAllHashSlotsWithoutBorrowingBuffers(t *testing.T) {
	s := &deadlineSource{}
	for i := 255; i >= 0; i-- {
		s.slots = append(s.slots, meta.HashSlot(i))
	}
	w := willWorkerFixture(t, s)
	var state willScanState
	for range 8 {
		o := w.sweep(context.Background(), &state, func(meta.MQTTWillKey) bool { t.Fatal("empty scan admitted work"); return true })
		require.Equal(t, 32, o.Pages)
		require.Zero(t, o.Failures)
	}
	require.Len(t, s.visited, 256)
	seen := map[uint16]bool{}
	for i, h := range s.visited {
		require.False(t, seen[h])
		seen[h] = true
		require.Equal(t, meta.MQTTReadWillRecovery, s.queries[i].Kind)
	}
	require.EqualValues(t, 255, s.slots[0])
}

func TestWillWorkerSelectsDetachedDueWorkAndResetsFutureBoundary(t *testing.T) {
	s := &deadlineSource{slots: []meta.HashSlot{7}}
	rows := []meta.MQTTWill{deadlineWill("waiting", meta.MQTTWillWaiting, 5000), deadlineWill("ready", meta.MQTTWillReady, 6000), deadlineWill("executing", meta.MQTTWillExecuting, 7000), deadlineWill("future", meta.MQTTWillReady, 200000)}
	s.read = func(_ uint16, q meta.MQTTRead) (meta.MQTTReadResult, error) {
		return meta.MQTTReadResult{Wills: rows, After: willCursor(rows[3])}, nil
	}
	w := willWorkerFixture(t, s)
	var state willScanState
	var called []string
	for range 2 {
		w.sweep(context.Background(), &state, func(k meta.MQTTWillKey) bool { called = append(called, k.ClientID); return true })
	}
	require.Equal(t, []string{"ready", "executing", "ready", "executing"}, called)
	require.Equal(t, meta.MQTTReadCursor{}, s.queries[1].After)
}

func TestWillWorkerPressureRetainsUnadmittedCursorAndYieldsSlots(t *testing.T) {
	s := &deadlineSource{slots: []meta.HashSlot{0, 1}}
	rows := []meta.MQTTWill{deadlineWill("aa", meta.MQTTWillReady, 5000), deadlineWill("bb", meta.MQTTWillReady, 5000)}
	s.read = func(h uint16, q meta.MQTTRead) (meta.MQTTReadResult, error) {
		if h == 1 {
			return meta.MQTTReadResult{}, errors.New("unavailable")
		}
		r := rows
		if q.After.Will.Key.ClientID == "aa" {
			r = r[1:]
		}
		return meta.MQTTReadResult{Wills: r, After: willCursor(r[len(r)-1])}, nil
	}
	w := willWorkerFixture(t, s)
	w.opts.PagesPerTurn = 1
	var state willScanState
	o := w.sweep(context.Background(), &state, func(k meta.MQTTWillKey) bool { return k.ClientID == "aa" })
	require.Equal(t, 1, o.Scheduled)
	require.Equal(t, willCursor(rows[0]), state.cursors[0])
	w.sweep(context.Background(), &state, func(meta.MQTTWillKey) bool { t.Fatal("failed page dispatched"); return true })
	var next string
	w.sweep(context.Background(), &state, func(k meta.MQTTWillKey) bool { next = k.ClientID; return true })
	require.Equal(t, []uint16{0, 1, 0}, s.visited)
	require.Equal(t, "bb", next)
	s.slots = []meta.HashSlot{1}
	w.sweep(context.Background(), &state, func(meta.MQTTWillKey) bool { return true })
	_, found := state.cursors[0]
	require.False(t, found, "lost leadership retained scan hints")
}

func TestWillWorkerRejectsWholeMalformedOrLatePages(t *testing.T) {
	for _, fault := range []string{"order", "stage", "cursor", "wrong-kind", "cancelled"} {
		t.Run(fault, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s := &deadlineSource{slots: []meta.HashSlot{0}}
			s.read = func(_ uint16, q meta.MQTTRead) (meta.MQTTReadResult, error) {
				rows := []meta.MQTTWill{deadlineWill("aa", meta.MQTTWillReady, 5000), deadlineWill("bb", meta.MQTTWillReady, 5000)}
				r := meta.MQTTReadResult{Wills: rows, After: willCursor(rows[1])}
				switch fault {
				case "order":
					r.Wills[1] = rows[0]
				case "stage":
					r.Wills[1].Stage = meta.MQTTWillPublished
				case "cursor":
					r.After.Will.RecoveryAtMS++
				case "wrong-kind":
					r.Sessions = []meta.MQTTSession{deadlineRow("a", 5000)}
				case "cancelled":
					cancel()
				}
				return r, nil
			}
			w := willWorkerFixture(t, s)
			var state willScanState
			o := w.sweep(ctx, &state, func(meta.MQTTWillKey) bool { t.Fatal("bad page dispatched"); return true })
			require.Positive(t, o.Failures)
			require.Zero(t, o.Scheduled)
		})
	}
}

func TestWillWorkerRejectsUnboundedOptions(t *testing.T) {
	for i, amend := range []func(*WillWorkerOptions){func(o *WillWorkerOptions) { o.Workers = 5 }, func(o *WillWorkerOptions) { o.PagesPerTurn = 33 }, func(o *WillWorkerOptions) { o.PageSize = 17 }, func(o *WillWorkerOptions) { o.ExecutionTimeout = 6 * time.Second }, func(o *WillWorkerOptions) { o.CallTimeout = 3 * time.Second }, func(o *WillWorkerOptions) { o.Interval = -1 }} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			o := WillWorkerOptions{Source: &deadlineSource{}, Executor: willWorkFunc(func(context.Context, meta.MQTTWillKey) error { return nil })}
			amend(&o)
			_, err := NewWillWorker(o)
			require.ErrorIs(t, err, ErrWillWorkerInvalid)
		})
	}
}
