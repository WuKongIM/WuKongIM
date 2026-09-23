package message

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
)

func TestMQTTSourceProtectionRetentionReadReopenAndBackup(t *testing.T) {
	ctx := context.Background()
	s := openTestMessageStore(t)
	defer func() { s.close(t) }()
	l := testChannelLog(s)
	id, key := l.id, l.key
	metadata := publicationFixture(t)
	for i := uint64(1); i <= 3; i++ {
		if _, err := l.Append(ctx, []Record{{ID: i, Payload: []byte("body"), PublicationMetadata: metadata}}, AppendOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.StoreCheckpoint(ctx, Checkpoint{Epoch: 1, HW: 3}); err != nil {
		t.Fatal(err)
	}
	state := MQTTSourceState{Generation: "source-1", Revision: 1, StartAfter: 1, CopiedThrough: 1}
	for range 2 {
		if err := l.ApplyMQTTSourceState(ctx, 0, state); err != nil {
			t.Fatal(err)
		}
	}
	trim, err := l.TrimPrefixThrough(ctx, 3)
	if err != nil || trim.Deleted != 1 || trim.More {
		t.Fatalf("protected trim: %+v %v", trim, err)
	}
	retention, _, err := l.LoadRetentionState(ctx)
	if err != nil || retention.LocalRetentionThroughSeq != 3 || retention.PhysicalRetentionThroughSeq != 1 {
		t.Fatalf("logical/physical boundaries: %+v %v", retention, err)
	}
	rows, err := l.ReadMQTTProtectedSource(ctx, "source-1", 2, 3, ReadOptions{Limit: 2, MaxBytes: 4096})
	if err != nil || len(rows) != 2 || !bytes.Equal(rows[0].PublicationMetadata, metadata) {
		t.Fatalf("hidden protected content lost: %+v %v", rows, err)
	}
	clear(rows[0].PublicationMetadata)
	rows, err = l.ReadMQTTProtectedSource(ctx, "source-1", 2, 3, ReadOptions{Limit: 2, MaxBytes: len(metadata) + 4})
	if err != nil || len(rows) != 1 || !bytes.Equal(rows[0].PublicationMetadata, metadata) {
		t.Fatalf("ownership/budget: %+v %v", rows, err)
	}
	state.Revision, state.CopiedThrough, state.ReceiptDigest = 2, 2, [32]byte{1}
	if err := l.ApplyMQTTSourceState(ctx, 1, state); err != nil {
		t.Fatal(err)
	}
	trim, err = l.TrimPrefixThrough(ctx, 3)
	if err != nil || trim.Deleted != 1 || trim.DeletedThroughSeq != 2 || trim.More {
		t.Fatalf("copy receipt trim: %+v %v", trim, err)
	}
	if err := l.TruncateFrom(ctx, 3); !errors.Is(err, dberrors.ErrConflict) {
		t.Fatalf("truncated committed protected source: %v", err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	s.close(t)
	s = openTestMessageStoreAt(t, s.path)
	l = mustAcquireChannel(t, s.db, key, id)
	defer l.Close()
	got, ok, err := l.LoadMQTTSourceState(ctx)
	if err != nil || !ok || got != state {
		t.Fatalf("reopen state: %+v %v", got, err)
	}
	body := readBackupSnapshot(t, s.db, BackupSnapshotRequest{HashSlot: 1, Channels: []BackupChannelCut{{Key: key, ID: id, Checkpoint: Checkpoint{Epoch: 1, HW: 3}}}})
	target := openTestMessageStore(t)
	defer target.close(t)
	if _, err := target.db.ImportBackupSnapshot(ctx, body); err != nil {
		t.Fatal(err)
	}
	restored := mustAcquireChannel(t, target.db, key, id)
	defer restored.Close()
	got, ok, err = restored.LoadMQTTSourceState(ctx)
	if err != nil || !ok || got != state {
		t.Fatalf("backup state: %+v %v", got, err)
	}
	rows, err = restored.ReadMQTTProtectedSource(ctx, "source-1", 3, 3, ReadOptions{Limit: 1, MaxBytes: 4096})
	if err != nil || len(rows) != 1 {
		t.Fatalf("restore protected source: %+v %v", rows, err)
	}
	trim, err = restored.TrimPrefixThrough(ctx, 3)
	if err != nil || trim.Deleted != 0 {
		t.Fatalf("restore lost protection: %+v %v", trim, err)
	}
	reader, err := s.db.OpenBackupSnapshot(ctx, BackupSnapshotRequest{HashSlot: 1, Channels: []BackupChannelCut{{Key: key, ID: id, Checkpoint: Checkpoint{Epoch: 1, HW: 1}}}})
	if err == nil {
		_, err = io.ReadAll(reader)
		reader.Close()
	}
	if err == nil {
		t.Fatal("backup accepted copy frontier above selected HW")
	}
}

func TestMQTTSourceProtectionRejectsInvalidTransitionsAndReads(t *testing.T) {
	ctx := context.Background()
	s := openTestMessageStore(t)
	defer s.close(t)
	l := testChannelLog(s)
	defer l.Close()
	if _, err := l.Append(ctx, []Record{{ID: 1, Payload: []byte("a")}, {ID: 2, Payload: []byte("b")}}, AppendOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := l.StoreCheckpoint(ctx, Checkpoint{HW: 1}); err != nil {
		t.Fatal(err)
	}
	initial := MQTTSourceState{Generation: "g", Revision: 1}
	for _, change := range []func(*MQTTSourceState){
		func(s *MQTTSourceState) { s.Generation = "" }, func(s *MQTTSourceState) { s.Generation = "\xff" },
		func(s *MQTTSourceState) { s.Generation = strings.Repeat("g", 129) }, func(s *MQTTSourceState) { s.Revision = 0 },
		func(s *MQTTSourceState) { s.StartAfter, s.CopiedThrough = 2, 2 },
		func(s *MQTTSourceState) { s.CopiedThrough = 1 }, func(s *MQTTSourceState) { s.ReceiptDigest[0] = 1 },
	} {
		bad := initial
		change(&bad)
		if err := l.ApplyMQTTSourceState(ctx, 0, bad); err == nil {
			t.Fatalf("invalid creation: %+v", bad)
		}
	}
	if err := l.ApplyMQTTSourceState(ctx, 0, initial); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*MQTTSourceState){
		func(s *MQTTSourceState) { s.Generation = "different" }, func(s *MQTTSourceState) { s.StartAfter = 1 },
		func(s *MQTTSourceState) { s.CopiedThrough = 2 }, func(s *MQTTSourceState) { s.ReceiptDigest = [32]byte{} },
		func(s *MQTTSourceState) { s.Revision = 3 },
	} {
		bad := MQTTSourceState{Generation: "g", Revision: 2, CopiedThrough: 1, ReceiptDigest: [32]byte{1}}
		change(&bad)
		if err := l.ApplyMQTTSourceState(ctx, 1, bad); err == nil {
			t.Fatalf("invalid advancement: %+v", bad)
		}
	}
	for _, req := range []struct {
		g             string
		from, through uint64
		opts          ReadOptions
	}{
		{"other", 1, 1, ReadOptions{Limit: 1, MaxBytes: 100}}, {"g", 0, 1, ReadOptions{Limit: 1, MaxBytes: 100}},
		{"g", 1, 2, ReadOptions{Limit: 1, MaxBytes: 100}}, {"g", 1, 1, ReadOptions{}},
		{"g", 1, 1, ReadOptions{Limit: 257, MaxBytes: 100}}, {"g", 1, 1, ReadOptions{Limit: 1, MaxBytes: 17 << 20}},
	} {
		if _, err := l.ReadMQTTProtectedSource(ctx, req.g, req.from, req.through, req.opts); err == nil {
			t.Fatalf("invalid read: %+v", req)
		}
	}
	// Missing committed content must never look like an exhausted source.
	b := s.db.engine.NewBatch()
	defer b.Close()
	if err := b.Delete(encodeMessageRowKey(l.key, 1, messageHeaderFamilyID)); err != nil {
		t.Fatal(err)
	}
	if err := b.Commit(true); err != nil {
		t.Fatal(err)
	}
	if _, err := l.ReadMQTTProtectedSource(ctx, "g", 1, 1, ReadOptions{Limit: 1, MaxBytes: 100}); !errors.Is(err, dberrors.ErrCorruptState) {
		t.Fatalf("source gap skipped: %v", err)
	}
}

func TestMQTTSourceProtectionCannotStartBehindPhysicalErasure(t *testing.T) {
	ctx := context.Background()
	s := openTestMessageStore(t)
	defer s.close(t)
	l := testChannelLog(s)
	defer l.Close()
	if _, err := l.Append(ctx, []Record{{ID: 1, Payload: []byte("a")}, {ID: 2, Payload: []byte("b")}}, AppendOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := l.StoreCheckpoint(ctx, Checkpoint{HW: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.TrimPrefixThrough(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if err := l.ApplyMQTTSourceState(ctx, 0, MQTTSourceState{Generation: "g", Revision: 1}); !errors.Is(err, dberrors.ErrConflict) {
		t.Fatalf("created protection after erasure: %v", err)
	}
}

func TestMQTTSourceProtectionAtEmptyLogNeverUsesUnboundedTrim(t *testing.T) {
	ctx := context.Background()
	s := openTestMessageStore(t)
	defer s.close(t)
	l := testChannelLog(s)
	defer l.Close()
	if err := l.ApplyMQTTSourceState(ctx, 0, MQTTSourceState{Generation: "before-first-message", Revision: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(ctx, []Record{{ID: 1, Payload: []byte("protected")}}, AppendOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := l.StoreCheckpoint(ctx, Checkpoint{HW: 1}); err != nil {
		t.Fatal(err)
	}
	trim, err := l.TrimPrefixThrough(ctx, 1)
	if err != nil || trim.Deleted != 0 || trim.More {
		t.Fatalf("zero copied-through became unbounded trim: %+v %v", trim, err)
	}
	rows, err := l.ReadMQTTProtectedSource(ctx, "before-first-message", 1, 1, ReadOptions{Limit: 1, MaxBytes: 100})
	if err != nil || len(rows) != 1 {
		t.Fatalf("first source publication lost: %+v %v", rows, err)
	}
}

func TestMQTTSourceProtectionMissingCheckpointFailsClosed(t *testing.T) {
	ctx := context.Background()
	s := openTestMessageStore(t)
	defer s.close(t)
	l := testChannelLog(s)
	defer l.Close()
	state := MQTTSourceState{Generation: "g", Revision: 1}
	if err := l.ApplyMQTTSourceState(ctx, 0, state); err != nil {
		t.Fatal(err)
	}
	if _, exists, err := l.loadCheckpoint(ctx); err != nil || !exists {
		t.Fatalf("activation did not initialize an explicit checkpoint: %v", err)
	}
	if _, err := l.Append(ctx, []Record{{ID: 1, Payload: []byte("protected")}}, AppendOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := l.StoreCheckpoint(ctx, Checkpoint{HW: 1}); err != nil {
		t.Fatal(err)
	}
	b := s.db.engine.NewBatch()
	defer b.Close()
	if err := b.Delete(encodeCheckpointKey(l.key)); err != nil {
		t.Fatal(err)
	}
	if err := b.Commit(true); err != nil {
		t.Fatal(err)
	}
	if err := l.TruncateFrom(ctx, 1); !errors.Is(err, dberrors.ErrCorruptState) {
		t.Fatalf("missing HW authorized destructive truncation: %v", err)
	}
	if err := l.ApplyMQTTSourceState(ctx, 0, state); !errors.Is(err, dberrors.ErrCorruptState) {
		t.Fatalf("exact retry hid missing HW: %v", err)
	}
}
