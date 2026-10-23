package cluster

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/raftlog"
)

func TestRaftStorageMetricsPreservesWriteStallAndSlowDiskReports(t *testing.T) {
	got := storageMetricsFromRaftLog(raftlog.MetricsSnapshot{
		Available:               true,
		WriteStallMemTableCount: 2, WriteStallL0Count: 3, WriteStallOtherCount: 5,
		WriteStallTotalNanos: 700, WriteStallMaxNanos: 400, WriteStallActive: true,
		DiskSlowWALEvents: 11, DiskSlowWALMaxNanos: 600,
		DiskSlowOtherEvents: 13, DiskSlowOtherMaxNanos: 900,
	})
	if got.WriteStallMemTableCount != 2 || got.WriteStallL0Count != 3 || got.WriteStallOtherCount != 5 || got.WriteStallTotalNanos != 700 || got.WriteStallMaxNanos != 400 || !got.WriteStallActive {
		t.Fatalf("Raft stall report was not propagated: %+v", got)
	}
	if got.DiskSlowWALEvents != 11 || got.DiskSlowWALMaxNanos != 600 || got.DiskSlowOtherEvents != 13 || got.DiskSlowOtherMaxNanos != 900 {
		t.Fatalf("Raft slow-disk report was not propagated: %+v", got)
	}
}
