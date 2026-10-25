package chatlifecycle

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// The worker wire contract must retain deadline coverage independently of failures.
func deadlineWireMessages(t *testing.T, body string) WorkerMessageSnapshot {
	t.Helper()
	var messages WorkerMessageSnapshot
	if err := json.Unmarshal([]byte(body), &messages); err != nil {
		t.Fatal(err)
	}
	return messages
}
func deadlineWireCounts(t *testing.T, messages WorkerMessageSnapshot) [3]uint64 {
	t.Helper()
	body, err := json.Marshal(messages)
	if err != nil {
		t.Fatal(err)
	}
	// TerminalReasons is an object, so decode only the three exact public counters.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	var result [3]uint64
	for i, key := range []string{"first_attempt_deadlines", "first_attempt_deadline_resolved", "first_attempt_timeouts"} {
		if raw[key] == nil {
			t.Errorf("wire counter %s missing", key)
			continue
		}
		if err := json.Unmarshal(raw[key], &result[i]); err != nil {
			t.Fatal(err)
		}
	}
	return result
}
func TestCoordinatorRetainsFirstDeadlineCoverage(t *testing.T) {
	fence := WorkerFence{RunID: "deadline-wire", AssignmentID: "deadline-wire", Generation: 1}
	aggregator, err := NewCoordinatorSnapshotAggregator(fence)
	if err != nil {
		t.Fatal(err)
	}
	snapshots := coordinatorSnapshotFixture(fence, 1, time.Second, 30)
	for i := range snapshots {
		snapshots[i].Messages = deadlineWireMessages(t, `{"first_attempts":20,"first_attempt_failures":1,"first_attempt_deadlines":15,"first_attempt_deadline_resolved":10,"first_attempt_timeouts":3}`)
	}
	aggregate, err := aggregator.Aggregate(snapshots)
	if err != nil {
		t.Fatal(err)
	}
	if got := deadlineWireCounts(t, aggregate.Messages); got != [3]uint64{45, 30, 9} {
		t.Fatalf("deadline projection=%v", got)
	}
	if aggregate.Messages.FirstAttemptFailures != 3 {
		t.Fatalf("deadline count contaminated failures: %+v", aggregate.Messages)
	}
}
func TestWorkerRejectsContradictoryFirstDeadlineCoverage(t *testing.T) {
	for _, body := range []string{
		`{"first_attempts":10,"first_attempt_deadlines":11}`,
		`{"first_attempts":10,"first_attempt_deadlines":8,"first_attempt_deadline_resolved":9}`,
		`{"first_attempts":10,"first_attempt_deadlines":8,"first_attempt_deadline_resolved":6,"first_attempt_timeouts":7}`,
	} {
		snapshots := coordinatorSnapshotFixture(WorkerFence{RunID: "d", AssignmentID: "d", Generation: 1}, 1, time.Second, 10)
		snapshots[0].Messages = deadlineWireMessages(t, body)
		if validWorkerSnapshot(snapshots[0]) {
			t.Errorf("contradictory counters accepted: %s", body)
		}
	}
}
func TestCoordinatorRejectsFirstDeadlineCounterRegression(t *testing.T) {
	for _, field := range []string{"first_attempt_deadlines", "first_attempt_deadline_resolved", "first_attempt_timeouts"} {
		t.Run(field, func(t *testing.T) {
			fence := WorkerFence{RunID: "deadline-regression", AssignmentID: "d", Generation: 1}
			aggregator, err := NewCoordinatorSnapshotAggregator(fence)
			if err != nil {
				t.Fatal(err)
			}
			old := coordinatorSnapshotFixture(fence, 1, time.Second, 10)
			body := map[string]uint64{"first_attempts": 20, "first_attempt_deadlines": 15, "first_attempt_deadline_resolved": 10, "first_attempt_timeouts": 3}
			encoded, _ := json.Marshal(body)
			for i := range old {
				old[i].Messages = deadlineWireMessages(t, string(encoded))
			}
			if _, err := aggregator.Aggregate(old); err != nil {
				t.Fatal(err)
			}
			next := coordinatorSnapshotFixture(fence, 2, 2*time.Second, 10)
			for i := range next {
				next[i].Messages = old[i].Messages
			}
			body[field]--
			encoded, _ = json.Marshal(body)
			next[0].Messages = deadlineWireMessages(t, string(encoded))
			if _, err := aggregator.Aggregate(next); !errors.Is(err, ErrCoordinatorSnapshotRegression) {
				t.Fatalf("regression %s accepted: %v", field, err)
			}
		})
	}
}
