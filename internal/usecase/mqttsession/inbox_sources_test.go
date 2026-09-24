package mqttsession_test

import (
	"context"
	"errors"
	"testing"
	"time"

	app "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/stretchr/testify/require"
)

type inboxSourceFixture struct {
	*subscriptionsFixture
	store         *groupSourceStore
	sources       *app.InboxSources
	qualification meta.MQTTSourceBinding
	intent        meta.MQTTSubscription
	channel       app.SourceChannel
	tail          uint64
	protectCalls  int
	protect       func(context.Context, int, app.SourceChannel) (app.ProtectedSource, error)
}

func setupInboxSource(t *testing.T) *inboxSourceFixture {
	t.Helper()
	base := setupSubscriptions(t)
	base.version = 0
	r := subscriptionRequest()
	r.Topic, r.TargetID, r.TargetKind = "wk/v1/users/YWxpY2U/inbox", "alice", meta.MQTTSubscriptionUserInbox
	base.project.establish = func(context.Context, app.SubscriptionProjectionRequest) (app.SubscriptionProjectionReceipt, error) {
		return app.SubscriptionProjectionReceipt{}, app.ErrReplayPending
	}
	_, err := base.subscriptions.Subscribe(context.Background(), base.connection.Owner, r)
	require.ErrorIs(t, err, app.ErrReplayPending)
	f := &inboxSourceFixture{subscriptionsFixture: base, store: &groupSourceStore{subscriptionStore: base.store}, intent: base.subscription(t, r.Topic), channel: app.SourceChannel{ID: channelid.EncodePersonChannel("alice", "bob"), Type: 1}, tail: 10}
	f.qualification = meta.MQTTSourceBinding{
		Key: meta.MQTTSourceBindingKey{Owner: meta.MQTTBindingOwner{Kind: meta.MQTTBindingUID, ID: "alice"}, Namespace: "main", ClientID: "client", SessionGeneration: f.intent.SessionGeneration, SubscriptionGeneration: f.intent.Generation},
		UID: "alice", Topic: f.intent.Topic, Revision: 1, IntentRevision: f.intent.Revision, OperationID: f.intent.OperationID, Stage: meta.MQTTBindingPreparing, RecoveryAtMS: f.now.UnixMilli(), UpdatedAtMS: f.now.UnixMilli(),
	}
	_, err = f.store.CompareAndSwapMQTTSourceBinding(context.Background(), 0, f.qualification)
	require.NoError(t, err)
	f.sources, err = app.NewInboxSources(app.InboxSourceOptions{Store: f.store, Now: func() time.Time { return f.now }, Sources: groupSourceProtector(func(ctx context.Context, ch app.SourceChannel) (app.ProtectedSource, error) {
		f.protectCalls++
		if f.protect != nil {
			return f.protect(ctx, f.protectCalls, ch)
		}
		return app.ProtectedSource{Channel: ch, Generation: "person-generation", CommittedThrough: f.tail}, nil
	})})
	require.NoError(t, err)
	return f
}

func (f *inboxSourceFixture) prepare() (app.PreparedInboxSource, error) {
	return f.sources.Prepare(context.Background(), f.qualification.Key, f.channel)
}

func TestMQTTInboxSourcePreparesOfflineIndependentSources(t *testing.T) {
	f := setupInboxSource(t)
	require.NoError(t, f.service.Disconnect(context.Background(), app.DisconnectCommand{Owner: f.connection.Owner, Normal: true}))
	require.Equal(t, meta.MQTTSessionOffline, f.row(t).State)
	first, err := f.prepare()
	require.NoError(t, err)
	require.True(t, first.Needed)
	require.Equal(t, meta.MQTTBindingActive, first.Binding.Stage)
	require.Equal(t, uint64(10), first.Cursor.StartAfter)
	require.Equal(t, first.Cursor.Revision, first.Binding.ProgressRevision)
	require.Equal(t, meta.MQTTSessionOffline, f.row(t).State, "source preparation cannot activate a socket")
	require.Zero(t, f.owners.Snapshot().Operations)
	f.channel.ID, f.tail = channelid.EncodePersonChannel("alice", "carol"), 20
	second, err := f.prepare()
	require.NoError(t, err)
	require.True(t, second.Needed)
	require.NotEqual(t, first.Cursor.Key, second.Cursor.Key)
	require.Equal(t, uint64(20), second.Cursor.StartAfter)
	f.channel.ID, f.tail = channelid.EncodePersonChannel("alice", "bob"), 30
	again, err := f.prepare()
	require.NoError(t, err)
	require.Equal(t, first, again)
	require.Equal(t, meta.MQTTSubscriptionPreparing, f.subscription(t, f.intent.Topic).Stage)
	page, err := f.store.ReadMQTT(context.Background(), meta.MQTTRead{Kind: meta.MQTTReadDeliveryCursors, Namespace: "main", ClientID: "client", SessionGeneration: f.intent.SessionGeneration, SubscriptionGeneration: f.intent.Generation, Limit: 64})
	require.NoError(t, err)
	require.Len(t, page.DeliveryCursors, 2)
}

func TestMQTTInboxSourceLostRepliesPreserveFixedBoundary(t *testing.T) {
	for _, cut := range []string{"unknown", "boundary", "cursor", "active"} {
		t.Run(cut, func(t *testing.T) {
			f := setupInboxSource(t)
			lost := errors.New("committed reply lost")
			failed := false
			f.store.afterBinding = func(r meta.MQTTSourceBinding) error {
				hit := cut == "unknown" && !r.BoundaryKnown || cut == "boundary" && r.BoundaryKnown && r.Stage == meta.MQTTBindingPreparing || cut == "active" && r.Stage == meta.MQTTBindingActive
				if hit && !failed {
					failed = true
					return lost
				}
				return nil
			}
			f.store.afterCursor = func(meta.MQTTDeliveryCursorMutation) error {
				if cut == "cursor" && !failed {
					failed = true
					return lost
				}
				return nil
			}
			got, err := f.prepare()
			require.ErrorIs(t, err, lost)
			require.Zero(t, got)
			require.True(t, failed)
			require.NoError(t, f.service.Disconnect(context.Background(), app.DisconnectCommand{Owner: f.connection.Owner, Normal: true}))
			f.tail = 20
			got, err = f.prepare()
			require.NoError(t, err)
			require.True(t, got.Needed)
			want := uint64(10)
			if cut == "unknown" {
				want = 20
			}
			require.Equal(t, want, got.Binding.StartAfter)
			require.Equal(t, want, got.Cursor.StartAfter)
			require.Equal(t, meta.MQTTBindingActive, got.Binding.Stage)
		})
	}
}

func TestMQTTInboxSourceUnknownBindingPrecedesCaptureAndSurvivesTakeover(t *testing.T) {
	f := setupInboxSource(t)
	f.protect = func(_ context.Context, n int, ch app.SourceChannel) (app.ProtectedSource, error) {
		if n == 2 {
			page, err := f.store.ReadMQTT(context.Background(), meta.MQTTRead{Kind: meta.MQTTReadSourceCandidates, Owner: meta.MQTTBindingOwner{Kind: meta.MQTTBindingChannel, ID: "1:" + ch.ID, Generation: "person-generation"}, Limit: 1})
			require.NoError(t, err)
			require.Len(t, page.Bindings, 1)
			require.False(t, page.Bindings[0].BoundaryKnown)
			c, err := f.service.Connect(context.Background(), command())
			require.NoError(t, err)
			require.Equal(t, f.connection.Owner.SessionGeneration, c.Owner.SessionGeneration)
			require.Greater(t, c.Owner.OwnerGeneration, f.connection.Owner.OwnerGeneration)
		}
		return app.ProtectedSource{Channel: ch, Generation: "person-generation", CommittedThrough: 10}, nil
	}
	got, err := f.prepare()
	require.NoError(t, err)
	require.True(t, got.Needed)
	require.Equal(t, 2, f.protectCalls)
	require.Equal(t, uint64(10), got.Cursor.StartAfter)
}

func TestMQTTInboxSourceClosedIntentDoesNotAdmitOrReleaseDebt(t *testing.T) {
	for _, mode := range []string{"qualification", "unsubscribe", "clean_start", "during_prepare"} {
		t.Run(mode, func(t *testing.T) {
			f := setupInboxSource(t)
			closeIntent := func() {
				_, err := f.subscriptions.Unsubscribe(context.Background(), f.connection.Owner, f.intent.Topic)
				require.NoError(t, err)
			}
			switch mode {
			case "qualification":
				r := f.qualification
				r.Revision++
				r.IntentRevision++
				r.Stage = meta.MQTTBindingRemoving
				_, err := f.store.CompareAndSwapMQTTSourceBinding(context.Background(), 1, r)
				require.NoError(t, err)
			case "unsubscribe":
				closeIntent()
			case "clean_start":
				c := command()
				c.CleanStart = true
				_, err := f.service.Connect(context.Background(), c)
				require.NoError(t, err)
			case "during_prepare":
				f.store.afterBinding = func(r meta.MQTTSourceBinding) error {
					if !r.BoundaryKnown {
						closeIntent()
					}
					return nil
				}
			}
			got, err := f.prepare()
			require.NoError(t, err)
			require.Zero(t, got)
			if mode == "during_prepare" {
				q := meta.MQTTRead{Kind: meta.MQTTReadSourceCandidates, Owner: meta.MQTTBindingOwner{Kind: meta.MQTTBindingChannel, ID: "1:" + f.channel.ID, Generation: "person-generation"}, Limit: 1}
				r, err := f.store.ReadMQTT(context.Background(), q)
				require.NoError(t, err)
				require.Len(t, r.Bindings, 1, "cleanup remains durable")
				require.False(t, r.Bindings[0].BoundaryKnown)
			} else {
				require.Zero(t, f.protectCalls)
			}
		})
	}
}

func TestMQTTInboxSourceRejectsInvalidOrUncertainEvidence(t *testing.T) {
	for _, mode := range []string{"foreign_person", "group", "noncanonical", "missing_qualification", "mixed_directory", "source_error", "source_panic", "foreign_source", "changed_generation", "regressed_source", "canceled", "clock"} {
		t.Run(mode, func(t *testing.T) {
			f := setupInboxSource(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch mode {
			case "foreign_person":
				f.channel.ID = channelid.EncodePersonChannel("bob", "carol")
			case "group":
				f.channel.Type = 2
			case "noncanonical":
				left, right, err := channelid.DecodePersonChannel(f.channel.ID)
				require.NoError(t, err)
				f.channel.ID = right + "@" + left
			case "missing_qualification":
				f.qualification.Key.ClientID = "absent"
			case "mixed_directory":
				f.fixture.store.read = func(r meta.MQTTReadResult) meta.MQTTReadResult {
					r.Directory = []meta.ChannelKey{{ChannelID: "x", ChannelType: 1}}
					return r
				}
			case "source_error":
				f.protect = func(context.Context, int, app.SourceChannel) (app.ProtectedSource, error) {
					return app.ProtectedSource{}, errors.New("unavailable")
				}
			case "source_panic":
				f.protect = func(context.Context, int, app.SourceChannel) (app.ProtectedSource, error) {
					panic("private credential")
				}
			case "foreign_source":
				f.protect = func(_ context.Context, _ int, ch app.SourceChannel) (app.ProtectedSource, error) {
					ch.ID = "foreign"
					return app.ProtectedSource{Channel: ch, Generation: "person-generation", CommittedThrough: 10}, nil
				}
			case "changed_generation":
				f.protect = func(_ context.Context, n int, ch app.SourceChannel) (app.ProtectedSource, error) {
					gen := "person-generation"
					if n == 2 {
						gen = "other"
					}
					return app.ProtectedSource{Channel: ch, Generation: gen, CommittedThrough: 10}, nil
				}
			case "regressed_source":
				_, err := f.prepare()
				require.NoError(t, err)
				f.tail = 9
			case "canceled":
				f.store.afterCursor = func(meta.MQTTDeliveryCursorMutation) error { cancel(); return nil }
			case "clock":
				_, err := f.prepare()
				require.NoError(t, err)
				f.now = time.Time{}
			}
			got, err := f.sources.Prepare(ctx, f.qualification.Key, f.channel)
			require.Error(t, err)
			require.Zero(t, got)
			require.NotContains(t, err.Error(), "private credential")
		})
	}
}

func TestMQTTInboxSourceRejectsCursorOlderThanCommittedInit(t *testing.T) {
	f := setupInboxSource(t)
	f.store.afterCursor = func(meta.MQTTDeliveryCursorMutation) error {
		f.fixture.store.read = func(r meta.MQTTReadResult) meta.MQTTReadResult {
			if len(r.DeliveryCursors) == 1 {
				r.DeliveryCursors[0].Revision--
			}
			return r
		}
		return nil
	}
	r, err := f.prepare()
	require.ErrorIs(t, err, app.ErrEvidence, "a post-commit read cannot precede its exact receipt")
	require.Zero(t, r)
}
