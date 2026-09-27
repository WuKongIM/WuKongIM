package mqttsession

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

type consumerWorkFunc func(context.Context, meta.MQTTSourceBindingKey) (ConsumerWork, error)

func (f consumerWorkFunc) MaintainConsumer(ctx context.Context, k meta.MQTTSourceBindingKey) (ConsumerWork, error) {
	return f(ctx, k)
}
func consumerBinding(id string, at int64) meta.MQTTSourceBinding {
	return meta.MQTTSourceBinding{Key: meta.MQTTSourceBindingKey{Owner: meta.MQTTBindingOwner{Kind: meta.MQTTBindingChannel, ID: "2:group", Generation: "generation"}, Namespace: "main", ClientID: id, SessionGeneration: 1, SubscriptionGeneration: 1}, UID: "alice", Topic: "topic", Revision: 1, IntentRevision: 1, ProgressRevision: 1, AuthorizationVersion: 1, OperationID: "operation", Stage: meta.MQTTBindingActive, BoundaryKnown: true, ProtectionRevision: 1, UpdatedAtMS: 1000, RecoveryAtMS: at}
}
func consumerCursor(b meta.MQTTSourceBinding) meta.MQTTReadCursor {
	return meta.MQTTReadCursor{SourceRecovery: meta.MQTTSourceBindingRecoveryCursor{RecoveryAtMS: b.RecoveryAtMS, Key: b.Key}}
}
func consumerWorkerFixture(t *testing.T, s DeadlineSource) *ConsumerWorker {
	t.Helper()
	w, e := NewConsumerWorker(ConsumerWorkerOptions{Source: s, Maintainer: consumerWorkFunc(func(context.Context, meta.MQTTSourceBindingKey) (ConsumerWork, error) { return ConsumerWork{}, nil }), Now: func() time.Time { return time.UnixMilli(100000) }})
	require.NoError(t, e)
	return w
}

func TestConsumerWorkerRotatesSlotsAndPreservesPressureCursor(t *testing.T) {
	s := &deadlineSource{}
	for i := 255; i >= 0; i-- {
		s.slots = append(s.slots, meta.HashSlot(i))
	}
	w := consumerWorkerFixture(t, s)
	var state consumerScanState
	for range 8 {
		o := w.sweep(context.Background(), &state, func(consumerWorkKey) bool { t.Fatal("empty page dispatched"); return true })
		require.Equal(t, 32, o.Pages)
	}
	require.Len(t, s.visited, 256)
	require.EqualValues(t, 255, s.slots[0])
	require.Len(t, state.cursors, 0)
	s.slots = []meta.HashSlot{0, 1}
	s.visited = nil
	rows := []meta.MQTTSourceBinding{consumerBinding("aa", 5000), consumerBinding("bb", 5000)}
	s.read = func(h uint16, q meta.MQTTRead) (meta.MQTTReadResult, error) {
		if h == 1 {
			return meta.MQTTReadResult{}, errors.New("unavailable")
		}
		r := rows
		if q.After.SourceRecovery.Key.ClientID == "aa" {
			r = r[1:]
		}
		return meta.MQTTReadResult{Bindings: r, After: consumerCursor(r[len(r)-1])}, nil
	}
	w.opts.PagesPerTurn = 1
	state = consumerScanState{}
	w.sweep(context.Background(), &state, func(k consumerWorkKey) bool { return k.binding.ClientID == "aa" })
	require.Equal(t, consumerCursor(rows[0]), state.cursors[0])
	w.sweep(context.Background(), &state, func(consumerWorkKey) bool { t.Fatal("failed page dispatched"); return true })
	var next string
	w.sweep(context.Background(), &state, func(k consumerWorkKey) bool { next = k.binding.ClientID; return true })
	require.Equal(t, "bb", next)
	require.Equal(t, []uint16{0, 1, 0}, s.visited)
	s.slots = []meta.HashSlot{1}
	w.sweep(context.Background(), &state, func(consumerWorkKey) bool { return true })
	_, ok := state.cursors[0]
	require.False(t, ok)
}

func TestConsumerWorkerRejectsWholeInvalidOrLatePage(t *testing.T) {
	for _, fault := range []string{"order", "stage", "cursor", "extra", "cancelled"} {
		t.Run(fault, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s := &deadlineSource{slots: []meta.HashSlot{0}}
			s.read = func(_ uint16, q meta.MQTTRead) (meta.MQTTReadResult, error) {
				a, b := consumerBinding("aa", 5000), consumerBinding("bb", 5000)
				r := meta.MQTTReadResult{Bindings: []meta.MQTTSourceBinding{a, b}, After: consumerCursor(b)}
				switch fault {
				case "order":
					r.Bindings[1] = a
				case "stage":
					r.Bindings[1].Stage = meta.MQTTBindingRemoved
				case "cursor":
					r.After.SourceRecovery.RecoveryAtMS++
				case "extra":
					r.Accounting = &meta.MQTTAccountingRange{}
				case "cancelled":
					cancel()
				}
				return r, nil
			}
			w := consumerWorkerFixture(t, s)
			var state consumerScanState
			w.sweep(ctx, &state, func(consumerWorkKey) bool { t.Fatal("invalid page dispatched"); return true })
			require.Empty(t, state.cursors)
		})
	}
}

func TestConsumerWorkerIncludesUIDAndResetsFutureBoundary(t *testing.T) {
	s := &deadlineSource{slots: []meta.HashSlot{0}}
	a, b, c := consumerBinding("a", 1000), consumerBinding("b", 2000), consumerBinding("c", 200000)
	a.Key.Owner = meta.MQTTBindingOwner{Kind: meta.MQTTBindingUID, ID: "alice"}
	a.BoundaryKnown = false
	a.ProtectionRevision = 0
	a.DiscoveryDone = true
	s.read = func(_ uint16, q meta.MQTTRead) (meta.MQTTReadResult, error) {
		return meta.MQTTReadResult{Bindings: []meta.MQTTSourceBinding{a, b, c}, After: consumerCursor(c)}, nil
	}
	w := consumerWorkerFixture(t, s)
	var state consumerScanState
	var keys []string
	for range 2 {
		w.sweep(context.Background(), &state, func(k consumerWorkKey) bool { keys = append(keys, k.binding.ClientID); return true })
	}
	require.Equal(t, []string{"a", "b", "a", "b"}, keys)
	require.Empty(t, state.cursors)
}

// Metadata ScanIndex preserves the request cursor on a terminal page, including
// when that page has rows. The worker still resumes from visited row keys under
// cohort pressure; a terminal page cannot discard unadmitted work.
func TestConsumerWorkerTerminalPageRetainsRequestCursor(t *testing.T) {
	s := &deadlineSource{slots: []meta.HashSlot{0}}
	a, b := consumerBinding("a", 1000), consumerBinding("b", 2000)
	s.read = func(_ uint16, q meta.MQTTRead) (meta.MQTTReadResult, error) {
		rows := []meta.MQTTSourceBinding{a, b}
		if q.After == consumerCursor(a) {
			rows = rows[1:]
		}
		return meta.MQTTReadResult{Bindings: rows, After: q.After, Done: true}, nil
	}
	w := consumerWorkerFixture(t, s)
	var state consumerScanState
	first := w.sweep(context.Background(), &state, func(k consumerWorkKey) bool { return k.binding.ClientID == "a" })
	require.Equal(t, 1, first.Scheduled)
	require.Zero(t, first.Failures)
	require.Equal(t, consumerCursor(a), state.cursors[0])
	var keys []string
	second := w.sweep(context.Background(), &state, func(k consumerWorkKey) bool { keys = append(keys, k.binding.ClientID); return true })
	require.Equal(t, []string{"b"}, keys)
	require.Zero(t, second.Failures)
	require.Empty(t, state.cursors)
}
