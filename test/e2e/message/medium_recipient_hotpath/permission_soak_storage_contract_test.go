//go:build e2e

package medium_recipient_hotpath

import (
	"errors"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"testing"
	"time"
)

// Failure modes: unbounded retention, fabricated zero on failed/missing scrape,
// retaining arbitrary metric labels, and aliasing the caller's scrape buffer.
func TestPermissionSoakStorageHistoryBound(t *testing.T) {
	h := &permissionSoakStorageHistory{}
	for i := 0; i < 200; i++ {
		h.record(1, time.Unix(int64(i), 0), time.Millisecond, nil, nil)
	}
	if h.Total != 200 || len(h.Recent) != 180 || h.Recent[0].StartedUnixMS != 20000 {
		t.Fatalf("retention: %+v", h)
	}
}
func TestPermissionSoakStorageHistoryMissingIsNotZero(t *testing.T) {
	h := &permissionSoakStorageHistory{}
	h.record(2, time.Unix(1, 0), time.Millisecond, nil, errors.New("private address"))
	h.record(3, time.Unix(2, 0), time.Millisecond, nil, nil)
	if !h.Recent[0].Failed || h.Recent[1].Failed || len(h.Recent[0].Values) != 0 || len(h.Recent[1].Values) != 0 {
		t.Fatalf("fabricated sample: %+v", h)
	}
}
func TestPermissionSoakStorageHistoryBoundsLabelsAndCopies(t *testing.T) {
	h := &permissionSoakStorageHistory{}
	samples := []suite.MetricSample{
		{Name: "wukongim_storage_pebble_memtable_count", Labels: map[string]string{"store": "channel_log"}, Value: 4},
		{Name: "wukongim_storage_pebble_memtable_count", Labels: map[string]string{"store": "unknown-private"}, Value: 99},
		{Name: "unrelated_metric", Labels: map[string]string{"store": "channel_log"}, Value: 99},
	}
	h.record(3, time.Unix(3, 0), 2*time.Millisecond, samples, nil)
	samples[0].Value = 99
	r := h.Recent[0]
	if r.NodeID != 3 || r.FetchMS != 2 || len(r.Values) != 1 || r.Values["channel_log/memtable_count"] != 4 {
		t.Fatalf("unsafe capture: %+v", r)
	}
}
