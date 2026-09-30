package mqttsession_test

import (
	"context"
	"errors"
	"testing"
	"time"

	app "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestDeliveryIdleSkipsReadsAndRefreshesMissedWake(t *testing.T) {
	f, accounting, _ := setupWindow(t)
	f.setMessages(nil)
	now := f.now
	c, err := app.NewDeliveryCoordinator(app.DeliveryCoordinatorOptions{Sender: makeSender(t, f, f.options.Authorization), Accounting: accounting, IdleRefresh: 10 * time.Second, Now: func() time.Time { return now }})
	require.NoError(t, err)
	sent := 0
	s, err := c.Open(context.Background(), f.connection, deliverySink{enqueue: func(context.Context, app.PreparedDelivery, bool) (app.DeliveryDisposition, error) {
		sent++
		return app.DeliveryQueued, nil
	}})
	require.NoError(t, err)
	out, err := s.Turn(context.Background())
	require.NoError(t, err)
	require.False(t, out.Again)
	require.Equal(t, []string{f.key.SourceID}, out.Sources)
	reads := 0
	f.window.read = func(meta.MQTTRead, *meta.MQTTReadResult) { reads++ }
	for range 20 {
		_, err = s.Turn(context.Background())
		require.NoError(t, err)
	}
	require.Zero(t, reads, "quiet polls must not read Slot authority")
	f.setMessages([]ch.MQTTReplayPublication{{Message: ch.Message{Payload: []byte("missed hint")}}})
	now = now.Add(10 * time.Second)
	_, err = s.Turn(context.Background())
	require.NoError(t, err)
	require.Positive(t, reads)
	require.Equal(t, 1, sent, "bounded refresh must recover a missing source wake")
}

func TestDeliveryIdleWakeDuringPassCannotInstallQuietHint(t *testing.T) {
	for _, during := range []bool{false, true} {
		t.Run(map[bool]string{false: "after_pass", true: "during_pass"}[during], func(t *testing.T) {
			f, a, _ := setupWindow(t)
			f.setMessages(nil)
			c, err := app.NewDeliveryCoordinator(app.DeliveryCoordinatorOptions{Sender: makeSender(t, f, f.options.Authorization), Accounting: a, IdleRefresh: 10 * time.Second, Now: func() time.Time { return f.now }})
			require.NoError(t, err)
			s, err := c.Open(context.Background(), f.connection, deliverySink{})
			require.NoError(t, err)
			if during {
				f.window.read = func(q meta.MQTTRead, _ *meta.MQTTReadResult) {
					if q.Kind == meta.MQTTReadDeliveryCursors {
						s.NotifyDelivery()
					}
				}
			}
			_, err = s.Turn(context.Background())
			require.NoError(t, err)
			if !during {
				s.NotifyDelivery()
			}
			reads := 0
			f.window.read = func(meta.MQTTRead, *meta.MQTTReadResult) { reads++ }
			_, err = s.Turn(context.Background())
			require.NoError(t, err)
			require.Positive(t, reads)
		})
	}
}

func TestDeliveryIdleStillChecksCancellationFencingAndFreshRevocation(t *testing.T) {
	for _, mode := range []string{"cancel", "fence", "revocation", "clock_regression"} {
		t.Run(mode, func(t *testing.T) {
			f, a, _ := setupWindow(t)
			f.setMessages(nil)
			now := f.now
			c, err := app.NewDeliveryCoordinator(app.DeliveryCoordinatorOptions{Sender: makeSender(t, f, f.options.Authorization), Accounting: a, IdleRefresh: 10 * time.Second, Now: func() time.Time { return now }})
			require.NoError(t, err)
			s, err := c.Open(context.Background(), f.connection, deliverySink{})
			require.NoError(t, err)
			_, err = s.Turn(context.Background())
			require.NoError(t, err)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch mode {
			case "cancel":
				cancel()
			case "fence":
				require.NoError(t, f.owners.Fence(f.connection.Owner))
			case "revocation":
				f.denied = true
				now = now.Add(10 * time.Second)
			case "clock_regression":
				now = now.Add(-time.Second)
			}
			reads := 0
			f.window.read = func(meta.MQTTRead, *meta.MQTTReadResult) { reads++ }
			out, err := s.Turn(ctx)
			switch mode {
			case "cancel":
				require.ErrorIs(t, err, context.Canceled)
				require.Zero(t, reads)
			case "fence":
				require.NoError(t, err)
				require.True(t, out.Done)
			case "revocation":
				require.NoError(t, err)
				require.True(t, out.Done)
				require.Equal(t, meta.MQTTSessionRevoked, f.row(t).TerminationReason)
			case "clock_regression":
				require.NoError(t, err)
				require.Positive(t, reads, "regressed time cannot extend the skip window")
			}
			require.Zero(t, f.owners.Snapshot().Operations)
		})
	}
}

func TestDeliveryIdleBusyAndErrorsCannotSuppressWork(t *testing.T) {
	for _, mode := range []string{"busy", "error"} {
		t.Run(mode, func(t *testing.T) {
			f, a, _ := setupWindow(t)
			f.setMessages([]ch.MQTTReplayPublication{{Message: ch.Message{Payload: []byte("one")}}})
			c, err := app.NewDeliveryCoordinator(app.DeliveryCoordinatorOptions{Sender: makeSender(t, f, f.options.Authorization), Accounting: a, IdleRefresh: 10 * time.Second, Now: func() time.Time { return f.now }})
			require.NoError(t, err)
			busy := true
			sent := 0
			s, err := c.Open(context.Background(), f.connection, deliverySink{enqueue: func(context.Context, app.PreparedDelivery, bool) (app.DeliveryDisposition, error) {
				if busy {
					return app.DeliveryBusy, nil
				}
				sent++
				return app.DeliveryQueued, nil
			}})
			require.NoError(t, err)
			if mode == "error" {
				f.window.read = func(meta.MQTTRead, *meta.MQTTReadResult) { panic(errors.New("temporary port failure")) }
			}
			_, err = s.Turn(context.Background())
			if mode == "error" {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			busy = false
			reads := 0
			f.window.read = func(meta.MQTTRead, *meta.MQTTReadResult) { reads++ }
			_, err = s.Turn(context.Background())
			require.NoError(t, err)
			require.Positive(t, reads)
			require.Equal(t, 1, sent)
		})
	}
}
