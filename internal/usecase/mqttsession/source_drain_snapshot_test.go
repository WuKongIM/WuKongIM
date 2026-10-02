package mqttsession_test

import (
	"context"
	"errors"
	"testing"
	"time"

	app "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

// A real renewal or ACK between cursor and accounting reads changes a coherent
// snapshot, not its validity. A drain must yield before proposing stale charges.
func TestSourceDrainYieldsOnlyValidNewerAccountingSnapshot(t *testing.T) {
	for _, mode := range []string{"renew", "ack", "regressed-parent", "foreign-cursor", "corrupt-head", "unchanged-parent-cursor", "foreign-owner", "canceled", "unknown"} {
		t.Run(mode, func(t *testing.T) {
			f, accounting, window := setupWindow(t)
			f.setMessages([]ch.MQTTReplayPublication{{Message: ch.Message{Payload: []byte("first")}}, {Message: ch.Message{Payload: []byte("second")}}})
			accountWindow(t, f, accounting)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cancelRead := cancel
			prepared, err := window.Prepare(ctx, f.connection.Owner, f.key)
			require.NoError(t, err)
			require.NotNil(t, prepared.Delivery)
			drain, err := app.NewSourceDrain(app.SourceDrainOptions{Store: f.window, Owners: f.owners, Now: func() time.Time { return f.now }})
			require.NoError(t, err)
			ack, err := app.NewAcknowledgements(app.AcknowledgementOptions{Store: f.window, Owners: f.owners, Now: func() time.Time { return f.now }})
			require.NoError(t, err)
			advances := 0
			f.window.before = func(m meta.MQTTWindowMutation) {
				if m.Op == meta.MQTTWindowAdvance {
					advances++
				}
			}
			reads := 0
			lost := errors.New("accounting read unavailable")
			f.store.query = func(ctx context.Context, q meta.MQTTRead) (meta.MQTTReadResult, error) {
				if q.Kind != meta.MQTTReadAccounting {
					return f.store.sessionStore.ReadMQTT(ctx, q)
				}
				reads++
				if mode == "unknown" {
					return meta.MQTTReadResult{}, lost
				}
				if mode == "renew" || mode == "corrupt-head" {
					_, e := f.service.Renew(ctx, f.connection.Owner)
					require.NoError(t, e)
				}
				if mode == "ack" {
					x := prepared.Delivery.Exchange
					_, e := ack.Acknowledge(ctx, app.AcknowledgementCommand{Owner: f.connection.Owner, Key: f.key, PacketID: x.PacketID, DeliveryOrder: x.DeliveryOrder})
					require.NoError(t, e)
				}
				r, e := f.store.sessionStore.ReadMQTT(ctx, q)
				switch mode {
				case "regressed-parent":
					r.Session.Revision--
				case "foreign-cursor":
					r.DeliveryCursors[0].Key.SourceID = "2:other"
				case "corrupt-head":
					r.Accounting.Items[len(r.Accounting.Items)-1].Bytes++
				case "unchanged-parent-cursor":
					r.DeliveryCursors[0].Revision--
				case "foreign-owner":
					r.Session.OwnerGeneration++
				case "canceled":
					cancelRead()
				}
				return r, e
			}
			f.project.remove = func(ctx context.Context, r app.SubscriptionProjectionRequest) (app.SubscriptionProjectionReceipt, error) {
				if mode == "canceled" {
					var stop context.CancelFunc
					ctx, stop = context.WithCancel(ctx)
					defer stop()
					cancelRead = stop
				}
				key := meta.MQTTSourceBindingKey{Owner: meta.MQTTBindingOwner{Kind: meta.MQTTBindingChannel, ID: f.key.SourceID, Generation: f.key.SourceGeneration}, Namespace: f.key.Namespace, ClientID: f.key.ClientID, SessionGeneration: f.key.SessionGeneration, SubscriptionGeneration: f.key.SubscriptionGeneration}
				_, e := drain.Seal(ctx, r.Owner, key)
				return projectionReceipt(r), e
			}
			_, err = f.subscriptions.Unsubscribe(ctx, f.connection.Owner, f.intent.Topic)
			require.Error(t, err)
			require.Equal(t, 1, reads)
			require.Zero(t, advances, "no stale accounting mutation may be proposed")
			if mode == "renew" || mode == "ack" {
				require.ErrorIs(t, err, app.ErrSourceDrainPending)
				f.store.query = nil
				existed, err := f.subscriptions.Unsubscribe(ctx, f.connection.Owner, f.intent.Topic)
				require.NoError(t, err)
				require.True(t, existed)
				require.Equal(t, 1, advances)
			} else {
				require.NotErrorIs(t, err, app.ErrSourceDrainPending)
				if mode == "unknown" {
					require.ErrorIs(t, err, lost)
				}
				if mode == "canceled" {
					require.ErrorIs(t, err, context.Canceled)
				}
			}
			require.Zero(t, f.owners.Snapshot().Operations)
		})
	}
}
