//go:build integration

package replication_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/replication"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
)

// Real cancellation crosses admission, preparation and physical commit. Every
// result is checked against public durable state, never against a private
// callback count or a fabricated storage error.
func TestPromiseBatchCancellationDoesNotInventDurableFence(t *testing.T) {
	factory := store.NewMessageDBFactory(t.TempDir())
	adapter, err := replication.NewStoreAdapter(replication.StoreAdapterConfig{Factory: factory, MaxBatchItems: 64, MaxBatchBytes: 64 << 10})
	if err != nil {
		_ = factory.Close()
		t.Fatal(err)
	}
	ctx, stop := context.WithTimeout(context.Background(), 120*time.Second)
	defer stop()
	type sample struct {
		DelayMicros       int64 `json:"delay_us"`
		AdmissionCanceled bool  `json:"admission_canceled"`
		HigherCanceled    bool  `json:"higher_canceled"`
		LowerStale        bool  `json:"lower_stale"`
		PromisePresent    bool  `json:"promise_present"`
	}
	samples := make([]sample, 0, 128)
	invalid, inconsistent, insideCanceled, persisted := false, 0, 0, 0
	delays := []time.Duration{0, 10 * time.Microsecond, 25 * time.Microsecond, 50 * time.Microsecond, 100 * time.Microsecond, 250 * time.Microsecond, 500 * time.Microsecond, time.Millisecond}
	for cohort := 0; cohort < cap(samples); cohort++ {
		if ctx.Err() != nil {
			invalid = true
			break
		}
		items := make([]replication.LoadRequest, 64)
		for i := range items {
			name := fmt.Sprintf("cancel-%03d-%02d", cohort, i)
			id := ch.ChannelID{ID: name, Type: 1}
			items[i] = replication.LoadRequest{ChannelKey: ch.ChannelKeyForID(id), ChannelID: id, Promise: replication.AuthorityID{ChannelEpoch: 1, LeaderTerm: 3, FenceVersion: 1}}
		}
		items[1] = items[0]
		items[1].Promise.LeaderTerm = 2
		call, cancel := context.WithCancel(ctx)
		delay := delays[cohort%len(delays)]
		joined := make(chan struct{})
		go func() {
			defer close(joined)
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-timer.C:
				cancel()
			case <-call.Done():
			}
		}()
		result, loadErr := adapter.Load(call, replication.LoadBatch{Items: items})
		cancel()
		<-joined
		s := sample{DelayMicros: delay.Microseconds(), AdmissionCanceled: errors.Is(loadErr, context.Canceled)}
		if loadErr != nil && !s.AdmissionCanceled {
			invalid = true
			break
		}
		if loadErr == nil {
			if len(result.Items) != len(items) {
				invalid = true
				break
			}
			s.HigherCanceled = errors.Is(result.Items[0].Err, context.Canceled)
			s.LowerStale = errors.Is(result.Items[1].Err, ch.ErrStaleMeta)
			if s.HigherCanceled {
				insideCanceled++
			}
		}
		channel, openErr := factory.ChannelStore(items[0].ChannelKey, items[0].ChannelID)
		if openErr != nil {
			invalid = true
			break
		}
		_, present, readErr := channel.(store.AuthorityPromiser).AuthorityPromise(ctx)
		closeErr := channel.Close()
		if readErr != nil || closeErr != nil {
			invalid = true
			break
		}
		s.PromisePresent = present
		if present {
			persisted++
		}
		if s.LowerStale && !present {
			inconsistent++
		}
		samples = append(samples, s)
	}
	leases := factory.ChannelEntryMetricsSnapshot()
	closeErr := factory.Close()
	if closeErr != nil || leases.ActiveEntries != 0 || leases.OutstandingLeases != 0 || leases.BackgroundPins != 0 {
		invalid = true
	}
	report := struct {
		Samples        []sample `json:"samples"`
		Invalid        bool     `json:"invalid"`
		InsideCanceled int      `json:"inside_canceled"`
		Persisted      int      `json:"persisted"`
		Inconsistent   int      `json:"inconsistent_stale"`
	}{samples, invalid, insideCanceled, persisted, inconsistent}
	data, _ := json.Marshal(report)
	t.Log(string(data))
	if invalid || insideCanceled == 0 || persisted == 0 {
		t.Fatal("invalid cancellation coverage")
	}
	if inconsistent != 0 {
		t.Fatalf("%d canceled batches claimed a durable higher fence that does not exist", inconsistent)
	}
}
