package raftlog

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/cockroachdb/pebble/v2"
	raft "go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
)

type pebbleBenchConfig struct {
	mode    string
	groups  int
	entries int
	payload int
}

type pebbleStressConfig struct {
	enabled  bool
	duration time.Duration
	groups   int
	writers  int
	payload  int
}

type stressGroupModel struct {
	mu            sync.Mutex
	groupID       uint64
	firstIndex    uint64
	lastIndex     uint64
	appliedIndex  uint64
	snapshotIndex uint64
	snapshotTerm  uint64
	entryTerm     uint64
	confState     raftpb.ConfState
}

type stressGroupExpectation struct {
	groupID       uint64
	firstIndex    uint64
	lastIndex     uint64
	appliedIndex  uint64
	snapshotIndex uint64
	snapshotTerm  uint64
	entryTerm     uint64
	confState     raftpb.ConfState
}

type legacyPebbleState struct {
	hardState raftpb.HardState
	applied   uint64
	snapshot  raftpb.Snapshot
	entries   []raftpb.Entry
}

func loadPebbleBenchConfig(tb testing.TB) pebbleBenchConfig {
	tb.Helper()

	mode := os.Getenv("WRAFT_RAFTSTORE_BENCH_SCALE")
	if mode == "" {
		mode = "default"
	}

	switch mode {
	case "default":
		return pebbleBenchConfig{
			mode:    mode,
			groups:  8,
			entries: 128,
			payload: 256,
		}
	case "heavy":
		return pebbleBenchConfig{
			mode:    mode,
			groups:  64,
			entries: 2048,
			payload: 1024,
		}
	default:
		tb.Fatalf("unsupported WRAFT_RAFTSTORE_BENCH_SCALE %q", mode)
		return pebbleBenchConfig{}
	}
}

func loadPebbleStressConfig() (pebbleStressConfig, error) {
	cfg := pebbleStressConfig{
		enabled:  os.Getenv("WRAFT_RAFTSTORE_STRESS") == "1",
		duration: 5 * time.Minute,
		groups:   64,
		writers:  8,
		payload:  256,
	}
	if !cfg.enabled {
		return cfg, nil
	}

	if value := os.Getenv("WRAFT_RAFTSTORE_STRESS_DURATION"); value != "" {
		duration, err := time.ParseDuration(value)
		if err != nil {
			return pebbleStressConfig{}, fmt.Errorf("parse WRAFT_RAFTSTORE_STRESS_DURATION: %w", err)
		}
		cfg.duration = duration
	}

	var err error
	if cfg.groups, err = loadPositiveIntEnv("WRAFT_RAFTSTORE_STRESS_GROUPS", cfg.groups); err != nil {
		return pebbleStressConfig{}, err
	}
	if cfg.writers, err = loadPositiveIntEnv("WRAFT_RAFTSTORE_STRESS_WRITERS", cfg.writers); err != nil {
		return pebbleStressConfig{}, err
	}
	if cfg.payload, err = loadPositiveIntEnv("WRAFT_RAFTSTORE_STRESS_PAYLOAD", cfg.payload); err != nil {
		return pebbleStressConfig{}, err
	}

	return cfg, nil
}

func loadPositiveIntEnv(name string, fallback int) (int, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}

	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", name, err)
	}
	if parsed <= 0 {
		return 0, fmt.Errorf("%s must be > 0", name)
	}
	return parsed, nil
}

func openBenchDB(tb testing.TB) (*DB, string) {
	tb.Helper()

	path := filepath.Join(tb.TempDir(), "raft")
	db, err := Open(path)
	if err != nil {
		tb.Fatalf("Open(%q) error = %v", path, err)
	}
	return db, path
}

func mustOpenPebbleDB(tb testing.TB, path string) *DB {
	tb.Helper()

	db, err := Open(path)
	if err != nil {
		tb.Fatalf("Open(%q) error = %v", path, err)
	}
	return db
}

func reopenPebbleDB(tb testing.TB, db *DB, path string) *DB {
	tb.Helper()

	if db != nil {
		if err := db.Close(); err != nil {
			tb.Fatalf("Close(%q) error = %v", path, err)
		}
	}
	return mustOpenPebbleDB(tb, path)
}

func closeBenchDB(tb testing.TB, db *DB, path string) {
	tb.Helper()
	if db == nil {
		return
	}
	if err := db.Close(); err != nil {
		tb.Fatalf("Close(%q) error = %v", path, err)
	}
}

func benchEntry(index, term uint64, payloadSize int) raftpb.Entry {
	entry := raftpb.Entry{
		Index: index,
		Term:  term,
	}
	if payloadSize > 0 {
		entry.Data = make([]byte, payloadSize)
		fill := byte(index)
		for i := range entry.Data {
			entry.Data[i] = fill
		}
	}
	return entry
}

func benchEntries(startIndex uint64, count int, term uint64, payloadSize int) []raftpb.Entry {
	entries := make([]raftpb.Entry, 0, count)
	for i := 0; i < count; i++ {
		entries = append(entries, benchEntry(startIndex+uint64(i), term, payloadSize))
	}
	return entries
}

func benchPersistentState(startIndex uint64, count int, term uint64, payloadSize int) multiraft.PersistentState {
	entries := benchEntries(startIndex, count, term, payloadSize)
	if len(entries) == 0 {
		return multiraft.PersistentState{}
	}

	hs := raftpb.HardState{
		Term:   term,
		Commit: entries[len(entries)-1].Index,
	}
	return multiraft.PersistentState{
		HardState: &hs,
		Entries:   entries,
	}
}

func mustSave(tb testing.TB, store multiraft.Storage, st multiraft.PersistentState) {
	tb.Helper()
	if err := store.Save(context.Background(), st); err != nil {
		tb.Fatalf("Save() error = %v", err)
	}
}

func mustMarkApplied(tb testing.TB, store multiraft.Storage, index uint64) {
	tb.Helper()
	if err := store.MarkApplied(context.Background(), index); err != nil {
		tb.Fatalf("MarkApplied(%d) error = %v", index, err)
	}
}

func mustEntries(tb testing.TB, store multiraft.Storage, lo, hi, maxSize uint64) []raftpb.Entry {
	tb.Helper()
	entries, err := store.Entries(context.Background(), lo, hi, maxSize)
	if err != nil {
		tb.Fatalf("Entries(%d,%d,%d) error = %v", lo, hi, maxSize, err)
	}
	return entries
}

func mustLastIndex(tb testing.TB, store multiraft.Storage) uint64 {
	tb.Helper()
	index, err := store.LastIndex(context.Background())
	if err != nil {
		tb.Fatalf("LastIndex() error = %v", err)
	}
	return index
}

func mustFirstIndex(tb testing.TB, store multiraft.Storage) uint64 {
	tb.Helper()
	index, err := store.FirstIndex(context.Background())
	if err != nil {
		tb.Fatalf("FirstIndex() error = %v", err)
	}
	return index
}

func mustTerm(tb testing.TB, store multiraft.Storage, index uint64) uint64 {
	tb.Helper()
	term, err := store.Term(context.Background(), index)
	if err != nil {
		tb.Fatalf("Term(%d) error = %v", index, err)
	}
	return term
}

func mustInitialState(tb testing.TB, store multiraft.Storage) multiraft.BootstrapState {
	tb.Helper()
	state, err := store.InitialState(context.Background())
	if err != nil {
		tb.Fatalf("InitialState() error = %v", err)
	}
	return state
}

func mustWriteLegacyPebbleState(tb testing.TB, path string, scope Scope, state legacyPebbleState) {
	tb.Helper()

	db, err := pebble.Open(path, &pebble.Options{})
	if err != nil {
		tb.Fatalf("pebble.Open(%q) error = %v", path, err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			tb.Fatalf("Close(%q) error = %v", path, err)
		}
	}()

	batch := db.NewBatch()
	defer batch.Close()

	if !raft.IsEmptyHardState(state.hardState) {
		data, err := state.hardState.Marshal()
		if err != nil {
			tb.Fatalf("HardState.Marshal() error = %v", err)
		}
		if err := batch.Set(encodeHardStateKey(scope), data, nil); err != nil {
			tb.Fatalf("batch.Set(hardState) error = %v", err)
		}
	}
	if state.applied > 0 {
		value := make([]byte, 8)
		binary.BigEndian.PutUint64(value, state.applied)
		if err := batch.Set(encodeAppliedIndexKey(scope), value, nil); err != nil {
			tb.Fatalf("batch.Set(appliedIndex) error = %v", err)
		}
	}
	if !raft.IsEmptySnap(state.snapshot) {
		data, err := state.snapshot.Marshal()
		if err != nil {
			tb.Fatalf("Snapshot.Marshal() error = %v", err)
		}
		if err := batch.Set(encodeSnapshotKey(scope), data, nil); err != nil {
			tb.Fatalf("batch.Set(snapshot) error = %v", err)
		}
	}
	for _, entry := range state.entries {
		data, err := entry.Marshal()
		if err != nil {
			tb.Fatalf("Entry.Marshal() error = %v", err)
		}
		if err := batch.Set(encodeEntryKey(scope, entry.Index), data, nil); err != nil {
			tb.Fatalf("batch.Set(entry %d) error = %v", entry.Index, err)
		}
	}

	if err := batch.Commit(pebble.Sync); err != nil {
		tb.Fatalf("batch.Commit() error = %v", err)
	}
}

func hasScopeMetadata(tb testing.TB, db *DB, scope Scope) bool {
	tb.Helper()

	_, closer, err := db.db.Get(encodeMetaKey(scope, recordTypeMeta))
	if err != nil {
		if errors.Is(err, pebble.ErrNotFound) {
			return false
		}
		tb.Fatalf("Get(group metadata) error = %v", err)
	}
	defer closer.Close()
	return true
}

func openStressDB(tb testing.TB) (*DB, string) {
	tb.Helper()
	return openBenchDB(tb)
}

func newStressGroupModels(groupCount int, entryTerm uint64) []stressGroupModel {
	models := make([]stressGroupModel, groupCount)
	for i := range models {
		models[i] = stressGroupModel{
			groupID:    uint64(i + 1),
			firstIndex: 1,
			entryTerm:  entryTerm,
		}
	}
	return models
}

func (m *stressGroupModel) snapshot() stressGroupExpectation {
	m.mu.Lock()
	defer m.mu.Unlock()

	return stressGroupExpectation{
		groupID:       m.groupID,
		firstIndex:    m.firstIndex,
		lastIndex:     m.lastIndex,
		appliedIndex:  m.appliedIndex,
		snapshotIndex: m.snapshotIndex,
		snapshotTerm:  m.snapshotTerm,
		entryTerm:     m.entryTerm,
		confState:     cloneConfState(m.confState),
	}
}

func verifyPebbleGroupState(tb testing.TB, store multiraft.Storage, model stressGroupExpectation, payloadSize int) {
	tb.Helper()

	state := mustInitialState(tb, store)
	if state.AppliedIndex != model.appliedIndex {
		tb.Fatalf("group %d AppliedIndex = %d, want %d", model.groupID, state.AppliedIndex, model.appliedIndex)
	}
	if model.snapshotIndex > 0 && !reflect.DeepEqual(state.ConfState, model.confState) {
		tb.Fatalf("group %d ConfState = %#v, want %#v", model.groupID, state.ConfState, model.confState)
	}

	wantFirst := model.firstIndex
	if model.snapshotIndex > 0 {
		wantFirst = model.snapshotIndex + 1
	}
	if model.lastIndex == 0 && model.snapshotIndex == 0 {
		wantFirst = 1
	}
	if got := mustFirstIndex(tb, store); got != wantFirst {
		tb.Fatalf("group %d FirstIndex() = %d, want %d", model.groupID, got, wantFirst)
	}
	if got := mustLastIndex(tb, store); got != model.lastIndex {
		tb.Fatalf("group %d LastIndex() = %d, want %d", model.groupID, got, model.lastIndex)
	}
	if model.snapshotIndex > 0 {
		if got := mustTerm(tb, store, model.snapshotIndex); got != model.snapshotTerm {
			tb.Fatalf("group %d Term(snapshot=%d) = %d, want %d", model.groupID, model.snapshotIndex, got, model.snapshotTerm)
		}
	}
	if model.lastIndex == 0 {
		return
	}

	lo := model.lastIndex
	if lo > 15 {
		lo -= 15
	}
	if lo < wantFirst {
		lo = wantFirst
	}
	entries := mustEntries(tb, store, lo, model.lastIndex+1, 0)
	if len(entries) != int(model.lastIndex-lo+1) {
		tb.Fatalf("group %d len(Entries()) = %d, want %d", model.groupID, len(entries), model.lastIndex-lo+1)
	}
	for i, entry := range entries {
		wantIndex := lo + uint64(i)
		if entry.Index != wantIndex {
			tb.Fatalf("group %d entries[%d].Index = %d, want %d", model.groupID, i, entry.Index, wantIndex)
		}
		if entry.Term != model.entryTerm {
			tb.Fatalf("group %d entries[%d].Term = %d, want %d", model.groupID, i, entry.Term, model.entryTerm)
		}
		wantData := bytes.Repeat([]byte{byte(wantIndex)}, payloadSize)
		if !bytes.Equal(entry.Data, wantData) {
			tb.Fatalf("group %d entries[%d].Data mismatch", model.groupID, i)
		}
	}
}

func TestPebbleBenchScaleConfigDefaultsAndOverrides(t *testing.T) {
	t.Setenv("WRAFT_RAFTSTORE_BENCH_SCALE", "")
	cfg := loadPebbleBenchConfig(t)
	if cfg.mode != "default" {
		t.Fatalf("mode = %q, want %q", cfg.mode, "default")
	}

	t.Setenv("WRAFT_RAFTSTORE_BENCH_SCALE", "heavy")
	cfg = loadPebbleBenchConfig(t)
	if cfg.mode != "heavy" {
		t.Fatalf("mode = %q, want %q", cfg.mode, "heavy")
	}
}

func TestPebbleLoadStressConfigRejectsInvalidValues(t *testing.T) {
	t.Setenv("WRAFT_RAFTSTORE_STRESS", "1")
	t.Setenv("WRAFT_RAFTSTORE_STRESS_DURATION", "not-a-duration")

	if _, err := loadPebbleStressConfig(); err == nil {
		t.Fatal("loadPebbleStressConfig() error = nil, want invalid duration error")
	}
}
