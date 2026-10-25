//go:build integration

package app

import (
	"fmt"
	"sync"
	"testing"
	"time"

	messagedb "github.com/WuKongIM/WuKongIM/pkg/db/message"
	"github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
	obsmetrics "github.com/WuKongIM/WuKongIM/pkg/metrics"
)

// The production observer chain must emit one native observation per physical
// group, even when that commit completes multiple independently submitted jobs.
func TestStorageNativeCommitMetricsFollowGroupedMessageCommit(t *testing.T) {
	engine, err := messagedb.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	registry := obsmetrics.New(1, "native-commit")
	engine.ConfigureCommitCoordinator(messagedb.CommitCoordinatorConfig{
		FlushWindow: time.Second, QueueSize: 8, MaxRequests: 2,
		Observer: storageCommitMetricsObserver{metrics: registry},
	})
	stores := make([]*messagedb.ChannelStore, 2)
	records := make([]channelcompat.Record, 2)
	for i := range stores {
		id := fmt.Sprintf("native-commit-%d", i)
		stores[i], err = engine.ForChannel(channelcompat.ChannelKey(id+":1"), channelcompat.ChannelID{ID: id, Type: 1})
		if err != nil {
			t.Fatal(err)
		}
		defer stores[i].Close()
		records[i], err = messagedb.EncodeMessageRecord(channelcompat.Message{
			MessageID: uint64(i + 1), ChannelID: id, ChannelType: 1,
			FromUID: "sender", ClientMsgNo: fmt.Sprintf("client-%d", i), Payload: []byte("payload"),
		}, 1)
		if err != nil {
			t.Fatal(err)
		}
	}
	var joined sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan error, len(stores))
	for i := range stores {
		joined.Add(1)
		go func(i int) {
			defer joined.Done()
			<-start
			_, err := stores[i].Append([]channelcompat.Record{records[i]})
			errs <- err
		}(i)
	}
	close(start)
	joined.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	requests := findAppMetricByLabels(t, requireAppMetricFamily(t, families, "wukongim_storage_commit_batch_requests"), map[string]string{"store": "message"}).GetHistogram()
	if requests.GetSampleCount() != 1 || requests.GetSampleSum() != 2 {
		t.Fatalf("physical batch observations=%d logical requests=%g, want one group of two", requests.GetSampleCount(), requests.GetSampleSum())
	}
	durations := requireAppMetricFamily(t, families, "wukongim_storage_commit_batch_duration_seconds")
	for _, stage := range []string{
		"collect", "build", "commit", "publish", "total",
		"native_total", "native_semaphore_wait", "native_wal_queue_wait",
		"native_memtable_write_stall", "native_l0_read_amp_write_stall", "native_wal_rotation", "native_commit_wait",
	} {
		h := findAppMetricByLabels(t, durations, map[string]string{"store": "message", "result": "ok", "stage": stage}).GetHistogram()
		if h.GetSampleCount() != 1 {
			t.Fatalf("stage %s count=%d, want one physical observation", stage, h.GetSampleCount())
		}
	}
	if len(durations.GetMetric()) != 12 {
		t.Fatalf("stage series=%d, want five existing plus seven native", len(durations.GetMetric()))
	}
	for i, store := range stores {
		got, ok, err := store.GetMessageBySeq(1)
		if err != nil || !ok || got.MessageID != uint64(i+1) {
			t.Fatalf("committed message %d: got=%+v ok=%v err=%v", i, got, ok, err)
		}
	}
}
