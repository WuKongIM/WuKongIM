package mqttsession_test

import (
	"context"
	"errors"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/publication"
	"github.com/stretchr/testify/require"
)

func TestOriginalQoS0CannotRepeatAcrossTakeoverBeforeCompletion(t *testing.T) {
	for _, source := range []publication.Source{publication.SourceMQTT, publication.SourceWill} {
		t.Run(map[publication.Source]string{publication.SourceMQTT: "mqtt", publication.SourceWill: "will"}[source], func(t *testing.T) {
			f, a, w := setupWindow(t)
			f.setMessages([]ch.MQTTReplayPublication{{Message: ch.Message{PublicationMetadata: f.publication(source, 0, "main", "other", nil)}}})
			accountWindow(t, f, a)
			first, err := w.Prepare(context.Background(), f.connection.Owner, f.key)
			require.NoError(t, err)
			require.NotNil(t, first.Delivery)
			cursor := readAcknowledgementCursor(t, f.groupSourceFixture, f.key)
			require.EqualValues(t, 11, cursor.WindowThrough)
			require.EqualValues(t, 11, cursor.CompletedThrough)
			require.Zero(t, f.row(t).OutboundInflight)
			require.True(t, first.Advanced)
			// Model loss after enqueue but before any completion call. Only the durable
			// cursor survives; the next owner must never receive this position again.
			resumed, err := f.service.Connect(context.Background(), command())
			require.NoError(t, err)
			require.True(t, resumed.SessionPresent)
			second, err := w.Prepare(context.Background(), resumed.Owner, f.key)
			require.NoError(t, err)
			require.True(t, second.Idle)
			require.Nil(t, second.Delivery)
			changed, err := w.CompleteQoS0(context.Background(), *first.Delivery)
			require.Error(t, err)
			require.False(t, changed)
			require.Equal(t, 1, f.window.writes)
		})
	}
}
func TestOriginalQoS0ClaimFailuresNeverExposeAnotherAttempt(t *testing.T) {
	for _, fault := range []string{"lost_reply", "unchanged", "bad_receipt", "panic", "canceled"} {
		t.Run(fault, func(t *testing.T) {
			f, a, w := setupWindow(t)
			f.setMessages([]ch.MQTTReplayPublication{{Message: ch.Message{PublicationMetadata: f.publication(publication.SourceMQTT, 0, "main", "other", nil)}}})
			accountWindow(t, f, a)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f.window.after = func(r *meta.MQTTWindowResult) error {
				switch fault {
				case "lost_reply":
					return errors.New("lost claim reply")
				case "unchanged":
					r.Status = meta.MQTTWindowUnchanged
				case "bad_receipt":
					r.PacketID = 1
				case "panic":
					panic("secret")
				case "canceled":
					cancel()
				}
				return nil
			}
			got, err := w.Prepare(ctx, f.connection.Owner, f.key)
			if fault == "unchanged" {
				require.NoError(t, err)
				require.True(t, got.Advanced)
			} else {
				require.Error(t, err)
				require.Zero(t, got)
				require.NotContains(t, err.Error(), "secret")
			}
			require.Nil(t, got.Delivery)
			f.window.after = nil
			next, err := w.Prepare(context.Background(), f.connection.Owner, f.key)
			require.NoError(t, err)
			require.True(t, next.Idle)
			require.Nil(t, next.Delivery)
			require.Equal(t, 1, f.window.writes)
		})
	}
}
func TestOriginalQoS0CompletionDoesNotDependOnUnrelatedRevision(t *testing.T) {
	f, a, w := setupWindow(t)
	f.setMessages([]ch.MQTTReplayPublication{{Message: ch.Message{PublicationMetadata: f.publication(publication.SourceMQTT, 0, "main", "other", nil)}}})
	accountWindow(t, f, a)
	got, err := w.Prepare(context.Background(), f.connection.Owner, f.key)
	require.NoError(t, err)
	require.NotNil(t, got.Delivery)
	q := subscriptionRequest()
	q.NoLocal = false
	_, err = f.subscriptions.Subscribe(context.Background(), f.connection.Owner, q)
	require.NoError(t, err)
	changed, err := w.CompleteQoS0(context.Background(), *got.Delivery)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, 1, f.window.writes)
}
func TestOriginalQoS0RejectsContradictoryCharge(t *testing.T) {
	f, _, w := setupWindow(t)
	f.setMessages([]ch.MQTTReplayPublication{{Message: ch.Message{PublicationMetadata: f.publication(publication.SourceMQTT, 0, "main", "other", nil)}}})
	o := f.connection.Owner
	sub := f.subscription(t, f.intent.Topic)
	// A malformed qualification producer claimed an original-QoS-0 position.
	got, err := f.store.MutateMQTTDeliveryCursor(context.Background(), meta.MQTTDeliveryCursorMutation{Key: f.key, ExpectedRevision: f.row(t).Revision, OwnerGeneration: o.OwnerGeneration, OwnerNodeID: o.NodeID, OwnerBootID: o.BootID, ConnectionID: o.ConnectionID, Op: meta.MQTTCursorAccountQualified, Topic: sub.Topic, AuthorizationVersion: sub.AuthorizationVersion, Through: 11, AddedMessages: 1, AddedBytes: f.page.Records[0].AccountedBytes, UpdatedAtMS: f.now.UnixMilli(), Qualified: &meta.MQTTQualifiedAccounting{From: 11, SubscriptionRevision: sub.Revision, Items: []meta.MQTTAccountingItem{{Position: 11, Bytes: f.page.Records[0].AccountedBytes}}}})
	require.NoError(t, err)
	require.Equal(t, meta.MQTTSessionCASApplied, got.Status)
	result, err := w.Prepare(context.Background(), o, f.key)
	require.Error(t, err)
	require.Zero(t, result)
	require.EqualValues(t, 1, f.row(t).PendingMessages)
	require.Zero(t, f.window.writes)
}
