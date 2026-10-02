package mqttsession_test

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	app "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/publication"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
	"github.com/stretchr/testify/require"
)

type accountingFixture struct {
	*groupSourceFixture
	t             *testing.T
	key           meta.MQTTDeliveryCursorKey
	placement     ch.Meta
	plan          ch.MQTTReplayPlan
	page          ch.MQTTReplayConsumerPage
	before        func(string)
	writes, reads int
}

func (f *accountingFixture) visit(ctx context.Context, stage string) {
	deadline, ok := ctx.Deadline()
	require.True(f.t, ok)
	require.LessOrEqual(f.t, time.Until(deadline), 5*time.Second)
	if f.before != nil {
		f.before(stage)
	}
}
func (f *accountingFixture) ResolveChannelMetaFresh(ctx context.Context, id ch.ChannelID) (ch.Meta, error) {
	f.visit(ctx, "placement")
	require.Equal(f.t, f.placement.ID, id)
	return f.placement, nil
}
func (f *accountingFixture) PlanChannelMQTTReplay(ctx context.Context, q ch.MQTTReplayPlanRequest) (ch.MQTTReplayPlan, error) {
	f.visit(ctx, "anchor")
	require.True(f.t, q.Valid())
	return f.plan, nil
}
func (f *accountingFixture) ReadChannelMQTTReplay(ctx context.Context, q ch.MQTTReplayConsumerRequest) (ch.MQTTReplayConsumerPage, error) {
	f.visit(ctx, "content")
	f.reads++
	require.True(f.t, q.Valid())
	require.Equal(f.t, f.key.SourceGeneration, q.Request.Range.Generation)
	require.Equal(f.t, uint64(11), q.Request.Range.From)
	require.LessOrEqual(f.t, q.Request.Range.Through-q.Request.Range.From, uint64(255))
	return f.page, nil
}
func (f *accountingFixture) ReadMQTT(ctx context.Context, q meta.MQTTRead) (meta.MQTTReadResult, error) {
	f.visit(ctx, "metadata")
	return f.store.ReadMQTT(ctx, q)
}
func (f *accountingFixture) MutateMQTTDeliveryCursor(ctx context.Context, m meta.MQTTDeliveryCursorMutation) (meta.MQTTDeliveryCursorResult, error) {
	f.visit(ctx, "commit")
	f.writes++
	require.Equal(f.t, meta.MQTTCursorAccountQualified, m.Op)
	return f.store.MutateMQTTDeliveryCursor(ctx, m)
}
func setupAccounting(t *testing.T) (*accountingFixture, *app.Accounting) {
	t.Helper()
	base := setupGroupSource(t)
	generation := "mqtt-log-v1:01" + strings.Repeat("00", 31)
	base.protect = func(_ int, id app.SourceChannel) (app.ProtectedSource, error) {
		return app.ProtectedSource{Channel: id, Generation: generation, CommittedThrough: 10}, nil
	}
	prepared, err := base.prepare()
	require.NoError(t, err)
	base.project.establish = func(_ context.Context, r app.SubscriptionProjectionRequest) (app.SubscriptionProjectionReceipt, error) {
		return projectionReceipt(r), nil
	}
	_, err = base.subscriptions.Subscribe(context.Background(), base.connection.Owner, subscriptionRequest())
	require.NoError(t, err)
	id := ch.ChannelID{ID: "group", Type: 2}
	f := &accountingFixture{groupSourceFixture: base, t: t, key: prepared.Cursor.Key, placement: ch.Meta{ID: id, Key: ch.ChannelKeyForID(id), Epoch: 1, LeaderEpoch: 1, RouteGeneration: 1, Leader: 1, Replicas: []ch.NodeID{1, 2, 3}, ISR: []ch.NodeID{1, 2}, MinISR: 2, Status: ch.StatusActive}}
	f.setMessages([]ch.MQTTReplayPublication{{Message: ch.Message{Payload: []byte("native"), FromUID: "alice"}}})
	a, err := app.NewAccounting(app.AccountingOptions{Store: f, Metadata: f, Channels: f, Authorization: base.options.Authorization, Now: func() time.Time { return f.now }})
	require.NoError(t, err)
	return f, a
}
func (f *accountingFixture) setMessages(entries []ch.MQTTReplayPublication) {
	// Synthetic source proofs are confined to this port fixture. Real Node
	// composition is separately verified against committed source storage.
	before := ch.MQTTReplayPrefix{Generation: f.key.SourceGeneration, Through: 10, TotalBytes: 1, TotalStoredBytes: 100, Digest: [32]byte{1}}
	f.page = ch.MQTTReplayConsumerPage{Before: before}
	tail := before
	for i, e := range entries {
		e.Message.ChannelID, e.Message.ChannelType = "group", 2
		e.Message.MessageID, e.Message.MessageSeq = uint64(100+i), uint64(11+i)
		if e.Message.ServerTimestampMS == 0 {
			e.Message.ServerTimestampMS = f.now.UnixMilli()
		}
		e.ContentVersion, e.ContentHash, e.Digest = 1, [32]byte{2}, [32]byte{byte(i + 3)}
		e.AccountedBytes = uint64(len(e.Message.Payload) + len(e.Message.PublicationMetadata))
		e.TotalBytes = tail.TotalBytes + e.AccountedBytes
		e.TotalStoredBytes = tail.TotalStoredBytes + e.AccountedBytes + 128
		tail.Through, tail.TotalBytes, tail.TotalStoredBytes, tail.Digest = e.Message.MessageSeq, e.TotalBytes, e.TotalStoredBytes, e.Digest
		f.page.Records = append(f.page.Records, e)
	}
	f.page.After = tail
	f.plan = ch.MQTTReplayPlan{Source: ch.MQTTSourceSnapshot{Generation: f.key.SourceGeneration, CommittedThrough: tail.Through + 1}, HasAnchor: true, Anchor: ch.MQTTReplayAnchorProof{Anchor: quorumlog.MQTTReplayAnchor{SourceCommand: ch.CommandID{1}, Through: tail.Through, TotalBytes: tail.TotalBytes, TotalStoredBytes: tail.TotalStoredBytes, Digest: tail.Digest}, Manifest: ch.ProposalManifest{Version: 5, ChannelEpoch: 1, LeaderTerm: 1, FenceVersion: 1, CommandID: ch.CommandID{2}, BaseOffset: tail.Through, LastOffset: tail.Through + 1, PreviousIndex: tail.Through, PreviousTerm: 1, PreviousDigest: ch.EntryDigest{3}, Digest: ch.EntryDigest{4}}}}
}
func (f *accountingFixture) publication(source publication.Source, qos byte, namespace, client string, expiry *uint32) []byte {
	m := publication.Metadata{Source: source, QoS: qos, AcceptedAtMS: f.now.UnixMilli(), PublisherNamespace: namespace, PublisherClientID: client, OriginalTopic: f.intent.Topic}
	if source == publication.SourceWill {
		m.AcceptedAtMS = 0
	}
	if expiry != nil {
		m.Properties = []publication.Property{{Kind: publication.MessageExpiry, Number: *expiry}}
	}
	b, err := publication.Encode(m)
	require.NoError(f.t, err)
	return b
}
func TestAccountingQualifiesOriginalSemanticsOnlineAndOffline(t *testing.T) {
	for _, offline := range []bool{false, true} {
		t.Run(map[bool]string{false: "online", true: "offline"}[offline], func(t *testing.T) {
			f, a := setupAccounting(t)
			sub := f.subscription(t, f.intent.Topic)
			require.True(t, sub.NoLocal)
			zero := uint32(0)
			entries := []ch.MQTTReplayPublication{
				{Message: ch.Message{Payload: []byte("native"), FromUID: "alice"}},
				{Message: ch.Message{Payload: []byte("same UID native command"), FromUID: "alice", SyncOnce: true}},
				{Message: ch.Message{Payload: []byte("internal"), SyncOnce: true}, Internal: true},
				{Message: ch.Message{PublicationMetadata: f.publication(publication.SourceMQTT, 0, "main", "other", nil)}},
				{Message: ch.Message{PublicationMetadata: f.publication(publication.SourceMQTT, 1, sub.Namespace, sub.ClientID, nil)}},
				{Message: ch.Message{PublicationMetadata: f.publication(publication.SourceMQTT, 1, "other", sub.ClientID, nil)}},
				{Message: ch.Message{PublicationMetadata: f.publication(publication.SourceMQTT, 1, "main", "other", &zero)}},
				{Message: ch.Message{PublicationMetadata: f.publication(publication.SourceWill, 1, "main", "other", nil)}},
				{Message: ch.Message{Payload: []byte("native expired"), Expire: 1, ServerTimestampMS: f.now.Add(-time.Second).UnixMilli()}},
				{Message: ch.Message{PublicationMetadata: f.publication(publication.SourceWill, 1, "main", "other", &zero)}},
			}
			f.setMessages(entries)
			if offline {
				require.NoError(t, f.service.Disconnect(context.Background(), app.DisconnectCommand{Owner: f.connection.Owner, Normal: true}))
			}
			got, err := a.Account(context.Background(), f.key)
			require.NoError(t, err)
			require.True(t, got.Changed)
			require.False(t, got.Ended)
			require.EqualValues(t, 20, got.Through)
			require.EqualValues(t, 4, got.AddedMessages)
			var total uint64
			for _, i := range []int{0, 1, 5, 7} {
				total += f.page.Records[i].AccountedBytes
			}
			require.Equal(t, total, got.AddedBytes)
			r, err := f.store.ReadMQTT(context.Background(), meta.MQTTRead{Kind: meta.MQTTReadAccounting, CursorKey: f.key})
			require.NoError(t, err)
			require.Equal(t, []meta.MQTTAccountingItem{{Position: 11, Bytes: f.page.Records[0].AccountedBytes}, {Position: 12, Bytes: f.page.Records[1].AccountedBytes}, {Position: 16, Bytes: f.page.Records[5].AccountedBytes}, {Position: 18, Bytes: f.page.Records[7].AccountedBytes}}, r.Accounting.Items)
			again, err := a.Account(context.Background(), f.key)
			require.NoError(t, err)
			require.True(t, again.Idle)
			require.False(t, again.Changed)
			require.Equal(t, 1, f.writes)
			require.Equal(t, 1, f.reads)
		})
	}
}
func TestAccountingRejectsUnprovenOrChangedWork(t *testing.T) {
	for _, fault := range []string{"no-anchor", "gap", "wrong-digest", "wrong-source", "overlay", "metadata", "native-expiry-overflow", "closed", "denied", "permission-change", "takeover", "options-race", "placement", "expired-offline", "expired-active", "clock", "canceled", "panic", "partial"} {
		t.Run(fault, func(t *testing.T) {
			f, a := setupAccounting(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch fault {
			case "no-anchor":
				f.plan.HasAnchor = false
				f.plan.Anchor = ch.MQTTReplayAnchorProof{}
			case "gap":
				f.page.Records[0].Message.MessageSeq++
			case "wrong-digest":
				f.page.After.Digest = [32]byte{88}
				f.page.Records[0].Digest = f.page.After.Digest
			case "wrong-source":
				f.plan.Source.Generation = "foreign"
			case "overlay":
				f.page.Records[0].Message.Version = 1
			case "metadata":
				f.setMessages([]ch.MQTTReplayPublication{{Message: ch.Message{PublicationMetadata: []byte{99}}}})
			case "native-expiry-overflow":
				f.setMessages([]ch.MQTTReplayPublication{{Message: ch.Message{Expire: 1, ServerTimestampMS: math.MaxInt64}}})
			case "closed":
				_, err := f.subscriptions.Unsubscribe(ctx, f.connection.Owner, f.intent.Topic)
				require.NoError(t, err)
			case "denied":
				f.denied = true
			case "permission-change":
				f.before = func(stage string) {
					if stage == "content" {
						f.version++
					}
				}
			case "takeover":
				f.before = func(stage string) {
					if stage == "content" {
						_, err := f.service.Connect(ctx, command())
						require.NoError(t, err)
					}
				}
			case "options-race":
				f.before = func(stage string) {
					if stage == "content" {
						q := subscriptionRequest()
						q.NoLocal = false
						_, err := f.subscriptions.Subscribe(ctx, f.connection.Owner, q)
						require.NoError(t, err)
					}
				}
			case "placement":
				f.before = func(stage string) {
					if stage == "content" {
						f.placement.LeaderEpoch++
					}
				}
			case "expired-offline":
				require.NoError(t, f.service.Disconnect(ctx, app.DisconnectCommand{Owner: f.connection.Owner, Normal: true}))
				f.now = f.now.Add(25 * time.Hour)
			case "expired-active":
				f.now = f.now.Add(time.Minute)
			case "clock":
				f.now = f.now.Add(-time.Second)
			case "canceled":
				f.before = func(stage string) {
					if stage == "content" {
						cancel()
					}
				}
			case "panic":
				f.before = func(stage string) {
					if stage == "content" {
						panic("secret dependency")
					}
				}
			case "partial":
				f.store.query = func(c context.Context, q meta.MQTTRead) (meta.MQTTReadResult, error) {
					r, err := f.store.sessionStore.ReadMQTT(c, q)
					r.Done = false
					return r, err
				}
			}
			got, err := a.Account(ctx, f.key)
			require.Error(t, err)
			require.Zero(t, got)
			require.NotContains(t, err.Error(), "secret")
			require.Zero(t, f.row(t).PendingMessages)
		})
	}
}
func TestAccountingPreservesCommitAfterLostReply(t *testing.T) {
	f, a := setupAccounting(t)
	lost := errors.New("lost accounting reply")
	f.store.afterCursor = func(meta.MQTTDeliveryCursorMutation) error { return lost }
	got, err := a.Account(context.Background(), f.key)
	require.ErrorIs(t, err, lost)
	require.Zero(t, got)
	require.EqualValues(t, 1, f.row(t).PendingMessages)
	f.store.afterCursor = nil
	got, err = a.Account(context.Background(), f.key)
	require.NoError(t, err)
	require.True(t, got.Idle)
	require.Equal(t, 1, f.writes)
}
func TestAccountingZeroQoSAdvancesWithoutReceipts(t *testing.T) {
	f, a := setupAccounting(t)
	request := subscriptionRequest()
	request.RequestedQoS = 0
	_, err := f.subscriptions.Subscribe(context.Background(), f.connection.Owner, request)
	require.NoError(t, err)
	got, err := a.Account(context.Background(), f.key)
	require.NoError(t, err)
	require.True(t, got.Changed)
	require.Zero(t, got.AddedMessages)
	r, err := f.store.ReadMQTT(context.Background(), meta.MQTTRead{Kind: meta.MQTTReadAccounting, CursorKey: f.key})
	require.NoError(t, err)
	require.Nil(t, r.Accounting)
	require.EqualValues(t, 11, r.DeliveryCursors[0].AccountedThrough)
	require.Zero(t, r.Session.PendingMessages)
}

func TestAccountingQuotaResultPreservesDebtWithoutClaimingIsolation(t *testing.T) {
	f, a := setupAccounting(t)
	row := f.row(t)
	row.Revision++
	row.QuotaMessages = 1
	changed, err := f.store.CompareAndSwapMQTTSession(context.Background(), row.Revision-1, row)
	require.NoError(t, err)
	require.Equal(t, meta.MQTTSessionCASApplied, changed.Status)
	f.setMessages([]ch.MQTTReplayPublication{{Message: ch.Message{Payload: []byte("one")}}, {Message: ch.Message{Payload: []byte("two")}}})
	got, err := a.Account(context.Background(), f.key)
	require.NoError(t, err)
	require.True(t, got.Ended)
	require.Equal(t, f.connection.Owner, got.Owner)
	durable := f.row(t)
	require.Equal(t, meta.MQTTSessionEnded, durable.State)
	require.Equal(t, meta.MQTTSessionQuota, durable.TerminationReason)
	require.EqualValues(t, 2, durable.PendingMessages)
	again, err := a.Account(context.Background(), f.key)
	require.Error(t, err)
	require.Zero(t, again)
}

func TestAccountingRejectsClockRegressionWithinOneTurn(t *testing.T) {
	f, _ := setupAccounting(t)
	calls := 0
	a, err := app.NewAccounting(app.AccountingOptions{Store: f, Metadata: f, Channels: f, Authorization: f.options.Authorization, Now: func() time.Time {
		calls++
		switch calls {
		case 1:
			return f.now.Add(10 * time.Millisecond)
		case 2:
			return f.now.Add(20 * time.Millisecond)
		default:
			return f.now.Add(15 * time.Millisecond)
		}
	}})
	require.NoError(t, err)
	got, err := a.Account(context.Background(), f.key)
	require.ErrorIs(t, err, app.ErrClock)
	require.Zero(t, got)
	require.Zero(t, f.writes)
}

func TestAccountingRejectsCursorDebtAboveSessionTotals(t *testing.T) {
	f, a := setupAccounting(t)
	f.store.query = func(ctx context.Context, q meta.MQTTRead) (meta.MQTTReadResult, error) {
		r, err := f.store.sessionStore.ReadMQTT(ctx, q)
		if err == nil && q.Kind == meta.MQTTReadDeliveryCursor {
			c := &r.DeliveryCursors[0]
			c.AccountedThrough, c.WindowThrough = 11, 11
			c.PendingMessages, c.PendingBytes = 1, 6
			c.InflightCount, c.InflightBytes = 1, 6
			c.HeadPacketID, c.TailPacketID = 1, 1
			require.NoError(t, meta.ValidateMQTTDeliveryCursor(*c))
		}
		return r, err
	}
	got, err := a.Account(context.Background(), f.key)
	require.ErrorIs(t, err, app.ErrEvidence)
	require.Zero(t, got)
	require.Zero(t, f.writes)
}
