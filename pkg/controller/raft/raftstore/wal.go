package raftstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	raft "go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
)

type walConfig struct {
	Dir         string
	NodeID      uint64
	SegmentSize uint64
	// legacyPrefix is enabled only by the read-only recovery proof in legacy.go.
	legacyPrefix bool
}

type replayState struct {
	HardState    raftpb.HardState
	Entries      []raftpb.Entry
	Snapshot     raftpb.SnapshotMetadata
	AppliedIndex uint64
	ConfState    raftpb.ConfState
}

type wal struct {
	cfg walConfig

	mu        sync.Mutex
	file      *os.File
	seq       uint64
	first     uint64
	lastIndex uint64
	crc       uint32
	hardState raftpb.HardState
	// legacyHeaderAccepted records a missing predecessor CRC, never body corruption.
	legacyHeaderAccepted bool
	anchors              map[string]uint32
	// afterRelease is a test fault seam after one deletion has been synced.
	afterRelease func(string) error
}

func openWAL(cfg walConfig) (*wal, error) {
	if cfg.SegmentSize == 0 {
		cfg.SegmentSize = defaultWALSegmentSize
	}
	if err := os.MkdirAll(cfg.Dir, 0o755); err != nil {
		return nil, err
	}
	anchors, err := loadLegacyAnchors(cfg.Dir, cfg.NodeID)
	if err != nil {
		return nil, err
	}
	w := &wal{cfg: cfg, anchors: anchors}
	files, err := walSegmentFiles(cfg.Dir)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return w.createSegment(0, 1)
	}
	if err := w.loadTailState(files); err != nil {
		return nil, err
	}
	return w.openTail(files[len(files)-1])
}

func (w *wal) replay() (replayState, error) { return w.replayMode(true) }

func (w *wal) replayMode(allowIncompleteTail bool) (replayState, error) {
	files, err := walSegmentFiles(w.cfg.Dir)
	if err != nil {
		return replayState{}, err
	}
	state := replayState{}
	var crc uint32
	for fileIdx, path := range files {
		if fileIdx > 0 {
			prev, _, err := parseSegmentName(filepath.Base(files[fileIdx-1]))
			if err != nil {
				return replayState{}, err
			}
			seq, _, err := parseSegmentName(filepath.Base(path))
			if err != nil {
				return replayState{}, err
			}
			if seq != prev+1 {
				return replayState{}, fmt.Errorf("controller/raftstore: missing WAL segment before %s", filepath.Base(path))
			}
		}
		f, err := os.Open(path)
		if err != nil {
			return replayState{}, err
		}
		sawCompleteRecord := false
		for {
			rec, nextCRC, err := w.readWALRecord(f, crc, fileIdx, sawCompleteRecord)
			if err != nil {
				if errors.Is(err, io.EOF) {
					if !sawCompleteRecord {
						_ = f.Close()
						return replayState{}, fmt.Errorf("%w: empty WAL segment %s", ErrTruncatedRecord, filepath.Base(path))
					}
					break
				}
				if allowIncompleteTail && errors.Is(err, ErrTruncatedRecord) && fileIdx == len(files)-1 && sawCompleteRecord {
					break
				}
				_ = f.Close()
				return replayState{}, err
			}
			sawCompleteRecord = true
			crc = nextCRC
			if err := applyRecord(&state, rec); err != nil {
				_ = f.Close()
				return replayState{}, err
			}
		}
		if err := f.Close(); err != nil {
			return replayState{}, err
		}
	}
	return state, nil
}

// readWALRecord validates segment structure and node identity in addition to CRC.
func (w *wal) readWALRecord(f *os.File, crc uint32, fileIdx int, saw bool) (walRecord, uint32, error) {
	offset, err := f.Seek(0, io.SeekCurrent)
	if err != nil {
		return walRecord{}, crc, err
	}
	anchor, anchored := w.anchors[filepath.Base(f.Name())]
	anchored = anchored && fileIdx == 0 && !saw
	legacy := (w.cfg.legacyPrefix || anchored) && fileIdx == 0 && !saw
	rec, next, err := readRecordMode(f, crc, legacy)
	if err != nil {
		if errors.Is(err, io.EOF) {
			return rec, next, err
		}
		return rec, next, fmt.Errorf("%w: segment %s offset %d", err, filepath.Base(f.Name()), offset)
	}
	if anchored && (rec.Type != recordSegmentHeader || next != anchor) {
		return walRecord{}, crc, fmt.Errorf("%w: legacy anchor differs for %s", ErrCRCMismatch, filepath.Base(f.Name()))
	}
	header := rec.Type == recordSegmentHeader || rec.Type == recordSegmentHeaderV2
	if header == saw {
		return walRecord{}, crc, fmt.Errorf("controller/raftstore: invalid segment header placement: %s offset %d", filepath.Base(f.Name()), offset)
	}
	if header {
		payload := rec.Payload
		if rec.Type == recordSegmentHeaderV2 {
			if len(payload) != 16 || string(payload[:8]) != "WKWAL002" {
				return walRecord{}, crc, fmt.Errorf("controller/raftstore: invalid WAL v2 header: %s", filepath.Base(f.Name()))
			}
			payload = payload[8:]
		}
		node, err := unmarshalUint64(payload)
		if err != nil || node != w.cfg.NodeID {
			return walRecord{}, crc, fmt.Errorf("controller/raftstore: WAL segment node identity mismatch: %s", filepath.Base(f.Name()))
		}
		if legacy && rec.Type == recordSegmentHeader && recordCRC(crc, rec.Type, rec.Payload) != next {
			w.legacyHeaderAccepted = true
		}
	}
	return rec, next, nil
}

func (w *wal) appendReady(ctx context.Context, hardState raftpb.HardState, entries []raftpb.Entry, snapshot raftpb.SnapshotMetadata) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return fmt.Errorf("controller/raftstore: wal is closed")
	}
	mustSync := raft.MustSync(hardState, w.hardState, len(entries)) || snapshot.Index > 0
	if len(entries) > 0 {
		payload, err := marshalEntryRecord(entries)
		if err != nil {
			return err
		}
		if err := w.writeLocked(walRecord{Type: recordEntries, Payload: payload}); err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.Index > w.lastIndex {
				w.lastIndex = entry.Index
			}
		}
	}
	if !raft.IsEmptyHardState(hardState) {
		payload, err := marshalHardStateRecord(hardState)
		if err != nil {
			return err
		}
		if err := w.writeLocked(walRecord{Type: recordHardState, Payload: payload}); err != nil {
			return err
		}
		w.hardState = hardState
	}
	if snapshot.Index > 0 {
		payload, err := marshalSnapshotRecord(snapshot)
		if err != nil {
			return err
		}
		if err := w.writeLocked(walRecord{Type: recordSnapshot, Payload: payload}); err != nil {
			return err
		}
		if snapshot.Index > w.lastIndex {
			w.lastIndex = snapshot.Index
		}
	}
	if mustSync {
		if err := w.file.Sync(); err != nil {
			return err
		}
	}
	return w.cutIfNeededLocked()
}

func (w *wal) appendAppliedIndex(ctx context.Context, index uint64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return fmt.Errorf("controller/raftstore: wal is closed")
	}
	if err := w.writeLocked(walRecord{Type: recordAppliedIndex, Payload: marshalUint64(index)}); err != nil {
		return err
	}
	if err := w.file.Sync(); err != nil {
		return err
	}
	return w.cutIfNeededLocked()
}

// releaseBefore removes only a complete prefix whose entries are all covered.
// A segment's first index alone cannot prove that its last entry is disposable.
func (w *wal) releaseBefore(index uint64) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	files, err := walSegmentFiles(w.cfg.Dir)
	if err != nil {
		return err
	}
	var crc uint32
	removeCount := 0
	legacySuccessors := make(map[string]uint32)
	for fileIdx, path := range files {
		if fileIdx == len(files)-1 {
			break
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		var maxIndex uint64
		saw := false
		for {
			rec, next, err := w.readWALRecord(f, crc, fileIdx, saw)
			if errors.Is(err, io.EOF) && saw {
				break
			}
			if err != nil {
				_ = f.Close()
				return err
			}
			saw, crc = true, next
			if rec.Type == recordEntries {
				entries, err := unmarshalEntryRecord(rec.Payload)
				if err != nil {
					_ = f.Close()
					return err
				}
				for _, entry := range entries {
					if entry.Index > maxIndex {
						maxIndex = entry.Index
					}
				}
			}
		}
		if err := f.Close(); err != nil {
			return err
		}
		if maxIndex >= index {
			break
		}
		// Retained legacy headers need their validated boundary persisted before
		// deletion; new-format headers carry their own independent checksum.
		next, err := os.Open(files[fileIdx+1])
		if err != nil {
			return err
		}
		header, headerCRC, err := readRecord(next, crc)
		_ = next.Close()
		if err != nil {
			return err
		}
		if header.Type == recordSegmentHeader {
			legacySuccessors[files[fileIdx+1]] = headerCRC
		} else if header.Type != recordSegmentHeaderV2 {
			return fmt.Errorf("controller/raftstore: invalid successor segment header")
		}
		removeCount = fileIdx + 1
	}
	for i, path := range files[:removeCount] {
		if crc, ok := legacySuccessors[files[i+1]]; ok {
			if err := w.rememberLegacyAnchor(files[i+1], crc); err != nil {
				return err
			}
		}
		if err := os.Remove(path); err != nil {
			return err
		}
		if err := syncDir(w.cfg.Dir); err != nil {
			return err
		}
		if w.afterRelease != nil {
			if err := w.afterRelease(path); err != nil {
				return err
			}
		}
	}
	return nil
}

func (w *wal) close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Sync()
	if closeErr := w.file.Close(); err == nil {
		err = closeErr
	}
	w.file = nil
	return err
}

func (w *wal) createSegment(seq, first uint64) (*wal, error) {
	w.seq = seq
	w.first = first
	path := filepath.Join(w.cfg.Dir, segmentName(seq, first))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	w.file = f
	if err := w.writeLocked(walRecord{Type: recordSegmentHeaderV2, Payload: segmentHeaderV2(w.cfg.NodeID)}); err != nil {
		_ = f.Close()
		return nil, err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return nil, err
	}
	if err := syncDir(w.cfg.Dir); err != nil {
		_ = f.Close()
		return nil, err
	}
	return w, nil
}

func (w *wal) openTail(path string) (*wal, error) {
	seq, first, err := parseSegmentName(filepath.Base(path))
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	w.seq = seq
	w.first = first
	w.file = f
	return w, nil
}

func (w *wal) loadTailState(files []string) error {
	state, err := w.replay()
	if err != nil {
		return err
	}
	w.hardState = state.HardState
	for _, entry := range state.Entries {
		if entry.Index > w.lastIndex {
			w.lastIndex = entry.Index
		}
	}
	if state.Snapshot.Index > w.lastIndex {
		w.lastIndex = state.Snapshot.Index
	}
	var crc uint32
	for fileIdx, path := range files {
		flags := os.O_RDONLY
		if fileIdx == len(files)-1 {
			flags = os.O_RDWR
		}
		f, err := os.OpenFile(path, flags, 0)
		if err != nil {
			return err
		}
		lastCompleteOffset := int64(0)
		for {
			_, next, err := w.readWALRecord(f, crc, fileIdx, lastCompleteOffset > 0)
			if err != nil {
				if errors.Is(err, io.EOF) {
					if lastCompleteOffset == 0 {
						_ = f.Close()
						return fmt.Errorf("%w: empty WAL segment %s", ErrTruncatedRecord, filepath.Base(path))
					}
					break
				}
				if errors.Is(err, ErrTruncatedRecord) && fileIdx == len(files)-1 && lastCompleteOffset > 0 {
					if err := f.Truncate(lastCompleteOffset); err != nil {
						_ = f.Close()
						return err
					}
					if err := f.Sync(); err != nil {
						_ = f.Close()
						return err
					}
					break
				}
				_ = f.Close()
				return err
			}
			crc = next
			lastCompleteOffset, err = f.Seek(0, io.SeekCurrent)
			if err != nil {
				_ = f.Close()
				return err
			}
		}
		if err := f.Close(); err != nil {
			return err
		}
	}
	w.crc = crc
	return nil
}

func (w *wal) writeLocked(rec walRecord) error {
	if err := writeRecord(w.file, rec, w.crc); err != nil {
		return err
	}
	w.crc = recordCRC(w.crc, rec.Type, rec.Payload)
	return nil
}

func (w *wal) cutIfNeededLocked() error {
	pos, err := w.file.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	if uint64(pos) < w.cfg.SegmentSize {
		return nil
	}
	if err := w.file.Sync(); err != nil {
		return err
	}
	if err := w.file.Close(); err != nil {
		return err
	}
	w.seq++
	first := w.lastIndex + 1
	if first == 0 {
		first = 1
	}
	path := filepath.Join(w.cfg.Dir, segmentName(w.seq, first))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	w.file = f
	w.first = first
	if err := w.writeLocked(walRecord{Type: recordSegmentHeaderV2, Payload: segmentHeaderV2(w.cfg.NodeID)}); err != nil {
		return err
	}
	if err := w.file.Sync(); err != nil {
		return err
	}
	return syncDir(w.cfg.Dir)
}

func applyRecord(state *replayState, rec walRecord) error {
	switch rec.Type {
	case recordSegmentHeader, recordSegmentHeaderV2:
		return nil
	case recordEntries:
		entries, err := unmarshalEntryRecord(rec.Payload)
		if err != nil {
			return err
		}
		if len(entries) > 0 {
			first := entries[0].Index
			kept := state.Entries[:0]
			for _, entry := range state.Entries {
				if entry.Index < first {
					kept = append(kept, entry)
				}
			}
			state.Entries = append(kept, entries...)
			for _, entry := range entries {
				if err := applyConfEntry(&state.ConfState, entry); err != nil {
					return err
				}
			}
		}
	case recordHardState:
		hs, err := unmarshalHardStateRecord(rec.Payload)
		if err != nil {
			return err
		}
		state.HardState = hs
	case recordSnapshot:
		meta, err := unmarshalSnapshotRecord(rec.Payload)
		if err != nil {
			return err
		}
		state.Snapshot = meta
		state.ConfState = cloneConfState(meta.ConfState)
		state.Entries = trimEntriesAfter(state.Entries, meta.Index)
	case recordAppliedIndex:
		index, err := unmarshalUint64(rec.Payload)
		if err != nil {
			return err
		}
		state.AppliedIndex = index
	default:
		return fmt.Errorf("controller/raftstore: unknown wal record type %d", rec.Type)
	}
	return nil
}

func applyConfEntry(conf *raftpb.ConfState, entry raftpb.Entry) error {
	switch entry.Type {
	case raftpb.EntryConfChange:
		var cc raftpb.ConfChange
		if err := cc.Unmarshal(entry.Data); err != nil {
			return err
		}
		applyConfChange(conf, cc.Type, cc.NodeID)
	case raftpb.EntryConfChangeV2:
		var cc raftpb.ConfChangeV2
		if err := cc.Unmarshal(entry.Data); err != nil {
			return err
		}
		for _, change := range cc.Changes {
			applyConfChange(conf, change.Type, change.NodeID)
		}
	}
	return nil
}

func applyConfChange(conf *raftpb.ConfState, typ raftpb.ConfChangeType, nodeID uint64) {
	switch typ {
	case raftpb.ConfChangeAddNode:
		addUnique(&conf.Voters, nodeID)
		removeValue(&conf.Learners, nodeID)
	case raftpb.ConfChangeAddLearnerNode:
		if !contains(conf.Voters, nodeID) {
			addUnique(&conf.Learners, nodeID)
		}
	case raftpb.ConfChangeRemoveNode:
		removeValue(&conf.Voters, nodeID)
		removeValue(&conf.Learners, nodeID)
		removeValue(&conf.VotersOutgoing, nodeID)
		removeValue(&conf.LearnersNext, nodeID)
	}
}

func addUnique(values *[]uint64, v uint64) {
	if contains(*values, v) {
		return
	}
	*values = append(*values, v)
}

func removeValue(values *[]uint64, v uint64) {
	out := (*values)[:0]
	for _, value := range *values {
		if value != v {
			out = append(out, value)
		}
	}
	*values = out
}

func contains(values []uint64, v uint64) bool {
	for _, value := range values {
		if value == v {
			return true
		}
	}
	return false
}

func walSegmentFiles(dir string) ([]string, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.wal"))
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return filepath.Base(files[i]) < filepath.Base(files[j]) })
	return files, nil
}

func segmentName(seq, first uint64) string {
	return fmt.Sprintf("%016x-%016x.wal", seq, first)
}

func parseSegmentName(name string) (uint64, uint64, error) {
	name = strings.TrimSuffix(name, ".wal")
	parts := strings.Split(name, "-")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("controller/raftstore: invalid wal segment %q", name)
	}
	seq, err := strconv.ParseUint(parts[0], 16, 64)
	if err != nil {
		return 0, 0, err
	}
	first, err := strconv.ParseUint(parts[1], 16, 64)
	if err != nil {
		return 0, 0, err
	}
	return seq, first, nil
}

func trimEntriesAfter(entries []raftpb.Entry, index uint64) []raftpb.Entry {
	if len(entries) == 0 {
		return nil
	}
	out := entries[:0]
	for _, entry := range entries {
		if entry.Index > index {
			out = append(out, entry)
		}
	}
	return out
}
