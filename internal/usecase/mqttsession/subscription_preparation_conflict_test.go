package mqttsession_test

import (
	"context"
	"errors"
	"testing"

	app "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestSubscriptionPreparationRebasesOnlyDefiniteUnchangedIntent(t *testing.T) {
	for _, stage := range []string{"quota", "intent"} {
		for _, mode := range []string{"renew-once", "renew-forever", "changed-child", "changed-owner", "unchanged-parent", "port-conflict", "lost-reply", "canceled", "revoked"} {
			t.Run(stage+"/"+mode, func(t *testing.T) {
				f := setupSubscriptions(t)
				r := subscriptionRequest()
				_, err := f.subscriptions.Subscribe(context.Background(), f.connection.Owner, r)
				require.NoError(t, err)
				_, err = f.subscriptions.Unsubscribe(context.Background(), f.connection.Owner, r.Topic)
				require.NoError(t, err)
				before := f.subscription(t, r.Topic)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				calls := 0
				lost := errors.New("preparation reply unavailable")
				contend := func(ctx context.Context) {
					if calls == 1 || mode == "renew-forever" {
						_, e := f.service.Renew(ctx, f.connection.Owner)
						require.NoError(t, e)
					}
					if mode == "revoked" {
						f.denied = true
					}
				}
				f.store.query = func(ctx context.Context, q meta.MQTTRead) (meta.MQTTReadResult, error) {
					if stage == "quota" && q.Kind == meta.MQTTReadSubscriptions {
						calls++
						if mode == "port-conflict" {
							return meta.MQTTReadResult{}, app.ErrConflict
						}
						if mode == "lost-reply" {
							return meta.MQTTReadResult{}, lost
						}
						if mode != "unchanged-parent" {
							contend(ctx)
						}
					}
					out, e := f.store.sessionStore.ReadMQTT(ctx, q)
					if stage == "quota" && q.Kind == meta.MQTTReadSubscriptions && mode == "unchanged-parent" {
						out.Session.Revision++
					}
					if calls > 0 && q.Kind == meta.MQTTReadSubscription {
						if mode == "changed-owner" {
							out.Session.OwnerGeneration++
						}
						if mode == "changed-child" {
							out.Subscriptions[0].NoLocal = !out.Subscriptions[0].NoLocal
						}
					}
					if stage == "quota" && q.Kind == meta.MQTTReadSubscriptions && mode == "canceled" {
						cancel()
					}
					return out, e
				}
				if stage == "intent" {
					f.store.mutate = func(ctx context.Context, m meta.MQTTSubscriptionMutation) (meta.MQTTSessionCASResult, error) {
						if m.Subscription.Stage != meta.MQTTSubscriptionPreparing {
							return f.store.commitSubscription(ctx, m)
						}
						calls++
						if mode == "port-conflict" {
							return meta.MQTTSessionCASResult{}, app.ErrConflict
						}
						if mode == "unchanged-parent" {
							return meta.MQTTSessionCASResult{Status: meta.MQTTSessionCASConflict, CurrentRevision: m.ExpectedRevision}, nil
						}
						if mode == "lost-reply" {
							_, e := f.store.commitSubscription(ctx, m)
							require.NoError(t, e)
							return meta.MQTTSessionCASResult{}, lost
						}
						contend(ctx)
						out, e := f.store.commitSubscription(ctx, m)
						if mode == "canceled" {
							cancel()
						}
						return out, e
					}
				}
				got, err := f.subscriptions.Subscribe(ctx, f.connection.Owner, r)
				f.store.query = nil
				if mode == "renew-once" {
					require.NoError(t, err)
					require.Equal(t, 2, calls)
					require.Equal(t, meta.MQTTSubscriptionActive, got.Stage)
					require.Greater(t, got.Generation, before.Generation)
				} else {
					require.Error(t, err)
					require.Zero(t, got)
					want := 1
					if mode == "renew-forever" {
						want = 3
					}
					require.Equal(t, want, calls)
					if mode == "lost-reply" {
						require.ErrorIs(t, err, lost)
					}
				}
				require.Zero(t, f.owners.Snapshot().Operations)
			})
		}
	}
}

type preparingCursorStore struct {
	*groupSourceStore
	init func(context.Context, meta.MQTTDeliveryCursorMutation) (meta.MQTTDeliveryCursorResult, error)
}

func (s *preparingCursorStore) MutateMQTTDeliveryCursor(ctx context.Context, m meta.MQTTDeliveryCursorMutation) (meta.MQTTDeliveryCursorResult, error) {
	return s.init(ctx, m)
}

func TestGroupCursorInitRebasesOnlyDefinitePinnedIntent(t *testing.T) {
	for _, mode := range []string{"renew-once", "renew-forever", "changed-child", "changed-owner", "unchanged-parent", "port-conflict", "lost-reply", "canceled", "cursor-installed"} {
		t.Run(mode, func(t *testing.T) {
			f := setupGroupSource(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			lost := errors.New("cursor reply unavailable")
			store := &preparingCursorStore{groupSourceStore: f.store}
			store.init = func(ctx context.Context, m meta.MQTTDeliveryCursorMutation) (meta.MQTTDeliveryCursorResult, error) {
				calls++
				require.EqualValues(t, 10, m.Through, "rejection must retain the first durable source boundary")
				if mode == "port-conflict" {
					return meta.MQTTDeliveryCursorResult{}, app.ErrConflict
				}
				if mode == "unchanged-parent" {
					return meta.MQTTDeliveryCursorResult{Status: meta.MQTTSessionCASConflict, CurrentRevision: m.ExpectedRevision}, nil
				}
				if mode == "lost-reply" {
					_, e := f.store.MutateMQTTDeliveryCursor(ctx, m)
					require.NoError(t, e)
					return meta.MQTTDeliveryCursorResult{}, lost
				}
				if calls == 1 || mode == "renew-forever" {
					_, e := f.service.Renew(ctx, f.connection.Owner)
					require.NoError(t, e)
				}
				out, e := f.store.MutateMQTTDeliveryCursor(ctx, m)
				if calls == 1 {
					f.tail = 20
					if mode == "cursor-installed" {
						m.ExpectedRevision = f.row(t).Revision
						other, e := f.store.MutateMQTTDeliveryCursor(ctx, m)
						require.NoError(t, e)
						require.Equal(t, meta.MQTTSessionCASApplied, other.Status)
					}
					if mode == "changed-child" || mode == "changed-owner" {
						f.store.query = func(ctx context.Context, q meta.MQTTRead) (meta.MQTTReadResult, error) {
							r, e := f.store.sessionStore.ReadMQTT(ctx, q)
							if r.Session != nil && mode == "changed-owner" {
								r.Session.OwnerGeneration++
							}
							if len(r.Subscriptions) == 1 && mode == "changed-child" {
								r.Subscriptions[0].NoLocal = !r.Subscriptions[0].NoLocal
							}
							return r, e
						}
					}
					if mode == "canceled" {
						cancel()
					}
				}
				return out, e
			}
			f.opts.Store = store
			var err error
			f.sources, err = app.NewGroupSources(f.opts)
			require.NoError(t, err)
			got, err := f.sources.Prepare(ctx, f.connection.Owner, f.intent.Topic)
			if mode == "renew-once" || mode == "cursor-installed" {
				require.NoError(t, err)
				want := 2
				if mode == "cursor-installed" {
					want = 1
				}
				require.Equal(t, want, calls)
				require.EqualValues(t, 10, got.Cursor.StartAfter)
			} else {
				require.Error(t, err)
				require.Zero(t, got)
				want := 1
				if mode == "renew-forever" {
					want = 3
				}
				require.Equal(t, want, calls)
				if mode == "lost-reply" {
					require.ErrorIs(t, err, lost)
				}
			}
			require.Zero(t, f.owners.Snapshot().Operations)
		})
	}
}
