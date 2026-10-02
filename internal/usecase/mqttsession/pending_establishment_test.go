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

type pendingEstablishmentProjection func(context.Context, app.SubscriptionProjectionRequest) (app.SubscriptionProjectionReceipt, error)

func (f pendingEstablishmentProjection) EstablishOffline(ctx context.Context, r app.SubscriptionProjectionRequest) (app.SubscriptionProjectionReceipt, error) {
	return f(ctx, r)
}

type pendingEstablishmentEnder func(context.Context, app.EndCommand) error

func (f pendingEstablishmentEnder) End(ctx context.Context, r app.EndCommand) error { return f(ctx, r) }

type pendingEstablishmentFixture struct {
	*subscriptionsFixture
	intent          meta.MQTTSubscription
	options         app.SubscriptionEstablishmentOptions
	afterProjection func(context.Context)
	projections     int
}

func setupPendingEstablishment(t *testing.T, target string) *pendingEstablishmentFixture {
	t.Helper()
	f := &pendingEstablishmentFixture{}
	var project pendingEstablishmentProjection
	if target == "group" {
		g := setupGroupSource(t)
		f.subscriptionsFixture, f.intent = g.subscriptionsFixture, g.intent
		project = offlineGroupProjection(t, g, func(context.Context, meta.MQTTBindingOwner, uint64) error { return nil }).EstablishOffline
	} else {
		i := setupInboxEstablishment(t)
		i.directory(t, meta.ChannelKey{ChannelID: i.channel.ID, ChannelType: 1})
		f.subscriptionsFixture, f.intent = i.subscriptionsFixture, i.intent
		p, err := app.NewInboxEstablishment(i.options)
		require.NoError(t, err)
		project = p.EstablishOffline
	}
	require.NoError(t, f.service.Disconnect(context.Background(), app.DisconnectCommand{Owner: f.connection.Owner, Normal: true}))
	wrapped := pendingEstablishmentProjection(func(ctx context.Context, r app.SubscriptionProjectionRequest) (app.SubscriptionProjectionReceipt, error) {
		f.projections++
		out, err := project(ctx, r)
		if err == nil && f.afterProjection != nil {
			f.afterProjection(ctx)
		}
		return out, err
	})
	f.options = app.SubscriptionEstablishmentOptions{Store: f.store, Inbox: wrapped, Groups: wrapped, Authorization: f.subscriptionsFixture.options.Authorization, Ender: f.service, Now: func() time.Time { return f.now }}
	return f
}

func TestPendingEstablishmentCompletesBothTargetsAndRetainsFinalWriteRecovery(t *testing.T) {
	for _, target := range []string{"group", "inbox"} {
		for _, cut := range []string{"none", "before_commit", "lost_reply", "cas_rejected", "parent_update"} {
			t.Run(target+"/"+cut, func(t *testing.T) {
				f := setupPendingEstablishment(t, target)
				p, err := app.NewSubscriptionEstablishment(f.options)
				require.NoError(t, err)
				before := f.row(t)
				lost := errors.New("final reply unknown")
				writes := 0
				f.store.mutate = func(ctx context.Context, m meta.MQTTSubscriptionMutation) (meta.MQTTSessionCASResult, error) {
					writes++
					require.Equal(t, meta.MQTTSubscriptionActive, m.Subscription.Stage)
					if cut == "before_commit" {
						return meta.MQTTSessionCASResult{}, lost
					}
					if cut == "cas_rejected" {
						return meta.MQTTSessionCASResult{Status: meta.MQTTSessionCASConflict}, nil
					}
					r, e := f.store.commitSubscription(ctx, m)
					if cut == "lost_reply" {
						require.NoError(t, e)
						return meta.MQTTSessionCASResult{}, lost
					}
					return r, e
				}
				if cut == "parent_update" {
					f.afterProjection = func(ctx context.Context) {
						row := f.row(t)
						rev := row.Revision
						row.Revision++
						_, e := f.store.CompareAndSwapMQTTSession(ctx, rev, row)
						require.NoError(t, e)
					}
				}
				hint := removalHint(f.intent)
				hint.RecoveryAtMS = 1 << 60
				out, err := p.Reconcile(context.Background(), hint)
				if cut == "before_commit" || cut == "lost_reply" || cut == "cas_rejected" {
					require.Error(t, err)
					require.Zero(t, out)
					require.Equal(t, 1, writes, "one attempt, including definite rejection")
					page, e := f.store.ReadMQTT(context.Background(), meta.MQTTRead{Kind: meta.MQTTReadSubscriptionRecovery, Limit: 16})
					require.NoError(t, e)
					if cut == "lost_reply" {
						require.Empty(t, page.Subscriptions)
					} else {
						require.Len(t, page.Subscriptions, 1)
						require.Equal(t, meta.MQTTSubscriptionPreparing, page.Subscriptions[0].Stage)
					}
					f.store.mutate = nil
					out, err = p.Reconcile(context.Background(), hint)
				}
				require.NoError(t, err)
				require.True(t, out.Activated)
				require.False(t, out.RevokedEnded)
				active := f.subscription(t, f.intent.Topic)
				want := f.intent
				want.Stage = meta.MQTTSubscriptionActive
				want.RecoveryAtMS = 0
				want.Revision = active.Revision
				want.UpdatedAtMS = active.UpdatedAtMS
				require.Equal(t, want, active)
				require.Equal(t, meta.MQTTSessionOffline, f.row(t).State)
				require.Equal(t, before.OfflineExpiresAtMS, f.row(t).OfflineExpiresAtMS)
				require.Zero(t, f.owners.Snapshot().Operations)
				count := f.store.mutations
				f.afterProjection = nil
				out, err = p.Reconcile(context.Background(), hint)
				require.NoError(t, err)
				require.True(t, out.Activated)
				require.Equal(t, count, f.store.mutations)
			})
		}
	}
}

func TestPendingEstablishmentRejectsUnprovedFinalCompletion(t *testing.T) {
	for _, mode := range []string{"owner", "child", "expired", "cancel", "panic", "wrong_receipt", "wrong_revision", "permission_unknown", "projection_unknown"} {
		t.Run(mode, func(t *testing.T) {
			f := setupPendingEstablishment(t, "group")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f.afterProjection = func(ctx context.Context) {
				switch mode {
				case "owner":
					_, e := f.service.Connect(ctx, command())
					require.NoError(t, e)
				case "child":
					f.store.query = func(ctx context.Context, q meta.MQTTRead) (meta.MQTTReadResult, error) {
						r, e := f.store.sessionStore.ReadMQTT(ctx, q)
						if q.Kind == meta.MQTTReadSubscription {
							r.Subscriptions[0].NoLocal = !r.Subscriptions[0].NoLocal
						}
						return r, e
					}
				case "expired":
					f.now = f.now.Add(time.Duration(f.row(t).OfflineExpiresAtMS-f.now.UnixMilli()) * time.Millisecond)
				case "cancel":
					cancel()
				case "panic":
					panic("sensitive")
				}
			}
			if mode == "wrong_receipt" || mode == "projection_unknown" {
				f.options.Groups = pendingEstablishmentProjection(func(context.Context, app.SubscriptionProjectionRequest) (app.SubscriptionProjectionReceipt, error) {
					if mode == "projection_unknown" {
						return app.SubscriptionProjectionReceipt{}, errors.New("source unavailable")
					}
					return app.SubscriptionProjectionReceipt{}, nil
				})
			}
			if mode == "permission_unknown" {
				f.options.Authorization = subscriptionAuthorizer(func(context.Context, string, app.SubscriptionRequest) (uint64, error) {
					return 0, errors.New("authority unavailable")
				})
			}
			if mode == "wrong_revision" {
				f.store.mutate = func(context.Context, meta.MQTTSubscriptionMutation) (meta.MQTTSessionCASResult, error) {
					return meta.MQTTSessionCASResult{Status: meta.MQTTSessionCASApplied, CurrentRevision: 1}, nil
				}
			}
			p, err := app.NewSubscriptionEstablishment(f.options)
			require.NoError(t, err)
			out, err := p.Reconcile(ctx, removalHint(f.intent))
			require.Error(t, err)
			require.Zero(t, out)
			require.NotContains(t, err.Error(), "sensitive")
			f.store.query = nil
			require.Equal(t, meta.MQTTSubscriptionPreparing, f.subscription(t, f.intent.Topic).Stage)
			require.NotEqual(t, meta.MQTTSessionEnded, f.row(t).State)
		})
	}
}

func TestPendingEstablishmentRevocationUsesExactEndingAndRecoversLostReply(t *testing.T) {
	for _, mode := range []string{"denied", "version", "after_projection", "end_error", "end_lost_reply", "end_late", "takeover_before_end"} {
		t.Run(mode, func(t *testing.T) {
			f := setupPendingEstablishment(t, "group")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "version" {
				f.version++
			} else if mode == "after_projection" {
				f.afterProjection = func(context.Context) { f.denied = true }
			} else {
				f.denied = true
			}
			calls := 0
			lost := errors.New("ending unknown")
			f.options.Ender = pendingEstablishmentEnder(func(ctx context.Context, c app.EndCommand) error {
				calls++
				require.Equal(t, f.connection.Owner, c.Owner)
				require.Equal(t, meta.MQTTSessionRevoked, c.Reason)
				if mode == "end_error" {
					return lost
				}
				if mode == "takeover_before_end" {
					_, e := f.service.Connect(ctx, command())
					require.NoError(t, e)
				}
				err := f.service.End(ctx, c)
				if mode == "end_lost_reply" && calls == 1 {
					require.NoError(t, err)
					return lost
				}
				if mode == "end_late" {
					cancel()
				}
				return err
			})
			p, err := app.NewSubscriptionEstablishment(f.options)
			require.NoError(t, err)
			out, err := p.Reconcile(ctx, removalHint(f.intent))
			if mode == "end_error" || mode == "end_lost_reply" || mode == "end_late" || mode == "takeover_before_end" {
				require.Error(t, err)
				require.Zero(t, out)
				if mode != "end_lost_reply" {
					return
				}
				out, err = p.Reconcile(ctx, removalHint(f.intent))
				require.Equal(t, 2, calls)
			}
			require.NoError(t, err)
			require.False(t, out.Activated)
			require.True(t, out.RevokedEnded)
			require.Equal(t, meta.MQTTSessionEnded, f.row(t).State)
			require.Equal(t, meta.MQTTSessionRevoked, f.row(t).TerminationReason)
			require.Equal(t, meta.MQTTSubscriptionPreparing, f.subscription(t, f.intent.Topic).Stage)
		})
	}
}

func TestPendingEstablishmentIgnoresInapplicableHints(t *testing.T) {
	for _, mode := range []string{"active_owner", "new_lifetime", "missing", "removing", "ended_quota"} {
		t.Run(mode, func(t *testing.T) {
			f := setupPendingEstablishment(t, "inbox")
			if mode == "active_owner" {
				_, err := f.service.Connect(context.Background(), command())
				require.NoError(t, err)
			}
			f.store.query = func(ctx context.Context, q meta.MQTTRead) (meta.MQTTReadResult, error) {
				r, e := f.store.sessionStore.ReadMQTT(ctx, q)
				if q.Kind == meta.MQTTReadSubscription {
					switch mode {
					case "new_lifetime":
						r.Session.Generation++
					case "missing":
						r.Subscriptions = nil
					case "removing":
						r.Subscriptions[0].Stage = meta.MQTTSubscriptionRemoving
					case "ended_quota":
						r.Session.State, r.Session.OfflineExpiresAtMS, r.Session.TerminationReason = meta.MQTTSessionEnded, 0, meta.MQTTSessionQuota
					}
				}
				return r, e
			}
			p, err := app.NewSubscriptionEstablishment(f.options)
			require.NoError(t, err)
			before := f.store.mutations
			out, err := p.Reconcile(context.Background(), removalHint(f.intent))
			require.NoError(t, err)
			require.Zero(t, out)
			require.Zero(t, f.projections)
			require.Equal(t, before, f.store.mutations)
		})
	}
}
