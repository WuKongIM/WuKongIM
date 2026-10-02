package mqttsession_test

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	owner "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	app "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestEndSessionWaitsForOwnerThenResolvesWillAndPreventsResume(t *testing.T) {
	f := setup(t)
	c := command()
	c.Will = will(t)
	closed := make(chan struct{})
	c.CloseTransport = func(context.Context) error { close(closed); return nil }
	connection, err := f.service.Connect(context.Background(), c)
	require.NoError(t, err)
	before := f.row(t)
	op, err := f.owners.Begin(context.Background(), connection.Owner)
	require.NoError(t, err)
	defer op.Done()
	done := make(chan error, 1)
	go func() {
		done <- f.service.End(context.Background(), app.EndCommand{Owner: connection.Owner, Reason: meta.MQTTSessionRevoked})
	}()
	<-closed
	select {
	case err := <-done:
		t.Fatalf("ended before admitted effects drained: %v", err)
	default:
	}
	require.Equal(t, before, f.row(t))
	_, err = f.owners.Begin(context.Background(), connection.Owner)
	require.ErrorIs(t, err, owner.ErrOwnerFenced)
	op.Done()
	require.NoError(t, <-done)
	ended := f.row(t)
	require.Equal(t, meta.MQTTSessionEnded, ended.State)
	require.Equal(t, meta.MQTTSessionRevoked, ended.TerminationReason)
	require.Zero(t, ended.WillGeneration)
	w := readEndWill(t, f, connection)
	require.Equal(t, meta.MQTTWillReady, w.Stage)
	require.Equal(t, f.now.UnixMilli(), w.DisconnectedAtMS)
	require.Equal(t, w.DisconnectedAtMS, w.DueAtMS)
	require.NoError(t, f.service.End(context.Background(), app.EndCommand{Owner: connection.Owner, Reason: meta.MQTTSessionSourceLost}))
	require.Equal(t, ended, f.row(t))
	require.Equal(t, w, readEndWill(t, f, connection))
	fresh, err := f.service.Connect(context.Background(), command())
	require.NoError(t, err)
	require.False(t, fresh.SessionPresent)
	require.Equal(t, connection.Owner.SessionGeneration+1, fresh.Owner.SessionGeneration)
	require.ErrorIs(t, f.service.End(context.Background(), app.EndCommand{Owner: connection.Owner, Reason: meta.MQTTSessionRevoked}), app.ErrFenced)
	c = command()
	c.UID = "other"
	_, err = f.service.Connect(context.Background(), c)
	require.ErrorIs(t, err, app.ErrBinding)
}

func TestEndSessionRetainsUnfinishedDeliveryResponsibility(t *testing.T) {
	f, _, pending := setupExchangeRecovery(t, true)
	before := f.row(t)
	cursor := readAcknowledgementCursor(t, f.groupSourceFixture, f.key)
	want := make([][]meta.MQTTInflight, len(pending))
	for i, p := range pending {
		r, err := f.window.ReadMQTT(context.Background(), meta.MQTTRead{Kind: meta.MQTTReadInflight, Namespace: f.key.Namespace, ClientID: f.key.ClientID, SessionGeneration: f.key.SessionGeneration, PacketID: p.Exchange.PacketID})
		require.NoError(t, err)
		want[i] = r.Inflight // Later admission has already updated the earlier links.
	}
	require.NoError(t, f.service.End(context.Background(), app.EndCommand{Owner: f.connection.Owner, Reason: meta.MQTTSessionSourceLost}))
	after := f.row(t)
	require.Equal(t, before.PendingMessages, after.PendingMessages)
	require.Equal(t, before.PendingBytes, after.PendingBytes)
	require.Equal(t, before.OutboundInflight, after.OutboundInflight)
	require.Equal(t, before.NextPacketID, after.NextPacketID)
	require.Equal(t, before.NextDeliveryOrder, after.NextDeliveryOrder)
	require.Equal(t, cursor, readAcknowledgementCursor(t, f.groupSourceFixture, f.key))
	for i, p := range pending {
		r, err := f.window.ReadMQTT(context.Background(), meta.MQTTRead{Kind: meta.MQTTReadInflight, Namespace: f.key.Namespace, ClientID: f.key.ClientID, SessionGeneration: f.key.SessionGeneration, PacketID: p.Exchange.PacketID})
		require.NoError(t, err)
		require.Equal(t, want[i], r.Inflight)
	}
}

func TestEndSessionPreservesWillClocksAndDetachedDecisions(t *testing.T) {
	for _, state := range []string{"waiting", "normal", "ready", "expired_active", "slow_isolation"} {
		t.Run(state, func(t *testing.T) {
			f := setup(t)
			c := command()
			c.Will = will(t)
			connection, err := f.service.Connect(context.Background(), c)
			require.NoError(t, err)
			lease := f.row(t).LeaseUntilMS
			if state == "waiting" || state == "normal" || state == "ready" {
				require.NoError(t, f.service.Disconnect(context.Background(), app.DisconnectCommand{Owner: connection.Owner, Normal: state == "normal"}))
				f.now = f.now.Add(time.Second)
			}
			if state == "ready" {
				f.now = f.now.Add(5 * time.Second)
				require.NoError(t, f.service.ReconcileDeadline(context.Background(), connection.Owner))
			}
			if state == "expired_active" {
				f.now = f.now.Add(20 * time.Second)
			}
			before := readEndWill(t, f, connection)
			observed := f.now.UnixMilli()
			if state == "slow_isolation" {
				f.opts.Isolation = isolation(func(ctx context.Context, o contract.Owner) error {
					err := f.owners.Quiesce(ctx, o)
					f.now = f.now.Add(20 * time.Second)
					return err
				})
				f.service, err = app.New(f.opts)
				require.NoError(t, err)
			}
			require.NoError(t, f.service.End(context.Background(), app.EndCommand{Owner: connection.Owner, Reason: meta.MQTTSessionExplicit}))
			got := readEndWill(t, f, connection)
			switch state {
			case "normal", "ready":
				require.Equal(t, before, got)
			case "waiting":
				require.Equal(t, before.DisconnectedAtMS, got.DisconnectedAtMS)
				require.Equal(t, observed, got.DueAtMS)
			case "expired_active":
				require.Equal(t, lease, got.DisconnectedAtMS)
				require.Equal(t, lease+5000, got.DueAtMS)
			case "slow_isolation":
				require.Equal(t, observed, got.DisconnectedAtMS)
				require.Equal(t, observed, got.DueAtMS)
			}
		})
	}
}

func TestEndSessionFailureNeverAuthorizesAnotherEffect(t *testing.T) {
	for _, fault := range []string{"invalid_reason", "canceled", "missing", "foreign", "partial", "exhausted", "clock", "wall_clock", "unproved", "successor", "cancel_read", "cancel_isolation", "panic_read", "panic_isolation", "lost_reply", "bad_receipt", "live_will_receipt", "conflict", "panic_commit", "cancel_commit"} {
		t.Run(fault, func(t *testing.T) {
			f := setup(t)
			connection, err := f.service.Connect(context.Background(), command())
			require.NoError(t, err)
			before := f.row(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			q := app.EndCommand{Owner: connection.Owner, Reason: meta.MQTTSessionRevoked}
			committed := false
			switch fault {
			case "invalid_reason":
				q.Reason = meta.MQTTSessionCleanStart
			case "canceled":
				cancel()
			case "clock":
				f.now = f.now.Add(-time.Second)
			case "wall_clock":
				f.now = f.now.Round(0)
			case "missing", "foreign", "partial", "exhausted", "cancel_read", "panic_read":
				f.store.read = func(r meta.MQTTReadResult) meta.MQTTReadResult {
					switch fault {
					case "missing":
						r.Session = nil
					case "foreign":
						r.Session.UID = "foreign"
						r.Session.ClientID = "foreign"
					case "partial":
						r.Done = false
					case "exhausted":
						r.Session.Revision = math.MaxUint64
					case "cancel_read":
						cancel()
					case "panic_read":
						panic("secret")
					}
					return r
				}
			case "unproved", "successor", "cancel_isolation", "panic_isolation":
				f.opts.Isolation = isolation(func(c context.Context, o contract.Owner) error {
					switch fault {
					case "unproved":
						return owner.ErrOwnerUnknown
					case "panic_isolation":
						panic("secret")
					case "cancel_isolation":
						cancel()
						return nil
					default:
						_, err := f.service.Connect(c, command())
						return err
					}
				})
			default:
				committed = true
				f.store.after = func(_ meta.MQTTLifecycleMutation, r meta.MQTTLifecycleResult) (meta.MQTTLifecycleResult, error) {
					switch fault {
					case "lost_reply":
						return r, errors.New("lost end reply")
					case "bad_receipt":
						r.CurrentRevision++
					case "live_will_receipt":
						r.WillGeneration = 1
					case "conflict":
						r.Status = meta.MQTTSessionCASConflict
					case "panic_commit":
						panic("secret")
					case "cancel_commit":
						cancel()
					}
					return r, nil
				}
			}
			service, err := app.New(f.opts)
			if fault == "wall_clock" {
				service = f.service
			} else {
				require.NoError(t, err)
			}
			err = service.End(ctx, q)
			require.Error(t, err)
			require.NotContains(t, err.Error(), "secret")
			f.store.read, f.store.after = nil, nil
			if committed {
				require.Equal(t, meta.MQTTSessionEnded, f.row(t).State)
				require.Equal(t, before.Revision+1, f.row(t).Revision)
				require.NoError(t, f.service.End(context.Background(), q))
				require.Equal(t, before.Revision+1, f.row(t).Revision)
			} else if fault == "successor" {
				require.Equal(t, meta.MQTTSessionActive, f.row(t).State)
				require.Equal(t, connection.Owner.OwnerGeneration+1, f.row(t).OwnerGeneration)
			} else {
				require.Equal(t, before, f.row(t))
			}
		})
	}
}

func readEndWill(t *testing.T, f *fixture, c app.Connection) meta.MQTTWill {
	t.Helper()
	r, err := f.store.ReadMQTT(context.Background(), meta.MQTTRead{Kind: meta.MQTTReadWill, WillKey: meta.MQTTWillKey{Namespace: c.Owner.Key.Namespace, ClientID: c.Owner.Key.ClientID, SessionGeneration: c.Owner.SessionGeneration, WillGeneration: c.WillGeneration}})
	require.NoError(t, err)
	require.Len(t, r.Wills, 1)
	return r.Wills[0]
}
