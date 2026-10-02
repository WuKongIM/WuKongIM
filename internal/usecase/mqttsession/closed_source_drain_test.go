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

func backgroundDrain(t *testing.T, store *progressStore, now func() time.Time) *app.SourceDrain {
	t.Helper()
	d, err := app.NewSourceDrain(app.SourceDrainOptions{Store: &drainStore{progressStore: store}, Now: now})
	require.NoError(t, err)
	return d
}

func closedDrainFixture(t *testing.T) (*groupSourceFixture, *drainStore, app.PreparedGroupSource) {
	t.Helper()
	f, s, _, prepared := progressFixture(t)
	advanceProgressWindow(t, f, prepared)
	o := f.connection.Owner
	m := meta.MQTTDeliveryCursorMutation{Key: prepared.Cursor.Key, OwnerGeneration: o.OwnerGeneration, OwnerNodeID: o.NodeID, OwnerBootID: o.BootID, ConnectionID: o.ConnectionID, Op: meta.MQTTCursorAccountQualified, Topic: prepared.Cursor.Topic, AuthorizationVersion: prepared.Cursor.AuthorizationVersion, AddedMessages: 1, AddedBytes: 4, UpdatedAtMS: f.now.UnixMilli()}
	for _, position := range []uint64{13, 14} {
		m.ExpectedRevision, m.Through = f.row(t).Revision, position
		m.Qualified = &meta.MQTTQualifiedAccounting{From: position, SubscriptionRevision: f.subscription(t, prepared.Binding.Topic).Revision, Items: []meta.MQTTAccountingItem{{Position: position, Bytes: 4}}}
		r, err := f.store.MutateMQTTDeliveryCursor(context.Background(), m)
		require.NoError(t, err)
		require.Equal(t, meta.MQTTSessionCASApplied, r.Status)
	}
	f.project.remove = func(context.Context, app.SubscriptionProjectionRequest) (app.SubscriptionProjectionReceipt, error) {
		return app.SubscriptionProjectionReceipt{}, app.ErrSourceDrainPending
	}
	_, err := f.subscriptions.Unsubscribe(context.Background(), o, prepared.Binding.Topic)
	require.ErrorIs(t, err, app.ErrSourceDrainPending)
	require.NoError(t, f.service.Disconnect(context.Background(), app.DisconnectCommand{Owner: o, Normal: true}))
	require.Zero(t, f.owners.Snapshot().Held)
	return f, &drainStore{progressStore: s}, prepared
}

func TestClosedSourceDrainOfflineBoundedAndLostReplies(t *testing.T) {
	for _, cut := range []string{"bounded", "seal", "window"} {
		t.Run(cut, func(t *testing.T) {
			f, s, prepared := closedDrainFixture(t)
			d, err := app.NewSourceDrain(app.SourceDrainOptions{Store: s, Now: func() time.Time { return f.now }})
			require.NoError(t, err)
			ctx := context.Background()
			q := meta.MQTTRead{Kind: meta.MQTTReadInflightPage, Namespace: f.connection.Owner.Key.Namespace, ClientID: f.connection.Owner.Key.ClientID, SessionGeneration: f.connection.Owner.SessionGeneration, Limit: 16}
			before, err := f.store.ReadMQTT(ctx, q)
			require.NoError(t, err)
			old := f.row(t)
			f.denied = true
			lost := errors.New("committed cleanup reply lost")
			if cut == "seal" {
				f.store.afterBinding = func(meta.MQTTSourceBinding) error { return lost }
			}
			if cut == "window" {
				s.afterWindow = func(meta.MQTTWindowMutation) error { return lost }
			}
			_, err = d.ReconcileClosed(ctx, prepared.Binding.Key)
			if cut == "bounded" {
				require.ErrorIs(t, err, app.ErrSourceDrainPending)
				require.EqualValues(t, 3, f.row(t).PendingMessages, "one bounded range per turn")
			} else {
				require.ErrorIs(t, err, lost)
			}
			f.store.afterBinding, s.afterWindow = nil, nil
			var result app.SourceDrainResult
			for range 3 {
				result, err = d.ReconcileClosed(ctx, prepared.Binding.Key)
				if !errors.Is(err, app.ErrSourceDrainPending) {
					break
				}
			}
			require.NoError(t, err)
			require.EqualValues(t, 14, result.Binding.EndThrough)
			require.EqualValues(t, 14, result.Cursor.WindowThrough)
			require.EqualValues(t, 10, result.Cursor.CompletedThrough)
			require.EqualValues(t, 2, f.row(t).PendingMessages)
			require.Equal(t, meta.MQTTSessionOffline, f.row(t).State)
			require.Equal(t, old.OfflineExpiresAtMS, f.row(t).OfflineExpiresAtMS)
			after, err := f.store.ReadMQTT(ctx, q)
			require.NoError(t, err)
			require.Equal(t, before.Inflight, after.Inflight)
			again, err := d.ReconcileClosed(ctx, prepared.Binding.Key)
			require.NoError(t, err)
			require.Equal(t, result, again)
			require.Zero(t, f.owners.Snapshot().Held)
		})
	}
}

func TestClosedSourceDrainRecoversUnknownPreparationOffline(t *testing.T) {
	for _, boundary := range []bool{false, true} {
		f := setupGroupSource(t)
		lost := errors.New("lost preparation")
		f.store.afterBinding = func(b meta.MQTTSourceBinding) error {
			if b.BoundaryKnown == boundary {
				return lost
			}
			return nil
		}
		_, err := f.prepare()
		require.ErrorIs(t, err, lost)
		f.store.afterBinding = nil
		f.project.remove = func(context.Context, app.SubscriptionProjectionRequest) (app.SubscriptionProjectionReceipt, error) {
			return app.SubscriptionProjectionReceipt{}, app.ErrSourceDrainPending
		}
		_, err = f.subscriptions.Unsubscribe(context.Background(), f.connection.Owner, f.intent.Topic)
		require.Error(t, err)
		require.NoError(t, f.service.Disconnect(context.Background(), app.DisconnectCommand{Owner: f.connection.Owner, Normal: true}))
		s := &drainStore{progressStore: &progressStore{groupSourceStore: f.store}}
		d, err := app.NewSourceDrain(app.SourceDrainOptions{Store: s, Sources: f.opts.Sources, Now: func() time.Time { return f.now }})
		require.NoError(t, err)
		k := meta.MQTTSourceBindingKey{Owner: meta.MQTTBindingOwner{Kind: meta.MQTTBindingChannel, ID: "2:group", Generation: "protected-generation"}, Namespace: f.intent.Namespace, ClientID: f.intent.ClientID, SessionGeneration: f.intent.SessionGeneration, SubscriptionGeneration: f.intent.Generation}
		r, err := d.ReconcileClosed(context.Background(), k)
		require.NoError(t, err)
		require.Equal(t, r.Cursor.StartAfter, r.Cursor.CompletedThrough)
		require.Equal(t, r.Cursor.StartAfter, r.Binding.EndThrough)
		require.Zero(t, r.Cursor.PendingMessages)
		require.Zero(t, f.owners.Snapshot().Held)
	}
}

func TestClosedSourceDrainRejectsUnprovedAuthorityBeforeWrites(t *testing.T) {
	for _, fault := range []string{"active", "uid", "partial", "extra", "owner-changed", "ended", "new-lifetime", "cancel", "read-error"} {
		t.Run(fault, func(t *testing.T) {
			f, s, prepared := closedDrainFixture(t)
			d := backgroundDrain(t, s.progressStore, func() time.Time { return f.now })
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if fault == "cancel" {
				cancel()
			}
			reads := 0
			s.read = func(q meta.MQTTRead, r *meta.MQTTReadResult) error {
				if fault == "read-error" {
					return errors.New("read failed")
				}
				if fault == "partial" {
					r.Done = false
				}
				if fault == "extra" {
					r.Directory = []meta.ChannelKey{{}}
				}
				if q.Kind == meta.MQTTReadSubscription {
					reads++
					if fault == "active" {
						r.Subscriptions[0].Stage, r.Subscriptions[0].RecoveryAtMS = meta.MQTTSubscriptionActive, 0
					}
					if fault == "uid" {
						r.Session.UID = "another"
					}
					if fault == "owner-changed" && reads > 1 {
						r.Session.OwnerGeneration++
					}
					if fault == "ended" {
						r.Session.State, r.Session.OfflineExpiresAtMS, r.Session.TerminationReason = meta.MQTTSessionEnded, 0, meta.MQTTSessionExpired
					}
					if fault == "new-lifetime" {
						r.Session.Generation++
					}
				}
				return nil
			}
			before := f.row(t)
			_, err := d.ReconcileClosed(ctx, prepared.Binding.Key)
			require.Error(t, err)
			require.Zero(t, s.writes)
			s.read = nil
			require.Equal(t, before, f.row(t))
		})
	}
}

func TestConsumerMaintenanceResumesClosedSourceWithoutLocalOwner(t *testing.T) {
	f, s, prepared := closedDrainFixture(t)
	d := backgroundDrain(t, s.progressStore, func() time.Time { return f.now })
	p, err := app.NewSourceProgress(app.SourceProgressOptions{Store: s, Now: func() time.Time { return f.now }})
	require.NoError(t, err)
	r, err := app.NewSourceRemoval(app.SourceRemovalOptions{Store: s, Now: func() time.Time { return f.now }})
	require.NoError(t, err)
	_, accounting := setupAccounting(t)
	c, err := app.NewConsumerMaintenance(app.ConsumerMaintenanceOptions{Store: s, Accounting: accounting, Drain: d, Progress: p, Removal: r, Ender: f.service})
	require.NoError(t, err)
	for _, remaining := range []uint64{3, 2} {
		out, err := c.Maintain(context.Background(), prepared.Binding.Key)
		require.NoError(t, err)
		require.False(t, out.Accounted || out.QuotaEnded || out.RevokedEnded || out.Removed)
		require.Equal(t, remaining, f.row(t).PendingMessages)
	}
	require.EqualValues(t, 2, f.row(t).OutboundInflight)
	require.Zero(t, f.owners.Snapshot().Held)
}
