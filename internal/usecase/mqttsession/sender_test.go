package mqttsession_test

import (
	"context"
	"errors"
	"testing"
	"time"

	app "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/publication"
	"github.com/stretchr/testify/require"
)

type deliverySink struct {
	enqueue func(context.Context, app.PreparedDelivery, bool) (app.DeliveryDisposition, error)
	close   func(context.Context, meta.MQTTSessionEndReason) error
}

func (s deliverySink) Enqueue(ctx context.Context, d app.PreparedDelivery, dup bool) (app.DeliveryDisposition, error) {
	return s.enqueue(ctx, d, dup)
}
func (s deliverySink) Close(ctx context.Context, reason meta.MQTTSessionEndReason) error {
	if s.close != nil {
		return s.close(ctx, reason)
	}
	return nil
}
func makeSender(t *testing.T, f *windowFixture, authorization app.SubscriptionAuthorizer) *app.Sender {
	t.Helper()
	s, err := app.NewSender(app.SenderOptions{Window: app.WindowAdmissionOptions{Store: f, Owners: f.owners, Metadata: f, Channels: f, Authorization: authorization, Now: func() time.Time { return f.now }}, Ender: f.service})
	require.NoError(t, err)
	return s
}

func TestSenderResumesOldOrderBeforeNewAndBusyDoesNotAdvance(t *testing.T) {
	f, _, old := setupExchangeRecovery(t, true)
	c, err := f.service.Connect(context.Background(), command())
	require.NoError(t, err)
	s := makeSender(t, f, f.options.Authorization)
	busy := true
	var orders []uint64
	var duplicates []bool
	stream, err := s.Open(context.Background(), c, deliverySink{enqueue: func(ctx context.Context, d app.PreparedDelivery, dup bool) (app.DeliveryDisposition, error) {
		require.Equal(t, 1, f.owners.Snapshot().Operations)
		if busy {
			return app.DeliveryBusy, nil
		}
		orders = append(orders, d.Exchange.DeliveryOrder)
		duplicates = append(duplicates, dup)
		if len(orders) <= 2 {
			require.Equal(t, old[len(orders)-1].Publication, d.Publication)
		}
		return app.DeliveryQueued, nil
	}})
	require.NoError(t, err)
	blocked, err := stream.Turn(context.Background(), f.key)
	require.NoError(t, err)
	require.True(t, blocked.Busy)
	require.Empty(t, orders)
	require.EqualValues(t, 2, f.row(t).OutboundInflight)
	busy = false
	for i := 0; i < 3; i++ {
		got, err := stream.Turn(context.Background(), f.key)
		require.NoError(t, err)
		require.True(t, got.Enqueued)
	}
	require.Equal(t, []uint64{1, 2, 3}, orders)
	require.Equal(t, []bool{true, true, false}, duplicates)
	idle, err := stream.Turn(context.Background(), f.key)
	require.NoError(t, err)
	require.True(t, idle.Idle)
	require.Len(t, orders, 3)
}

func TestSenderLostNewAdmissionReplyUsesDUPFalseWithoutAnotherAdmission(t *testing.T) {
	f, a, _ := setupWindow(t)
	f.setMessages([]ch.MQTTReplayPublication{{Message: ch.Message{Payload: []byte("one")}}})
	accountWindow(t, f, a)
	s := makeSender(t, f, f.options.Authorization)
	var sent []app.PreparedDelivery
	stream, err := s.Open(context.Background(), f.connection, deliverySink{enqueue: func(_ context.Context, d app.PreparedDelivery, dup bool) (app.DeliveryDisposition, error) {
		require.False(t, dup)
		sent = append(sent, d)
		return app.DeliveryQueued, nil
	}})
	require.NoError(t, err)
	f.window.after = func(*meta.MQTTWindowResult) error { return errors.New("lost admission reply") }
	got, err := stream.Turn(context.Background(), f.key)
	require.Error(t, err)
	require.False(t, got.Enqueued)
	require.Empty(t, sent)
	require.EqualValues(t, 1, f.row(t).OutboundInflight)
	f.window.after = nil
	got, err = stream.Turn(context.Background(), f.key)
	require.NoError(t, err)
	require.True(t, got.Enqueued)
	require.Len(t, sent, 1)
	require.EqualValues(t, 1, sent[0].Exchange.DeliveryOrder)
	require.Equal(t, 1, f.window.writes)
}

func TestSenderDowngradedQoS0CompletesAfterConcurrentRenewAndLostReply(t *testing.T) {
	for _, lost := range []bool{false, true} {
		t.Run(map[bool]string{false: "renew", true: "lost_completion"}[lost], func(t *testing.T) {
			f, a, _ := setupWindow(t)
			f.setMessages([]ch.MQTTReplayPublication{{Message: ch.Message{Payload: []byte("charged")}}})
			accountWindow(t, f, a)
			q := subscriptionRequest()
			q.RequestedQoS = 0
			_, err := f.subscriptions.Subscribe(context.Background(), f.connection.Owner, q)
			require.NoError(t, err)
			s := makeSender(t, f, f.options.Authorization)
			count := 0
			stream, err := s.Open(context.Background(), f.connection, deliverySink{enqueue: func(ctx context.Context, d app.PreparedDelivery, dup bool) (app.DeliveryDisposition, error) {
				count++
				require.Zero(t, d.QoS)
				require.False(t, dup)
				f.now = f.now.Add(time.Millisecond)
				_, err := f.service.Renew(ctx, d.Owner)
				require.NoError(t, err)
				if lost {
					f.window.after = func(*meta.MQTTWindowResult) error { return errors.New("lost completion reply") }
				}
				return app.DeliveryQueued, nil
			}})
			require.NoError(t, err)
			got, err := stream.Turn(context.Background(), f.key)
			if lost {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.True(t, got.Advanced)
			}
			require.True(t, got.Enqueued)
			f.window.after = nil
			if lost {
				got, err = stream.Turn(context.Background(), f.key)
				require.NoError(t, err)
				require.True(t, got.Advanced)
				require.False(t, got.Enqueued)
			}
			got, err = stream.Turn(context.Background(), f.key)
			require.NoError(t, err)
			require.True(t, got.Idle)
			require.Equal(t, 1, count)
			require.Zero(t, f.row(t).PendingMessages)
			require.Zero(t, f.row(t).PendingBytes)
		})
	}
}

func TestSenderOriginalQoS0DoesNotRetryUnqueuedClaim(t *testing.T) {
	for _, disposition := range []app.DeliveryDisposition{app.DeliveryBusy, app.DeliveryExpired} {
		t.Run(map[app.DeliveryDisposition]string{app.DeliveryBusy: "busy", app.DeliveryExpired: "expired"}[disposition], func(t *testing.T) {
			f, a, _ := setupWindow(t)
			f.setMessages([]ch.MQTTReplayPublication{{Message: ch.Message{PublicationMetadata: f.publication(publication.SourceMQTT, 0, "main", "other", nil)}}})
			accountWindow(t, f, a)
			count := 0
			stream, err := makeSender(t, f, f.options.Authorization).Open(context.Background(), f.connection, deliverySink{enqueue: func(context.Context, app.PreparedDelivery, bool) (app.DeliveryDisposition, error) {
				count++
				return disposition, nil
			}})
			require.NoError(t, err)
			got, err := stream.Turn(context.Background(), f.key)
			require.NoError(t, err)
			require.True(t, got.Advanced)
			require.False(t, got.Enqueued)
			got, err = stream.Turn(context.Background(), f.key)
			require.NoError(t, err)
			require.True(t, got.Idle)
			require.Equal(t, 1, count)
		})
	}
}

func TestSenderFinalRevocationEndsAfterReleasingScope(t *testing.T) {
	f, a, _ := setupWindow(t)
	f.setMessages([]ch.MQTTReplayPublication{{Message: ch.Message{Payload: []byte("private")}}})
	accountWindow(t, f, a)
	reads := 0
	f.window.read = func(q meta.MQTTRead, _ *meta.MQTTReadResult) {
		if q.Kind == meta.MQTTReadInflight {
			reads++
			if reads == 2 {
				f.denied = true
			}
		}
	}
	closed := false
	stream, err := makeSender(t, f, f.options.Authorization).Open(context.Background(), f.connection, deliverySink{
		enqueue: func(context.Context, app.PreparedDelivery, bool) (app.DeliveryDisposition, error) {
			t.Fatal("revoked publication was enqueued")
			return 0, nil
		},
		close: func(_ context.Context, reason meta.MQTTSessionEndReason) error {
			require.Zero(t, f.owners.Snapshot().Operations)
			require.Equal(t, meta.MQTTSessionRevoked, reason)
			closed = true
			return nil
		},
	})
	require.NoError(t, err)
	got, err := stream.Turn(context.Background(), f.key)
	require.ErrorIs(t, err, app.ErrSubscriptionDenied)
	require.True(t, got.Ended)
	require.True(t, closed)
	require.Equal(t, meta.MQTTSessionEnded, f.row(t).State)
	require.EqualValues(t, 1, f.row(t).OutboundInflight)
	fresh, err := f.service.Connect(context.Background(), command())
	require.NoError(t, err)
	require.False(t, fresh.SessionPresent)
}

func TestSenderUnavailablePermissionDoesNotEndAndReentrantTurnYields(t *testing.T) {
	f, a, _ := setupWindow(t)
	f.setMessages([]ch.MQTTReplayPublication{{Message: ch.Message{Payload: []byte("one")}}})
	accountWindow(t, f, a)
	unavailable := true
	authorization := subscriptionAuthorizer(func(ctx context.Context, uid string, q app.SubscriptionRequest) (uint64, error) {
		if unavailable {
			return 0, errors.New("authority unavailable")
		}
		return f.options.Authorization.AuthorizeSubscription(ctx, uid, q)
	})
	var stream *app.DeliveryStream
	stream, err := makeSender(t, f, authorization).Open(context.Background(), f.connection, deliverySink{enqueue: func(ctx context.Context, _ app.PreparedDelivery, _ bool) (app.DeliveryDisposition, error) {
		inner, err := stream.Turn(ctx, f.key)
		require.NoError(t, err)
		require.True(t, inner.Busy)
		return app.DeliveryQueued, nil
	}, close: func(context.Context, meta.MQTTSessionEndReason) error {
		t.Fatal("unavailable authority closed stream")
		return nil
	}})
	require.NoError(t, err)
	_, err = stream.Turn(context.Background(), f.key)
	require.Error(t, err)
	require.Equal(t, meta.MQTTSessionActive, f.row(t).State)
	unavailable = false
	got, err := stream.Turn(context.Background(), f.key)
	require.NoError(t, err)
	require.True(t, got.Enqueued)
}

func TestSenderAmbiguousEnqueueNeverRetriesConnection(t *testing.T) {
	for _, fault := range []string{"error", "panic", "unknown", "fenced"} {
		t.Run(fault, func(t *testing.T) {
			f, a, _ := setupWindow(t)
			f.setMessages([]ch.MQTTReplayPublication{{Message: ch.Message{Payload: []byte("one")}}})
			accountWindow(t, f, a)
			count, closed := 0, false
			stream, err := makeSender(t, f, f.options.Authorization).Open(context.Background(), f.connection, deliverySink{enqueue: func(_ context.Context, d app.PreparedDelivery, _ bool) (app.DeliveryDisposition, error) {
				count++
				switch fault {
				case "error":
					return 0, errors.New("unknown enqueue")
				case "panic":
					panic("secret")
				case "unknown":
					return 0, nil
				default:
					require.NoError(t, f.owners.Fence(d.Owner))
					return app.DeliveryQueued, nil
				}
			}, close: func(context.Context, meta.MQTTSessionEndReason) error {
				require.Zero(t, f.owners.Snapshot().Operations)
				closed = true
				return nil
			}})
			require.NoError(t, err)
			_, err = stream.Turn(context.Background(), f.key)
			require.Error(t, err)
			require.NotContains(t, err.Error(), "secret")
			require.True(t, closed)
			require.EqualValues(t, 1, f.row(t).OutboundInflight)
			_, err = stream.Turn(context.Background(), f.key)
			require.ErrorIs(t, err, app.ErrDeliveryClosed)
			require.Equal(t, 1, count)
		})
	}
}

func TestSenderOwnerPressureYieldsBeforeOrInsideTurn(t *testing.T) {
	f, a, _ := setupWindow(t)
	f.setMessages([]ch.MQTTReplayPublication{{Message: ch.Message{Payload: []byte("one")}}})
	accountWindow(t, f, a)
	count := 0
	stream, err := makeSender(t, f, f.options.Authorization).Open(context.Background(), f.connection, deliverySink{enqueue: func(context.Context, app.PreparedDelivery, bool) (app.DeliveryDisposition, error) {
		count++
		return app.DeliveryQueued, nil
	}})
	require.NoError(t, err)
	var release []func()
	for i := 0; i < 8; i++ {
		op, err := f.owners.Begin(context.Background(), f.connection.Owner)
		require.NoError(t, err)
		defer op.Done()
		release = append(release, op.Done)
	}
	for _, freeOne := range []bool{false, true} {
		if freeOne {
			release[0]()
		}
		got, err := stream.Turn(context.Background(), f.key)
		require.NoError(t, err)
		require.True(t, got.Busy)
		require.Zero(t, count)
	}
	for _, done := range release {
		done()
	}
	got, err := stream.Turn(context.Background(), f.key)
	require.NoError(t, err)
	require.True(t, got.Enqueued)
	require.Equal(t, 1, count)
}
