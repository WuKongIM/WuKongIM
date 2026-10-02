package mqttsession_test

import (
	"context"
	"errors"
	"testing"
	"time"

	app "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func offlineGroupProjection(t *testing.T, f *groupSourceFixture, replay groupReplayConfirmation) *app.GroupProjection {
	t.Helper()
	p, err := app.NewGroupProjection(app.GroupProjectionOptions{Store: &drainStore{progressStore: &progressStore{groupSourceStore: f.store}}, Owners: f.owners, Authorization: f.opts.Authorization, Sources: f.opts.Sources, Now: func() time.Time { return f.now }, Replay: replay})
	require.NoError(t, err)
	return p
}

func TestOfflineGroupEstablishmentPreservesEveryDurableBoundary(t *testing.T) {
	for _, cut := range []string{"before_registration", "unknown", "boundary", "cursor", "active", "replay"} {
		t.Run(cut, func(t *testing.T) {
			f := setupGroupSource(t)
			lost := errors.New("preparation reply lost")
			f.store.afterBinding = func(b meta.MQTTSourceBinding) error {
				if cut == "unknown" && !b.BoundaryKnown || cut == "boundary" && b.BoundaryKnown && b.Stage == meta.MQTTBindingPreparing || cut == "active" && b.Stage == meta.MQTTBindingActive {
					return lost
				}
				return nil
			}
			f.store.afterCursor = func(meta.MQTTDeliveryCursorMutation) error {
				if cut == "cursor" {
					return lost
				}
				return nil
			}
			var confirmed uint64
			pending := true
			p := offlineGroupProjection(t, f, func(_ context.Context, _ meta.MQTTBindingOwner, start uint64) error {
				if pending && cut == "replay" {
					return app.ErrReplayPending
				}
				confirmed = start
				return nil
			})
			r := app.SubscriptionProjectionRequest{Owner: f.connection.Owner, UID: "alice", Subscription: f.intent}
			if cut != "before_registration" {
				got, err := p.Establish(context.Background(), r)
				require.Error(t, err)
				require.Zero(t, got)
			}
			require.NoError(t, f.service.Disconnect(context.Background(), app.DisconnectCommand{Owner: f.connection.Owner, Normal: true}))
			before := f.row(t)
			f.store.afterBinding, f.store.afterCursor, pending = nil, nil, false
			f.tail = 20
			got, err := p.EstablishOffline(context.Background(), r)
			require.NoError(t, err)
			require.Equal(t, projectionReceipt(r), got)
			want := uint64(10)
			if cut == "before_registration" || cut == "unknown" {
				want = 20
			}
			require.Equal(t, want, confirmed)
			page, err := f.store.ReadMQTT(context.Background(), meta.MQTTRead{Kind: meta.MQTTReadDeliveryCursors, Namespace: f.intent.Namespace, ClientID: f.intent.ClientID, SessionGeneration: f.intent.SessionGeneration, SubscriptionGeneration: f.intent.Generation, Limit: 2})
			require.NoError(t, err)
			require.Len(t, page.DeliveryCursors, 1)
			require.Equal(t, want, page.DeliveryCursors[0].StartAfter)
			require.Equal(t, f.intent, f.subscription(t, f.intent.Topic), "projection alone cannot activate intent")
			require.Equal(t, meta.MQTTSessionOffline, f.row(t).State)
			require.Equal(t, before.OfflineExpiresAtMS, f.row(t).OfflineExpiresAtMS)
			require.Zero(t, f.owners.Snapshot().Operations)
			f.tail = 30
			again, err := p.EstablishOffline(context.Background(), r)
			require.NoError(t, err)
			require.Equal(t, got, again)
			require.Equal(t, want, confirmed)
			_, err = p.Establish(context.Background(), r)
			require.Error(t, err, "foreground still needs live Owner admission")
		})
	}
}

func TestOfflineInboxEstablishmentResumesQualificationAndBoundedPages(t *testing.T) {
	for _, cut := range []string{"before_qualification", "qualification", "progress", "complete", "replay"} {
		t.Run(cut, func(t *testing.T) {
			f := setupInboxEstablishment(t)
			f.directory(t, meta.ChannelKey{ChannelID: f.channel.ID, ChannelType: 1})
			lost := errors.New("qualification reply lost")
			f.store.afterBinding = func(b meta.MQTTSourceBinding) error {
				if b.Key.Owner.Kind == meta.MQTTBindingUID && (cut == "qualification" && b.Revision == 1 || cut == "progress" && b.DiscoveryAfterChannelID != "" && !b.DiscoveryDone || cut == "complete" && b.DiscoveryDone) {
					return lost
				}
				return nil
			}
			var boundary uint64
			pending := true
			f.replay = func(_ context.Context, _ meta.MQTTBindingOwner, start uint64) error {
				boundary = start
				if pending && cut == "replay" {
					return app.ErrReplayPending
				}
				return nil
			}
			p, err := app.NewInboxEstablishment(f.options)
			require.NoError(t, err)
			if cut != "before_qualification" {
				got, err := p.Establish(context.Background(), f.request())
				require.Error(t, err)
				require.Zero(t, got)
			}
			require.NoError(t, f.service.Disconnect(context.Background(), app.DisconnectCommand{Owner: f.connection.Owner, Normal: true}))
			f.store.afterBinding, pending, f.tail = nil, false, 20
			got, err := p.EstablishOffline(context.Background(), f.request())
			require.NoError(t, err)
			require.Equal(t, projectionReceipt(f.request()), got)
			want := uint64(10)
			if cut == "before_qualification" || cut == "qualification" {
				want = 20
			}
			require.Equal(t, want, boundary)
			q := f.readQualification(t)
			require.True(t, q.DiscoveryDone)
			require.Equal(t, meta.MQTTBindingActive, q.Stage)
			require.Equal(t, f.intent, f.subscription(t, f.intent.Topic))
			again, err := p.EstablishOffline(context.Background(), f.request())
			require.NoError(t, err)
			require.Equal(t, got, again)
			require.Zero(t, f.owners.Snapshot().Operations)
			_, err = p.Establish(context.Background(), f.request())
			require.Error(t, err)
		})
	}
	t.Run("one_page_per_turn", func(t *testing.T) {
		f := setupInboxEstablishment(t)
		f.directory(t, meta.ChannelKey{ChannelID: "g", ChannelType: 2}, meta.ChannelKey{ChannelID: f.channel.ID, ChannelType: 1})
		require.NoError(t, f.service.Disconnect(context.Background(), app.DisconnectCommand{Owner: f.connection.Owner, Normal: true}))
		p, err := app.NewInboxEstablishment(f.options)
		require.NoError(t, err)
		got, err := p.EstablishOffline(context.Background(), f.request())
		require.ErrorIs(t, err, app.ErrReplayPending)
		require.Zero(t, got)
		require.Equal(t, "g", f.readQualification(t).DiscoveryAfterChannelID)
		require.Zero(t, f.protectCalls)
		got, err = p.EstablishOffline(context.Background(), f.request())
		require.NoError(t, err)
		require.Equal(t, projectionReceipt(f.request()), got)
		require.Equal(t, 1, f.replays)
	})
}

func TestOfflineInboxEstablishmentDoesNotRecaptureOwnerInsideSourcePreparation(t *testing.T) {
	f := setupInboxEstablishment(t)
	f.directory(t, meta.ChannelKey{ChannelID: f.channel.ID, ChannelType: 1})
	r := f.request()
	require.NoError(t, f.service.Disconnect(context.Background(), app.DisconnectCommand{Owner: r.Owner, Normal: true}))
	f.protect = func(ctx context.Context, call int, ch app.SourceChannel) (app.ProtectedSource, error) {
		require.Equal(t, 1, call)
		resumed, err := f.service.Connect(ctx, command())
		require.NoError(t, err)
		require.NotEqual(t, r.Owner, resumed.Owner)
		return app.ProtectedSource{Channel: ch, Generation: "person-generation", CommittedThrough: 10}, nil
	}
	p, err := app.NewInboxEstablishment(f.options)
	require.NoError(t, err)
	got, err := p.EstablishOffline(context.Background(), r)
	require.Error(t, err)
	require.Zero(t, got)
	require.Zero(t, f.replays)
	page, err := f.store.ReadMQTT(context.Background(), meta.MQTTRead{Kind: meta.MQTTReadDeliveryCursors, Namespace: f.intent.Namespace, ClientID: f.intent.ClientID, SessionGeneration: f.intent.SessionGeneration, SubscriptionGeneration: f.intent.Generation, Limit: 2})
	require.NoError(t, err)
	require.Empty(t, page.DeliveryCursors, "nested source preparation must not write under a replacement Owner")
	bindings, err := f.store.ReadMQTT(context.Background(), meta.MQTTRead{Kind: meta.MQTTReadSourceCandidates, Owner: meta.MQTTBindingOwner{Kind: meta.MQTTBindingChannel, ID: "1:" + f.channel.ID, Generation: "person-generation"}, Limit: 2})
	require.NoError(t, err)
	require.Empty(t, bindings.Bindings)
}

// Exercise the same authority failures through both concrete projection paths.
func TestOfflineEstablishmentRejectsChangedAuthorityAndLateEffects(t *testing.T) {
	for _, target := range []string{"group", "inbox"} {
		for _, mode := range []string{"active", "ended", "expired", "clock", "owner", "uid", "intent", "denied", "version", "missing", "mixed", "cancel_before", "owner_after_replay", "child_after_replay", "denied_after_replay", "version_after_replay", "cancel_after_replay", "panic", "replay_pending"} {
			t.Run(target+"/"+mode, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				var base *subscriptionsFixture
				var request app.SubscriptionProjectionRequest
				var run func(context.Context, app.SubscriptionProjectionRequest) (app.SubscriptionProjectionReceipt, error)
				replayed := false
				replay := groupReplayConfirmation(func(context.Context, meta.MQTTBindingOwner, uint64) error {
					replayed = true
					switch mode {
					case "denied_after_replay":
						base.denied = true
					case "version_after_replay":
						base.version++
					case "cancel_after_replay":
						cancel()
					case "panic":
						panic("sensitive callback")
					case "replay_pending":
						return app.ErrReplayPending
					}
					return nil
				})
				if target == "group" {
					f := setupGroupSource(t)
					base = f.subscriptionsFixture
					request = app.SubscriptionProjectionRequest{Owner: f.connection.Owner, UID: "alice", Subscription: f.intent}
					run = offlineGroupProjection(t, f, replay).EstablishOffline
				} else {
					f := setupInboxEstablishment(t)
					base = f.subscriptionsFixture
					f.directory(t, meta.ChannelKey{ChannelID: f.channel.ID, ChannelType: 1})
					f.replay = replay
					p, err := app.NewInboxEstablishment(f.options)
					require.NoError(t, err)
					request, run = f.request(), p.EstablishOffline
				}
				if mode != "active" {
					require.NoError(t, base.service.Disconnect(ctx, app.DisconnectCommand{Owner: base.connection.Owner, Normal: true}))
				}
				before := base.row(t)
				switch mode {
				case "expired":
					base.now = base.now.Add(time.Duration(before.OfflineExpiresAtMS-base.now.UnixMilli()) * time.Millisecond)
				case "clock":
					base.now = base.now.Add(-time.Hour)
				case "owner":
					request.Owner.OwnerGeneration++
				case "uid":
					request.UID = "foreign"
				case "intent":
					request.Subscription.NoLocal = !request.Subscription.NoLocal
				case "denied":
					base.denied = true
				case "version":
					base.version++
				case "cancel_before":
					cancel()
				}
				base.store.query = func(ctx context.Context, q meta.MQTTRead) (meta.MQTTReadResult, error) {
					page, err := base.store.sessionStore.ReadMQTT(ctx, q)
					if q.Kind == meta.MQTTReadSubscription {
						switch mode {
						case "ended":
							page.Session.State, page.Session.OfflineExpiresAtMS, page.Session.TerminationReason = meta.MQTTSessionEnded, 0, meta.MQTTSessionExpired
						case "missing":
							page.Session, page.Subscriptions = nil, nil
						case "mixed":
							page.Runtime = &meta.MQTTRuntimeView{}
						case "owner_after_replay":
							if replayed {
								page.Session.OwnerGeneration++
							}
						case "child_after_replay":
							if replayed {
								page.Subscriptions[0].NoLocal = !page.Subscriptions[0].NoLocal
							}
						}
					}
					return page, err
				}
				got, err := run(ctx, request)
				require.Error(t, err)
				require.Zero(t, got)
				require.NotContains(t, err.Error(), "sensitive")
				require.Zero(t, base.owners.Snapshot().Operations)
				base.store.query = nil
				require.Equal(t, meta.MQTTSubscriptionPreparing, base.subscription(t, request.Subscription.Topic).Stage)
			})
		}
	}
}
