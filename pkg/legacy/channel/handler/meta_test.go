package handler

import (
	"errors"
	"testing"
	"time"

	core "github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
)

func TestApplyMetaRejectsConflictingReplay(t *testing.T) {
	id := core.ChannelID{ID: "c1", Type: 1}
	svc, _, _ := newAppendService(t, id)

	err := svc.ApplyMeta(core.Meta{
		ID:          id,
		Epoch:       7,
		LeaderEpoch: 9,
		Leader:      2,
		Replicas:    []core.NodeID{1},
		ISR:         []core.NodeID{1},
		MinISR:      1,
		Status:      core.StatusActive,
		Features:    core.Features{MessageSeqFormat: core.MessageSeqFormatU64},
	})
	if !errors.Is(err, core.ErrConflictingMeta) {
		t.Fatalf("expected ErrConflictingMeta, got %v", err)
	}
}

func TestApplyMetaUpdatesRetentionBoundaryInCache(t *testing.T) {
	id := core.ChannelID{ID: "c1", Type: 1}
	svc, _, _ := newAppendService(t, id)
	key := KeyFromChannelID(id)

	next := core.Meta{
		Key:                 key,
		ID:                  id,
		Epoch:               7,
		LeaderEpoch:         9,
		Leader:              1,
		Replicas:            []core.NodeID{1},
		ISR:                 []core.NodeID{1},
		MinISR:              1,
		Status:              core.StatusActive,
		Features:            core.Features{MessageSeqFormat: core.MessageSeqFormatU64},
		RetentionThroughSeq: 88,
	}
	if err := svc.ApplyMeta(next); err != nil {
		t.Fatalf("ApplyMeta() error = %v", err)
	}

	snapshot, ok := svc.MetaSnapshot(key)
	if !ok {
		t.Fatalf("MetaSnapshot() missing key %q", key)
	}
	if snapshot.RetentionThroughSeq != 88 {
		t.Fatalf("RetentionThroughSeq = %d, want 88", snapshot.RetentionThroughSeq)
	}
}

func TestApplyMetaUpdatesWriteFenceInCache(t *testing.T) {
	id := core.ChannelID{ID: "c1", Type: 1}
	svc, _, _ := newAppendService(t, id)
	key := KeyFromChannelID(id)
	until := time.UnixMilli(1_700_000_000_000).UTC()

	next := core.Meta{
		Key:         key,
		ID:          id,
		Epoch:       7,
		LeaderEpoch: 9,
		Leader:      1,
		Replicas:    []core.NodeID{1},
		ISR:         []core.NodeID{1},
		MinISR:      1,
		Status:      core.StatusActive,
		Features:    core.Features{MessageSeqFormat: core.MessageSeqFormatU64},
		WriteFence:  core.WriteFence{Token: "task-1", Version: 2, Reason: core.WriteFenceReasonMigration, Until: until},
	}
	if err := svc.ApplyMeta(next); err != nil {
		t.Fatalf("ApplyMeta() error = %v", err)
	}

	snapshot, ok := svc.MetaSnapshot(key)
	if !ok {
		t.Fatalf("MetaSnapshot() missing key %q", key)
	}
	if snapshot.WriteFence != next.WriteFence {
		t.Fatalf("WriteFence = %+v, want %+v", snapshot.WriteFence, next.WriteFence)
	}
}

func TestApplyMetaRejectsStaleWriteFenceVersion(t *testing.T) {
	id := core.ChannelID{ID: "c1", Type: 1}
	svc, _, _ := newAppendService(t, id)
	key := KeyFromChannelID(id)
	until := time.UnixMilli(1_700_000_000_000).UTC()

	newer := core.Meta{
		Key:         key,
		ID:          id,
		Epoch:       7,
		LeaderEpoch: 9,
		Leader:      1,
		Replicas:    []core.NodeID{1},
		ISR:         []core.NodeID{1},
		MinISR:      1,
		Status:      core.StatusActive,
		Features:    core.Features{MessageSeqFormat: core.MessageSeqFormatU64},
		WriteFence:  core.WriteFence{Token: "task-2", Version: 2, Reason: core.WriteFenceReasonMigration, Until: until},
	}
	if err := svc.ApplyMeta(newer); err != nil {
		t.Fatalf("ApplyMeta(newer) error = %v", err)
	}
	stale := newer
	stale.WriteFence = core.WriteFence{Token: "task-1", Version: 1, Reason: core.WriteFenceReasonMigration, Until: until}

	err := svc.ApplyMeta(stale)
	if !errors.Is(err, core.ErrStaleMeta) {
		t.Fatalf("ApplyMeta(stale) error = %v, want ErrStaleMeta", err)
	}
	snapshot, ok := svc.MetaSnapshot(key)
	if !ok {
		t.Fatalf("MetaSnapshot() missing key %q", key)
	}
	if snapshot.WriteFence != newer.WriteFence {
		t.Fatalf("WriteFence = %+v, want newer %+v", snapshot.WriteFence, newer.WriteFence)
	}
}

func TestStatusReturnsErrStaleMetaWhenCacheMisses(t *testing.T) {
	svc, err := New(Config{
		Runtime:    &fakeRuntime{channels: map[core.ChannelKey]*fakeChannelHandle{}},
		Store:      openTestEngine(t),
		MessageIDs: &fakeMessageIDGenerator{},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = svc.Status(core.ChannelID{ID: "missing", Type: 1})
	if !errors.Is(err, core.ErrStaleMeta) {
		t.Fatalf("expected ErrStaleMeta, got %v", err)
	}
}

func TestRestoreMetaRemovesCachedIDWhenMetaIsDeleted(t *testing.T) {
	id := core.ChannelID{ID: "c-restore-delete", Type: 1}
	svc, _, _ := newAppendService(t, id)
	raw := svc.(*service)
	key := KeyFromChannelID(id)

	raw.RestoreMeta(key, core.Meta{}, false)

	raw.mu.RLock()
	_, exists := raw.keys[id]
	raw.mu.RUnlock()
	if exists {
		t.Fatalf("expected RestoreMeta delete to remove cached id %v", id)
	}
}

func TestStatusReturnsRuntimeAndMetaValues(t *testing.T) {
	id := core.ChannelID{ID: "c1", Type: 1}
	svc, rt, _ := newAppendService(t, id)
	rt.channels[KeyFromChannelID(id)].status.HW = 12

	status, err := svc.Status(id)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.Key != KeyFromChannelID(id) {
		t.Fatalf("Key = %q, want %q", status.Key, KeyFromChannelID(id))
	}
	if status.ID != id {
		t.Fatalf("ID = %+v, want %+v", status.ID, id)
	}
	if status.Status != core.StatusActive {
		t.Fatalf("Status = %d, want %d", status.Status, core.StatusActive)
	}
	if status.Leader != 1 || status.LeaderEpoch != 9 {
		t.Fatalf("leader status = %+v", status)
	}
	if status.HW != 12 || status.CommittedSeq != 12 {
		t.Fatalf("commit status = %+v", status)
	}
}

func TestStatusIncludesRetentionFloor(t *testing.T) {
	id := core.ChannelID{ID: "c1", Type: 1}
	svc, rt, _ := newAppendService(t, id)
	handle := rt.channels[KeyFromChannelID(id)]
	handle.status.RetentionThroughSeq = 88
	handle.status.MinAvailableSeq = 89

	status, err := svc.Status(id)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.RetentionThroughSeq != 88 || status.MinAvailableSeq != 89 {
		t.Fatalf("retention status = %+v", status)
	}
}

func TestStatusReturnsNotReadyWhenCommitHWIsProvisional(t *testing.T) {
	id := core.ChannelID{ID: "c1", Type: 1}
	svc, rt, _ := newAppendService(t, id)
	handle := rt.channels[KeyFromChannelID(id)]
	handle.status.HW = 12
	handle.status.CheckpointHW = 3
	handle.status.CommitReady = false

	_, err := svc.Status(id)
	if !errors.Is(err, core.ErrNotReady) {
		t.Fatalf("expected ErrNotReady, got %v", err)
	}
}
