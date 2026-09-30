//go:build e2e

package medium_recipient_hotpath

import (
	"encoding/json"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"strings"
	"testing"
	"time"
)

// A bounded history reuses the existing sampler; it never makes an HTTP call.
// pressureSampler.mu protects recording. Log only after the sampler joins.
type permissionSoakStorageHistory struct {
	Total  int                           `json:"total"`
	Recent []permissionSoakStorageSample `json:"recent"`
}
type permissionSoakStorageSample struct {
	NodeID        uint64  `json:"node_id"`
	StartedUnixMS int64   `json:"started_unix_ms"`
	FetchMS       float64 `json:"fetch_ms"`
	Failed        bool    `json:"failed"`
	// Missing metrics remain absent; scrape errors must not become zero values.
	Values map[string]float64 `json:"values"`
}

func (h *permissionSoakStorageHistory) record(nodeID uint64, started time.Time, elapsed time.Duration, samples []suite.MetricSample, err error) {
	r := permissionSoakStorageSample{NodeID: nodeID, StartedUnixMS: started.UnixMilli(), FetchMS: float64(elapsed) / float64(time.Millisecond), Failed: err != nil, Values: make(map[string]float64)}
	if err == nil {
		for _, sample := range samples {
			store := sample.Labels["store"]
			if store != "channel_log" && store != "meta" {
				continue
			}
			name := strings.TrimPrefix(sample.Name, "wukongim_storage_pebble_")
			switch name {
			case "memtable_count", "memtable_size_bytes", "read_amplification", "flush_count", "flushes_in_progress", "flush_bytes_written", "compaction_count", "compactions_in_progress", "compaction_bytes_read", "compaction_bytes_written", "compaction_estimated_debt_bytes", "compaction_in_progress_bytes", "wal_bytes_in", "wal_bytes_written", "sstable_size_bytes", "disk_usage_bytes":
				r.Values[store+"/"+name] = sample.Value
			}
		}
	}
	h.Total++
	if len(h.Recent) == 180 {
		copy(h.Recent, h.Recent[1:])
		h.Recent = h.Recent[:179]
	}
	h.Recent = append(h.Recent, r)
}
func (h *permissionSoakStorageHistory) log(t *testing.T) {
	if h == nil {
		return
	}
	data, err := json.Marshal(h)
	if err != nil {
		t.Errorf("marshal storage history: %v", err)
		return
	}
	t.Logf("WKRC-PERMISSION-SOAK-STORAGE %s", data)
}
