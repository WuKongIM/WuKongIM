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

func offlineInboxRemoval(t *testing.T, count int) (*inboxRemovalFixture, app.SubscriptionProjectionRequest) {
	t.Helper()
	f := setupInboxRemoval(t, count)
	f.project.remove = func(context.Context, app.SubscriptionProjectionRequest) (app.SubscriptionProjectionReceipt, error) {
		return app.SubscriptionProjectionReceipt{}, app.ErrSourceDrainPending
	}
	_, err := f.subscriptions.Unsubscribe(context.Background(), f.connection.Owner, f.intent.Topic)
	require.ErrorIs(t, err, app.ErrSourceDrainPending)
	require.NoError(t, f.service.Disconnect(context.Background(), app.DisconnectCommand{Owner: f.connection.Owner, Normal: true}))
	require.Zero(t, f.owners.Snapshot().Held)
	return f, app.SubscriptionProjectionRequest{Owner: f.connection.Owner, UID: "alice", Subscription: f.subscription(t, f.intent.Topic)}
}

func closedInboxRemoval(t *testing.T, f *inboxRemovalFixture) *app.InboxRemoval {
	t.Helper()
	drain, err := app.NewSourceDrain(app.SourceDrainOptions{Store: f.s, Now: func() time.Time { return f.now }})
	require.NoError(t, err)
	p, err := app.NewInboxRemoval(app.InboxRemovalOptions{Store: f.s, ClosedDrain: drain, PageSize: 1, Now: func() time.Time { return f.now }})
	require.NoError(t, err)
	return p
}

func TestClosedInboxRemovalPagesOfflineAndPreservesExchanges(t *testing.T) {
	f, r := offlineInboxRemoval(t, 2)
	p := closedInboxRemoval(t, f)
	ctx := context.Background()
	q := meta.MQTTRead{Kind: meta.MQTTReadInflightPage, Namespace: r.Owner.Key.Namespace, ClientID: r.Owner.Key.ClientID, SessionGeneration: r.Owner.SessionGeneration, Limit: 16}
	before, err := f.s.ReadMQTT(ctx, q)
	require.NoError(t, err)
	initial := f.readQualification(t)
	old := f.row(t)
	f.denied = true
	receipt, err := p.RemoveClosed(ctx, r)
	require.ErrorIs(t, err, app.ErrSourceDrainPending)
	require.Zero(t, receipt)
	checkpoint := f.readQualification(t)
	require.Equal(t, f.cursors[0].Key.SourceID, checkpoint.DrainAfterSourceID)
	require.Equal(t, initial.DiscoveryAfterChannelID, checkpoint.DiscoveryAfterChannelID)
	require.EqualValues(t, 3, f.row(t).PendingMessages)
	receipt, err = p.RemoveClosed(ctx, r)
	require.NoError(t, err)
	require.Equal(t, r.Subscription.Revision, receipt.IntentRevision)
	require.True(t, f.readQualification(t).DrainDone)
	require.Equal(t, meta.MQTTBindingRemoved, f.readQualification(t).Stage)
	require.Equal(t, meta.MQTTSubscriptionRemoving, f.subscription(t, r.Subscription.Topic).Stage, "projection alone cannot complete intent")
	require.EqualValues(t, 2, f.row(t).PendingMessages)
	require.Equal(t, meta.MQTTSessionOffline, f.row(t).State)
	require.Equal(t, old.OfflineExpiresAtMS, f.row(t).OfflineExpiresAtMS)
	after, err := f.s.ReadMQTT(ctx, q)
	require.NoError(t, err)
	require.Equal(t, before.Inflight, after.Inflight)
	again, err := p.RemoveClosed(ctx, r)
	require.NoError(t, err)
	require.Equal(t, receipt, again)
	require.Zero(t, f.owners.Snapshot().Held)
}

func TestClosedInboxRemovalResumesEveryCommittedCheckpoint(t *testing.T) {
	for _, cut := range []string{"closed", "window", "progress", "complete"} {
		t.Run(cut, func(t *testing.T) {
			f, r := offlineInboxRemoval(t, 1)
			p := closedInboxRemoval(t, f)
			lost := errors.New("committed reply lost")
			failed := false
			f.store.afterBinding = func(b meta.MQTTSourceBinding) error {
				if b.Key.Owner.Kind != meta.MQTTBindingUID {
					return nil
				}
				hit := cut == "closed" && b.DrainVersion == 1 && b.DrainAfterSourceID == "" || cut == "progress" && b.DrainAfterSourceID != "" && !b.DrainDone || cut == "complete" && b.DrainDone
				if hit && !failed {
					failed = true
					return lost
				}
				return nil
			}
			f.s.afterWindow = func(meta.MQTTWindowMutation) error {
				if cut == "window" && !failed {
					failed = true
					return lost
				}
				return nil
			}
			receipt, err := p.RemoveClosed(context.Background(), r)
			require.ErrorIs(t, err, lost)
			require.Zero(t, receipt)
			require.True(t, failed)
			f.store.afterBinding = nil
			f.s.afterWindow = nil
			_, err = p.RemoveClosed(context.Background(), r)
			require.NoError(t, err)
			require.True(t, f.readQualification(t).DrainDone)
			require.EqualValues(t, 1, f.row(t).PendingMessages)
			require.EqualValues(t, 1, f.row(t).OutboundInflight)
		})
	}
}

func TestClosedInboxRemovalDiscoversMissingQualification(t *testing.T) {
	f, r := offlineInboxRemoval(t, 0)
	p := closedInboxRemoval(t, f)
	_, err := p.RemoveClosed(context.Background(), r)
	require.NoError(t, err)
	b := f.readQualification(t)
	require.True(t, b.DrainDone)
	require.Equal(t, meta.MQTTBindingRemoved, b.Stage)
	require.Zero(t, b.DiscoveryDone)
	require.Zero(t, b.DrainAfterSourceID)
}

func TestClosedInboxRemovalRejectsUnprovedOrLateEvidence(t *testing.T) {
	for _, fault := range []string{"uid", "child", "active", "owner", "lifetime", "ended", "runtime", "partial", "cancel", "panic", "late-owner", "late-child", "clock"} {
		t.Run(fault, func(t *testing.T) {
			f, r := offlineInboxRemoval(t, 1)
			p := closedInboxRemoval(t, f)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if fault == "uid" {
				r.UID = "other"
			}
			if fault == "child" {
				r.Subscription.Revision++
			}
			if fault == "active" {
				r.Subscription.Stage = meta.MQTTSubscriptionActive
				r.Subscription.RecoveryAtMS = 0
			}
			if fault == "cancel" {
				cancel()
			}
			if fault == "clock" {
				f.now = f.now.Add(-time.Hour)
			}
			readCount := 0
			f.s.read = func(q meta.MQTTRead, page *meta.MQTTReadResult) error {
				if fault == "panic" {
					panic("sensitive")
				}
				if fault == "partial" {
					page.Done = false
				}
				if fault == "runtime" {
					page.Runtime = &meta.MQTTRuntimeView{}
				}
				if q.Kind == meta.MQTTReadSubscription {
					readCount++
					if fault == "owner" || fault == "late-owner" && readCount > 1 {
						page.Session.OwnerGeneration++
					}
					if fault == "lifetime" {
						page.Session.Generation++
					}
					if fault == "ended" {
						page.Session.State = meta.MQTTSessionEnded
						page.Session.OfflineExpiresAtMS = 0
						page.Session.TerminationReason = meta.MQTTSessionExpired
					}
					if fault == "late-child" && readCount > 1 {
						page.Subscriptions[0].Revision++
					}
				}
				return nil
			}
			before := f.row(t)
			receipt, err := p.RemoveClosed(ctx, r)
			require.Error(t, err)
			require.Zero(t, receipt)
			require.NotContains(t, err.Error(), "sensitive")
			require.Zero(t, f.s.writes)
			f.s.read = nil
			require.Equal(t, before, f.row(t))
		})
	}
}

// A nested cleanup must inherit the caller's captured Owner, not capture a
// successor that connected between the outer check and the source read.
func TestClosedInboxRemovalDoesNotRecaptureOwnerInsideDrain(t *testing.T) {
	f, r := offlineInboxRemoval(t, 1)
	p := closedInboxRemoval(t, f)
	before := f.row(t)
	changed := false
	f.s.read = func(q meta.MQTTRead, _ *meta.MQTTReadResult) error {
		if !changed && q.Kind == meta.MQTTReadSourceBinding && q.BindingKey.Owner.Kind == meta.MQTTBindingChannel {
			changed = true
			next, err := f.service.Connect(context.Background(), command())
			require.NoError(t, err)
			require.Greater(t, next.Owner.OwnerGeneration, r.Owner.OwnerGeneration)
		}
		return nil
	}
	receipt, err := p.RemoveClosed(context.Background(), r)
	require.Error(t, err)
	require.Zero(t, receipt)
	require.True(t, changed)
	require.Equal(t, before.PendingMessages, f.row(t).PendingMessages, "captured old work cannot release backlog under successor authority")
	require.Empty(t, f.readQualification(t).DrainAfterSourceID)
}
