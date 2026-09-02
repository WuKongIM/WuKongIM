package taskaudit

import (
	"errors"
	"testing"
	"time"
)

func TestSnapshotFiltersRequireEveryDeclaredSelector(t *testing.T) {
	snapshot := Snapshot{
		TaskID: "slot-migrate-42", Kind: "slot_migrate", Status: "running", SlotID: 42,
		LeaderID: 1, SourceNode: 2, TargetNode: 3,
		Summary: "Moving replica", LastReason: "Awaiting quorum",
	}
	for name, request := range map[string]ListRequest{
		"all exact": {Kind: "slot_migrate", Status: "running", SlotID: 42, NodeID: 2, Keyword: "QUORUM"},
		"leader":    {NodeID: 1},
		"source":    {NodeID: 2},
		"target":    {NodeID: 3},
		"task id":   {Keyword: "MIGRATE-42"},
		"summary":   {Keyword: "moving"},
	} {
		if !snapshotMatches(snapshot, request) {
			t.Fatalf("matching %s selector rejected: %+v", name, request)
		}
	}
	for name, request := range map[string]ListRequest{
		"kind":    {Kind: "leader_transfer"},
		"status":  {Status: "failed"},
		"slot":    {SlotID: 41},
		"node":    {NodeID: 4},
		"keyword": {Keyword: "not-present"},
	} {
		if snapshotMatches(snapshot, request) {
			t.Fatalf("mismatching %s selector accepted: %+v", name, request)
		}
	}
}

func TestEventCloneAndUTF8TruncationProtectStoredProjection(t *testing.T) {
	original := Event{TaskID: "task", Details: map[string]any{"step": "prepare", "count": 1}}
	cloned := cloneEvent(original)
	cloned.Details["step"] = "commit"
	if original.Details["step"] != "prepare" {
		t.Fatalf("event details aliased: original=%+v clone=%+v", original.Details, cloned.Details)
	}
	if got := cloneEvent(Event{TaskID: "empty"}); got.Details != nil {
		t.Fatalf("nil details changed: %+v", got)
	}

	if got := truncateUTF8("abc", 0); got != "abc" {
		t.Fatalf("unbounded truncation = %q", got)
	}
	if got := truncateUTF8("abc", 3); got != "abc" {
		t.Fatalf("exact truncation = %q", got)
	}
	if got := truncateUTF8("ab界cd", 4); got != "ab" {
		t.Fatalf("UTF-8 truncation split rune: %q", got)
	}
}

func TestAuditOrderingUsesRaftIndexTimeThenEventID(t *testing.T) {
	base := time.Unix(100, 0)
	events := []Event{
		{EventID: "z", AppliedRaftIndex: 2, OccurredAt: base},
		{EventID: "b", AppliedRaftIndex: 1, OccurredAt: base.Add(time.Second)},
		{EventID: "a", AppliedRaftIndex: 1, OccurredAt: base.Add(time.Second)},
		{EventID: "c", AppliedRaftIndex: 1, OccurredAt: base},
	}
	sortEventsAsc(events)
	want := []string{"c", "a", "b", "z"}
	for index, event := range events {
		if event.EventID != want[index] {
			t.Fatalf("event order = %+v", events)
		}
	}

	snapshots := []Snapshot{
		{TaskID: "b", LastAppliedRaftIndex: 5},
		{TaskID: "a", LastAppliedRaftIndex: 5},
		{TaskID: "newest", LastAppliedRaftIndex: 6},
	}
	sortSnapshotsDesc(snapshots)
	if snapshots[0].TaskID != "newest" || snapshots[1].TaskID != "a" || snapshots[2].TaskID != "b" {
		t.Fatalf("snapshot order = %+v", snapshots)
	}
}

func TestStoreLifecycleRejectsMissingPathAndClosesIdempotently(t *testing.T) {
	if _, err := Open("", Options{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("empty store path error = %v", err)
	}
	if err := (*Store)(nil).Close(); err != nil {
		t.Fatalf("nil store close = %v", err)
	}
	store, err := Open(t.TempDir()+"/audit.jsonl", Options{})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}
