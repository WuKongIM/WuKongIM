package raftstore

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	raft "go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
)

func TestStoreImplementsRaftStorageBoundarySemantics(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, Config{Dir: filepath.Join(t.TempDir(), "raft"), NodeID: 1})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()

	if first, err := store.FirstIndex(); err != nil || first != 1 {
		t.Fatalf("empty FirstIndex() = %d, %v", first, err)
	}
	if last, err := store.LastIndex(); err != nil || last != 0 {
		t.Fatalf("empty LastIndex() = %d, %v", last, err)
	}
	entries := []raftpb.Entry{
		{Index: 1, Term: 1, Data: []byte("one")},
		{Index: 2, Term: 1, Data: []byte("two")},
		{Index: 3, Term: 1, Data: []byte("three")},
	}
	if err := store.SaveReady(ctx, raftpb.HardState{Term: 1, Vote: 1, Commit: 3}, entries, raftpb.Snapshot{}); err != nil {
		t.Fatalf("SaveReady() error = %v", err)
	}
	if got, err := store.Entries(2, 2, 0); err != nil || got != nil {
		t.Fatalf("empty Entries() = %#v, %v", got, err)
	}
	if _, err := store.Entries(4, 5, 0); !errors.Is(err, raft.ErrUnavailable) {
		t.Fatalf("missing Entries() error = %v", err)
	}
	limited, err := store.Entries(1, 4, uint64(entries[0].Size()))
	if err != nil || len(limited) != 1 || limited[0].Index != 1 {
		t.Fatalf("size-limited Entries() = %#v, %v", limited, err)
	}
	limited[0].Data[0] = 'X'
	again, err := store.Entries(1, 2, 0)
	if err != nil || string(again[0].Data) != "one" {
		t.Fatalf("Entries() exposed mutable storage: %#v, %v", again, err)
	}
	if term, err := store.Term(1); err != nil || term != 1 {
		t.Fatalf("Term(1) = %d, %v", term, err)
	}
	if _, err := store.Term(4); !errors.Is(err, raft.ErrUnavailable) {
		t.Fatalf("Term(4) error = %v", err)
	}

	snapshot := raftpb.Snapshot{
		Data: []byte("state-at-two"),
		Metadata: raftpb.SnapshotMetadata{
			Index: 2,
			Term:  1,
			ConfState: raftpb.ConfState{
				Voters: []uint64{1},
			},
		},
	}
	if err := store.SaveSnapshot(ctx, snapshot); err != nil {
		t.Fatalf("SaveSnapshot() error = %v", err)
	}
	if term, err := store.Term(2); err != nil || term != 1 {
		t.Fatalf("snapshot Term(2) = %d, %v", term, err)
	}
	if _, err := store.Term(1); !errors.Is(err, raft.ErrCompacted) {
		t.Fatalf("compacted Term(1) error = %v", err)
	}
	if _, err := store.Entries(2, 3, 0); !errors.Is(err, raft.ErrCompacted) {
		t.Fatalf("compacted Entries() error = %v", err)
	}
	if first, err := store.FirstIndex(); err != nil || first != 3 {
		t.Fatalf("snapshotted FirstIndex() = %d, %v", first, err)
	}

	loaded, err := store.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	loaded.Data[0] = 'X'
	loaded.Metadata.ConfState.Voters[0] = 9
	loadedAgain, _ := store.Snapshot()
	if string(loadedAgain.Data) != "state-at-two" || loadedAgain.Metadata.ConfState.Voters[0] != 1 {
		t.Fatalf("Snapshot() exposed mutable storage: %#v", loadedAgain)
	}

	if err := store.MarkAppliedBatch(ctx, 3); err != nil {
		t.Fatalf("MarkAppliedBatch() error = %v", err)
	}
	if err := store.MarkAppliedBatch(ctx, 2); err != nil || store.AppliedIndex() != 3 {
		t.Fatalf("non-advancing applied index = %d, %v", store.AppliedIndex(), err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := store.Compact(canceled, 2); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Compact() error = %v", err)
	}
}

func TestStoreRejectsInvalidConfigCancellationAndClosedWAL(t *testing.T) {
	if _, err := Open(context.Background(), Config{NodeID: 1}); err == nil {
		t.Fatal("empty storage directory unexpectedly accepted")
	}
	if _, err := Open(context.Background(), Config{Dir: t.TempDir()}); err == nil {
		t.Fatal("zero node ID unexpectedly accepted")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Open(canceled, Config{Dir: t.TempDir(), NodeID: 1}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Open() error = %v", err)
	}

	store, err := Open(context.Background(), Config{Dir: t.TempDir(), NodeID: 1})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if store.cfg.SegmentSize != defaultWALSegmentSize || !filepath.IsAbs(store.cfg.Dir) {
		t.Fatalf("normalized config = %#v", store.cfg)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if err := (*Store)(nil).Close(); err != nil {
		t.Fatalf("nil Close() error = %v", err)
	}
	if err := store.SaveReady(context.Background(), raftpb.HardState{Term: 1}, nil, raftpb.Snapshot{}); err == nil {
		t.Fatal("SaveReady() on a closed WAL unexpectedly succeeded")
	}
	if err := store.MarkAppliedBatch(context.Background(), 1); err == nil {
		t.Fatal("MarkAppliedBatch() on a closed WAL unexpectedly succeeded")
	}
}

func TestSnapshotAndMetadataFilesFailClosed(t *testing.T) {
	ctx := context.Background()
	if snapshot, err := loadSnapshotFile(""); err != nil || !raft.IsEmptySnap(snapshot) {
		t.Fatalf("empty snapshot path = %#v, %v", snapshot, err)
	}
	missing := filepath.Join(t.TempDir(), "missing.snap")
	if snapshot, err := loadSnapshotFile(missing); err != nil || !raft.IsEmptySnap(snapshot) {
		t.Fatalf("missing snapshot = %#v, %v", snapshot, err)
	}

	dir := t.TempDir()
	malformed := filepath.Join(dir, "malformed.snap")
	if err := os.WriteFile(malformed, []byte("not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSnapshotFile(malformed); err == nil {
		t.Fatal("malformed snapshot unexpectedly loaded")
	}
	corrupt := filepath.Join(dir, "corrupt.snap")
	body, err := json.Marshal(snapshotEnvelope{
		Version:  snapshotVersion,
		Metadata: raftpb.SnapshotMetadata{Index: 3, Term: 2},
		Data:     []byte("state"),
		Checksum: "incorrect",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(corrupt, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSnapshotFile(corrupt); err == nil {
		t.Fatal("snapshot checksum mismatch unexpectedly loaded")
	}

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := saveSnapshotFile(canceled, dir, raftpb.Snapshot{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled snapshot save error = %v", err)
	}
	legacy := filepath.Join(dir, "legacy.snap")
	body, err = json.Marshal(snapshotEnvelope{
		Version:  snapshotVersion,
		Metadata: raftpb.SnapshotMetadata{Index: 4, Term: 2},
		Data:     []byte("legacy-state"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if snapshot, err := loadSnapshotFile(legacy); err != nil || string(snapshot.Data) != "legacy-state" {
		t.Fatalf("legacy snapshot = %#v, %v", snapshot, err)
	}

	metaPath := filepath.Join(dir, "meta", "meta.json")
	meta := metadata{NodeID: 7, AppliedIndex: 11}
	if err := saveMetadata(ctx, metaPath, meta); err != nil {
		t.Fatalf("saveMetadata() error = %v", err)
	}
	loadedMeta, err := loadMetadata(metaPath)
	if err != nil || loadedMeta.Version != metadataVersion || loadedMeta.NodeID != 7 || loadedMeta.AppliedIndex != 11 {
		t.Fatalf("loaded metadata = %#v, %v", loadedMeta, err)
	}
	if err := saveMetadata(canceled, metaPath, meta); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled metadata save error = %v", err)
	}
	if empty, err := loadMetadata(filepath.Join(dir, "absent.json")); err != nil || !reflect.DeepEqual(empty, metadata{}) {
		t.Fatalf("missing metadata = %#v, %v", empty, err)
	}
	badMeta := filepath.Join(dir, "bad-meta.json")
	if err := os.WriteFile(badMeta, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadMetadata(badMeta); err == nil {
		t.Fatal("malformed metadata unexpectedly loaded")
	}
	if err := syncDir(filepath.Join(dir, "missing-dir")); err == nil {
		t.Fatal("syncDir() unexpectedly opened a missing directory")
	}
}

func TestPersistedMembershipChangesReplayExactly(t *testing.T) {
	var conf raftpb.ConfState
	applyConfChange(&conf, raftpb.ConfChangeAddNode, 1)
	applyConfChange(&conf, raftpb.ConfChangeAddNode, 1)
	applyConfChange(&conf, raftpb.ConfChangeAddLearnerNode, 1)
	applyConfChange(&conf, raftpb.ConfChangeAddLearnerNode, 2)
	if len(conf.Voters) != 1 || conf.Voters[0] != 1 || len(conf.Learners) != 1 || conf.Learners[0] != 2 {
		t.Fatalf("membership after adds = %#v", conf)
	}
	applyConfChange(&conf, raftpb.ConfChangeAddNode, 2)
	if !contains(conf.Voters, 2) || contains(conf.Learners, 2) {
		t.Fatalf("promoted learner membership = %#v", conf)
	}
	conf.VotersOutgoing = []uint64{2}
	conf.LearnersNext = []uint64{2}
	applyConfChange(&conf, raftpb.ConfChangeRemoveNode, 2)
	if contains(conf.Voters, 2) || contains(conf.VotersOutgoing, 2) || contains(conf.LearnersNext, 2) {
		t.Fatalf("removed node remains in membership = %#v", conf)
	}

	change := raftpb.ConfChangeV2{Changes: []raftpb.ConfChangeSingle{
		{Type: raftpb.ConfChangeAddLearnerNode, NodeID: 3},
		{Type: raftpb.ConfChangeRemoveNode, NodeID: 1},
	}}
	data, err := change.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if err := applyConfEntry(&conf, raftpb.Entry{Type: raftpb.EntryConfChangeV2, Data: data}); err != nil {
		t.Fatalf("apply ConfChangeV2: %v", err)
	}
	if contains(conf.Voters, 1) || !contains(conf.Learners, 3) {
		t.Fatalf("membership after v2 change = %#v", conf)
	}
	if err := applyConfEntry(&conf, raftpb.Entry{Type: raftpb.EntryConfChange, Data: []byte{0xff}}); err == nil {
		t.Fatal("malformed membership entry unexpectedly applied")
	}
	if err := applyConfEntry(&conf, raftpb.Entry{Type: raftpb.EntryConfChangeV2, Data: []byte{0xff}}); err == nil {
		t.Fatal("malformed v2 membership entry unexpectedly applied")
	}
}

func TestRecordDecodersRejectIncompletePayloadsAndUnknownTypes(t *testing.T) {
	for name, payload := range map[string][]byte{
		"missing count": nil,
		"missing size":  {0, 0, 0, 1},
		"short entry":   {0, 0, 0, 1, 0, 0, 0, 2, 0},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := unmarshalEntryRecord(payload); !errors.Is(err, ErrTruncatedRecord) {
				t.Fatalf("unmarshalEntryRecord() error = %v", err)
			}
		})
	}
	if _, err := unmarshalUint64([]byte{1}); !errors.Is(err, ErrTruncatedRecord) {
		t.Fatalf("unmarshalUint64() error = %v", err)
	}

	var shortLength [4]byte
	binary.BigEndian.PutUint32(shortLength[:], 4)
	if _, _, err := readRecord(bytes.NewReader(shortLength[:]), 0); !errors.Is(err, ErrTruncatedRecord) {
		t.Fatalf("short frame length error = %v", err)
	}
	binary.BigEndian.PutUint32(shortLength[:], 10)
	if _, _, err := readRecord(bytes.NewReader(append(shortLength[:], 1, 2)), 0); !errors.Is(err, ErrTruncatedRecord) {
		t.Fatalf("truncated frame error = %v", err)
	}
	if err := applyRecord(&replayState{}, walRecord{Type: recordType(255)}); err == nil {
		t.Fatal("unknown WAL record type unexpectedly applied")
	}
	for _, name := range []string{"bad", "zzzz-0001.wal", "0001-zzzz.wal"} {
		if _, _, err := parseSegmentName(name); err == nil {
			t.Fatalf("invalid segment name %q unexpectedly parsed", name)
		}
	}
}
