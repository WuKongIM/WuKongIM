package message

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	channel "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
)

func TestReplayBackupSnapshotReaderPreservesBoundariesAndCommittedRows(t *testing.T) {
	ctx := context.Background()
	engine := openCompatEngine(t)
	store := mustForChannel(t, engine, "replay-room:3", channel.ChannelID{ID: "replay-room", Type: 3})
	defer store.Close()

	row := messageRow{
		MessageID: 901, FramerFlags: 4, Setting: 7,
		ClientMsgNo: "client-901", ChannelID: "replay-room", ChannelType: 3,
		FromUID: "alice", ServerTimestampMS: 1_700_000_000_901,
		Payload: []byte("portable-payload"),
	}
	recordToAppend, err := compatibilityRecordFromRow(row)
	if err != nil {
		t.Fatalf("compatibilityRecordFromRow(): %v", err)
	}
	recordToAppend.Epoch = 4
	if _, err := store.Append([]channel.Record{recordToAppend}); err != nil {
		t.Fatalf("Append(): %v", err)
	}
	if err := store.StoreCheckpoint(channel.Checkpoint{Epoch: 4, HW: 1}); err != nil {
		t.Fatalf("StoreCheckpoint(): %v", err)
	}

	body := readBackupSnapshot(t, engine.db, BackupSnapshotRequest{
		HashSlot: 19,
		Channels: []BackupChannelCut{{
			Key: "replay-room:3",
			ID:  ChannelID{ID: "replay-room", Type: 3},
			Checkpoint: Checkpoint{
				Epoch: 4,
				HW:    1,
			},
		}},
	})

	var events []string
	var boundary BackupSnapshotBoundary
	var record BackupSnapshotRecord
	stats, err := ReplayBackupSnapshotReader(
		ctx,
		bytes.NewReader(body),
		int64(len(body)),
		func(got BackupSnapshotBoundary) error {
			events = append(events, "boundary")
			boundary = got
			return nil
		},
		func(got BackupSnapshotRecord) error {
			events = append(events, "record")
			got.Payload = append([]byte(nil), got.Payload...)
			record = got
			return nil
		},
	)
	if err != nil {
		t.Fatalf("ReplayBackupSnapshotReader(): %v", err)
	}
	if stats != (BackupSnapshotStats{HashSlot: 19, ChannelCount: 1, MessageCount: 1, MaxMessageID: 901}) {
		t.Fatalf("stats = %+v", stats)
	}
	if got, want := events, []string{"boundary", "record"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("visitor order = %v, want %v", got, want)
	}
	if boundary != (BackupSnapshotBoundary{
		ChannelKey: "replay-room:3", ChannelID: "replay-room", ChannelType: 3,
		Epoch: 4, HW: 1,
	}) {
		t.Fatalf("boundary = %+v", boundary)
	}
	if record.Boundary != boundary || record.MessageSeq != 1 || record.MessageID != 901 ||
		record.Setting != 7 || record.FromUID != "alice" || record.ClientMsgNo != "client-901" ||
		record.ServerTimestampMS != 1_700_000_000_901 || !record.SyncOnce || string(record.Payload) != "portable-payload" {
		t.Fatalf("record = %+v", record)
	}
}

func TestReplayBackupSnapshotReaderPropagatesVisitorAndContextFailures(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)
	log := mustAcquireChannel(t, store.db, "replay-errors:1", ChannelID{ID: "replay-errors", Type: 1})
	defer log.Close()
	if _, err := log.Append(context.Background(), []Record{{ID: 1, Payload: []byte("one")}}, AppendOptions{Mode: AppendStrict}); err != nil {
		t.Fatalf("Append(): %v", err)
	}
	body := readBackupSnapshot(t, store.db, BackupSnapshotRequest{
		HashSlot: 2,
		Channels: []BackupChannelCut{{
			Key: "replay-errors:1", ID: ChannelID{ID: "replay-errors", Type: 1},
			Checkpoint: Checkpoint{Epoch: 1, HW: 1},
		}},
	})

	visitorErr := errors.New("stop replay")
	_, err := ReplayBackupSnapshotReader(context.Background(), bytes.NewReader(body), int64(len(body)),
		func(BackupSnapshotBoundary) error { return visitorErr },
		func(BackupSnapshotRecord) error { return nil },
	)
	if !errors.Is(err, visitorErr) {
		t.Fatalf("boundary visitor error = %v, want %v", err, visitorErr)
	}
	_, err = ReplayBackupSnapshotReader(context.Background(), bytes.NewReader(body), int64(len(body)),
		func(BackupSnapshotBoundary) error { return nil },
		func(BackupSnapshotRecord) error { return visitorErr },
	)
	if !errors.Is(err, visitorErr) {
		t.Fatalf("record visitor error = %v, want %v", err, visitorErr)
	}
	_, err = ReplayBackupSnapshotReader(context.Background(), bytes.NewReader(body), int64(len(body)), nil,
		func(BackupSnapshotRecord) error { return nil },
	)
	if !errors.Is(err, dberrors.ErrInvalidArgument) {
		t.Fatalf("nil boundary visitor error = %v, want invalid argument", err)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = ReplayBackupSnapshotReader(canceled, bytes.NewReader(body), int64(len(body)),
		func(BackupSnapshotBoundary) error { return nil },
		func(BackupSnapshotRecord) error { return nil },
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled replay error = %v, want context canceled", err)
	}
}
