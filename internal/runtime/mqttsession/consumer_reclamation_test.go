package mqttsession

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

type consumerReclamationIndexFunc func(context.Context, uint16) (meta.MQTTReclamationIndexResult, error)

func (f consumerReclamationIndexFunc) BuildMQTTReclamationIndex(c context.Context, h uint16) (meta.MQTTReclamationIndexResult, error) {
	return f(c, h)
}

type consumerReclamationFunc func(context.Context, meta.MQTTSessionCursor) (bool, error)

func (f consumerReclamationFunc) ReclaimSession(c context.Context, k meta.MQTTSessionCursor) (bool, error) {
	return f(c, k)
}
func consumerReclamationRow(id string) meta.MQTTSession {
	return meta.MQTTSession{Namespace: "main", ClientID: id, UID: "alice", Generation: 2, OwnerGeneration: 2, OwnerNodeID: 1, OwnerBootID: "boot", ConnectionID: 2, NextPacketID: 1, NextDeliveryOrder: 1, Revision: 4, State: meta.MQTTSessionEnded, TerminationReason: meta.MQTTSessionExplicit, UpdatedAtMS: 1000, SessionExpirySec: 60, QuotaMessages: 100, QuotaBytes: 10000, WindowLimit: 10, ReceiveMaximum: 10, MaxPacketBytes: 10000}
}
func consumerReclamationCursor(s meta.MQTTSession) meta.MQTTReadCursor {
	return meta.MQTTReadCursor{Session: meta.MQTTSessionCursor{Namespace: s.Namespace, ClientID: s.ClientID}}
}
func enableReclamationWork(w *ConsumerWorker) {
	w.opts.ReclamationIndex = consumerReclamationIndexFunc(func(context.Context, uint16) (meta.MQTTReclamationIndexResult, error) {
		return meta.MQTTReclamationIndexResult{Done: true}, nil
	})
	w.opts.Reclamation = consumerReclamationFunc(func(context.Context, meta.MQTTSessionCursor) (bool, error) { return true, nil })
}
func TestConsumerReclamationRotatesAllStreamsAndBoundsBuilds(t *testing.T) {
	s := &deadlineSource{}
	for i := 255; i >= 0; i-- {
		s.slots = append(s.slots, meta.HashSlot(i))
	}
	w := consumerWorkerFixture(t, s)
	enableSubscriptionWork(w)
	enableReclamationWork(w)
	builds := map[uint16]int{}
	w.opts.ReclamationIndex = consumerReclamationIndexFunc(func(_ context.Context, h uint16) (meta.MQTTReclamationIndexResult, error) {
		builds[h]++
		return meta.MQTTReclamationIndexResult{Scanned: 1, Done: true}, nil
	})
	var state consumerScanState
	indexed := 0
	for range 48 {
		o := w.sweep(context.Background(), &state, func(consumerWorkKey) bool { t.Fatal("empty page"); return false })
		require.Equal(t, 32, o.Pages)
		indexed += o.ReclamationIndexRows
	}
	require.Equal(t, 256, indexed)
	require.Len(t, builds, 256)
	require.Len(t, s.queries, 1536)
	for h, n := range builds {
		require.Equal(t, 1, n, "slot %d", h)
	}
	kinds := map[meta.MQTTReadKind]int{}
	for _, q := range s.queries {
		kinds[q.Kind]++
	}
	require.Equal(t, 512, kinds[meta.MQTTReadSessionReclamation])
	require.Equal(t, 512, kinds[meta.MQTTReadSourceRecovery])
	require.Equal(t, 512, kinds[meta.MQTTReadSubscriptionRecovery])
	require.Len(t, state.indexReady, 256)
	s.slots = nil
	w.sweep(context.Background(), &state, func(consumerWorkKey) bool { return true })
	require.Empty(t, state.indexReady)
	require.Empty(t, state.cursors)
}
func TestConsumerReclamationWaitsForCoverageAndRejectsLateOrBadBuild(t *testing.T) {
	for _, fault := range []string{"partial", "error", "cancel", "panic", "negative", "oversize", "incomplete"} {
		t.Run(fault, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s := &deadlineSource{slots: []meta.HashSlot{0}}
			w := consumerWorkerFixture(t, s)
			enableReclamationWork(w)
			calls := 0
			w.opts.ReclamationIndex = consumerReclamationIndexFunc(func(context.Context, uint16) (meta.MQTTReclamationIndexResult, error) {
				calls++
				r := meta.MQTTReclamationIndexResult{Scanned: 64}
				switch fault {
				case "error":
					return r, errors.New("unavailable")
				case "cancel":
					cancel()
					r.Done = true
				case "panic":
					panic("secret")
				case "negative":
					r.Scanned = -1
				case "oversize":
					r.Scanned = 65
				case "incomplete":
					r.Scanned = 1
				}
				return r, nil
			})
			var state consumerScanState
			o := w.sweep(ctx, &state, func(consumerWorkKey) bool { t.Fatal("unbuilt index dispatched"); return false })
			require.Equal(t, 1, calls)
			for _, q := range s.queries {
				require.NotEqual(t, meta.MQTTReadSessionReclamation, q.Kind)
			}
			require.Empty(t, state.indexReady)
			if fault == "partial" {
				require.Equal(t, 64, o.ReclamationIndexRows)
				require.Zero(t, o.Failures)
			} else {
				require.Positive(t, o.Failures)
				require.Zero(t, o.ReclamationIndexRows)
			}
		})
	}
}
func TestConsumerReclamationRetainsPressureCursorAndRebuildsAfterReadFailure(t *testing.T) {
	s := &deadlineSource{slots: []meta.HashSlot{0}}
	a, b := consumerReclamationRow("z"), consumerReclamationRow("aa")
	require.NoError(t, meta.ValidateMQTTSession(a))
	require.NoError(t, meta.ValidateMQTTSession(b))
	fail := false
	s.read = func(_ uint16, q meta.MQTTRead) (meta.MQTTReadResult, error) {
		if q.Kind != meta.MQTTReadSessionReclamation {
			return meta.MQTTReadResult{Done: true, After: q.After}, nil
		}
		if fail {
			return meta.MQTTReadResult{}, errors.New("coverage unavailable")
		}
		rows := []meta.MQTTSession{a, b}
		if q.After == consumerReclamationCursor(a) {
			rows = rows[1:]
		}
		return meta.MQTTReadResult{Sessions: rows, Done: true, After: consumerReclamationCursor(b)}, nil
	}
	w := consumerWorkerFixture(t, s)
	enableReclamationWork(w)
	var state consumerScanState
	o := w.sweep(context.Background(), &state, func(k consumerWorkKey) bool {
		require.Zero(t, k.binding)
		require.Zero(t, k.subscription)
		return k.session.ClientID == "z"
	})
	require.Equal(t, 1, o.Scheduled)
	var got []meta.MQTTSessionCursor
	o = w.sweep(context.Background(), &state, func(k consumerWorkKey) bool { got = append(got, k.session); return true })
	require.Zero(t, o.Failures)
	require.Equal(t, []meta.MQTTSessionCursor{consumerReclamationCursor(b).Session}, got)
	require.Empty(t, state.cursors)
	fail = true
	o = w.sweep(context.Background(), &state, func(consumerWorkKey) bool { t.Fatal("failed page"); return false })
	require.Positive(t, o.Failures)
	require.Empty(t, state.indexReady)
}
func TestConsumerReclamationRejectsWholeInvalidPage(t *testing.T) {
	for _, fault := range []string{"order", "duplicate", "current live", "done marker", "cursor", "extra", "partial empty", "oversize", "cancel"} {
		t.Run(fault, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s := &deadlineSource{slots: []meta.HashSlot{0}}
			s.read = func(_ uint16, q meta.MQTTRead) (meta.MQTTReadResult, error) {
				if q.Kind != meta.MQTTReadSessionReclamation {
					return meta.MQTTReadResult{Done: true}, nil
				}
				a, b := consumerReclamationRow("z"), consumerReclamationRow("aa")
				r := meta.MQTTReadResult{Sessions: []meta.MQTTSession{a, b}, Done: true, After: consumerReclamationCursor(b)}
				switch fault {
				case "order":
					r.Sessions[0], r.Sessions[1] = b, a
				case "duplicate":
					r.Sessions[1] = a
				case "current live":
					r.Sessions[1].State = meta.MQTTSessionOffline
					r.Sessions[1].TerminationReason = 0
					r.Sessions[1].OfflineExpiresAtMS = 2000
					r.Sessions[1].ReclaimedThroughGeneration = 1
				case "done marker":
					r.Sessions[1].ReclaimedThroughGeneration = 2
				case "cursor":
					r.After = q.After
				case "extra":
					r.Runtime = &meta.MQTTRuntimeView{}
				case "partial empty":
					r.Sessions = nil
					r.Done = false
					r.After = q.After
				case "oversize":
					for range 16 {
						r.Sessions = append(r.Sessions, b)
					}
				case "cancel":
					cancel()
				}
				return r, nil
			}
			w := consumerWorkerFixture(t, s)
			enableReclamationWork(w)
			var state consumerScanState
			o := w.sweep(ctx, &state, func(consumerWorkKey) bool { t.Fatal("invalid page admitted"); return true })
			require.Positive(t, o.Failures)
			require.Empty(t, state.cursors)
		})
	}
}
func TestConsumerReclamationRequiresCompletePorts(t *testing.T) {
	w := consumerWorkerFixture(t, &deadlineSource{})
	enableReclamationWork(w)
	_, e := NewConsumerWorker(w.opts)
	require.NoError(t, e)
	o := w.opts
	o.Reclamation = nil
	_, e = NewConsumerWorker(o)
	require.Error(t, e)
	o = w.opts
	o.ReclamationIndex = nil
	_, e = NewConsumerWorker(o)
	require.Error(t, e)
}
