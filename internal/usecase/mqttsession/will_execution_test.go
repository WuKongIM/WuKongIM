package mqttsession_test

import (
	"context"
	"errors"
	"testing"
	"time"

	app "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/publication"
	"github.com/stretchr/testify/require"
)

type willExecutionStore struct {
	*sessionStore
	after func(meta.MQTTWill, meta.MQTTWillResult) (meta.MQTTWillResult, error)
}

func (s *willExecutionStore) CompareAndSwapMQTTWill(ctx context.Context, revision uint64, row meta.MQTTWill) (meta.MQTTWillResult, error) {
	b := s.db.NewBatch()
	defer b.Close()
	r, err := b.CompareAndSwapMQTTWill(7, revision, row)
	if err != nil {
		return meta.MQTTWillResult{}, err
	}
	if err = b.Commit(ctx); err != nil {
		return meta.MQTTWillResult{}, err
	}
	if s.after != nil {
		return s.after(row, *r)
	}
	return *r, nil
}

type willPublications struct {
	lookup  func(context.Context, app.WillPublication) (app.WillPublicationReceipt, bool, error)
	publish func(context.Context, app.WillPublication) error
}

func (p willPublications) LookupWillPublication(ctx context.Context, q app.WillPublication) (app.WillPublicationReceipt, bool, error) {
	return p.lookup(ctx, q)
}
func (p willPublications) PublishWill(ctx context.Context, q app.WillPublication) error {
	return p.publish(ctx, q)
}

type willExecutionFixture struct {
	*fixture
	store                          *willExecutionStore
	key                            meta.MQTTWillKey
	opts                           app.WillExecutionOptions
	receipt                        app.WillPublicationReceipt
	published, authorized, looked  int
	lookupErr, publishErr, authErr error
	found                          bool
}

func newWillExecutionFixture(t *testing.T) *willExecutionFixture {
	t.Helper()
	f := &willExecutionFixture{fixture: setup(t)}
	f.store = &willExecutionStore{sessionStore: f.fixture.store}
	cmd := command()
	cmd.Will = will(t)
	cmd.Will.DelaySeconds = 0
	c, err := f.service.Connect(context.Background(), cmd)
	require.NoError(t, err)
	require.NoError(t, f.service.Disconnect(context.Background(), app.DisconnectCommand{Owner: c.Owner}))
	f.key = readDeadlineWill(t, f.fixture, c).Wills[0].Key
	require.Equal(t, meta.MQTTWillReady, f.read(t).Stage)
	f.receipt = app.WillPublicationReceipt{MessageID: 991, MessageSeq: 73, PublishedAtMS: f.now.UnixMilli()}
	f.opts = app.WillExecutionOptions{Store: f.store, NodeID: 9, BootID: "executor", Now: func() time.Time { return f.now }, LeaseDuration: 10 * time.Second, TurnTimeout: time.Second}
	f.opts.Authorizer = willAuthorizer(func(context.Context, string, app.WillTarget) error { f.authorized++; return f.authErr })
	f.opts.Publications = willPublications{
		lookup: func(_ context.Context, q app.WillPublication) (app.WillPublicationReceipt, bool, error) {
			f.looked++
			md, err := publication.Decode(q.PublicationMetadata)
			require.NoError(t, err)
			require.Equal(t, f.read(t).IdempotencyKey, md.ServerWillKey)
			if !f.found || f.lookupErr != nil {
				return app.WillPublicationReceipt{}, false, f.lookupErr
			}
			return f.receipt, true, nil
		},
		publish: func(ctx context.Context, q app.WillPublication) error {
			f.published++
			_, bounded := ctx.Deadline()
			require.True(t, bounded)
			require.Equal(t, "alice", q.UID)
			require.Equal(t, "group", q.Target.TargetID)
			require.Equal(t, "will-no", q.ClientMsgNo)
			require.Equal(t, []byte("goodbye"), q.Payload)
			md, err := publication.Decode(q.PublicationMetadata)
			require.NoError(t, err)
			require.Equal(t, publication.SourceWill, md.Source)
			require.Equal(t, f.read(t).IdempotencyKey, md.ServerWillKey)
			require.Zero(t, md.AcceptedAtMS)
			return f.publishErr
		},
	}
	return f
}

func (f *willExecutionFixture) read(t *testing.T) meta.MQTTWill {
	t.Helper()
	r, err := f.store.ReadMQTT(context.Background(), meta.MQTTRead{Kind: meta.MQTTReadWill, WillKey: f.key})
	require.NoError(t, err)
	require.Len(t, r.Wills, 1)
	return r.Wills[0]
}
func (f *willExecutionFixture) executor(t *testing.T) *app.WillExecutor {
	t.Helper()
	e, err := app.NewWillExecutor(f.opts)
	require.NoError(t, err)
	return e
}

func TestWillExecutionPublishesDetachedTaskAndRecoversTerminalReceipt(t *testing.T) {
	f := newWillExecutionFixture(t)
	// A different current lifetime cannot hide the detached obligation.
	cmd := command()
	cmd.CleanStart = true
	_, err := f.service.Connect(context.Background(), cmd)
	require.NoError(t, err)
	f.found = true
	e := f.executor(t)
	r, err := e.Execute(context.Background(), f.key)
	require.NoError(t, err)
	require.False(t, r.Pending)
	require.Equal(t, meta.MQTTWillPublished, r.Stage)
	require.Equal(t, f.receipt, r.Receipt)
	require.Equal(t, 1, f.published)
	require.Equal(t, 1, f.authorized)
	f.authErr = app.ErrWillDenied
	again, err := e.Execute(context.Background(), f.key)
	require.NoError(t, err)
	require.Equal(t, r, again)
	require.Equal(t, 1, f.published)
	require.Equal(t, 1, f.authorized)
}

func TestWillExecutionUnknownPublishRecoveredBeforePermission(t *testing.T) {
	f := newWillExecutionFixture(t)
	f.publishErr = errors.New("reply lost after append")
	_, err := f.executor(t).Execute(context.Background(), f.key)
	require.Error(t, err)
	require.Equal(t, meta.MQTTWillExecuting, f.read(t).Stage)
	f.now = f.now.Add(11 * time.Second)
	f.found, f.authErr = true, app.ErrWillDenied
	f.opts.BootID = "successor"
	r, err := f.executor(t).Execute(context.Background(), f.key)
	require.NoError(t, err)
	require.Equal(t, meta.MQTTWillPublished, r.Stage)
	require.Equal(t, f.receipt, r.Receipt)
	require.Equal(t, uint64(2), f.read(t).ExecutionGeneration)
	require.Equal(t, 1, f.published)
	require.Equal(t, 1, f.authorized, "recovery cannot replace a committed receipt with present denial")
}

func TestWillExecutionAbsenceNeverRedispatchesOrRejectsUnknownWork(t *testing.T) {
	f := newWillExecutionFixture(t)
	_, err := f.executor(t).Execute(context.Background(), f.key)
	require.ErrorIs(t, err, app.ErrWillPending)
	f.now = f.now.Add(11 * time.Second)
	f.authErr = app.ErrWillDenied
	r, err := f.executor(t).Execute(context.Background(), f.key)
	require.ErrorIs(t, err, app.ErrWillPending)
	require.True(t, r.Pending)
	require.Equal(t, meta.MQTTWillExecuting, f.read(t).Stage)
	require.Equal(t, 1, f.published)
	require.Equal(t, 1, f.authorized)
}

func TestWillExecutionFreshDenialAndUnavailableAuthority(t *testing.T) {
	for _, denial := range []bool{false, true} {
		t.Run(map[bool]string{true: "denied", false: "unavailable"}[denial], func(t *testing.T) {
			f := newWillExecutionFixture(t)
			f.authErr = errors.New("authority unavailable")
			if denial {
				f.authErr = app.ErrWillDenied
			}
			r, err := f.executor(t).Execute(context.Background(), f.key)
			if denial {
				require.NoError(t, err)
				require.Equal(t, meta.MQTTWillRejected, r.Stage)
				require.Equal(t, meta.MQTTWillPermissionRevoked, f.read(t).RejectReason)
			} else {
				require.ErrorIs(t, err, f.authErr)
				require.Equal(t, meta.MQTTWillExecuting, f.read(t).Stage)
			}
			require.Zero(t, f.published)
		})
	}
}

func TestWillExecutionClaimRepliesDoNotInventDispatchOwnership(t *testing.T) {
	for _, mode := range []string{"lost", "unchanged", "conflict", "wrong revision"} {
		t.Run(mode, func(t *testing.T) {
			f := newWillExecutionFixture(t)
			f.store.after = func(_ meta.MQTTWill, r meta.MQTTWillResult) (meta.MQTTWillResult, error) {
				switch mode {
				case "lost":
					return meta.MQTTWillResult{}, errors.New("claim reply lost")
				case "unchanged":
					r.Status = meta.MQTTSessionCASUnchanged
				case "conflict":
					r.Status = meta.MQTTSessionCASConflict
				case "wrong revision":
					r.CurrentRevision++
				}
				return r, nil
			}
			_, err := f.executor(t).Execute(context.Background(), f.key)
			require.Error(t, err)
			require.Zero(t, f.published)
			require.Zero(t, f.authorized)
		})
	}
}

func TestWillExecutionLostFinalReplyRecoversWithoutSending(t *testing.T) {
	f := newWillExecutionFixture(t)
	f.found = true
	f.store.after = func(w meta.MQTTWill, r meta.MQTTWillResult) (meta.MQTTWillResult, error) {
		if w.Stage == meta.MQTTWillPublished {
			return meta.MQTTWillResult{}, errors.New("final reply lost")
		}
		return r, nil
	}
	e := f.executor(t)
	_, err := e.Execute(context.Background(), f.key)
	require.Error(t, err)
	f.authErr = app.ErrWillDenied
	r, err := e.Execute(context.Background(), f.key)
	require.NoError(t, err)
	require.Equal(t, f.receipt, r.Receipt)
	require.Equal(t, 1, f.published)
}

func TestWillExecutionStopsEffectsAfterCancellationOrLease(t *testing.T) {
	for _, mode := range []string{"cancel", "claim late", "authorize late", "clock regressed", "wall only", "publish late"} {
		t.Run(mode, func(t *testing.T) {
			f := newWillExecutionFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f.found = true
			if mode == "cancel" {
				cancel()
			}
			if mode == "claim late" {
				f.store.after = func(_ meta.MQTTWill, r meta.MQTTWillResult) (meta.MQTTWillResult, error) {
					f.now = f.now.Add(11 * time.Second)
					return r, nil
				}
			}
			if mode == "authorize late" || mode == "clock regressed" || mode == "wall only" {
				f.opts.Authorizer = willAuthorizer(func(context.Context, string, app.WillTarget) error {
					switch mode {
					case "authorize late":
						f.now = f.now.Add(11 * time.Second)
					case "clock regressed":
						f.now = f.now.Add(-time.Second)
					case "wall only":
						f.now = f.now.Round(0)
					}
					return nil
				})
			}
			if mode == "publish late" {
				p := f.opts.Publications.(willPublications)
				orig := p.publish
				p.publish = func(ctx context.Context, q app.WillPublication) error {
					err := orig(ctx, q)
					f.now = f.now.Add(11 * time.Second)
					return err
				}
				f.opts.Publications = p
			}
			_, err := f.executor(t).Execute(ctx, f.key)
			require.Error(t, err)
			require.NotEqual(t, meta.MQTTWillPublished, f.read(t).Stage)
			if mode != "publish late" {
				require.Zero(t, f.published)
			}
		})
	}
}

func TestWillExecutionRejectsInvalidTemplateAndReceipt(t *testing.T) {
	for _, mode := range []string{"template", "receipt", "read identity"} {
		t.Run(mode, func(t *testing.T) {
			f := newWillExecutionFixture(t)
			f.found = true
			if mode == "receipt" {
				f.receipt.PublishedAtMS = 0
			} else {
				f.fixture.store.read = func(r meta.MQTTReadResult) meta.MQTTReadResult {
					if mode == "template" {
						r.Wills[0].QoS = 0
					} else {
						r.Wills[0].Key.WillGeneration++
					}
					return r
				}
			}
			_, err := f.executor(t).Execute(context.Background(), f.key)
			require.ErrorIs(t, err, app.ErrEvidence)
		})
	}
}

type blockedWillExecutionStore struct {
	*willExecutionStore
	entered, release chan struct{}
}

func (s blockedWillExecutionStore) ReadMQTT(ctx context.Context, _ meta.MQTTRead) (meta.MQTTReadResult, error) {
	s.entered <- struct{}{}
	select {
	case <-s.release:
		return meta.MQTTReadResult{}, app.ErrWillPending
	case <-ctx.Done():
		return meta.MQTTReadResult{}, ctx.Err()
	}
}
func TestWillExecutionBoundsConcurrentTurnsWithoutQueue(t *testing.T) {
	f := newWillExecutionFixture(t)
	s := blockedWillExecutionStore{willExecutionStore: f.store, entered: make(chan struct{}, 4), release: make(chan struct{})}
	f.opts.Store = s
	e := f.executor(t)
	done := make(chan error, 4)
	for i := 0; i < 4; i++ {
		go func() { _, err := e.Execute(context.Background(), f.key); done <- err }()
	}
	for i := 0; i < 4; i++ {
		<-s.entered
	}
	_, err := e.Execute(context.Background(), f.key)
	require.ErrorIs(t, err, app.ErrWillBusy)
	close(s.release)
	for i := 0; i < 4; i++ {
		require.Error(t, <-done)
	}
}

func TestWillExecutionRejectsInvalidOptions(t *testing.T) {
	f := newWillExecutionFixture(t)
	for _, change := range []func(*app.WillExecutionOptions){
		func(o *app.WillExecutionOptions) { o.Store = nil }, func(o *app.WillExecutionOptions) { o.Publications = nil }, func(o *app.WillExecutionOptions) { o.Authorizer = nil },
		func(o *app.WillExecutionOptions) { o.NodeID = 0 }, func(o *app.WillExecutionOptions) { o.BootID = "" },
		func(o *app.WillExecutionOptions) { o.LeaseDuration = 0 }, func(o *app.WillExecutionOptions) { o.TurnTimeout = 6 * time.Second },
		func(o *app.WillExecutionOptions) { o.Now = func() time.Time { return time.Now().Round(0) } },
	} {
		o := f.opts
		change(&o)
		_, err := app.NewWillExecutor(o)
		require.Error(t, err)
	}
}

func TestWillExecutionDependencyDeadlineDoesNotOutliveLease(t *testing.T) {
	f := newWillExecutionFixture(t)
	f.opts.LeaseDuration = 3 * time.Second
	f.opts.TurnTimeout = 5 * time.Second
	f.found = true
	p := f.opts.Publications.(willPublications)
	pub := p.publish
	p.publish = func(ctx context.Context, q app.WillPublication) error {
		deadline, ok := ctx.Deadline()
		require.True(t, ok)
		require.False(t, deadline.After(f.now.Add(f.opts.LeaseDuration)), "SEND dependencies cannot outlive the pre-proposal lease deadline")
		return pub(ctx, q)
	}
	f.opts.Publications = p
	_, err := f.executor(t).Execute(context.Background(), f.key)
	require.NoError(t, err)
}
