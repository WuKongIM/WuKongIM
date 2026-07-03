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
}

func openWAL(cfg walConfig) (*wal, error) {
	if cfg.SegmentSize == 0 {
		cfg.SegmentSize = defaultWALSegmentSize
	}
	if err := os.MkdirAll(cfg.Dir, 0o755); err != nil {
		return nil, err
	}
	w := &wal{cfg: cfg}
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

func (w *wal) replay() (replayState, error) {
	files, err := walSegmentFiles(w.cfg.Dir)
	if err != nil {
		return replayState{}, err
	}
	state := replayState{}
	var crc uint32
	for fileIdx, path := range files {
		f, err := os.Open(path)
		if err != nil {
			return replayState{}, err
		}
		for {
			rec, nextCRC, err := readRecord(f, crc)
			if err != nil {
				if errors.Is(err, io.EOF) {
					break
				}
				if errors.Is(err, ErrTruncatedRecord) && fileIdx == len(files)-1 {
					break
				}
				_ = f.Close()
				return replayState{}, err
			}
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

func (w *wal) appendReady(ctx context.Context, hardState raftpb.HardState, entries []raftpb.Entry, snapshot raftpb.SnapshotMetadata) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return fmt.Errorf("controllerv2/raftstore: wal is closed")
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
		return fmt.Errorf("controllerv2/raftstore: wal is closed")
	}
	if err := w.writeLocked(walRecord{Type: recordAppliedIndex, Payload: marshalUint64(index)}); err != nil {
		return err
	}
	if err := w.file.Sync(); err != nil {
		return err
	}
	return w.cutIfNeededLocked()
}

func (w *wal) releaseBefore(index uint64) error {
	files, err := walSegmentFiles(w.cfg.Dir)
	if err != nil {
		return err
	}
	if len(files) <= 1 {
		return nil
	}
	for _, path := range files[:len(files)-1] {
		_, first, err := parseSegmentName(filepath.Base(path))
		if err != nil {
			return err
		}
		if first >= index {
			continue
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
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
	if err := w.writeLocked(walRecord{Type: recordSegmentHeader, Payload: marshalUint64(w.cfg.NodeID)}); err != nil {
		_ = f.Close()
		return nil, err
	}
	if err := f.Sync(); err != nil {
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
	for _, path := range files {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		for {
			_, next, err := readRecord(f, crc)
			if err != nil {
				if errors.Is(err, io.EOF) || errors.Is(err, ErrTruncatedRecord) {
					break
				}
				_ = f.Close()
				return err
			}
			crc = next
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
	if err := w.writeLocked(walRecord{Type: recordSegmentHeader, Payload: marshalUint64(w.cfg.NodeID)}); err != nil {
		return err
	}
	return w.file.Sync()
}

func applyRecord(state *replayState, rec walRecord) error {
	switch rec.Type {
	case recordSegmentHeader:
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
		return fmt.Errorf("controllerv2/raftstore: unknown wal record type %d", rec.Type)
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
		return 0, 0, fmt.Errorf("controllerv2/raftstore: invalid wal segment %q", name)
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
