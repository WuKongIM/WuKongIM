package mqttsession_test

import (
	"context"
	"testing"
	"time"

	app "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func setupExchangeRecovery(t *testing.T, extra ...bool) (*windowFixture, *app.ExchangeRecovery, []app.PreparedDelivery) {
	t.Helper()
	f, a, w := setupWindow(t)
	f.setMessages([]ch.MQTTReplayPublication{{Message: ch.Message{Payload: []byte("first"), Expire: 1}}, {Message: ch.Message{Payload: []byte("second")}}})
	if len(extra) > 0 && extra[0] {
		f.setMessages([]ch.MQTTReplayPublication{{Message: ch.Message{Payload: []byte("first"), Expire: 1}}, {Message: ch.Message{Payload: []byte("second")}}, {Message: ch.Message{Payload: []byte("new")}}})
	}
	accountWindow(t, f, a)
	var pending []app.PreparedDelivery
	for i := 0; i < 2; i++ {
		p, err := w.Prepare(context.Background(), f.connection.Owner, f.key)
		require.NoError(t, err)
		require.NotNil(t, p.Delivery)
		pending = append(pending, *p.Delivery)
	}
	r, err := app.NewExchangeRecovery(app.ExchangeRecoveryOptions{Store: f, Owners: f.owners, Metadata: f, Channels: f, Authorization: f.options.Authorization, Now: func() time.Time { return f.now }})
	require.NoError(t, err)
	f.window.writes = 0
	return f, r, pending
}
func TestExchangeRecoveryResumesOriginalOrderAfterUnsubscribeAndReplacement(t *testing.T) {
	f, r, pending := setupExchangeRecovery(t)
	ctx := context.Background()
	_, err := f.subscriptions.Unsubscribe(ctx, f.connection.Owner, f.intent.Topic)
	require.NoError(t, err)
	// Persist the real normal-removal boundary while exchanges remain unfinished.
	drain, err := app.NewSourceDrain(app.SourceDrainOptions{Store: f.window, Owners: f.owners, Now: func() time.Time { return f.now }})
	require.NoError(t, err)
	bindingKey := meta.MQTTSourceBindingKey{Owner: meta.MQTTBindingOwner{Kind: meta.MQTTBindingChannel, ID: f.key.SourceID, Generation: f.key.SourceGeneration}, Namespace: f.key.Namespace, ClientID: f.key.ClientID, SessionGeneration: f.key.SessionGeneration, SubscriptionGeneration: f.key.SubscriptionGeneration}
	_, err = drain.Seal(ctx, f.connection.Owner, bindingKey)
	require.NoError(t, err)
	q := subscriptionRequest()
	q.RequestedQoS = 0
	q.SubscriptionIdentifier++
	_, err = f.subscriptions.Subscribe(ctx, f.connection.Owner, q)
	require.NoError(t, err)
	require.Greater(t, f.subscription(t, q.Topic).Generation, f.key.SubscriptionGeneration)
	resumed, err := f.service.Connect(ctx, command())
	require.NoError(t, err)
	require.True(t, resumed.SessionPresent)
	f.now = f.now.Add(2 * time.Second) // begun expiry cannot erase recovery
	var after meta.MQTTInflightCursor
	for i := 0; i < 2; i++ {
		got, err := r.Next(ctx, resumed.Owner, after)
		require.NoError(t, err)
		require.NotNil(t, got.Delivery)
		require.Equal(t, pending[i].Publication, got.Delivery.Publication)
		require.Equal(t, pending[i].Exchange.Publication, got.Delivery.Exchange.Publication)
		require.Equal(t, pending[i].Exchange.PacketID, got.Delivery.Exchange.PacketID)
		require.Equal(t, resumed.Owner, got.Delivery.Owner)
		require.EqualValues(t, 1, got.Delivery.QoS)
		require.Equal(t, pending[i].SubscriptionIdentifier, got.Delivery.SubscriptionIdentifier)
		require.Equal(t, meta.MQTTInflightCursor{DeliveryOrder: pending[i].Exchange.DeliveryOrder, PacketID: pending[i].Exchange.PacketID}, got.After)
		after = got.After
	}
	done, err := r.Next(ctx, resumed.Owner, after)
	require.NoError(t, err)
	require.True(t, done.Done)
	require.Nil(t, done.Delivery)
	require.Zero(t, f.window.writes)
	require.EqualValues(t, 2, f.row(t).OutboundInflight)
}
func TestExchangeRecoveryDoesNotExposeConcurrentACKOrNewAdmission(t *testing.T) {
	for _, fault := range []string{"ack_target", "ack_other", "new_admission"} {
		t.Run(fault, func(t *testing.T) {
			f, r, pending := setupExchangeRecovery(t, fault == "new_admission")
			ctx := context.Background()
			ack, err := app.NewAcknowledgements(app.AcknowledgementOptions{Store: f.window, Owners: f.owners, Now: func() time.Time { return f.now }})
			require.NoError(t, err)
			f.before = func(stage string) {
				if stage != "content" {
					return
				}
				f.before = nil
				if fault == "new_admission" {
					window, e := app.NewWindowAdmission(app.WindowAdmissionOptions{Store: f, Owners: f.owners, Metadata: f, Channels: f, Authorization: f.options.Authorization, Now: func() time.Time { return f.now }})
					require.NoError(t, e)
					admitted, e := window.Prepare(ctx, f.connection.Owner, f.key)
					require.NoError(t, e)
					require.NotNil(t, admitted.Delivery)
					return
				}
				i := 0
				if fault == "ack_other" {
					i = 1
				}
				_, e := ack.Acknowledge(ctx, app.AcknowledgementCommand{Owner: f.connection.Owner, Key: f.key, PacketID: pending[i].Exchange.PacketID, DeliveryOrder: pending[i].Exchange.DeliveryOrder})
				require.NoError(t, e)
			}
			got, err := r.Next(ctx, f.connection.Owner, meta.MQTTInflightCursor{})
			if fault == "ack_other" {
				require.NoError(t, err)
				require.NotNil(t, got.Delivery)
				require.Zero(t, got.Delivery.Exchange.NextPacketID)
			} else {
				require.Error(t, err)
				require.Zero(t, got)
			}
		})
	}
}
func TestExchangeRecoveryRejectsUnprovedCurrentWork(t *testing.T) {
	for _, fault := range []string{"empty_page", "partial_page", "cursor", "foreign_order", "uid", "owner", "foreign_cursor", "debt", "binding_released", "binding_progress", "permission", "permission_change", "placement", "no_anchor", "gap", "hash", "message", "edited", "cancel", "clock", "regression", "panic", "readback", "final_owner", "final_debt"} {
		t.Run(fault, func(t *testing.T) {
			f, r, _ := setupExchangeRecovery(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			after := meta.MQTTInflightCursor{}
			switch fault {
			case "cursor":
				after.PacketID = 1
			case "empty_page", "partial_page", "foreign_order", "uid", "owner":
				f.window.read = func(q meta.MQTTRead, p *meta.MQTTReadResult) {
					if q.Kind != meta.MQTTReadInflightPage {
						return
					}
					switch fault {
					case "empty_page":
						p.Inflight = nil
						p.Done = true
						p.After = meta.MQTTReadCursor{}
					case "partial_page":
						p.Inflight = nil
						p.Done = false
					case "foreign_order":
						p.Inflight[0].DeliveryOrder = 0
					case "uid":
						p.Session.UID = "foreign"
					case "owner":
						p.Session.OwnerGeneration++
					}
				}
			case "foreign_cursor", "debt":
				f.window.read = func(q meta.MQTTRead, p *meta.MQTTReadResult) {
					if q.Kind == meta.MQTTReadDeliveryCursor {
						if fault == "foreign_cursor" {
							p.DeliveryCursors[0].Key.SourceGeneration = "foreign"
						} else {
							p.Session.PendingMessages = 0
							p.Session.PendingBytes = 0
						}
					}
				}
			case "binding_released", "binding_progress":
				f.window.read = func(q meta.MQTTRead, p *meta.MQTTReadResult) {
					if q.Kind == meta.MQTTReadSourceBinding {
						if fault == "binding_released" {
							p.Bindings[0].ReleaseReason = meta.MQTTBindingDrained
						} else {
							p.Bindings[0].CompletedThrough = 12
						}
					}
				}
			case "permission":
				f.denied = true
			case "permission_change":
				f.before = func(s string) {
					if s == "content" {
						f.version++
					}
				}
			case "placement":
				f.before = func(s string) {
					if s == "content" {
						f.placement.LeaderEpoch++
					}
				}
			case "no_anchor":
				f.plan.HasAnchor = false
				f.plan.Anchor = ch.MQTTReplayAnchorProof{}
			case "gap":
				f.page.Records[0].Message.MessageSeq++
			case "hash":
				f.page.Records[0].ContentHash[0]++
			case "message":
				f.page.Records[0].Message.MessageID++
			case "edited":
				f.page.Records[0].Message.Version++
			case "cancel":
				f.before = func(s string) {
					if s == "content" {
						cancel()
					}
				}
			case "clock":
				f.now = f.now.Add(-time.Second)
			case "regression":
				f.now = f.now.Add(time.Second)
				f.before = func(s string) {
					if s == "content" {
						f.now = f.now.Add(-time.Millisecond)
					}
				}
			case "panic":
				f.before = func(s string) {
					if s == "content" {
						panic("secret")
					}
				}
			case "readback", "final_owner", "final_debt":
				f.window.read = func(q meta.MQTTRead, p *meta.MQTTReadResult) {
					if q.Kind == meta.MQTTReadInflight {
						if fault == "readback" {
							p.Inflight[0].Publication.Bytes++
						} else if fault == "final_debt" {
							p.Session.OutboundInflight = 0
							p.Session.PendingMessages = 0
							p.Session.PendingBytes = 0
						} else {
							p.Session.OwnerGeneration++
						}
					}
				}
			}
			got, err := r.Next(ctx, f.connection.Owner, after)
			require.Error(t, err)
			require.Zero(t, got)
			require.NotContains(t, err.Error(), "secret")
			require.Zero(t, f.window.writes)
		})
	}
}

func TestExchangeRecoveryRejectsClockRegressionBeforeEmptyResult(t *testing.T) {
	f, _, _ := setupWindow(t)
	r, err := app.NewExchangeRecovery(app.ExchangeRecoveryOptions{Store: f, Owners: f.owners, Metadata: f, Channels: f, Authorization: f.options.Authorization, Now: func() time.Time { return f.now }})
	require.NoError(t, err)
	f.now = f.now.Add(time.Second)
	f.before = func(stage string) {
		if stage == "metadata" {
			f.now = f.now.Add(-time.Millisecond)
		}
	}
	got, err := r.Next(context.Background(), f.connection.Owner, meta.MQTTInflightCursor{})
	require.ErrorIs(t, err, app.ErrClock)
	require.Zero(t, got)
}
