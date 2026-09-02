package message

import (
	"errors"
	"testing"

	channel "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
)

func TestCompatibilityApplyFetchPersistsRowsCheckpointAndEpochAtomically(t *testing.T) {
	engine := openCompatEngine(t)
	store := mustForChannel(t, engine, "apply-workflow:1", channel.ChannelID{ID: "apply-workflow", Type: 1})
	defer store.Close()

	checkpoint := channel.Checkpoint{Epoch: 5, HW: 2}
	leo, err := store.StoreApplyFetchWithEpoch(channel.ApplyFetchStoreRequest{
		Records: []channel.Record{
			compatTestRecord(t, 9_001, "apply-workflow", "client-1"),
			compatTestRecord(t, 9_002, "apply-workflow", "client-2"),
		},
		Checkpoint: &checkpoint,
	}, &channel.EpochPoint{Epoch: 5, StartOffset: 0})
	if err != nil || leo != 2 {
		t.Fatalf("StoreApplyFetchWithEpoch() = (%d, %v), want (2, nil)", leo, err)
	}
	gotCheckpoint, err := store.LoadCheckpoint()
	if err != nil || gotCheckpoint != checkpoint {
		t.Fatalf("LoadCheckpoint() = (%+v, %v), want %+v", gotCheckpoint, err, checkpoint)
	}
	history, err := store.LoadHistory()
	if err != nil || len(history) != 1 || history[0] != (channel.EpochPoint{Epoch: 5, StartOffset: 0}) {
		t.Fatalf("LoadHistory() = (%+v, %v)", history, err)
	}

	hw := uint64(3)
	leo, err = store.StoreApplyFetchTrusted(channel.ApplyFetchStoreRequest{
		PreviousCommittedHW: 2,
		Records:             []channel.Record{compatTestRecord(t, 9_003, "apply-workflow", "client-3")},
		CheckpointHW:        &hw,
	})
	if err != nil || leo != 3 {
		t.Fatalf("StoreApplyFetchTrusted() = (%d, %v), want (3, nil)", leo, err)
	}
	gotCheckpoint, err = store.LoadCheckpoint()
	if err != nil || gotCheckpoint != (channel.Checkpoint{Epoch: 5, HW: 3}) {
		t.Fatalf("checkpoint after HW-only advance = (%+v, %v)", gotCheckpoint, err)
	}

	checkpoint = channel.Checkpoint{Epoch: 6, HW: 4}
	leo, err = store.StoreApplyFetchTrustedWithEpoch(channel.ApplyFetchStoreRequest{
		PreviousCommittedHW: 3,
		Records:             []channel.Record{compatTestRecord(t, 9_004, "apply-workflow", "client-4")},
		Checkpoint:          &checkpoint,
	}, &channel.EpochPoint{Epoch: 6, StartOffset: 3})
	if err != nil || leo != 4 {
		t.Fatalf("StoreApplyFetchTrustedWithEpoch() = (%d, %v), want (4, nil)", leo, err)
	}

	leo, err = store.StoreApplyFetch(channel.ApplyFetchStoreRequest{
		Records: []channel.Record{compatTestRecord(t, 9_005, "apply-workflow", "client-5")},
	})
	if err != nil || leo != 5 {
		t.Fatalf("StoreApplyFetch() = (%d, %v), want (5, nil)", leo, err)
	}
	leo, err = store.StoreApplyFetchTrusted(channel.ApplyFetchStoreRequest{})
	if err != nil || leo != 5 {
		t.Fatalf("empty StoreApplyFetchTrusted() = (%d, %v), want no-op LEO 5", leo, err)
	}

	history, err = store.LoadHistory()
	if err != nil || len(history) != 2 || history[1] != (channel.EpochPoint{Epoch: 6, StartOffset: 3}) {
		t.Fatalf("final history = (%+v, %v)", history, err)
	}
	records, err := store.Read(0, 1<<20)
	if err != nil || len(records) != 5 {
		t.Fatalf("Read() records=%+v err=%v, want five", records, err)
	}
	for index, record := range records {
		if record.ID != uint64(9_001+index) || record.Index != uint64(index+1) {
			t.Fatalf("record[%d] = %+v", index, record)
		}
	}
}

func TestCompatibilityApplyFetchRejectsInconsistentReplicationStateWithoutWrites(t *testing.T) {
	newStore := func(t *testing.T, suffix string) *ChannelStore {
		t.Helper()
		engine := openCompatEngine(t)
		return mustForChannel(t, engine, channel.ChannelKey("apply-guard-"+suffix), channel.ChannelID{ID: "apply-guard-" + suffix, Type: 1})
	}
	oneRecord := func(t *testing.T, suffix string) []channel.Record {
		t.Helper()
		return []channel.Record{compatTestRecord(t, 9_100, "apply-guard-"+suffix, "client")}
	}

	tests := []struct {
		name       string
		request    func(*testing.T) channel.ApplyFetchStoreRequest
		epochPoint *channel.EpochPoint
		want       error
	}{
		{
			name: "checkpoint and HW supplied together",
			request: func(t *testing.T) channel.ApplyFetchStoreRequest {
				hw := uint64(1)
				return channel.ApplyFetchStoreRequest{Records: oneRecord(t, "both"), Checkpoint: &channel.Checkpoint{Epoch: 1, HW: 1}, CheckpointHW: &hw}
			},
			want: channel.ErrInvalidArgument,
		},
		{
			name: "log start beyond committed HW",
			request: func(t *testing.T) channel.ApplyFetchStoreRequest {
				return channel.ApplyFetchStoreRequest{Records: oneRecord(t, "bad-start"), Checkpoint: &channel.Checkpoint{Epoch: 1, LogStartOffset: 2, HW: 1}}
			},
			want: channel.ErrCorruptState,
		},
		{
			name: "checkpoint regresses coordinator evidence",
			request: func(t *testing.T) channel.ApplyFetchStoreRequest {
				return channel.ApplyFetchStoreRequest{PreviousCommittedHW: 2, Records: oneRecord(t, "regress"), Checkpoint: &channel.Checkpoint{Epoch: 1, HW: 1}}
			},
			want: channel.ErrCorruptState,
		},
		{
			name: "checkpoint exceeds next LEO",
			request: func(t *testing.T) channel.ApplyFetchStoreRequest {
				return channel.ApplyFetchStoreRequest{Records: oneRecord(t, "checkpoint-ahead"), Checkpoint: &channel.Checkpoint{Epoch: 1, HW: 2}}
			},
			want: channel.ErrCorruptState,
		},
		{
			name: "HW-only checkpoint exceeds next LEO",
			request: func(t *testing.T) channel.ApplyFetchStoreRequest {
				hw := uint64(2)
				return channel.ApplyFetchStoreRequest{Records: oneRecord(t, "hw-ahead"), CheckpointHW: &hw}
			},
			want: channel.ErrCorruptState,
		},
		{
			name: "epoch boundary does not match local LEO",
			request: func(t *testing.T) channel.ApplyFetchStoreRequest {
				return channel.ApplyFetchStoreRequest{Records: oneRecord(t, "bad-epoch")}
			},
			epochPoint: &channel.EpochPoint{Epoch: 2, StartOffset: 1},
			want:       channel.ErrCorruptState,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newStore(t, testNameKey(test.name))
			defer store.Close()
			leo, err := store.StoreApplyFetchWithEpoch(test.request(t), test.epochPoint)
			if !errors.Is(err, test.want) {
				t.Fatalf("StoreApplyFetchWithEpoch() = (%d, %v), want %v", leo, err, test.want)
			}
			if got := store.LEO(); got != 0 {
				t.Fatalf("LEO after rejected apply = %d, want 0", got)
			}
		})
	}
}

func TestCompatibilityApplyFetchHWNoopPreservesCheckpointAndReleasesOwnership(t *testing.T) {
	engine := openCompatEngine(t)
	store := mustForChannel(t, engine, "apply-hw-noop:1", channel.ChannelID{ID: "apply-hw-noop", Type: 1})
	defer store.Close()
	if _, err := store.Append([]channel.Record{compatTestRecord(t, 9_201, "apply-hw-noop", "client")}); err != nil {
		t.Fatalf("Append(): %v", err)
	}
	want := channel.Checkpoint{Epoch: 7, LogStartOffset: 1, HW: 1}
	if err := store.StoreCheckpoint(want); err != nil {
		t.Fatalf("StoreCheckpoint(): %v", err)
	}
	hw := uint64(1)
	leo, err := store.StoreApplyFetchTrusted(channel.ApplyFetchStoreRequest{PreviousCommittedHW: 1, CheckpointHW: &hw})
	if err != nil || leo != 1 {
		t.Fatalf("StoreApplyFetchTrusted(no-op HW) = (%d, %v)", leo, err)
	}
	got, err := store.LoadCheckpoint()
	if err != nil || got != want {
		t.Fatalf("checkpoint = (%+v, %v), want %+v", got, err, want)
	}
	if !store.log.checkpointMu.TryLock() {
		t.Fatal("checkpoint ownership was not released after a no-op apply")
	}
	store.log.checkpointMu.Unlock()
	if snapshot := engine.ChannelEntryMetricsSnapshot(); snapshot.BackgroundPins != 0 {
		t.Fatalf("background pins = %d, want zero", snapshot.BackgroundPins)
	}
}

func testNameKey(name string) string {
	key := make([]byte, 0, len(name))
	for index := range name {
		character := name[index]
		if character >= 'a' && character <= 'z' {
			key = append(key, character)
		}
	}
	return string(key)
}
