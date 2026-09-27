package mqttsession_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	app "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/stretchr/testify/require"
)

type inboxEstablishSource func(context.Context, meta.MQTTSourceBindingKey, app.SourceChannel) (app.PreparedInboxSource, error)

func (f inboxEstablishSource) Prepare(ctx context.Context, k meta.MQTTSourceBindingKey, ch app.SourceChannel) (app.PreparedInboxSource, error) {
	return f(ctx, k, ch)
}

type inboxEstablishStore struct {
	*groupSourceStore
	write func(context.Context, uint64, meta.MQTTSourceBinding) (meta.MQTTSourceBindingResult, error)
}

func (s *inboxEstablishStore) CompareAndSwapMQTTSourceBinding(ctx context.Context, rev uint64, row meta.MQTTSourceBinding) (meta.MQTTSourceBindingResult, error) {
	if s.write != nil {
		return s.write(ctx, rev, row)
	}
	return s.groupSourceStore.CompareAndSwapMQTTSourceBinding(ctx, rev, row)
}

type inboxEstablishmentFixture struct {
	*inboxSourceFixture
	metadata *inboxEstablishStore
	options  app.InboxEstablishmentOptions
	replays  int
	replay   func(context.Context, meta.MQTTBindingOwner, uint64) error
}

func setupInboxEstablishment(t *testing.T) *inboxEstablishmentFixture {
	t.Helper()
	base := setupSubscriptions(t)
	base.version = 0
	request := subscriptionRequest()
	request.Topic, request.TargetID, request.TargetKind = "wk/v1/users/YWxpY2U/inbox", "alice", meta.MQTTSubscriptionUserInbox
	base.project.establish = func(context.Context, app.SubscriptionProjectionRequest) (app.SubscriptionProjectionReceipt, error) {
		return app.SubscriptionProjectionReceipt{}, app.ErrReplayPending
	}
	_, err := base.subscriptions.Subscribe(context.Background(), base.connection.Owner, request)
	require.ErrorIs(t, err, app.ErrReplayPending)
	f := &inboxEstablishmentFixture{inboxSourceFixture: &inboxSourceFixture{subscriptionsFixture: base, store: &groupSourceStore{subscriptionStore: base.store}, intent: base.subscription(t, request.Topic), channel: app.SourceChannel{ID: channelid.EncodePersonChannel("alice", "bob"), Type: 1}, tail: 10}}
	f.qualification.Key = meta.MQTTSourceBindingKey{Owner: meta.MQTTBindingOwner{Kind: meta.MQTTBindingUID, ID: "alice"}, Namespace: f.intent.Namespace, ClientID: f.intent.ClientID, SessionGeneration: f.intent.SessionGeneration, SubscriptionGeneration: f.intent.Generation}
	f.metadata = &inboxEstablishStore{groupSourceStore: f.store}
	f.sources, err = app.NewInboxSources(app.InboxSourceOptions{Store: f.metadata, Now: func() time.Time { return f.now }, Sources: groupSourceProtector(func(ctx context.Context, ch app.SourceChannel) (app.ProtectedSource, error) {
		f.protectCalls++
		q := f.readQualification(t)
		require.Equal(t, meta.MQTTBindingPreparing, q.Stage, "qualification precedes initial source preparation")
		if f.protect != nil {
			return f.protect(ctx, f.protectCalls, ch)
		}
		return app.ProtectedSource{Channel: ch, Generation: "person-generation", CommittedThrough: f.tail}, nil
	})})
	require.NoError(t, err)
	f.options = app.InboxEstablishmentOptions{Store: f.metadata, Owners: f.owners, Authorization: base.options.Authorization, Sources: f.sources, PageSize: 1, Now: func() time.Time { return f.now }, Replay: groupReplayConfirmation(func(ctx context.Context, owner meta.MQTTBindingOwner, start uint64) error {
		f.replays++
		if f.replay != nil {
			return f.replay(ctx, owner, start)
		}
		return nil
	})}
	return f
}

func (f *inboxEstablishmentFixture) readQualification(t *testing.T) meta.MQTTSourceBinding {
	t.Helper()
	q, found, err := f.store.db.HashSlot(7).GetMQTTSourceBinding(context.Background(), f.qualification.Key)
	require.NoError(t, err)
	require.True(t, found)
	return q
}

func (f *inboxEstablishmentFixture) directory(t *testing.T, keys ...meta.ChannelKey) {
	t.Helper()
	for _, key := range keys {
		require.NoError(t, f.store.db.HashSlot(7).UpsertUserChannelMembership(context.Background(), meta.UserChannelMembership{UID: "alice", ChannelID: key.ChannelID, ChannelType: key.ChannelType, Tombstone: true, TombstoneAt: 1}))
	}
}

func (f *inboxEstablishmentFixture) request() app.SubscriptionProjectionRequest {
	return app.SubscriptionProjectionRequest{Owner: f.connection.Owner, UID: "alice", Subscription: f.intent}
}

func TestMQTTInboxEstablishmentPagesThenActivatesWithoutRescanning(t *testing.T) {
	f := setupInboxEstablishment(t)
	keys := []meta.ChannelKey{{ChannelID: "g", ChannelType: 2}, {ChannelID: f.channel.ID, ChannelType: 1}, {ChannelID: strings.Repeat("z", 4096), ChannelType: 255}}
	f.directory(t, keys...)
	projection, err := app.NewInboxEstablishment(f.options)
	require.NoError(t, err)
	f.project.establish = projection.Establish
	for i, key := range keys {
		active, err := f.subscriptions.Reconcile(context.Background(), f.connection.Owner, f.intent.Topic)
		if i < len(keys)-1 {
			require.ErrorIs(t, err, app.ErrReplayPending)
			require.Zero(t, active)
		} else {
			require.NoError(t, err)
			require.Equal(t, meta.MQTTSubscriptionActive, active.Stage)
		}
		q := f.readQualification(t)
		require.Equal(t, key.ChannelID, q.DiscoveryAfterChannelID)
		require.Equal(t, uint8(key.ChannelType), q.DiscoveryAfterChannelType)
		require.Equal(t, i == len(keys)-1, q.DiscoveryDone)
	}
	require.Equal(t, 2, f.protectCalls)
	require.Equal(t, 1, f.replays)
	f.store.query = func(ctx context.Context, q meta.MQTTRead) (meta.MQTTReadResult, error) {
		if q.Kind == meta.MQTTReadInboxDirectory {
			t.Fatal("completed discovery rescanned on option replacement")
		}
		return f.store.sessionStore.ReadMQTT(ctx, q)
	}
	r := subscriptionRequest()
	r.Topic, r.TargetID, r.TargetKind = f.intent.Topic, "alice", meta.MQTTSubscriptionUserInbox
	r.NoLocal = true
	active, err := f.subscriptions.Subscribe(context.Background(), f.connection.Owner, r)
	require.NoError(t, err)
	require.True(t, active.NoLocal)
	require.Equal(t, f.intent.Generation, active.Generation)
	require.Equal(t, 1, f.replays)
	require.Equal(t, 2, f.protectCalls)
	require.Equal(t, f.intent.Revision, f.readQualification(t).IntentRevision, "active option replacement retains established qualification")
	require.Zero(t, f.owners.Snapshot().Operations)
}

func TestMQTTInboxEstablishmentLostRepliesAndReplayResume(t *testing.T) {
	for _, cut := range []string{"qualification", "progress", "complete", "replay"} {
		t.Run(cut, func(t *testing.T) {
			f := setupInboxEstablishment(t)
			f.directory(t, meta.ChannelKey{ChannelID: f.channel.ID, ChannelType: 1})
			lost := errors.New("reply lost")
			failed := false
			f.store.afterBinding = func(q meta.MQTTSourceBinding) error {
				if q.Key.Owner.Kind != meta.MQTTBindingUID {
					return nil
				}
				hit := cut == "qualification" && q.Revision == 1 || cut == "progress" && q.DiscoveryAfterChannelID != "" && !q.DiscoveryDone || cut == "complete" && q.DiscoveryDone
				if hit && !failed {
					failed = true
					return lost
				}
				return nil
			}
			f.replay = func(context.Context, meta.MQTTBindingOwner, uint64) error {
				if cut == "replay" && !failed {
					failed = true
					return app.ErrReplayPending
				}
				return nil
			}
			projection, err := app.NewInboxEstablishment(f.options)
			require.NoError(t, err)
			got, err := projection.Establish(context.Background(), f.request())
			require.Error(t, err)
			require.Zero(t, got)
			require.True(t, failed)
			if cut != "replay" {
				require.ErrorIs(t, err, lost)
			} else {
				require.Empty(t, f.readQualification(t).DiscoveryAfterChannelID)
			}
			f.connection, err = f.service.Connect(context.Background(), command())
			require.NoError(t, err)
			f.tail = 20
			got, err = projection.Establish(context.Background(), f.request())
			require.NoError(t, err)
			require.Equal(t, projectionReceipt(f.request()), got)
			q := f.readQualification(t)
			require.Equal(t, meta.MQTTBindingActive, q.Stage)
			require.True(t, q.DiscoveryDone)
			cursors, err := f.store.ReadMQTT(context.Background(), meta.MQTTRead{Kind: meta.MQTTReadDeliveryCursors, Namespace: f.intent.Namespace, ClientID: f.intent.ClientID, SessionGeneration: f.intent.SessionGeneration, SubscriptionGeneration: f.intent.Generation, Limit: 64})
			require.NoError(t, err)
			require.Len(t, cursors.DeliveryCursors, 1)
			want := uint64(10)
			if cut == "qualification" {
				want = 20
			}
			require.Equal(t, want, cursors.DeliveryCursors[0].StartAfter)
			require.Zero(t, f.owners.Snapshot().Operations)
		})
	}
}

func TestMQTTInboxEstablishmentRejectsInvalidEvidenceAndLateEffects(t *testing.T) {
	for _, mode := range []string{"uid", "intent", "kind", "stage", "denied", "version", "foreign-person", "malformed-person", "mixed", "duplicate", "cursor", "short", "reversed", "closed-source", "bad-source", "replay-error", "replay-panic", "cancel", "lost-owner", "revoked", "clock", "cas-conflict", "cas-receipt", "final-qualification", "mixed-intent", "mixed-qualification", "overflow"} {
		t.Run(mode, func(t *testing.T) {
			f := setupInboxEstablishment(t)
			f.directory(t, meta.ChannelKey{ChannelID: f.channel.ID, ChannelType: 1})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			r := f.request()
			switch mode {
			case "uid":
				r.UID = "foreign"
			case "intent":
				r.Subscription.Revision++
			case "kind":
				r.Subscription.TargetKind = meta.MQTTSubscriptionGroup
			case "stage":
				r.Subscription.Stage = meta.MQTTSubscriptionActive
			case "denied":
				f.denied = true
			case "version":
				f.version = 1
			}
			f.options.Sources = inboxEstablishSource(func(ctx context.Context, key meta.MQTTSourceBindingKey, ch app.SourceChannel) (app.PreparedInboxSource, error) {
				prepared, err := f.sources.Prepare(ctx, key, ch)
				if mode == "closed-source" {
					return app.PreparedInboxSource{}, nil
				}
				if mode == "bad-source" {
					prepared.Cursor.StartAfter++
				}
				return prepared, err
			})
			f.replay = func(context.Context, meta.MQTTBindingOwner, uint64) error {
				switch mode {
				case "replay-error":
					return errors.New("replay unavailable")
				case "replay-panic":
					panic("sensitive")
				case "cancel":
					cancel()
				case "lost-owner":
					f.owners.Fence(f.connection.Owner)
				case "revoked":
					f.denied = true
				case "clock":
					f.now = f.now.Add(-time.Hour)
				}
				return nil
			}
			f.metadata.write = func(ctx context.Context, rev uint64, row meta.MQTTSourceBinding) (meta.MQTTSourceBindingResult, error) {
				if row.Key.Owner.Kind == meta.MQTTBindingUID && rev > 0 {
					if mode == "cas-conflict" {
						return meta.MQTTSourceBindingResult{Status: meta.MQTTSessionCASConflict}, nil
					}
					if mode == "cas-receipt" {
						return meta.MQTTSourceBindingResult{Status: meta.MQTTSessionCASApplied, CurrentRevision: row.Revision + 1}, nil
					}
				}
				return f.store.CompareAndSwapMQTTSourceBinding(ctx, rev, row)
			}
			f.store.query = func(ctx context.Context, q meta.MQTTRead) (meta.MQTTReadResult, error) {
				result, err := f.store.sessionStore.ReadMQTT(ctx, q)
				if q.Kind == meta.MQTTReadInboxDirectory {
					switch mode {
					case "foreign-person":
						result.Directory[0].ChannelID = channelid.EncodePersonChannel("carol", "bob")
						result.After.Directory = result.Directory[0]
					case "malformed-person":
						result.Directory[0].ChannelID = "malformed"
						result.After.Directory = result.Directory[0]
					case "mixed":
						result.Bindings = []meta.MQTTSourceBinding{f.readQualification(t)}
					case "duplicate":
						result.Directory = append(result.Directory, result.Directory[0])
					case "cursor":
						result.After.Directory.ChannelType++
					case "short":
						result.Directory = nil
						result.Done = false
						result.After = q.After
					case "reversed":
						result.Directory = []meta.ChannelKey{{ChannelID: "zz", ChannelType: 2}, {ChannelID: "a", ChannelType: 2}}
						result.After.Directory = result.Directory[1]
					}
				}
				if mode == "mixed-intent" && q.Kind == meta.MQTTReadSubscription {
					result.Directory = []meta.ChannelKey{{ChannelID: "foreign", ChannelType: 2}}
				}
				if mode == "mixed-qualification" && q.Kind == meta.MQTTReadSourceBinding {
					result.Session = &meta.MQTTSession{}
				}
				if mode == "overflow" && q.Kind == meta.MQTTReadSourceBinding && len(result.Bindings) == 1 && result.Bindings[0].Key.Owner.Kind == meta.MQTTBindingUID {
					result.Bindings[0].Revision = ^uint64(0)
				}
				if mode == "final-qualification" && q.Kind == meta.MQTTReadSourceBinding && len(result.Bindings) == 1 && result.Bindings[0].Key.Owner.Kind == meta.MQTTBindingUID && result.Bindings[0].DiscoveryDone {
					result.Bindings[0].IntentRevision++
				}
				return result, err
			}
			if mode == "duplicate" || mode == "reversed" {
				f.options.PageSize = 2
			}
			projection, err := app.NewInboxEstablishment(f.options)
			require.NoError(t, err)
			got, err := projection.Establish(ctx, r)
			require.Error(t, err)
			require.Zero(t, got)
			require.NotContains(t, err.Error(), "sensitive")
			require.Equal(t, meta.MQTTSubscriptionPreparing, f.subscription(t, f.intent.Topic).Stage)
			require.Zero(t, f.owners.Snapshot().Operations)
		})
	}
}

func TestMQTTInboxEstablishmentBoundsAndEmptyCompletion(t *testing.T) {
	f := setupInboxEstablishment(t)
	for _, change := range []func(*app.InboxEstablishmentOptions){func(o *app.InboxEstablishmentOptions) { o.Store = nil }, func(o *app.InboxEstablishmentOptions) { o.Owners = nil }, func(o *app.InboxEstablishmentOptions) { o.Authorization = nil }, func(o *app.InboxEstablishmentOptions) { o.Sources = nil }, func(o *app.InboxEstablishmentOptions) { o.Replay = nil }, func(o *app.InboxEstablishmentOptions) { o.PageSize = -1 }, func(o *app.InboxEstablishmentOptions) { o.PageSize = 65 }, func(o *app.InboxEstablishmentOptions) { o.Timeout = -1 }, func(o *app.InboxEstablishmentOptions) { o.Timeout = 2 * time.Minute }, func(o *app.InboxEstablishmentOptions) { o.Now = func() time.Time { return time.Time{} } }} {
		o := f.options
		change(&o)
		_, err := app.NewInboxEstablishment(o)
		require.Error(t, err)
	}
	f.options.PageSize = 0
	p, err := app.NewInboxEstablishment(f.options)
	require.NoError(t, err)
	_, err = p.Establish(nil, f.request())
	require.Error(t, err)
	got, err := p.Establish(context.Background(), f.request())
	require.NoError(t, err)
	require.Equal(t, projectionReceipt(f.request()), got)
	require.True(t, f.readQualification(t).DiscoveryDone)
	require.Zero(t, f.protectCalls)
	require.Zero(t, f.replays)
}
