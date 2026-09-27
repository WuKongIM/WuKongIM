package mqttsession

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

type consumerSubscriptionFunc func(context.Context, meta.MQTTSubscriptionRecoveryCursor) error

func (f consumerSubscriptionFunc) MaintainSubscription(ctx context.Context, k meta.MQTTSubscriptionRecoveryCursor) (bool, error) {
	err := f(ctx, k)
	return err == nil, err
}
func consumerSubscription(id string, at int64) meta.MQTTSubscription {
	return meta.MQTTSubscription{Namespace: "main", ClientID: id, SessionGeneration: 1, Topic: "topic", Generation: 2, Revision: 3, TargetKind: meta.MQTTSubscriptionUserInbox, TargetID: "alice", GrantedQoS: 1, Stage: meta.MQTTSubscriptionRemoving, OperationID: "operation", UpdatedAtMS: 1000, RecoveryAtMS: at}
}
func consumerSubscriptionCursor(s meta.MQTTSubscription) meta.MQTTReadCursor {
	return meta.MQTTReadCursor{Subscription: meta.MQTTSubscriptionRecoveryCursor{RecoveryAtMS: s.RecoveryAtMS, Namespace: s.Namespace, ClientID: s.ClientID, SessionGeneration: s.SessionGeneration, Topic: s.Topic}}
}
func enableSubscriptionWork(w *ConsumerWorker) {
	w.opts.Subscriptions = consumerSubscriptionFunc(func(context.Context, meta.MQTTSubscriptionRecoveryCursor) error { return nil })
}

func TestConsumerSubscriptionsRotateBothStreamsAcross256Slots(t *testing.T) {
	s := &deadlineSource{}
	for i := 255; i >= 0; i-- {
		s.slots = append(s.slots, meta.HashSlot(i))
	}
	w := consumerWorkerFixture(t, s)
	enableSubscriptionWork(w)
	var state consumerScanState
	for range 16 {
		o := w.sweep(context.Background(), &state, func(consumerWorkKey) bool { t.Fatal("empty page"); return false })
		require.Equal(t, 32, o.Pages)
	}
	require.Len(t, s.queries, 512)
	for i, q := range s.queries {
		require.EqualValues(t, i/2, s.visited[i])
		want := meta.MQTTReadSourceRecovery
		if i%2 == 1 {
			want = meta.MQTTReadSubscriptionRecovery
		}
		require.Equal(t, want, q.Kind)
	}
	require.Empty(t, state.cursors)
}
func TestConsumerSubscriptionsRetainPressureSkipPreparingAndNormalizeHints(t *testing.T) {
	s := &deadlineSource{slots: []meta.HashSlot{0}}
	a, b, c := consumerSubscription("a", 1000), consumerSubscription("b", 2000), consumerSubscription("c", 3000)
	a.Stage = meta.MQTTSubscriptionPreparing
	s.read = func(_ uint16, q meta.MQTTRead) (meta.MQTTReadResult, error) {
		if q.Kind == meta.MQTTReadSourceRecovery {
			return meta.MQTTReadResult{Done: true, After: q.After}, nil
		}
		rows := []meta.MQTTSubscription{a, b, c}
		if q.After == consumerSubscriptionCursor(b) {
			rows = rows[2:]
		}
		return meta.MQTTReadResult{Subscriptions: rows, Done: true, After: q.After}, nil
	}
	w := consumerWorkerFixture(t, s)
	enableSubscriptionWork(w)
	var state consumerScanState
	var got []consumerWorkKey
	o := w.sweep(context.Background(), &state, func(k consumerWorkKey) bool {
		require.Zero(t, k.binding)
		require.Zero(t, k.subscription.RecoveryAtMS)
		got = append(got, k)
		return k.subscription.ClientID == "b"
	})
	require.Equal(t, 1, o.Scheduled)
	require.Len(t, got, 2)
	require.Equal(t, "b", got[0].subscription.ClientID)
	got = nil
	o = w.sweep(context.Background(), &state, func(k consumerWorkKey) bool { got = append(got, k); return true })
	require.Zero(t, o.Failures)
	require.Len(t, got, 1)
	require.Equal(t, "c", got[0].subscription.ClientID)
	require.Empty(t, state.cursors)
}
func TestConsumerSubscriptionsRejectCompleteInvalidPages(t *testing.T) {
	for _, fault := range []string{"duplicate", "active", "removed", "cursor", "extra", "partial_empty", "too_many", "cancel"} {
		t.Run(fault, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s := &deadlineSource{slots: []meta.HashSlot{0}}
			s.read = func(_ uint16, q meta.MQTTRead) (meta.MQTTReadResult, error) {
				if q.Kind == meta.MQTTReadSourceRecovery {
					return meta.MQTTReadResult{Done: true}, nil
				}
				a, b := consumerSubscription("a", 1000), consumerSubscription("b", 2000)
				r := meta.MQTTReadResult{Subscriptions: []meta.MQTTSubscription{a, b}, Done: true}
				switch fault {
				case "duplicate":
					r.Subscriptions[1] = a
				case "active":
					r.Subscriptions[1].Stage = meta.MQTTSubscriptionActive
					r.Subscriptions[1].RecoveryAtMS = 0
				case "removed":
					r.Subscriptions[1].Stage = meta.MQTTSubscriptionRemoved
					r.Subscriptions[1].RecoveryAtMS = 0
				case "cursor":
					r.After = consumerSubscriptionCursor(a)
				case "extra":
					r.Bindings = []meta.MQTTSourceBinding{consumerBinding("a", 1000)}
				case "partial_empty":
					r.Subscriptions = nil
					r.Done = false
				case "too_many":
					for range 16 {
						r.Subscriptions = append(r.Subscriptions, b)
					}
				case "cancel":
					cancel()
				}
				return r, nil
			}
			w := consumerWorkerFixture(t, s)
			enableSubscriptionWork(w)
			var state consumerScanState
			o := w.sweep(ctx, &state, func(consumerWorkKey) bool { t.Fatal("invalid page dispatched"); return true })
			require.Positive(t, o.Failures)
			require.Empty(t, state.cursors)
		})
	}
}
func TestConsumerSubscriptionsResetFutureAndLostSlotHints(t *testing.T) {
	s := &deadlineSource{slots: []meta.HashSlot{0}}
	s.read = func(_ uint16, q meta.MQTTRead) (meta.MQTTReadResult, error) {
		if q.Kind == meta.MQTTReadSourceRecovery {
			return meta.MQTTReadResult{Done: true}, nil
		}
		r := consumerSubscription("a", 200000)
		return meta.MQTTReadResult{Subscriptions: []meta.MQTTSubscription{r}, After: consumerSubscriptionCursor(r)}, nil
	}
	w := consumerWorkerFixture(t, s)
	enableSubscriptionWork(w)
	var state consumerScanState
	w.sweep(context.Background(), &state, func(consumerWorkKey) bool { t.Fatal("future page dispatched"); return true })
	require.Empty(t, state.cursors)
	state.cursors[1] = consumerSubscriptionCursor(consumerSubscription("a", 2000))
	s.slots = nil
	w.sweep(context.Background(), &state, func(consumerWorkKey) bool { return true })
	require.Empty(t, state.cursors)
}
