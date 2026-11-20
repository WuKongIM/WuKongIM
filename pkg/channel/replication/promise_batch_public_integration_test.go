//go:build integration

package replication_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/replication"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
)

// This public storage resource contract counts physical WAL syncs, not method
// calls. Already-admitted promises share one engine and retain their durable
// authority checks. Reopen and lower-authority rejection guard against a false
// improvement obtained by omitting writes or acknowledging them early.
func TestPromiseBatchDurabilityDoesNotScaleSyncsWithChannels(t *testing.T) {
	report := runPromiseBatchProbe(t.TempDir())
	if path := os.Getenv("WK_TEST_PROMISE_REPORT"); path != "" {
		data, err := json.MarshalIndent(report, "", "  ")
		if err == nil {
			var file *os.File
			file, err = os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if err == nil {
				_, err = file.Write(append(data, '\n'))
				if closeErr := file.Close(); err == nil {
					err = closeErr
				}
			}
		}
		if err != nil {
			report.Completed, report.ResourceRED = false, false
			report.Error = "artifact publication failed"
		}
	}
	data, _ := json.Marshal(report)
	t.Log(string(data))
	if !report.Completed {
		t.Fatal("invalid promise-batch evidence: " + report.Error)
	}
	if report.ResourceRED {
		t.Fatalf("64 admitted promises used %d WAL syncs; fixed budget is %d", report.FreshSyncs, report.SyncBudget)
	}
}

type promiseBatchReport struct {
	Channels        int    `json:"channels"`
	SyncBudget      uint64 `json:"sync_budget"`
	FreshSyncs      uint64 `json:"fresh_syncs"`
	FreshWallNanos  int64  `json:"fresh_wall_nanos"`
	RepeatSyncs     uint64 `json:"repeat_syncs"`
	ReopenChecked   int    `json:"reopen_checked"`
	StaleRejected   int    `json:"stale_rejected"`
	FactoriesClosed int    `json:"factories_closed"`
	Completed       bool   `json:"completed"`
	ResourceRED     bool   `json:"resource_red"`
	Error           string `json:"error,omitempty"`
}

func runPromiseBatchProbe(path string) (report promiseBatchReport) {
	report.Channels, report.SyncBudget = 64, 4
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	factory := store.NewMessageDBFactory(path)
	defer func() {
		if factory != nil {
			if err := factory.Close(); err != nil {
				report.Completed, report.ResourceRED = false, false
				report.Error = "factory cleanup failed"
			} else {
				report.FactoriesClosed++
			}
		}
	}()
	makeAdapter := func() (replication.ReplicaStore, error) {
		return replication.NewStoreAdapter(replication.StoreAdapterConfig{Factory: factory, MaxBatchItems: 64, MaxBatchBytes: 64 << 10})
	}
	adapter, err := makeAdapter()
	if err != nil {
		report.Error = "adapter construction failed"
		return
	}
	batch := replication.LoadBatch{Items: make([]replication.LoadRequest, report.Channels)}
	for i := range batch.Items {
		name := fmt.Sprintf("promise-batch-%02d", i)
		batch.Items[i] = replication.LoadRequest{ChannelKey: ch.ChannelKey("1:" + name), ChannelID: ch.ChannelID{ID: name, Type: 1}, Promise: replication.AuthorityID{ChannelEpoch: 1, LeaderTerm: 2, FenceVersion: 1}}
	}
	check := func(result replication.LoadBatchResult, err error) bool {
		if err != nil || len(result.Items) != report.Channels {
			return false
		}
		for _, item := range result.Items {
			if item.Err != nil || item.State.LEO != 0 || item.State.Committed != 0 || len(item.Entries) != 0 {
				return false
			}
		}
		return true
	}
	before := factory.MetricsSnapshot().WALFsyncCount
	start := time.Now()
	result, err := adapter.Load(ctx, batch)
	report.FreshWallNanos = time.Since(start).Nanoseconds()
	after := factory.MetricsSnapshot().WALFsyncCount
	if !check(result, err) || after <= before {
		report.Error = "fresh durable result or WAL observation invalid"
		return
	}
	report.FreshSyncs = after - before
	result, err = adapter.Load(ctx, batch)
	next := factory.MetricsSnapshot().WALFsyncCount
	if !check(result, err) || next < after {
		report.Error = "equal promise result or metric invalid"
		return
	}
	report.RepeatSyncs = next - after
	if report.RepeatSyncs != 0 {
		report.Error = "equal durable promises caused new WAL syncs"
		return
	}
	if factory.ChannelEntryMetricsSnapshot().ActiveEntries != 0 {
		report.Error = "load retained active channel leases"
		return
	}
	if err := factory.Close(); err != nil {
		report.Error = "first factory close failed"
		return
	}
	report.FactoriesClosed++
	factory = store.NewMessageDBFactory(path)
	adapter, err = makeAdapter()
	if err != nil {
		report.Error = "reopened adapter construction failed"
		return
	}
	for _, item := range batch.Items {
		channel, err := factory.ChannelStore(item.ChannelKey, item.ChannelID)
		if err != nil {
			report.Error = "reopened channel unavailable"
			return
		}
		promiser, ok := channel.(store.AuthorityPromiser)
		if !ok {
			_ = channel.Close()
			report.Error = "reopened promise interface unavailable"
			return
		}
		promise, found, readErr := promiser.AuthorityPromise(ctx)
		closeErr := channel.Close()
		if readErr != nil || closeErr != nil || !found || promise != (store.AuthorityFence{ChannelEpoch: 1, LeaderTerm: 2, FenceVersion: 1}) {
			report.Error = "durable promise did not survive reopen"
			return
		}
		report.ReopenChecked++
	}
	for i := range batch.Items {
		batch.Items[i].Promise.LeaderTerm = 1
	}
	result, err = adapter.Load(ctx, batch)
	if err != nil || len(result.Items) != report.Channels {
		report.Error = "stale authority batch result invalid"
		return
	}
	for _, item := range result.Items {
		if item.Err != ch.ErrStaleMeta {
			report.Error = "stale authority was not rejected per item"
			return
		}
		report.StaleRejected++
	}
	report.Completed = true
	report.ResourceRED = report.FreshSyncs > report.SyncBudget
	return
}
