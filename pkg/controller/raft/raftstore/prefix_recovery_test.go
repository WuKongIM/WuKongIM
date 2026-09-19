package raftstore

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"go.etcd.io/raft/v3/raftpb"
)

// legacyPrunedStore models the pre-upgrade writer, including its cross-file CRC.
func legacyPrunedStore(t *testing.T) (Config, raftpb.Snapshot, []raftpb.Entry) {
	t.Helper()
	ctx := context.Background()
	cfg := Config{Dir: t.TempDir(), NodeID: 103, SegmentSize: 512}
	snap := raftpb.Snapshot{Data: []byte("durable snapshot"), Metadata: raftpb.SnapshotMetadata{Index: 6, Term: 1, ConfState: raftpb.ConfState{Voters: []uint64{101, 102, 103}}}}
	path, err := saveSnapshotFile(ctx, filepath.Join(cfg.Dir, "snap"), snap)
	require.NoError(t, err)
	m := metadata{Version: metadataVersion, NodeID: 103, HardState: raftpb.HardState{Term: 1, Vote: 103, Commit: 8}, AppliedIndex: 8, Snapshot: snapshotMeta{Index: 6, Term: 1, Path: path}, ConfState: snap.Metadata.ConfState}
	require.NoError(t, saveMetadata(ctx, filepath.Join(cfg.Dir, "meta.json"), m))
	dir := filepath.Join(cfg.Dir, "wal")
	require.NoError(t, os.Mkdir(dir, 0755))
	var crc uint32
	var suffix []raftpb.Entry
	for seq := uint64(0); seq < 2; seq++ {
		f, err := os.Create(filepath.Join(dir, segmentName(seq, seq*4+1)))
		require.NoError(t, err)
		appendRecord := func(typ recordType, payload []byte) {
			r := walRecord{Type: typ, Payload: payload}
			require.NoError(t, writeRecord(f, r, crc))
			crc = recordCRC(crc, typ, payload)
		}
		appendRecord(recordSegmentHeader, marshalUint64(103))
		for i := seq*4 + 1; i <= seq*4+4; i++ {
			e := raftpb.Entry{Index: i, Term: 1, Data: bytes.Repeat([]byte{byte(i)}, 32)}
			p, err := marshalEntryRecord([]raftpb.Entry{e})
			require.NoError(t, err)
			appendRecord(recordEntries, p)
			if i > 6 {
				suffix = append(suffix, e)
			}
		}
		if seq == 1 {
			p, err := marshalSnapshotRecord(snap.Metadata)
			require.NoError(t, err)
			appendRecord(recordSnapshot, p)
			p, err = marshalHardStateRecord(m.HardState)
			require.NoError(t, err)
			appendRecord(recordHardState, p)
			appendRecord(recordAppliedIndex, marshalUint64(8))
		}
		require.NoError(t, f.Close())
	}
	require.NoError(t, os.Remove(filepath.Join(dir, segmentName(0, 1))))
	return cfg, snap, suffix
}

func TestLegacyPrunedPrefixUpgradePreservesStateAndAppendRestart(t *testing.T) {
	cfg, snap, suffix := legacyPrunedStore(t)
	ctx := context.Background()
	original := filepath.Join(cfg.Dir, "wal", segmentName(1, 5))
	before, err := os.ReadFile(original)
	require.NoError(t, err)
	s, err := Open(ctx, cfg)
	require.NoError(t, err)
	got, err := s.Snapshot()
	require.NoError(t, err)
	require.Equal(t, snap, got)
	entries, err := s.Entries(7, 9, 0)
	require.NoError(t, err)
	require.Equal(t, suffix, entries)
	hs, conf, err := s.InitialState()
	require.NoError(t, err)
	require.Equal(t, raftpb.HardState{Term: 1, Vote: 103, Commit: 8}, hs)
	require.Equal(t, snap.Metadata.ConfState, conf)
	require.Equal(t, uint64(8), s.AppliedIndex())
	require.NoError(t, s.Close())
	after, err := os.ReadFile(original)
	require.NoError(t, err)
	require.Equal(t, before, after)
	s, err = Open(ctx, cfg)
	require.NoError(t, err)
	for i := uint64(9); i <= 24; i++ {
		require.NoError(t, s.SaveReady(ctx, raftpb.HardState{Term: 1, Vote: 103, Commit: i}, []raftpb.Entry{{Index: i, Term: 1, Data: bytes.Repeat([]byte("x"), 128)}}, raftpb.Snapshot{}))
	}
	require.NoError(t, s.MarkAppliedBatch(ctx, 24))
	snap.Metadata.Index = 22
	require.NoError(t, s.SaveSnapshot(ctx, snap))
	require.NoError(t, s.Compact(ctx, 20))
	require.NoError(t, s.Close())
	for i := 0; i < 2; i++ {
		s, err = Open(ctx, cfg)
		require.NoError(t, err)
		require.Equal(t, uint64(24), s.AppliedIndex())
		entries, err = s.Entries(23, 25, 0)
		require.NoError(t, err)
		require.Len(t, entries, 2)
		require.NoError(t, s.Close())
	}
}

func TestLegacyPrunedPrefixRejectsUnprovenOrCorruptData(t *testing.T) {
	for _, kind := range []string{"payload", "header_crc", "header_node", "header_type", "truncated", "missing_snapshot", "snapshot_checksum", "metadata_commit", "metadata_vote", "metadata_node", "missing_meta", "missing_suffix"} {
		t.Run(kind, func(t *testing.T) {
			cfg, _, _ := legacyPrunedStore(t)
			walPath := filepath.Join(cfg.Dir, "wal", segmentName(1, 5))
			data, err := os.ReadFile(walPath)
			require.NoError(t, err)
			metaPath := filepath.Join(cfg.Dir, "meta.json")
			m, err := loadMetadata(metaPath)
			require.NoError(t, err)
			switch kind {
			case "payload":
				data[30] ^= 1
			case "header_crc":
				data[5] ^= 1
			case "header_node":
				data[16] ^= 1
			case "header_type":
				data[4] = byte(recordEntries)
			case "truncated":
				data = data[:len(data)-1]
			case "missing_snapshot":
				require.NoError(t, os.Remove(m.Snapshot.Path))
			case "snapshot_checksum":
				b, err := os.ReadFile(m.Snapshot.Path)
				require.NoError(t, err)
				b = bytes.Replace(b, []byte(`"checksum": "`), []byte(`"checksum": "ff`), 1)
				require.NoError(t, os.WriteFile(m.Snapshot.Path, b, 0644))
			case "metadata_commit":
				m.HardState.Commit++
				require.NoError(t, saveMetadata(context.Background(), metaPath, m))
			case "metadata_vote":
				m.HardState.Vote = 101
				require.NoError(t, saveMetadata(context.Background(), metaPath, m))
			case "metadata_node":
				m.NodeID = 101
				require.NoError(t, saveMetadata(context.Background(), metaPath, m))
			case "missing_meta":
				require.NoError(t, os.Remove(metaPath))
			case "missing_suffix": // A physically valid but logically missing final entry is not repairable.
				var out bytes.Buffer
				var crc uint32
				for pos := 0; pos < len(data); {
					n := int(binary.BigEndian.Uint32(data[pos : pos+4]))
					frame := data[pos+4 : pos+4+n]
					r := walRecord{Type: recordType(frame[0]), Payload: frame[5:]}
					skip := false
					if r.Type == recordEntries {
						es, err := unmarshalEntryRecord(r.Payload)
						require.NoError(t, err)
						skip = es[0].Index == 8
					}
					if !skip {
						if pos == 0 {
							out.Write(data[:4+n])
							crc = binary.BigEndian.Uint32(frame[1:5])
						} else {
							require.NoError(t, writeRecord(&out, r, crc))
							crc = recordCRC(crc, r.Type, r.Payload)
						}
					}
					pos += 4 + n
				}
				data = out.Bytes()
			}
			require.NoError(t, os.WriteFile(walPath, data, 0644))
			before := map[string][]byte{}
			require.NoError(t, filepath.WalkDir(cfg.Dir, func(p string, d os.DirEntry, e error) error {
				if e != nil {
					return e
				}
				if !d.IsDir() {
					b, e := os.ReadFile(p)
					if e != nil {
						return e
					}
					before[p] = b
				}
				return nil
			}))
			s, err := Open(context.Background(), cfg)
			if s != nil {
				_ = s.Close()
			}
			require.Error(t, err)
			for p, b := range before {
				got, e := os.ReadFile(p)
				require.NoError(t, e)
				require.Equal(t, b, got, p)
			}
		})
	}
}

func TestCompactedSegmentsRemainRestartable(t *testing.T) {
	ctx := context.Background()
	cfg := Config{Dir: t.TempDir(), NodeID: 1, SegmentSize: 256}
	s, err := Open(ctx, cfg)
	require.NoError(t, err)
	for i := uint64(1); i <= 20; i++ {
		require.NoError(t, s.SaveReady(ctx, raftpb.HardState{Term: 1, Vote: 1, Commit: i}, []raftpb.Entry{{Index: i, Term: 1, Data: bytes.Repeat([]byte("x"), 128)}}, raftpb.Snapshot{}))
	}
	require.NoError(t, s.MarkAppliedBatch(ctx, 20))
	require.NoError(t, s.SaveSnapshot(ctx, raftpb.Snapshot{Data: []byte("snapshot"), Metadata: raftpb.SnapshotMetadata{Index: 18, Term: 1, ConfState: raftpb.ConfState{Voters: []uint64{1}}}}))
	before, err := walSegmentFiles(filepath.Join(cfg.Dir, "wal"))
	require.NoError(t, err)
	require.NoError(t, s.Compact(ctx, 15))
	after, err := walSegmentFiles(filepath.Join(cfg.Dir, "wal"))
	require.NoError(t, err)
	require.Less(t, len(after), len(before))
	require.NoError(t, s.Close())
	s, err = Open(ctx, cfg)
	require.NoError(t, err)
	defer s.Close()
	entries, err := s.Entries(19, 21, 0)
	require.NoError(t, err)
	require.Len(t, entries, 2)
}

func TestLegacyCRCErrorRemainsDiscoverable(t *testing.T) {
	cfg, _, _ := legacyPrunedStore(t)
	p := filepath.Join(cfg.Dir, "wal", segmentName(1, 5))
	b, err := os.ReadFile(p)
	require.NoError(t, err)
	b[len(b)-1] ^= 1
	require.NoError(t, os.WriteFile(p, b, 0644))
	_, err = Open(context.Background(), cfg)
	require.True(t, errors.Is(err, ErrCRCMismatch))
}

func TestCompactionRetainsSegmentStraddlingBoundary(t *testing.T) {
	ctx := context.Background()
	cfg := Config{Dir: t.TempDir(), NodeID: 1, SegmentSize: 1 << 20}
	s, err := Open(ctx, cfg)
	require.NoError(t, err)
	entries := []raftpb.Entry{{Index: 1, Term: 1}, {Index: 2, Term: 1}, {Index: 3, Term: 1}}
	require.NoError(t, s.SaveReady(ctx, raftpb.HardState{Term: 1, Commit: 3}, entries, raftpb.Snapshot{}))
	// Force a cut without depending on the encoded record sizes.
	s.wal.cfg.SegmentSize = 1
	require.NoError(t, s.MarkAppliedBatch(ctx, 3))
	s.wal.cfg.SegmentSize = 1 << 20
	snap := raftpb.Snapshot{Data: []byte("snapshot"), Metadata: raftpb.SnapshotMetadata{Index: 2, Term: 1, ConfState: raftpb.ConfState{Voters: []uint64{1}}}}
	require.NoError(t, s.SaveSnapshot(ctx, snap))
	require.NoError(t, s.Compact(ctx, 2))
	require.NoError(t, s.Close())
	s, err = Open(ctx, cfg)
	require.NoError(t, err)
	defer s.Close()
	got, err := s.Entries(3, 4, 0)
	require.NoError(t, err)
	require.Equal(t, entries[2:], got)
}

func TestV2HeaderCorruptionCannotUseLegacyRecovery(t *testing.T) {
	cfg, _, _ := legacyPrunedStore(t)
	p := filepath.Join(cfg.Dir, "wal", segmentName(1, 5))
	old, err := os.ReadFile(p)
	require.NoError(t, err)
	var b bytes.Buffer
	crc := uint32(0)
	for pos := 0; pos < len(old); {
		n := int(binary.BigEndian.Uint32(old[pos : pos+4]))
		frame := old[pos+4 : pos+4+n]
		rec := walRecord{Type: recordType(frame[0]), Payload: frame[5:]}
		if pos == 0 {
			rec = walRecord{Type: recordSegmentHeaderV2, Payload: segmentHeaderV2(cfg.NodeID)}
		}
		require.NoError(t, writeRecord(&b, rec, crc))
		crc = recordCRC(crc, rec.Type, rec.Payload)
		pos += 4 + n
	}
	data := b.Bytes()
	data[4] = byte(recordSegmentHeader)
	require.NoError(t, os.WriteFile(p, data, 0644))
	_, err = Open(context.Background(), cfg)
	require.ErrorIs(t, err, ErrCRCMismatch)
	got, err := os.ReadFile(p)
	require.NoError(t, err)
	require.Equal(t, data, got)
}

func TestRecoveredLegacyPrefixSurvivesWALAheadOfMetadataAndTornTail(t *testing.T) {
	for _, kind := range []string{"hard_state", "applied_index", "torn_tail"} {
		t.Run(kind, func(t *testing.T) {
			cfg, _, _ := legacyPrunedStore(t)
			s, err := Open(context.Background(), cfg)
			require.NoError(t, err)
			switch kind {
			case "hard_state":
				require.NoError(t, s.wal.appendReady(context.Background(), raftpb.HardState{Term: 2, Vote: 103, Commit: 9}, []raftpb.Entry{{Index: 9, Term: 2, Data: []byte("nine")}}, raftpb.SnapshotMetadata{}))
			case "applied_index":
				require.NoError(t, s.SaveReady(context.Background(), raftpb.HardState{Term: 1, Vote: 103, Commit: 9}, []raftpb.Entry{{Index: 9, Term: 1, Data: []byte("nine")}}, raftpb.Snapshot{}))
				require.NoError(t, s.wal.appendAppliedIndex(context.Background(), 9))
			case "torn_tail":
				_, err = s.wal.file.Write([]byte{0, 0})
				require.NoError(t, err)
			}
			require.NoError(t, s.Close())
			s, err = Open(context.Background(), cfg)
			require.NoError(t, err)
			defer s.Close()
			if kind == "hard_state" {
				hs, _, err := s.InitialState()
				require.NoError(t, err)
				require.Equal(t, uint64(9), hs.Commit)
				require.Equal(t, uint64(2), hs.Term)
			}
			if kind == "applied_index" {
				require.Equal(t, uint64(9), s.AppliedIndex())
			}
		})
	}
}

func TestInterruptedLegacyCompactionKeepsEveryPrefixRecoverable(t *testing.T) {
	for _, stopAfter := range []int{1, 2} {
		t.Run(fmt.Sprint(stopAfter), func(t *testing.T) {
			ctx := context.Background()
			cfg := Config{Dir: t.TempDir(), NodeID: 1}
			snap := raftpb.Snapshot{Data: []byte("covered snapshot"), Metadata: raftpb.SnapshotMetadata{Index: 8, Term: 1, ConfState: raftpb.ConfState{Voters: []uint64{1}}}}
			path, err := saveSnapshotFile(ctx, filepath.Join(cfg.Dir, "snap"), snap)
			require.NoError(t, err)
			hs := raftpb.HardState{Term: 1, Vote: 1, Commit: 8}
			require.NoError(t, saveMetadata(ctx, filepath.Join(cfg.Dir, "meta.json"), metadata{Version: 1, NodeID: 1, HardState: hs, AppliedIndex: 8, Snapshot: snapshotMeta{Index: 8, Term: 1, Path: path}, ConfState: snap.Metadata.ConfState}))
			walDir := filepath.Join(cfg.Dir, "wal")
			require.NoError(t, os.Mkdir(walDir, 0755))
			var crc uint32
			for seq := uint64(0); seq < 3; seq++ {
				first := uint64(9)
				if seq == 0 {
					first = 1
				}
				f, err := os.Create(filepath.Join(walDir, segmentName(seq, first)))
				require.NoError(t, err)
				appendRecord := func(typ recordType, payload []byte) {
					rec := walRecord{Type: typ, Payload: payload}
					require.NoError(t, writeRecord(f, rec, crc))
					crc = recordCRC(crc, typ, payload)
				}
				if seq == 2 {
					appendRecord(recordSegmentHeaderV2, segmentHeaderV2(1))
				} else {
					appendRecord(recordSegmentHeader, marshalUint64(1))
				}
				if seq == 0 {
					p, err := marshalEntryRecord([]raftpb.Entry{{Index: 1, Term: 1}, {Index: 8, Term: 1}})
					require.NoError(t, err)
					appendRecord(recordEntries, p)
					p, err = marshalSnapshotRecord(snap.Metadata)
					require.NoError(t, err)
					appendRecord(recordSnapshot, p)
				}
				if seq < 2 {
					p, err := marshalHardStateRecord(hs)
					require.NoError(t, err)
					appendRecord(recordHardState, p)
					appendRecord(recordAppliedIndex, marshalUint64(8))
				}
				require.NoError(t, f.Close())
			}
			s, err := Open(ctx, cfg)
			require.NoError(t, err)
			removed := 0
			interrupted := errors.New("power interruption")
			s.wal.afterRelease = func(string) error {
				removed++
				if removed == stopAfter {
					return interrupted
				}
				return nil
			}
			require.ErrorIs(t, s.Compact(ctx, 8), interrupted)
			require.NoError(t, s.Close())
			s, err = Open(ctx, cfg)
			require.NoError(t, err)
			defer s.Close()
			got, err := s.Snapshot()
			require.NoError(t, err)
			require.Equal(t, snap, got)
			require.Equal(t, uint64(8), s.AppliedIndex())
		})
	}
}

func TestLegacyAnchorCorruptionFailsClosed(t *testing.T) {
	cfg, _, _ := legacyPrunedStore(t)
	s, err := Open(context.Background(), cfg)
	require.NoError(t, err)
	require.NoError(t, s.Close())
	path := filepath.Join(cfg.Dir, "wal", legacyAnchorFile)
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	b = bytes.Replace(b, []byte(`"checksum":"`), []byte(`"checksum":"00`), 1)
	require.NoError(t, os.WriteFile(path, b, 0600))
	_, err = Open(context.Background(), cfg)
	require.Error(t, err)
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, b, got)
}
