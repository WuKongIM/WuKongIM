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

func removalHint(r meta.MQTTSubscription) meta.MQTTSubscriptionRecoveryCursor {
	return meta.MQTTSubscriptionRecoveryCursor{Namespace: r.Namespace, ClientID: r.ClientID, SessionGeneration: r.SessionGeneration, Topic: r.Topic}
}
func pendingRemoval(t *testing.T, f *inboxRemovalFixture) *app.SubscriptionRemoval {
	t.Helper()
	groups, err := app.NewSourceDrain(app.SourceDrainOptions{Store: f.s, Now: func() time.Time { return f.now }})
	require.NoError(t, err)
	p, err := app.NewSubscriptionRemoval(app.SubscriptionRemovalOptions{Store: f.s, Inbox: closedInboxRemoval(t, f), Groups: groups, Now: func() time.Time { return f.now }})
	require.NoError(t, err)
	return p
}
func TestPendingRemovalRetainsSubscriptionRecoveryAfterQualificationDone(t *testing.T) {
	for _, cut := range []string{"before_commit", "lost_reply"} {
		t.Run(cut, func(t *testing.T) {
			f, r := offlineInboxRemoval(t, 1)
			p := pendingRemoval(t, f)
			lost := errors.New("subscription completion interrupted")
			old := f.row(t)
			f.store.mutate = func(ctx context.Context, m meta.MQTTSubscriptionMutation) (meta.MQTTSessionCASResult, error) {
				require.Equal(t, meta.MQTTSubscriptionRemoved, m.Subscription.Stage)
				if cut == "lost_reply" {
					_, err := f.store.commitSubscription(ctx, m)
					require.NoError(t, err)
				}
				return meta.MQTTSessionCASResult{}, lost
			}
			completed, err := p.Reconcile(context.Background(), removalHint(r.Subscription))
			require.ErrorIs(t, err, lost)
			require.False(t, completed)
			require.Equal(t, meta.MQTTBindingRemoved, f.readQualification(t).Stage)
			page, err := f.s.ReadMQTT(context.Background(), meta.MQTTRead{Kind: meta.MQTTReadSubscriptionRecovery, Limit: 16})
			require.NoError(t, err)
			if cut == "before_commit" {
				require.Len(t, page.Subscriptions, 1)
				require.Equal(t, meta.MQTTSubscriptionRemoving, page.Subscriptions[0].Stage)
			} else {
				require.Empty(t, page.Subscriptions)
			}
			f.store.mutate = nil
			completed, err = p.Reconcile(context.Background(), removalHint(r.Subscription))
			require.NoError(t, err)
			require.True(t, completed)
			require.Equal(t, meta.MQTTSubscriptionRemoved, f.subscription(t, r.Subscription.Topic).Stage)
			page, err = f.s.ReadMQTT(context.Background(), meta.MQTTRead{Kind: meta.MQTTReadSubscriptionRecovery, Limit: 16})
			require.NoError(t, err)
			require.Empty(t, page.Subscriptions)
			require.EqualValues(t, 1, f.row(t).OutboundInflight)
			require.EqualValues(t, 1, f.row(t).PendingMessages)
			require.Equal(t, old.OfflineExpiresAtMS, f.row(t).OfflineExpiresAtMS)
			require.Equal(t, meta.MQTTSessionOffline, f.row(t).State)
		})
	}
}
func TestPendingRemovalDoesNotActivateOrFollowNewLifetime(t *testing.T) {
	for _, mode := range []string{"preparing", "active", "new_lifetime", "ended", "missing"} {
		t.Run(mode, func(t *testing.T) {
			f, r := offlineInboxRemoval(t, 0)
			p := pendingRemoval(t, f)
			f.s.read = func(q meta.MQTTRead, page *meta.MQTTReadResult) error {
				if q.Kind == meta.MQTTReadSubscription {
					switch mode {
					case "preparing":
						page.Subscriptions[0].Stage = meta.MQTTSubscriptionPreparing
					case "active":
						page.Subscriptions[0].Stage = meta.MQTTSubscriptionActive
						page.Subscriptions[0].RecoveryAtMS = 0
					case "new_lifetime":
						page.Session.Generation++
					case "ended":
						page.Session.State = meta.MQTTSessionEnded
						page.Session.OfflineExpiresAtMS = 0
						page.Session.TerminationReason = meta.MQTTSessionExpired
					case "missing":
						page.Subscriptions = nil
					}
				}
				return nil
			}
			before := f.store.mutations
			completed, err := p.Reconcile(context.Background(), removalHint(r.Subscription))
			require.NoError(t, err)
			require.False(t, completed)
			require.Equal(t, before, f.store.mutations)
			require.Zero(t, f.s.writes)
		})
	}
}
func TestPendingRemovalRejectsUnprovedFinalAuthority(t *testing.T) {
	for _, mode := range []string{"extra", "partial", "owner_changed", "child_changed", "late_cancel", "cas_conflict", "cas_receipt", "panic"} {
		t.Run(mode, func(t *testing.T) {
			f, r := offlineInboxRemoval(t, 0)
			p := pendingRemoval(t, f)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			completedProjection := false
			f.store.afterBinding = func(b meta.MQTTSourceBinding) error {
				if b.Stage == meta.MQTTBindingRemoved {
					completedProjection = true
				}
				return nil
			}
			f.s.read = func(q meta.MQTTRead, page *meta.MQTTReadResult) error {
				if mode == "extra" {
					page.Runtime = &meta.MQTTRuntimeView{}
				}
				if mode == "partial" {
					page.Done = false
				}
				if completedProjection && q.Kind == meta.MQTTReadSubscription {
					switch mode {
					case "owner_changed":
						page.Session.OwnerGeneration++
					case "child_changed":
						page.Subscriptions[0].Revision++
					case "late_cancel":
						cancel()
					}
				}
				return nil
			}
			f.store.mutate = func(context.Context, meta.MQTTSubscriptionMutation) (meta.MQTTSessionCASResult, error) {
				if mode == "panic" {
					panic("sensitive")
				}
				if mode == "cas_conflict" {
					return meta.MQTTSessionCASResult{Status: meta.MQTTSessionCASConflict}, nil
				}
				return meta.MQTTSessionCASResult{Status: meta.MQTTSessionCASApplied, CurrentRevision: 1}, nil
			}
			completed, err := p.Reconcile(ctx, removalHint(r.Subscription))
			require.Error(t, err)
			require.False(t, completed)
			require.NotContains(t, err.Error(), "sensitive")
			f.s.read = nil
			require.Equal(t, meta.MQTTSubscriptionRemoving, f.subscription(t, r.Subscription.Topic).Stage)
		})
	}
}
func TestPendingRemovalGroupDiscoveryBeforeBindingOffline(t *testing.T) {
	for _, cut := range []string{"none", "registration_reply", "cursor"} {
		t.Run(cut, func(t *testing.T) {
			f := setupGroupSource(t)
			if cut == "cursor" {
				_, err := f.prepare()
				require.NoError(t, err)
			}
			f.project.remove = func(context.Context, app.SubscriptionProjectionRequest) (app.SubscriptionProjectionReceipt, error) {
				return app.SubscriptionProjectionReceipt{}, app.ErrSourceDrainPending
			}
			_, err := f.subscriptions.Unsubscribe(context.Background(), f.connection.Owner, f.intent.Topic)
			require.ErrorIs(t, err, app.ErrSourceDrainPending)
			require.NoError(t, f.service.Disconnect(context.Background(), app.DisconnectCommand{Owner: f.connection.Owner, Normal: true}))
			store := &drainStore{progressStore: &progressStore{groupSourceStore: f.store}}
			groups, err := app.NewSourceDrain(app.SourceDrainOptions{Store: store, Sources: f.opts.Sources, Now: func() time.Time { return f.now }})
			require.NoError(t, err)
			inbox, err := app.NewInboxRemoval(app.InboxRemovalOptions{Store: store, ClosedDrain: groups, Now: func() time.Time { return f.now }})
			require.NoError(t, err)
			p, err := app.NewSubscriptionRemoval(app.SubscriptionRemovalOptions{Store: store, Inbox: inbox, Groups: groups, Now: func() time.Time { return f.now }})
			require.NoError(t, err)
			if cut == "registration_reply" {
				f.store.afterBinding = func(meta.MQTTSourceBinding) error { return errors.New("lost registration") }
			}
			completed, err := p.Reconcile(context.Background(), removalHint(f.intent))
			if cut == "registration_reply" {
				require.Error(t, err)
				require.False(t, completed)
				f.store.afterBinding = nil
				completed, err = p.Reconcile(context.Background(), removalHint(f.intent))
			}
			require.NoError(t, err)
			require.True(t, completed)
			require.Equal(t, meta.MQTTSubscriptionRemoved, f.subscription(t, f.intent.Topic).Stage)
			require.Equal(t, meta.MQTTSessionOffline, f.row(t).State)
			require.Zero(t, f.row(t).PendingMessages)
			require.Zero(t, f.owners.Snapshot().Held)
		})
	}
}

func TestPendingRemovalForegroundAcceptsSameCompletedIntent(t *testing.T) {
	for _, cut := range []string{"projection_success", "projection_conflict", "projection_pending", "completion_race"} {
		t.Run(cut, func(t *testing.T) {
			f := setupInboxRemoval(t, 1)
			p := pendingRemoval(t, f)
			foreground, err := app.NewInboxRemoval(f.options)
			require.NoError(t, err)
			f.project.remove = func(ctx context.Context, r app.SubscriptionProjectionRequest) (app.SubscriptionProjectionReceipt, error) {
				if cut == "completion_race" {
					return foreground.Remove(ctx, r)
				}
				done, err := p.Reconcile(ctx, removalHint(r.Subscription))
				require.NoError(t, err)
				require.True(t, done)
				if cut == "projection_conflict" {
					return app.SubscriptionProjectionReceipt{}, app.ErrConflict
				}
				if cut == "projection_pending" {
					return app.SubscriptionProjectionReceipt{}, app.ErrSourceDrainPending
				}
				return projectionReceipt(r), nil
			}
			if cut == "completion_race" {
				f.store.mutate = func(ctx context.Context, m meta.MQTTSubscriptionMutation) (meta.MQTTSessionCASResult, error) {
					if m.Subscription.Stage == meta.MQTTSubscriptionRemoved {
						f.store.mutate = nil
						done, err := p.Reconcile(ctx, removalHint(m.Subscription))
						require.NoError(t, err)
						require.True(t, done)
					}
					return f.store.commitSubscription(ctx, m)
				}
			}
			existed, err := f.subscriptions.Unsubscribe(context.Background(), f.connection.Owner, f.intent.Topic)
			require.NoError(t, err)
			require.True(t, existed)
			require.EqualValues(t, 1, f.row(t).PendingMessages)
			require.EqualValues(t, 1, f.row(t).OutboundInflight)
			require.Equal(t, meta.MQTTSubscriptionRemoved, f.subscription(t, f.intent.Topic).Stage)
			require.Zero(t, f.owners.Snapshot().Operations)
		})
	}
}

func TestPendingRemovalForegroundDoesNotAcceptDifferentCompletedChild(t *testing.T) {
	for _, fault := range []string{"generation", "operation", "options", "owner"} {
		t.Run(fault, func(t *testing.T) {
			f := setupInboxRemoval(t, 0)
			p := pendingRemoval(t, f)
			f.project.remove = func(ctx context.Context, r app.SubscriptionProjectionRequest) (app.SubscriptionProjectionReceipt, error) {
				done, err := p.Reconcile(ctx, removalHint(r.Subscription))
				require.NoError(t, err)
				require.True(t, done)
				f.store.query = func(ctx context.Context, q meta.MQTTRead) (meta.MQTTReadResult, error) {
					page, err := f.store.sessionStore.ReadMQTT(ctx, q)
					if q.Kind == meta.MQTTReadSubscription && len(page.Subscriptions) == 1 {
						switch fault {
						case "generation":
							page.Subscriptions[0].Generation++
						case "operation":
							page.Subscriptions[0].OperationID = "other"
						case "options":
							page.Subscriptions[0].NoLocal = !page.Subscriptions[0].NoLocal
						case "owner":
							page.Session.OwnerGeneration++
						}
					}
					return page, err
				}
				return app.SubscriptionProjectionReceipt{}, app.ErrConflict
			}
			existed, err := f.subscriptions.Unsubscribe(context.Background(), f.connection.Owner, f.intent.Topic)
			require.Error(t, err)
			require.False(t, existed)
		})
	}
}
