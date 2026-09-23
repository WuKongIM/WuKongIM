package mqttsession_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	"github.com/WuKongIM/WuKongIM/internal/contracts/protocolmeta"
	owner "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	app "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/publication"
	"github.com/stretchr/testify/require"
)

type tokenVerifier func(context.Context, string, protocolmeta.DeviceFlag, string) (protocolmeta.DeviceLevel, error)

func (f tokenVerifier) VerifyToken(c context.Context, u string, d protocolmeta.DeviceFlag, token string) (protocolmeta.DeviceLevel, error) {
	return f(c, u, d, token)
}

type willAuthorizer func(context.Context, string, app.WillTarget) error

func (f willAuthorizer) AuthorizeWill(c context.Context, u string, w app.WillTarget) error {
	return f(c, u, w)
}

type isolation func(context.Context, contract.Owner) error

func (f isolation) Quiesce(c context.Context, o contract.Owner) error { return f(c, o) }

// This is real deterministic metadata storage, deliberately not cluster authority.
// The production Node already implements the same three-method port.
type sessionStore struct {
	db      *meta.MetaDB
	before  func(meta.MQTTLifecycleMutation)
	after   func(meta.MQTTLifecycleMutation, meta.MQTTLifecycleResult) (meta.MQTTLifecycleResult, error)
	renewed func()
	read    func(meta.MQTTReadResult) meta.MQTTReadResult
	calls   atomic.Int32
}

func (s *sessionStore) ReadMQTT(c context.Context, q meta.MQTTRead) (meta.MQTTReadResult, error) {
	r, e := s.db.ReadMQTTState(c, 7, q)
	if e == nil && s.read != nil {
		r = s.read(r)
	}
	return r, e
}
func (s *sessionStore) ApplyMQTTLifecycle(c context.Context, m meta.MQTTLifecycleMutation) (meta.MQTTLifecycleResult, error) {
	s.calls.Add(1)
	if s.before != nil {
		s.before(m)
	}
	b := s.db.NewBatch()
	defer b.Close()
	r, e := b.ApplyMQTTLifecycle(7, m)
	if e != nil {
		return meta.MQTTLifecycleResult{}, e
	}
	if e = b.Commit(c); e != nil {
		return meta.MQTTLifecycleResult{}, e
	}
	if s.after != nil {
		return s.after(m, *r)
	}
	return *r, nil
}
func (s *sessionStore) CompareAndSwapMQTTSession(c context.Context, v uint64, row meta.MQTTSession) (meta.MQTTSessionCASResult, error) {
	b := s.db.NewBatch()
	defer b.Close()
	r, e := b.CompareAndSwapMQTTSession(7, v, row)
	if e != nil {
		return meta.MQTTSessionCASResult{}, e
	}
	if e = b.Commit(c); e != nil {
		return meta.MQTTSessionCASResult{}, e
	}
	if s.renewed != nil {
		s.renewed()
	}
	return *r, nil
}

type fixture struct {
	service *app.App
	opts    app.Options
	owners  *owner.Owners
	store   *sessionStore
	now     time.Time
}

func setup(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{now: time.Now()}
	store, e := db.OpenNodeStore(db.DefaultNodeStoreOptions(t.TempDir()))
	require.NoError(t, e)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	f.store = &sessionStore{db: store.Meta()}
	f.owners, e = owner.NewOwners(owner.OwnerOptions{NodeID: 1, BootID: "boot", Capacity: 64, MaxOperations: 8, PendingTimeout: time.Minute, MaxLease: time.Minute, CloseRetry: time.Second, Now: func() time.Time { return f.now }})
	require.NoError(t, e)
	f.opts = app.Options{Store: f.store, Owners: f.owners, Isolation: f.owners, Tokens: tokenVerifier(func(context.Context, string, protocolmeta.DeviceFlag, string) (protocolmeta.DeviceLevel, error) {
		return protocolmeta.DeviceLevelMaster, nil
	}), Wills: willAuthorizer(func(context.Context, string, app.WillTarget) error { return nil }), Now: func() time.Time { return f.now }, LeaseDuration: 10 * time.Second, CleanupTimeout: time.Second, SessionExpiryLimitSec: 86400, QuotaMessages: 10000, QuotaBytes: 64 << 20, WindowLimit: 64}
	f.service, e = app.New(f.opts)
	require.NoError(t, e)
	t.Cleanup(func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		if t.Failed() {
			cancel()
		} // Do not hide a failed scope assertion behind a teardown hang.
		err := f.owners.Close(ctx)
		if !t.Failed() {
			require.NoError(t, err)
		}
	})
	return f
}
func command() app.ConnectCommand {
	return app.ConnectCommand{Key: contract.Key{Namespace: "main", ClientID: "client"}, UID: "alice", Token: "token", DeviceFlag: protocolmeta.DeviceFlagWeb, SessionExpirySec: 60, ReceiveMaximum: 64, MaxPacketBytes: 1 << 20, CloseTransport: func(context.Context) error { return nil }}
}
func (f *fixture) row(t *testing.T) meta.MQTTSession {
	t.Helper()
	r, e := f.store.ReadMQTT(context.Background(), meta.MQTTRead{Kind: meta.MQTTReadSession, Namespace: "main", ClientID: "client"})
	require.NoError(t, e)
	require.NotNil(t, r.Session)
	return *r.Session
}
func will(t *testing.T) *app.Will {
	t.Helper()
	b, e := publication.Encode(publication.Metadata{Source: publication.SourceWill, QoS: 1, PublisherNamespace: "main", PublisherClientID: "client", OriginalTopic: "wk/v1/groups/Z3JvdXA/messages"})
	require.NoError(t, e)
	return &app.Will{WillTarget: app.WillTarget{Topic: "wk/v1/groups/Z3JvdXA/messages", TargetID: "group", TargetType: 2}, QoS: 1, DelaySeconds: 5, ClientMsgNo: "will-no", Payload: []byte("goodbye"), PublicationMetadata: b}
}
func rowOwner(r meta.MQTTSession) contract.Owner {
	return contract.Owner{Key: contract.Key{Namespace: r.Namespace, ClientID: r.ClientID}, SessionGeneration: r.Generation, OwnerGeneration: r.OwnerGeneration, NodeID: r.OwnerNodeID, BootID: r.OwnerBootID, ConnectionID: r.ConnectionID}
}

func TestConnectAuthenticatesBeforeEffectsAndBindsClientID(t *testing.T) {
	f := setup(t)
	c := command()
	f.opts.Tokens = tokenVerifier(func(context.Context, string, protocolmeta.DeviceFlag, string) (protocolmeta.DeviceLevel, error) {
		return 0, errors.New("credential denied")
	})
	service, e := app.New(f.opts)
	require.NoError(t, e)
	_, e = service.Connect(context.Background(), c)
	require.Error(t, e)
	require.Zero(t, f.store.calls.Load())
	require.Zero(t, f.owners.Snapshot().Held)
	first, e := f.service.Connect(context.Background(), c)
	require.NoError(t, e)
	c.UID = "mallory"
	c.CleanStart = true
	_, e = f.service.Connect(context.Background(), c)
	require.ErrorIs(t, e, app.ErrBinding)
	require.Equal(t, uint64(1), f.row(t).Revision)
	op, e := f.owners.Begin(context.Background(), first.Owner)
	require.NoError(t, e)
	op.Done()
	c = command()
	c.Key.ClientID = "different"
	_, e = f.service.Connect(context.Background(), c)
	require.NoError(t, e)
	require.Equal(t, 2, f.owners.Snapshot().Active, "MQTT inherits no master-device kick")
}

func TestConnectActivatesAfterCommitAndOwnsWillTemplate(t *testing.T) {
	f := setup(t)
	c := command()
	c.SessionExpirySec = ^uint32(0)
	c.Will = will(t)
	f.store.before = func(m meta.MQTTLifecycleMutation) {
		_, e := f.owners.Begin(context.Background(), rowOwner(m.Session))
		require.ErrorIs(t, e, owner.ErrOwnerFenced)
	}
	started := f.now
	f.store.after = func(_ meta.MQTTLifecycleMutation, r meta.MQTTLifecycleResult) (meta.MQTTLifecycleResult, error) {
		f.now = f.now.Add(2 * time.Second)
		return r, nil
	}
	result, e := f.service.Connect(context.Background(), c)
	require.NoError(t, e)
	require.False(t, result.SessionPresent)
	require.Equal(t, uint32(86400), result.SessionExpirySec)
	require.True(t, result.Lease.Until.Equal(started.Add(10*time.Second)), "latency extended local lease")
	row := f.row(t)
	require.LessOrEqual(t, result.Lease.Until.UnixNano(), row.LeaseUntilMS*int64(time.Millisecond), "durable lease expires before local gate")
	require.Less(t, row.LeaseUntilMS*int64(time.Millisecond)-result.Lease.Until.UnixNano(), int64(time.Millisecond))
	require.Equal(t, row.Revision, row.WillGeneration)
	c.Will.Payload[0] = 'X'
	c.Will.PublicationMetadata[0] = 0
	w, found, e := f.store.db.HashSlot(7).GetMQTTWill(context.Background(), meta.MQTTWillKey{Namespace: "main", ClientID: "client", SessionGeneration: 1, WillGeneration: 1})
	require.NoError(t, e)
	require.True(t, found)
	require.Equal(t, "goodbye", string(w.Payload))
	require.Equal(t, byte(1), w.PublicationMetadata[0])
	require.Equal(t, meta.MQTTWillArmed, w.Stage)
	op, e := f.owners.Begin(context.Background(), result.Owner)
	require.NoError(t, e)
	op.Done()
}

func TestConnectWaitsForExactOldOwnerDrain(t *testing.T) {
	f := setup(t)
	c := command()
	closed := make(chan struct{})
	c.CloseTransport = func(context.Context) error { close(closed); return nil }
	first, e := f.service.Connect(context.Background(), c)
	require.NoError(t, e)
	op, e := f.owners.Begin(context.Background(), first.Owner)
	require.NoError(t, e)
	type outcome struct {
		value app.Connection
		err   error
	}
	done := make(chan outcome, 1)
	go func() { v, e := f.service.Connect(context.Background(), command()); done <- outcome{v, e} }()
	<-closed
	select {
	case r := <-done:
		t.Fatalf("takeover before admitted effect completed: %+v", r)
	default:
	}
	require.Equal(t, first.Owner, rowOwner(f.row(t)))
	_, e = f.owners.Begin(context.Background(), first.Owner)
	require.ErrorIs(t, e, owner.ErrOwnerFenced)
	op.Done()
	next := <-done
	require.NoError(t, next.err)
	require.True(t, next.value.SessionPresent)
	require.Equal(t, first.Owner.SessionGeneration, next.value.Owner.SessionGeneration)
	require.Equal(t, first.Owner.OwnerGeneration+1, next.value.Owner.OwnerGeneration)
	row := f.row(t)
	require.ErrorIs(t, f.service.Disconnect(context.Background(), app.DisconnectCommand{Owner: first.Owner, Normal: true}), app.ErrFenced)
	require.Equal(t, row, f.row(t))
}

func TestConnectRejectsUnprovedIsolationAndChangedOwner(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "unproved", true: "successor"}[changed], func(t *testing.T) {
			f := setup(t)
			first, e := f.service.Connect(context.Background(), command())
			require.NoError(t, e)
			f.opts.Isolation = isolation(func(ctx context.Context, o contract.Owner) error {
				if !changed {
					return owner.ErrOwnerUnknown
				}
				require.NoError(t, f.owners.Quiesce(ctx, o))
				r := f.row(t)
				r.Revision++
				r.OwnerGeneration++
				r.ConnectionID = 999
				r.LeaseUntilMS += 1000
				r.WillGeneration, r.LastLifecycleDigest = 0, ""
				_, e := f.store.ApplyMQTTLifecycle(ctx, meta.MQTTLifecycleMutation{ExpectedRevision: r.Revision - 1, ExpectedGeneration: first.Owner.SessionGeneration, OwnerGeneration: first.Owner.OwnerGeneration, OwnerNodeID: first.Owner.NodeID, OwnerBootID: first.Owner.BootID, ConnectionID: first.Owner.ConnectionID, Event: meta.MQTTLifecycleConnect, Session: r})
				return e
			})
			s, e := app.New(f.opts)
			require.NoError(t, e)
			_, e = s.Connect(context.Background(), command())
			require.Error(t, e)
			if !changed {
				require.Equal(t, first.Owner, rowOwner(f.row(t)))
			} else {
				require.Equal(t, uint64(999), f.row(t).ConnectionID)
			}
		})
	}
}

func TestConnectExpiredOwnerRecordsOriginalDisconnectAndDueWill(t *testing.T) {
	f := setup(t)
	c := command()
	c.Will = will(t)
	first, e := f.service.Connect(context.Background(), c)
	require.NoError(t, e)
	old := f.row(t)
	f.now = f.now.Add(20 * time.Second)
	next, e := f.service.Connect(context.Background(), command())
	require.NoError(t, e)
	require.True(t, next.SessionPresent)
	require.Equal(t, first.Owner.SessionGeneration, next.Owner.SessionGeneration)
	w, found, e := f.store.db.HashSlot(7).GetMQTTWill(context.Background(), meta.MQTTWillKey{Namespace: "main", ClientID: "client", SessionGeneration: 1, WillGeneration: 1})
	require.NoError(t, e)
	require.True(t, found)
	require.Equal(t, meta.MQTTWillReady, w.Stage)
	require.Equal(t, old.LeaseUntilMS, w.DisconnectedAtMS)
	require.Equal(t, old.LeaseUntilMS+5000, w.DueAtMS)
	c = command()
	c.CleanStart = true
	fresh, e := f.service.Connect(context.Background(), c)
	require.NoError(t, e)
	require.False(t, fresh.SessionPresent)
	require.Equal(t, next.Owner.SessionGeneration+1, fresh.Owner.SessionGeneration)
}

func TestConnectFailureNeverActivatesCandidate(t *testing.T) {
	for _, kind := range []string{"uncertain", "late", "malformed", "conflict"} {
		t.Run(kind, func(t *testing.T) {
			f := setup(t)
			c := command()
			var closes atomic.Int32
			c.CloseTransport = func(context.Context) error { closes.Add(1); return nil }
			f.store.after = func(_ meta.MQTTLifecycleMutation, r meta.MQTTLifecycleResult) (meta.MQTTLifecycleResult, error) {
				switch kind {
				case "uncertain":
					return r, errors.New("commit acknowledgement lost")
				case "late":
					f.now = f.now.Add(11 * time.Second)
				case "malformed":
					r.CurrentRevision++
				case "conflict":
					r.Status = meta.MQTTSessionCASConflict
				}
				return r, nil
			}
			_, e := f.service.Connect(context.Background(), c)
			require.Error(t, e)
			require.Equal(t, int32(1), closes.Load())
			require.Zero(t, f.owners.Snapshot().Held)
			require.Equal(t, int32(1), f.store.calls.Load(), "uncertain acquisition retried another proposal")
			if kind == "uncertain" {
				f.store.after = nil
				r, e := f.service.Connect(context.Background(), command())
				require.NoError(t, e)
				require.Equal(t, uint64(2), r.Owner.OwnerGeneration)
			}
		})
	}
}

func TestConnectRejectsWillForgeryBeforeExistingOwnerEffects(t *testing.T) {
	f := setup(t)
	first, e := f.service.Connect(context.Background(), command())
	require.NoError(t, e)
	c := command()
	c.Will = will(t)
	c.Will.QoS = 0
	_, e = f.service.Connect(context.Background(), c)
	require.ErrorIs(t, e, app.ErrInvalid)
	c.Will = will(t)
	f.opts.Wills = willAuthorizer(func(context.Context, string, app.WillTarget) error { return errors.New("denied") })
	s, e := app.New(f.opts)
	require.NoError(t, e)
	_, e = s.Connect(context.Background(), c)
	require.Error(t, e)
	require.Equal(t, uint64(1), f.row(t).Revision)
	op, e := f.owners.Begin(context.Background(), first.Owner)
	require.NoError(t, e)
	op.Done()
}

func TestRenewPreservesStateAndCannotResurrectExpiredOwner(t *testing.T) {
	f := setup(t)
	c := command()
	c.Will = will(t)
	first, e := f.service.Connect(context.Background(), c)
	require.NoError(t, e)
	old := f.row(t)
	f.now = f.now.Add(time.Second)
	started := f.now
	lease, e := f.service.Renew(context.Background(), first.Owner)
	require.NoError(t, e)
	row := f.row(t)
	require.Equal(t, old.WillGeneration, row.WillGeneration)
	require.Equal(t, old.LastLifecycleDigest, row.LastLifecycleDigest)
	require.Equal(t, old.Revision+1, row.Revision)
	require.True(t, lease.Until.Equal(started.Add(10*time.Second)))
	f.store.renewed = func() { f.now = f.now.Add(20 * time.Second) }
	_, e = f.service.Renew(context.Background(), first.Owner)
	require.Error(t, e)
	_, e = f.owners.Begin(context.Background(), first.Owner)
	require.ErrorIs(t, e, owner.ErrOwnerFenced)
}

func TestDisconnectAppliesExactAtomicWillDecisionAndExpiry(t *testing.T) {
	f := setup(t)
	c := command()
	c.Will = will(t)
	first, e := f.service.Connect(context.Background(), c)
	require.NoError(t, e)
	override := uint32(120)
	require.NoError(t, f.service.Disconnect(context.Background(), app.DisconnectCommand{Owner: first.Owner, Normal: true, SessionExpirySec: &override}))
	r := f.row(t)
	require.Equal(t, meta.MQTTSessionOffline, r.State)
	require.Equal(t, f.now.UnixMilli()+120000, r.OfflineExpiresAtMS)
	require.Zero(t, r.LeaseUntilMS)
	w, _, e := f.store.db.HashSlot(7).GetMQTTWill(context.Background(), meta.MQTTWillKey{Namespace: "main", ClientID: "client", SessionGeneration: 1, WillGeneration: 1})
	require.NoError(t, e)
	require.Equal(t, meta.MQTTWillCancelled, w.Stage)
	resumed, e := f.service.Connect(context.Background(), command())
	require.NoError(t, e)
	require.True(t, resumed.SessionPresent)
	c = command()
	c.CleanStart = true
	c.SessionExpirySec = 0
	zero, e := f.service.Connect(context.Background(), c)
	require.NoError(t, e)
	require.ErrorIs(t, f.service.Disconnect(context.Background(), app.DisconnectCommand{Owner: zero.Owner, Normal: true, SessionExpirySec: &override}), app.ErrInvalid)
	op, e := f.owners.Begin(context.Background(), zero.Owner)
	require.NoError(t, e)
	op.Done()
	require.NoError(t, f.service.Disconnect(context.Background(), app.DisconnectCommand{Owner: zero.Owner, Normal: false}))
	require.Equal(t, meta.MQTTSessionEnded, f.row(t).State)
}

func TestConnectRejectsClockRegressionAndForeignRead(t *testing.T) {
	f := setup(t)
	_, e := f.service.Connect(context.Background(), command())
	require.NoError(t, e)
	before := f.row(t)
	f.now = f.now.Add(-time.Second)
	_, e = f.service.Connect(context.Background(), command())
	require.Error(t, e)
	require.Equal(t, before, f.row(t))
	f.now = f.now.Add(time.Second)
	f.store.read = func(r meta.MQTTReadResult) meta.MQTTReadResult {
		if r.Session != nil {
			r.Session.ClientID = "foreign"
		}
		return r
	}
	_, e = f.service.Connect(context.Background(), command())
	require.ErrorIs(t, e, app.ErrEvidence)
}

func TestConnectRejectsUnboundedInputWithoutPersistingCredential(t *testing.T) {
	f := setup(t)
	for _, mutate := range []func(*app.ConnectCommand){func(c *app.ConnectCommand) { c.Token = strings.Repeat("x", 16385) }, func(c *app.ConnectCommand) { c.ReceiveMaximum = 0 }, func(c *app.ConnectCommand) { c.Key.ClientID = "" }, func(c *app.ConnectCommand) { c.DeviceFlag = 3 }, func(c *app.ConnectCommand) { c.CloseTransport = nil }} {
		c := command()
		mutate(&c)
		_, e := f.service.Connect(context.Background(), c)
		require.ErrorIs(t, e, app.ErrInvalid)
	}
	require.Zero(t, f.store.calls.Load())
	require.Zero(t, f.owners.Snapshot().Held)
	opts := f.opts
	opts.LeaseDuration = 2 * time.Minute
	_, e := app.New(opts)
	require.ErrorIs(t, e, app.ErrInvalid)
	opts = f.opts
	opts.Isolation = nil
	_, e = app.New(opts)
	require.ErrorIs(t, e, app.ErrInvalid)
}

// A quota transition may end durable state before local transport cleanup runs.
// A fresh authoritative rejection must immediately stop further local admission.
func TestRenewKnownTerminationOrClockFailureFencesLocalOwner(t *testing.T) {
	for _, kind := range []string{"ended", "clock"} {
		t.Run(kind, func(t *testing.T) {
			f := setup(t)
			connected, e := f.service.Connect(context.Background(), command())
			require.NoError(t, e)
			held, e := f.owners.Begin(context.Background(), connected.Owner)
			require.NoError(t, e)
			defer held.Done()
			if kind == "clock" {
				f.now = f.now.Add(-time.Second)
			} else {
				old := f.row(t)
				next := old
				next.Revision++
				next.State = meta.MQTTSessionEnded
				next.LeaseUntilMS = 0
				next.TerminationReason = meta.MQTTSessionQuota
				next.WillGeneration = 0
				next.LastLifecycleDigest = ""
				_, e = f.store.ApplyMQTTLifecycle(context.Background(), meta.MQTTLifecycleMutation{ExpectedRevision: old.Revision, ExpectedGeneration: old.Generation, OwnerGeneration: old.OwnerGeneration, OwnerNodeID: old.OwnerNodeID, OwnerBootID: old.OwnerBootID, ConnectionID: old.ConnectionID, Event: meta.MQTTLifecycleEnd, Session: next})
				require.NoError(t, e)
			}
			_, e = f.service.Renew(context.Background(), connected.Owner)
			require.Error(t, e)
			next, e := f.owners.Begin(context.Background(), connected.Owner)
			if next != nil {
				next.Done()
			}
			require.ErrorIs(t, e, owner.ErrOwnerFenced, "confirmed invalid authority left execution open")
			require.Equal(t, 1, f.owners.Snapshot().Operations, "renewal retained its own scope")
		})
	}
}

func TestRenewDependencyPanicReleasesOperation(t *testing.T) {
	f := setup(t)
	connected, e := f.service.Connect(context.Background(), command())
	require.NoError(t, e)
	f.store.read = func(meta.MQTTReadResult) meta.MQTTReadResult { panic("storage failure") }
	require.Panics(t, func() { _, _ = f.service.Renew(context.Background(), connected.Owner) })
	require.Zero(t, f.owners.Snapshot().Operations, "renewal leaked a scope across panic")
}

func TestDisconnectIsolationDelayPreservesNormalDecisionAndOfflineClock(t *testing.T) {
	f := setup(t)
	c := command()
	c.Will = will(t)
	connected, e := f.service.Connect(context.Background(), c)
	require.NoError(t, e)
	observed := f.now.UnixMilli()
	f.opts.Isolation = isolation(func(ctx context.Context, o contract.Owner) error {
		err := f.owners.Quiesce(ctx, o)
		f.now = f.now.Add(20 * time.Second)
		return err
	})
	service, e := app.New(f.opts)
	require.NoError(t, e)
	require.NoError(t, service.Disconnect(context.Background(), app.DisconnectCommand{Owner: connected.Owner, Normal: true}))
	row := f.row(t)
	require.Equal(t, observed+60000, row.OfflineExpiresAtMS, "isolation delay restarted offline lifetime")
	w, _, e := f.store.db.HashSlot(7).GetMQTTWill(context.Background(), meta.MQTTWillKey{Namespace: "main", ClientID: "client", SessionGeneration: 1, WillGeneration: 1})
	require.NoError(t, e)
	require.Equal(t, meta.MQTTWillCancelled, w.Stage, "normal disconnect became abnormal while isolation waited")
}

func TestQueuedDisconnectPreservesTrustedObservation(t *testing.T) {
	f := setup(t)
	cmd := command()
	cmd.Will = will(t)
	connection, err := f.service.Connect(context.Background(), cmd)
	require.NoError(t, err)
	f.now = f.now.Add(time.Second)
	observed := f.now
	f.now = f.now.Add(20 * time.Second)
	require.NoError(t, f.service.Disconnect(context.Background(), app.DisconnectCommand{Owner: connection.Owner, Normal: true, ObservedAt: observed}))
	row := f.row(t)
	require.Equal(t, observed.UnixMilli()+60000, row.OfflineExpiresAtMS)
	require.Zero(t, row.WillGeneration)
}
func TestQueuedDisconnectRejectsFutureAndWallOnlyObservation(t *testing.T) {
	for _, mode := range []string{"future", "wall"} {
		t.Run(mode, func(t *testing.T) {
			f := setup(t)
			connection, err := f.service.Connect(context.Background(), command())
			require.NoError(t, err)
			observed := f.now.Add(time.Second)
			if mode == "wall" {
				observed = f.now.Round(0)
			}
			require.Error(t, f.service.Disconnect(context.Background(), app.DisconnectCommand{Owner: connection.Owner, Normal: true, ObservedAt: observed}))
			require.Equal(t, meta.MQTTSessionActive, f.row(t).State)
		})
	}
}
