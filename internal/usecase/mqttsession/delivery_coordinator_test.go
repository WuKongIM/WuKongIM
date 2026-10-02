package mqttsession_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	app "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/publication"
	"github.com/stretchr/testify/require"
)

func TestDeliveryCoordinatorDiscoversAccountsAndSendsWithoutCallerKeys(t *testing.T) {
	f, accounting, _ := setupWindow(t)
	f.setMessages([]ch.MQTTReplayPublication{{Message: ch.Message{Payload: []byte("one")}}, {Message: ch.Message{Payload: []byte("two")}}})
	coordinator, err := app.NewDeliveryCoordinator(app.DeliveryCoordinatorOptions{Sender: makeSender(t, f, f.options.Authorization), Accounting: accounting})
	require.NoError(t, err)
	var sent []app.PreparedDelivery
	stream, err := coordinator.Open(context.Background(), f.connection, deliverySink{enqueue: func(_ context.Context, d app.PreparedDelivery, dup bool) (app.DeliveryDisposition, error) {
		require.False(t, dup)
		sent = append(sent, d)
		return app.DeliveryQueued, nil
	}})
	require.NoError(t, err)
	idle := false
	for i := 0; i < 12; i++ {
		out, err := stream.Turn(context.Background())
		require.NoError(t, err)
		require.False(t, out.Done)
		if !out.Again {
			idle = true
			break
		}
	}
	require.True(t, idle)
	require.Len(t, sent, 2)
	require.Equal(t, f.page.Records[0], sent[0].Publication)
	require.Equal(t, f.page.Records[1], sent[1].Publication)
	require.EqualValues(t, 2, f.row(t).PendingMessages)
	require.EqualValues(t, 2, f.row(t).OutboundInflight)
}

func TestDeliveryCoordinatorRecoversBeforeDiscoveringNewSources(t *testing.T) {
	f, _, old := setupExchangeRecovery(t, true)
	resumed, err := f.service.Connect(context.Background(), command())
	require.NoError(t, err)
	accounting, err := app.NewAccounting(app.AccountingOptions{Store: f, Metadata: f, Channels: f, Authorization: f.options.Authorization, Now: func() time.Time { return f.now }})
	require.NoError(t, err)
	coordinator, err := app.NewDeliveryCoordinator(app.DeliveryCoordinatorOptions{Sender: makeSender(t, f, f.options.Authorization), Accounting: accounting})
	require.NoError(t, err)
	var delivered []app.PreparedDelivery
	var duplicates []bool
	discovered := false
	f.window.read = func(q meta.MQTTRead, _ *meta.MQTTReadResult) {
		if q.Kind == meta.MQTTReadSubscriptions {
			discovered = true
			require.Len(t, delivered, 2, "new discovery must wait for original exchange recovery")
		}
	}
	stream, err := coordinator.Open(context.Background(), resumed, deliverySink{enqueue: func(_ context.Context, d app.PreparedDelivery, dup bool) (app.DeliveryDisposition, error) {
		delivered = append(delivered, d)
		duplicates = append(duplicates, dup)
		return app.DeliveryQueued, nil
	}})
	require.NoError(t, err)
	for i := 0; i < 3; i++ {
		_, err := stream.Turn(context.Background())
		require.NoError(t, err)
	}
	require.True(t, discovered)
	require.Equal(t, []bool{true, true, false}, duplicates)
	require.Equal(t, old[0].Publication, delivered[0].Publication)
	require.Equal(t, old[1].Publication, delivered[1].Publication)
	require.EqualValues(t, 3, delivered[2].Exchange.DeliveryOrder)
}

func TestDeliveryCoordinatorRotatesPartialInboxSourcesWithoutStarvingGroup(t *testing.T) {
	f, _, _ := setupWindow(t)
	request := app.SubscriptionRequest{Topic: "wk/v1/users/YWxpY2U/messages", TargetKind: meta.MQTTSubscriptionUserInbox, TargetID: "alice", RequestedQoS: 1}
	sub, err := f.subscriptions.Subscribe(context.Background(), f.connection.Owner, request)
	require.NoError(t, err)
	keys := []meta.MQTTDeliveryCursorKey{}
	for _, source := range []struct{ id, generation string }{{"1:a", "01"}, {"1:a", "02"}, {"1:zz", "01"}} {
		key := meta.MQTTDeliveryCursorKey{Namespace: f.key.Namespace, ClientID: f.key.ClientID, SessionGeneration: f.key.SessionGeneration, SubscriptionGeneration: sub.Generation, SourceKind: meta.MQTTSourceChannel, SourceID: source.id, SourceGeneration: "mqtt-log-v1:" + source.generation + strings.Repeat("00", 31)}
		o := f.connection.Owner
		_, err = f.store.MutateMQTTDeliveryCursor(context.Background(), meta.MQTTDeliveryCursorMutation{Key: key, ExpectedRevision: f.row(t).Revision, OwnerGeneration: o.OwnerGeneration, OwnerNodeID: o.NodeID, OwnerBootID: o.BootID, ConnectionID: o.ConnectionID, Op: meta.MQTTCursorInit, Topic: sub.Topic, AuthorizationVersion: sub.AuthorizationVersion, Through: 10, UpdatedAtMS: f.now.UnixMilli()})
		require.NoError(t, err)
		keys = append(keys, key)
	}
	accounting, err := app.NewAccounting(app.AccountingOptions{Store: f, Metadata: f, Channels: f, Authorization: f.options.Authorization, Now: func() time.Time { return f.now }})
	require.NoError(t, err)
	coordinator, err := app.NewDeliveryCoordinator(app.DeliveryCoordinatorOptions{Sender: makeSender(t, f, f.options.Authorization), Accounting: accounting})
	require.NoError(t, err)
	var attempted []meta.MQTTDeliveryCursorKey
	f.window.read = func(q meta.MQTTRead, _ *meta.MQTTReadResult) {
		if q.Kind == meta.MQTTReadDeliveryCursor && q.CursorKey.SubscriptionGeneration == sub.Generation {
			attempted = append(attempted, q.CursorKey)
		}
	}
	sent := 0
	stream, err := coordinator.Open(context.Background(), f.connection, deliverySink{enqueue: func(_ context.Context, d app.PreparedDelivery, _ bool) (app.DeliveryDisposition, error) {
		require.Equal(t, f.key, d.Exchange.Key)
		sent++
		return app.DeliveryQueued, nil
	}})
	require.NoError(t, err)
	for i := 0; i < 6; i++ {
		_, err := stream.Turn(context.Background())
		if i%2 == 0 {
			// These real cursors intentionally have unfinished source bindings.
			require.ErrorIs(t, err, app.ErrEvidence)
		} else {
			require.NoError(t, err)
		}
	}
	require.Equal(t, keys, attempted, "tied source identities must retain distinct generations")
	require.Equal(t, 1, sent)
}

func TestDeliveryCoordinatorRetainsFailedRevocationCleanupAfterFencing(t *testing.T) {
	f, accounting, _ := setupWindow(t)
	f.denied = true
	unavailable := true
	f.fixture.opts.Isolation = isolation(func(ctx context.Context, o contract.Owner) error {
		require.Zero(t, f.owners.Snapshot().Operations)
		if unavailable {
			return errors.New("isolation unavailable")
		}
		return f.owners.Quiesce(ctx, o)
	})
	var err error
	f.service, err = app.New(f.fixture.opts)
	require.NoError(t, err)
	coordinator, err := app.NewDeliveryCoordinator(app.DeliveryCoordinatorOptions{Sender: makeSender(t, f, f.options.Authorization), Accounting: accounting})
	require.NoError(t, err)
	stream, err := coordinator.Open(context.Background(), f.connection, deliverySink{enqueue: func(context.Context, app.PreparedDelivery, bool) (app.DeliveryDisposition, error) {
		t.Fatal("revoked task sent")
		return 0, nil
	}})
	require.NoError(t, err)
	out, err := stream.Turn(context.Background())
	require.Error(t, err)
	require.False(t, out.Done)
	require.Equal(t, meta.MQTTSessionActive, f.row(t).State)
	unavailable, f.denied = false, false
	out, err = stream.Turn(context.Background())
	require.NoError(t, err)
	require.True(t, out.Done)
	require.Equal(t, meta.MQTTSessionRevoked, f.row(t).TerminationReason)
}

func TestDeliveryCoordinatorRejectsBadPagesBeforeAccounting(t *testing.T) {
	for _, fault := range []string{"sub_extra", "sub_generation", "sub_cursor", "source_extra", "source_generation", "source_revision", "source_cursor", "source_topic", "membership", "cancel", "panic"} {
		t.Run(fault, func(t *testing.T) {
			f, accounting, _ := setupWindow(t)
			coordinator, err := app.NewDeliveryCoordinator(app.DeliveryCoordinatorOptions{Sender: makeSender(t, f, f.options.Authorization), Accounting: accounting})
			require.NoError(t, err)
			sent := 0
			stream, err := coordinator.Open(context.Background(), f.connection, deliverySink{enqueue: func(context.Context, app.PreparedDelivery, bool) (app.DeliveryDisposition, error) {
				sent++
				return app.DeliveryQueued, nil
			}})
			require.NoError(t, err)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f.window.read = func(q meta.MQTTRead, r *meta.MQTTReadResult) {
				if strings.HasPrefix(fault, "sub_") {
					if q.Kind != meta.MQTTReadSubscriptions {
						return
					}
					switch fault {
					case "sub_extra":
						r.Subscriptions = append(r.Subscriptions, r.Subscriptions[0])
					case "sub_generation":
						r.Subscriptions[0].SessionGeneration++
					case "sub_cursor":
						r.Done = false
					}
					return
				}
				if q.Kind != meta.MQTTReadDeliveryCursors {
					return
				}
				switch fault {
				case "source_extra":
					r.DeliveryCursors = append(r.DeliveryCursors, r.DeliveryCursors[0])
				case "source_generation":
					r.DeliveryCursors[0].Key.SubscriptionGeneration++
				case "source_revision":
					r.DeliveryCursors[0].Revision = r.Session.Revision + 1
				case "source_cursor":
					r.Done = false
				case "source_topic":
					r.DeliveryCursors[0].Topic = "foreign"
				case "membership":
					r.Membership = &meta.MQTTMembershipView{}
				case "cancel":
					cancel()
				case "panic":
					panic("private metadata detail")
				}
			}
			_, err = stream.Turn(ctx)
			require.Error(t, err)
			require.NotContains(t, err.Error(), "private")
			require.Zero(t, sent)
			require.Zero(t, f.accountingFixture.writes)
			require.Zero(t, f.window.writes)
			require.Zero(t, f.owners.Snapshot().Operations)
			f.window.read = nil
			_, err = stream.Turn(context.Background())
			require.NoError(t, err)
			require.Equal(t, 1, sent, "invalid page cannot poison subsequent source discovery")
		})
	}
}

func TestDeliveryCoordinatorUnavailableAuthorityAndReentryDoNotEnd(t *testing.T) {
	f, _, _ := setupWindow(t)
	unavailable := true
	authorization := subscriptionAuthorizer(func(ctx context.Context, uid string, q app.SubscriptionRequest) (uint64, error) {
		if unavailable {
			return 0, errors.New("authority unavailable")
		}
		return f.options.Authorization.AuthorizeSubscription(ctx, uid, q)
	})
	accounting, err := app.NewAccounting(app.AccountingOptions{Store: f, Metadata: f, Channels: f, Authorization: authorization, Now: func() time.Time { return f.now }})
	require.NoError(t, err)
	coordinator, err := app.NewDeliveryCoordinator(app.DeliveryCoordinatorOptions{Sender: makeSender(t, f, authorization), Accounting: accounting})
	require.NoError(t, err)
	var stream *app.ConnectionDelivery
	sent := 0
	stream, err = coordinator.Open(context.Background(), f.connection, deliverySink{enqueue: func(ctx context.Context, _ app.PreparedDelivery, _ bool) (app.DeliveryDisposition, error) {
		out, err := stream.Turn(ctx)
		require.NoError(t, err)
		require.False(t, out.Done)
		sent++
		return app.DeliveryQueued, nil
	}, close: func(context.Context, meta.MQTTSessionEndReason) error {
		t.Fatal("unavailable authority closed the connection")
		return nil
	}})
	require.NoError(t, err)
	out, err := stream.Turn(context.Background())
	require.Error(t, err)
	require.False(t, out.Done)
	require.Equal(t, meta.MQTTSessionActive, f.row(t).State)
	unavailable = false
	_, err = stream.Turn(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, sent)
}

func TestDeliveryCoordinatorTakeoverClosesOnlyOldTask(t *testing.T) {
	f, accounting, _ := setupWindow(t)
	coordinator, err := app.NewDeliveryCoordinator(app.DeliveryCoordinatorOptions{Sender: makeSender(t, f, f.options.Authorization), Accounting: accounting})
	require.NoError(t, err)
	closed := false
	stream, err := coordinator.Open(context.Background(), f.connection, deliverySink{enqueue: func(context.Context, app.PreparedDelivery, bool) (app.DeliveryDisposition, error) {
		t.Fatal("old connection sent after takeover")
		return 0, nil
	}, close: func(_ context.Context, reason meta.MQTTSessionEndReason) error {
		require.Zero(t, reason)
		closed = true
		return nil
	}})
	require.NoError(t, err)
	resumed, err := f.service.Connect(context.Background(), command())
	require.NoError(t, err)
	before := f.row(t)
	out, err := stream.Turn(context.Background())
	require.NoError(t, err)
	require.True(t, out.Done)
	require.True(t, closed)
	require.Equal(t, before, f.row(t))
	op, err := f.owners.Begin(context.Background(), resumed.Owner)
	require.NoError(t, err)
	op.Done()
}

func TestDeliveryCoordinatorQoS0ContinuesAtFullQoS1Window(t *testing.T) {
	f, accounting, _ := setupWindow(t)
	row := f.row(t)
	row.Revision++
	row.WindowLimit = 1
	_, err := f.store.CompareAndSwapMQTTSession(context.Background(), row.Revision-1, row)
	require.NoError(t, err)
	f.setMessages([]ch.MQTTReplayPublication{
		{Message: ch.Message{Payload: []byte("awaiting ack")}},
		{Message: ch.Message{PublicationMetadata: f.publication(publication.SourceMQTT, 0, "main", "other", nil)}},
	})
	coordinator, err := app.NewDeliveryCoordinator(app.DeliveryCoordinatorOptions{Sender: makeSender(t, f, f.options.Authorization), Accounting: accounting})
	require.NoError(t, err)
	var qos []byte
	stream, err := coordinator.Open(context.Background(), f.connection, deliverySink{enqueue: func(_ context.Context, d app.PreparedDelivery, _ bool) (app.DeliveryDisposition, error) {
		qos = append(qos, d.QoS)
		return app.DeliveryQueued, nil
	}})
	require.NoError(t, err)
	for i := 0; i < 3; i++ {
		_, err := stream.Turn(context.Background())
		require.NoError(t, err)
	}
	require.Equal(t, []byte{1, 0}, qos)
	require.EqualValues(t, 1, f.row(t).OutboundInflight)
	require.EqualValues(t, 1, f.row(t).PendingMessages)
}

func TestDeliveryCoordinatorRequiresConstructedBoundedDependencies(t *testing.T) {
	f, accounting, _ := setupWindow(t)
	sender := makeSender(t, f, f.options.Authorization)
	for _, o := range []app.DeliveryCoordinatorOptions{
		{Sender: sender}, {Accounting: accounting},
		{Sender: &app.Sender{}, Accounting: accounting},
		{Sender: sender, Accounting: &app.Accounting{}},
		{Sender: sender, Accounting: accounting, MaxSubscriptions: -1},
		{Sender: sender, Accounting: accounting, MaxSubscriptions: 1025},
	} {
		_, err := app.NewDeliveryCoordinator(o)
		require.ErrorIs(t, err, app.ErrInvalid)
	}
}

func TestDeliveryCoordinatorEndsAccountingRevocationAndQuotaOutsideScope(t *testing.T) {
	for _, mode := range []string{"revoked", "quota", "lost_quota_reply"} {
		t.Run(mode, func(t *testing.T) {
			f, accounting, _ := setupWindow(t)
			f.setMessages([]ch.MQTTReplayPublication{{Message: ch.Message{Payload: []byte("one")}}, {Message: ch.Message{Payload: []byte("two")}}})
			expected := meta.MQTTSessionRevoked
			if mode == "revoked" {
				f.denied = true
			} else {
				expected = meta.MQTTSessionQuota
				row := f.row(t)
				row.Revision++
				row.QuotaMessages = 1
				_, err := f.store.CompareAndSwapMQTTSession(context.Background(), row.Revision-1, row)
				require.NoError(t, err)
			}
			if mode == "lost_quota_reply" {
				f.store.afterCursor = func(meta.MQTTDeliveryCursorMutation) error { return errors.New("committed accounting reply lost") }
			}
			coordinator, err := app.NewDeliveryCoordinator(app.DeliveryCoordinatorOptions{Sender: makeSender(t, f, f.options.Authorization), Accounting: accounting})
			require.NoError(t, err)
			var closed []meta.MQTTSessionEndReason
			stream, err := coordinator.Open(context.Background(), f.connection, deliverySink{
				enqueue: func(context.Context, app.PreparedDelivery, bool) (app.DeliveryDisposition, error) {
					t.Fatal("ended lifetime sent a publication")
					return 0, nil
				},
				close: func(_ context.Context, reason meta.MQTTSessionEndReason) error {
					require.Zero(t, f.owners.Snapshot().Operations)
					closed = append(closed, reason)
					return nil
				},
			})
			require.NoError(t, err)
			done := false
			for i := 0; i < 3; i++ {
				out, _ := stream.Turn(context.Background())
				if out.Done {
					done = true
					break
				}
			}
			require.True(t, done, "terminal cleanup must not retry ordinary discovery")
			require.Equal(t, []meta.MQTTSessionEndReason{expected}, closed)
			require.Equal(t, meta.MQTTSessionEnded, f.row(t).State)
			require.Equal(t, expected, f.row(t).TerminationReason)
			require.Zero(t, f.owners.Snapshot().Held)
			if mode != "revoked" {
				require.EqualValues(t, 2, f.row(t).PendingMessages)
			}
		})
	}
}
