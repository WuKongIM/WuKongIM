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

type progressStore struct {
	*groupSourceStore
	reads, writes int
	read          func(meta.MQTTRead, *meta.MQTTReadResult) error
	write         func(context.Context, uint64, meta.MQTTSourceBinding) (meta.MQTTSourceBindingResult, error)
}

func (s *progressStore) ReadMQTT(ctx context.Context, q meta.MQTTRead) (meta.MQTTReadResult, error) {
	s.reads++
	r, e := s.groupSourceStore.ReadMQTT(ctx, q)
	if e == nil && s.read != nil {
		e = s.read(q, &r)
	}
	return r, e
}
func (s *progressStore) CompareAndSwapMQTTSourceBinding(ctx context.Context, v uint64, b meta.MQTTSourceBinding) (meta.MQTTSourceBindingResult, error) {
	s.writes++
	if s.write != nil {
		return s.write(ctx, v, b)
	}
	return s.groupSourceStore.CompareAndSwapMQTTSourceBinding(ctx, v, b)
}

func progressFixture(t *testing.T) (*groupSourceFixture, *progressStore, *app.SourceProgress, app.PreparedGroupSource) {
	t.Helper()
	f := setupGroupSource(t)
	p, err := f.prepare()
	require.NoError(t, err)
	s := &progressStore{groupSourceStore: f.store}
	c, err := app.NewSourceProgress(app.SourceProgressOptions{Store: s, Now: func() time.Time { return f.now }})
	require.NoError(t, err)
	return f, s, c, p
}

func advanceProgressWindow(t *testing.T, f *groupSourceFixture, p app.PreparedGroupSource) func(int) {
	t.Helper()
	ctx := context.Background()
	// This fixture supplies the projection receipt; product SUBACK wiring is
	// deliberately outside the metadata window/progress contract being tested.
	f.project.establish = func(_ context.Context, r app.SubscriptionProjectionRequest) (app.SubscriptionProjectionReceipt, error) {
		return projectionReceipt(r), nil
	}
	_, err := f.subscriptions.Subscribe(ctx, f.connection.Owner, subscriptionRequest())
	require.NoError(t, err)
	o := f.connection.Owner
	m := meta.MQTTDeliveryCursorMutation{Key: p.Cursor.Key, ExpectedRevision: f.row(t).Revision, OwnerGeneration: o.OwnerGeneration, OwnerNodeID: o.NodeID, OwnerBootID: o.BootID, ConnectionID: o.ConnectionID, Op: meta.MQTTCursorAccount, Topic: p.Cursor.Topic, AuthorizationVersion: p.Cursor.AuthorizationVersion, Through: p.Cursor.StartAfter + 2, AddedMessages: 2, AddedBytes: 2, UpdatedAtMS: f.now.UnixMilli()}
	r, err := f.store.MutateMQTTDeliveryCursor(ctx, m)
	require.NoError(t, err)
	require.Equal(t, meta.MQTTSessionCASApplied, r.Status)
	mutate := func(m meta.MQTTWindowMutation) meta.MQTTWindowResult {
		m.Key, m.ExpectedRevision = p.Cursor.Key, f.row(t).Revision
		m.OwnerGeneration, m.OwnerNodeID, m.OwnerBootID, m.ConnectionID = o.OwnerGeneration, o.NodeID, o.BootID, o.ConnectionID
		m.UpdatedAtMS = f.now.UnixMilli()
		b := f.store.db.NewBatch()
		defer b.Close()
		r, e := b.MutateMQTTWindow(7, m)
		require.NoError(t, e)
		require.NoError(t, b.Commit(ctx))
		require.Equal(t, meta.MQTTWindowApplied, r.Status)
		return *r
	}
	var pending []meta.MQTTWindowResult
	for i := uint64(1); i <= 2; i++ {
		pending = append(pending, mutate(meta.MQTTWindowMutation{Op: meta.MQTTWindowAdmit, Publication: meta.MQTTInflightPublication{Position: p.Cursor.StartAfter + i, MessageID: 100 + i, MessageSeq: p.Cursor.StartAfter + i, ContentVersion: 1, ContentHash: strings.Repeat("a", 64), Bytes: 1, SubscriptionIdentifier: subscriptionRequest().SubscriptionIdentifier}}))
	}
	return func(i int) {
		mutate(meta.MQTTWindowMutation{Op: meta.MQTTWindowAck, PacketID: pending[i].PacketID, DeliveryOrder: pending[i].DeliveryOrder})
	}
}

func TestSourceProgressCoalescesACKGapsAndRecoversLostReply(t *testing.T) {
	f, s, c, p := progressFixture(t)
	ack := advanceProgressWindow(t, f, p)
	for _, step := range []func(){func() {}, func() { ack(1) }} {
		step()
		got, err := c.Reconcile(context.Background(), p.Binding.Key)
		require.NoError(t, err)
		require.False(t, got.Changed)
		require.Equal(t, p.Binding, got.Binding)
	}
	require.Zero(t, s.writes)
	ack(0)
	lost := errors.New("committed reply lost")
	f.store.afterBinding = func(meta.MQTTSourceBinding) error { return lost }
	got, err := c.Reconcile(context.Background(), p.Binding.Key)
	require.ErrorIs(t, err, lost)
	require.Zero(t, got)
	f.store.afterBinding = nil
	got, err = c.Reconcile(context.Background(), p.Binding.Key)
	require.NoError(t, err)
	require.False(t, got.Changed)
	require.Equal(t, p.Cursor.StartAfter+2, got.Binding.CompletedThrough)
	require.Equal(t, p.Binding.Revision+1, got.Binding.Revision)
	require.Greater(t, got.Binding.ProgressRevision, p.Binding.ProgressRevision)
	require.Equal(t, p.Binding.ProtectionRevision, got.Binding.ProtectionRevision)
	require.Equal(t, 1, s.writes)
}

func TestSourceProgressRetainsRemovalAndDistinguishesOfflineFromEnded(t *testing.T) {
	for _, mode := range []string{"offline", "ended", "new_lifetime", "sealed_end"} {
		t.Run(mode, func(t *testing.T) {
			f, s, c, p := progressFixture(t)
			if mode == "sealed_end" {
				ack := advanceProgressWindow(t, f, p)
				ack(1)
				ack(0)
				b := p.Binding
				b.Revision++
				b.IntentRevision++
				b.Stage = meta.MQTTBindingRemoving
				b.EndKnown = true
				b.EndThrough = b.StartAfter + 1
				_, err := s.groupSourceStore.CompareAndSwapMQTTSourceBinding(context.Background(), p.Binding.Revision, b)
				require.NoError(t, err)
				p.Binding = b
			} else if mode == "new_lifetime" {
				cmd := command()
				cmd.CleanStart = true
				_, err := f.service.Connect(context.Background(), cmd)
				require.NoError(t, err)
			} else {
				var expiry *uint32
				if mode == "ended" {
					zero := uint32(0)
					expiry = &zero
				}
				require.NoError(t, f.service.Disconnect(context.Background(), app.DisconnectCommand{Owner: f.connection.Owner, Normal: true, SessionExpirySec: expiry}))
			}
			got, err := c.Reconcile(context.Background(), p.Binding.Key)
			require.NoError(t, err)
			if mode == "offline" {
				require.False(t, got.Changed)
				require.Equal(t, p.Binding, got.Binding)
				require.Zero(t, s.writes)
				return
			}
			require.True(t, got.Changed)
			require.True(t, got.NeedsRemoval)
			require.Equal(t, meta.MQTTBindingRemoving, got.Binding.Stage)
			require.Equal(t, p.Binding.ProtectionRevision, got.Binding.ProtectionRevision)
			if mode == "sealed_end" {
				require.Equal(t, p.Binding.EndThrough, got.Binding.CompletedThrough)
				require.Zero(t, got.Binding.ReleaseReason)
			} else {
				require.Equal(t, meta.MQTTBindingSessionEnded, got.Binding.ReleaseReason)
				require.Equal(t, p.Binding.CompletedThrough, got.Binding.CompletedThrough)
			}
			again, err := c.Reconcile(context.Background(), p.Binding.Key)
			require.NoError(t, err)
			require.False(t, again.Changed)
			require.True(t, again.NeedsRemoval)
			require.Equal(t, got.Binding, again.Binding)
			require.Equal(t, 1, s.writes)
		})
	}
}

func TestSourceProgressRejectsUnprovenViewsAndWriteResults(t *testing.T) {
	for _, mode := range []string{"binding_missing", "binding_key", "binding_extra", "session_missing", "session_uid", "session_future", "cursor_missing", "cursor_key", "cursor_topic", "cursor_auth", "cursor_boundary", "cursor_revision", "extra", "incomplete", "read_error", "cancel_read", "cas_conflict", "cas_error", "cas_revision", "cancel_write", "clock"} {
		t.Run(mode, func(t *testing.T) {
			f, s, c, p := progressFixture(t)
			ack := advanceProgressWindow(t, f, p)
			ack(1)
			ack(0)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s.read = func(q meta.MQTTRead, r *meta.MQTTReadResult) error {
				if q.Kind == meta.MQTTReadSourceBinding {
					switch mode {
					case "binding_missing":
						r.Bindings = nil
					case "binding_key":
						r.Bindings[0].Key.ClientID = "foreign"
					case "binding_extra":
						r.Bindings = append(r.Bindings, r.Bindings[0])
					}
					return nil
				}
				switch mode {
				case "session_missing":
					r.Session = nil
				case "session_uid":
					r.Session.UID = "foreign"
				case "session_future":
					r.Session.Generation = 0
				case "cursor_missing":
					r.DeliveryCursors = nil
				case "cursor_key":
					r.DeliveryCursors[0].Key.SourceGeneration = "foreign"
				case "cursor_topic":
					r.DeliveryCursors[0].Topic = "other"
				case "cursor_auth":
					r.DeliveryCursors[0].AuthorizationVersion++
				case "cursor_boundary":
					r.DeliveryCursors[0].StartAfter++
				case "cursor_revision":
					r.DeliveryCursors[0].Revision = r.Session.Revision + 1
				case "extra":
					r.Bindings = []meta.MQTTSourceBinding{p.Binding}
				case "incomplete":
					r.Done = false
				case "read_error":
					return context.DeadlineExceeded
				case "cancel_read":
					cancel()
				}
				return nil
			}
			if strings.HasPrefix(mode, "cas_") || mode == "cancel_write" {
				s.write = func(_ context.Context, v uint64, _ meta.MQTTSourceBinding) (meta.MQTTSourceBindingResult, error) {
					switch mode {
					case "cas_conflict":
						return meta.MQTTSourceBindingResult{Status: meta.MQTTSessionCASConflict}, nil
					case "cas_error":
						return meta.MQTTSourceBindingResult{}, context.DeadlineExceeded
					case "cancel_write":
						cancel()
					}
					return meta.MQTTSourceBindingResult{Status: meta.MQTTSessionCASApplied, CurrentRevision: v}, nil
				}
			}
			if mode == "clock" {
				f.now = time.UnixMilli(1)
			}
			got, err := c.Reconcile(ctx, p.Binding.Key)
			require.Error(t, err)
			require.Zero(t, got)
			if !strings.HasPrefix(mode, "cas_") && mode != "cancel_write" {
				require.Zero(t, s.writes)
			}
			stored, err := f.store.ReadMQTT(context.Background(), meta.MQTTRead{Kind: meta.MQTTReadSourceBinding, BindingKey: p.Binding.Key})
			require.NoError(t, err)
			require.Equal(t, p.Binding, stored.Bindings[0])
		})
	}
}

func TestSourceProgressBoundsUnknownResponsibilitiesAndInput(t *testing.T) {
	for _, mode := range []string{"unknown", "removed", "overflow", "cancel_before"} {
		t.Run(mode, func(t *testing.T) {
			f, s, c, p := progressFixture(t)
			if mode == "overflow" {
				ack := advanceProgressWindow(t, f, p)
				ack(0)
				ack(1)
			}
			s.read = func(q meta.MQTTRead, r *meta.MQTTReadResult) error {
				if q.Kind == meta.MQTTReadSourceBinding {
					b := &r.Bindings[0]
					switch mode {
					case "unknown":
						b.Stage = meta.MQTTBindingPreparing
						b.BoundaryKnown = false
						b.StartAfter = 0
						b.CompletedThrough = 0
						b.ProgressRevision = 0
						b.ProtectionRevision = 0
					case "removed":
						b.Stage = meta.MQTTBindingRemoved
						b.RecoveryAtMS = 0
						b.ReleaseReason = meta.MQTTBindingSessionEnded
					case "overflow":
						b.Revision = ^uint64(0)
					}
				} else if mode == "unknown" {
					r.DeliveryCursors = nil
				}
				return nil
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancel_before" {
				cancel()
			}
			result, err := c.Reconcile(ctx, p.Binding.Key)
			if mode == "overflow" || mode == "cancel_before" {
				require.Error(t, err)
				require.Zero(t, result)
			} else {
				require.NoError(t, err)
				require.False(t, result.Changed)
				require.False(t, result.NeedsRemoval)
			}
			require.Zero(t, s.writes)
			if mode == "removed" {
				require.Equal(t, 1, s.reads)
			}
			if mode == "cancel_before" {
				require.Zero(t, s.reads)
			}
		})
	}
	_, s, c, p := progressFixture(t)
	for _, o := range []app.SourceProgressOptions{{}, {Store: s, Timeout: -time.Second}, {Store: s, Timeout: 2 * time.Minute}} {
		_, err := app.NewSourceProgress(o)
		require.Error(t, err)
	}
	_, err := c.Reconcile(nil, p.Binding.Key)
	require.Error(t, err)
	p.Binding.Key.Owner.Kind = 3 // Unsupported owner kind still fails before storage.
	p.Binding.Key.Owner.Generation = ""
	_, err = c.Reconcile(context.Background(), p.Binding.Key)
	require.Error(t, err)
	require.Zero(t, s.reads)
}
