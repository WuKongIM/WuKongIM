//go:build integration

package mqttsession

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	gr "github.com/WuKongIM/WuKongIM/pkg/goroutine"
	"github.com/stretchr/testify/require"
)

func TestConsumerWorkerBoundedCohortJoinedStopAndFreshRestart(t *testing.T) {
	s := &deadlineSource{slots: []meta.HashSlot{0}}
	s.read = func(_ uint16, q meta.MQTTRead) (meta.MQTTReadResult, error) {
		var rows []meta.MQTTSourceBinding
		for i := range 12 {
			r := consumerBinding(fmt.Sprintf("%02d", i), 5000)
			if compareConsumerCursor(q.After.SourceRecovery, consumerCursor(r).SourceRecovery) < 0 {
				rows = append(rows, r)
			}
		}
		if len(rows) == 0 {
			return meta.MQTTReadResult{After: q.After, Done: true}, nil
		}
		return meta.MQTTReadResult{Bindings: rows, After: consumerCursor(rows[len(rows)-1]), Done: true}, nil
	}
	entered, cancelled := make(chan meta.MQTTSourceBindingKey, 16), make(chan struct{}, 16)
	release := make(chan struct{})
	var calls atomic.Int32
	executor := consumerWorkFunc(func(ctx context.Context, k meta.MQTTSourceBindingKey) (ConsumerWork, error) {
		calls.Add(1)
		entered <- k
		select {
		case <-ctx.Done():
			cancelled <- struct{}{}
			<-release
			return ConsumerWork{}, ctx.Err()
		case <-release:
			return ConsumerWork{}, nil
		}
	})
	registry := gr.New()
	w, err := NewConsumerWorker(ConsumerWorkerOptions{Source: s, Maintainer: executor, Workers: 4, Registry: registry, Interval: 10 * time.Millisecond})
	require.NoError(t, err)
	start, cancelStart := context.WithCancel(context.Background())
	defer cancelStart()
	require.NoError(t, w.Start(start))
	require.NoError(t, w.Start(start))
	seen := map[meta.MQTTSourceBindingKey]bool{}
	for range 4 {
		select {
		case k := <-entered:
			require.False(t, seen[k])
			seen[k] = true
		case <-time.After(2 * time.Second):
			t.Fatal("cohort did not start")
		}
	}
	cancelStart()
	select {
	case <-cancelled:
		t.Fatal("startup context became runtime lifetime")
	default:
	}
	stop, cancelStop := context.WithCancel(context.Background())
	cancelStop()
	require.ErrorIs(t, w.Stop(stop), context.Canceled)
	for range 4 {
		select {
		case <-cancelled:
		case <-time.After(time.Second):
			t.Fatal("work was not cancelled")
		}
	}
	require.ErrorIs(t, w.Start(context.Background()), ErrConsumerWorkerStopping)
	require.EqualValues(t, 4, calls.Load(), "queue or discovery exceeded the fixed cohort")
	close(release)
	joined, done := context.WithTimeout(context.Background(), 3*time.Second)
	defer done()
	require.NoError(t, w.Stop(joined))
	require.NoError(t, registry.Group(gr.ModuleMQTT).Wait(joined))
	before := len(s.queries)
	require.NoError(t, w.Start(context.Background()))
	select {
	case <-entered:
	case <-joined.Done():
		t.Fatal("restart did not execute")
	}
	require.NoError(t, w.Stop(joined))
	require.NoError(t, registry.Group(gr.ModuleMQTT).Wait(joined))
	require.Equal(t, meta.MQTTReadCursor{}, s.queries[before].After)
	require.Zero(t, registry.Snapshot().ManagedTotal)
	t.Log("Consumer scheduling: max_admitted=4 stop_joined=true no_overlap=true restart_cursor_reset=true")
}

func TestConsumerWorkerSharesCohortWithPendingSubscriptions(t *testing.T) {
	s := &deadlineSource{slots: []meta.HashSlot{0}}
	s.read = func(_ uint16, q meta.MQTTRead) (meta.MQTTReadResult, error) {
		if q.Kind == meta.MQTTReadSourceRecovery {
			return meta.MQTTReadResult{Bindings: []meta.MQTTSourceBinding{consumerBinding("a", 1000)}, Done: true, After: q.After}, nil
		}
		return meta.MQTTReadResult{Subscriptions: []meta.MQTTSubscription{consumerSubscription("b", 2000)}, Done: true, After: q.After}, nil
	}
	entered := make(chan string, 4)
	cancelled := make(chan string, 4)
	release := make(chan struct{})
	var calls atomic.Int32
	work := func(ctx context.Context, kind string) error {
		calls.Add(1)
		entered <- kind
		<-ctx.Done()
		cancelled <- kind
		<-release
		return ctx.Err()
	}
	registry := gr.New()
	w, err := NewConsumerWorker(ConsumerWorkerOptions{Source: s, Workers: 2, Registry: registry, Interval: 10 * time.Millisecond,
		Maintainer: consumerWorkFunc(func(ctx context.Context, _ meta.MQTTSourceBindingKey) (ConsumerWork, error) {
			return ConsumerWork{}, work(ctx, "source")
		}),
		Subscriptions: consumerSubscriptionFunc(func(ctx context.Context, k meta.MQTTSubscriptionRecoveryCursor) error {
			if k.RecoveryAtMS != 0 {
				return fmt.Errorf("timestamp entered identity")
			}
			return work(ctx, "subscription")
		})})
	require.NoError(t, err)
	require.NoError(t, w.Start(context.Background()))
	seen := map[string]bool{}
	for range 2 {
		select {
		case kind := <-entered:
			seen[kind] = true
		case <-time.After(2 * time.Second):
			t.Fatal("shared cohort did not run both kinds")
		}
	}
	require.True(t, seen["source"] && seen["subscription"])
	stop, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, w.Stop(stop), context.Canceled)
	for range 2 {
		select {
		case <-cancelled:
		case <-time.After(time.Second):
			t.Fatal("work did not cancel")
		}
	}
	require.ErrorIs(t, w.Start(context.Background()), ErrConsumerWorkerStopping)
	require.EqualValues(t, 2, calls.Load())
	close(release)
	joined, done := context.WithTimeout(context.Background(), 2*time.Second)
	defer done()
	require.NoError(t, w.Stop(joined))
	require.NoError(t, registry.Group(gr.ModuleMQTT).Wait(joined))
	require.Zero(t, registry.Snapshot().ManagedTotal)
	t.Log("sources_and_subscriptions=true max_admitted=2 joined_stop=true no_overlap=true")
}
