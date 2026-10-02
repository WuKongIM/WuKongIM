package mqttsession_test

import (
	"context"
	"errors"
	"testing"
	"time"

	app "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestGroupDrainDiscoversPreparationBeforeFirstBinding(t *testing.T) {
	for _, cut := range []string{"before_protection", "protection_reply", "registration_reply", "cursor"} {
		t.Run(cut, func(t *testing.T) {
			ctx := context.Background()
			f := setupGroupSource(t)
			lost := errors.New("source protection reply lost")
			if cut == "protection_reply" {
				f.protect = func(_ int, _ app.SourceChannel) (app.ProtectedSource, error) { return app.ProtectedSource{}, lost }
				_, err := f.prepare()
				require.ErrorIs(t, err, lost)
			}
			var original app.PreparedGroupSource
			if cut == "cursor" {
				var err error
				original, err = f.prepare()
				require.NoError(t, err)
			}
			f.protect = nil
			f.tail = 20
			s := &drainStore{progressStore: &progressStore{groupSourceStore: f.store}}
			drain, err := app.NewSourceDrain(app.SourceDrainOptions{Store: s, Owners: f.owners, Sources: f.opts.Sources, Now: func() time.Time { return f.now }})
			require.NoError(t, err)
			if cut == "registration_reply" {
				f.store.afterBinding = func(b meta.MQTTSourceBinding) error {
					require.False(t, b.BoundaryKnown)
					require.Equal(t, f.intent.Generation, b.IntentRevision)
					return lost
				}
			}
			if cut == "cursor" {
				f.protect = func(int, app.SourceChannel) (app.ProtectedSource, error) { return app.ProtectedSource{}, lost }
			}
			var sealed app.SourceDrainResult
			f.project.remove = func(c context.Context, r app.SubscriptionProjectionRequest) (app.SubscriptionProjectionReceipt, error) {
				var e error
				sealed, e = drain.SealGroup(c, r.Owner, r.Subscription.Topic)
				return projectionReceipt(r), e
			}
			f.denied = true
			_, err = f.subscriptions.Unsubscribe(ctx, f.connection.Owner, f.intent.Topic)
			if cut == "registration_reply" {
				require.ErrorIs(t, err, lost)
				require.Equal(t, meta.MQTTSubscriptionRemoving, f.subscription(t, f.intent.Topic).Stage)
				f.store.afterBinding = nil
				oldOwner := f.connection.Owner
				resumed, e := f.service.Connect(ctx, command())
				require.NoError(t, e)
				f.connection = resumed
				_, e = drain.SealGroup(ctx, oldOwner, f.intent.Topic)
				require.Error(t, e)
				_, err = f.subscriptions.Unsubscribe(ctx, resumed.Owner, f.intent.Topic)
			}
			require.NoError(t, err)
			require.Equal(t, meta.MQTTSubscriptionRemoved, f.subscription(t, f.intent.Topic).Stage)
			want := uint64(20)
			if cut == "cursor" {
				want = original.Binding.StartAfter
			}
			require.Equal(t, want, sealed.Binding.StartAfter)
			require.Equal(t, want, sealed.Binding.EndThrough)
			require.Equal(t, want, sealed.Cursor.CompletedThrough)
			require.Zero(t, f.row(t).PendingMessages)
			again, e := drain.SealGroup(ctx, f.connection.Owner, f.intent.Topic)
			require.NoError(t, e)
			require.Equal(t, sealed, again)
			removal, e := app.NewSourceRemoval(app.SourceRemovalOptions{Store: s, Now: func() time.Time { return f.now }})
			require.NoError(t, e)
			_, e = removal.Reconcile(ctx, sealed.Binding.Key)
			require.NoError(t, e)
			removed, e := removal.Reconcile(ctx, sealed.Binding.Key)
			require.NoError(t, e)
			require.Equal(t, meta.MQTTBindingRemoved, removed.Binding.Stage)
			require.Zero(t, f.owners.Snapshot().Operations)
		})
	}
}

func TestGroupDrainRejectsUnprovenDiscovery(t *testing.T) {
	for _, mode := range []string{"open", "source_error", "foreign_source", "extra_cursor", "missing_binding", "changed_intent", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			f := setupGroupSource(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "extra_cursor" || mode == "missing_binding" {
				_, e := f.prepare()
				require.NoError(t, e)
			}
			s := &drainStore{progressStore: &progressStore{groupSourceStore: f.store}}
			drain, e := app.NewSourceDrain(app.SourceDrainOptions{Store: s, Owners: f.owners, Sources: f.opts.Sources, Now: func() time.Time { return f.now }})
			require.NoError(t, e)
			if mode != "open" {
				f.project.remove = func(context.Context, app.SubscriptionProjectionRequest) (app.SubscriptionProjectionReceipt, error) {
					return app.SubscriptionProjectionReceipt{}, app.ErrEvidence
				}
				_, e = f.subscriptions.Unsubscribe(ctx, f.connection.Owner, f.intent.Topic)
				require.Error(t, e)
			}
			writes := 0
			f.store.afterBinding = func(meta.MQTTSourceBinding) error { writes++; return nil }
			switch mode {
			case "source_error":
				f.protect = func(int, app.SourceChannel) (app.ProtectedSource, error) {
					return app.ProtectedSource{}, context.DeadlineExceeded
				}
			case "foreign_source":
				f.protect = func(int, app.SourceChannel) (app.ProtectedSource, error) {
					return app.ProtectedSource{Channel: app.SourceChannel{ID: "other", Type: 2}, Generation: "other", CommittedThrough: 20}, nil
				}
			case "extra_cursor", "missing_binding":
				f.store.query = func(c context.Context, q meta.MQTTRead) (meta.MQTTReadResult, error) {
					r, e := f.store.sessionStore.ReadMQTT(c, q)
					if mode == "extra_cursor" && q.Kind == meta.MQTTReadDeliveryCursors {
						r.DeliveryCursors = append(r.DeliveryCursors, r.DeliveryCursors...)
					}
					if mode == "missing_binding" && q.Kind == meta.MQTTReadSourceBinding {
						r.Bindings = nil
					}
					return r, e
				}
			case "changed_intent":
				f.protect = func(_ int, id app.SourceChannel) (app.ProtectedSource, error) {
					f.store.query = func(c context.Context, q meta.MQTTRead) (meta.MQTTReadResult, error) {
						r, e := f.store.sessionStore.ReadMQTT(c, q)
						if len(r.Subscriptions) > 0 {
							r.Subscriptions[0].OperationID = "changed"
						}
						return r, e
					}
					return app.ProtectedSource{Channel: id, Generation: "protected-generation", CommittedThrough: 20}, nil
				}
			case "canceled":
				cancel()
			}
			got, e := drain.SealGroup(ctx, f.connection.Owner, f.intent.Topic)
			require.Error(t, e)
			require.Zero(t, got)
			require.Zero(t, writes)
			require.Zero(t, f.owners.Snapshot().Operations)
		})
	}
}
