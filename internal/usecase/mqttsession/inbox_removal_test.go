package mqttsession_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	app "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/stretchr/testify/require"
)

type inboxRemovalDrain func(context.Context, contract.Owner, meta.MQTTSourceBindingKey) (app.SourceDrainResult, error)

func (f inboxRemovalDrain) Seal(ctx context.Context, owner contract.Owner, key meta.MQTTSourceBindingKey) (app.SourceDrainResult, error) {
	return f(ctx, owner, key)
}

type inboxRemovalFixture struct {
	*inboxEstablishmentFixture
	s       *drainStore
	drain   *app.SourceDrain
	options app.InboxRemovalOptions
	cursors []meta.MQTTDeliveryCursor
	windows []meta.MQTTWindowResult
}

func setupInboxRemoval(t *testing.T, count int) *inboxRemovalFixture {
	t.Helper()
	f := &inboxRemovalFixture{inboxEstablishmentFixture: setupInboxEstablishment(t)}
	f.s = &drainStore{progressStore: &progressStore{groupSourceStore: f.store}}
	var err error
	f.drain, err = app.NewSourceDrain(app.SourceDrainOptions{Store: f.s, Owners: f.owners, Now: func() time.Time { return f.now }})
	require.NoError(t, err)
	f.options = app.InboxRemovalOptions{Store: f.s, Owners: f.owners, Drain: f.drain, PageSize: 1, Now: func() time.Time { return f.now }}
	if count == 0 {
		return f
	}
	for i := range count {
		f.directory(t, meta.ChannelKey{ChannelID: channelid.EncodePersonChannel("alice", fmt.Sprintf("peer%d", i)), ChannelType: 1})
	}
	initialOptions := f.inboxEstablishmentFixture.options
	initialOptions.PageSize = 64
	initial, err := app.NewInboxEstablishment(initialOptions)
	require.NoError(t, err)
	f.project.establish = initial.Establish
	_, err = f.subscriptions.Reconcile(context.Background(), f.connection.Owner, f.intent.Topic)
	require.NoError(t, err)
	r, err := f.s.ReadMQTT(context.Background(), meta.MQTTRead{Kind: meta.MQTTReadDeliveryCursors, Namespace: f.intent.Namespace, ClientID: f.intent.ClientID, SessionGeneration: f.intent.SessionGeneration, SubscriptionGeneration: f.intent.Generation, Limit: 64})
	require.NoError(t, err)
	require.Len(t, r.DeliveryCursors, count)
	f.cursors = r.DeliveryCursors
	for i, c := range f.cursors {
		o := f.connection.Owner
		result, err := f.s.MutateMQTTDeliveryCursor(context.Background(), meta.MQTTDeliveryCursorMutation{Key: c.Key, ExpectedRevision: f.row(t).Revision, OwnerGeneration: o.OwnerGeneration, OwnerNodeID: o.NodeID, OwnerBootID: o.BootID, ConnectionID: o.ConnectionID, Op: meta.MQTTCursorAccount, Topic: c.Topic, Through: c.StartAfter + 2, AddedMessages: 2, AddedBytes: 2, UpdatedAtMS: f.now.UnixMilli()})
		require.NoError(t, err)
		require.Equal(t, meta.MQTTSessionCASApplied, result.Status)
		window, err := f.s.MutateMQTTWindow(context.Background(), meta.MQTTWindowMutation{Key: c.Key, ExpectedRevision: f.row(t).Revision, OwnerGeneration: o.OwnerGeneration, OwnerNodeID: o.NodeID, OwnerBootID: o.BootID, ConnectionID: o.ConnectionID, Op: meta.MQTTWindowAdmit, Publication: meta.MQTTInflightPublication{Position: c.StartAfter + 1, MessageID: uint64(100 + i), MessageSeq: c.StartAfter + 1, ContentVersion: 1, ContentHash: strings.Repeat("a", 64), Bytes: 1, SubscriptionIdentifier: f.intent.SubscriptionIdentifier}, UpdatedAtMS: f.now.UnixMilli()})
		require.NoError(t, err)
		require.Equal(t, meta.MQTTWindowApplied, window.Status)
		f.windows = append(f.windows, window)
	}
	return f
}

func (f *inboxRemovalFixture) install(t *testing.T) *app.InboxRemoval {
	t.Helper()
	p, err := app.NewInboxRemoval(f.options)
	require.NoError(t, err)
	f.project.remove = p.Remove
	return p
}

func TestMQTTInboxRemovalPagesPreserveInflightAndAcks(t *testing.T) {
	f := setupInboxRemoval(t, 2)
	f.install(t)
	initial := f.readQualification(t)
	f.denied = true // Cleanup must remain possible after receive permission is lost.
	_, err := f.subscriptions.Unsubscribe(context.Background(), f.connection.Owner, f.intent.Topic)
	require.ErrorIs(t, err, app.ErrSourceDrainPending)
	require.EqualValues(t, 3, f.row(t).PendingMessages)
	q := f.readQualification(t)
	require.Equal(t, meta.MQTTBindingRemoving, q.Stage)
	require.False(t, q.DrainDone)
	require.Equal(t, f.cursors[0].Key.SourceID, q.DrainAfterSourceID)
	require.Equal(t, initial.DiscoveryAfterChannelID, q.DiscoveryAfterChannelID)
	existed, err := f.subscriptions.Unsubscribe(context.Background(), f.connection.Owner, f.intent.Topic)
	require.NoError(t, err)
	require.True(t, existed)
	q = f.readQualification(t)
	require.True(t, q.DrainDone)
	require.Equal(t, meta.MQTTBindingRemoved, q.Stage)
	require.Equal(t, meta.MQTTSubscriptionRemoved, f.subscription(t, f.intent.Topic).Stage)
	require.EqualValues(t, 2, f.row(t).PendingMessages)
	require.EqualValues(t, 2, f.row(t).OutboundInflight)
	for i, c := range f.cursors {
		inflight, err := f.s.ReadMQTT(context.Background(), meta.MQTTRead{Kind: meta.MQTTReadInflight, Namespace: c.Key.Namespace, ClientID: c.Key.ClientID, SessionGeneration: c.Key.SessionGeneration, PacketID: f.windows[i].PacketID})
		require.NoError(t, err)
		require.Len(t, inflight.Inflight, 1)
		require.Equal(t, c.StartAfter+1, inflight.Inflight[0].Publication.Position)
		o := f.connection.Owner
		ack, err := f.s.MutateMQTTWindow(context.Background(), meta.MQTTWindowMutation{Key: c.Key, ExpectedRevision: f.row(t).Revision, OwnerGeneration: o.OwnerGeneration, OwnerNodeID: o.NodeID, OwnerBootID: o.BootID, ConnectionID: o.ConnectionID, Op: meta.MQTTWindowAck, PacketID: f.windows[i].PacketID, DeliveryOrder: f.windows[i].DeliveryOrder, UpdatedAtMS: f.now.UnixMilli()})
		require.NoError(t, err)
		require.Equal(t, meta.MQTTWindowApplied, ack.Status)
	}
	require.Zero(t, f.row(t).PendingMessages)
	require.Zero(t, f.row(t).OutboundInflight)
	require.Zero(t, f.owners.Snapshot().Operations)
}

func TestMQTTInboxRemovalLostRepliesResumeExactProgress(t *testing.T) {
	for _, cut := range []string{"closed", "window", "progress", "complete"} {
		t.Run(cut, func(t *testing.T) {
			f := setupInboxRemoval(t, 1)
			f.install(t)
			lost := errors.New("reply lost")
			failed := false
			f.store.afterBinding = func(q meta.MQTTSourceBinding) error {
				if q.Key.Owner.Kind != meta.MQTTBindingUID {
					return nil
				}
				hit := cut == "closed" && q.DrainVersion == 1 && q.DrainAfterSourceID == "" || cut == "progress" && q.DrainAfterSourceID != "" && !q.DrainDone || cut == "complete" && q.DrainDone
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
			_, err := f.subscriptions.Unsubscribe(context.Background(), f.connection.Owner, f.intent.Topic)
			require.ErrorIs(t, err, lost)
			require.True(t, failed)
			f.connection, err = f.service.Connect(context.Background(), command())
			require.NoError(t, err)
			existed, err := f.subscriptions.Unsubscribe(context.Background(), f.connection.Owner, f.intent.Topic)
			require.NoError(t, err)
			require.True(t, existed)
			require.True(t, f.readQualification(t).DrainDone)
			require.EqualValues(t, 1, f.row(t).PendingMessages)
			require.EqualValues(t, 1, f.row(t).OutboundInflight)
			r, err := f.s.ReadMQTT(context.Background(), meta.MQTTRead{Kind: meta.MQTTReadDeliveryCursor, CursorKey: f.cursors[0].Key})
			require.NoError(t, err)
			require.Equal(t, f.cursors[0].StartAfter, r.DeliveryCursors[0].StartAfter)
			require.Zero(t, f.owners.Snapshot().Operations)
		})
	}
}

func TestMQTTInboxRemovalCancelsEmptyAndRetainsCursorlessDebt(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(fmt.Sprint(unknown), func(t *testing.T) {
			f := setupInboxRemoval(t, 0)
			var sourceKey meta.MQTTSourceBindingKey
			if unknown {
				f.directory(t, meta.ChannelKey{ChannelID: f.channel.ID, ChannelType: 1})
				initial, err := app.NewInboxEstablishment(f.inboxEstablishmentFixture.options)
				require.NoError(t, err)
				f.project.establish = initial.Establish
				f.store.afterBinding = func(b meta.MQTTSourceBinding) error {
					if b.Key.Owner.Kind == meta.MQTTBindingChannel && !b.BoundaryKnown {
						sourceKey = b.Key
						return errors.New("interrupted")
					}
					return nil
				}
				_, err = f.subscriptions.Reconcile(context.Background(), f.connection.Owner, f.intent.Topic)
				require.Error(t, err)
				f.store.afterBinding = nil
			}
			f.options.Drain = inboxRemovalDrain(func(context.Context, contract.Owner, meta.MQTTSourceBindingKey) (app.SourceDrainResult, error) {
				t.Fatal("empty cursor prefix cannot invent source release")
				return app.SourceDrainResult{}, nil
			})
			f.install(t)
			existed, err := f.subscriptions.Unsubscribe(context.Background(), f.connection.Owner, f.intent.Topic)
			require.NoError(t, err)
			require.True(t, existed)
			require.True(t, f.readQualification(t).DrainDone)
			if unknown {
				r, err := f.s.ReadMQTT(context.Background(), meta.MQTTRead{Kind: meta.MQTTReadSourceBinding, BindingKey: sourceKey})
				require.NoError(t, err)
				require.Len(t, r.Bindings, 1)
				require.Equal(t, meta.MQTTBindingPreparing, r.Bindings[0].Stage)
				require.False(t, r.Bindings[0].BoundaryKnown)
			}
			require.Zero(t, f.row(t).PendingMessages)
			require.Zero(t, f.owners.Snapshot().Operations)
		})
	}
}

func TestMQTTInboxRemovalRejectsInconsistentEvidenceAndLateEffects(t *testing.T) {
	for _, mode := range []string{"uid", "intent", "stage", "mixed", "duplicate", "cursor", "short", "foreign", "pending", "bad-drain", "panic", "cancel", "fenced", "clock", "cas-conflict", "cas-receipt", "final"} {
		t.Run(mode, func(t *testing.T) {
			f := setupInboxRemoval(t, 1)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f.s.read = func(q meta.MQTTRead, r *meta.MQTTReadResult) error {
				if q.Kind == meta.MQTTReadDeliveryCursors {
					switch mode {
					case "mixed":
						r.Bindings = []meta.MQTTSourceBinding{f.readQualification(t)}
					case "duplicate":
						r.DeliveryCursors = append(r.DeliveryCursors, r.DeliveryCursors[0])
					case "cursor":
						r.After.Delivery = r.DeliveryCursors[0].Key
					case "short":
						r.DeliveryCursors = nil
						r.Done = false
					case "foreign":
						r.DeliveryCursors[0].Key.SourceID = "2:foreign"
					}
				}
				if mode == "final" && q.Kind == meta.MQTTReadSourceBinding && len(r.Bindings) == 1 && r.Bindings[0].Key.Owner.Kind == meta.MQTTBindingUID && r.Bindings[0].DrainDone {
					r.Bindings[0].Revision++
				}
				return nil
			}
			f.s.write = func(ctx context.Context, rev uint64, b meta.MQTTSourceBinding) (meta.MQTTSourceBindingResult, error) {
				if b.Key.Owner.Kind == meta.MQTTBindingUID && b.DrainAfterSourceID != "" {
					if mode == "cas-conflict" {
						return meta.MQTTSourceBindingResult{Status: meta.MQTTSessionCASConflict}, nil
					}
					if mode == "cas-receipt" {
						return meta.MQTTSourceBindingResult{Status: meta.MQTTSessionCASApplied, CurrentRevision: b.Revision + 1}, nil
					}
				}
				return f.store.CompareAndSwapMQTTSourceBinding(ctx, rev, b)
			}
			f.options.Drain = inboxRemovalDrain(func(c context.Context, o contract.Owner, k meta.MQTTSourceBindingKey) (app.SourceDrainResult, error) {
				if mode == "pending" {
					return app.SourceDrainResult{}, app.ErrSourceDrainPending
				}
				if mode == "panic" {
					panic("sensitive")
				}
				result, err := f.drain.Seal(c, o, k)
				switch mode {
				case "bad-drain":
					result.Cursor.WindowThrough--
				case "cancel":
					cancel()
				case "fenced":
					f.owners.Fence(f.connection.Owner)
				case "clock":
					f.now = f.now.Add(-time.Hour)
				}
				return result, err
			})
			if mode == "duplicate" {
				f.options.PageSize = 2
			}
			p := f.install(t)
			f.project.remove = func(ctx context.Context, r app.SubscriptionProjectionRequest) (app.SubscriptionProjectionReceipt, error) {
				switch mode {
				case "uid":
					r.UID = "foreign"
				case "intent":
					r.Subscription.Revision++
				case "stage":
					r.Subscription.Stage = meta.MQTTSubscriptionActive
				}
				got, err := p.Remove(ctx, r)
				require.Zero(t, got)
				return got, err
			}
			_, err := f.subscriptions.Unsubscribe(ctx, f.connection.Owner, f.intent.Topic)
			require.Error(t, err)
			require.NotContains(t, err.Error(), "sensitive")
			require.Equal(t, meta.MQTTSubscriptionRemoving, f.subscription(t, f.intent.Topic).Stage)
			require.Zero(t, f.owners.Snapshot().Operations)
		})
	}
}

func TestMQTTInboxRemovalRequiresBoundedCompletePorts(t *testing.T) {
	f := setupInboxRemoval(t, 0)
	for _, change := range []func(*app.InboxRemovalOptions){func(o *app.InboxRemovalOptions) { o.Store = nil }, func(o *app.InboxRemovalOptions) { o.Owners = nil }, func(o *app.InboxRemovalOptions) { o.Drain = nil }, func(o *app.InboxRemovalOptions) { o.PageSize = -1 }, func(o *app.InboxRemovalOptions) { o.PageSize = 65 }, func(o *app.InboxRemovalOptions) { o.Timeout = -1 }, func(o *app.InboxRemovalOptions) { o.Timeout = 2 * time.Minute }, func(o *app.InboxRemovalOptions) { o.Now = func() time.Time { return time.Time{} } }} {
		o := f.options
		change(&o)
		_, err := app.NewInboxRemoval(o)
		require.Error(t, err)
	}
	f.options.PageSize = 0
	p, err := app.NewInboxRemoval(f.options)
	require.NoError(t, err)
	_, err = p.Remove(nil, f.request())
	require.Error(t, err)
}
