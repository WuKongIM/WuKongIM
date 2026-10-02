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

type drainStore struct {
	*progressStore
	afterWindow func(meta.MQTTWindowMutation) error
}

func TestSourceDrainCancelsInterruptedPreparationWithExplicitEmptyCursor(t *testing.T) {
	for _, cut := range []string{"unknown", "boundary", "cursor", "cancel_reply_lost"} {
		t.Run(cut, func(t *testing.T) {
			ctx := context.Background()
			f := setupGroupSource(t)
			lost := errors.New("committed preparation reply lost")
			f.store.afterBinding = func(b meta.MQTTSourceBinding) error {
				if cut == "unknown" && !b.BoundaryKnown || (cut == "boundary" || cut == "cancel_reply_lost") && b.BoundaryKnown {
					return lost
				}
				return nil
			}
			f.store.afterCursor = func(meta.MQTTDeliveryCursorMutation) error {
				if cut == "cursor" {
					return lost
				}
				return nil
			}
			_, err := f.prepare()
			require.ErrorIs(t, err, lost)
			f.store.afterBinding, f.store.afterCursor = nil, nil
			key := meta.MQTTSourceBindingKey{Owner: meta.MQTTBindingOwner{Kind: meta.MQTTBindingChannel, ID: "2:group", Generation: "protected-generation"}, Namespace: f.intent.Namespace, ClientID: f.intent.ClientID, SessionGeneration: f.intent.SessionGeneration, SubscriptionGeneration: f.intent.Generation}
			s := &drainStore{progressStore: &progressStore{groupSourceStore: f.store}}
			drain, err := app.NewSourceDrain(app.SourceDrainOptions{Store: s, Owners: f.owners, Sources: f.opts.Sources, Now: func() time.Time { return f.now }})
			require.NoError(t, err)
			if cut == "unknown" {
				f.tail = 20 // An unknown start may select one fresh protected boundary.
			}
			if cut == "cancel_reply_lost" {
				f.store.afterCursor = func(m meta.MQTTDeliveryCursorMutation) error {
					require.Equal(t, meta.MQTTCursorCancelInit, m.Op)
					return lost
				}
			}
			var sealed app.SourceDrainResult
			f.project.remove = func(c context.Context, request app.SubscriptionProjectionRequest) (app.SubscriptionProjectionReceipt, error) {
				var e error
				sealed, e = drain.Seal(c, request.Owner, key)
				return projectionReceipt(request), e
			}
			_, err = f.subscriptions.Unsubscribe(ctx, f.connection.Owner, f.intent.Topic)
			if cut == "cancel_reply_lost" {
				require.ErrorIs(t, err, lost)
				f.store.afterCursor = nil
				_, err = f.subscriptions.Unsubscribe(ctx, f.connection.Owner, f.intent.Topic)
			}
			require.NoError(t, err)
			require.True(t, sealed.Binding.EndKnown)
			require.Equal(t, f.tail, sealed.Binding.StartAfter)
			require.Equal(t, sealed.Binding.StartAfter, sealed.Binding.EndThrough)
			require.Equal(t, sealed.Binding.EndThrough, sealed.Cursor.CompletedThrough)
			require.Positive(t, sealed.Binding.ProgressRevision)
			require.Zero(t, f.row(t).PendingMessages)
			removal, err := app.NewSourceRemoval(app.SourceRemovalOptions{Store: s, Now: func() time.Time { return f.now }})
			require.NoError(t, err)
			_, err = removal.Reconcile(ctx, key)
			require.NoError(t, err)
			removed, err := removal.Reconcile(ctx, key)
			require.NoError(t, err)
			require.Equal(t, meta.MQTTBindingRemoved, removed.Binding.Stage)
			require.Zero(t, f.owners.Snapshot().Operations)
		})
	}
}

func (s *drainStore) MutateMQTTWindow(ctx context.Context, m meta.MQTTWindowMutation) (meta.MQTTWindowResult, error) {
	b := s.db.NewBatch()
	defer b.Close()
	r, err := b.MutateMQTTWindow(7, m)
	if err != nil {
		return meta.MQTTWindowResult{}, err
	}
	if err = b.Commit(ctx); err != nil {
		return meta.MQTTWindowResult{}, err
	}
	if s.afterWindow != nil {
		if err = s.afterWindow(m); err != nil {
			return meta.MQTTWindowResult{}, err
		}
	}
	return *r, nil
}

func TestSourceDrainUnsubscribeReleasesOnlyUnadmittedBacklog(t *testing.T) {
	ctx := context.Background()
	f, store, progress, prepared := progressFixture(t)
	ack := advanceProgressWindow(t, f, prepared)
	o := f.connection.Owner
	account := meta.MQTTDeliveryCursorMutation{Key: prepared.Cursor.Key, ExpectedRevision: f.row(t).Revision, OwnerGeneration: o.OwnerGeneration, OwnerNodeID: o.NodeID, OwnerBootID: o.BootID, ConnectionID: o.ConnectionID, Op: meta.MQTTCursorAccountQualified, Topic: prepared.Cursor.Topic, AuthorizationVersion: prepared.Cursor.AuthorizationVersion, Through: prepared.Cursor.StartAfter + 3, AddedMessages: 1, AddedBytes: 4, UpdatedAtMS: f.now.UnixMilli()}
	account.Qualified = &meta.MQTTQualifiedAccounting{From: prepared.Cursor.StartAfter + 3, SubscriptionRevision: f.subscription(t, prepared.Binding.Topic).Revision, Items: []meta.MQTTAccountingItem{{Position: prepared.Cursor.StartAfter + 3, Bytes: 4}}}
	r, err := f.store.MutateMQTTDeliveryCursor(ctx, account)
	require.NoError(t, err)
	require.Equal(t, meta.MQTTSessionCASApplied, r.Status)
	account.ExpectedRevision = f.row(t).Revision
	account.Through++
	account.Qualified.From++
	account.Qualified.Items[0].Position++
	r, err = f.store.MutateMQTTDeliveryCursor(ctx, account)
	require.NoError(t, err)
	require.Equal(t, meta.MQTTSessionCASApplied, r.Status)
	readInflight := func() []meta.MQTTInflight {
		r, e := f.store.ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadInflightPage, Namespace: o.Key.Namespace, ClientID: o.Key.ClientID, SessionGeneration: o.SessionGeneration, Limit: 16})
		require.NoError(t, e)
		return r.Inflight
	}
	originalExchanges := readInflight()
	require.Len(t, originalExchanges, 2)
	drain, err := app.NewSourceDrain(app.SourceDrainOptions{Store: &drainStore{progressStore: store}, Owners: f.owners, Now: func() time.Time { return f.now }})
	require.NoError(t, err)
	var sealed app.SourceDrainResult
	f.project.remove = func(c context.Context, request app.SubscriptionProjectionRequest) (app.SubscriptionProjectionReceipt, error) {
		var e error
		sealed, e = drain.Seal(c, request.Owner, prepared.Binding.Key)
		return projectionReceipt(request), e
	}
	f.denied = true // Lost receive permission must not prevent unsubscribe.
	existed, err := f.subscriptions.Unsubscribe(ctx, o, prepared.Binding.Topic)
	require.ErrorIs(t, err, app.ErrSourceDrainPending)
	require.Equal(t, meta.MQTTSubscriptionRemoving, f.subscription(t, prepared.Binding.Topic).Stage)
	require.EqualValues(t, 3, f.row(t).PendingMessages)
	existed, err = f.subscriptions.Unsubscribe(ctx, o, prepared.Binding.Topic)
	require.NoError(t, err)
	require.True(t, existed)
	require.Equal(t, meta.MQTTSubscriptionRemoved, f.subscription(t, prepared.Binding.Topic).Stage)
	require.Equal(t, meta.MQTTBindingRemoving, sealed.Binding.Stage)
	require.EqualValues(t, 14, sealed.Binding.EndThrough)
	require.EqualValues(t, 14, sealed.Cursor.AccountedThrough)
	require.EqualValues(t, 14, sealed.Cursor.WindowThrough)
	require.EqualValues(t, 10, sealed.Cursor.CompletedThrough)
	require.EqualValues(t, 2, sealed.Cursor.PendingMessages)
	require.EqualValues(t, 2, sealed.Cursor.PendingBytes)
	require.EqualValues(t, 2, f.row(t).PendingMessages)
	require.EqualValues(t, 2, f.row(t).PendingBytes)
	require.Equal(t, originalExchanges, readInflight())
	again, err := drain.Seal(ctx, o, prepared.Binding.Key)
	require.NoError(t, err)
	require.Equal(t, sealed, again)
	ack(1)
	gap, err := progress.Reconcile(ctx, prepared.Binding.Key)
	require.NoError(t, err)
	require.EqualValues(t, 10, gap.Binding.CompletedThrough)
	ack(0)
	complete, err := progress.Reconcile(ctx, prepared.Binding.Key)
	require.NoError(t, err)
	require.EqualValues(t, 14, complete.Binding.CompletedThrough)
	removal, err := app.NewSourceRemoval(app.SourceRemovalOptions{Store: store, Now: func() time.Time { return f.now }})
	require.NoError(t, err)
	_, err = removal.Reconcile(ctx, prepared.Binding.Key)
	require.NoError(t, err)
	removed, err := removal.Reconcile(ctx, prepared.Binding.Key)
	require.NoError(t, err)
	require.Equal(t, meta.MQTTBindingRemoved, removed.Binding.Stage)
	require.Equal(t, meta.MQTTBindingDrained, removed.Binding.ReleaseReason)
	require.Zero(t, f.row(t).PendingMessages)
	require.Zero(t, f.row(t).OutboundInflight)
	require.Zero(t, f.owners.Snapshot().Operations)
}

func TestSourceDrainResumesLostCommitsAndCompletedRemoval(t *testing.T) {
	for _, cut := range []string{"seal", "window", "already_removed"} {
		t.Run(cut, func(t *testing.T) {
			ctx := context.Background()
			f, store, progress, prepared := progressFixture(t)
			ack := advanceProgressWindow(t, f, prepared)
			o := f.connection.Owner
			account := meta.MQTTDeliveryCursorMutation{Key: prepared.Cursor.Key, ExpectedRevision: f.row(t).Revision, OwnerGeneration: o.OwnerGeneration, OwnerNodeID: o.NodeID, OwnerBootID: o.BootID, ConnectionID: o.ConnectionID, Op: meta.MQTTCursorAccount, Topic: prepared.Cursor.Topic, AuthorizationVersion: prepared.Cursor.AuthorizationVersion, Through: 14, AddedMessages: 2, AddedBytes: 8, UpdatedAtMS: f.now.UnixMilli()}
			r, err := f.store.MutateMQTTDeliveryCursor(ctx, account)
			require.NoError(t, err)
			require.Equal(t, meta.MQTTSessionCASApplied, r.Status)
			s := &drainStore{progressStore: store}
			drain, err := app.NewSourceDrain(app.SourceDrainOptions{Store: s, Owners: f.owners, Now: func() time.Time { return f.now }})
			require.NoError(t, err)
			lost := errors.New("committed removal reply lost")
			if cut == "seal" {
				f.store.afterBinding = func(meta.MQTTSourceBinding) error { return lost }
			} else {
				s.afterWindow = func(meta.MQTTWindowMutation) error { return lost }
			}
			f.project.remove = func(c context.Context, request app.SubscriptionProjectionRequest) (app.SubscriptionProjectionReceipt, error) {
				_, e := drain.Seal(c, request.Owner, prepared.Binding.Key)
				return projectionReceipt(request), e
			}
			_, err = f.subscriptions.Unsubscribe(ctx, o, prepared.Binding.Topic)
			require.ErrorIs(t, err, lost)
			require.Equal(t, meta.MQTTSubscriptionRemoving, f.subscription(t, prepared.Binding.Topic).Stage)
			if cut == "seal" {
				require.EqualValues(t, 4, f.row(t).PendingMessages)
			} else {
				require.EqualValues(t, 2, f.row(t).PendingMessages)
			}
			f.store.afterBinding, s.afterWindow = nil, nil
			if cut == "already_removed" {
				ack(0)
				ack(1)
				_, err = progress.Reconcile(ctx, prepared.Binding.Key)
				require.NoError(t, err)
				removal, e := app.NewSourceRemoval(app.SourceRemovalOptions{Store: s, Now: func() time.Time { return f.now }})
				require.NoError(t, e)
				_, err = removal.Reconcile(ctx, prepared.Binding.Key)
				require.NoError(t, err)
				result, e := removal.Reconcile(ctx, prepared.Binding.Key)
				require.NoError(t, e)
				require.Equal(t, meta.MQTTBindingRemoved, result.Binding.Stage)
			}
			resumed, err := f.service.Connect(ctx, command())
			require.NoError(t, err)
			require.Equal(t, o.SessionGeneration, resumed.Owner.SessionGeneration)
			_, err = drain.Seal(ctx, o, prepared.Binding.Key)
			require.Error(t, err)
			_, err = f.subscriptions.Unsubscribe(ctx, resumed.Owner, prepared.Binding.Topic)
			require.NoError(t, err)
			require.Equal(t, meta.MQTTSubscriptionRemoved, f.subscription(t, prepared.Binding.Topic).Stage)
			got, err := drain.Seal(ctx, resumed.Owner, prepared.Binding.Key)
			require.NoError(t, err)
			require.EqualValues(t, 14, got.Binding.EndThrough)
			require.EqualValues(t, 14, got.Cursor.WindowThrough)
			require.EqualValues(t, got.Cursor.InflightCount, f.row(t).PendingMessages)
			require.Zero(t, f.owners.Snapshot().Operations)
		})
	}
}

func TestSourceDrainPreservesNewSubscriptionAndItsQuota(t *testing.T) {
	ctx := context.Background()
	f, store, progress, prepared := progressFixture(t)
	ack := advanceProgressWindow(t, f, prepared)
	o := f.connection.Owner
	account := func(cursor meta.MQTTDeliveryCursor, through, count, bytes uint64) {
		r, err := f.store.MutateMQTTDeliveryCursor(ctx, meta.MQTTDeliveryCursorMutation{Key: cursor.Key, ExpectedRevision: f.row(t).Revision, OwnerGeneration: o.OwnerGeneration, OwnerNodeID: o.NodeID, OwnerBootID: o.BootID, ConnectionID: o.ConnectionID, Op: meta.MQTTCursorAccount, Topic: cursor.Topic, AuthorizationVersion: cursor.AuthorizationVersion, Through: through, AddedMessages: count, AddedBytes: bytes, UpdatedAtMS: f.now.UnixMilli()})
		require.NoError(t, err)
		require.Equal(t, meta.MQTTSessionCASApplied, r.Status)
	}
	account(prepared.Cursor, 14, 2, 2)
	// Controlled projection completion leaves the old source to reconcile after
	// a new same-topic generation has already acquired independent responsibility.
	_, err := f.subscriptions.Unsubscribe(ctx, o, prepared.Binding.Topic)
	require.NoError(t, err)
	_, err = f.subscriptions.Subscribe(ctx, o, subscriptionRequest())
	require.NoError(t, err)
	f.tail = 20
	newSource, err := f.prepare()
	require.NoError(t, err)
	require.NotEqual(t, prepared.Binding.Key, newSource.Binding.Key)
	account(newSource.Cursor, 23, 3, 6)
	readNew := func() meta.MQTTDeliveryCursor {
		r, err := f.store.ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadDeliveryCursor, CursorKey: newSource.Cursor.Key})
		require.NoError(t, err)
		require.Len(t, r.DeliveryCursors, 1)
		return r.DeliveryCursors[0]
	}
	original := readNew()
	drain, err := app.NewSourceDrain(app.SourceDrainOptions{Store: &drainStore{progressStore: store}, Owners: f.owners, Now: func() time.Time { return f.now }})
	require.NoError(t, err)
	sealed, err := drain.Seal(ctx, o, prepared.Binding.Key)
	require.NoError(t, err)
	require.EqualValues(t, 14, sealed.Binding.EndThrough)
	require.EqualValues(t, 5, f.row(t).PendingMessages)
	again, err := drain.Seal(ctx, o, prepared.Binding.Key)
	require.NoError(t, err)
	require.Equal(t, sealed, again)
	ack(0)
	ack(1)
	_, err = progress.Reconcile(ctx, prepared.Binding.Key)
	require.NoError(t, err)
	removal, err := app.NewSourceRemoval(app.SourceRemovalOptions{Store: store, Now: func() time.Time { return f.now }})
	require.NoError(t, err)
	_, err = removal.Reconcile(ctx, prepared.Binding.Key)
	require.NoError(t, err)
	removed, err := removal.Reconcile(ctx, prepared.Binding.Key)
	require.NoError(t, err)
	require.Equal(t, meta.MQTTBindingRemoved, removed.Binding.Stage)
	require.Equal(t, original, readNew())
	require.EqualValues(t, 3, f.row(t).PendingMessages)
	require.EqualValues(t, 6, f.row(t).PendingBytes)
	require.Equal(t, meta.MQTTSubscriptionActive, f.subscription(t, prepared.Binding.Topic).Stage)
}
