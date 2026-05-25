package message

import (
	"context"
	"testing"
)

func TestSnapshotPayloadRoundTripAndReplace(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)

	log := testChannelLog(store)
	_, ok, err := log.LoadSnapshotPayload(context.Background())
	if err != nil {
		t.Fatalf("LoadSnapshotPayload() empty: %v", err)
	}
	if ok {
		t.Fatal("LoadSnapshotPayload() empty ok = true, want false")
	}
	if err := log.StoreSnapshotPayload(context.Background(), []byte("first")); err != nil {
		t.Fatalf("StoreSnapshotPayload(first): %v", err)
	}
	if err := log.StoreSnapshotPayload(context.Background(), []byte("second")); err != nil {
		t.Fatalf("StoreSnapshotPayload(second): %v", err)
	}
	payload, ok, err := log.LoadSnapshotPayload(context.Background())
	if err != nil {
		t.Fatalf("LoadSnapshotPayload(): %v", err)
	}
	if !ok || string(payload) != "second" {
		t.Fatalf("snapshot = (%q, %v), want (second, true)", payload, ok)
	}
}

func TestSnapshotInstallStoresPayloadCheckpointAndEpoch(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)

	log := testChannelLog(store)
	if _, err := log.Append(context.Background(), testRecords(1, "one", "two", "three"), AppendOptions{}); err != nil {
		t.Fatalf("Append(): %v", err)
	}
	if err := log.AppendHistory(context.Background(), EpochPoint{Epoch: 1, StartOffset: 0}); err != nil {
		t.Fatalf("AppendHistory(1): %v", err)
	}
	if err := log.AppendHistory(context.Background(), EpochPoint{Epoch: 2, StartOffset: 2}); err != nil {
		t.Fatalf("AppendHistory(2): %v", err)
	}

	snap := Snapshot{Epoch: 3, EndOffset: 3, Payload: []byte("snap")}
	checkpoint := Checkpoint{Epoch: 3, LogStartOffset: 3, HW: 3}
	leo, err := log.InstallSnapshot(context.Background(), snap, checkpoint, EpochPoint{Epoch: 3, StartOffset: 3})
	if err != nil {
		t.Fatalf("InstallSnapshot(): %v", err)
	}
	if leo != 3 {
		t.Fatalf("InstallSnapshot() leo = %d, want 3", leo)
	}

	payload, ok, err := log.LoadSnapshotPayload(context.Background())
	if err != nil {
		t.Fatalf("LoadSnapshotPayload(): %v", err)
	}
	if !ok || string(payload) != "snap" {
		t.Fatalf("snapshot payload = (%q, %v), want (snap, true)", payload, ok)
	}
	gotCheckpoint, ok, err := log.LoadCheckpoint(context.Background())
	if err != nil {
		t.Fatalf("LoadCheckpoint(): %v", err)
	}
	if !ok || gotCheckpoint != checkpoint {
		t.Fatalf("checkpoint = (%+v, %v), want (%+v, true)", gotCheckpoint, ok, checkpoint)
	}
	history, ok, err := log.LoadHistory(context.Background())
	if err != nil {
		t.Fatalf("LoadHistory(): %v", err)
	}
	wantHistory := []EpochPoint{{Epoch: 1, StartOffset: 0}, {Epoch: 2, StartOffset: 2}, {Epoch: 3, StartOffset: 3}}
	if !ok || len(history) != len(wantHistory) {
		t.Fatalf("history = (%+v, %v), want %+v", history, ok, wantHistory)
	}
	for i := range wantHistory {
		if history[i] != wantHistory[i] {
			t.Fatalf("history[%d] = %+v, want %+v", i, history[i], wantHistory[i])
		}
	}
}
