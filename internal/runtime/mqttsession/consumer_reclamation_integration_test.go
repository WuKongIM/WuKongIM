//go:build integration

package mqttsession

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestConsumerReclamationSharesCohortAndJoinsStop(t *testing.T) {
	s := &deadlineSource{slots: []meta.HashSlot{0}}
	s.read = func(_ uint16, q meta.MQTTRead) (meta.MQTTReadResult, error) {
		switch q.Kind {
		case meta.MQTTReadSessionReclamation:
			r := consumerReclamationRow("r")
			return meta.MQTTReadResult{Sessions: []meta.MQTTSession{r}, After: consumerReclamationCursor(r), Done: true}, nil
		case meta.MQTTReadSubscriptionRecovery:
			return meta.MQTTReadResult{Subscriptions: []meta.MQTTSubscription{consumerSubscription("s", 1000)}, After: q.After, Done: true}, nil
		default:
			return meta.MQTTReadResult{Bindings: []meta.MQTTSourceBinding{consumerBinding("b", 1000)}, After: q.After, Done: true}, nil
		}
	}
	w := consumerWorkerFixture(t, s)
	enableReclamationWork(w)
	w.opts.Workers = 3
	w.opts.Interval = 10 * time.Millisecond
	entered, cancelled := make(chan string, 6), make(chan string, 6)
	release := make(chan struct{})
	var calls atomic.Int32
	run := func(c context.Context, k string) error {
		calls.Add(1)
		entered <- k
		<-c.Done()
		cancelled <- k
		<-release
		return c.Err()
	}
	w.opts.Maintainer = consumerWorkFunc(func(c context.Context, _ meta.MQTTSourceBindingKey) (ConsumerWork, error) {
		return ConsumerWork{}, run(c, "binding")
	})
	w.opts.Subscriptions = consumerSubscriptionFunc(func(c context.Context, _ meta.MQTTSubscriptionRecoveryCursor) error { return run(c, "subscription") })
	w.opts.Reclamation = consumerReclamationFunc(func(c context.Context, _ meta.MQTTSessionCursor) (bool, error) { return true, run(c, "reclamation") })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
		_ = w.Stop(ctx)
	})
	require.NoError(t, w.Start(ctx))
	seen := map[string]bool{}
	for range 3 {
		select {
		case k := <-entered:
			seen[k] = true
		case <-ctx.Done():
			t.Fatal("shared cohort did not start")
		}
	}
	require.Len(t, seen, 3)
	require.EqualValues(t, 3, calls.Load())
	stopped, stop := context.WithCancel(context.Background())
	stop()
	require.ErrorIs(t, w.Stop(stopped), context.Canceled)
	for range 3 {
		select {
		case <-cancelled:
		case <-ctx.Done():
			t.Fatal("stop did not cancel all kinds")
		}
	}
	require.ErrorIs(t, w.Start(ctx), ErrConsumerWorkerStopping)
	close(release)
	require.NoError(t, w.Stop(ctx))
	require.EqualValues(t, 3, calls.Load())
	t.Log("mqtt_reclamation_cohort_evidence: all_three_streams=true retained_keys=3 stop_joined=true no_overlap=true")
}
func TestConsumerReclamationLateAndPanicDoNotConfirmOrLeakAdmission(t *testing.T) {
	for _, fault := range []string{"panic", "late", "success"} {
		t.Run(fault, func(t *testing.T) {
			s := &deadlineSource{slots: []meta.HashSlot{0}}
			var finished atomic.Bool
			s.read = func(_ uint16, q meta.MQTTRead) (meta.MQTTReadResult, error) {
				if q.Kind != meta.MQTTReadSessionReclamation || finished.Load() {
					return meta.MQTTReadResult{Done: true, After: q.After}, nil
				}
				r := consumerReclamationRow("r")
				return meta.MQTTReadResult{Sessions: []meta.MQTTSession{r}, After: consumerReclamationCursor(r), Done: true}, nil
			}
			w := consumerWorkerFixture(t, s)
			enableReclamationWork(w)
			w.opts.Workers = 1
			w.opts.Interval = 10 * time.Millisecond
			w.opts.ExecutionTimeout = 20 * time.Millisecond
			w.opts.Reclamation = consumerReclamationFunc(func(c context.Context, _ meta.MQTTSessionCursor) (bool, error) {
				defer finished.Store(true)
				if fault == "panic" {
					panic("secret")
				}
				if fault == "late" {
					<-c.Done()
				}
				return true, nil
			})
			observed := make(chan ConsumerObservation, 8)
			w.opts.Observe = func(o ConsumerObservation) {
				if o.Completed > 0 {
					observed <- o
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			t.Cleanup(func() { _ = w.Stop(ctx) })
			require.NoError(t, w.Start(ctx))
			select {
			case o := <-observed:
				require.Zero(t, o.Admitted)
				if fault == "success" {
					require.Equal(t, 1, o.ReclamationConfirmed)
					require.Zero(t, o.Failures)
				} else {
					require.Zero(t, o.ReclamationConfirmed)
					require.Equal(t, 1, o.Failures)
				}
			case <-ctx.Done():
				t.Fatal("failed executor leaked admission")
			}
			require.NoError(t, w.Stop(ctx))
		})
	}
}
