//go:build integration

package replication_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/bench/counterwindow"
	messagedb "github.com/WuKongIM/WuKongIM/pkg/db/message"
	"github.com/WuKongIM/WuKongIM/pkg/metrics"
)

// Setup is excluded by subtraction without resetting a live observer. The
// original benchmark's commit accumulator remains independently resettable.
func TestAppendCounterWindowSeparatesSetupAndPhysicalTail(t *testing.T) {
	m := metrics.New(0, "benchmark-cluster")
	observer := &durableQuorumCommitObserver{counters: m.Storage}
	observe := func(d time.Duration) {
		observer.ObserveCommitCoordinatorBatch(messagedb.CommitCoordinatorBatchEvent{Requests: 1, Records: 1, CommitDuration: d, TotalDuration: d})
	}
	observe(time.Second)
	observer.reset()
	dir := t.TempDir()
	finish := counterwindow.Start(t, dir, "channel-append-counters/v1", "test boundary", 3000, 500, m.PrometheusRegistry())
	observe(450 * time.Millisecond)
	m.ChannelRuntime.ObserveReplicationStage("peer_foreground_exchange", "ok", 5*time.Millisecond)
	finish()
	var before, after counterwindow.Snapshot
	for name, target := range map[string]*counterwindow.Snapshot{"before": &before, "after": &after} {
		data, err := os.ReadFile(filepath.Join(dir, name+".json"))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, target); err != nil {
			t.Fatal(err)
		}
	}
	physical := func(s counterwindow.Snapshot) (uint64, float64) {
		for _, f := range s.Families {
			if f.GetName() != "wukongim_storage_commit_batch_duration_seconds" {
				continue
			}
			for _, m := range f.Metric {
				for _, l := range m.Label {
					if l.GetName() == "stage" && l.GetValue() == "commit" {
						return m.Histogram.GetSampleCount(), m.Histogram.GetSampleSum()
					}
				}
			}
		}
		t.Fatal("missing physical commit histogram")
		return 0, 0
	}
	n0, s0 := physical(before)
	n1, s1 := physical(after)
	if n0 != 1 || n1-n0 != 1 || s1-s0 < .449999 || s1-s0 > .450001 {
		t.Fatalf("incorrect physical delta: %d/%d %g/%g", n0, n1, s0, s1)
	}
	original := observer.snapshot()
	if original.batches != 1 || original.commit != 450*time.Millisecond {
		t.Fatalf("original accumulator changed: %+v", original)
	}
	found := false
	for _, f := range after.Families {
		if f.GetName() == "wukongim_channelv2_replication_stage_duration_seconds" {
			found = true
		}
	}
	if !found {
		t.Fatal("missing foreground replication stage")
	}
}
