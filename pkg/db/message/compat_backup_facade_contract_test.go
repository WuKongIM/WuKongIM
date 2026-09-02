package message

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	channel "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
)

func TestEngineBackupFacadeStreamsAndRestoresOneCommittedCut(t *testing.T) {
	ctx := context.Background()
	source, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open(source): %v", err)
	}
	defer source.Close()
	log := mustAcquireChannel(t, source.db, "facade-backup:5", ChannelID{ID: "facade-backup", Type: 5})
	defer log.Close()
	if _, err := log.Append(ctx, []Record{{
		ID: 4_201, FromUID: "u1", ClientMsgNo: "client-4201", Payload: []byte("backup-row"),
	}}, AppendOptions{Mode: AppendStrict}); err != nil {
		t.Fatalf("Append(): %v", err)
	}
	if err := log.StoreCheckpoint(ctx, Checkpoint{Epoch: 8, HW: 1}); err != nil {
		t.Fatalf("StoreCheckpoint(): %v", err)
	}
	request := BackupSnapshotRequest{
		HashSlot: 31,
		Channels: []BackupChannelCut{{
			Key: "facade-backup:5", ID: ChannelID{ID: "facade-backup", Type: 5},
			Checkpoint: Checkpoint{Epoch: 8, HW: 1},
		}},
	}

	plainReader, err := source.OpenBackupSnapshot(ctx, request)
	if err != nil {
		t.Fatalf("OpenBackupSnapshot(): %v", err)
	}
	plainBody, err := io.ReadAll(plainReader)
	if err != nil {
		t.Fatalf("ReadAll(plain snapshot): %v", err)
	}
	if err := plainReader.Close(); err != nil {
		t.Fatalf("Close(plain snapshot): %v", err)
	}

	reader, stats, err := source.OpenBackupSnapshotWithStats(ctx, request)
	if err != nil {
		t.Fatalf("OpenBackupSnapshotWithStats(): %v", err)
	}
	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll(snapshot): %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("Close(snapshot): %v", err)
	}
	if !bytes.Equal(body, plainBody) {
		t.Fatal("facade snapshot variants encoded different committed cuts")
	}
	if stats != (BackupSnapshotStats{HashSlot: 31, ChannelCount: 1, MessageCount: 1, MaxMessageID: 4_201}) {
		t.Fatalf("snapshot stats = %+v", stats)
	}

	target, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open(target): %v", err)
	}
	defer target.Close()
	readerStats, err := target.ImportBackupSnapshotReader(ctx, bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("ImportBackupSnapshotReader(): %v", err)
	}
	if readerStats != stats {
		t.Fatalf("reader import stats = %+v, want %+v", readerStats, stats)
	}
	byteStats, err := target.ImportBackupSnapshot(ctx, body)
	if err != nil {
		t.Fatalf("ImportBackupSnapshot(idempotent): %v", err)
	}
	if byteStats != stats {
		t.Fatalf("byte import stats = %+v, want %+v", byteStats, stats)
	}
	restored := mustAcquireChannel(t, target.db, "facade-backup:5", ChannelID{ID: "facade-backup", Type: 5})
	defer restored.Close()
	records, err := restored.Read(ctx, 1, ReadOptions{})
	if err != nil {
		t.Fatalf("Read(restored): %v", err)
	}
	if len(records) != 1 || records[0].MessageID != 4_201 || records[0].MessageSeq != 1 || string(records[0].Payload) != "backup-row" {
		t.Fatalf("restored records = %+v", records)
	}
}

func TestEngineBackupFacadeRejectsUnavailableAndCanceledEngines(t *testing.T) {
	var nilEngine *Engine
	request := BackupSnapshotRequest{}
	if _, err := nilEngine.OpenBackupSnapshot(context.Background(), request); !errors.Is(err, channel.ErrClosed) {
		t.Fatalf("nil OpenBackupSnapshot() error = %v, want closed", err)
	}
	if _, _, err := nilEngine.OpenBackupSnapshotWithStats(context.Background(), request); !errors.Is(err, channel.ErrClosed) {
		t.Fatalf("nil OpenBackupSnapshotWithStats() error = %v, want closed", err)
	}
	if _, err := nilEngine.ImportBackupSnapshot(context.Background(), nil); !errors.Is(err, channel.ErrClosed) {
		t.Fatalf("nil ImportBackupSnapshot() error = %v, want closed", err)
	}
	if _, err := nilEngine.ImportBackupSnapshotReader(context.Background(), bytes.NewReader(nil), 0); !errors.Is(err, channel.ErrClosed) {
		t.Fatalf("nil ImportBackupSnapshotReader() error = %v, want closed", err)
	}

	engine, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := engine.OpenBackupSnapshot(canceled, request); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled OpenBackupSnapshot() error = %v", err)
	}
	if _, _, err := engine.OpenBackupSnapshotWithStats(canceled, request); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled OpenBackupSnapshotWithStats() error = %v", err)
	}
	if _, err := engine.ImportBackupSnapshot(canceled, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled ImportBackupSnapshot() error = %v", err)
	}
	if _, err := engine.ImportBackupSnapshotReader(canceled, bytes.NewReader(nil), 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled ImportBackupSnapshotReader() error = %v", err)
	}
	if err := engine.Close(); err != nil {
		t.Fatalf("Close(): %v", err)
	}
	if _, err := engine.OpenBackupSnapshot(context.Background(), request); !errors.Is(err, channel.ErrClosed) {
		t.Fatalf("closed OpenBackupSnapshot() error = %v, want closed", err)
	}
}
