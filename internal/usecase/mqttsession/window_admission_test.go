package mqttsession_test

import (
	"context"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	app "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/publication"
	"github.com/stretchr/testify/require"
)

type windowFixture struct {
	*accountingFixture
	window *acknowledgementStore
}

func (f *windowFixture) MutateMQTTWindow(ctx context.Context, m meta.MQTTWindowMutation) (meta.MQTTWindowResult, error) {
	f.visit(ctx, "window")
	return f.window.MutateMQTTWindow(ctx, m)
}
func (f *windowFixture) ReadMQTT(ctx context.Context, q meta.MQTTRead) (meta.MQTTReadResult, error) {
	f.visit(ctx, "metadata")
	return f.window.ReadMQTT(ctx, q)
}
func (f *windowFixture) ReadChannelMQTTReplay(ctx context.Context, q ch.MQTTReplayConsumerRequest) (ch.MQTTReplayConsumerPage, error) {
	f.visit(ctx, "content")
	require.True(f.t, q.Valid())
	require.Equal(f.t, f.key.SourceGeneration, q.Request.Range.Generation)
	require.LessOrEqual(f.t, q.Request.Range.Through-q.Request.Range.From, uint64(255))
	// Keep complete-page fault injections intact; later turns receive exact slices.
	if q.Request.Range.From == f.page.Before.Through+1 && q.Request.Range.Through == f.page.After.Through {
		return f.page, nil
	}
	p := ch.MQTTReplayConsumerPage{Before: f.page.Before}
	for _, e := range f.page.Records {
		prefix := f.page.Before
		prefix.Through, prefix.TotalBytes, prefix.TotalStoredBytes, prefix.Digest = e.Message.MessageSeq, e.TotalBytes, e.TotalStoredBytes, e.Digest
		if e.Message.MessageSeq < q.Request.Range.From {
			p.Before = prefix
			continue
		}
		if e.Message.MessageSeq > q.Request.Range.Through {
			break
		}
		p.Records = append(p.Records, e)
		p.After = prefix
	}
	return p, nil
}
func setupWindow(t *testing.T) (*windowFixture, *app.Accounting, *app.WindowAdmission) {
	t.Helper()
	f, a := setupAccounting(t)
	w := &windowFixture{accountingFixture: f, window: &acknowledgementStore{groupSourceStore: f.store}}
	service, err := app.NewWindowAdmission(app.WindowAdmissionOptions{Store: w, Owners: f.owners, Metadata: w, Channels: w, Authorization: f.options.Authorization, Now: func() time.Time { return f.now }})
	require.NoError(t, err)
	return w, a, service
}
func accountWindow(t *testing.T, f *windowFixture, a *app.Accounting) {
	t.Helper()
	_, err := a.Account(context.Background(), f.key)
	require.NoError(t, err)
}
func TestWindowAdmissionReturnsProvedOriginalExchange(t *testing.T) {
	f, a, w := setupWindow(t)
	f.setMessages([]ch.MQTTReplayPublication{{Message: ch.Message{Payload: []byte("first"), SyncOnce: true}}, {Message: ch.Message{Payload: []byte("second")}}})
	accountWindow(t, f, a)
	for i := 0; i < 2; i++ {
		got, err := w.Prepare(context.Background(), f.connection.Owner, f.key)
		require.NoError(t, err)
		require.NotNil(t, got.Delivery)
		d := got.Delivery
		original := f.page.Records[i]
		require.Equal(t, original, d.Publication)
		require.Equal(t, f.connection.Owner, d.Owner)
		require.EqualValues(t, 1, d.QoS)
		require.Equal(t, f.intent.Topic, d.Topic)
		require.Equal(t, subscriptionRequest().SubscriptionIdentifier, d.SubscriptionIdentifier)
		require.Equal(t, meta.MQTTInflightPublication{Position: original.Message.MessageSeq, MessageID: original.Message.MessageID, MessageSeq: original.Message.MessageSeq, ContentVersion: original.ContentVersion, ContentHash: hex.EncodeToString(original.ContentHash[:]), Bytes: original.AccountedBytes, SubscriptionIdentifier: d.SubscriptionIdentifier}, d.Exchange.Publication)
		require.Equal(t, f.key, d.Exchange.Key)
		require.EqualValues(t, i+1, d.Exchange.DeliveryOrder)
	}
	idle, err := w.Prepare(context.Background(), f.connection.Owner, f.key)
	require.NoError(t, err)
	require.True(t, idle.Idle)
	require.Nil(t, idle.Delivery)
	require.Equal(t, 2, f.window.writes)
	require.EqualValues(t, 2, f.row(t).OutboundInflight)
}
func TestWindowAdmissionSkipsWithOriginalDebitAndPreservesACKGap(t *testing.T) {
	f, a, w := setupWindow(t)
	expiry := uint32(1)
	f.setMessages([]ch.MQTTReplayPublication{
		{Message: ch.Message{Payload: []byte("begun")}},
		{Message: ch.Message{PublicationMetadata: f.publication(publication.SourceMQTT, 1, "main", "other", &expiry)}},
		{Message: ch.Message{Payload: []byte("control"), SyncOnce: true}, Internal: true},
		{Message: ch.Message{Payload: []byte("last")}},
	})
	accountWindow(t, f, a)
	first, err := w.Prepare(context.Background(), f.connection.Owner, f.key)
	require.NoError(t, err)
	require.NotNil(t, first.Delivery)
	f.now = f.now.Add(time.Second)
	skipped, err := w.Prepare(context.Background(), f.connection.Owner, f.key)
	require.NoError(t, err)
	require.True(t, skipped.Advanced)
	require.EqualValues(t, 13, skipped.Through)
	require.Nil(t, skipped.Delivery)
	cursor := readAcknowledgementCursor(t, f.groupSourceFixture, f.key)
	require.EqualValues(t, 10, cursor.CompletedThrough)
	require.EqualValues(t, 2, cursor.PendingMessages)
	require.Equal(t, f.page.Records[0].AccountedBytes+f.page.Records[3].AccountedBytes, cursor.PendingBytes)
	last, err := w.Prepare(context.Background(), f.connection.Owner, f.key)
	require.NoError(t, err)
	require.EqualValues(t, 14, last.Delivery.Exchange.Publication.Position)
}
func TestWindowAdmissionDoesNotResurrectUnchargedQoS1(t *testing.T) {
	f, a, w := setupWindow(t)
	sub := f.subscription(t, f.intent.Topic)
	f.setMessages([]ch.MQTTReplayPublication{{Message: ch.Message{PublicationMetadata: f.publication(publication.SourceMQTT, 1, sub.Namespace, sub.ClientID, nil)}}})
	accountWindow(t, f, a)
	q := subscriptionRequest()
	q.NoLocal = false
	_, err := f.subscriptions.Subscribe(context.Background(), f.connection.Owner, q)
	require.NoError(t, err)
	got, err := w.Prepare(context.Background(), f.connection.Owner, f.key)
	require.NoError(t, err)
	require.True(t, got.Advanced)
	require.Nil(t, got.Delivery)
	require.Zero(t, f.row(t).PendingMessages)
	require.Zero(t, f.row(t).OutboundInflight)
}
func TestWindowAdmissionQoS0CompletesOnlyCapturedCandidate(t *testing.T) {
	for _, downgrade := range []bool{false, true} {
		t.Run(map[bool]string{false: "original_qos0", true: "subscription_downgrade"}[downgrade], func(t *testing.T) {
			f, a, w := setupWindow(t)
			if !downgrade {
				f.setMessages([]ch.MQTTReplayPublication{{Message: ch.Message{PublicationMetadata: f.publication(publication.SourceMQTT, 0, "main", "other", nil)}}})
			}
			accountWindow(t, f, a)
			if downgrade {
				q := subscriptionRequest()
				q.RequestedQoS = 0
				_, err := f.subscriptions.Subscribe(context.Background(), f.connection.Owner, q)
				require.NoError(t, err)
			}
			before := f.row(t)
			got, err := w.Prepare(context.Background(), f.connection.Owner, f.key)
			require.NoError(t, err)
			require.NotNil(t, got.Delivery)
			require.Zero(t, got.Delivery.QoS)
			require.Zero(t, got.Delivery.Exchange)
			if downgrade {
				require.Equal(t, before, f.row(t))
				require.Zero(t, f.window.writes)
			} else {
				require.True(t, got.Advanced)
				require.Equal(t, before.Revision+1, f.row(t).Revision)
				require.Equal(t, 1, f.window.writes)
			}
			forged := app.PreparedDelivery{Owner: got.Delivery.Owner, Publication: got.Delivery.Publication, QoS: 0}
			changed, err := w.CompleteQoS0(context.Background(), forged)
			require.Error(t, err)
			require.False(t, changed)
			// Public presentation fields cannot redirect the private completion mutation.
			got.Delivery.Owner.OwnerGeneration++
			got.Delivery.Publication.Message.MessageSeq++
			changed, err = w.CompleteQoS0(context.Background(), *got.Delivery)
			require.NoError(t, err)
			require.Equal(t, downgrade, changed)
			require.Zero(t, f.row(t).PendingMessages)
			require.Zero(t, f.row(t).PendingBytes)
			require.Zero(t, f.row(t).OutboundInflight)
			cursor := readAcknowledgementCursor(t, f.groupSourceFixture, f.key)
			require.EqualValues(t, 11, cursor.CompletedThrough)
			changed, err = w.CompleteQoS0(context.Background(), *got.Delivery)
			if downgrade {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.False(t, changed)
			require.Equal(t, 1, f.window.writes)
		})
	}
}
func TestWindowAdmissionQoS0RejectsChangedParentAndAmbiguousCommit(t *testing.T) {
	for _, fault := range []string{"options", "lost_reply", "cancel", "clock", "panic"} {
		t.Run(fault, func(t *testing.T) {
			f, a, w := setupWindow(t)
			accountWindow(t, f, a)
			q := subscriptionRequest()
			q.RequestedQoS = 0
			_, err := f.subscriptions.Subscribe(context.Background(), f.connection.Owner, q)
			require.NoError(t, err)
			got, err := w.Prepare(context.Background(), f.connection.Owner, f.key)
			require.NoError(t, err)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch fault {
			case "options":
				q.NoLocal = false
				_, err = f.subscriptions.Subscribe(ctx, f.connection.Owner, q)
				require.NoError(t, err)
			case "lost_reply":
				f.window.after = func(*meta.MQTTWindowResult) error { return errors.New("lost reply") }
			case "cancel":
				cancel()
			case "clock":
				f.now = f.now.Add(-time.Second)
			case "panic":
				f.window.after = func(*meta.MQTTWindowResult) error { panic("secret") }
			}
			changed, err := w.CompleteQoS0(ctx, *got.Delivery)
			require.Error(t, err)
			require.False(t, changed)
			require.NotContains(t, err.Error(), "secret")
			if fault == "lost_reply" || fault == "panic" {
				require.Zero(t, f.row(t).PendingMessages)
			} else {
				require.EqualValues(t, 1, f.row(t).PendingMessages)
			}
		})
	}
}
func TestWindowAdmissionFullDoesNotExposeDelivery(t *testing.T) {
	f, a, w := setupWindow(t)
	f.setMessages([]ch.MQTTReplayPublication{{Message: ch.Message{Payload: []byte("one")}}, {Message: ch.Message{Payload: []byte("two")}}})
	accountWindow(t, f, a)
	row := f.row(t)
	row.Revision++
	row.WindowLimit = 1
	_, err := f.store.CompareAndSwapMQTTSession(context.Background(), row.Revision-1, row)
	require.NoError(t, err)
	first, err := w.Prepare(context.Background(), f.connection.Owner, f.key)
	require.NoError(t, err)
	require.NotNil(t, first.Delivery)
	got, err := w.Prepare(context.Background(), f.connection.Owner, f.key)
	require.NoError(t, err)
	require.True(t, got.Full)
	require.Nil(t, got.Delivery)
	require.EqualValues(t, 1, f.row(t).OutboundInflight)
	require.EqualValues(t, 2, f.row(t).PendingMessages)
}
func TestWindowAdmissionRejectsUnprovenOrChangedWork(t *testing.T) {
	for _, fault := range []string{"missing_head", "wrong_bytes", "partial", "extra", "foreign_cursor", "debt", "uid", "owner", "legacy", "subscription", "binding", "no_anchor", "gap", "digest", "metadata", "overlay", "denied", "permission_change", "placement", "options_race", "clock", "clock_regression", "expired", "canceled", "panic", "lost_reply", "bad_receipt", "readback", "readback_owner", "reply_panic", "canceled_after_commit"} {
		t.Run(fault, func(t *testing.T) {
			f, a, w := setupWindow(t)
			accountWindow(t, f, a)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch fault {
			case "missing_head", "wrong_bytes", "partial", "extra", "foreign_cursor", "debt", "uid", "owner", "legacy":
				f.window.read = func(q meta.MQTTRead, r *meta.MQTTReadResult) {
					if q.Kind != meta.MQTTReadAccounting {
						return
					}
					switch fault {
					case "missing_head":
						r.Accounting = nil
					case "wrong_bytes":
						r.Accounting.Items[0].Bytes++
						r.DeliveryCursors[0].PendingBytes++
						r.Session.PendingBytes++
					case "partial":
						r.Done = false
					case "extra":
						r.Wills = []meta.MQTTWill{{}}
					case "foreign_cursor":
						r.DeliveryCursors[0].Key.SourceGeneration = "foreign"
					case "debt":
						r.Session.PendingMessages = 0
						r.Session.PendingBytes = 0
					case "uid":
						r.Session.UID = "foreign"
					case "owner":
						r.Session.OwnerGeneration++
					case "legacy":
						r.Accounting = nil
						r.DeliveryCursors[0].AccountingVersion = 0
						r.DeliveryCursors[0].AccountingHead = 0
						r.DeliveryCursors[0].AccountingTail = 0
					}
				}
			case "subscription":
				f.window.read = func(q meta.MQTTRead, r *meta.MQTTReadResult) {
					if q.Kind == meta.MQTTReadSubscription {
						r.Subscriptions[0].Generation++
					}
				}
			case "binding":
				f.window.read = func(q meta.MQTTRead, r *meta.MQTTReadResult) {
					if q.Kind == meta.MQTTReadSourceBinding {
						r.Bindings[0].OperationID += "foreign"
					}
				}
			case "no_anchor":
				f.plan.HasAnchor = false
				f.plan.Anchor = ch.MQTTReplayAnchorProof{}
			case "gap":
				f.page.Records[0].Message.MessageSeq++
			case "digest":
				f.page.After.Digest = [32]byte{88}
				f.page.Records[0].Digest = f.page.After.Digest
			case "metadata":
				f.page.Records[0].Message.PublicationMetadata = []byte{99}
			case "overlay":
				f.page.Records[0].Message.Version = 1
			case "denied":
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
			case "options_race":
				f.before = func(s string) {
					if s == "content" {
						q := subscriptionRequest()
						q.NoLocal = false
						_, err := f.subscriptions.Subscribe(ctx, f.connection.Owner, q)
						require.NoError(t, err)
					}
				}
			case "clock":
				f.now = f.now.Add(-time.Second)
			case "clock_regression":
				f.now = f.now.Add(time.Second)
				f.before = func(s string) {
					if s == "content" {
						f.now = f.now.Add(-time.Millisecond)
					}
				}
			case "expired":
				f.now = f.now.Add(time.Minute)
			case "canceled":
				f.before = func(s string) {
					if s == "content" {
						cancel()
					}
				}
			case "panic":
				f.before = func(s string) {
					if s == "content" {
						panic("secret")
					}
				}
			case "lost_reply":
				f.window.after = func(*meta.MQTTWindowResult) error { return errors.New("lost reply") }
			case "bad_receipt":
				f.window.after = func(r *meta.MQTTWindowResult) error { r.DeliveryOrder++; return nil }
			case "readback":
				f.window.read = func(q meta.MQTTRead, r *meta.MQTTReadResult) {
					if q.Kind == meta.MQTTReadInflight {
						r.Inflight[0].Publication.MessageID++
					}
				}
			case "readback_owner":
				f.window.read = func(q meta.MQTTRead, r *meta.MQTTReadResult) {
					if q.Kind == meta.MQTTReadInflight {
						r.Session.OwnerGeneration++
					}
				}
			case "reply_panic":
				f.window.after = func(*meta.MQTTWindowResult) error { panic("secret") }
			case "canceled_after_commit":
				f.window.after = func(*meta.MQTTWindowResult) error { cancel(); return nil }
			}
			got, err := w.Prepare(ctx, f.connection.Owner, f.key)
			require.Error(t, err)
			require.Zero(t, got)
			require.NotContains(t, err.Error(), "secret")
			switch fault {
			case "lost_reply", "bad_receipt", "readback", "readback_owner", "reply_panic", "canceled_after_commit":
				require.EqualValues(t, 1, f.row(t).OutboundInflight)
			default:
				require.Zero(t, f.row(t).OutboundInflight)
			}
		})
	}
}

func TestWindowAdmissionConsumesAtMostOneAccountingReceipt(t *testing.T) {
	f, _, w := setupWindow(t)
	q := subscriptionRequest()
	q.NoLocal = false
	_, err := f.subscriptions.Subscribe(context.Background(), f.connection.Owner, q)
	require.NoError(t, err)
	f.setMessages([]ch.MQTTReplayPublication{
		{Message: ch.Message{PublicationMetadata: f.publication(publication.SourceMQTT, 1, f.key.Namespace, f.key.ClientID, nil)}},
		{Message: ch.Message{PublicationMetadata: f.publication(publication.SourceMQTT, 1, f.key.Namespace, f.key.ClientID, nil)}},
	})
	a, err := app.NewAccounting(app.AccountingOptions{Store: f, Metadata: f, Channels: f, Authorization: f.options.Authorization, Now: func() time.Time { return f.now }, PageSize: 1})
	require.NoError(t, err)
	for i := 0; i < 2; i++ {
		accountWindow(t, f, a)
	}
	q.NoLocal = true
	_, err = f.subscriptions.Subscribe(context.Background(), f.connection.Owner, q)
	require.NoError(t, err)
	for i := 0; i < 2; i++ {
		got, err := w.Prepare(context.Background(), f.connection.Owner, f.key)
		require.NoError(t, err)
		require.True(t, got.Advanced)
		require.EqualValues(t, 11+i, got.Through)
		require.Nil(t, got.Delivery)
		require.EqualValues(t, 1-i, f.row(t).PendingMessages)
	}
	require.Equal(t, 2, f.window.writes)
}
