package multiraft

import (
	"errors"
	"testing"
	"time"
)

func TestRecoveryProgressIsThrottledScopedAndReportsFailure(t *testing.T) {
	logger := newRecordingLogger("slot")
	now := time.Unix(100, 0)
	progress := newRecoveryReporter(logger, 1, 7)
	progress.now = func() time.Time { return now }
	for i := int64(0); i < 10000; i++ {
		progress.report(RecoveryProgress{Stage: "snapshot_install", Entries: i, TotalEntries: 10000})
	}
	if len(logger.entries()) != 1 {
		t.Fatalf("unbounded logs: %d", len(logger.entries()))
	}
	now = now.Add(5 * time.Second)
	progress.report(RecoveryProgress{Stage: "snapshot_install", Entries: 10000, TotalEntries: 10000})
	progress.fail(errors.New("disk unavailable"))
	entries := logger.entries()
	if len(entries) != 3 {
		t.Fatalf("missing progress/failure: %d", len(entries))
	}
	for _, entry := range entries {
		if f, ok := entry.field("slotID"); !ok || f.Value != uint64(7) {
			t.Fatal("missing Slot scope")
		}
		if _, ok := entry.field("nodeID"); !ok {
			t.Fatal("missing node scope")
		}
	}
	if entries[2].level != "ERROR" {
		t.Fatal("failure emitted as success")
	}
}
