package mqttsession_test

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	app "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func readDeadlineWill(t *testing.T, f *fixture, c app.Connection) meta.MQTTReadResult {
	t.Helper()
	r, e := f.store.ReadMQTT(context.Background(), meta.MQTTRead{Kind: meta.MQTTReadWill, WillKey: meta.MQTTWillKey{Namespace: c.Owner.Key.Namespace, ClientID: c.Owner.Key.ClientID, SessionGeneration: c.Owner.SessionGeneration, WillGeneration: c.WillGeneration}})
	require.NoError(t, e)
	require.NotNil(t, r.Session)
	require.Len(t, r.Wills, 1)
	return r
}

func TestReconcileDeadlineFencesCandidatesAndIgnoresLiveOrRenewedLease(t *testing.T) {
	f := setup(t)
	c, e := f.service.Connect(context.Background(), command())
	require.NoError(t, e)
	f.opts.Isolation = isolation(func(context.Context, contract.Owner) error {
		t.Fatal("live/stale candidate reached isolation")
		return nil
	})
	service, e := app.New(f.opts)
	require.NoError(t, e)
	calls := f.store.calls.Load()
	require.NoError(t, service.ReconcileDeadline(context.Background(), c.Owner))
	f.now = f.now.Add(9 * time.Second)
	_, e = service.Renew(context.Background(), c.Owner)
	require.NoError(t, e)
	f.now = f.now.Add(2 * time.Second)
	require.NoError(t, service.ReconcileDeadline(context.Background(), c.Owner), "old deadline cannot expire the renewed lease")
	stale := c.Owner
	stale.OwnerGeneration++
	require.ErrorIs(t, service.ReconcileDeadline(context.Background(), stale), app.ErrFenced)
	stale = c.Owner
	stale.Key.ClientID = "absent"
	require.ErrorIs(t, service.ReconcileDeadline(context.Background(), stale), app.ErrFenced)
	require.Equal(t, calls, f.store.calls.Load())
	require.Equal(t, meta.MQTTSessionActive, f.row(t).State)
}

func TestReconcileDeadlineExpiredActiveNeedsIsolationAndKeepsOriginalClocks(t *testing.T) {
	f := setup(t)
	cmd := command()
	cmd.Will = will(t)
	c, e := f.service.Connect(context.Background(), cmd)
	require.NoError(t, e)
	original := f.row(t)
	f.now = f.now.Add(100 * time.Second)
	unproved := errors.New("old owner unavailable")
	f.opts.Isolation = isolation(func(context.Context, contract.Owner) error { return unproved })
	service, e := app.New(f.opts)
	require.NoError(t, e)
	require.ErrorIs(t, service.ReconcileDeadline(context.Background(), c.Owner), unproved)
	require.Equal(t, original, f.row(t))
	require.NoError(t, f.service.ReconcileDeadline(context.Background(), c.Owner))
	r := readDeadlineWill(t, f, c)
	require.Equal(t, meta.MQTTSessionOffline, r.Session.State)
	require.Equal(t, original.LeaseUntilMS+60000, r.Session.OfflineExpiresAtMS)
	require.Equal(t, original.LeaseUntilMS, r.Wills[0].DisconnectedAtMS)
	require.Equal(t, original.LeaseUntilMS+5000, r.Wills[0].DueAtMS)
	require.Equal(t, meta.MQTTWillWaiting, r.Wills[0].Stage, "one call commits at most one lifecycle event")
	require.NoError(t, f.service.ReconcileDeadline(context.Background(), c.Owner))
	r = readDeadlineWill(t, f, c)
	require.Equal(t, meta.MQTTSessionEnded, r.Session.State)
	require.Equal(t, meta.MQTTSessionExpired, r.Session.TerminationReason)
	require.Equal(t, meta.MQTTWillReady, r.Wills[0].Stage)
	require.Equal(t, original.LeaseUntilMS+5000, r.Wills[0].DueAtMS)
	require.Zero(t, r.Session.WillGeneration)
}

func TestReconcileDeadlinePromotesWillBeforeExpiryAndPreservesDelivery(t *testing.T) {
	f := setup(t)
	cmd := command()
	cmd.Will = will(t)
	c, e := f.service.Connect(context.Background(), cmd)
	require.NoError(t, e)
	require.NoError(t, f.service.Disconnect(context.Background(), app.DisconnectCommand{Owner: c.Owner}))
	before := f.row(t)
	calls := f.store.calls.Load()
	f.now = f.now.Add(4 * time.Second)
	require.NoError(t, f.service.ReconcileDeadline(context.Background(), c.Owner))
	require.Equal(t, calls, f.store.calls.Load())
	f.now = f.now.Add(time.Second)
	require.NoError(t, f.service.ReconcileDeadline(context.Background(), c.Owner))
	r := readDeadlineWill(t, f, c)
	require.Equal(t, meta.MQTTWillReady, r.Wills[0].Stage)
	require.Equal(t, meta.MQTTSessionOffline, r.Session.State)
	require.Zero(t, r.Session.WillGeneration)
	require.Equal(t, before.OfflineExpiresAtMS, r.Session.OfflineExpiresAtMS)
	// Everything except the conditional lifecycle receipt and resolved reference
	// remains identical, including quotas, pending counters and packet allocators.
	after := *r.Session
	after.Revision, after.UpdatedAtMS, after.WillGeneration, after.LastLifecycleDigest = before.Revision, before.UpdatedAtMS, before.WillGeneration, before.LastLifecycleDigest
	require.Equal(t, before, after)
	ready := r.Wills[0]
	f.now = f.now.Add(time.Minute)
	require.NoError(t, f.service.ReconcileDeadline(context.Background(), c.Owner))
	r = readDeadlineWill(t, f, c)
	require.Equal(t, meta.MQTTSessionEnded, r.Session.State)
	require.Equal(t, ready, r.Wills[0], "ending an offline lifetime must not rewrite detached work")
	require.Equal(t, "alice", r.Session.UID)
	calls = f.store.calls.Load()
	require.NoError(t, f.service.ReconcileDeadline(context.Background(), c.Owner))
	require.Equal(t, calls, f.store.calls.Load())
}

func TestReconcileDeadlineNormalDisconnectDoesNotResurrectWill(t *testing.T) {
	f := setup(t)
	cmd := command()
	cmd.Will = will(t)
	c, e := f.service.Connect(context.Background(), cmd)
	require.NoError(t, e)
	require.NoError(t, f.service.Disconnect(context.Background(), app.DisconnectCommand{Owner: c.Owner, Normal: true}))
	f.now = f.now.Add(time.Minute)
	require.NoError(t, f.service.ReconcileDeadline(context.Background(), c.Owner))
	r := readDeadlineWill(t, f, c)
	require.Equal(t, meta.MQTTSessionEnded, r.Session.State)
	require.Equal(t, meta.MQTTWillCancelled, r.Wills[0].Stage)
}

func TestReconcileDeadlineConcurrentReconnectCannotBeOverwritten(t *testing.T) {
	f := setup(t)
	cmd := command()
	cmd.Will = will(t)
	c, e := f.service.Connect(context.Background(), cmd)
	require.NoError(t, e)
	require.NoError(t, f.service.Disconnect(context.Background(), app.DisconnectCommand{Owner: c.Owner}))
	f.now = f.now.Add(5 * time.Second)
	var successor app.Connection
	f.store.before = func(meta.MQTTLifecycleMutation) {
		f.store.before = nil
		successor, e = f.service.Connect(context.Background(), command())
		require.NoError(t, e)
	}
	require.ErrorIs(t, f.service.ReconcileDeadline(context.Background(), c.Owner), app.ErrConflict)
	r := readDeadlineWill(t, f, c)
	require.Equal(t, successor.Owner, rowOwner(*r.Session))
	require.Equal(t, meta.MQTTSessionActive, r.Session.State)
	require.Equal(t, meta.MQTTWillReady, r.Wills[0].Stage)
}

func TestReconcileDeadlineRejectsInvalidOrIncoherentAuthority(t *testing.T) {
	for _, mode := range []string{"missing-will", "foreign-will", "wrong-will-owner", "wrong-session", "changed-reference", "duplicate-will", "future-update", "overflow-revision"} {
		t.Run(mode, func(t *testing.T) {
			f := setup(t)
			cmd := command()
			cmd.Will = will(t)
			c, e := f.service.Connect(context.Background(), cmd)
			require.NoError(t, e)
			require.NoError(t, f.service.Disconnect(context.Background(), app.DisconnectCommand{Owner: c.Owner}))
			f.now = f.now.Add(5 * time.Second)
			f.store.read = func(r meta.MQTTReadResult) meta.MQTTReadResult {
				if len(r.Wills) == 0 {
					return r
				}
				switch mode {
				case "missing-will":
					r.Wills = nil
				case "foreign-will":
					r.Wills[0].Key.ClientID = "other"
				case "wrong-will-owner":
					r.Wills[0].OwnerGeneration++
				case "wrong-session":
					r.Session.ClientID = "other"
				case "changed-reference":
					r.Session.WillGeneration = 0
				case "duplicate-will":
					r.Wills = append(r.Wills, r.Wills[0])
				case "future-update":
					r.Session.UpdatedAtMS = f.now.Add(time.Second).UnixMilli()
				case "overflow-revision":
					r.Session.Revision = math.MaxUint64
				}
				return r
			}
			calls := f.store.calls.Load()
			require.Error(t, f.service.ReconcileDeadline(context.Background(), c.Owner))
			require.Equal(t, calls, f.store.calls.Load())
		})
	}
	for _, mode := range []string{"uncertain", "malformed", "conflict"} {
		t.Run(mode, func(t *testing.T) {
			f := setup(t)
			c, e := f.service.Connect(context.Background(), command())
			require.NoError(t, e)
			require.NoError(t, f.service.Disconnect(context.Background(), app.DisconnectCommand{Owner: c.Owner, Normal: true}))
			f.now = f.now.Add(time.Minute)
			f.store.after = func(_ meta.MQTTLifecycleMutation, r meta.MQTTLifecycleResult) (meta.MQTTLifecycleResult, error) {
				switch mode {
				case "uncertain":
					return r, context.DeadlineExceeded
				case "malformed":
					r.CurrentRevision++
				case "conflict":
					r.Status = meta.MQTTSessionCASConflict
				}
				return r, nil
			}
			require.Error(t, f.service.ReconcileDeadline(context.Background(), c.Owner))
			f.store.after = nil
			require.NoError(t, f.service.ReconcileDeadline(context.Background(), c.Owner), "retry rereads durable state")
		})
	}
}

func TestReconcileDeadlineCancellationAndClockFailureHaveNoEffects(t *testing.T) {
	f := setup(t)
	c, e := f.service.Connect(context.Background(), command())
	require.NoError(t, e)
	calls := f.store.calls.Load()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, f.service.ReconcileDeadline(ctx, c.Owner), context.Canceled)
	require.ErrorIs(t, f.service.ReconcileDeadline(nil, c.Owner), app.ErrInvalid)
	require.ErrorIs(t, f.service.ReconcileDeadline(context.Background(), contract.Owner{}), app.ErrInvalid)
	f.now = f.now.Add(-time.Second)
	require.ErrorIs(t, f.service.ReconcileDeadline(context.Background(), c.Owner), app.ErrClock)
	require.Equal(t, calls, f.store.calls.Load())
}
