package mqttsession_test

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	app "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

type reclamationStore struct {
	*sessionStore
	query  func(context.Context, meta.MQTTRead) (meta.MQTTReadResult, error)
	write  func(context.Context, meta.MQTTSessionReclamation) (meta.MQTTSessionReclamationResult, error)
	writes []meta.MQTTSessionReclamation
}

func (s *reclamationStore) ReadMQTT(c context.Context, q meta.MQTTRead) (meta.MQTTReadResult, error) {
	if s.query != nil {
		return s.query(c, q)
	}
	return s.sessionStore.ReadMQTT(c, q)
}
func (s *reclamationStore) ReclaimMQTTSession(c context.Context, m meta.MQTTSessionReclamation) (meta.MQTTSessionReclamationResult, error) {
	s.writes = append(s.writes, m)
	if s.write != nil {
		return s.write(c, m)
	}
	b := s.db.NewBatch()
	defer b.Close()
	r, e := b.ReclaimMQTTSession(7, m)
	if e != nil {
		return meta.MQTTSessionReclamationResult{}, e
	}
	if e = b.Commit(c); e != nil {
		return meta.MQTTSessionReclamationResult{}, e
	}
	return *r, nil
}
func reclamationFixture(t *testing.T) (*fixture, *reclamationStore, app.SessionReclamationOptions, meta.MQTTSessionCursor) {
	t.Helper()
	f := setup(t)
	c, e := f.service.Connect(context.Background(), command())
	require.NoError(t, e)
	require.NoError(t, f.service.End(context.Background(), app.EndCommand{Owner: c.Owner, Reason: meta.MQTTSessionExplicit}))
	s := &reclamationStore{sessionStore: f.store}
	return f, s, app.SessionReclamationOptions{Store: s, Ender: f.service, Now: func() time.Time { return f.now }}, meta.MQTTSessionCursor{Namespace: c.Owner.Key.Namespace, ClientID: c.Owner.Key.ClientID}
}
func TestSessionReclamationRereadsAfterExactEndAndLeavesSuccessorRunning(t *testing.T) {
	for _, successor := range []bool{false, true} {
		t.Run(map[bool]string{false: "ended current", true: "live successor"}[successor], func(t *testing.T) {
			f, s, o, k := reclamationFixture(t)
			old := f.row(t)
			calls := 0
			o.Ender = pendingEstablishmentEnder(func(ctx context.Context, c app.EndCommand) error {
				calls++
				require.Equal(t, rowOwner(old), c.Owner)
				require.Equal(t, meta.MQTTSessionExplicit, c.Reason)
				require.NoError(t, f.service.End(ctx, c))
				row := f.row(t)
				row.Revision++
				_, e := f.store.CompareAndSwapMQTTSession(ctx, row.Revision-1, row)
				return e
			})
			if successor {
				c, e := f.service.Connect(context.Background(), command())
				require.NoError(t, e)
				require.Greater(t, c.Owner.SessionGeneration, old.Generation)
			}
			r, e := app.NewSessionReclamation(o)
			require.NoError(t, e)
			out, e := r.Reconcile(context.Background(), k)
			require.NoError(t, e)
			require.True(t, out.Changed && out.Completed)
			require.Len(t, s.writes, 1)
			require.Equal(t, old.Generation, s.writes[0].ThroughGeneration)
			if successor {
				require.Zero(t, calls)
				row := f.row(t)
				require.Equal(t, meta.MQTTSessionActive, row.State)
				scope, e := f.owners.Begin(context.Background(), rowOwner(row))
				require.NoError(t, e)
				scope.Done()
			} else {
				require.Equal(t, 1, calls)
				require.Equal(t, old.Revision+1, s.writes[0].ExpectedRevision)
			}
			out, e = r.Reconcile(context.Background(), k)
			require.NoError(t, e)
			require.Zero(t, out)
			require.Len(t, s.writes, 1)
		})
	}
}
func TestSessionReclamationRequiresFreshIsolatedUnchangedOwner(t *testing.T) {
	for _, fault := range []string{"isolation unavailable", "panic", "cancel", "owner", "uid", "generation", "state", "clock"} {
		t.Run(fault, func(t *testing.T) {
			f, s, o, k := reclamationFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			row := f.row(t)
			o.Ender = pendingEstablishmentEnder(func(context.Context, app.EndCommand) error {
				switch fault {
				case "isolation unavailable":
					return errors.New("unavailable")
				case "panic":
					panic("secret")
				case "cancel":
					cancel()
				case "owner":
					row.OwnerGeneration++
				case "uid":
					row.UID = "other"
				case "generation":
					row.Generation++
				case "state":
					row.State = meta.MQTTSessionOffline
					row.TerminationReason = 0
					row.OfflineExpiresAtMS = row.UpdatedAtMS + 1000
				case "clock":
					f.now = f.now.Add(-time.Second)
				}
				s.query = func(context.Context, meta.MQTTRead) (meta.MQTTReadResult, error) {
					return meta.MQTTReadResult{Session: &row, Done: true}, nil
				}
				return nil
			})
			r, e := app.NewSessionReclamation(o)
			require.NoError(t, e)
			out, e := r.Reconcile(ctx, k)
			require.Error(t, e)
			require.Zero(t, out)
			require.Empty(t, s.writes)
			require.NotContains(t, e.Error(), "secret")
		})
	}
}
func TestSessionReclamationRejectsBadAuthorityBeforeEffects(t *testing.T) {
	for _, fault := range []string{"incomplete", "cursor", "extra", "identity", "revision", "clock", "cancel", "panic", "read error"} {
		t.Run(fault, func(t *testing.T) {
			f, s, o, k := reclamationFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			row := f.row(t)
			s.query = func(context.Context, meta.MQTTRead) (meta.MQTTReadResult, error) {
				r := meta.MQTTReadResult{Session: &row, Done: true}
				switch fault {
				case "incomplete":
					r.Done = false
				case "cursor":
					r.After.Session = k
				case "extra":
					r.Runtime = &meta.MQTTRuntimeView{}
				case "identity":
					row.ClientID = "other"
				case "revision":
					row.Revision = math.MaxUint64
				case "clock":
					row.UpdatedAtMS += 1000
				case "cancel":
					cancel()
				case "panic":
					panic("secret")
				case "read error":
					return r, errors.New("unavailable")
				}
				return r, nil
			}
			o.Ender = pendingEstablishmentEnder(func(context.Context, app.EndCommand) error { t.Fatal("bad authority reached isolation"); return nil })
			r, e := app.NewSessionReclamation(o)
			require.NoError(t, e)
			out, e := r.Reconcile(ctx, k)
			require.Error(t, e)
			require.Zero(t, out)
			require.Empty(t, s.writes)
		})
	}
}
func TestSessionReclamationDoesNotRetryUnknownOrContradictoryWrites(t *testing.T) {
	for _, fault := range []string{"partial", "complete", "unchanged", "conflict", "unknown", "late", "panic", "wrong revision", "wrong marker", "no progress", "extra removals", "bad status"} {
		t.Run(fault, func(t *testing.T) {
			_, s, o, k := reclamationFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s.write = func(_ context.Context, m meta.MQTTSessionReclamation) (meta.MQTTSessionReclamationResult, error) {
				r := meta.MQTTSessionReclamationResult{Status: meta.MQTTSessionCASApplied, CurrentRevision: m.ExpectedRevision + 1, Done: true, ReclaimedThroughGeneration: m.ThroughGeneration}
				switch fault {
				case "partial":
					r.Done = false
					r.ReclaimedThroughGeneration = 0
					r.RemovedSubscriptions = 64
				case "unchanged":
					r.Status = meta.MQTTSessionCASUnchanged
					r.CurrentRevision += 10
					r.ReclaimedThroughGeneration++
				case "conflict":
					r = meta.MQTTSessionReclamationResult{Status: meta.MQTTSessionCASConflict, CurrentRevision: m.ExpectedRevision + 1}
				case "unknown":
					return r, errors.New("lost reply")
				case "late":
					cancel()
				case "panic":
					panic("secret")
				case "wrong revision":
					r.CurrentRevision++
				case "wrong marker":
					r.ReclaimedThroughGeneration++
				case "no progress":
					r.Done = false
					r.ReclaimedThroughGeneration = 0
				case "extra removals":
					r.RemovedSubscriptions = 65
				case "bad status":
					r.Status = 0
				}
				return r, nil
			}
			r, e := app.NewSessionReclamation(o)
			require.NoError(t, e)
			out, e := r.Reconcile(ctx, k)
			require.Len(t, s.writes, 1)
			switch fault {
			case "partial":
				require.NoError(t, e)
				require.True(t, out.Changed)
				require.False(t, out.Completed)
			case "complete":
				require.NoError(t, e)
				require.True(t, out.Changed && out.Completed)
			case "unchanged":
				require.NoError(t, e)
				require.False(t, out.Changed)
				require.True(t, out.Completed)
			default:
				require.Error(t, e)
				require.Zero(t, out)
			}
		})
	}
}
func TestSessionReclamationNoDebtAndInvalidOptions(t *testing.T) {
	_, s, o, k := reclamationFixture(t)
	for _, state := range []string{"absent", "live first", "completed"} {
		s.query = func(context.Context, meta.MQTTRead) (meta.MQTTReadResult, error) {
			r, e := s.sessionStore.ReadMQTT(context.Background(), meta.MQTTRead{Kind: meta.MQTTReadSession, Namespace: k.Namespace, ClientID: k.ClientID})
			switch state {
			case "absent":
				r.Session = nil
			case "live first":
				r.Session.State = meta.MQTTSessionOffline
				r.Session.TerminationReason = 0
				r.Session.OfflineExpiresAtMS = r.Session.UpdatedAtMS + 1000
			case "completed":
				r.Session.ReclaimedThroughGeneration = r.Session.Generation
			}
			return r, e
		}
		o.Ender = pendingEstablishmentEnder(func(context.Context, app.EndCommand) error { t.Fatal("no debt isolated"); return nil })
		r, e := app.NewSessionReclamation(o)
		require.NoError(t, e)
		out, e := r.Reconcile(context.Background(), k)
		require.NoError(t, e)
		require.Zero(t, out)
		require.Empty(t, s.writes)
	}
	for _, mutate := range []func(*app.SessionReclamationOptions){func(o *app.SessionReclamationOptions) { o.Store = nil }, func(o *app.SessionReclamationOptions) { o.Ender = nil }, func(o *app.SessionReclamationOptions) { o.Timeout = -1 }, func(o *app.SessionReclamationOptions) { o.Timeout = 6 * time.Second }} {
		bad := o
		mutate(&bad)
		_, e := app.NewSessionReclamation(bad)
		require.Error(t, e)
	}
}

func TestConnectRetainsReclamationMarkerAcrossFreshLifetimes(t *testing.T) {
	for _, mode := range []string{"ended", "clean start", "expired offline", "zero active"} {
		t.Run(mode, func(t *testing.T) {
			f, _, o, k := reclamationFixture(t)
			var current app.Connection
			var e error
			if mode != "ended" {
				c := command()
				c.SessionExpirySec = 1
				if mode == "zero active" {
					c.SessionExpirySec = 0
				}
				current, e = f.service.Connect(context.Background(), c)
				require.NoError(t, e)
			}
			r, e := app.NewSessionReclamation(o)
			require.NoError(t, e)
			result, e := r.Reconcile(context.Background(), k)
			require.NoError(t, e)
			require.True(t, result.Completed)
			before := f.row(t)
			require.EqualValues(t, 1, before.ReclaimedThroughGeneration)
			if mode == "expired offline" {
				require.NoError(t, f.service.Disconnect(context.Background(), app.DisconnectCommand{Owner: current.Owner, Normal: true}))
				f.now = f.now.Add(2 * time.Second)
			}
			next := command()
			next.CleanStart = mode == "clean start"
			connected, e := f.service.Connect(context.Background(), next)
			require.NoError(t, e, "fresh CONNECT must preserve the durable cleanup boundary")
			require.False(t, connected.SessionPresent)
			require.Equal(t, before.Generation+1, connected.Owner.SessionGeneration)
			require.EqualValues(t, 1, f.row(t).ReclaimedThroughGeneration)
			scope, e := f.owners.Begin(context.Background(), connected.Owner)
			require.NoError(t, e)
			scope.Done()
		})
	}
}

// A normal DISCONNECT can finish after CONNECT's isolation/read but before its
// proposal. Rebase only a definite rejection under that same exact old Owner.
func TestConnectRebasesDefiniteConflictWithoutExtendingCandidate(t *testing.T) {
	for _, clean := range []bool{false, true} {
		t.Run(map[bool]string{false: "resume", true: "clean start"}[clean], func(t *testing.T) {
			f := setup(t)
			ctx := context.Background()
			first, e := f.service.Connect(ctx, command())
			require.NoError(t, e)
			started := f.now
			calls := 0
			f.store.before = func(m meta.MQTTLifecycleMutation) {
				if m.Event != meta.MQTTLifecycleConnect {
					return
				}
				calls++
				if calls == 1 {
					require.NoError(t, f.service.Disconnect(ctx, app.DisconnectCommand{Owner: first.Owner, Normal: true}))
					f.now = f.now.Add(time.Second)
				}
			}
			c := command()
			c.CleanStart = clean
			connected, e := f.service.Connect(ctx, c)
			require.NoError(t, e)
			require.Equal(t, 2, calls)
			require.Equal(t, started.Add(f.opts.LeaseDuration), connected.Lease.Until)
			require.Equal(t, !clean, connected.SessionPresent)
			require.Equal(t, first.Owner.OwnerGeneration+1, connected.Owner.OwnerGeneration)
			require.EqualValues(t, 2, connected.Owner.ConnectionID, "retry reserves no additional candidate")
			scope, e := f.owners.Begin(ctx, connected.Owner)
			require.NoError(t, e)
			scope.Done()
		})
	}
}
func TestConnectConflictRebaseRemainsBoundedAndPinned(t *testing.T) {
	for _, fault := range []string{"three conflicts", "unknown write", "successor", "freshness change", "cancel", "clock regression", "lease expired"} {
		t.Run(fault, func(t *testing.T) {
			f := setup(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			first, e := f.service.Connect(ctx, command())
			require.NoError(t, e)
			calls := 0
			var successor app.Connection
			f.store.before = func(m meta.MQTTLifecycleMutation) {
				if m.Event != meta.MQTTLifecycleConnect {
					return
				}
				calls++
				if fault == "unknown write" {
					return
				}
				switch fault {
				case "successor":
					f.store.before = nil
					successor, e = f.service.Connect(ctx, command())
					require.NoError(t, e)
				case "freshness change":
					require.NoError(t, f.service.End(ctx, app.EndCommand{Owner: first.Owner, Reason: meta.MQTTSessionExplicit}))
				default:
					row := f.row(t)
					row.Revision++
					_, e := f.store.CompareAndSwapMQTTSession(ctx, row.Revision-1, row)
					require.NoError(t, e)
				}
				switch fault {
				case "cancel":
					cancel()
				case "clock regression":
					f.now = f.now.Add(-time.Second)
				case "lease expired":
					f.now = f.now.Add(f.opts.LeaseDuration)
				}
			}
			if fault == "unknown write" {
				f.store.after = func(m meta.MQTTLifecycleMutation, r meta.MQTTLifecycleResult) (meta.MQTTLifecycleResult, error) {
					if m.Event == meta.MQTTLifecycleConnect {
						return r, errors.Join(app.ErrConflict, errors.New("reply uncertain"))
					}
					return r, nil
				}
			}
			_, e = f.service.Connect(ctx, command())
			require.Error(t, e)
			want := 1
			if fault == "three conflicts" {
				want = 3
			}
			require.Equal(t, want, calls)
			if fault == "successor" {
				scope, e := f.owners.Begin(ctx, successor.Owner)
				require.NoError(t, e)
				scope.Done()
			}
		})
	}
}
