package mqttsession_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	app "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

type acknowledgementStore struct {
	*groupSourceStore
	reads, writes int
	read          func(meta.MQTTRead, *meta.MQTTReadResult)
	before        func(meta.MQTTWindowMutation)
	after         func(*meta.MQTTWindowResult) error
	write         func(context.Context, meta.MQTTWindowMutation) (meta.MQTTWindowResult, error)
}

func (s *acknowledgementStore) ReadMQTT(ctx context.Context, q meta.MQTTRead) (meta.MQTTReadResult, error) {
	s.reads++
	r, e := s.groupSourceStore.ReadMQTT(ctx, q)
	if e == nil && s.read != nil {
		s.read(q, &r)
	}
	return r, e
}
func (s *acknowledgementStore) MutateMQTTWindow(ctx context.Context, m meta.MQTTWindowMutation) (meta.MQTTWindowResult, error) {
	s.writes++
	if s.write != nil {
		return s.write(ctx, m)
	}
	if s.before != nil {
		s.before(m)
	}
	b := s.db.NewBatch()
	defer b.Close()
	r, e := b.MutateMQTTWindow(7, m)
	if e != nil {
		return meta.MQTTWindowResult{}, e
	}
	if e = b.Commit(ctx); e != nil {
		return meta.MQTTWindowResult{}, e
	}
	if s.after != nil {
		if e = s.after(r); e != nil {
			return meta.MQTTWindowResult{}, e
		}
	}
	return *r, nil
}
func acknowledgementFixture(t *testing.T) (*groupSourceFixture, *acknowledgementStore, *app.Acknowledgements, []app.AcknowledgementCommand) {
	t.Helper()
	f := setupGroupSource(t)
	prepared, e := f.prepare()
	require.NoError(t, e)
	f.project.establish = func(_ context.Context, r app.SubscriptionProjectionRequest) (app.SubscriptionProjectionReceipt, error) {
		return projectionReceipt(r), nil
	}
	_, e = f.subscriptions.Subscribe(context.Background(), f.connection.Owner, subscriptionRequest())
	require.NoError(t, e)
	s := &acknowledgementStore{groupSourceStore: f.store}
	o := f.connection.Owner
	_, e = s.MutateMQTTDeliveryCursor(context.Background(), meta.MQTTDeliveryCursorMutation{Key: prepared.Cursor.Key, ExpectedRevision: f.row(t).Revision, OwnerGeneration: o.OwnerGeneration, OwnerNodeID: o.NodeID, OwnerBootID: o.BootID, ConnectionID: o.ConnectionID, Op: meta.MQTTCursorAccount, Topic: prepared.Cursor.Topic, AuthorizationVersion: prepared.Cursor.AuthorizationVersion, Through: prepared.Cursor.StartAfter + 2, AddedMessages: 2, AddedBytes: 2, UpdatedAtMS: f.now.UnixMilli()})
	require.NoError(t, e)
	var commands []app.AcknowledgementCommand
	for i := uint64(1); i <= 2; i++ {
		position := prepared.Cursor.StartAfter + i
		r, e := s.MutateMQTTWindow(context.Background(), meta.MQTTWindowMutation{Key: prepared.Cursor.Key, ExpectedRevision: f.row(t).Revision, OwnerGeneration: o.OwnerGeneration, OwnerNodeID: o.NodeID, OwnerBootID: o.BootID, ConnectionID: o.ConnectionID, Op: meta.MQTTWindowAdmit, Publication: meta.MQTTInflightPublication{Position: position, MessageID: 100 + i, MessageSeq: position, ContentVersion: 1, ContentHash: strings.Repeat("a", 64), Bytes: 1, SubscriptionIdentifier: subscriptionRequest().SubscriptionIdentifier}, UpdatedAtMS: f.now.UnixMilli()})
		require.NoError(t, e)
		require.Equal(t, meta.MQTTWindowApplied, r.Status)
		commands = append(commands, app.AcknowledgementCommand{Owner: o, Key: prepared.Cursor.Key, PacketID: r.PacketID, DeliveryOrder: r.DeliveryOrder})
	}
	s.writes = 0
	ack, e := app.NewAcknowledgements(app.AcknowledgementOptions{Store: s, Owners: f.owners, Now: func() time.Time { return f.now }})
	require.NoError(t, e)
	return f, s, ack, commands
}
func readAcknowledgementCursor(t *testing.T, f *groupSourceFixture, key meta.MQTTDeliveryCursorKey) meta.MQTTDeliveryCursor {
	t.Helper()
	r, e := f.store.ReadMQTT(context.Background(), meta.MQTTRead{Kind: meta.MQTTReadDeliveryCursor, CursorKey: key})
	require.NoError(t, e)
	require.Len(t, r.DeliveryCursors, 1)
	return r.DeliveryCursors[0]
}

func TestAcknowledgementsPreserveGapsAndCompleteAfterUnsubscribe(t *testing.T) {
	f, s, a, commands := acknowledgementFixture(t)
	ctx := context.Background()
	_, e := f.subscriptions.Unsubscribe(ctx, f.connection.Owner, subscriptionRequest().Topic)
	require.NoError(t, e)
	f.denied = true
	second, e := a.Acknowledge(ctx, commands[1])
	require.NoError(t, e)
	require.True(t, second.Changed)
	require.False(t, second.Absent)
	cursor := readAcknowledgementCursor(t, f, commands[0].Key)
	require.Equal(t, cursor.StartAfter, cursor.CompletedThrough)
	require.EqualValues(t, 1, cursor.InflightCount)
	duplicate, e := a.Acknowledge(ctx, commands[1])
	require.NoError(t, e)
	require.True(t, duplicate.Absent)
	require.False(t, duplicate.Changed)
	require.Equal(t, 1, s.writes)
	first, e := a.Acknowledge(ctx, commands[0])
	require.NoError(t, e)
	require.True(t, first.Changed)
	cursor = readAcknowledgementCursor(t, f, commands[0].Key)
	require.Equal(t, cursor.AccountedThrough, cursor.CompletedThrough)
	require.Zero(t, cursor.PendingMessages)
	require.Zero(t, cursor.PendingBytes)
	require.Zero(t, f.row(t).OutboundInflight)
	require.Equal(t, 3, s.reads)
	require.Equal(t, 2, s.writes)
}

func TestAcknowledgementsRecoverLostReplyWithoutASecondMutation(t *testing.T) {
	f, s, a, commands := acknowledgementFixture(t)
	lost := errors.New("lost ACK commit reply")
	s.after = func(*meta.MQTTWindowResult) error { return lost }
	got, e := a.Acknowledge(context.Background(), commands[0])
	require.ErrorIs(t, e, lost)
	require.Zero(t, got)
	s.after = nil
	got, e = a.Acknowledge(context.Background(), commands[0])
	require.NoError(t, e)
	require.True(t, got.Absent)
	require.False(t, got.Changed)
	require.Equal(t, 1, s.writes)
	require.EqualValues(t, 1, f.row(t).OutboundInflight)
}

func TestAcknowledgementsRejectStaleIdentityOrInvalidEvidence(t *testing.T) {
	for _, fault := range []string{"order", "source", "foreign_session", "owner", "canceled", "expired", "no_session", "uid", "ended", "partial", "extra", "invalid_row", "wrong_packet", "read_panic", "bad_receipt", "reply_panic", "canceled_after_commit"} {
		t.Run(fault, func(t *testing.T) {
			f, s, a, commands := acknowledgementFixture(t)
			q := commands[0]
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch fault {
			case "order":
				q.DeliveryOrder++
			case "source":
				q.Key.SourceGeneration = "foreign"
			case "foreign_session":
				q.Key.SessionGeneration++
			case "owner":
				q.Owner.OwnerGeneration++
			case "canceled":
				cancel()
			case "expired":
				f.now = f.now.Add(time.Minute)
			case "no_session":
				s.read = func(_ meta.MQTTRead, r *meta.MQTTReadResult) { r.Session = nil }
			case "uid":
				s.read = func(_ meta.MQTTRead, r *meta.MQTTReadResult) { r.Session.UID = "foreign" }
			case "ended":
				s.read = func(_ meta.MQTTRead, r *meta.MQTTReadResult) {
					r.Session.State = meta.MQTTSessionEnded
					r.Session.TerminationReason = meta.MQTTSessionQuota
				}
			case "partial":
				s.read = func(_ meta.MQTTRead, r *meta.MQTTReadResult) { r.Done = false }
			case "extra":
				s.read = func(_ meta.MQTTRead, r *meta.MQTTReadResult) { r.Bindings = []meta.MQTTSourceBinding{{}} }
			case "invalid_row":
				s.read = func(_ meta.MQTTRead, r *meta.MQTTReadResult) { r.Inflight[0].Publication.Position = 0 }
			case "wrong_packet":
				s.read = func(_ meta.MQTTRead, r *meta.MQTTReadResult) { r.Inflight[0].PacketID++ }
			case "read_panic":
				s.read = func(meta.MQTTRead, *meta.MQTTReadResult) { panic("secret") }
			case "bad_receipt":
				s.after = func(r *meta.MQTTWindowResult) error { r.DeliveryOrder++; return nil }
			case "reply_panic":
				s.after = func(*meta.MQTTWindowResult) error { panic("secret") }
			case "canceled_after_commit":
				s.after = func(*meta.MQTTWindowResult) error { cancel(); return nil }
			}
			got, e := a.Acknowledge(ctx, q)
			require.Error(t, e)
			require.Zero(t, got)
			switch fault {
			case "bad_receipt", "reply_panic", "canceled_after_commit":
				require.Equal(t, 1, s.writes)
				require.EqualValues(t, 1, f.row(t).OutboundInflight)
			default:
				require.Zero(t, s.writes)
				require.EqualValues(t, 2, f.row(t).OutboundInflight)
			}
			if fault == "read_panic" || fault == "reply_panic" {
				require.ErrorIs(t, e, app.ErrSubscriptionCallback)
				require.NotContains(t, e.Error(), "secret")
			}
		})
	}
}

// The native renewal and ACK both update the Session revision, while the sent
// exchange remains the same. A definite CAS rejection must not require reconnect.
func TestAcknowledgementsCompleteAcrossConcurrentRenewal(t *testing.T) {
	f, store, acknowledgements, commands := acknowledgementFixture(t)
	before := f.row(t)
	store.before = func(m meta.MQTTWindowMutation) {
		store.before = nil
		require.Equal(t, meta.MQTTWindowAck, m.Op)
		f.now = f.now.Add(time.Millisecond)
		_, err := f.service.Renew(context.Background(), commands[0].Owner)
		require.NoError(t, err)
	}
	result, err := acknowledgements.Acknowledge(context.Background(), commands[0])
	require.NoError(t, err)
	require.True(t, result.Changed)
	require.False(t, result.Absent)
	require.Equal(t, 2, store.writes)
	require.EqualValues(t, 1, f.row(t).OutboundInflight)
	require.Equal(t, before.Revision+2, f.row(t).Revision)
	cursor := readAcknowledgementCursor(t, f, commands[0].Key)
	require.Equal(t, cursor.StartAfter+1, cursor.CompletedThrough)
}

func TestAcknowledgementsRetainOriginalExchangeAcrossOtherAcknowledgements(t *testing.T) {
	for _, same := range []bool{false, true} {
		t.Run(map[bool]string{false: "neighbor", true: "already-absent"}[same], func(t *testing.T) {
			f, store, acknowledgements, commands := acknowledgementFixture(t)
			if same {
				store.before = func(meta.MQTTWindowMutation) {
					store.before = nil
					f.now = f.now.Add(time.Millisecond)
					_, err := f.service.Renew(context.Background(), commands[0].Owner)
					require.NoError(t, err)
				}
				store.after = func(r *meta.MQTTWindowResult) error {
					store.after = nil
					require.Equal(t, meta.MQTTWindowConflict, r.Status)
					result, err := acknowledgements.Acknowledge(context.Background(), commands[0])
					require.NoError(t, err)
					require.True(t, result.Changed)
					return nil
				}
			} else {
				store.before = func(meta.MQTTWindowMutation) {
					store.before = nil
					result, err := acknowledgements.Acknowledge(context.Background(), commands[1])
					require.NoError(t, err)
					require.True(t, result.Changed)
				}
			}
			result, err := acknowledgements.Acknowledge(context.Background(), commands[0])
			require.NoError(t, err)
			if same {
				require.Equal(t, app.AcknowledgementResult{Absent: true}, result)
				require.Equal(t, 2, store.writes, "absence must not trigger another proposal")
				require.EqualValues(t, 1, f.row(t).OutboundInflight)
			} else {
				require.Equal(t, app.AcknowledgementResult{Changed: true}, result)
				require.Equal(t, 3, store.writes)
				require.Zero(t, f.row(t).OutboundInflight)
				cursor := readAcknowledgementCursor(t, f, commands[0].Key)
				require.Equal(t, cursor.AccountedThrough, cursor.CompletedThrough)
			}
		})
	}
}

func TestAcknowledgementsRetryOnlyDefiniteRejectionUnderPinnedAuthority(t *testing.T) {
	faults := []string{"churn", "unchanged-revision", "regressed-revision", "owner", "uid", "lifetime", "publication", "topic", "order", "source", "partial", "cancel", "expiry", "clock", "unknown-conflict", "unknown-timeout"}
	for _, fault := range faults {
		t.Run(fault, func(t *testing.T) {
			f, store, acknowledgements, commands := acknowledgementFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			originalRevision := f.row(t).Revision
			store.before = func(meta.MQTTWindowMutation) {
				if fault != "churn" {
					store.before = nil
				}
				f.now = f.now.Add(time.Millisecond)
				_, err := f.service.Renew(context.Background(), commands[0].Owner)
				require.NoError(t, err)
				switch fault {
				case "cancel":
					cancel()
				case "expiry":
					f.now = f.now.Add(time.Minute)
				case "clock":
					f.now = f.now.Add(-2 * time.Millisecond)
				}
			}
			store.read = func(_ meta.MQTTRead, r *meta.MQTTReadResult) {
				if store.writes == 0 {
					return
				}
				switch fault {
				case "unchanged-revision":
					r.Session.Revision = originalRevision
				case "regressed-revision":
					r.Session.Revision = originalRevision - 1
				case "owner":
					r.Session.OwnerGeneration++
				case "uid":
					r.Session.UID = "other"
				case "lifetime":
					r.Session.Generation++
				case "publication":
					r.Inflight[0].Publication.MessageID++
				case "topic":
					r.Inflight[0].Topic += "/changed"
				case "order":
					r.Inflight[0].DeliveryOrder++
				case "source":
					r.Inflight[0].Key.SourceGeneration += "-changed"
				case "partial":
					r.Done = false
				}
			}
			if fault == "unknown-conflict" || fault == "unknown-timeout" {
				store.write = func(context.Context, meta.MQTTWindowMutation) (meta.MQTTWindowResult, error) {
					if fault == "unknown-conflict" {
						return meta.MQTTWindowResult{}, app.ErrConflict
					}
					return meta.MQTTWindowResult{}, context.DeadlineExceeded
				}
			}
			result, err := acknowledgements.Acknowledge(ctx, commands[0])
			require.Error(t, err)
			require.Zero(t, result)
			wantWrites := 1
			if fault == "churn" {
				wantWrites = 3
			}
			require.Equal(t, wantWrites, store.writes)
			require.EqualValues(t, 2, f.row(t).OutboundInflight, "failure must preserve both exchanges")
		})
	}
}
