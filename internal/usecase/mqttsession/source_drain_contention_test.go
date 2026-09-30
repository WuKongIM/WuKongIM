package mqttsession_test

import (
	"context"
	"errors"
	app "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

// Use real subscription/projection/drain/progress and storage transitions, with
// one fixed interleaving at the source CAS. Unknown effects never become pending.
func TestSourceDrainProgressBeforeSealDoesNotClose(t *testing.T) {
	for _, mode := range []string{"progress", "competing-drain", "changed-end", "progress-denied", "progress-with-exchange", "port-conflict", "lost-reply", "unchanged-rejection", "fake-newer-rejection", "changed-owner", "changed-child", "changed-binding", "regressed-binding", "regressed-progress", "changed-cursor", "regressed-parent", "read-unavailable", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f, s, progress, prepared := progressFixture(t)
			ack := advanceProgressWindow(t, f, prepared)
			ack(0)
			if mode != "progress-with-exchange" {
				ack(1)
			}
			store := &drainStore{progressStore: s}
			projection, err := app.NewGroupProjection(app.GroupProjectionOptions{Store: store, Owners: f.owners, Authorization: f.opts.Authorization, Sources: f.opts.Sources, Replay: groupReplayConfirmation(func(context.Context, meta.MQTTBindingOwner, uint64) error { return nil }), Now: func() time.Time { return f.now }})
			require.NoError(t, err)
			f.project.remove = projection.Remove
			lost := errors.New("source reply unavailable")
			calls := 0
			before := prepared.Binding
			s.write = func(c context.Context, expected uint64, b meta.MQTTSourceBinding) (meta.MQTTSourceBindingResult, error) {
				calls++
				s.write = nil
				switch mode {
				case "port-conflict":
					return meta.MQTTSourceBindingResult{}, app.ErrConflict
				case "lost-reply":
					r, e := f.store.CompareAndSwapMQTTSourceBinding(c, expected, b)
					require.NoError(t, e)
					require.Equal(t, meta.MQTTSessionCASApplied, r.Status)
					return meta.MQTTSourceBindingResult{}, lost
				case "unchanged-rejection":
					return meta.MQTTSourceBindingResult{Status: meta.MQTTSessionCASConflict, CurrentRevision: expected}, nil
				case "fake-newer-rejection":
					return meta.MQTTSourceBindingResult{Status: meta.MQTTSessionCASConflict, CurrentRevision: expected + 1}, nil
				}
				if mode == "competing-drain" || mode == "changed-end" {
					f.now = f.now.Add(time.Millisecond)
					background, e := app.NewSourceDrain(app.SourceDrainOptions{Store: &drainStore{progressStore: &progressStore{groupSourceStore: f.store}}, Owners: f.owners, Now: func() time.Time { return f.now }})
					require.NoError(t, e)
					result, e := background.ReconcileClosed(c, prepared.Binding.Key)
					require.NoError(t, e)
					require.True(t, result.Binding.EndKnown)
					before = result.Binding
				} else {
					result, e := progress.Reconcile(c, prepared.Binding.Key)
					require.NoError(t, e)
					require.True(t, result.Changed)
					require.Equal(t, b.Key, result.Binding.Key)
					before = result.Binding
				}
				if mode == "progress-denied" {
					f.denied = true
				}
				receipt, e := f.store.CompareAndSwapMQTTSourceBinding(c, expected, b)
				require.NoError(t, e)
				require.Equal(t, meta.MQTTSessionCASConflict, receipt.Status)
				if mode == "canceled" {
					cancel()
				}
				parentRevision := f.row(t).Revision
				f.store.query = func(c context.Context, q meta.MQTTRead) (meta.MQTTReadResult, error) {
					if mode == "read-unavailable" {
						return meta.MQTTReadResult{}, lost
					}
					r, e := f.store.sessionStore.ReadMQTT(c, q)
					if r.Session != nil {
						switch mode {
						case "changed-owner":
							r.Session.OwnerGeneration++
						case "regressed-parent":
							r.Session.Revision = parentRevision - 1
						}
					}
					if len(r.Subscriptions) == 1 && mode == "changed-child" {
						r.Subscriptions[0].NoLocal = !r.Subscriptions[0].NoLocal
					}
					if len(r.Bindings) == 1 {
						switch mode {
						case "changed-end":
							r.Bindings[0].EndThrough++
						case "changed-binding":
							r.Bindings[0].OperationID = "other-operation"
						case "regressed-binding":
							r.Bindings[0].Revision--
						case "regressed-progress":
							r.Bindings[0].CompletedThrough = prepared.Binding.CompletedThrough
						}
					}
					if len(r.DeliveryCursors) == 1 && mode == "changed-cursor" {
						r.DeliveryCursors[0].StartAfter++
					}
					return r, e
				}
				return receipt, nil
			}
			_, err = f.subscriptions.Unsubscribe(ctx, f.connection.Owner, prepared.Binding.Topic)
			require.Equal(t, 1, calls, "no inline source CAS retry")
			require.ErrorIs(t, err, app.ErrSubscriptionUnconfirmed)
			f.store.query = nil
			require.Equal(t, meta.MQTTSubscriptionRemoving, f.subscription(t, prepared.Binding.Topic).Stage)
			require.Zero(t, f.owners.Snapshot().Operations)
			if mode != "progress" && mode != "competing-drain" && mode != "progress-denied" && mode != "progress-with-exchange" {
				require.NotErrorIs(t, err, app.ErrSourceDrainPending)
				if mode == "lost-reply" || mode == "read-unavailable" {
					require.ErrorIs(t, err, lost)
				}
				if mode == "canceled" {
					require.ErrorIs(t, err, context.Canceled)
				}
				return
			}
			require.ErrorIs(t, err, app.ErrSourceDrainPending, "competing monotonic source progress retains bounded pending removal")
			state, e := f.store.ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadSourceBinding, BindingKey: prepared.Binding.Key})
			require.NoError(t, e)
			require.Equal(t, before, state.Bindings[0])
			require.Equal(t, mode == "competing-drain", state.Bindings[0].EndKnown)
			existed, e := f.subscriptions.Unsubscribe(ctx, f.connection.Owner, prepared.Binding.Topic)
			require.NoError(t, e)
			require.True(t, existed)
			require.Equal(t, meta.MQTTSubscriptionRemoved, f.subscription(t, prepared.Binding.Topic).Stage)
			state, e = f.store.ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadSourceBinding, BindingKey: prepared.Binding.Key})
			require.NoError(t, e)
			require.Equal(t, prepared.Cursor.StartAfter, state.Bindings[0].StartAfter)
			require.Equal(t, prepared.Cursor.StartAfter+2, state.Bindings[0].EndThrough)
			if mode == "progress-with-exchange" {
				require.EqualValues(t, 1, f.row(t).PendingMessages)
				r, e := f.store.ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadInflightPage, Namespace: f.connection.Owner.Key.Namespace, ClientID: f.connection.Owner.Key.ClientID, SessionGeneration: f.connection.Owner.SessionGeneration, Limit: 16})
				require.NoError(t, e)
				require.Len(t, r.Inflight, 1)
			} else {
				require.Zero(t, f.row(t).PendingMessages)
			}
			require.Zero(t, f.owners.Snapshot().Operations)
		})
	}
}
