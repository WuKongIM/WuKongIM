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

// Failure inventory: owner renewal may reject cursor Init after a fixed source
// boundary is durable. Resume only definite CAS rejection with the same Owner
// and full child, newer parent, and unchanged boundary; cap proposals at three.
// Changed authority/intent, unchanged parent, uncertain replies, cancellation,
// and a foreign cursor cannot grant retry or a successful preparation receipt.
func TestInboxCursorInitRebasesOnlyDefinitePinnedIntent(t *testing.T) {
	for _, mode := range []string{"renew-once", "renew-forever", "changed-child", "changed-owner", "unchanged-parent", "port-conflict", "lost-reply", "canceled", "cursor-installed", "foreign-cursor"} {
		t.Run(mode, func(t *testing.T) {
			f := setupInboxSource(t)
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
					if mode == "cursor-installed" || mode == "foreign-cursor" {
						if mode == "foreign-cursor" {
							m.Through++
						}
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
			var err error
			f.sources, err = app.NewInboxSources(app.InboxSourceOptions{Store: store, Now: func() time.Time { return f.now }, Sources: groupSourceProtector(func(_ context.Context, ch app.SourceChannel) (app.ProtectedSource, error) {
				return app.ProtectedSource{Channel: ch, Generation: "person-generation", CommittedThrough: f.tail}, nil
			})})
			require.NoError(t, err)
			got, err := f.sources.PrepareIntent(ctx, app.SubscriptionProjectionRequest{Owner: f.connection.Owner, UID: "alice", Subscription: f.intent}, f.qualification.Key, f.channel)
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
