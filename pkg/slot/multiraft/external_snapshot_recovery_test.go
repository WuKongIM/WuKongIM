package multiraft_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/raftlog"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/cockroachdb/pebble/v2"
	"go.etcd.io/raft/v3/raftpb"
)

func TestRuntimeCompactionWithPebbleExternalSnapshotRestoresAfterRestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "raftlog")
	snapshotPath := filepath.Join(dir, "raft-snapshots")
	slotID := multiraft.SlotID(270)
	marker := []byte("external-snapshot-marker-restored-after-restart")
	postSnapshot := []byte("post-snapshot-entry")

	db := openExternalSnapshotDB(t, dbPath, snapshotPath)
	rt := newExternalSnapshotRuntime(t)
	fsm := newExternalSnapshotStateMachine(marker)
	if err := rt.BootstrapSlot(ctx, multiraft.BootstrapSlotRequest{
		Slot: multiraft.SlotOptions{
			ID:           slotID,
			Storage:      db.ForSlot(uint64(slotID)),
			StateMachine: fsm,
		},
		Voters: []multiraft.NodeID{1},
	}); err != nil {
		t.Fatalf("BootstrapSlot() error = %v", err)
	}
	waitForExternalSnapshotCondition(t, func() bool {
		st, err := rt.Status(slotID)
		return err == nil && st.Role == multiraft.RoleLeader
	})

	fut, err := rt.Propose(ctx, slotID, externalProposalPayload(0, []byte("before-snapshot-entry")))
	if err != nil {
		t.Fatalf("Propose(before snapshot) error = %v", err)
	}
	snapshotResult := waitExternalSnapshotFuture(t, fut)
	compacted, err := rt.CompactLog(ctx, slotID)
	if err != nil {
		t.Fatalf("CompactLog() error = %v", err)
	}
	if !compacted.Compacted {
		t.Fatalf("CompactLog().Compacted = false, skipped=%q", compacted.SkippedReason)
	}
	if compacted.AfterSnapshotIndex != snapshotResult.Index {
		t.Fatalf("snapshot index = %d, want %d", compacted.AfterSnapshotIndex, snapshotResult.Index)
	}

	fut, err = rt.Propose(ctx, slotID, externalProposalPayload(0, postSnapshot))
	if err != nil {
		t.Fatalf("Propose(post snapshot) error = %v", err)
	}
	waitExternalSnapshotFuture(t, fut)

	closeExternalSnapshotRuntime(t, rt)
	closeExternalSnapshotDB(t, db, dbPath)

	assertPebbleValuesDoNotContain(t, dbPath, marker)
	assertExternalSnapshotChunksContain(t, snapshotPath, marker)

	db = openExternalSnapshotDB(t, dbPath, snapshotPath)
	defer closeExternalSnapshotDB(t, db, dbPath)
	rt = newExternalSnapshotRuntime(t)
	defer closeExternalSnapshotRuntime(t, rt)
	restartedFSM := newExternalSnapshotStateMachine(marker)
	if err := rt.OpenSlot(ctx, multiraft.SlotOptions{
		ID:           slotID,
		Storage:      db.ForSlot(uint64(slotID)),
		StateMachine: restartedFSM,
	}); err != nil {
		t.Fatalf("OpenSlot(restarted) error = %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if restartedFSM.restoredSnapshotContains(marker) && restartedFSM.appliedCommand(postSnapshot) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	restored, commands := restartedFSM.debugState()
	t.Fatalf("restarted state machine restored=%q commands=%q, want restored marker and replayed %q", restored, commands, postSnapshot)
}

func TestRuntimeConcurrentSnapshotSavePreservesPerScopeWriteOrder(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db := openExternalSnapshotDB(t, filepath.Join(dir, "raftlog"), filepath.Join(dir, "raft-snapshots"))
	defer closeExternalSnapshotDB(t, db, filepath.Join(dir, "raftlog"))

	blocked := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	restoreHook := raftlog.TestingSetSnapshotWriteFileHook(func(path string, data []byte) error {
		once.Do(func() {
			close(blocked)
			<-release
		})
		return os.WriteFile(path, data, 0o600)
	})
	defer restoreHook()

	store := db.ForSlot(271)
	snapshotDone := make(chan error, 1)
	snap := raftpb.Snapshot{
		Data: []byte("chunk-staging-blocked"),
		Metadata: raftpb.SnapshotMetadata{
			Index: 2,
			Term:  1,
			ConfState: raftpb.ConfState{
				Voters: []uint64{1},
			},
		},
	}
	go func() {
		hs := raftpb.HardState{Term: 1, Commit: 2}
		snapshotDone <- store.Save(ctx, multiraft.PersistentState{HardState: &hs, Snapshot: &snap})
	}()

	select {
	case <-blocked:
	case err := <-snapshotDone:
		t.Fatalf("snapshot Save() finished before chunk staging blocked: %v", err)
	case <-time.After(time.Second):
		t.Fatal("snapshot Save() did not reach chunk staging hook")
	}

	laterDone := make(chan error, 1)
	go func() {
		if err := store.Save(ctx, multiraft.PersistentState{
			Entries: []raftpb.Entry{{Index: 3, Term: 1, Type: raftpb.EntryNormal, Data: []byte("later-entry")}},
		}); err != nil {
			laterDone <- err
			return
		}
		laterDone <- store.MarkApplied(ctx, 3)
	}()

	select {
	case err := <-laterDone:
		t.Fatalf("later same-scope write completed before snapshot staging released: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	if err := <-snapshotDone; err != nil {
		t.Fatalf("snapshot Save() error = %v", err)
	}
	if err := <-laterDone; err != nil {
		t.Fatalf("later same-scope write error = %v", err)
	}
	state, err := store.InitialState(ctx)
	if err != nil {
		t.Fatalf("InitialState() error = %v", err)
	}
	if state.AppliedIndex != 3 {
		t.Fatalf("AppliedIndex = %d, want later MarkApplied index 3", state.AppliedIndex)
	}
	last, err := store.LastIndex(ctx)
	if err != nil {
		t.Fatalf("LastIndex() error = %v", err)
	}
	if last != 3 {
		t.Fatalf("LastIndex() = %d, want later entry index 3", last)
	}
}

func openExternalSnapshotDB(t *testing.T, dbPath, snapshotPath string) *raftlog.DB {
	t.Helper()
	db, err := raftlog.Open(dbPath, raftlog.Options{
		SnapshotPath:      snapshotPath,
		SnapshotChunkSize: 8,
	})
	if err != nil {
		t.Fatalf("raftlog.Open(%q) error = %v", dbPath, err)
	}
	return db
}

func closeExternalSnapshotDB(t *testing.T, db *raftlog.DB, path string) {
	t.Helper()
	if db == nil {
		return
	}
	if err := db.Close(); err != nil {
		t.Fatalf("DB.Close(%q) error = %v", path, err)
	}
}

func newExternalSnapshotRuntime(t *testing.T) *multiraft.Runtime {
	t.Helper()
	rt, err := multiraft.New(multiraft.Options{
		NodeID:       1,
		TickInterval: 10 * time.Millisecond,
		Workers:      1,
		Transport:    externalSnapshotTransport{},
		Raft: multiraft.RaftOptions{
			ElectionTick:  10,
			HeartbeatTick: 1,
			LogCompaction: multiraft.LogCompactionConfig{
				Enabled:        true,
				EnabledSet:     true,
				TriggerEntries: 1000,
				CheckInterval:  time.Hour,
			},
		},
	})
	if err != nil {
		t.Fatalf("multiraft.New() error = %v", err)
	}
	return rt
}

func closeExternalSnapshotRuntime(t *testing.T, rt *multiraft.Runtime) {
	t.Helper()
	if rt == nil {
		return
	}
	if err := rt.Close(); err != nil {
		t.Fatalf("Runtime.Close() error = %v", err)
	}
}

func externalProposalPayload(hashSlot uint16, data []byte) []byte {
	const proposalEnvelopeSize = 10
	payload := make([]byte, proposalEnvelopeSize+len(data))
	binary.BigEndian.PutUint16(payload[:2], hashSlot)
	binary.BigEndian.PutUint64(payload[2:proposalEnvelopeSize], 1781754611123)
	copy(payload[proposalEnvelopeSize:], data)
	return payload
}

func waitExternalSnapshotFuture(t *testing.T, fut multiraft.Future) multiraft.Result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := fut.Wait(ctx)
	if err != nil {
		t.Fatalf("future.Wait() error = %v", err)
	}
	return result
}

func waitForExternalSnapshotCondition(t *testing.T, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met before timeout")
}

func assertPebbleValuesDoNotContain(t *testing.T, dbPath string, marker []byte) {
	t.Helper()
	db, err := pebble.Open(dbPath, &pebble.Options{})
	if err != nil {
		t.Fatalf("pebble.Open(%q) error = %v", dbPath, err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Fatalf("pebble.Close(%q) error = %v", dbPath, err)
		}
	}()

	iter, err := db.NewIter(nil)
	if err != nil {
		t.Fatalf("NewIter() error = %v", err)
	}
	defer iter.Close()
	manifestValues := 0
	for valid := iter.First(); valid; valid = iter.Next() {
		value := iter.Value()
		if bytes.Contains(value, marker) {
			t.Fatalf("Pebble value for key %x contains snapshot payload marker", iter.Key())
		}
		if bytes.HasPrefix(value, []byte("WKSM")) {
			manifestValues++
		}
	}
	if err := iter.Error(); err != nil {
		t.Fatalf("iterator error = %v", err)
	}
	if manifestValues != 1 {
		t.Fatalf("manifest values = %d, want exactly one raw snapshot manifest value", manifestValues)
	}
}

func assertExternalSnapshotChunksContain(t *testing.T, snapshotPath string, marker []byte) {
	t.Helper()
	var payload bytes.Buffer
	err := filepath.WalkDir(snapshotPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		payload.Write(data)
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir(%q) error = %v", snapshotPath, err)
	}
	if !bytes.Contains(payload.Bytes(), marker) {
		t.Fatalf("external snapshot chunks under %q do not contain marker %q", snapshotPath, marker)
	}
}

type externalSnapshotTransport struct{}

func (externalSnapshotTransport) Send(context.Context, []multiraft.Envelope) error {
	return nil
}

type externalSnapshotStateMachine struct {
	mu        sync.Mutex
	marker    []byte
	restored  []byte
	commands  [][]byte
	lastIndex uint64
}

func newExternalSnapshotStateMachine(marker []byte) *externalSnapshotStateMachine {
	return &externalSnapshotStateMachine{marker: append([]byte(nil), marker...)}
}

func (s *externalSnapshotStateMachine) Apply(_ context.Context, cmd multiraft.Command) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastIndex = cmd.Index
	s.commands = append(s.commands, append([]byte(nil), cmd.Data...))
	return append([]byte("ok:"), cmd.Data...), nil
}

func (s *externalSnapshotStateMachine) Restore(_ context.Context, snap multiraft.Snapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.restored = append([]byte(nil), snap.Data...)
	return nil
}

func (s *externalSnapshotStateMachine) Snapshot(context.Context) (multiraft.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data := []byte(fmt.Sprintf("idx=%d ", s.lastIndex))
	data = append(data, s.marker...)
	return multiraft.Snapshot{Data: data}, nil
}

func (s *externalSnapshotStateMachine) restoredSnapshotContains(marker []byte) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return bytes.Contains(s.restored, marker)
}

func (s *externalSnapshotStateMachine) appliedCommand(command []byte) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, applied := range s.commands {
		if bytes.Equal(applied, command) {
			return true
		}
	}
	return false
}

func (s *externalSnapshotStateMachine) debugState() ([]byte, [][]byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	commands := make([][]byte, 0, len(s.commands))
	for _, command := range s.commands {
		commands = append(commands, append([]byte(nil), command...))
	}
	return append([]byte(nil), s.restored...), commands
}
