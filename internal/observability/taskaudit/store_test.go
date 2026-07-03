package taskaudit

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStoreAppendReplayAndQuery(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "controller-v2-events.jsonl")
	store, err := Open(path, Options{})
	require.NoError(t, err)
	require.NoError(t, store.Append(ctx, testEvent("task-a", 10, EventCreated)))
	require.NoError(t, store.Append(ctx, testEvent("task-b", 11, EventFailed)))
	require.NoError(t, store.Close())

	reopened, err := Open(path, Options{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reopened.Close()) })

	list, err := reopened.List(ctx, ListRequest{Limit: 10})
	require.NoError(t, err)
	require.Equal(t, 2, list.Total)
	require.False(t, list.Truncated)
	require.Equal(t, []string{"task-b", "task-a"}, snapshotIDs(list.Items))

	events, err := reopened.Events(ctx, "task-a")
	require.NoError(t, err)
	require.Equal(t, "task-a", events.Task.TaskID)
	require.Len(t, events.Events, 1)
	require.Equal(t, uint64(10), events.Events[0].AppliedRaftIndex)
}

func TestStoreRetainedSnapshotKeepsLatestStep(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "controller-v2-events.jsonl")
	store, err := Open(path, Options{})
	require.NoError(t, err)
	require.NoError(t, store.Append(ctx, testEventWithStep("task-a", 10, EventCreated, "open_learner")))
	require.NoError(t, store.Append(ctx, testEventWithStep("task-a", 11, EventParticipantProgress, "promote_learner")))
	require.NoError(t, store.Close())

	reopened, err := Open(path, Options{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reopened.Close()) })

	list, err := reopened.List(ctx, ListRequest{Limit: 10})
	require.NoError(t, err)
	require.Len(t, list.Items, 1)
	require.Equal(t, "promote_learner", list.Items[0].Step)
}

func TestStoreRetentionKeepsLatestTasksAndEvents(t *testing.T) {
	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "controller-v2-events.jsonl"), Options{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })

	for i := 1; i <= 201; i++ {
		require.NoError(t, store.Append(ctx, testEvent(fmt.Sprintf("task-%03d", i), uint64(i), EventCreated)))
	}
	list, err := store.List(ctx, ListRequest{Limit: 500})
	require.NoError(t, err)
	require.Equal(t, 200, list.Total)
	require.Len(t, list.Items, 200)
	require.Equal(t, "task-201", list.Items[0].TaskID)
	require.Equal(t, "task-002", list.Items[len(list.Items)-1].TaskID)

	for i := 1; i <= 51; i++ {
		require.NoError(t, store.Append(ctx, testEvent("long-task", uint64(1000+i), EventParticipantProgress)))
	}
	events, err := store.Events(ctx, "long-task")
	require.NoError(t, err)
	require.True(t, events.Truncated)
	require.True(t, events.Task.Truncated)
	require.Equal(t, 50, events.Task.EventCount)
	require.Len(t, events.Events, 50)
	require.Equal(t, uint64(1002), events.Events[0].AppliedRaftIndex)
	require.Equal(t, uint64(1051), events.Events[len(events.Events)-1].AppliedRaftIndex)
}

func TestStoreRetentionCompactsJSONLWhenTasksAreDropped(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "controller-v2-events.jsonl")
	store, err := Open(path, Options{MaxTasks: 2, MaxEventsPerTask: 5})
	require.NoError(t, err)

	require.NoError(t, store.Append(ctx, testEvent("task-001", 1, EventCreated)))
	require.NoError(t, store.Append(ctx, testEvent("task-002", 2, EventCreated)))
	require.NoError(t, store.Append(ctx, testEvent("task-003", 3, EventCreated)))
	require.NoError(t, store.Close())

	require.Equal(t, 2, countLines(t, path))
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NotContains(t, string(body), "task-001")

	reopened, err := Open(path, Options{MaxTasks: 2, MaxEventsPerTask: 5})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reopened.Close()) })
	list, err := reopened.List(ctx, ListRequest{Limit: 10})
	require.NoError(t, err)
	require.Equal(t, []string{"task-003", "task-002"}, snapshotIDs(list.Items))
}

func TestStoreSkipsCorruptJSONLLines(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "controller-v2-events.jsonl")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(`{"event_id":"event-ok","task_id":"task-ok","type":"created","kind":"bootstrap","status":"pending","applied_raft_index":7,"occurred_at":"2026-06-29T10:00:00Z"}`+"\nnot-json\n"), 0o644))

	store, err := Open(path, Options{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })

	list, err := store.List(ctx, ListRequest{Limit: 10})
	require.NoError(t, err)
	require.Equal(t, 1, list.Total)
	require.Equal(t, "task-ok", list.Items[0].TaskID)
}

func TestStoreAppendDuplicateDoesNotWriteJSONL(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "controller-v2-events.jsonl")
	store, err := Open(path, Options{})
	require.NoError(t, err)
	event := testEvent("task-a", 1, EventCreated)

	require.NoError(t, store.Append(ctx, event))
	require.NoError(t, store.Append(ctx, event))
	require.NoError(t, store.Close())

	require.Equal(t, 1, countLines(t, path))
}

func TestStoreCompactionSerializesWithAppend(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "controller-v2-events.jsonl")
	store, err := Open(path, Options{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	errCh := make(chan error, 1)
	go func() {
		for i := 1; i <= 75; i++ {
			if err := store.Append(ctx, testEvent("task-a", uint64(i), EventParticipantProgress)); err != nil {
				errCh <- err
				return
			}
		}
		errCh <- nil
	}()
	require.NoError(t, store.Compact(ctx))
	require.NoError(t, <-errCh)
	require.NoError(t, store.Close())

	reopened, err := Open(path, Options{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reopened.Close()) })
	events, err := reopened.Events(ctx, "task-a")
	require.NoError(t, err)
	require.Len(t, events.Events, 50)
	require.Equal(t, uint64(26), events.Events[0].AppliedRaftIndex)
	require.Equal(t, uint64(75), events.Events[len(events.Events)-1].AppliedRaftIndex)
}

func testEvent(taskID string, index uint64, typ EventType) Event {
	return Event{
		EventID:          fmt.Sprintf("%s-%d-%s", taskID, index, typ),
		TaskID:           taskID,
		Type:             typ,
		Kind:             "bootstrap",
		Status:           "pending",
		SlotID:           1,
		LeaderID:         1,
		AppliedRaftIndex: index,
		AppliedRaftTerm:  2,
		CommandKind:      "test_command",
		OccurredAt:       time.Date(2026, 6, 29, 10, 0, 0, int(index), time.UTC),
		Summary:          "test event",
	}
}

func testEventWithStep(taskID string, index uint64, typ EventType, step string) Event {
	event := testEvent(taskID, index, typ)
	event.Details = map[string]any{"step": step}
	return event
}

func snapshotIDs(items []Snapshot) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.TaskID)
	}
	return out
}

func countLines(t *testing.T, path string) int {
	t.Helper()
	file, err := os.Open(path)
	require.NoError(t, err)
	defer file.Close()
	scanner := bufio.NewScanner(file)
	var count int
	for scanner.Scan() {
		count++
	}
	require.NoError(t, scanner.Err())
	return count
}
