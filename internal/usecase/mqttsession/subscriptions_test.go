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

type subscriptionStore struct {
	*sessionStore
	mutate    func(context.Context, meta.MQTTSubscriptionMutation) (meta.MQTTSessionCASResult, error)
	query     func(context.Context, meta.MQTTRead) (meta.MQTTReadResult, error)
	mutations int
}

func (s *subscriptionStore) ReadMQTT(ctx context.Context, q meta.MQTTRead) (meta.MQTTReadResult, error) {
	if s.query != nil {
		return s.query(ctx, q)
	}
	return s.sessionStore.ReadMQTT(ctx, q)
}
func (s *subscriptionStore) MutateMQTTSubscription(ctx context.Context, m meta.MQTTSubscriptionMutation) (meta.MQTTSessionCASResult, error) {
	s.mutations++
	if s.mutate != nil {
		return s.mutate(ctx, m)
	}
	return s.commitSubscription(ctx, m)
}
func (s *subscriptionStore) commitSubscription(ctx context.Context, m meta.MQTTSubscriptionMutation) (meta.MQTTSessionCASResult, error) {
	b := s.db.NewBatch()
	defer b.Close()
	r, err := b.MutateMQTTSubscription(7, m)
	if err != nil {
		return meta.MQTTSessionCASResult{}, err
	}
	err = b.Commit(ctx)
	if err != nil {
		return meta.MQTTSessionCASResult{}, err
	}
	return *r, nil
}

type subscriptionAuthorizer func(context.Context, string, app.SubscriptionRequest) (uint64, error)

func (f subscriptionAuthorizer) AuthorizeSubscription(ctx context.Context, uid string, r app.SubscriptionRequest) (uint64, error) {
	return f(ctx, uid, r)
}

type subscriptionProjection struct {
	establish func(context.Context, app.SubscriptionProjectionRequest) (app.SubscriptionProjectionReceipt, error)
	remove    func(context.Context, app.SubscriptionProjectionRequest) (app.SubscriptionProjectionReceipt, error)
}

func (p subscriptionProjection) Establish(ctx context.Context, r app.SubscriptionProjectionRequest) (app.SubscriptionProjectionReceipt, error) {
	return p.establish(ctx, r)
}
func (p subscriptionProjection) Remove(ctx context.Context, r app.SubscriptionProjectionRequest) (app.SubscriptionProjectionReceipt, error) {
	return p.remove(ctx, r)
}
func projectionReceipt(r app.SubscriptionProjectionRequest) app.SubscriptionProjectionReceipt {
	return app.SubscriptionProjectionReceipt{Namespace: r.Subscription.Namespace, ClientID: r.Subscription.ClientID, SessionGeneration: r.Subscription.SessionGeneration, Topic: r.Subscription.Topic, SubscriptionGeneration: r.Subscription.Generation, OperationID: r.Subscription.OperationID, IntentRevision: r.Subscription.Revision}
}

type subscriptionsFixture struct {
	*fixture
	store                *subscriptionStore
	subscriptions        *app.Subscriptions
	options              app.SubscriptionOptions
	connection           app.Connection
	project              *subscriptionProjection
	established, removed []app.SubscriptionProjectionRequest
	version              uint64
	denied               bool
}

func setupSubscriptions(t *testing.T) *subscriptionsFixture {
	t.Helper()
	f := &subscriptionsFixture{fixture: setup(t), version: 7}
	f.store = &subscriptionStore{sessionStore: f.fixture.store}
	var err error
	f.connection, err = f.service.Connect(context.Background(), command())
	require.NoError(t, err)
	f.project = &subscriptionProjection{establish: func(ctx context.Context, r app.SubscriptionProjectionRequest) (app.SubscriptionProjectionReceipt, error) {
		require.Equal(t, 1, f.owners.Snapshot().Operations)
		_, deadline := ctx.Deadline()
		require.True(t, deadline)
		stored := f.subscription(t, r.Subscription.Topic)
		require.Equal(t, meta.MQTTSubscriptionPreparing, stored.Stage)
		require.Equal(t, stored, r.Subscription)
		require.Equal(t, "alice", r.UID)
		f.established = append(f.established, r)
		return projectionReceipt(r), nil
	}, remove: func(ctx context.Context, r app.SubscriptionProjectionRequest) (app.SubscriptionProjectionReceipt, error) {
		require.Equal(t, meta.MQTTSubscriptionRemoving, f.subscription(t, r.Subscription.Topic).Stage)
		f.removed = append(f.removed, r)
		return projectionReceipt(r), nil
	}}
	f.options = app.SubscriptionOptions{Store: f.store, Owners: f.owners, Authorization: subscriptionAuthorizer(func(ctx context.Context, uid string, r app.SubscriptionRequest) (uint64, error) {
		require.Equal(t, "alice", uid)
		if f.denied {
			return 0, app.ErrSubscriptionDenied
		}
		return f.version, nil
	}), Projection: f.project, Now: func() time.Time { return f.now }, MaxSubscriptions: 2}
	f.subscriptions, err = app.NewSubscriptions(f.options)
	require.NoError(t, err)
	return f
}
func subscriptionRequest() app.SubscriptionRequest {
	return app.SubscriptionRequest{Topic: "wk/v1/groups/Z3JvdXA/messages", TargetKind: meta.MQTTSubscriptionGroup, TargetID: "group", RequestedQoS: 2, NoLocal: true, RetainAsPublished: true, RetainHandling: 2, SubscriptionIdentifier: 33}
}
func (f *subscriptionsFixture) subscription(t *testing.T, topic string) meta.MQTTSubscription {
	t.Helper()
	r, err := f.store.sessionStore.ReadMQTT(context.Background(), meta.MQTTRead{Kind: meta.MQTTReadSubscription, Namespace: "main", ClientID: "client", SessionGeneration: f.connection.Owner.SessionGeneration, Topic: topic})
	require.NoError(t, err)
	require.Len(t, r.Subscriptions, 1)
	return r.Subscriptions[0]
}
func TestSubscriptionsEstablishReplaceRemoveAndRecreate(t *testing.T) {
	f := setupSubscriptions(t)
	r := subscriptionRequest()
	first, err := f.subscriptions.Subscribe(context.Background(), f.connection.Owner, r)
	require.NoError(t, err)
	require.Equal(t, meta.MQTTSubscriptionActive, first.Stage)
	require.Equal(t, uint8(1), first.GrantedQoS)
	require.Equal(t, uint64(7), first.AuthorizationVersion)
	require.Len(t, f.established, 1)
	r.RequestedQoS = 0
	r.NoLocal = false
	r.SubscriptionIdentifier = 41
	second, err := f.subscriptions.Subscribe(context.Background(), f.connection.Owner, r)
	require.NoError(t, err)
	require.Equal(t, first.Generation, second.Generation)
	require.Equal(t, first.OperationID, second.OperationID)
	require.Zero(t, second.GrantedQoS)
	require.Equal(t, uint32(41), second.SubscriptionIdentifier)
	require.Len(t, f.established, 1)
	// Removal remains possible after permission has been revoked.
	f.denied = true
	existed, err := f.subscriptions.Unsubscribe(context.Background(), f.connection.Owner, r.Topic)
	require.NoError(t, err)
	require.True(t, existed)
	require.Equal(t, meta.MQTTSubscriptionRemoved, f.subscription(t, r.Topic).Stage)
	require.Len(t, f.removed, 1)
	existed, err = f.subscriptions.Unsubscribe(context.Background(), f.connection.Owner, r.Topic)
	require.NoError(t, err)
	require.False(t, existed)
	require.Len(t, f.removed, 1)
	f.denied = false
	third, err := f.subscriptions.Subscribe(context.Background(), f.connection.Owner, r)
	require.NoError(t, err)
	require.Greater(t, third.Generation, second.Generation)
	require.NotEqual(t, third.OperationID, second.OperationID)
	require.Zero(t, f.owners.Snapshot().Operations)
}
func TestSubscriptionsResumePendingProjectionWithStableIntent(t *testing.T) {
	for _, remove := range []bool{false, true} {
		t.Run(map[bool]string{false: "prepare", true: "remove"}[remove], func(t *testing.T) {
			f := setupSubscriptions(t)
			r := subscriptionRequest()
			unavailable := errors.New("projection unavailable")
			if remove {
				_, err := f.subscriptions.Subscribe(context.Background(), f.connection.Owner, r)
				require.NoError(t, err)
				f.project.remove = func(context.Context, app.SubscriptionProjectionRequest) (app.SubscriptionProjectionReceipt, error) {
					return app.SubscriptionProjectionReceipt{}, unavailable
				}
				_, err = f.subscriptions.Unsubscribe(context.Background(), f.connection.Owner, r.Topic)
				require.ErrorIs(t, err, unavailable)
			} else {
				f.project.establish = func(context.Context, app.SubscriptionProjectionRequest) (app.SubscriptionProjectionReceipt, error) {
					return app.SubscriptionProjectionReceipt{}, unavailable
				}
				_, err := f.subscriptions.Subscribe(context.Background(), f.connection.Owner, r)
				require.ErrorIs(t, err, unavailable)
			}
			pending := f.subscription(t, r.Topic)
			// Resume on another owner of the same durable lifetime, without resetting intent.
			next, err := f.service.Connect(context.Background(), command())
			require.NoError(t, err)
			require.True(t, next.SessionPresent)
			f.connection = next
			complete := func(_ context.Context, q app.SubscriptionProjectionRequest) (app.SubscriptionProjectionReceipt, error) {
				require.Equal(t, pending, q.Subscription)
				require.Equal(t, next.Owner, q.Owner)
				return projectionReceipt(q), nil
			}
			f.project.establish = complete
			f.project.remove = complete
			row, err := f.subscriptions.Reconcile(context.Background(), next.Owner, r.Topic)
			require.NoError(t, err)
			require.Equal(t, pending.Generation, row.Generation)
			require.Equal(t, pending.OperationID, row.OperationID)
			expected := meta.MQTTSubscriptionActive
			if remove {
				expected = meta.MQTTSubscriptionRemoved
			}
			require.Equal(t, expected, row.Stage)
		})
	}
}
func TestSubscriptionsRejectUnauthorizedChangedAndStaleOwners(t *testing.T) {
	for _, mode := range []string{"denied", "other_inbox", "owner", "changed_authority", "changed_target", "pending_options"} {
		t.Run(mode, func(t *testing.T) {
			f := setupSubscriptions(t)
			r := subscriptionRequest()
			owner := f.connection.Owner
			switch mode {
			case "denied":
				f.denied = true
			case "other_inbox":
				r.TargetKind = meta.MQTTSubscriptionUserInbox
				r.TargetID = "bob"
			case "owner":
				owner.OwnerGeneration++
			case "changed_authority", "changed_target":
				_, err := f.subscriptions.Subscribe(context.Background(), owner, r)
				require.NoError(t, err)
				if mode == "changed_authority" {
					f.version++
				} else {
					r.TargetID = "other"
				}
			case "pending_options":
				f.project.establish = func(context.Context, app.SubscriptionProjectionRequest) (app.SubscriptionProjectionReceipt, error) {
					return app.SubscriptionProjectionReceipt{}, errors.New("pending")
				}
				_, err := f.subscriptions.Subscribe(context.Background(), owner, r)
				require.Error(t, err)
				r.NoLocal = false
			}
			before := f.store.mutations
			_, err := f.subscriptions.Subscribe(context.Background(), owner, r)
			require.Error(t, err)
			require.Equal(t, before, f.store.mutations)
			require.Zero(t, f.owners.Snapshot().Operations)
		})
	}
}
func TestSubscriptionsProjectionRechecksIdentityPermissionAndLease(t *testing.T) {
	for _, mode := range []string{"wrong_receipt", "revoke", "expire", "cancel", "panic", "renew"} {
		t.Run(mode, func(t *testing.T) {
			f := setupSubscriptions(t)
			r := subscriptionRequest()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f.project.establish = func(_ context.Context, q app.SubscriptionProjectionRequest) (app.SubscriptionProjectionReceipt, error) {
				receipt := projectionReceipt(q)
				switch mode {
				case "wrong_receipt":
					receipt.ClientID = "foreign"
				case "revoke":
					f.denied = true
				case "expire":
					f.now = f.now.Add(11 * time.Second)
				case "cancel":
					cancel()
				case "panic":
					panic("secret")
				case "renew":
					_, err := f.service.Renew(context.Background(), f.connection.Owner)
					require.NoError(t, err)
				}
				return receipt, nil
			}
			row, err := f.subscriptions.Subscribe(ctx, f.connection.Owner, r)
			if mode == "renew" {
				require.NoError(t, err)
				require.Equal(t, meta.MQTTSubscriptionActive, row.Stage)
			} else {
				require.Error(t, err)
				require.NotContains(t, err.Error(), "secret")
				require.Equal(t, meta.MQTTSubscriptionPreparing, f.subscription(t, r.Topic).Stage)
			}
			require.Zero(t, f.owners.Snapshot().Operations)
		})
	}
}
func TestSubscriptionsLostCommitIsRecoveredWithoutNewGeneration(t *testing.T) {
	f := setupSubscriptions(t)
	r := subscriptionRequest()
	f.store.mutate = func(ctx context.Context, m meta.MQTTSubscriptionMutation) (meta.MQTTSessionCASResult, error) {
		_, err := f.store.commitSubscription(ctx, m)
		require.NoError(t, err)
		return meta.MQTTSessionCASResult{}, errors.New("lost response")
	}
	_, err := f.subscriptions.Subscribe(context.Background(), f.connection.Owner, r)
	require.Error(t, err)
	pending := f.subscription(t, r.Topic)
	require.Equal(t, meta.MQTTSubscriptionPreparing, pending.Stage)
	require.Empty(t, f.established)
	f.store.mutate = nil
	row, err := f.subscriptions.Subscribe(context.Background(), f.connection.Owner, r)
	require.NoError(t, err)
	require.Equal(t, pending.Generation, row.Generation)
	require.Equal(t, pending.OperationID, row.OperationID)
}
func TestSubscriptionsBoundQuotaAndConcurrentRevision(t *testing.T) {
	for _, mode := range []string{"limit", "revision", "cursor", "budget", "receipt"} {
		t.Run(mode, func(t *testing.T) {
			f := setupSubscriptions(t)
			r := subscriptionRequest()
			switch mode {
			case "limit":
				f.options.MaxSubscriptions = 1
				var err error
				f.subscriptions, err = app.NewSubscriptions(f.options)
				require.NoError(t, err)
				_, err = f.subscriptions.Subscribe(context.Background(), f.connection.Owner, r)
				require.NoError(t, err)
				r.Topic += "2"
				r.TargetID = "next"
			case "revision":
				f.store.mutate = func(ctx context.Context, m meta.MQTTSubscriptionMutation) (meta.MQTTSessionCASResult, error) {
					_, err := f.service.Renew(ctx, f.connection.Owner)
					require.NoError(t, err)
					return f.store.commitSubscription(ctx, m)
				}
			case "cursor", "budget":
				f.store.query = func(ctx context.Context, q meta.MQTTRead) (meta.MQTTReadResult, error) {
					out, err := f.store.sessionStore.ReadMQTT(ctx, q)
					if err != nil || q.Kind != meta.MQTTReadSubscriptions {
						return out, err
					}
					out.Done = false
					if mode == "budget" {
						out.Subscriptions = []meta.MQTTSubscription{{Namespace: "main", ClientID: "client", SessionGeneration: f.connection.Owner.SessionGeneration, Topic: q.After.Topic + "x", Generation: 1, Revision: 1, TargetKind: meta.MQTTSubscriptionGroup, TargetID: "old", OperationID: "old", GrantedQoS: 1, Stage: meta.MQTTSubscriptionRemoved, UpdatedAtMS: f.now.UnixMilli()}}
						out.After.Topic = out.Subscriptions[0].Topic
					}
					return out, nil
				}
			case "receipt":
				f.store.mutate = func(context.Context, meta.MQTTSubscriptionMutation) (meta.MQTTSessionCASResult, error) {
					return meta.MQTTSessionCASResult{Status: meta.MQTTSessionCASApplied, CurrentRevision: 999}, nil
				}
			}
			_, err := f.subscriptions.Subscribe(context.Background(), f.connection.Owner, r)
			require.Error(t, err)
			require.Zero(t, f.owners.Snapshot().Operations)
		})
	}
}

// Cancellation callbacks may be scheduled later than Err becomes visible. This
// deterministic parent holds that callback until the usecase releases its scope.
type delayedCancellation struct {
	done      chan struct{}
	cancelled bool
}

func (c *delayedCancellation) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c *delayedCancellation) Done() <-chan struct{}       { return c.done }
func (c *delayedCancellation) Err() error {
	if c.cancelled {
		return context.Canceled
	}
	return nil
}
func (*delayedCancellation) Value(any) any                { return nil }
func (*delayedCancellation) AfterFunc(func()) func() bool { return func() bool { return true } }
func (c *delayedCancellation) cancel()                    { c.cancelled = true; close(c.done) }

func TestSubscriptionsSynchronousParentCancellationFencesCompletion(t *testing.T) {
	f := setupSubscriptions(t)
	parent := &delayedCancellation{done: make(chan struct{})}
	f.project.establish = func(_ context.Context, q app.SubscriptionProjectionRequest) (app.SubscriptionProjectionReceipt, error) {
		parent.cancel()
		return projectionReceipt(q), nil
	}
	_, err := f.subscriptions.Subscribe(parent, f.connection.Owner, subscriptionRequest())
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, meta.MQTTSubscriptionPreparing, f.subscription(t, subscriptionRequest().Topic).Stage)
}
