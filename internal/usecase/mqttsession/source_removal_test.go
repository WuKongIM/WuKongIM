package mqttsession_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	app "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestSourceRemovalAcknowledgesEndedLifetimeBeforeRemovingAndRecoversLostReplies(t *testing.T) {
	for _, mode := range []string{"ended", "new_lifetime", "lost_ack", "lost_removal"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			f, s, progress, prepared := progressFixture(t)
			removal, err := app.NewSourceRemoval(app.SourceRemovalOptions{Store: s, Now: func() time.Time { return f.now }})
			require.NoError(t, err)
			if mode == "new_lifetime" {
				cmd := command()
				cmd.CleanStart = true
				_, err = f.service.Connect(ctx, cmd)
				require.NoError(t, err)
			} else {
				zero := uint32(0)
				require.NoError(t, f.service.Disconnect(ctx, app.DisconnectCommand{Owner: f.connection.Owner, Normal: true, SessionExpirySec: &zero}))
			}
			ended, err := progress.Reconcile(ctx, prepared.Binding.Key)
			require.NoError(t, err)
			require.True(t, ended.NeedsRemoval)
			lost := errors.New("committed release reply lost")
			f.store.afterBinding = func(b meta.MQTTSourceBinding) error {
				if mode == "lost_ack" && b.Stage == meta.MQTTBindingRemoving || mode == "lost_removal" && b.Stage == meta.MQTTBindingRemoved {
					return lost
				}
				return nil
			}
			ack, err := removal.Reconcile(ctx, prepared.Binding.Key)
			if mode == "lost_ack" {
				require.ErrorIs(t, err, lost)
				require.Zero(t, ack)
			} else {
				require.NoError(t, err)
				require.True(t, ack.Changed)
				require.Equal(t, meta.MQTTBindingRemoving, ack.Binding.Stage)
				require.Equal(t, ack.Binding.Revision, ack.Binding.ProtectionRevision)
			}
			// The separately committed acknowledgement still retains responsibility.
			retained, err := f.store.ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadSourceRetention, Owner: prepared.Binding.Key.Owner, Limit: 1})
			require.NoError(t, err)
			require.Len(t, retained.Bindings, 1)
			require.Equal(t, meta.MQTTBindingRemoving, retained.Bindings[0].Stage)
			require.Positive(t, retained.Bindings[0].RecoveryAtMS)
			removed, err := removal.Reconcile(ctx, prepared.Binding.Key)
			if mode == "lost_removal" {
				require.ErrorIs(t, err, lost)
				require.Zero(t, removed)
			} else {
				require.NoError(t, err)
				require.Equal(t, meta.MQTTBindingRemoved, removed.Binding.Stage)
			}
			f.store.afterBinding = nil
			again, err := removal.Reconcile(ctx, prepared.Binding.Key)
			require.NoError(t, err)
			require.False(t, again.Changed)
			require.Equal(t, meta.MQTTBindingRemoved, again.Binding.Stage)
			require.Equal(t, ended.Binding.Revision+2, again.Binding.Revision)
			require.Equal(t, ended.Binding.Revision+1, again.Binding.ProtectionRevision)
			require.Equal(t, ended.Binding.ProgressRevision, again.Binding.ProgressRevision)
			require.Positive(t, again.Binding.RecoveryAtMS, "Removed stays scheduled for tombstone retirement")
			retained, err = f.store.ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadSourceRetention, Owner: prepared.Binding.Key.Owner, Limit: 1})
			require.NoError(t, err)
			require.Empty(t, retained.Bindings)
		})
	}
}

func completedRemovalFixture(t *testing.T, normal bool) (*groupSourceFixture, *progressStore, *app.SourceRemoval, meta.MQTTSourceBinding) {
	t.Helper()
	ctx := context.Background()
	f, s, progress, prepared := progressFixture(t)
	if normal {
		ack := advanceProgressWindow(t, f, prepared)
		ack(0)
		ack(1)
		_, err := f.subscriptions.Unsubscribe(ctx, f.connection.Owner, prepared.Binding.Topic)
		require.NoError(t, err)
		sealed := prepared.Binding
		sealed.Revision++
		sealed.IntentRevision = f.subscription(t, prepared.Binding.Topic).Revision
		sealed.Stage, sealed.EndKnown, sealed.EndThrough = meta.MQTTBindingRemoving, true, prepared.Cursor.StartAfter+2
		r, err := f.store.CompareAndSwapMQTTSourceBinding(ctx, prepared.Binding.Revision, sealed)
		require.NoError(t, err)
		require.Equal(t, meta.MQTTSessionCASApplied, r.Status)
	} else {
		zero := uint32(0)
		require.NoError(t, f.service.Disconnect(ctx, app.DisconnectCommand{Owner: f.connection.Owner, Normal: true, SessionExpirySec: &zero}))
	}
	b, err := progress.Reconcile(ctx, prepared.Binding.Key)
	require.NoError(t, err)
	require.True(t, b.NeedsRemoval)
	r, err := app.NewSourceRemoval(app.SourceRemovalOptions{Store: s, Now: func() time.Time { return f.now }})
	require.NoError(t, err)
	return f, s, r, b.Binding
}

func TestSourceRemovalRejectsUnprovenViewsAndUncertainWrites(t *testing.T) {
	for _, mode := range []string{"binding_missing", "binding_key", "binding_extra", "session_missing", "session_uid", "session_generation", "session_revision", "session_offline", "extra", "partial", "page_cursor", "read_error", "cancel_read", "cas_conflict", "cas_error", "cas_revision", "cas_status", "cancel_write", "clock", "overflow", "final_read_error"} {
		t.Run(mode, func(t *testing.T) {
			f, s, removal, original := completedRemovalFixture(t, false)
			if mode == "final_read_error" {
				ack, err := removal.Reconcile(context.Background(), original.Key)
				require.NoError(t, err)
				original = ack.Binding
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s.read = func(q meta.MQTTRead, r *meta.MQTTReadResult) error {
				if q.Kind == meta.MQTTReadSourceBinding {
					switch mode {
					case "binding_missing":
						r.Bindings = nil
					case "binding_key":
						r.Bindings[0].Key.ClientID = "foreign"
					case "binding_extra":
						r.Bindings = append(r.Bindings, r.Bindings[0])
					case "overflow":
						r.Bindings[0].Revision = ^uint64(0)
					}
					return nil
				}
				switch mode {
				case "session_missing":
					r.Session = nil
				case "session_uid":
					r.Session.UID = "foreign"
				case "session_generation":
					r.Session.Generation = 0
				case "session_revision":
					r.Session.Revision = original.ProgressRevision - 1
				case "session_offline":
					r.Session.State, r.Session.TerminationReason = meta.MQTTSessionOffline, 0
					r.Session.OfflineExpiresAtMS = f.now.Add(time.Hour).UnixMilli()
				case "extra":
					r.Bindings = []meta.MQTTSourceBinding{original}
				case "partial":
					r.Done = false
				case "page_cursor":
					r.After.Topic = "unexpected"
				case "read_error", "final_read_error":
					return context.DeadlineExceeded
				case "cancel_read":
					cancel()
				}
				return nil
			}
			if strings.HasPrefix(mode, "cas_") || mode == "cancel_write" {
				s.write = func(_ context.Context, v uint64, _ meta.MQTTSourceBinding) (meta.MQTTSourceBindingResult, error) {
					switch mode {
					case "cas_conflict":
						return meta.MQTTSourceBindingResult{Status: meta.MQTTSessionCASConflict}, nil
					case "cas_error":
						return meta.MQTTSourceBindingResult{}, context.DeadlineExceeded
					case "cas_status":
						return meta.MQTTSourceBindingResult{Status: 99, CurrentRevision: v + 1}, nil
					case "cancel_write":
						cancel()
					}
					return meta.MQTTSourceBindingResult{Status: meta.MQTTSessionCASApplied, CurrentRevision: v}, nil
				}
			}
			if mode == "clock" {
				f.now = time.UnixMilli(1)
			}
			got, err := removal.Reconcile(ctx, original.Key)
			require.Error(t, err)
			require.Zero(t, got)
			durable, err := f.store.ReadMQTT(context.Background(), meta.MQTTRead{Kind: meta.MQTTReadSourceBinding, BindingKey: original.Key})
			require.NoError(t, err)
			require.Equal(t, []meta.MQTTSourceBinding{original}, durable.Bindings)
		})
	}
}

func TestSourceRemovalRejectsChangedSubscriptionAndCursorProof(t *testing.T) {
	for _, mode := range []string{"subscription_missing", "subscription_operation", "subscription_auth", "subscription_target", "subscription_generation", "subscription_revision", "subscription_active", "cursor_missing", "cursor_key", "cursor_auth", "cursor_topic", "cursor_start", "cursor_revision", "cursor_regressed", "cursor_extra", "session_regressed", "accounted_beyond_end"} {
		t.Run(mode, func(t *testing.T) {
			f, s, removal, original := completedRemovalFixture(t, true)
			s.read = func(q meta.MQTTRead, r *meta.MQTTReadResult) error {
				if q.Kind == meta.MQTTReadSubscription {
					sub := &r.Subscriptions[0]
					switch mode {
					case "subscription_missing":
						r.Subscriptions = nil
					case "subscription_operation":
						sub.OperationID = "foreign"
					case "subscription_auth":
						sub.AuthorizationVersion++
					case "subscription_target":
						sub.TargetID = "foreign"
					case "subscription_generation":
						sub.Generation--
					case "subscription_revision":
						sub.Revision--
					case "subscription_active":
						sub.Stage = meta.MQTTSubscriptionActive
					}
				}
				if q.Kind == meta.MQTTReadDeliveryCursor {
					c := &r.DeliveryCursors[0]
					switch mode {
					case "cursor_missing":
						r.DeliveryCursors = nil
					case "cursor_key":
						c.Key.SourceGeneration = "foreign"
					case "cursor_auth":
						c.AuthorizationVersion++
					case "cursor_topic":
						c.Topic = "foreign"
					case "cursor_start":
						c.StartAfter++
					case "cursor_revision":
						c.Revision = r.Session.Revision + 1
					case "cursor_regressed":
						c.Revision = original.ProgressRevision - 1
					case "cursor_extra":
						r.DeliveryCursors = append(r.DeliveryCursors, *c)
					case "session_regressed":
						r.Session.Revision--
					case "accounted_beyond_end":
						c.AccountedThrough++
					}
				}
				return nil
			}
			got, err := removal.Reconcile(context.Background(), original.Key)
			if mode == "subscription_active" {
				require.NoError(t, err)
				require.False(t, got.Changed)
				require.Equal(t, original, got.Binding)
			} else {
				require.Error(t, err)
				require.Zero(t, got)
			}
			durable, err := f.store.ReadMQTT(context.Background(), meta.MQTTRead{Kind: meta.MQTTReadSourceBinding, BindingKey: original.Key})
			require.NoError(t, err)
			require.Equal(t, []meta.MQTTSourceBinding{original}, durable.Bindings)
		})
	}
}

func TestSourceRemovalRequiresClosedAdmissionAndCompleteSealedWindow(t *testing.T) {
	for _, mode := range []string{"removing", "removed", "new_subscription", "binding_changed"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			f, s, progress, prepared := progressFixture(t)
			ack := advanceProgressWindow(t, f, prepared)
			removal, err := app.NewSourceRemoval(app.SourceRemovalOptions{Store: s, Now: func() time.Time { return f.now }})
			require.NoError(t, err)
			if mode == "removing" {
				f.project.remove = func(context.Context, app.SubscriptionProjectionRequest) (app.SubscriptionProjectionReceipt, error) {
					return app.SubscriptionProjectionReceipt{}, app.ErrEvidence
				}
			}
			_, err = f.subscriptions.Unsubscribe(ctx, f.connection.Owner, prepared.Binding.Topic)
			if mode == "removing" {
				require.ErrorIs(t, err, app.ErrEvidence)
			} else {
				require.NoError(t, err)
			}
			sub := f.subscription(t, prepared.Binding.Topic)
			// The projection fixture seals the already-accounted range. Production
			// end capture/release of unadmitted backlog is a separate operation.
			sealed := prepared.Binding
			sealed.Revision++
			sealed.IntentRevision, sealed.Stage = sub.Revision, meta.MQTTBindingRemoving
			sealed.EndKnown, sealed.EndThrough = true, sealed.StartAfter+2
			stored, err := f.store.CompareAndSwapMQTTSourceBinding(ctx, prepared.Binding.Revision, sealed)
			require.NoError(t, err)
			require.Equal(t, meta.MQTTSessionCASApplied, stored.Status)
			if mode == "new_subscription" {
				newSub, e := f.subscriptions.Subscribe(ctx, f.connection.Owner, subscriptionRequest())
				require.NoError(t, e)
				require.Greater(t, newSub.Generation, sub.Generation)
			}
			ack(1)
			gap, err := removal.Reconcile(ctx, prepared.Binding.Key)
			require.NoError(t, err)
			require.False(t, gap.Changed)
			require.Equal(t, sealed, gap.Binding)
			ack(0)
			completed, err := progress.Reconcile(ctx, prepared.Binding.Key)
			require.NoError(t, err)
			require.Equal(t, sealed.EndThrough, completed.Binding.CompletedThrough)
			confirmed, err := removal.Reconcile(ctx, prepared.Binding.Key)
			require.NoError(t, err)
			require.True(t, confirmed.Changed)
			require.Equal(t, meta.MQTTBindingRemoving, confirmed.Binding.Stage)
			require.Equal(t, confirmed.Binding.Revision, confirmed.Binding.ProtectionRevision)
			if mode == "binding_changed" {
				changed := confirmed.Binding
				changed.Revision++
				result, e := f.store.CompareAndSwapMQTTSourceBinding(ctx, confirmed.Binding.Revision, changed)
				require.NoError(t, e)
				require.Equal(t, meta.MQTTSessionCASApplied, result.Status)
				confirmed, err = removal.Reconcile(ctx, prepared.Binding.Key)
				require.NoError(t, err)
				require.Equal(t, meta.MQTTBindingRemoving, confirmed.Binding.Stage)
				require.Equal(t, changed.Revision+1, confirmed.Binding.ProtectionRevision)
			}
			removed, err := removal.Reconcile(ctx, prepared.Binding.Key)
			require.NoError(t, err)
			require.Equal(t, meta.MQTTBindingRemoved, removed.Binding.Stage)
			require.Equal(t, meta.MQTTBindingDrained, removed.Binding.ReleaseReason)
			require.Equal(t, confirmed.Binding.ProtectionRevision, removed.Binding.ProtectionRevision)
			require.Equal(t, sealed.EndThrough, removed.Binding.CompletedThrough)
			if mode == "new_subscription" {
				require.Equal(t, meta.MQTTSubscriptionActive, f.subscription(t, prepared.Binding.Topic).Stage)
			}
		})
	}
}
