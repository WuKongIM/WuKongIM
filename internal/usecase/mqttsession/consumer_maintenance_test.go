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

func consumerFixture(t *testing.T) (*accountingFixture, *app.ConsumerMaintenance, meta.MQTTSourceBindingKey) {
	t.Helper()
	f, a := setupAccounting(t)
	p, e := app.NewSourceProgress(app.SourceProgressOptions{Store: f.store, Now: func() time.Time { return f.now }})
	require.NoError(t, e)
	r, e := app.NewSourceRemoval(app.SourceRemovalOptions{Store: f.store, Now: func() time.Time { return f.now }})
	require.NoError(t, e)
	c, e := app.NewConsumerMaintenance(app.ConsumerMaintenanceOptions{Store: f.store, Accounting: a, Progress: p, Removal: r, Ender: f.service})
	require.NoError(t, e)
	k := meta.MQTTSourceBindingKey{Owner: meta.MQTTBindingOwner{Kind: meta.MQTTBindingChannel, ID: f.key.SourceID, Generation: f.key.SourceGeneration}, Namespace: f.key.Namespace, ClientID: f.key.ClientID, SessionGeneration: f.key.SessionGeneration, SubscriptionGeneration: f.key.SubscriptionGeneration}
	return f, c, k
}

func TestConsumerMaintenanceAccountsWithoutOwnerAndRecoversQuotaEnd(t *testing.T) {
	for _, lost := range []bool{false, true} {
		t.Run(map[bool]string{false: "confirmed", true: "lost quota reply"}[lost], func(t *testing.T) {
			f, c, k := consumerFixture(t)
			ctx := context.Background()
			row := f.row(t)
			row.Revision++
			row.QuotaMessages = 1
			_, e := f.store.CompareAndSwapMQTTSession(ctx, row.Revision-1, row)
			require.NoError(t, e)
			require.NoError(t, f.service.Disconnect(ctx, app.DisconnectCommand{Owner: f.connection.Owner, Normal: true}))
			f.setMessages([]ch.MQTTReplayPublication{{Message: ch.Message{Payload: []byte("one")}}, {Message: ch.Message{Payload: []byte("two")}}})
			if lost {
				f.store.afterCursor = func(meta.MQTTDeliveryCursorMutation) error { return errors.New("reply lost") }
			}
			out, e := c.Maintain(ctx, k)
			if lost {
				require.Error(t, e)
				require.False(t, out.QuotaEnded)
				f.store.afterCursor = nil
				out, e = c.Maintain(ctx, k)
			}
			require.NoError(t, e)
			require.True(t, out.QuotaEnded)
			require.Equal(t, meta.MQTTSessionEnded, f.row(t).State)
			for range 3 {
				_, e = c.Maintain(ctx, k)
				require.NoError(t, e)
			}
			r, e := f.store.ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadSourceBinding, BindingKey: k})
			require.NoError(t, e)
			require.Len(t, r.Bindings, 1)
			require.Equal(t, meta.MQTTBindingRemoved, r.Bindings[0].Stage)
		})
	}
}

func TestConsumerMaintenanceDoesNotConvertUncertaintyIntoRevocation(t *testing.T) {
	for _, fault := range []string{"denied", "unavailable", "takeover", "partial", "cancelled"} {
		t.Run(fault, func(t *testing.T) {
			f, c, k := consumerFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch fault {
			case "denied":
				f.denied = true
			case "unavailable":
				f.before = func(stage string) {
					if stage == "placement" {
						f.placement.Leader = 0
					}
				}
			case "takeover":
				f.before = func(stage string) {
					if stage == "content" {
						_, e := f.service.Connect(ctx, command())
						require.NoError(t, e)
					}
				}
			case "partial":
				f.store.query = func(c context.Context, q meta.MQTTRead) (meta.MQTTReadResult, error) {
					r, e := f.store.sessionStore.ReadMQTT(c, q)
					r.Done = false
					return r, e
				}
			case "cancelled":
				cancel()
			}
			out, e := c.Maintain(ctx, k)
			if fault == "denied" {
				require.NoError(t, e)
				require.True(t, out.RevokedEnded)
				require.Equal(t, meta.MQTTSessionEnded, f.row(t).State)
			} else {
				require.Error(t, e)
				require.False(t, out.QuotaEnded || out.RevokedEnded)
				require.Equal(t, meta.MQTTSessionActive, f.row(t).State)
			}
		})
	}
}

func TestConsumerMaintenanceProjectsCommittedACKWithoutNewDelivery(t *testing.T) {
	f, s, progress, prepared := progressFixture(t)
	ack := advanceProgressWindow(t, f, prepared)
	ack(1)
	ack(0)
	_, accounting := setupAccounting(t)
	removal, e := app.NewSourceRemoval(app.SourceRemovalOptions{Store: s, Now: func() time.Time { return f.now }})
	require.NoError(t, e)
	c, e := app.NewConsumerMaintenance(app.ConsumerMaintenanceOptions{Store: s, Accounting: accounting, Progress: progress, Removal: removal, Ender: f.service})
	require.NoError(t, e)
	// Closed subscription skips new accounting; only contiguous ACK progress may advance.
	_, e = f.subscriptions.Unsubscribe(context.Background(), f.connection.Owner, f.intent.Topic)
	require.NoError(t, e)
	out, e := c.Maintain(context.Background(), prepared.Binding.Key)
	require.NoError(t, e)
	require.True(t, out.Projected)
	r, e := s.ReadMQTT(context.Background(), meta.MQTTRead{Kind: meta.MQTTReadSourceBinding, BindingKey: prepared.Binding.Key})
	require.NoError(t, e)
	require.Equal(t, prepared.Cursor.StartAfter+2, r.Bindings[0].CompletedThrough)
}
