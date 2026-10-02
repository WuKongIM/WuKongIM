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

type groupSourceStore struct {
	*subscriptionStore
	afterBinding func(meta.MQTTSourceBinding) error
	afterCursor  func(meta.MQTTDeliveryCursorMutation) error
}

func (s *groupSourceStore) CompareAndSwapMQTTSourceBinding(ctx context.Context, expected uint64, row meta.MQTTSourceBinding) (meta.MQTTSourceBindingResult, error) {
	b := s.db.NewBatch()
	defer b.Close()
	r, err := b.CompareAndSwapMQTTSourceBinding(7, expected, row)
	if err != nil {
		return meta.MQTTSourceBindingResult{}, err
	}
	if err = b.Commit(ctx); err != nil {
		return meta.MQTTSourceBindingResult{}, err
	}
	if s.afterBinding != nil {
		if err = s.afterBinding(row); err != nil {
			return meta.MQTTSourceBindingResult{}, err
		}
	}
	return *r, nil
}
func (s *groupSourceStore) MutateMQTTDeliveryCursor(ctx context.Context, m meta.MQTTDeliveryCursorMutation) (meta.MQTTDeliveryCursorResult, error) {
	b := s.db.NewBatch()
	defer b.Close()
	r, err := b.MutateMQTTDeliveryCursor(7, m)
	if err != nil {
		return meta.MQTTDeliveryCursorResult{}, err
	}
	if err = b.Commit(ctx); err != nil {
		return meta.MQTTDeliveryCursorResult{}, err
	}
	if s.afterCursor != nil {
		if err = s.afterCursor(m); err != nil {
			return meta.MQTTDeliveryCursorResult{}, err
		}
	}
	return *r, nil
}

type groupSourceProtector func(context.Context, app.SourceChannel) (app.ProtectedSource, error)

func (f groupSourceProtector) ProtectMQTTSource(c context.Context, id app.SourceChannel) (app.ProtectedSource, error) {
	return f(c, id)
}

type groupSourceFixture struct {
	*subscriptionsFixture
	store   *groupSourceStore
	sources *app.GroupSources
	opts    app.GroupSourceOptions
	intent  meta.MQTTSubscription
	tail    uint64
	protect func(int, app.SourceChannel) (app.ProtectedSource, error)
	calls   int
}

func setupGroupSource(t *testing.T) *groupSourceFixture {
	t.Helper()
	base := setupSubscriptions(t)
	base.project.establish = func(context.Context, app.SubscriptionProjectionRequest) (app.SubscriptionProjectionReceipt, error) {
		return app.SubscriptionProjectionReceipt{}, app.ErrEvidence
	}
	_, err := base.subscriptions.Subscribe(context.Background(), base.connection.Owner, subscriptionRequest())
	require.Error(t, err)
	f := &groupSourceFixture{subscriptionsFixture: base, store: &groupSourceStore{subscriptionStore: base.store}, intent: base.subscription(t, subscriptionRequest().Topic), tail: 10}
	f.opts = app.GroupSourceOptions{Store: f.store, Owners: f.owners, Authorization: f.options.Authorization, Now: func() time.Time { return f.now }, Sources: groupSourceProtector(func(ctx context.Context, id app.SourceChannel) (app.ProtectedSource, error) {
		f.calls++
		if f.protect != nil {
			return f.protect(f.calls, id)
		}
		return app.ProtectedSource{Channel: id, Generation: "protected-generation", CommittedThrough: f.tail}, nil
	})}
	f.sources, err = app.NewGroupSources(f.opts)
	require.NoError(t, err)
	return f
}
func (f *groupSourceFixture) prepare() (app.PreparedGroupSource, error) {
	return f.sources.Prepare(context.Background(), f.connection.Owner, f.intent.Topic)
}

func TestMQTTGroupSourceLostRepliesResumeStoredBoundary(t *testing.T) {
	for _, cut := range []string{"unknown", "boundary", "cursor", "active"} {
		t.Run(cut, func(t *testing.T) {
			f := setupGroupSource(t)
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
			require.Zero(t, f.owners.Snapshot().Operations)
			f.tail = 20
			got, err = f.prepare()
			require.NoError(t, err)
			want := uint64(10)
			if cut == "unknown" {
				want = 20
			}
			require.Equal(t, want, got.Binding.StartAfter)
			require.Equal(t, want, got.Cursor.StartAfter)
			require.Equal(t, meta.MQTTBindingActive, got.Binding.Stage)
			require.Equal(t, got.Cursor.Revision, got.Binding.ProgressRevision)
			require.Equal(t, meta.MQTTSubscriptionPreparing, f.subscription(t, f.intent.Topic).Stage, "source preparation cannot authorize SUBACK")
			f.tail = 30
			again, err := f.prepare()
			require.NoError(t, err)
			require.Equal(t, got, again)
		})
	}
}

func TestMQTTGroupSourceConfirmsAfterUnknownBindingAndPreservesRenewal(t *testing.T) {
	f := setupGroupSource(t)
	f.protect = func(n int, id app.SourceChannel) (app.ProtectedSource, error) {
		if n == 2 {
			q := meta.MQTTRead{Kind: meta.MQTTReadSourceCandidates, Owner: meta.MQTTBindingOwner{Kind: meta.MQTTBindingChannel, ID: "2:group", Generation: "protected-generation"}, Limit: 2}
			r, e := f.store.ReadMQTT(context.Background(), q)
			require.NoError(t, e)
			require.Len(t, r.Bindings, 1)
			require.False(t, r.Bindings[0].BoundaryKnown)
			_, e = f.service.Renew(context.Background(), f.connection.Owner)
			require.NoError(t, e)
		}
		return app.ProtectedSource{Channel: id, Generation: "protected-generation", CommittedThrough: 10}, nil
	}
	got, err := f.prepare()
	require.NoError(t, err)
	require.Equal(t, 2, f.calls)
	require.Equal(t, uint64(10), got.Cursor.StartAfter)
}

func TestMQTTGroupSourceRejectsStaleOrUnprovenPreparation(t *testing.T) {
	for _, mode := range []string{"denied", "permission-changed", "canceled", "source-error", "source-panic", "foreign-source", "generation-change", "source-regression", "removed", "cursor-corrupt", "clock", "takeover"} {
		t.Run(mode, func(t *testing.T) {
			f := setupGroupSource(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch mode {
			case "denied":
				f.denied = true
			case "permission-changed":
				f.store.afterCursor = func(meta.MQTTDeliveryCursorMutation) error { f.version++; return nil }
			case "canceled":
				f.store.afterCursor = func(meta.MQTTDeliveryCursorMutation) error { cancel(); return nil }
			case "source-error":
				f.protect = func(int, app.SourceChannel) (app.ProtectedSource, error) {
					return app.ProtectedSource{}, context.DeadlineExceeded
				}
			case "source-panic":
				f.protect = func(int, app.SourceChannel) (app.ProtectedSource, error) { panic("dependency") }
			case "foreign-source":
				f.protect = func(_ int, id app.SourceChannel) (app.ProtectedSource, error) {
					id.ID = "other"
					return app.ProtectedSource{Channel: id, Generation: "protected-generation", CommittedThrough: 10}, nil
				}
			case "generation-change":
				f.protect = func(n int, id app.SourceChannel) (app.ProtectedSource, error) {
					gen := "protected-generation"
					if n == 2 {
						gen = "successor"
					}
					return app.ProtectedSource{Channel: id, Generation: gen, CommittedThrough: 10}, nil
				}
			case "source-regression":
				_, err := f.prepare()
				require.NoError(t, err)
				f.tail = 9
			case "removed":
				_, err := f.prepare()
				require.NoError(t, err)
				_, err = f.subscriptions.Unsubscribe(context.Background(), f.connection.Owner, f.intent.Topic)
				require.NoError(t, err)
			case "cursor-corrupt":
				_, err := f.prepare()
				require.NoError(t, err)
				f.store.query = func(c context.Context, q meta.MQTTRead) (meta.MQTTReadResult, error) {
					r, e := f.store.sessionStore.ReadMQTT(c, q)
					if len(r.DeliveryCursors) > 0 {
						r.DeliveryCursors[0].StartAfter++
					}
					return r, e
				}
			case "clock":
				f.now = f.now.Round(0)
			case "takeover":
				f.store.afterCursor = func(meta.MQTTDeliveryCursorMutation) error {
					f.store.query = func(c context.Context, q meta.MQTTRead) (meta.MQTTReadResult, error) {
						r, e := f.store.sessionStore.ReadMQTT(c, q)
						if r.Session != nil {
							r.Session.ConnectionID++
						}
						return r, e
					}
					return nil
				}
			}
			got, err := f.sources.Prepare(ctx, f.connection.Owner, f.intent.Topic)
			require.Error(t, err)
			require.Zero(t, got)
			require.Zero(t, f.owners.Snapshot().Operations)
		})
	}
}

func TestMQTTGroupSourceClockCancellationStopsNextCommit(t *testing.T) {
	f := setupGroupSource(t)
	parent := &delayedCancellation{done: make(chan struct{})}
	cancelOnClock := false
	writes := 0
	f.store.afterBinding = func(meta.MQTTSourceBinding) error {
		writes++
		cancelOnClock = true
		return nil
	}
	f.opts.Now = func() time.Time {
		if cancelOnClock && parent.Err() == nil {
			parent.cancel()
		}
		return f.now
	}
	sources, err := app.NewGroupSources(f.opts)
	require.NoError(t, err)
	got, err := sources.Prepare(parent, f.connection.Owner, f.intent.Topic)
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, got)
	require.Equal(t, 1, writes, "cancellation during the authority clock check must precede the next durable commit")
	require.Zero(t, f.owners.Snapshot().Operations)
}
